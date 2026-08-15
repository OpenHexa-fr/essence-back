"""Mapping Elasticsearch explicite du domaine stations-service."""

from typing import Any

from openhexa_core.elasticsearch.mappings import BOOLEAN, DATE, FLOAT, GEO_POINT, KEYWORD

STATION_MAPPING: dict[str, Any] = {
    "properties": {
        "station_id": KEYWORD,
        "nom": KEYWORD,
        "adresse": KEYWORD,
        "ville": KEYWORD,
        "code_postal": KEYWORD,
        "location": GEO_POINT,
        "sp95": FLOAT,
        "sp98": FLOAT,
        "e10": FLOAT,
        "e85": FLOAT,
        "gazole": FLOAT,
        "gplc": FLOAT,
        "mise_a_jour": DATE,
        "autoroute": BOOLEAN,
    }
}
