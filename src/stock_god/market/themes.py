"""Immutable daily themes, catalyst provenance and one-shot lifecycle collection."""

import base64
from concurrent.futures import ThreadPoolExecutor
from datetime import timezone
import hashlib
import json
import sqlite3
import unicodedata
from uuid import uuid4

from .common import MarketDataError, database_rows, envelope, now, number, require_date, timestamp

STAGES = ("观察", "发酵", "加速", "分歧", "退潮")


def normalize(text):
    return "".join(char for char in text.strip().lower() if not char.isspace() and unicodedata.category(char)[0] not in "PS")


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


def camel(row):
    def key(value):
        first, *rest = value.split("_")
        return first+"".join(part.title() for part in rest)
    return {key(name): value for name, value in row.items() if name != "id"}


def next_stage(previous, heat, rank, source_count, conflict):
    if not previous:
        return 1, "观察"
    cycle, stage = previous["cycle_no"], previous["lifecycle_stage"]
    if stage == "观察" and source_count >= 2 and heat >= 50:
        stage = "发酵"
    elif stage == "发酵" and heat >= 75 and rank <= 10:
        stage = "加速"
    elif stage == "加速" and (conflict or previous["heat_score"]-heat >= 15):
        stage = "分歧"
    elif stage == "分歧" and heat < 40:
        stage = "退潮"
    elif stage == "退潮" and heat >= 50:
        cycle, stage = cycle+1, "观察"
    return cycle, stage


def insert(connection, table, row):
    names = list(row)
    connection.execute(f"INSERT INTO {table} ({','.join(names)}) VALUES ({','.join('?' for _ in names)})", tuple(row.values()))


