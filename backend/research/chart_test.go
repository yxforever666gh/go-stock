package research

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type chartTestProvider struct {
	cached       ChartProviderSnapshot
	refreshed    ChartProviderSnapshot
	cachedCalls  int
	refreshCalls int
	started      chan struct{}
	release      chan struct{}
	once         sync.Once
}

func (provider *chartTestProvider) LoadCached(context.Context, string, time.Time, time.Time) (ChartProviderSnapshot, error) {
	provider.cachedCalls++
	return provider.cached, nil
}

func (provider *chartTestProvider) Refresh(context.Context, string, time.Time, time.Time, []string) (ChartProviderSnapshot, error) {
	provider.refreshCalls++
	if provider.started != nil {
		provider.once.Do(func() { close(provider.started) })
		<-provider.release
	}
	return provider.refreshed, nil
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

func seedChartRecommendation(t *testing.T, repo *Repository, signal time.Time) Recommendation {
	t.Helper()
	recommendation := seedRecommendation(t, repo, "active", signal, signal, "")
	return recommendation
}

func TestRecommendationChartUsesCacheAndFullCostReturns(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation)
	buyAt := time.Date(2026, 8, 18, 14, 35, 40, 0, shanghaiLocation)
	sellAt := time.Date(2026, 8, 19, 10, 0, 15, 0, shanghaiLocation)
	recommendation := seedChartRecommendation(t, repo, time.Date(2026, 8, 18, 14, 0, 0, 0, shanghaiLocation))
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
	bars := []ChartMinuteBar{
		{At: time.Date(2026, 8, 17, 15, 0, 0, 0, shanghaiLocation), Open: 9.5, High: 9.5, Low: 9.5, Close: 9.5, Source: "test"},
		{At: time.Date(2026, 8, 18, 9, 30, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10, Source: "test"},
		{At: time.Date(2026, 8, 18, 14, 36, 0, 0, shanghaiLocation), Open: 10.1, High: 10.2, Low: 10, Close: 10.1, Source: "test"},
		{At: time.Date(2026, 8, 18, 15, 0, 0, 0, shanghaiLocation), Open: 10.1, High: 10.2, Low: 10, Close: 10.1, Source: "test"},
		{At: time.Date(2026, 8, 19, 9, 30, 0, 0, shanghaiLocation), Open: 10.5, High: 10.6, Low: 10.5, Close: 10.6, Source: "test"},
		{At: time.Date(2026, 8, 19, 10, 0, 0, 0, shanghaiLocation), Open: 10.7, High: 10.9, Low: 10.7, Close: 10.8, Source: "test"},
		{At: now, Open: 10.9, High: 11.1, Low: 10.9, Close: 11, Source: "test"},
	}
	provider := &chartTestProvider{cached: ChartProviderSnapshot{Bars: bars, RefreshedAt: now}}
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
	wantPnL := 430 + CalculateSellCost(11, 60).NetCashFlow - 1005
	if math.Abs(chart.CurrentNetPnL-wantPnL) > 1e-8 || math.Abs(chart.CurrentNetYieldRate-wantPnL/1005) > 1e-8 {
		t.Fatalf("full-cost current return = %.8f/%.8f, want %.8f/%.8f", chart.CurrentNetPnL, chart.CurrentNetYieldRate, wantPnL, wantPnL/1005)
	}
	if chart.Bars[len(chart.Bars)-1].NetYieldRate == 0 {
		t.Fatal("minute full-cost yield was not calculated")
	}
}

func TestRecommendationChartRefreshRejectsSameRecommendationConcurrently(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation)
	recommendation := seedChartRecommendation(t, repo, now)
	provider := &chartTestProvider{started: make(chan struct{}), release: make(chan struct{}), refreshed: ChartProviderSnapshot{RefreshedAt: now}}
	calendar := &chartTestCalendar{open: map[string]bool{"2026-08-19": true}}
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{}, calendar)
	service.SetRecommendationChartProvider(provider)
	service.now = func() time.Time { return now }
	finished := make(chan error, 1)
	go func() {
		_, err := service.RecommendationChart(context.Background(), recommendation.RecommendationID, true)
		finished <- err
	}()
	<-provider.started
	if _, err := service.RecommendationChart(context.Background(), recommendation.RecommendationID, true); !errors.Is(err, ErrChartRefreshInProgress) {
		t.Fatalf("concurrent refresh error = %v", err)
	}
	close(provider.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestNearestChartMarkerFiveMinuteAndTradingSegmentBoundaries(t *testing.T) {
	tradeAt := time.Date(2026, 8, 19, 11, 25, 0, 0, shanghaiLocation)
	exactBoundary := tradeAt.Add(5 * time.Minute)
	if got, ok := nearestChartMarker(tradeAt, []ChartMinuteBar{{At: exactBoundary}}); !ok || !got.Equal(exactBoundary) {
		t.Fatalf("five minute boundary should snap: %v %v", got, ok)
	}
	if _, ok := nearestChartMarker(tradeAt, []ChartMinuteBar{{At: tradeAt.Add(5*time.Minute + time.Nanosecond)}}); ok {
		t.Fatal("marker beyond five minutes must not snap")
	}
	if _, ok := nearestChartMarker(time.Date(2026, 8, 19, 11, 30, 0, 0, shanghaiLocation),
		[]ChartMinuteBar{{At: time.Date(2026, 8, 19, 13, 0, 0, 0, shanghaiLocation)}}); ok {
		t.Fatal("marker must not cross the lunch break")
	}
}

func TestChartSessionsReportsInternalMinuteGapAsPartial(t *testing.T) {
	day := "2026-08-19"
	to := time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation)
	bars := []ChartMinuteBar{
		{At: time.Date(2026, 8, 19, 9, 31, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
		{At: time.Date(2026, 8, 19, 9, 32, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
		{At: time.Date(2026, 8, 19, 9, 45, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
		{At: time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
	}
	sessions, missing := chartSessions([]string{day}, bars, bars[0].At, to)
	if len(sessions) != 1 || sessions[0].Status != "partial" || len(missing) != 1 || missing[0] != day {
		t.Fatalf("sessions=%+v missing=%v", sessions, missing)
	}
}

func TestChartRangeClampsLunchAndPreOpenDoesNotCreateFutureSession(t *testing.T) {
	entry := time.Date(2026, 8, 18, 14, 35, 0, 0, shanghaiLocation)
	detail := RecommendationDetail{Recommendation: Recommendation{SignalAt: entry}, Trades: []SimulatedTrade{{Side: "buy", TradedAt: entry}}}
	lunchNow := time.Date(2026, 8, 19, 12, 15, 0, 0, shanghaiLocation)
	_, lunchEnd := chartRange(detail, lunchNow)
	if got := lunchEnd.Format("2006-01-02 15:04"); got != "2026-08-19 11:30" {
		t.Fatalf("lunch range end = %s", got)
	}
	calendar := &chartTestCalendar{open: map[string]bool{"2026-08-18": true, "2026-08-19": true}}
	preOpenNow := time.Date(2026, 8, 19, 9, 0, 0, 0, shanghaiLocation)
	from, preOpenEnd := chartRange(detail, preOpenNow)
	sessions, err := chartTradingSessions(context.Background(), calendar, from, preOpenEnd, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := preOpenEnd.Format("2006-01-02 15:04"); got != "2026-08-19 09:00" {
		t.Fatalf("pre-open range end = %s", got)
	}
	if len(sessions) != 1 || sessions[0] != "2026-08-18" {
		t.Fatalf("pre-open sessions = %v", sessions)
	}
}
