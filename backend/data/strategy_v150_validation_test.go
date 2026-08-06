package data

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCalculateV150ForwardValidationRemainsPendingWithoutSamples(t *testing.T) {
	result := calculateV150ForwardValidation(nil)
	if result.Status != "forward_validation" || result.Label != "前向验证中" {
		t.Fatalf("unexpected empty validation status: %+v", result)
	}
	if len(result.UnmetConditions) != 8 {
		t.Fatalf("expected every validation gate to remain unmet, got %+v", result.UnmetConditions)
	}
}

func TestCalculateV150ForwardValidationUsesAllFrozenThresholds(t *testing.T) {
	original := db.Dao
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "v150-forward-validation.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Dao = original
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close validation database: %v", err)
		}
	})
	db.Dao = database
	if err := database.AutoMigrate(&models.AiRecommendDailyBar{}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, cnLocation())
	bars := make([]models.AiRecommendDailyBar, 0, 60)
	for day := 0; day < 60; day++ {
		bars = append(bars, models.AiRecommendDailyBar{StockCode: "510300.SH", TradeDate: base.AddDate(0, 0, day), Close: 4})
	}
	if err := database.Create(&bars).Error; err != nil {
		t.Fatal(err)
	}

	items := make([]models.AiRecommendStocksYieldItem, 0, 100)
	for index := 0; index < 100; index++ {
		dayOffset := (index % 40) * 59 / 39
		sell := 10.2
		items = append(items, models.AiRecommendStocksYieldItem{
			SummaryVersion:            "1.5.0",
			StockCode:                 "000001.SZ",
			ExecutionState:            "conditional",
			BacktestEligibility:       recommendBacktestEligible,
			RecommendTime:             base.AddDate(0, 0, dayOffset).Add(10 * time.Hour).Format("2006-01-02 15:04:05"),
			ActivationStatus:          "activated",
			BuyAmount:                 10,
			SellAmount:                &sell,
			ExcessYieldRate:           0.60,
			BenchmarkYieldRateText:    "+1.00%",
			ExcessYieldRateText:       "+0.60%",
			YieldRate:                 1.00,
			YieldRateText:             "+1.00%",
			V150LedgerAccountingReady: true,
			V150LedgerClosed:          true,
			V150LedgerEntryCash:       9000,
			V150LedgerNetPnL:          90,
		})
	}
	result := calculateV150ForwardValidationAsOf(items, base.AddDate(0, 0, 59).Add(23*time.Hour))
	if result.Status != "validated" || len(result.UnmetConditions) != 0 {
		t.Fatalf("expected all frozen validation thresholds to pass: %+v", result)
	}
	if result.TradingDayCount != 60 || result.ClosedTradeCount != 100 || result.ComparableTradeCount != 100 || result.RecommendationDayCount != 40 {
		t.Fatalf("sample counts changed: %+v", result)
	}
	if result.NetMeanPct < 0.75 || result.NetExcessMeanPct < 0.50 || result.ProfitFactor < 1.25 || result.DailyLowerBound90Pct <= 0 {
		t.Fatalf("metric thresholds changed: %+v", result)
	}
	uncomparable := items[0]
	uncomparable.BenchmarkYieldRateText = "--"
	uncomparable.ExcessYieldRateText = "--"
	withMissingComparison := calculateV150ForwardValidationAsOf(
		append(items, uncomparable),
		base.AddDate(0, 0, 59).Add(23*time.Hour),
	)
	if withMissingComparison.Status == "validated" || withMissingComparison.ClosedTradeCount != 101 ||
		withMissingComparison.ComparableTradeCount != 100 {
		t.Fatalf("a selectively missing benchmark comparison passed validation: %+v", withMissingComparison)
	}
}

