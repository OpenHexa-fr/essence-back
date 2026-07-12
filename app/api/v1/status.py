"""Route d'état de disponibilité des données.

Utilisée par le frontend pour masquer un bandeau "synchronisation en cours"
une fois que l'index stations a reçu au moins un document.
"""

from __future__ import annotations

from elasticsearch import AsyncElasticsearch
from fastapi import APIRouter, Depends
from openhexa_core.elasticsearch.client import get_client
from openhexa_core.elasticsearch.search import count
from pydantic import BaseModel

from app.config import Settings, get_settings

router = APIRouter(tags=["status"])


class DomainStatus(BaseModel):
    stations: bool


async def _es_client() -> AsyncElasticsearch:
    return await get_client()


@router.get("/status", response_model=DomainStatus)
async def status(
    client: AsyncElasticsearch = Depends(_es_client),
    settings: Settings = Depends(get_settings),
) -> DomainStatus:
    """Indique si au moins une station a été ingérée."""
    stations_count = await count(client, f"{settings.es_index_prefix}-stations")
    return DomainStatus(stations=stations_count > 0)
