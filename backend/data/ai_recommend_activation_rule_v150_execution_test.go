package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

type recordingMarketSummaryV150OrderEventStore struct {
	calls    int
	delegate marketSummaryV150OrderEventStore
	contexts []context.Context
	batches  [][]models.OrderEvent
}

func (s *recordingMarketSummaryV150OrderEventStore) AppendOrderEvents(ctx context.Context, runID string, events []models.OrderEvent) error {
	s.calls++
	s.contexts = append(s.contexts, ctx)
	s.batches = append(s.batches, append([]models.OrderEvent(nil), events...))
	if s.delegate != nil {
		return s.delegate.AppendOrderEvents(ctx, runID, events)
	}
	return nil
}

func TestBuildMarketSummaryV150CompletedBarsSupportsStartAndEndLabels(t *testing.T) {
	loc := cnLocation()
	start := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	for _, endLabeled := range []bool{false, true} {
		raw := marketSummaryV150TestMinuteBucket(start, 10, 10.1, 100, endLabeled)
		bars, gaps := buildMarketSummaryV150CompletedBars(raw, start.Add(15*time.Minute), start)
		if len(gaps) != 0 || len(bars) != 1 {
			t.Fatalf("endLabeled=%v bars=%d gaps=%v", endLabeled, len(bars), gaps)
		}
		if !bars[0].Start.Equal(start) || !bars[0].Completed || bars[0].IntervalMinutes != 15 {
			t.Fatalf("unexpected completed bar: %+v", bars[0])
		}
	}

	incomplete := marketSummaryV150TestMinuteBucket(start, 10, 10.1, 100, false)[:14]
	if _, gaps := buildMarketSummaryV150CompletedBars(incomplete, start.Add(15*time.Minute), start); len(gaps) == 0 {
		t.Fatalf("incomplete bucket gaps=%v", gaps)
	}
	missingWholeBucket := append(marketSummaryV150TestMinuteBucket(start, 10, 10.1, 100, false), marketSummaryV150TestMinuteBucket(start.Add(30*time.Minute), 10.1, 10.2, 100, false)...)
	if _, gaps := buildMarketSummaryV150CompletedBars(missingWholeBucket, start.Add(45*time.Minute), start); len(gaps) == 0 || !strings.Contains(strings.Join(gaps, ";"), "09:45 bucket is entirely missing") {
		t.Fatalf("entire missing bucket was not rejected: %v", gaps)
	}
}

func TestMarketSummaryV150ExecutionTrueCrossingNextBarFillAndAppend(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	plan := marketSummaryV150TestBreakoutPlan(validFrom)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, plan)
	seedMarketSummaryV150DailyClose(t, rec.StockCode, decision.AddDate(0, 0, -1), 10)

	prior := validFrom.AddDate(0, 0, -1)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(15*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom, 9.9, 9.95, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(15*time.Minute), 9.95, 10.10, 150, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(30*time.Minute), 10.05, 10.08, 100, false))

	ctx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{
		Now:                validFrom.Add(45 * time.Minute),
		InTradingSession:   true,
		LatestTradeDate:    decision,
		DisableMinuteFetch: true,
	})
	activationAt, rawPrice, info := resolveMarketSummaryV150Activation(rec, ctx, false)
	if activationAt == nil || !activationAt.Equal(validFrom.Add(30*time.Minute)) {
		t.Fatalf("activationAt=%v status=%s reason=%s", activationAt, info.DataStatus, info.DataStatusReason)
	}
	if rawPrice != 10.05 || info.V150Entry == nil || info.V150Entry.Position.Quantity != 900 {
		t.Fatalf("entry=%+v rawPrice=%.4f", info.V150Entry, rawPrice)
	}
	if info.V150Entry.Position.EntryPrice <= rawPrice {
		t.Fatalf("10bp adverse slippage not applied: raw=%.4f effective=%.4f", rawPrice, info.V150Entry.Position.EntryPrice)
	}

	var events []models.OrderEvent
	if err := db.Dao.Where("run_id = ?", rec.StrategyRunID).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if got := marketSummaryV150TestEventTypes(events); got != "rule_issued,signal,order,fill" {
		t.Fatalf("events=%s", got)
	}
	fill := events[len(events)-1]
	if fill.Quantity != 900 || fill.Price <= rawPrice || fill.Fees <= 0 {
		t.Fatalf("persisted fill=%+v", fill)
	}
	// Replaying the same frozen input is idempotent and must not duplicate the
	// append-only lifecycle.
	if _, _, retry := resolveMarketSummaryV150Activation(rec, ctx, false); retry.V150Entry == nil {
		t.Fatalf("idempotent retry failed: %+v", retry)
	}
	var count int64
	if err := db.Dao.Model(&models.OrderEvent{}).Where("run_id = ?", rec.StrategyRunID).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
	state := buildYieldRecordStateFromRecommend(rec, nil, ctx)
	if state.ActivationStatus != "activated" || state.BuyTime == nil || !state.BuyTime.Equal(validFrom.Add(30*time.Minute)) || state.BuyAmount != 10.05 {
		t.Fatalf("production yield-state path did not use V1.5 fill: %+v", state)
	}
	if state.StopLossAmount == nil || state.StopProfitAmount == nil || *state.StopLossAmount >= state.BuyAmount || *state.StopProfitAmount <= state.BuyAmount {
		t.Fatalf("yield state did not receive repriced backend plan: %+v", state)
	}
}

func TestMarketSummaryV150EntryRejectsSixthOpenUsingPointInTimeLedger(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(validFrom))
	seedMarketSummaryV150BreakoutBars(t, rec, decision, validFrom)

	historicalFillAt := validFrom.Add(15 * time.Minute)
	futureExitAt := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	for index, symbol := range []string{"600001.SH", "600002.SH", "600003.SH", "600004.SH", "600005.SH"} {
		seedMarketSummaryV150PortfolioRule(t, index+1, symbol, fmt.Sprintf("sector-%d", index+1), decision, validFrom, true, &futureExitAt)
	}

	fillAt := validFrom.Add(30 * time.Minute)
	before, err := loadMarketSummaryV150ExecutionPortfolioState(db.Dao, rec.StrategyRuleID, fillAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.OpenSymbols) != 5 {
		t.Fatalf("historical open symbols=%v, want five despite later exits", before.OpenSymbols)
	}
	for _, symbol := range []string{"600001.SH", "600002.SH", "600003.SH", "600004.SH", "600005.SH"} {
		if !before.OpenSymbols[symbol] {
			t.Fatalf("historical fill at %s was changed by a later event: %v", historicalFillAt, before.OpenSymbols)
		}
	}

	activationAt, _, info := resolveMarketSummaryV150Activation(rec, withTestMarketSummaryV150OrderEventSink(yieldBuildContext{
		Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true,
	}), false)
	if activationAt != nil || info.V150Entry != nil || !strings.Contains(info.DataStatusReason, v150.RejectPortfolioCapacity) {
		t.Fatalf("sixth position was not rejected: at=%v info=%+v", activationAt, info)
	}

	// Point-in-time replay may consume the exit only once the immutable event
	// itself has been frozen; EventAt alone must not make a later append visible.
	after, err := loadMarketSummaryV150ExecutionPortfolioState(db.Dao, rec.StrategyRuleID, futureExitAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(after.OpenSymbols) != 0 {
		t.Fatalf("exits were not applied once causally frozen: %v", after.OpenSymbols)
	}
}

