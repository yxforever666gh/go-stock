# 蝶梦 MCP 全接口说明

本 MCP 保留本地 `get_bar`、`get_bars`，下列44项蝶梦能力保持不变；另有指数和集合竞价5项本地工具，共51个工具。普通接口只读；`diemeng_stock_daily_dump` 会在本机保存下载文件，已标记文件写入行为。本文接口定义来自仓库中的账号权限文档；实际权限以联网验证及上游返回为准。

## 重启及连接

电脑或隧道重启后，先打开本机 7890 代理，再运行 `scripts/start-minute-api.ps1`。等待连接成功后，从 `H:\Download\go-stock-minute-api\mcp-url.txt` 复制本次完整 `/mcp` 地址，重新填写到 ChatGPT（认证选 None）；旧地址不会自动更新。更新工具定义后也需要重新扫描工具。

[完整启动、停止和重启步骤](分钟数据API说明.md)。当前没有替用户完成 ChatGPT 账号内的添加和扫描；账号是否允许文件下载工具须在实际接入时确认。

## URL 和 Key 的本机配置

配置文件：`C:\Users\yxforever\.codex\secrets\diemeng\minute-api.json`。第一次已从现有 go-stock 设置只读导入；此后程序只读取独立配置，不再访问运行数据库。目录权限限制为当前用户和 SYSTEM。不要把该文件复制到仓库或上传到 ChatGPT。

文件格式（以下仅占位符）：

```json
{
  "base_url": "https://你的蝶梦站点/api",
  "api_key": "你的蝶梦Key"
}
```

需要更换 URL 或 Key 时，只在本机编辑此文件，再停止并重新启动脚本。新配置在启动时读取为快照，运行中不自动重载。仅填写站点根地址时会补 `/api`；带其他路径的 base_url 原样保留。独立运行程序可通过 `-diemeng-config` 指定其他配置文件。配置文件不存在时保留 CSV 查询；蝶梦工具会明确报告未配置。

Key 仅作为蝶梦请求头发送，不进入工具参数或正常/错误结果，不随 Cloudflare 临时域名改变。MCP 入口仍无鉴权：知道地址的人可以调用工具，并消耗你的蝶梦额度。

## 本地优先和分钟单位

`get_bar`、`get_bars` 支持 `source`：`auto`（默认）、`local`、`diemeng`。本地1分钟查询同时读取2000—2025历史包及当前2026目录，跨年合并排序，同一时刻以当前目录优先；其他周期只读当前目录。auto 在全部适用本地目录仍缺文件或无记录时调用蝶梦；本地已有部分结果时直接返回本地结果，不与蝶梦拼接、不推断交易分钟缺失；CSV 损坏明确报错。需要完整蝶梦区间时指定 `source=diemeng`。未配置蝶梦时 auto 保留本地行为，source=diemeng 明确报错。

统一分钟结果带 `source`（csv/diemeng）和 `adjustment`（CSV 为 unknown，蝶梦历史分时为 none）。蝶梦 `/stock/history` 的 vol 明确为“手”，转换为 volume（股，乘100）；涨跌额、涨跌幅、换手率、流通股本和总股本未提供时为 null，不填0。amount 保留原值：蝶梦文档未注明该字段单位，不声称已换算为元。其他独立工具保留原字段及原单位，响应附字段说明，不将财报里的字符串或 null 强制转换成数字。

统一分钟查询只请求上游一页；若上游报告仍有后续页，则明确要求缩小时间范围或使用 `diemeng_stock_history` 的 page 参数继续查询，不把截断结果冒充完整区间。

## 调用、分页和下载

每个普通工具请求仅访问其固定接口，不接受任意 URL、请求头或 Key 参数。GET 使用查询参数，POST 使用 JSON。省略可选参数时由上游应用其文档默认值；不同接口的最大分页值按各自文档校验。返回 source、endpoint、response（原业务信封）和 fields（字段含义）。page 从0开始；需要下一页时显式传 page+1，不自动批量回补。

蝶梦默认直连，不继承系统代理；Cloudflare 仍走7890。请求超时60秒，同一进程两次请求至少间隔1200毫秒，取消可中断等待。不自动重试鉴权、权限、额度或其他失败；HTTP/业务错误返回 MCP 工具错误。单页JSON超过64MiB明确报错，要求缩小页大小或时间范围，不静默裁剪。

`diemeng_stock_daily_dump` 仅在明确要求全市场下载时调用，不能用作普通查询的自动回退。压缩文件保存到 `H:\Download\go-stock-diemeng`，返回路径、字节数、SHA256及日期/周期摘要；失败文件清理，不展开整市场数据到对话。文档限制同一日期一天最多10次，超出后该日期禁用3天；不做自动重试。返回的是本机文件路径，不是公网下载地址。

## 全部工具目录

| 工具 | 用途 | 方法 |
| --- | --- | --- |
| `diemeng_basic_calendar` | 获取交易日历 | GET |
| `diemeng_stock_financial_indicator` | 获取财务指标报表数据 | POST |
| `diemeng_stock_income` | 获取利润表数据 | POST |
| `diemeng_stock_balancesheet` | 获取资产负债表数据 | POST |
| `diemeng_stock_cashflow` | 获取现金流量表数据 | POST |
| `diemeng_stock_finance` | 获取每日财务数据 | POST |
| `diemeng_stock_forecast` | 获取财报预告 | POST |
| `diemeng_stock_list` | 获取股票列表 | GET |
| `diemeng_stock_daily` | 获取日K线数据 | POST |
| `diemeng_stock_market_distribution_history` | 获取历史市场涨跌分布 | POST |
| `diemeng_stock_kline` | 获取周期K线数据 | POST |
| `diemeng_stock_kline_adj` | 获取周期K线数据(复权) | POST |
| `diemeng_stock_daily_adj` | 获取日K线数据(复权) | POST |
| `diemeng_stock_min_adj` | 获取分钟K线数据(复权) | POST |
| `diemeng_stock_adj_factor` | 获取复权因子(涨跌幅算法) | POST |
| `diemeng_stock_adj_factor_changes` | 复权因子变更查询 | POST |
| `diemeng_stock_history` | 获取历史分时（未复权） | POST |
| `diemeng_stock_daily_dump` | 下载全市场当天全部分时 | POST |
| `diemeng_stock_suspension` | 获取股票停牌信息 | GET |
| `diemeng_stock_st_info` | 获取ST信息 | POST |
| `diemeng_stock_macd` | 获取MACD指标 | POST |
| `diemeng_stock_kdj` | 获取KDJ指标 | POST |
| `diemeng_stock_rsi` | 获取RSI指标 | POST |
| `diemeng_stock_boll` | 获取BOLL指标 | POST |
| `diemeng_stock_ma` | 获取均线指标 | POST |
| `diemeng_stock_mavol` | 获取MAVOL指标 | POST |
| `diemeng_index_history` | 指数历史分时 | POST |
| `diemeng_index_daily` | 指数历史日K | POST |
| `diemeng_index_weight` | 获取指数成分和权重 | POST |
| `diemeng_index_macd` | 获取MACD指标 | POST |
| `diemeng_index_kdj` | 获取KDJ指标 | POST |
| `diemeng_index_rsi` | 获取RSI指标 | POST |
| `diemeng_index_boll` | 获取BOLL指标 | POST |
| `diemeng_index_ma` | 获取均线指标 | POST |
| `diemeng_tdx_blocks` | 获取 tdx 板块列表 | GET |
| `diemeng_tdx_block_stocks` | 获取 tdx 板块成分股 | GET |
| `diemeng_tdx_daily` | 获取 tdx 板块日K | GET |
| `diemeng_tdx_minute` | 获取 tdx 板块分钟K线 | POST |
| `diemeng_dc_blocks` | 获取 dc 板块列表 | POST |
| `diemeng_dc_block_stocks` | 获取 dc 板块成分股 | POST |
| `diemeng_dc_daily` | 获取 dc 板块日K | POST |
| `diemeng_index_ths_sector_categories` | 获取 ths 板块分类 | POST |
| `diemeng_index_ths_constituent_stocks` | 获取 ths 成分股 | POST |
| `diemeng_index_ths_daily` | 获取 ths 日线数据 | POST |

## diemeng_basic_calendar

**获取交易日历** · `GET /api/basic/calendar`

获取交易日历数据。支持按日期范围查询。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| start_time | string | 是 | 开始日期 (YYYY-MM-DD) |
| end_time | string | 是 | 结束日期 (YYYY-MM-DD) |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "start_time": "2026-08-12"
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| date | string | 日历日期 |
| is_open | integer | 是否交易日 (0:休市, 1:交易) |

## diemeng_stock_financial_indicator

**获取财务指标报表数据** · `POST /api/stock/financial_indicator`

从 stock_financial_indicator 表获取股票财务指标数据。请求参数 stock_code / end_date / ann_date 三选一至少传一个。每次最多返回 10000 条数据。接口实际返回该表全部字段（除 update_flag、create_time），下方已展示完整字段清单。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH"。支持数组。 |
| end_date | string | 否 | 报告期最后日期，格式 YYYY-MM-DD。 |
| ann_date | string | 否 | 公告日期，格式 YYYY-MM-DD。 |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000，最大10000) |

调用参数示例：

