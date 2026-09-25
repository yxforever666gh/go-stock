# 蝶梦 MCP 验证记录

本页保留此前44项蝶梦接口专项验证的历史结果。当前服务已扩展为51个工具，新增本地数据验证见[本地数据接入验证](本地数据接入验证.md)。

验证日期：2026-09-12T02:50:31+08:00

## 结论

- 已包装44个蝶梦工具，加上2个统一分钟工具，共46个。实际上游抽样：已验证12项，权限受限0项，尚未实测32项，调用失败0项。
- 本地模拟测试覆盖44个接口的路由、方法、鉴权头、参数传递、特殊约束、分页及响应；通过 fast 验证。
- 本机与 Cloudflare 公网 MCP 均完成初始化和46工具发现；确认仅全市场下载工具标记为文件写入。
- get_bar 默认返回本地中国移动2026-08-12 13:45；11个数值字段与CSV一致。显式 source=diemeng 成功，来源/复权口径、成交量手乘100转股和其他基础数值与蝶梦原始响应一致。
- 全市场下载仅用模拟GZIP、压缩错误信封及损坏文件测试，未消耗真实下载次数。
- ChatGPT账号内的添加、工具扫描及下载工具权限尚未实测；公网协议通过不等于账号接入完成。

## 实测范围

样例使用中国移动600941.SH、2026-08-12、2025年年报、上证指数、沪深300权重及板块资料；每个支持分页的接口取page=0、page_size=1。日历仅取一个日期。成功空结果也计作请求成功，不表示上游一定有目标日期数据。

| 工具 | 状态 | 说明 |
| --- | --- | --- |
| `diemeng_basic_calendar` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_stock_financial_indicator` | 尚未实测 |  |
| `diemeng_stock_income` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_stock_balancesheet` | 尚未实测 |  |
| `diemeng_stock_cashflow` | 尚未实测 |  |
| `diemeng_stock_finance` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_stock_forecast` | 尚未实测 |  |
| `diemeng_stock_list` | 尚未实测 |  |
| `diemeng_stock_daily` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_stock_market_distribution_history` | 尚未实测 |  |
| `diemeng_stock_kline` | 尚未实测 |  |
| `diemeng_stock_kline_adj` | 尚未实测 |  |
| `diemeng_stock_daily_adj` | 尚未实测 |  |
| `diemeng_stock_min_adj` | 尚未实测 |  |
| `diemeng_stock_adj_factor` | 尚未实测 |  |
| `diemeng_stock_adj_factor_changes` | 尚未实测 |  |
| `diemeng_stock_history` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_stock_daily_dump` | 尚未实测 | 仅模拟验证，未消耗真实下载次数 |
| `diemeng_stock_suspension` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_stock_st_info` | 尚未实测 |  |
| `diemeng_stock_macd` | 尚未实测 |  |
| `diemeng_stock_kdj` | 尚未实测 |  |
| `diemeng_stock_rsi` | 尚未实测 |  |
| `diemeng_stock_boll` | 尚未实测 |  |
| `diemeng_stock_ma` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_stock_mavol` | 尚未实测 |  |
| `diemeng_index_history` | 尚未实测 |  |
| `diemeng_index_daily` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_index_weight` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_index_macd` | 尚未实测 |  |
| `diemeng_index_kdj` | 尚未实测 |  |
| `diemeng_index_rsi` | 尚未实测 |  |
| `diemeng_index_boll` | 尚未实测 |  |
| `diemeng_index_ma` | 尚未实测 |  |
| `diemeng_tdx_blocks` | 尚未实测 |  |
| `diemeng_tdx_block_stocks` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_tdx_daily` | 尚未实测 |  |
| `diemeng_tdx_minute` | 尚未实测 |  |
| `diemeng_dc_blocks` | 尚未实测 |  |
| `diemeng_dc_block_stocks` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_dc_daily` | 尚未实测 |  |
| `diemeng_index_ths_sector_categories` | 尚未实测 |  |
| `diemeng_index_ths_constituent_stocks` | 已验证 | 单页请求成功（空结果也表示调用成功，不保证该日期存在记录） |
| `diemeng_index_ths_daily` | 尚未实测 |  |

## 连接与配置

当前公网地址从 `H:\Download\go-stock-minute-api\mcp-url.txt` 获取。每次电脑/隧道重启后都要重新获取并填写到ChatGPT，旧连接不会自动更新。
旧go-stock设置中的域名在首次探测时连续连接失败；独立MCP私有配置已修正为权限文档提供的站点，随后12项抽样均成功。原go-stock设置未改动，Key未输出到测试日志或文档。

## 复验命令

```powershell
pwsh -NoProfile -Command "& ./scripts/verify.ps1 -Tier fast -GoPackage @('./internal/diemeng','./cmd/minute-api')"

$env:GO_STOCK_DIEMENG_LIVE = '1'
go test -tags integration ./internal/diemeng -run '^TestLiveSamples$' -count=1 -v

$env:GO_STOCK_MCP_LIVE = '1'
go test -tags integration ./internal/diemeng -run '^TestPublicMCP$' -count=1 -v
```

联网命令仅在明确需要时运行，不属于fast/domain/release。不要自动反复探测权限失败或进行全市场下载。
