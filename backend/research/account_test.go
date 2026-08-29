package research

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fundingCalendar struct {
	closed map[string]bool
	err    error
}

func (calendar fundingCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	if calendar.err != nil {
		return false, calendar.err
	}
	local := ShanghaiTime(value)
	if calendar.closed[local.Format("2006-01-02")] {
		return false, nil
	}
	return local.Weekday() != time.Saturday && local.Weekday() != time.Sunday, nil
}

func fundingTestService(t *testing.T, startAfter string) (*Service, *gorm.DB) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "research.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&AnalysisRun{}, &Recommendation{}, &LifecycleMessage{}, &DecisionEvent{}, &LifecycleObservation{}, &AnalysisTrigger{}, &BuyOpportunity{},
		&SimulatedAccount{}, &SimulatedTrade{}, &Position{}, &AccountCashFlow{}, &FundingPlan{}, &AccountValuationSnapshot{}); err != nil {
		t.Fatal(err)
	}
	account := SimulatedAccount{ID: 1, InitialCash: LegacyInitialCash, Cash: LegacyInitialCash}
	if err := database.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	flow := AccountCashFlow{FlowID: "initial", Sequence: 0, Type: "initial_deposit", Amount: LegacyInitialCash,
		EffectiveAt: time.Date(2026, 8, 18, 9, 20, 0, 0, shanghaiLocation), TradingDate: "2026-08-18",
		NetAssetValueAfter: LegacyInitialCash, UnitValueBefore: 1, UnitsIssued: LegacyInitialCash}
	if err := database.Create(&flow).Error; err != nil {
		t.Fatal(err)
	}
	plan := FundingPlan{ID: 1, InitialContribution: LegacyInitialCash, TargetContribution: TargetContribution,
		DepositAmount: ScheduledDepositAmount, PlannedDeposits: ScheduledDepositCount, StartAfterTradingDate: startAfter}
	if err := database.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	service := NewService(NewRepository(database), &scriptedAI{}, &scriptedQuotes{}, fundingCalendar{})
	return service, database
}

func TestProcessScheduledFundingIsTradingDayWindowedAndIdempotent(t *testing.T) {
	service, database := fundingTestService(t, "2026-08-18")
	ctx := context.Background()
	before := time.Date(2026, 8, 19, 9, 19, 59, 0, shanghaiLocation)
	result, err := service.ProcessScheduledFunding(ctx, before)
	if err != nil || result.Applied || result.Reason != "outside_funding_window" {
		t.Fatalf("before window result=%+v err=%v", result, err)
	}
	now := time.Date(2026, 8, 19, 9, 20, 0, 0, shanghaiLocation)
	result, err = service.ProcessScheduledFunding(ctx, now)
	if err != nil || !result.Applied || result.CompletedDeposits != 1 || result.RemainingDeposits != 3 {
		t.Fatalf("funding result=%+v err=%v", result, err)
	}
	duplicate, err := service.ProcessScheduledFunding(ctx, now.Add(time.Minute))
	if err != nil || duplicate.Applied {
		t.Fatalf("duplicate result=%+v err=%v", duplicate, err)
	}
	var account SimulatedAccount
	_ = database.First(&account, 1).Error
	if account.Cash != 200000 {
		t.Fatalf("cash=%f", account.Cash)
	}
	var flows, snapshots int64
	_ = database.Model(&AccountCashFlow{}).Count(&flows).Error
	_ = database.Model(&AccountValuationSnapshot{}).Count(&snapshots).Error
	if flows != 2 || snapshots != 2 {
		t.Fatalf("flows=%d snapshots=%d", flows, snapshots)
	}
}

