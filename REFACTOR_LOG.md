# Refactor Log

## 2026-03-13 Round 6

- 将 `CheckStockBaseInfo` 从主文件拆到 `app_baseinfo_runtime.go`，并收敛为统一的远端抓取替换流程。
- 将 `SummaryStockNews` 从主文件拆到 `app_summary_runtime.go`，拆出单次执行、工具回退、配置回退、结果持久化等辅助函数。
- 保持现有业务行为不变，重点是减少 `app.go` 里的流程型代码长度与职责混杂。
- 补充 `app_runtime_refactor_test.go`，覆盖 cron 时间规范化、同步新闻转换、总结回退判断、请求级失败判断。
- 修复拆分过程中暴露的两个编译问题：
  - `NtfyNews.Time` 测试数据类型写错。
  - `app.go` 中遗留未使用的 `strconv` import。

### Verification

- `go test -run 'TestNormalizeSummaryCronTimes|TestBuildSyncedTelegraph|TestShouldSummaryFailover|TestIsLikelyRequestLevelFailure' .`
- `go test -run '^$' . ./cmd/go-stock-cli ./internal/...`
- `go test -run '^$' ./...`

## 2026-03-13 Round 7

- 修复 `backend/data` 收益追踪过滤逻辑，`shouldTrackRecommendInYield` 不再把未知分类默认放行，同时保留中文混合分类别名的兼容追踪。
- 拆开推荐收益列表两层 `BuyTime` 语义：
  - `mapRecommendRecordToYieldItem` 直调路径保留原始推荐时间，避免 legacy 状态映射串档。
  - `mapRecommendRecordToYieldItemWithRecordState` 包装路径继续输出实际买点时间，保持现有接口一致性。
- 修复 `buildYieldRecordStateFromRecommend` 的买入时间决策：
  - 已存在 `record_state.BuyTime` 时优先保留历史值。
  - 无历史值时使用更保守的收益统计起点时间，避免无效卖出快照和冻结卖出重算错位。

### Verification

- `go test ./backend/data -run 'TestMapRecommendRecordToYieldItem_SkipStaleSellState|TestMapRecommendRecordToYieldItem_KeepValidSellState|TestShouldTrackRecommendInYield|TestShouldTrackRecommendInYield_AllowsRawChineseCategoryVariants|TestMapRecommendRecordToYieldItemWithRecordState_FallbackLegacyState|TestMapRecommendRecordToYieldItemWithRecordState_PreferRecordState|TestBuildYieldRecordStateFromRecommend_RecomputesFrozenSellWithOpenGap|TestBuildYieldRecordStateFromRecommendClearsInvalidExistingSell'`
- `go test ./...`
- `cd frontend && npm run lint`
