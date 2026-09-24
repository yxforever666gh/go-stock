"""Local daily materialization and indexed breadth queries, independent of main DB."""

from datetime import datetime
from pathlib import Path
import sqlite3
from threading import Event

from .minute_local import CN, day_time, normalize_symbol

SCHEMA = (
    "CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL)",
    "CREATE TABLE IF NOT EXISTS daily_bar (stock_code TEXT NOT NULL,trade_date TEXT NOT NULL,stock_name TEXT,open REAL,high REAL,low REAL,close REAL,pre_close REAL,pct_chg REAL,volume REAL,amount REAL,PRIMARY KEY(stock_code,trade_date)) WITHOUT ROWID",
    "DROP INDEX IF EXISTS idx_daily_bar_date",
    "CREATE INDEX IF NOT EXISTS idx_daily_bar_date_pct ON daily_bar(trade_date,pct_chg)",
    "CREATE TABLE IF NOT EXISTS refresh_log (dataset TEXT NOT NULL,trade_date TEXT NOT NULL,status TEXT NOT NULL,rows INTEGER NOT NULL,detail TEXT,fetched_at TEXT NOT NULL,PRIMARY KEY(dataset,trade_date)) WITHOUT ROWID",
)


def validate_dates(start, end):
    first, last = day_time(start), day_time(end)
    if first is not None and last is not None and first > last:
        raise ValueError("开始日期不能晚于结束日期")


def breadth_query(start="", end=""):
    validate_dates(start, end)
    query = "SELECT trade_date,SUM(CASE WHEN pct_chg>0 THEN 1 ELSE 0 END) AS up,SUM(CASE WHEN pct_chg<0 THEN 1 ELSE 0 END) AS down,SUM(CASE WHEN pct_chg=0 THEN 1 ELSE 0 END) AS flat,COUNT(*) AS total FROM daily_bar WHERE pct_chg IS NOT NULL"
    args = []
    if start:
        query += " AND trade_date>=?"
        args.append(start)
    if end:
        query += " AND trade_date<=?"
        args.append(end)
    return query + " GROUP BY trade_date ORDER BY trade_date", args


class DailyStore:
    def __init__(self, directory, *, writable=False):
        self.path = Path(directory).resolve() / "daily.sqlite"
        self.writable = writable
        if writable or not self.path.exists():
            self.path.parent.mkdir(parents=True, exist_ok=True)
            with sqlite3.connect(self.path, timeout=10) as db:
                db.execute("PRAGMA journal_mode=WAL")
                db.execute("PRAGMA synchronous=NORMAL")
                for statement in SCHEMA:
                    db.execute(statement)

    def connect(self, *, write=False):
        if write and not self.writable:
            raise ValueError("日线库为只读")
        db = sqlite3.connect(
            str(self.path) if write else self.path.as_uri() + "?mode=ro", uri=not write, timeout=10
        )
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA busy_timeout=10000")
        if not write:
            db.execute("PRAGMA query_only=ON")
        return db

    def status(self):
        db = self.connect()
        try:
            row = db.execute(
                "SELECT COALESCE(MIN(trade_date),''),COALESCE(MAX(trade_date),''),COUNT(*),COUNT(DISTINCT trade_date) FROM daily_bar"
            ).fetchone()
            failed = [
                row[0]
                for row in db.execute(
                    "SELECT trade_date FROM refresh_log WHERE dataset='daily_bar' AND status<>'ok' ORDER BY trade_date"
                )
            ]
            last = db.execute(
                "SELECT COALESCE(MAX(fetched_at),'') FROM refresh_log WHERE dataset='daily_bar'"
            ).fetchone()[0]
            return dict(
                first_date=row[0],
                last_date=row[1],
                bars=row[2],
                dates=row[3],
                failed_dates=failed,
                last_fetched_at=last,
            )
        finally:
            db.close()

    def breadth(self, start="", end=""):
        query, args = breadth_query(start, end)
        db = self.connect()
        try:
            return [dict(row) for row in db.execute(query, args)]
        finally:
            db.close()

    def bars(self, codes, start="", end=""):
        validate_dates(start, end)
        if not codes or len(codes) > 100:
            raise ValueError("至少提供一个股票代码，一次最多100个")
        normalized = [normalize_symbol(code) for code in codes]
        if not all(normalized):
            raise ValueError("股票代码无效")
        query = (
            "SELECT stock_code,trade_date,COALESCE(stock_name,'') AS stock_name,open,high,low,close,pre_close,pct_chg,volume,amount FROM daily_bar WHERE stock_code IN ("
            + ",".join("?" for _ in normalized)
            + ")"
        )
        args = list(normalized)
        if start:
            query += " AND trade_date>=?"
            args.append(start)
        if end:
            query += " AND trade_date<=?"
            args.append(end)
        db = self.connect()
        try:
            return [dict(row) for row in db.execute(query + " ORDER BY stock_code,trade_date", args)]
        finally:
            db.close()

    def refreshed_dates(self):
        db = self.connect()
        try:
            return dict(db.execute("SELECT trade_date,status FROM refresh_log WHERE dataset='daily_bar'"))
        finally:
            db.close()

    def mark(self, day, status, count, detail=""):
        db = self.connect(write=True)
        try:
            db.execute(
                "INSERT OR REPLACE INTO refresh_log VALUES(?,?,?,?,?,?)",
                ("daily_bar", day, status, count, detail, datetime.now(CN).isoformat()),
            )
            db.commit()
        finally:
            db.close()

    def put_day(self, day, rows):
        if any(row["trade_date"] != day for row in rows):
            raise ValueError("日线响应包含其他交易日")
        db = self.connect(write=True)
        try:
            db.execute("BEGIN IMMEDIATE")
            db.execute("DELETE FROM daily_bar WHERE trade_date=?", (day,))
            fields = (
                "stock_code",
                "trade_date",
                "stock_name",
                "open",
                "high",
                "low",
                "close",
                "pre_close",
                "pct_chg",
                "volume",
                "amount",
            )
            db.executemany(
                "INSERT OR REPLACE INTO daily_bar VALUES(" + ",".join("?" for _ in fields) + ")",
                [[row.get(field) for field in fields] for row in rows],
            )
            db.execute(
                "INSERT OR REPLACE INTO refresh_log VALUES(?,?,?,?,?,?)",
                ("daily_bar", day, "ok", len(rows), "", datetime.now(CN).isoformat()),
            )
            db.commit()
        except BaseException:
            db.rollback()
            raise
        finally:
            db.close()


