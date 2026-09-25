import json
import sqlite3
from datetime import datetime, timedelta
from pathlib import Path

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from stock_god.market import MarketDataError, create_router
from stock_god.market.charts import aggregate
from stock_god.market.common import CN, instrument
from stock_god.market.prediction_inputs import filter_at_cutoff
from stock_god.market.quotes import parse_tencent
from stock_god.market.text_analysis import analyze_tokens, dedupe
from stock_god.market.themes import next_stage


def tencent(price="10", high="11", low="9"):
    fields = ["0"] * 50
    values = {
        1: "浦发银行",
        3: price,
        4: "9.8",
        5: "9.9",
        30: "20260924145000",
        32: "2.04",
        33: high,
        34: low,
        35: "10/1000/1000000",
        36: "1000",
        37: "100",
    }
    for index, value in values.items():
        fields[index] = value
    return 'v_sh600000="' + "~".join(fields) + '";'


def test_quote_parses_high_not_percent_and_lot_units():
    row = parse_tencent(tencent())[0]
    assert row["high"] == 11
    assert row["low"] == 9
    assert row["volume"] == 100000
    assert row["amount"] == 1000000
    assert row["asOf"] == "2026-09-24T14:50:00+08:00"
    with pytest.raises(MarketDataError, match="OHLC"):
        parse_tencent(tencent(high="2.04"))


def test_sina_fallback_snapshot_never_updates_followed_price(make_market, config):
    calls = []

    def request(req):
        calls.append(req.url.host)
        if req.url.host == "qt.gtimg.cn":
            return httpx.Response(503)
        fields = ["0"] * 33
        for index, value in {
            0: "浦发银行",
            1: "9.9",
            2: "9.8",
            3: "10",
            4: "11",
            5: "9",
            8: "10000",
            9: "100000",
            30: "2026-09-24",
            31: "14:50:00",
        }.items():
            fields[index] = value
        return httpx.Response(
            200, content=('var hq_str_sh600000="' + ",".join(fields) + '";').encode("gb18030")
        )

    service = make_market(request)
    before = config.main_db.read_bytes()
    result = service.stock_snapshot("600000.SH")
    assert result["当前价格"] == "10.0"
    assert result["costPrice"] == 9
    assert calls == ["qt.gtimg.cn", "hq.sinajs.cn"]
    assert config.main_db.read_bytes() == before


def test_bad_provider_does_not_return_zero_success(make_market):
    service = make_market(lambda request: httpx.Response(200, text="broken"))
    with pytest.raises(MarketDataError, match="quotes unavailable"):
        service.quote("600000")


def test_eastmoney_reports_send_research_page_referer(make_market):
    requests = []

    def request(req):
        requests.append(req)
        if req.headers.get("referer") != "https://data.eastmoney.com/report/stock.jshtml":
            return httpx.Response(567)
        if req.headers.get("origin") != "https://data.eastmoney.com":
            return httpx.Response(567)
        return httpx.Response(200, json={"data": [{"title": "fixture report"}]})

    service = make_market(request)
    assert service.research_reports("sh600519") == [{"title": "fixture report"}]
    assert service.research_reports("016", industry=True) == [{"title": "fixture report"}]
    assert [req.url.path for req in requests] == ["/report/list2", "/report/list"]
    assert requests[0].method == "POST"
    assert json.loads(requests[0].content)["code"] == "600519"
    assert requests[1].method == "GET"


def test_calendar_strict_outage_and_holiday(make_market):
    with pytest.raises(MarketDataError, match="token"):
        make_market().is_trading_day(datetime(2026, 10, 1, tzinfo=CN))

    def request(req):
        assert json.loads(req.content)["api_name"] == "trade_cal"
        return httpx.Response(
            200,
            json={
                "code": 0,
                "data": {
                    "fields": ["exchange", "cal_date", "is_open"],
                    "items": [["SSE", "20261001", 0], ["SSE", "20260930", 1]],
                },
            },
        )

    service = make_market(request, {"tushareToken": "fixture"})
    assert service.is_trading_day(datetime(2026, 10, 1, tzinfo=CN)) is False
    assert service.is_trading_day(datetime(2026, 9, 30, tzinfo=CN)) is True


def test_settings_snapshot_and_injected_client_ownership(make_market):
    settings = {"tushareToken": "original", "nested": {"items": [1]}}
    service = make_market(settings=settings)
    settings["nested"]["items"].append(2)
    assert service.settings["nested"]["items"] == [1]
    clone = service.with_settings({"tushareToken": "new"})
    clone.close()
    assert not service.http.client.is_closed
    assert service.settings["tushareToken"] == "original"


