import gzip
from hashlib import sha256
import json
import time

import httpx
import pytest

from stock_god.market.mcp_catalog import CATALOG
from stock_god.market.minute_local import minute_time
from stock_god.market.minute_provider import DiemengClient


def client(tmp_path, handler, **kwargs):
    return DiemengClient(
        "https://example.test/api",
        "test-private-key",
        download_dir=tmp_path,
        client=httpx.Client(transport=httpx.MockTransport(handler)),
        interval=kwargs.pop("interval", 0),
        **kwargs,
    )


@pytest.mark.parametrize("endpoint", CATALOG, ids=lambda e: e["name"])
def test_all_44_frozen_endpoint_contracts_and_redaction(endpoint, tmp_path):
    calls = []
    compressed = gzip.compress(b'{"600941.SH":[["13:45",96.43,225]]}')

    def handle(request):
        calls.append(request)
        assert request.url.path == endpoint["path"] and request.method == endpoint["method"]
        assert request.headers["apiKey"] == "test-private-key"
        assert "test-private-key" not in str(request.url)
        if request.method == "GET":
            assert dict(request.url.params) == {key: str(value) for key, value in endpoint["example"].items()}
        else:
            assert json.loads(request.content) == endpoint["example"]
        if endpoint.get("download"):
            return httpx.Response(200, stream=httpx.ByteStream(compressed))
        return httpx.Response(
            200,
            json=dict(
                code=200,
                msg="ok",
                data=dict(
                    page=0,
                    total=3,
                    list=[dict(value=None, numeric_string="1.2300", secret="test-private-key")],
                ),
            ),
        )

    result = client(tmp_path, handle).call(endpoint["name"], endpoint["example"])
    assert len(calls) == 1 and result["source"] == "diemeng" and result["endpoint"] == endpoint["path"]
    assert "test-private-key" not in json.dumps(result)
    if endpoint.get("download"):
        from pathlib import Path

        assert Path(result["download"]["path"]).read_bytes() == compressed
        assert result["download"]["sha256"] == sha256(compressed).hexdigest()
    else:
        row = result["response"]["data"]["list"][0]
        assert row["value"] is None and row["numeric_string"] == "1.2300"
        assert row["secret"] == "[REDACTED]"


@pytest.mark.parametrize(
    "name,args",
    [
        ("diemeng_stock_income", {}),
        ("diemeng_stock_income", {"stock_code": "600941.SH", "end_date": "2026-02-30"}),
        ("diemeng_stock_income", {"stock_code": "600941.SH", "page": -1}),
        ("diemeng_stock_income", {"stock_code": "600941.SH", "page_size": 10001}),
        ("diemeng_stock_forecast", {"stock_code": "600941.SH", "finish_date": "2026-08-12"}),
        (
            "diemeng_stock_macd",
            {
                "stock_code": "600941.SH",
                "start_time": "2026-08-12",
                "end_time": "2026-08-12",
                "slow_period": 10,
            },
        ),
        (
            "diemeng_stock_macd",
            {
                "stock_code": "600941.SH",
                "start_time": "2026-08-12 13:45:00",
                "end_time": "2026-08-12 13:45:00",
            },
        ),
        (
            "diemeng_stock_ma",
            {
                "stock_code": "600941.SH",
                "start_time": "2026-08-12",
                "end_time": "2026-08-12",
                "ma_periods": "5,0",
            },
        ),
        ("diemeng_basic_calendar", {"start_time": "2026-08-13", "end_time": "2026-08-12"}),
        ("diemeng_stock_list", {"url": "https://other.example"}),
        ("diemeng_stock_list", {"apiKey": "test-private-key"}),
        ("arbitrary_url", {}),
    ],
)
def test_validation_happens_before_network_and_redacts_secrets(name, args, tmp_path):
    c = client(tmp_path, lambda request: pytest.fail("invalid input reached network"))
    with pytest.raises(ValueError) as caught:
        c.call(name, args)
    assert "test-private-key" not in str(caught.value)


