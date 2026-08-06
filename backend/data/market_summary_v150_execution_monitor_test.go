package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/execution"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

type marketSummaryV150MonitorContextKey struct{}

type recordingMarketSummaryV150ContextEvaluator struct {
	delegate marketSummaryV150ExecutionEvaluator
	calls    int
}

func (e *recordingMarketSummaryV150ContextEvaluator) Evaluate(ctx execution.ExecutionContext) (execution.EvaluationResult, error) {
	e.calls++
	return e.delegate.Evaluate(ctx)
}

func loadMarketSummaryV150ActiveExecutionRecordsForTest(observedAt time.Time) ([]models.AiRecommendStocks, int, int, int, []string, error) {
	sink := newMarketSummaryV150OrderEventSink(context.Background(), persistence.NewGORMOrderEventStore(db.Dao))
	return loadMarketSummaryV150ActiveExecutionRecordsWithSink(observedAt, sink)
}

func TestCompatibilityExecutionMonitorCanceledContextDoesNotAppend(t *testing.T) {
	store := &recordingMarketSummaryV150OrderEventStore{}
	monitor := NewCompatibilityExecutionMonitor(store, execution.Evaluator{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := monitor.Run(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if store.calls != 0 {
		t.Fatalf("canceled monitor appended %d event batches", store.calls)
	}
}

func TestMarketSummaryV150CompatibilityProducersWithoutStoreFailClosed(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	now := time.Now().In(cnLocation())
	if _, err := RunMarketSummaryV150ExecutionMonitor(now); !errors.Is(err, errMarketSummaryV150OrderEventStoreUnavailable) {
		t.Fatalf("compatibility monitor error = %v", err)
	}
	if _, _, _, _, _, err := loadMarketSummaryV150ActiveExecutionRecords(now); !errors.Is(err, errMarketSummaryV150OrderEventStoreUnavailable) {
		t.Fatalf("compatibility enumeration error = %v", err)
	}
	var count int64
	if err := db.Dao.Model(&models.OrderEvent{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing store compatibility producers appended %d order events", count)
	}
}

func TestCompatibilityExecutionMonitorFillUsesInjectedStoreAndContext(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	observedAt := time.Date(2026, 8, 4, 10, 15, 46, 0, loc)
	initMarketSummaryV150ExecutionTestDB(t)
	if err := db.Dao.AutoMigrate(&models.AiRecommendYieldMeta{}, &models.AiRecommendYieldRecordState{}); err != nil {
		t.Fatal(err)
	}
	recommendation := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, marketSummaryV150TestBreakoutPlan(validFrom), true)
	seedMarketSummaryV150BreakoutBars(t, recommendation, decision, validFrom)

	previousNow := marketSummaryV150ExecutionSecurityNow
	previousCorporateActionNow := marketSummaryV150CorporateActionNow
	previousFetch := fetchMarketSummaryV150ExecutionSecurityFactFn
	securityNow := validFrom.Add(-time.Minute)
	marketSummaryV150ExecutionSecurityNow = func() time.Time { return securityNow }
	marketSummaryV150CorporateActionNow = func() time.Time { return observedAt }
	fetchMarketSummaryV150ExecutionSecurityFactFn = func(symbol string, at time.Time) (marketSummaryV150ExecutionSecurityFact, error) {
		return marketSummaryV150ExecutionSecurityFact{
			Symbol: symbol, Name: "test", Market: "SH", Board: "MAIN", Currency: "CNY",
			Status: "L", ListStatus: "L", Source: "test_realtime_quote", SourceAt: at.Add(-time.Minute),
		}, nil
	}
	t.Cleanup(func() {
		marketSummaryV150ExecutionSecurityNow = previousNow
		marketSummaryV150CorporateActionNow = previousCorporateActionNow
		fetchMarketSummaryV150ExecutionSecurityFactFn = previousFetch
	})
	if _, err := refreshMarketSummaryV150ExecutionSecurityObservation(recommendation.StrategyRunID, recommendation.StockCode, true); err != nil {
		t.Fatalf("seed causal pre-open security observation: %v", err)
	}
	securityNow = observedAt

	store := &recordingMarketSummaryV150OrderEventStore{delegate: persistence.NewGORMOrderEventStore(db.Dao)}
	evaluator := &recordingMarketSummaryV150ContextEvaluator{delegate: execution.Evaluator{}}
	monitor := NewCompatibilityExecutionMonitor(store, evaluator)
	ctx := context.WithValue(context.Background(), marketSummaryV150MonitorContextKey{}, "monitor-fill")
	result, err := monitor.Run(ctx, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProcessedCount != 1 || store.calls != 1 || len(store.batches) != 1 {
		t.Fatalf("result=%+v store calls=%d batches=%d", result, store.calls, len(store.batches))
	}
	if evaluator.calls == 0 {
		t.Fatal("production monitor did not invoke backend/execution evaluator")
	}
	if got := marketSummaryV150TestEventTypes(store.batches[0]); got != "signal,order,fill" {
		t.Fatalf("injected batch=%s", got)
	}
	if len(store.contexts) != 1 || store.contexts[0].Value(marketSummaryV150MonitorContextKey{}) != "monitor-fill" {
		t.Fatal("monitor append did not preserve the caller context")
	}
	var events []models.OrderEvent
	if err := db.Dao.Where("rule_id = ?", recommendation.StrategyRuleID).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if got := marketSummaryV150TestEventTypes(events); got != "rule_issued,signal,order,fill" {
		t.Fatalf("persisted events=%s", got)
	}
}

func TestCompatibilityExecutionMonitorCorruptPlanRejectUsesInjectedStoreAndContext(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	observedAt := time.Date(2026, 8, 4, 10, 15, 46, 0, loc)
	initMarketSummaryV150ExecutionTestDB(t)
	recommendation := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, marketSummaryV150TestBreakoutPlan(validFrom), false)
	if err := db.Dao.Exec("DROP TRIGGER immutable_strategy_rule_snapshot_update").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Exec("UPDATE strategy_rule_snapshot SET payload_json = ? WHERE rule_id = ?", `{}`, recommendation.StrategyRuleID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Exec("CREATE TRIGGER immutable_strategy_rule_snapshot_update BEFORE UPDATE ON strategy_rule_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_rule_snapshot'); END").Error; err != nil {
		t.Fatal(err)
	}

	store := &recordingMarketSummaryV150OrderEventStore{delegate: persistence.NewGORMOrderEventStore(db.Dao)}
	monitor := NewCompatibilityExecutionMonitor(store, execution.Evaluator{})
	ctx := context.WithValue(context.Background(), marketSummaryV150MonitorContextKey{}, "monitor-reject")
	result, err := monitor.Run(ctx, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.SkippedCount != 1 || store.calls != 1 || len(store.batches) != 1 || len(store.batches[0]) != 1 || store.batches[0][0].EventType != string(v150.EventReject) {
		t.Fatalf("result=%+v calls=%d batches=%+v", result, store.calls, store.batches)
	}
	if len(store.contexts) != 1 || store.contexts[0].Value(marketSummaryV150MonitorContextKey{}) != "monitor-reject" {
		t.Fatal("corrupt-plan reject did not preserve the caller context")
	}
}

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
	ctx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{
		Force: true, Reason: "v150_execution_monitor", Now: now,
		LatestTradeDate: normalizeDailyTradeDate(previousDay), V150EvaluationCutoff: marketSummaryV150SessionTime(previousDay, 15, 0),
		FailOnV150ObservationRefreshError: true,
	})
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

	pendingRule := seedMarketSummaryV150MonitorFrozenRule(t, "600001.SH", "bank", decision, validFrom, false, nil)
	openRule := seedMarketSummaryV150MonitorFrozenRule(t, "000002.SZ", "technology", decision, validFrom, true, nil)
	exitDay := shiftToNextCNOpenTradeDaySafe(validFrom.AddDate(0, 0, 1))
	exitAt := marketSummaryV150SessionTime(exitDay, 10, 0)
	closedRule := seedMarketSummaryV150MonitorFrozenRule(t, "600003.SH", "consumer", decision, validFrom, true, &exitAt)
	missingProjectionRule := seedMarketSummaryV150MonitorFrozenRule(t, "600004.SH", "industrial", decision, validFrom, false, nil)
	analysisProjectionRule := seedMarketSummaryV150MonitorFrozenRule(t, "000005.SZ", "healthcare", decision, validFrom, false, nil)
	openProjection := marketSummaryV150MonitorRecommendation(openRule, "000002.SZ", decision)
	openProjection.StockCode = "300999.SZ"
	openProjection.StrategyRunID = "forged-run"
	openProjection.RecommendCategory = "avoid"
	analysisProjection := marketSummaryV150MonitorRecommendation(analysisProjectionRule, "000005.SZ", decision)
	analysisProjection.ExecutionState = recommendExecutionAnalysisOnly
	rows := []models.AiRecommendStocks{
		marketSummaryV150MonitorRecommendation(pendingRule, "600001.SH", decision),
		openProjection,
		marketSummaryV150MonitorRecommendation(closedRule, "600003.SH", decision),
		{DataTime: &decision, StockCode: "600006.SH", SummaryVersion: v150.StrategyVersion, StrategyRunID: "missing-run", StrategyRuleID: "missing-rule"},
		analysisProjection,
	}
	analysisOnly := marketSummaryV150MonitorRecommendation(pendingRule, "600001.SH", decision)
	analysisOnly.ExecutionState = recommendExecutionAnalysisOnly
	rows = append(rows, analysisOnly)
	if err := db.Dao.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	active, pending, open, skipped, warnings, err := loadMarketSummaryV150ActiveExecutionRecordsForTest(validFrom)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 4 || pending != 3 || open != 1 {
		t.Fatalf("active=%d pending=%d open=%d, want 4/3/1", len(active), pending, open)
	}
	warningText := strings.Join(warnings, ";")
	if skipped != 0 || !strings.Contains(warningText, "duplicate display projections") ||
		!strings.Contains(warningText, "analysis_only display projection ignored") ||
		!strings.Contains(warningText, "missing display projection") {
		t.Fatalf("skipped=%d warnings=%v", skipped, warnings)
	}
	seen := map[string]models.AiRecommendStocks{}
	for _, record := range active {
		if _, duplicate := seen[record.StrategyRuleID]; duplicate {
			t.Fatalf("immutable rule returned more than once: %s", record.StrategyRuleID)
		}
		seen[record.StrategyRuleID] = record
	}
	if _, ok := seen[pendingRule]; !ok {
		t.Fatalf("pending rule missing: %+v", seen)
	}
	if _, ok := seen[openRule]; !ok {
		t.Fatalf("open rule missing: %+v", seen)
	}
	if _, ok := seen[missingProjectionRule]; !ok {
		t.Fatalf("rule without projection missing: %+v", seen)
	}
	if _, ok := seen[analysisProjectionRule]; !ok {
		t.Fatalf("rule with only analysis projection missing: %+v", seen)
	}
	if _, ok := seen[closedRule]; ok {
		t.Fatalf("wrong lifecycle selection: %+v", seen)
	}
	if got := seen[openRule]; got.StockCode != "000002.SZ" || got.StrategyRunID == "forged-run" ||
		got.RecommendCategory != recommendExecutionConditional || got.ExecutionState != recommendExecutionConditional || got.RecommendStatus != "valid" {
		t.Fatalf("mutable projection changed frozen execution identity: %+v", got)
	}
	if got := seen[missingProjectionRule]; got.ID != 0 || got.StrategyRuleID != missingProjectionRule {
		t.Fatalf("missing display projection changed execution enumeration: %+v", got)
	}
	if got := seen[analysisProjectionRule]; got.ID != 0 || got.ExecutionState != recommendExecutionConditional {
		t.Fatalf("analysis-only display projection changed execution enumeration: %+v", got)
	}
}

func TestMarketSummaryV150ExecutionFrozenRuleWithoutProjectionStillFills(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	initMarketSummaryV150ExecutionTestDB(t)
	recommendation := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, marketSummaryV150TestBreakoutPlan(validFrom), true)
	seedMarketSummaryV150BreakoutBars(t, recommendation, decision, validFrom)
	if db.Dao.Migrator().HasTable(&models.AiRecommendStocks{}) {
		t.Fatal("fixture unexpectedly created the optional recommendation projection table")
	}

	active, pending, open, skipped, _, err := loadMarketSummaryV150ActiveExecutionRecordsForTest(validFrom.Add(45 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || pending != 1 || open != 0 || skipped != 0 {
		t.Fatalf("active=%d pending=%d open=%d skipped=%d, want 1/1/0/0", len(active), pending, open, skipped)
	}
	if active[0].ID != 0 || active[0].StrategyRuleID != recommendation.StrategyRuleID {
		t.Fatalf("missing projection did not produce an executable compatibility record: %+v", active[0])
	}

	ctx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{
		Now: validFrom.Add(45 * time.Minute), InTradingSession: true,
		LatestTradeDate: decision, DisableMinuteFetch: true,
	})
	activationAt, _, info := resolveMarketSummaryV150Activation(active[0], ctx, false)
	if activationAt == nil || !activationAt.Equal(validFrom.Add(30*time.Minute)) || info.V150Entry == nil {
		t.Fatalf("frozen rule without display projection did not execute: at=%v info=%+v", activationAt, info)
	}
	var events []models.OrderEvent
	if err := db.Dao.Where("rule_id = ?", recommendation.StrategyRuleID).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if got := marketSummaryV150TestEventTypes(events); got != "rule_issued,signal,order,fill" {
		t.Fatalf("events=%s", got)
	}
}

func TestMarketSummaryV150ExecutionMonitorRejectsCorruptFrozenEntryPlan(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	observedAt := validFrom.Add(45 * time.Minute)
	initMarketSummaryV150ExecutionTestDB(t)
	recommendation := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, marketSummaryV150TestBreakoutPlan(validFrom), false)

	// Fault injection models storage-level corruption by briefly bypassing the
	// immutable update trigger in this disposable test database. The production
	// mutation guard is restored before the monitor is invoked.
	if err := db.Dao.Exec("DROP TRIGGER immutable_strategy_rule_snapshot_update").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Exec("UPDATE strategy_rule_snapshot SET payload_json = ? WHERE rule_id = ?", `{}`, recommendation.StrategyRuleID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Exec("CREATE TRIGGER immutable_strategy_rule_snapshot_update BEFORE UPDATE ON strategy_rule_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_rule_snapshot'); END").Error; err != nil {
		t.Fatal(err)
	}
	active, pending, open, skipped, warnings, err := loadMarketSummaryV150ActiveExecutionRecordsForTest(observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 || pending != 0 || open != 0 || skipped != 1 {
		t.Fatalf("active=%d pending=%d open=%d skipped=%d, want 0/0/0/1", len(active), pending, open, skipped)
	}
	if warningText := strings.Join(warnings, ";"); !strings.Contains(warningText, "rejected invalid frozen entry plan") ||
		!strings.Contains(warningText, "frozen V1.5 plan symbol is invalid") {
		t.Fatalf("warnings=%v", warnings)
	}

	var events []models.OrderEvent
	if err := db.Dao.Where("rule_id = ?", recommendation.StrategyRuleID).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if got := marketSummaryV150TestEventTypes(events); got != "rule_issued,reject" {
		t.Fatalf("events=%s", got)
	}
	if !strings.Contains(events[1].Reason, marketSummaryV150DataHealthReject) || !events[1].EventAt.Equal(observedAt) {
		t.Fatalf("reject=%+v", events[1])
	}

	// The reject is terminal and idempotent: later scans neither re-enumerate
	// the rule nor append another lifecycle event.
	active, pending, open, skipped, _, err = loadMarketSummaryV150ActiveExecutionRecordsForTest(observedAt.Add(15 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 || pending != 0 || open != 0 || skipped != 0 {
		t.Fatalf("terminal rescan active=%d pending=%d open=%d skipped=%d", len(active), pending, open, skipped)
	}
	var eventCount int64
	if err := db.Dao.Model(&models.OrderEvent{}).Where("rule_id = ?", recommendation.StrategyRuleID).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 2 {
		t.Fatalf("event count=%d, want 2", eventCount)
	}
}

func TestMarketSummaryV150ExecutionMonitorPreservesOpenLedgerWhenPlanIsCorrupt(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	observedAt := validFrom.Add(45 * time.Minute)
	initMarketSummaryV150ExecutionTestDB(t)
	ruleID := seedMarketSummaryV150MonitorFrozenRule(t, "600001.SH", "bank", decision, validFrom, true, nil)

	if err := db.Dao.Exec("DROP TRIGGER immutable_strategy_rule_snapshot_update").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Exec("UPDATE strategy_rule_snapshot SET payload_json = ? WHERE rule_id = ?", `{}`, ruleID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Exec("CREATE TRIGGER immutable_strategy_rule_snapshot_update BEFORE UPDATE ON strategy_rule_snapshot BEGIN SELECT RAISE(ABORT, 'immutable table strategy_rule_snapshot'); END").Error; err != nil {
		t.Fatal(err)
	}

	active, pending, open, skipped, warnings, err := loadMarketSummaryV150ActiveExecutionRecordsForTest(observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 || pending != 0 || open != 1 || skipped != 1 {
		t.Fatalf("active=%d pending=%d open=%d skipped=%d, want 0/0/1/1", len(active), pending, open, skipped)
	}
	if warningText := strings.Join(warnings, ";"); !strings.Contains(warningText, "open rule has invalid frozen entry plan") {
		t.Fatalf("warnings=%v", warnings)
	}
	var events []models.OrderEvent
	if err := db.Dao.Where("rule_id = ?", ruleID).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if got := marketSummaryV150TestEventTypes(events); got != "rule_issued,signal,order,fill" {
		t.Fatalf("open ledger was rewritten: events=%s", got)
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

func seedMarketSummaryV150MonitorFrozenRule(t *testing.T, symbol, sector string, decision, validFrom time.Time, withFill bool, exitAt *time.Time) string {
	t.Helper()
	plan := marketSummaryV150TestBreakoutPlan(validFrom)
	plan.Symbol = symbol
	recommendation := appendMarketSummaryV150ExecutionFixtureWithSector(t, decision, plan, sector, false)
	if !withFill {
		return recommendation.StrategyRuleID
	}
	var run models.StrategyRunSnapshot
	if err := db.Dao.Where("run_id = ?", recommendation.StrategyRunID).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	cfg := v150.FixedStrategyV150Config()
	scenario := cfg.SlippageScenarios()[0]
	entryRaw := 10.0
	unitCost := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket(symbol), entryRaw, cfg.RoundLotSize, scenario, cfg)
	size := v150.SizeRoundLot(unitCost.EffectivePrice, cfg.TargetCashPerPosition, cfg)
	entryCost := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket(symbol), entryRaw, size.Quantity, scenario, cfg)
	fillAt := validFrom.Add(15 * time.Minute)
	events := []v150.OrderEvent{
		{Type: v150.EventSignal, At: validFrom, Symbol: symbol, Reason: string(v150.PathBreakout)},
		{Type: v150.EventOrder, At: fillAt, Symbol: symbol, Reason: "next_bar_market_order"},
		{Type: v150.EventFill, At: fillAt, Symbol: symbol, Price: entryCost.EffectivePrice, Quantity: size.Quantity},
	}
	accounting := marketSummaryV150EventAccounting{Entry: &entryCost}
	if exitAt != nil {
		exitCost := v150.CalculateTradeCost(v150.SideSell, v150.ResolveMarket(symbol), 10.5, size.Quantity, scenario, cfg)
		accounting.Exit = &exitCost
		events = append(events,
			v150.OrderEvent{Type: v150.EventExitSignal, At: *exitAt, Symbol: symbol, Price: exitCost.EffectivePrice, Quantity: size.Quantity, Reason: string(v150.ExitTarget)},
			v150.OrderEvent{Type: v150.EventExitFill, At: *exitAt, Symbol: symbol, Price: exitCost.EffectivePrice, Quantity: size.Quantity, Reason: string(v150.ExitTarget)},
		)
	}
	if err := appendMarketSummaryV150OrderEventsWithStore(
		context.Background(), persistence.NewGORMOrderEventStore(db.Dao), recommendation, run, events, accounting,
	); err != nil {
		t.Fatal(err)
	}
	return recommendation.StrategyRuleID
}
