"""Market evidence envelopes preserve source errors and observation timestamps."""

import csv
from datetime import datetime, timedelta
from decimal import Decimal, ROUND_HALF_UP
import io
import math
import re
import sqlite3
from statistics import median

from .common import MarketDataError, database_rows, envelope, instrument, now, number, security_id, timestamp
from .news import SINA, array

FLOW_SORT = {"netamount": "f62", "main_ratio": "f184", "super_large_netamount": "f66", "large_netamount": "f72",
             "medium_netamount": "f78", "small_netamount": "f84", "change_pct": "f3", "avg_changeratio": "f3",
             "inamount": "f62", "outamount": "f62"}


def breadth_data(rows):
    observations = [row for row in rows if row.get("price", 0) and row.get("changePct") is not None]
    if not observations:
        raise MarketDataError("market breadth has no valid observations")
    changes = [row["changePct"] for row in observations]
    up, down = 0, 0
    for row in observations:
        previous = row.get("preClose")
        if not previous:
            continue
        code = row["code"]
        fraction = .05 if "ST" in row["name"].upper() else .3 if code.startswith("bj") else .2 if code.startswith(("sh68", "sz30")) else .1
        upper = float((Decimal(str(previous))*(1+Decimal(str(fraction)))).quantize(Decimal(".01"), rounding=ROUND_HALF_UP))
        lower = float((Decimal(str(previous))*(1-Decimal(str(fraction)))).quantize(Decimal(".01"), rounding=ROUND_HALF_UP))
        up += row["price"] >= upper-.00001
        down += row["price"] <= lower+.00001
    return {"total": len(observations), "advances": sum(value > 0 for value in changes), "declines": sum(value < 0 for value in changes),
            "flat": sum(value == 0 for value in changes), "limitUps": up, "limitDowns": down,
            "newHighs": None, "newLows": None, "medianChangePct": median(changes)}


