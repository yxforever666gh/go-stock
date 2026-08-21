package data

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
)

func TestResearchChartProviderLoadCachedRejectsAdjustedBars(t *testing.T) {
	initMinuteCacheTestDB(t, "chart-provider.db")
	start := time.Date(2026, 8, 19, 9, 30, 0, 0, cnLocation())
	rows := []minuteBar{
		{TradeTime: start.Add(time.Minute), Open: 10, High: 10.2, Low: 9.9, Close: 10.1, Source: "tencent"},
		{TradeTime: start.Add(2 * time.Minute), Open: 10.1, High: 10.3, Low: 10, Close: 10.2, Source: "akshare:sina:adjustment=qfq"},
	}
	if _, err := upsertMinuteBarsToCache("601899.SH", rows, ""); err != nil {
		t.Fatal(err)
	}
	provider := NewResearchChartProvider(nil)
	snapshot, err := provider.LoadCached(context.Background(), "sh601899", start, start.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Bars) != 1 || snapshot.Bars[0].Source != "tencent" {
		t.Fatalf("bars=%+v", snapshot.Bars)
	}
	if len(snapshot.ProviderErrors) != 1 || snapshot.ProviderErrors[0].Provider != "cache" {
		t.Fatalf("provider errors=%+v", snapshot.ProviderErrors)
	}
}

func TestChartProviderNormalizesCodesAndSanitizesErrors(t *testing.T) {
	keys, err := chartMinuteCacheKeys("sh601899")
	if err != nil || len(keys) == 0 || keys[0] != "601899.SH" {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	message := sanitizeChartError(errors.New("upstream api_key=secret-value token=token-value failed"))
	if strings.Contains(message, "secret-value") || strings.Contains(message, "token-value") || !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("unsanitized message=%q", message)
	}
}

func TestEnabledChartMinuteProvidersPrivatePriorityFallsBackToPublicSources(t *testing.T) {
	initMinuteCacheTestDB(t, "chart-provider-private-fallback.db")
	setChartProviderSettings(t, map[string]any{
		"minute_provider_order":   "private,tencent,sina,akshare",
		"private_minute_enabled":  true,
		"private_minute_base_url": "https://minute.example.test",
		"private_minute_api_key":  "test-key",
		"private_minute_level":    "1min",
		"akshare_enabled":         true,
		"sina_minute_enabled":     true,
		"tencent_minute_enabled":  true,
	})

	got := chartProviderNames(enabledChartMinuteProviders(time.Now().In(cnLocation())))
	want := []string{"diemeng", "tencent", "sina", "akshare"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("private-priority providers = %v; want %v", got, want)
	}
}

func TestEnabledChartMinuteProvidersPublicPriorityFallsBackToPrivateSource(t *testing.T) {
	initMinuteCacheTestDB(t, "chart-provider-public-fallback.db")
	setChartProviderSettings(t, map[string]any{
		"minute_provider_order":   "tencent,sina,akshare,private",
		"private_minute_enabled":  true,
		"private_minute_base_url": "https://minute.example.test",
		"private_minute_api_key":  "test-key",
		"private_minute_level":    "1min",
		"akshare_enabled":         true,
		"sina_minute_enabled":     true,
		"tencent_minute_enabled":  true,
	})

	got := chartProviderNames(enabledChartMinuteProviders(time.Now().In(cnLocation())))
	want := []string{"tencent", "sina", "akshare", "diemeng"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public-priority providers = %v; want %v", got, want)
	}
}

func TestEnabledChartMinuteProvidersHonorsIndividualSwitches(t *testing.T) {
	initMinuteCacheTestDB(t, "chart-provider-disabled-sources.db")
	setChartProviderSettings(t, map[string]any{
		"minute_provider_order":   "private,akshare,sina,tencent",
		"private_minute_enabled":  true,
		"private_minute_base_url": "https://minute.example.test",
		"private_minute_api_key":  "test-key",
		"private_minute_level":    "1min",
		"akshare_enabled":         false,
		"sina_minute_enabled":     true,
		"tencent_minute_enabled":  false,
	})

	got := chartProviderNames(enabledChartMinuteProviders(time.Now().In(cnLocation())))
	want := []string{"diemeng", "sina"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("providers with switches = %v; want %v", got, want)
	}
}

func TestEnabledChartMinuteProvidersSkipsPrivateSourceUnlessItProvidesOneMinuteBars(t *testing.T) {
	initMinuteCacheTestDB(t, "chart-provider-private-level.db")
	setChartProviderSettings(t, map[string]any{
		"minute_provider_order":   "private,tencent,sina,akshare",
		"private_minute_enabled":  true,
		"private_minute_base_url": "https://minute.example.test",
		"private_minute_api_key":  "test-key",
		"private_minute_level":    "5min",
		"akshare_enabled":         true,
		"sina_minute_enabled":     true,
		"tencent_minute_enabled":  true,
	})

	got := chartProviderNames(enabledChartMinuteProviders(time.Now().In(cnLocation())))
	want := []string{"tencent", "sina", "akshare"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("providers with non-1min private source = %v; want %v", got, want)
	}
}

func setChartProviderSettings(t *testing.T, values map[string]any) {
	t.Helper()
	// GetSettingConfig creates the singleton settings row for a fresh test DB.
	// Using a map below is intentional: disabled switches must persist false.
	_ = GetSettingConfig()
	if err := db.Dao.Model(&Settings{}).Where("1 = 1").Updates(values).Error; err != nil {
		t.Fatalf("update chart provider settings: %v", err)
	}
}

func chartProviderNames(items []chartMinuteProvider) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.name)
	}
	return result
}
