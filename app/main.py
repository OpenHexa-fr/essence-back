"""Point d'entrée de l'API OpenHexa Essence."""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import structlog
from elasticsearch import AsyncElasticsearch
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from openhexa_core.elasticsearch.client import close_client, get_client
from openhexa_core.elasticsearch.index import create_index, ensure_alias
from openhexa_core.elasticsearch.search import count

from app.api.v1 import stations, status
from app.config import Settings, get_settings
from app.domain.stations.ingestion import ingest_stations
from app.domain.stations.mappings import STATION_MAPPING

logger = structlog.get_logger(__name__)


async def _index_has_data(client: AsyncElasticsearch, index_alias: str) -> bool:
    """True si `index_alias` contient déjà au moins un document.

    Avec `min-replicas: 0` (scale-to-zero), le process redémarre à chaque cold
    start : sans ce check, l'ingestion initiale repartirait de zéro à chaque
    fois alors que les données sont déjà indexées (le rafraîchissement à la
    demande de `domain/stations/refresh.py` couvre déjà la fraîcheur). Si le
    comptage échoue, on retente l'ingestion par prudence plutôt que de la
    sauter à tort.
    """
    try:
        return await count(client, index_alias) > 0
    except Exception:  # noqa: BLE001 - le comptage ne doit jamais bloquer/casser le polling
        return False


async def _polling_loop(client: AsyncElasticsearch, settings: Settings, index_alias: str) -> None:
    """Synchronisation de fond, filet de sécurité en complément du rafraîchissement
    à la demande déclenché par les recherches (voir `domain/stations/refresh.py`).
    """
    if await _index_has_data(client, index_alias):
        logger.info("stations_polling_initial_run_skipped", reason="index_already_populated")
    else:
        try:
            await ingest_stations(client, index_alias, settings.data_gouv_live_url)
        except Exception:  # noqa: BLE001 - le polling ne doit jamais s'arrêter sur une erreur réseau
            logger.exception("stations_polling_failed")

    while True:
        await asyncio.sleep(settings.polling_interval_seconds)
        try:
            await ingest_stations(client, index_alias, settings.data_gouv_live_url)
        except Exception:  # noqa: BLE001 - le polling ne doit jamais s'arrêter sur une erreur réseau
            logger.exception("stations_polling_failed")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    settings = get_settings()
    client = await get_client(settings)

    alias = f"{settings.es_index_prefix}-stations"
    index_name = f"{alias}-000001"
    await create_index(client, index_name, STATION_MAPPING)
    await ensure_alias(client, alias, index_name)

    polling_task = asyncio.create_task(_polling_loop(client, settings, alias))

    logger.info("essence_api_started")
    yield

    polling_task.cancel()
    with contextlib.suppress(asyncio.CancelledError):
        await polling_task
    await close_client()
    logger.info("essence_api_stopped")


app = FastAPI(title="OpenHexa Essence API", lifespan=lifespan)
# API publique en lecture seule (données ouvertes, pas de cookies/session) :
# CORS permissif nécessaire puisque le frontend est servi sur une origine distincte.
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["GET"],
    allow_headers=["*"],
)
app.include_router(stations.router, prefix="/api/v1")
app.include_router(status.router, prefix="/api/v1")
