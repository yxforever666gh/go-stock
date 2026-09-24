"""Read-only fund and ETF providers with exchange-owned identity and field fallback."""

import html
import json
import math
import re
import time
from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
from datetime import datetime
from typing import Any
from urllib.parse import urlencode

import httpx
from bs4 import BeautifulSoup

from stock_god.config import AppConfig

from .common import (
    CN,
    USER_AGENT,
    ZERO_TIME,
    MarketDataError,
    Transport,
    database_rows,
    envelope,
    instrument,
    now,
)

RANK_URL = "https://fund.eastmoney.com/data/rankhandler.aspx"
SINA_FUND_URL = "https://vip.stock.finance.sina.com.cn/fund_center/data/jsonp.php/IO.XSRV2.CallbackList/NetValueReturn_Service.NetValueReturnOpen"
SSE_URL = "https://query.sse.com.cn/commonSoaQuery.do"
SZSE_URL = "https://www.szse.cn/api/report/ShowReport/data"
EAST_IDENTITY_URL = "https://push2.eastmoney.com/api/qt/clist/get"
EAST_QUOTES_URL = "https://push2.eastmoney.com/api/qt/ulist.np/get"
NAV_URL = "https://fund.eastmoney.com/ETFN_jzzzl.html"
BASIC_URL = "https://fundf10.eastmoney.com/jbgk_{}.html"
HOLDING_URL = "https://fundmobapi.eastmoney.com/FundMNewApi/FundMNInverstPosition"
FUND_CATEGORIES = {"all", "stock", "mixed", "bond", "index", "qdii", "fof"}
ETF_CATEGORIES = {"all", "broad", "industry", "cross_border", "bond", "commodity", "money"}
FUND_PERIODS = dict(
    zip(
        ("day", "week", "month", "3m", "6m", "1y", "3y", "ytd", "since_inception", "scale"),
        (
            "dayReturn",
            "weekReturn",
            "monthReturn",
            "threeMonthReturn",
            "sixMonthReturn",
            "oneYearReturn",
            "threeYearReturn",
            "yearToDateReturn",
            "sinceInceptionReturn",
            "scale",
        ), strict=False,
    )
)
EAST_SORTS = dict(
    zip(FUND_PERIODS, ("rzdf", "1zzf", "1yzf", "3yzf", "6yzf", "1nzf", "3nzf", "jnzf", "lnzf", "jjgm"), strict=False)
)
SINA_SORTS = dict(zip(FUND_PERIODS, ("zdf", "z", "y", "3y", "6y", "1n", "3n", "jn", "ln", "jjgm"), strict=False))
ETF_NUMBERS = (
    "price",
    "changeRate",
    "amount",
    "turnoverRate",
    "nav",
    "premiumRate",
    "shares",
    "scale",
    "netInflow",
)
FUND_NUMBERS = ("nav", *FUND_PERIODS.values())
QUOTE_NUMBERS = ("price", "changeRate", "amount", "turnoverRate", "netInflow", "shares", "scale")
FUNDAMENTAL_NUMBERS = ("nav", "premiumRate", "shares", "scale", "managementFee")
DATE_PATTERN = re.compile(r"\d{4}[-/.]\d{1,2}[-/.]\d{1,2}")


def _value(row, *keys):
    for key in keys:
        if key in row:
            return row[key]
    for key, value in row.items():
        if key.lower() in {k.lower() for k in keys}:
            return value
    return None


def _string(row, *keys):
    value = _value(row, *keys)
    if value is None:
        return ""
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    return str(value).strip()


def _numeric(value):
    if value is None or isinstance(value, bool):
        return None
    text = str(value).strip()
    for unit in (",", "%", "亿元", "亿份", "亿", "万元", "万份", "万", "元", "份"):
        text = text.replace(unit, "")
    try:
        result = float(text)
        return result if math.isfinite(result) else None
    except ValueError:
        return None


def _money(value, *, rankhandler=False):
    parsed = _numeric(value)
    if parsed is None:
        return None
    text = str(value)
    multiplier = (
        1e12
        if "万亿" in text
        else 1e8
        if "亿" in text
        else 1e4
        if "万" in text
        else 1e8
        if rankhandler
        else 1
    )
    return parsed * multiplier


def _date(value):
    text = str(value or "").strip()
    if text in ("", "-", "--"):
        return ""
    for fmt in ("%Y-%m-%d", "%Y%m%d", "%Y/%m/%d", "%Y.%m.%d"):
        try:
            return datetime.strptime(text, fmt).date().isoformat()
        except ValueError:
            pass
    try:
        parsed = datetime.fromisoformat(text.replace("Z", "+00:00"))
        return (parsed.astimezone(CN) if parsed.tzinfo else parsed).date().isoformat()
    except ValueError:
        return _date(text[:10]) if len(text) > 10 else ""


def _datetime(value):
    text = str(value or "").strip()
    try:
        parsed = (
            datetime.strptime(text, "%Y%m%d%H%M%S")
            if text.isdigit() and len(text) == 14
            else datetime.fromisoformat(text.replace("Z", "+00:00").replace("/", "-"))
        )
        return (parsed.replace(tzinfo=CN) if parsed.tzinfo is None else parsed.astimezone(CN)).isoformat()
    except ValueError:
        day = _date(text)
        return day + "T00:00:00+08:00" if day else ""


def _json(raw: str) -> Any:
    text = raw.strip().lstrip("\ufeff")
    starts = [i for i in (text.find("{"), text.find("[")) if i >= 0]
    if starts:
        start = min(starts)
        end = text.rfind("}" if text[start] == "{" else "]")
        text = text[start : end + 1]
    try:
        value = json.loads(text)
    except json.JSONDecodeError as exc:
        raise MarketDataError("provider returned invalid JSON") from exc
    if isinstance(value, dict) and (_numeric(_value(value, "ErrCode", "errCode", "errorCode")) or 0) != 0:
        raise MarketDataError(
            f"provider business error code {_string(value, 'ErrCode', 'errCode', 'errorCode')}: {_string(value, 'ErrMsg', 'errMsg', 'errorMessage', 'message') or 'provider business error'}"
        )
    return value


