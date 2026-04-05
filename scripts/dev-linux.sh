#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

echo "[go-stock] checking Linux dependencies..."

if ! command -v wails >/dev/null 2>&1; then
  echo "[go-stock] missing wails CLI. install it first:"
  echo "  go install github.com/wailsapp/wails/v2/cmd/wails@latest"
  exit 1
fi

if ! command -v pkg-config >/dev/null 2>&1; then
  echo "[go-stock] missing pkg-config. install it first:"
  echo "  sudo apt-get update && sudo apt-get install -y pkg-config"
  exit 1
fi

missing_pkgs=()
if ! pkg-config --exists gtk+-3.0; then
  missing_pkgs+=("libgtk-3-dev")
fi

if ! pkg-config --exists webkit2gtk-4.1 && ! pkg-config --exists webkit2gtk-4.0; then
  missing_pkgs+=("libwebkit2gtk-4.1-dev (or libwebkit2gtk-4.0-dev)")
fi

if [ ${#missing_pkgs[@]} -gt 0 ]; then
  echo "[go-stock] missing desktop libs: ${missing_pkgs[*]}"
  echo "[go-stock] for Ubuntu/Debian, run:"
  echo "  sudo apt-get update && sudo apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev"
  echo "  # if your distro has no 4.1 package, use libwebkit2gtk-4.0-dev"
  exit 1
fi

# wails.json pins frontend dev server URL to localhost:5173.
# Ensure this port is free to avoid URL/port mismatch at runtime.
if ss -ltn | rg -q ':5173[[:space:]]'; then
  echo "[go-stock] port 5173 is occupied, trying to clean stale go-stock dev processes..."
  pkill -f "$PROJECT_ROOT/frontend/node_modules/.bin/vite" || true
  pkill -f "wails dev" || true
  sleep 1
fi

if ss -ltn | rg -q ':5173[[:space:]]'; then
  echo "[go-stock] port 5173 is still occupied. stop the process and retry."
  echo "[go-stock] inspect with: ss -ltnp | rg ':5173'"
  exit 1
fi

echo "[go-stock] starting wails dev..."
export NODE_OPTIONS="${NODE_OPTIONS:---max-old-space-size=4096}"
export GO_STOCK_DB_LOG_LEVEL="${GO_STOCK_DB_LOG_LEVEL:-silent}"
export GO_STOCK_LOG_LEVEL="${GO_STOCK_LOG_LEVEL:-warn}"
# -skipbindings significantly reduces noisy KnownStructs/Not found logs in daily dev.
wails dev -m -s -skipbindings -v 0 -loglevel Error
