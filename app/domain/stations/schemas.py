"""Modèles Pydantic du domaine stations-service."""

from __future__ import annotations

from openhexa_core.models import BaseDocument, BasePaginatedResponse
from pydantic import BaseModel


class GeoPoint(BaseModel):
    lat: float
    lon: float


class Station(BaseDocument):
    station_id: str
    nom: str | None = None
    adresse: str | None = None
    ville: str | None = None
    code_postal: str | None = None
    location: GeoPoint | None = None
    sp95: float | None = None
    sp98: float | None = None
    e10: float | None = None
    e85: float | None = None
    gazole: float | None = None
    gplc: float | None = None
    mise_a_jour: str | None = None
    autoroute: bool = False


class StationSearchParams(BaseModel):
    ville: str | None = None
    code_postal: str | None = None
    carburant: str | None = None
    lat: float | None = None
    lon: float | None = None
    radius_km: float = 10.0


class StationSearchResponse(BasePaginatedResponse[Station]):
    pass
