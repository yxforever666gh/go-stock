"""Ordered data transformations for the published 1–35 migration ledger."""

import json
import uuid
from datetime import timedelta
from pathlib import Path

from . import research1
from .capital import rebase_capital, second_topup
from .common import (
    SHANGHAI,
    SLOTS,
    deterministic,
    insert,
    legacy_cost,
    now,
    one,
    parse_time,
    rows,
    slot_at,
    update,
)

DIRECTORY = Path(__file__).parent
UPDATES = json.loads((DIRECTORY / "data_updates.json").read_text(encoding="utf8"))
PROJECTION = json.loads((DIRECTORY / "settings_projection.json").read_text(encoding="utf8"))
COMMON_FIELDS = """tushareToken crawlTimeOut kDays browserPath browserPoolSize httpProxy httpProxyEnabled forceNoProxyForFetch qgqpBId experimentalEvidenceEnabled minuteProviderMode minuteProviderOrder minuteLongHistoryHintEnabled privateMinuteEnabled privateMinuteBaseUrl privateMinuteApiKey privateMinuteTimeoutSec privateMinuteMinIntervalMs privateMinuteProxyMode privateMinuteLevel akshareEnabled sinaMinuteEnabled tencentMinuteEnabled eastmoneyMinuteEnabled akshareMinuteSourceMode""".split()
CENTER_FIELDS = {
    "research1": "aiCapitalDeploymentEnabled aiTargetCapitalUtilization aiMaxImmediateBuysPerRun aiReanalysisIntervalMinutes aiReviewStartTime aiReviewIntervalMinutes".split(),
    "research2": "research2AutoEnabled research2EmailEnabled research2EmailTo research2EmailFrom research2EmailSmtpHost research2EmailSmtpPort research2EmailSmtpUsername research2EmailSmtpPassword research2EmailSlots".split(),
}


def apply_data(db, version, *, introduced_capital=False):
    for sql in UPDATES.get(str(version), []):
        if version == 21 and "ai_capital_deployment_enabled = CASE" in sql and not introduced_capital:
            continue
        db.execute(sql)
    if version == 3:
        insert(
            db,
            research1.ACCOUNT,
            dict(
                id=1,
                initial_cash=100000,
                cash=100000,
                frozen=0,
                frozen_reason="",
                created_at=now(),
                updated_at=now(),
            ),
            ignore=True,
        )
    elif version == 6:
        recovery(db)
    elif version == 8:
        direct_buy(db)
    elif version == 10:
        research1.initialize_funding(db)
    elif version == 12:
        research1.fixed_capital(db)
    elif version == 13:
        insert(
            db,
            "research2_accounts",
            dict(id=1, slot="09:50", initial_cash=12000, cash=12000, created_at=now(), updated_at=now()),
            ignore=True,
        )
    elif version == 25:
        for table, field in (
            (research1.RUNS, "strategy_version"),
            (research1.RUNS, "data_profile_version"),
            ("research_v270_buy_opportunities", "data_profile_version"),
            ("research_v160_lifecycle_observations", "data_profile_version"),
            (research1.EVENTS, "decision_policy_version"),
        ):
            db.execute(
                f"UPDATE {table} SET {field}='legacy-unversioned' WHERE {field} IS NULL OR TRIM({field})=''"
            )
        db.execute(
            "UPDATE research_v270_buy_opportunities SET requested_action=action WHERE requested_action IS NULL OR TRIM(requested_action)=''"
        )
        db.execute(
            "UPDATE research_v270_buy_opportunities SET decision_quote_status=CASE WHEN quote_price>0 AND quote_at IS NOT NULL THEN 'legacy-recorded' ELSE 'legacy-unavailable' END WHERE decision_quote_status IS NULL OR TRIM(decision_quote_status)='' OR decision_quote_status='legacy-unavailable'"
        )
    elif version == 26:
        db.execute(
            "UPDATE research2_analysis_runs SET trigger_source='legacy-unversioned' WHERE trigger_source IS NULL OR TRIM(trigger_source)=''"
        )
        db.execute(
            "UPDATE research2_recommendations SET selection_role='legacy-unversioned' WHERE selection_role IS NULL OR TRIM(selection_role)=''"
        )
    elif version == 27:
        independent_settings(db)
    elif version == 28:
        split_slots(db)
    elif version == 29:
        research1.freeze(db)
    elif version == 31:
        rebase_capital(db)
    elif version == 32:
        reset_emails(db)
    elif version == 33:
        update(
            db,
            "research2_recommendations",
            dict(buy_day_limit_status="pending", buy_day_limit_source_json="[]", updated_at=now()),
            "TRIM(COALESCE(buy_day_limit_status,''))=''",
        )
    elif version == 34:
        update(
            db,
            "research2_execution_chains",
            dict(allocation_policy="legacy_recorded", updated_at=now()),
            "TRIM(COALESCE(allocation_policy,''))=''",
        )
    elif version == 35:
        second_topup(db)


