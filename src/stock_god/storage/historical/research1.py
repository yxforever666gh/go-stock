"""Retired research migration algorithms, frozen independently of prediction."""

import json
import uuid

from .common import (
    SHANGHAI,
    deterministic,
    insert,
    legacy_cost,
    now,
    one,
    parse_time,
    rows,
    update,
    valid_price,
)
from .timestamps import time_key

ACCOUNT = "research_v160_simulated_accounts"
RUNS = "research_v160_analysis_runs"
RECOMMENDATIONS = "research_v160_recommendations"
POSITIONS = "research_v160_positions"
TRADES = "research_v160_simulated_trades"
EVENTS = "research_v160_decision_events"
FLOWS = "research_v170_account_cash_flows"
SNAPSHOTS = "research_v170_account_snapshots"
PLANS = "research_v170_funding_plans"
MESSAGES = "research_v160_lifecycle_messages"
FREEZE_REASON = "用户要求暂停研究中心1，全部清仓并冻结"


def initialize_funding(db):
    account = one(db, ACCOUNT, "id=1")
    if not account:
        raise ValueError("research1 account missing for funding migration")
    effective = account["created_at"] or now()
    day = parse_time(effective).astimezone(SHANGHAI).date().isoformat()
    insert(
        db,
        FLOWS,
        dict(
            flow_id=deterministic("go-stock-1.7.0-initial-contribution"),
            sequence=0,
            type="initial_deposit",
            amount=100000,
            effective_at=effective,
            trading_date=day,
            net_asset_value_before=0,
            net_asset_value_after=100000,
            unit_value_before=1,
            units_issued=100000,
            created_at=now(),
        ),
        ignore=True,
    )
    insert(
        db,
        PLANS,
        dict(
            id=1,
            initial_contribution=100000,
            target_contribution=500000,
            deposit_amount=100000,
            planned_deposits=4,
            completed_deposits=0,
            start_after_trading_date=parse_time(now()).astimezone(SHANGHAI).date().isoformat(),
            last_deposit_trading_date="",
            created_at=now(),
            updated_at=now(),
        ),
        ignore=True,
    )
    insert(
        db,
        SNAPSHOTS,
        dict(
            snapshot_id="initial-deposit-baseline",
            snapshot_type="initial_deposit",
            trading_date=day,
            valued_at=effective,
            cash=100000,
            position_value=0,
            net_asset_value=100000,
            cumulative_net_contribution=100000,
            unit_value=1,
            time_weighted_return=0,
            valuation_status="baseline",
            created_at=now(),
        ),
        ignore=True,
    )
    db.execute(
        "UPDATE research_v160_recommendations SET reserved_cash=50000 WHERE status IN ('buy_pending','pending') AND reserved_cash=0"
    )