def _rows(value: Any, *keys: str) -> list[dict[str, Any]]:
    found = []
    if isinstance(value, list):
        for item in value:
            found.extend(_rows(item, *keys))
    elif isinstance(value, dict):
        if _string(value, *keys):
            found.append(value)
        else:
            for nested in value.values():
                found.extend(_rows(nested, *keys))
    return found


def _plain(value):
    return re.sub(r"<[^>]*>", "", html.unescape(str(value or "")), flags=re.S).strip()


def _fund_code(value):
    text = re.sub(r"^(?:sh|sz|of)", "", str(value).strip().lower())
    return text if re.fullmatch(r"\d{6}", text) else ""


def _etf_code(value, market="", *, unknown=False):
    text = str(value).strip().lower()
    if unknown and not re.fullmatch(r"(?:sh|sz)?\d{6}", text):
        text = text[-6:]
    if not re.fullmatch(r"(?:sh|sz)?\d{6}", text):
        return ""
    if market and not text.startswith(("sh", "sz")):
        text = market.lower() + text
    try:
        return instrument(text, "etf")["code"]
    except ValueError:
        return ""


def _fund_category(value, *, infer=False):
    text = str(value).lower().strip()
    if not infer and "fof" in text:
        return "fof"
    if "qdii" in text or (not infer and "海外" in text):
        return "qdii"
    if "fof" in text:
        return "fof"
    if not infer and any(word in text for word in ("指数", "联接")):
        return "index"
    if "债" in text:
        return "bond"
    if any(word in text for word in ("指数", "联接")) or (infer and "etf" in text):
        return "index"
    if "股票" in text or text in ("gp", "stock"):
        return "stock"
    if "混合" in text or text in ("hh", "mixed") or infer:
        return "mixed"
    return "all"


def _etf_category(name, tracking=""):
    text = (name + " " + tracking).lower().strip()
    groups = (
        ("money", ("货币", "现金", "添益", "保证金")),
        ("commodity", ("黄金", "白银", "有色", "豆粕", "原油")),
        ("bond", ("债", "国开", "国债")),
        ("cross_border", ("纳斯达克", "标普", "恒生", "日经", "德国", "法国", "沙特", "跨境")),
        ("broad", ("沪深300", "中证500", "中证1000", "上证50", "创业板", "科创50", "a500")),
    )
    return next((category for category, words in groups if any(word in text for word in words)), "industry")


def _merge(primary, fallback):
    result = deepcopy(primary)
    for key, value in fallback.items():
        if key not in result or result[key] is None or result[key] == "" or result[key] == []:
            result[key] = deepcopy(value)
    return result


def _parse_funds(raw, provider):
    result = []
    if provider == "eastmoney" and ("rankData" in raw or "datas:" in raw):
        marker = raw.find("datas")
        start = raw.find("[", marker)
        if marker < 0 or start < 0:
            raise MarketDataError("eastmoney rankhandler response is missing datas")
        try:
            rows, _ = json.JSONDecoder().raw_decode(raw[start:])
        except json.JSONDecodeError as exc:
            raise MarketDataError("invalid rankhandler datas array") from exc
        for raw_row in rows:
            if not isinstance(raw_row, str):
                raise MarketDataError("rankhandler row must be a CSV string")
            fields = raw_row.split(",")
            if len(fields) < 7:
                continue
            code, name = _fund_code(fields[0]), fields[1].strip()
            if not code or not name:
                continue
            item: dict[str, Any] = dict.fromkeys(FUND_NUMBERS)
            item.update(
                code=code,
                name=name,
                category=_fund_category(name, infer=True),
                navDate=_date(fields[3]),
                rank=0,
            )
            for index, key in (
                (4, "nav"),
                (6, "dayReturn"),
                (7, "weekReturn"),
                (8, "monthReturn"),
                (9, "threeMonthReturn"),
                (10, "sixMonthReturn"),
                (11, "oneYearReturn"),
                (13, "threeYearReturn"),
                (14, "yearToDateReturn"),
                (15, "sinceInceptionReturn"),
            ):
                item[key] = _numeric(fields[index]) if index < len(fields) else None
            item["scale"] = _money(fields[-1], rankhandler=True) if len(fields) > 16 else None
            result.append(item)
    else:
        rows = _rows(
            _json(raw),
            *(
                ("FCODE", "fundCode", "code")
                if provider == "eastmoney"
                else ("symbol", "fundcode", "code", "FCODE")
            ),
        )
        east = provider == "eastmoney"
        aliases = {
            "nav": ("DWJZ", "unitNav", "nav", "dwjz") if east else ("dwjz", "nav", "unit_nav"),
            "dayReturn": ("RZDF", "dayReturn", "zdf", "syl_1d") if east else ("zdf", "rzdf", "day_return"),
            "weekReturn": ("SYL_Z", "weekReturn", "syl_z") if east else ("z", "week_return", "syl_z"),
            "monthReturn": ("SYL_Y", "monthReturn", "syl_y", "syl_1m")
            if east
            else ("y", "month_return", "syl_y"),
            "threeMonthReturn": ("SYL_3Y", "threeMonthReturn", "syl_3y", "syl_3m")
            if east
            else ("3y", "three_month_return", "syl_3y"),
            "sixMonthReturn": ("SYL_6Y", "sixMonthReturn", "syl_6y", "syl_6m")
            if east
            else ("6y", "six_month_return", "syl_6y"),
            "oneYearReturn": ("SYL_1N", "oneYearReturn", "syl_1n", "syl_1y")
            if east
            else ("1n", "one_year_return", "syl_1n"),
            "threeYearReturn": ("SYL_3N", "threeYearReturn", "syl_3n", "syl_3y_return")
            if east
            else ("3n", "three_year_return", "syl_3n"),
            "yearToDateReturn": ("SYL_JN", "yearToDateReturn", "syl_jn", "ytd")
            if east
            else ("jn", "year_to_date_return", "syl_jn"),
            "sinceInceptionReturn": ("SYL_LN", "sinceInceptionReturn", "syl_ln")
            if east
            else ("ln", "since_inception_return", "syl_ln"),
        }
        for row in rows:
            code = _fund_code(
                _string(
                    row,
                    *(
                        ("FCODE", "fundCode", "code", "symbol")
                        if east
                        else ("symbol", "fundcode", "code", "FCODE")
                    ),
                )
            )
            name = _string(
                row,
                *(
                    ("SHORTNAME", "fundName", "name", "sname")
                    if east
                    else ("sname", "name", "fundname", "SHORTNAME")
                ),
            )
            if not code or not name:
                continue
            category = _fund_category(
                _string(
                    row,
                    *(
                        ("FTYPE", "fundType", "category", "type")
                        if east
                        else ("type", "fundtype", "category")
                    ),
                )
            )
            item: dict[str, Any] = {key: _numeric(_value(row, *names)) for key, names in aliases.items()}
            item.update(
                code=code,
                name=name,
                category=category if category != "all" else _fund_category(name, infer=True),
                rank=0,
                scale=_money(
                    _value(
                        row,
                        *(
                            ("JJGM", "fundSize", "scale", "ENDNAV")
                            if east
                            else ("jjgm", "fund_size", "scale")
                        ),
                    )
                ),
            )
            for field, names in (
                ("navDate", ("FSRQ", "navDate", "jzrq") if east else ("jzrq", "nav_date")),
                ("scaleDate", ("GMRQ", "scaleDate", "fundSizeDate") if east else ("gmdate", "scale_date")),
            ):
                value = _date(_string(row, *names))
                if value:
                    item[field] = value
            result.append(item)
    if rows and not result:
        raise MarketDataError(provider + " fund ranking rows contained no usable identities")
    normalized = {}
    for item in result:
        normalized[item["code"]] = _merge(normalized.get(item["code"], {}), item)
    return list(normalized.values())


