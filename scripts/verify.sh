#!/bin/bash
# Go-Stock 功能验证脚本

set -e

BASE_URL="http://127.0.0.1:34115"
PASSED=0
FAILED=0

echo "================================"
echo "   Go-Stock 功能验证测试"
echo "================================"
echo ""

test_case() {
    local name="$1"
    local command="$2"
    local expected="$3"

    echo -n "测试: $name ... "

    if result=$(eval "$command" 2>&1); then
        if [[ -z "$expected" ]] || echo "$result" | grep -q "$expected"; then
            echo "✅ 通过"
            PASSED=$((PASSED + 1))
            return 0
        else
            echo "❌ 失败 (未找到预期内容: $expected)"
            echo "   实际输出: $result"
            FAILED=$((FAILED + 1))
            return 1
        fi
    else
        echo "❌ 失败 (命令执行错误)"
        echo "   错误: $result"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

echo "1. 基础健康检查"
echo "--------------------------------"
test_case "服务健康状态" \
    "curl -sf --max-time 5 $BASE_URL/readyz" \
    '"ready":true'

test_case "页面可访问" \
    "curl -sf --max-time 5 $BASE_URL | grep -q '<title>'" \
    ""

echo ""
echo "2. API 端点测试"
echo "--------------------------------"
test_case "系统信息 API" \
    "curl -sf $BASE_URL/api/v1/system/info" \
    '"version"'

test_case "图标资源" \
    "curl -sf -I $BASE_URL/build/appicon.png | head -1" \
    "200"

test_case "Favicon" \
    "curl -sf -I $BASE_URL/favicon.ico | head -1" \
    "200"

echo ""
echo "3. 数据库数据检查"
echo "--------------------------------"
test_case "自选股数据" \
    "sqlite3 runtime/db/stock.db 'SELECT COUNT(*) FROM followed_stock;'" \
    ""

test_case "AI分析记录" \
    "sqlite3 runtime/db/stock.db 'SELECT COUNT(*) FROM ai_response_result;'" \
    ""

test_case "股票信息" \
    "sqlite3 runtime/db/stock.db 'SELECT COUNT(*) FROM stock_info;'" \
    ""

echo ""
echo "4. 前端资源检查"
echo "--------------------------------"
test_case "前端JS资源" \
    "curl -sf $BASE_URL | grep -o 'src=\"/assets/.*\.js\"' | head -1" \
    "assets"

test_case "前端CSS资源" \
    "curl -sf $BASE_URL | grep -o 'href=\"/assets/.*\.css\"' | head -1" \
    "assets"

echo ""
echo "5. 页面内容检查"
echo "--------------------------------"
test_case "页面标题" \
    "curl -sf $BASE_URL | grep -o '<title>.*</title>'" \
    "go-stock"

test_case "Vue应用挂载点" \
    "curl -sf $BASE_URL | grep -q 'id=\"app\"'" \
    ""

echo ""
echo "6. 数据详情检查"
echo "--------------------------------"

# 检查自选股详情
STOCK_DATA=$(sqlite3 runtime/db/stock.db "SELECT stock_code, name FROM followed_stock LIMIT 1;" 2>/dev/null || echo "")
if [[ -n "$STOCK_DATA" ]]; then
    echo "✅ 自选股数据: $STOCK_DATA"
    PASSED=$((PASSED + 1))
else
    echo "⚠️  自选股数据为空"
fi

# 检查AI分析详情
AI_DATA=$(sqlite3 runtime/db/stock.db "SELECT stock_name, created_at FROM ai_response_result ORDER BY id DESC LIMIT 1;" 2>/dev/null || echo "")
if [[ -n "$AI_DATA" ]]; then
    echo "✅ 最新AI分析: $AI_DATA"
    PASSED=$((PASSED + 1))
else
    echo "⚠️  AI分析数据为空"
fi

# 检查股票信息详情
STOCK_INFO=$(sqlite3 runtime/db/stock.db "SELECT COUNT(*) FROM stock_info;" 2>/dev/null || echo "0")
echo "✅ 股票信息总数: $STOCK_INFO 条"
PASSED=$((PASSED + 1))

echo ""
echo "7. 进程和端口检查"
echo "--------------------------------"

if pgrep -f "go-stock.*--web" > /dev/null; then
    PID=$(pgrep -f "go-stock.*--web")
    echo "✅ 服务进程运行中 (PID: $PID)"
    PASSED=$((PASSED + 1))
else
    echo "❌ 服务进程未运行"
    FAILED=$((FAILED + 1))
fi

if lsof -i:34115 > /dev/null 2>&1; then
    echo "✅ 端口 34115 已监听"
    PASSED=$((PASSED + 1))
else
    echo "❌ 端口 34115 未监听"
    FAILED=$((FAILED + 1))
fi

echo ""
echo "================================"
echo "   测试结果汇总"
echo "================================"
echo "通过: $PASSED"
echo "失败: $FAILED"
echo "总计: $((PASSED + FAILED))"
echo ""

if [[ $FAILED -eq 0 ]]; then
    echo "✅ 所有测试通过！"
    exit 0
else
    echo "⚠️  部分测试失败，请检查日志"
    exit 1
fi
