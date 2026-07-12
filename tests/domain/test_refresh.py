"""Tests du rafraîchissement à la demande (déclenché par une recherche)."""

from __future__ import annotations

import asyncio
from unittest.mock import AsyncMock, Mock

import pytest
from elasticsearch import AsyncElasticsearch

from app.domain.stations import refresh as refresh_module
from app.domain.stations.refresh import StationsRefresher

_CLIENT = Mock(spec=AsyncElasticsearch)


def test_is_stale_before_any_refresh() -> None:
    refresher = StationsRefresher(ttl_seconds=600)

    assert refresher.is_stale() is True


async def test_trigger_if_stale_runs_ingestion_in_background(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    mock_ingest = AsyncMock(return_value=(10, 0))
    monkeypatch.setattr(refresh_module, "ingest_stations", mock_ingest)

    refresher = StationsRefresher(ttl_seconds=600)
    refresher.trigger_if_stale(client=_CLIENT, index_alias="idx", source_url="url")

    task = refresher._task
    assert task is not None
    await task

    mock_ingest.assert_awaited_once_with(_CLIENT, "idx", "url")
    assert refresher.is_stale() is False


async def test_trigger_if_stale_is_noop_once_fresh(monkeypatch: pytest.MonkeyPatch) -> None:
    mock_ingest = AsyncMock(return_value=(10, 0))
    monkeypatch.setattr(refresh_module, "ingest_stations", mock_ingest)

    refresher = StationsRefresher(ttl_seconds=600)
    refresher.trigger_if_stale(client=_CLIENT, index_alias="idx", source_url="url")
    task = refresher._task
    assert task is not None
    await task

    refresher.trigger_if_stale(client=_CLIENT, index_alias="idx", source_url="url")

    mock_ingest.assert_awaited_once()


async def test_trigger_if_stale_does_not_start_a_second_task_while_in_flight(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    started = asyncio.Event()
    release = asyncio.Event()

    async def slow_ingest(*_args: object) -> tuple[int, int]:
        started.set()
        await release.wait()
        return (10, 0)

    monkeypatch.setattr(refresh_module, "ingest_stations", slow_ingest)

    refresher = StationsRefresher(ttl_seconds=600)
    refresher.trigger_if_stale(client=_CLIENT, index_alias="idx", source_url="url")
    first_task = refresher._task
    assert first_task is not None

    await started.wait()
    refresher.trigger_if_stale(client=_CLIENT, index_alias="idx", source_url="url")

    assert refresher._task is first_task

    release.set()
    await first_task
