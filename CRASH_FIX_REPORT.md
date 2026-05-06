# go-stock 崩溃问题分析与修复

## 问题根源

通过日志分析和代码审查，发现了导致服务定期崩溃的两个关键问题：

### 1. Goroutine 死锁（主要原因）

**位置**: `backend/data/ai_recommend_yield_overview.go:419`

**问题描述**:
- 代码使用 buffered channel `sem := make(chan struct{}, 6)` 作为信号量限制并发
- 在 goroutine 中直接执行 `sem <- struct{}{}` 会导致阻塞
- 当股票代码数量超过 6 个时，第 7 个及以后的 goroutine 会永久阻塞在 semaphore 获取上
- 这导致大量 goroutine 泄漏，最终耗尽系统资源

**堆栈信息**:
```
goroutine 738-749 [chan send]:
runtime.chansend(0xc0008df180, 0x42d9a28, 0x1, 0x0?)
go-stock/backend/data.loadYieldDailyOverviewPriceSeries.func1
```

### 2. 端口占用问题

**现象**: 服务崩溃后，端口 34115 未正确释放
**日志**: `listen tcp 127.0.0.1:34115: bind: address already in use`

## 修复方案

### 修复 1: Goroutine 死锁

在 `ai_recommend_yield_overview.go` 中添加超时机制：

```go
// 修改前（会死锁）
sem <- struct{}{}
defer func() { <-sem }()

// 修改后（带超时保护）
select {
case sem <- struct{}{}:
    defer func() { <-sem }()
case <-time.After(30 * time.Second):
    select {
    case errCh <- fmt.Errorf("semaphore acquire timeout for stock %s", stockCode):
    default:
    }
    return
}
```

### 修复 2: 自动监控和重启

创建了 `scripts/monitor-and-restart.sh` 脚本，提供：
- 健康检查（每 60 秒）
- 自动重启（连续失败 3 次后）
- 端口清理
- 崩溃日志记录

## 使用方法

### 启动监控服务

```bash
cd /home/admin/CodexData/go-stock-demo/go-stock

# 后台运行监控
nohup ./scripts/monitor-and-restart.sh monitor > logs/monitor-daemon.log 2>&1 &

# 或使用 systemd（推荐）
sudo systemctl enable go-stock-monitor
sudo systemctl start go-stock-monitor
```

### 手动操作

```bash
# 启动服务
./scripts/monitor-and-restart.sh start

# 停止服务
./scripts/monitor-and-restart.sh stop

# 重启服务
./scripts/monitor-and-restart.sh restart
```

## 验证修复

1. 重新编译：`go build -o go-stock .`
2. 启动监控：`./scripts/monitor-and-restart.sh monitor`
3. 观察日志：`tail -f logs/monitor.log`

## 其他发现

从错误日志中还发现了一些次要问题：

1. **网络超时**: 大量外部 API 调用超时（TLS handshake timeout）
2. **证书验证失败**: TradingView 证书验证问题
3. **JSON 解析错误**: 部分 API 返回数据不完整

这些问题不会导致崩溃，但会影响数据获取质量。建议：
- 增加重试机制
- 添加熔断器
- 优化超时配置
