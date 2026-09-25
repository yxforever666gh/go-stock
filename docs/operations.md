# 本地运行、验证与发布

## 环境与启动

使用 `.python-version` 指定的 Python 3.13、`uv.lock` 锁定的依赖，以及 Node.js 20+ 和 PowerShell 7。首次准备：

```powershell
uv sync --frozen
Push-Location frontend
npm ci
npm run build
Pop-Location
uv run --frozen python -m stock_god db status
```

新安装或有待执行迁移时，先明确目标数据库并备份，然后显式执行 `uv run --frozen python -m stock_god db migrate`。服务启动只检查 schema，待迁移时拒绝就绪。

开发运行：

```powershell
uv run --frozen python -m stock_god serve
```

默认地址为 `http://127.0.0.1:34115`。`serve --no-scheduler` 可用于隔离的页面/API 检查：它恢复中断状态，但不自动启动预测、邮件和行情任务。现有数据库副本路径必须通过环境明确设置；正式运行使用已部署制品及 `runtime/current.json`。

部署后的日常操作：

```powershell
.\启动项目.cmd
pwsh -File scripts/release.ps1 -Command status
pwsh -File scripts/release.ps1 -Command restart
pwsh -File scripts/release.ps1 -Command stop
```

启动器跟随已核验的发布指针。`/livez` 表示进程存活，`/readyz` 同时提供迁移、服务、调度就绪及版本/commit/制品哈希；关于页面展示后端返回的真实版本。

## 持久数据与临时文件

| 内容 | 默认位置或配置 |
| --- | --- |
| 主数据库 | `data/stock.db`；`STOCK_GOD_DB_PATH` |
| 分钟缓存库 | `data/minute.db`；`STOCK_GOD_MINUTE_DB_PATH` |
| 大型原始行情包 | 项目内 `A股历史分钟线数据包/`；由 `STOCK_GOD_MARKET_DATA_ROOT` 指定 |
| 派生索引 | `runtime/minute-index`；`STOCK_GOD_MARKET_INDEX_DIR` |
| 已部署制品和解释器 | `runtime/releases`、`runtime/toolchain` |
| 部署/回滚记录及数据库备份 | `runtime/deployments` |
| 运行日志 | `runtime/logs` |
| 一次性测试脚本、数据库副本、截图 | `H:\Download` 对应任务目录 |

数据、依赖缓存、日志、构建结果和临时脚本不入 Git。不得把一次性测试指向生产库。只清理本次任务产生的副产物；历史数据、旧版回滚制品及既有用户文件保留。

## 分级验证

```powershell
pwsh -File scripts/verify.ps1 -Tier fast -TestPath tests/prediction/test_prediction.py
pwsh -File scripts/verify.ps1 -Tier domain -Domain prediction
pwsh -File scripts/verify.ps1 -Tier domain -Domain contracts
pwsh -File scripts/verify.ps1 -Tier release
```

领域可选 `prediction`、`market`、`storage`、`web`、`contracts`；前端定向检查使用 `-FrontendTest`，路径相对于 `frontend`。`release` 包括全量离线测试、lint、类型、契约与前端构建，只用于明确的发布或全门禁请求。通过的检查不因无关改动重复执行。

接口变更后运行 `uv run --frozen python -m stock_god.contracts --write` 更新生成 TS，再运行无 `--write` 的检查。边界测试要求已退役路径不再出现、无 Go 运行时源码、包依赖方向和版本一致。

## 6.0.0 发布验收

最终候选必须完成本地 release 门禁、独立冷启动链路、重启/失败恢复链路，以及用户要求的一次真实行情和模型预测。真实预测使用 `H:\Download` 中的临时数据库副本；测试邮件发本地 SMTP fixture。原始输出、临时 test 和数据库副本不入库，保留脱敏验证回执及其哈希。

验证回执绑定 commit、依赖锁和制品身份。构建候选后使用 `scripts/release.py inspect --candidate <目录>` 检查；部署使用：

```powershell
uv run --frozen python scripts/release.py build --uv <uv.exe完整路径>
uv run --frozen python scripts/release.py inspect --candidate <候选目录>
uv run --frozen python scripts/release.py deploy --candidate <候选目录> --proof <验收回执JSON>
```

`proof` 必须来自已执行的验证，不能手写“通过”代替测试。当前四个阶段为 `local-release-gate`、`offline-cold`、`offline-restart`、`live-prediction`。候选内容变化后，旧回执失效。

用户明确授权后才创建 annotated tag `6.0.0` 并推送对应 commit/tag，随后核对远端 SHA。GitHub 使用统一 SSH key 与 `127.0.0.1:7890` 代理，无直连回退，也不创建 GitHub Actions。普通开发 commit 不自动 push。

部署校验候选和回执，停止已识别进程，备份双库，迁移并验证数据库，切换发布指针并启动一次；完成后核对 `/readyz`、进程身份和浏览器版本。失败时按部署回执恢复双库及旧指针：

```powershell
uv run --frozen python scripts/release.py rollback --receipt <runtime/deployments中的receipt.json>
```

归档的旧 Go 可执行文件只用于已有部署回执的回滚；当前开发、构建和运行均使用 Python。
