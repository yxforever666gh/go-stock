#!/usr/bin/env python3
import json
import os
import re
import signal
import sys
from datetime import datetime


def _to_float(v):
    try:
        if v is None:
            return 0.0
        return float(v)
    except Exception:
        return 0.0


def _pick_column(columns, candidates):
    for name in candidates:
        if name in columns:
            return name
    return None


def _normalize_time(v):
    if v is None:
        return ""
    if isinstance(v, str):
        text = v.strip()
        if not text:
            return ""
        for fmt in (
            "%Y-%m-%d %H:%M:%S",
            "%Y-%m-%d %H:%M",
            "%Y/%m/%d %H:%M:%S",
            "%Y/%m/%d %H:%M",
            "%Y%m%d%H%M%S",
            "%Y%m%d%H%M",
        ):
            try:
                dt = datetime.strptime(text, fmt)
                return dt.strftime("%Y-%m-%d %H:%M:%S")
            except Exception:
                continue
        return text
    try:
        return v.to_pydatetime().strftime("%Y-%m-%d %H:%M:%S")
    except Exception:
        try:
            return datetime.fromtimestamp(v.timestamp()).strftime("%Y-%m-%d %H:%M:%S")
        except Exception:
            return str(v)

def _parse_dt(text: str):
    if text is None:
        return None
    raw = str(text).strip()
    if not raw:
        return None
    for fmt in (
        "%Y-%m-%d %H:%M:%S",
        "%Y-%m-%d %H:%M",
        "%Y/%m/%d %H:%M:%S",
        "%Y/%m/%d %H:%M",
        "%Y%m%d%H%M%S",
        "%Y%m%d%H%M",
        "%Y-%m-%d",
        "%Y/%m/%d",
        "%Y%m%d",
    ):
        try:
            return datetime.strptime(raw, fmt)
        except Exception:
            continue
    return None


def _to_ymd(text: str):
    dt = _parse_dt(text)
    if dt is None:
        return ""
    return dt.strftime("%Y%m%d")