func TestMarketSummaryV150ConcurrentSameBarEntriesCannotPierceSectorLimit(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	initMarketSummaryV150ExecutionTestDB(t)

	leftPlan := marketSummaryV150TestBreakoutPlan(validFrom)
	rightPlan := leftPlan
	rightPlan.Symbol = "000001.SZ"
	left := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, leftPlan, true)
	right := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, rightPlan, true)
	// The mutable legacy projection is presentation-only; changing its sector
	// must not bypass the sector stored in the frozen candidate snapshot.
	right.BkName = "forged-sector"
	seedMarketSummaryV150BreakoutBars(t, left, decision, validFrom)
	seedMarketSummaryV150BreakoutBars(t, right, decision, validFrom)

	previousHook := marketSummaryV150BeforeEntryCriticalSection
	var arrived sync.WaitGroup
	arrived.Add(2)
	release := make(chan struct{})
	marketSummaryV150BeforeEntryCriticalSection = func() {
		arrived.Done()
		<-release
	}
	t.Cleanup(func() { marketSummaryV150BeforeEntryCriticalSection = previousHook })

	type result struct {
		entry *marketSummaryV150EntryExecution
		info  triggerEvalInfo
	}
	results := make(chan result, 2)
	ctx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true})
	for _, record := range []models.AiRecommendStocks{right, left} {
		record := record
		go func() {
			_, _, info := resolveMarketSummaryV150Activation(record, ctx, false)
			results <- result{entry: info.V150Entry, info: info}
		}()
	}
	arrived.Wait()
	close(release)

	filled := 0
	rejected := 0
	for count := 0; count < 2; count++ {
		got := <-results
		if got.entry != nil {
			filled++
			continue
		}
		if strings.Contains(got.info.DataStatusReason, v150.RejectSectorDailyLimit) {
			rejected++
		}
	}
	if filled != 1 || rejected != 1 {
		t.Fatalf("concurrent same-bar sector admission filled=%d rejected=%d", filled, rejected)
	}
	var fillCount int64
	if err := db.Dao.Model(&models.OrderEvent{}).Where("event_type = ?", string(v150.EventFill)).Count(&fillCount).Error; err != nil {
		t.Fatal(err)
	}
	if fillCount != 1 {
		t.Fatalf("immutable ledger contains %d fills, want exactly one", fillCount)
	}
}

func TestMarketSummaryV150ConcurrentSameBarEntriesCannotPierceDailyLimit(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	initMarketSummaryV150ExecutionTestDB(t)

	plans := []v150.TradePlan{
		marketSummaryV150TestBreakoutPlan(validFrom),
		marketSummaryV150TestBreakoutPlan(validFrom),
		marketSummaryV150TestBreakoutPlan(validFrom),
	}
	plans[1].Symbol = "000001.SZ"
	plans[2].Symbol = "000002.SZ"
	sectors := []string{"bank", "technology", "consumer"}
	records := make([]models.AiRecommendStocks, 0, len(plans))
	for index, plan := range plans {
		record := appendMarketSummaryV150ExecutionFixtureWithSector(t, decision, plan, sectors[index], true)
		seedMarketSummaryV150BreakoutBars(t, record, decision, validFrom)
		records = append(records, record)
	}

	previousHook := marketSummaryV150BeforeEntryCriticalSection
	var arrived sync.WaitGroup
	arrived.Add(len(records))
	release := make(chan struct{})
	marketSummaryV150BeforeEntryCriticalSection = func() {
		arrived.Done()
		<-release
	}
	t.Cleanup(func() { marketSummaryV150BeforeEntryCriticalSection = previousHook })

	type result struct {
		entry *marketSummaryV150EntryExecution
		info  triggerEvalInfo
	}
	results := make(chan result, len(records))
	ctx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true})
	for _, record := range records {
		record := record
		go func() {
			_, _, info := resolveMarketSummaryV150Activation(record, ctx, false)
			results <- result{entry: info.V150Entry, info: info}
		}()
	}
	arrived.Wait()
	close(release)

	filled := 0
	rejected := 0
	for count := 0; count < len(records); count++ {
		got := <-results
		if got.entry != nil {
			filled++
			continue
		}
		if strings.Contains(got.info.DataStatusReason, v150.RejectDailyEntryLimit) {
			rejected++
		}
	}
	wantFilled := v150.FixedStrategyV150Config().RiskOnDailyCap
	if filled != wantFilled || rejected != 1 {
		t.Fatalf("concurrent same-bar daily admission filled=%d rejected=%d", filled, rejected)
	}
	var fillCount int64
	if err := db.Dao.Model(&models.OrderEvent{}).Where("event_type = ?", string(v150.EventFill)).Count(&fillCount).Error; err != nil {
		t.Fatal(err)
	}
	if fillCount != int64(wantFilled) {
		t.Fatalf("immutable ledger contains %d fills, want daily cap %d", fillCount, wantFilled)
	}
}

func TestMarketSummaryV150ConcurrentEntriesCannotPierceOpenPositionLimit(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	initMarketSummaryV150ExecutionTestDB(t)

	// Four positions filled on the prior trade day leave exactly one holdings
	// slot. Unrelated pending rules deliberately outnumber the remaining slot;
	// they must not reserve it.
	priorDecision := decision.AddDate(0, 0, -1)
	priorValidFrom := validFrom.AddDate(0, 0, -1)
	for index, symbol := range []string{"600011.SH", "600012.SH", "600013.SH", "600014.SH"} {
		seedMarketSummaryV150PortfolioRule(t, index+1, symbol, fmt.Sprintf("held-sector-%d", index+1), priorDecision, priorValidFrom, true, nil)
	}
	for index, symbol := range []string{"600021.SH", "600022.SH", "600023.SH"} {
		seedMarketSummaryV150PortfolioRule(t, index+11, symbol, fmt.Sprintf("pending-sector-%d", index+1), decision, validFrom, false, nil)
	}

	plans := []v150.TradePlan{
		marketSummaryV150TestBreakoutPlan(validFrom),
		marketSummaryV150TestBreakoutPlan(validFrom),
	}
	plans[1].Symbol = "000001.SZ"
	sectors := []string{"bank", "technology"}
	records := make([]models.AiRecommendStocks, 0, len(plans))
	for index, plan := range plans {
		record := appendMarketSummaryV150ExecutionFixtureWithSector(t, decision, plan, sectors[index], true)
		seedMarketSummaryV150BreakoutBars(t, record, decision, validFrom)
		records = append(records, record)
	}

	previousHook := marketSummaryV150BeforeEntryCriticalSection
	var arrived sync.WaitGroup
	arrived.Add(len(records))
	release := make(chan struct{})
	marketSummaryV150BeforeEntryCriticalSection = func() {
		arrived.Done()
		<-release
	}
	t.Cleanup(func() { marketSummaryV150BeforeEntryCriticalSection = previousHook })

	type result struct {
		entry *marketSummaryV150EntryExecution
		info  triggerEvalInfo
	}
	results := make(chan result, len(records))
	ctx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true})
	for _, record := range records {
		record := record
		go func() {
			_, _, info := resolveMarketSummaryV150Activation(record, ctx, false)
			results <- result{entry: info.V150Entry, info: info}
		}()
	}
	arrived.Wait()
	close(release)

	filled := 0
	capacityRejected := 0
	for count := 0; count < len(records); count++ {
		got := <-results
		if got.entry != nil {
			filled++
			continue
		}
		if strings.Contains(got.info.DataStatusReason, v150.RejectPortfolioCapacity) {
			capacityRejected++
		}
	}
	if filled != 1 || capacityRejected != 1 {
		t.Fatalf("concurrent final-slot admission filled=%d capacityRejected=%d", filled, capacityRejected)
	}

	fillAt := validFrom.Add(30 * time.Minute)
	portfolio, err := loadMarketSummaryV150ExecutionAdmissionPortfolioState(db.Dao, "", fillAt)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(portfolio.OpenSymbols); got != v150.FixedStrategyV150Config().MaximumOpenPositions {
		t.Fatalf("atomic replay reconstructed %d open holdings, want %d: %v", got, v150.FixedStrategyV150Config().MaximumOpenPositions, portfolio.OpenSymbols)
	}
	var fillCount int64
	if err := db.Dao.Model(&models.OrderEvent{}).Where("event_type = ?", string(v150.EventFill)).Count(&fillCount).Error; err != nil {
		t.Fatal(err)
	}
	if fillCount != int64(v150.FixedStrategyV150Config().MaximumOpenPositions) {
		t.Fatalf("immutable ledger contains %d fills, want exactly five concurrent holdings", fillCount)
	}
}

