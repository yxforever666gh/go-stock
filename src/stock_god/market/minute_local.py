"""Local CSV market data, exact timestamp matching and conflict detection."""

import csv
import json
import math
import os
import re
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta, timezone
from decimal import Decimal
from pathlib import Path
from stat import FILE_ATTRIBUTE_REPARSE_POINT
from typing import Any

CN = timezone(timedelta(hours=8))
EPOCH = datetime(1970, 1, 1, tzinfo=UTC)
SYMBOL = re.compile(r"(?:sh|sz|bj)\d{6}\Z")
PERIODS = {"1m": "1分钟", "5m": "5分钟", "15m": "15分钟", "30m": "30分钟", "60m": "60分钟"}
STOCK_HEADERS = "日期,开盘,最高,最低,收盘,成交量(股),成交额(元),涨跌(元),涨跌幅(%),换手率(%),流通股本(股),总股本(股)".split(
    ","
)
STOCK_FIELDS = (
    "open high low close volume amount change change_pct turnover_rate_pct float_shares total_shares".split()
)
INDEX_HEADERS = "日期,时间,开盘,最高,最低,收盘,成交量,成交额".split(",")
AUCTION_HEADERS = "code trade_date prev_close current volume amount cjcs b1_p b1_v b2_p b2_v a1_p a1_v a2_p a2_v total_bid average_bid total_ask average_ask".split()
JSON_NUMBER = re.compile(r"-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?\Z")


class MinuteReadError(ValueError):
    """Existing data is corrupt or its validated index is stale."""


def minute_time(value: str) -> int:
    if not isinstance(value, str):
        raise ValueError("时间必须是字符串")
    match = re.fullmatch(r"(\d{4}-\d\d-\d\d)T(\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)", value)
    if match:
        parsed = datetime.fromisoformat(match[1] + "T" + match[2] + match[4].replace("Z", "+00:00"))
        nanos = int((match[3] or "").ljust(9, "0"))
    else:
        if not re.fullmatch(r"\d{4}-\d\d-\d\d \d\d:\d\d(?::\d\d)?", value):
            raise ValueError("时间无效：使用带时区RFC3339或北京时间 YYYY-MM-DD HH:mm[:ss]")
        parsed = datetime.strptime(
            value, "%Y-%m-%d %H:%M:%S" if len(value) == 19 else "%Y-%m-%d %H:%M"
        ).replace(tzinfo=CN)
        nanos = 0
    difference = parsed.astimezone(UTC) - EPOCH
    return (difference.days * 86400 + difference.seconds) * 1_000_000_000 + nanos


