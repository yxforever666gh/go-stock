from datetime import UTC, datetime, timedelta

import pytest

from stock_god.prediction.evidence_store import EvidenceStore


def test_failure_and_success_have_durable_frozen_source_records(core_database):
    cutoff = datetime(2026, 9, 25, 1, 35, tzinfo=UTC)
    store = EvidenceStore(core_database, clock=lambda: cutoff + timedelta(seconds=1))
    ident = store.begin("run", cutoff)
    raw = {
        "evidenceSetId": ident,
        "cutoffAt": cutoff.isoformat(),
        "documents": [
            {
                "sourceId": "stock:sh600000",
                "sourceName": "provider",
                "category": "stock",
                "availableAt": cutoff.isoformat(),
                "collectedAt": cutoff.isoformat(),
                "content": '{"price":10}',
            },
            {
                "sourceId": "stock:sh600000",
                "sourceName": "provider",
                "category": "stock",
                "availableAt": (cutoff + timedelta(seconds=1)).isoformat(),
                "collectedAt": cutoff.isoformat(),
                "content": '{"price":11}',
            },
            {
                "sourceId": "failed",
                "sourceName": "news",
                "category": "market",
                "collectedAt": cutoff.isoformat(),
                "content": "",
                "error": "fixture unavailable",
            },
        ],
    }
    result = store.capture("run", raw, error=ValueError("partial source"))
    assert result["evidenceSetId"] == ident
    with core_database.connection() as con:
        batch = con.execute("SELECT * FROM research_evidence_sets").fetchone()
        items = con.execute("SELECT * FROM research_evidence_items ORDER BY id").fetchall()
    assert batch["status"] == "frozen" and batch["frozen_at"]
    assert [row["status"] for row in items] == ["ok", "after_cutoff", "unavailable"]
    assert items[0]["entity_id"] == "sh600000"
    assert items[1]["source_id"] == "stock:sh600000-2-2"
    assert len(batch["content_hash"]) == 64
    store.capture("run", raw)
    changed = {**raw, "documents": []}
    with pytest.raises(ValueError, match="already frozen"):
        store.capture("run", changed)
    with core_database.connection() as con:
        assert con.execute("SELECT COUNT(*) FROM research_evidence_items").fetchone()[0] == 3


def test_unknown_availability_and_utf8_summary_limit(core_database):
    cutoff = datetime(2026, 9, 25, 1, 35, tzinfo=UTC)
    store = EvidenceStore(core_database, clock=lambda: cutoff)
    store.capture(
        "failed-run",
        {
            "cutoffAt": cutoff.isoformat(),
            "documents": [
                {"sourceName": "金融", "category": "market", "content": "行情" * 300},
            ],
        },
        error=ValueError("source failure"),
    )
    with core_database.connection() as con:
        item = con.execute("SELECT * FROM research_evidence_items").fetchone()
    assert item["status"] == "unavailable"
    assert item["summary"].endswith("…")
    assert len(item["summary"].encode()) <= 515
