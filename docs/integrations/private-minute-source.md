# 私人分钟线来源接入说明

本说明用于公开仓库中的通用接入场景，不包含任何特定私有域名、私有部署地址或个人服务信息。

## 适用场景

- 当公共分钟线来源无法覆盖更长时间的历史窗口时，可以切换到“私人分钟线来源”
- 当前本地 Web 应用推荐通过“设置页 -> 分钟线数据源 -> 私人分钟线来源”填写 URL 和 API Key
- 历史环境变量 `GO_STOCK_DIEMENG_*` 仅保留兼容与迁移导入用途，不建议继续作为长期主配置入口

## 需要准备的内容

- 一个你自己可控的分钟线服务地址
- 对应的 API Key 或其他鉴权信息
- 明确该服务的超时、最小调用间隔、代理要求和支持的分钟级别

## URL 约定

- 调用 URL 请填写你自己的服务地址，例如：`https://example.com/api`
- 如果只填写站点根路径且你的服务接口实际挂在 `/api`，程序会自动补齐 `/api`
- 如果你的服务接口不在 `/api`，请直接填写完整路径，例如：`https://example.com/custom-minute-api`

## 鉴权

- 默认 Header Key 推荐使用：`apiKey`
- 兼容 Header Key：`X-API-Key`

示例：

```bash
curl -H "apiKey: <your_api_key>" https://example.com/api/stock/list
```

## 设置页建议值

- 分钟线模式：按需切到“私人分钟线来源”
- 调用 URL：填写你自己的服务地址
- API Key：填写你自己的密钥
- 超时：`60`
- 最小间隔：`1200`
- 代理模式：`disable`
- 分钟级别：`1min`

## 兼容环境变量

仅用于首轮导入或脚本部署兼容：

- `GO_STOCK_DIEMENG_API_KEY`
- `GO_STOCK_DIEMENG_API_KEY_FILE`（推荐，文件仅含一行密钥）
- `GO_STOCK_SECRETS_DIR`（配合 `GO_STOCK_DIEMENG_API_KEY=secret://diemeng_api_key`）
- `GO_STOCK_DIEMENG_BASE_URL`
- `GO_STOCK_DIEMENG_TIMEOUT_SEC`
- `GO_STOCK_DIEMENG_MIN_INTERVAL_MS`
- `GO_STOCK_DIEMENG_PROXY_MODE`
- `GO_STOCK_DIEMENG_LEVEL`

## 说明

- 这里保留 `GO_STOCK_DIEMENG_*` 只是为了兼容旧部署与历史配置迁移
- 部署环境优先使用 `*_FILE` 或 `secret://` 引用；不要把密钥写入源码、镜像或普通容器环境变量
- 当前公开仓库不默认绑定任何具体私人分钟线服务
- 如果你准备公开自己的衍生仓库，建议继续使用通用说明，不要把私有接入地址和凭据写入版本库
