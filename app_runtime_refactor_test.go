package main

import (
	"go-stock/backend/data"
	"go-stock/backend/models"
	"go-stock/internal/service"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSummaryCronTimes(t *testing.T) {
	times := normalizeSummaryCronTimes(" 14:30,09:40，11:30,09:40,foo ")
	expected := []string{"09:40", "11:30", "14:30"}
	if len(times) != len(expected) {
		t.Fatalf("unexpected count: got=%v want=%v", times, expected)
	}
	for idx := range expected {
		if times[idx] != expected[idx] {
			t.Fatalf("unexpected times: got=%v want=%v", times, expected)
		}
	}
}

func TestBuildYieldEmailCronSpecWeekdaysOnly(t *testing.T) {
	spec := buildYieldEmailCronSpec("09:30")
	want := "CRON_TZ=Asia/Shanghai 0 30 09 * * 1-5"
	if spec != want {
		t.Fatalf("unexpected cron spec: got=%s want=%s", spec, want)
	}
}

func TestBuildSyncedTelegraph(t *testing.T) {
	now := time.Now()
	news := &models.NtfyNews{
		Title:   "标题",
		Message: "内容",
		Time:    int(now.Unix()),
		Tags:    []string{"财联社电报", "rotating_light"},
	}
	telegraph := buildSyncedTelegraph(news, "中性")
	if telegraph == nil {
		t.Fatal("expected telegraph")
	}
	if telegraph.Source != "财联社电报" {
		t.Fatalf("unexpected source: %s", telegraph.Source)
	}
	if !telegraph.IsRed {
		t.Fatal("expected red telegraph")
	}
	if telegraph.SentimentResult != "中性" {
		t.Fatalf("unexpected sentiment: %s", telegraph.SentimentResult)
	}
}

func TestShouldSummaryFailover(t *testing.T) {
	plainTextFailover := shouldSummaryFailover(summaryRunResult{text: "ok"})
	if marketSummaryRequiresV150Backend() && !plainTextFailover {
		t.Fatal("V1.5 plain text without a frozen backend decision should failover")
	}
	if !marketSummaryRequiresV150Backend() && plainTextFailover {
		t.Fatal("legacy non-empty text should not failover")
	}
	if !shouldSummaryFailover(summaryRunResult{}) {
		t.Fatal("empty result should failover")
	}
	if !shouldSummaryFailover(summaryRunResult{errs: []string{"context deadline exceeded"}}) {
		t.Fatal("timeout error should failover")
	}
}

func TestIsLikelyRequestLevelFailure(t *testing.T) {
	if !isLikelyRequestLevelFailure([]string{"invalid api key"}) {
		t.Fatal("api key error should be request-level failure")
	}
	if !isLikelyRequestLevelFailure([]string{"模型用量耗尽，请稍后再试"}) {
		t.Fatal("quota exhausted error should be request-level failure")
	}
	if isLikelyRequestLevelFailure([]string{"工具调用失败"}) {
		t.Fatal("generic tool failure should not be treated as request-level failure")
	}
}

func TestBuildMarketSummarySupplementNoteUsesDynamicTarget(t *testing.T) {
	note := buildMarketSummarySupplementNote(true, 4, []string{"300001.SZ"}, nil)
	if !strings.Contains(note, "第一轮生产候选不足 4 只") {
		t.Fatalf("expected dynamic production target in supplement note: %s", note)
	}
	if !strings.Contains(note, "补位候选：300001") {
		t.Fatalf("expected supplement note to include normalized candidate codes: %s", note)
	}
	if strings.Contains(note, "不足 2 只") {
		t.Fatalf("supplement note must not retain the legacy fixed target: %s", note)
	}
}

func TestCollectRuntimeSupplementCandidateCodesIncludesRepairableFailures(t *testing.T) {
	codes := collectRuntimeSupplementCandidateCodes(nil, []models.MarketSummaryTradePlanRepairCandidate{{StockCode: "300002.SZ"}})
	if len(codes) != 1 || codes[0] != "300002" {
		t.Fatalf("unexpected supplement candidate codes: %v", codes)
	}
}

func TestResolveAIFallbackOrder(t *testing.T) {
	cfg := &data.SettingConfig{
		AiConfigs: []*data.AIConfig{
			{ID: 1, Name: "su8"},
			{ID: 3, Name: "Right Codes"},
			{ID: 4, Name: "codex-for-me"},
		},
	}

	order := data.ResolveAIFallbackOrder(cfg, 0)
	expected := []int{1, 3, 4}
	if len(order) != len(expected) {
		t.Fatalf("unexpected fallback order length: got=%v want=%v", order, expected)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Fatalf("unexpected fallback order: got=%v want=%v", order, expected)
		}
	}

	order = data.ResolveAIFallbackOrder(cfg, 4)
	expected = []int{4, 1, 3}
	if len(order) != len(expected) {
		t.Fatalf("unexpected requested fallback order length: got=%v want=%v", order, expected)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Fatalf("unexpected requested fallback order: got=%v want=%v", order, expected)
		}
	}
}

func TestSelectPrimaryAIConfigID(t *testing.T) {
	cfg := &data.SettingConfig{
		AiConfigs: []*data.AIConfig{
			{ID: 7, Name: "primary"},
			{ID: 8, Name: "secondary"},
		},
	}

	if got := data.SelectPrimaryAIConfigID(cfg); got != 7 {
		t.Fatalf("unexpected primary ai config id: got=%d want=%d", got, 7)
	}
}