def _parse_identities(raw, market):
    keys = (
        ("f12", "code")
        if market == "eastmoney"
        else ("SEC_CODE", "ZQDM", "zqdm", "code", "fundCode", "FUND_CODE", "sys_key")
    )
    rows = _rows(_json(raw), *keys)
    items = []
    for row in rows:
        code = _plain(_string(row, *keys))
        match = re.search(r"\d{6}", code)
        code = match[0] if match else code
        current_market = (
            ("SH" if _string(row, "f13", "market") == "1" else "SZ") if market == "eastmoney" else market
        )
        canonical = _etf_code(code, current_market)
        if not canonical:
            continue
        name = (
            _string(row, "f14", "name")
            if market == "eastmoney"
            else _plain(
                _string(
                    row,
                    "SEC_NAME",
                    "SEC_ABBR",
                    "ZQJC",
                    "zqjc",
                    "name",
                    "fundName",
                    "FUND_ABBR",
                    "fundAbbr",
                    "secNameFull",
                    "jjjcurl",
                )
            )
        )
        if not name:
            continue
        fund_class = _plain(_string(row, "jjlb", "JJLB", "subClass"))
        if current_market == "SZ" and fund_class and "ETF" not in fund_class.upper():
            continue
        status = _string(row, "STATUS", "status", "listingStatus", "SSZT").lower()
        listed = not any(word in status for word in ("退", "终止")) and status != "delisted"
        tracking = _string(row, "INDEX_NAME", "indexName", "trackingIndex", "BZSM")
        category = _etf_category(name, tracking)
        text = (fund_class + " " + _plain(_string(row, "tzlb", "TZLB")) + " " + name).lower()
        for category_value, words in (
            ("cross_border", ("跨境", "qdii", "香港", "海外")),
            ("money", ("货币", "现金")),
            ("commodity", ("商品", "黄金", "白银", "原油")),
            ("bond", ("债",)),
        ):
            if any(word in text for word in words):
                category = category_value
                break
        items.append(
            dict(
                code=canonical,
                name=name,
                market=current_market,
                category=category,
                trackingIndex=tracking,
                managementFee=_numeric(_value(row, "MANAGEMENT_FEE", "managementFee", "GLFL")),
                listDate=_date(
                    _string(row, "LIST_DATE", "LISTING_DATE", "SSRQ", "ssrq", "listDate", "listingDate")
                ),
                listed=listed,
            )
        )
    if rows and not items:
        raise MarketDataError("exchange response contained no supported ETF codes")
    return items


def _parse_quotes(raw, provider):
    result = {}
    if provider == "eastmoney":
        rows = _rows(_json(raw), "f12", "code")
        for row in rows:
            code = _etf_code(
                _string(row, "f12", "code"), "SH" if _string(row, "f13", "market") == "1" else "SZ"
            )
            if not code:
                continue
            item: dict[str, Any] = dict.fromkeys(QUOTE_NUMBERS)
            for key, aliases in {
                "price": ("f2", "price"),
                "changeRate": ("f3", "changeRate"),
                "amount": ("f6", "amount"),
                "turnoverRate": ("f8", "turnoverRate"),
                "netInflow": ("f62", "netInflow"),
            }.items():
                item[key] = _numeric(_value(row, *aliases))
            at = _numeric(_value(row, "f124", "timestamp"))
            item.update(
                code=code, quoteTime=datetime.fromtimestamp(at, CN).isoformat() if at and at > 0 else ""
            )
            result[code] = item
        if rows and not result:
            raise MarketDataError("eastmoney quote rows contained no usable ETF data")
        return result
    for line in raw.split(";"):
        if "=" not in line:
            continue
        variable, payload = line.strip().split("=", 1)
        code = _etf_code(re.sub(r"^(?:v_r_|v_|var hq_str_)", "", variable.strip()))
        if not code:
            continue
        parts = payload.strip().strip('"').split("~" if provider == "tencent" else ",")
        if len(parts) < (6 if provider == "tencent" else 10):
            continue
        item: dict[str, Any] = dict.fromkeys(QUOTE_NUMBERS)
        item.update(code=code, price=_numeric(parts[3]), quoteTime="")
        if provider == "tencent":
            for index, key, multiplier in (
                (32, "changeRate", 1),
                (38, "turnoverRate", 1),
                (44, "scale", 1e8),
                (72, "shares", 1),
            ):
                value = _numeric(parts[index]) if len(parts) > index else None
                item[key] = value * multiplier if value is not None else None
            composite = parts[35].split("/") if len(parts) > 35 else []
            item["amount"] = _numeric(composite[2]) if len(composite) >= 3 else None
            if item["amount"] is None and len(parts) > 37:
                value = _numeric(parts[37])
                item["amount"] = value * 1e4 if value is not None else None
            item["quoteTime"] = next(
                (_datetime(parts[i]) for i in (30, 29) if len(parts) > i and _datetime(parts[i])), ""
            )
        else:
            item["amount"] = _numeric(parts[9])
            previous = _numeric(parts[2]) or 0
            item["changeRate"] = (
                ((_numeric(parts[3]) or 0) - previous) / previous * 100 if previous > 0 else None
            )
            if len(parts) > 31:
                item["quoteTime"] = _datetime(parts[30] + " " + parts[31])
        result[code] = item
    if raw.strip() and not result:
        raise MarketDataError(provider + " quote response contained no usable ETF rows")
    return result


