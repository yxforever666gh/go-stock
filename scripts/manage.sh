#!/bin/bash
# Go-Stock 快速操作脚本

show_menu() {
    echo "================================"
    echo "   Go-Stock 服务管理"
    echo "================================"
    echo "1. 启动服务"
    echo "2. 停止服务"
    echo "3. 重启服务"
    echo "4. 查看状态"
    echo "5. 查看日志"
    echo "6. 安装 systemd 服务"
    echo "7. 卸载 systemd 服务"
    echo "8. 清理端口占用"
    echo "9. 健康检查"
    echo "0. 退出"
    echo "================================"
}

check_health() {
    echo "正在检查服务健康状态..."
    if curl -sf --max-time 5 http://127.0.0.1:34115/readyz > /dev/null 2>&1; then
        echo "✅ 服务运行正常"
        curl -s http://127.0.0.1:34115/readyz | jq . 2>/dev/null || curl -s http://127.0.0.1:34115/readyz
    else
        echo "❌ 服务未响应"
    fi
}

view_logs() {
    echo "选择要查看的日志："
    echo "1. 监控日志 (monitor.log)"
    echo "2. 应用输出 (web-mode.out)"
    echo "3. 应用错误 (web-mode.err)"
    echo "4. 崩溃信息 (crash-info.log)"
    echo "5. systemd 日志"
    read -p "请选择 [1-5]: " log_choice

    case $log_choice in
        1) tail -f logs/monitor.log ;;
        2) tail -f logs/web-mode.out ;;
        3) tail -f logs/web-mode.err ;;
        4) tail -f logs/crash-info.log 2>/dev/null || echo "暂无崩溃记录" ;;
        5) sudo journalctl -u go-stock-monitor -f ;;
        *) echo "无效选择" ;;
    esac
}

install_systemd() {
    echo "正在安装 systemd 服务..."
    sudo cp scripts/go-stock-monitor.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable go-stock-monitor
    echo "✅ systemd 服务已安装并设置为开机自启"
    echo "使用 'sudo systemctl start go-stock-monitor' 启动服务"
}

uninstall_systemd() {
    echo "正在卸载 systemd 服务..."
    sudo systemctl stop go-stock-monitor 2>/dev/null
    sudo systemctl disable go-stock-monitor 2>/dev/null
    sudo rm -f /etc/systemd/system/go-stock-monitor.service
    sudo systemctl daemon-reload
    echo "✅ systemd 服务已卸载"
}

clean_port() {
    echo "正在清理端口 34115..."
    PORT_PID=$(lsof -ti:34115 2>/dev/null || true)
    if [ -n "$PORT_PID" ]; then
        echo "发现占用进程: $PORT_PID"
        sudo kill -9 $PORT_PID 2>/dev/null || true
        sleep 1
        echo "✅ 端口已清理"
    else
        echo "✅ 端口未被占用"
    fi
}

view_status() {
    echo "================================"
    echo "   服务状态"
    echo "================================"

    # 检查进程
    if pgrep -f "go-stock.*--web" > /dev/null; then
        echo "进程状态: ✅ 运行中"
        echo "PID: $(pgrep -f 'go-stock.*--web')"
    else
        echo "进程状态: ❌ 未运行"
    fi

    # 检查端口
    if lsof -i:34115 > /dev/null 2>&1; then
        echo "端口状态: ✅ 34115 已监听"
    else
        echo "端口状态: ❌ 34115 未监听"
    fi

    # 健康检查
    check_health

    # systemd 状态
    if systemctl is-active go-stock-monitor > /dev/null 2>&1; then
        echo "systemd: ✅ 服务运行中"
    else
        echo "systemd: ⚠️  服务未运行或未安装"
    fi
}

cd "$(dirname "$0")/.." || exit 1

while true; do
    show_menu
    read -p "请选择操作 [0-9]: " choice

    case $choice in
        1)
            ./scripts/monitor-and-restart.sh start
            ;;
        2)
            ./scripts/monitor-and-restart.sh stop
            ;;
        3)
            ./scripts/monitor-and-restart.sh restart
            ;;
        4)
            view_status
            ;;
        5)
            view_logs
            ;;
        6)
            install_systemd
            ;;
        7)
            uninstall_systemd
            ;;
        8)
            clean_port
            ;;
        9)
            check_health
            ;;
        0)
            echo "再见！"
            exit 0
            ;;
        *)
            echo "无效选择，请重试"
            ;;
    esac

    echo ""
    read -p "按回车键继续..."
done
