from decimal import Decimal
from threading import Event

import pytest

from stock_god.market.minute_index import AuctionIndex, relocate_index
from stock_god.market.minute_local import (
    AUCTION_HEADERS,
    INDEX_HEADERS,
    STOCK_HEADERS,
    LocalStore,
    minute_time,
    query_with_source,
)


def fixture(root, relative, body):
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(body.encode("utf-8"))
    return path


def stock_row(stamp, close="10"):
    return (
        stamp
        + ",10,11,9,"
        + close
        + ",22500,2169410,-0.02,-0.02074043347505963,0.002492334989894246,902767890,21687215000"
    )


def auction_row(stamp, price="10", code="600941.SH"):
    return code + "," + stamp + ",10," + price + ",0,0,0,10,100,,0,10,100,,0,0,,0,"


def test_stock_history_current_overrides_and_exact_nanosecond_selection(tmp_path):
    header = ",".join(STOCK_HEADERS) + "\n"
    fixture(
        tmp_path, "A股个股/2025/1分钟/sh600941.csv", header + stock_row("2025-12-31 15:00:00", "10") + "\n"
    )
    fixture(
        tmp_path,
        "A股个股/2026/1分钟/sh600941.csv",
        header
        + stock_row("2025-12-31 15:00:00", "11")
        + "\n"
        + stock_row("2026-01-05 09:30:00", "12")
        + "\n",
    )
    store = LocalStore(tmp_path)
    rows = store.stock_range(
        "sh600941", "1m", minute_time("2025-12-31 15:00"), minute_time("2026-01-05 09:30")
    )
    assert [row["close"] for row in rows] == [11, 12]
    exact = minute_time("2026-01-05T09:30:00.000000001+08:00")
    assert store.stock_range("sh600941", "1m", exact, exact) == []

    class Provider:
        def minutes(self, *args):
            pytest.fail("partial local data must not trigger fallback")

    result = query_with_source(
        store,
        Provider(),
        "sh600941",
        "1m",
        "auto",
        minute_time("2026-01-05 09:30"),
        minute_time("2026-01-05 09:31"),
    )
    assert result["source"] == "csv" and len(result["data"]) == 1


@pytest.mark.parametrize("bad", ["null", "true", "12 ", "01", "NaN", '"""12"""'])
def test_corrupt_rows_outside_requested_window_are_not_skipped(tmp_path, bad):
    fixture(
        tmp_path,
        "A股个股/1分钟/sh600941.csv",
        ",".join(STOCK_HEADERS) + "\n" + stock_row("2025-01-01 10:00:00", bad) + "\n",
    )
    with pytest.raises((ValueError, RuntimeError)):
        LocalStore(tmp_path).stock_range(
            "sh600941", "1m", minute_time("2026-01-05 09:30"), minute_time("2026-01-05 09:31")
        )


def test_index_identity_deduplication_and_exact_decimal_conflicts(tmp_path):
    fixture(
        tmp_path, "分钟K线-指数/对应名称.csv", "index,code,name\n1,sh000001,上证指数\n2,sh999999,上证指数\n"
    )
    header = ",".join(INDEX_HEADERS) + "\n"
    fixture(tmp_path, "分钟K线-指数/2025/1分钟/sh000001.csv", header + "2025-01-02,09:31,1,2,1,2,0,100\n")
    path = fixture(
        tmp_path,
        "分钟K线-指数/2026/1分钟/sh000001.csv",
        header + "2025-01-02,09:31,1.0,2.0,1.0,2.0,0.0,100.0\n",
    )
    store = LocalStore(tmp_path)
    at = minute_time("2025-01-02T01:31:00Z")
    assert store.search_indices("上证") == [dict(symbol="sh000001", name="上证指数", periods=["1m"])]
    rows = store.index_bars("sh000001", "1m", at, at)
    assert len(rows) == 1 and rows[0]["volume"] == Decimal("0.0")
    path.write_text(header + "2025-01-02,09:31,1,2,1,2.000000000000000000001,0,100\n", encoding="utf-8")
    with pytest.raises(ValueError, match="数据冲突"):
        store.index_bars("sh000001", "1m", at, at)


