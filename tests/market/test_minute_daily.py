import sqlite3

import pytest

from stock_god.market.minute_daily import DailyStore, breadth_query, fetch_day, refresh_daily


def row(code, day, pct):
    return dict(
        stock_code=code,
        trade_date=day,
        stock_name=code,
        open=10,
        high=11,
        low=9,
        close=10,
        pre_close=10,
        pct_chg=pct,
        volume=0,
        amount=0,
    )


def test_daily_sqlite_coverage_null_breadth_index_and_readonly(tmp_path):
    store = DailyStore(tmp_path, writable=True)
    store.put_day(
        "2026-09-01",
        [
            row("sh600001", "2026-09-01", 1),
            row("sz000001", "2026-09-01", -1),
            row("sh600002", "2026-09-01", 0),
            row("sh600003", "2026-09-01", None),
        ],
    )
    store.mark("2026-09-02", "failed", 0, "fixture failure")
    readonly = DailyStore(tmp_path)
    assert readonly.breadth("2026-09-01", "2026-09-01") == [
        dict(trade_date="2026-09-01", up=1, down=1, flat=1, total=3)
    ]
    assert readonly.bars(["600003.SH"])[0]["pct_chg"] is None
    assert readonly.status()["failed_dates"] == ["2026-09-02"]
    connection = readonly.connect()
    try:
        query, args = breadth_query("2026-09-01", "2026-09-02")
        plan = " ".join(str(tuple(r)) for r in connection.execute("EXPLAIN QUERY PLAN " + query, args))
        assert "COVERING INDEX idx_daily_bar_date_pct" in plan
        with pytest.raises(sqlite3.OperationalError):
            connection.execute("DELETE FROM daily_bar")
    finally:
        connection.close()


def test_daily_write_failure_preserves_prior_day(tmp_path):
    store = DailyStore(tmp_path, writable=True)
    original = row("sh600001", "2026-09-01", 1)
    store.put_day("2026-09-01", [original])
    broken = row("sh600002", "2026-09-01", -1)
    broken["stock_code"] = None
    with pytest.raises(sqlite3.IntegrityError):
        store.put_day("2026-09-01", [broken])
    assert store.bars(["sh600001"]) == [original]


def test_daily_fetch_pages_resume_and_never_silently_truncates(tmp_path):
    calls = []
    truncate = False

    class Provider:
        def call(self, name, args):
            calls.append((name, args))
            if name == "diemeng_basic_calendar":
                return {"response": {"data": [{"date": "2026-09-01", "is_open": 1}]}}
            page = args["page"]
            record = {**row("sh600001" if page == 0 else "sz000001", "2026-09-01", None), "vol": 100}
            return {"response": {"data": {"total": 2, "list": [] if truncate and page == 1 else [record]}}}

    provider = Provider()
    store = DailyStore(tmp_path, writable=True)
    report = refresh_daily(store, provider, "2026-09-01", "2026-09-01")
    assert report["rows"] == 2 and report["ok"] == 1
    assert [args["page"] for name, args in calls if name == "diemeng_stock_daily"] == [0, 1]
    assert refresh_daily(store, provider, "2026-09-01", "2026-09-01")["skipped"] == 1
    truncate = True
    with pytest.raises(ValueError, match="未完整"):
        fetch_day(provider, "2026-09-01")