def test_source_date_does_not_make_sibling_news_safe():
    cutoff = datetime(2026, 9, 24, 10, tzinfo=CN)
    filtered, _, keep = filter_at_cutoff(
        {
            "safe": {"publishedAt": "2026-09-24T09:00:00+08:00", "title": "safe"},
            "unknown": {"title": "unverified"},
            "future": {"publishedAt": "2026-09-24T11:00:00+08:00", "title": "future"},
        },
        cutoff,
    )
    assert keep and set(filtered) == {"safe"}


def test_bar_cache_get_does_not_fetch(make_market, config):
    service = make_market()
    at = datetime(2026, 9, 24, 9, 30, tzinfo=CN)
    with sqlite3.connect(config.minute_db) as db:
        db.execute(
            "INSERT INTO minute_bar VALUES(?,?,?,?,?,?,?,?,?,?)",
            (
                "600000.SH",
                int(at.timestamp() * 1000),
                10,
                11,
                9,
                10.5,
                100,
                1000,
                "tencent:none",
                int(at.timestamp() * 1000),
            ),
        )
    assert len(service.cached_bars("sh600000", at, at + timedelta(minutes=1))) == 1
    assert service.cached_bars("sh600000", at + timedelta(days=1), at + timedelta(days=2)) == []


def test_aggregation_respects_midday_session():
    rows = [
        {
            "time": f"2026-09-24T{clock}:00+08:00",
            "open": 10.0,
            "high": 11.0,
            "low": 9.0,
            "close": 10.5,
            "volume": 1.0,
            "amount": 10.0,
            "source": "fixture:none",
        }
        for clock in ("11:29", "13:00", "13:01")
    ]
    grouped = aggregate(rows, "60m")
    assert len(grouped) == 2
    assert grouped[0]["volume"] == 1
    assert grouped[1]["volume"] == 2


def test_drawings_cas_and_immutable_revision_history(make_market, config):
    service = make_market()
    point = {"time": "2026-09-24T09:30:00+08:00", "value": 10}
    result = service.drawings(
        "600000",
        "stock",
        "SH",
        "day",
        "none",
        expected_revision=0,
        drawings=[{"id": "one", "type": "horizontal_line", "points": [point]}],
    )
    assert result["revision"] == 1
    with pytest.raises(ValueError, match="revision conflict"):
        service.drawings("600000", "stock", "SH", "day", "none", expected_revision=0, drawings=[])
    deleted = service.drawings("600000", "stock", "SH", "day", "none", expected_revision=1, delete=True)
    assert deleted["revision"] == 2 and deleted["drawings"] == []
    with sqlite3.connect(config.main_db) as db:
        assert db.execute("SELECT COUNT(*) FROM chart_drawing_revisions").fetchone()[0] == 2
        with pytest.raises(sqlite3.IntegrityError, match="immutable"):
            db.execute("DELETE FROM chart_drawing_revisions")


def test_market_routes_remove_all_watchlist_and_validate_query(make_market):
    app = FastAPI()
    app.state.market = make_market()
    router = create_router()
    app.include_router(router)
    assert not any("watchlist" in route.path for route in router.routes)
    with TestClient(app) as client:
        assert client.get("/api/v1/market/fund-flows?scope=bad").status_code == 400
        assert client.get("/api/v1/instruments/sh600000/chart?limit=5001").status_code == 400
        assert client.get("/api/v1/watchlist/stocks").status_code == 404
        assert client.get("/api/v1/stocks/search?key=银行").json()[0]["name"] == "浦发银行"


def test_fund_flows_fallback_provenance_not_historical(make_market):
    def request(req):
        if "eastmoney" in req.url.host:
            return httpx.Response(503)
        return httpx.Response(
            200,
            json=[
                {
                    "category": "fixture",
                    "name": "测试行业",
                    "netamount": "100",
                    "inamount": "130",
                    "outamount": "30",
                    "avg_changeratio": ".03",
                }
            ],
        )

    service = make_market(request)
    result = service.fund_flows("sector")
    assert result["status"] == "partial" and result["source"] == "sina"
    assert result["data"][0]["changePct"] == 3
    historical = service.fund_flows("sector", "2025-01-01")
    assert historical["status"] == "unavailable"


def test_financial_score_preserves_negation_degree_and_transition():
    assert analyze_tokens(["不", "上涨"])["Score"] == -4
    assert analyze_tokens(["大幅", "上涨"])["Score"] == 7.2
    assert analyze_tokens(["上涨", "但是", "下跌"])["Score"] == -1


def test_news_deduplication_merges_sources():
    row = {
        "id": 1,
        "content": "人工智能产业加速发展",
        "title": "",
        "data_time": "2026-09-24T09:30:00+08:00",
        "source": "A",
    }
    result = dedupe([row, row | {"id": 2, "source": "B"}])
    assert len(result) == 1 and result[0]["sources"] == {"A", "B"}


