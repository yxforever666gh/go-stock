"""WAL-aware backups and read-only integrity verification."""

import sqlite3
from hashlib import sha256
from pathlib import Path

from .db import Database


def file_sha256(path: str | Path) -> str:
    digest = sha256()
    with Path(path).open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_database(path: str | Path) -> dict:
    with Database(path, read_only=True).connection() as connection:
        results = [str(row[0]) for row in connection.execute("PRAGMA quick_check")]
        if results != ["ok"]:
            raise ValueError("SQLite integrity check failed: " + "; ".join(results))
        return {"path": str(Path(path).resolve()), "quickCheck": "ok"}


def backup_database(source_path: str | Path, destination_path: str | Path) -> dict:
    source = Path(source_path).resolve()
    destination = Path(destination_path).resolve()
    if source == destination:
        raise ValueError("backup destination must differ from source")
    if destination.exists():
        raise FileExistsError(f"backup destination already exists: {destination}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    # Exclusive reservation prevents overwriting an archive created concurrently.
    with destination.open("xb"):
        pass
    try:
        with Database(source, read_only=True).connection() as source_db:
            target_db = sqlite3.connect(destination)
            try:
                source_db.backup(target_db, pages=256, sleep=0.01)
            finally:
                target_db.close()
        result = verify_database(destination)
        result.update({"sha256": file_sha256(destination), "bytes": destination.stat().st_size})
        return result
    except BaseException:
        destination.unlink(missing_ok=True)
        raise
