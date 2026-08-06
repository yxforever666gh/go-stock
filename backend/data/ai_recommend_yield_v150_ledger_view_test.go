package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
)

func TestGetAiRecommendStocksYieldListV150UsesFrozenRulesAndLedgerLifecycle(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	decisionAt := time.Date(2026, 8, 7, 9, 15, 0, 0, loc)
	asOf := time.Date(2026, 8, 7, 10, 5, 0, 0, loc)
	originalNow := timeNow
	timeNow = func() time.Time { return asOf }
	t.Cleanup(func() { timeNow = originalNow })

	holding := appendV150YieldLedgerViewRule(t, "holding", "000001.SZ", "sealed holding", decisionAt)
	fill := appendV150YieldLedgerViewFill(t, holding, 10)
	holding.projectionID = createV150YieldLedgerViewProjection(t, holding, "pending", "999.00", asOf)

	forged := appendV150YieldLedgerViewRule(t, "forged", "000002.SZ", "sealed pending with projection", decisionAt)
	forged.projectionID = createV150YieldLedgerViewProjection(t, forged, "activated", "888.00", asOf)
	if err := db.Dao.Create(&models.AiRecommendYieldRecordState{
		RecommendID: forged.projectionID, StockCode: forged.symbol,
		ActivationStatus: "activated", BuyAmount: 1, CurrentPrice: 888,
		YieldRate: 88700, YieldRateText: "+88700.00%",
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Same stock and trade date as forged. V1.5 must enumerate both RuleIDs
	// instead of applying the legacy same-day symbol collapse.
	withoutProjection := appendV150YieldLedgerViewRule(t, "without-projection", "000002.SZ", "sealed without projection", decisionAt)
	withoutProjectionFill := appendV150YieldLedgerViewFill(t, withoutProjection, 20)

	service := NewAiRecommendStocksService()
	for _, cohort := range []string{marketSummaryVersion150, strategyCohortCurrent} {
		page, err := service.GetAiRecommendStocksYieldList(&models.AiRecommendStocksQuery{
			StrategyCohort: cohort,
			YieldMode:      aiRecommendYieldModeFast,
			PageSize:       20,
		})
		if err != nil {
			t.Fatalf("cohort %s: %v", cohort, err)
		}
		if page.CalcMode != aiRecommendYieldModeStrict {
			t.Fatalf("cohort %s calcMode=%q, want strict", cohort, page.CalcMode)
		}
		if page.Total != 3 || len(page.List) != 3 {
			t.Fatalf("cohort %s returned total/list=%d/%d, want 3/3", cohort, page.Total, len(page.List))
		}
		items := v150YieldLedgerViewItemsByRule(page.List)
		assertV150YieldLedgerViewHolding(t, items[holding.ruleID], holding, fill, 0, "--")
		if item := items[forged.ruleID]; item.RowKey != forged.ruleID || item.RecommendID != forged.projectionID ||
			item.ActivationStatus != "pending" || item.BuyAmount != 0 || item.SellAmount != nil ||
			item.YieldRate != 0 || item.YieldRateText != "--" || item.V150LedgerAccountingReady {
			t.Fatalf("forged mutable activation leaked into V1.5 item: %+v", item)
		}
		assertV150YieldLedgerViewHolding(t, items[withoutProjection.ruleID], withoutProjection, withoutProjectionFill, 0, "--")
		if page.TotalYieldRateText != "--" {
			t.Fatalf("missing cache quote published portfolio return: %q", page.TotalYieldRateText)
		}
		if page.BenchmarkRateText != "--" || page.StrategyXirrText != "--" || page.BenchmarkXirrText != "--" ||
			page.ExcessYieldRateText != "--" || page.ExcessXirrText != "--" {
			t.Fatalf("RecommendID=0 collision published benchmark comparison: %+v", page)
		}
		if !containsV150YieldLedgerViewWarning(page.V150HealthWarnings, "", v150ForwardValidationCohortHealthCode) {
			t.Fatalf("policy-invalid complete cohort did not fail the aggregate closed: %v", page.V150HealthWarnings)
		}
		if !containsV150YieldLedgerViewWarning(page.V150HealthWarnings, holding.symbol, v150YieldValuationHealthCode) {
			t.Fatalf("missing quote health warning: %v", page.V150HealthWarnings)
		}
	}

	quoteAt := asOf.Add(-time.Minute)
	availableAt := quoteAt.Add(10 * time.Second)
	if err := db.Dao.Create(&StockInfo{
		Model: gorm.Model{CreatedAt: availableAt, UpdatedAt: availableAt},
		Date:  quoteAt.Format(time.DateOnly), Time: quoteAt.Format(time.TimeOnly),
		Code: holding.symbol, Name: holding.name, Price: "10.50",
	}).Error; err != nil {
		t.Fatal(err)
	}

	page, err := service.GetAiRecommendStocksYieldList(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
		YieldMode:      aiRecommendYieldModeFast,
		PageSize:       20,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := v150YieldLedgerViewItemsByRule(page.List)[holding.ruleID]
	entryCash := fill.Price*fill.Quantity + fill.Fees
	cfg := v150.FixedStrategyV150Config()
	mark := v150.CalculateTradeCost(v150.SideSell, v150.ResolveMarket(holding.symbol), 10.50, int(fill.Quantity), cfg.SlippageScenarios()[0], cfg)
	wantYield := round2((mark.CashFlow - entryCash) / entryCash * 100)
	assertV150YieldLedgerViewHolding(t, item, holding, fill, 10.50, formatSignedPercent(wantYield))
	if item.YieldRate != wantYield || !item.V150LedgerAccountingReady {
		t.Fatalf("cache quote ledger yield=%v/%q ready=%v, want %v; item=%+v", item.YieldRate, item.YieldRateText, item.V150LedgerAccountingReady, wantYield, item)
	}
	if item.CurrentPrice == 999 {
		t.Fatalf("forged projection mark affected yield: %+v", item)
	}
}

func TestV150YieldQuoteFreshnessUsesFixedFiveMinuteBoundary(t *testing.T) {
	asOf := time.Date(2026, 8, 7, 10, 0, 0, 0, cnLocation())
	if !v150YieldQuoteIsFresh(asOf.Add(-5*time.Minute), asOf) {
		t.Fatal("exact five-minute cache quote should be fresh")
	}
	if v150YieldQuoteIsFresh(asOf.Add(-5*time.Minute-time.Nanosecond), asOf) {
		t.Fatal("quote older than five minutes should be unavailable")
	}
	if v150YieldQuoteIsFresh(asOf.Add(time.Nanosecond), asOf) {
		t.Fatal("future quote should be unavailable")
	}
}

func TestV150YieldBenchmarkRejectsActivatedRulesWithoutProjectionIdentity(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{
		{SummaryVersion: v150.StrategyVersion, RecommendID: 0, ActivationStatus: "activated", BacktestEligibility: recommendBacktestEligible},
		{SummaryVersion: v150.StrategyVersion, RecommendID: 0, ActivationStatus: "activated", BacktestEligibility: recommendBacktestEligible},
	}
	if !v150YieldHasActivatedRuleWithoutProjection(items) {
		t.Fatal("two projection-less activated rules must fail closed before RecommendID-keyed benchmark aggregation")
	}
	items[0].ActivationStatus = "pending"
	items[1].RecommendID = 42
	if v150YieldHasActivatedRuleWithoutProjection(items) {
		t.Fatal("pending rule or uniquely projected activated rule should not trip the identity guard")
	}
}

type v150YieldLedgerViewFixture struct {
	runID        string
	ruleID       string
	candidateID  string
	symbol       string
	name         string
	decisionAt   time.Time
	validFromAt  time.Time
	tradeDate    string
	projectionID uint
}

func initV150YieldLedgerViewTestDB(t *testing.T) {
	t.Helper()
	initMarketSummaryV150ExecutionTestDB(t)
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AiRecommendYieldOverride{},
		&models.AiRecommendOpeningReview{},
		&StockInfo{},
	); err != nil {
		t.Fatal(err)
	}
	clearMinuteCoverageStatsCache()
}

func appendV150YieldLedgerViewRule(
	t *testing.T,
	suffix, symbol, name string,
	decisionAt time.Time,
) v150YieldLedgerViewFixture {
	t.Helper()
	runID := "yield-ledger-view-run-" + suffix
	ruleID := "yield-ledger-view-rule-" + suffix
	candidateID := "yield-ledger-view-candidate-" + suffix
	frozenAt := decisionAt.Add(2 * time.Minute)
	validFromAt := decisionAt.Add(15 * time.Minute)
	tradeDate := decisionAt.Format(time.DateOnly)
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, RunSlot: "open",
			StartedAt: decisionAt.Add(-15 * time.Minute), AsOf: decisionAt.Add(-5 * time.Minute),
			DataCutoffAt: decisionAt.Add(-5 * time.Minute), DecisionAt: decisionAt,
			GeneratedAt: decisionAt.Add(time.Minute), ValidFromAt: &validFromAt, Mode: "neutral",
			ConfigHash: v150.FixedStrategyV150ConfigHash(), InputHash: "input-" + suffix,
			PayloadJSON: `{"run":{"regime":{"dailyCap":1}}}`, FrozenAt: &frozenAt,
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

func appendV150YieldLedgerViewFill(
	t *testing.T,
	fixture v150YieldLedgerViewFixture,
	fillPrice float64,
) models.OrderEvent {
	t.Helper()
	cfg := v150.FixedStrategyV150Config()
	quantity := v150.SizeRoundLot(fillPrice, cfg.TargetCashPerPosition, cfg).Quantity
	rawPrice := fillPrice / (1 + cfg.BaseSlippageBPS/10_000)
	cost := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket(fixture.symbol), rawPrice, quantity, cfg.SlippageScenarios()[0], cfg)
	signalAt := fixture.validFromAt.Add(15 * time.Minute)
	orderAt := signalAt.Add(15 * time.Minute)
	signalFrozenAt := signalAt.Add(10 * time.Second)
	orderFrozenAt := orderAt.Add(10 * time.Second)
	fillFrozenAt := orderAt.Add(20 * time.Second)
	events := []models.OrderEvent{
		{
			EventID: fixture.ruleID + "|signal", RunID: fixture.runID, RuleID: fixture.ruleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fixture.tradeDate, Symbol: fixture.symbol,
			EventType: "signal", Sequence: 2, EventAt: signalAt, Price: fillPrice,
			Reason: "pullback", PayloadJSON: `{}`, FrozenAt: &signalFrozenAt,
		},
		{
			EventID: fixture.ruleID + "|order", RunID: fixture.runID, RuleID: fixture.ruleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fixture.tradeDate, Symbol: fixture.symbol,
			EventType: "order", Sequence: 3, EventAt: orderAt, Quantity: float64(quantity),
			Reason: "next_bar", PayloadJSON: `{}`, FrozenAt: &orderFrozenAt,
		},
		{
			EventID: fixture.ruleID + "|fill", RunID: fixture.runID, RuleID: fixture.ruleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fixture.tradeDate, Symbol: fixture.symbol,
			EventType: "fill", Sequence: 4, EventAt: orderAt, Price: fillPrice, Quantity: float64(quantity),
			Fees:   cost.Commission + cost.TransferFee + cost.StampDuty,
			Reason: "filled", PayloadJSON: `{}`, FrozenAt: &fillFrozenAt,
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

func createV150YieldLedgerViewProjection(
	t *testing.T,
	fixture v150YieldLedgerViewFixture,
	activationStatus, currentPrice string,
	asOf time.Time,
) uint {
	t.Helper()
	row := models.AiRecommendStocks{
		DataTime: &fixture.decisionAt, ProviderName: "mutable-provider", ModelName: "mutable-model",
		StockCode: fixture.symbol, StockName: "forged projection name",
		SummaryVersion: v150.StrategyVersion, StrategyRunID: fixture.runID, StrategyRuleID: fixture.ruleID,
		ExecutionState: recommendExecutionAnalysisOnly, RecommendStatus: "forged",
		ActivationStatus: activationStatus, StockCurrentPrice: currentPrice,
		StockCurrentPriceTime: asOf.Format(time.DateTime),
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func v150YieldLedgerViewItemsByRule(items []models.AiRecommendStocksYieldItem) map[string]models.AiRecommendStocksYieldItem {
	result := make(map[string]models.AiRecommendStocksYieldItem, len(items))
	for _, item := range items {
		result[item.RowKey] = item
	}
	return result
}

func assertV150YieldLedgerViewHolding(
	t *testing.T,
	item models.AiRecommendStocksYieldItem,
	fixture v150YieldLedgerViewFixture,
	fill models.OrderEvent,
	currentPrice float64,
	yieldText string,
) {
	t.Helper()
	if item.RowKey != fixture.ruleID || item.RecommendID != fixture.projectionID || item.ActivationStatus != "activated" ||
		item.PositionStatus != "holding" || item.BuyAmount != fill.Price || item.ActivationPrice != fill.Price ||
		item.CurrentPrice != currentPrice || item.YieldRateText != yieldText {
		t.Fatalf("holding item did not follow frozen fill/cache quote: %+v", item)
	}
}

func containsV150YieldLedgerViewWarning(warnings []string, symbol, code string) bool {
	want := code
	if normalized := normalizeRecommendStockCode(symbol); normalized != "" {
		want = normalized + ":" + code
	}
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