func TestCalculateV150ForwardValidationExcludesUncomparableBenchmarkTrades(t *testing.T) {
	sell := 10.2
	items := []models.AiRecommendStocksYieldItem{
		{
			SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-04 10:00:00", ActivationStatus: "activated", BuyAmount: 10, SellAmount: &sell,
			ExcessYieldRate: 99, YieldRate: 1, YieldRateText: "+1.00%",
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9000, V150LedgerNetPnL: 90,
		},
		{
			SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-05 10:00:00", ActivationStatus: "activated", BuyAmount: 10, SellAmount: &sell,
			BenchmarkYieldRateText: "+0.10%", ExcessYieldRate: 0.6, ExcessYieldRateText: "+0.60%",
			YieldRate: 1, YieldRateText: "+1.00%",
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9000, V150LedgerNetPnL: 90,
		},
	}

	result := calculateV150ForwardValidation(items)
	if result.ClosedTradeCount != 2 || result.ComparableTradeCount != 1 {
		t.Fatalf("benchmark comparability counts = closed:%d comparable:%d", result.ClosedTradeCount, result.ComparableTradeCount)
	}
	if result.NetExcessMeanPct != 0.6 {
		t.Fatalf("uncomparable default excess entered validation mean: %.4f", result.NetExcessMeanPct)
	}
	if !strings.Contains(strings.Join(result.UnmetConditions, ";"), "基准可比平仓 1/100") {
		t.Fatalf("missing comparable-sample gate: %+v", result.UnmetConditions)
	}
}

func TestCalculateV150ForwardValidationProfitFactorUsesLedgerNetPnL(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{
		{SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-04 10:00:00", ActivationStatus: "activated", YieldRate: 2,
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9000, V150LedgerNetPnL: 200},
		{SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-05 10:00:00", ActivationStatus: "activated", YieldRate: -1,
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9900, V150LedgerNetPnL: -100},
	}
	result := calculateV150ForwardValidation(items)
	if result.ClosedTradeCount != 2 || result.ProfitFactor != 2 || result.NetMeanPct != .5 {
		t.Fatalf("validation did not use ledger return/PnL: %+v", result)
	}
}