def test_auction_index_resume_zero_null_pagination_and_change_detection(tmp_path):
    root = tmp_path / "data"
    header = ",".join(AUCTION_HEADERS) + "\r\n"
    first = fixture(
        root,
        "集合竞价/2025/a.csv",
        header
        + auction_row("2025-08-12 09:15:03")
        + "\r\n"
        + auction_row("2025-08-12 09:15:00", "9")
        + "\r\n",
    )
    fixture(root, "集合竞价/2025/b.csv", header + auction_row("2025-08-12 09:15:00", "9.0") + "\r\n")
    store = LocalStore(root)
    index = AuctionIndex(root, tmp_path / "index", writable=True)
    try:
        index.prepare(store.auction_files)
        start, end = minute_time("2025-08-12 09:15:00"), minute_time("2025-08-12 09:15:03")
        result = index.query("sh600941", start, end, 0, 1)
        assert result["total"] == 2 and result["next_page"] == 1
        row = result["data"][0]
        assert len(row) == 20 and row["volume"] == 0 and row["b2_p"] is None
        assert index.query("sh600941", start, end, 1, 1)["next_page"] is None
        messages = []
        index.prepare(store.auction_files, messages.append)
        assert len(messages) == 2 and all("cached" in m for m in messages)
        first.write_text(header + auction_row("2025-08-12 09:15:00", "12") + "\r\n", encoding="utf-8")
        with pytest.raises(ValueError, match="变化"):
            index.query("sh600941", start, end)
        index.prepare(LocalStore(root).auction_files)
        with pytest.raises(ValueError, match="数据冲突"):
            index.query("sh600941", start, end)
    finally:
        index.close()


def test_malformed_auction_file_rolls_back_only_that_file_and_cancellation(tmp_path):
    root = tmp_path / "data"
    header = ",".join(AUCTION_HEADERS) + "\n"
    fixture(root, "集合竞价/a.csv", header + auction_row("2025-01-02 09:15:00") + "\n")
    bad = fixture(root, "集合竞价/b.csv", header + auction_row("2025-01-02 09:15:03") + "\nshort,row\n")
    index = AuctionIndex(root, tmp_path / "index", writable=True)
    try:
        with pytest.raises(ValueError):
            index.prepare(LocalStore(root).auction_files)
        assert index.db.execute("SELECT COUNT(*) FROM files").fetchone()[0] == 1
        bad.write_text(header + auction_row("2025-01-02 09:15:03") + "\n", encoding="utf-8")
        stop = Event()
        stop.set()
        with pytest.raises(InterruptedError):
            index.prepare(LocalStore(root).auction_files, stop=stop)
        assert index.db.execute("SELECT COUNT(*) FROM files").fetchone()[0] == 1
        index.prepare(LocalStore(root).auction_files)
    finally:
        index.close()


def test_relocate_requires_moved_tree_and_identical_file_fingerprints(tmp_path):
    old = tmp_path / "old"
    new = tmp_path / "new"
    directory = tmp_path / "index"
    fixture(
        old, "集合竞价/a.csv", ",".join(AUCTION_HEADERS) + "\n" + auction_row("2025-01-02 09:15:00") + "\n"
    )
    index = AuctionIndex(old, directory, writable=True)
    index.prepare(LocalStore(old).auction_files)
    index.close()
    with pytest.raises(ValueError):
        relocate_index(directory, old, new)
    old.rename(new)
    assert relocate_index(directory, old, new)["files"] == 1
    readonly = AuctionIndex(new, directory)
    try:
        readonly.ready(LocalStore(new).auction_files)
    finally:
        readonly.close()