def _fundamental(code):
    return dict(
        code=code,
        **dict.fromkeys(FUNDAMENTAL_NUMBERS),
        navDate="",
        scaleDate="",
        trackingIndex="",
        holdings=[],
    )


def _parse_nav_html(raw, identities):
    soup = BeautifulSoup(raw, "html.parser")
    dates = [
        match[0] for node in soup.select("th,caption") if (match := DATE_PATTERN.search(node.get_text()))
    ]
    fallback = DATE_PATTERN.search(raw)
    day = _date(dates[0] if dates else fallback[0] if fallback else "")
    allowed = {i["code"] for i in identities}
    result = {}
    recognized = 0
    for row in soup.select("tr"):
        cells = [_plain(c.get_text()) for c in row.select("td")]
        if len(cells) < 10:
            continue
        position = next(((i, m[0]) for i, c in enumerate(cells) if (m := re.search(r"\d{6}", c))), None)
        if position is None or position[0] + 9 >= len(cells):
            continue
        index, digits = position
        code = _etf_code(digits, unknown=True)
        if not code:
            continue
        recognized += 1
        if code not in allowed:
            continue
        item = _fundamental(code)
        item.update(nav=_numeric(cells[index + 2]), navDate=day, premiumRate=_numeric(cells[index + 9]))
        result[code] = item
    if not recognized:
        raise MarketDataError("eastmoney ETF NAV page contained no recognizable ETF rows")
    return result


def _parse_sina_fundamentals(raw, identities):
    allowed = {i["code"] for i in identities}
    rows = _rows(_json(raw), "symbol", "fundcode", "code")
    result = {}
    for row in rows:
        code = _etf_code(_string(row, "symbol", "fundcode", "code"), unknown=True)
        if code not in allowed:
            continue
        item = _fundamental(code)
        for key, aliases in {
            "nav": ("dwjz", "nav"),
            "premiumRate": ("premium_rate", "premiumRate"),
            "shares": ("shares", "total_shares"),
            "scale": ("jjgm", "scale", "fund_size"),
        }.items():
            item[key] = (_money if key in ("shares", "scale") else _numeric)(_value(row, *aliases))
        item.update(
            navDate=_date(_string(row, "jzrq", "navDate")),
            scaleDate=_date(_string(row, "gmdate", "scaleDate")),
        )
        result[code] = item
    if rows and not result and identities:
        raise MarketDataError("sina fundamentals did not match requested ETF identities")
    return result


def _parse_basic(raw, code):
    item = _fundamental(code)
    for row in BeautifulSoup(raw, "html.parser").select("tr"):
        for heading, value in zip(row.select("th"), row.select("td"), strict=False):
            key, text = _plain(heading.get_text()), _plain(value.get_text())
            if "管理费率" in key:
                match = re.search(r"[-+]?\d+(?:\.\d+)?", text)
                if match:
                    item["managementFee"] = _numeric(match[0])
            elif "跟踪标的" in key:
                item["trackingIndex"] = text
    if item["managementFee"] is None and not item["trackingIndex"]:
        raise MarketDataError("eastmoney ETF basic page contained neither management fee nor tracking index")
    return item


def _parse_holdings(raw):
    result = {}
    for row in _rows(_json(raw), "GPDM", "stockCode", "code", "ZQDM"):
        code, name = (
            _string(row, "GPDM", "stockCode", "code", "ZQDM"),
            _string(row, "GPJC", "stockName", "name", "ZQJC"),
        )
        if not code or not name or code in result:
            continue
        item = dict(code=code, name=name, weight=_numeric(_value(row, "JZBL", "weight", "ratio")))
        day = _date(_string(row, "FSRQ", "REPORTDATE", "asOf"))
        if day:
            item["asOf"] = day
        result[code] = item
    return _sort(list(result.values()), "weight", "desc")


def _sort(items, key, direction):
    return sorted(
        items,
        key=lambda item: (
            item.get(key) is None,
            (item.get(key) or 0) * (1 if direction == "asc" else -1),
            item["code"],
        ),
    )


def _query(query, etf=False) -> dict[str, Any]:
    result: dict[str, Any] = dict(
        category=str(query.get("category") or "all").lower().strip(),
        q=str(query.get("q") or "").strip(),
        sortDirection=str(query.get("sortDirection") or "desc").lower().strip(),
        page=int(query.get("page") or 1),
        pageSize=int(query.get("pageSize") or 20),
    )
    if not result["page"]:
        result["page"] = 1
    if not result["pageSize"]:
        result["pageSize"] = 20
    result["sort" if etf else "period"] = str(
        query.get("sort" if etf else "period") or ("amount" if etf else "1y")
    ).strip()
    if not etf:
        result["period"] = result["period"].lower()
    if result["category"] not in (ETF_CATEGORIES if etf else FUND_CATEGORIES):
        raise ValueError("invalid fund/ETF category")
    if result["sortDirection"] not in ("asc", "desc"):
        raise ValueError("invalid sort direction")
    if result["page"] < 1 or not 1 <= result["pageSize"] <= 100:
        raise ValueError("page must be at least 1 and pageSize between 1 and 100")
    if (
        etf
        and result["sort"]
        not in ("changeRate", "amount", "turnoverRate", "premiumRate", "scale", "netInflow")
    ) or (not etf and result["period"] not in FUND_PERIODS):
        raise ValueError("invalid fund period or ETF sort")
    return result


