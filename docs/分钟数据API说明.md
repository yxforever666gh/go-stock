# 个股、指数、集合竞价 API 与 MCP 使用说明

服务在本机运行，数据原文件留在本机，通过 Cloudflare 临时隧道提供无鉴权 MCP。现有个股查询及44项蝶梦工具保留，新增指数与竞价能力，共51个工具。

## 重启后必须重新填写地址

电脑或隧道重新启动后，Quick Tunnel 可能生成新域名。它不是运行中不断变化，也不会自动更新 ChatGPT 中的旧连接。

1. 打开代理软件，确保 `127.0.0.1:7890` 在运行。
2. 在项目根目录运行：

   ```powershell
   pwsh -NoProfile -File .\scripts\start-minute-api.ps1
   ```

3. 等待构建、全部竞价索引准备、API和隧道连接完成。第一次须扫描全部竞价数据；以后只重建变化文件。未完成时不会发布可用地址。
4. 从输出或以下文件复制**本次完整 MCP 地址，必须包含 `/mcp`**：

   ```powershell
   Get-Content -LiteralPath 'H:\Download\go-stock-minute-api\mcp-url.txt'
   ```

5. 在 ChatGPT 自定义 MCP 中重新填写地址，认证选择 **None／不使用认证**，重新扫描工具。若无法修改旧连接地址，删除旧连接再创建。当前应发现51个工具。
6. 在聊天中选择这个连接再查询。公网协议验证不等于已经完成 ChatGPT 账号内的添加和扫描。

不要填写 localhost、普通行情链接或聊天记录中的旧域名。浏览器用GET打开 `/mcp` 返回405是正常的，MCP客户端使用POST协议。

停止命令：

```powershell
pwsh -NoProfile -File .\scripts\start-minute-api.ps1 -Stop
```

启动脚本关闭或停止时会结束其拥有的进程，并清理地址文件；强制断电可能留下旧文件，下次启动会先清理它们。索引阶段也可停止，当前文件事务回滚，已完成文件保留。当前没有开机自启。

## 统一数据根目录

启动脚本默认读取 `H:\Program Data\go-stock\A股历史分钟线数据包`，可用 `-DataRoot` 覆盖。程序入口改为统一的 `-data-root`；旧的年份目录和历史目录两个参数已移除，不能再用原命令启动。

```text
A股历史分钟线数据包
├─ A股个股
│  ├─ 1分钟(2000-2025)\1分钟\股票代码.csv
│  └─ 2026_20260911_173537\2026\各分钟周期\股票代码.csv
├─ 分钟K线-指数
│  ├─ 对应名称.csv
│  └─ 年份目录\各分钟周期\指数代码.csv
└─ 集合竞价
   └─ 年份目录\tick_3s_*.csv
```

本次发现：个股33517个CSV约138GB；指数8350个行情CSV及1份名称表约9.5GB；竞价177个CSV约36GB。目录标签不是完整覆盖证明：竞价实际发现2023—2026年，不能把“2020-2024”父目录当作包含2020—2022年；股票和指数各自起始日期以真实记录为准，亦不代表数据已经更新到今天。

新增或替换数据后重启脚本，以刷新文件目录和索引。未识别的CSV位置或格式明确报错；名称表、字段说明表和文字备注不作为行情记录处理。所有原始文件均只读。

## 工具及 HTTP 接口

| 工具 | 用途 | 主要参数 |
| --- | --- | --- |
| `get_bar` | 个股单根K线 | symbol、time；period默认1m，source默认auto |
| `get_bars` | 个股区间K线 | symbol、start_time、end_time；period、source可选 |
| `search_indices` | 本地指数名称、代码及可用周期 | query可选，省略返回全部本地可用指数 |
| `get_index_bar` | 指数精确时刻K线 | symbol、time；period默认1m |
| `get_index_bars` | 指数区间K线 | symbol、start_time、end_time；period默认1m |
| `get_auction_snapshot` | 竞价精确时刻快照 | symbol、time |
| `get_auction_snapshots` | 竞价区间分页查询 | symbol、start_time、end_time；page默认0，page_size默认500、最大5000 |
| 44个 `diemeng_` 工具 | 蝶梦完整查询能力 | 见[蝶梦接口说明](蝶梦MCP接口说明.md) |

本地代码格式统一使用 `sh600941`、`sz000001`、`bj920000`；竞价文件中的 `600941.SH` 在内部对应 `sh600941`。股票与指数查询入口分开，不因代码相似或名称相同而混合。指数的别名代码保留，例如名称表中的上证指数可能有多个代码，只有存在数据文件的代码才出现在搜索结果里。

时间支持带时区RFC3339或北京时间 `YYYY-MM-DD HH:mm`、`YYYY-MM-DD HH:mm:ss`，区间包含两端。精确工具不自动取附近时刻；竞价快照虽标为3秒数据，也不保证每个3秒位置都有记录。找不到返回found=false和null；范围无匹配返回空数组。

HTTP接口：

| 路径 | 参数及行为 |
| --- | --- |
| `/api/bars` | 原接口，symbol、period、start/end日期；只读个股CSV，日期包含全天 |
| `/api/indices` | query可选，查本地指数名称与代码 |
| `/api/index/bars` | symbol、period；time精确查询，或start_time/end_time区间查询 |
| `/api/auction/snapshots` | symbol；time或start_time/end_time；支持page/page_size |

