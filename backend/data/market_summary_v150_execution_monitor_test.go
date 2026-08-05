package data

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
)

func TestMarketSummaryV150ExecutionUsesExplicitCompletedCutoffDuringGrace(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	wallClock := time.Date(2026, 8, 5, 9, 45, 30, 0, loc)
	previous := shiftToPrevCNOpenTradeDaySafe(day.AddDate(0, 0, -1))
	previousClose := marketSummaryV150SessionTime(previous, 15, 0)

	ctx := yieldBuildContext{
		Now:                  wallClock,
		InTradingSession:     true,
		LatestTradeDate:      day,
		V150EvaluationCutoff: previousClose,
		DisableMinuteFetch:   true,
	}
	if got := marketSummaryV150ExecutionEvaluatedThrough(ctx); !got.Equal(previousClose) {
		t.Fatalf("explicit cutoff=%s, want %s", got, previousClose)
	}
	ctx.V150EvaluationCutoff = time.Time{}
	if got := marketSummaryV150ExecutionEvaluatedThrough(ctx); !got.Equal(time.Date(2026, 8, 5, 9, 45, 0, 0, loc)) {
		t.Fatalf("legacy wall-clock evaluation=%s, want current 09:45 boundary", got)
	}
}

func TestMarketSummaryV150ExecutionObservationFailureSkipsSymbolAndIsRetryable(t *testing.T) {
	loc := cnLocation()
	now := time.Date(2026, 8, 5, 9, 29, 10, 0, loc)
	decision := time.Date(2026, 8, 5, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 5, 13, 0, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(validFrom))

	previousNow := marketSummaryV150ExecutionSecurityNow
	previousCorporateActionNow := marketSummaryV150CorporateActionNow
	previousFetch := fetchMarketSummaryV150ExecutionSecurityFactFn
	marketSummaryV150ExecutionSecurityNow = func() time.Time { return now }
	marketSummaryV150CorporateActionNow = func() time.Time { return now }
	fetchCalls := 0
	fetchMarketSummaryV150ExecutionSecurityFactFn = func(symbol string, observedAt time.Time) (marketSummaryV150ExecutionSecurityFact, error) {
		fetchCalls++
		if fetchCalls == 1 {
			return marketSummaryV150ExecutionSecurityFact{}, errors.New("temporary quote failure")
		}
		return marketSummaryV150ExecutionSecurityFact{
			Symbol: symbol, Name: "test", Market: "SH", Board: "MAIN", Currency: "CNY",
			Status: "L", ListStatus: "L", Source: "test_realtime_quote", SourceAt: observedAt.Add(-time.Minute),
		}, nil
	}
	t.Cleanup(func() {
		marketSummaryV150ExecutionSecurityNow = previousNow
		marketSummaryV150CorporateActionNow = previousCorporateActionNow
		fetchMarketSummaryV150ExecutionSecurityFactFn = previousFetch
	})

	previousDay := shiftToPrevCNOpenTradeDaySafe(normalizeDailyTradeDate(now).AddDate(0, 0, -1))
	ctx := yieldBuildContext{
		Force: true, Reason: "v150_execution_monitor", Now: now,
		LatestTradeDate: normalizeDailyTradeDate(previousDay), V150EvaluationCutoff: marketSummaryV150SessionTime(previousDay, 15, 0),
		FailOnV150ObservationRefreshError: true,
	}
	records := []*marketSummaryV150ScheduledRecord{{record: rec, ruleID: rec.StrategyRuleID}}
	err := processMarketSummaryV150RecordsInEventOrder(records, ctx, newAiRecommendYieldSnapshotWriter(0, 100))
	var observationErr *MarketSummaryV150ExecutionObservationError
	if !errors.As(err, &observationErr) || !strings.Contains(err.Error(), rec.StockCode) {
		t.Fatalf("first pass error=%v, want structured per-symbol observation error", err)
	}
	var rejectCount int64
	if countErr := db.Dao.Model(&models.OrderEvent{}).
		Where("rule_id = ? AND event_type = ?", rec.StrategyRuleID, string(v150.EventReject)).
		Count(&rejectCount).Error; countErr != nil {
		t.Fatal(countErr)
	}
	if rejectCount != 0 {
		t.Fatalf("temporary observation failure permanently rejected %d rules", rejectCount)
	}

	// The same scheduler slot is safe to retry. Once the observation succeeds,
	// the pre-open pass completes without manufacturing an early bar event.
	records = []*marketSummaryV150ScheduledRecord{{record: rec, ruleID: rec.StrategyRuleID}}
	if err := processMarketSummaryV150RecordsInEventOrder(records, ctx, newAiRecommendYieldSnapshotWriter(0, 100)); err != nil {
		t.Fatalf("same-slot retry failed: %v", err)
	}
	if fetchCalls != 2 {
		t.Fatalf("fetchCalls=%d, want 2", fetchCalls)
	}
	if _, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, validFrom); err != nil {
		t.Fatalf("successful retry did not freeze a causal pre-open observation: %v", err)
	}
}

