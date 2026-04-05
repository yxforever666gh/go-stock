# Public Release Checklist

本文件用于把当前仓库从私有改为公开前的准备项收敛为可执行检查单。

## 1. 许可证与来源

- 当前仓库本地许可证文件为 `Apache-2.0`。
- 截至 `2026-04-05`，原始公开来源仓库 `ArvinLovegood/go-stock` 的 GitHub 页面显示为 `Apache-2.0`。
- 即便如此，公开当前仓库前仍建议保留来源说明，并核对历史版权声明、NOTICE 需求和衍生关系描述是否完整。
- 在切换公开前，必须先确认当前仓库代码是否仍属于原项目衍生版本，以及当前许可证是否与来源兼容。
- 如果无法证明已完整替换原 GPL 代码或取得额外授权，建议先把许可证问题澄清，再执行公开。

## 2. 历史提交敏感信息

- 当前工作树已移除 `docs/integrations/diemeng-api.md`，但历史提交中曾出现过该文件。
- `.claude/settings.local.json` 也曾被提交到历史中，虽然内容很小，但不适合作为公开仓库的一部分。
- 在正式公开前，建议人工复查历史提交、Tag 和 GitHub Actions 日志，确认不存在以下内容：
  - 私有接口接入说明
  - API Key / Token / SMTP 凭据
  - 内部域名、专线地址、代理配置
  - 本地工作区结构或仅面向个人工具的配置

## 3. 当前仓库公开面

以下公开仓库基础文件已经具备：

- `README.md`
- `LICENSE`
- `CHANGELOG.md`
- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `SECURITY.md`
- `.github/ISSUE_TEMPLATE/*`
- `.github/pull_request_template.md`

## 4. 建议公开前再做一次检查

- 检查 `Settings -> Actions` 历史日志是否包含不应公开的输出。
- 检查 `Settings -> Secrets and variables`，确认没有依赖“公开仓库不可见”的错误认知。
- 检查 `About` 区域文案、`Topics`、仓库描述、主页链接是否准备好对外展示。
- 检查是否需要新增 `NOTICE` 或 README 来源说明，明确与原始项目的关系。
- 检查最近一次 release/tag 的版本号、变更日志和 README 是否一致。

## 5. 正式切换公开前的建议顺序

1. 先确认许可证和来源归属。
2. 再决定是否需要清理 Git 历史。
3. 审核 GitHub Actions 历史与仓库设置。
4. 补全 README 中的公开说明。
5. 最后在 GitHub 仓库设置中执行 `Change repository visibility`。
