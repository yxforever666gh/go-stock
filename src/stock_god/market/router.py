"""Preserved market HTTP routes; retired watchlist routes are deliberately absent."""

import json
from datetime import timedelta

from fastapi import APIRouter, Request
from fastapi.concurrency import run_in_threadpool
from fastapi.responses import JSONResponse

from .common import MarketDataError, envelope, now, require_date, timestamp


def bounded(query, key, default, minimum, maximum):
    value = int(query.get(key) or default)
    if not minimum <= value <= maximum:
        raise ValueError(f"{key} must be between {minimum} and {maximum}")
    return value


def validation_response(request, error):
    path = request.scope["route"].path
    query = request.query_params
    code = request.path_params.get("code", "")
    data = None
    if path == "/api/v1/market/hot/words":
        data = {"items": []}
    elif path == "/api/v1/market/fund-flows":
        data = []
    elif path.endswith("/timeline"):
        data = {"code": code, "points": []}
    elif path.endswith("/futures/positions"):
        data = {"variety": query.get("symbol", ""), "rows": []}
    elif path.endswith("/margin"):
        data = {"scope": query.get("scope", "market"), "rows": []}
    elif path.endswith(("/auction", "/trades")):
        data = {
            "code": code,
            "assetType": query.get("assetType", "stock"),
            "date": query.get("date", ""),
            "snapshots" if path.endswith("/auction") else "items": [],
        }
    if data is not None:
        message = str(error)
        field = next(
            (
                name
                for name in (
                    "hours",
                    "baselineDays",
                    "limit",
                    "scope",
                    "sort",
                    "date",
                    "symbol",
                    "cursor",
                    "assetType",
                    "code",
                )
                if name.lower() in message.lower()
            ),
            "request",
        )
        value = envelope(
            data,
            "validation",
            status="unavailable",
            errors=[{"provider": "validation", "code": "invalid_" + field, "message": message}],
        )
        return JSONResponse(value, status_code=400)
    return JSONResponse({"error": str(error)}, status_code=409 if "revision conflict" in str(error) else 400)


def dispatch(service, request: Request, body: dict | None):
    path = request.scope["route"].path
    q, p = request.query_params, request.path_params
    body = body or {}
    code = p.get("code", "")
    if path == "/api/v1/stocks/search":
        return service.stock_search(q.get("key", ""))
    if path == "/api/v1/stocks/query":
        return service.query_stocks(str(body.get("words", "")))
    if path.endswith("/snapshot"):
        return service.stock_snapshot(code)
    if path.endswith("/kline"):
        days = bounded(q, "days", 120, 1, 5000)
        rows = service.bars(code, now() - timedelta(days=days * 2), now(), "day", "qfq", days)
        return [
            {
                "day": row["time"][:10],
                **{key: str(row[key]) for key in ("open", "close", "high", "low", "volume")},
            }
            for row in rows
        ]
    if path.endswith("/minute-line"):
        return service.minute_line(code, q.get("name", ""))
    if path == "/api/v1/market/telegraphs":
        return service.telegraphs(q.get("source", ""))
    if path == "/api/v1/market/telegraphs/refresh":
        return service.refresh_telegraphs(str(body.get("source", "")))
    if path == "/api/v1/market/indexes/global":
        return service.global_indexes()
    if path == "/api/v1/market/industries/rank":
        return service.industry_rank(q.get("sort", ""), bounded(q, "count", 20, 1, 100))
    if path.endswith("/money-rank"):
        return service.money_rank(
            q.get("category", "0"), q.get("sort", "netamount"), stocks="/stocks/" in path
        )
    if path.endswith("/money-trend"):
        return service.money_trend(code, bounded(q, "days", 10, 1, 1000))
    if path == "/api/v1/market/long-tiger":
        return service.long_tiger(require_date(q.get("date", "")))
    if path.endswith("/research-reports"):
        return service.research_reports(code, industry="/industries/" in path)
    if path.endswith("/notices"):
        return service.notices(code)
    if path == "/api/v1/market/dictionary":
        return service.dictionary(q.get("code", ""))
    if path == "/api/v1/market/sentiment/weighted":
        if not isinstance(body.get("text"), str):
            raise ValueError("text is required")
        return service.analyze_news(body["text"], save=False)
    if path == "/api/v1/market/hot/stocks":
        return service.hot_stocks(q.get("marketType", "10"))
    if path == "/api/v1/market/hot/events":
        return service.hot_events(bounded(q, "size", 10, 1, 100))
    if path == "/api/v1/market/hot/topics":
        return service.hot_topics(bounded(q, "size", 10, 1, 100))
    if path == "/api/v1/market/calendars/investment":
        return service.investment_calendar(q.get("yearMonth", ""))
    if path == "/api/v1/market/calendars/cls":
        return service.cls_calendar()
    if path == "/api/v1/market/breadth":
        return service.breadth()
    if path == "/api/v1/market/fund-flows":
        return service.fund_flows(
            q.get("scope", ""),
            require_date(q.get("date", "")),
            q.get("sort", "netamount").lower(),
            bounded(q, "limit", 20, 1, 100),
        )
    if path.endswith("/timeline"):
        return service.fund_flow_timeline(code)
    if path == "/api/v1/market/futures/positions":
        return service.futures_positions(q.get("symbol", "").upper(), require_date(q.get("date", "")))
    if path == "/api/v1/market/margin":
        return service.margin(q.get("scope", "market"), q.get("code", ""), require_date(q.get("date", "")))
    if path == "/api/v1/market/hot/words":
        return service.hot_words(
            bounded(q, "hours", 24, 1, 72),
            bounded(q, "baselineDays", 7, 3, 30),
            bounded(q, "limit", 30, 1, 100),
        )
    if path.endswith("/auction"):
        return service.auction(code, q.get("assetType", "stock"), require_date(q.get("date", "")))
    if path.endswith("/trades"):
        return service.trades(
            code,
            q.get("assetType", "stock"),
            require_date(q.get("date", "")),
            bounded(q, "cursor", 0, 0, 1000000),
            bounded(q, "limit", 100, 1, 500),
        )
    if path.endswith("/chart"):
        end = q.get("to")
        if end and len(end) == 10:
            end = timestamp(end).replace(hour=23, minute=59, second=59, microsecond=999999)
        return service.chart(
            code,
            q.get("assetType", "stock"),
            q.get("market", ""),
            q.get("period", "day"),
            q.get("adjustment", ""),
            q.get("from"),
            end,
            bounded(q, "limit", 500, 1, 5000),
        )
    if path.endswith("/drawings"):
        values = body if request.method == "PUT" else q
        for key in ("period", "adjustment"):
            if not values.get(key):
                raise ValueError(key + " is required")
        revision = None
        if request.method != "GET":
            if "expectedRevision" not in values:
                raise ValueError("expectedRevision is required")
            revision = int(values["expectedRevision"])
        return service.drawings(
            code,
            values.get("assetType", "stock"),
            values.get("market", ""),
            values["period"],
            values["adjustment"],
            expected_revision=revision,
            drawings=body.get("drawings"),
            delete=request.method == "DELETE",
        )
    if path.startswith("/api/v1/themes"):
        return service.theme_api(
            p.get("id"),
            "snapshots"
            if path.endswith("/daily-snapshots")
            else "catalysts"
            if path.endswith("/catalysts")
            else "detail"
            if p.get("id")
            else "list",
            dict(q),
        )
    if path == "/api/v1/funds/search":
        return service.fund_search(q.get("key", ""))
    if path == "/api/v1/funds/rankings":
        return service.fund_rankings(dict(q))
    if path == "/api/v1/etfs/rankings":
        return service.etf_rankings(dict(q))
    if path == "/api/v1/etfs/search":
        return service.etf_search(q.get("q", ""), bounded(q, "limit", 20, 1, 50))
    if path == "/api/v1/etfs/{code}":
        return service.etf_detail(code)
    raise LookupError("market route not found")


