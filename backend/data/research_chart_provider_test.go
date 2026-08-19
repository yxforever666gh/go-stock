package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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
