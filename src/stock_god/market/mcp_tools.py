"""The unchanged public market tool names, backed by Python domain functions."""

import json
from typing import Any

from jsonschema import Draft202012Validator
from mcp import types
from mcp.server.lowlevel import Server
from starlette.concurrency import run_in_threadpool

from .mcp_catalog import CATALOG
from .minute_local import (
    iso_time,
    minute_time,
    normalize_period,
    normalize_symbol,
    query_with_source,
    time_range,
    transport_value,
)


def _schema(properties, required=()):
    return dict(type="object", properties=properties, required=list(required), additionalProperties=False)


def tool_definitions(daily_available=True):
    string = {"type": "string"}
    local = (
        (
            "search_indices",
            "查找本地指数代码/名称与实际可用周期；股票和指数分开。",
            _schema({"query": string}),
        ),
        (
            "get_index_bar",
            "精确读取本地指数某时刻K线，无记录found=false；不调整时间与未注明的单位。",
            _schema({"symbol": string, "period": string, "time": string}, ("symbol", "time")),
        ),
        (
            "get_index_bars",
            "本地指数区间包含两端；相同记录去重，冲突报错。",
            _schema(
                {"symbol": string, "period": string, "start_time": string, "end_time": string},
                ("symbol", "start_time", "end_time"),
            ),
        ),
        (
            "get_auction_snapshot",
            "精确本地集合竞价快照；保留19个原字段与time，空白为null、0仍为0。",
            _schema({"symbol": string, "time": string}, ("symbol", "time")),
        ),
        (
            "get_auction_snapshots",
            "分页读取本地集合竞价区间，返回total与next_page；单位保持原值。",
            _schema(
                {
                    "symbol": string,
                    "start_time": string,
                    "end_time": string,
                    "page": {"type": "integer", "minimum": 0},
                    "page_size": {"type": "integer", "minimum": 1, "maximum": 5000},
                },
                ("symbol", "start_time", "end_time"),
            ),
        ),
        (
            "get_bar",
            "精确查询股票K线；auto优先本地，缺文件/无记录时蝶梦回退；成交量为股。",
            _schema(
                {
                    "source": {"type": "string", "enum": ["auto", "local", "diemeng"]},
                    "symbol": string,
                    "period": string,
                    "time": string,
                },
                ("symbol", "time"),
            ),
        ),
        (
            "get_bars",
            "股票区间包含两端；auto不拼接部分本地结果。CSV复权unknown，蝶梦none。",
            _schema(
                {
                    "source": {"type": "string", "enum": ["auto", "local", "diemeng"]},
                    "symbol": string,
                    "period": string,
                    "start_time": string,
                    "end_time": string,
                },
                ("symbol", "start_time", "end_time"),
            ),
        ),
    )
    definitions = []
    for name, description, schema in local:
        annotations = types.ToolAnnotations(readOnlyHint=True, idempotentHint=True)
        if name not in ("get_bar", "get_bars"):
            annotations.openWorldHint = False
        definitions.append(
            types.Tool(name=name, description=description, inputSchema=schema, annotations=annotations)
        )
    if daily_available:
        for name, description, schema in (
            ("get_refresh_status", "查询本地日线库覆盖范围、总行数和失败日期；不联网。", _schema({})),
            (
                "get_market_breadth",
                "按日聚合涨跌平家数，只统计pct_chg非空记录；纯本地覆盖索引SQL。",
                _schema({"start_date": string, "end_date": string}, ("start_date", "end_date")),
            ),
            (
                "get_daily_bars",
                "按代码和日期读取本地不复权日线，不联网；缺失数值保留null，成交量保留上游单位。",
                _schema(
                    {
                        "stock_codes": {"type": "array", "items": string, "minItems": 1, "maxItems": 100},
                        "start_date": string,
                        "end_date": string,
                    },
                    ("stock_codes",),
                ),
            ),
        ):
            definitions.append(
                types.Tool(
                    name=name,
                    description=description,
                    inputSchema=schema,
                    annotations=types.ToolAnnotations(
                        readOnlyHint=True, idempotentHint=True, openWorldHint=False
                    ),
                )
            )
    for endpoint in CATALOG:
        description = (
            endpoint["title"]
            + "。"
            + endpoint["description"]
            + " 数据来源：蝶梦。保留原始字段和单位；分页只返回请求的一页。"
        )
        download = endpoint.get("download", False)
        if download:
            description += "仅在明确要求全市场下载时调用，会在本机保存文件。每日期一天最多10次；不自动重试。"
        definitions.append(
            types.Tool(
                name=endpoint["name"],
                description=description,
                inputSchema=endpoint["input_schema"],
                annotations=types.ToolAnnotations(
                    readOnlyHint=not download, idempotentHint=not download, destructiveHint=False
                ),
            )
        )
    return definitions