func TestResolveAIProviderNameFromConfigs_ByConfigID(t *testing.T) {
	configs := []*data.AIConfig{
		{ID: 3, Name: "OpenAI Primary", BaseUrl: "https://api.openai.com/v1", ModelName: "gpt-5.4"},
	}

	if got := resolveAIProviderNameFromConfigs(configs, 3, "gpt-5.4"); got != "OpenAI Primary" {
		t.Fatalf("unexpected provider by config id: got=%q want=%q", got, "OpenAI Primary")
	}
}

func TestResolveAIProviderNameFromConfigs_ByConfigIDFallbackToDetectedProvider(t *testing.T) {
	configs := []*data.AIConfig{
		{ID: 3, BaseUrl: "https://api.openai.com/v1", ModelName: "gpt-5.4"},
	}

	if got := resolveAIProviderNameFromConfigs(configs, 3, "gpt-5.4"); got != "OpenAI" {
		t.Fatalf("unexpected provider by config id fallback: got=%q want=%q", got, "OpenAI")
	}
}

func TestResolveAIProviderNameFromConfigs_FallbackToModelName(t *testing.T) {
	if got := resolveAIProviderNameFromConfigs(nil, 0, "gpt-5.4"); got != "" {
		t.Fatalf("unexpected fallback provider for gpt model: got=%q want empty", got)
	}
	if got := resolveAIProviderNameFromConfigs(nil, 0, "deepseek-chat"); got != "DeepSeek" {
		t.Fatalf("unexpected provider by model fallback: got=%q want=%q", got, "DeepSeek")
	}
}

func TestMergeMarketSummarySaveResultAccountsForUpgrade(t *testing.T) {
	target := &models.MarketSummaryRecommendSaveResult{
		SavedCount:        4,
		ProductionCount:   2,
		AnalysisOnlyCount: 2,
		SavedStockCodes:   []string{"000001.SZ"},
		RepairableTradePlanFailures: []models.MarketSummaryTradePlanRepairCandidate{
			{StockCode: "000002.SZ"},
		},
	}
	extra := &models.MarketSummaryRecommendSaveResult{
		SavedCount:         1,
		UpgradedCount:      1,
		ProductionCount:    2,
		AnalysisOnlyCount:  0,
		SavedStockCodes:    []string{"000003.SZ"},
		UpgradedStockCodes: []string{"000002.SZ"},
	}
	mergeMarketSummarySaveResult(target, extra)
	if target.SavedCount != 5 || target.UpgradedCount != 1 || target.ProductionCount != 4 || target.AnalysisOnlyCount != 1 {
		t.Fatalf("unexpected merged counters: %+v", target)
	}
	if len(target.RepairableTradePlanFailures) != 0 {
		t.Fatalf("upgraded repair candidate should be removed: %+v", target.RepairableTradePlanFailures)
	}
	if len(target.SavedStockCodes) != 2 || len(target.UpgradedStockCodes) != 1 {
		t.Fatalf("unexpected merged codes: saved=%v upgraded=%v", target.SavedStockCodes, target.UpgradedStockCodes)
	}
}

func TestSelectRuntimeRepairableVerifiedCandidatesRequiresSnapshot(t *testing.T) {
	verified, repairable := selectRuntimeRepairableVerifiedCandidates(
		[]models.MarketSummaryVerifiedCandidateSnapshot{{StockCode: "000001.SZ"}},
		[]models.MarketSummaryTradePlanRepairCandidate{{StockCode: "000001.SZ", RecommendID: 1}, {StockCode: "000002.SZ", RecommendID: 2}},
	)
	if len(verified) != 1 || len(repairable) != 1 || repairable[0].StockCode != "000001" {
		t.Fatalf("unexpected verified repair selection: verified=%v repairable=%v", verified, repairable)
	}
}

func TestBuildMarketSummaryCandidateFunnelExplainsHardLimits(t *testing.T) {
	legacyPolicy := data.ResolveMarketSummaryRecommendationCountPolicy("推荐20只股票")
	policy := service.MarketSummaryRecommendationCountPolicy{
		MinimumOutput: legacyPolicy.MinimumOutput, MaximumOutput: legacyPolicy.MaximumOutput, ProductionTarget: legacyPolicy.ProductionTarget,
		RequestedMinimum: legacyPolicy.RequestedMinimum, RequestedMaximum: legacyPolicy.RequestedMaximum, Source: legacyPolicy.Source,
		Custom: legacyPolicy.Custom, Clamped: legacyPolicy.Clamped,
	}
	text := buildMarketSummaryCandidateFunnel(
		&data.MarketSummaryRouteLogSnapshot{IndicatorCandidateCt: 120, IndicatorAIInputCt: 50, DiscoveryCandidateCt: 36, VerifiedCandidateCt: 8},
		policy,
		&models.MarketSummaryRecommendSaveResult{AIOutputCount: 20, SavedCount: 8, ProductionCount: 4, AnalysisOnlyCount: 4, BlockedCount: 12},
		8,
		8,
	)
	for _, want := range []string{"指标候选：120", "模型允许超量输出", "生产候选 4 只", "不代表已经激活", "系统单次上限为 12 只", "数量上限截断 8 行"} {
		if !strings.Contains(text, want) {
			t.Fatalf("funnel missing %q: %s", want, text)
		}
	}
}