func TestMarketSummaryV150ExecutionDailyCapUsesFrozenRegime(t *testing.T) {
	cfg := v150.FixedStrategyV150Config()
	for _, test := range []struct {
		name    string
		regime  v150.RegimeDecision
		path    v150.TradePath
		payload bool
		want    int
	}{
		{name: "risk_on", regime: v150.RegimeDecision{Regime: v150.RegimeRiskOn, DailyCap: cfg.RiskOnDailyCap}, path: v150.PathPullback, payload: true, want: cfg.RiskOnDailyCap},
		{name: "neutral", regime: v150.RegimeDecision{Regime: v150.RegimeNeutral, DailyCap: cfg.NeutralDailyCap}, path: v150.PathPullback, payload: true, want: cfg.NeutralDailyCap},
		{name: "risk_off", regime: v150.RegimeDecision{Regime: v150.RegimeRiskOff, DailyCap: cfg.RiskOffDailyCap}, path: v150.PathPullback, payload: true, want: 0},
		{name: "unknown_pullback_is_conservative", path: v150.PathPullback, want: cfg.NeutralDailyCap},
		{name: "legacy_breakout_metadata", path: v150.PathBreakout, want: cfg.RiskOnDailyCap},
	} {
		t.Run(test.name, func(t *testing.T) {
			frozen := marketSummaryV150FrozenExecutionPlan{Plan: v150.TradePlan{Path: test.path}, Rule: models.RuleSnapshot{Path: string(test.path)}}
			if test.payload {
				frozen.Run.PayloadJSON = marketSummaryV150ExecutionTestRunPayload(t, test.regime)
			}
			if got := marketSummaryV150ExecutionDailyCap(frozen, cfg); got != test.want {
				t.Fatalf("daily cap=%d, want %d", got, test.want)
			}
		})
	}
}

func TestMarketSummaryV150EntryRejectsOtherRuleWithSameSymbol(t *testing.T) {
	for _, test := range []struct {
		name       string
		withFill   bool
		wantReason string
	}{
		{name: "open", withFill: true, wantReason: v150.RejectDuplicateOpen},
		{name: "pending", withFill: false, wantReason: v150.RejectDuplicatePending},
	} {
		t.Run(test.name, func(t *testing.T) {
			loc := cnLocation()
			decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
			validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
			rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(validFrom))
			seedMarketSummaryV150BreakoutBars(t, rec, decision, validFrom)
			otherRuleID := seedMarketSummaryV150PortfolioRule(t, 1, rec.StockCode, "other-sector", decision, validFrom, test.withFill, nil)
			if otherRuleID == rec.StrategyRuleID {
				t.Fatal("test fixture did not create a distinct rule")
			}

			activationAt, _, info := resolveMarketSummaryV150Activation(rec, withTestMarketSummaryV150OrderEventSink(yieldBuildContext{
				Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true,
			}), false)
			if activationAt != nil || info.V150Entry != nil || !strings.Contains(info.DataStatusReason, test.wantReason) {
				t.Fatalf("same-symbol %s rule was not rejected: at=%v info=%+v", test.name, activationAt, info)
			}
		})
	}
}

func TestMarketSummaryV150ExitHonorsT1AndGapStop(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 7, 1, 9, 30, 0, 0, loc)
	plan := marketSummaryV150TestBreakoutPlan(validFrom)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, plan)
	seedMarketSummaryV150DailyClose(t, rec.StockCode, decision.AddDate(0, 0, -1), 10)

	prior := validFrom.AddDate(0, 0, -1)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(15*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom, 9.9, 9.95, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(15*time.Minute), 9.95, 10.10, 150, false))
	// The entry bar itself crosses below the stop, but T+1 forbids a same-day
	// sell and the executor must leave it open.
	entryBucket := marketSummaryV150TestMinuteBucket(validFrom.Add(30*time.Minute), 10.05, 9.40, 100, false)
	for index := range entryBucket {
		entryBucket[index].Low = 9.30
	}
	seedMarketSummaryV150Minutes(t, rec.StockCode, entryBucket)
	entryCtx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true})
	_, _, entryInfo := resolveMarketSummaryV150Activation(rec, entryCtx, false)
	if entryInfo.V150Entry == nil {
		t.Fatalf("entry failed: %s", entryInfo.DataStatusReason)
	}
	status, _, _, sameDayInfo := evaluateMarketSummaryV150Exit(rec, *entryInfo.V150Entry, entryCtx, false)
	if status != "" || sameDayInfo.DataStatus != "正常" {
		t.Fatalf("same-day exit=%q info=%+v", status, sameDayInfo)
	}

	nextDay := shiftToNextCNOpenTradeDaySafe(decision.AddDate(0, 0, 1))
	nextOpen := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 30, 0, 0, loc)
	seedMarketSummaryV150FreshSecurity(t, rec, nextOpen)
	seedMarketSummaryV150DailyClose(t, rec.StockCode, nextDay.AddDate(0, 0, -1), 10)
	seedMarketSummaryV150FlatBuckets(t, rec.StockCode, validFrom.Add(45*time.Minute), nextOpen, 10.20)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(nextOpen, 9.20, 9.30, 100, false))
	// Event-order replay performs provider I/O up front, then evaluates each
	// checkpoint cache-only while still requiring the dedicated daily execution
	// observation seeded above.
	exitCtx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{
		Now: nextOpen.Add(15 * time.Minute), InTradingSession: true, LatestTradeDate: nextDay,
		DisableMinuteFetch: true, RequireV150ExecutionObservation: true,
	})
	status, exitAt, rawExit, exitInfo := evaluateMarketSummaryV150Exit(rec, *entryInfo.V150Entry, exitCtx, false)
	if status != "已止损" || !exitAt.Equal(nextOpen) || rawExit != 9.20 {
		t.Fatalf("gap exit status=%q at=%v price=%.4f info=%+v", status, exitAt, rawExit, exitInfo)
	}
	var events []models.OrderEvent
	if err := db.Dao.Where("run_id = ?", rec.StrategyRunID).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if got := marketSummaryV150TestEventTypes(events); got != "rule_issued,signal,order,fill,exit_signal,exit_fill" {
		t.Fatalf("events=%s", got)
	}
	if !events[len(events)-1].EventAt.In(loc).After(events[3].EventAt.In(loc)) {
		t.Fatal("exit fill does not satisfy T+1 causality")
	}
}

func TestMarketSummaryV150ExitUsesDayTen1445TimeExit(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 7, 1, 9, 30, 0, 0, loc)
	plan := marketSummaryV150TestBreakoutPlan(validFrom)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, plan)
	seedMarketSummaryV150DailyClose(t, rec.StockCode, decision.AddDate(0, 0, -1), 10)
	prior := validFrom.AddDate(0, 0, -1)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(15*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom, 9.9, 9.95, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(15*time.Minute), 9.95, 10.10, 150, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(30*time.Minute), 10.05, 10.08, 100, false))
	entryCtx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true})
	_, _, entryInfo := resolveMarketSummaryV150Activation(rec, entryCtx, false)
	if entryInfo.V150Entry == nil {
		t.Fatalf("entry failed: %s", entryInfo.DataStatusReason)
	}

	exitDay := normalizeDailyTradeDate(decision)
	for count := 1; count < 10; count++ {
		exitDay = shiftToNextCNOpenTradeDaySafe(exitDay.AddDate(0, 0, 1))
	}
	exitStart := time.Date(exitDay.Year(), exitDay.Month(), exitDay.Day(), 14, 45, 0, 0, loc)
	seedMarketSummaryV150FreshSecurityRange(t, rec, shiftToNextCNOpenTradeDaySafe(decision.AddDate(0, 0, 1)), exitDay)
	seedMarketSummaryV150DailyCloseRange(t, rec.StockCode, decision, previousMarketSummaryV150OpenTradeDay(exitDay), 10.10)
	seedMarketSummaryV150FlatBuckets(t, rec.StockCode, validFrom.Add(45*time.Minute), exitStart, 10.20)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(exitStart, 10.20, 10.20, 100, false))
	exitCtx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: exitStart.Add(15 * time.Minute), InTradingSession: true, LatestTradeDate: exitDay, DisableMinuteFetch: true})
	status, exitAt, price, info := evaluateMarketSummaryV150Exit(rec, *entryInfo.V150Entry, exitCtx, false)
	if status != marketSummaryV150TimeExitStatus || !exitAt.Equal(exitStart) || price != 10.20 {
		t.Fatalf("time exit status=%q at=%v price=%.4f info=%+v", status, exitAt, price, info)
	}
}