class MinuteTools:
    def __init__(self, store, auction=None, daily=None, provider=None):
        self.store = store
        self.auction = auction
        self.daily = daily
        self.provider = provider
        self.definitions = {tool.name: tool for tool in tool_definitions(daily is not None)}

    def call(self, name, args) -> dict[str, Any]:
        if name not in self.definitions:
            raise ValueError("未知工具：" + name)
        Draft202012Validator(self.definitions[name].inputSchema).validate(args)
        if name.startswith("diemeng_"):
            if self.provider is None:
                raise ValueError("蝶梦未配置：请填写本机私有配置文件后重启")
            return self.provider.call(name, args)
        if name == "search_indices":
            return dict(source="csv_index", indices=self.store.search_indices(args.get("query", "")))
        if name == "get_refresh_status":
            assert self.daily is not None
            return self.daily.status()
        if name == "get_market_breadth":
            assert self.daily is not None
            return dict(
                source="sqlite_local",
                timezone="Asia/Shanghai",
                unit="count",
                data=self.daily.breadth(args["start_date"], args["end_date"]),
            )
        if name == "get_daily_bars":
            assert self.daily is not None
            codes = [normalize_symbol(code) for code in args["stock_codes"]]
            if not all(codes):
                raise ValueError("股票代码无效")
            return dict(
                source="sqlite_local",
                timezone="Asia/Shanghai",
                symbols=codes,
                data=self.daily.bars(codes, args.get("start_date", ""), args.get("end_date", "")),
            )
        point = "time" in args
        if point:
            start = end = minute_time(args["time"])
        else:
            start, end = time_range(args["start_time"], args["end_time"])
        symbol = args["symbol"]
        period = args.get("period", "")
        if name.startswith("get_auction_"):
            if self.auction is None:
                raise ValueError("竞价索引未就绪")
            result = self.auction.query(
                symbol,
                start,
                end,
                0 if point else args.get("page", 0),
                1 if point else args.get("page_size") or 500,
            )
            if point:
                return dict(
                    symbol=symbol,
                    source=result["source"],
                    timezone=result["timezone"],
                    units=result["units"],
                    time=iso_time(start),
                    found=result["total"] > 0,
                    snapshot=result["data"][0] if result["data"] else None,
                )
            return result
        period = normalize_period(symbol, period)
        if name.startswith("get_index_"):
            result = self.store.index_result(
                symbol, period, self.store.index_bars(symbol, period, start, end)
            )
        else:
            result = query_with_source(
                self.store, self.provider, symbol, period, args.get("source", ""), start, end
            )
        if point:
            rows = result.pop("data")
            result.update(time=iso_time(start), found=bool(rows), bar=rows[0] if rows else None)
        return result


def create_mcp_server(tools):
    server = Server("stock-god-minute-data", version="6.0.0")

    @server.list_tools()
    async def list_tools():
        return list(tools.definitions.values())

    @server.call_tool(validate_input=False)
    async def call_tool(name, arguments):
        try:
            result = transport_value(await run_in_threadpool(tools.call, name, arguments))
            return types.CallToolResult(
                content=[
                    types.TextContent(
                        type="text",
                        text=json.dumps(result, ensure_ascii=False, separators=(",", ":"), allow_nan=False),
                    )
                ],
                structuredContent=result,
                isError=False,
            )
        except Exception as error:
            message = str(error)
            if tools.provider is not None:
                message = tools.provider._redact(message)
            return types.CallToolResult(content=[types.TextContent(type="text", text=message)], isError=True)

    return server