@pytest.mark.parametrize(
    "status,body",
    [
        (401, "test-private-key"),
        (403, "denied"),
        (429, "quota"),
        (302, ""),
        (200, '{"code":403,"msg":"test-private-key denied","data":null}'),
        (200, "test-private-key"),
        (200, '{"code":200,"data":[]}garbage'),
    ],
)
def test_failures_never_retry_or_leak_credentials(status, body, tmp_path):
    calls = []

    def handle(request):
        calls.append(request)
        return httpx.Response(status, text=body, headers={"Location": "https://other.example"})

    with pytest.raises(ValueError) as caught:
        client(tmp_path, handle).call("diemeng_stock_list", {})
    assert len(calls) == 1 and "test-private-key" not in str(caught.value)


def test_minute_volume_conversion_null_fields_and_no_partial_page(tmp_path):
    total = 1

    def handle(request):
        return httpx.Response(
            200,
            json={
                "code": 200,
                "data": {
                    "total": total,
                    "list": [
                        {
                            "trade_time": "2026-08-12 13:45:00",
                            "open": 10,
                            "high": 11,
                            "low": 9,
                            "close": 10,
                            "amount": 2000,
                            "vol": "2.25",
                        }
                    ],
                },
            },
        )

    c = client(tmp_path, handle)
    at = minute_time("2026-08-12 13:45")
    rows = c.minutes("sh600941", "1m", at, at)
    assert rows[0]["volume"] == 225
    assert rows[0]["change"] is None and rows[0]["float_shares"] is None
    total = 2
    with pytest.raises(ValueError, match="后续分页"):
        c.minutes("sh600941", "1m", at, at)


def test_throttle_and_cancel(tmp_path):
    c = client(tmp_path, lambda request: httpx.Response(200, json={"code": 200, "data": []}), interval=0.035)
    c.call("diemeng_stock_list", {})
    start = time.monotonic()
    c.call("diemeng_stock_list", {})
    assert time.monotonic() - start >= 0.02
    c.stop.set()
    with pytest.raises(ValueError, match="取消"):
        c.call("diemeng_stock_list", {})


@pytest.mark.parametrize(
    "payload",
    [
        b'{"code":200,"data":[]}',
        b'{"600941.SH":42}',
        b"[42]",
        b'{"600941.SH":[]}trailing',
        b'[{"value":NaN}]',
    ],
)
def test_invalid_gzip_download_is_removed(payload, tmp_path):
    zipped = gzip.compress(payload)
    c = client(tmp_path, lambda request: httpx.Response(200, stream=httpx.ByteStream(zipped)))
    with pytest.raises(ValueError):
        c.call("diemeng_stock_daily_dump", {"date": "2026-08-12"})
    assert not list(tmp_path.glob("*.gz"))


def test_config_missing_does_not_disable_local_service(tmp_path):
    assert DiemengClient.load(tmp_path / "missing.json", download_dir=tmp_path) is None
    for url in (
        "ftp://example.test",
        "https://user:pass@example.test",
        "https://example.test/api?q=secret",
        "https://example.test/#secret",
    ):
        with pytest.raises(ValueError):
            DiemengClient(url, "secret", download_dir=tmp_path)


def test_gzip_json_and_split_download_magic(tmp_path):
    class Chunks(httpx.SyncByteStream):
        def __init__(self, body):
            self.body = body

        def __iter__(self):
            yield self.body[:1]
            yield self.body[1:]

    compressed = gzip.compress(b'{"code":200,"data":[{"secret":"test-private-key"}]}')
    c = client(
        tmp_path,
        lambda request: httpx.Response(200, headers={"Content-Encoding": "gzip"}, stream=Chunks(compressed)),
    )
    assert c.call("diemeng_stock_list", {})["response"]["data"][0]["secret"] == "[REDACTED]"
    dump = gzip.compress(b'{"600941.SH":[]}')
    c = client(tmp_path, lambda request: httpx.Response(200, stream=Chunks(dump)))
    result = c.call("diemeng_stock_daily_dump", {"date": "2026-08-12"})
    assert result["download"]["bytes"] == len(dump)


def test_config_invalid_types_are_rejected(tmp_path):
    for data in ([], {"base_url": "https://example.test/api", "api_key": None}):
        path = tmp_path / "private.json"
        path.write_text(json.dumps(data), encoding="utf-8")
        with pytest.raises(ValueError):
            DiemengClient.load(path, download_dir=tmp_path)
