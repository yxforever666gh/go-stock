# go-stock

![go-stock social preview](./docs/assets/social-preview.png)

Go-Stock 是基于 Go、Vue 3、Naive UI 和 SQLite 的 Windows 本地股票行情与 AI 研究工具。

[Releases](https://github.com/yxforever666gh/go-stock/releases) | [发布说明](./RELEASE_NOTES.md)

> 本仓库基于公开项目 [`ArvinLovegood/go-stock`](https://github.com/ArvinLovegood/go-stock) 演化，不是原作者官方仓库。

## 核心流程

1. 卖出、程序启动或检测到资金缺口时写入持久化事件；交易日 `09:35—14:25` 自动执行完整分析，连续两分钟内卖出合并为一轮。
2. 可部署资金为 `现金 - 待买预留 - max(净资产 × 10%, 5万元)`；不足 5 万元时保留卖出事件并等待资金累积。
3. AI 依次完成大盘、板块、个股与严格 JSON 最终决策，将候选分为立即买入、等待和放弃；单轮最多立即买入 2 只、等待 5 只。
4. 系统再次校验实时价格、价格区间、行情新鲜度、停牌/涨跌停、现金缓冲和重复股票；单笔含费不超过 5 万元，一手已超限的股票不自动买入。
5. 等待候选不预留现金。若完成后仍有资金缺口，30 分钟后重新执行完整分析；跨越交易窗口的结论作废并于下一交易日重新分析。
6. 每分钟生命周期扫描、每只持仓从实际复查完成时间独立计算的 15 分钟复查及收盘账户快照继续运行；研究中心2的隔夜强势策略不受影响。

研究中心2的任务启动窗口为交易日 `[09:55,13:00)`：09:55正常运行使用09:50—09:55的五个已闭合交易分钟，之后恢复或补位使用实际启动点之前最近5个已闭合交易分钟；午休期间使用上午最后5个交易分钟。每轮最多保留3只主选和3只备选，选股时距涨停不足1.5%、执行时距涨停不足1%的标的不可成交，主选失败后按排名递补；当日仍不足三笔时继续补位至13:00。当前策略版本为 `research2-trailing5-v9`，证据版本为 `research2-trailing5-v7`；其 12,000 元账户、推荐、成交与收益完全独立，现金按尚缺席位预留并以 100 股整手向下取整。

## 当前能力

- 证据按 `available_at` 判断研究截止，`collected_at` 仅用于审计；实验性市场与题材证据默认关闭。
- 每日题材按“观察 → 发酵 → 加速 → 分歧 → 退潮”冻结快照，来源可独立降级；基金和 ETF 不进入荐股或模拟交易。
- Prompt、证据、工具清单和模型响应以不可覆盖的审计载荷保存；研究回放不会回填或改写原研究结果。
- 知识库支持 TXT、Markdown 和 PDF 文本，只有人工批准且未失效的版本才参与检索；外部文本始终按不可信线索处理。
- 市场页提供热词突增、市场宽度、资金流和期指等数据；来源覆盖不足时明确降级，不用虚假零值代替缺失数据。
- 基金页提供场外基金排行和场内 ETF 自选、行情与详情；统一图表支持股票、指数和 ETF 的多周期、复权、指标与绘图工具。

## 模拟账户与净收益

- 初始资金：固定 `500,000 元`，不再计划或执行追加注资
- 历史重复仓位保持不变；新买入禁止与已有持仓、待买任务或本轮有效观察股票重复
- 单只股票含费用现金支出上限：`50,000 元`，按合法整手向下取整；一手已超过上限时跳过
- 资金保留额：`max(净资产 × 10%, 50,000 元)`；等待候选不占用现金
- 普通沪深 A 股：100 股整数倍；科创板首次申报至少 200 股
- 成本：佣金、最低佣金、卖出印花税、过户费和双边滑点
- 净收益额：`现金 + 持仓按可卖出净值估值 - 500,000 元固定初始资金`
- 策略净收益率：使用固定本金口径的单位净值与时间加权收益率（TWR）

已平仓交易采用实际净现金流；未平仓持仓按最新价扣除预计卖出成本估值。不计算或展示基准收益率、超额收益率和 XIRR。

## 界面结构

- 市场行情：保留快讯、指数、行业排名、资金流、龙虎榜、公告、研报、热点、选股与名站信息；热点页增加“每日炒作题材”视图，展示题材阶段、冻结快照、成分股与催化来源冲突。
- 基金：异步提供基金自选、场外基金排行和场内 ETF 排名、搜索、自选与详情；ETF 详情复用统一图表，展示来源时间与降级状态。
- 研究中心1：保留原研究页签并增加 `知识库与记忆` 管理入口，可导入、检索、审批文档版本和记忆候选。
- 研究中心2：提供独立的 `AI分析报告`、`股票推荐记录`、`股票收益率` 和 `设置` 页签；设置页显示隔夜强势自动策略开关及研究中心2专属报告邮件配置。
- 设置：不再单独占用侧边栏入口；通用设置、数据接口和模型配置继续共享，两个中心的策略控制项互不串页。
- 资金补位策略：启用后由卖出、启动恢复和资金缺口自动触发；目标利用率、单轮买入数与重分析间隔可配置，固定分析时间和手动分析入口已移除。

## AI 配置

AI 模型配置表从上到下即为回退顺序，关闭的模型会被跳过。研究调用采用流式接收：连续 300 秒没有有效推理、心跳、状态或正文事件才超时，活跃推理不限制总时长。瞬态错误在当前模型最多尝试 5 次，不可恢复错误立即切换下一模型。API Key 仍由用户在本机设置，迁移不会复制或生成密钥。

Responses API 优先使用 `previous_response_id` 延续单股会话；中转站不支持续接时，只重发该股票本地保存并压缩后的历史消息。

## 数据库

当前 App 版本和主/分钟库 schema 以 `internal/releaseinfo/release_manifest.json`、运行时 `/readyz` 与 Release Notes 为准。已发布迁移保持不可改写，并在升级前创建可校验的数据库归档。

## 本地运行

要求 Go 1.25+、Node.js 20+。

```powershell
cd frontend
npm install
npm run build
cd ..
go run .
```

默认监听 `http://127.0.0.1:34115`，可用 `--web-addr` 修改监听地址。

已部署的 Windows 发布物可直接双击 `启动项目.cmd`；如只需启动服务、不打开浏览器，可运行：

```powershell
.\启动项目.cmd -NoBrowser
```

网络来源审计已从主程序移到独立开发工具，需要时运行：

```powershell
.\scripts\network-audit.ps1
```

磁盘清理默认只预览，并验证生产双库、保留发布和逐级回滚归档；确认清单后才显式执行：

```powershell
.\scripts\cleanup-workspace.ps1
.\scripts\cleanup-workspace.ps1 -Apply
```

清理会按脚本内的恢复策略保留必要发布物与迁移归档。生产数据库、当前日志、Git 数据和前端依赖始终不属于清理目标。

## 验证

日常开发默认只运行与改动直接相关的快速验证：

```powershell
.\scripts\verify.ps1 -Tier fast -GoPackage ./backend/research2
.\scripts\verify.ps1 -Tier fast -FrontendTest src/utils/number-format.test.mjs
```

跨越一个完整领域时运行领域验证；只有正式发布或明确要求完整门禁时才运行发布验证：

```powershell
.\scripts\verify.ps1 -Tier domain -Domain research2
.\scripts\verify.ps1 -Tier release
```

`fast`、`domain` 和 `release` 验证均关闭真实网络和集成测试开关；普通测试必须自行使用临时 fixture。真实来源、浏览器、邮件和生产数据库验证不属于日常开发入口。详细范围与停止条件见 [`AGENTS.md`](./AGENTS.md)。

只有明确需要真实来源合同时才启用 integration build tag；测试仍必须使用临时数据库：

```powershell
$env:GO_STOCK_LIVE_MARKET_NEWS = '1'
go test -tags integration -run '^TestRefreshResearchNewsLiveContract$' ./backend/data
Remove-Item Env:GO_STOCK_LIVE_MARKET_NEWS
$env:GO_STOCK_LIVE_EASTMONEY = '1'
go test -tags integration -run '^TestResearch2FullMarketLiveContract$' ./backend/data
Remove-Item Env:GO_STOCK_LIVE_EASTMONEY
```

## 许可证

许可证与第三方来源说明见 [LICENSE](./LICENSE)。股票数据和 AI 输出仅用于研究与软件验证，不构成投资建议。