class Themes:
    def _theme_identity(self, value):
        rows = database_rows(self.config.main_db,
            "SELECT * FROM market_themes WHERE theme_id=? OR normalized_name=? OR theme_id IN (SELECT theme_id FROM market_theme_aliases WHERE normalized_alias=?) LIMIT 1",
            (value, normalize(value), normalize(value)))
        if not rows:
            raise LookupError("theme not found")
        return rows[0]

    def _theme_snapshot(self, theme_id, day="", cutoff=None):
        condition, values = "theme_id=?", [theme_id]
        if day:
            condition += " AND trade_date=?"
            values.append(day)
        if cutoff:
            condition += " AND julianday(frozen_at)<=julianday(?)"
            values.append(timestamp(cutoff).isoformat())
        rows = database_rows(self.config.main_db, "SELECT * FROM market_theme_daily_snapshots WHERE "+condition+" ORDER BY trade_date DESC LIMIT 1", tuple(values))
        return rows[0] if rows else None

    def _theme_claims(self, event_id, cutoff):
        rows = database_rows(self.config.main_db,
            "SELECT * FROM market_catalyst_source_claims WHERE catalyst_event_id=? AND available_at IS NOT NULL AND julianday(available_at)<=julianday(?) ORDER BY available_at,source_claim_id",
            (event_id, cutoff))
        return [camel(row) | {"evidenceItemIds": []} for row in rows]

    def _theme_catalysts(self, snapshot):
        if not snapshot:
            return []
        rows = database_rows(self.config.main_db,
            "SELECT e.* FROM market_catalyst_events e JOIN market_theme_snapshot_catalysts s ON s.catalyst_event_id=e.catalyst_event_id WHERE s.snapshot_id=? AND e.first_available_at IS NOT NULL AND julianday(e.first_available_at)<=julianday(?) ORDER BY event_at DESC,catalyst_event_id",
            (snapshot["snapshot_id"], snapshot["frozen_at"]))
        result = []
        for row in rows:
            claims = self._theme_claims(row["catalyst_event_id"], snapshot["frozen_at"])
            stances = {claim["stance"] for claim in claims}
            result.append(camel(row) | {"sources": claims, "hasConflict": {"supports", "contradicts"} <= stances})
        return result

    def theme_api(self, theme_id, mode, query):
        for key in ("date", "from", "to"):
            require_date(query.get(key, ""))
        stage = query.get("stage", "")
        if stage and stage not in STAGES:
            raise ValueError("invalid theme stage")
        limit = int(query.get("limit") or (30 if mode == "snapshots" else 20))
        if not 1 <= limit <= 100:
            raise ValueError("limit must be 1..100")
        cursor = query.get("cursor", "")
        try:
            offset = int(base64.urlsafe_b64decode(cursor+"="*((4-len(cursor)%4)%4)).decode()) if cursor else 0
            if offset < 0:
                raise ValueError()
        except (ValueError, UnicodeError) as exc:
            raise ValueError("invalid cursor") from exc
        def paged(items):
            selected = items[offset:offset+limit]
            next_value = base64.urlsafe_b64encode(str(offset+limit).encode()).decode().rstrip("=") if offset+limit < len(items) else ""
            return {"items": selected, **({"nextCursor": next_value} if next_value else {})}
        as_of = None
        if mode == "list":
            sort = query.get("sort", "rank")
            if sort not in {"rank", "heat", "stage"}:
                raise ValueError("sort must be rank, heat or stage")
            day = query.get("date")
            if not day:
                dates = database_rows(self.config.main_db, "SELECT MAX(trade_date) AS trade_date FROM market_theme_daily_snapshots")
                day = dates[0]["trade_date"] or now().date().isoformat()
            snapshots = database_rows(self.config.main_db, "SELECT s.*,t.canonical_name FROM market_theme_daily_snapshots s JOIN market_themes t ON t.theme_id=s.theme_id WHERE trade_date=?", (day,))
            items = []
            for row in snapshots:
                if stage and row["lifecycle_stage"] != stage or query.get("q", "").lower() not in row["canonical_name"].lower():
                    continue
                aliases = [value["alias"] for value in database_rows(self.config.main_db, "SELECT alias FROM market_theme_aliases WHERE theme_id=?", (row["theme_id"],))]
                previous = database_rows(self.config.main_db, "SELECT lifecycle_stage FROM market_theme_daily_snapshots WHERE theme_id=? AND trade_date<? ORDER BY trade_date DESC LIMIT 1", (row["theme_id"], day))
                constituents = database_rows(self.config.main_db, "SELECT asset_type,market,code,name,role FROM market_theme_snapshot_constituents WHERE snapshot_id=? ORDER BY rank LIMIT 3", (row["snapshot_id"],))
                items.append(camel(row) | {"name": row["canonical_name"], "aliases": aliases, "stageChanged": bool(previous and previous[0]["lifecycle_stage"] != row["lifecycle_stage"]),
                    **({"previousLifecycleStage": previous[0]["lifecycle_stage"]} if previous else {}), "representativeSecurities": [camel(item) for item in constituents]})
            items.sort(key=lambda item: (item["rank"], item["themeId"]) if sort == "rank" else (-item["heatScore"], item["themeId"]) if sort == "heat" else (STAGES.index(item["lifecycleStage"]), item["rank"]))
            data = {"tradeDate": day, **paged(items)}
            as_of = max((row["frozenAt"] for row in items), default=None)
        else:
            identity = self._theme_identity(theme_id)
            theme_id = identity["theme_id"]
            snapshot = self._theme_snapshot(theme_id, query.get("date", ""))
            as_of = snapshot["frozen_at"] if snapshot else None
            if mode == "snapshots":
                start, end = query.get("from", ""), query.get("to", "")
                if start and end and start > end:
                    raise ValueError("from must not be after to")
                rows = database_rows(self.config.main_db, "SELECT * FROM market_theme_daily_snapshots WHERE theme_id=? ORDER BY trade_date DESC", (theme_id,))
                items = [camel(row) for row in rows if (not start or row["trade_date"] >= start) and (not end or row["trade_date"] <= end) and (not stage or row["lifecycle_stage"] == stage)]
                data = {"themeId": theme_id, **paged(items)}
            elif mode == "catalysts":
                status, minimum = query.get("status", ""), int(query.get("minCredibility") or 0)
                if status not in {"", "active", "disputed", "retracted", "expired"} or not 0 <= minimum <= 100:
                    raise ValueError("invalid catalyst filter")
                items = [row for row in self._theme_catalysts(snapshot) if (not status or row["status"] == status) and row["credibilityScore"] >= minimum]
                data = {"themeId": theme_id, "tradeDate": snapshot["trade_date"] if snapshot else query.get("date", now().date().isoformat()), **paged(items)}
            else:
                aliases = [row["alias"] for row in database_rows(self.config.main_db, "SELECT alias FROM market_theme_aliases WHERE theme_id=?", (theme_id,))]
                constituents = database_rows(self.config.main_db, "SELECT * FROM market_theme_snapshot_constituents WHERE snapshot_id=? ORDER BY rank", (snapshot["snapshot_id"],)) if snapshot else []
                catalysts = self._theme_catalysts(snapshot)
                claims = [claim for event in catalysts for claim in event["sources"]]
                data = {"theme": {"themeId": theme_id, "name": identity["canonical_name"], "description": identity.get("description") or "", "status": identity["status"], "aliases": aliases},
                        "snapshot": camel(snapshot) if snapshot else None, "constituents": [camel(row) for row in constituents],
                        "catalystSummary": {"total": len(catalysts), "supports": sum(row["stance"] == "supports" for row in claims),
                            "contradicts": sum(row["stance"] == "contradicts" for row in claims), "hasConflict": any(row["hasConflict"] for row in catalysts)}}
        result = envelope(data, "theme_repository", as_of=as_of, status="empty" if mode != "detail" and not data.get("items") else "ok")
        result["evidenceProfile"] = "market-evidence-v2"
        return result

    def prediction_theme_documents(self, cutoff):
        rows = database_rows(self.config.main_db,
            "SELECT s.* FROM market_theme_daily_snapshots s WHERE julianday(frozen_at)<=julianday(?) AND NOT EXISTS (SELECT 1 FROM market_theme_daily_snapshots n WHERE n.theme_id=s.theme_id AND n.trade_date>s.trade_date AND julianday(n.frozen_at)<=julianday(?)) ORDER BY rank LIMIT 10",
            (timestamp(cutoff).isoformat(), timestamp(cutoff).isoformat()))
        documents = []
        for row in rows:
            data = self.theme_api(row["theme_id"], "detail", {"date": row["trade_date"]})
            documents.append({"sourceId": "theme:"+row["snapshot_id"], "sourceName": data["data"]["theme"]["name"], "sourceRef": "db:market_theme_daily_snapshots/"+row["snapshot_id"],
                "category": "theme", "collectedAt": now().isoformat(), "retrievedAt": now().isoformat(), "availableAt": row["frozen_at"], "publishedAt": row["frozen_at"],
                "content": json.dumps(data, ensure_ascii=False), "error": "", "stockCode": ""})
        return documents

    def collect_theme_signals(self, at):
        sources = [("hot_topic", "东方财富热门话题", lambda: self.hot_topics(30)), ("hot_event", "雪球热点事件", lambda: self.hot_events(30)),
                   ("news", "新闻/电报", lambda: self.telegraphs()), ("fund_flow", "概念资金流", lambda: self.fund_flows("concept", limit=50))]
        def collect(item):
            kind, source, call = item
            try:
                payload = call()
                if kind == "fund_flow":
                    if payload["status"] == "unavailable":
                        raise MarketDataError("concept fund flows unavailable")
                    payload = payload["data"]
                result = []
                for rank, row in enumerate(payload, 1):
                    name = next((str(row[key]).strip() for key in ("TopicName", "topicName", "tag", "name", "title") if row.get(key)), "")
                    if not name:
                        continue
                    title = row.get("title") or row.get("content") or name
                    published = row.get("publishedAt") or row.get("dataTime") or row.get("publishTime")
                    event_at = timestamp(published) if published else timestamp(at)
                    source_name = row.get("source") or source
                    strength = min(100, max(0, number(row.get("Hot", row.get("hot", row.get("heat"))), 0)))
                    if not strength:
                        strength = {"hot_topic": 45, "hot_event": 50, "news": 50, "fund_flow": 45}[kind]
                        if kind == "fund_flow":
                            strength += (number(row.get("changePct"), 0)*3)+(5 if number(row.get("netAmount"), 0)>0 else -5 if number(row.get("netAmount"), 0)<0 else 0)
                        strength = min(100, max(0, strength+max(0, 6-rank/5)))
                    result.append({"name": name, "title": title, "summary": row.get("summary") or row.get("TopicDesc") or row.get("content") or title,
                        "source": source_name, "kind": kind, "strength": strength, "at": event_at.isoformat(), "published": event_at.isoformat() if published else None,
                        "available": event_at.isoformat() if published else timestamp(at).isoformat(), "ref": row.get("url") or "urn:stock-god:theme-source:"+digest(json.dumps(row, ensure_ascii=False, sort_keys=True)),
                        "stance": "contradicts" if row.get("sentimentResult") in ("negative", "利空", "负面") else "supports", "rawHash": digest(json.dumps(row, ensure_ascii=False, sort_keys=True))})
                return result, {"source": source, "status": "ok" if result else "empty", "signalCount": len(result)}, None
            except (MarketDataError, ValueError, KeyError) as exc:
                return [], {"source": source, "status": "unavailable", "signalCount": 0}, {"source": source, "code": "unavailable", "message": str(exc)}
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(collect, sources))
        return [signal for values, _, _ in results for signal in values], [state for _, state, _ in results], [error for _, _, error in results if error]

    def refresh_themes(self, at):
        at = timestamp(at)
        signals, states, errors = self.collect_theme_signals(at)
        frozen = max(now(), at).isoformat()
        result = {"status": "unavailable", "tradeDate": at.date().isoformat(), "observedAt": at.isoformat(), "frozenAt": frozen,
                  "sourceStates": states, "sourceErrors": errors, "errors": [], "frozenSnapshotIds": [], "themes": []}
        groups = {}
        for signal in signals:
            if normalize(signal["name"]):
                groups.setdefault(normalize(signal["name"]), []).append(signal)
        ranked = []
        for key, group in groups.items():
            heat = min(100, max(row["strength"] for row in group)+min(15, (len({row["source"] for row in group})-1)*5)+min(10, (len(group)-1)*2))
            ranked.append((round(heat, 2), key, group))
        ranked.sort(key=lambda item: (-item[0], item[1]))
        for rank, (heat, key, group) in enumerate(ranked, 1):
            try:
                with sqlite3.connect(self.config.main_db, timeout=10) as connection:
                    connection.row_factory = sqlite3.Row
                    connection.execute("BEGIN IMMEDIATE")
                    identity = connection.execute("SELECT * FROM market_themes WHERE normalized_name=? OR theme_id IN (SELECT theme_id FROM market_theme_aliases WHERE normalized_alias=?) LIMIT 1", (key, key)).fetchone()
                    theme_id = identity["theme_id"] if identity else "theme-"+digest(key)[:32]
                    name = identity["canonical_name"] if identity else min(row["name"] for row in group)
                    if not identity:
                        insert(connection, "market_themes", {"theme_id": theme_id, "canonical_name": name, "normalized_name": key, "description": "", "status": "active", "created_at": frozen, "updated_at": frozen})
                    existing = connection.execute("SELECT * FROM market_theme_daily_snapshots WHERE theme_id=? AND trade_date=?", (theme_id, at.date().isoformat())).fetchone()
                    if existing:
                        snapshot = dict(existing)
                    else:
                        previous = connection.execute("SELECT * FROM market_theme_daily_snapshots WHERE theme_id=? AND trade_date<? ORDER BY trade_date DESC LIMIT 1", (theme_id, at.date().isoformat())).fetchone()
                        catalyst_ids, conflict = [], False
                        for signal in group:
                            if signal["kind"] == "fund_flow":
                                continue
                            event_time = timestamp(signal["at"]).astimezone(timezone.utc).replace(second=0, microsecond=0).isoformat().replace("+00:00", "Z")
                            event_key = digest("|".join((normalize(signal["kind"]), normalize(signal["title"]), event_time, "")))
                            event_id = "catalyst-"+digest(theme_id+"|"+event_key)[:32]
                            event = connection.execute("SELECT * FROM market_catalyst_events WHERE catalyst_event_id=?", (event_id,)).fetchone()
                            if not event:
                                insert(connection, "market_catalyst_events", {"catalyst_event_id": event_id, "theme_id": theme_id,
                                    "event_fingerprint": digest(normalize(theme_id)+"|"+event_key), "event_type": signal["kind"], "title": signal["title"],
                                    "summary": signal["summary"], "event_at": signal["at"], "first_available_at": signal["available"],
                                    "credibility_score": 65 if signal["kind"] == "hot_topic" else 60 if signal["kind"] == "hot_event" else 70,
                                    "status": "active", "entity_keys_json": "[]", "created_at": frozen})
                            ref_hash = digest(signal["ref"])
                            claim = connection.execute("SELECT 1 FROM market_catalyst_source_claims WHERE catalyst_event_id=? AND source_ref_hash=?", (event_id, ref_hash)).fetchone()
                            if not claim:
                                insert(connection, "market_catalyst_source_claims", {"source_claim_id": "claim-"+digest(theme_id+"|"+signal["ref"]+"|"+signal["stance"]+"|"+signal["summary"])[:32],
                                    "catalyst_event_id": event_id, "source_name": signal["source"], "source_ref": signal["ref"], "source_ref_hash": ref_hash,
                                    "stance": signal["stance"], "source_credibility_score": 65, "summary": signal["summary"], "claim_fingerprint": digest(normalize(signal["summary"])),
                                    "published_at": signal["published"], "available_at": signal["available"], "collected_at": frozen, "raw_payload_hash": signal["rawHash"], "created_at": frozen})
                            stances = {row[0] for row in connection.execute("SELECT stance FROM market_catalyst_source_claims WHERE catalyst_event_id=?", (event_id,))}
                            disputed = {"supports", "contradicts"} <= stances
                            if disputed:
                                connection.execute("UPDATE market_catalyst_events SET status='disputed' WHERE catalyst_event_id=?", (event_id,))
                            conflict |= disputed
                            catalyst_ids.append(event_id)
                        catalyst_ids = sorted(set(catalyst_ids))
                        count = len({row["source"] for row in group if row["kind"] != "fund_flow"})
                        cycle, stage = next_stage(dict(previous) if previous else None, heat, rank, count, conflict)
                        summary = "；".join(dict.fromkeys(row["summary"] for row in sorted(group, key=lambda row: -row["strength"])))[:1200]
                        snapshot = {"snapshot_id": str(uuid4()), "theme_id": theme_id, "trade_date": at.date().isoformat(), "cycle_no": cycle,
                            "lifecycle_stage": stage, "rank": rank, "heat_score": heat, "summary": summary, "observed_at": at.isoformat(), "frozen_at": frozen,
                            "content_hash": digest(json.dumps([theme_id, at.isoformat(), cycle, stage, heat, summary, catalyst_ids], ensure_ascii=False)),
                            "constituent_count": 0, "catalyst_count": len(catalyst_ids), "conflicting_catalyst_count": int(conflict), "created_at": frozen}
                        insert(connection, "market_theme_daily_snapshots", snapshot)
                        for event_id in catalyst_ids:
                            insert(connection, "market_theme_snapshot_catalysts", {"snapshot_id": snapshot["snapshot_id"], "catalyst_event_id": event_id, "created_at": frozen})
                    result["frozenSnapshotIds"].append(snapshot["snapshot_id"])
                    result["themes"].append({"themeId": theme_id, "themeName": name, "snapshotId": snapshot["snapshot_id"], "lifecycleStage": snapshot["lifecycle_stage"],
                        "cycleNo": snapshot["cycle_no"], "rank": snapshot["rank"], "heatScore": snapshot["heat_score"], "existing": bool(existing)})
            except (sqlite3.Error, ValueError, KeyError) as exc:
                result["errors"].append({"themeName": key, "operation": "freeze", "message": str(exc)})
        if result["frozenSnapshotIds"]:
            result["status"] = "partial" if errors or result["errors"] else "ok"
        elif not errors and not signals:
            result["status"] = "empty"
        return result
