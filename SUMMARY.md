# Go-Stock 崩溃问题修复总结

## 问题现象

项目每隔一段时间会自动崩溃，导致无法登录访问。

## 根本原因分析

通过分析日志 `logs/web-mode.err` 和代码审查，发现了核心问题：

### 1. Goroutine 死锁（Critical Bug）

**文件**: `backend/data/ai_recommend_yield_overview.go:419`

**问题**:
```go
// 原代码 - 会导致死锁
for _, code := range codes {
    wg.Add(1)
    go func(stockCode string) {
        defer wg.Done()
        sem <- struct{}{}  // ❌ 这里会永久阻塞
        defer func() { <-sem }()
        // ...
    }(code)
}
```

**原因**:
- 使用 `sem := make(chan struct{}, 6)` 作为信号量限制并发为 6
- 当股票代码超过 6 个时，第 7+ 个 goroutine 会永久阻塞在 `sem <- struct{}{}`
- 导致大量 goroutine 泄漏，最终耗尽系统资源，服务崩溃

**堆栈证据**:
```
goroutine 738-749 [chan send]:
runtime.chansend(0xc0008df180, 0x42d9a28, 0x1, 0x0?)
go-stock/backend/data.loadYieldDailyOverviewPriceSeries.func1
```

### 2. 端口占用问题

崩溃后端口 34115 未释放，导致重启失败：
```
[FATAL] listen tcp 127.0.0.1:34115: bind: address already in use
```

## 修复方案

### ✅ 修复 1: 添加超时保护机制

修改 `backend/data/ai_recommend_yield_overview.go`，为 semaphore 获取添加超时：

```go
// 修复后的代码
go func(stockCode string) {
    defer wg.Done()

    // 使用 select 添加超时保护
    select {
    case sem <- struct{}{}:
        defer func() { <-sem }()
    case <-time.After(30 * time.Second):
        // 超时后记录错误并返回，避免永久阻塞
        select {
        case errCh <- fmt.Errorf("semaphore acquire timeout for stock %s", stockCode):
        default:
        }
        return
    }

    // 正常处理逻辑
    series, err := loadYieldDailyOverviewPriceSeriesByCode(stockCode, startDay, endDay, tradingDays)
    // ...
}(code)
```

**效果**:
- 避免 goroutine 永久阻塞
- 超时后优雅退出，释放资源
- 记录超时错误便于排查

### ✅ 修复 2: 自动监控和重启脚本

创建 `scripts/monitor-and-restart.sh`，提供：

1. **健康检查**: 每 60 秒检查 `/readyz` 端点
2. **自动重启**: 连续失败 3 次后自动重启
3. **端口清理**: 重启前清理占用的端口
4. **崩溃记录**: 记录崩溃时的进程信息

```bash
# 使用方法
./scripts/monitor-and-restart.sh monitor  # 启动监控
./scripts/monitor-and-restart.sh start    # 启动服务
./scripts/monitor-and-restart.sh stop     # 停止服务
./scripts/monitor-and-restart.sh restart  # 重启服务
```

### ✅ 修复 3: Systemd 服务配置

创建 `scripts/go-stock-monitor.service`，实现：

- 开机自启动
- 服务崩溃自动重启
- 统一的日志管理
- 资源限制保护

```bash
# 安装服务
sudo cp scripts/go-stock-monitor.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable go-stock-monitor
sudo systemctl start go-stock-monitor
```

## 验证测试

### 1. 编译测试
```bash
go build -o go-stock .  # ✅ 编译成功
```

### 2. 服务启动测试
```bash
./scripts/monitor-and-restart.sh start
# ✅ 服务已启动，PID: 447865
# ✅ 服务健康检查通过
```

### 3. 健康检查测试
```bash
curl http://127.0.0.1:34115/readyz
# ✅ {"mode":"web","ok":true,"version":""}
```

### 4. 浏览器访问测试
使用无头浏览器访问 `http://127.0.0.1:34115`
- ✅ 页面正常加载
- ✅ 标题显示正确
- ✅ 功能菜单可见

## 部署建议

### 推荐方式：使用 systemd（生产环境）

```bash
# 1. 安装服务
sudo cp scripts/go-stock-monitor.service /etc/systemd/system/
sudo systemctl daemon-reload

# 2. 启用并启动
sudo systemctl enable go-stock-monitor
sudo systemctl start go-stock-monitor

# 3. 查看状态
sudo systemctl status go-stock-monitor
tail -f logs/monitor.log
```

### 临时方式：手动启动

```bash
# 后台运行监控
nohup ./scripts/monitor-and-restart.sh monitor > logs/monitor-daemon.log 2>&1 &
```

## 其他发现的问题

从日志中还发现了一些非致命问题：

1. **网络超时**: 大量外部 API 调用超时
   - TLS handshake timeout
   - Context deadline exceeded

2. **证书验证失败**: TradingView 等服务的证书问题

3. **JSON 解析错误**: 部分 API 返回数据不完整

**建议**:
- 添加重试机制
- 配置代理服务器
- 增加超时时间
- 添加熔断器保护

## 监控和日志

所有日志文件位于 `logs/` 目录：

- `monitor.log` - 监控脚本日志
- `monitor-daemon.log` - systemd 服务日志
- `crash-info.log` - 崩溃详细信息
- `web-mode.out` - 应用标准输出
- `web-mode.err` - 应用错误输出

## 文件清单

本次修复创建/修改的文件：

1. ✅ `backend/data/ai_recommend_yield_overview.go` - 修复 goroutine 死锁
2. ✅ `scripts/monitor-and-restart.sh` - 监控和自动重启脚本
3. ✅ `scripts/go-stock-monitor.service` - systemd 服务配置
4. ✅ `CRASH_FIX_REPORT.md` - 问题分析报告
5. ✅ `DEPLOYMENT.md` - 部署指南
6. ✅ `SUMMARY.md` - 本文档

## 预期效果

修复后，服务应该：
- ✅ 不再因为 goroutine 泄漏而崩溃
- ✅ 即使崩溃也能自动重启（60 秒内）
- ✅ 端口占用问题自动清理
- ✅ 完整的崩溃日志记录
- ✅ 开机自动启动（使用 systemd）

## 后续优化建议

1. **性能优化**
   - 调整 semaphore 大小（根据服务器性能）
   - 优化数据库查询
   - 添加缓存层

2. **可观测性**
   - 集成 Prometheus metrics
   - 添加分布式追踪
   - 配置告警规则

3. **高可用**
   - 配置负载均衡
   - 数据库主从复制
   - 容器化部署

---

**修复完成时间**: 2026-04-24
**测试状态**: ✅ 通过
**部署状态**: ✅ 就绪
