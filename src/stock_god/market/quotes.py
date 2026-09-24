"""Tencent/Sina quotes, stock master and strict exchange calendar."""

from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta
import re

from .common import CN, MarketDataError, database_rows, instrument, now, number, timestamp


def parse_tencent(text: str) -> list[dict]:
    quotes = []
    for code, payload in re.findall(r'v_(?:r_)?([a-z0-9_]+)="([^"]*)"', text):
        parts = [part.strip() for part in payload.split("~")]
        if len(parts) < 38:
            continue
        at = timestamp(parts[30] if len(parts[30]) == 14 or "/" in parts[30] else parts[29])
        quote = {"code": code, "name": parts[1], "price": number(parts[3]), "preClose": number(parts[4]),
                 "open": number(parts[5]), "high": number(parts[33]), "low": number(parts[34]),
                 "asOf": at.isoformat(), "source": "tencent", "status": "ok"}
        if any(quote[key] is None or quote[key] < 0 for key in ("price", "preClose", "open", "high", "low")):
            raise MarketDataError("Tencent quote contains invalid prices")
        high, low = quote["high"], quote["low"]
        if (high or low) and (not high or not low or high < low or
                             any(value > 0 and not low <= value <= high for value in (quote["price"], quote["open"]))):
            raise MarketDataError("Tencent OHLC is inconsistent")
        if code.startswith("hk"):
            quote.update(volume=number(parts[36]), amount=number(parts[37]))
        else:
            composite = parts[35].split("/")
            if len(composite) != 3 or number(composite[1]) is None or number(composite[2]) is None:
                raise MarketDataError("Tencent turnover fields are invalid")
            quote.update(volume=number(composite[1]) * 100, amount=number(composite[2]))
            for level in range(5):
                quote[f"bid{level+1}"] = number(parts[9 + level*2])
                quote[f"bidVolume{level+1}"] = number(parts[10 + level*2])
                quote[f"ask{level+1}"] = number(parts[19 + level*2])
                quote[f"askVolume{level+1}"] = number(parts[20 + level*2])
        quote["changePct"] = (quote["price"] / quote["preClose"] - 1) * 100 if quote["preClose"] else None
        quotes.append(quote)
    if not quotes:
        raise MarketDataError("Tencent returned no valid quotes")
    return quotes


def parse_sina(text: str) -> list[dict]:
    quotes = []
    for code, payload in re.findall(r'hq_str_([a-z0-9_.-]+)="([^"]*)"', text):
        values = [part.strip() for part in payload.split(",")]
        if code.startswith("hk") and len(values) >= 19:
            mapping = {"name": 1, "open": 2, "preClose": 3, "high": 4, "low": 5, "price": 6,
                       "volume": 12, "amount": 11}
            at = timestamp(values[17] + " " + values[18])
        elif code.startswith("gb_") and len(values) >= 35:
            mapping = {"name": 0, "open": 5, "preClose": 26, "high": 6, "low": 7, "price": 1,
                       "volume": 10, "amount": 30}
            at = datetime.fromisoformat(values[3]).replace(tzinfo=__import__("zoneinfo").ZoneInfo("America/New_York"))
        elif code.startswith(("sh", "sz", "bj")) and len(values) >= 32:
            mapping = {"name": 0, "open": 1, "preClose": 2, "high": 4, "low": 5, "price": 3,
                       "volume": 8, "amount": 9}
            at = timestamp(values[30] + " " + values[31])
        else:
            continue
        quote = {key: values[index] if key == "name" else number(values[index]) for key, index in mapping.items()}
        if quote["price"] is None or quote["preClose"] is None:
            raise MarketDataError("Sina returned invalid quote prices")
        quote.update(code=code, asOf=at.astimezone(CN).isoformat(), source="sina", status="ok")
        quote["changePct"] = (quote["price"] / quote["preClose"] - 1) * 100 if quote["preClose"] else None
        if code.startswith(("sh", "sz", "bj")):
            for level in range(5):
                quote[f"bidVolume{level+1}"] = number(values[10 + level*2])
                quote[f"bid{level+1}"] = number(values[11 + level*2])
                quote[f"askVolume{level+1}"] = number(values[20 + level*2])
                quote[f"ask{level+1}"] = number(values[21 + level*2])
        quotes.append(quote)
    if not quotes:
        raise MarketDataError("Sina returned no valid quotes")
    return quotes


