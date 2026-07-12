"""Configuration de l'API Essence : paramètres Elasticsearch (via core) + polling."""

from __future__ import annotations

from functools import lru_cache

from openhexa_core.config import ESSettings
from pydantic_settings import SettingsConfigDict


class Settings(ESSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    prix_carburants_url: str = "https://donnees.roulez-eco.fr/opendata/instantane"
    polling_interval_seconds: int = 3600


@lru_cache
def get_settings() -> Settings:
    return Settings()