func TestMarketSummaryV150PendingTimeExitUsesNextDayFirstTradableBar(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 7, 1, 9, 30, 0, 0, loc)
	plan := marketSummaryV150TestBreakoutPlan(validFrom)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, plan)
	seedMarketSummaryV150DailyClose(t, rec.StockCode, decision.AddDate(0, 0, -1), 10)
	prior := validFrom.AddDate(0, 0, -1)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(15*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom, 9.9, 9.95, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(15*time.Minute), 9.95, 10.10, 150, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(30*time.Minute), 10.05, 10.08, 100, false))
	entryCtx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: validFrom.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: decision, DisableMinuteFetch: true})
	_, _, entryInfo := resolveMarketSummaryV150Activation(rec, entryCtx, false)
	if entryInfo.V150Entry == nil {
		t.Fatalf("entry failed: %s", entryInfo.DataStatusReason)
	}

	day10 := normalizeDailyTradeDate(decision)
	for count := 1; count < 10; count++ {
		day10 = shiftToNextCNOpenTradeDaySafe(day10.AddDate(0, 0, 1))
	}
	day10Exit := time.Date(day10.Year(), day10.Month(), day10.Day(), 14, 45, 0, 0, loc)
	nextDay := shiftToNextCNOpenTradeDaySafe(day10.AddDate(0, 0, 1))
	nextOpen := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 30, 0, 0, loc)
	seedMarketSummaryV150FreshSecurityRange(t, rec, shiftToNextCNOpenTradeDaySafe(decision.AddDate(0, 0, 1)), nextDay)
	seedMarketSummaryV150DailyCloseRange(t, rec.StockCode, decision, previousMarketSummaryV150OpenTradeDay(nextDay), 10.10)
	seedMarketSummaryV150FlatBuckets(t, rec.StockCode, validFrom.Add(45*time.Minute), day10Exit, 10.20)
	// Main-board 10% limit-down from the 10.10 previous close is 9.09.
	// The day-10 time exit becomes pending because this full bar is locked.
	locked := marketSummaryV150TestMinuteBucket(day10Exit, 9.09, 9.09, 100, false)
	for index := range locked {
		locked[index].Open, locked[index].High, locked[index].Low, locked[index].Close = 9.09, 9.09, 9.09, 9.09
	}
	seedMarketSummaryV150Minutes(t, rec.StockCode, locked)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(nextOpen, 10.10, 10.10, 100, false))
	exitCtx := withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: nextOpen.Add(15 * time.Minute), InTradingSession: true, LatestTradeDate: nextDay, DisableMinuteFetch: true})
	status, exitAt, price, info := evaluateMarketSummaryV150Exit(rec, *entryInfo.V150Entry, exitCtx, false)
	if status != marketSummaryV150TimeExitStatus || !exitAt.Equal(nextOpen) || price != 10.10 {
		t.Fatalf("pending time exit status=%q at=%v price=%.4f info=%+v", status, exitAt, price, info)
	}
}

func TestMarketSummaryV150PriorDaySecurityStatusIsNotCarriedForward(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
	nextDay := shiftToNextCNOpenTradeDaySafe(decision.AddDate(0, 0, 1))
	_, err := loadMarketSummaryV150SecurityState(rec.StrategyRunID, rec.StockCode, time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 30, 0, 0, loc))
	if err == nil || !strings.Contains(err.Error(), "execution-day security state") {
		t.Fatalf("prior-day security state did not fail closed: %v", err)
	}
}

func TestMarketSummaryV150SecurityStateUsesLaterRunSameDaySnapshot(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
	eventAt := time.Date(2026, 7, 2, 9, 30, 0, 0, loc)
	observedAt := eventAt.Add(-20 * time.Minute)
	statusRunID := seedMarketSummaryV150StatusRun(t, rec.StockCode, observedAt, observedAt.Add(2*time.Minute))

	state, err := loadMarketSummaryV150SecurityState(rec.StrategyRunID, rec.StockCode, eventAt)
	if err != nil {
		t.Fatalf("later same-day status was rejected: %v", err)
	}
	if !state.Tradable || state.Row.RunID != statusRunID {
		t.Fatalf("security state=%+v, want later run %s", state, statusRunID)
	}
}

func TestMarketSummaryV150SecurityStateRejectsFutureFrozenRunWithoutSameDayFallback(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
	eventAt := time.Date(2026, 7, 2, 9, 30, 0, 0, loc)
	observedAt := eventAt.Add(-20 * time.Minute)
	seedMarketSummaryV150StatusRun(t, rec.StockCode, observedAt, eventAt.Add(time.Minute))

	_, err := loadMarketSummaryV150SecurityState(rec.StrategyRunID, rec.StockCode, eventAt)
	if err == nil {
		t.Fatal("future-frozen status observation incorrectly authorized an event and prior-day state was reused")
	}
}

func TestMarketSummaryV150CrossDayExecutionSecurityStatus(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		isST         bool
		isSuspended  bool
		wantTradable bool
		wantST       bool
	}{
		{name: "suspended", status: "SUSPENDED", isSuspended: true},
		{name: "st", status: "L", isST: true, wantTradable: true, wantST: true},
		{name: "delisted", status: "D", isSuspended: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loc := cnLocation()
			decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
			rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
			nextDay := shiftToNextCNOpenTradeDaySafe(decision.AddDate(0, 0, 1))
			observedAt := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 15, 0, 0, loc)
			seedMarketSummaryV150ExecutionSecurityObservationRun(
				t, rec.StrategyRunID, rec.StockCode, test.status, test.isST, test.isSuspended, observedAt, observedAt, &observedAt,
			)

			state, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, observedAt.Add(15*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if state.Tradable != test.wantTradable || state.Row.IsST != test.wantST {
				t.Fatalf("state=%+v, want tradable=%v isST=%v", state, test.wantTradable, test.wantST)
			}
		})
	}
}

func TestMarketSummaryV150ExecutionSecuritySameDayCausality(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
	day := shiftToNextCNOpenTradeDaySafe(decision.AddDate(0, 0, 1))
	activeAt := time.Date(day.Year(), day.Month(), day.Day(), 9, 20, 0, 0, loc)
	suspendedAt := time.Date(day.Year(), day.Month(), day.Day(), 10, 10, 0, 0, loc)
	activeRunID := seedMarketSummaryV150ExecutionSecurityObservationRun(t, rec.StrategyRunID, rec.StockCode, "L", false, false, activeAt, activeAt, &activeAt)
	suspendedRunID := seedMarketSummaryV150ExecutionSecurityObservationRun(t, rec.StrategyRunID, rec.StockCode, "SUSPENDED", false, true, suspendedAt, suspendedAt, &suspendedAt)

	before, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, loc))
	if err != nil || !before.Tradable || before.Row.RunID != activeRunID {
		t.Fatalf("10:00 state=%+v err=%v, want earlier active observation %s", before, err, activeRunID)
	}
	after, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, time.Date(day.Year(), day.Month(), day.Day(), 10, 15, 0, 0, loc))
	if err != nil || after.Tradable || after.Row.RunID != suspendedRunID {
		t.Fatalf("10:15 state=%+v err=%v, want suspended observation %s", after, err, suspendedRunID)
	}
	if _, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, activeAt.Add(-time.Minute)); err == nil {
		t.Fatal("a future same-day observation authorized an earlier event")
	}
}

func TestMarketSummaryV150DelayedOnlineObservationCannotBackfillPastFill(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
	scanAt := time.Date(2026, 7, 2, 10, 30, 0, 0, loc)
	signalAt := time.Date(2026, 7, 2, 10, 0, 0, 0, loc)
	fillAt := time.Date(2026, 7, 2, 10, 15, 0, 0, loc)

	previousNow := marketSummaryV150ExecutionSecurityNow
	previousFetch := fetchMarketSummaryV150ExecutionSecurityFactFn
	marketSummaryV150ExecutionSecurityNow = func() time.Time { return scanAt }
	fetchMarketSummaryV150ExecutionSecurityFactFn = func(symbol string, observedAt time.Time) (marketSummaryV150ExecutionSecurityFact, error) {
		return marketSummaryV150ExecutionSecurityFact{
			Symbol: symbol, Name: "test", Market: "SH", Board: "MAIN", Currency: "CNY", Status: "L", ListStatus: "L",
			Source: "test_realtime_quote", SourceAt: observedAt.Add(-time.Minute),
		}, nil
	}
	t.Cleanup(func() {
		marketSummaryV150ExecutionSecurityNow = previousNow
		fetchMarketSummaryV150ExecutionSecurityFactFn = previousFetch
	})
	if _, err := refreshMarketSummaryV150ExecutionSecurityObservation(rec.StrategyRunID, rec.StockCode, true); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, signalAt); err == nil {
		t.Fatal("10:30 observation retroactively authorized the 10:00 signal")
	}
	if _, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, fillAt); err == nil {
		t.Fatal("10:30 observation retroactively authorized the 10:15 fill")
	}
	if state, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, scanAt.Add(time.Minute)); err != nil || !state.Tradable {
		t.Fatalf("observation was not available prospectively: state=%+v err=%v", state, err)
	}
}