def fixed_capital(db):
    account = one(db, ACCOUNT, "id=1")
    flows = rows(db, FLOWS, "ORDER BY sequence,id")
    initial = [f for f in flows if f["type"] == "initial_deposit" and f["sequence"] == 0]
    if len(initial) != 1 or any(
        f["type"] not in ("initial_deposit", "scheduled_deposit")
        or f["amount"] <= 0
        or (f["type"] == "scheduled_deposit" and f["sequence"] <= 0)
        or (f["type"] == "initial_deposit" and f["sequence"] != 0)
        for f in flows
    ):
        raise ValueError("invalid fixed-capital contribution ledger")
    total = sum(f["amount"] for f in flows)
    if total > 500000 + 1e-6 or account is None or account["initial_cash"] not in (100000, 500000):
        raise ValueError("fixed-capital migration cannot rebase account")
    update(
        db,
        ACCOUNT,
        dict(initial_cash=500000, cash=account["cash"] + 500000 - total, updated_at=now()),
        "id=1",
    )
    update(
        db,
        FLOWS,
        dict(
            amount=500000,
            net_asset_value_before=0,
            net_asset_value_after=500000,
            unit_value_before=1,
            units_issued=500000,
        ),
        "id=?",
        (initial[0]["id"],),
    )
    db.execute("DELETE FROM " + FLOWS + " WHERE type='scheduled_deposit'")
    db.execute("DELETE FROM " + SNAPSHOTS + " WHERE snapshot_type IN ('pre_deposit','post_deposit')")
    for snapshot in rows(db, SNAPSHOTS, "ORDER BY valued_at,id"):
        contribution = snapshot["cumulative_net_contribution"]
        if contribution <= 0 or contribution > 500000 + 1e-6:
            raise ValueError("invalid snapshot contribution")
        gap = 500000 - contribution
        unit = (snapshot["net_asset_value"] + gap) / 500000
        update(
            db,
            SNAPSHOTS,
            dict(
                cash=snapshot["cash"] + gap,
                net_asset_value=snapshot["net_asset_value"] + gap,
                cumulative_net_contribution=500000,
                unit_value=unit,
                time_weighted_return=unit - 1,
            ),
            "id=?",
            (snapshot["id"],),
        )
    insert(
        db,
        PLANS,
        dict(
            id=1,
            initial_contribution=500000,
            target_contribution=500000,
            deposit_amount=0,
            planned_deposits=0,
            completed_deposits=0,
            start_after_trading_date="",
            last_deposit_trading_date="",
        ),
        ignore=True,
    )
    update(
        db,
        PLANS,
        dict(
            initial_contribution=500000,
            target_contribution=500000,
            deposit_amount=0,
            planned_deposits=0,
            completed_deposits=0,
            start_after_trading_date="",
            last_deposit_trading_date="",
            updated_at=now(),
        ),
        "id=1",
    )
    restore_historical_buy(db)