def local_datetime(stamp: int) -> datetime:
    return (EPOCH + timedelta(seconds=stamp // 1_000_000_000)).astimezone(CN)


def iso_time(stamp: int) -> str:
    local = local_datetime(stamp)
    fraction = f".{stamp % 1_000_000_000:09d}".rstrip("0") if stamp % 1_000_000_000 else ""
    return local.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "+08:00"


def day_time(value: str) -> int | None:
    if not value:
        return None
    parsed = datetime.strptime(value, "%Y-%m-%d")
    if parsed.strftime("%Y-%m-%d") != value:
        raise ValueError("日期须为 YYYY-MM-DD")
    return minute_time(value + " 00:00:00")


def time_range(start, end):
    first, last = minute_time(start), minute_time(end)
    if first > last:
        raise ValueError("开始时间不能晚于结束时间")
    return first, last


def normalize_period(symbol, period=""):
    period = period or "1m"
    if not isinstance(symbol, str) or not SYMBOL.fullmatch(symbol) or period not in PERIODS:
        raise ValueError("股票/指数代码或周期无效")
    return period


def normalize_symbol(value):
    text = str(value).strip().lower()
    if SYMBOL.fullmatch(text):
        return text
    match = re.fullmatch(r"(\d{6})\.(sh|sz|bj)", text)
    return match[2] + match[1] if match else ""


def numeric(value, nullable=False):
    if value == "" and nullable:
        return None
    if not JSON_NUMBER.fullmatch(value):
        raise MinuteReadError("无效数值")
    return Decimal(value) if any(ch in value for ch in ".eE") else int(value)


def transport_value(value) -> Any:
    if isinstance(value, Decimal):
        converted = float(value)
        if not math.isfinite(converted):
            raise ValueError("数值超出JSON传输范围")
        return converted
    if isinstance(value, dict):
        return {key: transport_value(item) for key, item in value.items()}
    if isinstance(value, list):
        return [transport_value(item) for item in value]
    return value


def json_text(value):
    return json.dumps(transport_value(value), ensure_ascii=False, separators=(",", ":"), allow_nan=False)


@dataclass(frozen=True)
class SourceFile:
    path: Path
    relative: str
    size: int
    modified: int

    def check(self):
        stat = self.path.stat()
        if self.path.is_symlink() or stat.st_size != self.size or stat.st_mtime_ns != self.modified:
            raise MinuteReadError("数据文件已变化，请重建索引：" + self.relative)


def read_csv(path: Path, expected):
    try:
        with path.open("r", encoding="utf-8-sig", newline="") as source:
            reader = csv.reader(source, strict=True)
            header = next(reader, None)
            if header != expected:
                raise MinuteReadError("CSV表头不符合约定：" + str(path))
            for row in reader:
                if not row:
                    continue
                if len(row) != len(expected):
                    raise MinuteReadError("CSV字段数量无效：" + str(path))
                yield row
    except (csv.Error, UnicodeError) as error:
        raise MinuteReadError("CSV数据解析失败：" + str(path)) from error


def merge_row(values, origins, row, relative):
    key = row["time"]
    if key in values and values[key] != row:
        raise MinuteReadError(f"数据冲突 {key}：{origins[key]} / {relative}")
    values[key] = row
    origins[key] = relative


class LocalStore:
    def __init__(self, root):
        self.root = Path(root).resolve()
        if not self.root.is_dir():
            raise ValueError("data-root必须是存在的目录")
        self.stocks: dict[str, list[SourceFile]] = {}
        self.indices: dict[str, list[SourceFile]] = {}
        self.names: dict[str, str] = {}
        self.auction_files: list[SourceFile] = []
        folders = {value: key for key, value in PERIODS.items()}
        files = []
        directories = [self.root]
        while directories:
            with os.scandir(directories.pop()) as entries:
                for entry in entries:
                    path = Path(entry.path)
                    if entry.is_dir():
                        if not entry.is_symlink():
                            if not path.resolve().is_relative_to(self.root):
                                raise ValueError("数据目录越界：" + path.relative_to(self.root).as_posix())
                            directories.append(path)
                        continue
                    if path.suffix.lower() != ".csv":
                        continue
                    relative = path.relative_to(self.root).as_posix()
                    if entry.is_symlink():
                        raise ValueError("未识别或符号链接CSV：" + relative)
                    file_stat = entry.stat(follow_symlinks=False)
                    if (
                        getattr(file_stat, "st_file_attributes", 0) & FILE_ATTRIBUTE_REPARSE_POINT
                        and not path.resolve().is_relative_to(self.root)
                    ):
                        raise ValueError("未识别或符号链接CSV：" + relative)
                    files.append((path, file_stat))
        # Keep the previous global path order: later stock sources override earlier ones.
        for path, file_stat in sorted(files, key=lambda item: item[0].as_posix()):
            relative = path.relative_to(self.root).as_posix()
            parts = relative.split("/")
            if len(parts) < 2:
                raise ValueError("未识别或符号链接CSV：" + relative)
            if parts[0] == "分钟K线-指数" and path.name == "对应名称.csv":
                for row in read_csv(path, ["index", "code", "name"]):
                    self.names[row[1]] = row[2]
                continue
            file = SourceFile(path, relative, file_stat.st_size, file_stat.st_mtime_ns)
            if parts[0] == "集合竞价":
                self.auction_files.append(file)
                continue
            period = folders.get(path.parent.name)
            symbol = path.stem
            if not period or not SYMBOL.fullmatch(symbol):
                raise ValueError("未识别行情CSV：" + relative)
            destination = (
                self.stocks if parts[0] == "A股个股" else self.indices if parts[0] == "分钟K线-指数" else None
            )
            if destination is None:
                raise ValueError("未识别数据类别：" + relative)
            destination.setdefault(symbol + "/" + period, []).append(file)

    def stock_days(self, symbol, period="", start=None, end=None):
        period = normalize_period(symbol, period)
        values = {}
        found = False
        for file in self.stocks.get(symbol + "/" + period, []):
            try:
                for cells in read_csv(file.path, STOCK_HEADERS):
                    if not re.fullmatch(r"\d{4}-\d\d-\d\d \d\d:\d\d:\d\d", cells[0]):
                        raise MinuteReadError("股票CSV时间无效")
                    try:
                        stamp = minute_time(cells[0])
                        numbers = [numeric(value) for value in cells[1:]]
                    except ValueError as error:
                        raise MinuteReadError(str(error)) from error
                    if (start is not None and stamp < start) or (
                        end is not None and stamp >= end + 86400 * 1_000_000_000
                    ):
                        continue
                    row = {"time": iso_time(stamp), **dict(zip(STOCK_FIELDS, numbers, strict=False))}
                    values[row["time"]] = row
                found = True
            except FileNotFoundError:
                continue
        if not found:
            raise FileNotFoundError("股票数据文件不存在")
        return [values[key] for key in sorted(values)]

    def stock_range(self, symbol, period, start, end):
        if start > end:
            raise ValueError("开始时间不能晚于结束时间")
        rows = self.stock_days(
            symbol,
            period,
            day_time(local_datetime(start).date().isoformat()),
            day_time(local_datetime(end).date().isoformat()),
        )
        return [row for row in rows if start <= minute_time(row["time"]) <= end]

    def search_indices(self, query=""):
        found = {}
        for key in self.indices:
            symbol, period = key.split("/")
            found.setdefault(symbol, set()).add(period)
        needle = query.strip().lower()
        return [
            dict(
                symbol=symbol, name=self.names.get(symbol, ""), periods=[p for p in PERIODS if p in available]
            )
            for symbol, available in sorted(found.items())
            if needle in (symbol + " " + self.names.get(symbol, "")).lower()
        ]

    def index_bars(self, symbol, period, start, end):
        period = normalize_period(symbol, period)
        if start > end:
            raise ValueError("开始时间不能晚于结束时间")
        values = {}
        origins = {}
        for file in self.indices.get(symbol + "/" + period, []):
            for cells in read_csv(file.path, INDEX_HEADERS):
                try:
                    if len(cells[1]) != 5:
                        raise ValueError("指数CSV时间无效")
                    stamp = minute_time(cells[0] + " " + cells[1])
                    numbers = [numeric(v) for v in cells[2:]]
                except ValueError as error:
                    raise MinuteReadError(str(error)) from error
                if not start <= stamp <= end:
                    continue
                row = {"time": iso_time(stamp), **dict(zip(STOCK_FIELDS[:6], numbers, strict=False))}
                merge_row(values, origins, row, file.relative)
        return [values[key] for key in sorted(values)]

    def index_result(self, symbol, period, rows) -> dict[str, Any]:
        return dict(
            symbol=symbol,
            name=self.names.get(symbol, ""),
            period=period or "1m",
            source="csv_index",
            timezone="Asia/Shanghai",
            units=dict(price="index_points", volume="unknown", amount="unknown"),
            data=rows,
        )


def query_with_source(store, provider, symbol, period, source, start, end) -> dict[str, Any]:
    source = source or "auto"
    period = normalize_period(symbol, period)
    if source not in ("auto", "local", "diemeng"):
        raise ValueError("source仅支持auto、local、diemeng")
    if start > end:
        raise ValueError("开始时间不能晚于结束时间")
    base = dict(symbol=symbol, period=period, timezone="Asia/Shanghai")
    if source != "diemeng":
        try:
            rows = store.stock_range(symbol, period, start, end)
        except FileNotFoundError:
            if source == "local" or provider is None:
                raise
        else:
            if source == "local" or rows or provider is None:
                return dict(**base, source="csv", adjustment="unknown", data=rows)
    if provider is None:
        raise ValueError("蝶梦未配置：请填写本机私有配置文件后重启")
    return dict(
        **base, source="diemeng", adjustment="none", data=provider.minutes(symbol, period, start, end)
    )
