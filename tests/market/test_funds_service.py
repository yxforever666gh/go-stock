import json
import sqlite3
from threading import Lock
import time

import httpx
import pytest

from stock_god.config import AppConfig
from stock_god.market.common import Transport
from stock_god.market.funds import Funds
from test_funds_parsers import EAST_FUND, SINA_FUND, NAV_HTML, BASIC_HTML, tencent_quote


class Service(Funds):
    def __init__(self, root, handler):
        self.config = AppConfig(
            root=root,
            main_db=root / "data/stock.db",
            minute_db=root / "data/minute.db",
            frontend_dist=root / "frontend/dist",
            market_data_root=root / "market-data",
            market_index_dir=root / "index",
            scheduler_enabled=False,
        )
        self.settings = {}
        self.http = Transport({}, httpx.Client(transport=httpx.MockTransport(handler)))


def response(body, status=200):
    return httpx.Response(status, content=body.encode("utf8") if isinstance(body, str) else body)


def test_fund_rankings_fetch_all_pages_before_local_search(tmp_path):
    pages = []

    def handle(request):
        assert request.url.host == "fund.eastmoney.com"
        page = int(request.url.params["pi"])
        pages.append(page)
        data = f'var rankData={{datas:["00000{page},基金{page},PY,2026-08-27,1,1,0,0,0,0,0,{page},0,0,0,0,x,x,10"],allRecords:20211,allPages:3}}'
        return response(data)

    result = Service(tmp_path, handle).fund_rankings({"q": "000003"})
    assert sorted(pages) == [1, 2, 3]
    assert result["status"] == "ok"
    assert result["data"]["total"] == 1
    assert result["data"]["items"][0]["code"] == "000003"
    assert result["data"]["items"][0]["rank"] == 1


def test_primary_empty_search_does_not_trigger_fallback(tmp_path):
    calls = []

    def handle(request):
        calls.append(request.url.host)
        return response(EAST_FUND)

    result = Service(tmp_path, handle).fund_rankings({"q": "missing"})
    assert calls == ["fund.eastmoney.com"]
    assert result["data"]["items"] == [] and result["status"] == "ok"


def test_fund_failure_fallback_and_provenance(tmp_path):
    def handle(request):
        return response("limited", 429) if request.url.host == "fund.eastmoney.com" else response(SINA_FUND)

    result = Service(tmp_path, handle).fund_rankings({})
    assert result["status"] == "partial"
    assert result["source"] == "sina_fund"
    assert result["errors"][0]["code"] == "rate_limited"
    assert result["data"]["items"][0]["navDate"] == "2026-08-27"
    assert [s["provider"] for s in result["sources"]] == ["eastmoney", "sina_fund"]


def test_fund_all_sources_fail_is_unavailable(tmp_path):
    def handle(request):
        return response("limited", 429) if request.url.host == "fund.eastmoney.com" else response("[]")

    result = Service(tmp_path, handle).fund_rankings({})
    assert result["status"] == "unavailable"
    assert [e["code"] for e in result["errors"]] == ["rate_limited", "empty_data"]


def test_partial_fund_pages_keep_primary_values_fill_nulls_from_fallback(tmp_path):
    def handle(request):
        if request.url.host == "fund.eastmoney.com":
            if request.url.params["pi"] == "2":
                return response("down", 500)
            return response(
                'var rankData={datas:["000001,价值混合,PY,2026-08-27,1.2,1.2,0,--,1,2,3,12,0,4,5,6,x,x,--"],allPages:2}'
            )
        return response(
            'cb({"data":[{"symbol":"000001","sname":"new name","1n":"99","z":"3","jjgm":"2亿"}]})'
        )

    result = Service(tmp_path, handle).fund_rankings({})
    item = result["data"]["items"][0]
    assert result["status"] == "partial"
    assert item["name"] == "价值混合"
    assert item["oneYearReturn"] == 12
    assert item["weekReturn"] == 3
    assert item["dayReturn"] == 0


def identity_response(code="510300", name="沪深300ETF"):
    return json.dumps(
        {
            "result": [
                {
                    "SEC_CODE": code,
                    "SEC_NAME": name,
                    "INDEX_NAME": "沪深300",
                    "LIST_DATE": "2012-05-28",
                    "STATUS": "listed",
                }
            ]
        },
        ensure_ascii=False,
    )


def test_exchange_pagination_preserves_successes_and_authoritative_name(tmp_path):
    pages = []

    def handle(request):
        if request.url.host == "query.sse.com.cn":
            return response(identity_response())
        if request.url.host == "www.szse.cn":
            page = int(request.url.params["PAGENO"])
            pages.append(page)
            if page == 2:
                return response("limited", 429)
            return response(
                json.dumps(
                    [
                        {
                            "metadata": {"pagecount": 3},
                            "data": [
                                {
                                    "sys_key": f"15900{page}",
                                    "jjjcurl": f"ETF-{page}",
                                    "jjlb": "ETF",
                                    "ssrq": "2020-01-01",
                                }
                            ],
                        }
                    ]
                )
            )
        raise AssertionError("identity fallback must not replace a successful exchange list")

    result = Service(tmp_path, handle)._etf_identities(time.monotonic() + 12)
    assert sorted(pages) == [1, 2, 3]
    assert result["status"] == "partial"
    assert {i["code"] for i in result["data"]} == {"sh510300", "sz159001", "sz159003"}
    assert result["errors"][0]["code"] == "rate_limited"


