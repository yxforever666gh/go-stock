package data

import (
	"math"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

func TestFillV150YieldRecordMetricsReadsClosedCorporateActionLedger(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	entryAt := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	actionAt := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	exitAt := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	entry, action, exit := seedV150YieldCorporateActionLedger(t, "run-ledger-closed", "rule-ledger-closed", entryAt, actionAt, &exitAt)

	state := models.AiRecommendYieldRecordState{
		StockCode: "000001.SZ", ActivationStatus: "activated", BuyAmount: 10,
	}
	record := models.AiRecommendStocks{
		SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ",
		StrategyRunID: entry.RunID, StrategyRuleID: entry.RuleID,
		ActivationRuleJSON: `{}`,
	}
	record.ID = 1
	fillYieldRecordMetrics(&state, yieldRecordMetricContext{Record: record, AsOf: exitAt.Add(time.Hour)})
	entryCash := entry.Price*entry.Quantity + entry.Fees
	exitCash := exit.Price*exit.Quantity - exit.Fees
	want := round2((exitCash + action.CashAmount - entryCash) / entryCash * 100)
	if state.YieldRate != want || state.YieldRateText != formatSignedPercent(want) {
		t.Fatalf("ledger yield=%.2f %q want=%.2f; entry=%+v action=%+v exit=%+v", state.YieldRate, state.YieldRateText, want, entry, action, exit)
	}
	state.RecommendID = record.ID
	state.SellTime = &exitAt
	realized := 8.4
	state.RealizedSellAmount = &realized
	item := mapRecommendRecordStateToYieldItem(record, state)
	if err := attachV150YieldLedgerProjection(&item, record, exitAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	wantNetPnL := roundReplayTestValue(exitCash + action.CashAmount - entryCash)
	if item.YieldRate != want || !item.V150LedgerAccountingReady || !item.V150LedgerClosed ||
		item.V150LedgerQuantity != action.Quantity || item.V150LedgerCorporateCash != action.CashAmount ||
		math.Abs(item.V150LedgerNetPnL-wantNetPnL) > 1e-8 {
		t.Fatalf("API item lost corporate-action ledger accounting: %+v", item)
	}
	total, totalText := calculateYieldTotalByItems([]models.AiRecommendStocksYieldItem{item})
	wantTotal := round2(wantNetPnL / v150.FixedStrategyV150Config().PortfolioCash * 100)
	if total != wantTotal || totalText != formatSignedPercent(wantTotal) {
		t.Fatalf("portfolio total ignored ledger cash/quantity: total=%.2f/%q want=%.2f", total, totalText, wantTotal)
	}
	validation := calculateV150ForwardValidation([]models.AiRecommendStocksYieldItem{item})
	if validation.ClosedTradeCount != 1 || validation.ProfitFactor != 999 {
		t.Fatalf("validation ignored profitable corporate-action ledger: %+v", validation)
	}
}

func TestBuildYieldFallbackPageAttachesClosedCorporateActionLedger(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldOverride{},
		&models.AiRecommendYieldMeta{},
	); err != nil {
		t.Fatal(err)
	}
	loc := cnLocation()
	entryAt := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	actionAt := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	exitAt := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	asOf := exitAt.Add(time.Hour)
	entry, action, exit := seedV150YieldCorporateActionLedger(t, "run-ledger-fallback", "rule-ledger-fallback", entryAt, actionAt, &exitAt)

	record := models.AiRecommendStocks{
		DataTime: &entryAt, SummaryVersion: v150.StrategyVersion,
		StockCode: "000001.SZ", StockName: "ledger fallback", ExecutionState: recommendExecutionConditional,
		RecommendCategory: "conditional", RecommendStatus: "valid", ActivationStatus: "activated",
		ActivationRuleJSON: `{}`, StrategyRunID: entry.RunID, StrategyRuleID: entry.RuleID,
	}
	if err := db.Dao.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	realized := 99.0 // Deliberately wrong: the API must ignore this mutable display value.
	state := models.AiRecommendYieldRecordState{
		RecommendID: record.ID, StockCode: record.StockCode, RecommendTime: &entryAt,
		ActivationStatus: "activated", ActivationTime: &entryAt, BuyTime: &entryAt, BuyAmount: 10,
		SellTime: &exitAt, RealizedSellAmount: &realized, CurrentPrice: realized,
	}
	if err := db.Dao.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	oldNow := timeNow
	timeNow = func() time.Time { return asOf }
	t.Cleanup(func() { timeNow = oldNow })

	page, err := NewAiRecommendStocksService().buildYieldFallbackPage(
		&models.AiRecommendStocksQuery{StrategyCohort: marketSummaryVersion150},
		1, 20, asOf.Format("2006-01-02 15:04:05"), true, 50, "", 0, time.Time{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.List) != 1 {
		t.Fatalf("fallback list length=%d", len(page.List))
	}
	item := page.List[0]
	entryCash := entry.Price*entry.Quantity + entry.Fees
	exitCash := exit.Price*exit.Quantity - exit.Fees
	wantNetPnL := roundReplayTestValue(exitCash + action.CashAmount - entryCash)
	if !item.V150LedgerAccountingReady || !item.V150LedgerClosed ||
		item.V150LedgerQuantity != action.Quantity || item.V150LedgerCorporateCash != action.CashAmount ||
		math.Abs(item.V150LedgerNetPnL-wantNetPnL) > 1e-8 {
		t.Fatalf("fallback page lost sealed corporate-action ledger: %+v", item)
	}
	wantTotal := round2(wantNetPnL / v150.FixedStrategyV150Config().PortfolioCash * 100)
	if page.TotalYieldRate != wantTotal || page.TotalYieldRateText != formatSignedPercent(wantTotal) {
		t.Fatalf("fallback total=%v/%q want=%v", page.TotalYieldRate, page.TotalYieldRateText, wantTotal)
	}
}

func TestFillV150YieldRecordMetricsCannotSeeLateFrozenExit(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	entryAt := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	actionAt := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	exitAt := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	asOf := exitAt.Add(time.Hour)
	lateFrozenAt := asOf.Add(time.Hour)
	entry, _, _ := seedV150YieldCorporateActionLedger(t, "run-ledger-late", "rule-ledger-late", entryAt, actionAt, &exitAt, lateFrozenAt)

	realized := 8.4
	state := models.AiRecommendYieldRecordState{
		StockCode: "000001.SZ", ActivationStatus: "activated", BuyAmount: 10,
		RealizedSellAmount: &realized,
	}
	record := models.AiRecommendStocks{
		SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ",
		StrategyRunID: entry.RunID, StrategyRuleID: entry.RuleID,
	}
	fillYieldRecordMetrics(&state, yieldRecordMetricContext{Record: record, AsOf: asOf})
	if state.YieldRate != 0 || state.YieldRateText != "--" {
		t.Fatalf("late-frozen exit leaked into point-in-time projection: %+v", state)
	}
}

func TestFillV150YieldRecordMetricsReadsOpenCorporateActionLedger(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	entryAt := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	actionAt := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	entry, action, _ := seedV150YieldCorporateActionLedger(t, "run-ledger-open", "rule-ledger-open", entryAt, actionAt, nil)

	state := models.AiRecommendYieldRecordState{
		StockCode: "000001.SZ", ActivationStatus: "activated", BuyAmount: 10, CurrentPrice: 8.2,
	}
	record := models.AiRecommendStocks{
		SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ",
		StrategyRunID: entry.RunID, StrategyRuleID: entry.RuleID,
	}
	fillYieldRecordMetrics(&state, yieldRecordMetricContext{Record: record, AsOf: actionAt.Add(time.Hour)})
	cfg := v150.FixedStrategyV150Config()
	mark := v150.CalculateTradeCost(v150.SideSell, v150.MarketSZ, state.CurrentPrice, int(action.Quantity), cfg.SlippageScenarios()[0], cfg)
	entryCash := entry.Price*entry.Quantity + entry.Fees
	want := round2((mark.CashFlow + action.CashAmount - entryCash) / entryCash * 100)
	if state.YieldRate != want || state.YieldRateText != formatSignedPercent(want) {
		t.Fatalf("open ledger yield=%.2f %q want=%.2f", state.YieldRate, state.YieldRateText, want)
	}
}

func TestFillYieldRecordMetricsKeepsLegacyCohortPriceOnly(t *testing.T) {
	realized := 8.4
	state := models.AiRecommendYieldRecordState{
		StockCode: "000001.SZ", ActivationStatus: "activated", BuyAmount: 10,
		RealizedSellAmount: &realized,
	}
	record := models.AiRecommendStocks{SummaryVersion: "1.4.2", StockCode: "000001.SZ", StrategyRunID: "missing", StrategyRuleID: "missing"}
	fillYieldRecordMetrics(&state, yieldRecordMetricContext{Record: record, AsOf: time.Now()})
	want := calculateNetYield(state.StockCode, state.BuyAmount, realized)
	if !want.Valid || state.YieldRate != want.YieldRate || state.YieldRateText != want.YieldText {
		t.Fatalf("legacy metric changed: got=%.2f/%q want=%+v", state.YieldRate, state.YieldRateText, want)
	}
}

func seedV150YieldCorporateActionLedger(
	t *testing.T,
	runID, ruleID string,
	entryAt, actionAt time.Time,
	exitAt *time.Time,
	exitFrozenAtOverride ...time.Time,
) (models.OrderEvent, models.OrderEvent, models.OrderEvent) {
	t.Helper()
	cfg := v150.FixedStrategyV150Config()
	entryCost := v150.CalculateTradeCost(v150.SideBuy, v150.MarketSZ, 10, 900, cfg.SlippageScenarios()[0], cfg)
	entryFrozen := entryAt.Add(time.Second)
	actionFrozen := actionAt.Add(time.Second)
	entry := models.OrderEvent{
		EventID: runID + "|fill", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
		TradeDate: entryAt.Format(time.DateOnly), Symbol: "000001.SZ", EventType: "fill", Sequence: 1,
		EventAt: entryAt, Price: entryCost.EffectivePrice, Quantity: 900,
		Fees: entryCost.Commission + entryCost.TransferFee + entryCost.StampDuty, PayloadJSON: `{}`, FrozenAt: &entryFrozen,
	}
	action := models.OrderEvent{
		EventID: runID + "|corporate", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
		TradeDate: entryAt.Format(time.DateOnly), Symbol: "000001.SZ", EventType: "corporate_action", Sequence: 2,
		EventAt: actionAt, Quantity: 1125, CashAmount: 108, AdjustmentFactor: .8,
		PayloadJSON: `{}`, FrozenAt: &actionFrozen,
	}
	events := []models.OrderEvent{entry, action}
	exit := models.OrderEvent{}
	if exitAt != nil {
		exitCost := v150.CalculateTradeCost(v150.SideSell, v150.MarketSZ, 8.4, int(action.Quantity), cfg.SlippageScenarios()[0], cfg)
		exitFrozen := exitAt.Add(time.Second)
		if len(exitFrozenAtOverride) > 0 {
			exitFrozen = exitFrozenAtOverride[0]
		}
		exit = models.OrderEvent{
			EventID: runID + "|exit", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
			TradeDate: entryAt.Format(time.DateOnly), Symbol: "000001.SZ", EventType: "exit_fill", Sequence: 3,
			EventAt: *exitAt, Price: exitCost.EffectivePrice, Quantity: action.Quantity,
			Fees: exitCost.Commission + exitCost.TransferFee + exitCost.StampDuty, Reason: "target", PayloadJSON: `{}`, FrozenAt: &exitFrozen,
		}
		events = append(events, exit)
	}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	entry, action = events[0], events[1]
	if len(events) == 3 {
		exit = events[2]
	}
	if math.Trunc(action.Quantity) != action.Quantity {
		t.Fatalf("test action produced fractional shares: %+v", action)
	}
	return entry, action, exit
}

func roundReplayTestValue(value float64) float64 {
	return math.Round(value*1e8) / 1e8
}