class _ProviderFailure(MarketDataError):
    def __init__(self, message, code="provider_unavailable"):
        super().__init__(message)
        self.code = code


def _asof(values, fields):
    times = [
        _datetime(item.get(field, ""))
        for item in (values.values() if isinstance(values, dict) else values)
        for field in fields
    ]
    return max(times, default="") or ZERO_TIME


def _provider(name, data: Any, refs=(), errors=(), fields=()) -> dict[str, Any]:
    status = "partial" if errors and data else "unavailable" if not data else "ok"
    messages = "; ".join(str(error) for error in errors)
    as_of = _asof(data, fields)
    source = dict(provider=name, status=status, asOf=as_of)
    if refs:
        source["sourceRef"] = ",".join(sorted(set(refs)))
    if messages:
        source["message"] = messages
    elif not data:
        source["message"] = "数据源返回空数据"
    problems = [
        dict(provider=name, code=getattr(error, "code", "provider_unavailable"), message=str(error))
        for error in errors
    ]
    if not data and not problems:
        problems = [dict(provider=name, code="empty_data", message="数据源返回空数据")]
    return dict(data=data, name=name, asOf=as_of, status=status, sources=[source], errors=problems)


def _response(data, results, *, status=None) -> dict[str, Any]:
    errors = [error for result in results for error in result["errors"]]
    sources = [source for result in results for source in result["sources"]]
    names = list(dict.fromkeys(result["name"] for result in results if result["data"]))
    chosen = status or ("partial" if errors or any(r["status"] != "ok" for r in results) else "ok")
    result = envelope(
        data,
        "+".join(names),
        status=chosen,
        as_of=max((r["asOf"] for r in results), default=ZERO_TIME),
        errors=errors,
    )
    result.pop("evidenceProfile", None)
    result["sources"] = sources
    result["warnings"] = []
    return result