MCP结果同时包含结构化数据和对应JSON文本。参数错误、读取/格式错误和冲突均明确返回；HTTP参数错误为400，读取失败为500（缺文件为404）。

## 来源、单位与冲突

个股 `source=auto` 优先读取全部适用本地文件，仍缺文件或无记录时调用已配置的蝶梦；local仅本地，diemeng仅蝶梦。已有部分本地结果时不与蝶梦自动拼接。CSV损坏明确报错。历史包在前、当前年度包在后，按相对路径稳定排序；相同时间以较后的当前包为准。数据目录中存在的周期均可查询，不从1分钟临时聚合其他周期。

个股CSV结果保留开高低收、成交量、成交额、涨跌额、涨跌幅、换手率、流通股本、总股本，成交量为股、成交额为元、百分数字段保留原百分数。source=csv、adjustment=unknown；不推断原始复权口径，不移动K线时间。蝶梦原有成交量转换及字段null规则不变。

指数source=csv_index，把日期和时间合成北京时间；价格是指数点位，volume和amount单位未注明，标为unknown并保留原值。成交量0仍是0，不改成缺失。

竞价source=csv_auction，保留全部19个字段及新增标准化time：

```text
code, trade_date, prev_close, current, volume, amount, cjcs,
b1_p, b1_v, b2_p, b2_v, a1_p, a1_v, a2_p, a2_v,
total_bid, average_bid, total_ask, average_ask
```

字段含义依据随附说明表：前收盘价、现价、成交量/金额/次数、买卖一二档价量、十档委买委卖总量及均价。空白值为null，数值0保留为0；单位文档未明确，保留原值。不根据09:25备注推算未匹配量。范围结果返回total、page、page_size和next_page，按next_page继续取，不把第一页当作完整结果。

指数及竞价跨文件合并后按时间排序，数值相同的重复记录去重（如9与9.0视为相同）。同代码、同时间但值不同，返回冲突错误并列明相对文件路径；不静默选择，也不混合两个指数别名。

## 集合竞价位置索引

索引目录固定在启动脚本的 `H:\Download\go-stock-minute-api\auction-index`，包含独立的 `positions.sqlite`。这是可复用的运行资料，日常不要当临时文件删除；删除后需重新扫描。原始CSV不写入索引库，库中仅存文件大小/修改时间、记录数、真实时间范围及股票/交易日/连续字节区间。

索引不依赖全文件有序。每个文件独立事务提交，取消或格式失败时只回滚当前文件。新文件加入、文件大小/修改时间变化、文件删除或根目录变化后，准备阶段会更新索引；查询发现原文件变化会报错，避免使用错误位置。运行期间新增文件需重启重新发现。所有竞价文件准备完成并校验后才启动API与隧道。

单独准备索引（通常无需手动执行，启动脚本会执行）：

```powershell
go build -o 'H:\Download\go-stock-minute-api\minute-api.exe' ./cmd/minute-api
& 'H:\Download\go-stock-minute-api\minute-api.exe' -data-root 'H:\Program Data\go-stock\A股历史分钟线数据包' -prepare-data
```

程序可用 `-index-dir` 更换派生索引目录；不访问或迁移 go-stock 的 stock.db/minute.db。测试使用临时目录，真实索引与生产数据库分开。

## 查询示例

个股历史单点：`get_bar({"symbol":"sh600941","time":"2023-09-12 14:45","source":"local"})`。

指数先调用 `search_indices({"query":"上证指数"})`，再用返回代码调用 `get_index_bar({"symbol":"sh000001","period":"1m","time":"2026-01-05 09:31"})`。

竞价区间：

```json
{"symbol":"sz000001","start_time":"2026-01-05 09:15:00","end_time":"2026-01-05 09:25:01","page":0,"page_size":500}
```

跨年个股仍使用get_bars，例如2025-12-31 15:00至2026-01-05 09:30。本地股票和指数按文件流式读取，仅为命中记录构造结果，不将全部历史文件加载入内存；建议给出明确查询范围。

## Cloudflare、私有配置与停止

API监听127.0.0.1:18080。现有转发程序通过7890代理连接Cloudflare，使用127.0.0.1:17844转发加密隧道，17845处理网址申请。申请可能超过cloudflared内置15秒限制，脚本先准备一次响应（最多60秒）再启动隧道；凭据仅在内存中使用一次，不写入文件。

保留 `--http-host-header 127.0.0.1:18080` 以兼容MCP默认Host检查。服务、代理、启动脚本必须持续运行。临时域名不是固定域名；本机直连可达性受网络影响，公网测试通过7890代理访问实际域名完成。

蝶梦URL与Key仍在仓库外的私有配置中，不因更换数据目录或临时域名改变。MCP入口无鉴权，知道网址的人可以调用数据并消耗蝶梦额度。全市场下载是唯一标记本地文件写入的工具；其他50个工具只读。ChatGPT账号可能限制下载工具，须在实际接入时确认。

日志与程序保存在 `H:\Download\go-stock-minute-api`，索引进度见index.stdout.log，失败见index.stderr.log。停止时清理进程和临时域名，保留派生索引供下次复用。

## 验证

针对性验证：

```powershell
pwsh -NoProfile -File .\scripts\verify.ps1 -Tier fast -GoPackage ./cmd/minute-api
```

明确需要真实联网检查时，使用专门的integration测试和显式环境开关，不能加入fast/domain/release。实际数据清单、索引结果和公网样例见[本地数据接入验证](本地数据接入验证.md)。