def restore_historical_buy(db):
    run_id = "4bf3e4d9-959a-48c6-95b7-56104167c6cd"
    found = rows(db, RUNS, "WHERE run_id=?", (run_id,))
    if not found:
        return
    if len(found) != 1:
        raise ValueError("approved historical analysis run not unique")
    run = found[0]
    ids = {
        key: deterministic("go-stock-1.7.7-china-ping-an-" + key)
        for key in ("recommendation", "buy-trade", "correction-event")
    }
    recommendation_id = ids["recommendation"]
    counts = [
        db.execute("SELECT COUNT(*) FROM " + table + " WHERE " + column + "=?", (value,)).fetchone()[0]
        for table, column, value in (
            (RECOMMENDATIONS, "recommendation_id", recommendation_id),
            (TRADES, "trade_id", ids["buy-trade"]),
            (POSITIONS, "recommendation_id", recommendation_id),
            (EVENTS, "event_id", ids["correction-event"]),
            (MESSAGES, "recommendation_id", recommendation_id),
        )
    ]
    if counts == [1, 1, 1, 1, 3]:
        _verify_correction(db, ids)
        return
    if any(counts):
        raise ValueError("historical buy correction partially applied")
    signal = "2026-08-24T12:15:05.857285700+08:00"
    at = "2026-08-24T13:00:00+08:00"
    quote_at = "2026-08-24T13:01:00+08:00"
    header = "| 股票名称 | 股票代码 | AI分析摘要 | 主要风险 | 来源编号 |"
    report = run["final_report"] or ""
    if (
        run["status"] != "no_recommendation"
        or run["recommendation_count"] != 0
        or time_key(run["completed_at"]) != time_key(signal)
        or "中国平安" not in report
        or header not in report
    ):
        raise ValueError("approved report does not match immutable evidence")
    if db.execute(
        "SELECT COUNT(*) FROM " + RECOMMENDATIONS + " WHERE analysis_run_id=?", (run_id,)
    ).fetchone()[0]:
        raise ValueError("approved historical run has unrelated recommendations")
    cost = legacy_cost(55.17, 1000, "buy")
    account = one(db, ACCOUNT, "id=1")
    closing = one(db, SNAPSHOTS, "snapshot_id='daily-close-2026-08-24'")
    if not account or account["cash"] + 1e-8 < -cost["net_cash_flow"] or not closing:
        raise ValueError("historical correction requires cash and daily close snapshot")
    setting = one(db, "settings") or {}
    hour, minute = 9, 50
    try:
        raw = setting.get("ai_review_start_time") or "09:50"
        hour, minute = map(int, raw.split(":"))
        if not 0 <= hour < 24 or not 0 <= minute < 60:
            hour, minute = 9, 50
    except (ValueError, TypeError):
        hour, minute = 9, 50
    current = rows(
        db,
        POSITIONS,
        "WHERE stock_code='sh601318' AND status='open' AND current_price>0 AND current_price_at IS NOT NULL ORDER BY current_price_at DESC,id DESC LIMIT 1",
    )
    mark, mark_at = (
        (current[0]["current_price"], current[0]["current_price_at"])
        if current
        else (54.93, "2026-08-24T15:00:00+08:00")
    )
    summary = "金融防御属性突出，板块资金改善，个股资金连续流入，并在弱市中保持相对强势"
    risk = "日内涨幅和换手放大后存在获利回吐风险；财务与估值数据不完整"
    refs = "S067,S068,S069,S070,S071,S072,S073,S074,S075"
    decision = "1.7.7 历史重复持仓补买纠正"
    insert(
        db,
        RECOMMENDATIONS,
        dict(
            recommendation_id=recommendation_id,
            analysis_run_id=run_id,
            stock_code="sh601318",
            stock_name="中国平安",
            signal_at=signal,
            ai_summary=summary,
            main_risk=risk,
            source_refs=refs,
            status="active",
            next_check_at=f"2026-08-25T{hour:02}:{minute:02}:00+08:00",
            activated_at=at,
            activation_price=cost["execution_price"],
            quantity=1000,
            total_fees=cost["total_fees"],
            last_decision=decision,
            last_decision_at=at,
            created_at=signal,
            updated_at=at,
        ),
    )
    insert(
        db,
        POSITIONS,
        dict(
            recommendation_id=recommendation_id,
            stock_code="sh601318",
            stock_name="中国平安",
            market="SH",
            quantity=1000,
            entry_at=at,
            entry_price=cost["execution_price"],
            buy_fees=cost["total_fees"],
            current_price=mark,
            current_price_at=mark_at,
            status="open",
            created_at=at,
            updated_at=mark_at,
        ),
    )
    insert(
        db,
        TRADES,
        dict(
            trade_id=ids["buy-trade"],
            recommendation_id=recommendation_id,
            stock_code="sh601318",
            side="buy",
            traded_at=at,
            market_price=55.17,
            quantity=1000,
            created_at=at,
            **cost,
        ),
    )
    row = f"| 中国平安 | sh601318 | {summary} | {risk} | {refs} |"
    message_values = (
        (
            1,
            "system",
            "initial",
            "该推荐独立于同股既有仓位，按报告完成时点建立信号并单独管理后续卖出。",
            signal,
            run["model_name"],
        ),
        (2, "assistant", "initial", row, signal, run["model_name"]),
        (
            3,
            "system",
            "holding",
            "1.7.7 历史纠正：报告于午休期间完成，模拟成交顺延至 13:00；价格证据为腾讯 13:01 首分钟K线开盘价 55.17 元。",
            at,
            "",
        ),
    )
    for sequence, role, phase, content, created, model in message_values:
        insert(
            db,
            MESSAGES,
            dict(
                recommendation_id=recommendation_id,
                sequence=sequence,
                role=role,
                phase=phase,
                content=content,
                model=model,
                created_at=created,
            ),
        )
    insert(
        db,
        EVENTS,
        dict(
            event_id=ids["correction-event"],
            recommendation_id=recommendation_id,
            decision_type=decision,
            decided_at=at,
            reason="移除重复股票限制后，按原报告完成时间和下一交易时段开盘证据补记独立模拟买入。",
            quote_price=55.17,
            quote_at=quote_at,
            source_refs='["S068","S070","tencent:m1:202608241301:open=55.17"]',
            data_status="complete",
            created_at=at,
        ),
    )
    update(db, ACCOUNT, dict(cash=account["cash"] + cost["net_cash_flow"], updated_at=now()), "id=1")
    cash = closing["cash"] + cost["net_cash_flow"]
    position = closing["position_value"] + legacy_cost(54.93, 1000, "sell")["net_cash_flow"]
    nav = cash + position
    update(
        db,
        SNAPSHOTS,
        dict(
            cash=cash,
            position_value=position,
            net_asset_value=nav,
            cumulative_net_contribution=500000,
            unit_value=nav / 500000,
            time_weighted_return=nav / 500000 - 1,
        ),
        "id=?",
        (closing["id"],),
    )
    lines = report.strip().split("\n")
    index = next(i for i, line in enumerate(lines) if line.strip() == header)
    if index + 1 >= len(lines) or not lines[index + 1].strip().startswith("|---"):
        raise ValueError("approved report separator invalid")
    update(
        db,
        RUNS,
        dict(
            status="success",
            recommendation_count=1,
            failure_reason="",
            final_report="\n".join(lines[: index + 2] + [row]),
            updated_at=now(),
        ),
        "id=?",
        (run["id"],),
    )
    _verify_correction(db, ids)


