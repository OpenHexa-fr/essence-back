"""Requêtes de recherche Elasticsearch pour le domaine stations-service."""

from __future__ import annotations

from typing import Any

from elasticsearch import AsyncElasticsearch, NotFoundError
from openhexa_core.elasticsearch.ingestion import make_document_id
from openhexa_core.elasticsearch.search import paginate

from app.domain.stations.schemas import StationSearchParams


def _build_station_query(params: StationSearchParams) -> dict[str, Any]:
    """Construit la clause `query` : carburant disponible et/ou rayon de recherche.

    Seuls ces deux critères sont exposés en recherche (le type de carburant et
    le rayon autour d'une position) — pas de filtre par ville, service ou
    plafond de prix.
    """
    filters: list[dict[str, Any]] = []

    if params.carburant:
        filters.append({"exists": {"field": params.carburant}})

    if params.lat is not None and params.lon is not None:
        filters.append(
            {
                "geo_distance": {
                    "distance": f"{params.radius_km}km",
                    "location": {"lat": params.lat, "lon": params.lon},
                }
            }
        )

    if not filters:
        return {"match_all": {}}
    return {"bool": {"filter": filters}}


def _build_station_sort(params: StationSearchParams) -> list[dict[str, Any]]:
    """Construit la clause `sort` : distance croissante ou prix croissant.

    Le tri "distance" exige une position (lat/lon) ; le tri "prix" exige un
    carburant sélectionné (on ne peut pas trier sur un prix sans savoir lequel).
    Si les données nécessaires manquent, ou si aucun tri n'est demandé, on
    retombe sur l'ordre stable de l'index (`_seq_no`) : pas de notion de
    récence ou de score de pertinence.
    """
    if params.tri == "distance" and params.lat is not None and params.lon is not None:
        return [
            {
                "_geo_distance": {
                    "location": {"lat": params.lat, "lon": params.lon},
                    "order": "asc",
                    "unit": "km",
                }
            },
            {"_seq_no": "asc"},
        ]
    if params.tri == "prix" and params.carburant:
        return [{params.carburant: "asc"}, {"_seq_no": "asc"}]
    return [{"_seq_no": "asc"}]


async def search_stations(
    client: AsyncElasticsearch,
    index: str,
    params: StationSearchParams,
    search_after: list[Any] | None = None,
    size: int = 20,
) -> dict[str, Any]:
    """Recherche des stations selon `params`, paginée par `search_after`."""
    query = _build_station_query(params)
    sort = _build_station_sort(params)
    return await paginate(
        client, index=index, query=query, sort=sort, search_after=search_after, size=size
    )


async def get_station_by_id(
    client: AsyncElasticsearch, index: str, station_id: str
) -> dict[str, Any] | None:
    """Retourne la station correspondant à `station_id`, ou None si absente."""
    try:
        response = await client.get(index=index, id=make_document_id(station_id))
    except NotFoundError:
        return None
    return dict(response["_source"])
