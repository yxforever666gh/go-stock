#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

WEB_ADDR="${GO_STOCK_WEB_ADDR:-127.0.0.1:34115}"
WEB_PORT="${WEB_ADDR##*:}"
LOG_DIR="$PROJECT_ROOT/logs"
PID_FILE="$LOG_DIR/web-mode.nohup.pid"

usage() {
  cat <<EOF
Usage:
  bash scripts/webctl.sh <command>

Commands:
  status   Show running pid/cmdline
  stop     Stop web server (SIGINT -> SIGTERM -> SIGKILL)
  pause    Pause web server (SIGSTOP)
  resume   Resume paused web server (SIGCONT)
  restart  Stop then start

Env:
  GO_STOCK_WEB_ADDR  default: 127.0.0.1:34115
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

find_web_pids_by_ps() {
  # Fallback: when ss doesn't expose pid info (permissions/namespace), try scanning processes.
  # Only consider the actual go-stock executable (avoid matching "bash -c ... go-stock --web ...").
  # comm is the executable name (argv[0] basename) on Linux.
  ps -eo pid=,comm=,args= 2>/dev/null | awk '
    $2 == "go-stock" && $0 ~ /--web/ {print $1}
  ' | sort -n | uniq
}

cmdline_of() {
  local pid="$1"
  local path="/proc/$pid/cmdline"
  if [[ ! -r "$path" ]]; then
    return 0
  fi
  tr '\0' ' ' <"$path" 2>/dev/null || true
}

ensure_go_stock_web() {
  local pid="$1"
  local cmdline
  cmdline="$(cmdline_of "$pid")"
  if [[ "$cmdline" != *"--web"* ]]; then
    echo "[go-stock] pid=${pid} does not look like a --web process: ${cmdline}"
    return 1
  fi
  if [[ "$cmdline" != *"go-stock"* ]]; then
    echo "[go-stock] pid=${pid} does not look like go-stock: ${cmdline}"
    return 1
  fi
  return 0
}

wait_exit() {
  local pid="$1"
  local timeout_sec="${2:-10}"
  local started
  started="$(date +%s)"
  while kill -0 "$pid" 2>/dev/null; do
    local now
    now="$(date +%s)"
    if (( now - started >= timeout_sec )); then
      return 1
    fi
    sleep 0.2
  done
  return 0
}

require_pid() {
  if [[ -f "$PID_FILE" ]]; then
    local file_pid
    file_pid="$(tr -d '[:space:]' <"$PID_FILE" 2>/dev/null || true)"
    if [[ -n "$file_pid" ]] && kill -0 "$file_pid" 2>/dev/null; then
      echo "$file_pid"
      return 0
    fi
    rm -f "$PID_FILE"
  fi

  local pid
  if pid="$(find_listen_pid "$WEB_PORT")"; then
    if [[ -n "$pid" && -r "/proc/$pid/cmdline" ]]; then
      echo "$pid"
      return 0
    fi
    # Stale pid from ss output; fall back below.
  fi

  # If port isn't listening, the server may have crashed/hung while leaving a process behind,
  # or ss can't show pid. Fall back to searching by cmdline.
  local pids
  pids="$(find_web_pids_by_ps || true)"
  if [[ -n "$pids" ]]; then
    echo "$pids" | head -n1
    return 0
  fi

  echo ""
  return 1
}

cmd="${1:-}"
if [[ -z "$cmd" ]]; then
  usage
  exit 2
fi

case "$cmd" in
  status)
    if pid="$(require_pid)"; then
      cmdline="$(cmdline_of "$pid")"
      echo "[go-stock] web listens on http://${WEB_ADDR} (pid=${pid})"
      echo "[go-stock] cmd: ${cmdline}"
      exit 0
    fi
    echo "[go-stock] not running on http://${WEB_ADDR}"
    exit 0
    ;;
  pause)
    pid="$(require_pid)"
    if [[ -z "$pid" ]]; then
      echo "[go-stock] not running on http://${WEB_ADDR}"
      exit 1
    fi
    ensure_go_stock_web "$pid"
    kill -STOP "$pid"
    echo "[go-stock] paused pid=${pid} (SIGSTOP). Note: port may still appear LISTEN but the server won't accept requests."
    ;;
  resume)
    pid="$(require_pid)"
    if [[ -z "$pid" ]]; then
      echo "[go-stock] not running on http://${WEB_ADDR}"
      exit 1
    fi
    ensure_go_stock_web "$pid"
    kill -CONT "$pid"
    echo "[go-stock] resumed pid=${pid} (SIGCONT)."
    ;;
  stop)
    pid="$(require_pid)"
    if [[ -z "$pid" ]]; then
      echo "[go-stock] not running on http://${WEB_ADDR}"
      exit 0
    fi

    # Stop all go-stock --web processes we can see, not just the listener pid.
    # This avoids "restart.sh stop 没用" when the server is hung/crashed and not listening anymore.
    pids="$(find_web_pids_by_ps || true)"
    if [[ -z "$pids" ]]; then
      pids="$pid"
    fi

    failed=0
    while IFS= read -r one; do
      [[ -z "$one" ]] && continue
      if ! ensure_go_stock_web "$one"; then
        continue
      fi

      echo "[go-stock] stopping pid=${one} ..."
      kill -INT "$one" 2>/dev/null || true
      if wait_exit "$one" 8; then
        echo "[go-stock] stopped pid=${one} (SIGINT)."
        continue
      fi

      kill -TERM "$one" 2>/dev/null || true
      if wait_exit "$one" 6; then
        echo "[go-stock] stopped pid=${one} (SIGTERM)."
        continue
      fi

      kill -KILL "$one" 2>/dev/null || true
      if wait_exit "$one" 3; then
        echo "[go-stock] killed pid=${one} (SIGKILL)."
        continue
      fi

      echo "[go-stock] failed to stop pid=${one}."
      failed=1
    done <<< "$pids"

    rm -f "$PID_FILE"

    exit "$failed"
    ;;
  restart)
    bash scripts/webctl.sh stop || true
    exec bash scripts/restart.sh start
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    echo "[go-stock] unknown command: $cmd"
    usage
    exit 2
    ;;
esac
