"""Configuration de l'API Essence : paramètres Elasticsearch (via core) + polling.

Source des prix : l'API JSON `data.economie.gouv.fr` (dataset "Prix des
carburants en France - Flux instantané - v2"), reprise du client gouvernemental
du projet Pump-price (https://github.com/Draclest/Pump-price) plutôt que le
flux ZIP/XML `donnees.roulez-eco.fr` utilisé auparavant : un enregistrement par
station, prix déjà aplatis par carburant (`gazole_prix`, `sp95_prix`, ...) et
coordonnées en degrés décimaux, sans les écueils du flux XML (coordonnées à
rescaler, dates non-ISO). Le jeu de données est republié toutes les 10 minutes
côté gouvernement — c'est cette cadence que `search_refresh_ttl_seconds`
exploite pour le rafraîchissement à la demande (voir `domain/stations/refresh.py`).
"""

from __future__ import annotations

from functools import lru_cache

from openhexa_core.config import ESSettings
from pydantic_settings import SettingsConfigDict


class Settings(ESSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    data_gouv_live_url: str = (
        "https://data.economie.gouv.fr/api/explore/v2.1/catalog/datasets/"
        "prix-des-carburants-en-france-flux-instantane-v2/exports/json"
    )
    # Synchronisation régulière de fond (filet de sécurité) : quotidienne par défaut,
    # car le rafraîchissement à la demande (déclenché par les recherches) couvre
    # déjà la fraîcheur intra-journée, alignée sur les 10 min du flux source.
    polling_interval_seconds: int = 24 * 3600
    search_refresh_ttl_seconds: int = 600


@lru_cache
def get_settings() -> Settings:
    return Settings()