```json
{
  "stock_code": "600941.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码（对应 Tushare 的 ts_code） |
| ann_date | string | 公告日期 |
| end_date | string | 报告期 |
| eps | float \| string \| null | 基本每股收益 |
| dt_eps | float \| string \| null | 稀释每股收益 |
| total_revenue_ps | float \| string \| null | 每股营业总收入 |
| revenue_ps | float \| string \| null | 每股营业收入 |
| capital_rese_ps | float \| string \| null | 每股资本公积 |
| surplus_rese_ps | float \| string \| null | 每股盈余公积 |
| undist_profit_ps | float \| string \| null | 每股未分配利润 |
| extra_item | float \| string \| null | 非经常性损益 |
| profit_dedt | float \| string \| null | 扣除非经常性损益后的净利润（扣非净利润） |
| gross_margin | float \| string \| null | 毛利 |
| current_ratio | float \| string \| null | 流动比率 |
| quick_ratio | float \| string \| null | 速动比率 |
| cash_ratio | float \| string \| null | 保守速动比率 |
| invturn_days | float \| string \| null | 存货周转天数 |
| arturn_days | float \| string \| null | 应收账款周转天数 |
| inv_turn | float \| string \| null | 存货周转率 |
| ar_turn | float \| string \| null | 应收账款周转率 |
| ca_turn | float \| string \| null | 流动资产周转率 |
| fa_turn | float \| string \| null | 固定资产周转率 |
| assets_turn | float \| string \| null | 总资产周转率 |
| op_income | float \| string \| null | 经营活动净收益 |
| valuechange_income | float \| string \| null | 价值变动净收益 |
| interst_income | float \| string \| null | 利息费用 |
| daa | float \| string \| null | 折旧与摊销 |
| ebit | float \| string \| null | 息税前利润 |
| ebitda | float \| string \| null | 息税折旧摊销前利润 |
| fcff | float \| string \| null | 企业自由现金流量 |
| fcfe | float \| string \| null | 股权自由现金流量 |
| current_exint | float \| string \| null | 无息流动负债 |
| noncurrent_exint | float \| string \| null | 无息非流动负债 |
| interestdebt | float \| string \| null | 带息债务 |
| netdebt | float \| string \| null | 净债务 |
| tangible_asset | float \| string \| null | 有形资产 |
| working_capital | float \| string \| null | 营运资金 |
| networking_capital | float \| string \| null | 营运流动资本 |
| invest_capital | float \| string \| null | 全部投入资本 |
| retained_earnings | float \| string \| null | 留存收益 |
| diluted2_eps | float \| string \| null | 期末摊薄每股收益 |
| bps | float \| string \| null | 每股净资产 |
| ocfps | float \| string \| null | 每股经营活动产生的现金流量净额 |
| retainedps | float \| string \| null | 每股留存收益 |
| cfps | float \| string \| null | 每股现金流量净额 |
| ebit_ps | float \| string \| null | 每股息税前利润 |
| fcff_ps | float \| string \| null | 每股企业自由现金流量 |
| fcfe_ps | float \| string \| null | 每股股东自由现金流量 |
| netprofit_margin | float \| string \| null | 销售净利率 |
| grossprofit_margin | float \| string \| null | 销售毛利率 |
| cogs_of_sales | float \| string \| null | 销售成本率 |
| expense_of_sales | float \| string \| null | 销售期间费用率 |
| profit_to_gr | float \| string \| null | 净利润/营业总收入 |
| saleexp_to_gr | float \| string \| null | 销售费用/营业总收入 |
| adminexp_of_gr | float \| string \| null | 管理费用/营业总收入 |
| finaexp_of_gr | float \| string \| null | 财务费用/营业总收入 |
| impai_ttm | float \| string \| null | 资产减值损失/营业总收入 |
| gc_of_gr | float \| string \| null | 营业总成本/营业总收入 |
| op_of_gr | float \| string \| null | 营业利润/营业总收入 |
| ebit_of_gr | float \| string \| null | 息税前利润/营业总收入 |
| roe | float \| string \| null | 净资产收益率 |
| roe_waa | float \| string \| null | 加权平均净资产收益率 |
| roe_dt | float \| string \| null | 净资产收益率(扣除非经常损益) |
| roa | float \| string \| null | 总资产报酬率 |
| npta | float \| string \| null | 总资产净利润 |
| roic | float \| string \| null | 投入资本回报率 |
| roe_yearly | float \| string \| null | 年化净资产收益率 |
| roa2_yearly | float \| string \| null | 年化总资产报酬率 |
| roe_avg | float \| string \| null | 平均净资产收益率(增发条件) |
| opincome_of_ebt | float \| string \| null | 经营活动净收益/利润总额 |
| investincome_of_ebt | float \| string \| null | 价值变动净收益/利润总额 |
| n_op_profit_of_ebt | float \| string \| null | 营业外收支净额/利润总额 |
| tax_to_ebt | float \| string \| null | 所得税/利润总额 |
| dtprofit_to_profit | float \| string \| null | 扣除非经常损益后的净利润/净利润 |
| salescash_to_or | float \| string \| null | 销售商品提供劳务收到的现金/营业收入 |
| ocf_to_or | float \| string \| null | 经营活动产生的现金流量净额/营业收入 |
| ocf_to_opincome | float \| string \| null | 经营活动产生的现金流量净额/经营活动净收益 |
| capitalized_to_da | float \| string \| null | 资本支出/折旧和摊销 |
| debt_to_assets | float \| string \| null | 资产负债率 |
| assets_to_eqt | float \| string \| null | 权益乘数 |
| dp_assets_to_eqt | float \| string \| null | 权益乘数(杜邦分析) |
| ca_to_assets | float \| string \| null | 流动资产/总资产 |
| nca_to_assets | float \| string \| null | 非流动资产/总资产 |
| tbassets_to_totalassets | float \| string \| null | 有形资产/总资产 |
| int_to_talcap | float \| string \| null | 带息债务/全部投入资本 |
| eqt_to_talcapital | float \| string \| null | 归属于母公司的股东权益/全部投入资本 |
| currentdebt_to_debt | float \| string \| null | 流动负债/负债合计 |
| longdeb_to_debt | float \| string \| null | 非流动负债/负债合计 |
| ocf_to_shortdebt | float \| string \| null | 经营活动产生的现金流量净额/流动负债 |
| debt_to_eqt | float \| string \| null | 产权比率 |
| eqt_to_debt | float \| string \| null | 归属于母公司的股东权益/负债合计 |
| eqt_to_interestdebt | float \| string \| null | 归属于母公司的股东权益/带息债务 |
| tangibleasset_to_debt | float \| string \| null | 有形资产/负债合计 |
| tangasset_to_intdebt | float \| string \| null | 有形资产/带息债务 |
| tangibleasset_to_netdebt | float \| string \| null | 有形资产/净债务 |
| ocf_to_debt | float \| string \| null | 经营活动产生的现金流量净额/负债合计 |
| ocf_to_interestdebt | float \| string \| null | 经营活动产生的现金流量净额/带息债务 |
| ocf_to_netdebt | float \| string \| null | 经营活动产生的现金流量净额/净债务 |
| ebit_to_interest | float \| string \| null | 已获利息倍数(EBIT/利息费用) |
| longdebt_to_workingcapital | float \| string \| null | 长期债务与营运资金比率 |
| ebitda_to_debt | float \| string \| null | 息税折旧摊销前利润/负债合计 |
| turn_days | float \| string \| null | 营业周期 |
| roa_yearly | float \| string \| null | 年化总资产净利率 |
| roa_dp | float \| string \| null | 总资产净利率(杜邦分析) |
| fixed_assets | float \| string \| null | 固定资产合计 |
| profit_prefin_exp | float \| string \| null | 扣除财务费用前营业利润 |
| non_op_profit | float \| string \| null | 非营业利润 |
| op_to_ebt | float \| string \| null | 营业利润／利润总额 |
| nop_to_ebt | float \| string \| null | 非营业利润／利润总额 |
| ocf_to_profit | float \| string \| null | 经营活动产生的现金流量净额／营业利润 |
| cash_to_liqdebt | float \| string \| null | 货币资金／流动负债 |
| cash_to_liqdebt_withinterest | float \| string \| null | 货币资金／带息流动负债 |
| op_to_liqdebt | float \| string \| null | 营业利润／流动负债 |
| op_to_debt | float \| string \| null | 营业利润／负债合计 |
| roic_yearly | float \| string \| null | 年化投入资本回报率 |
| total_fa_trun | float \| string \| null | 固定资产合计周转率 |
| profit_to_op | float \| string \| null | 利润总额／营业收入 |
| q_opincome | float \| string \| null | 经营活动单季度净收益 |
| q_investincome | float \| string \| null | 价值变动单季度净收益 |
| q_dtprofit | float \| string \| null | 扣除非经常损益后的单季度净利润 |
| q_eps | float \| string \| null | 每股收益(单季度) |
| q_netprofit_margin | float \| string \| null | 销售净利率(单季度) |
| q_gsprofit_margin | float \| string \| null | 销售毛利率(单季度) |
| q_exp_to_sales | float \| string \| null | 销售期间费用率(单季度) |
| q_profit_to_gr | float \| string \| null | 净利润／营业总收入(单季度) |
| q_saleexp_to_gr | float \| string \| null | 销售费用／营业总收入(单季度) |
| q_adminexp_to_gr | float \| string \| null | 管理费用／营业总收入(单季度) |
| q_finaexp_to_gr | float \| string \| null | 财务费用／营业总收入(单季度) |
| q_impair_to_gr_ttm | float \| string \| null | 资产减值损失／营业总收入(单季度) |
| q_gc_to_gr | float \| string \| null | 营业总成本／营业总收入(单季度) |
| q_op_to_gr | float \| string \| null | 营业利润／营业总收入(单季度) |
| q_roe | float \| string \| null | 净资产收益率(单季度) |
| q_dt_roe | float \| string \| null | 净资产单季度收益率(扣除非经常损益) |
| q_npta | float \| string \| null | 总资产净利润(单季度) |
| q_opincome_to_ebt | float \| string \| null | 经营活动净收益／利润总额(单季度) |
| q_investincome_to_ebt | float \| string \| null | 价值变动净收益／利润总额(单季度) |
| q_dtprofit_to_profit | float \| string \| null | 扣非净利润／净利润(单季度) |
| q_salescash_to_or | float \| string \| null | 销售商品提供劳务收到的现金／营业收入(单季度) |
| q_ocf_to_sales | float \| string \| null | 经营活动现金流净额／营业收入(单季度) |
| q_ocf_to_or | float \| string \| null | 经营活动现金流净额／经营活动净收益(单季度) |
| basic_eps_yoy | float \| string \| null | 基本每股收益同比增长率(%) |
| dt_eps_yoy | float \| string \| null | 稀释每股收益同比增长率(%) |
| cfps_yoy | float \| string \| null | 每股经营活动现金流净额同比增长率(%) |
| op_yoy | float \| string \| null | 营业利润同比增长率(%) |
| ebt_yoy | float \| string \| null | 利润总额同比增长率(%) |
| netprofit_yoy | float \| string \| null | 归母净利润同比增长率(%) |
| dt_netprofit_yoy | float \| string \| null | 归母净利润(扣非)同比增长率(%) |
| ocf_yoy | float \| string \| null | 经营活动现金流净额同比增长率(%) |
| roe_yoy | float \| string \| null | 净资产收益率(摊薄)同比增长率(%) |
| bps_yoy | float \| string \| null | 每股净资产相对年初增长率(%) |
| assets_yoy | float \| string \| null | 资产总计相对年初增长率(%) |
| eqt_yoy | float \| string \| null | 归母股东权益相对年初增长率(%) |
| tr_yoy | float \| string \| null | 营业总收入同比增长率(%) |
| or_yoy | float \| string \| null | 营业收入同比增长率(%) |
| q_gr_yoy | float \| string \| null | 营业总收入同比增长率(%)(单季度) |
| q_gr_qoq | float \| string \| null | 营业总收入环比增长率(%)(单季度) |
| q_sales_yoy | float \| string \| null | 营业收入同比增长率(%)(单季度) |
| q_sales_qoq | float \| string \| null | 营业收入环比增长率(%)(单季度) |
| q_op_yoy | float \| string \| null | 营业利润同比增长率(%)(单季度) |
| q_op_qoq | float \| string \| null | 营业利润环比增长率(%)(单季度) |
| q_profit_yoy | float \| string \| null | 净利润同比增长率(%)(单季度) |
| q_profit_qoq | float \| string \| null | 净利润环比增长率(%)(单季度) |
| q_netprofit_yoy | float \| string \| null | 归母净利润同比增长率(%)(单季度) |
| q_netprofit_qoq | float \| string \| null | 归母净利润环比增长率(%)(单季度) |
| equity_yoy | float \| string \| null | 净资产同比增长率 |
| rd_exp | float \| string \| null | 研发费用 |

## diemeng_stock_income

**获取利润表数据** · `POST /api/stock/income`

从 stock_income 表获取利润表数据。请求参数 stock_code / end_date / ann_date 三选一至少传一个。每次最多返回 10000 条数据。接口实际返回该表全部字段（除 update_flag、create_time），下方已展示完整字段清单。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH"。支持数组。 |
| end_date | string | 否 | 报告期最后日期，格式 YYYY-MM-DD。 |
| ann_date | string | 否 | 公告日期，格式 YYYY-MM-DD。 |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000，最大10000) |

调用参数示例：

```json
{
  "stock_code": "600941.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码（对应 Tushare 的 ts_code） |
| ann_date | string | 公告日期 |
| f_ann_date | string | 实际公告日期 |
| end_date | string | 报告期 |
| report_type | float \| string \| null | 报表类型 |
| comp_type | float \| string \| null | 公司类型(1一般工商业2银行3保险4证券) |
| end_type | float \| string \| null | 报告期类型 |
| basic_eps | float \| string \| null | 基本每股收益 |
| diluted_eps | float \| string \| null | 稀释每股收益 |
| total_revenue | float \| string \| null | 营业总收入 |
| revenue | float \| string \| null | 营业收入 |
| int_income | float \| string \| null | 利息收入 |
| prem_earned | float \| string \| null | 已赚保费 |
| comm_income | float \| string \| null | 手续费及佣金收入 |
| n_commis_income | float \| string \| null | 手续费及佣金净收入 |
| n_oth_income | float \| string \| null | 其他经营净收益 |
| n_oth_b_income | float \| string \| null | 加:其他业务净收益 |
| prem_income | float \| string \| null | 保险业务收入 |
| out_prem | float \| string \| null | 减:分出保费 |
| une_prem_reser | float \| string \| null | 提取未到期责任准备金 |
| reins_income | float \| string \| null | 其中:分保费收入 |
| n_sec_tb_income | float \| string \| null | 代理买卖证券业务净收入 |
| n_sec_uw_income | float \| string \| null | 证券承销业务净收入 |
| n_asset_mg_income | float \| string \| null | 受托客户资产管理业务净收入 |
| oth_b_income | float \| string \| null | 其他业务收入 |
| fv_value_chg_gain | float \| string \| null | 加:公允价值变动净收益 |
| invest_income | float \| string \| null | 加:投资净收益 |
| ass_invest_income | float \| string \| null | 其中:对联营企业和合营企业的投资收益 |
| forex_gain | float \| string \| null | 加:汇兑净收益 |
| total_cogs | float \| string \| null | 营业总成本 |
| oper_cost | float \| string \| null | 减:营业成本 |
| int_exp | float \| string \| null | 减:利息支出 |
| comm_exp | float \| string \| null | 减:手续费及佣金支出 |
| biz_tax_surchg | float \| string \| null | 减:营业税金及附加 |
| sell_exp | float \| string \| null | 减:销售费用 |
| admin_exp | float \| string \| null | 减:管理费用 |
| fin_exp | float \| string \| null | 减:财务费用 |
| assets_impair_loss | float \| string \| null | 减:资产减值损失 |
| prem_refund | float \| string \| null | 退保金 |
| compens_payout | float \| string \| null | 赔付总支出 |
| reser_insur_liab | float \| string \| null | 提取保险责任准备金 |
| div_payt | float \| string \| null | 保户红利支出 |
| reins_exp | float \| string \| null | 分保费用 |
| oper_exp | float \| string \| null | 营业支出 |
| compens_payout_refu | float \| string \| null | 减:摊回赔付支出 |
| insur_reser_refu | float \| string \| null | 减:摊回保险责任准备金 |
| reins_cost_refund | float \| string \| null | 减:摊回分保费用 |
| other_bus_cost | float \| string \| null | 其他业务成本 |
| operate_profit | float \| string \| null | 营业利润 |
| non_oper_income | float \| string \| null | 加:营业外收入 |
| non_oper_exp | float \| string \| null | 减:营业外支出 |
| nca_disploss | float \| string \| null | 其中:减:非流动资产处置净损失 |
| total_profit | float \| string \| null | 利润总额 |
| income_tax | float \| string \| null | 所得税费用 |
| n_income | float \| string \| null | 净利润(含少数股东损益) |
| n_income_attr_p | float \| string \| null | 净利润(不含少数股东损益) |
| minority_gain | float \| string \| null | 少数股东损益 |
| oth_compr_income | float \| string \| null | 其他综合收益 |
| t_compr_income | float \| string \| null | 综合收益总额 |
| compr_inc_attr_p | float \| string \| null | 归属于母公司(或股东)的综合收益总额 |
| compr_inc_attr_m_s | float \| string \| null | 归属于少数股东的综合收益总额 |
| ebit | float \| string \| null | 息税前利润 |
| ebitda | float \| string \| null | 息税折旧摊销前利润 |
| insurance_exp | float \| string \| null | 保险业务支出 |
| undist_profit | float \| string \| null | 年初未分配利润 |
| distable_profit | float \| string \| null | 可分配利润 |
| rd_exp | float \| string \| null | 研发费用 |
| fin_exp_int_exp | float \| string \| null | 财务费用:利息费用 |
| fin_exp_int_inc | float \| string \| null | 财务费用:利息收入 |
| transfer_surplus_rese | float \| string \| null | 盈余公积转入 |
| transfer_housing_imprest | float \| string \| null | 住房周转金转入 |
| transfer_oth | float \| string \| null | 其他转入 |
| adj_lossgain | float \| string \| null | 调整以前年度损益 |
| withdra_legal_surplus | float \| string \| null | 提取法定盈余公积 |
| withdra_legal_pubfund | float \| string \| null | 提取法定公益金 |
| withdra_biz_devfund | float \| string \| null | 提取企业发展基金 |
| withdra_rese_fund | float \| string \| null | 提取储备基金 |
| withdra_oth_ersu | float \| string \| null | 提取任意盈余公积金 |
| workers_welfare | float \| string \| null | 职工奖金福利 |
| distr_profit_shrhder | float \| string \| null | 可供股东分配的利润 |
| prfshare_payable_dvd | float \| string \| null | 应付优先股股利 |
| comshare_payable_dvd | float \| string \| null | 应付普通股股利 |
| capit_comstock_div | float \| string \| null | 转作股本的普通股股利 |
| continued_net_profit | float \| string \| null | 持续经营净利润 |

## diemeng_stock_balancesheet

**获取资产负债表数据** · `POST /api/stock/balancesheet`

从 stock_balancesheet 表获取资产负债表数据。请求参数 stock_code / end_date / ann_date 三选一至少传一个。每次最多返回 10000 条数据。接口实际返回该表全部字段（除 update_flag、create_time），下方已展示完整字段清单。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH"。支持数组。 |
| end_date | string | 否 | 报告期最后日期，格式 YYYY-MM-DD。 |
| ann_date | string | 否 | 公告日期，格式 YYYY-MM-DD。 |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000，最大10000) |

