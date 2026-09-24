"""Frozen schema 31/35 capital rebasing. Never used by live prediction."""

from collections import defaultdict
from datetime import timedelta
import math

from .common import (
    SHANGHAI,
    SLOTS,
    deterministic,
    fixed_fifth_buy,
    insert,
    legacy_cost,
    lot_size,
    money,
    now,
    one,
    parse_time,
    rows,
    update,
    valid_price,
)

EVENTS = "research2_account_capital_events"
LEDGER = "research2_account_ledger_snapshots"
BASIS = "capital_ledger_v1"
INITIAL_AT = "2026-08-27T09:30:00+08:00"
TOPUP_AT = "2026-09-21T09:25:00+08:00"
REBASE_DAY = "2026-09-21"


def event_id(*parts):
    return deterministic("go-stock:research2:capital-ledger:v1:" + ":".join(parts))


def capital_event(slot, kind, amount, external, source, at, key):
    return dict(
        event_id=event_id(slot, key),
        slot=slot,
        event_type=kind,
        amount=amount,
        external=int(external),
        source=source,
        effective_at=at,
        trading_date=parse_time(at).astimezone(SHANGHAI).date().isoformat(),
        created_at=now(),
    )


def _verify_accounts(db):
    accounts = rows(db, "research2_accounts", "ORDER BY slot")
    if len(accounts) != 24 or {a["slot"] for a in accounts} != set(SLOTS):
        raise ValueError("capital migration requires exactly 24 valid slot accounts")
    return accounts


def _base_events(db):
    from .timestamps import time_key

    result = []
    for slot in SLOTS:
        result.extend(
            (
                capital_event(
                    slot, "initial_external", 10000, True, "user_initial_capital", INITIAL_AT, "initial"
                ),
                capital_event(
                    slot, "top_up_external", 10000, True, "user_top_up", TOPUP_AT, "top-up-20260921"
                ),
            )
        )
    cash = dict.fromkeys(SLOTS, 10000.0)
    for trade in sorted(
        rows(db, "research2_trades"), key=lambda t: (time_key(t["traded_at"]), t["trade_id"], t["id"])
    ):
        at = parse_time(trade["traded_at"])
        flow = trade["net_cash_flow"] or 0
        if trade["slot"] not in SLOTS or at < parse_time(INITIAL_AT) or not math.isfinite(flow):
            raise ValueError("invalid historical trade: " + trade["trade_id"])
        if at >= parse_time(TOPUP_AT):
            continue
        slot = trade["slot"]
        if flow < 0 and cash[slot] + flow < -1e-7:
            shortfall = -(cash[slot] + flow)
            # Preserve nanosecond ordering without truncating source timestamps.
            from .timestamps import before_one_nanosecond

            result.append(
                capital_event(
                    slot,
                    "legacy_pool_transfer",
                    shortfall,
                    False,
                    "legacy_shared_pool",
                    before_one_nanosecond(trade["traded_at"]),
                    "legacy-transfer-" + trade["trade_id"],
                )
            )
            cash[slot] += shortfall
        cash[slot] += flow
        if cash[slot] < -1e-7:
            raise ValueError("historical trade overdraws account")
    return result


def rebase_capital(db):
    _verify_accounts(db)
    if db.execute("SELECT COUNT(*) FROM " + EVENTS).fetchone()[0]:
        verify_capital(db)
        return
    count = db.execute(
        "SELECT COUNT(*) FROM research2_trades s JOIN research2_recommendations r "
        "ON r.recommendation_id=s.recommendation_id JOIN research2_analysis_runs a "
        "ON a.run_id=r.analysis_run_id WHERE a.trading_date=? AND s.side='sell'",
        (REBASE_DAY,),
    ).fetchone()[0]
    if count:
        raise ValueError("capital rebase refuses dependent sells for 2026-09-21 buys")
    events = _base_events(db)
    candidates, cash, bases, filled = _rebase_plan(db, events)
    for event in events:
        insert(db, EVENTS, event)
    for candidate in candidates:
        db.execute(
            "DELETE FROM research2_trades WHERE recommendation_id=? AND side='buy'",
            (candidate["item"]["recommendation_id"],),
        )
    for candidate in candidates:
        _apply_candidate(db, candidate)
    for chain_id, base in bases.items():
        count = filled[chain_id]
        update(
            db,
            "research2_execution_chains",
            dict(
                allocation_base_cash=base,
                filled_slots=count,
                status="completed",
                stop_reason="按固定五分之一仓位重算后已完成五笔买入"
                if count >= 5
                else "当日有效报告的固定五分之一仓位重算完成",
                updated_at=now(),
            ),
            "chain_id=?",
            (chain_id,),
        )
    for slot in SLOTS:
        update(
            db,
            "research2_accounts",
            dict(initial_cash=10000, cash=cash[slot], updated_at=now()),
            "slot=?",
            (slot,),
        )
    rebuild_ledger(db)
    verify_capital(db)


