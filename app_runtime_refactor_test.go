package main

import (
	"go-stock/backend/data"
	"go-stock/backend/models"
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
	if shouldSummaryFailover(summaryRunResult{text: "ok"}) {
		t.Fatal("non-empty text should not failover")
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
