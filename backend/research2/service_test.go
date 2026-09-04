package research2

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"go-stock/internal/marketquote"
	"go-stock/internal/recommendationchart"
	"go-stock/internal/trading"

	"github.com/google/uuid"
)

func TestRecordBuyInitializesCurrentMarkAndLiveReturnIsNotPersisted(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, shanghai())
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: "run", StockCode: "sh600000", StockName: "test", SignalAt: now.Add(-time.Minute), FinalScore: 70, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", TargetBuyAt: now}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	trade := Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: now, MarketPrice: 10, ExecutionPrice: 10.01, Quantity: 100, Commission: 5, TransferFee: 0.01, NetCashFlow: -1006.01}
	if err := repository.RecordBuy(context.Background(), item.RecommendationID, trade, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	var stored Recommendation
	if err := repository.DB().Where("recommendation_id = ?", item.RecommendationID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CurrentPrice != 10 || stored.CurrentPriceAt == nil || !stored.CurrentPriceAt.Equal(now) {
		t.Fatalf("buy mark = %.2f/%v", stored.CurrentPrice, stored.CurrentPriceAt)
	}
	wantPnL := trading.CalculateSellCost(10, 100).NetCashFlow - 1006.01
	rows, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || math.Abs(rows[0].NetPnL-wantPnL) > 1e-8 || math.Abs(rows[0].NetYieldRate-wantPnL/1006.01) > 1e-8 {
		t.Fatalf("live row = %+v, want pnl %.8f", rows, wantPnL)
	}
	if err := repository.DB().Where("recommendation_id = ?", item.RecommendationID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.NetPnL != 0 || stored.NetYieldRate != 0 {
		t.Fatalf("unrealized return was persisted: %+v", stored)
	}
}

type blockingQuoteProvider struct {
	mu      sync.Mutex
	quotes  map[string]marketquote.Quote
	errors  map[string]error
	started chan string
	release <-chan struct{}
	current int
	maximum int
}

func (p *blockingQuoteProvider) CurrentQuote(_ context.Context, code string) (marketquote.Quote, error) {
	p.mu.Lock()
	p.current++
	if p.current > p.maximum {
		p.maximum = p.current
	}
	p.mu.Unlock()
	if p.started != nil {
		p.started <- code
	}
	if p.release != nil {
		<-p.release
	}
	p.mu.Lock()
	p.current--
	quote, err := p.quotes[code], p.errors[code]
	p.mu.Unlock()
	return quote, err
}

func TestServiceRefreshesHoldingsConcurrentlyAndFallsBackToLastMark(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 8, 31, 10, 5, 0, 0, shanghai())
	items := []Recommendation{
		{RecommendationID: "one", AnalysisRunID: "run", StockCode: "sh600000", StockName: "one", SignalAt: now, Status: "active", BuyPrice: 10, BuyMarketPrice: 10, BuyFees: 5, Quantity: 100, CurrentPrice: 10, CurrentPriceAt: timePointer(now.Add(-time.Minute))},
		{RecommendationID: "two", AnalysisRunID: "run", StockCode: "sz000001", StockName: "two", SignalAt: now, Status: "sell_pending", BuyPrice: 20, BuyMarketPrice: 20, BuyFees: 5, Quantity: 100, CurrentPrice: 20, CurrentPriceAt: timePointer(now.Add(-time.Minute))},
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Model(&Account{}).Where("id = ?", 1).Update("cash", 9000.0).Error; err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	provider := &blockingQuoteProvider{quotes: map[string]marketquote.Quote{"sh600000": {Code: "sh600000", Price: 11, At: now}}, errors: map[string]error{"sz000001": errors.New("unavailable")}, started: make(chan string, 2), release: release}
	service := NewService(repository, provider)
	type overviewResult struct {
		value AccountOverview
		err   error
	}
	done := make(chan overviewResult, 1)
	go func() {
		value, err := service.Overview(context.Background())
		done <- overviewResult{value: value, err: err}
	}()
	<-provider.started
	<-provider.started
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	provider.mu.Lock()
	maximum := provider.maximum
	provider.mu.Unlock()
	if maximum < 2 {
		t.Fatalf("quotes were not fetched concurrently, max=%d", maximum)
	}
	wantValue := trading.CalculateSellCost(11, 100).NetCashFlow + trading.CalculateSellCost(20, 100).NetCashFlow
	if math.Abs(result.value.PositionValue-wantValue) > 1e-8 || math.Abs(result.value.NetAssetValue-(9000+wantValue)) > 1e-8 {
		t.Fatalf("overview=%+v want position value %.8f", result.value, wantValue)
	}
	var first, second Recommendation
	if err := repository.DB().Where("recommendation_id = ?", "one").First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Where("recommendation_id = ?", "two").First(&second).Error; err != nil {
		t.Fatal(err)
	}
	if first.CurrentPrice != 11 || first.CurrentPriceAt == nil || !first.CurrentPriceAt.Equal(now) {
		t.Fatalf("successful mark was not saved: %+v", first)
	}
	if second.CurrentPrice != 20 || second.CurrentPriceAt == nil || !second.CurrentPriceAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("failed quote did not preserve cached mark: %+v", second)
	}

	performance, err := repository.Performance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(performance.NetAssetValue-result.value.NetAssetValue) > 1e-8 || len(performance.Curve) != 1 || performance.Curve[0].SnapshotType != "current" {
		t.Fatalf("performance did not use current account valuation: %+v", performance)
	}
}

func TestClosedRecommendationKeepsRealizedReturn(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, shanghai())
	item := Recommendation{RecommendationID: "closed", AnalysisRunID: "run", StockCode: "sh600000", StockName: "test", SignalAt: now, Status: "closed", BuyPrice: 10, BuyFees: 5, Quantity: 100, CurrentPrice: 99, NetPnL: 83, NetYieldRate: 83.0 / 1005}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	rows, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].NetPnL != 83 || math.Abs(rows[0].NetYieldRate-83.0/1005) > 1e-12 {
		t.Fatalf("closed realized return changed: %+v", rows)
	}
}

