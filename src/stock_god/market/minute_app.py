"""Standalone local minute HTTP/MCP process and explicit maintenance commands."""

import argparse
from contextlib import asynccontextmanager
from pathlib import Path
import signal
from threading import Event

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
from mcp.server.transport_security import TransportSecuritySettings
from starlette.concurrency import run_in_threadpool
from starlette.routing import Route
import uvicorn

from stock_god.config import AppConfig
from .mcp_tools import MinuteTools, create_mcp_server
from .minute_daily import DailyStore, refresh_daily
from .minute_index import AuctionIndex
from .minute_local import (
    MinuteReadError,
    LocalStore,
    SYMBOL,
    day_time,
    minute_time,
    normalize_period,
    time_range,
    transport_value,
)
from .minute_provider import DiemengClient, default_config_path


def create_app(store, auction=None, daily=None, provider=None):
    tools = MinuteTools(store, auction, daily, provider)
    server = create_mcp_server(tools)
    manager = StreamableHTTPSessionManager(
        server,
        json_response=True,
        stateless=True,
        security_settings=TransportSecuritySettings(
            enable_dns_rebinding_protection=True,
            allowed_hosts=["127.0.0.1:*", "localhost:*", "[::1]:*", "testserver"],
            allowed_origins=["http://127.0.0.1:*", "http://localhost:*"],
        ),
    )

    @asynccontextmanager
    async def lifespan(app):
        async with manager.run():
            yield

    app = FastAPI(title="Stock God Minute Data", version="6.0.0", lifespan=lifespan)
    app.state.tools = tools

    class MCPTransport:
        async def __call__(self, scope, receive, send):
            await manager.handle_request(scope, receive, send)

    app.router.routes.append(Route("/mcp", MCPTransport(), methods=["GET", "POST", "DELETE"]))

    def dispatch(path, q):
        if path in ("/livez", "/readyz"):
            return dict(ready=True, appVersion="6.0.0", tools=len(tools.definitions))
        if path == "/api/indices":
            return dict(source="csv_index", indices=store.search_indices(q.get("query", "")))
        symbol = q.get("symbol", "")
        period = normalize_period(symbol, q.get("period", ""))
        if path == "/api/bars":
            start, end = day_time(q.get("start", "")), day_time(q.get("end", ""))
            if start is not None and end is not None and start > end:
                raise ValueError("开始日期不能晚于结束日期")
            return dict(
                symbol=symbol,
                period=period,
                timezone="Asia/Shanghai",
                data=store.stock_days(symbol, period, start, end),
            )
        if q.get("time"):
            start = end = minute_time(q["time"])
        else:
            start, end = time_range(q.get("start_time", ""), q.get("end_time", ""))
        if path == "/api/index/bars":
            return store.index_result(symbol, period, store.index_bars(symbol, period, start, end))
        if path == "/api/auction/snapshots":
            page = int(q.get("page", 0))
            size = int(q.get("page_size", 500))
            if not SYMBOL.fullmatch(symbol) or page < 0 or not 1 <= size <= 5000:
                raise ValueError("page须非负，page_size须在1至5000之间")
            if auction is None:
                raise RuntimeError("竞价索引未就绪")
            return auction.query(symbol, start, end, page, size)
        raise LookupError("unknown minute route")

    async def endpoint(request: Request):
        try:
            return JSONResponse(
                transport_value(
                    await run_in_threadpool(dispatch, request.url.path, dict(request.query_params))
                )
            )
        except MinuteReadError as error:
            return JSONResponse({"error": str(error)}, status_code=500)
        except FileNotFoundError as error:
            return JSONResponse({"error": str(error)}, status_code=404)
        except ValueError as error:
            return JSONResponse({"error": str(error)}, status_code=400)
        except (OSError, RuntimeError) as error:
            return JSONResponse({"error": str(error)}, status_code=500)

    for path in (
        "/livez",
        "/readyz",
        "/api/bars",
        "/api/indices",
        "/api/index/bars",
        "/api/auction/snapshots",
    ):
        app.add_api_route(path, endpoint, methods=["GET"])
    return app


def main(argv=None):
    config = AppConfig.from_env()
    parser = argparse.ArgumentParser(description="Stock God 分钟行情 API / MCP")
    parser.add_argument("--data-root", "-data-root", default=str(config.market_data_root))
    parser.add_argument("--index-dir", "-index-dir", default=str(config.market_index_dir))
    parser.add_argument("--prepare-data", "-prepare-data", action="store_true")
    parser.add_argument("--refresh-daily", "-refresh-daily", action="store_true")
    parser.add_argument("--from", "-from", dest="start", default="")
    parser.add_argument("--to", "-to", dest="end", default="")
    parser.add_argument("--diemeng-config", "-diemeng-config", default=str(default_config_path()))
    args = parser.parse_args(argv)
    stop = Event()
    if args.prepare_data or args.refresh_daily:
        signal.signal(signal.SIGINT, lambda *_: stop.set())
    provider = None
    auction = None
    try:
        if args.refresh_daily:
            if not args.start or not args.end:
                parser.error("--refresh-daily需要--from和--to")
            provider = DiemengClient.load(
                args.diemeng_config, download_dir=Path(args.index_dir) / "downloads", stop=stop
            )
            report = refresh_daily(
                DailyStore(args.index_dir, writable=True), provider, args.start, args.end, print, stop
            )
            print(
                f"刷新完成：交易日 {report['total']}，新写入 {report['ok']}，跳过 {report['skipped']}，失败 {report['failed']}，共 {report['rows']} 行"
            )
            return 0 if not report["failed"] else 1
        store = LocalStore(args.data_root)
        auction = AuctionIndex(store.root, args.index_dir, writable=args.prepare_data)
        if args.prepare_data:
            auction.prepare(store.auction_files, print, stop)
            print(
                f"READY: stock series={len(store.stocks)}, index series={len(store.indices)}, auction files={len(store.auction_files)}"
            )
            return 0
        auction.ready(store.auction_files)
        daily = DailyStore(args.index_dir)
        provider = DiemengClient.load(
            args.diemeng_config, download_dir=Path(args.index_dir) / "downloads", stop=stop
        )
        uvicorn.run(
            create_app(store, auction, daily, provider), host="127.0.0.1", port=18080, log_level="info"
        )
        return 0
    finally:
        if provider is not None:
            provider.close()
        if auction is not None:
            auction.close()


if __name__ == "__main__":
    raise SystemExit(main())
