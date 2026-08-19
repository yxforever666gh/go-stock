package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/research"
)

type fakeHistoricalChartRefresher struct {
	snapshot research.ChartProviderSnapshot
	err      error
}

func (fake fakeHistoricalChartRefresher) Refresh(context.Context, string, time.Time, time.Time, []string) (research.ChartProviderSnapshot, error) {
	return fake.snapshot, fake.err
}

func TestResolveHistoricalBuyMarketEvidencePrefersFirstMinuteBar(t *testing.T) {
	day := time.Date(2026, 8, 18, 0, 0, 0, 0, cnLocation())
	provider := fakeHistoricalChartRefresher{snapshot: research.ChartProviderSnapshot{Bars: []research.ChartMinuteBar{
		{At: day.Add(9*time.Hour + 32*time.Minute), Open: 101, High: 102, Low: 100, Close: 101.5, Source: "akshare-eastmoney-unadjusted-1m"},
		{At: day.Add(9*time.Hour + 31*time.Minute), Open: 100, High: 101, Low: 99, Close: 100.5, Source: "tencent-unadjusted-1m"},
	}}}
	result, err := resolveHistoricalBuyMarketEvidence(context.Background(), provider, func(string) ([]historicalDailyBar, error) {
		return []historicalDailyBar{{Date: "2026-08-17", Close: 95}, {Date: "2026-08-18", Open: 99, Close: 101}}, nil
	}, "sz300308", "中际旭创", "2026-08-18", day.Add(18*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.EntryQuote.Price != 100 || result.EntrySource != "tencent-unadjusted-1m" || result.EntryQuote.PreviousClose != 95 {
		t.Fatalf("unexpected minute evidence: %+v", result)
	}
}

func TestResolveHistoricalBuyMarketEvidenceFallsBackToSinaDailyOpen(t *testing.T) {
	day := time.Date(2026, 8, 18, 0, 0, 0, 0, cnLocation())
	provider := fakeHistoricalChartRefresher{err: errors.New("all minute providers failed")}
	result, err := resolveHistoricalBuyMarketEvidence(context.Background(), provider, func(string) ([]historicalDailyBar, error) {
		return []historicalDailyBar{{Date: "2026-08-17", Close: 190}, {Date: "2026-08-18", Open: 200, Close: 205}}, nil
	}, "sh688012", "中微公司", "2026-08-18", day.Add(18*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.EntryQuote.Price != 200 || result.EntrySource != "sina-unadjusted-daily-open" || result.EntryQuote.At.Hour() != 9 || result.EntryQuote.At.Minute() != 30 {
		t.Fatalf("unexpected daily fallback: %+v", result)
	}
}