def main():
    # Avoid noisy "BrokenPipeError: [Errno 32]" when stdout is piped and the
    # consumer exits early (e.g. `| head`). This is safe for CLI usage.
    try:
        signal.signal(signal.SIGPIPE, signal.SIG_DFL)
    except Exception:
        pass

    if len(sys.argv) < 4:
        print("[]")
        return

    raw_symbol = (sys.argv[1] or "").strip()
    start_date = (sys.argv[2] or "").strip()
    end_date = (sys.argv[3] or "").strip()

    if not raw_symbol or not start_date or not end_date:
        print("[]")
        return

    try:
        import akshare as ak
    except Exception as exc:
        sys.stderr.write(f"import akshare failed: {exc}\n")
        sys.exit(1)

    digits = "".join(re.findall(r"\d", raw_symbol))
    if len(digits) != 6:
        sys.stderr.write(f"invalid symbol: {raw_symbol}\n")
        sys.exit(1)

    upper = raw_symbol.strip().upper()
    if ".SH" in upper or upper.startswith("SH"):
        sina_symbol = "sh" + digits
    elif ".SZ" in upper or upper.startswith("SZ"):
        sina_symbol = "sz" + digits
    else:
        # Best-effort for plain digits: 6/9 -> sh, others -> sz
        sina_symbol = ("sh" if digits.startswith(("6", "9")) else "sz") + digits

    source = (os.getenv("GO_STOCK_AKSHARE_MINUTE_SOURCE") or "sina").strip().lower()
    adjust = (os.getenv("GO_STOCK_AKSHARE_MINUTE_ADJUST") or "").strip().lower()
    if adjust not in ("", "qfq", "hfq"):
        adjust = ""

    start_ymd = _to_ymd(start_date)
    end_ymd = _to_ymd(end_date)
    today_ymd = datetime.now().strftime("%Y%m%d")
    # If the request spans multiple days or isn't for "today", prefer EM history API.
    prefer_em = False
    if start_ymd and end_ymd:
        prefer_em = (start_ymd != end_ymd) or (start_ymd != today_ymd) or (end_ymd != today_ymd)

    def fetch_sina():
        return ak.stock_zh_a_minute(symbol=sina_symbol, period="1", adjust=adjust)

    def fetch_em():
        # AkShare doc notes 1-minute data is recent-only and does not support adjust.
        em_adjust = ""
        return ak.stock_zh_a_hist_min_em(
            symbol=digits,
            start_date=start_ymd or start_date,
            end_date=end_ymd or end_date,
            period="1",
            adjust=em_adjust,
        )

    df = None
    last_exc = None
    if source == "sina":
        try:
            df = fetch_sina()
        except Exception as exc:
            last_exc = exc
    elif source == "em":
        try:
            df = fetch_em()
        except Exception as exc:
            last_exc = exc
    else:
        # auto: choose provider based on requested time window.
        # - historical/multi-day: prefer EM
        # - intraday today: prefer Sina
        first = fetch_em if prefer_em else fetch_sina
        second = fetch_sina if prefer_em else fetch_em
        try:
            df = first()
        except Exception as exc:
            last_exc = exc
            try:
                df = second()
                last_exc = None
            except Exception as exc2:
                last_exc = exc2

    if df is None and last_exc is not None:
        sys.stderr.write(f"akshare fetch failed (source={source}, symbol={raw_symbol}): {last_exc}\n")
        sys.exit(1)

    if df is None or getattr(df, "empty", True):
        print("[]")
        return

    columns = list(df.columns)
    time_col = _pick_column(columns, ["时间", "日期", "day", "time"])
    open_col = _pick_column(columns, ["开盘", "open"])
    high_col = _pick_column(columns, ["最高", "high"])
    low_col = _pick_column(columns, ["最低", "low"])
    close_col = _pick_column(columns, ["收盘", "close"])
    volume_col = _pick_column(columns, ["成交量", "volume", "vol"])
    amount_col = _pick_column(columns, ["成交额", "amount"])

    if not time_col or not open_col or not high_col or not low_col or not close_col:
        sys.stderr.write(f"unexpected columns: {columns}\n")
        sys.exit(1)

    results = []
    for _, row in df.iterrows():
        trade_time = _normalize_time(row.get(time_col))
        if not trade_time:
            continue
        # Filter by requested window since some providers only return recent days.
        # Keep string comparison fallback if parsing fails.
        if start_date and trade_time < start_date:
            continue
        if end_date and trade_time > end_date:
            continue
        results.append(
            {
                "trade_time": trade_time,
                "open": _to_float(row.get(open_col)),
                "high": _to_float(row.get(high_col)),
                "low": _to_float(row.get(low_col)),
                "close": _to_float(row.get(close_col)),
                "volume": _to_float(row.get(volume_col)) if volume_col else 0.0,
                "amount": _to_float(row.get(amount_col)) if amount_col else 0.0,
            }
        )

    # If auto mode and the preferred provider returned no rows in the requested
    # window, retry with the other provider once (common: Sina only has recent minutes).
    if source not in ("sina", "em") and len(results) == 0:
        try:
            df2 = (fetch_sina() if prefer_em else fetch_em)
            if df2 is not None and not getattr(df2, "empty", True):
                columns2 = list(df2.columns)
                time_col2 = _pick_column(columns2, ["时间", "日期", "day", "time"])
                open_col2 = _pick_column(columns2, ["开盘", "open"])
                high_col2 = _pick_column(columns2, ["最高", "high"])
                low_col2 = _pick_column(columns2, ["最低", "low"])
                close_col2 = _pick_column(columns2, ["收盘", "close"])
                volume_col2 = _pick_column(columns2, ["成交量", "volume", "vol"])
                amount_col2 = _pick_column(columns2, ["成交额", "amount"])
                if time_col2 and open_col2 and high_col2 and low_col2 and close_col2:
                    for _, row in df2.iterrows():
                        trade_time = _normalize_time(row.get(time_col2))
                        if not trade_time:
                            continue
                        if start_date and trade_time < start_date:
                            continue
                        if end_date and trade_time > end_date:
                            continue
                        results.append(
                            {
                                "trade_time": trade_time,
                                "open": _to_float(row.get(open_col2)),
                                "high": _to_float(row.get(high_col2)),
                                "low": _to_float(row.get(low_col2)),
                                "close": _to_float(row.get(close_col2)),
                                "volume": _to_float(row.get(volume_col2)) if volume_col2 else 0.0,
                                "amount": _to_float(row.get(amount_col2)) if amount_col2 else 0.0,
                            }
                        )
        except Exception:
            pass

    try:
        print(json.dumps(results, ensure_ascii=False))
    except BrokenPipeError:
        # When stdout consumer exits early (e.g., piping to head), avoid noisy tracebacks.
        pass


if __name__ == "__main__":
    main()