func TestScheduledFundingStopsAtFourAndDoesNotCatchUpSameDay(t *testing.T) {
	service, database := fundingTestService(t, "2026-08-18")
	ctx := context.Background()
	for day := 19; day <= 24; day++ {
		now := time.Date(2026, 8, day, 9, 20, 0, 0, shanghaiLocation)
		_, err := service.ProcessScheduledFunding(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	var account SimulatedAccount
	_ = database.First(&account, 1).Error
	if account.Cash != TargetContribution {
		t.Fatalf("cash=%f want=%f", account.Cash, TargetContribution)
	}
	var plan FundingPlan
	_ = database.First(&plan, 1).Error
	if plan.CompletedDeposits != ScheduledDepositCount {
		t.Fatalf("plan=%+v", plan)
	}
	var scheduled int64
	_ = database.Model(&AccountCashFlow{}).Where("type = ?", "scheduled_deposit").Count(&scheduled).Error
	if scheduled != ScheduledDepositCount {
		t.Fatalf("scheduled flows=%d", scheduled)
	}
}

func TestScheduledFundingPreservesUnitValueAndTWR(t *testing.T) {
	service, database := fundingTestService(t, "2026-08-18")
	service.now = func() time.Time {
		return time.Date(2026, 8, 19, 9, 21, 0, 0, shanghaiLocation)
	}
	if err := database.Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 110000.0).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 9, 20, 0, 0, shanghaiLocation)
	result, err := service.ProcessScheduledFunding(context.Background(), now)
	if err != nil || !result.Applied {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	overview, err := service.AccountOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(overview.TimeWeightedReturn-0.1) > 1e-9 || math.Abs(overview.NetYieldRate-0.1) > 1e-9 {
		t.Fatalf("twr=%f netYield=%f", overview.TimeWeightedReturn, overview.NetYieldRate)
	}
	if overview.CumulativeNetContribution != 200000 || overview.NetProfit != 10000 || math.Abs(overview.CumulativeCapitalReturn-0.05) > 1e-9 {
		t.Fatalf("overview=%+v", overview)
	}
}

func TestScheduledFundingCalendarFailureDoesNotWrite(t *testing.T) {
	service, database := fundingTestService(t, "2026-08-18")
	service.calendar = fundingCalendar{err: errors.New("calendar unavailable")}
	_, err := service.ProcessScheduledFunding(context.Background(), time.Date(2026, 8, 19, 9, 20, 0, 0, shanghaiLocation))
	if err == nil {
		t.Fatal("expected calendar error")
	}
	var flows int64
	_ = database.Model(&AccountCashFlow{}).Count(&flows).Error
	if flows != 1 {
		t.Fatalf("flows=%d", flows)
	}
}

func TestNextContributionStaysInCurrentRetryWindow(t *testing.T) {
	service, _ := fundingTestService(t, "2026-08-18")
	next := service.nextContributionAt(context.Background(), time.Date(2026, 8, 19, 9, 21, 37, 0, shanghaiLocation))
	if next == nil || next.Format("2006-01-02 15:04") != "2026-08-19 09:22" {
		t.Fatalf("next=%v", next)
	}
}

func TestScheduledFundingRejectsStaleQuoteAndAcceptsPreviousTradingClose(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		quoteAt    time.Time
		wantPrice  float64
		wantStatus string
	}{
		{name: "stale", quoteAt: time.Date(2026, 8, 14, 15, 0, 0, 0, shanghaiLocation), wantPrice: 12, wantStatus: "partial"},
		{name: "previous close", quoteAt: time.Date(2026, 8, 18, 15, 0, 0, 0, shanghaiLocation), wantPrice: 20, wantStatus: "complete"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, database := fundingTestService(t, "2026-08-18")
			entry := time.Date(2026, 8, 18, 10, 0, 0, 0, shanghaiLocation)
			storedAt := entry
			position := Position{RecommendationID: "quote-age", StockCode: "sh600000", StockName: "浦发银行", Market: "SH",
				Quantity: 100, EntryAt: entry, EntryPrice: 10, BuyFees: 5, CurrentPrice: 12, CurrentPriceAt: &storedAt, Status: "open"}
			if err := database.Create(&position).Error; err != nil {
				t.Fatal(err)
			}
			service.quotes = &scriptedQuotes{quotes: []Quote{{Code: position.StockCode, Name: position.StockName, Market: "SH", Price: 20, At: testCase.quoteAt}}}
			now := time.Date(2026, 8, 19, 9, 20, 0, 0, shanghaiLocation)
			result, err := service.ProcessScheduledFunding(context.Background(), now)
			if err != nil || !result.Applied {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var stored Position
			if err := database.First(&stored, position.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.CurrentPrice != testCase.wantPrice {
				t.Fatalf("current price=%f want=%f", stored.CurrentPrice, testCase.wantPrice)
			}
			var snapshot AccountValuationSnapshot
			if err := database.Where("snapshot_id = ?", "pre-deposit-2026-08-19").First(&snapshot).Error; err != nil {
				t.Fatal(err)
			}
			if snapshot.ValuationStatus != testCase.wantStatus {
				t.Fatalf("status=%s want=%s", snapshot.ValuationStatus, testCase.wantStatus)
			}
		})
	}
}

type delayedAccountQuotes struct {
	delay time.Duration
	calls atomic.Int64
}

func (provider *delayedAccountQuotes) CurrentQuote(ctx context.Context, code string) (Quote, error) {
	provider.calls.Add(1)
	select {
	case <-ctx.Done():
		return Quote{}, ctx.Err()
	case <-time.After(provider.delay):
	}
	return Quote{Code: code, Name: code, Market: "SH", Price: 10, At: time.Now()}, nil
}

func TestAccountOverviewRefreshesTenHoldingsWithBoundedConcurrency(t *testing.T) {
	service, database := fundingTestService(t, "2026-08-18")
	entry := time.Date(2026, 8, 18, 10, 0, 0, 0, shanghaiLocation)
	for index := 0; index < 10; index++ {
		position := Position{RecommendationID: newID(), StockCode: "sh" + fmt.Sprintf("%06d", 600000+index), StockName: "股票",
			Market: "SH", Quantity: 100, EntryAt: entry, EntryPrice: 10, CurrentPrice: 10, Status: "open"}
		if err := database.Create(&position).Error; err != nil {
			t.Fatal(err)
		}
	}
	provider := &delayedAccountQuotes{delay: 100 * time.Millisecond}
	service.quotes = provider
	started := time.Now()
	if _, err := service.AccountOverview(context.Background()); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if provider.calls.Load() != 10 || elapsed >= 600*time.Millisecond {
		t.Fatalf("calls=%d elapsed=%s", provider.calls.Load(), elapsed)
	}
}

func TestProcessScheduledSnapshotIsIdempotentAndPerformanceUsesTWR(t *testing.T) {
	service, database := fundingTestService(t, "2026-08-18")
	if err := database.Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 105000.0).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 15, 5, 0, 0, shanghaiLocation)
	applied, err := service.ProcessScheduledSnapshot(context.Background(), now)
	if err != nil || !applied {
		t.Fatalf("snapshot applied=%v err=%v", applied, err)
	}
	applied, err = service.ProcessScheduledSnapshot(context.Background(), now.Add(time.Minute))
	if err != nil || applied {
		t.Fatalf("duplicate snapshot applied=%v err=%v", applied, err)
	}
	performance, err := service.AccountPerformance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(performance.TimeWeightedReturn-0.05) > 1e-9 || performance.NetProfit != 5000 || len(performance.Curve) != 2 {
		t.Fatalf("performance=%+v", performance)
	}
	if performance.Metrics.SampleLevel != "样本不足" || performance.Metrics.IndustryConcentration != nil {
		t.Fatalf("metrics=%+v", performance.Metrics)
	}
}