def test_theme_lifecycle_only_advances_one_stage():
    assert next_stage(None, 100, 1, 5, False) == (1, "观察")
    prior = {"cycle_no": 1, "lifecycle_stage": "观察", "heat_score": 60}
    assert next_stage(prior, 100, 1, 1, False) == (1, "观察")
    assert next_stage(prior, 100, 1, 2, False) == (1, "发酵")
    assert next_stage(prior | {"lifecycle_stage": "退潮"}, 60, 1, 2, False) == (2, "观察")


def test_theme_freeze_idempotent_and_cutoff_safe(make_market):
    service = make_market()
    at = datetime(2026, 9, 24, 15, 10, tzinfo=CN)
    signal = {
        "name": "人工智能",
        "title": "新产品发布",
        "summary": "产业新产品",
        "source": "fixture",
        "kind": "news",
        "strength": 70,
        "at": at.isoformat(),
        "published": at.isoformat(),
        "available": at.isoformat(),
        "ref": "https://example.test/news/1",
        "stance": "supports",
        "rawHash": "fixture",
    }
    service.collect_theme_signals = lambda at: ([signal], [], [])
    first = service.refresh_themes(at)
    second = service.refresh_themes(at)
    assert first["status"] == "ok"
    assert first["frozenSnapshotIds"] == second["frozenSnapshotIds"]
    assert second["themes"][0]["existing"]
    assert service.prediction_theme_documents(at - timedelta(minutes=1)) == []
    assert len(service.theme_api(None, "list", {"date": at.date().isoformat()})["data"]["items"]) == 1


def test_financial_tokenization_matches_original_gse_fixture(make_market):
    service = make_market()
    tokenizer, _, _ = service._analyzers()
    samples = json.loads(
        (Path(__file__).parent / "fixtures/gse_financial_tokens.json").read_text(encoding="utf-8")
    )
    for text, expected in samples.items():
        assert list(tokenizer.cut(text, HMM=True)) == expected
        actual = service.sentiment_weighted(text)
        assert actual["result"] == analyze_tokens(expected)


def test_quote_exposes_execution_limits_and_suspension(make_market):
    service = make_market(
        lambda req: httpx.Response(200, content=tencent(price="10.78", high="10.78").encode("gb18030"))
    )
    value = service.quote("sh600000")
    assert value["limitUp"] and not value["limitDown"] and not value["suspended"]
    assert value["previousClose"] == 9.8


def test_auction_live_and_historical_cache_have_same_matched_values(make_market, monkeypatch):
    at = datetime(2026, 9, 24, 10, tzinfo=CN)
    monkeypatch.setattr("stock_god.market.evidence.now", lambda: at)

    def request(req):
        return httpx.Response(
            200,
            json={
                "data": {
                    "prePrice": 10,
                    "details": ["09:20:00,10.1,100,0,1", "09:29:00,10.2,200,0,2", "09:30:00,10.3,300,0,1"],
                }
            },
        )

    service = make_market(request)
    live = service.auction("sh600000")
    assert live["status"] == "partial"
    assert len(live["data"]["snapshots"]) == 2
    assert live["data"]["gapPct"] == pytest.approx(2)
    monkeypatch.setattr("stock_god.market.evidence.now", lambda: at + timedelta(days=1))
    cached = service.auction("sh600000", day="2026-09-24")
    assert cached["data"]["snapshots"] == [item | {"unmatchedSide": ""} for item in live["data"]["snapshots"]]
    assert cached["data"]["finalSnapshot"]["price"] == 10.2


def test_collect_failure_keeps_explicit_failed_evidence(make_market, monkeypatch):
    service = make_market()

    def fail():
        raise MarketDataError("all feeds failed")

    monkeypatch.setattr(service, "full_market", fail)
    with pytest.raises(MarketDataError) as error:
        service.collect_prediction_evidence(datetime(2026, 9, 24, 10, tzinfo=CN), set(), 12000)
    assert error.value.evidence["degraded"]
    assert error.value.evidence["candidates"] == []
    assert error.value.evidence["documents"][0]["error"] == "all feeds failed"


