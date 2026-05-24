# go-stock

![go-stock social preview](./docs/assets/social-preview.png)

基于 Go、Wails、Vue 3 和 Naive UI 的本地优先股票分析工具，支持桌面模式和本地 Web 模式，聚焦于自选股、市场资讯、AI 分析报告、推荐收益跟踪与邮件报告。

[Releases](https://github.com/yxforever666gh/go-stock/releases) | [更新日志](./CHANGELOG.md) | [公开检查清单](./PUBLIC_RELEASE_CHECKLIST.md) | [仓库展示信息](./PUBLIC_REPO_METADATA.md)

> 来源说明：当前仓库基于公开项目 [`ArvinLovegood/go-stock`](https://github.com/ArvinLovegood/go-stock) 改编整理而来，不是原作者官方仓库；当前公开版只保留适合继续协作与二次开发的内容。

## 项目定位

- 本地优先：核心数据、运行时目录与大部分分析链路在本地完成，不依赖必须在线的中心化控制台
- 双模式运行：同时支持 Wails 桌面壳与本地 Web 模式
- AI 工作台：覆盖市场资讯总结、个股分析、推荐记录与收益率跟踪
- 工程化可维护：应用层 API、前端路由与重型逻辑已完成分层收敛，更适合继续迭代

## 第一眼能力

- 自选股与分组管理，支持个股详情、K 线与分时相关视图
- 市场行情、全球指数、行业排名与资讯总结
- AI 分析报告历史、推荐记录与推荐收益率追踪
- 邮件报告、运行时任务、SQLite 本地存储
- OpenAI 兼容接口、DeepSeek、Ollama、LM Studio、火山方舟等模型接入

当前仓库是一个持续迭代中的业务仓库，README 以“如何运行、如何构建、当前实际包含什么能力”为主，不再复用外部宣传型说明。

## 项目来源

- 当前仓库与公开项目 `ArvinLovegood/go-stock` 存在演化关系，公开前建议再次核对来源说明、版权归属和许可证兼容性。
- 当前 `1.2.8` 公开快照已主动移除个人赞赏码、联系方式、构建产物、运行数据库和本地私有接入说明，避免把不适合公开仓库的内容继续带到云端。
- 当前公开版不再保留二维码打赏、赞助码校验和赞助分流下载入口，只保留公开仓库版本说明与来源说明。
- 如果你需要追溯原始公开项目，请直接访问上面的原作者仓库链接；当前仓库维护的是公开整理版的后续改动。
- 如果你准备把当前仓库从私有切换为公开，建议先阅读 [PUBLIC_RELEASE_CHECKLIST.md](./PUBLIC_RELEASE_CHECKLIST.md) 并逐项确认。

## 1.2.8 近期重点

- `市场行情` 页完成新一轮分栏重构，快讯、指数、资金流、龙虎榜、公告、研报、热门题材、指标选股和名站优选等栏目改成更清晰的独立 tab 结构。
- 顶层菜单和运行时事件从 `App.vue` 中进一步拆开，市场页与研究页切换统一走模块化入口，降低壳层与业务页耦合。
- `收益率统计` 页补齐独立的按交易日 overview 数据链路，用来生成全库 `累计走势` 与 `单日变化` 图表，不再依赖明细页拼装统计图。
- AI 推荐收益率重算主链路继续拆分，激活判定、提供方选择、重算调度、概览统计和写库职责更清楚，后续维护 strict 收益率更容易做定向修复。
- 应用运行时状态与发布工程化同步收口，新增 CI 工作流，并把 README、更新日志、Release Notes、About 页和版本元数据统一切到 `1.2.8`。

## AI 推荐收益率激活规则

- AI 推荐记录现在区分“人类展示字段”和“机器执行字段”。
- `recommendReason / buySignal / remarks` 继续用于人类阅读；收益率 strict 模式不再依赖这些自然语言直接判定激活时点。
- 机器执行字段统一为 `activationRuleJson`，至少包含：
  - `signalType`
  - `evaluationWindow`
  - `baseline`
  - `operator`
  - `thresholdValue`
  - `confirmBars`
  - `expireTradeDays`
- 如果是区间触发，还会包含 `thresholdMax`；如果涉及量能阈值，还会包含 `volumeRatio / volumeWindow / volumeMetric`。

最小示例：

```json
{
  "signalType": "price_range_with_volume",
  "evaluationWindow": "5m",
  "baseline": "avg_amount_5x5m",
  "operator": ">=",
  "thresholdValue": 9.42,
  "thresholdMax": 9.56,
  "volumeRatio": 1.2,
  "confirmBars": 1,
  "volumeWindow": 5,
  "volumeMetric": "amount",
  "expireTradeDays": 5
}
```

对应的人类描述可以写成：

```text
价格触发：未来 5 个交易日内股价进入 9.42-9.56 主买入区；
量能触发：5 分钟成交额不低于近 5 个 5 分钟均额的 1.2 倍；
逻辑触发：核心催化未证伪且板块未转弱。
```

注意：

- `放量 / 缩量 / 强势 / 承接 / 不追` 这类模糊词不会再直接参与收益率计算。
- 如果 AI 只能输出自然语言、无法输出结构化规则，该记录会被标记为“未结构化”，不会进入 strict 收益率统计。

## 当前能力

- 股票自选与分组管理
  - 自选股查看、分组切换、个股详情、K 线与分时相关视图
- 市场行情
  - 市场快讯、全球股指、重大指数、行业排名等页面
  - 市场资讯 AI 总结与推荐补写链路
- 研究中心
  - AI 分析报告历史查看
  - 股票推荐记录与推荐收益率记录
  - 报告支持 Markdown 预览、复制、保存、社区分享
- AI 能力
  - 支持 OpenAI 兼容接口、DeepSeek、Ollama、LM Studio、火山方舟等模型接入
  - 支持 AI 对话页、市场资讯总结、推荐收益相关链路
- 邮件报告
  - 支持测试邮件、收益率 CSV、最新 AI 分析报告发送
  - 已补充邮件发送日志与审计能力
- 运行时与任务
  - 定时任务、运行时目录初始化、SQLite 数据存储
  - 桌面模式与 `--web` 模式共用核心业务逻辑

## 技术栈

- 后端
  - Go `1.25`
  - Wails `v2`
  - GORM + SQLite
  - Zap 日志
  - cron 定时任务
- 前端
  - Vue 3
  - Vite
  - Naive UI
  - ECharts
  - md-editor-v3

## 项目结构

- `app.go`
  - Wails 暴露给前端的主要应用入口
- `backend/`
  - 数据抓取、推荐收益、市场资讯、配置读写等核心业务
- `internal/service/`
  - 运行期 service 拆分
- `frontend/`
  - Vue 前端工程
- `runtime/`
  - 默认运行时目录，包含数据库、词典等运行文件
- `scripts/`
  - 桌面/Web 模式开发、启动、停止、构建脚本

## 环境要求

### 通用

- Go 1.25+
- Node.js 18+ 与 npm

### 桌面模式额外依赖

- Wails CLI
  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```

### Linux 桌面依赖

Ubuntu / Debian 可先安装：

```bash
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

如果发行版没有 `libwebkit2gtk-4.1-dev`，可改用 `libwebkit2gtk-4.0-dev`。

## 安装前端依赖

```bash
cd frontend
npm install
```

## 运行方式

### 1. Linux 桌面开发模式

```bash
./scripts/dev-linux.sh
```

说明：

- 该脚本会检查 `wails`、`pkg-config`、GTK/WebKit 依赖
- 默认使用低噪声模式启动 Wails 开发环境
- 如果改动了 Wails 绑定接口，可额外手动执行一次：

```bash
wails dev -m -s
```

### 2. 本地 Web 联调模式

适合前后端联调，前端走 Vite 热更新。

```bash
./scripts/dev-web.sh
```

默认：

- 后端 API：`http://127.0.0.1:34115`
- 前端开发服务：`http://127.0.0.1:5173`

### 3. 本地 Web 常驻模式

适合本机浏览器访问，不启动桌面壳。

```bash
./scripts/restart.sh start
```

默认地址：

- `http://127.0.0.1:34115`

停止服务：

```bash
./scripts/restart.sh stop
```

查看、暂停、恢复、重启：

```bash
bash scripts/webctl.sh status
bash scripts/webctl.sh pause
bash scripts/webctl.sh resume
bash scripts/restart.sh restart
```

### 4. Web 模式推荐启动流程

如果前端改过，先重新构建前端，再启动 Web 服务：

```bash
cd frontend
npm run build
cd ..
./scripts/restart.sh start
```

原因：

- Web 模式会使用 `frontend/dist` 静态资源
- 不先构建就启动，浏览器可能看到旧页面

## 常用环境变量

### Web 模式

- `GO_STOCK_WEB_ADDR`
  - Web 监听地址，默认 `127.0.0.1:34115`
- `GO_STOCK_WEB_FOREGROUND`
  - 设为 `1` 时前台运行
- `GO_STOCK_SKIP_BUILD`
  - 设为 `1` 时跳过 `restart.sh start` 里的 Go 构建
- `GO_STOCK_RUNTIME_DIR`
  - 指定运行时目录，默认 `./runtime`
- `GO_STOCK_DB_PATH`
  - 指定数据库路径或 DSN
- `GO_STOCK_LOG_LEVEL`
  - 应用日志级别
- `GO_STOCK_DB_LOG_LEVEL`
  - 数据库日志级别

示例：

```bash
GO_STOCK_WEB_ADDR=127.0.0.1:34116 ./scripts/restart.sh start
```

### 公开版可选服务覆盖

- `GO_STOCK_BASEINFO_BASE_URL`
  - 覆盖基础股票资料同步地址，默认指向当前公开仓库的 `build/*.json`
- `GO_STOCK_SHARE_UPLOAD_URL`
  - 配置分析结果分享服务地址；未配置时，“分享分析”会提示改用本地导出
- `GO_STOCK_SYNC_NEWS_URL_TEMPLATE`
  - 配置资讯同步接口模板，使用 `{since}` 占位；未配置时不会主动连接外部资讯同步服务

### 分钟线 / 数据源相关

- 现在推荐优先在“设置页 -> 分钟线数据源”里配置。
- 默认模式是“公共源优先”，适合实时与短周期分钟线。
- 公共源支持按开关启用：
  - `AKShare`
  - `新浪分钟线`
  - `腾讯分钟线`
- `AKShare` 支持来源偏好：
  - `自动`
  - `新浪`
  - `东方财富`
- 如果需要覆盖更长时间的历史分钟线，请切换到“私人分钟线来源”，并填写：
  - 调用 URL
  - API Key
  - 超时、最小间隔、代理模式、分钟级别

说明：

- 公共源不是长历史分钟线方案。历史跨度较大时，请直接使用私人分钟线来源。
- 设置页不会展示具体付费服务商品牌名，只保留通用配置入口。
- 旧的环境变量仍然兼容，但当前更推荐把它们当作“迁移导入默认值”，保存后以设置页配置为准。
- 历史兼容前缀仍为 `GO_STOCK_DIEMENG_*`，这只是兼容旧部署的命名，不代表当前仓库默认绑定任何特定私有服务。

兼容环境变量示例：

- 公共链路
  - `GO_STOCK_MINUTE_PROVIDER`
  - `GO_STOCK_AKSHARE_MINUTE_SOURCE`
  - `GO_STOCK_AKSHARE_PROXY_MODE`
  - `GO_STOCK_AKSHARE_TIMEOUT_SEC`
- 私人分钟线来源
  - `GO_STOCK_DIEMENG_API_KEY`
  - `GO_STOCK_DIEMENG_BASE_URL`
  - `GO_STOCK_DIEMENG_PROXY_MODE`
  - `GO_STOCK_DIEMENG_TIMEOUT_SEC`
  - `GO_STOCK_DIEMENG_MIN_INTERVAL_MS`
  - `GO_STOCK_DIEMENG_LEVEL`
- 其他
  - `GO_STOCK_DB_BUSY_TIMEOUT_MS`

兼容说明：

- 当前内部仍兼容既有私人分钟线来源环境变量前缀 `GO_STOCK_DIEMENG_*`
- 这些环境变量在桌面应用里更适合作为首次导入来源；保存设置后，运行时优先使用设置页中的私人分钟线配置
- `GO_STOCK_DIEMENG_BASE_URL` 应填写你自己的私人分钟线服务地址；如果只填写站点根路径且服务接口位于 `/api`，程序会自动补齐 `/api`
- `GO_STOCK_DIEMENG_PROXY_MODE=disable`
  - 强制该私人来源直连
- `GO_STOCK_DIEMENG_PROXY_MODE=inherit`
  - 跟随系统代理
- `GO_STOCK_DIEMENG_PROXY_MODE=settings`
  - 使用设置页里的 HTTP 代理

## 构建

### 通用桌面构建

```bash
./scripts/build.sh
```

### Windows

```bash
./scripts/build-windows.sh
```

### macOS

```bash
./scripts/build-macos.sh
```

或：

```bash
./scripts/build-macos-arm.sh
./scripts/build-macos-intel.sh
```

### Linux

```bash
./scripts/build-linux.sh
```

如果前端构建出现内存不足，可显式提高 Node 内存：

```bash
NODE_OPTIONS=--max-old-space-size=4096 ./scripts/build-linux.sh
```

## 数据与运行目录

默认运行数据会放在：

- `runtime/`

典型内容包括：

- `runtime/db/stock.db`
- `runtime/dict/user.txt`

项目里也存在一些历史导出与构建产物目录，例如：

- `dist/`
- `exports/`

它们不等同于运行必需目录，但在当前仓库里属于业务流程的一部分。

## 工程化命令

仓库根目录新增了统一工程入口：

```bash
make test
make lint
make build-web
make dev-web
make build-desktop
```

说明：

- `make test`
  - 运行 `go test ./...`
- `make lint`
  - 运行 Go 静态检查与前端 ESLint
- `make build-web`
  - 构建前端静态资源

## 前端结构约定

- `frontend/src/views/`
  - 路由级页面入口，只负责页面装配
- `frontend/src/components/`
  - 业务展示组件和已有页面主体
- `frontend/src/services/app-api.js`
  - Wails `App` 绑定的统一导出入口，后续页面优先从这里接入

## 版本信息

当前仓库版本信息以以下位置为准：

- 应用产品版本：`wails.json`
- 更新说明：`CHANGELOG.md`

当前版本：

- `1.3.1`

建议发布新版本时至少同步以下文件：

- `wails.json`
- `CHANGELOG.md`
- `README.md`

## 常见问题

### 页面还是旧版本

先重新构建前端，再重启 Web 服务：

```bash
cd frontend
npm run build
cd ..
./scripts/restart.sh stop
./scripts/restart.sh start
```

### 34115 或 5173 端口被占用

```bash
ss -ltnp | rg ':34115|:5173'
```

如果是 Web 模式占用，也可以直接：

```bash
bash scripts/webctl.sh stop
```

### `build/bin/go-stock` 不存在

`restart.sh start` 默认会自动 `go build`。如果你设置了 `GO_STOCK_SKIP_BUILD=1`，需要先手动构建：

```bash
go build -o build/bin/go-stock .
```

### Linux 下桌面模式起不来

优先检查：

- `wails` CLI 是否安装
- `pkg-config` 是否存在
- GTK / WebKit2GTK 依赖是否齐全

直接运行：

```bash
./scripts/dev-linux.sh
```

脚本会把缺什么依赖打印出来。

## 说明

- 这是一个高频迭代中的项目仓库，README 只描述当前仓库内真实存在的功能与脚本
- AI 分析、推荐、邮件与行情数据都依赖外部模型或第三方数据源，结果不应视为投资建议
- 如果你准备发布版本，优先同步更新 `CHANGELOG.md`、`README.md` 与 `wails.json`

## 版本更新日志

### 1.3.1

- 收益率相关页面把当前策略分层统一展示为 `V1.3.1`，不再向用户暴露 `Current / phase3-v4` 这类内部工程名。
- `股票收益率`、`收益率统计`、`股票推荐记录` 的分层下拉和提示文案同步调整，保持同一套产品版本表达。
- 内部筛选值仍保留 `current` / `phase3-v4`，不改变已有收益统计口径、数据库记录和后端兼容逻辑。

### 1.3.0

- 回测算法补强时间戳验证，降低激活规则使用未来数据导致事后拟合的风险。
- 新增综合收益统计、机会成本和选择偏差分析，用来评估未激活、跳过和不符合条件的推荐。
- 量能条件从固定阈值升级为历史分位数模式，更适配不同股票和市场流动性。
- 基准对比优化为空仓期计入货币基金收益，并补充资金利用率分析。
- 调整 `sameDayOnly` 与开盘复核顺序，先执行风险保护，再处理同日时效约束。

### 1.2.8

- `市场行情` 页完成分栏重构，快讯、指数、资金流、龙虎榜、公告、研报等功能拆成独立 tab。
- 顶层菜单和运行时事件从 `App.vue` 继续抽离，市场页与研究页切换走统一模块入口。
- `收益率统计` 页新增独立日级 overview 数据链路，支持全库累计走势和单日变化图表。
- AI 推荐收益率重算链路继续拆分，激活判定、分钟线提供方、调度、概览统计和写库职责更清楚。
- 补齐 CI、README、更新日志、Release Notes、About 页和版本元数据同步。

### 1.2.7

- `研究中心` 新增独立 `收益率统计` 栏目，将策略级统计与股票收益率明细页拆开。
- 收益率统计集中展示策略收益率、现金流匹配基准、超额收益、XIRR、最大回撤、跑赢占比等指标。
- 修复统计页部分指标显示 `--` 的问题，策略侧 XIRR 和最大回撤不再被 benchmark 失败连带清空。
- 股票收益率页保留明细、分钟回放、手动补算和报错排查，统计职责迁移到独立页面。
- 关键指标增加悬浮解释，方便直接理解收益、基准、超额、回撤和数据时间等含义。

### 1.2.6

- 股票收益率页新增“查看全库收益走势”弹窗，支持累计走势和单日变化。
- 全库收益弹窗补齐 strict 口径日级汇总接口，卖出冻结、持仓最新价和交易日对齐逻辑更一致。
- 单日收益率改为按当日参与盈亏计算的持仓成本统计，避免历史累计成本池摊薄。
- 网络与数据源链路增强，补齐 Web 图标路由、财联社/新浪资讯直连与代理竞速、分钟线 host 自动切换。
- 手动下载分钟线新增任务审计、scope 收敛、SQLite busy 重试和进度节流，降低锁库与无效回算。

### 1.2.5

- 市场资讯 AI 推荐新增 `activation_rule_json`，strict 收益率只读取机器可执行规则。
- 推荐入库从批量强依赖改为逐条保存，单条规则不完整不再拖死整批推荐。
- 保存校验禁止 `放量 / 缩量 / 强势 / 承接 / 不追` 等模糊词直接作为激活条件。
- 定时市场总结调整为 `09:40 / 11:30 / 14:30`，并支持开盘复核、总结邮件和失败原因邮件。
- 研究报告、推荐记录、收益率列表统一共享日期范围，跨页面排查同一批推荐更连贯。

### 1.2.4

- 公开仓库发布前完成清理，移除不适合公开的支付二维码、运行数据库、构建产物和私有接入说明。
- 应用层 API 按领域拆分，`app.go` 收敛为主结构和共享状态入口。
- 前端路由收敛到 `views/` 页面层，业务组件保留在 `components/`，并新增统一 `app-api.js` 服务出口。
- AI 智能体页面重做为带历史侧栏的多轮会话体验，支持会话持久化和标题自动总结。
- 设置页新增分钟线数据源配置，默认改为公共源优先，并保留私人分钟线来源的通用配置入口。
- strict 收益率口径、分钟线回放、上一交易日活跃度和同口径基准稳定性全面补强。

### 1.2.3

- AI 分析报告、推荐记录和股票收益率统一到“主买入区激活”语义。
- 新增 `signal_time / activation_status / activation_time / activation_price` 等状态字段，未激活前不再提前计算买入收益。
- 手动下载分钟线升级为下载后触发收益重算，补齐状态回写。
- 收益率栏目增加已跳过、已放弃、过期未触发和无法回算等明确状态。
- 调整模拟仓位和列表展示颜色，减少小额佣金放大和状态语义混乱。

### 1.2.2

- 邮件发送新增完整审计表，覆盖测试邮件、收益率 CSV、手动 AI 报告和定时 AI 报告。
- 设置页新增最近邮件发送日志，可查看发送记录、附件摘要和失败原因。
- 邮件任务新增互斥、同一分钟去重和 cron 重载串行保护，降低重复发送风险。
- 收紧收益率 CSV 自动发送条件，避免页面查询或后台轮询时连续触发邮件。
- 市场资讯推荐补写增加同日同股去重，并清理历史重复推荐数据。

### 1.2.1

- Codex 大模型请求新增固定 fallback 链路，按 `RightCode -> codex-for-me -> su8` 自动轮换。
- fallback 只在网络错误、超时、`429`、`5xx` 等可重试故障时触发，避免参数错误被误判为渠道故障。
- 设置页新增 Codex fallback 顺序展示，但不再提供一键导入临时凭据的入口。

### 1.2.0

- 市场资讯 AI 总结支持定时执行，新增任务记录、冷却保护和工具模式失败回退。
- AI 总结结果可自动补写推荐股票记录，打通市场总结与推荐收益统计。
- AI 推荐收益率重算链路重构，支持全量重算、范围重算、排队重试和运行中状态管理。
- 新增邮件报告能力，可生成收益率 Excel 和最新 AI 总结 DOCX 附件。
- 应用服务、启动流程、运行时目录、设置页和 Web/桌面模式启动逻辑完成一轮系统整理。
