"""Routes stations-service et prix carburants."""

from __future__ import annotations

from elasticsearch import AsyncElasticsearch
from fastapi import APIRouter, Depends, HTTPException, Query
from openhexa_core.elasticsearch.client import get_client

from app.config import Settings, get_settings
from app.domain.stations.refresh import StationsRefresher, get_stations_refresher
from app.domain.stations.schemas import (
    Station,
    StationSearchParams,
    StationSearchResponse,
    StationSort,
)
from app.domain.stations.search import get_station_by_id, search_stations

router = APIRouter(prefix="/stations", tags=["stations"])


async def _es_client() -> AsyncElasticsearch:
    return await get_client()


@router.get("/search", response_model=StationSearchResponse)
async def search(
    ville: str | None = None,
    code_postal: str | None = None,
    carburant: str | None = None,
    lat: float | None = None,
    lon: float | None = None,
    radius_km: float = 10.0,
    prix_max: float | None = None,
    service_24_7: bool | None = None,
    paiement_cb: bool | None = None,
    boutique: bool | None = None,
    tri: StationSort | None = None,
    search_after: list[str] | None = Query(None),
    size: int = 20,
    client: AsyncElasticsearch = Depends(_es_client),
    settings: Settings = Depends(get_settings),
    refresher: StationsRefresher = Depends(get_stations_refresher),
) -> StationSearchResponse:
    """Recherche des stations-service par ville, carburant disponible ou proximité géographique.

    Déclenche, si nécessaire, un rafraîchissement à la demande depuis le flux
    "instantané" (republié toutes les 10 min côté gouvernement) : la recherche
    en cours répond avec les données actuelles, mais garantit qu'une donnée
    trop ancienne ne le reste pas si des recherches ont lieu régulièrement.
    """
    params = StationSearchParams(
        ville=ville,
        code_postal=code_postal,
        carburant=carburant,
        lat=lat,
        lon=lon,
        radius_km=radius_km,
        prix_max=prix_max,
        service_24_7=service_24_7,
        paiement_cb=paiement_cb,
        boutique=boutique,
        tri=tri,
    )
    index = f"{settings.es_index_prefix}-stations"
    refresher.trigger_if_stale(client, index, settings.prix_carburants_url)
    page = await search_stations(client, index, params, search_after=search_after, size=size)

    items = [Station.model_validate(hit["_source"]) for hit in page["hits"]]
    return StationSearchResponse(
        items=items, total=page["total"], next_search_after=page["next_search_after"]
    )


@router.get("/{station_id}", response_model=Station)
async def get_by_id(
    station_id: str,
    client: AsyncElasticsearch = Depends(_es_client),
    settings: Settings = Depends(get_settings),
) -> Station:
    """Retourne la fiche d'une station par son identifiant."""
    index = f"{settings.es_index_prefix}-stations"
    source = await get_station_by_id(client, index, station_id)
    if source is None:
        raise HTTPException(status_code=404, detail="Station introuvable")
    return Station.model_validate(source)
