"""Resumable byte-offset auction index compatible with positions.sqlite."""

import csv
import sqlite3
from pathlib import Path
from threading import Event, RLock

from .minute_local import (
    AUCTION_HEADERS,
    SYMBOL,
    LocalStore,
    SourceFile,
    iso_time,
    local_datetime,
    merge_row,
    minute_time,
    numeric,
)

SCHEMA = (
    "CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL)",
    "CREATE TABLE IF NOT EXISTS files (id INTEGER PRIMARY KEY,rel TEXT NOT NULL UNIQUE,size INTEGER NOT NULL,modified INTEGER NOT NULL,records INTEGER NOT NULL,first_time TEXT NOT NULL,last_time TEXT NOT NULL)",
    "CREATE TABLE IF NOT EXISTS blocks (file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,symbol TEXT NOT NULL,day TEXT NOT NULL,start INTEGER NOT NULL,end INTEGER NOT NULL,PRIMARY KEY(file_id,start)) WITHOUT ROWID",
    "CREATE INDEX IF NOT EXISTS blocks_lookup ON blocks(symbol,day)",
)


def offset_records(source, end=None):
    def lines():
        while end is None or source.tell() < end:
            line = source.readline(-1 if end is None else end - source.tell())
            if not line:
                return
            yield line.decode("utf-8")

    reader = csv.reader(lines(), strict=True)
    while True:
        begin = source.tell()
        try:
            row = next(reader)
        except StopIteration:
            return
        if row:
            yield row, begin, source.tell()


def validate_auction(row):
    if len(row) != len(AUCTION_HEADERS):
        raise ValueError("竞价字段数量无效")
    parts = row[0].split(".")
    if len(parts) != 2 or not SYMBOL.fullmatch(parts[1].lower() + parts[0]):
        raise ValueError("竞价股票代码无效")
    if len(row[1]) != 19:
        raise ValueError("竞价时间无效")
    stamp = minute_time(row[1])
    values = [numeric(cell, True) for cell in row[2:]]
    return parts[1].lower() + parts[0], stamp, values


def cancelled(event):
    if event is not None and event.is_set():
        raise InterruptedError("索引准备已取消")


