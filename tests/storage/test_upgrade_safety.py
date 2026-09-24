import pytest

from stock_god.storage.db import Database, quote_identifier as qi
from stock_god.storage import migrations
from stock_god.storage.archive import verify_archive
from test_published_upgrades import CASES, load_published


def case_for(tag):
    return next(case for case in CASES if tag in case["tags"])


@pytest.mark.migration
def test_schema35_retired_rows_remain_in_place_without_duplicate_archive(tmp_path):
    main = tmp_path / "main.db"
    with Database(main).transaction() as db:
        load_published(db, case_for("5.2.5"))
        preserved = {
            row[0]
            for row in db.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'research_v%' OR name LIKE 'knowledge_%')"
            )
        }
        before = {
            table: [tuple(row) for row in db.execute("SELECT * FROM " + qi(table))] for table in preserved
        }
    migrations.migrate(main, tmp_path / "minute.db")
    with Database(main, read_only=True).connection() as db:
        after = {
            table: [tuple(row) for row in db.execute("SELECT * FROM " + qi(table))] for table in preserved
        }
        assert before == after
        assert db.execute("SELECT COUNT(*) FROM legacy_archive_tables").fetchone()[0] == 0
        assert db.execute("SELECT COUNT(*) FROM legacy_archive_sets").fetchone()[0] == 0


@pytest.mark.migration
def test_destructive_migration_failure_rolls_back_and_resumes_original_archive(tmp_path, monkeypatch):
    main = tmp_path / "main.db"
    with Database(main).transaction() as db:
        load_published(db, case_for("1.6.5"))
        columns = {row["name"]: row for row in db.execute("PRAGMA table_info(ai_recommend_stocks)")}
        values = {
            name: ("" if "TEXT" in row["type"].upper() else 0)
            for name, row in columns.items()
            if row["notnull"] and row["dflt_value"] is None and not row["pk"]
        }
        values["stock_name"] = "original retained recommendation"
        db.execute(
            "INSERT INTO ai_recommend_stocks ("
            + ",".join(qi(k) for k in values)
            + ") VALUES ("
            + ",".join("?" for _ in values)
            + ")",
            tuple(values.values()),
        )
    original = migrations.apply_data

    def fail(db, version, **kwargs):
        if version == 9:
            raise RuntimeError("injected pre-commit failure")
        return original(db, version, **kwargs)

    monkeypatch.setattr(migrations, "apply_data", fail)
    with pytest.raises(RuntimeError, match="injected"):
        migrations.migrate(main, tmp_path / "minute.db")
    with Database(main, read_only=True).connection() as db:
        assert db.execute("SELECT MAX(id) FROM schema_migrations").fetchone()[0] == 8
        assert (
            db.execute("SELECT stock_name FROM ai_recommend_stocks").fetchone()[0]
            == "original retained recommendation"
        )
        archive_before = [
            tuple(r) for r in db.execute("SELECT * FROM legacy_archive_tables ORDER BY source_table")
        ]
        verify_archive(db)
    monkeypatch.setattr(migrations, "apply_data", original)
    migrations.migrate(main, tmp_path / "minute.db")
    with Database(main, read_only=True).connection() as db:
        assert not db.execute("SELECT 1 FROM sqlite_master WHERE name='ai_recommend_stocks'").fetchone()
        assert archive_before == [
            tuple(r) for r in db.execute("SELECT * FROM legacy_archive_tables ORDER BY source_table")
        ]
        table = db.execute(
            "SELECT archive_table FROM legacy_archive_tables WHERE source_table='ai_recommend_stocks'"
        ).fetchone()[0]
        assert (
            db.execute("SELECT stock_name FROM " + qi(table)).fetchone()[0]
            == "original retained recommendation"
        )


@pytest.mark.parametrize("fault", ["checksum", "gap", "future"])
def test_invalid_ledger_fails_before_archive_or_history_writes(tmp_path, fault):
    main = tmp_path / "main.db"
    with Database(main).transaction() as db:
        load_published(db, case_for("5.2.5"))
        if fault == "checksum":
            db.execute("UPDATE schema_migrations SET checksum='bad' WHERE id=5")
        elif fault == "gap":
            db.execute("DELETE FROM schema_migrations WHERE id=5")
        else:
            db.execute("INSERT INTO schema_migrations VALUES(99,'future','unknown','2026-01-01','7.0.0')")
    with pytest.raises(ValueError):
        migrations.migrate(main, tmp_path / "minute.db")
    with Database(main, read_only=True).connection() as db:
        assert not db.execute("SELECT 1 FROM sqlite_master WHERE name='legacy_archive_sets'").fetchone()


def test_conflicting_latest_schema_is_not_marked_migrated(tmp_path):
    main = tmp_path / "main.db"
    with Database(main).transaction() as db:
        load_published(db, case_for("5.2.5"))
        db.execute("DROP INDEX idx_research2_runs_date_attempt")
        db.execute("CREATE INDEX idx_research2_runs_date_attempt ON research2_analysis_runs(status)")
    with pytest.raises(ValueError, match="definition conflict"):
        migrations.migrate(main, tmp_path / "minute.db")
    with Database(main, read_only=True).connection() as db:
        assert db.execute("SELECT MAX(id) FROM schema_migrations").fetchone()[0] == 35
        assert not db.execute("SELECT 1 FROM sqlite_master WHERE name='legacy_archive_sets'").fetchone()


@pytest.mark.migration
@pytest.mark.parametrize("version", [0, 1, 2, 3])
def test_minute_versions_preserve_bars(version, tmp_path):
    main, minute = tmp_path / "main.db", tmp_path / "minute.db"
    migrations.migrate(main, minute)
    fresh = tmp_path / "old-minute.db"
    with Database(fresh).transaction() as db:
        if version:
            db.execute(migrations.LEDGER_SQL)
            for index in range(1, version + 1):
                migrations._schema(db, "minute", index)
                item = migrations.MANIFESTS["minute"][index - 1]
                db.execute(
                    "INSERT INTO schema_migrations VALUES(?,?,?,?,?)",
                    (index, item["name"], item["checksum"], "2026-01-01", "fixture"),
                )
            db.execute("INSERT INTO minute_bar VALUES('sh600000',123,1,2,0,1,NULL,0,'original',124)")
    result = migrations.migrate(main, fresh)
    assert result["minute"]["currentVersion"] == 3
    if version:
        with Database(fresh, read_only=True).connection() as db:
            row = db.execute("SELECT * FROM minute_bar").fetchone()
            assert tuple(row) == ("sh600000", 123, 1, 2, 0, 1, None, 0, "original", 124)