class Evidence:
    def breadth(self):
        def load():
            try:
                snapshot = self.full_market()
                at = max((row["asOf"] for row in snapshot["rows"] if row.get("asOf")), default=None)
                return envelope(breadth_data(snapshot["rows"]), snapshot["source"], as_of=at,
                                status="partial" if snapshot["errors"] else "ok", warnings=snapshot["errors"])
            except MarketDataError as exc:
                return envelope({"total": 0, "advances": 0, "declines": 0, "flat": 0, "limitUps": 0, "limitDowns": 0,
                                 "newHighs": None, "newLows": None, "medianChangePct": 0}, "", status="unavailable",
                                errors=[{"provider": "market", "code": "unavailable", "message": str(exc)}])
        return self.http.cached("breadth", 30, load)

    def fund_flows(self, scope, day="", sort="netamount", limit=20):
        if scope not in {"sector", "concept"} or sort not in FLOW_SORT or not 1 <= limit <= 100:
            raise ValueError("invalid fund flow scope, sort or limit")
        def primary():
            payload = self.http.json("https://push2delay.eastmoney.com/api/qt/clist/get",
                {"pn": 1, "pz": limit, "po": 1, "np": 1, "fltt": 2, "invt": 2, "fid": FLOW_SORT[sort],
                 "fs": "m:90 s:4" if scope == "sector" else "m:90 t:3", "stat": 1,
                 "fields": "f12,f14,f3,f62,f184,f66,f72,f78,f84,f124"})
            raw = payload.get("data", {}).get("diff")
            values = list(raw.values()) if isinstance(raw, dict) else raw
            if not values:
                raise MarketDataError("empty Eastmoney fund flows")
            rows = []
            as_of = None
            for value in values:
                if not value.get("f12") or number(value.get("f62")) is None:
                    continue
                rows.append({"code": value["f12"], "name": value.get("f14", ""), "netAmount": number(value["f62"]),
                             "inAmount": None, "outAmount": None, **{key: number(value.get(field)) for key, field in {
                                 "changePct": "f3", "mainNetRatio": "f184", "superLargeNetAmount": "f66", "largeNetAmount": "f72",
                                 "mediumNetAmount": "f78", "smallNetAmount": "f84"}.items()}})
                if number(value.get("f124")):
                    at = timestamp(value["f124"])
                    as_of = max(as_of, at) if as_of else at
            if not rows or day and (as_of is None or as_of.date().isoformat() != day):
                raise MarketDataError("fund flows date mismatch or empty rows")
            if sort in {"inamount", "outamount"}:
                raise MarketDataError("Eastmoney does not provide gross inflow/outflow sorting")
            return rows, as_of
        def fallback():
            if day:
                raise MarketDataError("Sina fund flow source has no verifiable historical date")
            values = array(self.http.json(SINA+"ssl_bkzj_bk", {"page": 1, "num": limit, "sort": sort, "asc": 0, "fenlei": "0" if scope == "sector" else "1"}))
            rows = []
            for value in values:
                net = number(value.get("netamount", value.get("net_amount")))
                if net is None:
                    continue
                change = number(value.get("avg_changeratio", value.get("changeratio")))
                rows.append({"code": str(value.get("category", value.get("code", ""))), "name": value.get("name", value.get("categoryname", "")),
                             "netAmount": net, "inAmount": number(value.get("inamount")), "outAmount": number(value.get("outamount")),
                             "changePct": change*100 if change is not None else None, "mainNetRatio": None,
                             "superLargeNetAmount": None, "largeNetAmount": None, "mediumNetAmount": None, "smallNetAmount": None})
            if not rows:
                raise MarketDataError("empty Sina fund flow rows")
            return rows[:limit], None
        result = self.http.chain([("eastmoney-delay", primary), ("sina", fallback)], [])
        if result["source"] == "sina" or result["asOf"].startswith("0001") and result["status"] == "ok":
            result["status"] = "partial"
            result["warnings"] = ["Source has no verifiable market timestamp"]
        return result

    def fund_flow_timeline(self, code):
        if not re.fullmatch("BK[0-9]{4}", code.upper()):
            raise ValueError("code must be BK followed by four digits")
        code = code.upper()
        def load():
            data = self.http.json("https://push2delay.eastmoney.com/api/qt/stock/fflow/kline/get",
                {"secid": "90."+code, "klt": 1, "lmt": 0, "fields1": "f1,f2,f3,f7", "fields2": "f51,f52,f53,f54,f55,f56"}).get("data")
            if not data or not isinstance(data.get("klines"), list):
                raise MarketDataError("fund flow timeline unavailable")
            points = []
            for line in data["klines"]:
                fields = line.split(",")
                if len(fields) < 6:
                    continue
                points.append({"at": timestamp(fields[0]).isoformat(), **{key: number(fields[index], 0) for key, index in {
                    "mainNetAmount": 1, "smallNetAmount": 2, "mediumNetAmount": 3, "largeNetAmount": 4, "superLargeNetAmount": 5}.items()}})
            if not points:
                raise MarketDataError("fund flow timeline contains no points")
            return {"code": code, "name": data.get("name", ""), "tradingDate": points[-1]["at"][:10], "points": points}, points[-1]["at"]
        return self.http.chain([("eastmoney-delay", load)], {"code": code, "points": []})

    def futures_positions(self, symbol, day=""):
        metadata = {"IF": ("沪深300股指期货", "sh000300"), "IH": ("上证50股指期货", "sh000016"),
                    "IC": ("中证500股指期货", "sh000905"), "IM": ("中证1000股指期货", "sh000852")}
        if symbol not in metadata:
            raise ValueError("symbol must be IF, IH, IC or IM")
        name, index = metadata[symbol]
        base = {"variety": symbol, "varietyName": name, "indexCode": index, "contractCode": "", "rows": []}
        endpoint = "https://datacenter-web.eastmoney.com/api/data/v1/get"
        def primary():
            contracts = array(self.http.json(endpoint, {"reportName": "RPT_FUTU_POSITIONCODE", "columns": "ALL", "pageSize": 5, "pageNumber": 1,
                "filter": f'(TRADE_CODE="{symbol}")(IS_MAINCODE="1")'}), "result", "data")
            if not contracts:
                raise MarketDataError("main futures contract unavailable")
            code = contracts[0]["SECURITY_CODE"]
            values = array(self.http.json(endpoint, {"reportName": "RPT_FUTU_NET_POSITION", "columns": "ALL", "pageSize": 60, "pageNumber": 1,
                "sortColumns": "TRADE_DATE", "sortTypes": -1, "filter": f'(SECURITY_CODE="{code}")'}), "result", "data")
            rows = [{"tradeDate": value["TRADE_DATE"][:10], **{key: number(value.get(field), 0) for key, field in {
                "settlePrice": "SETTLE_PRICE", "longPosition": "TOTAL_LONG_POSITION", "longChange": "LP_CHANGE_TOTAL",
                "shortPosition": "TOTAL_SHORT_POSITION", "shortChange": "SP_CHANGE_TOTAL", "netPosition": "NET_POSITION",
                "indexClose": "CLOSE_PRICE", "indexChange": "CLOSE_PRICE_CHANGE", "basis": "BASIS"}.items()}}
                for value in values if not day or value["TRADE_DATE"].startswith(day)]
            if not rows:
                raise MarketDataError("futures date unavailable")
            rows.sort(key=lambda row: row["tradeDate"])
            return base | {"contractCode": code, "rows": rows}, rows[-1]["tradeDate"]
        def fallback():
            target = timestamp(day) if day else now()
            for offset in range(1 if day else 7):
                date = target-timedelta(days=offset)
                try:
                    text = self.http.text(f"http://www.cffex.com.cn/sj/ccpm/{date:%Y%m}/{date:%d}/{symbol}_1.csv", encoding="gb18030")
                except MarketDataError:
                    continue
                members = [row for row in list(csv.reader(io.StringIO(text)))[2:] if len(row) >= 12 and row[2].strip().isdigit()]
                totals = {}
                for row in members:
                    totals[row[1]] = totals.get(row[1], 0) + number(row[4], 0)
                if not totals:
                    continue
                contract = max(totals, key=totals.get)
                longs = sum(number(row[7], 0) for row in members if row[1] == contract)
                shorts = sum(number(row[10], 0) for row in members if row[1] == contract)
                return base | {"contractCode": contract, "rows": [{"tradeDate": date.date().isoformat(), "longPosition": longs,
                    "shortPosition": shorts, "netPosition": longs-shorts, "settlePrice": 0, "longChange": 0, "shortChange": 0,
                    "indexClose": 0, "indexChange": 0, "basis": 0}]}, date
            raise MarketDataError("CFFEX position CSV unavailable")
        result = self.http.chain([("eastmoney", primary), ("cffex", fallback)], base)
        if result["source"] == "cffex":
            result["warnings"] = ["CFFEX fallback does not include settlement, index close or basis"]
        return result

    def margin(self, scope="market", code="", day=""):
        if scope not in {"market", "security"}:
            raise ValueError("scope must be market or security")
        if scope == "security":
            code = instrument(code)["code"]
            def security():
                values = array(self.http.json("https://datacenter.eastmoney.com/securities/api/data/v1/get",
                    {"reportName": "RPT_RZRQ_STOCKS_DETAIL", "columns": "ALL", "sortColumns": "TRADE_DATE", "sortTypes": -1,
                     "pageNumber": 1, "pageSize": 50, "filter": f'(SECUCODE="{code[2:]}.{code[:2].upper()}")'}), "result", "data")
                rows = [{"date": value["TRADE_DATE"][:10], "code": value.get("SECURITY_CODE", code[2:]), "name": value.get("SECURITY_NAME_ABBR", ""),
                         **{key: number(value.get(field), 0) for key, field in {"financingBalance": "FIN_BALANCE", "securitiesBalance": "LOAN_BALANCE",
                         "marginBalance": "MARGIN_BALANCE", "financingBuy": "FIN_BUY_AMT", "securitiesSell": "LOAN_SELL_VOL"}.items()}}
                        for value in values if not day or value["TRADE_DATE"].startswith(day)]
                if not rows:
                    raise MarketDataError("margin security data unavailable for requested date")
                return {"scope": scope, "rows": rows}, rows[0]["date"]
            return self.http.chain([("eastmoney", security)], {"scope": scope, "rows": []})
        rows, errors = [], []
        target_day = day
        for exchange in ("SSE", "SZSE"):
            try:
                if exchange == "SSE":
                    params = {"isPagination": "true", "pageHelp.pageSize": 1, "pageHelp.pageNo": 1, "sqlId": "RZRQ_HZ_INFO"}
                    if day:
                        params.update(beginDate=day.replace("-", ""), endDate=day.replace("-", ""))
                    data = self.http.json("https://query.sse.com.cn/commonSoaQuery.do", params,
                                          headers={"Referer": "https://www.sse.com.cn/market/othersdata/margin/sum/"})
                    values = data.get("result") or data.get("pageHelp", {}).get("data")
                    if not values:
                        raise MarketDataError("empty SSE margin")
                    value = values[0]
                    date = str(value["opDate"])
                    date = f"{date[:4]}-{date[4:6]}-{date[6:8]}" if len(date) == 8 else date[:10]
                    mapping = {"financingBalance": "rzye", "securitiesBalance": "rqylje", "marginBalance": "rzrqjyzl", "financingBuy": "rzmre", "securitiesSell": "rqmcl"}
                    scale = 1
                    target_day = day or date
                else:
                    payload = array(self.http.json("https://www.szse.cn/api/report/ShowReport/data",
                        {"SHOWTYPE": "JSON", "CATALOGID": "1837_xxpl", "TABKEY": "tab1", "txtDate": target_day},
                        headers={"Referer": "https://www.szse.cn/disclosure/margin/object/index.html"}))
                    if not payload or not payload[0].get("data"):
                        raise MarketDataError("empty SZSE margin")
                    value = payload[0]["data"][0]
                    found = re.search(r"\d{4}-\d{2}-\d{2}", str(payload[0].get("metadata", {}).get("subname", "")))
                    date = found.group() if found else target_day
                    mapping = {"financingBalance": "jrrzye", "securitiesBalance": "jrrjye", "marginBalance": "jrrzrjye", "financingBuy": "jrrzmr", "securitiesSell": "jrrjmc"}
                    scale = 1e8
                if target_day and date != target_day:
                    raise MarketDataError("exchange margin date mismatch")
                rows.append({"code": exchange, "name": "上海证券交易所" if exchange == "SSE" else "深圳证券交易所", "date": date,
                             **{key: number(value.get(field), 0)*scale for key, field in mapping.items()}})
            except (MarketDataError, ValueError, KeyError, TypeError) as exc:
                errors.append({"provider": exchange, "code": "unavailable", "message": str(exc)})
        return envelope({"scope": scope, "rows": rows}, "sse+szse", status="partial" if rows and errors else "ok" if rows else "unavailable",
                        as_of=target_day, errors=errors)

    def trades(self, code, asset_type="stock", day="", cursor=0, limit=100):
        code = instrument(code, asset_type)["code"]
        day = day or now().date().isoformat()
        empty = {"code": code, "assetType": asset_type, "date": day, "items": []}
        def cached():
            start = timestamp(day)
            values = database_rows(self.config.minute_db,
                "SELECT * FROM market_trade_tick WHERE asset_type=? AND symbol=? AND traded_at>=? AND traded_at<? ORDER BY traded_at,sequence LIMIT ? OFFSET ?",
                (asset_type, code, int(start.timestamp()*1000), int((start+timedelta(days=1)).timestamp()*1000), limit+1, cursor))
            if not values:
                raise MarketDataError("historical trade cache unavailable")
            items = [{"time": timestamp(value["traded_at"]).strftime("%H:%M:%S"), **{key: value[key] for key in ("price", "volume", "amount", "side")}} for value in values[:limit]]
            return empty | {"items": items, **({"nextCursor": str(cursor+limit)} if len(values) > limit else {})}, timestamp(values[min(len(values), limit)-1]["traded_at"])
        if day != now().date().isoformat():
            return self.http.chain([("local-trade-cache", cached)], empty)
        def live():
            payload = self.http.json("https://push2.eastmoney.com/api/qt/stock/details/get",
                {"secid": security_id(code), "fields1": "f1,f2,f3,f4", "fields2": "f51,f52,f53,f54,f55", "pos": -2000})
            items = []
            for line in array(payload, "data", "details"):
                fields = line.split(",")
                if len(fields) < 3 or number(fields[1]) is None or number(fields[2]) is None:
                    continue
                price, volume = number(fields[1]), number(fields[2])
                items.append({"time": fields[0] if len(fields[0]) == 8 else fields[0]+":00", "price": price, "volume": volume,
                              "amount": price*volume*100, "side": {"1": "buy", "B": "buy", "2": "sell", "S": "sell", "4": "neutral", "N": "neutral"}.get(fields[4] if len(fields)>4 else "", "")})
            if not items:
                raise MarketDataError("empty trade details")
            with sqlite3.connect(self.config.minute_db, timeout=10) as connection:
                for sequence, item in enumerate(items):
                    at = timestamp(day+"T"+item["time"])
                    connection.execute("INSERT INTO market_trade_tick(asset_type,symbol,traded_at,sequence,price,volume,amount,side,source,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(asset_type,symbol,traded_at,sequence) DO UPDATE SET price=excluded.price,volume=excluded.volume,amount=excluded.amount,side=excluded.side,updated_at=excluded.updated_at",
                        (asset_type, code, int(at.timestamp()*1000), sequence, item["price"], item["volume"], item["amount"], item["side"], "eastmoney", int(now().timestamp()*1000)))
            return empty | {"items": items[cursor:cursor+limit], **({"nextCursor": str(cursor+limit)} if len(items)>cursor+limit else {})}, day+"T"+items[-1]["time"]
        return self.http.chain([("eastmoney", live), ("local-trade-cache", cached)], empty)

    def auction(self, code, asset_type="stock", day=""):
        code = instrument(code, asset_type)["code"]
        day = day or now().date().isoformat()
        empty = {"code": code, "assetType": asset_type, "date": day, "snapshots": [], "finalSnapshot": None, "auctionStrength": None, "gapPct": None}
        def cached():
            rows = database_rows(self.config.minute_db, "SELECT * FROM market_auction_snapshot WHERE asset_type=? AND symbol=? AND trade_date=? ORDER BY observed_at",
                                 (asset_type, code, day))
            if not rows:
                raise MarketDataError("historical auction cache unavailable")
            items = [{"time": timestamp(row["observed_at"]).strftime("%H:%M:%S"), "price": row["indicative_price"],
                      "matchedVolume": row["matched_volume"], "matchedAmount": row["matched_amount"],
                      "unmatchedVolume": row["unmatched_volume"], "unmatchedSide": row["unmatched_side"]} for row in rows if row["phase"] != "final"]
            return empty | {"snapshots": items, "finalSnapshot": items[-1] if items else None}, timestamp(rows[-1]["observed_at"])
        if day != now().date().isoformat():
            return self.http.chain([("local-auction-cache", cached)], empty)
        details = self.trades(code, asset_type, day, 0, 500)
        selected = [row for row in details["data"]["items"] if "09:15:00" <= row["time"] <= "09:25:00"]
        if selected:
            items = [{"time": row["time"], "price": row["price"], "matchedVolume": row["volume"], "matchedAmount": row["amount"], "unmatchedVolume": None} for row in selected]
            return envelope(empty | {"snapshots": items, "finalSnapshot": items[-1]}, "eastmoney", status="partial", as_of=day+"T"+items[-1]["time"],
                            warnings=["Public trade ticks do not provide unmatched auction volume"])
        return self.http.chain([("local-auction-cache", cached)], empty)
