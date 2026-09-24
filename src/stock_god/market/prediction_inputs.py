"""Frozen candidate snapshot and timestamped source documents for prediction."""

from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta
from decimal import Decimal, ROUND_HALF_UP
import json
import math

from .common import CN, MarketDataError, now, timestamp
from .evidence import breadth_data


def metrics(bars, quote):
    result = {key: None for key in ("returnPct", "vwap", "distanceFromHighPct", "maxDrawdownPct", "recoveryPct",
                                    "volumeAcceleration", "dayReturnPct", "dayOpenReturnPct", "dayDistanceFromHighPct")}
    result.update(windowVolume=0., windowAmount=0., historicalBaseline="unavailable")
    if quote.get("preClose"):
        result["dayReturnPct"] = (quote["price"]/quote["preClose"]-1)*100
    if quote.get("open"):
        result["dayOpenReturnPct"] = (quote["price"]/quote["open"]-1)*100
    if quote.get("high"):
        result["dayDistanceFromHighPct"] = (quote["price"]/quote["high"]-1)*100
    if not bars:
        return result
    result["returnPct"] = (bars[-1]["close"]/bars[0]["open"]-1)*100
    result["windowVolume"] = sum(bar["volume"] for bar in bars)
    result["windowAmount"] = sum(bar["amount"] for bar in bars)
    if result["windowVolume"] > 0:
        result["vwap"] = sum(bar["close"]*bar["volume"] for bar in bars)/result["windowVolume"]
        result["vwapMethod"] = "volume_weighted_minute_close_proxy"
    high = max(bar["high"] for bar in bars)
    result["distanceFromHighPct"] = (bars[-1]["close"]/high-1)*100
    peak, trough, drawdown = 0., math.inf, 0.
    for bar in bars:
        if bar["high"] > peak:
            peak, trough = bar["high"], bar["low"]
        else:
            trough = min(trough, bar["low"])
        drawdown = max(drawdown, (peak-bar["low"])/peak*100)
    result["maxDrawdownPct"] = drawdown
    if peak > trough:
        result["recoveryPct"] = (bars[-1]["close"]-trough)/(peak-trough)*100
    middle = len(bars)//2
    first = sum(bar["volume"] for bar in bars[:middle])
    second = sum(bar["volume"] for bar in bars[middle:])
    if first:
        result["volumeAcceleration"] = (second/max(1, len(bars)-middle))/(first/max(1, middle))
    return {key: round(value, 4) if isinstance(value, float) else value for key, value in result.items()}


def filter_at_cutoff(value, cutoff, inherited=False):
    """A dated child never makes its untimestamped siblings eligible."""
    if isinstance(value, list):
        items = [filter_at_cutoff(item, cutoff, inherited) for item in value]
        valid = [(item, at) for item, at, keep in items if keep]
        return [item for item, _ in valid], max((at for _, at in valid if at), default=None), bool(valid) or inherited
    if isinstance(value, dict):
        direct = []
        for key, raw in value.items():
            if any(token in key.lower() for token in ("publish", "eventat", "event_at", "availableat", "available_at", "notice_date", "datetime", "date_time", "timestamp", "trade_time", "tradedate", "trade_date", "日期")):
                try:
                    direct.append(timestamp(raw))
                except (ValueError, TypeError, OverflowError):
                    continue
        if any(at > cutoff for at in direct):
            return None, max(direct), False
        result, dates = {}, list(direct)
        for key, item in value.items():
            filtered, at, keep = filter_at_cutoff(item, cutoff, inherited or bool(direct))
            if keep:
                result[key] = filtered
                if at:
                    dates.append(at)
        return result, max(dates, default=None), inherited or bool(direct) or bool(result)
    return value, None, inherited


