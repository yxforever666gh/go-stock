"""Preserve original legacy rows inside their own working SQLite database."""

from datetime import datetime, timezone
from hashlib import sha256
import json
import sqlite3
import struct

from .db import quote_identifier as qi


ARCHIVE_ID = "pre-stock-god-6.0.0"
ARCHIVE_DDL = (
    """CREATE TABLE IF NOT EXISTS legacy_archive_sets (
      archive_id TEXT PRIMARY KEY, source_version INTEGER NOT NULL,
      source_fingerprint TEXT NOT NULL, source_ledger_json TEXT NOT NULL,
      created_at TEXT NOT NULL, manifest_sha256 TEXT NOT NULL)""",
    """CREATE TABLE IF NOT EXISTS legacy_archive_tables (
      archive_id TEXT NOT NULL, source_table TEXT NOT NULL, archive_table TEXT NOT NULL UNIQUE,
      original_ddl_json TEXT NOT NULL, columns_json TEXT NOT NULL,
      rowid_column TEXT, row_count INTEGER NOT NULL, content_sha256 TEXT NOT NULL,
      PRIMARY KEY(archive_id, source_table),
      FOREIGN KEY(archive_id) REFERENCES legacy_archive_sets(archive_id) DEFERRABLE INITIALLY DEFERRED)""",
)


def create_archive_schema(connection: sqlite3.Connection):
    for statement in ARCHIVE_DDL:
        connection.execute(statement)


def _encoded(value) -> bytes:
    if value is None:
        return b"n"
    if isinstance(value, int):
        raw = str(value).encode("ascii")
        tag = b"i"
    elif isinstance(value, float):
        raw = struct.pack(">d", value)
        tag = b"r"
    elif isinstance(value, str):
        raw = value.encode("utf-8", "surrogatepass")
        tag = b"t"
    elif isinstance(value, bytes):
        raw, tag = value, b"b"
    else:
        raise TypeError(f"unsupported SQLite storage value: {type(value)}")
    return tag + struct.pack(">Q", len(raw)) + raw


def content_hash(connection, table, columns, order):
    digest = sha256(json.dumps(columns, ensure_ascii=False, separators=(",", ":")).encode())
    query = "SELECT " + ",".join(qi(c) for c in columns) + " FROM " + qi(table)
    if order:
        query += " ORDER BY " + ",".join(qi(c) for c in order)
    count = 0
    for row in connection.execute(query):
        digest.update(b"R")
        for value in row:
            digest.update(_encoded(value))
        count += 1
    return count, digest.hexdigest()


