# Stock God 安全问题反馈

通过 [项目 Issues](https://github.com/yxforever666gh/stock-god/issues) 联系维护者，先说明影响范围和可复现条件。涉及 API Key、SMTP 凭据、个人数据或访问地址时，仅提供脱敏信息；不要公开原数据库或完整请求日志。

主 Web 服务只监听本机回环地址，并校验来源。MCP 临时隧道是用户显式启动的独立入口，地址持有者可以调用其接口。密钥置于本机设置或仓库外配置文件，不能提交到 Git。

使用版本由 `/readyz` 和关于页确认。发布与回滚按 [运行说明](docs/operations.md) 执行；历史制品保留用于恢复，不自动重新启用已退役功能。