func TestResolveMarketSummaryV150ExecutionWindowStartupAndSessionBoundaries(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	previous := shiftToPrevCNOpenTradeDaySafe(day.AddDate(0, 0, -1))
	previousClose := marketSummaryV150SessionTime(previous, 15, 0)

	tests := []struct {
		name       string
		now        time.Time
		wantSlot   time.Time
		wantCutoff time.Time
	}{
		{"pre-market startup catches prior close", time.Date(2026, 8, 5, 8, 30, 0, 0, loc), previousClose, previousClose},
		{"pre-open observation", time.Date(2026, 8, 5, 9, 29, 10, 0, loc), marketSummaryV150SessionTime(day, 9, 30), previousClose},
		{"provider grace does not expose partial bar", time.Date(2026, 8, 5, 9, 45, 30, 0, loc), marketSummaryV150SessionTime(day, 9, 30), previousClose},
		{"first morning bar complete", time.Date(2026, 8, 5, 9, 45, 46, 0, loc), marketSummaryV150SessionTime(day, 9, 45), marketSummaryV150SessionTime(day, 9, 45)},
		{"pre-afternoon observation", time.Date(2026, 8, 5, 12, 59, 10, 0, loc), marketSummaryV150SessionTime(day, 13, 0), marketSummaryV150SessionTime(day, 11, 30)},
		{"last bar remains partial during grace", time.Date(2026, 8, 5, 15, 0, 30, 0, loc), marketSummaryV150SessionTime(day, 14, 45), marketSummaryV150SessionTime(day, 14, 45)},
		{"close includes 1445 time-exit bar", time.Date(2026, 8, 5, 15, 0, 46, 0, loc), marketSummaryV150SessionTime(day, 15, 0), marketSummaryV150SessionTime(day, 15, 0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window, ok := ResolveMarketSummaryV150ExecutionWindow(test.now)
			if !ok {
				t.Fatal("window unavailable")
			}
			if !window.SlotAt.Equal(test.wantSlot) || !window.EvaluationCutoff.Equal(test.wantCutoff) {
				t.Fatalf("window=%+v, want slot=%s cutoff=%s", window, test.wantSlot, test.wantCutoff)
			}
		})
	}
}

func TestLoadMarketSummaryV150ActiveExecutionRecordsIncludesPendingAndOpen(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}
	loc := cnLocation()
	decision := time.Date(2026, 8, 5, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)

	pendingRule := seedMarketSummaryV150PortfolioRule(t, 1, "600001.SH", "bank", decision, validFrom, false, nil)
	openRule := seedMarketSummaryV150PortfolioRule(t, 2, "000002.SZ", "technology", decision, validFrom, true, nil)
	exitDay := shiftToNextCNOpenTradeDaySafe(validFrom.AddDate(0, 0, 1))
	exitAt := marketSummaryV150SessionTime(exitDay, 10, 0)
	closedRule := seedMarketSummaryV150PortfolioRule(t, 3, "600003.SH", "consumer", decision, validFrom, true, &exitAt)
	rows := []models.AiRecommendStocks{
		marketSummaryV150MonitorRecommendation(pendingRule, "600001.SH", decision),
		marketSummaryV150MonitorRecommendation(openRule, "000002.SZ", decision),
		marketSummaryV150MonitorRecommendation(closedRule, "600003.SH", decision),
		{DataTime: &decision, StockCode: "600004.SH", SummaryVersion: v150.StrategyVersion, StrategyRunID: "missing-run", StrategyRuleID: "missing-rule"},
	}
	analysisOnly := marketSummaryV150MonitorRecommendation(pendingRule, "600001.SH", decision)
	analysisOnly.ExecutionState = recommendExecutionAnalysisOnly
	rows = append(rows, analysisOnly)
	if err := db.Dao.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	active, pending, open, skipped, warnings, err := loadMarketSummaryV150ActiveExecutionRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || pending != 1 || open != 1 {
		t.Fatalf("active=%d pending=%d open=%d, want 2/1/1", len(active), pending, open)
	}
	warningText := strings.Join(warnings, ";")
	if skipped != 2 || !strings.Contains(warningText, "missing frozen entry rule") || !strings.Contains(warningText, "analysis_only") {
		t.Fatalf("skipped=%d warnings=%v", skipped, warnings)
	}
	seen := map[string]bool{}
	for _, record := range active {
		seen[record.StrategyRuleID] = true
	}
	if !seen[pendingRule] || !seen[openRule] || seen[closedRule] {
		t.Fatalf("wrong lifecycle selection: %+v", seen)
	}
}

func marketSummaryV150MonitorRecommendation(ruleID, symbol string, decision time.Time) models.AiRecommendStocks {
	runID := strings.Split(ruleID, "|rule|")[0]
	return models.AiRecommendStocks{
		DataTime:                    &decision,
		StockCode:                   symbol,
		StockName:                   symbol,
		BkName:                      "test",
		SummaryVersion:              v150.StrategyVersion,
		StrategyRunID:               runID,
		StrategyRuleID:              ruleID,
		ExecutionState:              recommendExecutionConditional,
		ActivationStatus:            "pending",
		RecommendCategory:           recommendExecutionConditional,
		RecommendStopProfitPrice:    "12",
		RecommendStopLossPrice:      "9",
		RecommendStopProfitPriceMin: 12,
	}
}
