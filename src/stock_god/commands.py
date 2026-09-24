"""Retained query/AI CLI and explicit offline SQLite archive/compaction."""

import argparse
import asyncio
import hashlib
import json
import os
import sqlite3
import tempfile
import zipfile
from contextlib import ExitStack
from copy import deepcopy
from datetime import UTC, datetime
from pathlib import Path

from .ai.client import AIClient
from .config import AppConfig
from .market import MarketDataError, MarketServices
from .market.common import CN
from .settings import SettingsStore
from .storage.backup import backup_database, file_sha256, verify_database
from .storage.db import Database, quote_identifier
from .storage.migrations import status

LEGACY_TABLES = (
    "ai_recommend_daily_bar",
    "ai_recommend_minute_bar",
    "ai_recommend_opening_review",
    "ai_recommend_stocks",
    "ai_recommend_yield_dirty_code",
    "ai_recommend_yield_meta",
    "ai_recommend_yield_override",
    "ai_recommend_yield_record_state",
    "ai_recommend_yield_state",
    "strategy_backtest_metric",
    "strategy_backtest_run",
    "strategy_backtest_trade",
    "strategy_candidate_snapshot",
    "strategy_order_event",
    "strategy_rule_snapshot",
    "strategy_run_snapshot",
    "strategy_runtime_control",
)


def _boolean(value):
    if isinstance(value, bool):
        return value
    if value.lower() in {"true", "1", "yes"}:
        return True
    if value.lower() in {"false", "0", "no"}:
        return False
    raise argparse.ArgumentTypeError("expected true or false")


def _json_argument(parser):
    parser.add_argument(
        "--json",
        action="store_true",
        default=argparse.SUPPRESS,
        help="JSON output (AI emits incremental NDJSON)",
    )


def add_arguments(subparsers):
    quote = subparsers.add_parser("quote", help="Read a live stock quote")
    quote.add_argument("--code", required=True)
    _json_argument(quote)
    search = subparsers.add_parser("search", help="Natural-language Eastmoney stock screening")
    search.add_argument("--words", required=True)
    search.add_argument("--page-size", type=int, default=5000)
    search.add_argument("--qgqp-b-id", default="")
    _json_argument(search)
    ai = subparsers.add_parser("ai", help="Stream a stock analysis using prediction-owned model settings")
    ai.add_argument("--stock-code", required=True)
    ai.add_argument("--stock-name", required=True)
    ai.add_argument("--question", default="")
    ai.add_argument("--ai-config-id", type=int, default=0)
    ai.add_argument("--base-url", default="")
    ai.add_argument("--api-key", default="")
    ai.add_argument("--model", default="")
    ai.add_argument("--max-tokens", type=int)
    ai.add_argument("--temperature", type=float)
    ai.add_argument("--timeout", type=int, default=300)
    ai.add_argument(
        "--thinking",
        nargs="?",
        const=True,
        default=True,
        type=_boolean,
        help="Chat Completions thinking extension; other protocols retain their defaults",
    )
    _json_argument(ai)
    release = subparsers.add_parser("release", help="Inspect the immutable application release")
    release.add_subparsers(dest="operation", required=True).add_parser("inspect")


def add_db_arguments(subparsers):
    archive = subparsers.add_parser("archive", help="Create a permanent verified two-database ZIP")
    archive.add_argument("--output", type=Path, required=True)
    archive.add_argument("--source-app-version", required=True)
    archive.add_argument("--source-commit", required=True)
    _json_argument(archive)
    compact = subparsers.add_parser("compact", help="Offline VACUUM; keep all database records")
    compact.add_argument("--database", choices=("main",), default="main")
    _json_argument(compact)


def _snapshot(config):
    database = Database(config.main_db, read_only=True)
    try:
        return SettingsStore(database).load()
    finally:
        database.close()


