"""Durable evidence ownership and terminal freezing, independent of provider I/O."""

from __future__ import annotations

import copy
import hashlib
import json
import re
from datetime import UTC, datetime
from uuid import uuid4

from stock_god.jsonutil import dumps


def _time(value):
    if isinstance(value, datetime):
        return value
    if not value:
        return None
    result = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    return result if result.tzinfo else result.replace(tzinfo=UTC)


def _utc(value) -> str:
    stamp = _time(value)
    if stamp is None:
        return ""
    text = stamp.astimezone(UTC).isoformat()
    if "." in text:
        prefix, fraction = text.split(".", 1)
        digits = fraction.split("+", 1)[0].rstrip("0")
        return prefix + ("." + digits if digits else "") + "Z"
    return text.replace("+00:00", "Z")


def _hash(parts) -> str:
    return hashlib.sha256("\x1f".join(parts).encode()).hexdigest()


def _status(document, cutoff):
    available = _time(document.get("availableAt"))
    if available is None:
        return "unavailable"
    if available > cutoff:
        return "after_cutoff"
    if str(document.get("error") or "").strip():
        return "unavailable"
    try:
        payload = json.loads(document.get("content") or "null")
    except (TypeError, ValueError):
        payload = None
    if isinstance(payload, dict):
        status = str(payload.get("status") or "").lower().strip()
        if status in {"stale", "partial"}:
            return status
        if status == "empty":
            return "ok"
        if status in {"failed", "unavailable", "after_cutoff"}:
            return "unavailable"
        for key in ("code", "rc"):
            if key in payload and str(payload[key]).strip() not in {"", "0", "0.0", "200"}:
                return "unavailable"
        if payload.get("success") is False:
            return "unavailable"
    return "ok"


class EvidenceStore:
    def __init__(self, database, *, clock=None):
        self.database = database
        self.clock = clock or (lambda: datetime.now(UTC))

    def begin(self, run_id: str, cutoff) -> str:
        cutoff = _time(cutoff)
        if not run_id.strip() or cutoff is None:
            raise ValueError("evidence run and cutoff are required")
        ident = str(uuid4())
        with self.database.transaction() as con:
            con.execute(
                "INSERT INTO research_evidence_sets "
                "(evidence_set_id,owner_type,owner_id,cutoff_at,collector_version,evidence_profile_version,"
                "status,content_hash,created_at) VALUES (?,?,?,?,?,?,?,?,?)",
                (
                    ident,
                    "research2",
                    run_id,
                    cutoff.isoformat(),
                    "2.0",
                    "research2-slots-v8",
                    "collecting",
                    hashlib.sha256(b"").hexdigest(),
                    self.clock().isoformat(),
                ),
            )
        return ident

    def capture(self, run_id: str, raw_evidence: dict, error=None) -> dict:
        evidence = copy.deepcopy(raw_evidence)
        cutoff = _time(evidence.get("freezeAt") or evidence.get("cutoffAt"))
        if cutoff is None:
            raise ValueError("evidence cutoff is required")
        ident = evidence.get("evidenceSetId") or self.begin(run_id, cutoff)
        profile = evidence.get("evidenceProfileVersion") or "research2-slots-v8"
        evidence.update(evidenceSetId=ident, evidenceProfileVersion=profile, freezeAt=cutoff.isoformat())
        now = max(self.clock().astimezone(UTC), cutoff.astimezone(UTC))
        items, used = [], {}
        for index, document in enumerate(evidence.get("documents") or []):
            content, failure = str(document.get("content") or ""), str(document.get("error") or "").strip()
            source = str(document.get("sourceId") or "").strip()
            if not source:
                source = (
                    "source-"
                    + _hash(
                        [str(document.get(key) or "") for key in ("category", "sourceName", "sourceRef")]
                    )[:16]
                )
            used[source] = used.get(source, 0) + 1
            source_id = source if used[source] == 1 else f"{source}-{used[source]}-{index + 1}"
            name, category = (
                str(document.get("sourceName") or "unknown"),
                str(document.get("category") or "unknown"),
            )
            summary = name + " / " + category
            if document.get("sourceId"):
                summary += " / sourceId=" + document["sourceId"]
            summary += " / " + ("错误: " + failure if failure else content.strip() or "无可用内容")
            if len(summary.encode()) > 512:
                summary = summary.encode()[:512].decode("utf-8", "ignore") + "…"
            entity = re.search(r"sh[0-9]{6}", (source + " " + name).lower())
            entity = entity or re.search(r"sz[0-9]{6}", (source + " " + name).lower())
            payload = dumps({"content": content, "error": failure})
            item = {
                "evidence_item_id": str(uuid4()),
                "evidence_set_id": ident,
                "source_id": source_id,
                "source_name": name,
                "source_ref": str(document.get("sourceRef") or ""),
                "category": category,
                "entity_type": "stock" if entity else "",
                "entity_id": entity[0] if entity else "",
                "event_at": None,
                "available_at": _utc(document.get("availableAt")) or None,
                "collected_at": _utc(document.get("collectedAt")) or now.isoformat(),
                "status": _status(document, cutoff),
                "payload": payload.encode(),
                "payload_encoding": "identity",
                "summary": summary,
                "error_message": failure,
                "created_at": now.isoformat(),
            }
            item["content_hash"] = _hash(
                [
                    item[key] or ""
                    for key in (
                        "source_name",
                        "source_ref",
                        "category",
                        "entity_type",
                        "entity_id",
                        "event_at",
                        "available_at",
                        "status",
                        "payload_encoding",
                    )
                ]
                + [payload, summary, failure]
            )
            items.append(item)
        ordered = sorted(
            items,
            key=lambda item: "\x1f".join(
                item[key]
                for key in (
                    "category",
                    "source_name",
                    "source_ref",
                    "entity_type",
                    "entity_id",
                    "content_hash",
                    "status",
                )
            ),
        )
        digest = _hash(
            [_utc(cutoff), "2.0", profile]
            + [value for item in ordered for value in (item["content_hash"], item["status"])]
        )
        with self.database.transaction() as con:
            batch = con.execute(
                "SELECT * FROM research_evidence_sets WHERE evidence_set_id=?", (ident,)
            ).fetchone()
            if batch is None or batch["owner_id"] != run_id or batch["owner_type"] != "research2":
                raise ValueError("evidence batch does not belong to this prediction")
            if batch["frozen_at"] is not None:
                if batch["content_hash"] != digest:
                    raise ValueError("evidence batch is already frozen with different content")
                return evidence
            for item in items:
                con.execute(
                    f"INSERT INTO research_evidence_items ({','.join(item)}) VALUES "
                    f"({','.join('?' for _ in item)})",
                    tuple(item.values()),
                )
            con.execute(
                "UPDATE research_evidence_sets SET cutoff_at=?,evidence_profile_version=?,status=?,"
                "content_hash=?,frozen_at=? WHERE evidence_set_id=? AND frozen_at IS NULL",
                (cutoff.isoformat(), profile, "frozen", digest, now.isoformat(), ident),
            )
        return evidence
