import hashlib
import io
import json
import sqlite3
import zipfile
from datetime import UTC, datetime

import pytest

from stock_god.ai import Completion
from stock_god.audit import AuditConflict, AuditStore, _decode, redact_text


def test_immutable_audit_round_trip_and_redaction(core_database):
    store = AuditStore(core_database)
    store.begin("run")
    cutoff = datetime(2026, 9, 25, 1, 30, tzinfo=UTC)
    prompt = "Authorization: Bearer secret-token\nhttps://example.invalid?q=ok&api_key=url-secret"
    store.record(
        "run",
        "prediction",
        1,
        prompt,
        {"token": "evidence-secret", "price": 10},
        Completion("OK", "response", "model", "provider", []),
        [],
        cutoff,
        "template",
    )
    store.complete("run")
    view = store.detail("run")
    text = json.dumps(view)
    for secret in ("secret-token", "url-secret", "evidence-secret"):
        assert secret not in text
    assert view["state"]["status"] == "complete"
    assert view["payloads"][0]["rawResponse"] == "OK"
    assert (
        view["payloads"][0]["finalPromptSha256"]
        == hashlib.sha256(view["payloads"][0]["finalPrompt"].encode()).hexdigest()
    )
    with pytest.raises(AuditConflict):
        store.record("run", "prediction", 2, "changed", {})
    with pytest.raises(sqlite3.IntegrityError):
        with core_database.transaction() as con:
            con.execute("UPDATE research_audit_payloads SET phase='changed'")
    with zipfile.ZipFile(io.BytesIO(store.export("run"))) as archive:
        assert json.loads(archive.read("audit.json"))["ownerId"] == "run"


def test_template_versions_reuse_content_and_payload_hash_is_checked(core_database):
    store = AuditStore(core_database)
    for run in ("one", "two"):
        store.begin(run)
        store.record(
            run, "prediction", 1, "prompt", {}, "result", [], "2026-09-25T01:30:00Z", "same template"
        )
        store.complete(run)
    with core_database.connection() as con:
        assert con.execute("SELECT COUNT(*) FROM research_audit_prompt_versions").fetchone()[0] == 1
        row = dict(con.execute("SELECT * FROM research_audit_payloads LIMIT 1").fetchone())
    row["raw_response_sha256"] = "wrong"
    with pytest.raises(ValueError, match="hash mismatch"):
        _decode(row, "raw_response")


async def test_replay_runs_against_frozen_prompt_without_changing_source(core_database):
    store = AuditStore(core_database)
    store.begin("source")
    store.record(
        "source",
        "prediction",
        1,
        "frozen prompt",
        {"referencePrice": 10},
        "old response",
        [],
        "2026-09-25T01:30:00Z",
    )
    store.complete("source")
    before = store.detail("source")

    class Client:
        async def complete(self, *, prompt, phase):
            assert prompt == "frozen prompt"
            assert phase == "prediction"
            return Completion("new response", "response", "model", "provider", [])

    created = store.create_replay("source", 1)
    result = await store.execute_replay(created["replayId"], Client())
    assert result["status"] == "completed"
    assert result["diffSummary"]["changedCount"] == 1
    assert store.detail("source") == before
    with pytest.raises(AuditConflict):
        await store.execute_replay(created["replayId"], Client())


def test_archived_owner_has_no_runtime_entry_and_secrets_are_redacted():
    with pytest.raises(ValueError, match="inactive"):
        AuditStore(None, owner="research1")
    text, manifest = redact_text('{"nested":{"smtp_password":"abc"},"zero":0,"missing":null}')
    assert "abc" not in text
    assert manifest == {"fields": ["$.nested.smtp_password", "json.smtp_password"], "count": 2}
    assert json.loads(text)["zero"] == 0 and json.loads(text)["missing"] is None
