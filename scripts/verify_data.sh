#!/bin/bash
# 使用 curl 和 Node.js 进行页面数据验证

BASE_URL="http://127.0.0.1:34115"

echo "================================"
echo "   Go-Stock 页面数据验证"
echo "================================"
echo ""

# 1. 获取首页HTML
echo "1. 获取首页内容..."
HTML=$(curl -s "$BASE_URL")

if echo "$HTML" | grep -q "go-stock"; then
    echo "   ✅ 页面标题正确"
else
    echo "   ❌ 页面标题异常"
fi

if echo "$HTML" | grep -q "id=\"app\""; then
    echo "   ✅ Vue应用挂载点存在"
else
    echo "   ❌ Vue应用挂载点缺失"
fi

# 2. 检查静态资源
echo ""
echo "2. 检查静态资源..."

JS_FILES=$(echo "$HTML" | grep -o 'src="/assets/[^"]*\.js"' | wc -l)
CSS_FILES=$(echo "$HTML" | grep -o 'href="/assets/[^"]*\.css"' | wc -l)

echo "   ✅ JavaScript 文件: $JS_FILES 个"
echo "   ✅ CSS 文件: $CSS_FILES 个"

# 3. 测试 API 端点
echo ""
echo "3. 测试 API 端点..."

# 健康检查
HEALTH=$(curl -s "$BASE_URL/readyz")
if echo "$HEALTH" | grep -q '"ready":true'; then
    echo "   ✅ 健康检查通过"
else
    echo "   ❌ 健康检查失败"
fi

# 系统信息
SYSTEM_INFO=$(curl -s "$BASE_URL/api/v1/system/info")
echo "   ℹ️  系统信息 API 响应: $(echo $SYSTEM_INFO | jq -c . 2>/dev/null || echo $SYSTEM_INFO)"

# 4. 检查数据库数据
echo ""
echo "4. 检查数据库数据..."

FOLLOWED_COUNT=$(sqlite3 runtime/db/stock.db "SELECT COUNT(*) FROM followed_stock;" 2>/dev/null)
AI_COUNT=$(sqlite3 runtime/db/stock.db "SELECT COUNT(*) FROM ai_response_result;" 2>/dev/null)
STOCK_COUNT=$(sqlite3 runtime/db/stock.db "SELECT COUNT(*) FROM stock_info;" 2>/dev/null)

echo "   ✅ 自选股: $FOLLOWED_COUNT 条"
echo "   ✅ AI分析: $AI_COUNT 条"
echo "   ✅ 股票信息: $STOCK_COUNT 条"

# 5. 显示具体数据
echo ""
echo "5. 数据详情..."

echo ""
echo "   自选股列表:"
sqlite3 runtime/db/stock.db "SELECT '   - ' || stock_code || ' ' || name || ' (价格: ' || price || ')' FROM followed_stock LIMIT 5;" 2>/dev/null

echo ""
echo "   最近AI分析:"
sqlite3 runtime/db/stock.db "SELECT '   - ' || stock_name || ' (' || datetime(created_at) || ')' FROM ai_response_result ORDER BY id DESC LIMIT 3;" 2>/dev/null

# 6. 检查日志
echo ""
echo "6. 检查最近日志..."

if [ -f "logs/web-mode.err" ]; then
    ERROR_COUNT=$(tail -100 logs/web-mode.err | grep -c "ERROR\|FATAL\|panic" || echo "0")
    if [ "$ERROR_COUNT" -gt 0 ]; then
        echo "   ⚠️  发现 $ERROR_COUNT 个错误日志"
        echo "   最近错误:"
        tail -100 logs/web-mode.err | grep "ERROR\|FATAL" | tail -3 | sed 's/^/      /'
    else
        echo "   ✅ 最近无严重错误"
    fi
fi

# 7. 进程状态
echo ""
echo "7. 进程状态..."

if pgrep -x "go-stock" > /dev/null; then
    PID=$(pgrep -x "go-stock")
    UPTIME=$(ps -p $PID -o etime= | tr -d ' ')
    MEM=$(ps -p $PID -o rss= | awk '{printf "%.1f MB", $1/1024}')
    echo "   ✅ 进程运行中"
    echo "      PID: $PID"
    echo "      运行时间: $UPTIME"
    echo "      内存使用: $MEM"
else
    echo "   ❌ 进程未运行"
fi

echo ""
echo "================================"
echo "   验证完成"
echo "================================"
echo ""
echo "访问地址: $BASE_URL"
echo ""