def trading_days(client, start, end):
    validate_dates(start, end)
    response = client.call("diemeng_basic_calendar", dict(start_time=start, end_time=end))["response"]
    if not isinstance(response.get("data"), list):
        raise ValueError("交易日历data必须是数组")
    return [
        row["date"]
        for row in response["data"]
        if isinstance(row, dict) and row.get("is_open") == 1 and row.get("date")
    ]


def fetch_day(client, day):
    result = []
    seen = set()
    total = None
    for page in range(50):
        response = client.call(
            "diemeng_stock_daily", dict(start_time=day, end_time=day, page=page, page_size=10000)
        )["response"]
        data = response.get("data")
        if not isinstance(data, dict) or not isinstance(data.get("list"), list):
            raise ValueError("日线data/list响应结构无效")
        count = data.get("total")
        if isinstance(count, bool) or not isinstance(count, int) or count < 0:
            raise ValueError("日线total无效")
        if total is not None and count != total:
            raise ValueError("日线分页total发生变化")
        total = count
        if not data["list"]:
            break
        for record in data["list"]:
            if not isinstance(record, dict):
                raise ValueError("日线记录必须是对象")
            code = normalize_symbol(record.get("stock_code", ""))
            record_day = record.get("trade_date") or day
            if not code or record_day != day:
                raise ValueError("日线记录代码/交易日无效")
            key = (code, record_day)
            if key in seen:
                raise ValueError("日线分页返回重复记录")
            seen.add(key)
            row = dict(stock_code=code, trade_date=record_day, stock_name=record.get("stock_name") or "")
            for output, field in (
                ("open", "open"),
                ("high", "high"),
                ("low", "low"),
                ("close", "close"),
                ("pre_close", "pre_close"),
                ("pct_chg", "pct_chg"),
                ("volume", "vol"),
                ("amount", "amount"),
            ):
                value = record.get(field)
                if value is not None and (isinstance(value, bool) or not isinstance(value, (int, float))):
                    raise ValueError("日线数值无效：" + field)
                row[output] = value
            result.append(row)
        if len(result) >= total:
            break
    if not result:
        raise ValueError("日线返回为空")
    if len(result) != total:
        raise ValueError("日线分页未完整获取，拒绝保存截断数据")
    return result


def refresh_daily(store, client, start, end, progress=lambda message: None, stop: Event | None = None):
    if client is None:
        raise ValueError("蝶梦未配置：缺少本机私有配置文件")
    days = trading_days(client, start, end)
    done = store.refreshed_dates()
    report = dict(total=len(days), skipped=0, ok=0, failed=0, rows=0)
    for index, day in enumerate(days, 1):
        if stop is not None and stop.is_set():
            raise InterruptedError("日线刷新已取消")
        if done.get(day) == "ok":
            report["skipped"] += 1
            continue
        try:
            rows = fetch_day(client, day)
            store.put_day(day, rows)
        except (ValueError, OSError, sqlite3.Error) as error:
            report["failed"] += 1
            store.mark(day, "failed", 0, str(error))
            progress(f"[{index}/{len(days)}] {day} 失败：{error}")
            continue
        report["ok"] += 1
        report["rows"] += len(rows)
        progress(f"[{index}/{len(days)}] {day} 写入 {len(rows)} 行")
    return report
