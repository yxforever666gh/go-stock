#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

WEB_ADDR="${GO_STOCK_WEB_ADDR:-127.0.0.1:34115}"
WEB_PORT="${WEB_ADDR##*:}"
RUNTIME_DIR="${GO_STOCK_RUNTIME_DIR:-$PROJECT_ROOT/runtime}"
DB_DSN_DEFAULT="$RUNTIME_DIR/db/stock.db?cache_size=-524288&journal_mode=WAL"
DB_PATH="${GO_STOCK_DB_PATH:-$DB_DSN_DEFAULT}"
LOG_DIR="$PROJECT_ROOT/logs"
PID_FILE="$LOG_DIR/web-mode.nohup.pid"
OUT_FILE="$LOG_DIR/web-mode.out"
ERR_FILE="$LOG_DIR/web-mode.err"
START_TIMEOUT_SEC="${GO_STOCK_WEB_START_TIMEOUT_SEC:-30}"
READY_STABLE_CHECKS="${GO_STOCK_WEB_READY_STABLE_CHECKS:-3}"

usage() {
  cat <<EOF
Usage:
  bash scripts/restart.sh <command>

Commands:
  start    Start web mode
  stop     Stop web mode
  restart  Stop then start web mode

Env:
  GO_STOCK_WEB_ADDR             default: 127.0.0.1:34115
  GO_STOCK_WEB_FOREGROUND       set 1 to run in foreground
  GO_STOCK_SKIP_BUILD           set 1 to skip go build
  GO_STOCK_RUNTIME_DIR          default: ./runtime
  GO_STOCK_DB_PATH              custom sqlite path or DSN
  GO_STOCK_WEB_START_TIMEOUT_SEC  startup timeout seconds
EOF
}

find_listen_pid() {
  local port="$1"
  local raw
  raw="$(ss -ltnp 2>/dev/null | awk -v p=":${port}" '$4 ~ p"$" {print $NF; exit}')"
  if [[ -z "$raw" ]]; then
    return 1
  fi
  echo "$raw" | grep -o 'pid=[0-9]\+' | head -n1 | cut -d= -f2
}

cleanup_stale_pid_file() {
  if [[ ! -f "$PID_FILE" ]]; then
    return 0
  fi
  local pid
  pid="$(tr -d '[:space:]' <"$PID_FILE" 2>/dev/null || true)"
  if [[ -z "$pid" ]]; then
    rm -f "$PID_FILE"
    return 0
  fi
  if ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$PID_FILE"
  fi
}

wait_until_ready() {
  local started now pid current_pid
  started="$(date +%s)"
  while true; do
    if pid="$(find_listen_pid "$WEB_PORT")"; then
      local stable=1
      local check=1
      while (( check < READY_STABLE_CHECKS )); do
        sleep 0.5
        if ! kill -0 "$pid" 2>/dev/null; then
          stable=0
          break
        fi
        current_pid="$(find_listen_pid "$WEB_PORT" || true)"
        if [[ "$current_pid" != "$pid" ]]; then
          stable=0
          break
        fi
        ((check++))
      done
      if (( stable == 1 )); then
        echo "$pid"
        return 0
      fi
    fi

    if [[ -f "$PID_FILE" ]]; then
      local tracked_pid
      tracked_pid="$(tr -d '[:space:]' <"$PID_FILE" 2>/dev/null || true)"
      if [[ -n "$tracked_pid" ]] && ! kill -0 "$tracked_pid" 2>/dev/null; then
        echo "[go-stock] web mode exited early. recent stderr:" >&2
        tail -n 80 "$ERR_FILE" 2>/dev/null >&2 || true
        echo "[go-stock] recent stdout:" >&2
        tail -n 80 "$OUT_FILE" 2>/dev/null >&2 || true
        return 1
      fi
    fi

    now="$(date +%s)"
    if (( now - started >= START_TIMEOUT_SEC )); then
      echo "[go-stock] web mode did not become ready within ${START_TIMEOUT_SEC}s" >&2
      echo "[go-stock] recent stderr:" >&2
      tail -n 80 "$ERR_FILE" 2>/dev/null >&2 || true
      echo "[go-stock] recent stdout:" >&2
      tail -n 80 "$OUT_FILE" 2>/dev/null >&2 || true
      return 1
    fi
    sleep 0.5
  done
}

