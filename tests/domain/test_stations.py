"""Tests du domaine stations-service."""

from __future__ import annotations

from unittest.mock import AsyncMock, Mock

from elasticsearch import NotFoundError

from app.domain.stations.ingestion import parse_stations_records
from app.domain.stations.schemas import StationSearchParams
from app.domain.stations.search import (
    _build_station_query,
    _build_station_sort,
    get_station_by_id,
    search_stations,
)

_SAMPLE_RECORD = {
    "id": 12345678,
    "adresse": "1 rue de la Paix",
    "ville": "Paris",
    "cp": "75001",
    "pop": "A",
    "geom": {"lat": 48.8697, "lon": 2.3079},
    "gazole_prix": 1.850,
    "gazole_maj": "2026-07-10T05:25:45+00:00",
    "sp95_prix": 1.950,
    "sp95_maj": "2026-07-10T08:00:00+00:00",
}


def test_parse_stations_records_builds_documents_with_decimal_coordinates() -> None:
    stations = parse_stations_records([_SAMPLE_RECORD])

    assert len(stations) == 1
    station = stations[0]
    assert station["station_id"] == "12345678"
    assert station["location"] == {"lat": 48.8697, "lon": 2.3079}
    assert station["gazole"] == 1.850
    assert station["sp95"] == 1.950
    assert station["autoroute"] is True
    assert len(station["_id"]) == 16


def test_parse_stations_records_keeps_iso8601_dates_unchanged() -> None:
    # Le flux JSON data.economie.gouv.fr publie déjà des dates ISO-8601 avec
    # offset : contrairement à l'ancien flux XML roulez-eco.fr, aucune
    # conversion de format n'est nécessaire.
    stations = parse_stations_records([_SAMPLE_RECORD])

    assert stations[0]["mise_a_jour"] == "2026-07-10T08:00:00+00:00"


def test_parse_stations_records_skips_record_without_id() -> None:
    stations = parse_stations_records([{**_SAMPLE_RECORD, "id": None}])

    assert stations == []


def test_parse_stations_records_omits_location_when_geom_missing() -> None:
    record = {**_SAMPLE_RECORD, "geom": None}

    stations = parse_stations_records([record])

    assert len(stations) == 1
    assert stations[0]["location"] is None


def test_parse_stations_records_skips_malformed_station_without_failing_others() -> None:
    bad_record = {**_SAMPLE_RECORD, "id": 1, "gazole_prix": "not-a-number"}
    good_record = {**_SAMPLE_RECORD, "id": 2}

    stations = parse_stations_records([bad_record, good_record])

    assert len(stations) == 1
    assert stations[0]["station_id"] == "2"


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


def test_build_station_query_filters_on_fuel_family_with_should_clause() -> None:
    query = _build_station_query(StationSearchParams(carburant="sans_plomb"))

    should = query["bool"]["filter"][0]["bool"]["should"]
    assert {"exists": {"field": "e10"}} in should
    assert {"exists": {"field": "sp95"}} in should


def test_build_station_query_filters_on_fuel_family_prix_max_with_should_clause() -> None:
    query = _build_station_query(StationSearchParams(carburant="sans_plomb", prix_max=1.8))

    range_clause = query["bool"]["filter"][1]["bool"]["should"]
    assert {"range": {"e10": {"lte": 1.8}}} in range_clause
    assert {"range": {"sp95": {"lte": 1.8}}} in range_clause


def test_build_station_sort_by_fuel_family_uses_script_sort() -> None:
    sort = _build_station_sort(StationSearchParams(carburant="sans_plomb", tri="prix"))

    script_source = sort[0]["_script"]["script"]["source"]
    assert "doc['e10']" in script_source
    assert "doc['sp95']" in script_source


def test_build_station_query_filters_on_prix_max_when_carburant_set() -> None:
    query = _build_station_query(StationSearchParams(carburant="sp95", prix_max=1.8))

    assert {"range": {"sp95": {"lte": 1.8}}} in query["bool"]["filter"]


def test_build_station_query_ignores_prix_max_without_carburant() -> None:
    query = _build_station_query(StationSearchParams(prix_max=1.8))

    assert query == {"match_all": {}}


def test_build_station_sort_defaults_to_stable_order() -> None:
    assert _build_station_sort(StationSearchParams()) == [{"_seq_no": "asc"}]


def test_build_station_sort_by_price_requires_carburant() -> None:
    sort = _build_station_sort(StationSearchParams(carburant="sp95", tri="prix"))

    assert sort[0] == {"sp95": "asc"}


def test_build_station_sort_by_score_behaves_like_price() -> None:
    sort = _build_station_sort(StationSearchParams(carburant="sp95", tri="score"))

    assert sort[0] == {"sp95": "asc"}


def test_build_station_sort_by_price_falls_back_without_carburant() -> None:
    sort = _build_station_sort(StationSearchParams(tri="prix"))

    assert sort[0] == {"_seq_no": "asc"}


def test_build_station_sort_by_recent_does_not_require_carburant_or_location() -> None:
    sort = _build_station_sort(StationSearchParams(tri="recent"))

    assert sort[0] == {"mise_a_jour": "desc"}


def test_build_station_sort_by_distance_requires_location() -> None:
    sort = _build_station_sort(StationSearchParams(lat=45.75, lon=4.85, tri="distance"))

    assert sort[0] == {
        "_geo_distance": {
            "location": {"lat": 45.75, "lon": 4.85},
            "order": "asc",
            "unit": "km",
        }
    }


def test_build_station_sort_by_distance_falls_back_without_location() -> None:
    sort = _build_station_sort(StationSearchParams(tri="distance"))

    assert sort[0] == {"_seq_no": "asc"}


async def test_search_stations_calls_paginate_with_built_query() -> None:
    client = AsyncMock()
    client.search.return_value = {"hits": {"hits": [], "total": {"value": 0}}}

    result = await search_stations(
        client, "openhexa-stations", StationSearchParams(carburant="sp95")
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
