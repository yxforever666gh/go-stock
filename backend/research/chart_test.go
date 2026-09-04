package research

import (
	"context"
	"math"
	"testing"
	"time"

	"go-stock/internal/recommendationchart"
	"go-stock/internal/trading"
)

type chartTestProvider struct {
	cached       recommendationchart.ProviderSnapshot
	cachedCalls  int
	refreshCalls int
}

func (provider *chartTestProvider) LoadCached(context.Context, string, time.Time, time.Time) (recommendationchart.ProviderSnapshot, error) {
	provider.cachedCalls++
	return provider.cached, nil
}

func (provider *chartTestProvider) Refresh(context.Context, string, time.Time, time.Time, []string) (recommendationchart.ProviderSnapshot, error) {
	provider.refreshCalls++
	return recommendationchart.ProviderSnapshot{}, nil
}

type chartTestCalendar struct {
	open       map[string]bool
	strictCall int
	cachedCall int
}

func (calendar *chartTestCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	calendar.strictCall++
	return calendar.open[value.Format("2006-01-02")], nil
}

func (calendar *chartTestCalendar) IsTradingDayCached(value time.Time) (bool, bool) {
	calendar.cachedCall++
	open, known := calendar.open[value.Format("2006-01-02")]
	return open, known
}

func TestRecommendationChartMapsResearchDetailAndUsesCache(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation)
	buyAt := time.Date(2026, 8, 18, 14, 35, 40, 0, shanghaiLocation)
	sellAt := time.Date(2026, 8, 19, 10, 0, 15, 0, shanghaiLocation)
	signalAt := time.Date(2026, 8, 18, 14, 0, 0, 0, shanghaiLocation)
	recommendation := seedRecommendation(t, repo, "active", signalAt, signalAt, "")
	trades := []SimulatedTrade{
		{TradeID: newID(), RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode, Side: "buy", TradedAt: buyAt,
			MarketPrice: 10, ExecutionPrice: 10.01, Quantity: 100, TotalFees: 4, NetCashFlow: -1005},
		{TradeID: newID(), RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode, Side: "sell", TradedAt: sellAt,
			MarketPrice: 10.8, ExecutionPrice: 10.78, Quantity: 40, TotalFees: 2, NetCashFlow: 430},
	}
	if err := repo.DB().Create(&trades).Error; err != nil {
		t.Fatal(err)
	}
	position := Position{RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode, StockName: recommendation.StockName,
		Market: "SH", Quantity: 60, EntryAt: buyAt, EntryPrice: 10.01, BuyFees: 4, CurrentPrice: 10.8, Status: "open"}
	if err := repo.DB().Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	bars := []recommendationchart.MinuteBar{
		{At: time.Date(2026, 8, 17, 15, 0, 0, 0, shanghaiLocation), Open: 9.5, High: 9.5, Low: 9.5, Close: 9.5, Source: "test"},
		{At: time.Date(2026, 8, 18, 9, 30, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10, Source: "test"},
		{At: time.Date(2026, 8, 18, 14, 36, 0, 0, shanghaiLocation), Open: 10.1, High: 10.2, Low: 10, Close: 10.1, Source: "test"},
		{At: time.Date(2026, 8, 18, 15, 0, 0, 0, shanghaiLocation), Open: 10.1, High: 10.2, Low: 10, Close: 10.1, Source: "test"},
		{At: time.Date(2026, 8, 19, 9, 30, 0, 0, shanghaiLocation), Open: 10.5, High: 10.6, Low: 10.5, Close: 10.6, Source: "test"},
		{At: time.Date(2026, 8, 19, 10, 0, 0, 0, shanghaiLocation), Open: 10.7, High: 10.9, Low: 10.7, Close: 10.8, Source: "test"},
		{At: now, Open: 10.9, High: 11.1, Low: 10.9, Close: 11, Source: "test"},
	}
	provider := &chartTestProvider{cached: recommendationchart.ProviderSnapshot{Bars: bars, RefreshedAt: now}}
	calendar := &chartTestCalendar{open: map[string]bool{"2026-08-18": true, "2026-08-19": true}}
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{}, calendar)
	service.SetRecommendationChartProvider(provider)
	service.now = func() time.Time { return now }

	chart, err := service.RecommendationChart(context.Background(), recommendation.RecommendationID, false)
	if err != nil {
		t.Fatal(err)
	}
	if provider.cachedCalls != 1 || provider.refreshCalls != 0 || calendar.strictCall != 0 {
		t.Fatalf("cached chart performed unexpected work: cache=%d refresh=%d strictCalendar=%d", provider.cachedCalls, provider.refreshCalls, calendar.strictCall)
	}
	if len(chart.Bars) != 6 || len(chart.Trades) != 2 || chart.Trades[0].MarkerAt == nil || !chart.Trades[0].MarkerSnapped {
		t.Fatalf("unexpected bars or trade markers: bars=%d trades=%+v", len(chart.Bars), chart.Trades)
	}
	wantPnL := 430 + trading.CalculateSellCost(11, 60).NetCashFlow - 1005
	if math.Abs(chart.CurrentNetPnL-wantPnL) > 1e-8 || math.Abs(chart.CurrentNetYieldRate-wantPnL/1005) > 1e-8 {
		t.Fatalf("full-cost current return = %.8f/%.8f, want %.8f/%.8f", chart.CurrentNetPnL, chart.CurrentNetYieldRate, wantPnL, wantPnL/1005)
	}
	if chart.Bars[len(chart.Bars)-1].NetYieldRate == 0 {
		t.Fatal("minute full-cost yield was not calculated")
	}
}