def _verify_correction(db, ids):
    rec = one(db, RECOMMENDATIONS, "recommendation_id=?", (ids["recommendation"],))
    trade = one(db, TRADES, "trade_id=?", (ids["buy-trade"],))
    if (
        not rec
        or not trade
        or rec["quantity"] != 1000
        or trade["quantity"] != 1000
        or abs(trade["execution_price"] - 55.22517) > 1e-8
    ):
        raise ValueError("historical buy verification failed")
    lifecycle = (rec["status"] == "active" and rec["closed_at"] is None) or (
        rec["status"] == "closed"
        and rec["activated_at"]
        and rec["closed_at"]
        and time_key(rec["closed_at"]) >= time_key(rec["activated_at"])
    )
    if (
        rec["analysis_run_id"] != "4bf3e4d9-959a-48c6-95b7-56104167c6cd"
        or rec["stock_code"] != "sh601318"
        or not lifecycle
        or time_key(rec["signal_at"]) != time_key("2026-08-24T12:15:05.857285700+08:00")
        or time_key(rec["activated_at"]) != time_key("2026-08-24T13:00:00+08:00")
        or time_key(trade["traded_at"]) != time_key("2026-08-24T13:00:00+08:00")
        or abs(trade["market_price"] - 55.17) > 1e-8
    ):
        raise ValueError("historical buy evidence conflicts")


