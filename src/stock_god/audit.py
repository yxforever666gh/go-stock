"""Immutable, compressed prediction evidence and isolated model replays."""

from __future__ import annotations

import asyncio
import gzip
import hashlib
import io
import json
import re
import zipfile
from datetime import UTC, datetime
from typing import TYPE_CHECKING
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit
from uuid import uuid4

from .jsonutil import dumps

if TYPE_CHECKING:
    from .storage.db import Database


SECRET_KEY = r"(authorization|proxy[_-]?authorization|cookie|set[_-]?cookie|api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|client[_-]?secret|secret|smtp[_-]?(?:password|pass))"
SENSITIVE = re.compile(SECRET_KEY, re.I)
ASSIGNMENT = re.compile(SECRET_KEY + r"\s*([:=])\s*([^\s,;&\r\n]+)", re.I)
URL = re.compile(r"https?://[^\s<>\"']+")
AUTH = re.compile(r"(authorization|proxy-authorization)\s*:\s*(?:bearer|basic)\s+[^\s,;]+", re.I)
HEADER = re.compile(r"^(authorization|proxy-authorization|cookie|set-cookie)\s*:\s*[^\r\n]*", re.I | re.M)
QUOTED = re.compile(r'"' + SECRET_KEY + r'"\s*:\s*"[^"\\]*(?:\\.[^"\\]*)*"', re.I)


class AuditConflict(ValueError):
    pass


def now_text() -> str:
    return datetime.now(UTC).isoformat()


def redact_text(text: str) -> tuple[str, dict]:
    fields: set[str] = set()

    def walk(value, path="$"):
        if isinstance(value, dict):
            for key, child in value.items():
                if SENSITIVE.search(key.replace("-", "_")):
                    value[key] = "[REDACTED]"
                    fields.add(path + "." + key)
                else:
                    walk(child, path + "." + key)
        elif isinstance(value, list):
            for child in value:
                walk(child, path + "[]")

    try:
        decoded = json.loads(text)
        walk(decoded)
        text = dumps(decoded)
    except (ValueError, TypeError):
        pass

    def redact_url(match: re.Match[str]) -> str:
        value = urlsplit(match[0])
        pairs, changed = [], False
        for key, item in parse_qsl(value.query, keep_blank_values=True):
            if SENSITIVE.search(key):
                fields.add("query." + key.lower())
                item, changed = "[REDACTED]", True
            pairs.append((key, item))
        return urlunsplit(value._replace(query=urlencode(sorted(pairs)))) if changed else match[0]

    def replace(match, prefix, separator=":"):
        key = match[1]
        fields.add(prefix + key.lower().replace("-", "_"))
        return key + separator + " [REDACTED]"

    text = URL.sub(redact_url, text)
    text = HEADER.sub(lambda match: replace(match, "header."), text)
    text = AUTH.sub(lambda match: replace(match, "header."), text)

    def quoted(match):
        fields.add("json." + match[1].lower().replace("-", "_"))
        return '"' + match[1] + '":"[REDACTED]"'

    text = QUOTED.sub(quoted, text)
    text = ASSIGNMENT.sub(lambda match: replace(match, "text.", match[2]), text)
    return text, {"fields": sorted(fields), "count": len(fields)}


def _bundle(text: str) -> tuple[bytes, str]:
    raw = text.encode("utf-8")
    return gzip.compress(raw, compresslevel=9, mtime=0), hashlib.sha256(raw).hexdigest()


def _decode(row: dict, name: str) -> str:
    blob = row.get(name + "_blob")
    if blob is None:
        return ""
    if row.get(name + "_codec") != "gzip":
        raise ValueError("unsupported audit codec")
    raw = gzip.decompress(blob)
    if hashlib.sha256(raw).hexdigest() != row.get(name + "_sha256"):
        raise ValueError("audit payload hash mismatch")
    return raw.decode("utf-8")


def _camel(key: str) -> str:
    first, *rest = key.split("_")
    return first + "".join(part.capitalize() for part in rest)