func TestMarketSummaryV150ExecutionSecurityMissingOrUnknownFailsClosed(t *testing.T) {
	t.Run("missing availableAt", func(t *testing.T) {
		loc := cnLocation()
		decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
		rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
		observedAt := time.Date(2026, 7, 2, 9, 15, 0, 0, loc)
		seedMarketSummaryV150ExecutionSecurityObservationRun(t, rec.StrategyRunID, rec.StockCode, "L", false, false, observedAt, observedAt, nil)
		if _, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, observedAt.Add(time.Minute)); err == nil {
			t.Fatal("observation without availableAt did not fail closed")
		}
	})

	t.Run("unknown status", func(t *testing.T) {
		loc := cnLocation()
		decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
		rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
		observedAt := time.Date(2026, 7, 2, 9, 15, 0, 0, loc)
		seedMarketSummaryV150ExecutionSecurityObservationRun(t, rec.StrategyRunID, rec.StockCode, "UNKNOWN", false, false, observedAt, observedAt, &observedAt)
		if _, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, observedAt.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "unknown frozen security status") {
			t.Fatalf("unknown status did not fail closed: %v", err)
		}
	})
}

func TestMarketSummaryV150ExecutionSecurityRefreshIsAppendOnlyAndCacheOnlyIsReadOnly(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(decision.Add(30*time.Minute)))
	observedAt := time.Date(2026, 7, 2, 9, 20, 0, 0, loc)
	previousNow := marketSummaryV150ExecutionSecurityNow
	previousFetch := fetchMarketSummaryV150ExecutionSecurityFactFn
	marketSummaryV150ExecutionSecurityNow = func() time.Time { return observedAt }
	fetchCalls := 0
	fetchMarketSummaryV150ExecutionSecurityFactFn = func(symbol string, at time.Time) (marketSummaryV150ExecutionSecurityFact, error) {
		fetchCalls++
		return marketSummaryV150ExecutionSecurityFact{
			Symbol: symbol, Name: "test", Market: "SH", Board: "MAIN", Currency: "CNY", Status: "L", ListStatus: "L",
			Source: "test_realtime_quote", SourceAt: at.Add(-time.Minute),
		}, nil
	}
	t.Cleanup(func() {
		marketSummaryV150ExecutionSecurityNow = previousNow
		fetchMarketSummaryV150ExecutionSecurityFactFn = previousFetch
	})

	var beforeRuns int64
	if err := db.Dao.Model(&models.StrategyRunSnapshot{}).Count(&beforeRuns).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := refreshMarketSummaryV150ExecutionSecurityObservation(rec.StrategyRunID, rec.StockCode, false); err != nil {
		t.Fatal(err)
	}
	var cacheOnlyRuns int64
	if err := db.Dao.Model(&models.StrategyRunSnapshot{}).Count(&cacheOnlyRuns).Error; err != nil {
		t.Fatal(err)
	}
	if fetchCalls != 0 || cacheOnlyRuns != beforeRuns {
		t.Fatalf("cache-only refresh fetched=%d runs=%d, before=%d", fetchCalls, cacheOnlyRuns, beforeRuns)
	}

	observationRunID, err := refreshMarketSummaryV150ExecutionSecurityObservation(rec.StrategyRunID, rec.StockCode, true)
	if err != nil {
		t.Fatal(err)
	}
	if fetchCalls != 1 || observationRunID == "" {
		t.Fatalf("online refresh calls=%d runID=%q", fetchCalls, observationRunID)
	}
	state, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, observedAt.Add(time.Second))
	if err != nil || !state.Tradable || state.Row.RunID != observationRunID {
		t.Fatalf("appended observation state=%+v err=%v", state, err)
	}
	var original models.SecurityMasterHistory
	if err := db.Dao.Where("run_id = ?", rec.StrategyRunID).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	if original.EffectiveTo != nil {
		t.Fatalf("original immutable security row was closed or updated: %+v", original)
	}
}

