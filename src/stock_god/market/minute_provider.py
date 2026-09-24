"""Fixed-catalog Diemeng calls, validation, throttling and verified downloads."""

import gzip
import io
import json
import tempfile
import time
from datetime import datetime
from decimal import Decimal
from hashlib import sha256
from pathlib import Path
from threading import Event, Lock
from typing import Any
from urllib.parse import urlsplit

import httpx
from jsonschema import Draft202012Validator, ValidationError

from .mcp_catalog import CATALOG
from .minute_local import STOCK_FIELDS, iso_time, local_datetime, minute_time, normalize_period, numeric

DATE_LAYOUTS = {
    "2006-01-02": "%Y-%m-%d",
    "20060102": "%Y%m%d",
    "2006-01-02 15:04:05": "%Y-%m-%d %H:%M:%S",
    "2006-01-02 15:04": "%Y-%m-%d %H:%M",
    "2006": "%Y",
    "200601": "%Y%m",
    "2006-01": "%Y-%m",
}


def default_config_path():
    return Path.home() / ".codex/secrets/diemeng/minute-api.json"


def validate_arguments(endpoint, args):
    if not isinstance(args, dict):
        raise ValueError("接口参数必须是JSON对象")
    try:
        Draft202012Validator(endpoint["input_schema"]).validate(args)
    except ValidationError as error:
        raise ValueError("参数不符合 " + endpoint["name"] + " 的接口约定：" + error.message) from error
    parsed = {}
    for name, layouts in endpoint.get("date_formats", {}).items():
        if name not in args:
            continue
        for layout in layouts:
            fmt = DATE_LAYOUTS.get(layout)
            if fmt is None:
                raise ValueError("不支持的冻结日期格式：" + layout)
            try:
                stamp = datetime.strptime(args[name], fmt)
            except (ValueError, TypeError):
                continue
            if stamp.strftime(fmt) == args[name]:
                parsed[name] = stamp
                break
        else:
            raise ValueError(name + " 日期格式或日期值无效")
    for start, end in (("start_time", "end_time"), ("start_date", "end_date"), ("start_date", "finish_date")):
        if start in parsed and end in parsed and parsed[start] > parsed[end]:
            raise ValueError(start + " 不能晚于 " + end)
    if endpoint["path"] == "/api/stock/forecast" and ("start_date" in args) != ("finish_date" in args):
        raise ValueError("start_date 和 finish_date 必须成对提供")
    if endpoint["path"].endswith("/macd"):
        if args.get("slow_period", 26) <= args.get("fast_period", 12):
            raise ValueError("slow_period 必须大于 fast_period（默认12/26）")
        if (
            endpoint["path"] == "/api/stock/macd"
            and args.get("level", "daily") == "daily"
            and (len(args["start_time"]) != 10 or len(args["end_time"]) != 10)
        ):
            raise ValueError("日级MACD时间应使用 YYYY-MM-DD")
    for key in ("ma_periods", "mavol_periods"):
        if key in args:
            for part in args[key].split(","):
                text = part.strip()
                if not text.isdigit() or int(text) < 1 or str(int(text)) != text:
                    raise ValueError(key + " 必须是正整数周期列表")


def _json_load(raw):
    return json.loads(raw, parse_constant=_invalid_constant)


def _invalid_constant(value):
    raise ValueError("无效JSON数值：" + value)


class _JSONStream:
    def __init__(self, reader):
        self.reader = reader
        self.buffer = ""
        self.eof = False
        self.decoder = json.JSONDecoder(parse_constant=_invalid_constant)

    def fill(self):
        chunk = self.reader.read(65536)
        if not chunk:
            self.eof = True
        self.buffer += chunk

    def peek(self):
        while True:
            self.buffer = self.buffer.lstrip()
            if self.buffer or self.eof:
                return self.buffer[:1]
            self.fill()

    def token(self, want):
        if self.peek() != want:
            raise ValueError("下载JSON结构无效")
        self.buffer = self.buffer[1:]

    def value(self):
        self.peek()
        while True:
            try:
                value, end = self.decoder.raw_decode(self.buffer)
            except json.JSONDecodeError:
                if self.eof:
                    raise ValueError("下载JSON不完整") from None
                self.fill()
                continue
            self.buffer = self.buffer[end:]
            return value


def validate_dump(path):
    with gzip.open(path, "rt", encoding="utf-8") as reader:
        stream = _JSONStream(reader)
        kind = stream.peek()
        if kind not in ("[", "{"):
            raise ValueError("unexpected download root")
        stream.token(kind)
        closing = "]" if kind == "[" else "}"
        if stream.peek() != closing:
            while True:
                if kind == "{":
                    key = stream.value()
                    if not isinstance(key, str) or key in ("code", "msg", "data"):
                        raise ValueError("business envelope is not a market dump")
                    stream.token(":")
                value = stream.value()
                if not isinstance(value, dict if kind == "[" else list):
                    raise ValueError("invalid market record shape")
                if stream.peek() == closing:
                    break
                stream.token(",")
        stream.token(closing)
        if stream.peek():
            raise ValueError("invalid trailing download data")


