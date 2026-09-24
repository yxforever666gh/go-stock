"""Original Go provider fixtures, exercised without network calls."""

import pytest

from stock_god.market.funds import (
    _date,
    _datetime,
    _etf_code,
    _fund_code,
    _money,
    _numeric,
    _parse_basic,
    _parse_funds,
    _parse_holdings,
    _parse_identities,
    _parse_nav_html,
    _parse_quotes,
    _parse_sina_fundamentals,
    _query,
    _sort,
)
from stock_god.market.common import MarketDataError


EAST_FUND = '{"Data":{"Datas":[{"FCODE":"000001","SHORTNAME":"价值混合","FTYPE":"混合型","DWJZ":"1.2345","FSRQ":"2026-08-28","RZDF":"1.2","SYL_Z":"2.3","SYL_Y":"3.4","SYL_3Y":"4.5","SYL_6Y":"5.6","SYL_1N":"6.7","SYL_3N":"7.8","SYL_JN":"8.9","SYL_LN":"9.1","JJGM":"12.5亿元","GMRQ":"2026-06-30"}]}}'
SINA_FUND = 'IO.XSRV2.CallbackList({"data":[{"symbol":"000002","sname":"海外QDII","type":"QDII","dwjz":"2.1","jzrq":"2026/08/27","zdf":"0.5","1n":"12.3","jjgm":"2.5亿"}]})'
RANKHANDLER = 'var rankData = {datas:["002910,易方达供给改革混合,YFDGGGGHH,2026-08-27,3.7028,3.7028,-0.4,-0.46,5.35,18.44,20.1,30.2,0,40.3,12.5,130.5,x,x,66.5"],allRecords:1};'
NAV_HTML = '<html><body><table><thead><tr><th>关注</th><th>比较</th><th>序号</th><th>基金代码</th><th>基金简称</th><th colspan="2">2026-08-27</th></tr></thead><tbody><tr id="tr510300"><td>关注</td><td>比较</td><td>1</td><td><a>510300</a></td><td>沪深300ETF</td><td>4.0000</td><td>4.5000</td><td>3.9800</td><td>4.4800</td><td>0.0200</td><td>0.50%</td><td>4.0500</td><td>1.25%</td></tr></tbody></table></body></html>'
BASIC_HTML = "<table><tr><th>管理费率</th><td>0.15%（每年）</td><th>托管费率</th><td>0.05%（每年）</td></tr><tr><th>业绩比较基准</th><td>沪深300指数</td><th>跟踪标的</th><td>沪深300指数</td></tr></table>"


def tencent_quote(
    code="sh510300", *, price="4.123", amount="2835120646", scale="1103.25", shares="23578687700"
):
    parts = [""] * 87
    parts[1], parts[3], parts[30], parts[32], parts[35], parts[37], parts[38] = (
        "沪深300ETF",
        price,
        "20260828100102",
        "1.23",
        "x/10/" + amount,
        "283512",
        "2.5",
    )
    parts[44], parts[72] = scale, shares
    return f'v_{code}="' + "~".join(parts) + '";'


def test_original_fund_ranking_json_and_csv_metrics():
    east = _parse_funds(EAST_FUND, "eastmoney")[0]
    assert east["category"] == "mixed"
    assert east["nav"] == 1.2345
    assert east["scale"] == 1.25e9
    sina = _parse_funds(SINA_FUND, "sina_fund")[0]
    assert sina["category"] == "qdii"
    assert sina["oneYearReturn"] == 12.3
    assert sina["scale"] == 2.5e8
    assert sina["navDate"] == "2026-08-27"
    csv = _parse_funds(RANKHANDLER, "eastmoney")[0]
    assert [
        csv[key]
        for key in (
            "dayReturn",
            "weekReturn",
            "monthReturn",
            "threeMonthReturn",
            "sixMonthReturn",
            "oneYearReturn",
            "threeYearReturn",
            "yearToDateReturn",
            "sinceInceptionReturn",
        )
    ] == [-0.4, -0.46, 5.35, 18.44, 20.1, 30.2, 40.3, 12.5, 130.5]
    assert csv["scale"] == 6.65e9
    assert not csv.get("scaleDate")


def test_original_exchange_shapes_strip_markup_and_reject_lof():
    sse = _parse_identities(
        '{"result":[{"fundCode":"510300","fundAbbr":"300ETF","INDEX_NAME":"沪深300","listingDate":"2012-05-28","subClass":"01"}]}',
        "SH",
    )
    assert sse[0]["code"] == "sh510300"
    assert sse[0]["name"] == "300ETF"
    assert sse[0]["category"] == "broad"
    raw = '[{"metadata":{"pagecount":37},"data":[{"sys_key":"<a>159001</a>","jjjcurl":"<a>货币ETF</a>","jjlb":"ETF","tzlb":"货币型","ssrq":"2013-01-28","dqgm":"125.50亿元"},{"sys_key":"160001","jjjcurl":"LOF基金","jjlb":"LOF"}]}]'
    szse = _parse_identities(raw, "SZ")
    assert len(szse) == 1
    assert szse[0]["code"] == "sz159001"
    assert szse[0]["category"] == "money"
    assert "scale" not in szse[0]


