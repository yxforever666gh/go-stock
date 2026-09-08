package data

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestResearchNewsFetchStateDoesNotCrossInstances(t *testing.T) {
	first := NewMarketNewsApiWithSettings(&models.SettingConfig{Settings: &models.Settings{HttpProxy: "http://127.0.0.1:1111", HttpProxyEnabled: true}}, nil)
	second := NewMarketNewsApiWithSettings(&models.SettingConfig{Settings: &models.Settings{HttpProxy: "http://127.0.0.1:2222", HttpProxyEnabled: true}}, nil)
	sequence := first.marketNewsBeginFetch(marketNewsFetchKeySinaLive, marketNewsSourceSina)
	first.marketNewsFinishFetch(marketNewsFetchKeySinaLive, sequence, "proxy", true, errors.New("first proxy unavailable"))
	now := time.Now()
	if first.marketNewsFetchFailureForWindow(nil, now.Add(-time.Minute), now) == nil {
		t.Fatal("first instance lost its fetch failure")
	}
	if second.marketNewsFetchFailureForWindow(nil, now.Add(-time.Minute), now) != nil || len(second.GetMarketNewsFetchMeta(marketNewsFetchKeySinaLive)) != 0 {
		t.Fatal("second instance inherited first instance fetch status")
	}
	if _, err := first.GetNewsWindow(nil, now.Add(-time.Minute), now); err == nil {
		t.Fatal("nil injected storage must not fall back to global database")
	}
}

func TestResearchCalendarFailureAndCredentialsStayWithinSnapshot(t *testing.T) {
	setting := &models.SettingConfig{Settings: &models.Settings{TushareToken: "first"}}
	first := NewResearchTradingCalendar(setting)
	second := NewResearchTradingCalendar(&models.SettingConfig{Settings: &models.Settings{TushareToken: "second"}})
	setting.TushareToken = "later"
	first.cache.fetch = func(_ string, _, _ time.Time, _ int64) (map[string]bool, error) {
		return nil, errors.New("first unavailable")
	}
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, cnLocation())
	second.cache.fetch = func(_ string, _, _ time.Time, _ int64) (map[string]bool, error) {
		return map[string]bool{day.Format("2006-01-02"): true}, nil
	}
	if _, err := first.IsTradingDay(context.Background(), day); err == nil {
		t.Fatal("missing first failure")
	}
	if open, err := second.IsTradingDay(context.Background(), day); err != nil || !open {
		t.Fatalf("second calendar poisoned: %v", err)
	}
	if first.cache.setting.TushareToken != "first" {
		t.Fatal("calendar credentials changed during task")
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