def _resolve_ai(args, snapshot):
    explicit = [args.base_url.strip(), args.api_key.strip(), args.model.strip()]
    if any(explicit) and not all(explicit):
        raise ValueError("--base-url, --api-key and --model must be supplied together")
    if args.ai_config_id < 0 or args.timeout <= 0:
        raise ValueError("model ID must be nonnegative and timeout must be positive")
    if args.max_tokens is not None and args.max_tokens <= 0:
        raise ValueError("max-tokens must be positive")
    if args.temperature is not None and not 0 <= args.temperature <= 2:
        raise ValueError("temperature must be between 0 and 2")
    if all(explicit):
        selected = {
            "ID": 0,
            "owner": "research2",
            "baseUrl": explicit[0],
            "apiKey": explicit[1],
            "modelName": explicit[2],
            "name": explicit[2],
            "apiProtocol": "chat_completions",
            "timeOut": args.timeout,
            "maxTokens": args.max_tokens or 4096,
            "disabled": False,
        }
    else:
        if snapshot is None:
            raise ValueError("prediction settings are required when model flags are not supplied")
        candidates = [
            deepcopy(model)
            for model in snapshot.models
            if not model.get("disabled")
            and (not args.ai_config_id or model.get("ID", model.get("id")) == args.ai_config_id)
        ]
        if not candidates:
            raise ValueError("no active prediction-owned AI configuration matches the request")
        selected = candidates[0]
        if not all(selected.get(key) for key in ("baseUrl", "apiKey", "modelName")):
            raise ValueError("prediction AI configuration is incomplete")
    # Preserve provider defaults for prediction. CLI-only overrides never mutate persisted models.
    options = {}
    protocol = selected.get("apiProtocol", "chat_completions")
    if protocol == "chat_completions":
        if args.max_tokens is not None:
            options["maxTokens"] = args.max_tokens
        if args.temperature is not None:
            options["temperature"] = args.temperature
        if args.thinking:
            options["thinking"] = True
    elif protocol == "anthropic_messages" and args.max_tokens is not None:
        # max_tokens is required by this protocol, including the old CLI implementation.
        selected["maxTokens"] = args.max_tokens
    return selected, options


async def _ai(args, config, snapshot):
    model, options = _resolve_ai(args, snapshot)
    settings = snapshot.config if snapshot is not None else {}
    market = MarketServices(config, settings)
    question = (
        args.question.strip()
        or f"请结合当前可获得信息，对{args.stock_name}[{args.stock_code}]做短中线分析，并给出风险提示。"
    )
    messages = [
        {
            "role": "system",
            "content": "你是一名专业股票分析助手，请基于公开信息给出结构化、审慎的分析结论，不做收益承诺。",
        },
        {"role": "user", "content": "当前时间"},
        {"role": "assistant", "content": "当前本地时间是:" + datetime.now(CN).strftime("%Y-%m-%d %H:%M:%S")},
    ]
    try:
        try:
            quote = await asyncio.to_thread(market.quote, args.stock_code)
            messages.extend(
                [
                    {"role": "user", "content": f"当前{args.stock_name}[{args.stock_code}]价格是多少？"},
                    {
                        "role": "assistant",
                        "content": f"截止到{quote['asOf']},当前{args.stock_name}[{args.stock_code}]价格是{quote['price']}",
                    },
                ]
            )
        except MarketDataError:
            messages.append(
                {"role": "user", "content": "本次实时行情未能获取，请明确说明数据限制，不编造当前价格。"}
            )
        messages.append({"role": "user", "content": question})
        json_output = getattr(args, "json", False)
        emitted = False

        def delta(text):
            nonlocal emitted
            if not text:
                return
            emitted = True
            if json_output:
                print(
                    json.dumps({"code": 1, "question": question, "content": text}, ensure_ascii=False),
                    flush=True,
                )
            else:
                print(text, end="", flush=True)

        def attempt(record):
            if json_output:
                print(
                    json.dumps({"code": 1, "type": "attempt", "attempt": record}, ensure_ascii=False),
                    flush=True,
                )

        # One selected model and one attempt prevent retry/fallback concatenation after a partial answer.
        client = AIClient([model], max_attempts=1)
        try:
            completion = await client.complete(
                messages=messages,
                phase="cli-stock-lite",
                on_delta=delta,
                on_attempt=attempt,
                request_options=options or None,
            )
        except Exception as error:
            from .audit import redact_text

            safe = redact_text(str(error))[0]
            if json_output:
                print(
                    json.dumps({"code": 0, "question": question, "content": safe}, ensure_ascii=False),
                    flush=True,
                )
            raise
        if not emitted:
            # Protocols may deliver their body only in the terminal completion frame.
            delta(completion.content)
        if not json_output:
            print(flush=True)
    finally:
        market.close()


