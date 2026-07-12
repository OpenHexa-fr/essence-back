"""Configuration de l'API Essence : paramètres Elasticsearch (via core) + polling.

Source des prix : `instantane_ruptures`, et non `instantane`. Les deux couvrent
les mêmes ~9 800 stations (schéma XML identique, `instantane_ruptures` ajoute
des balises `<rupture>` que le parseur ignore) mais seul `instantane_ruptures`
est explicitement documenté par le jeu de données data.gouv.fr "Prix des
carburants en France - Flux instantané - v2" comme republié toutes les 10
minutes côté gouvernement — c'est cette cadence que `search_refresh_ttl_seconds`
exploite pour le rafraîchissement à la demande (voir `domain/stations/refresh.py`).
"""

from __future__ import annotations

from functools import lru_cache

from openhexa_core.config import ESSettings
from pydantic_settings import SettingsConfigDict


class Settings(ESSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    prix_carburants_url: str = "https://donnees.roulez-eco.fr/opendata/instantane_ruptures"
    # Synchronisation régulière de fond (filet de sécurité) : quotidienne par défaut,
    # car le rafraîchissement à la demande (déclenché par les recherches) couvre
    # déjà la fraîcheur intra-journée, alignée sur les 10 min du flux source.
    polling_interval_seconds: int = 24 * 3600
    search_refresh_ttl_seconds: int = 600


@lru_cache
def get_settings() -> Settings:
    return Settings()