def test_identity_fallback_is_used_only_after_both_exchanges_fail(tmp_path):
    def handle(request):
        if request.url.host == "push2.eastmoney.com":
            return response('{"data":{"diff":[{"f12":"510300","f13":1,"f14":"ETF fallback"}]}}')
        return response("unavailable", 503)

    result = Service(tmp_path, handle)._etf_identities(time.monotonic() + 12)
    assert result["status"] == "partial"
    assert result["data"][0]["code"] == "sh510300"
    assert len(result["errors"]) == 2


def test_empty_exchange_lists_keep_identity_fallback_visible(tmp_path):
    def handle(request):
        if request.url.host == "push2.eastmoney.com":
            return response('{"data":{"diff":[{"f12":"510300","f13":1,"f14":"ETF fallback"}]}}')
        return response("[]")

    result = Service(tmp_path, handle)._etf_identities(time.monotonic() + 12)
    assert result["status"] == "partial"
    assert result["errors"][0]["code"] == "identity_fallback"


def test_etf_detail_complete_chain_keeps_authoritative_fields_and_units(tmp_path):
    calls = []

    def handle(request):
        calls.append(str(request.url))
        if request.url.host == "query.sse.com.cn":
            return response(identity_response())
        if request.url.host == "www.szse.cn":
            return response("[]")
        if request.url.host == "qt.gtimg.cn":
            return response(tencent_quote().encode("gbk"))
        if request.url.host == "push2.eastmoney.com":
            return response('{"data":{"diff":[{"f12":"510300","f13":1,"f2":99,"f14":"WRONG","f62":88000}]}}')
        if request.url.path == "/ETFN_jzzzl.html":
            return response(NAV_HTML)
        if request.url.path == "/jbgk_510300.html":
            return response(BASIC_HTML)
        if request.url.path.endswith("FundMNInverstPosition"):
            return response('{"Data":[{"GPDM":"600519","GPJC":"贵州茅台","JZBL":"8.5","FSRQ":"2026/06/30"}]}')
        raise AssertionError(str(request.url))

    result = Service(tmp_path, handle).etf_detail("510300")
    assert result["status"] == "ok"
    item = result["data"]
    assert item["name"] == "沪深300ETF" and item["market"] == "SH"
    assert item["price"] == 4.123
    assert item["netInflow"] == 88000
    assert item["scale"] == 110325000000
    assert item["shares"] == 23578687700
    assert item["nav"] == 4 and item["premiumRate"] == 1.25
    assert item["managementFee"] == 0.15 and item["trackingIndex"] == "沪深300"
    assert item["holdings"][0]["asOf"] == "2026-06-30"
    assert item["chartInstrument"] == {"code": "sh510300", "assetType": "etf", "market": "SH"}
    assert not any("sinajs" in call or "NetValueReturn" in call for call in calls)


def test_holding_failure_retains_nav_and_returns_partial(tmp_path):
    def handle(request):
        if request.url.path == "/ETFN_jzzzl.html":
            return response(NAV_HTML)
        if request.url.path == "/jbgk_510300.html":
            return response(BASIC_HTML)
        if request.url.path.endswith("FundMNInverstPosition"):
            return response('{"Data":null,"ErrCode":61136,"ErrMsg":"holding unavailable"}')
        raise AssertionError(str(request.url))

    result = Service(tmp_path, handle)._etf_fundamental_provider(
        [{"code": "sh510300"}], "eastmoney_fund", time.monotonic() + 12
    )
    assert result["status"] == "partial"
    assert result["data"]["sh510300"]["nav"] == 4
    assert "61136" in result["errors"][0]["message"]


def test_etf_rankings_do_not_fanout_detail_calls(tmp_path):
    def handle(request):
        assert request.url.path == "/ETFN_jzzzl.html"
        return response(NAV_HTML)

    values = Service(tmp_path, handle)._etf_fundamental_provider(
        [{"code": "sh510300"}, {"code": "sz159915"}], "eastmoney_fund", time.monotonic() + 12
    )
    assert values["data"]["sh510300"]["nav"] == 4


def test_identity_outage_does_not_become_not_found(tmp_path):
    result = Service(tmp_path, lambda request: response("unavailable", 503)).etf_detail("510300")
    assert result["status"] == "unavailable"
    assert all(error["code"] != "not_found" for error in result["errors"])


def test_known_exchange_list_missing_code_is_not_found(tmp_path):
    def handle(request):
        return response(identity_response()) if request.url.host == "query.sse.com.cn" else response("[]")

    with pytest.raises(LookupError):
        Service(tmp_path, handle).etf_detail("159915")


