"""Frozen arithmetic, time and SQL helpers for already-published migrations."""

import json
import re
import uuid
from datetime import UTC, datetime, timedelta, timezone
from math import ceil, floor, isfinite
from pathlib import Path

from ..db import quote_identifier as qi

SHANGHAI = timezone(timedelta(hours=8))
SLOTS = [f"{minute // 60:02}:{minute % 60:02}" for minute in range(570, 690, 5)]
MODEL_DEFAULTS = json.loads((Path(__file__).parent / "model_defaults.json").read_text(encoding="utf8"))


def now():
    return datetime.now(UTC).isoformat()


def parse_time(value):
    if not value:
        return datetime.min.replace(tzinfo=SHANGHAI)
    if isinstance(value, datetime):
        return value if value.tzinfo else value.replace(tzinfo=SHANGHAI)
    parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    return parsed if parsed.tzinfo else parsed.replace(tzinfo=SHANGHAI)


def slot_at(value):
    at = parse_time(value).astimezone(SHANGHAI)
    minute = at.hour * 60 + at.minute
    return f"{minute // 60:02}:{minute // 5 * 5 % 60:02}" if 570 <= minute < 690 else ""


def money(value):
    scaled = value * 100
    return (floor(scaled + 0.5) if scaled >= 0 else ceil(scaled - 0.5)) / 100


def deterministic(value):
    return str(uuid.uuid5(uuid.NAMESPACE_OID, value))


def one(db, table, where="1", params=()):
    row = db.execute("SELECT * FROM " + qi(table) + " WHERE " + where + " LIMIT 1", params).fetchone()
    return dict(row) if row else None


def rows(db, table, suffix="", params=()):
    return [dict(row) for row in db.execute("SELECT * FROM " + qi(table) + " " + suffix, params)]


def insert(db, table, values, *, ignore=False):
    columns = {row["name"]: row for row in db.execute("PRAGMA table_info(" + qi(table) + ")")}
    defaults = {
        key: value
        for key, value in MODEL_DEFAULTS.get(table, {}).items()
        if key in columns and columns[key]["dflt_value"] is None
    }
    values = {**defaults, **values}
    fields = ",".join(qi(key) for key in values)
    db.execute(
        "INSERT "
        + ("OR IGNORE " if ignore else "")
        + "INTO "
        + qi(table)
        + " ("
        + fields
        + ") VALUES ("
        + ",".join("?" for _ in values)
        + ")",
        tuple(values.values()),
    )


def update(db, table, values, where, params=()):
    db.execute(
        "UPDATE " + qi(table) + " SET " + ",".join(qi(key) + "=?" for key in values) + " WHERE " + where,
        tuple(values.values()) + tuple(params),
    )


def lot_size(code):
    text = code.lower().strip()
    if not re.fullmatch(r"(?:sh|sz)?(?:60|68|00|30)[0-9]{4}", text):
        raise ValueError(f"invalid mainland stock code: {code}")
    digits = text[2:] if text.startswith(("sh", "sz")) else text
    return 200 if digits.startswith("68") else 100


def legacy_cost(price, quantity, side):
    execution = price * (1.001 if side == "buy" else 0.999)
    notional = execution * quantity
    commission = max(5.0, notional * 0.0003)
    transfer = notional * 0.00001
    stamp = notional * 0.0005 if side == "sell" else 0.0
    total = commission + transfer + stamp
    return dict(
        execution_price=execution,
        notional=notional,
        commission=commission,
        transfer_fee=transfer,
        stamp_duty=stamp,
        slippage_amount=abs(execution - price) * quantity,
        total_fees=total,
        net_cash_flow=-(notional + total) if side == "buy" else notional - total,
    )


def fixed_fifth_buy(code, price, cash, base):
    lot = lot_size(code)
    one_lot = legacy_cost(price, lot, "buy")
    cap = base / 5
    if -one_lot["net_cash_flow"] >= cap:
        if -one_lot["net_cash_flow"] > cash + 1e-8:
            raise ValueError("insufficient cash for minimum order")
        return lot, one_lot
    quantity = floor(cash / (price * 1.001) / lot) * lot
    while quantity >= lot:
        cost = legacy_cost(price, quantity, "buy")
        if -cost["net_cash_flow"] <= cash + 1e-8 and -cost["net_cash_flow"] < cap:
            return quantity, cost
        quantity -= lot
    raise ValueError("insufficient cash for minimum order")


def valid_price(value):
    return value is not None and isfinite(value) and value > 0