start_web() {
  mkdir -p "$LOG_DIR"
  mkdir -p "$RUNTIME_DIR/db"
  cleanup_stale_pid_file

  local pid cmdline
  if pid="$(find_listen_pid "$WEB_PORT")"; then
    cmdline="$(tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null || true)"
    if [[ "$cmdline" == *"go-stock"* && "$cmdline" == *"--web"* ]]; then
      echo "[go-stock] web mode already running on http://${WEB_ADDR} (pid=${pid})"
      return 0
    fi
    echo "[go-stock] port ${WEB_PORT} is already in use by pid=${pid}, cmd: ${cmdline}"
    echo "[go-stock] set GO_STOCK_WEB_ADDR to another address, e.g. GO_STOCK_WEB_ADDR=127.0.0.1:34116"
    return 1
  fi

  echo "[go-stock] starting web mode on http://${WEB_ADDR}"
  local proxy_mode
  proxy_mode="${GO_STOCK_PROXY_MODE:-disable}"
  if [[ "${proxy_mode}" != "inherit" ]]; then
    unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY || true
    export NO_PROXY="*"
    export no_proxy="*"
  fi

  if [[ "${GO_STOCK_SKIP_BUILD:-0}" != "1" ]]; then
    echo "[go-stock] building backend binary ..."
    mkdir -p build/bin
    go build -o build/bin/go-stock .
  fi
  if [[ ! -x "build/bin/go-stock" ]]; then
    echo "[go-stock] build/bin/go-stock not found. Either remove GO_STOCK_SKIP_BUILD=1 or build it first." >&2
    return 1
  fi

  local cmd=(./build/bin/go-stock --web)

  if [[ "${GO_STOCK_WEB_FOREGROUND:-0}" == "1" ]]; then
    exec env \
      GO_STOCK_WEB_ADDR="${WEB_ADDR}" \
      GO_STOCK_RUNTIME_DIR="${RUNTIME_DIR}" \
      GO_STOCK_DB_PATH="${DB_PATH}" \
      GO_STOCK_DB_LOG_LEVEL="${GO_STOCK_DB_LOG_LEVEL:-silent}" \
      GO_STOCK_LOG_LEVEL="${GO_STOCK_LOG_LEVEL:-error}" \
      GO_STOCK_DB_BUSY_TIMEOUT_MS="${GO_STOCK_DB_BUSY_TIMEOUT_MS:-8000}" \
      GO_STOCK_MINUTE_COVER_TRADE_DAYS="${GO_STOCK_MINUTE_COVER_TRADE_DAYS:-0}" \
      GO_STOCK_MINUTE_PROVIDER="${GO_STOCK_MINUTE_PROVIDER:-public}" \
      GO_STOCK_MINUTE_FALLBACK_AKSHARE="${GO_STOCK_MINUTE_FALLBACK_AKSHARE:-0}" \
      GO_STOCK_AKSHARE_MINUTE_SOURCE="${GO_STOCK_AKSHARE_MINUTE_SOURCE:-auto}" \
      GO_STOCK_AKSHARE_PROXY_MODE="${GO_STOCK_AKSHARE_PROXY_MODE:-disable}" \
      GO_STOCK_AKSHARE_TIMEOUT_SEC="${GO_STOCK_AKSHARE_TIMEOUT_SEC:-45}" \
      "${cmd[@]}"
  fi

  nohup setsid env \
    GO_STOCK_WEB_ADDR="${WEB_ADDR}" \
    GO_STOCK_RUNTIME_DIR="${RUNTIME_DIR}" \
    GO_STOCK_DB_PATH="${DB_PATH}" \
    GO_STOCK_DB_LOG_LEVEL="${GO_STOCK_DB_LOG_LEVEL:-silent}" \
    GO_STOCK_LOG_LEVEL="${GO_STOCK_LOG_LEVEL:-error}" \
    GO_STOCK_DB_BUSY_TIMEOUT_MS="${GO_STOCK_DB_BUSY_TIMEOUT_MS:-8000}" \
    GO_STOCK_MINUTE_COVER_TRADE_DAYS="${GO_STOCK_MINUTE_COVER_TRADE_DAYS:-0}" \
    GO_STOCK_MINUTE_PROVIDER="${GO_STOCK_MINUTE_PROVIDER:-public}" \
    GO_STOCK_MINUTE_FALLBACK_AKSHARE="${GO_STOCK_MINUTE_FALLBACK_AKSHARE:-0}" \
    GO_STOCK_AKSHARE_MINUTE_SOURCE="${GO_STOCK_AKSHARE_MINUTE_SOURCE:-auto}" \
    GO_STOCK_AKSHARE_PROXY_MODE="${GO_STOCK_AKSHARE_PROXY_MODE:-disable}" \
    GO_STOCK_AKSHARE_TIMEOUT_SEC="${GO_STOCK_AKSHARE_TIMEOUT_SEC:-45}" \
    "${cmd[@]}" </dev/null >>"$OUT_FILE" 2>>"$ERR_FILE" &

  local bg_pid
  bg_pid="$!"
  echo "$bg_pid" >"$PID_FILE"

  if pid="$(wait_until_ready)"; then
    echo "$pid" >"$PID_FILE"
    echo "[go-stock] web mode started on http://${WEB_ADDR} (pid=${pid})"
    echo "[go-stock] logs: $OUT_FILE , $ERR_FILE"
    echo "[go-stock] stop: bash scripts/restart.sh stop"
    return 0
  fi

  rm -f "$PID_FILE"
  return 1
}

stop_web() {
  exec bash scripts/webctl.sh stop
}

cmd="${1:-restart}"

case "$cmd" in
  start)
    start_web
    ;;
  stop)
    stop_web
    ;;
  restart)
    bash scripts/webctl.sh stop || true
    start_web
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    echo "[go-stock] unknown command: $cmd" >&2
    usage
    exit 2
    ;;
esac