def _table_counts(path):
    with Database(path, read_only=True).connection() as connection:
        names = [
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name NOT GLOB 'sqlite_*' ORDER BY name"
            )
        ]
        return {
            name: connection.execute("SELECT COUNT(*) FROM " + quote_identifier(name)).fetchone()[0]
            for name in names
        }


def _offline(stack, config):
    from .cli import process_lock

    stack.enter_context(process_lock(config.root / "runtime" / "web.lock"))
    if config.main_db.resolve() == config.minute_db.resolve():
        raise ValueError("main and minute database paths must differ")
    return stack


def create_archive(args, config):
    output = args.output.resolve()
    if output.suffix.lower() != ".zip":
        raise ValueError("archive output must use .zip extension")
    if not args.source_app_version.strip() or not args.source_commit.strip():
        raise ValueError("archive source application version and commit are required")
    if output.exists():
        raise FileExistsError("archive output already exists")
    temporary_root = Path("H:/Download") if Path("H:/Download").is_dir() else Path(tempfile.gettempdir())
    with ExitStack() as stack:
        _offline(stack, config)
        # Reserve writers on both files before either backup starts. Read-only backup connections can
        # still read the committed snapshots, while concurrent processes cannot change either database.
        for path in (config.main_db, config.minute_db):
            connection = sqlite3.connect(
                path.resolve().as_uri() + "?mode=rw", uri=True, isolation_level=None, timeout=0
            )
            stack.callback(connection.close)
            connection.execute("BEGIN IMMEDIATE")
            stack.callback(connection.rollback)
        staging = Path(
            stack.enter_context(tempfile.TemporaryDirectory(prefix="stock-god-archive-", dir=temporary_root))
        )
        main, minute = staging / "stock.db", staging / "minute.db"
        backups = [backup_database(config.main_db, main), backup_database(config.minute_db, minute)]
        schema = status(main, minute)
        counts = {"main": _table_counts(main), "minute": _table_counts(minute)}
        manifest = {
            "formatVersion": 1,
            "createdAt": datetime.now(UTC).isoformat(),
            "sourceAppVersion": args.source_app_version.strip(),
            "sourceCommit": args.source_commit.strip(),
            "mainSchemaVersion": schema["main"]["currentVersion"],
            "minuteSchemaVersion": schema["minute"]["currentVersion"],
            "files": [
                {"name": name, "sizeBytes": value["bytes"], "sha256": value["sha256"]}
                for name, value in zip(("stock.db", "minute.db"), backups, strict=True)
            ],
            "legacyTableRows": {name: counts["main"].get(name, 0) for name in LEGACY_TABLES},
            "tableRows": counts,
        }
        output.parent.mkdir(parents=True, exist_ok=True)
        created = False
        try:
            with output.open("xb") as handle:
                created = True
                with zipfile.ZipFile(
                    handle, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6
                ) as archive:
                    archive.write(main, "stock.db")
                    archive.write(minute, "minute.db")
                    archive.writestr(
                        "manifest.json", json.dumps(manifest, ensure_ascii=False, indent=2) + "\n"
                    )
                handle.flush()
                os.fsync(handle.fileno())
            with zipfile.ZipFile(output) as archive:
                if sorted(archive.namelist()) != ["manifest.json", "minute.db", "stock.db"]:
                    raise ValueError("archive entries do not match the expected manifest")
                if json.loads(archive.read("manifest.json")) != manifest:
                    raise ValueError("archive manifest verification failed")
                for item in manifest["files"]:
                    with archive.open(item["name"]) as entry:
                        digest = hashlib.sha256()
                        for block in iter(lambda: entry.read(1024 * 1024), b""):
                            digest.update(block)
                        if digest.hexdigest() != item["sha256"]:
                            raise ValueError("database archive checksum mismatch")
                    if archive.getinfo(item["name"]).file_size != item["sizeBytes"]:
                        raise ValueError("database archive size mismatch")
        except BaseException:
            if created:
                output.unlink(missing_ok=True)
            raise
    return {
        **schema,
        "archive": {"path": str(output), "sha256": file_sha256(output), "sizeBytes": output.stat().st_size},
    }


