import argparse
import hashlib
import json
import sqlite3
import zipfile
from dataclasses import dataclass
from pathlib import Path

import httpx
import pytest

from stock_god import commands
from stock_god.ai.client import AIClient
from stock_god.config import AppConfig


@pytest.fixture
def config(tmp_path):
    main, minute = tmp_path / "stock.db", tmp_path / "minute.db"
    for path in (main, minute):
        with sqlite3.connect(path) as connection:
            connection.execute("CREATE TABLE records(id INTEGER PRIMARY KEY,value TEXT NOT NULL)")
            connection.execute("INSERT INTO records VALUES(1,'retained history')")
    return AppConfig(tmp_path, main, minute, tmp_path / "dist", tmp_path / "market", tmp_path / "index")


def parse(*arguments):
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    commands.add_arguments(subparsers)
    database = subparsers.add_parser("db")
    commands.add_db_arguments(database.add_subparsers(dest="operation", required=True))
    return parser.parse_args(arguments)


def test_archive_contains_verified_wal_history_both_databases_and_never_overwrites(config):
    source = sqlite3.connect(config.main_db, isolation_level=None)
    try:
        source.execute("PRAGMA journal_mode=WAL")
        source.execute("INSERT INTO records VALUES(2,'committed WAL row')")
        assert Path(str(config.main_db) + "-wal").is_file()
        args = parse(
            "db",
            "archive",
            "--output",
            str(config.root / "archive.zip"),
            "--source-app-version",
            "5.2.5",
            "--source-commit",
            "a" * 40,
        )
        result = commands.execute(args, config)
        with zipfile.ZipFile(args.output) as archive:
            assert sorted(archive.namelist()) == ["manifest.json", "minute.db", "stock.db"]
            manifest = json.loads(archive.read("manifest.json"))
            assert manifest["sourceCommit"] == "a" * 40
            assert manifest["tableRows"]["main"]["records"] == 2
            assert manifest["tableRows"]["minute"]["records"] == 1
            assert len(manifest["legacyTableRows"]) == 17
            for item in manifest["files"]:
                payload = archive.read(item["name"])
                assert len(payload) == item["sizeBytes"]
                assert hashlib.sha256(payload).hexdigest() == item["sha256"]
            extracted = config.root / "copy.db"
            extracted.write_bytes(archive.read("stock.db"))
        with sqlite3.connect(extracted) as copy:
            assert copy.execute("SELECT value FROM records WHERE id=2").fetchone()[0] == "committed WAL row"
        original = args.output.read_bytes()
        assert hashlib.sha256(original).hexdigest() == result["archive"]["sha256"]
        with pytest.raises(FileExistsError):
            commands.execute(args, config)
        assert args.output.read_bytes() == original
    finally:
        source.close()


def test_compact_preserves_business_rows_and_schema(config):
    with sqlite3.connect(config.main_db) as connection:
        connection.execute(
            "CREATE TRIGGER immutable_records BEFORE DELETE ON records BEGIN SELECT RAISE(ABORT,'keep'); END"
        )
        before = connection.execute("SELECT * FROM records").fetchall()
        schema = connection.execute("SELECT type,name,sql FROM sqlite_master ORDER BY name").fetchall()
    result = commands.execute(parse("db", "compact"), config)
    assert result["compact"]["quickCheck"] == "ok"
    with sqlite3.connect(config.main_db) as connection:
        assert connection.execute("SELECT * FROM records").fetchall() == before
        assert (
            connection.execute("SELECT type,name,sql FROM sqlite_master ORDER BY name").fetchall() == schema
        )


def test_archive_rejects_same_database_and_unsafe_output(config):
    from dataclasses import replace

    args = parse(
        "db",
        "archive",
        "--output",
        str(config.root / "output.zip"),
        "--source-app-version",
        "5.2.5",
        "--source-commit",
        "a" * 40,
    )
    with pytest.raises(ValueError, match="differ"):
        commands.execute(args, replace(config, minute_db=config.main_db))
    args.output = config.root / "stock.db"
    with pytest.raises(ValueError, match="zip"):
        commands.execute(args, config)


@dataclass
class Snapshot:
    config: dict
    models: list


