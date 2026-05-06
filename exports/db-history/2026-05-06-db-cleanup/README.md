# 2026-05-06 数据库归档

执行时间：2026-05-06 23:37:11 +0800

## 当前活跃库

- 原路径：data/stock.db
- 处理：保留原文件，只复制备份为 active-data-stock-2026-05-06-2310.db
- 说明：运行中的 go-stock 进程 fd 指向 data/stock.db，该库最新推荐到 2026-05-06 14:30:00，分钟线到 2026-05-06 15:00:00。

## 已归档旧库

| 原路径 | 归档文件 | 处理原因 |
| --- | --- | --- |
| runtime/db/stock.db | runtime-stock-old-2026-04-23.db | 旧库，最新推荐只到 2026-04-23 09:40:00 |
| data/db/stock.db | data-db-stock-empty.db | 空库，推荐记录和分钟线均为 0 |
| go-stock.db | go-stock-empty.db | 0 字节空壳库 |
| data/go-stock.db | data-go-stock-empty.db | 0 字节空壳库 |
| data.db | data-empty.db | 0 字节空壳库 |

## 文件清单

```text
active-data-stock-2026-05-06-2310.db	442331136 bytes	2026-05-06 23:36:00.0326968660
data-db-stock-empty.db	503808 bytes	2026-03-15 05:12:42.9998234340
data-empty.db	0 bytes	2026-04-02 18:03:27.3813580130
data-go-stock-empty.db	0 bytes	2026-04-23 00:30:35.4302330940
go-stock-empty.db	0 bytes	2026-04-24 08:19:30.1168413630
README.md	830 bytes	2026-05-06 23:37:11.5437603850
runtime-stock-old-2026-04-23.db	389070848 bytes	2026-04-24 04:33:10.8689191780
```