def compact(config):
    with ExitStack() as stack:
        _offline(stack, config)
        if not config.main_db.is_file():
            raise FileNotFoundError(config.main_db)
        connection = sqlite3.connect(
            config.main_db.resolve().as_uri() + "?mode=rw", uri=True, isolation_level=None, timeout=0
        )
        stack.callback(connection.close)
        checkpoint = connection.execute("PRAGMA wal_checkpoint(TRUNCATE)").fetchone()
        if checkpoint[0]:
            raise RuntimeError(
                "database still has active readers or writers; compact requires offline access"
            )
        connection.execute("VACUUM")
        connection.execute("PRAGMA optimize")
        if [row[0] for row in connection.execute("PRAGMA quick_check")] != ["ok"]:
            raise ValueError("SQLite compact integrity check failed")
    return {
        **status(config.main_db, config.minute_db),
        "compact": {"database": "main", "quickCheck": "ok"},
        "minuteIntegrity": verify_database(config.minute_db),
    }


def execute(args, config: AppConfig):
    if args.command == "db":
        if args.operation == "archive":
            return create_archive(args, config)
        if args.operation == "compact":
            return compact(config)
        raise ValueError("unsupported database command")
    if args.command == "release":
        from .app import identity

        value = identity(config)
        return {
            "manifest": {
                key: value[key] for key in ("appVersion", "mainSchemaVersion", "minuteSchemaVersion")
            },
            "build": {key: value[key] for key in ("commit", "buildTime", "artifactSHA256", "dirty")},
        }
    explicit_ai = args.command == "ai" and all(
        getattr(args, name, "").strip() for name in ("base_url", "api_key", "model")
    )
    snapshot = None
    if config.main_db.is_file():
        try:
            snapshot = _snapshot(config)
        except (ValueError, sqlite3.Error):
            if not explicit_ai:
                raise
    if snapshot is None and not explicit_ai:
        raise ValueError("database is unavailable; migrate it explicitly or provide all AI model flags")
    if args.command == "ai":
        if not args.stock_name.strip() or not args.stock_code.strip():
            raise ValueError("stock-code and stock-name must not be empty")
        asyncio.run(_ai(args, config, snapshot))
        return None
    assert snapshot is not None
    settings = deepcopy(snapshot.config)
    if args.command == "search" and args.qgqp_b_id:
        settings["qgqpBId"] = args.qgqp_b_id.strip()
    market = MarketServices(config, settings)
    try:
        if args.command == "quote":
            return market.stock_snapshot(args.code.strip())
        if args.command != "search" or not args.words.strip():
            raise ValueError("search requires nonempty --words")
        fingerprint = settings.get("qgqpBId")
        if not fingerprint:
            raise ValueError("qgqp_b_id is required; pass --qgqp-b-id or save prediction settings")
        result = market.query_stocks(
            args.words.strip(), page_size=args.page_size if args.page_size > 0 else 5000
        )
        if not isinstance(result, dict):
            raise MarketDataError("stock search returned a non-object response")
        if int(result.get("code", 0)) < 0:
            raise MarketDataError(
                "stock search failed: " + str(result.get("message", "provider rejected request"))
            )
        return result
    finally:
        market.close()


def format_result(args, result):
    if result is None:
        return None
    if getattr(args, "json", False) or args.command not in {"quote", "search"}:
        return json.dumps(result, ensure_ascii=False, indent=2, default=str)
    if args.command == "quote":
        return (
            f"[{result['股票名称']}] {result['股票代码']}\n最新价: {result['当前价格']}\n"
            f"涨跌幅: {result.get('changePercent', 0):.2f}%\n涨跌额: {result.get('changePrice', 0):.4f}\n"
            f"开盘: {result['今日开盘价']}  最高: {result['今日最高价']}  最低: {result['今日最低价']}\n"
            f"成交量: {result['成交的股票数']}  成交额: {result['成交金额']}\n时间: {result['日期']} {result['时间']}"
        )
    data = result.get("data") or {}
    rows = next(
        (
            data[key]
            for key in ("list", "items", "data", "records", "result")
            if isinstance(data.get(key), list)
        ),
        [],
    )
    lines = [
        f"code: {result.get('code', 0)}",
        f"message: {result.get('message', '')}",
        f"rows: {len(rows)} (showing {min(20, len(rows))})",
    ]
    for index, row in enumerate(rows[:20], 1):
        code = next(
            (str(row[key]) for key in ("code", "stockCode", "securityCode", "symbol", "f12") if row.get(key)),
            "",
        )
        name = next(
            (str(row[key]) for key in ("name", "stockName", "securityName", "f14") if row.get(key)), ""
        )
        lines.append(f"{index}. {code} {name}")
    return "\n".join(lines)