def freeze(db):
    account = one(db, ACCOUNT, "id=1")
    if not account:
        raise ValueError("research1 account missing")
    at = now()
    if not account["frozen"]:
        cash = account["cash"]
        for position in rows(db, POSITIONS, "WHERE status='open' ORDER BY id"):
            source = "最近保存的有效行情"
            price, quoted_at = position["current_price"], position["current_price_at"]
            if not valid_price(price):
                price, quoted_at, source = position["entry_price"], position["entry_at"], "原始买入成交价"
            if not valid_price(price) or (position["quantity"] or 0) <= 0:
                raise ValueError("invalid research1 liquidation price/quantity")
            rec = one(db, RECOMMENDATIONS, "recommendation_id=?", (position["recommendation_id"],))
            if not rec:
                raise ValueError("liquidation recommendation missing")
            cost = legacy_cost(price, position["quantity"], "sell")
            cash += cost["net_cash_flow"]
            invested = position["entry_price"] * position["quantity"] + (position["buy_fees"] or 0)
            profit = cost["net_cash_flow"] - invested
            insert(
                db,
                TRADES,
                dict(
                    trade_id=str(uuid.uuid4()),
                    recommendation_id=position["recommendation_id"],
                    stock_code=rec["stock_code"],
                    side="sell",
                    traded_at=at,
                    market_price=price,
                    quantity=position["quantity"],
                    created_at=at,
                    **cost,
                ),
            )
            update(
                db,
                POSITIONS,
                dict(
                    status="closed",
                    exit_at=at,
                    exit_price=cost["execution_price"],
                    sell_fees=cost["total_fees"],
                    current_price=price,
                    current_price_at=at,
                    net_pn_l=profit,
                    updated_at=at,
                ),
                "id=?",
                (position["id"],),
            )
            update(
                db,
                RECOMMENDATIONS,
                dict(
                    status="closed",
                    closed_at=at,
                    close_price=cost["execution_price"],
                    next_check_at=None,
                    total_fees=(position["buy_fees"] or 0) + cost["total_fees"],
                    net_pn_l=profit,
                    net_yield_rate=profit / invested if invested > 0 else 0,
                    updated_at=at,
                ),
                "recommendation_id=?",
                (position["recommendation_id"],),
            )
            insert(
                db,
                EVENTS,
                dict(
                    event_id=str(uuid.uuid4()),
                    recommendation_id=position["recommendation_id"],
                    decision_type="冻结清仓",
                    decided_at=at,
                    reason=FREEZE_REASON + "；模拟卖出价格来源：" + source,
                    quote_price=price,
                    quote_at=quoted_at,
                    decision_policy_version="research1-lifecycle-v3",
                    created_at=at,
                ),
            )
        update(
            db,
            ACCOUNT,
            dict(cash=cash, frozen=1, frozen_at=at, frozen_reason=FREEZE_REASON, updated_at=at),
            "id=1",
        )
        update(
            db,
            RECOMMENDATIONS,
            dict(
                status="missed_window",
                reserved_cash=0,
                next_check_at=None,
                last_decision="冻结取消",
                last_decision_at=at,
                updated_at=at,
            ),
            "status IN ('pending','buy_pending')",
        )
        for table, error_column in (
            ("research_v270_analysis_triggers", "last_error"),
            (RUNS, "failure_reason"),
        ):
            update(
                db,
                table,
                dict(
                    status="failed",
                    completed_at=at,
                    lease_owner="",
                    lease_expires_at=None,
                    updated_at=at,
                    **{error_column: FREEZE_REASON},
                ),
                "status IN ('queued','running')",
            )
        update(
            db,
            "research_v270_buy_opportunities",
            dict(status="expired", expires_at=at, timing_reason=FREEZE_REASON, updated_at=at),
            "status='active'",
        )
        contribution, units = db.execute(
            "SELECT COALESCE(SUM(amount),0),COALESCE(SUM(units_issued),0) FROM " + FLOWS
        ).fetchone()
        unit = cash / units if units > 0 else 1
        insert(
            db,
            SNAPSHOTS,
            dict(
                snapshot_id="research1-freeze-4.0.1",
                snapshot_type="frozen",
                trading_date=parse_time(at).astimezone(SHANGHAI).date().isoformat(),
                valued_at=at,
                cash=cash,
                position_value=0,
                net_asset_value=cash,
                cumulative_net_contribution=contribution,
                unit_value=unit,
                time_weighted_return=unit - 1,
                valuation_status="frozen",
                created_at=at,
            ),
        )
    update(
        db,
        "research_audit_run_states",
        dict(status="failed", last_error=FREEZE_REASON, updated_at=at),
        "owner_type='research1' AND status='capturing'",
    )
    update(
        db,
        "research_replays",
        dict(status="failed", last_error=FREEZE_REASON, completed_at=at),
        "source_owner_type='research1' AND status IN ('queued','running')",
    )
    record = one(db, "research_settings", "center='research1'")
    if not record:
        raise ValueError("research1 settings unavailable")
    config = json.loads(record["config_json"])
    if config.get("aiCapitalDeploymentEnabled") is not False:
        config["aiCapitalDeploymentEnabled"] = False
        update(
            db,
            "research_settings",
            dict(
                config_json=json.dumps(config, ensure_ascii=False, sort_keys=True, separators=(",", ":")),
                revision=record["revision"] + 1,
            ),
            "center='research1'",
        )
