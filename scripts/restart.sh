#!/bin/bash
# go-stock Web 服务管理脚本

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOG_DIR="$PROJECT_ROOT/logs"
PID_FILE="$LOG_DIR/web-mode.nohup.pid"
PORT=34115
ADDR="127.0.0.1:$PORT"

mkdir -p "$LOG_DIR"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_DIR/monitor.log"
}

usage() {
    cat <<EOF
用法:
  bash scripts/restart.sh restart   # 只重启已有二进制，不编译
  bash scripts/restart.sh rebuild   # 重新构建前端和 Go 二进制后重启
  bash scripts/restart.sh start     # 只启动已有二进制，不编译
  bash scripts/restart.sh stop      # 停止当前 Web 服务
EOF
}

ensure_binary() {
    if [ ! -x "$PROJECT_ROOT/go-stock" ]; then
        log "未找到可执行二进制: $PROJECT_ROOT/go-stock"
        log "请先执行: bash scripts/restart.sh rebuild"
        exit 1
    fi
}

kill_old_process() {
    if [ -f "$PID_FILE" ]; then
        OLD_PID="$(tr -d '[:space:]' < "$PID_FILE" 2>/dev/null || true)"
        if [ -n "$OLD_PID" ] && ps -p "$OLD_PID" > /dev/null 2>&1; then
            log "杀死旧进程 PID: $OLD_PID"
            kill -9 "$OLD_PID" 2>/dev/null || true
        fi
        rm -f "$PID_FILE"
    fi

    PORT_PIDS="$(lsof -ti:"$PORT" 2>/dev/null || true)"
    if [ -n "$PORT_PIDS" ]; then
        log "清理占用端口 $PORT 的进程: $PORT_PIDS"
        kill -9 $PORT_PIDS 2>/dev/null || true
    fi
}

build_service() {
    cd "$PROJECT_ROOT" || exit 1
    log "构建前端静态资源..."
    (cd frontend && npm run build)
    if [ $? -ne 0 ]; then
        log "前端构建失败，停止重启"
        exit 1
    fi

    log "编译 go-stock 二进制..."
    go build -o go-stock .
    if [ $? -ne 0 ]; then
        log "Go 编译失败，停止重启"
        exit 1
    fi
}

start_existing() {
    ensure_binary
    start_service
}

restart_existing() {
    log "重启服务（不编译）..."
    ensure_binary
    kill_old_process
    start_service
}

rebuild_and_restart() {
    log "重新构建并重启服务..."
    build_service
    kill_old_process
    start_service
}

stop_service() {
    log "停止服务..."
    kill_old_process
    log "服务已停止"
}

start_service() {
    cd "$PROJECT_ROOT" || exit 1
    log "启动服务..."
    setsid nohup ./go-stock --web --web-addr="$ADDR" \
        > "$LOG_DIR/web-mode.out" 2> "$LOG_DIR/web-mode.err" < /dev/null &

    NEW_PID=$!
    echo "$NEW_PID" > "$PID_FILE"
    log "服务已启动，PID: $NEW_PID"

    for i in $(seq 1 60); do
        if curl -fsS "http://$ADDR/healthz" >/dev/null 2>&1; then
            log "服务健康检查通过"
            return 0
        fi
        if ! ps -p "$NEW_PID" >/dev/null 2>&1; then
            log "服务进程已退出，请查看 $LOG_DIR/web-mode.out 和 $LOG_DIR/web-mode.err"
            return 1
        fi
        sleep 1
    done

    log "服务启动超时，请查看 $LOG_DIR/web-mode.out 和 $LOG_DIR/web-mode.err"
    return 1
}

case "${1:-restart}" in
    restart)
        restart_existing
        ;;
    rebuild)
        rebuild_and_restart
        ;;
    start)
        log "启动服务（不编译）..."
        start_existing
        ;;
    stop)
        stop_service
        ;;
    help|-h|--help)
        usage
        ;;
    *)
        usage
        exit 1
        ;;
esac
