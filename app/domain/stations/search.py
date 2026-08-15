"""Requêtes de recherche Elasticsearch pour le domaine stations-service."""

from __future__ import annotations

from typing import Any

from elasticsearch import AsyncElasticsearch, NotFoundError
from openhexa_core.elasticsearch.ingestion import make_document_id
from openhexa_core.elasticsearch.search import paginate

from app.domain.stations.schemas import FUEL_FAMILIES, StationSearchParams


def _fuel_fields(carburant: str) -> list[str]:
    """Résout `carburant` en champs ES réels : la famille (`FUEL_FAMILIES`) si
    reconnue, sinon le carburant lui-même tel quel (comportement historique)."""
    return FUEL_FAMILIES.get(carburant, [carburant])


def _build_station_query(params: StationSearchParams) -> dict[str, Any]:
    """Construit la clause `query` : carburant (ou famille) disponible, plafond
    de prix et/ou rayon.

    Une famille (ex. "sans_plomb") matche si AU MOINS UN de ses carburants est
    disponible (`should`/`minimum_should_match`), pareil pour le plafond de
    prix (au moins un des carburants de la famille sous le plafond) — on ne
    filtre pas sur "tous les carburants de la famille", une station qui ne
    vend que du SP95 doit rester éligible au filtre "Sans-plomb".

    Le plafond de prix n'a de sens que relativement à un carburant précis :
    ignoré si `carburant` n'est pas fourni. Pas de filtre par ville ou service
    (données non extraites de la source actuelle, voir `ingestion.py`).
    """
    filters: list[dict[str, Any]] = []

    if params.carburant:
        fields = _fuel_fields(params.carburant)
        if len(fields) == 1:
            filters.append({"exists": {"field": fields[0]}})
            if params.prix_max is not None:
                filters.append({"range": {fields[0]: {"lte": params.prix_max}}})
        else:
            filters.append(
                {
                    "bool": {
                        "should": [{"exists": {"field": f}} for f in fields],
                        "minimum_should_match": 1,
                    }
                }
            )
            if params.prix_max is not None:
                filters.append(
                    {
                        "bool": {
                            "should": [{"range": {f: {"lte": params.prix_max}}} for f in fields],
                            "minimum_should_match": 1,
                        }
                    }
                )

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


def _family_min_price_sort(fields: list[str]) -> dict[str, Any]:
    """Trie par le plus bas prix parmi les carburants d'une famille.

    Pas de support natif ES pour trier sur le min de plusieurs champs : script
    Painless. `fields` vient uniquement de `FUEL_FAMILIES` (jamais d'une
    valeur utilisateur directe), donc pas de risque d'injection dans la source
    du script — voir `_fuel_fields`.
    """
    conditions = "\n".join(
        f"if (doc['{field}'].size() > 0 && doc['{field}'].value < best) "
        f"{{ best = doc['{field}'].value; }}"
        for field in fields
    )
    source = f"double best = Double.MAX_VALUE;\n{conditions}\nreturn best;"
    return {
        "_script": {
            "type": "number",
            "script": {"lang": "painless", "source": source},
            "order": "asc",
        }
    }


def _build_station_sort(params: StationSearchParams) -> list[dict[str, Any]]:
    """Construit la clause `sort` : distance, prix, récence, ou "score".

    - "distance" exige une position (lat/lon).
    - "prix"/"score" exigent un carburant (ou une famille) sélectionné(e) — on
      ne peut pas trier sur un prix sans savoir lequel. "score" est pour
      l'instant un simple alias de "prix" côté tri serveur — le vrai score
      composite (0-100, prix + distance) affiché en badge est calculé côté
      frontend à partir des résultats déjà triés, sans aller-retour serveur
      supplémentaire.
    - "recent" trie par fraîcheur du prix (`mise_a_jour` décroissant), seul
      tri qui ne requiert ni position ni carburant.

    Si les données nécessaires manquent pour le tri demandé, ou si aucun tri
    n'est demandé, on retombe sur l'ordre stable de l'index (`_seq_no`).
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
    if params.tri == "recent":
        return [{"mise_a_jour": "desc"}, {"_seq_no": "asc"}]
    if params.tri in ("prix", "score") and params.carburant:
        fields = _fuel_fields(params.carburant)
        if len(fields) == 1:
            return [{fields[0]: "asc"}, {"_seq_no": "asc"}]
        return [_family_min_price_sort(fields), {"_seq_no": "asc"}]
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