def seal_legacy_archive(connection, source_version: int, affected_tables: set[str]):
    """Caller owns BEGIN IMMEDIATE; source copies and metadata commit together."""
    create_archive_schema(connection)
    existing = connection.execute("SELECT * FROM legacy_archive_sets WHERE archive_id=?", (ARCHIVE_ID,)).fetchone()
    if existing:
        verify_archive(connection)
        return
    ledger = []
    if connection.execute("SELECT 1 FROM sqlite_master WHERE name='schema_migrations'").fetchone():
        ledger = [dict(row) for row in connection.execute("SELECT * FROM schema_migrations ORDER BY id")]
    objects = [dict(row) for row in connection.execute(
        "SELECT type,name,tbl_name,sql FROM sqlite_master WHERE sql IS NOT NULL "
        "AND name NOT LIKE 'legacy_archive_%' ORDER BY type,name")]
    fingerprint = sha256(json.dumps(objects, ensure_ascii=False, sort_keys=True).encode()).hexdigest()
    manifests = []
    for index, table in enumerate(sorted(affected_tables)):
        original = [obj for obj in objects if obj["tbl_name"] == table]
        table_object = next((obj for obj in original if obj["type"] == "table"), None)
        if table_object is None:
            continue
        columns = [dict(row) for row in connection.execute("PRAGMA table_xinfo(" + qi(table) + ")")]
        visible = [col["name"] for col in columns if col["hidden"] != 1]
        if not visible:
            raise ValueError(f"cannot archive table without visible columns: {table}")
        rowid_column = None
        rowid_expression = None
        if "WITHOUT ROWID" not in table_object["sql"].upper() and "VIRTUAL TABLE" not in table_object["sql"].upper():
            rowid_expression = next((name for name in ("rowid", "_rowid_", "oid") if name not in visible), None)
            if rowid_expression:
                rowid_column = "__original_rowid"
                while rowid_column in visible:
                    rowid_column += "_"
        archive_table = f"legacy_archive_v6_{index:04d}"
        if connection.execute("SELECT 1 FROM sqlite_master WHERE name=?", (archive_table,)).fetchone():
            raise ValueError(f"unsealed archive object already exists: {archive_table}")
        archive_columns = ([rowid_column] if rowid_column else []) + visible
        connection.execute("CREATE TABLE " + qi(archive_table) + " (" + ",".join(qi(c) for c in archive_columns) + ")")
        expressions = ([qi(rowid_expression)] if rowid_column else []) + [qi(c) for c in visible]
        connection.execute("INSERT INTO " + qi(archive_table) + " SELECT " + ",".join(expressions) + " FROM " + qi(table))
        order = [rowid_column] if rowid_column else [col["name"] for col in sorted(columns, key=lambda c: c["pk"]) if col["pk"]]
        if not order:
            order = visible
        count, digest = content_hash(connection, archive_table, archive_columns, order)
        source_query = "SELECT " + ",".join(expressions) + " FROM " + qi(table)
        source_order = [rowid_expression] if rowid_column else order
        source_query += " ORDER BY " + ",".join(qi(c) for c in source_order)
        source_digest = sha256(json.dumps(archive_columns, ensure_ascii=False, separators=(",", ":")).encode())
        source_count = 0
        for row in connection.execute(source_query):
            source_digest.update(b"R")
            for value in row:
                source_digest.update(_encoded(value))
            source_count += 1
        if (source_count, source_digest.hexdigest()) != (count, digest):
            raise ValueError(f"legacy archive differs from source: {table}")
        manifest = dict(source_table=table, archive_table=archive_table,
                        original_ddl_json=json.dumps(original, ensure_ascii=False),
                        columns_json=json.dumps({"source": columns, "columns": archive_columns, "order": order}, ensure_ascii=False),
                        rowid_column=rowid_column, row_count=count, content_sha256=digest)
        manifests.append(manifest)
        connection.execute("INSERT INTO legacy_archive_tables VALUES (?,?,?,?,?,?,?,?)",
                           (ARCHIVE_ID, table, archive_table, manifest["original_ddl_json"],
                            manifest["columns_json"], rowid_column, count, digest))
    manifest_hash = sha256(json.dumps(manifests, ensure_ascii=False, sort_keys=True).encode()).hexdigest()
    connection.execute("INSERT INTO legacy_archive_sets VALUES (?,?,?,?,?,?)",
                       (ARCHIVE_ID, source_version, fingerprint, json.dumps(ledger, default=str),
                        datetime.now(timezone.utc).isoformat(), manifest_hash))


def verify_archive(connection):
    if not connection.execute("SELECT 1 FROM sqlite_master WHERE name='legacy_archive_sets'").fetchone():
        return
    for batch in connection.execute("SELECT * FROM legacy_archive_sets ORDER BY archive_id"):
        manifests = []
        for row in connection.execute("SELECT * FROM legacy_archive_tables WHERE archive_id=? ORDER BY source_table", (batch["archive_id"],)):
            columns = json.loads(row["columns_json"])
            count, digest = content_hash(connection, row["archive_table"], columns["columns"], columns["order"])
            if (count, digest) != (row["row_count"], row["content_sha256"]):
                raise ValueError(f"legacy archive content conflict: {row['source_table']}")
            manifests.append({key: row[key] for key in row.keys() if key != "archive_id"})
        actual = sha256(json.dumps(manifests, ensure_ascii=False, sort_keys=True).encode()).hexdigest()
        if actual != batch["manifest_sha256"]:
            raise ValueError("legacy archive manifest conflict")
