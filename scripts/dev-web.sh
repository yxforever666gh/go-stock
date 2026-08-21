#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

if ! command -v go >/dev/null 2>&1; then
  echo "[go-stock] missing go in PATH"
  exit 1
fi

if ! command -v npm >/dev/null 2>&1; then
  echo "[go-stock] missing npm in PATH"
  exit 1
fi

BACK_PID=""
FRONT_PID=""

cleanup() {
  local status=$?
  if [[ -n "$FRONT_PID" ]] && kill -0 "$FRONT_PID" >/dev/null 2>&1; then
    kill "$FRONT_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$BACK_PID" ]] && kill -0 "$BACK_PID" >/dev/null 2>&1; then
    kill "$BACK_PID" >/dev/null 2>&1 || true
  fi
  wait >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

echo "[go-stock] starting backend web API on 127.0.0.1:34115 ..."
GO_STOCK_WEB_ADDR="${GO_STOCK_WEB_ADDR:-127.0.0.1:34115}" \
GO_STOCK_DB_LOG_LEVEL="${GO_STOCK_DB_LOG_LEVEL:-silent}" \
GO_STOCK_LOG_LEVEL="${GO_STOCK_LOG_LEVEL:-warn}" \
go run . &
BACK_PID=$!

sleep 1

echo "[go-stock] starting frontend dev server on http://127.0.0.1:5173 ..."
(
  cd frontend
  npm run dev
) &
FRONT_PID=$!

wait -n "$BACK_PID" "$FRONT_PID"
