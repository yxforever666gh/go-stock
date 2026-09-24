"""Nanosecond timestamps used by frozen Go financial ledgers."""

import re
from datetime import UTC, datetime, timedelta


def time_key(value):
    if not value:
        return -(10**30)
    text = str(value).strip().replace(" ", "T", 1).replace("Z", "+00:00")
    match = re.fullmatch(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?([+-]\d\d:\d\d)?", text)
    if not match:
        raise ValueError("unsupported persisted timestamp: " + text)
    whole = datetime.fromisoformat(match[1] + (match[3] or "+08:00"))
    epoch = datetime(1970, 1, 1, tzinfo=UTC)
    seconds = (whole.astimezone(UTC) - epoch).days * 86400 + (
        whole.astimezone(UTC) - epoch
    ).seconds
    return seconds * 1_000_000_000 + int((match[2] or "").ljust(9, "0")[:9])


def before_one_nanosecond(value):
    key = time_key(value) - 1
    seconds, ns = divmod(key, 1_000_000_000)
    whole = datetime(1970, 1, 1, tzinfo=UTC) + timedelta(seconds=seconds)
    return whole.strftime("%Y-%m-%dT%H:%M:%S") + f".{ns:09d}Z"