def _rebase_plan(db, capital):
    from .timestamps import time_key

    chains = rows(
        db,
        "research2_execution_chains",
        "WHERE trading_date=? AND TRIM(winner_run_id)<>'' ORDER BY scheduled_for,chain_id",
        (REBASE_DAY,),
    )
    candidates, target_ids, replay = [], set(), []
    for chain in chains:
        run = one(db, "research2_analysis_runs", "run_id=?", (chain["winner_run_id"],))
        if (
            not run
            or not run["generated_at"]
            or run["chain_id"] != chain["chain_id"]
            or run["slot"] != chain["slot"]
            or not run["published"]
        ):
            raise ValueError("capital rebase chain has invalid winner: " + chain["chain_id"])
        landed = run["persisted_at"] or run["generated_at"]
        replay.append((time_key(landed), 2, chain["chain_id"], chain["slot"], "base", chain["chain_id"]))
        last = landed
        items = rows(
            db,
            "research2_recommendations",
            "WHERE analysis_run_id=? ORDER BY final_score DESC,selection_rank,id",
            (run["run_id"],),
        )
        for item in items:
            if item["slot"] != run["slot"] or item["sell_at"]:
                raise ValueError("capital rebase recommendation has invalid ownership/dependent sale")
            quote = item["execution_quote_price"] or item["buy_market_price"] or 0
            quoted_at = item["execution_quote_at"] or item["buy_at"]
            execute = max((landed, quoted_at or landed, last), key=time_key)
            candidate = dict(
                item=item,
                chain=chain["chain_id"],
                landed=landed,
                quote=quote,
                quoted_at=quoted_at,
                execute=execute,
                result={},
            )
            candidates.append(candidate)
            target_ids.add(item["recommendation_id"])
            replay.append(
                (time_key(execute), 3, f"{len(candidates) - 1:08}", item["slot"], "candidate", candidate)
            )
            last = execute
    for event in capital:
        replay.append(
            (time_key(event["effective_at"]), 0, event["event_id"], event["slot"], "capital", event["amount"])
        )
    for trade in rows(db, "research2_trades"):
        if trade["recommendation_id"] in target_ids and trade["side"] == "buy":
            continue
        replay.append(
            (
                time_key(trade["traded_at"]),
                1,
                trade["trade_id"],
                trade["slot"],
                "trade",
                trade["net_cash_flow"] or 0,
            )
        )
    cash, bases, filled = dict.fromkeys(SLOTS, 0.0), {}, defaultdict(int)
    for _, _, key, slot, kind, data in sorted(replay, key=lambda e: e[:3]):
        if slot not in SLOTS:
            continue
        if kind in ("capital", "trade"):
            cash[slot] += data
        elif kind == "base":
            bases[data] = cash[slot]
        else:
            _plan_buy(data, cash, bases, filled)
        if cash[slot] < -1e-7:
            raise ValueError("capital replay overdraws " + slot + " at " + key)
    return candidates, cash, bases, filled