type research2ChartProvider struct {
	snapshot recommendationchart.ProviderSnapshot
}

func (p research2ChartProvider) LoadCached(context.Context, string, time.Time, time.Time) (recommendationchart.ProviderSnapshot, error) {
	return p.snapshot, nil
}

func (p research2ChartProvider) Refresh(context.Context, string, time.Time, time.Time, []string) (recommendationchart.ProviderSnapshot, error) {
	return p.snapshot, nil
}

func TestRecommendationChartAdaptsResearch2TradesToSharedEngine(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 8, 31, 10, 5, 0, 0, shanghai())
	run := AnalysisRun{RunID: "chart-run", TradingDate: "2026-08-31", ScheduledFor: now.Add(-15 * time.Minute), StartedAt: now.Add(-15 * time.Minute), EvidenceCutoffAt: now.Add(-10 * time.Minute), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: "chart-recommendation", AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "chart", SignalAt: now.Add(-7 * time.Minute), Status: "active", BuyAt: timePointer(now.Add(-5 * time.Minute)), BuyPrice: 10.01, BuyFees: 5.02, Quantity: 100, CurrentPrice: 11, CurrentPriceAt: timePointer(now)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	trade := Trade{TradeID: "chart-trade", RecommendationID: item.RecommendationID, Side: "buy", TradedAt: now.Add(-5 * time.Minute), MarketPrice: 10, ExecutionPrice: 10.01, Quantity: 100, Commission: 5, TransferFee: 0.02, NetCashFlow: -1006.02}
	if err := repository.DB().Create(&trade).Error; err != nil {
		t.Fatal(err)
	}
	quote := recommendationchart.Quote{Price: 11, At: now}
	provider := research2ChartProvider{snapshot: recommendationchart.ProviderSnapshot{
		Bars: []recommendationchart.MinuteBar{
			{At: now.Add(-5 * time.Minute), Open: 10, High: 10.1, Low: 9.9, Close: 10, Source: "fixture-unadjusted"},
			{At: now, Open: 10.9, High: 11.1, Low: 10.8, Close: 11, Source: "fixture-unadjusted"},
		},
		Quote: &quote, RefreshedAt: now,
	}}
	service := NewService(repository, nil)
	service.now = func() time.Time { return now }
	service.SetRecommendationChartProvider(provider, recommendationchart.WeekdayCalendar{})
	chart, err := service.RecommendationChart(context.Background(), item.RecommendationID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(chart.Trades) != 1 || math.Abs(chart.Trades[0].TotalFees-5.02) > 1e-8 || chart.Trades[0].MarkerAt == nil || chart.Trades[0].MarkerSnapped {
		t.Fatalf("chart trades=%+v", chart.Trades)
	}
	wantPnL := trading.CalculateSellCost(11, 100).NetCashFlow - 1006.02
	if chart.CurrentPrice != 11 || math.Abs(chart.CurrentNetPnL-wantPnL) > 1e-8 || len(chart.Bars) != 2 {
		t.Fatalf("chart=%+v want pnl %.8f", chart, wantPnL)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
