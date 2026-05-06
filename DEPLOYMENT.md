# Go-Stock 服务部署指南

## 快速部署

### 1. 安装 systemd 服务（推荐）

```bash
# 复制服务文件
sudo cp scripts/go-stock-monitor.service /etc/systemd/system/

# 重载 systemd
sudo systemctl daemon-reload

# 启用开机自启
sudo systemctl enable go-stock-monitor

# 启动服务
sudo systemctl start go-stock-monitor

# 查看状态
sudo systemctl status go-stock-monitor

# 查看日志
sudo journalctl -u go-stock-monitor -f
```

### 2. 手动启动监控

```bash
# 前台运行（用于测试）
./scripts/monitor-and-restart.sh monitor

# 后台运行
nohup ./scripts/monitor-and-restart.sh monitor > logs/monitor-daemon.log 2>&1 &
```

## 服务管理命令

```bash
# 启动
sudo systemctl start go-stock-monitor

# 停止
sudo systemctl stop go-stock-monitor

# 重启
sudo systemctl restart go-stock-monitor

# 查看状态
sudo systemctl status go-stock-monitor

# 查看实时日志
tail -f logs/monitor.log
tail -f logs/web-mode.out
tail -f logs/web-mode.err
```

## 监控配置

编辑 `scripts/monitor-and-restart.sh` 可调整：

- `CHECK_INTERVAL=60` - 健康检查间隔（秒）
- `MAX_FAILURES=3` - 触发重启的最大失败次数
- `HEALTH_URL` - 健康检查端点

## 故障排查

### 服务无法启动

```bash
# 检查端口占用
sudo lsof -i:34115

# 手动清理
sudo kill -9 $(lsof -ti:34115)

# 查看错误日志
tail -100 logs/web-mode.err
```

### 频繁重启

```bash
# 查看崩溃信息
cat logs/crash-info.log

# 查看监控日志
tail -100 logs/monitor.log

# 检查系统资源
top
free -h
df -h
```

## 性能优化建议

1. **数据库优化**
   - 定期清理旧数据
   - 添加索引
   - 设置合理的 `GO_STOCK_DB_BUSY_TIMEOUT_MS`

2. **并发控制**
   - 调整 semaphore 大小（当前为 6）
   - 增加超时时间（当前为 30 秒）

3. **网络优化**
   - 配置代理避免 TLS 超时
   - 增加 API 调用重试次数
   - 使用本地缓存

## 监控指标

监控脚本会记录：
- 健康检查状态
- 重启次数和时间
- 崩溃进程信息
- 端口占用情况

所有日志位于 `logs/` 目录：
- `monitor.log` - 监控主日志
- `monitor-daemon.log` - systemd 服务日志
- `crash-info.log` - 崩溃详情
- `web-mode.out` - 应用标准输出
- `web-mode.err` - 应用错误输出