def _plan_buy(candidate, cash, bases, filled):
    item = candidate["item"]
    status, reason = "analysis_only", item["failure_reason"] or ""
    eligible = item["status"] in (
        "active",
        "buy_pending",
        "standby",
        "standby_not_used",
        "missed_cash",
        "missed_untradable",
    )
    eligible |= (
        item["status"] == "analysis_only"
        and not item["execution_failure_code"]
        and (item["execution_quote_price"] or 0) > 0
        and bool(item["execution_quote_at"])
    )
    if eligible:
        if (item["execution_failure_code"] or "").strip() in (
            "suspended",
            "limit_up",
            "limit_down",
            "invalid_price",
            "near_limit_up",
            "quote_retry",
        ):
            status = "missed_untradable"
        elif not valid_price(candidate["quote"]) or not candidate["quoted_at"]:
            reason = "历史执行报价不可用，仅保留分析"
        elif (
            parse_time(candidate["quoted_at"]) < parse_time(candidate["landed"]).replace(microsecond=0)
            or parse_time(candidate["execute"]).astimezone(SHANGHAI).date().isoformat() != REBASE_DAY
            or parse_time(candidate["execute"]).astimezone(SHANGHAI).hour * 60
            + parse_time(candidate["execute"]).minute
            >= 690
        ):
            reason = "历史执行报价不在有效买入窗口，仅保留分析"
        elif filled[candidate["chain"]] >= 5:
            reason = "本区间当日已完成五笔买入，剩余评分仅保留分析"
        elif bases.get(candidate["chain"], 0) <= 0:
            status, reason = "missed_cash", "本区间启动资金不可用"
        else:
            try:
                quantity, cost = fixed_fifth_buy(
                    item["stock_code"], candidate["quote"], cash[item["slot"]], bases[candidate["chain"]]
                )
            except ValueError:
                try:
                    lot_cost = -legacy_cost(candidate["quote"], lot_size(item["stock_code"]), "buy")[
                        "net_cash_flow"
                    ]
                except ValueError:
                    lot_cost = 0
                status, reason = (
                    "missed_cash",
                    f"剩余现金{cash[item['slot']]:.2f}元不足支付一手含费成本{lot_cost:.2f}元",
                )
            else:
                cash[item["slot"]] += cost["net_cash_flow"]
                filled[candidate["chain"]] += 1
                candidate["result"] = dict(status="active", failure_reason="", quantity=quantity, cost=cost)
                return
    candidate["result"] = dict(status=status, failure_reason=reason)


def _apply_candidate(db, candidate):
    item, result = candidate["item"], candidate["result"]
    values = dict(
        status=result["status"],
        failure_reason=result["failure_reason"],
        buy_at=None,
        buy_market_price=0,
        buy_price=0,
        quantity=0,
        buy_fees=0,
        sell_at=None,
        sell_market_price=0,
        sell_price=0,
        sell_fees=0,
        target_sell_at=None,
        net_pn_l=0,
        net_yield_rate=0,
        hit_five_before_sell=None,
        hit_limit_up_full_day=None,
        hit_minus_three=None,
        metrics_finalized=0,
        updated_at=now(),
    )
    if result["status"] == "active":
        at, cost = candidate["execute"], result["cost"]
        hour, minute = map(int, item["slot"].split(":"))
        sell_at = (
            item["target_sell_at"]
            or (parse_time(at).astimezone(SHANGHAI) + timedelta(days=1))
            .replace(hour=hour, minute=minute, second=0, microsecond=0)
            .isoformat()
        )
        values.update(
            execution_failure_code="",
            buy_at=at,
            buy_market_price=candidate["quote"],
            buy_price=cost["execution_price"],
            quantity=result["quantity"],
            buy_fees=cost["commission"] + cost["transfer_fee"],
            target_sell_at=sell_at,
            current_price=item["current_price"] if (item["current_price"] or 0) > 0 else candidate["quote"],
            current_price_at=item["current_price_at"] if (item["current_price"] or 0) > 0 else at,
        )
        trade = dict(
            trade_id=event_id("rebase-buy", item["recommendation_id"]),
            slot=item["slot"],
            recommendation_id=item["recommendation_id"],
            side="buy",
            traded_at=at,
            market_price=candidate["quote"],
            quantity=result["quantity"],
            price_source="capital_rebase_stored_execution_quote",
            execution_mode="capital_rebase_fixed_fifth",
            quote_at=candidate["quoted_at"],
            created_at=now(),
        )
        trade.update(
            {
                key: cost[key]
                for key in (
                    "execution_price",
                    "commission",
                    "transfer_fee",
                    "slippage_amount",
                    "net_cash_flow",
                )
            }
        )
        insert(db, "research2_trades", trade)
    update(db, "research2_recommendations", values, "recommendation_id=?", (item["recommendation_id"],))


