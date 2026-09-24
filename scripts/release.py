"""Local immutable Python releases, verified activation and database rollback."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

from stock_god.cli import process_lock  # noqa: E402
from stock_god.storage.backup import backup_database, verify_database  # noqa: E402


def timestamp():
    return datetime.now(UTC).isoformat()


def read(path):
    return json.loads(Path(path).read_text(encoding="utf-8-sig"))


def write(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, path)


def digest(path):
    with Path(path).open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def inside(path, root):
    path, root = Path(path).resolve(), Path(root).resolve()
    if path == root or not path.is_relative_to(root):
        raise ValueError(f"path is outside intended directory: {path}")
    return path


def run(args, *, cwd=ROOT, env=None, capture=False):
    result = subprocess.run(
        [str(arg) for arg in args],
        cwd=cwd,
        env=env,
        check=True,
        stdout=subprocess.PIPE if capture else None,
        encoding="utf-8",
        errors="replace",
    )
    return result.stdout.strip() if capture else None


def clean_commit(root):
    if run(["git", "status", "--porcelain"], cwd=root, capture=True):
        raise ValueError("release requires a clean checkout")
    return run(["git", "rev-parse", "HEAD"], cwd=root, capture=True)


def bundle_files(directory):
    return {
        path.relative_to(directory).as_posix(): digest(path)
        for path in sorted(directory.rglob("*"))
        if path.is_file()
        and path.name != "build-manifest.json"
        and "__pycache__" not in path.parts
        and path.suffix not in {".pyc", ".pyo"}
    }


def verify_bundle(directory, *, root=ROOT):
    directory = inside(directory, root / "runtime/releases")
    manifest_path = directory / "build-manifest.json"
    manifest = read(manifest_path)
    if manifest.get("dirty") is not False or not manifest.get("files"):
        raise ValueError("candidate is dirty or has no file manifest")
    for name, expected in manifest["files"].items():
        path = inside(directory / name, directory)
        if not path.is_file() or digest(path) != expected:
            raise ValueError("candidate file hash mismatch: " + name)
    if manifest["files"] != bundle_files(directory):
        raise ValueError("candidate contains unrecorded files")
    interpreter = inside(manifest["interpreterExecutable"], root / "runtime/toolchain")
    if digest(interpreter) != manifest["interpreterSHA256"]:
        raise ValueError("release interpreter hash mismatch")
    return {
        **{
            name: manifest[name]
            for name in ("appVersion", "mainSchemaVersion", "minuteSchemaVersion", "commit")
        },
        "kind": "python",
        "releaseDirectory": str(directory),
        "pythonExecutable": str(directory / ".venv/Scripts/python.exe"),
        "interpreterExecutable": str(interpreter),
        "artifactSHA256": digest(manifest_path),
    }


def release_env(pointer, root=ROOT, *, scheduler=True):
    env = os.environ.copy()
    # Ignore inherited source-run overrides when starting a deployed release.
    for key in list(env):
        if key.startswith(("GO_STOCK_", "STOCK_GOD_")) or key in {"PYTHONPATH", "PYTHONHOME"}:
            env.pop(key)
    if pointer.get("kind") == "python":
        directory = Path(pointer["releaseDirectory"])
        env.update(
            STOCK_GOD_ROOT=str(root),
            STOCK_GOD_RELEASE_DIR=str(directory),
            STOCK_GOD_FRONTEND_DIST=str(directory / "frontend/dist"),
            STOCK_GOD_SCHEDULER="1" if scheduler else "0",
            PYTHONPATH=str(directory / "src"),
            PYTHONUTF8="1",
            PYTHONDONTWRITEBYTECODE="1",
        )
    else:
        env.update(
            GO_STOCK_WEB_ADDR="127.0.0.1:34115",
            GO_STOCK_DB_PATH=str(root / "data/stock.db") + "?_pragma=journal_mode(WAL)",
            GO_STOCK_MINUTE_DB_PATH=str(root / "data/minute.db") + "?_pragma=journal_mode(WAL)",
            ZONEINFO=pointer["zoneInfo"],
        )
    return env


def build(root, uv):
    commit = clean_commit(root)
    version = read(root / "src/stock_god/release_manifest.json")
    destination = root / "runtime/releases" / version["appVersion"] / commit
    if destination.exists():
        return verify_bundle(destination, root=root)
    cache = Path("H:/Download/stock-god-build-cache")
    env = os.environ.copy()
    env.update(
        UV_CACHE_DIR=str(cache / "uv"),
        UV_PYTHON_INSTALL_DIR=str(root / "runtime/toolchain/python"),
        HTTP_PROXY="http://127.0.0.1:7890",
        HTTPS_PROXY="http://127.0.0.1:7890",
        npm_config_cache=str(cache / "npm"),
        npm_config_proxy="http://127.0.0.1:7890",
        npm_config_https_proxy="http://127.0.0.1:7890",
    )
    python_version = (root / ".python-version").read_text(encoding="utf-8").strip()
    run([uv, "python", "install", python_version], env=env, cwd=root)
    interpreter = Path(
        run([uv, "python", "find", "--managed-python", python_version], env=env, cwd=root, capture=True)
    )
    inside(interpreter, root / "runtime/toolchain")
    npm = shutil.which("npm.cmd" if os.name == "nt" else "npm")
    if not npm:
        raise RuntimeError("npm is unavailable")
    run([npm, "ci", "--no-audit", "--no-fund"], cwd=root / "frontend", env=env)
    run([npm, "run", "build"], cwd=root / "frontend", env=env)
    destination.mkdir(parents=True)
    # A bundle is assembled at its final path because Windows venvs are not relocatable.
    for name in ("src/stock_god", "api", "frontend/dist"):
        shutil.copytree(
            root / name, destination / name, ignore=shutil.ignore_patterns("__pycache__", "*.pyc")
        )
    for name in ("pyproject.toml", "uv.lock", ".python-version", "LICENSE", "NOTICE"):
        if (root / name).is_file():
            shutil.copy2(root / name, destination / name)
    env["UV_PROJECT_ENVIRONMENT"] = str(destination / ".venv")
    run(
        [
            uv,
            "sync",
            "--frozen",
            "--no-dev",
            "--no-install-project",
            "--python",
            interpreter,
            "--project",
            destination,
        ],
        cwd=destination,
        env=env,
    )
    if clean_commit(root) != commit:
        raise RuntimeError("checkout changed during release build; candidate remains unsealed")
    manifest = {
        **version,
        "commit": commit,
        "buildTime": timestamp(),
        "dirty": False,
        "pythonVersion": python_version,
        "interpreterExecutable": str(interpreter),
        "interpreterSHA256": digest(interpreter),
        "files": bundle_files(destination),
    }
    write(destination / "build-manifest.json", manifest)
    return verify_bundle(destination, root=root)


def verify_pointer(pointer, root=ROOT):
    if pointer.get("kind") == "python":
        actual = verify_bundle(pointer["releaseDirectory"], root=root)
        for key in ("appVersion", "commit", "artifactSHA256", "pythonExecutable"):
            if actual[key] != pointer[key]:
                raise ValueError("release pointer identity mismatch: " + key)
    else:
        # Old Go binaries are accepted only as recorded rollback artifacts.
        for name, hash_name in (("binary", "artifactSHA256"), ("zoneInfo", "zoneInfoSHA256")):
            path = inside(pointer[name], root / "runtime/releases")
            if digest(path) != pointer[hash_name]:
                raise ValueError("rollback artifact hash mismatch: " + name)
    return pointer


def request(path="/readyz", method="GET"):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(
            urllib.request.Request("http://127.0.0.1:34115" + path, method=method), timeout=3
        ) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        if error.code == 503:
            return json.load(error)
        raise


def listening():
    with socket.socket() as connection:
        return connection.connect_ex(("127.0.0.1", 34115)) == 0


def assert_identity(pointer, state):
    for key in ("appVersion", "commit", "artifactSHA256", "mainSchemaVersion", "minuteSchemaVersion"):
        if state.get(key) != pointer[key]:
            raise RuntimeError("running service does not match release pointer: " + key)
    if state.get("dirty") or not state.get("readiness", {}).get("ready"):
        raise RuntimeError("running release is not ready or is dirty")
    if pointer.get("kind") == "python":
        if Path(state.get("pythonExecutable", "")).resolve() != Path(pointer["pythonExecutable"]).resolve():
            raise RuntimeError("running Python executable mismatch")
        if Path(state.get("releaseDirectory", "")).resolve() != Path(pointer["releaseDirectory"]).resolve():
            raise RuntimeError("running release directory mismatch")


def stop(pointer):
    if not listening():
        return
    assert_identity(pointer, request())
    request("/api/v1/system/shutdown", "POST")
    deadline = time.monotonic() + 30
    while listening() and time.monotonic() < deadline:
        time.sleep(0.2)
    if listening():
        raise RuntimeError("verified service did not stop; database files remain untouched")


def start(pointer, root=ROOT, *, scheduler=True):
    verify_pointer(pointer, root)
    if listening():
        state = request()
        assert_identity(pointer, state)
        return state
    log_dir = root / "runtime/logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    label = datetime.now().strftime("%Y%m%d-%H%M%S-%f")
    args = (
        [pointer["pythonExecutable"], "-m", "stock_god", "serve"]
        if pointer.get("kind") == "python"
        else [pointer["binary"]]
    )
    with (
        (log_dir / (label + ".out.log")).open("wb") as out,
        (log_dir / (label + ".err.log")).open("wb") as err,
    ):
        child = subprocess.Popen(
            args,
            cwd=root,
            env=release_env(pointer, root, scheduler=scheduler),
            stdin=subprocess.DEVNULL,
            stdout=out,
            stderr=err,
            creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
        )
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        if child.poll() is not None:
            raise RuntimeError("release process exited; inspect runtime/logs/" + label + ".err.log")
        try:
            state = request()
            assert_identity(pointer, state)
            write(root / "runtime/web-process.json", {"launcherPID": child.pid, "ready": state})
            return state
        except (OSError, ValueError, RuntimeError):
            time.sleep(0.25)
    raise RuntimeError("release did not become ready; inspect runtime/logs/" + label + ".err.log")


def validate_proof(path, pointer):
    proof = read(path)
    for key in ("commit", "artifactSHA256"):
        if proof.get(key) != pointer[key]:
            raise ValueError("acceptance belongs to a different candidate: " + key)
    required = {"local-release-gate", "offline-cold", "offline-restart", "live-prediction"}
    stages = proof.get("stages", {})
    if set(stages) != required or any(stage.get("passed") is not True for stage in stages.values()):
        raise ValueError("candidate acceptance is incomplete")
    if any(not stage.get("evidenceSHA256") for stage in stages.values()):
        raise ValueError("acceptance must identify its validation evidence")
    return proof


def database_command(pointer, root, command):
    return run(
        [pointer["pythonExecutable"], "-m", "stock_god", "db", command],
        cwd=root,
        env=release_env(pointer, root, scheduler=False),
        capture=True,
    )


def restore(receipt, root):
    before = receipt["before"]
    directory = inside(receipt["directory"], root / "runtime/deployments")
    if listening():
        stop(receipt["candidate"])
    restore_dir = directory / ("restore-" + datetime.now().strftime("%Y%m%d-%H%M%S-%f"))
    restore_dir.mkdir()
    for name in ("stock.db", "minute.db"):
        source = inside(directory / "backup" / name, directory)
        expected = receipt["backups"][name]["sha256"]
        if digest(source) != expected:
            raise ValueError("rollback backup changed: " + name)
        staged = restore_dir / name
        shutil.copy2(source, staged)
        verify_database(staged)
    # Preserve failed-upgrade files before putting verified backup copies in place.
    for name in ("stock.db", "minute.db"):
        destination = inside(root / "data" / name, root / "data")
        for suffix in ("", "-wal", "-shm"):
            current = inside(str(destination) + suffix, root / "data")
            if current.exists():
                os.replace(current, restore_dir / ("failed-" + name + suffix))
        os.replace(restore_dir / name, destination)
    write(root / "runtime/current.json", before)
    state = start(before, root)
    receipt.update(status="rolled_back", rolledBackAt=timestamp(), rollbackReady=state)
    write(directory / "receipt.json", receipt)
    return receipt


def deploy(directory, proof_path, root):
    candidate = verify_bundle(directory, root=root)
    proof = validate_proof(proof_path, candidate)
    before = verify_pointer(read(root / "runtime/current.json"), root)
    stamp = datetime.now().strftime("%Y%m%d-%H%M%S-%f")
    deployment = root / "runtime/deployments" / stamp
    deployment.mkdir(parents=True)
    receipt = {
        "directory": str(deployment),
        "before": before,
        "candidate": candidate,
        "acceptance": proof,
        "status": "prepared",
        "createdAt": timestamp(),
        "backups": {},
    }
    receipt_path = deployment / "receipt.json"
    write(receipt_path, receipt)
    stop(before)
    try:
        for name in ("stock.db", "minute.db"):
            receipt["backups"][name] = backup_database(root / "data" / name, deployment / "backup" / name)
        receipt["status"] = "backed_up"
        write(receipt_path, receipt)
        receipt["migration"] = json.loads(database_command(candidate, root, "migrate"))
        receipt["verification"] = json.loads(database_command(candidate, root, "verify"))
        receipt["status"] = "migrated"
        write(receipt_path, receipt)
        start(candidate, root, scheduler=False)
        stop(candidate)
        receipt["ready"] = start(candidate, root, scheduler=True)
        candidate["deployedAt"] = timestamp()
        write(root / "runtime/current.json", candidate)
        receipt.update(status="deployed", completedAt=timestamp())
        write(receipt_path, receipt)
        return receipt
    except BaseException as error:
        receipt["failure"] = str(error)
        write(receipt_path, receipt)
        if len(receipt["backups"]) == 2:
            restore(receipt, root)
        else:
            start(before, root)
        raise


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=ROOT)
    commands = parser.add_subparsers(dest="command", required=True)
    builder = commands.add_parser("build")
    builder.add_argument("--uv", type=Path, required=True)
    inspector = commands.add_parser("inspect")
    inspector.add_argument("--candidate", type=Path, required=True)
    deployer = commands.add_parser("deploy")
    deployer.add_argument("--candidate", type=Path, required=True)
    deployer.add_argument("--proof", type=Path, required=True)
    rollback = commands.add_parser("rollback")
    rollback.add_argument("--receipt", type=Path, required=True)
    for name in ("start", "stop", "restart", "status"):
        commands.add_parser(name)
    args = parser.parse_args(argv)
    root = args.root.resolve()
    with process_lock(root / "runtime/deployments/release.lock"):
        if args.command == "build":
            result = build(root, args.uv.resolve())
        elif args.command == "inspect":
            result = verify_bundle(args.candidate, root=root)
        elif args.command == "deploy":
            result = deploy(args.candidate, args.proof, root)
        elif args.command == "rollback":
            path = inside(args.receipt, root / "runtime/deployments")
            result = restore(read(path), root)
        else:
            pointer = verify_pointer(read(root / "runtime/current.json"), root)
            if args.command in {"stop", "restart"}:
                stop(pointer)
            if args.command in {"start", "restart"}:
                result = start(pointer, root)
            elif args.command == "status" and listening():
                result = request()
                assert_identity(pointer, result)
            else:
                result = {"running": False}
        print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