func TestV150ForwardValidationBadgeUsesCompleteCohortAcrossDateFilters(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	asOf := time.Date(2026, 8, 7, 16, 0, 0, 0, loc)
	originalNow := timeNow
	timeNow = func() time.Time { return asOf }
	t.Cleanup(func() { timeNow = originalNow })

	closed := appendV150YieldLedgerViewRule(
		t, "validation-complete-cohort", "000001.SZ", "sealed validation trade",
		time.Date(2026, 8, 6, 9, 15, 0, 0, loc),
	)
	fill := appendV150YieldLedgerViewFill(t, closed, 10)
	exit := appendV150YieldLedgerViewExit(t, closed, fill, 11)
	seedV150BenchmarkMinuteBars(t,
		minuteBar{TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4},
		minuteBar{TradeTime: exit.EventAt, Open: 4.2, High: 4.2, Low: 4.2, Close: 4.2},
	)
	day1 := normalizeYieldOverviewTradeDay(fill.EventAt)
	day2 := normalizeYieldOverviewTradeDay(exit.EventAt)
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day1, 4)
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day2, 4.2)
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day1, 4, false)
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day2, 4.2, false)
	seedV150YieldDailyOverviewRawClose(t, closed.symbol, day1, 10.5, false)

	service := NewAiRecommendStocksService()
	full, err := service.GetAiRecommendStocksYieldList(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150, PageSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := service.GetAiRecommendStocksYieldList(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
		StartDate:      "2026-08-07",
		EndDate:        "2026-08-07",
		PageSize:       20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if full.Total != 1 || narrow.Total != 0 {
		t.Fatalf("full/narrow totals = %d/%d, want 1/0", full.Total, narrow.Total)
	}
	if full.V150Validation == nil || narrow.V150Validation == nil ||
		!reflect.DeepEqual(full.V150Validation, narrow.V150Validation) {
		t.Fatalf("date filter changed complete-cohort badge: full=%+v narrow=%+v", full.V150Validation, narrow.V150Validation)
	}
	if full.V150Validation.ClosedTradeCount != 1 || full.V150Validation.ComparableTradeCount != 1 ||
		full.V150Validation.RecommendationDayCount != 1 {
		t.Fatalf("complete cohort was not evaluated: %+v", full.V150Validation)
	}
	if full.TotalYieldRateText == "--" || full.BenchmarkRateText == "--" ||
		full.TotalYieldRate != narrow.TotalYieldRate || full.TotalYieldRateText != narrow.TotalYieldRateText ||
		full.BenchmarkRate != narrow.BenchmarkRate || full.BenchmarkRateText != narrow.BenchmarkRateText ||
		full.ExcessYieldRate != narrow.ExcessYieldRate || full.MaxDrawdown != narrow.MaxDrawdown {
		t.Fatalf("date filter changed complete RuleID portfolio aggregate: full=%+v narrow=%+v", full, narrow)
	}
	if containsV150YieldLedgerViewWarning(narrow.V150HealthWarnings, "", v150BenchmarkPartialHealthCode) {
		t.Fatalf("projection-less rule was still treated as a partial benchmark: %v", narrow.V150HealthWarnings)
	}
}

func TestLoadV150ForwardValidationUsesSingleAsOfAndExcludesFutureBenchmarkDays(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	asOf := time.Date(2025, 1, 7, 16, 0, 0, 0, loc)
	originalNow := timeNow
	timeNow = func() time.Time { return asOf }
	t.Cleanup(func() { timeNow = originalNow })

	appendV150YieldLedgerViewRule(
		t, "validation-explicit-as-of", "000001.SZ", "sealed point-in-time recommendation",
		time.Date(2025, 1, 6, 9, 15, 0, 0, loc),
	)
	bars := []models.AiRecommendDailyBar{
		{StockCode: "510300.SH", TradeDate: time.Date(2025, 1, 6, 0, 0, 0, 0, loc), Close: 4},
		{StockCode: "510300.SH", TradeDate: time.Date(2025, 1, 7, 0, 0, 0, 0, loc), Close: 4.1},
		{StockCode: "510300.SH", TradeDate: time.Date(2025, 1, 8, 0, 0, 0, 0, loc), Close: 4.2},
	}
	if err := db.Dao.Create(&bars).Error; err != nil {
		t.Fatal(err)
	}

	first, err := loadV150ForwardValidation()
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadV150ForwardValidation()
	if err != nil {
		t.Fatal(err)
	}
	if first.TradingDayCount != 2 {
		t.Fatalf("trading days = %d, want only the two sessions visible by %s", first.TradingDayCount, asOf.Format(time.DateTime))
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same frozen asOf produced different validation results: first=%+v second=%+v", first, second)
	}
}

func TestCountCachedV150BenchmarkTradingDaysRequiresCompletePositiveClose(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2025, 1, 6, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	if err := db.Dao.Create([]models.AiRecommendDailyBar{
		{StockCode: defaultBenchmarkModelCode, TradeDate: day1, Close: 4},
		{StockCode: defaultBenchmarkModelCode, TradeDate: day2, Close: 0},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if got := countCachedV150BenchmarkTradingDays(day1.Add(9*time.Hour), day2.Add(16*time.Hour)); got != 1 {
		t.Fatalf("zero-close benchmark row counted as a validation day: %d", got)
	}
	if err := db.Dao.Model(&models.AiRecommendDailyBar{}).
		Where("stock_code = ? AND trade_date = ?", defaultBenchmarkModelCode, day2).
		Update("close", 4.1).Error; err != nil {
		t.Fatal(err)
	}
	if got := countCachedV150BenchmarkTradingDays(day1.Add(9*time.Hour), day2.Add(14*time.Hour)); got != 1 {
		t.Fatalf("incomplete current session counted before close: %d", got)
	}
	if got := countCachedV150BenchmarkTradingDays(day1.Add(9*time.Hour), day2.Add(16*time.Hour)); got != 2 {
		t.Fatalf("complete positive benchmark sessions = %d, want 2", got)
	}
}

func TestLoadV150ForwardValidationIgnoresForgedProjectionYieldStateAndOverride(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	asOf := time.Date(2026, 8, 7, 16, 0, 0, 0, loc)
	originalNow := timeNow
	timeNow = func() time.Time { return asOf }
	t.Cleanup(func() { timeNow = originalNow })

	closed := appendV150YieldLedgerViewRule(
		t, "validation-forged-projection", "000002.SZ", "sealed ledger truth",
		time.Date(2026, 8, 6, 9, 15, 0, 0, loc),
	)
	fill := appendV150YieldLedgerViewFill(t, closed, 10)
	appendV150YieldLedgerViewExit(t, closed, fill, 11)
	closed.projectionID = createV150YieldLedgerViewProjection(t, closed, "activated", "999.00", asOf)

	baseline, err := loadV150ForwardValidation()
	if err != nil {
		t.Fatal(err)
	}
	forgedSell := 999.0
	if err := db.Dao.Create(&models.AiRecommendYieldRecordState{
		RecommendID: closed.projectionID, StockCode: closed.symbol,
		ActivationStatus: "activated", BuyAmount: 1, RealizedSellAmount: &forgedSell,
		YieldRate: 99800, YieldRateText: "+99800.00%",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Create(&models.AiRecommendYieldState{
		StockCode: closed.symbol, ActivationStatus: "activated", BuyAmount: 1,
		RealizedSellAmount: &forgedSell, YieldRate: 99800, YieldRateText: "+99800.00%",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Create(&models.AiRecommendYieldOverride{
		RecommendID: closed.projectionID, StockCode: closed.symbol,
		ActivationStatusOverride: "invalid", RecommendBuyPrice: "1.00",
	}).Error; err != nil {
		t.Fatal(err)
	}

	afterForgery, err := loadV150ForwardValidation()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline, afterForgery) {
		t.Fatalf("mutable projection state changed ledger validation: before=%+v after=%+v", baseline, afterForgery)
	}
	if afterForgery.ClosedTradeCount != 1 || afterForgery.NetMeanPct == 99800 {
		t.Fatalf("forged yield entered validation: %+v", afterForgery)
	}
}

func TestV150ForwardValidationLoaderErrorReturnsEmptyBadgeAndHealthWarning(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	asOf := time.Date(2026, 8, 7, 16, 0, 0, 0, loc)
	originalNow := timeNow
	timeNow = func() time.Time { return asOf }
	t.Cleanup(func() { timeNow = originalNow })

	appendV150ForwardValidationRuleWithConfigHash(
		t, "validation-broken-old-run", "000003.SZ", "broken old run",
		time.Date(2026, 8, 5, 9, 15, 0, 0, loc),
		"forged-config-hash",
	)
	visible := appendV150YieldLedgerViewRule(
		t, "validation-visible-new-run", "000004.SZ", "visible new run",
		time.Date(2026, 8, 6, 9, 15, 0, 0, loc),
	)
	fill := appendV150YieldLedgerViewFill(t, visible, 10)
	exit := appendV150YieldLedgerViewExit(t, visible, fill, 11)
	seedV150BenchmarkMinuteBars(t,
		minuteBar{TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4},
		minuteBar{TradeTime: exit.EventAt, Open: 4.1, High: 4.1, Low: 4.1, Close: 4.1},
	)
	page, err := NewAiRecommendStocksService().GetAiRecommendStocksYieldList(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
		StartDate:      visible.tradeDate,
		EndDate:        visible.tradeDate,
		PageSize:       20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.V150Validation == nil {
		t.Fatalf("readable page did not survive complete-cohort validation error: %+v", page)
	}
	validation := page.V150Validation
	if validation.ClosedTradeCount != 0 || validation.RecommendationDayCount != 0 ||
		validation.NetMeanPct != 0 || len(validation.UnmetConditions) != 8 {
		t.Fatalf("page sample masqueraded as validation after loader error: %+v", validation)
	}
	if !containsV150YieldLedgerViewWarning(page.V150HealthWarnings, "", v150ForwardValidationCohortHealthCode) {
		t.Fatalf("missing complete-cohort validation health warning: %v", page.V150HealthWarnings)
	}
	if page.TotalYieldRateText != "--" || page.BenchmarkRateText != "--" ||
		page.ExcessYieldRateText != "--" || page.StrategyXirrText != "--" ||
		page.BenchmarkXirrText != "--" || page.MaxDrawdownText != "--" {
		t.Fatalf("broken complete cohort published filtered portfolio aggregate: %+v", page)
	}
}

func appendV150YieldLedgerViewExit(
	t *testing.T,
	fixture v150YieldLedgerViewFixture,
	fill models.OrderEvent,
	exitRawPrice float64,
) models.OrderEvent {
	t.Helper()
	cfg := v150.FixedStrategyV150Config()
	exitCost := v150.CalculateTradeCost(
		v150.SideSell,
		v150.ResolveMarket(fixture.symbol),
		exitRawPrice,
		int(fill.Quantity),
		cfg.SlippageScenarios()[0],
		cfg,
	)
	exitSignalAt := fill.EventAt.AddDate(0, 0, 1)
	exitOrderAt := exitSignalAt.Add(15 * time.Minute)
	exitFillAt := exitOrderAt.Add(15 * time.Minute)
	signalFrozenAt := exitSignalAt.Add(10 * time.Second)
	orderFrozenAt := exitOrderAt.Add(10 * time.Second)
	fillFrozenAt := exitFillAt.Add(10 * time.Second)
	events := []models.OrderEvent{
		{
			EventID: fixture.ruleID + "|exit-signal", RunID: fixture.runID, RuleID: fixture.ruleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fixture.tradeDate, Symbol: fixture.symbol,
			EventType: string(v150.EventExitSignal), Sequence: 5, EventAt: exitSignalAt,
			Price: exitCost.EffectivePrice, Quantity: fill.Quantity, Reason: string(v150.ExitTarget),
			PayloadJSON: `{}`, FrozenAt: &signalFrozenAt,
		},
		{
			EventID: fixture.ruleID + "|exit-order", RunID: fixture.runID, RuleID: fixture.ruleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fixture.tradeDate, Symbol: fixture.symbol,
			EventType: "exit_order", Sequence: 6, EventAt: exitOrderAt,
			Quantity: fill.Quantity, Reason: string(v150.ExitTarget), PayloadJSON: `{}`, FrozenAt: &orderFrozenAt,
		},
		{
			EventID: fixture.ruleID + "|exit-fill", RunID: fixture.runID, RuleID: fixture.ruleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fixture.tradeDate, Symbol: fixture.symbol,
			EventType: string(v150.EventExitFill), Sequence: 7, EventAt: exitFillAt,
			Price: exitCost.EffectivePrice, Quantity: fill.Quantity,
			Fees:   exitCost.Commission + exitCost.TransferFee + exitCost.StampDuty,
			Reason: string(v150.ExitTarget), PayloadJSON: `{}`, FrozenAt: &fillFrozenAt,
		},
	}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategyOrderEvents(context.Background(), db.Dao, fixture.runID, events); err != nil {
		t.Fatal(err)
	}
	return events[2]
}

func appendV150ForwardValidationRuleWithConfigHash(
	t *testing.T,
	suffix, symbol, name string,
	decisionAt time.Time,
	configHash string,
) v150YieldLedgerViewFixture {
	t.Helper()
	runID := "validation-run-" + suffix
	ruleID := "validation-rule-" + suffix
	candidateID := "validation-candidate-" + suffix
	frozenAt := decisionAt.Add(2 * time.Minute)
	validFromAt := decisionAt.Add(15 * time.Minute)
	tradeDate := decisionAt.Format(time.DateOnly)
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, RunSlot: "open",
			StartedAt: decisionAt.Add(-15 * time.Minute), AsOf: decisionAt.Add(-5 * time.Minute),
			DataCutoffAt: decisionAt.Add(-5 * time.Minute), DecisionAt: decisionAt,
			GeneratedAt: decisionAt.Add(time.Minute), ValidFromAt: &validFromAt, Mode: "neutral",
			ConfigHash: configHash, InputHash: "input-" + suffix,
			PayloadJSON: `{}`, FrozenAt: &frozenAt,
		},
		Candidates: []models.CandidateSnapshot{{
			CandidateID: candidateID, RunID: runID, StrategyVersion: v150.StrategyVersion,
			TradeDate: tradeDate, Symbol: symbol, Name: name, Sector: "test-sector",
			Rank: 1, FinalRank: 1, Decision: "selected", Score: 88, Eligible: true,
			PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		Rules: []models.RuleSnapshot{{
			RuleID: ruleID, RunID: runID, CandidateID: candidateID,
			StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, Symbol: symbol,
			RuleVersion: v150.StrategyVersion, RuleType: "entry", Path: string(v150.PathPullback),
			ValidFromAt: validFromAt, PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		OrderEvents: []models.OrderEvent{{
			EventID: ruleID + "|issued", RunID: runID, RuleID: ruleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, Symbol: symbol,
			EventType: "rule_issued", Sequence: 1, EventAt: decisionAt,
			Reason: "published", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		t.Fatal(err)
	}
	return v150YieldLedgerViewFixture{
		runID: runID, ruleID: ruleID, candidateID: candidateID,
		symbol: symbol, name: name, decisionAt: decisionAt,
		validFromAt: validFromAt, tradeDate: tradeDate,
	}
}

func TestOneSidedLowerBound90RequiresIndependentDays(t *testing.T) {
	if got := oneSidedLowerBound90([]float64{5}); got != 0 {
		t.Fatalf("single day must not claim a positive confidence bound: %.4f", got)
	}
	if got := oneSidedLowerBound90([]float64{1, 1, 1, 1}); got <= 0 {
		t.Fatalf("stable positive daily groups should have positive lower bound: %.4f", got)
	}
}
