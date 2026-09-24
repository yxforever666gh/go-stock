"""Explicit SQLite connection lifetime and serialized write transactions."""

import sqlite3
import time
from contextlib import contextmanager
from pathlib import Path

BUSY_DELAYS = (0.02, 0.04, 0.08, 0.16, 0.32)


def quote_identifier(value: str) -> str:
    return '"' + value.replace('"', '""') + '"'


def is_busy(error: sqlite3.Error) -> bool:
    code = getattr(error, "sqlite_errorcode", 0)
    return code & 0xFF in (sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED)


class Database:
    def __init__(self, path: str | Path, *, read_only: bool = False, busy_timeout_ms: int = 5000):
        self.path = str(path)
        self.read_only = read_only
        self.busy_timeout_ms = max(0, int(busy_timeout_ms))
        self._memory_connection = None
        if self.path == ":memory:":
            if read_only:
                raise ValueError("a read-only in-memory database has no source")
            self._memory_connection = self._connect()

    def _connect(self) -> sqlite3.Connection:
        if self.read_only:
            uri = Path(self.path).resolve().as_uri() + "?mode=ro"
            connection = sqlite3.connect(uri, uri=True, isolation_level=None,
                                         timeout=self.busy_timeout_ms / 1000)
        else:
            if self.path != ":memory:":
                Path(self.path).parent.mkdir(parents=True, exist_ok=True)
            connection = sqlite3.connect(self.path, isolation_level=None,
                                         timeout=self.busy_timeout_ms / 1000)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA foreign_keys=ON")
        connection.execute(f"PRAGMA busy_timeout={self.busy_timeout_ms}")
        if self.read_only:
            connection.execute("PRAGMA query_only=ON")
        else:
            connection.execute("PRAGMA journal_mode=WAL")
            connection.execute("PRAGMA synchronous=NORMAL")
        return connection

    @contextmanager
    def connection(self):
        connection = self._memory_connection or self._connect()
        try:
            yield connection
        finally:
            if connection is not self._memory_connection:
                connection.close()

    @contextmanager
    def transaction(self):
        if self.read_only:
            raise ValueError("cannot write through a read-only Database")
        with self.connection() as connection:
            if connection.in_transaction:
                raise RuntimeError("nested transactions require an explicit SAVEPOINT")
            for attempt in range(len(BUSY_DELAYS) + 1):
                try:
                    connection.execute("BEGIN IMMEDIATE")
                    break
                except sqlite3.OperationalError as error:
                    if not is_busy(error) or attempt == len(BUSY_DELAYS):
                        raise
                    time.sleep(BUSY_DELAYS[attempt])
            try:
                yield connection
                connection.commit()
            except BaseException:
                connection.rollback()
                raise

    def close(self):
        if self._memory_connection is not None:
            self._memory_connection.close()
            self._memory_connection = None