class DiemengClient:
    def __init__(self, base_url, api_key, *, download_dir, client=None, interval=1.2, timeout=60, stop=None):
        if not isinstance(base_url, str) or not isinstance(api_key, str):
            raise ValueError("蝶梦base_url与api_key必须是字符串")
        self.base_url = base_url.strip().rstrip("/")
        self._key = api_key.strip()
        parsed = urlsplit(self.base_url)
        if (
            parsed.scheme not in ("http", "https")
            or not parsed.netloc
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
            or not self._key
        ):
            raise ValueError("蝶梦配置需提供HTTP(S) base_url和非空api_key，URL不得包含凭据或查询参数")
        if not parsed.path:
            self.base_url += "/api"
        self.http = client or httpx.Client(timeout=timeout, follow_redirects=False, trust_env=False)
        self.owns_client = client is None
        self.download_dir = Path(download_dir).resolve()
        self.interval = interval
        self.timeout = timeout
        self.stop = stop or Event()
        self._gate = Lock()
        self._next = 0.0
        self.endpoints = {endpoint["name"]: endpoint for endpoint in CATALOG}

    @classmethod
    def load(cls, path=None, *, download_dir, **kwargs):
        path = Path(path) if path else default_config_path()
        if not path.exists():
            return None
        try:
            config = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as error:
            raise ValueError("无法读取蝶梦私有配置JSON") from error
        if not isinstance(config, dict):
            raise ValueError("蝶梦私有配置必须是JSON对象")
        return cls(config.get("base_url", ""), config.get("api_key", ""), download_dir=download_dir, **kwargs)

    def close(self):
        self.stop.set()
        if self.owns_client:
            self.http.close()

    def _redact(self, value):
        if isinstance(value, str):
            return value.replace(self._key, "[REDACTED]")
        if isinstance(value, list):
            return [self._redact(v) for v in value]
        if isinstance(value, dict):
            return {self._redact(k): self._redact(v) for k, v in value.items()}
        return value

    def _wait(self, deadline):
        remaining = max(0, deadline - time.monotonic())
        if not self._gate.acquire(timeout=remaining):
            raise ValueError("蝶梦请求等待超时或已取消")
        try:
            delay = max(0, self._next - time.monotonic())
            if delay > max(0, deadline - time.monotonic()) or self.stop.wait(delay):
                raise ValueError("蝶梦请求等待超时或已取消")
            if self.stop.is_set():
                raise ValueError("蝶梦请求已取消")
            self._next = time.monotonic() + self.interval
        finally:
            self._gate.release()

    def call(self, name, args) -> dict[str, Any]:
        endpoint = self.endpoints.get(name)
        if endpoint is None:
            raise ValueError("未知的蝶梦接口")
        try:
            validate_arguments(endpoint, args)
        except ValueError as error:
            raise ValueError(self._redact(str(error))) from None
        deadline = time.monotonic() + self.timeout
        self._wait(deadline)
        address = self.base_url + endpoint["path"].removeprefix("/api")
        headers = {"apiKey": self._key, "Content-Type": "application/json"}
        if endpoint.get("download"):
            headers["Accept-Encoding"] = "identity"
        options = dict(params=args) if endpoint["method"] == "GET" else dict(json=args)
        try:
            with self.http.stream(
                endpoint["method"],
                address,
                headers=headers,
                timeout=max(0.001, deadline - time.monotonic()),
                follow_redirects=False,
                **options,
            ) as response:
                if not 200 <= response.status_code < 300:
                    raise ValueError(
                        f"蝶梦HTTP {response.status_code}（鉴权/权限/额度或上游错误），未自动重试"
                    )
                chunks = iter((response.content,)) if response.is_stream_consumed else response.iter_raw()
                if endpoint.get("download"):
                    return self._download(endpoint, args, chunks, deadline)
                raw = bytearray()
                for chunk in chunks:
                    if self.stop.is_set() or time.monotonic() > deadline:
                        raise ValueError("蝶梦响应读取超时或已取消")
                    raw.extend(chunk)
                    if len(raw) > 64 << 20:
                        raise ValueError("蝶梦单页JSON超过64MiB，请缩小查询范围")
                if response.headers.get("Content-Encoding", "").lower() == "gzip":
                    with gzip.GzipFile(fileobj=io.BytesIO(raw)) as source:
                        raw = bytearray(source.read((64 << 20) + 1))
                return self._decode(endpoint, bytes(raw))
        except httpx.HTTPError:
            raise ValueError("蝶梦网络请求失败、超时或已取消") from None
        except (gzip.BadGzipFile, EOFError):
            raise ValueError("蝶梦压缩响应无效") from None

    def _decode(self, endpoint, raw) -> dict[str, Any]:
        if len(raw) > 64 << 20:
            raise ValueError("蝶梦单页JSON超过64MiB，请缩小查询范围")
        try:
            value = _json_load(raw)
        except (ValueError, UnicodeError):
            raise ValueError("蝶梦响应不是有效JSON对象") from None
        if not isinstance(value, dict):
            raise ValueError("蝶梦响应不是有效JSON对象")
        if str(value.get("code")) != "200":
            raise ValueError(self._redact(f"蝶梦业务错误 code={value.get('code')}: {value.get('msg')}"))
        if "data" not in value:
            raise ValueError("蝶梦响应缺少data")
        result = dict(source="diemeng", endpoint=endpoint["path"], response=self._redact(value))
        if endpoint.get("fields"):
            result["fields"] = [
                {key.capitalize(): value for key, value in field.items()} for field in endpoint["fields"]
            ]
        return result

    def _download(self, endpoint, args, chunks, deadline) -> dict[str, Any]:
        iterator = iter(chunks)
        first = next(iterator, b"")
        while len(first) < 2:
            extra = next(iterator, b"")
            if not extra:
                break
            first += extra
        if not first.startswith(b"\x1f\x8b"):
            raw = bytearray(first)
            for chunk in iterator:
                raw.extend(chunk)
                if len(raw) > 64 << 20:
                    break
            self._decode(endpoint, bytes(raw))
            raise ValueError("蝶梦下载未返回GZIP文件")
        self.download_dir.mkdir(parents=True, exist_ok=True)
        file = tempfile.NamedTemporaryFile(
            prefix="daily-" + args["date"] + "-", suffix=".json.gz", dir=self.download_dir, delete=False
        )
        path = Path(file.name)
        digest = sha256()
        count = 0
        try:
            for chunk in _prepend(first, iterator):
                if self.stop.is_set() or time.monotonic() > deadline:
                    raise ValueError("蝶梦下载中断，未完成文件已清理")
                file.write(chunk)
                digest.update(chunk)
                count += len(chunk)
            file.close()
            validate_dump(path)
            return dict(
                source="diemeng",
                endpoint=endpoint["path"],
                download=dict(
                    path=str(path),
                    bytes=count,
                    sha256=digest.hexdigest(),
                    summary=f"全市场 {args['date']}，level={args.get('level', '<nil>')}，GZIP文件已保存；未展开到对话。",
                ),
            )
        except BaseException:
            file.close()
            path.unlink(missing_ok=True)
            raise

    def minutes(self, symbol, period, start, end):
        period = normalize_period(symbol, period)
        if start > end:
            raise ValueError("开始时间不能晚于结束时间")
        args = dict(
            stock_code=symbol[2:] + "." + symbol[:2].upper(),
            level=period[:-1] + "min",
            start_time=local_datetime(start).strftime("%Y-%m-%d %H:%M:%S"),
            end_time=local_datetime(end).strftime("%Y-%m-%d %H:%M:%S"),
            page=0,
            page_size=10000,
        )
        data = self.call("diemeng_stock_history", args)["response"]["data"]
        if not isinstance(data, dict):
            raise ValueError("蝶梦分钟data必须是对象")
        items = data.get("list", data.get("items"))
        if not isinstance(items, list):
            raise ValueError("蝶梦分钟缺少list/items数组")
        total = data.get("total")
        if total is not None:
            if isinstance(total, bool) or not isinstance(total, int) or total < 0:
                raise ValueError("蝶梦分钟total无效")
            if total > len(items):
                raise ValueError("蝶梦分钟结果有后续分页，请缩小时间范围或使用diemeng_stock_history分页查询")
        rows = []
        for item in items:
            if (
                not isinstance(item, dict)
                or not isinstance(item.get("trade_time"), str)
                or len(item["trade_time"]) != 19
            ):
                raise ValueError("蝶梦分钟trade_time无效")
            stamp = minute_time(item["trade_time"])
            row = dict.fromkeys(STOCK_FIELDS[6:])
            row["time"] = iso_time(stamp)
            for field in ("open", "high", "low", "close", "amount", "vol"):
                if item.get(field) is None:
                    raise ValueError("蝶梦分钟缺少" + field)
                value = numeric(str(item[field]))
                if value is None:
                    raise ValueError("蝶梦分钟缺少" + field)
                if field == "vol":
                    volume = Decimal(value) * 100
                    if volume != volume.to_integral_value():
                        raise ValueError("蝶梦成交量换算为股后不是整数")
                    row["volume"] = int(volume)
                else:
                    row[field] = value
            if start <= stamp <= end:
                rows.append(row)
        return sorted(rows, key=lambda row: row["time"])


def _prepend(first, iterator):
    yield first
    yield from iterator