class Funds:
    config: AppConfig
    settings: dict
    http: Transport

    def _fund_read(self, url, params=None, *, referer="https://fund.eastmoney.com/", deadline):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise _ProviderFailure("fund provider deadline exceeded", "timeout")
        chunks = []
        length = 0
        try:
            with self.http.client.stream(
                "GET",
                url,
                params=params,
                headers={"User-Agent": USER_AGENT, "Referer": referer},
                timeout=min(15, remaining),
            ) as response:
                if not 200 <= response.status_code < 300:
                    raise _ProviderFailure(
                        f"provider HTTP {response.status_code}",
                        "rate_limited" if response.status_code == 429 else "provider_unavailable",
                    )
                for chunk in response.iter_bytes():
                    if time.monotonic() > deadline:
                        raise _ProviderFailure("fund provider deadline exceeded", "timeout")
                    length += len(chunk)
                    if length > 32 << 20:
                        raise _ProviderFailure("provider response exceeds 33554432 bytes")
                    chunks.append(chunk)
        except httpx.TimeoutException as exc:
            raise _ProviderFailure("fund provider timed out", "timeout") from exc
        except httpx.HTTPError as exc:
            raise _ProviderFailure("fund provider request failed (" + type(exc).__name__ + ")") from exc
        body = b"".join(chunks)
        try:
            return body.decode("utf-8")
        except UnicodeDecodeError:
            try:
                return body.decode("gbk")
            except UnicodeDecodeError as exc:
                raise _ProviderFailure("provider response has invalid text encoding") from exc

    def _fund_fetch(
        self, url, params, parser, *, deadline, referer="https://fund.eastmoney.com/"
    ) -> tuple[Any, str, Exception | None]:
        ref = url + ("?" + urlencode(sorted(params.items())) if params else "")
        try:
            data = parser(self._fund_read(url, params, referer=referer, deadline=deadline))
            return data, ref, None
        except (MarketDataError, ValueError, KeyError, TypeError, IndexError) as error:
            return None, ref, error

    def fund_search(self, key):
        pattern = "%" + str(key).strip() + "%"
        values = database_rows(
            self.config.main_db,
            "SELECT * FROM fund_basic WHERE deleted_at IS NULL AND (code LIKE ? OR name LIKE ?) LIMIT 10",
            (pattern, pattern),
        )
        stale = any(
            (normalized := _datetime(row.get("updated_at")))
            and (now() - datetime.fromisoformat(normalized)).total_seconds() > 86400
            for row in values
        )
        if not values or stale:
            try:
                catalogue = self.http.cached(
                    "fund-basic-catalogue", 86400, lambda: self._fund_catalogue(time.monotonic() + 12)
                )
            except MarketDataError:
                if not values:
                    raise
            else:
                existing = {row["code"]: row for row in values}
                matcher = re.compile(re.escape(pattern).replace("%", ".*").replace("_", "."), re.I | re.S)
                values = [
                    {**existing.get(item["code"], {}), **item}
                    for item in catalogue
                    if matcher.fullmatch(item["code"]) or matcher.fullmatch(item["name"])
                ][:10]
        strings = {
            "code": "code",
            "name": "name",
            "fullName": "full_name",
            "type": "type",
            "establishment": "establishment",
            "scale": "scale",
            "company": "company",
            "manager": "manager",
            "rating": "rating",
            "trackingTarget": "tracking_target",
            "netUnitValueDate": "net_unit_value_date",
            "netEstimatedUnitTime": "net_estimated_time",
        }
        numbers = {
            "netUnitValue": "net_unit_value",
            "netEstimatedUnit": "net_estimated_unit",
            "netAccumulated": "net_accumulated",
            **{
                f"netGrowth{suffix}": "net_growth_" + str(suffix).lower()
                for suffix in (1, 3, 6, 12, 36, 60, "YTD", "All")
            },
        }
        result = []
        for row in values:
            item = {field: row.get(column) or "" for field, column in strings.items()}
            item.update({field: row.get(column) for field, column in numbers.items()})
            item.update(
                ID=row.get("id", 0),
                CreatedAt=_datetime(row.get("created_at")) or ZERO_TIME,
                UpdatedAt=_datetime(row.get("updated_at")) or ZERO_TIME,
                DeletedAt=None,
            )
            result.append(item)
        return result

    def _fund_catalogue(self, deadline):
        raw = self._fund_read("https://fund.eastmoney.com/allfund.html", deadline=deadline)
        items = {}
        for anchor in BeautifulSoup(raw, "html.parser").select("ul.num_right li a"):
            text = anchor.get_text().strip()
            match = re.search(r"\b\d{6}\b", text + " " + str(anchor.get("href", "")))
            if not match:
                continue
            code = match[0]
            name = text.replace("（" + code + "）", "").replace("(" + code + ")", "").strip()
            if not name:
                continue
            items.setdefault(code, {"code": code, "name": name})
            if len(items) > 100000:
                raise MarketDataError("fund catalogue exceeds the supported bounded size")
        if not items:
            raise MarketDataError("fund catalogue source returned no usable identities")
        return list(items.values())

    def _fund_rankings_provider(self, query, name, deadline):
        if name == "sina_fund":
            params = dict(
                page="1",
                num="10000",
                sort=SINA_SORTS[query["period"]],
                asc=str(query["sortDirection"] == "asc").lower(),
            )
            data, ref, error = self._fund_fetch(
                SINA_FUND_URL,
                params,
                lambda raw: _parse_funds(raw, name),
                deadline=deadline,
                referer="https://finance.sina.com.cn/fund/",
            )
            return _provider(name, data or [], [ref], [error] if error else [], ("navDate",))
        today = now()
        try:
            start = today.replace(year=today.year - 3).date().isoformat()
        except ValueError:
            start = today.replace(year=today.year - 3, day=28).date().isoformat()
        params = dict(
            op="ph",
            dt="kf",
            ft={"stock": "gp", "mixed": "hh", "bond": "zq", "index": "zs"}.get(
                query["category"], query["category"]
            ),
            rs="",
            gs="0",
            sc=EAST_SORTS[query["period"]],
            st=query["sortDirection"],
            sd=start,
            ed=today.date().isoformat(),
            qdii="",
            tabSubtype=",,,,,",
            pi="1",
            pn="10000",
            dx="1",
            v=str(int(today.timestamp() * 1000)),
        )

        def parse_page(raw):
            pages = re.search(r"""(?i)["']?allPages["']?\s*:\s*(\d+)""", raw)
            records = re.search(r"""(?i)["']?allRecords["']?\s*:\s*(\d+)""", raw)
            count = int(pages[1]) if pages else math.ceil(int(records[1]) / 10000) if records else 1
            return _parse_funds(raw, name), max(1, min(count, 100))

        first, ref, error = self._fund_fetch(
            RANK_URL,
            params,
            parse_page,
            deadline=deadline,
            referer="https://fund.eastmoney.com/data/fundranking.html",
        )
        if error:
            return _provider(name, [], [ref], [error], ("navDate",))
        items, count = first
        refs = [ref]
        errors = []

        def page(number):
            return self._fund_fetch(
                RANK_URL,
                {**params, "pi": str(number)},
                lambda raw: _parse_funds(raw, name),
                deadline=deadline,
                referer="https://fund.eastmoney.com/data/fundranking.html",
            )

        if count > 1:
            with ThreadPoolExecutor(max_workers=min(4, count - 1)) as pool:
                for page_items, page_ref, page_error in pool.map(page, range(2, count + 1)):
                    refs.append(page_ref)
                    items.extend(page_items or [])
                    if page_error:
                        errors.append(page_error)
        if query["category"] != "all":
            for item in items:
                item["category"] = query["category"]
        return _provider(name, items, refs, errors, ("navDate",))

    def fund_rankings(self, query):
        query = _query(query)
        deadline = time.monotonic() + 12
        primary = self._fund_rankings_provider(query, "eastmoney", deadline)
        results = [primary]
        if not primary["data"] or primary["status"] != "ok":
            results.append(self._fund_rankings_provider(query, "sina_fund", deadline))
        merged = {}
        for result in results:
            for item in result["data"]:
                merged[item["code"]] = _merge(merged.get(item["code"], {}), item)
        items = [
            item
            for item in merged.values()
            if (query["category"] == "all" or item["category"] == query["category"])
            and (not query["q"] or query["q"].lower() in (item["code"] + " " + item["name"]).lower())
        ]
        items = _sort(items, FUND_PERIODS[query["period"]], query["sortDirection"])
        for index, item in enumerate(items, 1):
            item["rank"] = index
        start = (query["page"] - 1) * query["pageSize"]
        data = dict(
            items=items[start : start + query["pageSize"]],
            total=len(items),
            page=query["page"],
            pageSize=query["pageSize"],
            category=query["category"],
            period=query["period"],
        )
        day = max((i.get("navDate", "") for i in items), default="")
        if day:
            data["navDate"] = day
        return _response(
            data,
            results,
            status="unavailable"
            if not merged
            else "partial"
            if len(results) > 1 or any(r["status"] != "ok" for r in results)
            else "ok",
        )

    def _etf_identities(self, deadline):
        sse_params = {
            "isPagination": "true",
            "sqlId": "FUND_LIST",
            "fundType": "00",
            "subClass": "01,02,03,04,06,08,09,31,32,33,34,35,36,37,38",
            "pageHelp.pageSize": "2000",
            "pageHelp.pageNo": "1",
            "pageHelp.beginPage": "1",
            "pageHelp.endPage": "1",
        }
        szse_params = dict(SHOWTYPE="JSON", CATALOGID="1105", TABKEY="tab1", selectJjlb="ETF", PAGENO="1")

        def sse():
            return self._fund_fetch(
                SSE_URL,
                sse_params,
                lambda raw: _parse_identities(raw, "SH"),
                deadline=deadline,
                referer="https://www.sse.com.cn/assortment/fund/etf/list/",
            )

        def szse():
            def parse(raw):
                def page_count(value):
                    if isinstance(value, dict):
                        if _value(value, "pagecount") is not None:
                            return int(_numeric(_value(value, "pagecount")) or 1)
                        return next((n for nested in value.values() if (n := page_count(nested)) > 0), 0)
                    if isinstance(value, list):
                        return next((n for nested in value if (n := page_count(nested)) > 0), 0)
                    return 0

                return _parse_identities(raw, "SZ"), max(1, min(page_count(_json(raw)), 200))

            first, ref, error = self._fund_fetch(
                SZSE_URL,
                szse_params,
                parse,
                deadline=deadline,
                referer="https://www.szse.cn/market/product/list/etfList/index.html",
            )
            if error:
                return [], [ref], [error]
            items, count = first
            refs = [ref]
            errors = []

            def page(number):
                return self._fund_fetch(
                    SZSE_URL,
                    {**szse_params, "PAGENO": str(number)},
                    lambda raw: _parse_identities(raw, "SZ"),
                    deadline=deadline,
                    referer="https://www.szse.cn/market/product/list/etfList/index.html",
                )

            if count > 1:
                with ThreadPoolExecutor(max_workers=min(4, count - 1)) as pool:
                    for values, reference, problem in pool.map(page, range(2, count + 1)):
                        items.extend(values or [])
                        refs.append(reference)
                        if problem:
                            errors.append(problem)
            return items, refs, errors

        with ThreadPoolExecutor(max_workers=2) as pool:
            sse_future = pool.submit(sse)
            szse_future = pool.submit(szse)
            sse_items, sse_ref, sse_error = sse_future.result()
            items, refs, errors = szse_future.result()
        items = (sse_items or []) + items
        refs.append(sse_ref)
        if sse_error:
            errors.append(sse_error)
        if not items:
            params = dict(
                pn="2000",
                pz="2000",
                po="1",
                np="1",
                fltt="2",
                fid="f12",
                fs="b:MK0021,b:MK0022,b:MK0023,b:MK0024",
                fields="f12,f13,f14",
            )
            fallback, ref, error = self._fund_fetch(
                EAST_IDENTITY_URL,
                params,
                lambda raw: _parse_identities(raw, "eastmoney"),
                deadline=deadline,
                referer="https://quote.eastmoney.com/center/gridlist.html",
            )
            items = fallback or []
            refs.append(ref)
            if error:
                errors.append(error)
            elif items and not errors:
                errors.append(
                    _ProviderFailure(
                        "exchange lists were empty; Eastmoney identity fallback used", "identity_fallback"
                    )
                )
        normalized = {}
        for item in items:
            if item["code"] not in normalized and item["listed"]:
                normalized[item["code"]] = item
        return _provider(
            "sse+szse", sorted(normalized.values(), key=lambda i: i["code"]), refs, errors, ("listDate",)
        )

    def _etf_quote_provider(self, identities, name, deadline):
        codes = [i["code"] for i in identities]

        def batch(values):
            if name == "eastmoney":
                params = dict(
                    secids=",".join(("1." if c.startswith("sh") else "0.") + c[2:] for c in values),
                    fltt="2",
                    invt="2",
                    fields="f12,f13,f14,f2,f3,f6,f8,f62,f124",
                )
                return self._fund_fetch(
                    EAST_QUOTES_URL,
                    params,
                    lambda raw: _parse_quotes(raw, name),
                    deadline=deadline,
                    referer="https://quote.eastmoney.com/",
                )
            url = (
                "https://qt.gtimg.cn/q=" if name == "tencent" else "https://hq.sinajs.cn/list="
            ) + ",".join(values)
            return self._fund_fetch(
                url,
                None,
                lambda raw: _parse_quotes(raw, name),
                deadline=deadline,
                referer="https://gu.qq.com/" if name == "tencent" else "https://finance.sina.com.cn/",
            )

        batches = [codes[start : start + 80] for start in range(0, len(codes), 80)]
        result = {}
        refs = []
        errors = []
        with ThreadPoolExecutor(max_workers=min(6, len(batches)) or 1) as pool:
            for values, reference, error in pool.map(batch, batches):
                for code, item in (values or {}).items():
                    result[code] = _merge(result.get(code, {}), item)
                refs.append(reference)
                if error:
                    errors.append(error)
        return _provider(name, result, refs, errors, ("quoteTime",))

    def _etf_quotes(self, identities, deadline):
        values = {}
        results = []
        for name in ("tencent", "sina", "eastmoney"):
            required = (
                ("price", "changeRate", "amount", "quoteTime")
                if name == "sina"
                else ("price", "changeRate", "amount", "turnoverRate", "netInflow", "quoteTime")
            )
            missing = [
                i
                for i in identities
                if i["code"] not in values
                or any(
                    values[i["code"]].get(field) is None or values[i["code"]].get(field) == ""
                    for field in required
                )
            ]
            if not missing:
                continue
            result = self._etf_quote_provider(missing, name, deadline)
            results.append(result)
            for code, item in result["data"].items():
                values[code] = _merge(values.get(code, {}), item)
        return values, results

    def _etf_fundamental_provider(self, identities, name, deadline):
        if name == "sina_fund":
            values, ref, error = self._fund_fetch(
                SINA_FUND_URL,
                dict(page="1", num="10000", sort="zmjzz", asc="0"),
                lambda raw: _parse_sina_fundamentals(raw, identities),
                deadline=deadline,
                referer="https://finance.sina.com.cn/fund/",
            )
            return _provider(name, values or {}, [ref], [error] if error else [], ("navDate", "scaleDate"))
        values, ref, error = self._fund_fetch(
            NAV_URL, None, lambda raw: _parse_nav_html(raw, identities), deadline=deadline
        )
        values = values or {}
        refs = [ref]
        errors = [error] if error else []
        if len(identities) == 1:
            code = identities[0]["code"]
            basic, ref, error = self._fund_fetch(
                BASIC_URL.format(code[2:]), None, lambda raw: _parse_basic(raw, code), deadline=deadline
            )
            refs.append(ref)
            if basic:
                values[code] = _merge(values.get(code, {}), basic)
            if error:
                errors.append(error)
            holdings, ref, error = self._fund_fetch(
                HOLDING_URL,
                dict(FCODE=code[2:], pageIndex="1", pageSize="10"),
                _parse_holdings,
                deadline=deadline,
            )
            refs.append(ref)
            if holdings is not None:
                values.setdefault(code, _fundamental(code))["holdings"] = holdings
            if error:
                errors.append(error)
        return _provider(name, values, refs, errors, ("navDate", "scaleDate"))

    def _etf_fundamentals(self, identities, deadline):
        values = {}
        results = []
        for name in ("eastmoney_fund", "sina_fund"):
            missing = [
                i
                for i in identities
                if i["code"] not in values
                or values[i["code"]].get("nav") is None
                or not values[i["code"]].get("navDate")
            ]
            if not missing:
                break
            result = self._etf_fundamental_provider(missing, name, deadline)
            results.append(result)
            for code, item in result["data"].items():
                values[code] = _merge(values.get(code, {}), item)
        return values, results

    def _etf_data(self, identities, deadline):
        with ThreadPoolExecutor(max_workers=2) as pool:
            quotes = pool.submit(self._etf_quotes, identities, deadline)
            fundamentals = pool.submit(self._etf_fundamentals, identities, deadline)
            quote_values, quote_results = quotes.result()
            fund_values, fund_results = fundamentals.result()
        items = []
        for identity in identities:
            code = identity["code"]
            quote = quote_values.get(code, {})
            fund = fund_values.get(code, {})
            item = {key: identity[key] for key in ("code", "name", "market", "category")}
            item.update(
                {
                    key: quote.get(key)
                    for key in ("price", "changeRate", "amount", "turnoverRate", "netInflow")
                }
            )
            item["nav"] = fund.get("nav")
            premium = fund.get("premiumRate")
            if premium is None and quote.get("price") is not None and fund.get("nav") not in (None, 0):
                premium = (quote["price"] / fund["nav"] - 1) * 100
            item["premiumRate"] = premium
            for field in ("shares", "scale"):
                item[field] = fund.get(field) if fund.get(field) is not None else quote.get(field)
            for field, source in (("navDate", fund), ("quoteTime", quote)):
                if source.get(field):
                    item[field] = source[field]
            item["rank"] = 0
            items.append(item)
        return items, quote_results + fund_results, fund_values

    def etf_rankings(self, query):
        query = _query(query, True)
        deadline = time.monotonic() + 12
        identity = self._etf_identities(deadline)
        results = [identity]
        items = []
        if identity["data"]:
            items, other, _ = self._etf_data(identity["data"], deadline)
            results += other
        items = [
            item
            for item in items
            if (query["category"] == "all" or item["category"] == query["category"])
            and (not query["q"] or query["q"].lower() in (item["code"] + " " + item["name"]).lower())
        ]
        items = _sort(items, query["sort"], query["sortDirection"])
        for index, item in enumerate(items, 1):
            item["rank"] = index
        start = (query["page"] - 1) * query["pageSize"]
        page_items = items[start : start + query["pageSize"]]
        page = dict(
            items=page_items,
            total=len(items),
            page=query["page"],
            pageSize=query["pageSize"],
            category=query["category"],
            sort=query["sort"],
        )
        status = (
            "unavailable"
            if not identity["data"]
            else "partial"
            if _incomplete(page_items) or any(r["status"] != "ok" for r in results)
            else "ok"
        )
        return _response(page, results, status=status)

    def etf_search(self, q, limit=20):
        if not str(q).strip() or not 1 <= int(limit or 20) <= 50:
            raise ValueError("q is required and limit must be between 1 and 50")
        result = self.etf_rankings(dict(q=q, page=1, pageSize=int(limit or 20)))
        result["data"] = result["data"]["items"]
        return result

    def etf_detail(self, code):
        canonical = _etf_code(code)
        if not canonical:
            raise ValueError("invalid ETF code")
        deadline = time.monotonic() + 12
        identity_result = self._etf_identities(deadline)
        identity = next((item for item in identity_result["data"] if item["code"] == canonical), None)
        if not identity:
            if identity_result["status"] != "ok":
                result = _response(_empty_detail(), [identity_result], status="unavailable")
                result["errors"].append(
                    dict(
                        provider="etfs",
                        code="provider_unavailable",
                        message=f"ETF identity source is unavailable; listing status for {canonical} cannot be determined",
                    )
                )
                return result
            raise LookupError(f"ETF {canonical} is not listed by the exchange identity source")
        items, results, fundamentals = self._etf_data([identity], deadline)
        detail = items[0]
        fund = fundamentals.get(canonical, {})
        detail.update(
            trackingIndex=identity["trackingIndex"] or fund.get("trackingIndex", ""),
            managementFee=identity["managementFee"]
            if identity["managementFee"] is not None
            else fund.get("managementFee"),
            holdings=fund.get("holdings", []),
            chartInstrument=instrument(canonical, "etf", identity["market"]),
        )
        if identity["listDate"]:
            detail["listDate"] = identity["listDate"]
        result = _response(
            detail,
            [identity_result] + results,
            status="partial"
            if _incomplete(items) or any(r["status"] != "ok" for r in [identity_result] + results)
            else "ok",
        )
        result["sources"].append(
            dict(
                provider="unified_chart",
                status="ok",
                asOf=ZERO_TIME,
                sourceRef=f"/api/v1/instruments/{canonical}/chart?assetType=etf&market={identity['market']}&adjustment=none",
            )
        )
        return result


def _incomplete(items):
    return any(
        any(item.get(field) is None for field in ETF_NUMBERS)
        or not item.get("navDate")
        or not item.get("quoteTime")
        for item in items
    )


def _empty_detail():
    return dict(
        code="",
        name="",
        market="",
        category="",
        **dict.fromkeys(ETF_NUMBERS),
        rank=0,
        trackingIndex="",
        managementFee=None,
        holdings=[],
        chartInstrument=dict(assetType="", market="", code=""),
    )
