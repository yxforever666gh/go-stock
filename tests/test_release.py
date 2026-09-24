import importlib.util
from pathlib import Path

import pytest

from stock_god.storage.db import Database

spec = importlib.util.spec_from_file_location(
    "release_operations", Path(__file__).parents[1] / "scripts/release.py"
)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def candidate(root):
    directory = root / "runtime/releases/6.0.0/fixture-commit"
    directory.mkdir(parents=True)
    python = directory / ".venv/Scripts/python.exe"
    python.parent.mkdir(parents=True)
    python.write_bytes(b"fixture python launcher")
    interpreter = root / "runtime/toolchain/python/python.exe"
    interpreter.parent.mkdir(parents=True)
    interpreter.write_bytes(b"fixture interpreter")
    (directory / "app.py").write_text("fixture", encoding="utf-8")
    manifest = {
        "appVersion": "6.0.0",
        "mainSchemaVersion": 36,
        "minuteSchemaVersion": 3,
        "commit": "fixture-commit",
        "dirty": False,
        "interpreterExecutable": str(interpreter),
        "interpreterSHA256": release.digest(interpreter),
        "files": release.bundle_files(directory),
    }
    release.write(directory / "build-manifest.json", manifest)
    return directory, release.verify_bundle(directory, root=root)


def proof(path, pointer):
    value = {
        "commit": pointer["commit"],
        "artifactSHA256": pointer["artifactSHA256"],
        "stages": {
            name: {"passed": True, "evidenceSHA256": "a" * 64}
            for name in ("local-release-gate", "offline-cold", "offline-restart", "live-prediction")
        },
    }
    release.write(path, value)
    return value


def legacy(root):
    directory = root / "runtime/releases/5.2.5/old-commit"
    directory.mkdir(parents=True)
    (directory / "go-stock-web.exe").write_bytes(b"old rollback fixture")
    (directory / "zoneinfo.zip").write_bytes(b"old time zones")
    pointer = {
        "appVersion": "5.2.5",
        "mainSchemaVersion": 35,
        "minuteSchemaVersion": 3,
        "commit": "old-commit",
        "binary": str(directory / "go-stock-web.exe"),
        "artifactSHA256": release.digest(directory / "go-stock-web.exe"),
        "zoneInfo": str(directory / "zoneinfo.zip"),
        "zoneInfoSHA256": release.digest(directory / "zoneinfo.zip"),
    }
    release.write(root / "runtime/current.json", pointer)
    return pointer


def test_candidate_seals_environment_and_rejects_extra_files(tmp_path):
    directory, pointer = candidate(tmp_path)
    assert release.verify_pointer(pointer, tmp_path) == pointer
    (directory / "extra.py").write_text("unexpected", encoding="utf-8")
    with pytest.raises(ValueError, match="unrecorded"):
        release.verify_bundle(directory, root=tmp_path)
    (directory / "extra.py").unlink()
    (directory / "app.py").write_text("changed", encoding="utf-8")
    with pytest.raises(ValueError, match="hash mismatch"):
        release.verify_bundle(directory, root=tmp_path)
    with pytest.raises(ValueError, match="outside"):
        release.inside(tmp_path / "../outside", tmp_path)


def test_acceptance_is_bound_to_final_candidate_and_all_four_stages(tmp_path):
    _, pointer = candidate(tmp_path)
    path = tmp_path / "proof.json"
    value = proof(path, pointer)
    assert release.validate_proof(path, pointer) == value
    value["commit"] = "old"
    release.write(path, value)
    with pytest.raises(ValueError, match="different candidate"):
        release.validate_proof(path, pointer)
    value = proof(path, pointer)
    value["stages"]["offline-restart"]["passed"] = False
    release.write(path, value)
    with pytest.raises(ValueError, match="incomplete"):
        release.validate_proof(path, pointer)


def test_failed_upgrade_restores_both_databases_and_old_pointer(tmp_path, monkeypatch):
    directory, pointer = candidate(tmp_path)
    before = legacy(tmp_path)
    proof_path = tmp_path / "proof.json"
    proof(proof_path, pointer)
    for name in ("stock.db", "minute.db"):
        with Database(tmp_path / "data" / name).transaction() as db:
            db.execute("CREATE TABLE records(value TEXT)")
            db.execute("INSERT INTO records VALUES('preserve original')")
    stops, starts = [], []
    monkeypatch.setattr(release, "stop", lambda item: stops.append(item["commit"]))
    monkeypatch.setattr(
        release, "start", lambda item, root, **kw: starts.append(item["commit"]) or {"ok": True}
    )
    monkeypatch.setattr(release, "listening", lambda: False)

    def broken_migration(*args):
        with Database(tmp_path / "data/stock.db").transaction() as db:
            db.execute("UPDATE records SET value='partial upgrade'")
        raise RuntimeError("fixture migration failure")

    monkeypatch.setattr(release, "database_command", broken_migration)
    with pytest.raises(RuntimeError, match="fixture migration failure"):
        release.deploy(directory, proof_path, tmp_path)
    assert stops == [before["commit"]] and starts == [before["commit"]]
    assert release.read(tmp_path / "runtime/current.json") == before
    for name in ("stock.db", "minute.db"):
        with Database(tmp_path / "data" / name, read_only=True).connection() as db:
            assert db.execute("SELECT value FROM records").fetchone()[0] == "preserve original"
    receipt = release.read(next((tmp_path / "runtime/deployments").glob("*/receipt.json")))
    assert receipt["status"] == "rolled_back"
    assert len(receipt["backups"]) == 2
    assert list(Path(receipt["directory"]).glob("restore-*/failed-stock.db"))


def test_running_identity_rejects_stale_python_and_wrong_commit(tmp_path):
    _, pointer = candidate(tmp_path)
    state = {**pointer, "dirty": False, "readiness": {"ready": True}}
    release.assert_identity(pointer, state)
    state["pythonExecutable"] = str(tmp_path / "other-python.exe")
    with pytest.raises(RuntimeError, match="Python executable mismatch"):
        release.assert_identity(pointer, state)
    state.update(pointer)
    state["commit"] = "stale"
    with pytest.raises(RuntimeError, match="commit"):
        release.assert_identity(pointer, state)
