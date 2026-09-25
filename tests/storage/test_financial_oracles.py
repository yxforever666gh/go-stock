import json
from pathlib import Path

import pytest

from stock_god.storage.db import Database, quote_identifier as qi
from stock_god.storage.historical.research1 import fixed_capital, freeze
from stock_god.storage.historical.capital import rebase_capital
from stock_god.storage.historical.timestamps import time_key
from stock_god.storage.migrations import _schema


FIXTURES = Path(__file__).parents[1] / "fixtures/migrations"


def load_fixture(db, name):
    fixture = json.loads((FIXTURES / (name + "_before.json")).read_text(encoding="utf8"))
    for obj in sorted(fixture["objects"], key=lambda o: {"table": 0, "index": 1, "trigger": 2}[o["type"]]):
        if obj["type"] == "trigger" or obj["name"].startswith("knowledge_document_fts_"):
            continue
        db.execute(obj["sql"])
    for table, items in fixture["tables"].items():
        if table.startswith("knowledge_document_fts_"):
            continue
        for item in items or []:
            db.execute(
                "INSERT INTO "
                + qi(table)
                + " ("
                + ",".join(qi(c) for c in item)
                + ") VALUES ("
                + ",".join("?" for _ in item)
                + ")",
                tuple(item.values()),
            )
    for obj in fixture["objects"]:
        if obj["type"] == "trigger":
            db.execute(obj["sql"])


@pytest.mark.migration
@pytest.mark.parametrize(
    "case,version,apply",
    [("fixed_capital", 12, fixed_capital), ("freeze", 29, freeze), ("capital", 31, rebase_capital)],
)
def test_frozen_financial_migration_matches_go(case, version, apply, tmp_path, monkeypatch):
    golden = json.loads((FIXTURES / (case + "_after.json")).read_text(encoding="utf8"))["tables"]
    if case == "freeze":
        frozen_at = next(
            row["valued_at"]
            for row in golden["research_v170_account_snapshots"]
            if row["snapshot_id"] == "research1-freeze-4.0.1"
        )
        monkeypatch.setattr("stock_god.storage.historical.research1.now", lambda: frozen_at)
    database = Database(tmp_path / (case + ".db"))
    with database.transaction() as db:
        load_fixture(db, case)
    with database.transaction() as db:
        _schema(db, "main", version)
        apply(db)
    keys = {
        "research2_accounts": "slot",
        "research2_recommendations": "recommendation_id",
        "research2_trades": "trade_id",
        "research2_account_capital_events": "event_id",
        "research2_account_ledger_snapshots": "snapshot_id",
        "research2_execution_chains": "chain_id",
        "research_v160_simulated_accounts": "id",
        "research_v160_positions": "recommendation_id",
        "research_v160_recommendations": "recommendation_id",
        "research_v170_account_snapshots": "snapshot_id",
        "research_v170_account_cash_flows": "flow_id",
        "research_settings": "center",
    }
    for table, key in keys.items():
        if table not in golden:
            continue
        with database.connection() as db:
            actual = {r[key]: dict(r) for r in db.execute("SELECT * FROM " + qi(table))}
        expected = {r[key]: r for r in (golden[table] or [])}
        assert actual.keys() == expected.keys(), (case, table)
        for identity, want in expected.items():
            got = actual[identity]
            for field, value in want.items():
                if field in (
                    "id",
                    "created_at",
                    "updated_at",
                    "frozen_at",
                    "last_decision_at",
                    "exit_at",
                    "closed_at",
                ):
                    continue
                if case == "freeze" and field in ("valued_at", "current_price_at"):
                    continue
                if field == "config_json":
                    assert json.loads(got[field]) == json.loads(value), (case, table, field)
                elif field.endswith("_at") and value is not None:
                    assert time_key(got[field]) == time_key(value), (case, table, identity, field)
                elif isinstance(value, float):
                    assert got[field] == pytest.approx(value, abs=1e-8), (case, table, identity, field)
                else:
                    assert got[field] == value, (case, table, identity, field, got[field], value)
    with database.transaction() as db:
        before = db.total_changes
        apply(db)
        # Functions can update timestamps on idempotent validation; financial
        # cardinalities and values must remain equal, checked separately above.
        if case == "capital":
            assert db.total_changes == before
