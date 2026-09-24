"""OHLC providers, session-aware aggregation and existing drawing storage."""

import json
import sqlite3
from datetime import datetime, timedelta
from uuid import uuid4

from .common import (
    CN,
    MarketDataError,
    ProviderState,
    database_rows,
    envelope,
    evidence_instrument,
    instrument,
    now,
    number,
    security_id,
    timestamp,
)
from .news import array

PERIODS = {
    "1m": 1,
    "5m": 5,
    "15m": 15,
    "30m": 30,
    "60m": 60,
    "day": 1,
    "week": 6,
    "month": 23,
    "quarter": 66,
    "year": 260,
}


def valid_bars(rows, start, end):
    clean = {}
    for row in rows:
        at = timestamp(row["time"])
        if not start <= at <= end:
            continue
        values = [number(row.get(key)) for key in ("open", "high", "low", "close")]
        if any(value is None or value <= 0 for value in values):
            continue
        opening, high, low, close = (float(value) for value in values if value is not None)
        if high < max(opening, low, close) or low > min(opening, high, close):
            continue
        row["time"] = at.isoformat()
        clean[at] = row
    return [clean[key] for key in sorted(clean)]


def aggregate(rows, period):
    if period in {"1m", "day"}:
        return rows
    groups = {}
    for row in rows:
        at = timestamp(row["time"])
        if period.endswith("m"):
            minute = at.hour * 60 + at.minute
            session = 570 if 570 <= minute <= 690 else 780 if 780 <= minute <= 900 else None
            if session is None:
                continue
            bucket = min((minute - session) // PERIODS[period], 119 // PERIODS[period])
            key = (at.date(), session, bucket)
        elif period == "week":
            key = at.isocalendar()[:2]
        elif period == "month":
            key = (at.year, at.month)
        elif period == "quarter":
            key = (at.year, (at.month - 1) // 3)
        else:
            key = at.year
        if key not in groups:
            groups[key] = dict(row)
        else:
            result = groups[key]
            result.update(
                high=max(result["high"], row["high"]),
                low=min(result["low"], row["low"]),
                close=row["close"],
                volume=result["volume"] + row["volume"],
                amount=result["amount"] + row["amount"],
            )
    return list(groups.values())


class Charts(ProviderState):
    def cached_bars(self, code, start, end, period="1m", adjustment="none"):
        if not period.endswith("m") or adjustment != "none":
            raise MarketDataError("historical cache only proves unadjusted minute bars")
        return aggregate(
            self._cached_minute_bars(instrument(code)["code"], timestamp(start), timestamp(end)), period
        )

    def minute_line(self, code, name=""):
        code = instrument(code)["code"]
        endpoint = (
            "https://web.ifzq.gtimg.cn/appstock/app/UsMinute/query"
            if code.startswith("gb_")
            else "https://web.ifzq.gtimg.cn/appstock/app/minute/query"
        )
        data = self.http.json(endpoint, {"code": code})
        if data.get("code", 0) != 0:
            raise MarketDataError("minute-line source rejected request")
        payload = data.get("data", {}).get(code, {}).get("data", {})
        if not payload.get("date") or not isinstance(payload.get("data"), list):
            raise MarketDataError("minute-line source missing date or data")
        rows = []
        for line in payload["data"]:
            fields = line.split()
            if len(fields) < 3 or len(fields[0]) < 4:
                raise MarketDataError("malformed minute-line row")
            rows.append(
                {
                    "time": fields[0][:2] + ":" + fields[0][2:4],
                    "price": number(fields[1]),
                    "volume": number(fields[2]),
                    "amount": number(fields[3]) if len(fields) > 3 else 0,
                }
            )
        return {"priceData": rows, "date": payload["date"], "stockName": name, "stockCode": code}

    def _tencent_bars(self, code, start, end, period, adjustment, limit):
        minute = period.endswith("m")
        url = (
            "https://ifzq.gtimg.cn/appstock/app/kline/mkline"
            if minute
            else "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get"
        )
        unit = "m1" if minute else "day"
        count = min(1200 if minute else 12000, limit * PERIODS[period])
        params = {"param": f"{code},{unit},,,{count}" + ("" if minute else f",{adjustment}")}
        response = self.http.json(url, params, headers={"Referer": "https://gu.qq.com/"})
        if response.get("code", 0) != 0:
            raise MarketDataError("Tencent bar source rejected request")
        payload = response.get("data", {}).get(code, {})
        key = unit if minute or adjustment == "none" else adjustment + "day"
        rows = array(payload, key)
        result = []
        for row in rows:
            if len(row) < 6:
                continue
            at = (
                datetime.strptime(str(row[0]), "%Y%m%d%H%M").replace(tzinfo=CN)
                if minute
                else timestamp(row[0])
            )
            result.append(
                {
                    "time": at.isoformat(),
                    **{
                        field: number(row[index], 0)
                        for field, index in {"open": 1, "close": 2, "high": 3, "low": 4, "volume": 5}.items()
                    },
                    "amount": 0.0,
                    "source": "tencent:" + ("none" if minute else adjustment),
                }
            )
        return valid_bars(result, start, end)

    def _eastmoney_bars(self, code, start, end, period, adjustment, limit):
        minute = period.endswith("m")
        payload = self.http.json(
            "https://push2his.eastmoney.com/api/qt/stock/kline/get",
            {
                "secid": security_id(code),
                "klt": "1" if minute else "101",
                "fqt": {"none": 0, "qfq": 1, "hfq": 2}[adjustment],
                "beg": start.strftime("%Y%m%d"),
                "end": end.strftime("%Y%m%d"),
                "lmt": min(limit * PERIODS[period], 350000),
                "fields1": "f1,f2,f3,f4,f5,f6",
                "fields2": "f51,f52,f53,f54,f55,f56,f57",
            },
        )
        result = []
        for value in array(payload, "data", "klines"):
            row = value.split(",")
            if len(row) < 7:
                continue
            result.append(
                {
                    "time": timestamp(row[0]).isoformat(),
                    **{
                        field: number(row[index], 0)
                        for field, index in {
                            "open": 1,
                            "close": 2,
                            "high": 3,
                            "low": 4,
                            "volume": 5,
                            "amount": 6,
                        }.items()
                    },
                    "source": "eastmoney:" + adjustment,
                }
            )
        return valid_bars(result, start, end)

    def _sina_bars(self, code, start, end, period, adjustment, limit):
        if adjustment != "none":
            raise MarketDataError("Sina bars do not prove adjusted prices")
        rows = array(
            self.http.json(
                "https://quotes.sina.cn/cn/api/json_v2.php/CN_MarketDataService.getKLineData",
                {
                    "symbol": code,
                    "scale": 1 if period.endswith("m") else 240,
                    "ma": "no",
                    "datalen": min(limit * PERIODS[period], 12000),
                },
            )
        )
        result = [
            {
                "time": timestamp(row["day"]).isoformat(),
                **{
                    key: number(row.get(key), 0)
                    for key in ("open", "high", "low", "close", "volume", "amount")
                },
                "source": "sina:none",
            }
            for row in rows
        ]
        return valid_bars(result, start, end)

    def _cached_minute_bars(self, code, start, end):
        symbol = code[2:] + "." + code[:2].upper()
        rows = database_rows(
            self.config.minute_db,
            "SELECT * FROM minute_bar WHERE stock_code IN (?,?) AND trade_time>=? AND trade_time<=? ORDER BY trade_time",
            (symbol, code, int(start.timestamp() * 1000), int(end.timestamp() * 1000)),
        )
        result = [
            {
                "time": timestamp(row["trade_time"]).isoformat(),
                **{key: float(row[key] or 0) for key in ("open", "high", "low", "close", "volume", "amount")},
                "source": row["source"],
            }
            for row in rows
            if row.get("source") and "qfq" not in row["source"] and "hfq" not in row["source"]
        ]
        return valid_bars(result, start, end)

    def _save_minute_bars(self, code, rows):
        if not self.config.minute_db.is_file():
            raise MarketDataError("minute cache database unavailable")
        symbol = code[2:] + "." + code[:2].upper()
        try:
            with sqlite3.connect(self.config.minute_db, timeout=10) as connection:
                connection.executemany(
                    "INSERT INTO minute_bar(stock_code,trade_time,open,high,low,close,volume,amount,source,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(stock_code,trade_time) DO UPDATE SET open=excluded.open,high=excluded.high,low=excluded.low,close=excluded.close,volume=excluded.volume,amount=excluded.amount,source=excluded.source,updated_at=excluded.updated_at",
                    [
                        (
                            symbol,
                            int(timestamp(row["time"]).timestamp() * 1000),
                            *(row[key] for key in ("open", "high", "low", "close", "volume", "amount")),
                            row["source"],
                            int(now().timestamp() * 1000),
                        )
                        for row in rows
                    ],
                )
        except sqlite3.Error as exc:
            raise MarketDataError("minute cache write failed") from exc

    def _private_bars(self, code, start, end):
        base = str(self.settings.get("privateMinuteBaseUrl") or "https://mg.diemeng.chat/api").rstrip("/")
        result = []
        for page in range(1, 51):
            payload = self.http.json(
                base + "/stock/history",
                method="POST",
                headers={"apiKey": self.settings.get("privateMinuteApiKey", "")},
                body={
                    "stock_code": code[2:],
                    "level": "1min",
                    "start_time": start.strftime("%Y-%m-%d %H:%M:%S"),
                    "end_time": end.strftime("%Y-%m-%d %H:%M:%S"),
                    "page": page,
                    "page_size": 5000,
                },
            )
            if payload.get("code", 0) not in (0, 200):
                raise MarketDataError("private minute provider rejected request")
            data = payload.get("data") or {}
            items = data.get("items", data.get("list"))
            if not isinstance(items, list):
                raise MarketDataError("private minute data has no items")
            for row in items:
                result.append(
                    {
                        "time": timestamp(row["trade_time"]).isoformat(),
                        **{
                            key: number(row.get(key), 0) for key in ("open", "high", "low", "close", "amount")
                        },
                        "volume": number(row.get("vol"), 0),
                        "source": "private-minute:none",
                    }
                )
            if len(result) >= int(data.get("total", len(result))) or not items:
                return valid_bars(result, start, end)
        raise MarketDataError("private minute pagination exceeded 50 pages")

    def bars(self, code, start, end, period="1m", adjustment="none", limit=5000):
        return self._bars_with_source(code, start, end, period, adjustment, limit)[0]

    def _bars_with_source(self, code, start, end, period, adjustment, limit):
        start, end = timestamp(start), timestamp(end)
        if period not in PERIODS or adjustment not in {"none", "qfq", "hfq"} or start > end:
            raise ValueError("invalid bar period, adjustment or range")
        code = instrument(code)["code"]
        if period.endswith("m") and adjustment != "none":
            raw, source, failures = self._bars_with_source(code, start, end, period, "none", limit)
            daily_start = start.replace(hour=0, minute=0, second=0, microsecond=0)
            daily_end = end.replace(hour=23, minute=59, second=59, microsecond=0)
            daily_count = max(300, (daily_end - daily_start).days + 20)
            unadjusted, _, raw_failures = self._bars_with_source(
                code, daily_start, daily_end, "day", "none", daily_count
            )
            adjusted, _, adjusted_failures = self._bars_with_source(
                code, daily_start, daily_end, "day", adjustment, daily_count
            )
            failures.extend(raw_failures + adjusted_failures)
            raw_closes = {row["time"][:10]: row["close"] for row in unadjusted}
            adjusted_closes = {row["time"][:10]: row["close"] for row in adjusted}
            result, missing = [], set()
            for bar in raw:
                day = bar["time"][:10]
                if not raw_closes.get(day) or not adjusted_closes.get(day):
                    missing.add(day)
                    continue
                ratio = adjusted_closes[day] / raw_closes[day]
                result.append(
                    bar
                    | {
                        **{key: bar[key] * ratio for key in ("open", "high", "low", "close")},
                        "source": bar["source"] + "|adjustment=" + adjustment,
                    }
                )
            for day in sorted(missing):
                failures.append(
                    {
                        "provider": "adjustment",
                        "code": "factor_unavailable",
                        "message": day + " has no verified daily adjustment ratio",
                    }
                )
            if not result:
                raise MarketDataError("minute adjustment factors unavailable")
            return result, source, failures
        failures = []
        sources = []
        minute = period.endswith("m")
        if minute and adjustment == "none":
            sources.append(("local-minute-cache", lambda: self._cached_minute_bars(code, start, end)))
            if self.settings.get("privateMinuteEnabled"):
                sources.append(("private-minute", lambda: self._private_bars(code, start, end)))
        # Tencent minute payloads are always unadjusted. Never relabel them qfq/hfq.
        if not minute or adjustment == "none":
            sources.append(
                ("tencent", lambda: self._tencent_bars(code, start, end, period, adjustment, limit))
            )
        if adjustment == "none":
            sources.append(("sina", lambda: self._sina_bars(code, start, end, period, adjustment, limit)))
        sources.append(
            ("eastmoney", lambda: self._eastmoney_bars(code, start, end, period, adjustment, limit))
        )
        for source, load in sources:
            try:
                rows = load()
                if not rows:
                    raise MarketDataError("no bars in requested range")
                if source == "local-minute-cache":
                    effective_start = max(start, start.replace(hour=9, minute=30, second=0, microsecond=0))
                    effective_end = min(end, end.replace(hour=15, minute=0, second=0, microsecond=0))
                    if timestamp(rows[0]["time"]) > effective_start + timedelta(minutes=1) or timestamp(
                        rows[-1]["time"]
                    ) < effective_end - timedelta(minutes=1):
                        raise MarketDataError("minute cache does not cover requested window")
                elif minute and adjustment == "none":
                    try:
                        self._save_minute_bars(code, rows)
                    except MarketDataError as exc:
                        failures.append(
                            {
                                "provider": "local-minute-cache",
                                "code": "cache_write_failed",
                                "message": str(exc),
                            }
                        )
                return aggregate(rows, period)[-limit:], source, failures
            except (MarketDataError, ValueError, KeyError, TypeError) as exc:
                failures.append({"provider": source, "code": "unavailable", "message": str(exc)})
        raise MarketDataError("all bar providers failed: " + "; ".join(item["message"] for item in failures))

    def price_at(self, code, at, sell=False):
        at = timestamp(at)
        current = now()
        if abs((current - at).total_seconds()) <= 180:
            quote = self.quote(code)
            quoted = timestamp(quote["asOf"])
            if (
                quoted < at
                or quoted > current + timedelta(seconds=5)
                or current - quoted > timedelta(minutes=3)
            ):
                raise MarketDataError("live quote is outside execution window")
            return quote | {"quoteAt": quote["asOf"], "mode": "live_after_signal"}
        bars = self.bars(code, at, min(at + timedelta(minutes=15), current), "1m", "none")
        if not bars:
            raise MarketDataError("historical execution minute is unavailable")
        bar = bars[0]
        return {
            "code": instrument(code)["code"],
            "price": bar["open"],
            "asOf": bar["time"],
            "quoteAt": bar["time"],
            "source": bar["source"],
            "status": "ok",
            "mode": "historical_minute",
        }

    def daily_closes(self, code, start, end):
        start = timestamp(start).replace(hour=0, minute=0, second=0, microsecond=0)
        end = timestamp(end).replace(hour=23, minute=59, second=59, microsecond=0)
        rows = self.bars(code, start, end, "day", "none", max(30, (end - start).days + 20))
        return [
            {"tradingDate": row["time"][:10], "close": row["close"], "source": row["source"]} for row in rows
        ]

    def buy_day_data(self, recommendation):
        if not recommendation.get("buyAt"):
            raise MarketDataError("buy-day data requires buyAt")
        day = timestamp(recommendation["buyAt"])
        previous = None
        for offset in range(1, 21):
            candidate = day - timedelta(days=offset)
            if self.is_trading_day(candidate):
                previous = candidate.date().isoformat()
                break
        if previous is None:
            raise MarketDataError("previous trading day unavailable")
        code = recommendation["stockCode"]
        daily = self.daily_closes(code, day - timedelta(days=40), day)
        close = next((row["close"] for row in daily if row["tradingDate"] == previous), None)
        if not close:
            raise MarketDataError("verified previous trading-day close unavailable")
        bars = self.bars(
            code,
            day.replace(hour=9, minute=30, second=0, microsecond=0),
            day.replace(hour=15, minute=0, second=0, microsecond=0),
            "1m",
            "none",
        )
        result = {
            "previousClose": close,
            "bars": bars,
            "limitRate": 0.0,
            "noLimitReason": "",
            "sourceStatusJson": json.dumps(
                {"sources": sorted({row["source"] for row in bars}), "daily": daily, "errors": []}
            ),
        }
        if len(daily) < 6:
            result["noLimitReason"] = (
                "fewer than six verified pre-buy daily sessions; price-limit rule is unavailable"
            )
        else:
            normalized = instrument(code)["code"]
            name = recommendation.get("stockName", "").strip().upper()
            result["limitRate"] = (
                0.05
                if name.startswith(("ST", "*ST"))
                else 0.2
                if normalized.startswith(("sh68", "sz30"))
                else 0.3
                if normalized.startswith("bj")
                else 0.1
            )
        return result

    def chart(
        self,
        code,
        asset_type="stock",
        market="",
        period="day",
        adjustment="",
        start=None,
        end=None,
        limit=500,
    ):
        identity = evidence_instrument(code, asset_type, market)
        if period not in PERIODS or not 1 <= limit <= 5000:
            raise ValueError("invalid chart period or limit")
        adjustment = adjustment or ("qfq" if asset_type == "stock" else "none")
        if adjustment not in {"none", "qfq", "hfq"} or asset_type == "index" and adjustment != "none":
            raise ValueError("invalid chart adjustment")
        end = timestamp(end or now())
        start = (
            timestamp(start)
            if start
            else end - timedelta(minutes=limit * PERIODS[period] * 3)
            if period.endswith("m")
            else end - timedelta(days=limit * PERIODS[period] * 2)
        )
        if start > end:
            raise ValueError("from must not be after to")
        data = {
            "instrument": identity,
            "period": period,
            "adjustment": adjustment,
            "timezone": "Asia/Shanghai",
            "rangeFrom": start.isoformat(),
            "rangeTo": end.isoformat(),
            "bars": [],
            "missingIntervals": [],
        }
        try:
            rows, source, errors = self._bars_with_source(code, start, end, period, adjustment, limit)
            data["bars"] = rows
            return envelope(
                data, source, as_of=rows[-1]["time"], errors=errors, status="partial" if errors else "ok"
            )
        except MarketDataError as exc:
            data["missingIntervals"] = [
                {"from": start.isoformat(), "to": end.isoformat(), "reason": str(exc)}
            ]
            return envelope(
                data,
                "",
                status="unavailable",
                errors=[{"provider": "chart", "code": "unavailable", "message": str(exc)}],
            )

    def drawings(
        self,
        code,
        asset_type,
        market,
        period,
        adjustment,
        *,
        expected_revision=None,
        drawings=None,
        delete=False,
    ):
        identity = evidence_instrument(code, asset_type, market)
        if period not in PERIODS or adjustment not in {"none", "qfq", "hfq"}:
            raise ValueError("invalid drawing scope")
        key = ("user", "local", asset_type, identity["market"], identity["code"], period, adjustment)
        where = "scope_type=? AND scope_id=? AND asset_type=? AND market=? AND code=? AND period=? AND adjustment=?"
        if expected_revision is None:
            rows = database_rows(
                self.config.main_db, "SELECT * FROM chart_drawing_documents WHERE " + where, key
            )
            row = rows[0] if rows else None
        else:
            if expected_revision < 0:
                raise ValueError("expectedRevision must be non-negative")
            if not delete and (not isinstance(drawings, list) or len(drawings) > 500):
                raise ValueError("drawings must be an array of up to 500 entries")
            if not delete:
                assert isinstance(drawings, list)
                ids = set()
                for item in drawings:
                    if (
                        not isinstance(item, dict)
                        or not item.get("id")
                        or len(item["id"].encode()) > 128
                        or item["id"] in ids
                        or item.get("type")
                        not in {
                            "measure",
                            "trend_line",
                            "ray",
                            "fibonacci_retracement",
                            "horizontal_line",
                            "wave",
                        }
                    ):
                        raise ValueError("invalid or duplicate drawing")
                    ids.add(item["id"])
                    minimum, maximum = (
                        (1, 1)
                        if item["type"] == "horizontal_line"
                        else (3, 64)
                        if item["type"] == "wave"
                        else (2, 2)
                    )
                    if not minimum <= len(item.get("points", [])) <= maximum:
                        raise ValueError("invalid drawing point count")
                    for point in item.get("points", []):
                        timestamp(point["time"])
                        if number(point.get("value")) is None:
                            raise ValueError("invalid drawing point")
            payload = json.dumps([] if delete else drawings, ensure_ascii=False)
            if len(payload.encode()) > 256 * 1024:
                raise ValueError("drawing payload exceeds 256 KiB")
            with sqlite3.connect(self.config.main_db, timeout=10) as connection:
                connection.row_factory = sqlite3.Row
                connection.execute("BEGIN IMMEDIATE")
                current = connection.execute(
                    "SELECT * FROM chart_drawing_documents WHERE " + where, key
                ).fetchone()
                if (current["revision"] if current else 0) != expected_revision:
                    raise ValueError("chart drawing revision conflict")
                if delete and not current:
                    raise LookupError("chart drawing document not found")
                doc_id = current["drawing_document_id"] if current else str(uuid4())
                at = now().isoformat()
                revision = expected_revision + 1
                deleted = at if delete else None
                if current:
                    connection.execute(
                        "UPDATE chart_drawing_documents SET revision=?,drawings_json=?,deleted_at=?,updated_at=? WHERE "
                        + where,
                        (revision, payload, deleted, at) + key,
                    )
                else:
                    connection.execute(
                        "INSERT INTO chart_drawing_documents(drawing_document_id,scope_type,scope_id,asset_type,market,code,period,adjustment,revision,drawings_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
                        (doc_id,) + key + (revision, payload, at, at),
                    )
                connection.execute(
                    "INSERT INTO chart_drawing_revisions(document_id,revision,drawings_json,deleted_at,created_at) VALUES (?,?,?,?,?)",
                    (doc_id, revision, payload, deleted, at),
                )
                row = dict(
                    connection.execute("SELECT * FROM chart_drawing_documents WHERE " + where, key).fetchone()
                )
        return {
            "instrument": identity,
            "period": period,
            "adjustment": adjustment,
            "revision": row["revision"] if row else 0,
            "drawings": json.loads(row["drawings_json"]) if row else [],
            "updatedAt": row["updated_at"] if row else "0001-01-01T00:00:00Z",
            **({"deletedAt": row["deleted_at"]} if row and row["deleted_at"] else {}),
        }
