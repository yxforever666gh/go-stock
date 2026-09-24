"""Existing market news, rankings and reference provider contracts."""

import hashlib
import json
import re
import sqlite3
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import timedelta

from bs4 import BeautifulSoup

from .common import MarketDataError, ProviderState, Transport, database_rows, now, timestamp

SINA = "https://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow."


def array(payload, *path):
    value = payload
    for key in path:
        if not isinstance(value, dict) or key not in value:
            raise MarketDataError("provider response is missing " + ".".join(path))
        value = value[key]
    if not isinstance(value, list):
        raise MarketDataError("provider response must contain an array")
    return value


class News(ProviderState):
    def _news_fetch(self, url, params=None, *, json_response=False):
        if not self.http.owns_client:
            return self.http.json(url, params) if json_response else self.http.text(url, params)
        attempts = [self.settings | {"httpProxyEnabled": False, "forceNoProxyForFetch": True}]
        if self.settings.get("httpProxyEnabled") and self.settings.get("httpProxy"):
            attempts.append(self.settings | {"forceNoProxyForFetch": False})

        def fetch(settings):
            transport = Transport(settings)
            try:
                return transport.json(url, params) if json_response else transport.text(url, params)
            finally:
                transport.close()

        if len(attempts) == 1:
            return fetch(attempts[0])
        pool = ThreadPoolExecutor(max_workers=2)
        pending = [pool.submit(fetch, settings) for settings in attempts]
        errors = []
        try:
            for future in as_completed(pending):
                try:
                    return future.result()
                except MarketDataError as exc:
                    errors.append(str(exc))
            raise MarketDataError("news direct/proxy paths failed: " + "; ".join(errors))
        finally:
            # Each losing request owns its bounded client and closes it on completion.
            pool.shutdown(wait=False, cancel_futures=True)

    def global_indexes(self):
        result = self.http.json(
            "https://proxy.finance.qq.com/ifzqgtimg/appstock/app/rank/indexRankDetail2",
            headers={"Referer": "https://stockapp.finance.qq.com/mstats"},
        )
        if result.get("code", 0) != 0 or not isinstance(result.get("data"), dict):
            raise MarketDataError("global index source rejected request")
        data = result["data"]
        return {
            key: array(data | {key: data.get(key) or []}, key)
            for key in ("common", "america", "europe", "asia", "other")
        }

    def industry_rank(self, sort="", count=20):
        data = self.http.json(
            "https://proxy.finance.qq.com/ifzqgtimg/appstock/app/mktHs/rank",
            {"l": count, "p": 1, "t": "01/averatio", "ordertype": "", "o": sort},
        )
        if data.get("code", 0) != 0:
            raise MarketDataError("industry source rejected request")
        return array(data, "data")

    def money_rank(self, category="0", sort="netamount", *, stocks=False):
        endpoint = "ssl_bkzj_ssggzj" if stocks else "ssl_bkzj_bk"
        return array(
            self.http.json(
                SINA + endpoint,
                {"page": 1, "num": 20, "sort": sort or "netamount", "asc": 0, "fenlei": category},
            )
        )

    def money_trend(self, code, days=10):
        rows = array(
            self.http.json(
                SINA + "ssl_qsfx_zjlrqs",
                {"page": 1, "num": days, "sort": "opendate", "asc": 0, "daima": code},
            )
        )
        return list(reversed(rows))

    def notices(self, code=""):
        digits = ",".join(
            re.sub(r"^(sh|sz|bj|gb_|us_?)", "", value.lower()).split(".")[0] for value in code.split(",")
        )
        return array(
            self.http.json(
                "https://np-anotice-stock.eastmoney.com/api/security/ann",
                {
                    "page_size": 50,
                    "page_index": 1,
                    "ann_type": "SHA,CYB,SZA,BJA,INV",
                    "client_source": "web",
                    "f_node": 0,
                    "stock_list": digits,
                },
                headers={"Referer": "https://data.eastmoney.com/notices/hsa/5.html"},
            ),
            "data",
            "list",
        )

    def stock_concepts(self, code):
        from .common import instrument

        normalized = instrument(code)["code"]
        return array(
            self.http.json(
                "https://datacenter.eastmoney.com/securities/api/data/v1/get",
                {
                    "reportName": "RPT_F10_CORETHEME_BOARDTYPE",
                    "columns": "ALL",
                    "quoteColumns": "f3~05~NEW_BOARD_CODE~BOARD_YIELD",
                    "filter": f'(SECUCODE="{normalized[2:]}.{normalized[:2].upper()}")(IS_PRECISE="1")',
                    "pageNumber": 1,
                    "pageSize": 100,
                    "sortTypes": 1,
                    "sortColumns": "BOARD_RANK",
                    "source": "HSF10",
                    "client": "PC",
                },
            ),
            "result",
            "data",
        )

    def stock_financials(self, code):
        from .common import instrument

        normalized = instrument(code)["code"]
        return array(
            self.http.json(
                "https://datacenter.eastmoney.com/securities/api/data/v1/get",
                {
                    "reportName": "RPT_F10_FINANCE_DUPONT",
                    "columns": "ALL",
                    "filter": f'(SECUCODE="{normalized[2:]}.{normalized[:2].upper()}")',
                    "pageNumber": 1,
                    "pageSize": 12,
                    "sortTypes": -1,
                    "sortColumns": "REPORT_DATE",
                    "source": "HSF10",
                    "client": "PC",
                },
            ),
            "result",
            "data",
        )

    def macro_evidence(self, indicator):
        if indicator not in {"GDP", "CPI", "PPI", "PMI"}:
            raise ValueError("unsupported macro indicator")
        return self.http.json(
            "https://datacenter-web.eastmoney.com/api/data/v1/get",
            {
                "columns": "ALL",
                "pageNumber": 1,
                "pageSize": 20,
                "sortColumns": "REPORT_DATE",
                "sortTypes": -1,
                "source": "WEB",
                "client": "WEB",
                "reportName": "RPT_ECONOMY_" + indicator,
            },
        )

    def reuters_news(self):
        return self.http.json(
            "https://www.reuters.com/pf/api/v3/content/fetch/recent-stories-by-sections-v1",
            {
                "query": json.dumps(
                    {"section_ids": "/world/", "size": 4, "website": "reuters"}, separators=(",", ":")
                ),
                "d": 334,
                "mxId": "00000000",
                "_website": "reuters",
            },
            headers={"Referer": "https://www.reuters.com/world/china/"},
        )

    def interactive_answers(self, name):
        data = self.http.json(
            "https://irm.cninfo.com.cn/newircs/index/search",
            method="POST",
            form={"pageNo": 1, "pageSize": 30, "searchTypes": "11", "highLight": "true", "keyWord": name},
            headers={
                "Origin": "https://irm.cninfo.com.cn",
                "Referer": "https://irm.cninfo.com.cn/views/interactiveAnswer",
            },
        )
        if not isinstance(data.get("results"), list):
            raise MarketDataError("interactive answer response has no results array")
        return data

    def research_reports(self, code="", *, industry=False):
        end = now()
        values = {
            "beginTime": (end - timedelta(hours=7 * 365)).date().isoformat(),
            "endTime": end.date().isoformat(),
            "pageNo": 1,
            "pageSize": 50,
            "p": 1,
            "pageNum": 1,
            "pageNumber": 1,
            "industryCode": code if industry else "*",
        }
        if industry:
            values.update(industry="*", qType="1")
            data = self.http.json("https://reportapi.eastmoney.com/report/list", values)
        else:
            values["code"] = re.sub(r"^(sh|sz|bj|gb_|us_?)", "", code.lower()).split(".")[0]
            data = self.http.json("https://reportapi.eastmoney.com/report/list2", method="POST", body=values)
        return array(data, "data")

    def dictionary(self, code):
        return self.http.cached(
            "dictionary:" + code,
            86400,
            lambda: array(
                self.http.json("https://reportapi.eastmoney.com/report/bk", {"bkCode": code}), "data"
            ),
        )

    def long_tiger(self, day):
        params = {
            "reportName": "RPT_DAILYBILLBOARD_DETAILS",
            "columns": "ALL",
            "sortColumns": "TRADE_DATE,SECURITY_CODE",
            "sortTypes": "-1,1",
            "pageSize": 500,
            "pageNumber": 1,
            "source": "WEB",
            "client": "WEB",
        }
        if day:
            params["filter"] = f"(TRADE_DATE='{day}')"
        return array(
            self.http.json("https://datacenter-web.eastmoney.com/api/data/v1/get", params), "result", "data"
        )

    def hot_stocks(self, market_type="10"):
        self.http.text("https://xueqiu.com/hq")
        return array(
            self.http.json(
                "https://stock.xueqiu.com/v5/stock/hot_stock/list.json",
                {"page": 1, "size": 100, "_type": market_type, "type": market_type},
                headers={"Referer": "https://xueqiu.com/"},
            ),
            "data",
            "items",
        )

    def hot_events(self, size=10):
        self.http.text("https://xueqiu.com/")
        return array(
            self.http.json(
                "https://xueqiu.com/hot_event/list.json",
                {"count": size},
                headers={"Referer": "https://xueqiu.com/"},
            ),
            "list",
        )

    def hot_topics(self, size=10):
        return array(
            self.http.json(
                "https://gubatopic.eastmoney.com/interface/GetData.aspx",
                method="POST",
                form={
                    "param": f"ps={size}&p=1&type=0",
                    "path": "newtopic/api/Topic/HomePageListRead",
                    "env": "2",
                },
            ),
            "re",
        )

    def investment_calendar(self, year_month=""):
        return array(
            self.http.json(
                "https://app.jiuyangongshe.com/jystock-app/api/v1/timeline/list",
                method="POST",
                body={"date": year_month or now().strftime("%Y-%m"), "grade": "0"},
                headers={"Referer": "https://www.jiuyangongshe.com/", "platform": "3"},
            ),
            "data",
        )

    def cls_calendar(self):
        values = "app=CailianpressWeb&flag=0&os=web&sv=8.4.6&type=0"
        sign = hashlib.md5(hashlib.sha1(values.encode()).hexdigest().encode()).hexdigest()
        return array(
            self.http.json("https://www.cls.cn/api/calendar/web/list?" + values + "&sign=" + sign), "data"
        )

    def query_stocks(self, words, page_size=50):
        fingerprint = self.settings.get("qgqpBId") or self.settings.get("QgqpBId")
        if not fingerprint:
            raise MarketDataError("东方财富选股需要已配置的 qgqp_b_id")
        return self.http.json(
            "https://np-tjxg-g.eastmoney.com/api/smart-tag/stock/v3/pw/search-code",
            method="POST",
            headers={"Origin": "https://xuangu.eastmoney.com", "Referer": "https://xuangu.eastmoney.com/"},
            body={
                "keyWord": words,
                "pageSize": page_size,
                "pageNo": 1,
                "fingerprint": fingerprint,
                "gids": [],
                "matchWord": "",
                "timestamp": str(int(now().timestamp())),
                "shareToGuba": False,
                "requestId": "",
                "needCorrect": True,
                "removedConditionIdList": [],
                "xcId": "",
                "ownSelectAll": False,
                "dxInfo": [],
                "extraCondition": "",
            },
        )

    def telegraphs(self, source="", limit=50):
        query = "SELECT * FROM telegraph_list WHERE deleted_at IS NULL"
        params = ()
        if source:
            query += " AND source=?"
            params = (source,)
        rows = database_rows(
            self.config.main_db, query + " ORDER BY data_time DESC,time DESC LIMIT ?", params + (limit,)
        )
        output = [
            {
                "ID": row["id"],
                "title": row.get("title", ""),
                "content": row.get("content", ""),
                "source": row.get("source", ""),
                "url": row.get("url", ""),
                "time": row.get("time", ""),
                "dataTime": row.get("data_time", ""),
                "subjects": [],
                "stocks": [],
                "tags": [],
                "CreatedAt": row.get("created_at"),
                "isRed": bool(row.get("is_red", False)),
                "sentimentResult": row.get("sentiment_result", ""),
            }
            for row in rows
        ]
        if output:
            tables = {
                row["name"]
                for row in database_rows(
                    self.config.main_db, "SELECT name FROM sqlite_master WHERE type='table'"
                )
            }
            if {"tags", "telegraph_tags"} <= tables:
                links = database_rows(
                    self.config.main_db,
                    "SELECT l.telegraph_id,l.id,l.tag_id,t.name,t.type FROM telegraph_tags l JOIN tags t ON t.id=l.tag_id WHERE l.telegraph_id IN ("
                    + ",".join("?" for _ in output)
                    + ")",
                    tuple(row["ID"] for row in output),
                )
                by_id = {row["ID"]: row for row in output}
                for link in links:
                    item = by_id[link["telegraph_id"]]
                    item["tags"].append(
                        {"ID": link["id"], "tagId": link["tag_id"], "telegraphId": link["telegraph_id"]}
                    )
                    item["subjects" if link["type"] == "subject" else "stocks"].append(link["name"])
        return output

    def _live_news(self, source):
        if source == "财联社电报":
            try:
                payload = self._news_fetch(
                    "https://www.cls.cn/nodeapi/telegraphList",
                    {"app": "CailianpressWeb", "os": "web", "sv": "8.4.6"},
                    json_response=True,
                )
                items = array(payload, "data", "roll_data")
                if not items:
                    raise MarketDataError("empty CLS telegraph API")
                return [
                    {
                        "title": item.get("title", ""),
                        "content": item.get("content", ""),
                        "source": source,
                        "time": timestamp(item["ctime"]).strftime("%H:%M:%S"),
                        "dataTime": timestamp(item["ctime"]).isoformat(),
                        "url": item.get("shareurl") or f"https://www.cls.cn/detail/{item['id']}",
                        "isRed": item.get("level") != "C",
                        "subjects": [
                            tag["subject_name"] for tag in item.get("subjects", []) if tag.get("subject_name")
                        ],
                        "stocks": [],
                    }
                    for item in items
                ]
            except (MarketDataError, ValueError, KeyError):
                html = BeautifulSoup(self._news_fetch("https://www.cls.cn/telegraph"), "html.parser")
                result = []
                for node in html.select(".telegraph-content-box"):
                    spans = node.select("span")
                    if len(spans) != 2 or not spans[-1].get_text(strip=True):
                        continue
                    labels = node.select("a.label-item")
                    result.append(
                        {
                            "title": "",
                            "content": spans[-1].get_text(strip=True),
                            "source": source,
                            "time": spans[0].get_text(strip=True),
                            "dataTime": None,
                            "isRed": "c-de0422" in spans[-1].get_attribute_list("class"),
                            "subjects": [
                                label.get_text(strip=True)
                                for label in labels
                                if "link-label-item" not in label.get_attribute_list("class")
                            ],
                            "stocks": [
                                item.get_text(strip=True)
                                for item in node.select(".telegraph-stock-plate-box a")
                            ],
                            "url": next(
                                (
                                    label.get("href", "")
                                    for label in labels
                                    if "link-label-item" in label.get_attribute_list("class")
                                ),
                                "",
                            ),
                        }
                    )
                if not result:
                    raise MarketDataError("CLS API and HTML telegraph sources unavailable") from None
                return result
        if source == "新浪财经":
            raw = self._news_fetch(
                "https://zhibo.sina.com.cn/api/zhibo/feed",
                {"page": 1, "page_size": 50, "zhibo_id": 152, "tag_id": 0, "dire": "f"},
            )
            payload = (
                json.loads(raw)
                if raw.lstrip().startswith("{")
                else json.loads(raw[raw.find("(") + 1 : raw.rfind(")")])
            )
            return [
                {
                    "title": match.group(1)
                    if (match := re.search("【(.*?)】", item.get("rich_text", "")))
                    else "",
                    "content": item.get("rich_text", ""),
                    "source": source,
                    "time": timestamp(item["create_time"]).strftime("%H:%M:%S"),
                    "dataTime": timestamp(item["create_time"]).isoformat(),
                    "url": item.get("docurl", ""),
                    "subjects": [tag["name"] for tag in item.get("tag", []) if tag.get("name")],
                    "stocks": [],
                }
                for item in array(payload, "result", "data", "feed", "list")
            ]
        payload = self.http.json(
            "https://news-mediator.tradingview.com/news-flow/v2/news",
            {"filter": "lang:zh-Hans", "client": "screener", "streaming": "false"},
        )
        result = []
        for item in array(payload, "items")[:11]:
            if not item.get("title"):
                continue
            content = ""
            try:
                detail = self.http.json(
                    "https://news-headlines.tradingview.com/v3/story", {"id": item["id"], "lang": "zh-Hans"}
                )
                content = detail.get("shortDescription") or detail.get("short_description") or ""
            except MarketDataError:
                pass
            result.append(
                {
                    "title": item["title"],
                    "content": content,
                    "source": "外媒",
                    "time": timestamp(item["published"]).strftime("%H:%M:%S"),
                    "dataTime": timestamp(item["published"]).isoformat(),
                    "url": "https://cn.tradingview.com/news/" + item["id"],
                    "subjects": [],
                    "stocks": [],
                }
            )
        return result

    def refresh_telegraphs(self, source=""):
        source = {"cls": "财联社电报", "sina": "新浪财经", "tradingview": "外媒"}.get(source, source)
        selected = [source] if source else ["财联社电报", "新浪财经", "外媒"]
        result, failures = [], []
        for value in selected:
            if value not in {"财联社电报", "新浪财经", "外媒"}:
                raise ValueError("invalid telegraph source")
            try:
                result.extend(self._live_news(value))
            except (MarketDataError, ValueError, KeyError) as exc:
                failures.append(str(exc))
        if not result:
            raise MarketDataError("news providers unavailable: " + "; ".join(failures))
        # Persist source observations, never trading/watchlist state.
        with sqlite3.connect(self.config.main_db, timeout=10) as connection:
            columns = {row[1] for row in connection.execute("PRAGMA table_info(telegraph_list)")}
            if not columns:
                raise MarketDataError("telegraphs schema is unavailable")
            for item in result:
                field = "title" if item["title"] else "content"
                exists = connection.execute(
                    f"SELECT 1 FROM telegraph_list WHERE {field}=? LIMIT 1", (item[field],)
                ).fetchone()
                if not exists:
                    row = {
                        "title": item["title"],
                        "content": item["content"],
                        "source": item["source"],
                        "url": item["url"],
                        "time": item["time"],
                        "data_time": item["dataTime"],
                        "created_at": now().isoformat(),
                        "updated_at": now().isoformat(),
                        "is_red": bool(item.get("isRed")),
                        "sentiment_result": self.sentiment_weighted(item["content"])["result"]["Description"],
                    }
                    row = {key: val for key, val in row.items() if key in columns}
                    names = list(row)
                    inserted = connection.execute(
                        f"INSERT INTO telegraph_list ({','.join(names)}) VALUES ({','.join('?' for _ in names)})",
                        tuple(row.values()),
                    )
                    tables = {
                        value[0]
                        for value in connection.execute("SELECT name FROM sqlite_master WHERE type='table'")
                    }
                    if {"tags", "telegraph_tags"} <= tables:
                        for kind, label in (("subject", "subjects"), ("stock", "stocks")):
                            for name in item.get(label, []):
                                tag = connection.execute(
                                    "SELECT id FROM tags WHERE name=? AND type=? LIMIT 1", (name, kind)
                                ).fetchone()
                                tag_id = (
                                    tag[0]
                                    if tag
                                    else connection.execute(
                                        "INSERT INTO tags(name,type,created_at,updated_at) VALUES(?,?,?,?)",
                                        (name, kind, now().isoformat(), now().isoformat()),
                                    ).lastrowid
                                )
                                connection.execute(
                                    "INSERT INTO telegraph_tags(telegraph_id,tag_id,created_at,updated_at) VALUES(?,?,?,?)",
                                    (inserted.lastrowid, tag_id, now().isoformat(), now().isoformat()),
                                )
        return self.telegraphs(source)