class PredictionInputs:
    def collect_prediction_evidence(self, cutoff: datetime, exclusions, cash: float):
        started = timestamp(cutoff)
        snapshot = self.full_market()
        rows = snapshot["rows"]
        dates = [timestamp(row["asOf"]) for row in rows if row.get("asOf")]
        if not dates:
            raise MarketDataError("market snapshot has no verifiable observation timestamp")
        cutoff = max(dates)
        if cutoff > now()+timedelta(seconds=5):
            raise MarketDataError("market snapshot timestamp is in the future")
        window_end = cutoff.replace(second=0, microsecond=0)
        if (11, 30) <= (started.hour, started.minute) < (13, 0) or (11, 30) <= (cutoff.hour, cutoff.minute) < (13, 0):
            window_end = cutoff.replace(hour=11, minute=30, second=0, microsecond=0)
        if window_end.hour >= 15:
            window_end = cutoff.replace(hour=15, minute=0, second=0, microsecond=0)
        window_start = window_end-timedelta(minutes=5)
        excluded = {str(code).lower().replace(".sh", "").replace(".sz", "").removeprefix("sh").removeprefix("sz") for code in exclusions}
        eligible = []
        for row in rows:
            code, price, previous = row["code"], row.get("price"), row.get("preClose")
            if not code.startswith(("sh60", "sz00")) or code[2:] in excluded or "ST" in row["name"].upper() or "退" in row["name"]:
                continue
            if not price or not previous or not row.get("volume") or not row.get("amount") or not row.get("changePct") or row["changePct"] <= 0:
                continue
            if not row.get("asOf") or not cutoff-timedelta(minutes=3) <= timestamp(row["asOf"]) <= cutoff:
                continue
            try:
                listed = datetime.strptime(row.get("listingDate", ""), "%Y%m%d").replace(tzinfo=CN)
            except ValueError:
                continue
            if listed > cutoff:
                continue
            if (cutoff-listed).days < 45:
                sessions = sum(self.is_trading_day(listed+timedelta(days=offset)) for offset in range((cutoff-listed).days+1))
                if sessions < 10:
                    continue
            upper = float((Decimal(str(previous))*Decimal("1.1")).quantize(Decimal(".01"), rounding=ROUND_HALF_UP))
            if (upper-price)/upper*100 < 1.5-1e-7:
                continue
            notional = price*100
            lot_cost = notional+max(5, notional*.0002)+(notional*.00001 if code.startswith("sh") else 0)
            if lot_cost > cash:
                continue
            eligible.append(row)
        eligible.sort(key=lambda row: row["changePct"]*4+math.log10(max(1, row["amount"]))*2+(row.get("turnover") or 0), reverse=True)
        eligible = eligible[:12]
        documents, compact, candidates, reasons = [], [], [], []
        collected = now().isoformat()
        def document(source_id, category, value, available, error="", code="", source="", ref=""):
            at = timestamp(available).isoformat() if available else None
            return {"sourceId": source_id, "sourceName": source or source_id, "sourceRef": ref, "category": category,
                    "collectedAt": collected, "retrievedAt": collected, "availableAt": at, "publishedAt": at,
                    "content": json.dumps(value, ensure_ascii=False, separators=(",", ":")) if not error else "",
                    "error": error, "stockCode": code, "collectionStatus": "failed" if error else "ok"}
        documents.append(document("research2:market:full", "market", snapshot, cutoff, source=snapshot["source"]))
        def load_window(row):
            try:
                bars = [bar for bar in self.bars(row["code"], window_start, window_end, "1m", "none") if timestamp(bar["time"]) < window_end]
                if len(bars) < 4:
                    raise MarketDataError("fewer than four closed minute bars")
                return row, bars, ""
            except MarketDataError as exc:
                return row, [], str(exc)
        with ThreadPoolExecutor(max_workers=6) as pool:
            windows = list(pool.map(load_window, eligible))
        for row, bars, error in windows:
            code = row["code"]
            source_id = "research2:stock:"+code+":minute-window"
            documents.append(document(source_id, "stock", {"code": code, "bars": bars, "quote": row}, window_end, error, code, "分钟窗口"))
            quote = {"at": row["asOf"], "price": row["price"], "open": row["open"], "previousClose": row["preClose"], "high": row["high"], "low": row["low"],
                     "turnoverPct": row.get("turnover"), "mainFlow": row.get("mainFlow"), "dayVolume": row["volume"], "dayAmount": row["amount"]}
            compact.append({"entityId": "stock:"+code, "code": code, "name": row["name"], "coreEligible": not bool(error), "quote": quote,
                            "minuteBarCount": len(bars), "minuteSource": bars[0]["source"] if bars else "", "metrics": metrics(bars, row),
                            "sourceIds": ["research2:market:full", source_id], "missing": [error] if error else []})
            if error:
                reasons.append(code+": "+error)
            else:
                candidates.append({"code": code, "name": row["name"], "referencePrice": row["price"]})
        market = breadth_data(rows) | {"sourceId": "research2:market:full", "observed": len(rows), "reported": snapshot["reported"],
                                      "coveragePct": len(rows)/snapshot["reported"]*100, "sectorFlows": [], "conceptFlows": []}
        for scope in ("sector", "concept"):
            flow = self.fund_flows(scope)
            error = ""
            at = None if flow["asOf"].startswith("0001") else timestamp(flow["asOf"])
            if flow["status"] == "unavailable" or at is None or at > cutoff:
                error = "fund-flow source has no cutoff-safe observation"
                reasons.append(scope+": "+error)
            else:
                market[scope+"Flows"] = flow["data"]
            documents.append(document("research2:market:"+scope+"-flows", "sector", flow, at, error, source=flow["source"]))
        try:
            news = []
            for source in ("财联社电报", "新浪财经", "外媒"):
                try:
                    news.extend(self._live_news(source))
                except (MarketDataError, ValueError, KeyError) as exc:
                    reasons.append(source+": "+str(exc))
            accepted = [row for row in news if row.get("dataTime") and cutoff-timedelta(days=1) <= timestamp(row["dataTime"]) <= cutoff]
            documents.append(document("research2:news:market", "news", accepted, max((row["dataTime"] for row in accepted), default=None),
                                      "no cutoff-safe news" if not accepted else "", source="市场新闻"))
        except MarketDataError as exc:
            reasons.append(str(exc))
        for candidate in candidates:
            for category, collect in (("notices", self.notices), ("research", self.research_reports)):
                try:
                    raw = collect(candidate["code"])
                    filtered, at, keep = filter_at_cutoff(raw, cutoff)
                    error = "source has no cutoff-safe items" if not keep else ""
                    documents.append(document("research2:stock:"+candidate["code"]+":"+category, category, filtered, at, error,
                                              candidate["code"], category))
                except MarketDataError as exc:
                    documents.append(document("research2:stock:"+candidate["code"]+":"+category, category, [], None, str(exc), candidate["code"]))
        if hasattr(self, "prediction_theme_documents"):
            documents.extend(self.prediction_theme_documents(cutoff))
        sources = [{"sourceId": doc["sourceId"], "sourceName": doc["sourceName"], "category": doc["category"],
                    "status": "failed" if doc["error"] else "ok", "availableAt": doc["availableAt"], "error": doc["error"],
                    "summary": doc["content"][:480]} for doc in documents]
        payload = {"version": "research2-slots-v8", "windowStartAt": window_start.isoformat(), "windowEndAt": window_end.isoformat(),
                   "cutoffAt": cutoff.isoformat(), "freezeAt": now().isoformat(), "market": market, "candidates": compact,
                   "sources": sources, "degraded": bool(reasons), "degradedReasons": reasons}
        return {"availableCash": cash, "prompt": json.dumps(payload, ensure_ascii=False, separators=(",", ":")),
                "sourceStatusJson": json.dumps(sources, ensure_ascii=False), "candidates": candidates, "documents": documents,
                "evidenceProfileVersion": "research2-slots-v8", "cutoffAt": cutoff.isoformat(), "freezeAt": payload["freezeAt"],
                "windowStartAt": window_start.isoformat(), "windowEndAt": window_end.isoformat(), "coveragePct": market["coveragePct"],
                "degraded": bool(reasons), "degradedReasons": reasons, "candidateReferencePrices": {row["code"]: row["referencePrice"] for row in candidates}}