def test_search_overrides_fingerprint_only_on_local_snapshot(config, monkeypatch):
    snapshot = Snapshot({"qgqpBId": "saved"}, [])
    monkeypatch.setattr(commands, "_snapshot", lambda config: snapshot)
    captured = {}

    class Market:
        def __init__(self, config, settings):
            captured["settings"] = settings
            self.http = self

        def json(self, url, **kwargs):
            captured.update(kwargs)
            return {"code": 0, "data": {"list": [{"code": "600000", "name": "浦发银行"}]}}

        def query_stocks(self, words, page_size=50):
            return self.json(
                "fixture",
                body={
                    "keyWord": words,
                    "pageSize": page_size,
                    "fingerprint": captured["settings"]["qgqpBId"],
                },
            )

        def close(self):
            captured["closed"] = True

    monkeypatch.setattr(commands, "MarketServices", Market)
    args = parse("search", "--words", "盈利增长", "--page-size", "20", "--qgqp-b-id", "explicit")
    result = commands.execute(args, config)
    assert captured["body"]["pageSize"] == 20
    assert captured["body"]["fingerprint"] == "explicit"
    assert snapshot.config["qgqpBId"] == "saved"
    assert captured["closed"]
    assert "600000 浦发银行" in commands.format_result(args, result)


def test_ai_resolver_selects_prediction_snapshot_and_rejects_partial_flags():
    model = {
        "ID": 7,
        "apiKey": "fixture",
        "baseUrl": "https://model.test/v1",
        "modelName": "fixture-model",
        "disabled": False,
    }
    snapshot = Snapshot({}, [model, model | {"ID": 9, "disabled": True}])
    args = parse("ai", "--stock-code", "sh600000", "--stock-name", "浦发银行", "--ai-config-id", "7")
    resolved, options = commands._resolve_ai(args, snapshot)
    resolved["apiKey"] = "changed"
    assert snapshot.models[0]["apiKey"] == "fixture"
    assert options == {"thinking": True}
    args.ai_config_id = 9
    with pytest.raises(ValueError, match="prediction-owned"):
        commands._resolve_ai(args, snapshot)
    args.base_url = "https://new.test"
    with pytest.raises(ValueError, match="together"):
        commands._resolve_ai(args, snapshot)


def test_ai_stream_emits_actual_provider_deltas_before_completion(config, monkeypatch, capsys):
    snapshot = Snapshot(
        {},
        [
            {
                "ID": 7,
                "apiKey": "fixture",
                "baseUrl": "https://model.test/v1",
                "modelName": "fixture-model",
                "apiProtocol": "chat_completions",
                "disabled": False,
            }
        ],
    )
    monkeypatch.setattr(commands, "_snapshot", lambda config: snapshot)

    class Market:
        def __init__(self, *args):
            pass

        def quote(self, code):
            return {"price": 10, "asOf": "2026-09-25T09:30:00+08:00"}

        def close(self):
            pass

    monkeypatch.setattr(commands, "MarketServices", Market)
    observed = []

    class Stream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b'data: {"choices":[{"delta":{"content":"first"}}]}\n\n'
            observed.append(capsys.readouterr().out)
            yield b'data: {"choices":[{"delta":{"content":"second"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'

    def request(req):
        body = json.loads(req.content)
        assert body["stream"]
        assert body["temperature"] == 0.4
        assert body["max_tokens"] == 100
        assert all("knowledge" not in message["content"] for message in body["messages"])
        return httpx.Response(200, stream=Stream())

    monkeypatch.setattr(
        commands,
        "AIClient",
        lambda models, max_attempts: AIClient(
            models, max_attempts=max_attempts, transport=httpx.MockTransport(request)
        ),
    )
    args = parse(
        "ai",
        "--stock-code",
        "sh600000",
        "--stock-name",
        "浦发银行",
        "--json",
        "--max-tokens",
        "100",
        "--temperature",
        ".4",
    )
    assert commands.execute(args, config) is None
    assert any('"content": "first"' in output for output in observed)
    assert '"content": "second"' in capsys.readouterr().out


def test_retired_research_cli_not_registered():
    with pytest.raises(SystemExit):
        parse("research", "run-once")