def rebuild_ledger(db):
    from .timestamps import time_key

    db.execute("DELETE FROM " + LEDGER + " WHERE valuation_basis=?", (BASIS,))
    stream = [(time_key(e["effective_at"]), 0, e["event_id"], e) for e in rows(db, EVENTS)]
    stream += [(time_key(t["traded_at"]), 1, t["trade_id"], t) for t in rows(db, "research2_trades")]
    cash, external, transfer = defaultdict(float), defaultdict(float), defaultdict(float)
    positions = defaultdict(dict)
    for _, priority, key, value in sorted(stream, key=lambda e: e[:3]):
        slot = value["slot"]
        if slot not in SLOTS:
            continue
        if priority == 0:
            kind, at = value["event_type"], value["effective_at"]
            cash[slot] += value["amount"]
            (external if value["external"] else transfer)[slot] += value["amount"]
        else:
            kind, at = "trade", value["traded_at"]
            cash[slot] += value["net_cash_flow"] or 0
            if value["side"] == "buy":
                positions[slot][value["recommendation_id"]] = value
            elif value["side"] == "sell":
                positions[slot].pop(value["recommendation_id"], None)
        if cash[slot] < -1e-7:
            raise ValueError("capital ledger overdraws " + slot + " at " + key)
        position = sum(
            legacy_cost(p["market_price"], p["quantity"], "sell")["net_cash_flow"]
            for p in positions[slot].values()
            if (p["quantity"] or 0) > 0 and (p["market_price"] or 0) > 0
        )
        nav = cash[slot] + position
        profit = nav - external[slot] - transfer[slot]
        insert(
            db,
            LEDGER,
            dict(
                snapshot_id=deterministic(
                    "go-stock:research2:capital-ledger-snapshot:v1:" + ":".join((slot, kind, key))
                ),
                slot=slot,
                valued_at=at,
                trading_date=parse_time(at).astimezone(SHANGHAI).date().isoformat(),
                snapshot_type=kind,
                cash=money(cash[slot]),
                position_value=money(position),
                net_asset_value=money(nav),
                cumulative_external_capital=money(external[slot]),
                net_internal_transfer=money(transfer[slot]),
                net_profit=money(profit),
                cumulative_capital_return=profit / external[slot] if external[slot] > 0 else 0,
                valuation_basis=BASIS,
                created_at=now(),
            ),
        )


def second_topup(db):
    _verify_accounts(db)
    for slot in SLOTS:
        event = capital_event(
            slot,
            "top_up_external",
            10000,
            True,
            "user_top_up",
            "2026-09-23T09:25:00+08:00",
            "top-up-20260923",
        )
        stored = one(db, EVENTS, "event_id=?", (event["event_id"],))
        if stored:
            for field in ("event_id", "slot", "event_type", "external", "source", "trading_date"):
                if stored[field] != event[field]:
                    raise ValueError("authorized top-up conflicts: " + field)
            if abs(stored["amount"] - event["amount"]) > 0.01:
                raise ValueError("authorized top-up amount conflicts")
            if parse_time(stored["effective_at"]) != parse_time(event["effective_at"]):
                raise ValueError("authorized top-up time conflicts")
        else:
            insert(db, EVENTS, event)
            db.execute(
                "UPDATE research2_accounts SET cash=cash+10000,updated_at=? WHERE slot=?", (now(), slot)
            )
    rebuild_ledger(db)


def verify_capital(db):
    for account in _verify_accounts(db):
        for kind, source, at, key in (
            ("initial_external", "user_initial_capital", INITIAL_AT, "initial"),
            ("top_up_external", "user_top_up", TOPUP_AT, "top-up-20260921"),
        ):
            expected = capital_event(account["slot"], kind, 10000, True, source, at, key)
            stored = one(db, EVENTS, "event_id=?", (expected["event_id"],))
            if (
                not stored
                or any(
                    stored[field] != expected[field]
                    for field in ("slot", "event_type", "external", "source", "trading_date")
                )
                or abs(stored["amount"] - expected["amount"]) > 0.01
                or parse_time(stored["effective_at"]) != parse_time(at)
            ):
                raise ValueError("missing/conflicting required capital event in " + account["slot"])
        total = db.execute(
            "SELECT COALESCE(SUM(amount),0) FROM " + EVENTS + " WHERE slot=?", (account["slot"],)
        ).fetchone()[0]
        flow = db.execute(
            "SELECT COALESCE(SUM(net_cash_flow),0) FROM research2_trades WHERE slot=?", (account["slot"],)
        ).fetchone()[0]
        if (
            abs(account["initial_cash"] - 10000) > 0.01
            or account["cash"] < -1e-7
            or abs(account["cash"] - total - flow) > 0.01
        ):
            raise ValueError("capital/trade balance mismatch in " + account["slot"])
        count = db.execute(
            "SELECT COUNT(*) FROM " + LEDGER + " WHERE slot=? AND valuation_basis=?", (account["slot"], BASIS)
        ).fetchone()[0]
        if count < 2:
            raise ValueError("capital ledger snapshots missing in " + account["slot"])
