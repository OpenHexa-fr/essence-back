"""Rafraîchissement à la demande des prix carburants, déclenché par une recherche.

Le gouvernement republie le flux "instantané" toutes les 10 minutes (voir la
description du jeu de données data.gouv.fr "Prix des carburants en France -
Flux instantané - v2" : "Le flux de données instantané est mis à jour toutes
les 10 minutes"). Retélécharger le fichier national à chaque recherche serait
à la fois inutile (la source ne change pas plus vite) et coûteux (~12 Mo,
~10 000 documents à réindexer) : ce module ne déclenche donc un nouveau cycle
d'ingestion que si le dernier remonte à plus de `ttl_seconds`, et seulement si
aucun rafraîchissement n'est déjà en cours.

Le rafraîchissement tourne en tâche de fond (fire-and-forget) pour ne pas
pénaliser la latence de la recherche qui le déclenche : cette recherche voit
les données actuelles, la suivante bénéficiera des données fraîchement
réindexées. Ceci s'ajoute à la synchronisation régulière (boucle de polling,
par défaut quotidienne) démarrée au lifespan de l'application, qui reste le
filet de sécurité si aucune recherche n'a lieu pendant une longue période.
"""

from __future__ import annotations

import asyncio
import time
from functools import lru_cache

import structlog
from elasticsearch import AsyncElasticsearch

from app.domain.stations.ingestion import ingest_stations

logger = structlog.get_logger(__name__)

_DEFAULT_TTL_SECONDS = 600


class StationsRefresher:
    """Garde-fou stateful : au plus un rafraîchissement en vol, au plus un par `ttl_seconds`."""

    def __init__(self, ttl_seconds: int = _DEFAULT_TTL_SECONDS) -> None:
        self._ttl_seconds = ttl_seconds
        self._last_refresh_at: float | None = None
        self._task: asyncio.Task[None] | None = None

    def is_stale(self) -> bool:
        if self._last_refresh_at is None:
            return True
        return (time.monotonic() - self._last_refresh_at) > self._ttl_seconds

    def trigger_if_stale(
        self, client: AsyncElasticsearch, index_alias: str, source_url: str
    ) -> None:
        """Programme un rafraîchissement en tâche de fond si les données sont périmées.

        No-op si un rafraîchissement est déjà en cours : évite qu'une rafale de
        recherches simultanées ne déclenche autant de téléchargements concurrents
        du fichier national.
        """
        if not self.is_stale():
            return
        if self._task is not None and not self._task.done():
            return
        self._task = asyncio.create_task(self._refresh(client, index_alias, source_url))

    async def _refresh(
        self, client: AsyncElasticsearch, index_alias: str, source_url: str
    ) -> None:
        try:
            await ingest_stations(client, index_alias, source_url)
        except Exception:  # noqa: BLE001 - ne doit jamais faire remonter d'erreur à l'appelant
            logger.exception("stations_search_refresh_failed")
        finally:
            self._last_refresh_at = time.monotonic()


@lru_cache
def get_stations_refresher() -> StationsRefresher:
    from app.config import get_settings

    return StationsRefresher(ttl_seconds=get_settings().search_refresh_ttl_seconds)