GET_PATHS = (
    "stocks/search",
    "stocks/{code}/snapshot",
    "stocks/{code}/kline",
    "stocks/{code}/minute-line",
    "market/telegraphs",
    "market/indexes/global",
    "market/industries/rank",
    "market/industries/money-rank",
    "market/stocks/money-rank",
    "market/stocks/{code}/money-trend",
    "market/long-tiger",
    "market/stocks/research-reports",
    "market/stocks/{code}/research-reports",
    "market/stocks/notices",
    "market/stocks/{code}/notices",
    "market/industries/research-reports",
    "market/industries/{code}/research-reports",
    "market/dictionary",
    "market/hot/stocks",
    "market/hot/events",
    "market/hot/topics",
    "market/hot/words",
    "market/calendars/investment",
    "market/calendars/cls",
    "market/breadth",
    "market/fund-flows",
    "market/fund-flows/{code}/timeline",
    "market/futures/positions",
    "market/margin",
    "instruments/{code}/auction",
    "instruments/{code}/trades",
    "instruments/{code}/chart",
    "instruments/{code}/drawings",
    "themes",
    "themes/{id}",
    "themes/{id}/daily-snapshots",
    "themes/{id}/catalysts",
    "funds/search",
    "funds/rankings",
    "etfs/rankings",
    "etfs/search",
    "etfs/{code}",
)


def create_router(config=None):
    router = APIRouter()

    async def endpoint(request: Request):
        service = None
        try:
            body = await request.json() if request.method in {"POST", "PUT"} else None
            if body is not None and not isinstance(body, dict):
                raise ValueError("request body must be an object")
            factory = request.app.state.market
            settings_store = getattr(request.app.state, "settings", None)
            settings = (
                (await run_in_threadpool(settings_store.load)).config if settings_store else factory.settings
            )
            service = factory.with_settings(settings)
            value = await run_in_threadpool(dispatch, service, request, body)
            return JSONResponse(value)
        except (ValueError, TypeError, json.JSONDecodeError) as exc:
            return validation_response(request, exc)
        except LookupError as exc:
            return JSONResponse({"error": str(exc)}, status_code=404)
        except MarketDataError as exc:
            return JSONResponse({"error": str(exc), "status": "unavailable"}, status_code=503)
        finally:
            if service is not None:
                service.close()

    for path in GET_PATHS:
        router.add_api_route(
            "/api/v1/" + path, endpoint, methods=["GET"], name="market_" + path.replace("/", "_")
        )
    for method, path in (
        ("POST", "stocks/query"),
        ("POST", "market/telegraphs/refresh"),
        ("POST", "market/sentiment/weighted"),
        ("PUT", "instruments/{code}/drawings"),
        ("DELETE", "instruments/{code}/drawings"),
    ):
        router.add_api_route(
            "/api/v1/" + path,
            endpoint,
            methods=[method],
            name="market_" + method + "_" + path.replace("/", "_"),
        )
    return router