func TestFetchMarketSummaryV150ExecutionSecurityFactFailsClosed(t *testing.T) {
	tests := []struct {
		name             string
		listStatus       string
		stockName        string
		quote            *StockInfo
		wantStatus       string
		wantST           bool
		wantSuspended    bool
		wantErr          bool
		wantRealtimeCall bool
	}{
		{
			name: "listed active", listStatus: "L", stockName: "测试股份", wantStatus: "L", wantRealtimeCall: true,
			quote: &StockInfo{Code: "sh600000", Name: "测试股份", Date: "2026-07-02", Time: "09:45:00", Price: "10.10", Open: "10.00", PreClose: "9.90", Volume: "100", Amount: "1000"},
		},
		{
			name: "listed st", listStatus: "L", stockName: "*ST测试", wantStatus: "L", wantST: true, wantRealtimeCall: true,
			quote: &StockInfo{Code: "sh600000", Name: "*ST测试", Date: "2026-07-02", Time: "09:45:00", Price: "10.10", Open: "10.00", PreClose: "9.90", Volume: "100", Amount: "1000"},
		},
		{
			name: "intraday suspended", listStatus: "L", stockName: "测试股份", wantStatus: "SUSPENDED", wantSuspended: true, wantRealtimeCall: true,
			quote: &StockInfo{Code: "sh600000", Name: "测试股份", Date: "2026-07-02", Time: "09:45:00", Price: "0", Open: "0", PreClose: "9.90", Volume: "0", Amount: "0"},
		},
		{name: "delisted", listStatus: "D", stockName: "退市测试", wantStatus: "D", wantSuspended: true},
		{
			name: "stale quote", listStatus: "L", stockName: "测试股份", wantErr: true, wantRealtimeCall: true,
			quote: &StockInfo{Code: "sh600000", Name: "测试股份", Date: "2026-07-01", Time: "15:00:00", Price: "10.10", Open: "10.00", PreClose: "9.90", Volume: "100", Amount: "1000"},
		},
		{
			name: "missing quote fields", listStatus: "L", stockName: "测试股份", wantErr: true, wantRealtimeCall: true,
			quote: &StockInfo{Code: "sh600000", Name: "测试股份", Date: "2026-07-02", Time: "09:45:00", PreClose: "9.90"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initMarketSummaryV150ExecutionTestDB(t)
			if err := db.Dao.AutoMigrate(&StockBasic{}); err != nil {
				t.Fatal(err)
			}
			observedAt := time.Date(2026, 7, 2, 10, 0, 0, 0, cnLocation())
			basicAt := observedAt.AddDate(0, 0, -1)
			basic := StockBasic{
				TsCode: "600000.SH", Symbol: "600000", Name: test.stockName, Industry: "银行", Market: "主板", Exchange: "SSE", CurrType: "CNY",
				ListStatus: test.listStatus, ListDate: "19991110",
			}
			basic.CreatedAt, basic.UpdatedAt = basicAt, basicAt
			if err := db.Dao.Create(&basic).Error; err != nil {
				t.Fatal(err)
			}

			previousRealtime := runMarketSummaryV150ExecutionRealtimeWithTimeoutFn
			realtimeCalls := 0
			runMarketSummaryV150ExecutionRealtimeWithTimeoutFn = func(code string, timeout time.Duration) (*[]StockInfo, error) {
				realtimeCalls++
				if test.quote == nil {
					empty := []StockInfo{}
					return &empty, nil
				}
				rows := []StockInfo{*test.quote}
				return &rows, nil
			}
			t.Cleanup(func() { runMarketSummaryV150ExecutionRealtimeWithTimeoutFn = previousRealtime })

			fact, err := fetchMarketSummaryV150ExecutionSecurityFact("600000.SH", observedAt)
			if test.wantErr {
				if err == nil {
					t.Fatalf("fact=%+v, want fail-closed error", fact)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if fact.Status != test.wantStatus || fact.IsST != test.wantST || fact.IsSuspended != test.wantSuspended {
					t.Fatalf("fact=%+v, want status=%s isST=%v suspended=%v", fact, test.wantStatus, test.wantST, test.wantSuspended)
				}
			}
			if (realtimeCalls > 0) != test.wantRealtimeCall {
				t.Fatalf("realtime calls=%d, want called=%v", realtimeCalls, test.wantRealtimeCall)
			}
		})
	}
}

func TestMarketSummaryV150MissingSecurityStatusRejectsFailClosed(t *testing.T) {
	loc := cnLocation()
	start := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	plan := marketSummaryV150TestBreakoutPlan(start)
	rec := seedMarketSummaryV150ExecutionFixtureWithoutSecurity(t, start.Add(-30*time.Minute), plan)
	seedMarketSummaryV150DailyClose(t, rec.StockCode, start.AddDate(0, 0, -1), 10)
	prior := start.AddDate(0, 0, -1)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(15*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(start, 9.9, 9.95, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(start.Add(15*time.Minute), 9.95, 10.1, 150, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(start.Add(30*time.Minute), 10.05, 10.08, 100, false))
	_, _, info := resolveMarketSummaryV150Activation(rec, withTestMarketSummaryV150OrderEventSink(yieldBuildContext{Now: start.Add(45 * time.Minute), InTradingSession: true, LatestTradeDate: start, DisableMinuteFetch: true}), false)
	if info.DataStatus != "已跳过" || info.V150Entry != nil || !strings.Contains(info.DataStatusReason, "security status unavailable") {
		t.Fatalf("fail-closed result=%+v", info)
	}
}

func TestAppendMarketSummaryV150OrderEventsRequiresInjectedStoreAndIsIdempotent(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := decision.Add(30 * time.Minute)
	cfg := v150.FixedStrategyV150Config()
	cost := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket("600000.SH"), 10, 900, cfg.SlippageScenarios()[0], cfg)
	source := []v150.OrderEvent{
		{Type: v150.EventFill, At: validFrom.Add(30 * time.Minute), Symbol: "600000.SH", Price: cost.EffectivePrice, Quantity: cost.Quantity},
		{Type: v150.EventSignal, At: validFrom.Add(15 * time.Minute), Symbol: "600000.SH", Reason: string(v150.PathBreakout)},
		{Type: v150.EventOrder, At: validFrom.Add(30 * time.Minute), Symbol: "600000.SH", Reason: "next_bar_market_order"},
	}
	accounting := marketSummaryV150EventAccounting{Entry: &cost}
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(validFrom))
	var run models.StrategyRunSnapshot
	if err := db.Dao.Where("run_id = ?", rec.StrategyRunID).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := appendMarketSummaryV150OrderEvents(rec, run, source, accounting); !errors.Is(err, errMarketSummaryV150OrderEventStoreUnavailable) {
		t.Fatalf("compatibility producer error = %v", err)
	}
	var count int64
	if err := db.Dao.Model(&models.OrderEvent{}).
		Where("run_id = ? AND rule_id = ?", rec.StrategyRunID, rec.StrategyRuleID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("missing store changed immutable ledger row count to %d", count)
	}

	store := persistence.NewGORMOrderEventStore(db.Dao)
	if err := appendMarketSummaryV150OrderEventsWithStore(context.Background(), store, rec, run, source, accounting); err != nil {
		t.Fatal(err)
	}
	var rows []models.OrderEvent
	if err := db.Dao.Where("run_id = ? AND rule_id = ?", rec.StrategyRunID, rec.StrategyRuleID).
		Order("sequence ASC, event_id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || marketSummaryV150TestEventTypes(rows) != "rule_issued,signal,order,fill" {
		t.Fatalf("unexpected lifecycle rows: %s", marketSummaryV150TestEventTypes(rows))
	}
	if err := persistence.VerifyStrategyOrderEvents(rows); err != nil {
		t.Fatalf("persisted lifecycle seal: %v", err)
	}
	if err := appendMarketSummaryV150OrderEventsWithStore(context.Background(), store, rec, run, source, accounting); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if err := db.Dao.Model(&models.OrderEvent{}).
		Where("run_id = ? AND rule_id = ?", rec.StrategyRunID, rec.StrategyRuleID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(len(rows)) {
		t.Fatalf("idempotent retry changed row count to %d", count)
	}
}

func TestAppendMarketSummaryV150OrderEventsInjectedStoreHonorsPausedGate(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	if _, err := governance.SetStrategyRuntimeMode(
		context.Background(), db.Dao, governance.StrategyModePaused, v150.StrategyVersion, "sink test", "test",
	); err != nil {
		t.Fatal(err)
	}

	store := &recordingMarketSummaryV150OrderEventStore{}
	err := appendMarketSummaryV150OrderEventsWithStore(
		context.Background(), store,
		models.AiRecommendStocks{StockCode: "600000.SH", StrategyRunID: "paused-run", StrategyRuleID: "paused-rule"},
		models.StrategyRunSnapshot{RunID: "paused-run"},
		[]v150.OrderEvent{{Type: v150.EventSignal, At: time.Now().Add(-time.Minute), Symbol: "600000.SH"}},
		marketSummaryV150EventAccounting{},
	)
	if !errors.Is(err, governance.ErrStrategyPaused) {
		t.Fatalf("error = %v, want ErrStrategyPaused", err)
	}
	if store.calls != 0 {
		t.Fatalf("paused gate called injected store %d times", store.calls)
	}
}

func TestAppendMarketSummaryV150OrderEventsRejectsFutureEventWithoutFutureFreeze(t *testing.T) {
	loc := cnLocation()
	decision := time.Now().In(loc).Add(-2 * time.Hour).Truncate(time.Second)
	validFrom := decision.Add(30 * time.Minute)
	rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(validFrom))
	var run models.StrategyRunSnapshot
	if err := db.Dao.Where("run_id = ?", rec.StrategyRunID).First(&run).Error; err != nil {
		t.Fatal(err)
	}

	futureAt := time.Now().Add(time.Minute)
	err := appendMarketSummaryV150OrderEventsWithStore(context.Background(), persistence.NewGORMOrderEventStore(db.Dao), rec, run, []v150.OrderEvent{{
		Type: v150.EventSignal, At: futureAt, Symbol: rec.StockCode, Reason: string(v150.PathBreakout),
	}}, marketSummaryV150EventAccounting{})
	if err == nil || !strings.Contains(err.Error(), "is in the future") {
		t.Fatalf("future lifecycle event was not rejected: %v", err)
	}
	var events []models.OrderEvent
	if err := db.Dao.Where("run_id = ? AND rule_id = ?", rec.StrategyRunID, rec.StrategyRuleID).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if got := marketSummaryV150TestEventTypes(events); got != "rule_issued" {
		t.Fatalf("future event mutated immutable ledger: %s", got)
	}
}

func seedMarketSummaryV150ExecutionFixture(t *testing.T, decision time.Time, plan v150.TradePlan) models.AiRecommendStocks {
	return seedMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, plan, true)
}

func seedMarketSummaryV150BreakoutBars(t *testing.T, rec models.AiRecommendStocks, decision, validFrom time.Time) {
	t.Helper()
	seedMarketSummaryV150DailyClose(t, rec.StockCode, decision.AddDate(0, 0, -1), 10)
	prior := validFrom.AddDate(0, 0, -1)
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(15*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom, 9.9, 9.95, 100, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(15*time.Minute), 9.95, 10.10, 150, false))
	seedMarketSummaryV150Minutes(t, rec.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(30*time.Minute), 10.05, 10.08, 100, false))
}