def test_opening_snapshot_does_not_require_unavailable_prior_minutes(make_market, monkeypatch):
    service = make_market()
    at = datetime(2026, 9, 24, 9, 31, tzinfo=CN)
    row = {
        "code": "sh600000",
        "name": "浦发银行",
        "price": 10,
        "preClose": 9.8,
        "changePct": 2.04,
        "open": 10,
        "high": 10.1,
        "low": 9.9,
        "volume": 10000,
        "amount": 100000,
        "turnover": 1,
        "mainFlow": 100,
        "listingDate": "19991110",
        "asOf": at.isoformat(),
    }
    monkeypatch.setattr(
        service, "full_market", lambda: {"rows": [row], "reported": 1, "source": "fixture", "errors": []}
    )
    monkeypatch.setattr(service, "bars", lambda *a, **kw: [])
    monkeypatch.setattr(service, "prediction_window", lambda *a, **kw: [])
    monkeypatch.setattr(
        service,
        "fund_flows",
        lambda *a, **kw: {
            "data": [],
            "source": "fixture",
            "asOf": at.isoformat(),
            "status": "ok",
            "errors": [],
        },
    )
    for method in (
        "_live_news",
        "global_indexes",
        "reuters_news",
        "investment_calendar",
        "cls_calendar",
        "industry_rank",
        "money_rank",
        "hot_stocks",
        "hot_events",
        "hot_topics",
        "long_tiger",
        "macro_evidence",
        "notices",
        "research_reports",
        "stock_financials",
        "stock_concepts",
        "money_trend",
        "interactive_answers",
    ):
        monkeypatch.setattr(service, method, lambda *a, **kw: [])
    evidence = service.collect_prediction_evidence(at, set(), 12000)
    assert evidence["candidates"] == [{"code": "sh600000", "name": "浦发银行", "referencePrice": 10}]
    assert evidence["windowStartAt"] == "2026-09-24T09:30:00+08:00"
    assert evidence["degraded"]
    assert any(doc["sourceId"] == "research2:quote:sh600000" for doc in evidence["documents"])


def test_stock_master_rejects_partial_refresh_without_mutating_database(make_market, config):
    def request(req):
        if req.url.host == "api.tushare.pro":
            return httpx.Response(
                200,
                json={"code": 0, "data": {"fields": ["ts_code"], "items": [["600000.SH"]], "has_more": True}},
            )
        return httpx.Response(503)

    service = make_market(request, {"tushareToken": "fixture"})
    before = config.main_db.read_bytes()
    with pytest.raises(MarketDataError, match="retained existing"):
        service.refresh_stock_master()
    assert config.main_db.read_bytes() == before


def test_stock_master_initial_seed_is_local_and_identified(make_market, config):
    with sqlite3.connect(config.main_db) as db:
        db.execute("DELETE FROM tushare_stock_basic")
        for column in ("symbol", "market", "exchange", "curr_type"):
            db.execute(f"ALTER TABLE tushare_stock_basic ADD COLUMN {column} TEXT")
    value = make_market().initialize_stock_master()
    assert value["source"] == "packaged_seed" and value["usedSeed"]
    assert value["validRows"] >= 5000
    assert make_market().initialize_stock_master()["changed"] is False


def test_news_fallback_never_invents_publication_date(make_market):
    def request(req):
        if req.url.path == "/nodeapi/telegraphList":
            return httpx.Response(503)
        return httpx.Response(
            200,
            text='<div class="telegraph-content-box"><span>10:00:00</span><span class="c-de0422">人工智能发布</span><div><a class="label-item">人工智能</a></div></div>',
        )

    rows = make_market(request)._live_news("财联社电报")
    assert rows[0]["dataTime"] is None
    assert rows[0]["subjects"] == ["人工智能"]
    assert rows[0]["isRed"]


def test_chart_adjustment_uses_verified_daily_ratio(make_market, monkeypatch):
    service = make_market()
    at = datetime(2026, 9, 24, 9, 30, tzinfo=CN)
    raw = {
        "time": at.isoformat(),
        "open": 10.0,
        "high": 12.0,
        "low": 9.0,
        "close": 11.0,
        "volume": 100.0,
        "amount": 1100.0,
        "source": "fixture:none",
    }
    monkeypatch.setattr(service, "_cached_minute_bars", lambda *a: [raw])

    def daily(code, start, end, period, adjustment, limit):
        assert period == "day"
        return [raw | {"time": "2026-09-24T00:00:00+08:00", "close": 5.5 if adjustment == "qfq" else 11}]

    monkeypatch.setattr(service, "_tencent_bars", daily)
    result = service.bars("sh600000", at, at, "1m", "qfq")
    assert result[0]["open"] == 5
    assert result[0]["close"] == 5.5
    assert result[0]["volume"] == 100
    assert "adjustment=qfq" in result[0]["source"]


def test_chart_instrument_rejects_stock_index_mismatch(make_market):
    with pytest.raises(ValueError, match="assetType stock"):
        make_market().chart("sh000001", "stock")
    assert instrument("000001", "index")["code"] == "sh000001"