func TestPerformanceMetricsUseClosedTradesCostsAndValuationCurve(t *testing.T) {
	service, database := fundingTestService(t, "2026-08-18")
	entry := time.Date(2026, 8, 18, 10, 0, 0, 0, shanghaiLocation)
	exit := entry.Add(24 * time.Hour)
	positions := []Position{
		{RecommendationID: "gain", StockCode: "sh600000", StockName: "盈利", Market: "SH", Quantity: 100, EntryAt: entry, EntryPrice: 10, BuyFees: 5, Status: "closed", ExitAt: &exit, ExitPrice: 11, SellFees: 6, NetPnL: 100},
		{RecommendationID: "loss", StockCode: "sz000001", StockName: "亏损", Market: "SZ", Quantity: 100, EntryAt: entry, EntryPrice: 10, BuyFees: 5, Status: "closed", ExitAt: &exit, ExitPrice: 9.5, SellFees: 6, NetPnL: -50},
	}
	if err := database.Create(&positions).Error; err != nil {
		t.Fatal(err)
	}
	recommendations := []Recommendation{
		{RecommendationID: "gain", AnalysisRunID: "run", StockCode: "sh600000", StockName: "盈利", SignalAt: entry, Status: "closed"},
		{RecommendationID: "missed", AnalysisRunID: "run", StockCode: "sh600001", StockName: "错过", SignalAt: entry, Status: "missed_cash"},
	}
	if err := database.Create(&recommendations).Error; err != nil {
		t.Fatal(err)
	}
	trades := []SimulatedTrade{
		{TradeID: "buy", RecommendationID: "gain", StockCode: "sh600000", Side: "buy", TradedAt: entry, Quantity: 100, Notional: 1000, TotalFees: 5},
		{TradeID: "sell", RecommendationID: "gain", StockCode: "sh600000", Side: "sell", TradedAt: exit, Quantity: 100, Notional: 1100, TotalFees: 6},
	}
	if err := database.Create(&trades).Error; err != nil {
		t.Fatal(err)
	}
	overview := AccountOverview{NetAssetValue: 100000, PositionValue: 25000}
	curve := []AccountPerformancePoint{
		{UnitValue: 1, NetAssetValue: 100000, PositionValue: 20000},
		{UnitValue: 1.1, NetAssetValue: 110000, PositionValue: 55000},
		{UnitValue: 0.99, NetAssetValue: 99000, PositionValue: 0},
	}
	metrics, err := service.performanceMetrics(context.Background(), overview, curve)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.ClosedTrades != 2 || math.Abs(metrics.WinRate-0.5) > 1e-9 || metrics.TotalFees != 11 {
		t.Fatalf("basic metrics=%+v", metrics)
	}
	if math.Abs(metrics.AverageGainRate-100.0/1005.0) > 1e-9 || math.Abs(metrics.AverageLossRate-(-50.0/1005.0)) > 1e-9 || math.Abs(metrics.PayoffRatio-2) > 1e-9 {
		t.Fatalf("return metrics=%+v", metrics)
	}
	if math.Abs(metrics.MaxDrawdown-0.1) > 1e-9 || math.Abs(metrics.MissedExecutionRate-0.5) > 1e-9 {
		t.Fatalf("risk metrics=%+v", metrics)
	}
	wantUtilization := 0.25
	if math.Abs(metrics.CapitalUtilization-wantUtilization) > 1e-9 || metrics.AverageHoldingMinutes != 1440 {
		t.Fatalf("utilization metrics=%+v", metrics)
	}
}
