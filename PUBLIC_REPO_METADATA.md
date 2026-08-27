# Public Repo Metadata

本文件用于在 GitHub 仓库切换为公开前，统一整理对外展示信息，避免在 `About`、`Release`、仓库简介和 Topics 中临时拼凑文案。

## 仓库名称

- `go-stock`

## 仓库简介

### 中文版

基于 Go、Vue 3 和 SQLite 的本地 Web 股票分析工具，包含自选股、市场资讯、AI 分析报告、推荐收益跟踪与邮件报告能力；当前公开版基于 `ArvinLovegood/go-stock` 改编整理。

### English

Local-first web stock analysis tool built with Go, Vue 3 and SQLite, with watchlists, market insights, AI reports, recommendation tracking and email reporting; this public snapshot is adapted from `ArvinLovegood/go-stock`.

## About 区域建议

### Website

- 暂时留空
- 如果后续你准备了公开主页、演示页或文档站，再补上

### About 文案建议

- 建议直接写明：当前仓库基于 `ArvinLovegood/go-stock` 改编整理，不是原作者官方仓库。
- About 第一屏不要再放打赏、赞助、私有服务入口或任何个人联系方式。

### Social Preview

- 建议上传仓库内已准备好的社交预览图：
  - `docs/assets/social-preview.png`
- 这张图也已经用于 README 第一屏，风格与当前公开版文案保持一致
- 社交图建议保留“PUBLIC 1.6.0”以及“Derived from ArvinLovegood/go-stock”这类信息，避免对外误认为原项目官方仓库

### Topics

建议先控制在 8 到 12 个，避免堆太多无效标签。

推荐 Topics：

- `golang`
- `local-web-app`
- `vue3`
- `vite`
- `naive-ui`
- `echarts`
- `sqlite`
- `local-web-app`
- `stock-analysis`
- `ai`

## 仓库置顶说明

如果 GitHub 仓库简介只能放一段短文案，建议用中文版，和 README 第一段保持一致，不要额外承诺尚未稳定的能力。

## 仓库可见性切换前建议

- 确认 `README.md`、`RELEASE_NOTES.md`、`LICENSE`、`SECURITY.md` 已是当前公开版内容
- 确认 `main` 只保留当前单提交公开快照
- 确认远端发布标签与 App `1.6.0` 一致
- 确认 Releases 页面没有旧的私有发布说明
- 确认 Actions 历史日志里没有不适合公开的输出

## 切换为公开后建议立即检查

- 仓库首页 `About` 文案是否显示正常
- `Issues`、`Security`、`Contributing` 链接是否可正常访问
- `Releases` 页面是否展示 `1.6.0` 发布说明
- `README` 中的绝对路径跳转和截图引用是否正常
