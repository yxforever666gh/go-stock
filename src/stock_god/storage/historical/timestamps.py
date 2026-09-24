"""Nanosecond timestamps used by frozen Go financial ledgers."""

from datetime import datetime, timedelta, timezone
import re


def time_key(value):
    if not value:
        return -(10**30)
    text = str(value).strip().replace(" ", "T", 1).replace("Z", "+00:00")
    match = re.fullmatch(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?([+-]\d\d:\d\d)?", text)
    if not match:
        raise ValueError("unsupported persisted timestamp: " + text)
    whole = datetime.fromisoformat(match[1] + (match[3] or "+08:00"))
    epoch = datetime(1970, 1, 1, tzinfo=timezone.utc)
    seconds = (whole.astimezone(timezone.utc) - epoch).days * 86400 + (
        whole.astimezone(timezone.utc) - epoch
    ).seconds
    return seconds * 1_000_000_000 + int((match[2] or "").ljust(9, "0")[:9])


def before_one_nanosecond(value):
    key = time_key(value) - 1
    seconds, ns = divmod(key, 1_000_000_000)
    whole = datetime(1970, 1, 1, tzinfo=timezone.utc) + timedelta(seconds=seconds)
    return whole.strftime("%Y-%m-%dT%H:%M:%S") + f".{ns:09d}Z"
