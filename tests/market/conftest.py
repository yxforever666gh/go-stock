import sqlite3
from pathlib import Path

import httpx
import pytest

from stock_god.config import AppConfig
from stock_god.market import MarketServices


@pytest.fixture
def config(tmp_path):
    main, minute = tmp_path / "stock.db", tmp_path / "minute.db"
    with sqlite3.connect(main) as db:
        db.executescript("""
        CREATE TABLE tushare_stock_basic(id INTEGER PRIMARY KEY,ts_code TEXT,name TEXT,list_status TEXT,list_date TEXT,deleted_at TEXT);
        CREATE TABLE followed_stock(stock_code TEXT,price REAL,cost_price REAL,volume INTEGER,sort INTEGER,alarm_price REAL,alarm_change_percent REAL,is_del INTEGER);
        INSERT INTO tushare_stock_basic VALUES(1,'600000.SH','浦发银行','L','19991110',NULL);
        INSERT INTO followed_stock VALUES('sh600000',8.0,9.0,100,1,12,10,0);
        CREATE TABLE telegraph_list(id INTEGER PRIMARY KEY,created_at TEXT,updated_at TEXT,deleted_at TEXT,time TEXT,data_time TEXT,title TEXT,content TEXT,source TEXT,url TEXT,is_red BOOLEAN DEFAULT 0,sentiment_result TEXT);
        """)
        db.executescript(
            (Path(__file__).parent / "fixtures/historical_market_schema.sql").read_text(encoding="utf8")
        )
    with sqlite3.connect(minute) as db:
        db.executescript("""
        CREATE TABLE minute_bar(stock_code TEXT,trade_time INTEGER,open REAL,high REAL,low REAL,close REAL,volume REAL,amount REAL,source TEXT,updated_at INTEGER,PRIMARY KEY(stock_code,trade_time));
        CREATE TABLE market_trade_tick(asset_type TEXT,symbol TEXT,traded_at INTEGER,sequence INTEGER,price REAL,volume REAL,amount REAL,side TEXT,source TEXT,updated_at INTEGER,PRIMARY KEY(asset_type,symbol,traded_at,sequence));
        CREATE TABLE market_auction_snapshot(asset_type TEXT,symbol TEXT,trade_date TEXT,observed_at INTEGER,phase TEXT,indicative_price REAL,matched_volume REAL,matched_amount REAL,unmatched_volume REAL,unmatched_side TEXT,source TEXT,updated_at INTEGER,PRIMARY KEY(asset_type,symbol,trade_date,observed_at,phase));
        """)
    return AppConfig(
        tmp_path,
        main,
        minute,
        tmp_path / "dist",
        tmp_path / "market",
        tmp_path / "index",
        scheduler_enabled=False,
    )


@pytest.fixture
def make_market(config):
    clients = []

    def make(handler=None, settings=None):
        def forbidden(request):
            raise AssertionError("unexpected provider request: " + request.url.path)

        client = httpx.Client(transport=httpx.MockTransport(handler or forbidden))
        clients.append(client)
        return MarketServices(config, settings, client=client)

    yield make
    for client in clients:
        client.close()
