"""Ingestion des prix carburants (source data.economie.gouv.fr) dans Elasticsearch.

Reprend la logique du client gouvernemental du projet Pump-price
(https://github.com/Draclest/Pump-price, `app/services/gov_client.py`) : le
flux "instantané v2" expose un enregistrement JSON par station avec les prix
déjà aplatis par carburant (`gazole_prix`, `sp95_prix`, ...) et les coordonnées
en degrés décimaux, contrairement au flux ZIP/XML `donnees.roulez-eco.fr`
utilisé auparavant, qui exigeait un rescaling manuel des coordonnées et une
conversion de format de date non-ISO.
"""

from __future__ import annotations

from typing import Any

import httpx
import structlog
from elasticsearch import AsyncElasticsearch
from openhexa_core.elasticsearch.ingestion import bulk_index, make_document_id

logger = structlog.get_logger(__name__)

_FUEL_KEYS = ("sp95", "sp98", "e10", "e85", "gazole", "gplc")


async def fetch_stations_records(source_url: str) -> list[dict[str, Any]]:
    """Télécharge l'export JSON du flux instantané depuis `source_url`."""
    async with httpx.AsyncClient(timeout=90.0, follow_redirects=True) as http_client:
        response = await http_client.get(source_url)
        response.raise_for_status()
        raw = response.json()
    return raw if isinstance(raw, list) else raw.get("results", [])


def _parse_station_record(record: dict[str, Any]) -> dict[str, Any] | None:
    station_id = str(record.get("id") or "").strip()
    if not station_id:
        return None

    geom = record.get("geom") or {}
    latitude, longitude = geom.get("lat"), geom.get("lon")
    location = (
        {"lat": float(latitude), "lon": float(longitude)}
        if latitude is not None and longitude is not None
        else None
    )

    prices: dict[str, float] = {}
    mise_a_jour: str | None = None
    for field in _FUEL_KEYS:
        valeur = record.get(f"{field}_prix")
        if valeur is not None:
            prices[field] = float(valeur)
        maj = record.get(f"{field}_maj")
        if maj and (mise_a_jour is None or maj > mise_a_jour):
            mise_a_jour = maj

    return {
        "_id": make_document_id(station_id),
        "station_id": station_id,
        "adresse": record.get("adresse"),
        "ville": record.get("ville"),
        "code_postal": record.get("cp"),
        "location": location,
        # Le flux JSON publie déjà des dates ISO-8601 avec offset (ex:
        # "2026-07-10T05:25:45+00:00"), directement compatibles avec le mapping
        # ES `date` — pas de conversion de format nécessaire (contrairement à
        # l'ancien flux XML roulez-eco.fr).
        "mise_a_jour": mise_a_jour,
        "autoroute": record.get("pop") == "A",
        **prices,
    }


def parse_stations_records(records: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Parse les enregistrements du flux instantané en documents prêts à indexer.

    Une station dont les attributs sont malformés est ignorée plutôt que de faire
    échouer l'ingestion des ~9800 autres stations du flux.
    """
    documents = []
    for record in records:
        try:
            document = _parse_station_record(record)
        except (TypeError, ValueError) as exc:
            logger.warning(
                "station_parse_skipped", station_id=record.get("id"), error=str(exc)
            )
            continue
        if document is not None:
            documents.append(document)
    return documents


async def ingest_stations(
    client: AsyncElasticsearch, index_alias: str, source_url: str
) -> tuple[int, int]:
    """Télécharge, parse et indexe les stations depuis `source_url`."""
    records = await fetch_stations_records(source_url)
    documents = parse_stations_records(records)

    success, errors = await bulk_index(client, index_alias, documents)
    logger.info("stations_ingestion_completed", success=success, errors=errors)
    return success, errors
