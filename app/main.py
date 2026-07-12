"""Point d'entrée de l'API OpenHexa Essence."""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import structlog
from elasticsearch import AsyncElasticsearch
from fastapi import FastAPI
from openhexa_core.elasticsearch.client import close_client, get_client
from openhexa_core.elasticsearch.index import create_index, ensure_alias

from app.api.v1 import stations
from app.config import Settings, get_settings
from app.domain.stations.ingestion import ingest_stations
from app.domain.stations.mappings import STATION_MAPPING

logger = structlog.get_logger(__name__)


async def _polling_loop(client: AsyncElasticsearch, settings: Settings, index_alias: str) -> None:
    """Interroge périodiquement la source des prix carburants et réindexe les stations."""
    while True:
        try:
            await ingest_stations(client, index_alias, settings.prix_carburants_url)
        except Exception:  # noqa: BLE001 - le polling ne doit jamais s'arrêter sur une erreur réseau
            logger.exception("stations_polling_failed")
        await asyncio.sleep(settings.polling_interval_seconds)


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
app.include_router(stations.router, prefix="/api/v1")
