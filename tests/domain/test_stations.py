"""Tests du domaine stations-service."""

from __future__ import annotations

import io
import zipfile
from unittest.mock import AsyncMock, Mock

from elasticsearch import NotFoundError

from app.domain.stations.ingestion import extract_stations_xml, parse_stations_xml
from app.domain.stations.schemas import StationSearchParams
from app.domain.stations.search import _build_station_query, get_station_by_id, search_stations

_SAMPLE_XML = b"""<?xml version="1.0" encoding="UTF-8"?>
<pdv_liste>
  <pdv id="12345678" latitude="4529500" longitude="500000" cp="75001" pop="A">
    <adresse>1 rue de la Paix</adresse>
    <ville>Paris</ville>
    <prix nom="Gazole" id="1" maj="2024-01-15 10:00:00" valeur="1.850"/>
    <prix nom="SP95" id="2" maj="2024-01-15 10:05:00" valeur="1.950"/>
  </pdv>
</pdv_liste>
"""


def _build_sample_archive() -> bytes:
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w") as zip_file:
        zip_file.writestr("PrixCarburants_instantane.xml", _SAMPLE_XML)
    return buffer.getvalue()


def test_extract_stations_xml_reads_single_file_from_zip() -> None:
    archive = _build_sample_archive()

    raw_xml = extract_stations_xml(archive)

    assert b"<pdv_liste>" in raw_xml


def test_parse_stations_xml_builds_documents_with_scaled_coordinates() -> None:
    stations = parse_stations_xml(_SAMPLE_XML)

    assert len(stations) == 1
    station = stations[0]
    assert station["station_id"] == "12345678"
    assert station["location"] == {"lat": 45.295, "lon": 5.0}
    assert station["gazole"] == 1.850
    assert station["sp95"] == 1.950
    assert station["autoroute"] is True
    assert station["mise_a_jour"] == "2024-01-15 10:05:00"
    assert len(station["_id"]) == 16


def test_build_station_query_returns_match_all_without_filters() -> None:
    assert _build_station_query(StationSearchParams()) == {"match_all": {}}


def test_build_station_query_filters_on_carburant_existence() -> None:
    query = _build_station_query(StationSearchParams(carburant="gplc"))

    assert {"exists": {"field": "gplc"}} in query["bool"]["filter"]


def test_build_station_query_filters_on_geo_distance() -> None:
    query = _build_station_query(StationSearchParams(lat=45.75, lon=4.85, radius_km=5.0))

    assert {
        "geo_distance": {"distance": "5.0km", "location": {"lat": 45.75, "lon": 4.85}}
    } in query["bool"]["filter"]


async def test_search_stations_calls_paginate_with_built_query() -> None:
    client = AsyncMock()
    client.search.return_value = {"hits": {"hits": [], "total": {"value": 0}}}

    result = await search_stations(
        client, "openhexa-stations", StationSearchParams(ville="Paris")
    )

    assert result["total"] == 0
    client.search.assert_called_once()


async def test_get_station_by_id_returns_source_when_found() -> None:
    client = AsyncMock()
    client.get.return_value = {"_source": {"station_id": "12345678"}}

    station = await get_station_by_id(client, "openhexa-stations", "12345678")

    assert station == {"station_id": "12345678"}


async def test_get_station_by_id_returns_none_when_missing() -> None:
    client = AsyncMock()
    client.get.side_effect = NotFoundError("not found", meta=Mock(status=404), body=None)

    station = await get_station_by_id(client, "openhexa-stations", "unknown")

    assert station is None
