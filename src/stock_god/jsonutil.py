"""Deterministic JSON encoding for contracts previously hashed by encoding/json."""

import json
import math
from collections.abc import Mapping
from decimal import Decimal


def _string(value: str) -> str:
    return (
        json.dumps(value, ensure_ascii=False)
        .replace("<", "\\u003c")
        .replace(">", "\\u003e")
        .replace("&", "\\u0026")
        .replace("\u2028", "\\u2028")
        .replace("\u2029", "\\u2029")
    )


def dumps(value, *, sort_keys: bool = True) -> str:
    """Use Go-compatible float notation, HTML escaping and optional map sorting.

    Pass sort_keys=False for a struct payload with its original field order;
    callers must order any nested map fields explicitly in that case.
    """
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, str):
        return _string(value)
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        if not math.isfinite(value):
            raise ValueError("non-finite number is not valid JSON")
        magnitude = abs(value)
        if value == 0:
            return "-0" if math.copysign(1, value) < 0 else "0"
        text = repr(value)
        if 1e-6 <= magnitude < 1e21:
            text = format(Decimal(text), "f")
            return text.rstrip("0").rstrip(".") if "." in text else text
        if "e" not in text:
            text = format(value, ".16e")
        mantissa, exponent = text.split("e")
        mantissa = mantissa.rstrip("0").rstrip(".") if "." in mantissa else mantissa
        number = int(exponent)
        return mantissa + "e" + ("+" if number >= 0 else "-") + str(abs(number))
    if isinstance(value, Mapping):
        if any(not isinstance(key, str) for key in value):
            raise TypeError("JSON object keys must be strings")
        keys = sorted(value) if sort_keys else value
        return (
            "{" + ",".join(_string(key) + ":" + dumps(value[key], sort_keys=sort_keys) for key in keys) + "}"
        )
    if isinstance(value, (list, tuple)):
        return "[" + ",".join(dumps(item, sort_keys=sort_keys) for item in value) + "]"
    raise TypeError(f"unsupported JSON value: {type(value).__name__}")
