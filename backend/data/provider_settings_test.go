package data

import (
	"context"
	"testing"

	"go-stock/backend/models"
)

func TestResearchProviderSnapshotOwnsSettingsAndModels(t *testing.T) {
	auto := true
	setting := &models.SettingConfig{Settings: &models.Settings{TushareToken: "before", CrawlTimeOut: 12},
		AiConfigs: []*models.AIConfig{{ID: 7, ApiKey: "model-before"}}, MinuteProviderOrder: []string{"tencent"}, AIAnalysisAutoEnabled: &auto}
	options := ResearchAIClientOptionsForSettings(setting)
	stocks := NewStockDataApiWithSettings(setting)
	setting.TushareToken = "after"
	setting.AiConfigs[0].ApiKey = "model-after"
	setting.MinuteProviderOrder[0] = "private"
	auto = false
	if stocks.config.TushareToken != "before" || stocks.config.MinuteProviderOrder[0] != "tencent" || !*stocks.config.AIAnalysisAutoEnabled {
		t.Fatal("stock provider retained mutable settings")
	}
	first := options.LoadConfigs()
	if first[0].ApiKey != "model-before" {
		t.Fatal("model snapshot changed during task")
	}
	first[0].ApiKey = "mutated by attempt"
	if options.LoadConfigs()[0].ApiKey != "model-before" {
		t.Fatal("attempt mutated fallback configuration")
	}
}

func TestOpenAISettingsDefaultsDoNotMutateSnapshot(t *testing.T) {
	model := &models.AIConfig{ApiKey: "test", ModelName: "test-model"}
	setting := &models.SettingConfig{Settings: &models.Settings{}}
	provider := NewOpenAiWithSettings(context.Background(), model, setting)
	if provider.TimeOut != 300 || provider.CrawlTimeOut != 60 || provider.KDays != 60 {
		t.Fatal("provider defaults were not applied")
	}
	if model.TimeOut != 0 || setting.CrawlTimeOut != 0 || setting.KDays != 0 {
		t.Fatal("constructor mutated caller configuration")
	}
}
