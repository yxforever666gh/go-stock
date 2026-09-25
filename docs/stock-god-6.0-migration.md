# Stock God 6.0.0 退役与资源迁移记录

开发基线：`4838d135d2a1eac08ddb3ef892ad1f8c5bc47dee`（5.2.5，schema 35/3）。当前应用为 Python，主库迁至 36，分钟库保持 3；发布完成以受控部署回执和 `/readyz` 身份为准。

## 退役边界

删除旧 `backend/`、`internal/`、`cmd/`、根目录 Go 源码/测试、`go.mod`、`go.sum`、`.golangci.yml`、旧 Go 开发工具和 vendored SQLite Go 实现。旧市场展示、自选、研究中心一和知识库已没有页面、API、调度或模型注入。

Python 源码、Vue 前端、公开行情 API、分钟/MCP、原始行情和所有历史数据库资料继续保留。`LICENSE`、`NOTICE`、原 SQLite 第三方版权文本保留。原 Go 制品只存在于部署归档用于回滚；Git 历史未改写，也未执行 GC。

## 资源对应与核验

| 旧路径 | 当前路径 | 删除旧副本前的核对 |
| --- | --- | --- |
| `build/stock_basic.json` | `src/stock_god/market/resources/stock_basic.json` | 字节 SHA-256 相同：`ee91ce3a…` |
| `build/stock_base_info_hk.json` | `src/stock_god/market/resources/stock_base_info_hk.json` | 字节 SHA-256 相同：`80a554b0…` |
| `build/stock_base_info_us.json` | `src/stock_god/market/resources/stock_base_info_us.json` | 字节 SHA-256 相同：`63f43f8a…` |
| `backend/data/data/dict/base.txt` | `resources/finance.txt` | 428 行及字节 SHA-256 相同：`ee37464e…`；`sentiment_dict.txt` 为运行规范化副本 |
| `backend/data/words.txt` | `resources/sensitive_words.txt` | 41,769 行规范化换行后完全一致 |
| `backend/data/data/dict/user.txt` | `resources/user.txt` | 旧 185 行全部保留于新 193 行词库 |
| `runtime/dict/user.txt` | `resources/user.txt` | 字节 SHA-256 相同：`ab2dd2dc…`；本机运行覆盖文件不作为源码管理 |
| `backend/data/data/dict/zh/stop_word.txt` | `resources/stop_words.txt` | 字节 SHA-256 相同：`4b00cce5…` |
| `internal/diemeng/catalog.json` | `src/stock_god/market/mcp_catalog.py` | 44 个接口对象逐项相同 |
| `backend/research2/prompts/overnight_strength.md` | `src/stock_god/prediction/prompts/overnight_strength.md` | 策略提示词迁移，显示名更新 |
| `build/app.ico`、`build/appicon.png` | `frontend/public/app.ico`、`frontend/public/appicon.png` | Git 移动保留原图；构建后随前端发布 |

表中缩写 `resources/` 均指 `src/stock_god/market/resources/`。情绪权重与历史迁移数值以现有固定 fixture 和回归测试验证，不从新的业务规则反推旧数据。

当前使用文档归并为结构、运行发布、数据接口三份。此前验证记录和策略备忘放入 `docs/history`，旧 44 接口详细字段保留在 `docs/reference`；`RELEASE_NOTES.md` 保留历史发布记录。

## 发布验收

最终候选需通过 release 门禁、冷启动链路、重启补偿链路及隔离的真实行情/模型预测，然后创建 annotated `6.0.0`、核对远端 commit/tag，再部署重启并核对实际运行身份。详细命令与临时数据约束见 [运行与发布](operations.md)。