def recovery(db):
    for rec_id, event_id in (
        ("c49ade23-12f4-4aa0-8203-b985bfd9d7e4", "16300000-0000-4000-8000-000000000001"),
        ("699640bc-861e-4330-8023-4182173b3e9e", "16300000-0000-4000-8000-000000000002"),
    ):
        result = db.execute(
            "UPDATE research_v160_recommendations SET status='pending',next_check_at='2026-08-18T09:30:00+08:00',last_decision='',last_decision_at=NULL,updated_at=? WHERE recommendation_id=? AND status='invalidated'",
            (now(), rec_id),
        )
        if result.rowcount:
            insert(
                db,
                research1.EVENTS,
                dict(
                    event_id=event_id,
                    recommendation_id=rec_id,
                    decision_type="人工恢复",
                    decided_at=now(),
                    reason="1.6.3 启用累计4小时开盘交易时长规则，恢复为未激活并从下一交易日继续判断",
                    created_at=now(),
                ),
                ignore=True,
            )


def direct_buy(db):
    targets = (
        "c49ade23-12f4-4aa0-8203-b985bfd9d7e4",
        "699640bc-861e-4330-8023-4182173b3e9e",
        "3bf68fd1-d97f-4426-aa2c-cb63236be808",
        "053e7c47-a538-4d6d-9dbd-61e9897d8285",
    )
    at = now()
    for item in rows(
        db,
        research1.RECOMMENDATIONS,
        "WHERE status='pending' OR (recommendation_id IN (?,?,?,?) AND status='invalidated') ORDER BY signal_at,id",
        targets,
    ):
        rec_id = item["recommendation_id"]
        if one(db, research1.TRADES, "recommendation_id=? AND side='buy'", (rec_id,)) or one(
            db, research1.POSITIONS, "recommendation_id=?", (rec_id,)
        ):
            continue
        update(
            db,
            research1.RECOMMENDATIONS,
            dict(
                status="buy_pending",
                next_check_at=at,
                last_decision="待买入",
                last_decision_at=at,
                updated_at=at,
            ),
            "recommendation_id=?",
            (rec_id,),
        )
        insert(
            db,
            research1.EVENTS,
            dict(
                event_id=deterministic("go-stock-1.6.5-direct-buy:" + rec_id),
                recommendation_id=rec_id,
                decision_type="策略升级待买入",
                decided_at=at,
                reason="1.6.5 删除激活流程；保留历史记录并按原信号顺序进入一次性直接买入队列",
                created_at=at,
            ),
            ignore=True,
        )
    for position in rows(db, research1.POSITIONS, "WHERE status='open' ORDER BY entry_at,id"):
        next_day = (parse_time(position["entry_at"]).astimezone(SHANGHAI) + timedelta(days=1)).replace(
            hour=9, minute=50, second=0, microsecond=0
        )
        update(
            db,
            research1.RECOMMENDATIONS,
            dict(next_check_at=next_day.isoformat(), updated_at=at),
            "recommendation_id=? AND status IN ('active','sell_pending')",
            (position["recommendation_id"],),
        )


def independent_settings(db):
    defaults = dict(
        crawl_time_out=60,
        k_days=60,
        browser_pool_size=1,
        force_no_proxy_for_fetch=1,
        ai_capital_deployment_enabled=1,
        ai_target_capital_utilization=0.9,
        ai_max_immediate_buys_per_run=2,
        ai_reanalysis_interval_minutes=30,
        ai_review_start_time="09:50",
        ai_review_interval_minutes=15,
        research2_auto_enabled=1,
        minute_provider_mode="public",
        minute_provider_order="tencent,sina,akshare,private",
        minute_long_history_hint_enabled=1,
        akshare_enabled=1,
        sina_minute_enabled=1,
        tencent_minute_enabled=1,
        eastmoney_minute_enabled=1,
        private_minute_timeout_sec=60,
        private_minute_min_interval=1200,
        private_minute_proxy_mode="disable",
        private_minute_level="1min",
        akshare_minute_source_mode="auto",
    )
    existing = rows(db, "settings", "ORDER BY id LIMIT 1")
    settings = existing[0] if existing else defaults
    models = rows(
        db,
        "ai_config",
        "WHERE (owner='global' OR owner='' OR owner IS NULL) AND archived_at IS NULL ORDER BY id",
    )
    for center, center_fields in CENTER_FIELDS.items():
        if one(db, "research_settings", "center=?", (center,)):
            continue
        config = {}
        for field in COMMON_FIELDS + center_fields:
            if field == "minuteProviderOrder":
                config[field] = (
                    str(settings.get("minute_provider_order") or "").split(",")
                    if settings.get("minute_provider_order")
                    else []
                )
            elif field == "research2EmailSlots":
                config[field] = []
            else:
                column, typ = PROJECTION[field]
                value = settings.get(column)
                config[field] = bool(value) if typ == "bool" else (value or ("" if typ == "string" else 0))
        insert(
            db,
            "research_settings",
            dict(
                center=center,
                config_json=json.dumps(config, ensure_ascii=False, sort_keys=True, separators=(",", ":")),
                revision=1,
            ),
        )
        for original in models:
            clone = {k: v for k, v in original.items() if k != "id"}
            clone.update(owner=center, created_at=now(), updated_at=now())
            insert(db, "ai_config", clone)