调用参数示例：

```json
{
  "stock_code": "600941.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码（对应 Tushare 的 ts_code） |
| ann_date | string | 公告日期 |
| f_ann_date | string | 实际公告日期 |
| end_date | string | 报告期 |
| report_type | float \| string \| null | 报表类型 |
| comp_type | float \| string \| null | 公司类型(1一般工商业2银行3保险4证券) |
| end_type | float \| string \| null | 报告期类型 |
| total_share | float \| string \| null | 期末总股本 |
| cap_rese | float \| string \| null | 资本公积金 |
| undistr_porfit | float \| string \| null | 未分配利润 |
| surplus_rese | float \| string \| null | 盈余公积金 |
| special_rese | float \| string \| null | 专项储备 |
| money_cap | float \| string \| null | 货币资金 |
| trad_asset | float \| string \| null | 交易性金融资产 |
| notes_receiv | float \| string \| null | 应收票据 |
| accounts_receiv | float \| string \| null | 应收账款 |
| oth_receiv | float \| string \| null | 其他应收款 |
| prepayment | float \| string \| null | 预付款项 |
| div_receiv | float \| string \| null | 应收股利 |
| int_receiv | float \| string \| null | 应收利息 |
| inventories | float \| string \| null | 存货 |
| amor_exp | float \| string \| null | 待摊费用 |
| nca_within_1y | float \| string \| null | 一年内到期的非流动资产 |
| sett_rsrv | float \| string \| null | 结算备付金 |
| loanto_oth_bank_fi | float \| string \| null | 拆出资金 |
| premium_receiv | float \| string \| null | 应收保费 |
| reinsur_receiv | float \| string \| null | 应收分保账款 |
| reinsur_res_receiv | float \| string \| null | 应收分保合同准备金 |
| pur_resale_fa | float \| string \| null | 买入返售金融资产 |
| oth_cur_assets | float \| string \| null | 其他流动资产 |
| total_cur_assets | float \| string \| null | 流动资产合计 |
| fa_avail_for_sale | float \| string \| null | 可供出售金融资产 |
| htm_invest | float \| string \| null | 持有至到期投资 |
| lt_eqt_invest | float \| string \| null | 长期股权投资 |
| invest_real_estate | float \| string \| null | 投资性房地产 |
| time_deposits | float \| string \| null | 定期存款 |
| oth_assets | float \| string \| null | 其他资产 |
| lt_rec | float \| string \| null | 长期应收款 |
| fix_assets | float \| string \| null | 固定资产 |
| cip | float \| string \| null | 在建工程 |
| const_materials | float \| string \| null | 工程物资 |
| fixed_assets_disp | float \| string \| null | 固定资产清理 |
| produc_bio_assets | float \| string \| null | 生产性生物资产 |
| oil_and_gas_assets | float \| string \| null | 油气资产 |
| intan_assets | float \| string \| null | 无形资产 |
| r_and_d | float \| string \| null | 研发支出 |
| goodwill | float \| string \| null | 商誉 |
| lt_amor_exp | float \| string \| null | 长期待摊费用 |
| defer_tax_assets | float \| string \| null | 递延所得税资产 |
| decr_in_disbur | float \| string \| null | 发放贷款及垫款 |
| oth_nca | float \| string \| null | 其他非流动资产 |
| total_nca | float \| string \| null | 非流动资产合计 |
| cash_reser_cb | float \| string \| null | 现金及存放中央银行款项 |
| depos_in_oth_bfi | float \| string \| null | 存放同业和其它金融机构款项 |
| prec_metals | float \| string \| null | 贵金属 |
| deriv_assets | float \| string \| null | 衍生金融资产 |
| rr_reins_une_prem | float \| string \| null | 应收分保未到期责任准备金 |
| rr_reins_outstd_cla | float \| string \| null | 应收分保未决赔款准备金 |
| rr_reins_lins_liab | float \| string \| null | 应收分保寿险责任准备金 |
| rr_reins_lthins_liab | float \| string \| null | 应收分保长期健康险责任准备金 |
| refund_depos | float \| string \| null | 存出保证金 |
| ph_pledge_loans | float \| string \| null | 保户质押贷款 |
| refund_cap_depos | float \| string \| null | 存出资本保证金 |
| indep_acct_assets | float \| string \| null | 独立账户资产 |
| client_depos | float \| string \| null | 其中：客户资金存款 |
| client_prov | float \| string \| null | 其中：客户备付金 |
| transac_seat_fee | float \| string \| null | 其中:交易席位费 |
| invest_as_receiv | float \| string \| null | 应收款项类投资 |
| total_assets | float \| string \| null | 资产总计 |
| lt_borr | float \| string \| null | 长期借款 |
| st_borr | float \| string \| null | 短期借款 |
| cb_borr | float \| string \| null | 向中央银行借款 |
| depos_ib_deposits | float \| string \| null | 吸收存款及同业存放 |
| loan_oth_bank | float \| string \| null | 拆入资金 |
| trading_fl | float \| string \| null | 交易性金融负债 |
| notes_payable | float \| string \| null | 应付票据 |
| acct_payable | float \| string \| null | 应付账款 |
| adv_receipts | float \| string \| null | 预收款项 |
| sold_for_repur_fa | float \| string \| null | 卖出回购金融资产款 |
| comm_payable | float \| string \| null | 应付手续费及佣金 |
| payroll_payable | float \| string \| null | 应付职工薪酬 |
| taxes_payable | float \| string \| null | 应交税费 |
| int_payable | float \| string \| null | 应付利息 |
| div_payable | float \| string \| null | 应付股利 |
| oth_payable | float \| string \| null | 其他应付款 |
| acc_exp | float \| string \| null | 预提费用 |
| deferred_inc | float \| string \| null | 递延收益 |
| st_bonds_payable | float \| string \| null | 应付短期债券 |
| payable_to_reinsurer | float \| string \| null | 应付分保账款 |
| rsrv_insur_cont | float \| string \| null | 保险合同准备金 |
| acting_trading_sec | float \| string \| null | 代理买卖证券款 |
| acting_uw_sec | float \| string \| null | 代理承销证券款 |
| non_cur_liab_due_1y | float \| string \| null | 一年内到期的非流动负债 |
| oth_cur_liab | float \| string \| null | 其他流动负债 |
| total_cur_liab | float \| string \| null | 流动负债合计 |
| bond_payable | float \| string \| null | 应付债券 |
| lt_payable | float \| string \| null | 长期应付款 |
| specific_payables | float \| string \| null | 专项应付款 |
| estimated_liab | float \| string \| null | 预计负债 |
| defer_tax_liab | float \| string \| null | 递延所得税负债 |
| defer_inc_non_cur_liab | float \| string \| null | 递延收益-非流动负债 |
| oth_ncl | float \| string \| null | 其他非流动负债 |
| total_ncl | float \| string \| null | 非流动负债合计 |
| depos_oth_bfi | float \| string \| null | 同业和其它金融机构存放款项 |
| deriv_liab | float \| string \| null | 衍生金融负债 |
| depos | float \| string \| null | 吸收存款 |
| agency_bus_liab | float \| string \| null | 代理业务负债 |
| oth_liab | float \| string \| null | 其他负债 |
| prem_receiv_adva | float \| string \| null | 预收保费 |
| depos_received | float \| string \| null | 存入保证金 |
| ph_invest | float \| string \| null | 保户储金及投资款 |
| reser_une_prem | float \| string \| null | 未到期责任准备金 |
| reser_outstd_claims | float \| string \| null | 未决赔款准备金 |
| reser_lins_liab | float \| string \| null | 寿险责任准备金 |
| reser_lthins_liab | float \| string \| null | 长期健康险责任准备金 |
| indept_acc_liab | float \| string \| null | 独立账户负债 |
| pledge_borr | float \| string \| null | 其中:质押借款 |
| indem_payable | float \| string \| null | 应付赔付款 |
| policy_div_payable | float \| string \| null | 应付保单红利 |
| total_liab | float \| string \| null | 负债合计 |
| treasury_share | float \| string \| null | 减:库存股 |
| ordin_risk_reser | float \| string \| null | 一般风险准备 |
| forex_dinc_min_int | float \| string \| null | 外币报表折算差额 |
| total_liab_hldr_eqy | float \| string \| null | 负债及股东权益总计 |
| lt_payroll_payable | float \| string \| null | 长期应付职工薪酬 |
| oth_comp_income | float \| string \| null | 其他综合收益 |
| oth_eqt_tools | float \| string \| null | 其他权益工具 |
| oth_eqt_tools_p_shr | float \| string \| null | 其他权益工具(优先股) |
| lending_funds | float \| string \| null | 融出资金 |
| acc_receivable | float \| string \| null | 应收款项 |
| st_fin_payable | float \| string \| null | 应付短期融资款 |
| payables | float \| string \| null | 应付款项 |
| hfs_assets | float \| string \| null | 持有待售的资产 |
| hfs_sales | float \| string \| null | 持有待售的负债 |
| cost_fin_assets | float \| string \| null | 以摊余成本计量的金融资产 |
| fair_value_fin_assets | float \| string \| null | 以公允价值计量且其变动计入其他综合收益的金融资产 |
| contract_assets | float \| string \| null | 合同资产 |
| contract_liab | float \| string \| null | 合同负债 |
| accounts_receiv_bill | float \| string \| null | 应收票据及应收账款 |
| accounts_pay | float \| string \| null | 应付票据及应付账款 |
| oth_rcv_total | float \| string \| null | 其他应收款(合计) |
| fix_assets_total | float \| string \| null | 固定资产(合计) |
| cip_total | float \| string \| null | 在建工程(合计) |
| oth_pay_total | float \| string \| null | 其他应付款(合计) |
| long_pay_total | float \| string \| null | 长期应付款(合计) |
| debt_invest | float \| string \| null | 债权投资 |
| oth_debt_invest | float \| string \| null | 其他债权投资 |
| total_hldr_eqy_exc_min_int | float \| string \| null | 股东权益合计(不含少数股东权益) |

## diemeng_stock_cashflow

**获取现金流量表数据** · `POST /api/stock/cashflow`

从 stock_cashflow 表获取现金流量表数据。请求参数 stock_code / end_date / ann_date 三选一至少传一个。每次最多返回 10000 条数据。接口实际返回该表全部字段（除 update_flag、create_time），下方已展示完整字段清单。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH"。支持数组。 |
| end_date | string | 否 | 报告期最后日期，格式 YYYY-MM-DD。 |
| ann_date | string | 否 | 公告日期，格式 YYYY-MM-DD。 |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000，最大10000) |

调用参数示例：

```json
{
  "stock_code": "600941.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码（对应 Tushare 的 ts_code） |
| ann_date | string | 公告日期 |
| f_ann_date | string | 实际公告日期 |
| end_date | string | 报告期 |
| comp_type | float \| string \| null | 公司类型(1一般工商业2银行3保险4证券) |
| report_type | float \| string \| null | 报表类型 |
| end_type | float \| string \| null | 报告期类型 |
| net_profit | float \| string \| null | 净利润 |
| finan_exp | float \| string \| null | 财务费用 |
| c_fr_sale_sg | float \| string \| null | 销售商品、提供劳务收到的现金 |
| recp_tax_rends | float \| string \| null | 收到的税费返还 |
| n_depos_incr_fi | float \| string \| null | 客户存款和同业存放款项净增加额 |
| n_incr_loans_cb | float \| string \| null | 向中央银行借款净增加额 |
| n_inc_borr_oth_fi | float \| string \| null | 向其他金融机构拆入资金净增加额 |
| prem_fr_orig_contr | float \| string \| null | 收到原保险合同保费取得的现金 |
| n_incr_insured_dep | float \| string \| null | 保户储金净增加额 |
| n_reinsur_prem | float \| string \| null | 收到再保业务现金净额 |
| n_incr_disp_tfa | float \| string \| null | 处置交易性金融资产净增加额 |
| ifc_cash_incr | float \| string \| null | 收取利息和手续费净增加额 |
| n_incr_disp_faas | float \| string \| null | 处置可供出售金融资产净增加额 |
| n_incr_loans_oth_bank | float \| string \| null | 拆入资金净增加额 |
| n_cap_incr_repur | float \| string \| null | 回购业务资金净增加额 |
| c_fr_oth_operate_a | float \| string \| null | 收到其他与经营活动有关的现金 |
| c_inf_fr_operate_a | float \| string \| null | 经营活动现金流入小计 |
| c_paid_goods_s | float \| string \| null | 购买商品、接受劳务支付的现金 |
| c_paid_to_for_empl | float \| string \| null | 支付给职工以及为职工支付的现金 |
| c_paid_for_taxes | float \| string \| null | 支付的各项税费 |
| n_incr_clt_loan_adv | float \| string \| null | 客户贷款及垫款净增加额 |
| n_incr_dep_cbob | float \| string \| null | 存放央行和同业款项净增加额 |
| c_pay_claims_orig_inco | float \| string \| null | 支付原保险合同赔付款项的现金 |
| pay_handling_chrg | float \| string \| null | 支付手续费的现金 |
| pay_comm_insur_plcy | float \| string \| null | 支付保单红利的现金 |
| oth_cash_pay_oper_act | float \| string \| null | 支付其他与经营活动有关的现金 |
| st_cash_out_act | float \| string \| null | 经营活动现金流出小计 |
| n_cashflow_act | float \| string \| null | 经营活动产生的现金流量净额 |
| oth_recp_ral_inv_act | float \| string \| null | 收到其他与投资活动有关的现金 |
| c_disp_withdrwl_invest | float \| string \| null | 收回投资收到的现金 |
| c_recp_return_invest | float \| string \| null | 取得投资收益收到的现金 |
| n_recp_disp_fiolta | float \| string \| null | 处置固定资产、无形资产和其他长期资产收回的现金净额 |
| n_recp_disp_sobu | float \| string \| null | 处置子公司及其他营业单位收到的现金净额 |
| stot_inflows_inv_act | float \| string \| null | 投资活动现金流入小计 |
| c_pay_acq_const_fiolta | float \| string \| null | 购建固定资产、无形资产和其他长期资产支付的现金 |
| c_paid_invest | float \| string \| null | 投资支付的现金 |
| n_disp_subs_oth_biz | float \| string \| null | 取得子公司及其他营业单位支付的现金净额 |
| oth_pay_ral_inv_act | float \| string \| null | 支付其他与投资活动有关的现金 |
| n_incr_pledge_loan | float \| string \| null | 质押贷款净增加额 |
| stot_out_inv_act | float \| string \| null | 投资活动现金流出小计 |
| n_cashflow_inv_act | float \| string \| null | 投资活动产生的现金流量净额 |
| c_recp_borrow | float \| string \| null | 取得借款收到的现金 |
| proc_issue_bonds | float \| string \| null | 发行债券收到的现金 |
| oth_cash_recp_ral_fnc_act | float \| string \| null | 收到其他与筹资活动有关的现金 |
| stot_cash_in_fnc_act | float \| string \| null | 筹资活动现金流入小计 |
| free_cashflow | float \| string \| null | 企业自由现金流量 |
| c_prepay_amt_borr | float \| string \| null | 偿还债务支付的现金 |
| c_pay_dist_dpcp_int_exp | float \| string \| null | 分配股利、利润或偿付利息支付的现金 |
| incl_dvd_profit_paid_sc_ms | float \| string \| null | 其中:子公司支付给少数股东的股利、利润 |
| oth_cashpay_ral_fnc_act | float \| string \| null | 支付其他与筹资活动有关的现金 |
| stot_cashout_fnc_act | float \| string \| null | 筹资活动现金流出小计 |
| n_cash_flows_fnc_act | float \| string \| null | 筹资活动产生的现金流量净额 |
| eff_fx_flu_cash | float \| string \| null | 汇率变动对现金的影响 |
| n_incr_cash_cash_equ | float \| string \| null | 现金及现金等价物净增加额 |
| c_cash_equ_beg_period | float \| string \| null | 期初现金及现金等价物余额 |
| c_cash_equ_end_period | float \| string \| null | 期末现金及现金等价物余额 |
| c_recp_cap_contrib | float \| string \| null | 吸收投资收到的现金 |
| incl_cash_rec_saims | float \| string \| null | 其中:子公司吸收少数股东投资收到的现金 |
| uncon_invest_loss | float \| string \| null | 未确认投资损失 |
| prov_depr_assets | float \| string \| null | 加:资产减值准备 |
| depr_fa_coga_dpba | float \| string \| null | 固定资产折旧、油气资产折耗、生产性生物资产折旧 |
| amort_intang_assets | float \| string \| null | 无形资产摊销 |
| lt_amort_deferred_exp | float \| string \| null | 长期待摊费用摊销 |
| decr_deferred_exp | float \| string \| null | 待摊费用减少 |
| incr_acc_exp | float \| string \| null | 预提费用增加 |
| loss_disp_fiolta | float \| string \| null | 处置固定、无形资产和其他长期资产的损失 |
| loss_scr_fa | float \| string \| null | 固定资产报废损失 |
| loss_fv_chg | float \| string \| null | 公允价值变动损失 |
| invest_loss | float \| string \| null | 投资损失 |
| decr_def_inc_tax_assets | float \| string \| null | 递延所得税资产减少 |
| incr_def_inc_tax_liab | float \| string \| null | 递延所得税负债增加 |
| decr_inventories | float \| string \| null | 存货的减少 |
| decr_oper_payable | float \| string \| null | 经营性应收项目的减少 |
| incr_oper_payable | float \| string \| null | 经营性应付项目的增加 |
| others | float \| string \| null | 其他 |
| im_net_cashflow_oper_act | float \| string \| null | 经营活动产生的现金流量净额(间接法) |
| conv_debt_into_cap | float \| string \| null | 债务转为资本 |
| conv_copbonds_due_within_1y | float \| string \| null | 一年内到期的可转换公司债券 |
| fa_fnc_leases | float \| string \| null | 融资租入固定资产 |
| im_n_incr_cash_equ | float \| string \| null | 现金及现金等价物净增加额(间接法) |
| net_dism_capital_add | float \| string \| null | 拆出资金净增加额 |
| net_cash_rece_sec | float \| string \| null | 代理买卖证券收到的现金净额 |
| credit_impa_loss | float \| string \| null | 信用减值损失 |
| use_right_asset_dep | float \| string \| null | 使用权资产折旧 |
| oth_loss_asset | float \| string \| null | 其他资产减值损失 |
| end_bal_cash | float \| string \| null | 现金的期末余额 |
| beg_bal_cash | float \| string \| null | 减:现金的期初余额 |
| end_bal_cash_equ | float \| string \| null | 加:现金等价物的期末余额 |
| beg_bal_cash_equ | float \| string \| null | 减:现金等价物的期初余额 |

## diemeng_stock_finance

**获取每日财务数据** · `POST /api/stock/finance`

获取指定股票（支持多只）的每日财务指标数据（市盈率、市净率、换手率等）。支持按日期范围筛选。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH"。最大支持100个。 |
| start_time | string | 否 | 开始日期 (YYYY-MM-DD) |
| end_time | string | 否 | 结束日期 (YYYY-MM-DD) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| trade_date | string | 交易日期 |
| close | float | 收盘价 |
| turnover_rate | float | 换手率 |
| turnover_rate_f | float | 换手率(自由流通股) |
| volume_ratio | float | 量比 |
| pe | float | 市盈率(总市值/净利润) |
| pe_ttm | float | 市盈率TTM |
| pe_ttm_percentile | float | 市盈率TTM百分位 |
| pb | float | 市净率 |
| ps | float | 市销率 |
| ps_ttm | float | 市销率TTM |
| dv_ratio | float | 股息率 |
| dv_ttm | float | 股息率TTM |
| total_share | float | 总股本 |
| float_share | float | 流通股本 |
| free_share | float | 自由流通股本 |
| total_mv | float | 总市值 |
| circ_mv | float | 流通市值 |

## diemeng_stock_forecast

**获取财报预告** · `POST /api/stock/forecast`

查询 Tushare forecast_vip 财报预告数据。至少提供 stock_code、ann_date、end_date、start_date+finish_date 或 type 之一。日期支持 YYYY-MM-DD 或 YYYYMMDD，最多返回 10000 条。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string | 否 | 股票代码 |
| ann_date | string | 否 | 公告日期 |
| end_date | string | 否 | 报告期 |
| start_date | string | 否 | 公告日期起始值 |
| finish_date | string | 否 | 公告日期结束值，与 start_date 配套 |
| type | string | 否 | 预告类型 |
| page | integer | 否 | 页码，从 0 开始 |
| page_size | integer | 否 | 每页数量，最大 10000 |

调用参数示例：

```json
{
  "stock_code": "600941.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| ann_date | string | 公告日期 |
| end_date | string | 报告期 |
| type | string \| null | 预告类型 |
| p_change_min | number \| null | 净利润变动幅度下限 |
| p_change_max | number \| null | 净利润变动幅度上限 |
| net_profit_min | number \| null | 预计净利润下限 |
| net_profit_max | number \| null | 预计净利润上限 |
| last_parent_net | number \| null | 上年同期归母净利润 |
| first_ann_date | string \| null | 首次公告日期 |
| summary | string \| null | 业绩预告摘要 |
| change_reason | string \| null | 业绩变动原因 |

## diemeng_stock_list

**获取股票列表** · `GET /api/stock/list`

获取所有股票的基础信息列表（代码、名称、上市日期等）。

无输入参数。

调用参数示例：

```json
{}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| name | string | 股票名称 |
| area | string | 地域 |
| industry | string | 行业 |
| list_date | string | 上市日期 |
| symbol | string | 股票代码（不含后缀） |
| act_name | string | 实控人名称 |
| act_ent_type | string | 实控人企业性质 |
| list_status | string | 上市状态 (L:上市, D:退市, G:过会未交易, P:暂停上市) |
| delist_date | string | 退市日期 |
| is_hs | string | 是否沪深港通标的 (N:否, H:沪股通, S:深股通) |

## diemeng_stock_daily

**获取日K线数据** · `POST /api/stock/daily`

获取指定股票（支持多只）的日线级别行情数据（开高低收成交量等）。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH" 或 ["600000.SH", "000001.SZ"]。最大支持100个。如果不传，则返回全市场数据（分页）。 |
| start_time | string | 是 | 开始时间，格式 YYYY-MM-DD |
| end_time | string | 是 | 结束时间，格式 YYYY-MM-DD |
| volType | string | 否 | 成交量单位，可选 "share"(股，默认) 或 "lot"(手) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| trade_date | string | 交易日期 |
| stock_code | string | 股票代码 |
| stock_name | string | 股票名称 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| pre_close | float | 昨收价 |
| change | float | 涨跌额 |
| pct_chg | float | 涨跌幅(%) |
| vol | float | 成交量（单位由 volType 决定：share=股，lot=手） |
| amount | float | 成交额 |
| ah_vol | float | 盘后成交量（手） |
| ah_amount | float | 盘后成交额（千元） |

## diemeng_stock_market_distribution_history

**获取历史市场涨跌分布** · `POST /api/stock/market_distribution_history`

获取指定交易日的分钟级全市场涨跌分布。数据只读取历史表，不影响实时市场涨跌分布接口。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| date | string | 是 | 交易日期，格式 YYYY-MM-DD |

调用参数示例：

```json
{
  "date": "2026-08-12"
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| trade_time | string | 交易分钟 |
| up_count | integer | 上涨家数 |
| down_count | integer | 下跌家数 |
| flat_count | integer | 平盘家数 |
| limit_up_count | integer | 涨停家数 |
| limit_down_count | integer | 跌停家数 |
| up_over_10 | integer | 涨幅大于 10% 家数 |
| up_7_to_10 | integer | 涨幅 7% 至 10% 家数 |
| up_5_to_7 | integer | 涨幅 5% 至 7% 家数 |
| up_3_to_5 | integer | 涨幅 3% 至 5% 家数 |
| up_0_to_3 | integer | 涨幅 0% 至 3% 家数 |
| zero | integer | 涨跌幅为 0 的家数 |
| down_0_to_3 | integer | 跌幅 0% 至 3% 家数 |
| down_3_to_5 | integer | 跌幅 3% 至 5% 家数 |
| down_5_to_7 | integer | 跌幅 5% 至 7% 家数 |
| down_7_to_10 | integer | 跌幅 7% 至 10% 家数 |
| down_over_10 | integer | 跌幅大于 10% 家数 |

## diemeng_stock_kline

**获取周期K线数据** · `POST /api/stock/kline`

获取指定股票（支持多只）的周期（周/月）行情数据。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| period | string | 是 | K线周期，可选值: weekly, monthly |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH" 或 ["600000.SH", "000001.SZ"]。 |
| start_time | string | 是 | 开始时间，格式 YYYY-MM-DD |
| end_time | string | 是 | 结束时间，格式 YYYY-MM-DD |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "period": "weekly",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| trade_date | string | 交易日期(通常为该周期最后交易日) |
| stock_code | string | 股票代码 |
| stock_name | string | 股票名称 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| pre_close | float | 昨收价 |
| change | float | 涨跌额 |
| pct_chg | float | 涨跌幅(%) |
| vol | float | 成交量 |
| amount | float | 成交额 |
| ah_vol | float | 周期内盘后成交量合计（手） |
| ah_amount | float | 周期内盘后成交额合计（千元） |

## diemeng_stock_kline_adj

**获取周期K线数据(复权)** · `POST /api/stock/kline_adj`

获取指定股票的复权周期（周/月）K线数据。目前仅支持前复权(qfq)。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| period | string | 是 | K线周期，可选值: weekly, monthly |
| stock_code | string | 否 | 股票代码，例如 "600000.SH" |
| start_time | string | 否 | 开始时间，格式 YYYY-MM-DD |
| end_time | string | 否 | 结束时间，格式 YYYY-MM-DD |
| algo | string | 否 | 复权算法: "recursive" (默认), "factor" |
| volType | string | 否 | 成交量单位，可选 "share"(股，默认) 或 "lot"(手) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000，最大10000) |

调用参数示例：

```json
{
  "period": "weekly",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| stock_name | string | 股票名称 |
| trade_date | string | 交易日期 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| change | float | 涨跌额 |
| pct_chg | float | 涨跌幅(%) |
| vol | float | 成交量（单位由 volType 决定：share=股，lot=手） |
| amount | float | 成交额 |
| ah_vol | float | 周期内盘后成交量合计（手，不参与复权） |
| ah_amount | float | 周期内盘后成交额合计（千元，不参与复权） |

## diemeng_stock_daily_adj

**获取日K线数据(复权)** · `POST /api/stock/daily_adj`

获取指定股票的复权日K线数据。目前仅支持前复权(qfq)。
复权算法说明：
- recursive (递归复权): 默认算法。使用除权除息日的前复权因子进行递归计算。与 ths 算法一致。
- factor (涨跌幅复权): 使用每日的复权因子直接计算。公式：`当前价格 * 当日复权因子`。该算法比较适合量化回测。
注意事项：
- 当股票发生除权除息时，历史的前复权数据需要全部更新。
- 建议使用 `复权因子变更查询` 接口检查当天是否有因子变更，若有，则需要更新对应股票的历史复权数据。
参数说明：
- `stock_code` 和 (`start_time` + `end_time`) 必须至少提供其一。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string | 否 | 股票代码，例如 "600000.SH" |
| start_time | string | 否 | 开始时间，格式 YYYY-MM-DD |
| end_time | string | 否 | 结束时间，格式 YYYY-MM-DD |
| algo | string | 否 | 复权算法: "recursive" (默认), "factor" |
| volType | string | 否 | 成交量单位，可选 "share"(股，默认) 或 "lot"(手) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000，最大10000) |

调用参数示例：

```json
{
  "stock_code": "600941.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| stock_name | string | 股票名称 |
| trade_date | string | 交易日期 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| change | float | 涨跌额 |
| pct_chg | float | 涨跌幅(%) |
| vol | float | 成交量（单位由 volType 决定：share=股，lot=手） |
| amount | float | 成交额 |
| ah_vol | float | 盘后成交量（手，不参与复权） |
| ah_amount | float | 盘后成交额（千元，不参与复权） |

## diemeng_stock_min_adj

**获取分钟K线数据(复权)** · `POST /api/stock/min_adj`

获取指定股票的分钟级复权K线数据。支持1min/5min原始数据，以及15/30/60分钟聚合数据（从5分钟数据聚合），仅支持前复权(qfq)。
算法说明：
- recursive (递归复权): 默认算法。使用日期对应的复权因子进行递归计算。
- factor (涨跌幅复权): 使用每日的复权因子直接计算。
- 复权因子仅根据日期(YYYY-MM-DD)匹配，不区分具体的分钟时间。
- 15/30/60分钟数据按照A股交易时间对齐（上午09:30-11:30，下午13:00-15:00）。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string | 是 | 股票代码，例如 "600000.SH" |
| level | string | 是 | 数据级别: "1min", "5min", "15min", "30min", "60min" |
| start_time | string | 是 | 开始时间，格式 YYYY-MM-DD HH:MM:SS |
| end_time | string | 是 | 结束时间，格式 YYYY-MM-DD HH:MM:SS |
| algo | string | 否 | 复权算法: "recursive" (默认), "factor" |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "level": "1min",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12 13:45:00",
  "end_time": "2026-08-12 13:45:00",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| trade_time | string | 交易时间 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| vol | float | 成交量 |
| amount | float | 成交额 |

## diemeng_stock_adj_factor

**获取复权因子(涨跌幅算法)** · `POST /api/stock/adj_factor`

获取指定股票的复权因子数据。复权因子主要用于计算股票的前复权或后复权价格，消除除权除息（分红、配股、拆股等）带来的价格断层影响，保持股价走势的连续性。

计算公式：
- 后复权价格 = 原始价格 × 复权因子
- 前复权价格 = 原始价格 × 复权因子 ÷ 最新复权因子

优点：
1. 真实反映收益：能够真实反映投资者持有股票的实际收益情况。
2. 技术分析准确：消除价格跳空缺口，使均线、MACD等技术指标计算更准确。
3. 策略回测必备：量化交易回测时必须使用复权数据，否则会产生错误的买卖信号。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 "600000.SH"。最大支持100个。 |
| start_time | string | 是 | 开始日期 (YYYY-MM-DD) |
| end_time | string | 是 | 结束日期 (YYYY-MM-DD) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| trade_date | string | 交易日期 |
| adj_factor | float | 复权因子 |

## diemeng_stock_adj_factor_changes

**复权因子变更查询** · `POST /api/stock/adj_factor/changes`

查询指定日期是否有复权因子变更（即该日期是否为某股票的除权除息日）。
用于判断是否需要更新历史复权数据。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| date | string | 是 | 查询日期 (YYYY-MM-DD) |

调用参数示例：

```json
{
  "date": "2026-08-12"
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| data | string[] | 复权因子发生变更的股票代码列表 |

## diemeng_stock_history

**获取历史分时（未复权）** · `POST /api/stock/history`

获取指定股票的历史分时数据。支持1分钟、5分钟原始数据，以及15/30/60分钟数据。stock_code 为必填，不支持留空。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string | 是 | 股票代码，例如 "600000.SH"。必填且不能为空；仅支持单个股票代码。 |
| level | string | 是 | 数据级别: "1min", "5min", "15min", "30min", "60min" |
| start_time | string | 是 | 开始时间 (YYYY-MM-DD HH:MM:SS) |
| end_time | string | 是 | 结束时间 (YYYY-MM-DD HH:MM:SS) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "level": "1min",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12 13:45:00",
  "end_time": "2026-08-12 13:45:00",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| trade_time | string | 交易时间 |
| stock_code | string | 股票代码 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| vol | float | 成交量(单位手) |
| amount | float | 成交额 |

## diemeng_stock_daily_dump

**下载全市场当天全部分时** · `POST /api/stock/daily_dump`

下载全市场当天的全部1分钟或5分钟分时数据，或日线数据，或15/30/60分钟聚合数据。接口返回 GZIP 压缩的 JSON 文件。当 level=daily 时，返回数组格式的日线数据；当 level=1min/5min/15min/30min/60min 时，返回 Map<StockCode, List<Entry>> 格式的分时数据。15/30/60分钟数据从5分钟数据聚合，按照A股交易时间对齐（上午09:30-11:30，下午13:00-15:00）。数据量比较多，一个日期一天最多只能下载10次，超过后会被禁止下载该日期三天，请联系客服解封。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| date | string | 是 | 日期 (YYYY-MM-DD) |
| level | string | 否 | 级别 (daily/1min/5min/15min/30min/60min)。daily返回当天日线数据列表；1min/5min/15min/30min/60min返回分时数据Map，格式为 [时间(HH:MM), 开, 高, 低, 收, 量, 额]。 |

调用参数示例：

```json
{
  "date": "2026-08-12"
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| code | integer | 状态码 (200成功) |
| msg | string | 提示信息 |
| data | array \| object | 数据主体。当 level=daily 时，返回数组，每个元素包含完整日线字段；当 level=1min/5min 时，返回对象，Key是股票代码，Value是数组列表，每个数组元素为 [时间(HH:MM), 开, 高, 低, 收, 量, 额]。 |
| data[].trade_date | string | 交易日期 (仅daily级别) |
| data[].stock_code | string | 股票代码 (仅daily级别) |
| data[].open | float | 开盘价 (仅daily级别) |
| data[].high | float | 最高价 (仅daily级别) |
| data[].low | float | 最低价 (仅daily级别) |
| data[].close | float | 收盘价 (仅daily级别) |
| data[].pre_close | float | 昨收价 (仅daily级别) |
| data[].change | float | 涨跌额 (仅daily级别) |
| data[].pct_chg | float | 涨跌幅(%) (仅daily级别) |
| data[].vol | float | 成交量 (仅daily级别) |
| data[].amount | float | 成交额 (仅daily级别) |

## diemeng_stock_suspension

**获取股票停牌信息** · `GET /api/stock/suspension`

获取股票停牌信息。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string | 否 | 股票代码 |
| trade_date | string | 否 | 停牌日期 (YYYY-MM-DD) |
| page | integer | 否 | 页码 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| suspend_date | string | 停牌日期 |
| suspend_start_time | string | 当天停牌开始时间 |
| suspend_end_time | string | 当天停牌结束时间 |

## diemeng_stock_st_info

**获取ST信息** · `POST /api/stock/st_info`

获取指定股票的ST信息或按日期获取ST信息列表。股票代码或时间范围必须至少填一个。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 股票代码，例如 '600069.SH' |
| start_time | string | 否 | 开始日期 (YYYY-MM-DD) |
| end_time | string | 否 | 结束日期 (YYYY-MM-DD) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "stock_code": "600941.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| trade_date | string | 交易日期 |
| stock_name | string | 股票名称 |
| type | string | 类型 (例如 ST) |
| type_name | string | 类型名称 (例如 风险警示板) |

## diemeng_stock_macd

**获取MACD指标** · `POST /api/stock/macd`

从 ClickHouse 实时读取日K或分钟K线并计算 MACD，支持不复权和前复权。返回字段保留对应日K/分K字段，并追加 dif、dea、macd。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 是 | 股票代码，仅支持单只；数组只能传 1 个，例如 "600000.SH"。 |
| level | string | 否 | 级别：daily、1min、5min、15min、30min、60min，默认 daily。 |
| start_time | string | 是 | 开始时间；日级支持 YYYY-MM-DD，分钟级支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| end_time | string | 是 | 结束时间；日级支持 YYYY-MM-DD，分钟级支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| adjust | string | 否 | 复权模式：none(默认，不复权) 或 qfq(前复权)。 |
| fast_period | integer | 否 | 可省略，快线周期默认 12。 |
| slow_period | integer | 否 | 可省略，慢线周期默认 26，必须大于 fast_period。 |
| signal_period | integer | 否 | 可省略，信号线周期默认 9。 |
| page | integer | 否 | 可省略，默认 0；页码从 0 开始。 |
| page_size | integer | 否 | 可省略，默认 10000，最大 10000。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| trade_date / trade_time | string | 日级返回 trade_date，分钟级返回 trade_time |
| open/high/low/close/vol/amount | float | 与日K或分K接口一致的小数处理 |
| dif | float | DIF，保留 4 位小数 |
| dea | float | DEA，保留 4 位小数 |
| macd | float | MACD 柱，保留 4 位小数 |

## diemeng_stock_kdj

**获取KDJ指标** · `POST /api/stock/kdj`

从 ClickHouse 实时读取日K或分钟K线并计算 KDJ，支持不复权和前复权。返回字段保留对应日K/分K字段，并追加 k、d、j。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 是 | 股票代码，仅支持单只；数组只能传 1 个。 |
| level | string | 否 | 级别：daily、1min、5min、15min、30min、60min，默认 daily。 |
| start_time | string | 是 | 开始时间。 |
| end_time | string | 是 | 结束时间。 |
| adjust | string | 否 | 复权模式：none(默认) 或 qfq。 |
| kdj_period | integer | 否 | RSV 窗口，默认 9。 |
| k_period | integer | 否 | K 平滑周期，默认 3。 |
| d_period | integer | 否 | D 平滑周期，默认 3。 |
| page | integer | 否 | 页码，从 0 开始。 |
| page_size | integer | 否 | 每页数量，默认 10000，最大 10000。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| k | float | K 值，保留 2 位小数 |
| d | float | D 值，保留 2 位小数 |
| j | float | J 值，保留 2 位小数 |

## diemeng_stock_rsi

**获取RSI指标** · `POST /api/stock/rsi`

从 ClickHouse 实时读取日K或分钟K线并计算 RSI，支持不复权和前复权。返回字段保留对应日K/分K字段，并追加 rsi。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 是 | 股票代码，仅支持单只；数组只能传 1 个。 |
| level | string | 否 | 级别：daily、1min、5min、15min、30min、60min，默认 daily。 |
| start_time | string | 是 | 开始时间。 |
| end_time | string | 是 | 结束时间。 |
| adjust | string | 否 | 复权模式：none(默认) 或 qfq。 |
| rsi_period | integer | 否 | RSI 周期，默认 6。 |
| page | integer | 否 | 页码，从 0 开始。 |
| page_size | integer | 否 | 每页数量，默认 10000，最大 10000。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| rsi | float \| null | RSI 值，保留 2 位小数；预热不足时为 null |

## diemeng_stock_boll

**获取BOLL指标** · `POST /api/stock/boll`

从 ClickHouse 实时读取日K或分钟K线并计算 BOLL，支持不复权和前复权。返回字段保留对应日K/分K字段，并追加 boll_mid、boll_upper、boll_lower。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 是 | 股票代码，仅支持单只；数组只能传 1 个。 |
| level | string | 否 | 级别：daily、1min、5min、15min、30min、60min，默认 daily。 |
| start_time | string | 是 | 开始时间。 |
| end_time | string | 是 | 结束时间。 |
| adjust | string | 否 | 复权模式：none(默认) 或 qfq。 |
| boll_period | integer | 否 | 均线周期，默认 20。 |
| boll_std | number | 否 | 标准差倍数，默认 2。 |
| page | integer | 否 | 页码，从 0 开始。 |
| page_size | integer | 否 | 每页数量，默认 10000，最大 10000。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| boll_mid | float \| null | 中轨，保留 2 位小数；预热不足时为 null |
| boll_upper | float \| null | 上轨，保留 2 位小数；预热不足时为 null |
| boll_lower | float \| null | 下轨，保留 2 位小数；预热不足时为 null |

## diemeng_stock_ma

**获取均线指标** · `POST /api/stock/ma`

从 ClickHouse 实时读取日K或分钟K线并计算均线，支持不复权和前复权。返回字段保留对应日K/分K字段，并按周期追加 ma5、ma10、ma20 等字段。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 是 | 股票代码，仅支持单只；数组只能传 1 个。 |
| level | string | 否 | 级别：daily、1min、5min、15min、30min、60min，默认 daily。 |
| start_time | string | 是 | 开始时间。 |
| end_time | string | 是 | 结束时间。 |
| adjust | string | 否 | 复权模式：none(默认) 或 qfq。 |
| ma_periods | integer[] \| string | 否 | 均线周期，默认 [5,10,20,30,60]；也支持 "5,10,20"。 |
| page | integer | 否 | 页码，从 0 开始。 |
| page_size | integer | 否 | 每页数量，默认 10000，最大 10000。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| maN | float \| null | N 日/分钟均线，例如 ma5、ma10、ma20，保留 2 位小数；预热不足时为 null |

## diemeng_stock_mavol

**获取MAVOL指标** · `POST /api/stock/mavol`

从 ClickHouse 实时读取股票日K或分钟K线并计算 MAVOL。支持 daily/1min/5min/15min/30min/60min；股票支持不复权和前复权。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 是 | 股票代码，仅支持单个代码；数组只能传 1 个。 |
| level | string | 否 | 可省略，默认 daily；可选 daily、1min、5min、15min、30min、60min。 |
| start_time | string | 是 | 开始时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| end_time | string | 是 | 结束时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| adjust | string | 否 | 复权模式：none(默认，不复权) 或 qfq(前复权)。 |
| volType | string | 否 | 可省略，默认 share(股)；也可传 lot(手)。 |
| page | integer | 否 | 可省略，默认 0；页码从 0 开始。 |
| page_size | integer | 否 | 可省略，默认 10000，最大 10000。 |
| mavol_periods | integer[] \| string | 否 | 可省略，成交量均线周期默认 [5,10]；也支持 "5,10,20"。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "stock_code": "600941.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| stock_code | string | 股票代码 |
| trade_date / trade_time | string | 日级返回 trade_date，分钟级返回 trade_time |
| open/high/low/close/vol/amount | float | 对应K线行情字段 |
| mavolN | float \| null | N 日/分钟成交量均线，例如 mavol5、mavol10；预热不足时为 null |

## diemeng_index_history

**指数历史分时** · `POST /api/index/history`

获取指定指数的历史分时数据。支持1分钟、5分钟原始数据，以及15/30/60分钟合成数据。指数代码和日期至少需要填写一个。本接口仅包含交易所发布的指数，不包含中证指数公司发布的指数，例如中证1000；中证500等交易所指数可以查询。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string \| string[] | 否 | 指数代码，例如 "000001.SH"。支持单个或数组，最大支持100个。 |
| level | string | 否 | 数据级别: "1min", "5min", "15min", "30min", "60min" (默认1min) |
| start_time | string | 否 | 开始时间 (YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS) |
| end_time | string | 否 | 结束时间 (YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS) |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000，最大10000) |

调用参数示例：

```json
{
  "index_code": "000001.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| trade_time | string | 交易时间 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| amount | float | 成交额 |

## diemeng_index_daily

**指数历史日K** · `POST /api/index/daily`

获取指数历史日K数据。单次最多调取8000行记录，可以通过设置 start_date 和 end_date 进行区间查询。本接口仅包含交易所发布的指数，不包含中证指数公司发布的指数，例如中证1000；中证500等交易所指数可以查询。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| stock_code | string \| string[] | 否 | 指数代码，来源指数基础信息接口 |
| start_date | string | 否 | 开始日期 (YYYY-MM-DD) |
| end_date | string | 否 | 结束日期 (YYYY-MM-DD) |
| page | integer | 否 | 页码 (默认0) |
| page_size | integer | 否 | 每页数量 (默认2000, 最大8000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| ts_code | string | TS指数代码 |
| trade_date | string | 交易日 |
| open | float | 开盘点位 |
| high | float | 最高点位 |
| low | float | 最低点位 |
| close | float | 收盘点位 |
| pre_close | float | 昨日收盘点 |
| change | float | 涨跌点 |
| pct_chg | float | 涨跌幅(%) |
| vol | float | 成交量(手) |
| amount | float | 成交额(元) |

## diemeng_index_weight

**获取指数成分和权重** · `POST /api/index/weight`

获取指数成分和权重（月度数据）。index_code 必传；stock_code 用于筛选成分股代码（对应底层 con_code 字段）。不传 trade_date 时默认返回该指数最新月份数据，传 trade_date 时返回指定日期所在月份数据。接口仅按 apiKey 是否具备接口权限进行校验。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string | 是 | 指数代码，例如 000300.SH |
| stock_code | string \| string[] | 否 | 成分股代码，支持单个或数组；用于筛选 con_code |
| trade_date | string | 否 | 交易日期，支持 YYYY-MM 或 YYYY-MM-DD；查询时只按年和月过滤；不传默认最新月份 |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认2000，最大10000) |

调用参数示例：

```json
{
  "index_code": "000300.SH",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| stock_code | string | 成分股代码（由 con_code 映射） |
| trade_date | string | 交易日期（按月存储，返回该月第一天） |
| weight | float | 权重 |

## diemeng_index_macd

**获取MACD指标** · `POST /api/index/macd`

从 ClickHouse 实时读取指数日K或分钟K线并计算 MACD。支持 daily/1min/5min/15min/30min/60min；指数当前按不复权行情计算。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string \| string[] | 是 | 指数代码，仅支持单个代码；数组只能传 1 个。 |
| level | string | 否 | 可省略，默认 daily；可选 daily、1min、5min、15min、30min、60min。 |
| start_time | string | 是 | 开始时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| end_time | string | 是 | 结束时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| volType | string | 否 | 可省略，默认 share(股)；也可传 lot(手)。 |
| page | integer | 否 | 可省略，默认 0；页码从 0 开始。 |
| page_size | integer | 否 | 可省略，默认 10000，最大 10000。 |
| fast_period | integer | 否 | 可省略，快线周期默认 12。 |
| slow_period | integer | 否 | 可省略，慢线周期默认 26，必须大于 fast_period。 |
| signal_period | integer | 否 | 可省略，信号线周期默认 9。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "index_code": "000001.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| trade_date / trade_time | string | 日级返回 trade_date，分钟级返回 trade_time |
| open/high/low/close/amount | float | 分钟线行情字段；日线另含 vol |
| dif | float | DIF，保留 4 位小数 |
| dea | float | DEA，保留 4 位小数 |
| macd | float | MACD 柱，保留 4 位小数 |

## diemeng_index_kdj

**获取KDJ指标** · `POST /api/index/kdj`

从 ClickHouse 实时读取指数日K或分钟K线并计算 KDJ。支持 daily/1min/5min/15min/30min/60min；指数当前按不复权行情计算。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string \| string[] | 是 | 指数代码，仅支持单个代码；数组只能传 1 个。 |
| level | string | 否 | 可省略，默认 daily；可选 daily、1min、5min、15min、30min、60min。 |
| start_time | string | 是 | 开始时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| end_time | string | 是 | 结束时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| volType | string | 否 | 可省略，默认 share(股)；也可传 lot(手)。 |
| page | integer | 否 | 可省略，默认 0；页码从 0 开始。 |
| page_size | integer | 否 | 可省略，默认 10000，最大 10000。 |
| kdj_period | integer | 否 | 可省略，RSV 窗口默认 9。 |
| k_period | integer | 否 | 可省略，K 平滑周期默认 3。 |
| d_period | integer | 否 | 可省略，D 平滑周期默认 3。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "index_code": "000001.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| trade_date / trade_time | string | 日级返回 trade_date，分钟级返回 trade_time |
| open/high/low/close/amount | float | 分钟线行情字段；日线另含 vol |
| k | float | K 值，保留 2 位小数 |
| d | float | D 值，保留 2 位小数 |
| j | float | J 值，保留 2 位小数 |

## diemeng_index_rsi

**获取RSI指标** · `POST /api/index/rsi`

从 ClickHouse 实时读取指数日K或分钟K线并计算 RSI。支持 daily/1min/5min/15min/30min/60min；指数当前按不复权行情计算。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string \| string[] | 是 | 指数代码，仅支持单个代码；数组只能传 1 个。 |
| level | string | 否 | 可省略，默认 daily；可选 daily、1min、5min、15min、30min、60min。 |
| start_time | string | 是 | 开始时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| end_time | string | 是 | 结束时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| volType | string | 否 | 可省略，默认 share(股)；也可传 lot(手)。 |
| page | integer | 否 | 可省略，默认 0；页码从 0 开始。 |
| page_size | integer | 否 | 可省略，默认 10000，最大 10000。 |
| rsi_period | integer | 否 | 可省略，RSI 周期默认 6。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "index_code": "000001.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| trade_date / trade_time | string | 日级返回 trade_date，分钟级返回 trade_time |
| open/high/low/close/amount | float | 分钟线行情字段；日线另含 vol |
| rsi | float \| null | RSI 值，保留 2 位小数；预热不足时为 null |

## diemeng_index_boll

**获取BOLL指标** · `POST /api/index/boll`

从 ClickHouse 实时读取指数日K或分钟K线并计算 BOLL。支持 daily/1min/5min/15min/30min/60min；指数当前按不复权行情计算。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string \| string[] | 是 | 指数代码，仅支持单个代码；数组只能传 1 个。 |
| level | string | 否 | 可省略，默认 daily；可选 daily、1min、5min、15min、30min、60min。 |
| start_time | string | 是 | 开始时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| end_time | string | 是 | 结束时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| volType | string | 否 | 可省略，默认 share(股)；也可传 lot(手)。 |
| page | integer | 否 | 可省略，默认 0；页码从 0 开始。 |
| page_size | integer | 否 | 可省略，默认 10000，最大 10000。 |
| boll_period | integer | 否 | 可省略，均线周期默认 20。 |
| boll_std | number | 否 | 可省略，标准差倍数默认 2。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "index_code": "000001.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| trade_date / trade_time | string | 日级返回 trade_date，分钟级返回 trade_time |
| open/high/low/close/amount | float | 分钟线行情字段；日线另含 vol |
| boll_mid | float \| null | 中轨；预热不足时为 null |
| boll_upper | float \| null | 上轨；预热不足时为 null |
| boll_lower | float \| null | 下轨；预热不足时为 null |

## diemeng_index_ma

**获取均线指标** · `POST /api/index/ma`

从 ClickHouse 实时读取指数日K或分钟K线并计算 均线。支持 daily/1min/5min/15min/30min/60min；指数当前按不复权行情计算。所有标记为可选且带默认值的参数均可省略。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string \| string[] | 是 | 指数代码，仅支持单个代码；数组只能传 1 个。 |
| level | string | 否 | 可省略，默认 daily；可选 daily、1min、5min、15min、30min、60min。 |
| start_time | string | 是 | 开始时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| end_time | string | 是 | 结束时间；支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS。 |
| volType | string | 否 | 可省略，默认 share(股)；也可传 lot(手)。 |
| page | integer | 否 | 可省略，默认 0；页码从 0 开始。 |
| page_size | integer | 否 | 可省略，默认 10000，最大 10000。 |
| ma_periods | integer[] \| string | 否 | 可省略，均线周期默认 [5,10,20,30,60]；也支持 "5,10,20"。 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "index_code": "000001.SH",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| trade_date / trade_time | string | 日级返回 trade_date，分钟级返回 trade_time |
| open/high/low/close/amount | float | 分钟线行情字段；日线另含 vol |
| maN | float \| null | N 日/分钟均线，例如 ma5、ma10、ma20；预热不足时为 null |

## diemeng_tdx_blocks

**获取 tdx 板块列表** · `GET /api/tdx/blocks`

获取 tdx 板块列表数据。支持按板块名称筛选。板块类型（block_type）为必传参数。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| block_name | string | 否 | 板块名称 (例如: 5G概念) |
| block_type | integer | 是 | 板块类型 (0:行业板块, 1:风格板块, 2:概念板块, 3:指数板块) |
| page | integer | 否 | 页码 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "block_type": 0,
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| block_code | string | 板块代码 |
| block_name | string | 板块名称 |
| block_type | integer | 板块类型 (0:行业, 1:风格, 2:概念, 3:指数) |

## diemeng_tdx_block_stocks

**获取 tdx 板块成分股** · `GET /api/tdx/block_stocks`

获取 tdx 板块成分股数据。支持按板块代码或股票代码筛选，如果不传参数则返回所有数据。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| block_code | string | 否 | 板块代码 (例如: 880506.TDX) |
| stock_code | string | 否 | 股票代码 (例如: 000063.SZ) |
| page | integer | 否 | 页码 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| block_code | string | 板块代码 |
| block_name | string | 板块名称 |
| block_type | integer | 板块类型 (0:行业, 1:风格, 2:概念, 3:指数) |
| stock_code | string | 股票代码 |

## diemeng_tdx_daily

**获取 tdx 板块日K** · `GET /api/tdx/daily`

获取 tdx 板块指数日K线数据。支持按板块代码查询历史数据，或按日期查询当日所有板块数据。返回结果按 trade_date 升序（时间顺序）排列。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| board_code | string | 否 | 板块代码 (例如: 880471或880471.TDX) |
| trade_date | string | 否 | 交易日期 (YYYY-MM-DD) |
| start_date | string | 否 | 开始日期 (YYYY-MM-DD) |
| end_date | string | 否 | 结束日期 (YYYY-MM-DD) |
| page | integer | 否 | 页码 (默认0) |
| page_size | integer | 否 | 每页数量 (默认100) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| board_code | string | 板块代码 |
| trade_date | string | 交易日期 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| vol | float | 成交量 |
| amount | float | 成交额 |

## diemeng_tdx_minute

**获取 tdx 板块分钟K线** · `POST /api/tdx/minute`

获取 tdx 板块分钟K线数据。1min 读取去重后的明细数据；5min/15min/30min/60min 由 1 分钟数据按交易时段聚合。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| board_code | string \| string[] | 否 | 板块代码，例如 880201 或 880201.TDX |
| level | string | 否 | 分钟周期：1min/5min/15min/30min/60min，默认 1min |
| start_time | string | 是 | 开始时间，支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS |
| end_time | string | 是 | 结束时间，支持 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS |
| page | integer | 否 | 页码，从 0 开始 |
| page_size | integer | 否 | 每页数量，默认 10000，最大 10000 |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| board_code | string | 带 .TDX 后缀的板块代码 |
| board_name | string | 板块名称 |
| trade_time | string | 交易时间 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| vol | float | 成交量 |
| amount | float | 成交额 |
| pct_change | float | 涨跌幅 |
| amplitude | float | 振幅 |

## diemeng_dc_blocks

**获取 dc 板块列表** · `POST /api/dc/blocks`

获取 dc 板块列表数据。支持按板块代码、板块类型、板块名称筛选。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| block_code | string \| string[] | 否 | 板块代码 |
| block_type | string | 否 | 板块类型，例如：概念板块、行业板块、地域板块 |
| block_name | string | 否 | 板块名称（模糊匹配） |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认2000，最大8000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| block_code | string | 板块代码 |
| block_name | string | 板块名称 |
| block_type | string | 板块类型 |
| level | string | 行业层级 |

## diemeng_dc_block_stocks

**获取 dc 板块成分股** · `POST /api/dc/block_stocks`

获取 dc 板块成分股数据。支持按 block_code、trade_date、stock_code 任意组合筛选；如果三个条件都不传，默认返回最新交易日数据。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| block_code | string \| string[] | 否 | 板块代码 |
| trade_date | string | 否 | 交易日期，支持 YYYY-MM-DD 或 YYYYMMDD |
| stock_code | string \| string[] | 否 | 股票代码，例如 600000.SH |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认2000，最大8000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| block_code | string | 板块代码 |
| trade_date | string | 交易日期 |
| stock_code | string | 股票代码 |
| stock_name | string | 股票名称 |

## diemeng_dc_daily

**获取 dc 板块日K** · `POST /api/dc/daily`

获取 dc 板块日K数据。block_code 和 trade_date 二选一即可，支持同时传入做交集筛选。返回结果按 trade_date 升序（时间顺序）排列。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| block_code | string \| string[] | 否 | 板块代码 |
| trade_date | string | 否 | 交易日期，支持 YYYY-MM-DD 或 YYYYMMDD |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认2000，最大8000) |

调用参数示例：

```json
{
  "block_code": "BK0428.DC",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| block_code | string | 板块代码 |
| trade_date | string | 交易日期 |
| open | float | 开盘点位 |
| high | float | 最高点位 |
| low | float | 最低点位 |
| close | float | 收盘点位 |
| change | float | 涨跌点位 |
| pct_change | float | 涨跌幅(%) |
| vol | float | 成交量 |
| amount | float | 成交额 |
| swing | float | 振幅 |
| turnover_rate | float | 换手率 |

## diemeng_index_ths_sector_categories

**获取 ths 板块分类** · `POST /api/index/ths_sector_categories`

获取 ths 板块分类数据。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| type | string | 否 | 指数类型：N-概念指数 I-行业指数 R-地域指数 S-ths 特色指数 ST-ths 风格指数 TH-ths 主题指数 BB-ths 宽基指数 |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认1000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| name | string | 指数名称 |
| count | integer | 成分股数量 |
| exchange | string | 交易所 |
| list_date | string | 上市日期 |
| type | string | 指数类型 |
| code | string | 简码 |

## diemeng_index_ths_constituent_stocks

**获取 ths 成分股** · `POST /api/index/ths_constituent_stocks`

获取 ths 成分股数据。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| index_code | string | 否 | 指数代码，如 882001.TI |
| stock_code | string \| string[] | 否 | 股票代码 |
| page | integer | 否 | 页码，从0开始 (默认0) |
| page_size | integer | 否 | 每页数量 (默认1000) |

调用参数示例：

```json
{
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| index_code | string | 指数代码 |
| code | string | 简码 |
| stock_code | string | 股票代码 |
| stock_name | string | 股票名称 |
| plate_name | string | 板块名称 |

## diemeng_index_ths_daily

**获取 ths 日线数据** · `POST /api/index/ths_daily`

获取 ths 指数历史日K线数据。返回结果按 trade_date 升序（时间顺序）排列。该接口属于 THS 独立模块，支持首次开通及 30/90/180/366 天续费。

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| ths_code | string \| string[] | 否 | ths 指数代码 (例如: 881123.TI) |
| start_time | string | 是 | 开始日期 (YYYY-MM-DD) |
| end_time | string | 是 | 结束日期 (YYYY-MM-DD) |
| page | integer | 否 | 页码 (默认0) |
| page_size | integer | 否 | 每页数量 (默认10000) |

调用参数示例：

```json
{
  "end_time": "2026-08-12",
  "start_time": "2026-08-12",
  "page": 0,
  "page_size": 1
}
```

返回字段（沿用上游定义）：

| 字段 | 类型 | 含义及单位 |
| --- | --- | --- |
| ths_code | string | 指数代码 |
| trade_date | string | 交易日期 |
| open | float | 开盘价 |
| high | float | 最高价 |
| low | float | 最低价 |
| close | float | 收盘价 |
| pre_close | float | 昨收价 |
| avg_price | float | 均价 |
| change | float | 涨跌额 |
| pct_change | float | 涨跌幅(%) |
| vol | float | 成交量 |
| turnover_rate | float | 换手率 |

## 验证

本地 fast 测试覆盖全部44接口的路由、方法、鉴权头、参数透传、特殊约束、分页和错误处理；所有数据使用模拟响应和临时文件。另有明确 opt-in 的 `integration` 测试用于按类别抽样，默认不联网，不自动下载全市场数据。

```powershell
pwsh -NoProfile -Command "& ./scripts/verify.ps1 -Tier fast -GoPackage @('./internal/diemeng','./cmd/minute-api')"
```

实际联网抽样（明确需要联网时才执行）：

```powershell
$env:GO_STOCK_DIEMENG_LIVE = '1'
go test -tags integration ./internal/diemeng -run '^TestLiveSamples$' -count=1 -v
```

实际抽样状态见 [蝶梦 MCP 验证记录](蝶梦MCP验证记录.md)。模拟测试通过不等于全部接口均已联网验证，空结果也不保证该日期有数据。