func seedMarketSummaryV150PortfolioRule(t *testing.T, ordinal int, symbol, sector string, decision, validFrom time.Time, withFill bool, exitAt *time.Time) string {
	t.Helper()
	symbol = normalizeRecommendStockCode(symbol)
	runID := fmt.Sprintf("v150-portfolio-%02d-%s", ordinal, strings.ReplaceAll(symbol, ".", "-"))
	ruleID := runID + "|rule|" + symbol + "|pullback"
	candidateID := runID + "|candidate|" + symbol
	frozenAt := decision.Add(time.Second)
	expiresAt := marketSummaryV150PlanExpiresAt(validFrom, v150.FixedStrategyV150Config().ActivationValidTradeDays)
	tradeDate := decision.Format(time.DateOnly)
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate,
			StartedAt: decision.Add(-10 * time.Minute), AsOf: decision.Add(-5 * time.Minute), DataCutoffAt: decision.Add(-time.Minute),
			DecisionAt: decision, GeneratedAt: decision, ValidFromAt: &validFrom, Mode: "portfolio_test",
			ConfigHash: v150.FixedStrategyV150ConfigHash(), InputHash: "portfolio-test-" + runID, PayloadJSON: `{}`, FrozenAt: &frozenAt,
		},
		Candidates: []models.CandidateSnapshot{{
			CandidateID: candidateID, RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate,
			Symbol: symbol, Sector: sector, Decision: "production", Eligible: true, PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		Rules: []models.RuleSnapshot{{
			RuleID: ruleID, RunID: runID, CandidateID: candidateID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate,
			Symbol: symbol, RuleVersion: v150.StrategyVersion, RuleType: "entry", Path: string(v150.PathPullback),
			ValidFromAt: validFrom, ExpiresAt: &expiresAt, PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		OrderEvents: []models.OrderEvent{{
			EventID: runID + "|issued", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
			TradeDate: tradeDate, Symbol: symbol, EventType: "rule_issued", Sequence: 1, EventAt: decision,
			Reason: "portfolio_test", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		t.Fatal(err)
	}
	if !withFill {
		return ruleID
	}

	cfg := v150.FixedStrategyV150Config()
	scenario := cfg.SlippageScenarios()[0]
	entryRaw := 10.0
	unitCost := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket(symbol), entryRaw, cfg.RoundLotSize, scenario, cfg)
	size := v150.SizeRoundLot(unitCost.EffectivePrice, cfg.TargetCashPerPosition, cfg)
	entryCost := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket(symbol), entryRaw, size.Quantity, scenario, cfg)
	fillAt := validFrom.Add(15 * time.Minute)
	lifecycleFrozenAt := fillAt.Add(time.Minute)
	events := []models.OrderEvent{
		{EventID: runID + "|signal", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventSignal), Sequence: 2, EventAt: validFrom, Reason: string(v150.PathPullback), PayloadJSON: `{}`, FrozenAt: &lifecycleFrozenAt},
		{EventID: runID + "|order", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventOrder), Sequence: 3, EventAt: fillAt, Reason: "next_bar_market_order", PayloadJSON: `{}`, FrozenAt: &lifecycleFrozenAt},
		{EventID: runID + "|fill", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventFill), Sequence: 4, EventAt: fillAt, Price: entryCost.EffectivePrice, Quantity: float64(size.Quantity), Fees: entryCost.Commission + entryCost.TransferFee + entryCost.StampDuty, PayloadJSON: `{}`, FrozenAt: &lifecycleFrozenAt},
	}
	if exitAt != nil {
		exitCost := v150.CalculateTradeCost(v150.SideSell, v150.ResolveMarket(symbol), entryRaw, size.Quantity, scenario, cfg)
		exitFrozenAt := exitAt.Add(time.Minute)
		events = append(events,
			models.OrderEvent{EventID: runID + "|exit-signal", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventExitSignal), Sequence: 5, EventAt: *exitAt, Price: entryRaw, Quantity: float64(size.Quantity), Reason: string(v150.ExitTarget), PayloadJSON: `{}`, FrozenAt: &exitFrozenAt},
			models.OrderEvent{EventID: runID + "|exit-fill", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventExitFill), Sequence: 6, EventAt: *exitAt, Price: exitCost.EffectivePrice, Quantity: float64(size.Quantity), Fees: exitCost.Commission + exitCost.TransferFee + exitCost.StampDuty, Reason: string(v150.ExitTarget), PayloadJSON: `{}`, FrozenAt: &exitFrozenAt},
		)
	}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategyOrderEvents(context.Background(), db.Dao, runID, events); err != nil {
		t.Fatal(err)
	}
	return ruleID
}

func seedMarketSummaryV150ExecutionFixtureWithoutSecurity(t *testing.T, decision time.Time, plan v150.TradePlan) models.AiRecommendStocks {
	return seedMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, plan, false)
}

func seedMarketSummaryV150ExecutionFixtureWithSecurity(t *testing.T, decision time.Time, plan v150.TradePlan, includeSecurity bool) models.AiRecommendStocks {
	t.Helper()
	initMarketSummaryV150ExecutionTestDB(t)
	return appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, plan, includeSecurity)
}

func initMarketSummaryV150ExecutionTestDB(t *testing.T) {
	t.Helper()
	db.Init(filepath.Join(t.TempDir(), "v150-execution.db"))
	initMinuteSchemaForTest(t)
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() {
		_ = db.Close()
		db.Dao = nil
		db.MinuteDao = nil
	})
	if err := db.Dao.AutoMigrate(&models.AiRecommendMinuteBar{}, &models.AiRecommendDailyBar{}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.MigrateStrategyPersistence(db.Dao); err != nil {
		t.Fatal(err)
	}
}

func appendMarketSummaryV150ExecutionFixtureWithSecurity(t *testing.T, decision time.Time, plan v150.TradePlan, includeSecurity bool) models.AiRecommendStocks {
	return appendMarketSummaryV150ExecutionFixtureWithSector(t, decision, plan, "银行", includeSecurity)
}

