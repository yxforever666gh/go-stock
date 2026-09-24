"""Existing market news, rankings and reference provider contracts."""

from collections import Counter
from datetime import timedelta
import hashlib
import json
import re
import sqlite3

from bs4 import BeautifulSoup

from .common import MarketDataError, database_rows, envelope, now, timestamp

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


class News:
    def global_indexes(self):
        result = self.http.json("https://proxy.finance.qq.com/ifzqgtimg/appstock/app/rank/indexRankDetail2",
                                headers={"Referer": "https://stockapp.finance.qq.com/mstats"})
        if result.get("code", 0) != 0 or not isinstance(result.get("data"), dict):
            raise MarketDataError("global index source rejected request")
        data = result["data"]
        return {key: array(data | {key: data.get(key) or []}, key) for key in ("common", "america", "europe", "asia", "other")}

    def industry_rank(self, sort="", count=20):
        data = self.http.json("https://proxy.finance.qq.com/ifzqgtimg/appstock/app/mktHs/rank",
                              {"l": count, "p": 1, "t": "01/averatio", "ordertype": "", "o": sort})
        if data.get("code", 0) != 0:
            raise MarketDataError("industry source rejected request")
        return array(data, "data")

    def money_rank(self, category="0", sort="netamount", *, stocks=False):
        endpoint = "ssl_bkzj_ssggzj" if stocks else "ssl_bkzj_bk"
        return array(self.http.json(SINA+endpoint, {"page": 1, "num": 20, "sort": sort or "netamount", "asc": 0, "fenlei": category}))

    def money_trend(self, code, days=10):
        rows = array(self.http.json(SINA+"ssl_qsfx_zjlrqs", {"page": 1, "num": days, "sort": "opendate", "asc": 0, "daima": code}))
        return list(reversed(rows))

    def notices(self, code=""):
        digits = re.sub(r"^(sh|sz|bj|gb_|us_?)", "", code.lower()).split(".")[0]
        return array(self.http.json("https://np-anotice-stock.eastmoney.com/api/security/ann",
            {"page_size": 50, "page_index": 1, "ann_type": "SHA,CYB,SZA,BJA,INV", "client_source": "web", "f_node": 0,
             "stock_list": digits}, headers={"Referer": "https://data.eastmoney.com/notices/hsa/5.html"}), "data", "list")

    def research_reports(self, code="", *, industry=False):
        end = now()
        values = {"beginTime": (end-timedelta(hours=7*365)).date().isoformat(), "endTime": end.date().isoformat(),
                  "pageNo": 1, "pageSize": 50, "p": 1, "pageNum": 1, "pageNumber": 1,
                  "industryCode": code if industry else "*"}
        if industry:
            values.update(industry="*", qType="1")
            data = self.http.json("https://reportapi.eastmoney.com/report/list", values)
        else:
            values["code"] = re.sub(r"^(sh|sz|bj|gb_|us_?)", "", code.lower()).split(".")[0]
            data = self.http.json("https://reportapi.eastmoney.com/report/list2", method="POST", body=values)
        return array(data, "data")

    def dictionary(self, code):
        return self.http.cached("dictionary:"+code, 86400,
                                lambda: array(self.http.json("https://reportapi.eastmoney.com/report/bk", {"bkCode": code}), "data"))

    def long_tiger(self, day):
        params = {"reportName": "RPT_DAILYBILLBOARD_DETAILS", "columns": "ALL", "sortColumns": "TRADE_DATE,SECURITY_CODE",
                  "sortTypes": "-1,1", "pageSize": 500, "pageNumber": 1, "source": "WEB", "client": "WEB"}
        if day:
            params["filter"] = f"(TRADE_DATE='{day}')"
        return array(self.http.json("https://datacenter-web.eastmoney.com/api/data/v1/get", params), "result", "data")

    def hot_stocks(self, market_type="10"):
        self.http.text("https://xueqiu.com/hq")
        return array(self.http.json("https://stock.xueqiu.com/v5/stock/hot_stock/list.json",
                                   {"page": 1, "size": 100, "_type": market_type, "type": market_type},
                                   headers={"Referer": "https://xueqiu.com/"}), "data", "items")

    def hot_events(self, size=10):
        self.http.text("https://xueqiu.com/")
        return array(self.http.json("https://xueqiu.com/hot_event/list.json", {"count": size},
                                   headers={"Referer": "https://xueqiu.com/"}), "list")

    def hot_topics(self, size=10):
        return array(self.http.json("https://gubatopic.eastmoney.com/interface/GetData.aspx", method="POST",
                     form={"param": f"ps={size}&p=1&type=0", "path": "newtopic/api/Topic/HomePageListRead", "env": "2"}), "re")

    def investment_calendar(self, year_month=""):
        return array(self.http.json("https://app.jiuyangongshe.com/jystock-app/api/v1/timeline/list", method="POST",
                    body={"date": year_month or now().strftime("%Y-%m"), "grade": "0"},
                    headers={"Referer": "https://www.jiuyangongshe.com/", "platform": "3"}), "data")

    def cls_calendar(self):
        values = "app=CailianpressWeb&flag=0&os=web&sv=8.4.6&type=0"
        sign = hashlib.md5(hashlib.sha1(values.encode()).hexdigest().encode()).hexdigest()
        return array(self.http.json("https://www.cls.cn/api/calendar/web/list?"+values+"&sign="+sign), "data")

    def query_stocks(self, words):
        fingerprint = self.settings.get("qgqpBId") or self.settings.get("QgqpBId")
        if not fingerprint:
            raise MarketDataError("东方财富选股需要已配置的 qgqp_b_id")
        return self.http.json("https://np-tjxg-g.eastmoney.com/api/smart-tag/stock/v3/pw/search-code", method="POST",
            headers={"Origin": "https://xuangu.eastmoney.com", "Referer": "https://xuangu.eastmoney.com/"},
            body={"keyWord": words, "pageSize": 50, "pageNo": 1, "fingerprint": fingerprint, "gids": [], "matchWord": "",
                  "timestamp": str(int(now().timestamp())), "shareToGuba": False, "requestId": "", "needCorrect": True,
                  "removedConditionIdList": [], "xcId": "", "ownSelectAll": False, "dxInfo": [], "extraCondition": ""})

    def telegraphs(self, source=""):
        query = "SELECT * FROM telegraph_list WHERE deleted_at IS NULL"
        params = ()
        if source:
            query += " AND source=?"
            params = (source,)
        rows = database_rows(self.config.main_db, query+" ORDER BY data_time DESC,time DESC LIMIT 50", params)
        return [{"ID": row["id"], "title": row.get("title", ""), "content": row.get("content", ""),
                 "source": row.get("source", ""), "url": row.get("url", ""), "time": row.get("time", ""),
                 "dataTime": row.get("data_time", ""), "subjects": [], "stocks": [], "tags": [],
                 "isRed": bool(row.get("is_red", False)), "sentimentResult": row.get("sentiment_result", "")} for row in rows]

    def _live_news(self, source):
        if source == "财联社电报":
            payload = self.http.json("https://www.cls.cn/nodeapi/telegraphList", {"app": "CailianpressWeb", "os": "web", "sv": "8.4.6"})
            items = array(payload, "data", "roll_data")
            return [{"title": item.get("title", ""), "content": item.get("content", ""), "source": source,
                     "time": timestamp(item["ctime"]).isoformat(), "dataTime": timestamp(item["ctime"]).isoformat(),
                     "url": f"https://www.cls.cn/detail/{item['id']}", "subjectTags": []} for item in items]
        if source == "新浪财经":
            raw = self.http.text("https://zhibo.sina.com.cn/api/zhibo/feed", {"page": 1, "page_size": 50, "zhibo_id": 152, "tag_id": 0, "dire": "f"})
            payload = json.loads(raw) if raw.lstrip().startswith("{") else json.loads(raw[raw.find("(")+1:raw.rfind(")")])
            return [{"title": "", "content": item.get("rich_text", ""), "source": source,
                     "time": timestamp(item["create_time"]).isoformat(), "dataTime": timestamp(item["create_time"]).isoformat(),
                     "url": item.get("docurl", ""), "subjectTags": []} for item in array(payload, "result", "data", "feed", "list")]
        payload = self.http.json("https://news-mediator.tradingview.com/news-flow/v2/news", {"filter": "lang:zh-Hans", "client": "screener", "streaming": "false"})
        return [{"title": item.get("title", ""), "content": item.get("title", ""), "source": "外媒",
                 "time": timestamp(item["published"]).isoformat(), "dataTime": timestamp(item["published"]).isoformat(),
                 "url": "https://cn.tradingview.com/news/"+item["id"], "subjectTags": []} for item in array(payload, "items")]

    def refresh_telegraphs(self, source=""):
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
            raise MarketDataError("news providers unavailable: "+"; ".join(failures))
        # Persist source observations, never trading/watchlist state.
        with sqlite3.connect(self.config.main_db, timeout=10) as connection:
            columns = {row[1] for row in connection.execute("PRAGMA table_info(telegraph_list)")}
            if not columns:
                raise MarketDataError("telegraphs schema is unavailable")
            for item in result:
                exists = connection.execute("SELECT 1 FROM telegraph_list WHERE source=? AND content=? LIMIT 1",
                                            (item["source"], item["content"])).fetchone()
                if not exists:
                    row = {"title": item["title"], "content": item["content"], "source": item["source"], "url": item["url"],
                           "time": item["time"], "data_time": item["dataTime"], "created_at": now().isoformat(), "updated_at": now().isoformat()}
                    row = {key: val for key, val in row.items() if key in columns}
                    names = list(row)
                    connection.execute(f"INSERT INTO telegraph_list ({','.join(names)}) VALUES ({','.join('?' for _ in names)})", tuple(row.values()))
        return sorted(result, key=lambda row: row["time"], reverse=True)[:50]