def test_original_quote_units_and_timestamp():
    quote = _parse_quotes(tencent_quote(), "tencent")["sh510300"]
    assert quote["price"] == 4.123
    assert quote["amount"] == 2835120646
    assert quote["scale"] == 110325000000
    assert quote["shares"] == 23578687700
    assert quote["quoteTime"] == "2026-08-28T10:01:02+08:00"
    assert quote["netInflow"] is None
    quote = _parse_quotes(tencent_quote(amount="0", scale="0", shares="0"), "tencent")["sh510300"]
    assert quote["amount"] == quote["scale"] == quote["shares"] == 0
    parts = (
        ["创业板ETF", "2.0", "2.1", "2.2", "2.3", "1.9", "0", "0", "1000", "200000"]
        + ["0"] * 20
        + ["2026-08-28", "10:02:03", "00"]
    )
    sina = _parse_quotes('var hq_str_sz159915="' + ",".join(parts) + '";', "sina")["sz159915"]
    assert sina["changeRate"] == pytest.approx((2.2 / 2.1 - 1) * 100)
    east = _parse_quotes(
        '{"data":{"diff":[{"f12":"510300","f13":1,"f2":4.12,"f3":1.1,"f6":900000,"f8":2.2,"f62":88000,"f124":1787882462}]}}',
        "eastmoney",
    )
    assert east["sh510300"]["netInflow"] == 88000


def test_original_nav_basic_and_holding_fixtures():
    nav = _parse_nav_html(NAV_HTML, [{"code": "sh510300"}])["sh510300"]
    assert nav["nav"] == 4
    assert nav["premiumRate"] == 1.25
    assert nav["navDate"] == "2026-08-27"
    assert nav["shares"] is nav["scale"] is None
    basic = _parse_basic(BASIC_HTML, "sh510300")
    assert basic["trackingIndex"] == "沪深300指数"
    assert basic["managementFee"] == 0.15
    holdings = _parse_holdings(
        '{"data":[{"GPDM":"600519","GPJC":"贵州茅台","JZBL":"8.5","FSRQ":"2026/06/30"},{"GPDM":"600519","GPJC":"duplicate","JZBL":"9"},{"GPDM":"601318","GPJC":"中国平安","JZBL":"0"}]}'
    )
    assert [item["weight"] for item in holdings] == [8.5, 0]
    assert holdings[0]["asOf"] == "2026-06-30"
    sina = _parse_sina_fundamentals(
        'cb({"data":[{"symbol":"510300","dwjz":"3.9","jzrq":"20260827","shares":"10亿份","jjgm":"39亿元"}]})',
        [{"code": "sh510300"}],
    )
    assert sina["sh510300"]["shares"] == 1e9
    assert sina["sh510300"]["scale"] == 3.9e9


@pytest.mark.parametrize("raw", ['{"Data":null,"ErrCode":4,"ErrMsg":"404"}', "<html>blocked</html>"])
def test_provider_business_errors_are_not_successful_empty(raw):
    with pytest.raises(MarketDataError):
        _parse_funds(raw, "eastmoney")


@pytest.mark.parametrize(
    "value,expected",
    [("0", 0), ("0%", 0), ("--", None), ("nan", None), ("Infinity", None), ("1,234.5%", 1234.5)],
)
def test_null_and_zero_are_distinct(value, expected):
    assert _numeric(value) == expected


def test_units_dates_codes_and_nullable_sort():
    assert _money("1.5万亿") == 1.5e12
    assert _money("5", rankhandler=True) == 5e8
    assert _date("2026.8.7") == "2026-08-07"
    assert _date("2026-08-27T23:30:00Z") == "2026-08-28"
    assert _date("2026-02-30") == ""
    assert _datetime("20260828100100") == "2026-08-28T10:01:00+08:00"
    assert _fund_code("of000001") == "000001"
    assert _etf_code("sh159915") == ""
    assert _etf_code("000001") == ""
    assert _etf_code("510300.SH") == ""
    values = [dict(code="c", value=None), dict(code="b", value=0), dict(code="a", value=0)]
    assert [i["code"] for i in _sort(values, "value", "asc")] == ["a", "b", "c"]
    assert [i["code"] for i in _sort(values, "value", "desc")] == ["a", "b", "c"]


@pytest.mark.parametrize(
    "query,etf",
    [
        ({"category": "bad"}, False),
        ({"period": "unknown"}, False),
        ({"page": -1}, False),
        ({"pageSize": 101}, True),
        ({"sort": "unknown"}, True),
        ({"sortDirection": "sideways"}, True),
    ],
)
def test_queries_validate_before_provider_calls(query, etf):
    with pytest.raises(ValueError):
        _query(query, etf)