class AuctionIndex:
    def __init__(self, root, directory, *, writable=False):
        self.root = Path(root).resolve()
        self.path = Path(directory).resolve() / "positions.sqlite"
        self.files: list[SourceFile] = []
        self.lock = RLock()
        self.writable = writable
        if writable:
            self.path.parent.mkdir(parents=True, exist_ok=True)
        if not writable and not self.path.is_file():
            raise FileNotFoundError("请先运行 --prepare-data 建立竞价索引")
        self.db = sqlite3.connect(
            str(self.path) if writable else self.path.as_uri() + "?mode=ro",
            uri=not writable,
            check_same_thread=False,
            isolation_level=None,
            timeout=5,
        )
        self.db.row_factory = sqlite3.Row
        self.db.execute("PRAGMA foreign_keys=ON")
        self.db.execute("PRAGMA busy_timeout=5000")
        try:
            if writable:
                self.db.execute("PRAGMA journal_mode=WAL")
                self.db.execute("PRAGMA synchronous=NORMAL")
                for sql in SCHEMA:
                    self.db.execute(sql)
            saved = self.db.execute("SELECT value FROM metadata WHERE key='root'").fetchone()
            if not saved or saved[0] != str(self.root):
                if not writable:
                    raise ValueError("索引不属于当前data-root，请重新 --prepare-data")
                self.db.execute("BEGIN IMMEDIATE")
                try:
                    self.db.execute("DELETE FROM files")
                    self.db.execute("INSERT OR REPLACE INTO metadata VALUES('root',?)", (str(self.root),))
                    self.db.commit()
                except BaseException:
                    self.db.rollback()
                    raise
        except BaseException:
            self.db.close()
            raise

    def close(self):
        self.db.close()

    def ready(self, files):
        with self.lock:
            saved = {
                row["rel"]: (row["size"], row["modified"])
                for row in self.db.execute("SELECT rel,size,modified FROM files")
            }
            if len(saved) != len(files):
                raise ValueError("竞价文件清单变化，请先 --prepare-data")
            for file in files:
                if saved.get(file.relative) != (file.size, file.modified):
                    raise ValueError("竞价索引未就绪：" + file.relative)
                file.check()
            self.files = list(files)

    def prepare(self, files, progress=lambda message: None, stop: Event | None = None):
        if not self.writable:
            raise ValueError("索引为只读")
        with self.lock:
            valid = set()
            for index, file in enumerate(files, 1):
                cancelled(stop)
                valid.add(file.relative)
                saved = self.db.execute(
                    "SELECT size,modified FROM files WHERE rel=?", (file.relative,)
                ).fetchone()
                if saved and tuple(saved) == (file.size, file.modified):
                    file.check()
                    progress(f"[{index}/{len(files)}] cached {file.relative}")
                    continue
                progress(f"[{index}/{len(files)}] indexing {file.relative} ({file.size / 1e6:.1f} MB)")
                self._index_file(file, stop)
            for row in self.db.execute("SELECT rel FROM files").fetchall():
                if row[0] not in valid:
                    self.db.execute("DELETE FROM files WHERE rel=?", (row[0],))
            self.ready(files)
            self.db.execute("PRAGMA wal_checkpoint(TRUNCATE)")

    def _index_file(self, file, stop):
        file.check()
        with file.path.open("rb") as source:
            reader = offset_records(source)
            first = next(reader, None)
            if first is None:
                raise ValueError("竞价CSV为空")
            header = first[0]
            header[0] = header[0].lstrip("\ufeff")
            if header != AUCTION_HEADERS:
                raise ValueError("竞价CSV表头无效")
            self.db.execute("BEGIN IMMEDIATE")
            try:
                self.db.execute("DELETE FROM files WHERE rel=?", (file.relative,))
                identifier = self.db.execute(
                    "INSERT INTO files(rel,size,modified,records,first_time,last_time) VALUES(?,?,?,0,'','')",
                    (file.relative, file.size, file.modified),
                ).lastrowid
                current = None
                begin = finish = count = 0
                first_time = last_time = ""
                for row, start, end in reader:
                    cancelled(stop)
                    symbol, _, _ = validate_auction(row)
                    day = row[1][:10]
                    if current != (symbol, day):
                        if current:
                            self.db.execute(
                                "INSERT INTO blocks VALUES(?,?,?,?,?)", (identifier, *current, begin, finish)
                            )
                        current = (symbol, day)
                        begin = start
                    finish = end
                    count += 1
                    first_time = min(first_time or row[1], row[1])
                    last_time = max(last_time, row[1])
                if current:
                    self.db.execute(
                        "INSERT INTO blocks VALUES(?,?,?,?,?)", (identifier, *current, begin, finish)
                    )
                file.check()
                cancelled(stop)
                self.db.execute(
                    "UPDATE files SET records=?,first_time=?,last_time=? WHERE id=?",
                    (count, first_time, last_time, identifier),
                )
                self.db.commit()
            except BaseException:
                self.db.rollback()
                raise

    def query(self, symbol, start, end, page=0, page_size=500):
        if not SYMBOL.fullmatch(symbol) or start > end or page < 0 or not 1 <= page_size <= 5000:
            raise ValueError("代码、时间范围或分页参数无效")
        for file in self.files:
            file.check()
        with self.lock:
            blocks = self.db.execute(
                "SELECT f.rel,b.start,b.end FROM blocks b JOIN files f ON f.id=b.file_id WHERE b.symbol=? AND b.day>=? AND b.day<=? ORDER BY b.day,f.rel,b.start",
                (symbol, local_datetime(start).date().isoformat(), local_datetime(end).date().isoformat()),
            ).fetchall()
        values = {}
        origins = {}
        for block in blocks:
            path = (self.root / block["rel"]).resolve()
            if not path.is_relative_to(self.root):
                raise ValueError("竞价索引路径越界")
            with path.open("rb") as source:
                source.seek(block["start"])
                for cells, _, _ in offset_records(source, block["end"]):
                    code, stamp, numbers = validate_auction(cells)
                    if code != symbol:
                        raise ValueError("竞价索引与文件不匹配，请重建")
                    if not start <= stamp <= end:
                        continue
                    row = dict(
                        time=iso_time(stamp),
                        code=cells[0],
                        trade_date=cells[1],
                        **dict(zip(AUCTION_HEADERS[2:], numbers, strict=False)),
                    )
                    merge_row(values, origins, row, block["rel"])
        for file in self.files:
            file.check()
        ordered = [values[key] for key in sorted(values)]
        begin = min(len(ordered), page * page_size)
        finish = min(len(ordered), begin + page_size)
        return dict(
            symbol=symbol,
            source="csv_auction",
            timezone="Asia/Shanghai",
            units="unknown: original provider values",
            page=page,
            page_size=page_size,
            total=len(ordered),
            next_page=page + 1 if finish < len(ordered) else None,
            data=ordered[begin:finish],
        )


def relocate_index(directory, old_root, new_root):
    """Rebind an already-moved tree only after every stored fingerprint matches."""
    old = Path(old_root).absolute()
    new = Path(new_root).resolve()
    if old.exists() or old == new:
        raise ValueError("旧数据目录仍存在，不能按同盘移动重绑定索引")
    store = LocalStore(new)
    path = Path(directory).resolve() / "positions.sqlite"
    if not path.is_file():
        raise FileNotFoundError(path)
    db = sqlite3.connect(path, isolation_level=None)
    try:
        db.execute("BEGIN IMMEDIATE")
        saved = db.execute("SELECT value FROM metadata WHERE key='root'").fetchone()
        if not saved or Path(saved[0]).absolute() != old:
            raise ValueError("原索引根目录不匹配")
        expected = {r[0]: (r[1], r[2]) for r in db.execute("SELECT rel,size,modified FROM files")}
        actual = {f.relative: (f.size, f.modified) for f in store.auction_files}
        if expected != actual:
            raise ValueError("移动后的竞价文件指纹不匹配，必须重建索引")
        for file in store.auction_files:
            file.check()
        db.execute("UPDATE metadata SET value=? WHERE key='root'", (str(new),))
        db.commit()
        return dict(files=len(actual), root=str(new))
    except BaseException:
        db.rollback()
        raise
    finally:
        db.close()
