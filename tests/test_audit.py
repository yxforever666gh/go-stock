import gzip
import hashlib
import io
import json
import sqlite3
import zipfile
from datetime import UTC, datetime

import pytest

from stock_god.ai import Completion
from stock_god.audit import AuditConflict, AuditStore, _decode, _wire_time, redact_text


@pytest.mark.parametrize("blob", [None, b""])
def test_legacy_missing_optional_audit_payload_has_no_codec(blob):
    for name in ("raw_response", "repaired_response", "repair_log"):
        assert _decode({name + "_blob": blob, name + "_codec": None, name + "_sha256": None}, name) == ""


def test_nonempty_legacy_audit_payload_still_requires_supported_codec_and_hash():
    payload = gzip.compress(b"captured response")
    with pytest.raises(ValueError, match="unsupported audit codec"):
        _decode({"raw_response_blob": payload, "raw_response_codec": None}, "raw_response")
    with pytest.raises(ValueError, match="hash mismatch"):
        _decode(
            {"raw_response_blob": payload, "raw_response_codec": "gzip", "raw_response_sha256": "wrong"},
            "raw_response",
        )


def test_audit_wire_times_preserve_stored_nanoseconds_and_use_rfc3339():
    assert _wire_time("2026-09-24 09:50:00.123456789+08:00") == "2026-09-24T09:50:00.123456789+08:00"
    assert _wire_time(None) is None


def test_audit_optional_hashes_and_legacy_state_match_nonnullable_contract(core_database):
    store = AuditStore(core_database)
    missing = store.detail("before-audit-existed")
    assert missing["state"] == {
        "status": "legacy_unavailable",
        "payloadCount": 0,
        "lastError": "",
        "createdAt": "0001-01-01T00:00:00Z",
        "updatedAt": "0001-01-01T00:00:00Z",
    }
    store.begin("optional")
    store.record("optional", "prediction", 1, "prompt", {}, None, [], "2026-09-24T09:50:00+08:00")
    value = store.detail("optional")
    assert value["state"]["lastError"] == ""
    assert value["payloads"][0]["rawResponseSha256"] == ""
    assert value["payloads"][0]["repairedResponseSha256"] == ""


def test_restart_recovers_queued_and_running_replays_through_published_state_transitions(core_database):
    store = AuditStore(core_database)
    store.begin("source")
    store.record("source", "prediction", 1, "prompt", {}, "response", [], "2026-09-24T09:50:00+08:00")
    store.complete("source")
    queued = store.create_replay("source", 1)["replayId"]
    running = store.create_replay("source", 1)["replayId"]
    started = "2026-09-24T02:00:00Z"
    with core_database.transaction() as connection:
        connection.execute(
            "UPDATE research_replays SET status='running',started_at=? WHERE replay_id=?", (started, running)
        )
    with core_database.connection() as connection:
        source_before = dict(
            connection.execute(
                "SELECT * FROM research_audit_run_states WHERE owner_type='research2' AND owner_id='source'"
            ).fetchone()
        )
        trigger_before = connection.execute(
            "SELECT sql FROM sqlite_master WHERE name='identity_research_replays_update'"
        ).fetchone()[0]
    store.recover_replays()
    first = [store.get_replay(identity) for identity in (queued, running)]
    assert all(row["status"] == "failed" and row["startedAt"] and row["completedAt"] for row in first)
    assert first[1]["startedAt"] == started
    assert all(
        AuditStore(core_database, owner="replay").detail(identity)["state"]["status"] == "failed"
        for identity in (queued, running)
    )
    store.recover_replays()
    assert [store.get_replay(identity) for identity in (queued, running)] == first
    with core_database.connection() as connection:
        assert (
            dict(
                connection.execute(
                    "SELECT * FROM research_audit_run_states WHERE owner_type='research2' AND owner_id='source'"
                ).fetchone()
            )
            == source_before
        )
        assert (
            connection.execute(
                "SELECT sql FROM sqlite_master WHERE name='identity_research_replays_update'"
            ).fetchone()[0]
            == trigger_before
        )


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