def test_partial_exchange_outage_cannot_prove_a_missing_etf_is_unlisted(tmp_path):
    def handle(request):
        return (
            response(identity_response()) if request.url.host == "query.sse.com.cn" else response("down", 503)
        )

    result = Service(tmp_path, handle).etf_detail("159915")
    assert result["status"] == "unavailable"
    assert all(error["code"] != "not_found" for error in result["errors"])


def test_explicit_exchange_delisting_is_not_overridden_by_old_listing_date(tmp_path):
    def handle(request):
        if request.url.host == "query.sse.com.cn":
            return response(
                '{"result":[{"SEC_CODE":"510300","SEC_NAME":"ETF","LIST_DATE":"2012-05-28","STATUS":"delisted"},{"SEC_CODE":"510500","SEC_NAME":"中证500ETF","STATUS":"listed"}]}'
            )
        return response("[]")

    values = Service(tmp_path, handle)._etf_identities(time.monotonic() + 12)
    assert [item["code"] for item in values["data"]] == ["sh510500"]


def test_quote_batches_are_bounded_and_concurrent(tmp_path):
    lock = Lock()
    active = 0
    maximum = 0
    sizes = []

    def handle(request):
        nonlocal active, maximum
        codes = request.url.path.split("q=", 1)[1].split(",")
        with lock:
            active += 1
            maximum = max(maximum, active)
            sizes.append(len(codes))
        time.sleep(0.02)
        with lock:
            active -= 1
        return response("".join(tencent_quote(code) for code in codes))

    identities = [{"code": f"sh51{i:04d}"} for i in range(205)]
    result = Service(tmp_path, handle)._etf_quote_provider(identities, "tencent", time.monotonic() + 12)
    assert sorted(sizes) == [45, 80, 80]
    assert 1 < maximum <= 6
    assert len(result["data"]) == 205


def test_fund_search_is_read_only_and_keeps_legacy_wire_shape(tmp_path):
    data = tmp_path / "data"
    data.mkdir()
    path = data / "stock.db"
    with sqlite3.connect(path) as db:
        db.execute(
            "CREATE TABLE fund_basic(id INTEGER,code TEXT,name TEXT,deleted_at TEXT,net_growth_ytd REAL)"
        )
        db.execute("INSERT INTO fund_basic VALUES(1,'000001','价值基金',NULL,0)")
        db.execute("INSERT INTO fund_basic VALUES(2,'000002','已删除基金','2026-01-01',1)")
    before = path.read_bytes()
    result = Service(tmp_path, lambda request: pytest.fail("fund search must not fetch")).fund_search("基金")
    assert len(result) == 1 and result[0]["ID"] == 1
    assert result[0]["netGrowthYTD"] == 0
    assert result[0]["netUnitValue"] is None
    assert path.read_bytes() == before


def empty_fund_database(root):
    data = root / "data"
    data.mkdir()
    path = data / "stock.db"
    with sqlite3.connect(path) as db:
        db.execute("CREATE TABLE fund_basic(id INTEGER,code TEXT,name TEXT,deleted_at TEXT,updated_at TEXT)")
    return path


def test_new_database_search_loads_original_catalogue_with_bounded_cache(tmp_path):
    path = empty_fund_database(tmp_path)
    before = path.read_bytes()
    calls = []

    def handle(request):
        calls.append(request.url.path)
        return response(
            '<ul class="num_right"><li><a href="/000001.html">（000001）价值基金</a></li><li><a href="/000002.html">(000002)成长基金</a></li></ul>'
        )

    service = Service(tmp_path, handle)
    value = service.fund_search("价值")
    assert value[0]["code"] == "000001" and value[0]["ID"] == 0
    assert value[0]["netUnitValue"] is None
    assert service.fund_search("不存在") == []
    assert service.fund_search("000002")[0]["name"] == "成长基金"
    assert calls == ["/allfund.html"]
    assert path.read_bytes() == before


def test_unavailable_catalogue_never_becomes_empty_search_success(tmp_path):
    empty_fund_database(tmp_path)
    from stock_god.market.common import MarketDataError

    with pytest.raises(MarketDataError):
        Service(tmp_path, lambda request: response("down", 503)).fund_search("基金")


@pytest.mark.parametrize("upstream_ok", [True, False])
def test_stale_local_fund_metadata_survives_upstream_outage(tmp_path, upstream_ok):
    path = empty_fund_database(tmp_path)
    with sqlite3.connect(path) as db:
        db.execute("INSERT INTO fund_basic VALUES(7,'000001','旧基金名称',NULL,'2020-01-01')")
    before = path.read_bytes()

    def handle(request):
        return (
            response('<ul class="num_right"><li><a>(000001)新基金名称</a></li></ul>')
            if upstream_ok
            else response("down", 503)
        )

    result = Service(tmp_path, handle).fund_search("000001")
    assert result[0]["name"] == ("新基金名称" if upstream_ok else "旧基金名称")
    assert result[0]["ID"] == 7
    assert path.read_bytes() == before
