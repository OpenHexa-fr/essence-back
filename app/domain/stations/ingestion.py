"""Ingestion des prix carburants (source roulez-eco.fr) dans Elasticsearch.

La source distribue un ZIP contenant un unique fichier XML (`PrixCarburants_instantane.xml`).
Les coordonnées et les prix y sont exprimés en entiers à l'échelle 1e5 / 1e3
respectivement (convention historique du flux gouvernemental) : il faut diviser
pour obtenir des degrés décimaux et des euros. Cette hypothèse de format est à
valider face à l'export réel avant mise en production.
"""

from __future__ import annotations

import io
import zipfile
from typing import Any
from xml.etree import ElementTree

import httpx
import structlog
from elasticsearch import AsyncElasticsearch
from openhexa_core.elasticsearch.ingestion import bulk_index, make_document_id

logger = structlog.get_logger(__name__)

_COORDINATE_SCALE = 100_000

_FUEL_TAGS = {
    "SP95": "sp95",
    "SP98": "sp98",
    "E10": "e10",
    "E85": "e85",
    "Gazole": "gazole",
    "GPLc": "gplc",
}


async def fetch_stations_archive(source_url: str) -> bytes:
    """Télécharge l'archive ZIP des prix instantanés depuis `source_url`."""
    async with httpx.AsyncClient(timeout=60.0, follow_redirects=True) as http_client:
        response = await http_client.get(source_url)
        response.raise_for_status()
        return response.content


def extract_stations_xml(archive: bytes) -> bytes:
    """Extrait le contenu XML du ZIP téléchargé (un seul fichier attendu)."""
    with zipfile.ZipFile(io.BytesIO(archive)) as zip_file:
        xml_filename = zip_file.namelist()[0]
        return zip_file.read(xml_filename)


def _parse_station_element(pdv: ElementTree.Element) -> dict[str, Any]:
    station_id = pdv.attrib["id"]
    latitude_raw = pdv.attrib.get("latitude")
    longitude_raw = pdv.attrib.get("longitude")
    location = None
    if latitude_raw and longitude_raw:
        # Le flux réel contient occasionnellement des coordonnées non entières
        # (ex : latitude="4675351.71497") malgré la convention à l'échelle 1e5 :
        # `float()` tolère ce cas là où `int()` fait planter toute l'ingestion.
        location = {
            "lat": float(latitude_raw) / _COORDINATE_SCALE,
            "lon": float(longitude_raw) / _COORDINATE_SCALE,
        }

    prices: dict[str, float] = {}
    mise_a_jour: str | None = None
    for prix in pdv.findall("prix"):
        field = _FUEL_TAGS.get(prix.attrib.get("nom") or "")
        valeur = prix.attrib.get("valeur")
        if field and valeur:
            prices[field] = float(valeur)
        maj = prix.attrib.get("maj")
        if maj and (mise_a_jour is None or maj > mise_a_jour):
            mise_a_jour = maj

    adresse_el = pdv.find("adresse")
    ville_el = pdv.find("ville")

    document: dict[str, Any] = {
        "_id": make_document_id(station_id),
        "station_id": station_id,
        "adresse": adresse_el.text if adresse_el is not None else None,
        "ville": ville_el.text if ville_el is not None else None,
        "code_postal": pdv.attrib.get("cp"),
        "location": location,
        # Le flux réel publie "AAAA-MM-JJ HH:mm:ss" (séparateur espace) : le
        # mapping ES `date` attend un format ISO-8601 strict (séparateur "T"),
        # sans quoi l'indexation de la quasi-totalité des stations échoue
        # (vérifié en conditions réelles : 9678 erreurs sur 9805 documents).
        "mise_a_jour": mise_a_jour.replace(" ", "T") if mise_a_jour else None,
        "autoroute": pdv.attrib.get("pop") == "A",
        **prices,
    }
    return document


def parse_stations_xml(raw_xml: bytes) -> list[dict[str, Any]]:
    """Parse le XML des stations en une liste de documents prêts à indexer.

    Une station dont les attributs sont malformés est ignorée plutôt que de faire
    échouer l'ingestion des ~40 000 autres stations du flux (vérifié en conditions
    réelles : une latitude non entière a suffi à faire planter tout le traitement
    avant l'introduction de ce garde-fou).
    """
    root = ElementTree.fromstring(raw_xml)
    documents = []
    for pdv in root.findall("pdv"):
        try:
            documents.append(_parse_station_element(pdv))
        except (KeyError, ValueError) as exc:
            logger.warning(
                "station_parse_skipped", station_id=pdv.attrib.get("id"), error=str(exc)
            )
    return documents


async def ingest_stations(
    client: AsyncElasticsearch, index_alias: str, source_url: str
) -> tuple[int, int]:
    """Télécharge, décompresse, parse et indexe les stations depuis `source_url`."""
    archive = await fetch_stations_archive(source_url)
    raw_xml = extract_stations_xml(archive)
    documents = parse_stations_xml(raw_xml)

    success, errors = await bulk_index(client, index_alias, documents)
    logger.info("stations_ingestion_completed", success=success, errors=errors)
    return success, errors