func appendMarketSummaryV150ExecutionFixtureWithSector(t *testing.T, decision time.Time, plan v150.TradePlan, sector string, includeSecurity bool) models.AiRecommendStocks {
	t.Helper()
	symbol := normalizeRecommendStockCode(plan.Symbol)
	if symbol == "" {
		t.Fatal("test plan symbol is required")
	}
	sector = strings.TrimSpace(sector)
	if sector == "" {
		sector = "银行"
	}
	runID := "v150-execution-run-" + decision.Format("20060102150405") + "-" + strings.ReplaceAll(symbol, ".", "-")
	ruleID := runID + "|rule|" + symbol + "|" + string(plan.Path)
	frozenAt := decision.Add(time.Second)
	rulePayload, _ := json.Marshal(struct {
		Production struct {
			Plan v150.TradePlan `json:"plan"`
		} `json:"production"`
	}{Production: struct {
		Plan v150.TradePlan `json:"plan"`
	}{Plan: plan}})
	validFrom := plan.ValidFromAt
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: decision.Format(time.DateOnly),
			StartedAt: decision.Add(-10 * time.Minute), AsOf: decision.Add(-5 * time.Minute), DataCutoffAt: decision.Add(-time.Minute),
			DecisionAt: decision, GeneratedAt: decision, ValidFromAt: &validFrom, Mode: "test", ConfigHash: v150.FixedStrategyV150ConfigHash(),
			InputHash: "test-input", PayloadJSON: marketSummaryV150ExecutionTestRunPayload(t, v150.RegimeDecision{Regime: v150.RegimeRiskOn, DailyCap: v150.FixedStrategyV150Config().RiskOnDailyCap}), FrozenAt: &frozenAt,
		},
		Rules: []models.RuleSnapshot{{
			RuleID: ruleID, RunID: runID, CandidateID: runID + "|candidate|" + symbol, StrategyVersion: v150.StrategyVersion,
			TradeDate: decision.Format(time.DateOnly), Symbol: symbol, RuleVersion: v150.StrategyVersion, RuleType: "entry", Path: string(plan.Path),
			ValidFromAt: validFrom, PayloadJSON: string(rulePayload), FrozenAt: &frozenAt,
		}},
		Candidates: []models.CandidateSnapshot{{
			CandidateID: runID + "|candidate|" + symbol, RunID: runID, StrategyVersion: v150.StrategyVersion,
			TradeDate: decision.Format(time.DateOnly), Symbol: symbol, Sector: sector, Rank: 1,
			Decision: "production", Eligible: true, PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		OrderEvents: []models.OrderEvent{{
			EventID: runID + "|issued", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
			TradeDate: decision.Format(time.DateOnly), Symbol: symbol, EventType: "rule_issued", Sequence: 1,
			EventAt: decision, Reason: "test", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
	}
	if includeSecurity {
		bundle.SecurityMaster = []models.SecurityMasterHistory{{
			RecordID: runID + "|security", RunID: runID, SnapshotVersion: v150.StrategyVersion, Symbol: symbol, Market: string(v150.ResolveMarket(symbol)), Board: "MAIN",
			Status: "L", ListedAt: ptrTime(decision.AddDate(-1, 0, 0)), EffectiveFrom: decision.Add(-time.Minute), Source: "test",
			PayloadJSON: marketSummaryV150TestSecurityPayload(t, decision.Add(-time.Minute)), FrozenAt: &frozenAt,
		}}
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		t.Fatal(err)
	}
	seedMarketSummaryV150EmptyCorporateActionDay(t, runID, symbol, plan.ValidFromAt)
	return models.AiRecommendStocks{
		DataTime: &decision, StockCode: symbol, StockName: symbol, BkName: sector,
		SummaryVersion: v150.StrategyVersion, StrategyRunID: runID, StrategyRuleID: ruleID,
		ActivationRuleJSON: `{}`, RecommendCategory: recommendExecutionConditional,
		RecommendStopProfitPrice: "12", RecommendStopLossPrice: "9",
	}
}

func marketSummaryV150ExecutionTestRunPayload(t *testing.T, regime v150.RegimeDecision) string {
	t.Helper()
	payload := struct {
		Run struct {
			Regime v150.RegimeDecision `json:"regime"`
		} `json:"run"`
	}{}
	payload.Run.Regime = regime
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func marketSummaryV150TestBreakoutPlan(validFrom time.Time) v150.TradePlan {
	return v150.TradePlan{
		Symbol: "600000.SH", Path: v150.PathBreakout, DecisionTradeDayIndex: marketSummaryV150TradeDayIndex(validFrom), ValidFromTradeDayIndex: marketSummaryV150TradeDayIndex(validFrom),
		ValidFromAt: validFrom, EvaluationMinutes: 15, Trigger: 10, ReferenceEntry: 10, TargetResistance: 12,
		Stop: 9.5, Target: 11, RiskPerShare: 0.5, RewardRisk: 2, ATR14: 0.30, NegativeOvernightGapRisk60: 0.03,
		MinimumVolumeRatio: 1.2, NoActivationAfterMin: 14 * 60, ValidTradeDays: 3, MaxHoldTradeDays: 10,
		TrailingActivationR: 1, TrailingATRMultiple: 1.5,
	}
}

func marketSummaryV150TestMinuteBucket(start time.Time, open, close, amount float64, endLabeled bool) []minuteBar {
	rows := make([]minuteBar, 0, 15)
	for index := 0; index < 15; index++ {
		at := start.Add(time.Duration(index) * time.Minute)
		if endLabeled {
			at = at.Add(time.Minute)
		}
		price := open + (close-open)*float64(index+1)/15
		rows = append(rows, minuteBar{TradeTime: at, Open: open, High: maxFloat(open, price) + 0.01, Low: minFloat(open, price) - 0.01, Close: price, Volume: amount / 10, Amount: amount})
	}
	return rows
}

func seedMarketSummaryV150Minutes(t *testing.T, symbol string, rows []minuteBar) {
	t.Helper()
	modelsRows := make([]models.AiRecommendMinuteBar, 0, len(rows))
	for _, row := range rows {
		modelsRows = append(modelsRows, models.AiRecommendMinuteBar{StockCode: symbol, TradeTime: row.TradeTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume, Amount: row.Amount, Source: "test"})
	}
	if len(modelsRows) > 0 {
		if err := db.Dao.CreateInBatches(&modelsRows, 500).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func seedMarketSummaryV150FlatBuckets(t *testing.T, symbol string, from, until time.Time, price float64) {
	t.Helper()
	rows := make([]minuteBar, 0, 256)
	for current, guard := from, 0; current.Before(until) && guard < 16*15; guard++ {
		rows = append(rows, marketSummaryV150TestMinuteBucket(current, price, price, 100, false)...)
		next := nextMarketSummaryV150TradingBucketStart(current)
		if next.IsZero() || !next.After(current) {
			break
		}
		current = next
	}
	seedMarketSummaryV150Minutes(t, symbol, rows)
}

func seedMarketSummaryV150DailyClose(t *testing.T, symbol string, day time.Time, close float64) {
	t.Helper()
	day = normalizeDailyTradeDate(day)
	if err := db.Dao.Create(&models.AiRecommendDailyBar{StockCode: symbol, TradeDate: day, Open: close, High: close, Low: close, Close: close, Source: "test"}).Error; err != nil {
		t.Fatal(err)
	}
}

func seedMarketSummaryV150DailyCloseRange(t *testing.T, symbol string, from, to time.Time, close float64) {
	t.Helper()
	for day := normalizeDailyTradeDate(from); !day.After(normalizeDailyTradeDate(to)); day = day.AddDate(0, 0, 1) {
		if isCNOpenTradeDaySafe(day) {
			seedMarketSummaryV150DailyClose(t, symbol, day, close)
		}
	}
}

func seedMarketSummaryV150FreshSecurity(t *testing.T, rec models.AiRecommendStocks, at time.Time) {
	t.Helper()
	seedMarketSummaryV150ExecutionSecurityObservationRun(t, rec.StrategyRunID, rec.StockCode, "L", false, false, at, at, &at)
	seedMarketSummaryV150EmptyCorporateActionDay(t, rec.StrategyRunID, rec.StockCode, at)
}

func seedMarketSummaryV150EmptyCorporateActionDay(t *testing.T, originRunID, symbol string, at time.Time) {
	t.Helper()
	day := normalizeDailyTradeDate(at.In(cnLocation()))
	availableAt := time.Date(day.Year(), day.Month(), day.Day(), 9, 29, 0, 0, cnLocation())
	seedMarketSummaryV150CorporateActionObservation(
		t, originRunID, symbol, day, availableAt,
		marketSummaryV150CorporateActionStatusEmpty, nil, "", "",
	)
}

func seedMarketSummaryV150FreshSecurityRange(t *testing.T, rec models.AiRecommendStocks, from, to time.Time) {
	t.Helper()
	for day := normalizeDailyTradeDate(from); !day.After(normalizeDailyTradeDate(to)); day = day.AddDate(0, 0, 1) {
		if isCNOpenTradeDaySafe(day) {
			seedMarketSummaryV150FreshSecurity(t, rec, time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, cnLocation()))
		}
	}
}

func seedMarketSummaryV150StatusRun(t *testing.T, symbol string, observedAt, frozenAt time.Time) string {
	t.Helper()
	return seedMarketSummaryV150ExecutionSecurityObservationRun(t, "", symbol, "L", false, false, observedAt, frozenAt, &observedAt)
}

func seedMarketSummaryV150ExecutionSecurityObservationRun(
	t *testing.T,
	originRunID, symbol, status string,
	isST, isSuspended bool,
	observedAt, frozenAt time.Time,
	availableAt *time.Time,
) string {
	t.Helper()
	symbol = normalizeRecommendStockCode(symbol)
	statusKey := strings.ToLower(strings.TrimSpace(status))
	runID := "v150-status-run-" + statusKey + "-" + observedAt.Format("20060102T150405.000000000") + "-" + frozenAt.Format("150405.000000000")
	tradeDate := observedAt.In(cnLocation()).Format(time.DateOnly)
	securityPayload := `{}`
	if availableAt != nil {
		securityPayload = marketSummaryV150TestSecurityPayload(t, *availableAt)
	}
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, RunSlot: marketSummaryV150ExecutionSecurityObservationMode,
			StartedAt: observedAt.Add(-2 * time.Minute), AsOf: observedAt.Add(-time.Minute), DataCutoffAt: observedAt,
			DecisionAt: observedAt, GeneratedAt: observedAt, Mode: marketSummaryV150ExecutionSecurityObservationMode, ConfigHash: v150.FixedStrategyV150ConfigHash(),
			InputHash: "status-input-" + runID, PayloadJSON: fmt.Sprintf(`{"originRunId":%q}`, originRunID), FrozenAt: &frozenAt,
		},
		SecurityMaster: []models.SecurityMasterHistory{{
			RecordID: runID + "|security|" + symbol, RunID: runID, SnapshotVersion: v150.StrategyVersion,
			Symbol: symbol, Market: "SH", Board: "MAIN", Status: status, IsST: isST, IsSuspended: isSuspended, EffectiveFrom: observedAt,
			Source: "test_execution_security_observation", PayloadJSON: securityPayload, FrozenAt: &frozenAt,
		}},
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		t.Fatal(err)
	}
	return runID
}

func marketSummaryV150TestSecurityPayload(t *testing.T, availableAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		Security struct {
			AvailableAt string `json:"availableAt"`
		} `json:"security"`
	}{Security: struct {
		AvailableAt string `json:"availableAt"`
	}{AvailableAt: availableAt.Format(time.RFC3339Nano)}})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func marketSummaryV150TestEventTypes(events []models.OrderEvent) string {
	result := ""
	for _, event := range events {
		if result != "" {
			result += ","
		}
		result += event.EventType
	}
	return result
}

func ptrTime(value time.Time) *time.Time { return &value }

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
