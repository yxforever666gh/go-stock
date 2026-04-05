#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

echo "[go-stock] building linux app..."
export NODE_OPTIONS="${NODE_OPTIONS:---max-old-space-size=4096}"
wails build --clean --platform linux/amd64

echo "[go-stock] build finished."