def split_slots(db):
    existing = rows(db, "research2_accounts", "WHERE baseline_at IS NOT NULL")
    if len(existing) == 24:
        return
    if existing:
        raise ValueError("partial research2 slot initialization")
    old = one(db, "research2_accounts", "id=1")
    if not old or old["cash"] + 10000 < 0:
        raise ValueError("invalid research2 seed cash")
    at = now()
    cash = old["cash"] + 10000
    values = dict.fromkeys(SLOTS, 0.0)
    for item in rows(db, "research2_recommendations"):
        detected = slot_at(item["signal_at"])
        slot = detected or "09:50"
        changes: dict[str, object] = dict(slot=slot, legacy_slot_exception=int(not detected))
        if item["status"] in ("active", "sell_pending"):
            price = next(
                (
                    p
                    for p in (item["current_price"], item["buy_market_price"], item["buy_price"])
                    if p and p > 0
                ),
                0,
            )
            if price <= 0 or (item["quantity"] or 0) <= 0:
                raise ValueError("invalid migrated holding")
            value = legacy_cost(price, item["quantity"], "sell")["net_cash_flow"]
            changes["baseline_value"] = value
            values[slot] += value
        update(db, "research2_recommendations", changes, "id=?", (item["id"],))
        update(db, "research2_trades", dict(slot=slot), "recommendation_id=?", (item["recommendation_id"],))
    for slot in SLOTS:
        data = dict(
            slot=slot,
            cash=cash,
            seed_cash=old["cash"],
            baseline_at=at,
            baseline_net_asset_value=cash + values[slot],
            updated_at=at,
        )
        account = one(
            db,
            "research2_accounts",
            "id=1" if slot == "09:50" else "slot=?",
            () if slot == "09:50" else (slot,),
        )
        if slot != "09:50":
            data["initial_cash"] = cash
        if account:
            update(db, "research2_accounts", data, "id=?", (account["id"],))
        else:
            data["created_at"] = at
            insert(db, "research2_accounts", data)
        insert(
            db,
            "research2_account_snapshots",
            dict(
                slot=slot,
                snapshot_id=str(uuid.uuid4()),
                valued_at=at,
                trading_date=parse_time(at).astimezone(SHANGHAI).date().isoformat(),
                snapshot_type="slot_baseline",
                cash=cash,
                position_value=values[slot],
                net_asset_value=cash + values[slot],
                created_at=at,
            ),
        )
    for run in rows(db, "research2_analysis_runs"):
        update(
            db,
            "research2_analysis_runs",
            dict(
                scheduled_slot=slot_at(run["scheduled_for"]) or "09:50",
                slot=slot_at(run["generated_at"]) or "09:50",
                published=int(run["status"] in ("success", "no_recommendation")),
                persisted_at=run["generated_at"],
            ),
            "id=?",
            (run["id"],),
        )
    update(
        db,
        "research2_recommendations",
        dict(status="analysis_only", failure_reason="分区迁移已结束旧买入计划"),
        "status IN ('buy_pending','standby')",
    )


def reset_emails(db):
    update(
        db,
        "settings",
        dict(research2_email_enabled=0, research2_email_slots="[]", updated_at=now()),
        "research2_email_enabled=1 OR COALESCE(research2_email_slots,'')<>'[]'",
    )
    record = one(db, "research_settings", "center='research2'")
    if not record:
        raise ValueError("research2 settings unavailable for email migration")
    config = json.loads(record["config_json"])
    if config.get("research2EmailEnabled") or config.get("research2EmailSlots") != []:
        config.update(research2EmailEnabled=False, research2EmailSlots=[])
        update(
            db,
            "research_settings",
            dict(
                config_json=json.dumps(config, ensure_ascii=False, sort_keys=True, separators=(",", ":")),
                revision=record["revision"] + 1,
            ),
            "center='research2'",
        )
    update(
        db,
        "research2_email_deliveries",
        dict(
            status="cancelled",
            next_attempt_at=None,
            last_error="升级为按时间段即时发送，旧汇总邮件已取消",
            updated_at=now(),
        ),
        "status IN ('pending','retry_wait','sending')",
    )
