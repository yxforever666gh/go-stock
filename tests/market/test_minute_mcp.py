import json

from fastapi.testclient import TestClient

from stock_god.market.minute_app import create_app
from stock_god.market.minute_daily import DailyStore
from stock_god.market.minute_index import AuctionIndex
from stock_god.market.minute_local import STOCK_HEADERS, LocalStore
from test_minute_local import fixture, stock_row


def mcp(client, method, params):
    response = client.post(
        "/mcp",
        json=dict(jsonrpc="2.0", id=1, method=method, params=params),
        headers={"Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2025-06-18"},
    )
    assert response.status_code == 200, response.text
    assert "Mcp-Session-Id" not in response.headers
    assert "error" not in response.json(), response.json()
    return response.json()["result"]


def test_http_and_actual_stateless_mcp_session(tmp_path):
    root = tmp_path / "data"
    fixture(
        root,
        "A股个股/1分钟/sh600941.csv",
        ",".join(STOCK_HEADERS) + "\n" + stock_row("2026-08-12 13:45:00") + "\n",
    )
    store = LocalStore(root)
    index = AuctionIndex(root, tmp_path / "index", writable=True)
    index.prepare(store.auction_files)
    try:
        with TestClient(create_app(store, index, DailyStore(tmp_path / "index"))) as client:
            init = mcp(
                client,
                "initialize",
                dict(
                    protocolVersion="2025-06-18", capabilities={}, clientInfo=dict(name="test", version="1")
                ),
            )
            assert init["protocolVersion"] == "2025-06-18"
            listing = mcp(client, "tools/list", {})["tools"]
            assert len(listing) == 54
            assert len([tool for tool in listing if tool["name"].startswith("diemeng_")]) == 44
            result = mcp(
                client,
                "tools/call",
                dict(name="get_bar", arguments=dict(symbol="sh600941", time="2026-08-12T05:45:00Z")),
            )
            assert result["isError"] is False
            assert result["structuredContent"] == json.loads(result["content"][0]["text"])
            assert result["structuredContent"]["bar"]["volume"] == 22500
            missing = mcp(
                client,
                "tools/call",
                dict(
                    name="get_bar",
                    arguments=dict(symbol="sh600941", time="2026-08-12T13:45:00.000000001+08:00"),
                ),
            )
            assert missing["structuredContent"]["found"] is False
            error = mcp(client, "tools/call", dict(name="diemeng_stock_list", arguments={}))
            assert error["isError"] is True and "未配置" in error["content"][0]["text"]
            invalid = mcp(
                client,
                "tools/call",
                dict(name="get_bar", arguments=dict(symbol="../bad", time="2026-08-12 13:45")),
            )
            assert invalid["isError"] is True
            assert (
                client.get(
                    "/api/bars", params=dict(symbol="sh600941", start="2026-08-12", end="2026-08-12")
                ).json()["data"][0]["close"]
                == 10
            )
            assert client.get("/api/bars", params=dict(symbol="bad")).status_code == 400
            assert client.get("/api/bars", params=dict(symbol="sh600000")).status_code == 404
            assert client.get("/readyz").json()["tools"] == 54
            path = root / "A股个股/1分钟/sh600941.csv"
            path.write_text("bad header\n", encoding="utf-8")
            assert client.get("/api/bars", params={"symbol": "sh600941"}).status_code == 500
    finally:
        index.close()