class Quotes:
    def quotes(self, codes: list[str]) -> list[dict]:
        normalized = list(dict.fromkeys(instrument(code)["code"] for code in codes))
        found = {}
        failures = []
        for provider, url, parameter, parser in (
            ("tencent", "https://qt.gtimg.cn/", "q", parse_tencent),
            ("sina", "https://hq.sinajs.cn/", "list", parse_sina),
        ):
            missing = [code for code in normalized if code not in found]
            if provider == "tencent":
                missing = [code for code in missing if not code.startswith("gb_")]
            for offset in range(0, len(missing), 80):
                chunk = missing[offset:offset+80]
                try:
                    rows = parser(self.http.text(url, {parameter: ",".join(chunk)}, encoding="gb18030"))
                    for quote in rows:
                        if quote["code"] in chunk:
                            found[quote["code"]] = quote
                except (MarketDataError, ValueError) as exc:
                    failures.append(f"{provider}: {exc}")
        missing = [code for code in normalized if code not in found]
        if missing:
            raise MarketDataError(f"quotes unavailable for {', '.join(missing[:5])}; {'; '.join(failures)}")
        return [found[code] for code in normalized]

    def quote(self, code: str) -> dict:
        normalized = instrument(code)["code"]
        return self.http.cached("quote:" + normalized, 2, lambda: self.quotes([normalized])[0])

    def stock_master(self) -> list[dict]:
        return database_rows(self.config.main_db, "SELECT * FROM tushare_stock_basic WHERE deleted_at IS NULL")

    def stock_search(self, key: str) -> list[dict]:
        pattern = "%" + key + "%"
        output = database_rows(self.config.main_db,
                               "SELECT * FROM tushare_stock_basic WHERE deleted_at IS NULL AND (name LIKE ? OR ts_code LIKE ?)",
                               (pattern, pattern))
        tables = {row["name"] for row in database_rows(self.config.main_db, "SELECT name FROM sqlite_master WHERE type='table'")}
        for table, code_field, market in (("tushare_index_basic", "ts_code", "A"), ("stock_info_hk", "code", "HK"),
                                           ("stock_info_us", "code", "US")):
            if table not in tables:
                continue
            for row in database_rows(self.config.main_db, f"SELECT * FROM {table} WHERE name LIKE ? OR {code_field} LIKE ?", (pattern, pattern)):
                code = row[code_field]
                if market == "US":
                    code = code.lower().replace("us", "gb_", 1)
                output.append({"ts_code": code, "name": row["name"], "fullname": row.get("full_name", row["name"]),
                               "market": row.get("market", market), "symbol": row.get("symbol", "")})
        return output

    def stock_snapshot(self, code: str) -> dict:
        quote = self.quote(code)
        mapping = {"code": "股票代码", "name": "股票名称", "price": "当前价格", "volume": "成交的股票数",
                   "amount": "成交金额", "open": "今日开盘价", "preClose": "昨日收盘价", "high": "今日最高价", "low": "今日最低价"}
        output = {label: str(quote.get(key, "")) for key, label in mapping.items()}
        at = timestamp(quote["asOf"])
        output.update({"日期": at.date().isoformat(), "时间": at.strftime("%H:%M:%S"),
                       "市场": "HK" if code.startswith("hk") else "US" if code.startswith("gb_") else "A",
                       "changePercent": quote["changePct"], "changePrice": quote["price"] - quote["preClose"],
                       "source": quote["source"], "asOf": quote["asOf"]})
        for index, chinese in enumerate("一二三四五", 1):
            for key, label in (("bid", "买"), ("ask", "卖")):
                output[label+chinese+"报价"] = str(quote.get(f"{key}{index}", ""))
                output[label+chinese+"申报"] = str(quote.get(f"{key}Volume{index}", ""))
        # Legacy watchlist fields are optional read-only metadata. No follow-price update.
        table = database_rows(self.config.main_db, "SELECT name FROM sqlite_master WHERE type='table' AND name='followed_stock'")
        if table:
            rows = database_rows(self.config.main_db, "SELECT * FROM followed_stock WHERE stock_code=? AND is_del=0 LIMIT 1", (quote["code"],))
            if rows:
                for key, column in (("costPrice", "cost_price"), ("costVolume", "volume"), ("sort", "sort"),
                                    ("alarmPrice", "alarm_price"), ("alarmChangePercent", "alarm_change_percent")):
                    output[key] = rows[0].get(column)
        return output

    def is_trading_day(self, at: datetime) -> bool:
        day = timestamp(at)
        if day.weekday() >= 5:
            return False
        def load():
            token = self.settings.get("tushareToken", "")
            if not token:
                raise MarketDataError("trade calendar unavailable: Tushare token is empty")
            result = self.http.json("https://api.tushare.pro", method="POST", body={"api_name": "trade_cal", "token": token,
                "params": {"exchange": "SSE", "start_date": (day-timedelta(days=550)).strftime("%Y%m%d"),
                           "end_date": (day+timedelta(days=550)).strftime("%Y%m%d")}, "fields": "exchange,cal_date,is_open"})
            if result.get("code") != 0:
                raise MarketDataError("trade calendar provider rejected request")
            data = result.get("data") or {}
            rows = [dict(zip(data.get("fields", []), row)) for row in data.get("items", [])]
            if not rows:
                raise MarketDataError("empty trade calendar")
            return {datetime.strptime(row["cal_date"], "%Y%m%d").date().isoformat(): str(row["is_open"]) == "1" for row in rows}
        calendar = self.http.cached("calendar:" + str(day.year), 21600, load)
        if day.date().isoformat() not in calendar:
            raise MarketDataError("trade calendar does not cover requested date")
        return calendar[day.date().isoformat()]

    def full_market(self) -> dict:
        fields = "f2,f3,f5,f6,f8,f12,f13,f14,f15,f16,f17,f18,f26,f62,f124"
        base = {"po": "1", "fltt": "2", "invt": "2", "fid": "f3", "fields": fields,
                "fs": "m:1+t:2,m:0+t:6,b:MK0021,b:MK0022,b:MK0023,b:MK0024"}
        errors = []
        def page(n, size, shape):
            for host in ("82.push2", "80.push2", "push2delay"):
                try:
                    response = self.http.json(f"https://{host}.eastmoney.com/api/qt/clist/get",
                                              base | {"pn": n, "pz": size, "np": shape})
                    data = response.get("data") or {}
                    diff = data.get("diff")
                    rows = list(diff.values()) if isinstance(diff, dict) else diff
                    if not rows or not data.get("total"):
                        raise MarketDataError("empty market universe")
                    return rows, int(data["total"])
                except (MarketDataError, TypeError, ValueError) as exc:
                    errors.append(str(exc))
            raise MarketDataError("Eastmoney full-market sources unavailable")
        try:
            rows, total = page(1, 20000, 2)
            if len(rows) < total:
                rows, total = page(1, 100, 1)
                with ThreadPoolExecutor(max_workers=8) as pool:
                    for batch, _ in pool.map(lambda index: page(index, 100, 1), range(2, (total+99)//100+1)):
                        rows.extend(batch)
            mapped = []
            for row in rows:
                code = str(row["f12"])
                mapped.append({"code": instrument(code)["code"], "name": row.get("f14", ""),
                               **{key: number(row.get(field)) for key, field in {
                                   "price": "f2", "changePct": "f3", "volume": "f5", "amount": "f6", "turnover": "f8",
                                   "high": "f15", "low": "f16", "open": "f17", "preClose": "f18", "mainFlow": "f62"}.items()},
                               "listingDate": str(row.get("f26", "")),
                               "asOf": timestamp(row["f124"]).isoformat() if number(row.get("f124")) else None})
            unique = {row["code"]: row for row in mapped}
            if len(unique) / total < .95:
                raise MarketDataError("full-market coverage below 95%")
            return {"rows": list(unique.values()), "reported": total, "source": "eastmoney", "errors": errors}
        except MarketDataError as exc:
            errors.append(str(exc))
        master = self.stock_master()
        codes = [instrument(row["ts_code"])["code"] for row in master if row.get("list_status") in (None, "", "L")]
        if not codes:
            raise MarketDataError("no stock master for quote fallback")
        rows = []
        for offset in range(0, len(codes), 80):
            try:
                rows.extend(self.quotes(codes[offset:offset+80]))
            except MarketDataError as exc:
                errors.append(str(exc))
        if len(rows) / len(codes) < .95:
            raise MarketDataError("fallback full-market coverage below 95%")
        metadata = {instrument(row["ts_code"])["code"]: row for row in master}
        for row in rows:
            row["listingDate"] = metadata[row["code"]].get("list_date", "")
            row.setdefault("turnover", None)
            row.setdefault("mainFlow", None)
        return {"rows": rows, "reported": len(codes), "source": "tencent+sina", "errors": errors}