class AuditStore:
    def __init__(self, database: Database, *, owner: str = "research2"):
        if owner not in {"research2", "replay"}:
            raise ValueError("inactive audit owner")
        self.database, self.owner = database, owner

    @staticmethod
    def prepare_prompt(prompt: str) -> str:
        return redact_text(prompt)[0]

    def begin(self, owner_id: str):
        if not owner_id.strip():
            raise ValueError("audit owner ID is required")
        now = now_text()
        with self.database.transaction() as con:
            con.execute(
                "INSERT INTO research_audit_run_states "
                "(owner_type,owner_id,status,payload_count,created_at,updated_at) VALUES (?,?,?,0,?,?) "
                "ON CONFLICT DO NOTHING",
                (self.owner, owner_id, "capturing", now, now),
            )

    def complete(self, owner_id: str):
        self._finish(owner_id, "complete", None)

    def fail(self, owner_id: str, error):
        self._finish(owner_id, "failed", redact_text(str(error))[0])

    def _finish(self, owner_id, status, error):
        with self.database.transaction() as con:
            result = con.execute(
                "UPDATE research_audit_run_states SET status=?,last_error=?,updated_at=? "
                "WHERE owner_type=? AND owner_id=? AND status='capturing'",
                (status, error, now_text(), self.owner, owner_id),
            )
            if not result.rowcount:
                row = con.execute(
                    "SELECT status FROM research_audit_run_states WHERE owner_type=? AND owner_id=?",
                    (self.owner, owner_id),
                ).fetchone()
                if row is None or row["status"] != status:
                    raise AuditConflict("audit state is immutable")

    def record(
        self,
        owner_id: str,
        phase: str,
        call_sequence: int,
        prompt: str,
        evidence,
        result=None,
        attempts=None,
        cutoff=None,
        template: str = "",
        *,
        repaired=False,
        error=None,
    ):
        if not phase or call_sequence < 1:
            raise ValueError("invalid audit call")
        prompt, prompt_redactions = redact_text(prompt)
        evidence_text, evidence_redactions = redact_text(
            evidence if isinstance(evidence, str) else dumps(evidence)
        )
        template, _ = redact_text(template.strip() or prompt)
        template_blob, template_hash = _bundle(template)
        prompt_blob, prompt_hash = _bundle(prompt)
        evidence_blob, evidence_hash = _bundle(evidence_text)
        records = attempts or [{}]
        version = "2.3.0-" + template_hash[:12]
        cutoff_text = cutoff.isoformat() if isinstance(cutoff, datetime) else cutoff
        with self.database.transaction() as con:
            state = con.execute(
                "SELECT status FROM research_audit_run_states WHERE owner_type=? AND owner_id=?",
                (self.owner, owner_id),
            ).fetchone()
            if state is None or state["status"] != "capturing":
                raise AuditConflict("audit payload is immutable")
            version_id = None
            if self.owner != "replay":
                existing = con.execute(
                    "SELECT prompt_version_id,template_sha256 FROM research_audit_prompt_versions "
                    "WHERE research_scope=? AND phase=? AND version=?",
                    (self.owner, phase, version),
                ).fetchone()
                if existing:
                    if existing["template_sha256"] != template_hash:
                        raise AuditConflict("prompt version content changed")
                    version_id = existing["prompt_version_id"]
                else:
                    version_id = str(uuid4())
                    con.execute(
                        "INSERT INTO research_audit_prompt_versions "
                        "(prompt_version_id,research_scope,phase,version,template_codec,template_blob,"
                        "template_sha256,created_at) VALUES (?,?,?,?,?,?,?,?)",
                        (
                            version_id,
                            self.owner,
                            phase,
                            version,
                            "gzip",
                            template_blob,
                            template_hash,
                            now_text(),
                        ),
                    )
            for index, attempt in enumerate(records, 1):
                content = getattr(result, "content", result if isinstance(result, str) else "") or ""
                if (
                    attempt
                    and attempt.get("status") != "success"
                    and not (index == len(records) and not error)
                ):
                    content = ""
                content, response_redactions = redact_text(content)
                log_text, log_redactions = redact_text(
                    dumps(attempt) if attempt else "no provider attempt was emitted"
                )
                if error and index == len(records):
                    log_text += "\nerror=" + redact_text(str(error))[0]
                fields = sorted(
                    set(
                        prompt_redactions["fields"]
                        + evidence_redactions["fields"]
                        + response_redactions["fields"]
                        + log_redactions["fields"]
                    )
                )
                parameters = {"attempt": attempt} if attempt else {}
                if attempt.get("configId"):
                    parameters["actualConfigId"] = attempt["configId"]
                values = {
                    "payload_id": str(uuid4()),
                    "owner_type": self.owner,
                    "owner_id": owner_id,
                    "prompt_version_id": version_id,
                    "phase": phase,
                    "call_sequence": call_sequence,
                    "attempt": index,
                    "provider_name": attempt.get("providerName")
                    or getattr(result, "provider_name", "")
                    or "unavailable",
                    "model_name": attempt.get("modelName") or getattr(result, "model", "") or "unavailable",
                    "model_parameters_json": redact_text(dumps(parameters))[0],
                    "cutoff_at": cutoff_text,
                    "final_prompt_codec": "gzip",
                    "final_prompt_blob": prompt_blob,
                    "final_prompt_sha256": prompt_hash,
                    "evidence_codec": "gzip",
                    "evidence_blob": evidence_blob,
                    "evidence_sha256": evidence_hash,
                    "tools_json": "[]",
                    "redaction_manifest_json": dumps({"fields": fields, "count": len(fields)}),
                    "created_at": now_text(),
                }
                for name, text in (
                    ("repaired_response" if repaired else "raw_response", content),
                    ("repair_log", log_text),
                ):
                    if text:
                        blob, digest = _bundle(text)
                        values.update(
                            {name + "_codec": "gzip", name + "_blob": blob, name + "_sha256": digest}
                        )
                con.execute(
                    f"INSERT INTO research_audit_payloads ({','.join(values)}) VALUES "
                    f"({','.join('?' for _ in values)})",
                    tuple(values.values()),
                )
            con.execute(
                "UPDATE research_audit_run_states SET payload_count=payload_count+?,updated_at=? "
                "WHERE owner_type=? AND owner_id=?",
                (len(records), now_text(), self.owner, owner_id),
            )

    def detail(self, owner_id: str) -> dict:
        with self.database.connection() as con:
            state = con.execute(
                "SELECT * FROM research_audit_run_states WHERE owner_type=? AND owner_id=?",
                (self.owner, owner_id),
            ).fetchone()
            rows = con.execute(
                "SELECT * FROM research_audit_payloads WHERE owner_type=? AND owner_id=? "
                "ORDER BY call_sequence,attempt",
                (self.owner, owner_id),
            ).fetchall()
        result = {
            "availability": "available" if state else "legacy_unavailable",
            "ownerType": self.owner,
            "ownerId": owner_id,
            "state": {"status": "legacy_unavailable"},
            "payloads": [],
        }
        if state:
            result["state"] = {_camel(key): value for key, value in dict(state).items()}
        cutoff_values = []
        for record in rows:
            row = dict(record)
            payload = {
                _camel(key): value
                for key, value in row.items()
                if not key.endswith(("_blob", "_codec", "_json"))
            }
            payload.update(
                modelParameters=json.loads(row["model_parameters_json"]),
                finalPrompt=_decode(row, "final_prompt"),
                evidenceSnapshot=_decode(row, "evidence"),
                tools=json.loads(row["tools_json"]),
                rawResponse=_decode(row, "raw_response"),
                repairedResponse=_decode(row, "repaired_response"),
                repairLog=_decode(row, "repair_log"),
                redactionCount=json.loads(row["redaction_manifest_json"]).get("count", 0),
            )
            result["payloads"].append(payload)
            if row.get("cutoff_at"):
                cutoff_values.append(row["cutoff_at"])
        if cutoff_values:
            result["cutoffAt"] = min(cutoff_values)
        return result

    def export(self, owner_id: str) -> bytes:
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            info = zipfile.ZipInfo("audit.json", date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, dumps(self.detail(owner_id)).encode("utf-8"))
        return output.getvalue()

    def create_replay(self, owner_id: str, model_id: int) -> dict:
        source = self.detail(owner_id)
        if source["state"]["status"] != "complete" or not source["payloads"] or not source.get("cutoffAt"):
            raise ValueError("source audit is not complete or lacks replay evidence")
        if model_id < 1:
            raise ValueError("invalid replay model")
        replay_id = str(uuid4())
        with self.database.transaction() as con:
            con.execute(
                "INSERT INTO research_replays "
                "(replay_id,source_owner_type,source_owner_id,model_config_id,status,cutoff_at,created_at) "
                "VALUES (?,?,?,?,?,?,?)",
                (replay_id, "research2", owner_id, model_id, "queued", source["cutoffAt"], now_text()),
            )
        AuditStore(self.database, owner="replay").begin(replay_id)
        return self.get_replay(replay_id)

    def recover_replays(self):
        reason = "服务重启时回放尚未完成，请重新发起回放"
        with self.database.transaction() as con:
            rows = con.execute(
                "SELECT replay_id FROM research_replays WHERE source_owner_type='research2' "
                "AND status IN ('queued','running')"
            ).fetchall()
            for row in rows:
                con.execute(
                    "UPDATE research_replays SET status='failed',completed_at=?,last_error=? WHERE replay_id=?",
                    (now_text(), reason, row[0]),
                )
                con.execute(
                    "UPDATE research_audit_run_states SET status='failed',last_error=?,updated_at=? "
                    "WHERE owner_type='replay' AND owner_id=? AND status='capturing'",
                    (reason, now_text(), row[0]),
                )

    def get_replay(self, replay_id: str) -> dict:
        with self.database.connection() as con:
            row = con.execute(
                "SELECT * FROM research_replays WHERE replay_id=? AND source_owner_type='research2'",
                (replay_id,),
            ).fetchone()
            if row is None:
                raise KeyError("prediction replay not found")
            result_row = con.execute(
                "SELECT * FROM research_replay_results WHERE replay_id=?", (replay_id,)
            ).fetchone()
        result = {_camel(key): value for key, value in dict(row).items()}
        result.update(result="", resultSha256="", diffSummary={})
        if result_row:
            result.update(
                result=_decode(dict(result_row), "result"),
                resultSha256=result_row["result_sha256"],
                diffSummary=json.loads(result_row["diff_summary_json"]),
            )
        return result

    async def execute_replay(self, replay_id: str, client):
        row = self.get_replay(replay_id)
        with self.database.transaction() as con:
            if (
                con.execute(
                    "UPDATE research_replays SET status='running',started_at=? WHERE replay_id=? AND status='queued'",
                    (now_text(), replay_id),
                ).rowcount
                != 1
            ):
                raise AuditConflict("replay is no longer queued")
        recorder = AuditStore(self.database, owner="replay")
        outputs, diffs = [], []
        try:
            source = self.detail(row["sourceOwnerId"])
            for index, payload in enumerate(source["payloads"], 1):
                completion = await client.complete(prompt=payload["finalPrompt"], phase=payload["phase"])
                recorder.record(
                    replay_id,
                    payload["phase"],
                    index,
                    payload["finalPrompt"],
                    payload["evidenceSnapshot"],
                    completion,
                    completion.attempts,
                    row["cutoffAt"],
                )
                text = redact_text(completion.content)[0]
                _, digest = _bundle(text)
                original = payload.get("rawResponseSha256") or payload.get("repairedResponseSha256") or ""
                outputs.append(
                    {"callSequence": index, "phase": payload["phase"], "content": text, "sha256": digest}
                )
                diffs.append(
                    {
                        "callSequence": index,
                        "sourceSha256": original,
                        "replaySha256": digest,
                        "changed": digest != original,
                    }
                )
            blob, digest = _bundle(dumps(outputs))
            with self.database.transaction() as con:
                con.execute(
                    "INSERT INTO research_replay_results "
                    "(replay_result_id,replay_id,result_codec,result_blob,result_sha256,diff_summary_json,created_at) "
                    "VALUES (?,?,?,?,?,?,?)",
                    (
                        str(uuid4()),
                        replay_id,
                        "gzip",
                        blob,
                        digest,
                        dumps({"calls": diffs, "changedCount": sum(item["changed"] for item in diffs)}),
                        now_text(),
                    ),
                )
                con.execute(
                    "UPDATE research_replays SET status='completed',completed_at=? WHERE replay_id=?",
                    (now_text(), replay_id),
                )
            recorder.complete(replay_id)
        except (Exception, asyncio.CancelledError) as error:
            message = redact_text(str(error))[0] or "回放已取消"
            with self.database.transaction() as con:
                con.execute(
                    "UPDATE research_replays SET status='failed',completed_at=?,last_error=? WHERE replay_id=?",
                    (now_text(), message, replay_id),
                )
            recorder.fail(replay_id, message)
            raise
        return self.get_replay(replay_id)
