package data

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

func marketSummaryV150TestCandidate(symbol, sector string, signal float64, asOf time.Time) v150.Candidate {
	eventAt := asOf.Add(-time.Hour)
	return v150.Candidate{
		Symbol: symbol, Name: symbol, Sector: sector, Market: v150.MarketSZ,
		ListedAt: asOf.AddDate(-3, 0, 0), HasDailyData: true, HasCurrentData: true, HasRelativeStrengthData: true,
		Price: 10, PreviousClose: 9.9, MA10: 9.9, MA20: 9.8, MA60: 9.2,
		ATR14: 0.2, AverageAmount20: 2e8, DayChangeRatio: 0.01, GapRatio: 0.01,
		Resistance20: 10.4, TargetResistance: 12, NegativeOvernightGapRisk60: -0.025,
		EventAt: &eventAt,
		Signals: v150.ScoreSignals{
			TrendRelativeStrength: signal, SetupQuality: signal, SectorStrength: signal,
			EventStrength: signal, LiquidityRiskQuality: signal,
		},
	}
}

func TestRenderMarketSummaryV150ReportUsesBreakoutTriggerAsEntryRange(t *testing.T) {
	run := &MarketSummaryV150RunSnapshot{
		RunContext: v150.RunContext{RunID: "run-breakout-report"},
		Regime:     v150.RegimeDecision{Regime: v150.RegimeRiskOn},
		Candidates: []MarketSummaryV150CandidateSnapshot{{
			Source:    MarketSummaryV150SourceCandidate{StockName: "test"},
			Candidate: v150.Candidate{Symbol: "000001.SZ", Name: "test"},
		}},
		Production: []MarketSummaryV150ProductionSnapshot{{
			Symbol: "000001.SZ",
			Rank:   1,
			Score:  v150.ScoreBreakdown{Total: 80},
			Plan: v150.TradePlan{
				Path:    v150.PathBreakout,
				Trigger: 12.34,
				Stop:    11.80,
				Target:  13.42,
			},
		}},
	}

	report := renderMarketSummaryV150Report(run)
	if got := strings.Count(report, "12.34"); got != 2 {
		t.Fatalf("breakout report must render the trigger as both entry bounds; count=%d report=%q", got, report)
	}
	if strings.Contains(report, "0.00") {
		t.Fatalf("breakout report rendered the unused pullback bounds: %q", report)
	}
}

func marketSummaryV150TestSource(candidate v150.Candidate, asOf time.Time) MarketSummaryV150SourceCandidate {
	return MarketSummaryV150SourceCandidate{
		StockName: candidate.Name, StockCode: candidate.Symbol, BkName: candidate.Sector,
		Metrics: map[string]string{"maliciousLLMScore": "999", "maliciousLLMTarget": "9999"},
		Security: MarketSummaryV150SecuritySource{
			Name: candidate.Name, Market: "主板", Exchange: "SZSE", Board: "主板", Industry: candidate.Sector,
			Currency: "CNY", ListStatus: "L", ListDate: "20200101", ObservedAt: asOf.Format(time.RFC3339Nano), Source: "stock_basic",
		},
		DailyData: MarketSummaryV150DailyDataSource{
			AdjustmentSource: "tencent_qfq", LatestTradeDate: asOf.AddDate(0, 0, -1).Format(time.DateOnly), AdjustmentFactor: 1, Complete: true,
		},
		QuoteEvidence: &MarketSummaryV150EvidenceTiming{
			EvidenceID: "realtime-quote:" + candidate.Symbol, EvidenceType: "realtime_quote", SourceAt: asOf, AvailableAt: asOf,
		},
	}
}

func marketSummaryV150TestBenchmark(riskOff bool) v150.BenchmarkSnapshot {
	if riskOff {
		return v150.BenchmarkSnapshot{Code: v150.BenchmarkCode, Close: 90, MA20: 95, MA60: 100, MA20FiveDaysAgo: 96, DataPresent: true}
	}
	return v150.BenchmarkSnapshot{Code: v150.BenchmarkCode, Close: 110, MA20: 105, MA60: 100, MA20FiveDaysAgo: 103, DataPresent: true}
}

func TestMarketSummaryV150IsCurrentCohortWithoutRewritingV142(t *testing.T) {
	if marketSummaryCurrentVersion != marketSummaryVersion150 {
		t.Fatalf("current version = %q, want %q", marketSummaryCurrentVersion, marketSummaryVersion150)
	}
	for _, alias := range []string{"1.5.0", "v1.5.0", "150", "v150"} {
		if got := normalizeStrategyCohort(alias, strategyCohortAll); got != marketSummaryVersion150 {
			t.Fatalf("normalizeStrategyCohort(%q) = %q", alias, got)
		}
	}
	if got := normalizeStrategyCohort("1.4.2", strategyCohortAll); got != marketSummaryVersion142 {
		t.Fatalf("v1.4.2 alias was rewritten to %q", got)
	}
}

func TestApplyMarketSummaryDecisionTimelineIsNonZeroAndCausal(t *testing.T) {
	dataCutoff := time.Date(2026, 8, 4, 9, 40, 0, 0, cnLocation())
	decisionAt := dataCutoff.Add(95 * time.Second)
	rule := activationRule{
		Version: activationRuleVersionV3,
		Mode:    activationRuleModeAnyOf,
		Paths: []activationRule{{
			Name:             "pullback",
			SignalType:       "price_range_with_volume",
			Baseline:         "avg_amount_5x5m",
			ThresholdValue:   10,
			ThresholdMax:     10.2,
			VolumeRatio:      1.1,
			EvaluationWindow: "5m",
		}},
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	draft := &marketSummaryRecommendDraft{
		SummaryVersion:     marketSummaryVersion150,
		ActivationRuleJSON: string(raw),
	}

	applyMarketSummaryDecisionTimeline([]*marketSummaryRecommendDraft{draft}, dataCutoff, decisionAt)
	if draft.DataTime == nil || !draft.DataTime.Equal(decisionAt) {
		t.Fatalf("DataTime = %v, want decision completion %v", draft.DataTime, decisionAt)
	}
	parsed, err := parseActivationRuleJSON(draft.ActivationRuleJSON)
	if err != nil {
		t.Fatalf("parse stamped rule: %v", err)
	}
	assertTimeline := func(label string, item activationRule) {
		t.Helper()
		if item.GeneratedAt.IsZero() || item.DataCutoffTime.IsZero() || item.ValidFrom.IsZero() {
			t.Fatalf("%s timeline contains zero timestamps: %+v", label, item)
		}
		expectedValidFrom := time.Date(2026, 8, 4, 9, 45, 0, 0, cnLocation())
		if !item.GeneratedAt.Equal(decisionAt) || !item.ValidFrom.Equal(expectedValidFrom) {
			t.Fatalf("%s generated/valid timestamps = %v/%v, want %v/%v", label, item.GeneratedAt, item.ValidFrom, decisionAt, expectedValidFrom)
		}
		if !item.ValidFrom.After(item.GeneratedAt) {
			t.Fatalf("%s validFrom must be strictly after generatedAt: %v <= %v", label, item.ValidFrom, item.GeneratedAt)
		}
		if !item.DataCutoffTime.Equal(dataCutoff) || item.DataCutoffTime.After(item.GeneratedAt) {
			t.Fatalf("%s cutoff = %v, generated = %v", label, item.DataCutoffTime, item.GeneratedAt)
		}
	}
	assertTimeline("root", *parsed)
	if len(parsed.Paths) != 1 {
		t.Fatalf("paths = %d, want 1", len(parsed.Paths))
	}
	assertTimeline("path", parsed.Paths[0])
	if err := validateActivationRuleTimelineForPaths(parsed, *draft.toPreviewRecommend()); err != nil {
		t.Fatalf("causal timeline validation failed: %v", err)
	}
}

func TestNextMarketSummary15MinuteBarStartBoundaries(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	nextDayOpen := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	tests := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{name: "before open", at: day.Add(9 * time.Hour), want: day.Add(9*time.Hour + 30*time.Minute)},
		{name: "exact open is strict", at: day.Add(9*time.Hour + 30*time.Minute), want: day.Add(9*time.Hour + 45*time.Minute)},
		{name: "inside morning bar", at: day.Add(9*time.Hour + 41*time.Minute + 35*time.Second), want: day.Add(9*time.Hour + 45*time.Minute)},
		{name: "exact boundary advances", at: day.Add(9*time.Hour + 45*time.Minute), want: day.Add(10 * time.Hour)},
		{name: "last morning bar crosses lunch", at: day.Add(11*time.Hour + 29*time.Minute), want: day.Add(13 * time.Hour)},
		{name: "lunch break", at: day.Add(12 * time.Hour), want: day.Add(13 * time.Hour)},
		{name: "last afternoon bar available", at: day.Add(14*time.Hour + 44*time.Minute), want: day.Add(14*time.Hour + 45*time.Minute)},
		{name: "last bar start advances trade day", at: day.Add(14*time.Hour + 45*time.Minute), want: nextDayOpen},
		{name: "after close", at: day.Add(15 * time.Hour), want: nextDayOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextMarketSummary15MinuteBarStart(tt.at)
			if !got.Equal(tt.want) {
				t.Fatalf("next bar start = %v, want %v", got, tt.want)
			}
			if !got.After(tt.at) {
				t.Fatalf("next bar start must be strictly after decision: %v <= %v", got, tt.at)
			}
		})
	}
}

func TestPrepareMarketSummaryV150ActivationBarsDropsPreValidAndIncomplete(t *testing.T) {
	loc := cnLocation()
	validFrom := time.Date(2026, 8, 4, 9, 45, 0, 0, loc)
	rule := activationRule{
		Version:        activationRuleVersionV3,
		Mode:           activationRuleModeAnyOf,
		ValidFrom:      validFrom,
		GeneratedAt:    validFrom.Add(-5 * time.Minute),
		DataCutoffTime: validFrom.Add(-6 * time.Minute),
		Paths: []activationRule{{
			Name:             "pullback",
			SignalType:       "price_range_with_volume",
			EvaluationWindow: "15m",
			Baseline:         "manual_amount",
			ThresholdValue:   10,
			ThresholdMax:     11,
			Support:          10.3,
			VolumeRatio:      1,
			ConfirmBars:      1,
			VolumeWindow:     1,
			VolumeMetric:     "amount",
			ValidFrom:        validFrom,
		}},
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	rec := models.AiRecommendStocks{
		SummaryVersion:     marketSummaryVersion150,
		ActivationRuleJSON: string(raw),
		RecommendBuyPrice:  "10-11",
	}
	bars := []minuteBar{
		{TradeTime: validFrom.Add(-time.Minute), Open: 99, High: 100, Low: 98, Close: 99, Amount: 500},
		{TradeTime: validFrom, Open: 10, High: 10.4, Low: 9.9, Close: 10.2, Amount: 100},
		{TradeTime: validFrom.Add(14 * time.Minute), Open: 10.2, High: 10.6, Low: 10.1, Close: 10.5, Amount: 120},
		{TradeTime: validFrom.Add(15 * time.Minute), Open: 200, High: 201, Low: 199, Close: 200, Amount: 900},
	}

	prepared, reason := prepareMarketSummaryV150ActivationBars(rec, bars, validFrom.Add(22*time.Minute))
	if reason != "" {
		t.Fatalf("prepare reason = %q", reason)
	}
	if len(prepared) != 1 {
		t.Fatalf("prepared bars = %d, want one completed post-valid 15m bar", len(prepared))
	}
	wantObservedAt := validFrom.Add(15 * time.Minute)
	if !prepared[0].TradeTime.Equal(wantObservedAt) {
		t.Fatalf("completed bar observed at %v, want %v", prepared[0].TradeTime, wantObservedAt)
	}
	if prepared[0].High != 10.6 || prepared[0].Low != 9.9 {
		t.Fatalf("prepared OHLC leaked pre-valid/partial data: %+v", prepared[0])
	}
	if scan := resolveActivationRuleScan(rec, prepared); !scan.Triggered || !scan.Time.Equal(wantObservedAt) {
		t.Fatalf("completed v1.5 bar did not trigger at close: %+v", scan)
	}

	prepared, _ = prepareMarketSummaryV150ActivationBars(rec, bars, validFrom.Add(14*time.Minute))
	if len(prepared) != 0 {
		t.Fatalf("incomplete 15m interval entered activation scan: %+v", prepared)
	}
	prepared, _ = prepareMarketSummaryV150ActivationBars(rec, bars[1:2], validFrom.Add(15*time.Minute))
	if len(prepared) != 0 {
		t.Fatalf("truncated minute cache was treated as a complete 15m bar: %+v", prepared)
	}
}

func TestMarketSummaryV150PullbackRequiresStatefulSupportRecovery(t *testing.T) {
	loc := cnLocation()
	validFrom := time.Date(2026, 8, 4, 9, 45, 0, 0, loc)
	rule := &activationRule{
		Name:             "pullback",
		SignalType:       "price_range_with_volume",
		EvaluationWindow: "15m",
		ThresholdValue:   9.8,
		ThresholdMax:     10.1,
		Support:          10,
		ValidFrom:        validFrom,
	}
	bars := []minuteBar{
		// The first completed bar touches the entry zone but closes below
		// support. The second recovers support without touching the zone again.
		{TradeTime: validFrom.Add(15 * time.Minute), Open: 10.2, High: 10.2, Low: 9.9, Close: 9.95},
		{TradeTime: validFrom.Add(30 * time.Minute), Open: 9.95, High: 10.4, Low: 10.2, Close: 10.3},
	}
	scan := resolveSingleActivationRuleScan(models.AiRecommendStocks{SummaryVersion: marketSummaryVersion150}, rule, bars)
	if !scan.Triggered || !scan.Time.Equal(bars[1].TradeTime) || scan.Reason != "completed_15m_recovery" {
		t.Fatalf("stateful pullback recovery = %+v", scan)
	}

	missingSupport := *rule
	missingSupport.Support = 0
	if scan := resolveSingleActivationRuleScan(models.AiRecommendStocks{SummaryVersion: marketSummaryVersion150}, &missingSupport, bars); scan.Triggered {
		t.Fatalf("v1.5 pullback fabricated missing support: %+v", scan)
	}
}

func TestMarketSummaryV150CapacitySharesDailyAndActivePendingLimits(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-v150-capacity.db"))
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &models.AiRecommendYieldRecordState{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	loc := cnLocation()
	now := time.Date(2026, 8, 4, 14, 35, 0, 0, loc)
	previousDay := now.AddDate(0, 0, -1)
	seed := func(code, version, execution, status string, at time.Time) models.AiRecommendStocks {
		t.Helper()
		item := models.AiRecommendStocks{
			StockCode:        code,
			StockName:        code,
			SummaryVersion:   version,
			ExecutionState:   execution,
			ActivationStatus: status,
			DataTime:         &at,
		}
		if err := db.Dao.Create(&item).Error; err != nil {
			t.Fatalf("seed %s: %v", code, err)
		}
		return item
	}

	dailyPending := seed("000001.SZ", marketSummaryVersion150, recommendExecutionConditional, "pending", now)
	open := seed("000002.SZ", marketSummaryVersion150, recommendExecutionConditional, "activated", previousDay)
	closed := seed("000003.SZ", marketSummaryVersion150, recommendExecutionConditional, "activated", previousDay)
	seed("000004.SZ", marketSummaryVersion142, recommendExecutionConditional, "pending", now)
	seed("000005.SZ", marketSummaryVersion150, recommendExecutionAnalysisOnly, "pending", now)
	soldAt := now.Add(-time.Hour)
	states := []models.AiRecommendYieldRecordState{
		{RecommendID: open.ID, StockCode: open.StockCode, ActivationStatus: "activated"},
		{RecommendID: closed.ID, StockCode: closed.StockCode, ActivationStatus: "activated", SellTime: &soldAt},
	}
	if err := db.Dao.Create(&states).Error; err != nil {
		t.Fatalf("seed states: %v", err)
	}

	capacity, err := loadMarketSummaryV150Capacity(db.Dao, now)
	if err != nil {
		t.Fatalf("load capacity: %v", err)
	}
	if capacity.DailyProduction != 1 {
		t.Fatalf("daily production = %d, want 1", capacity.DailyProduction)
	}
	if capacity.ActivePending != 2 {
		t.Fatalf("active/pending = %d, want 2", capacity.ActivePending)
	}
	limit, err := resolveMarketSummaryV150ProductionLimit(db.Dao, now, 2)
	if err != nil {
		t.Fatalf("resolve limit: %v", err)
	}
	if limit != 1 {
		t.Fatalf("resolved daily shared limit = %d, want 1", limit)
	}

	for idx, code := range []string{"000006.SZ", "000007.SZ", "000008.SZ"} {
		at := previousDay.Add(-time.Duration(idx+1) * time.Hour)
		seed(code, marketSummaryVersion150, recommendExecutionConditional, "pending", at)
	}
	capacity, err = loadMarketSummaryV150Capacity(db.Dao, now)
	if err != nil {
		t.Fatalf("reload capacity: %v", err)
	}
	if capacity.ActivePending != marketSummaryMaxActivePendingCandidates {
		t.Fatalf("active/pending = %d, want %d", capacity.ActivePending, marketSummaryMaxActivePendingCandidates)
	}
	limit, err = resolveMarketSummaryV150ProductionLimit(db.Dao, now, 2)
	if err != nil {
		t.Fatalf("resolve active limit: %v", err)
	}
	if limit != 0 {
		t.Fatalf("active/pending cap should block new production, got %d", limit)
	}
	_ = dailyPending
}

func TestSecondRoundSelectionRejectsNewRecordsAndAuthorizedRepair(t *testing.T) {
	newDraft := &marketSummaryRecommendDraft{StockCode: "000001.SZ", SummaryVersion: marketSummaryVersion150}
	selections, stats := selectMarketSummaryRecommendDraftsForSave(
		[]*marketSummaryRecommendDraft{newDraft},
		nil,
		[]MarketSummaryVerifiedCandidateSnapshot{{StockCode: "000001.SZ"}},
		MarketSummaryRecommendSaveOptions{NewRecordLimit: 12, RequireVerifiedRepair: true},
	)
	if len(selections) != 0 || stats.BlockedCount != 1 {
		t.Fatalf("second-round new selection = %d blocked=%d, want 0/1", len(selections), stats.BlockedCount)
	}

	existing := models.AiRecommendStocks{
		StockCode:      "000002.SZ",
		SummaryVersion: marketSummaryVersion150,
		ExecutionState: recommendExecutionAnalysisOnly,
	}
	existing.ID = 42
	repair := models.MarketSummaryTradePlanRepairCandidate{RecommendID: existing.ID, StockCode: existing.StockCode}
	repairDraft := &marketSummaryRecommendDraft{StockCode: existing.StockCode, SummaryVersion: marketSummaryVersion150}
	selections, stats = selectMarketSummaryRecommendDraftsForSave(
		[]*marketSummaryRecommendDraft{repairDraft},
		[]models.AiRecommendStocks{existing},
		[]MarketSummaryVerifiedCandidateSnapshot{{StockCode: existing.StockCode}},
		MarketSummaryRecommendSaveOptions{
			NewRecordLimit:        0,
			RepairableFailures:    []models.MarketSummaryTradePlanRepairCandidate{repair},
			RequireVerifiedRepair: true,
		},
	)
	if len(selections) != 0 || stats.BlockedCount != 1 {
		t.Fatalf("analysis_only repair selection = %d blocked=%d, want 0/1", len(selections), stats.BlockedCount)
	}
	upgradedCount := 0
	repairedItem := &models.AiRecommendStocks{StockCode: existing.StockCode, SummaryVersion: marketSummaryVersion150}
	if err := upgradeMarketSummaryAnalysisOnlyRecommend(existing.ID, &repair, repairedItem, time.Now()); err == nil {
		upgradedCount++
	}
	if upgradedCount != 0 {
		t.Fatalf("v1.5 analysis_only upgraded count = %d, want 0", upgradedCount)
	}
}

func TestUpgradeMarketSummaryAnalysisOnlyRecommendRejectsLegacyVersion(t *testing.T) {
	existing := models.AiRecommendStocks{
		StockCode:      "000003.SZ",
		SummaryVersion: marketSummaryVersion142,
		ExecutionState: recommendExecutionAnalysisOnly,
	}
	existing.ID = 43
	repair := models.MarketSummaryTradePlanRepairCandidate{RecommendID: existing.ID, StockCode: existing.StockCode}
	item := &models.AiRecommendStocks{StockCode: existing.StockCode, SummaryVersion: marketSummaryVersion142}
	if err := upgradeMarketSummaryAnalysisOnlyRecommend(existing.ID, &repair, item, time.Now()); err == nil {
		t.Fatal("legacy analysis_only record must be irreversible")
	}
}

func TestUpgradeMarketSummaryAnalysisOnlyRecommendReadsPersistedV150Version(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-v150-irreversible.db"))
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	at := time.Date(2026, 8, 4, 10, 0, 0, 0, cnLocation())
	stored := models.AiRecommendStocks{
		StockCode:      "000002.SZ",
		StockName:      "irreversible",
		SummaryVersion: marketSummaryVersion150,
		ExecutionState: recommendExecutionAnalysisOnly,
		DataTime:       &at,
	}
	if err := db.Dao.Create(&stored).Error; err != nil {
		t.Fatalf("seed v1.5 analysis_only: %v", err)
	}
	repair := models.MarketSummaryTradePlanRepairCandidate{RecommendID: stored.ID, StockCode: stored.StockCode}
	// Deliberately spoof an older item version: the persisted v1.5 version must
	// still make the upgrade path fail closed.
	item := &models.AiRecommendStocks{StockCode: stored.StockCode, SummaryVersion: marketSummaryVersion142}
	upgradedCount := 0
	if err := upgradeMarketSummaryAnalysisOnlyRecommend(stored.ID, &repair, item, at); err == nil {
		upgradedCount++
	}
	if upgradedCount != 0 {
		t.Fatalf("persisted v1.5 analysis_only upgraded count = %d, want 0", upgradedCount)
	}
	var reloaded models.AiRecommendStocks
	if err := db.Dao.First(&reloaded, stored.ID).Error; err != nil {
		t.Fatalf("reload v1.5 analysis_only: %v", err)
	}
	if reloaded.ExecutionState != recommendExecutionAnalysisOnly || reloaded.SummaryVersion != marketSummaryVersion150 {
		t.Fatalf("irreversible record mutated: version=%q state=%q", reloaded.SummaryVersion, reloaded.ExecutionState)
	}
}

func TestMarketSummaryV150RanksCompletePoolBeforeTop18Deterministically(t *testing.T) {
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidates := make([]v150.Candidate, 0, 24)
	sources := make(map[string]MarketSummaryV150SourceCandidate, 24)
	for index := 0; index < 24; index++ {
		symbol := fmt.Sprintf("%06d.SZ", index+1)
		candidate := marketSummaryV150TestCandidate(symbol, fmt.Sprintf("sector-%d", index%4), 0.70+float64(index)/100, cutoff)
		candidates = append(candidates, candidate)
		sources[symbol] = marketSummaryV150TestSource(candidate, cutoff)
	}
	reversed := append([]v150.Candidate(nil), candidates...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}

	left, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), candidates, sources)
	if err != nil {
		t.Fatalf("left run: %v", err)
	}
	right, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), reversed, sources)
	if err != nil {
		t.Fatalf("right run: %v", err)
	}
	if len(left.Candidates) != 24 || len(left.VerificationSymbols) != v150.FixedStrategyV150Config().VerificationLimit {
		t.Fatalf("rank/top counts = %d/%d", len(left.Candidates), len(left.VerificationSymbols))
	}
	if !reflect.DeepEqual(left.VerificationSymbols, right.VerificationSymbols) {
		t.Fatalf("input order changed top18:\nleft=%v\nright=%v", left.VerificationSymbols, right.VerificationSymbols)
	}
	if left.DataHash != right.DataHash {
		t.Fatalf("input order changed data hash: %s != %s", left.DataHash, right.DataHash)
	}
	for index := range left.Candidates {
		if left.Candidates[index].Candidate.Symbol != right.Candidates[index].Candidate.Symbol || left.Candidates[index].Rank != index+1 {
			t.Fatalf("deterministic rank[%d] mismatch: %+v / %+v", index, left.Candidates[index], right.Candidates[index])
		}
	}
}

func TestMarketSummaryV150RiskOffProducesStructuredNoTrade(t *testing.T) {
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, cutoff)
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(true), []v150.Candidate{candidate}, map[string]MarketSummaryV150SourceCandidate{
		candidate.Symbol: marketSummaryV150TestSource(candidate, cutoff),
	})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	if err := finalizeMarketSummaryV150Run(run, nil, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(run.Production) != 0 || run.NoTradeReason != v150.RejectRiskOff {
		t.Fatalf("risk_off output = production:%d noTrade:%q", len(run.Production), run.NoTradeReason)
	}
}

func TestMarketSummaryV150PortfolioStateFailureRejectsOtherwiseValidCandidate(t *testing.T) {
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, cutoff)
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), []v150.Candidate{candidate}, map[string]MarketSummaryV150SourceCandidate{
		candidate.Symbol: marketSummaryV150TestSource(candidate, cutoff),
	})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	run.PortfolioStateStatus = "failed"
	verified := []marketSummaryVerifiedCandidate{{StockCode: candidate.Symbol}}
	if err := finalizeMarketSummaryV150Run(run, verified, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(run.Production) != 0 || run.NoTradeReason != marketSummaryV150PortfolioUnknown {
		t.Fatalf("portfolio failure output = production:%d noTrade:%q", len(run.Production), run.NoTradeReason)
	}
	if run.Candidates[0].PortfolioEligibility.Eligible ||
		!strings.Contains(strings.Join(run.Candidates[0].PortfolioEligibility.Reasons, ","), marketSummaryV150PortfolioUnknown) {
		t.Fatalf("portfolio failure did not reject candidate: %+v", run.Candidates[0].PortfolioEligibility)
	}
}

func TestFinalizeMarketSummaryV150RejectsQuoteStaleAtDecision(t *testing.T) {
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, cutoff)
	source := marketSummaryV150TestSource(candidate, cutoff)
	source.QuoteEvidence.SourceAt = cutoff.Add(-5*time.Minute - time.Second)
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "10:00", marketSummaryV150TestBenchmark(false), []v150.Candidate{candidate}, map[string]MarketSummaryV150SourceCandidate{candidate.Symbol: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeMarketSummaryV150Run(run, []marketSummaryVerifiedCandidate{{StockCode: candidate.Symbol}}, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(run.Production) != 0 || !strings.Contains(strings.Join(run.Candidates[0].Eligibility.Reasons, ","), v150.RejectMissingCurrent) {
		t.Fatalf("stale decision quote was not rejected: production=%+v candidate=%+v", run.Production, run.Candidates[0])
	}
}

func TestRefreshMarketSummaryV150VerificationQuotesUpdatesFrozenInputs(t *testing.T) {
	loc := cnLocation()
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	originalLoader := loadMarketSummaryV150RealtimeQuotesForRefresh
	originalNow := marketSummaryV150QuoteRefreshNow
	t.Cleanup(func() {
		loadMarketSummaryV150RealtimeQuotesForRefresh = originalLoader
		marketSummaryV150QuoteRefreshNow = originalNow
	})
	marketSummaryV150QuoteRefreshNow = func() time.Time { return now }
	loadMarketSummaryV150RealtimeQuotesForRefresh = func(_ []marketSummaryIndicatorCandidate) map[string]StockInfo {
		return map[string]StockInfo{"000001.SZ": {
			Name: "*ST平安", Price: "10.20", PreClose: "10.00", Open: "10.10", Amount: "200000000", Volume: "1000000",
			Date: "2026-08-04", Time: "09:59:00",
		}}
	}
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, now)
	candidate.Price = 9.8
	source := marketSummaryV150TestSource(candidate, now.Add(-time.Minute))
	run := &MarketSummaryV150RunSnapshot{
		VerificationSymbols: []string{candidate.Symbol},
		Candidates: []MarketSummaryV150CandidateSnapshot{{
			Candidate: candidate, Source: source, VerificationSelected: true,
		}},
	}
	refreshed, failed := refreshMarketSummaryV150VerificationQuotes(run)
	row := run.Candidates[0]
	if refreshed != 1 || failed != 0 || !row.Candidate.HasCurrentData || row.Candidate.Price != 10.2 || row.Candidate.PreviousClose != 10 || math.Abs(row.Candidate.DayChangeRatio-0.02) > 1e-12 || !row.Candidate.ST || row.Candidate.Name != "*ST平安" {
		t.Fatalf("quote refresh result=%d/%d candidate=%+v", refreshed, failed, row.Candidate)
	}
	if row.Source.QuoteEvidence == nil || !row.Source.QuoteEvidence.AvailableAt.Equal(now) || !row.Source.QuoteEvidence.SourceAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("quote refresh provenance=%+v", row.Source.QuoteEvidence)
	}
}

func TestMarketSummaryV150IgnoresMaliciousModelScoresTargetsAndState(t *testing.T) {
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, cutoff)
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), []v150.Candidate{candidate}, map[string]MarketSummaryV150SourceCandidate{
		candidate.Symbol: marketSummaryV150TestSource(candidate, cutoff),
	})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	verified := []marketSummaryVerifiedCandidate{{
		StockCode:     candidate.Symbol,
		Reason:        "LLM says score=999 target=9999 execution=activated",
		FeasiblePlans: []marketSummaryFeasiblePlan{{Path: "breakout", EntryRange: "1-1", WorstEntry: 1, StopLoss: 0.1, TakeProfit: 9999}},
	}}
	if err := finalizeMarketSummaryV150Run(run, verified, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(run.Production) != 1 {
		t.Fatalf("production=%d noTrade=%s candidates=%+v", len(run.Production), run.NoTradeReason, run.Candidates)
	}
	production := run.Production[0]
	if production.Score.Total != 100 || production.Plan.Target >= 100 || production.Plan.Target == 9999 {
		t.Fatalf("model prose changed backend score/target: %+v", production)
	}
	// Simulate a persistence-boundary regression independently of finalization:
	// without quote evidence the writer must not fabricate DataCutoffAt as the
	// current-price observation time.
	run.Candidates[0].Source.QuoteEvidence = nil
	recommend, err := buildMarketSummaryV150RecommendStock(run, production, "backend", marketSummaryV150LocalModelSpec)
	if err != nil {
		t.Fatalf("build recommendation: %v", err)
	}
	if recommend.SummaryVersion != marketSummaryVersion150 || recommend.ExecutionState != recommendExecutionConditional || recommend.ActivationStatus != "pending" {
		t.Fatalf("model prose changed backend state: version=%q execution=%q activation=%q", recommend.SummaryVersion, recommend.ExecutionState, recommend.ActivationStatus)
	}
	if recommend.RecommendStopProfitPriceMin != production.Plan.Target {
		t.Fatalf("persisted target=%v, backend=%v", recommend.RecommendStopProfitPriceMin, production.Plan.Target)
	}
	if recommend.StockCurrentPriceTime != "" {
		t.Fatalf("missing quote provenance must not be fabricated from cutoff: %q", recommend.StockCurrentPriceTime)
	}
}

func TestMarketSummaryV150RejectsCandidateWithEvidenceCausalityViolation(t *testing.T) {
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, cutoff)
	source := marketSummaryV150TestSource(candidate, cutoff)
	source.EventEvidence = []MarketSummaryV150EvidenceTiming{{
		EvidenceID: "future-availability", EvidenceType: "news", SourceAt: cutoff.Add(-time.Minute), AvailableAt: cutoff.Add(time.Second),
	}}
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), []v150.Candidate{candidate}, map[string]MarketSummaryV150SourceCandidate{candidate.Symbol: source})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	verified := []marketSummaryVerifiedCandidate{{StockCode: candidate.Symbol}}
	if err := finalizeMarketSummaryV150Run(run, verified, v150.PortfolioState{}, cutoff.Add(2*time.Second)); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(run.Production) != 0 || run.NoTradeReason != marketSummaryV150NoLegalCandidate {
		t.Fatalf("causality violation was not rejected: production=%d noTrade=%q", len(run.Production), run.NoTradeReason)
	}
	if !strings.Contains(strings.Join(run.Candidates[0].Eligibility.Reasons, ","), marketSummaryV150CausalityReject) {
		t.Fatalf("causality rejection missing: %+v", run.Candidates[0].Eligibility)
	}
}

func TestMarketSummaryV150EventAssessmentNeverRewardsNegativeNews(t *testing.T) {
	loc := cnLocation()
	asOf := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	sourceAt := asOf.Add(-time.Hour).Format(time.RFC3339Nano)
	availableAt := asOf.Add(-30 * time.Minute).Format(time.RFC3339Nano)
	item := marketSummaryIndicatorCandidate{StockName: "测试股份", StockCode: "000001.SZ", BkName: "软件"}
	negativeInput := marketSummaryDiscoveryInput{MarketNews: []marketSummaryDiscoverySnippet{{
		Title: "测试股份收到立案处罚并提示退市风险", Source: "exchange", SourceAt: sourceAt, AvailableAt: availableAt, EvidenceID: "negative-1",
	}}}
	eventAt, strength, timeline, assessment, warnings := marketSummaryV150EventSignal(item, negativeInput, asOf, nil)
	if eventAt != nil || strength != 0 || assessment.Direction != "negative" || len(timeline) != 1 || len(warnings) != 0 {
		t.Fatalf("negative event assessment = at:%v strength:%v timeline:%+v assessment:%+v warnings:%v", eventAt, strength, timeline, assessment, warnings)
	}
	positiveInput := marketSummaryDiscoveryInput{MarketNews: []marketSummaryDiscoverySnippet{{
		Title: "测试股份中标重大订单", Source: "exchange", SourceAt: sourceAt, AvailableAt: availableAt, EvidenceID: "positive-1",
	}}}
	eventAt, strength, _, assessment, warnings = marketSummaryV150EventSignal(item, positiveInput, asOf, nil)
	if eventAt == nil || strength <= 0 || strength > 1 || assessment.Direction != "positive" || assessment.Relevance != 1 || len(assessment.EvidenceIDs) != 1 || len(warnings) != 0 {
		t.Fatalf("positive structured assessment = at:%v strength:%v assessment:%+v warnings:%v", eventAt, strength, assessment, warnings)
	}
}

func TestMarketSummaryV150QuoteFreshnessIsSessionAware(t *testing.T) {
	loc := cnLocation()
	tests := []struct {
		name      string
		asOf      time.Time
		quoteDate string
		quoteTime string
		want      bool
	}{
		{name: "active session exact five minute boundary", asOf: time.Date(2026, 8, 4, 10, 0, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "09:55:00", want: true},
		{name: "active session rejects visibly stale quote", asOf: time.Date(2026, 8, 4, 10, 0, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "09:54:59", want: false},
		{name: "active session rejects prior trading day", asOf: time.Date(2026, 8, 4, 10, 0, 0, 0, loc), quoteDate: "2026-08-03", quoteTime: "15:00:00", want: false},
		{name: "future provider time is rejected", asOf: time.Date(2026, 8, 4, 10, 0, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "10:00:01", want: false},
		{name: "auction pause accepts last legal auction quote", asOf: time.Date(2026, 8, 4, 9, 27, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "09:25:00", want: true},
		{name: "auction pause rejects stale auction quote", asOf: time.Date(2026, 8, 4, 9, 27, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "09:19:59", want: false},
		{name: "auction rejects impossible pre-auction tick", asOf: time.Date(2026, 8, 4, 9, 16, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "09:11:00", want: false},
		{name: "morning session rejects auction-gap tick", asOf: time.Date(2026, 8, 4, 9, 30, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "09:27:00", want: false},
		{name: "lunch accepts last legal morning quote", asOf: time.Date(2026, 8, 4, 12, 0, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "11:30:00", want: true},
		{name: "lunch rejects old morning quote", asOf: time.Date(2026, 8, 4, 12, 0, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "11:24:59", want: false},
		{name: "lunch rejects impossible post-session tick", asOf: time.Date(2026, 8, 4, 12, 0, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "11:30:01", want: false},
		{name: "after close accepts official close", asOf: time.Date(2026, 8, 4, 15, 30, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "15:00:00", want: true},
		{name: "after close accepts five minute boundary", asOf: time.Date(2026, 8, 4, 15, 30, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "14:55:00", want: true},
		{name: "after close rejects stale quote", asOf: time.Date(2026, 8, 4, 15, 30, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "14:54:59", want: false},
		{name: "after close rejects impossible post-close tick", asOf: time.Date(2026, 8, 4, 15, 30, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "15:00:01", want: false},
		{name: "pre-open accepts prior official close", asOf: time.Date(2026, 8, 4, 9, 10, 0, 0, loc), quoteDate: "2026-08-03", quoteTime: "15:00:00", want: true},
		{name: "weekend accepts prior official close", asOf: time.Date(2026, 8, 2, 12, 0, 0, 0, loc), quoteDate: "2026-07-31", quoteTime: "15:00:00", want: true},
		{name: "missing market timestamp fails closed", asOf: time.Date(2026, 8, 4, 10, 0, 0, 0, loc), quoteDate: "2026-08-04", quoteTime: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quote := StockInfo{Date: test.quoteDate, Time: test.quoteTime}
			if got := marketSummaryV150QuoteIsFresh(quote, test.asOf); got != test.want {
				t.Fatalf("freshness=%v, want %v for quote=%s %s asOf=%s", got, test.want, test.quoteDate, test.quoteTime, test.asOf.Format(time.DateTime))
			}
		})
	}
}

func TestBuildMarketSummaryV150CandidateUsesRealtimeSTName(t *testing.T) {
	loc := cnLocation()
	asOf := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	item := marketSummaryIndicatorCandidate{StockName: "测试股份", StockCode: "000001.SZ", BkName: "银行"}
	basic := StockBasic{TsCode: item.StockCode, Name: "测试股份", Industry: item.BkName, ListStatus: "L", ListDate: "20200101"}
	quote := StockInfo{Name: "*ST测试", Date: asOf.Format(time.DateOnly), Time: "10:00:00"}

	candidate, _, _, _ := buildMarketSummaryV150Candidate(item, basic, quote, nil, marketSummaryDiscoveryInput{}, asOf, MarketSummaryV150DailyDataSource{})
	if !candidate.ST || candidate.Name != quote.Name {
		t.Fatalf("realtime ST marker was ignored: %+v", candidate)
	}
}

func TestMarketSummaryV150LegacyDailySourceMappingIsExplicitAndAuditable(t *testing.T) {
	if source, ok := normalizeMarketSummaryV150AdjustmentSource("sina"); !ok || source != "legacy_sina_label_mapped_to_tencent_qfq" {
		t.Fatalf("legacy source mapping = %q/%v", source, ok)
	}
	if source, ok := normalizeMarketSummaryV150AdjustmentSource("unknown_raw"); ok || source != "" {
		t.Fatalf("unknown adjustment source must fail closed: %q/%v", source, ok)
	}
}

func TestMarketSummaryV150RejectsStaleAdjustedDailySeries(t *testing.T) {
	loc := cnLocation()
	asOf := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	required := marketSummaryV150RequiredLatestDailyBar(asOf)
	descending := make([]dailyBar, 0, 66)
	for day := required; len(descending) < 66; day = day.AddDate(0, 0, -1) {
		if !isCNOpenTradeDaySafe(day) {
			continue
		}
		descending = append(descending, dailyBar{TradeDate: day, Open: 9.8, High: 10.2, Low: 9.7, Close: 10, Volume: 20_000_000, Amount: 200_000_000})
	}
	bars := make([]dailyBar, len(descending))
	for index := range descending {
		bars[len(descending)-1-index] = descending[index]
	}
	item := marketSummaryIndicatorCandidate{StockName: "测试股份", StockCode: "000001.SZ", BkName: "软件", Metrics: map[string]string{}}
	basic := StockBasic{TsCode: item.StockCode, Name: item.StockName, Industry: item.BkName, ListStatus: "L", ListDate: "20200101"}
	quote := StockInfo{Date: asOf.Format(time.DateOnly), Time: "10:00:00", Price: "10", PreClose: "9.9", Open: "9.9", Amount: "200000000", Volume: "20000000"}
	dailySource := MarketSummaryV150DailyDataSource{
		AdjustmentSource: "tencent_qfq", LatestTradeDate: required.Format(time.DateOnly), AdjustmentFactor: 1, Complete: true,
		SourceAt: time.Date(required.Year(), required.Month(), required.Day(), 15, 0, 0, 0, loc), AvailableAt: asOf.Add(-time.Hour),
	}
	fresh, warnings, _, _ := buildMarketSummaryV150Candidate(item, basic, quote, bars, marketSummaryDiscoveryInput{}, asOf, dailySource)
	if !fresh.HasDailyData {
		t.Fatalf("fresh qfq series rejected: warnings=%v candidate=%+v", warnings, fresh)
	}
	staleBars := bars[:len(bars)-1]
	dailySource.LatestTradeDate = staleBars[len(staleBars)-1].TradeDate.Format(time.DateOnly)
	dailySource.SourceAt = time.Date(staleBars[len(staleBars)-1].TradeDate.Year(), staleBars[len(staleBars)-1].TradeDate.Month(), staleBars[len(staleBars)-1].TradeDate.Day(), 15, 0, 0, 0, loc)
	stale, warnings, _, _ := buildMarketSummaryV150Candidate(item, basic, quote, staleBars, marketSummaryDiscoveryInput{}, asOf, dailySource)
	if stale.HasDailyData || !strings.Contains(strings.Join(warnings, ","), "daily_bar_stale") {
		t.Fatalf("stale daily series passed: hasDaily=%v warnings=%v", stale.HasDailyData, warnings)
	}
}

func TestMarketSummaryV150PortfolioUsesMostRecentStopAndExpiresOldPending(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-v150-portfolio.db"))
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(models.StrategyPersistenceModels()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	loc := cnLocation()
	asOf := time.Date(2026, 8, 12, 10, 0, 0, 0, loc)
	seedRule := func(runID, code, sector string, issuedAt, expiresAt time.Time, fillAt, stopAt *time.Time, frozenAt time.Time) {
		t.Helper()
		candidateID := runID + "|candidate|" + code
		ruleID := runID + "|rule|" + code
		candidate := models.CandidateSnapshot{
			CandidateID: candidateID, RunID: runID, StrategyVersion: v150.StrategyVersion,
			TradeDate: issuedAt.Format(time.DateOnly), Symbol: code, Sector: sector,
			SnapshotHash: runID + "-candidate", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}
		rule := models.RuleSnapshot{
			RuleID: ruleID, RunID: runID, CandidateID: candidateID, StrategyVersion: v150.StrategyVersion,
			TradeDate: issuedAt.Format(time.DateOnly), Symbol: code, RuleType: "entry", Path: string(v150.PathPullback),
			ValidFromAt: issuedAt.Add(time.Minute), ExpiresAt: &expiresAt,
			SnapshotHash: runID + "-rule", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}
		if err := db.Dao.Create(&candidate).Error; err != nil {
			t.Fatalf("seed candidate: %v", err)
		}
		if err := db.Dao.Create(&rule).Error; err != nil {
			t.Fatalf("seed rule: %v", err)
		}
		events := []models.OrderEvent{{
			EventID: ruleID + "|issued", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
			TradeDate: issuedAt.Format(time.DateOnly), Symbol: code, EventType: "rule_issued", Sequence: 1,
			EventAt: issuedAt, SnapshotHash: runID + "-issued", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}}
		if fillAt != nil {
			events = append(events, models.OrderEvent{
				EventID: ruleID + "|fill", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
				TradeDate: fillAt.Format(time.DateOnly), Symbol: code, EventType: string(v150.EventFill), Sequence: 2,
				EventAt: *fillAt, Price: 10, Quantity: 100, SnapshotHash: runID + "-fill", PayloadJSON: `{}`, FrozenAt: &frozenAt,
			})
		}
		if stopAt != nil {
			events = append(events, models.OrderEvent{
				EventID: ruleID + "|stop", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
				TradeDate: stopAt.Format(time.DateOnly), Symbol: code, EventType: string(v150.EventExitFill), Sequence: 3,
				EventAt: *stopAt, Price: 9, Quantity: 100, Reason: string(v150.ExitStop), SnapshotHash: runID + "-stop", PayloadJSON: `{}`, FrozenAt: &frozenAt,
			})
		}
		if err := db.Dao.Create(&events).Error; err != nil {
			t.Fatalf("seed events: %v", err)
		}
	}
	oldIssued := time.Date(2026, 7, 27, 10, 0, 0, 0, loc)
	oldFill := time.Date(2026, 7, 28, 10, 0, 0, 0, loc)
	oldStop := time.Date(2026, 7, 31, 10, 0, 0, 0, loc)
	seedRule("old-stop", "000001.SZ", "bank", oldIssued, oldIssued.AddDate(0, 0, 3), &oldFill, &oldStop, oldIssued.Add(time.Minute))
	recentIssued := time.Date(2026, 8, 7, 10, 0, 0, 0, loc)
	recentFill := time.Date(2026, 8, 10, 10, 0, 0, 0, loc)
	recentStop := time.Date(2026, 8, 11, 10, 0, 0, 0, loc)
	seedRule("recent-stop", "000001.SZ", "bank", recentIssued, asOf.AddDate(0, 0, 2), &recentFill, &recentStop, recentIssued.Add(time.Minute))
	oldPendingIssued := time.Date(2026, 7, 30, 10, 0, 0, 0, loc)
	oldPendingExpires := time.Date(2026, 8, 4, 15, 0, 0, 0, loc)
	seedRule("old-pending", "000002.SZ", "technology", oldPendingIssued, oldPendingExpires, nil, nil, oldPendingIssued.Add(time.Minute))
	// Friday after-close decision: validity starts Monday and remains pending
	// through Wednesday 15:00. Using recommendation.DataTime would expire it
	// early on Wednesday morning.
	freshIssued := time.Date(2026, 8, 7, 15, 10, 0, 0, loc)
	freshExpires := time.Date(2026, 8, 12, 15, 0, 0, 0, loc)
	seedRule("fresh-pending", "000003.SZ", "insurance", freshIssued, freshExpires, nil, nil, freshIssued.Add(time.Minute))
	futureIssued := asOf.Add(time.Hour)
	seedRule("future-pending", "000004.SZ", "materials", futureIssued, futureIssued.AddDate(0, 0, 3), nil, nil, futureIssued.Add(time.Minute))

	portfolio, err := loadMarketSummaryV150PortfolioState(db.Dao, asOf)
	if err != nil {
		t.Fatalf("load portfolio: %v", err)
	}
	if days, ok := portfolio.TradeDaysSinceLastStop["000001.SZ"]; !ok || days != 1 {
		t.Fatalf("most recent stop cooldown=%d/%v, want 1/true", days, ok)
	}
	if portfolio.PendingSymbols["000002.SZ"] {
		t.Fatal("pending rule older than three trading days occupied capacity forever")
	}
	if !portfolio.PendingSymbols["000003.SZ"] {
		t.Fatal("pending rule was expired from recommendation time instead of frozen rule expiry")
	}
	if portfolio.PendingSymbols["000004.SZ"] {
		t.Fatal("future recommendation contaminated a point-in-time quota check")
	}
}

func TestMarketSummaryV150DailyCapIsSharedAcrossSameDayRuns(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-v150-daily-cap.db"))
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(models.StrategyPersistenceModels()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	loc := cnLocation()
	priorDecision := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	frozenAt := priorDecision.Add(time.Minute)
	candidateID := "prior-run|candidate|000099.SZ"
	if err := db.Dao.Create(&models.CandidateSnapshot{
		CandidateID: candidateID, RunID: "prior-run", StrategyVersion: v150.StrategyVersion,
		TradeDate: priorDecision.Format(time.DateOnly), Symbol: "000099.SZ", Sector: "bank",
		SnapshotHash: "prior-candidate", PayloadJSON: `{}`, FrozenAt: &frozenAt,
	}).Error; err != nil {
		t.Fatalf("seed prior candidate: %v", err)
	}
	// Two paths for the same frozen run/symbol still consume one shared daily
	// stock slot, independent of the mutable recommendation projection.
	for index, path := range []v150.TradePath{v150.PathPullback, v150.PathBreakout} {
		ruleID := fmt.Sprintf("prior-rule-%d", index)
		expiresAt := time.Date(2026, 8, 6, 15, 0, 0, 0, loc)
		if err := db.Dao.Create(&models.RuleSnapshot{
			RuleID: ruleID, RunID: "prior-run", CandidateID: candidateID, StrategyVersion: v150.StrategyVersion,
			TradeDate: priorDecision.Format(time.DateOnly), Symbol: "000099.SZ", RuleType: "entry", Path: string(path),
			ValidFromAt: priorDecision.Add(5 * time.Minute), ExpiresAt: &expiresAt,
			SnapshotHash: ruleID + "-hash", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}).Error; err != nil {
			t.Fatalf("seed prior same-day rule: %v", err)
		}
		if err := db.Dao.Create(&models.OrderEvent{
			EventID: ruleID + "|issued", RunID: "prior-run", RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
			TradeDate: priorDecision.Format(time.DateOnly), Symbol: "000099.SZ", EventType: "rule_issued", Sequence: 1,
			EventAt: priorDecision, SnapshotHash: ruleID + "-event", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}).Error; err != nil {
			t.Fatalf("seed prior rule event: %v", err)
		}
	}
	asOf := time.Date(2026, 8, 4, 9, 50, 0, 0, loc)
	portfolio, err := loadMarketSummaryV150PortfolioState(db.Dao, asOf)
	if err != nil {
		t.Fatalf("load shared daily portfolio state: %v", err)
	}
	if portfolio.TodayEntries != 1 || portfolio.TodaySectorEntries["bank"] != 1 {
		t.Fatalf("deduplicated daily entries=%d sector=%d, want 1/1", portfolio.TodayEntries, portfolio.TodaySectorEntries["bank"])
	}

	cutoff := asOf.Add(time.Minute)
	first := marketSummaryV150TestCandidate("000001.SZ", "insurance", 1, cutoff)
	second := marketSummaryV150TestCandidate("000002.SZ", "technology", 0.99, cutoff)
	inputs := []v150.Candidate{first, second}
	sources := map[string]MarketSummaryV150SourceCandidate{
		first.Symbol: marketSummaryV150TestSource(first, cutoff), second.Symbol: marketSummaryV150TestSource(second, cutoff),
	}
	newRun := func(startedAt time.Time) *MarketSummaryV150RunSnapshot {
		run, runErr := newMarketSummaryV150Run(startedAt, cutoff, "09:50", marketSummaryV150TestBenchmark(false), inputs, sources)
		if runErr != nil {
			t.Fatalf("new same-day run: %v", runErr)
		}
		return run
	}
	verified := []marketSummaryVerifiedCandidate{{StockCode: first.Symbol}, {StockCode: second.Symbol}}
	run := newRun(asOf)
	if err := finalizeMarketSummaryV150Run(run, verified, portfolio, cutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize with one consumed slot: %v", err)
	}
	if len(run.Production) != 1 {
		t.Fatalf("production after one consumed risk-on slot=%d, want 1", len(run.Production))
	}

	full := cloneV150PortfolioState(portfolio)
	full.TodayEntries = 2
	fullRun := newRun(asOf.Add(time.Second))
	if err := finalizeMarketSummaryV150Run(fullRun, verified, full, cutoff.Add(2*time.Second)); err != nil {
		t.Fatalf("finalize with exhausted daily cap: %v", err)
	}
	if len(fullRun.Production) != 0 || fullRun.NoTradeReason != marketSummaryV150DailyCapReached {
		t.Fatalf("exhausted daily cap produced=%d noTrade=%q", len(fullRun.Production), fullRun.NoTradeReason)
	}
}

func TestPersistMarketSummaryV150RecommendationsRechecksSharedDailyQuota(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-v150-persist-quota.db"))
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := persistence.MigrateStrategyPersistence(db.Dao); err != nil {
		t.Fatalf("migrate immutable strategy tables: %v", err)
	}
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "insurance", 1, cutoff)
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), []v150.Candidate{candidate}, map[string]MarketSummaryV150SourceCandidate{
		candidate.Symbol: marketSummaryV150TestSource(candidate, cutoff),
	})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	if err := finalizeMarketSummaryV150Run(run, []marketSummaryVerifiedCandidate{{StockCode: candidate.Symbol}}, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(run.Production) != 1 {
		t.Fatalf("production=%d, want 1", len(run.Production))
	}
	// Consume both shared slots in the immutable ledger. The mutable
	// recommendation table is only a projection and must not influence quota.
	for index := 0; index < v150.FixedStrategyV150Config().RiskOnDailyCap; index++ {
		decisionAt := run.RunContext.DecisionAt
		frozenAt := decisionAt.Add(time.Hour)
		runID := fmt.Sprintf("already-issued-%d", index)
		ruleID := fmt.Sprintf("already-rule-%d", index)
		candidateID := runID + "|candidate"
		code := fmt.Sprintf("00009%d.SZ", index)
		candidateRow := models.CandidateSnapshot{
			CandidateID: candidateID, RunID: runID, StrategyVersion: v150.StrategyVersion,
			TradeDate: decisionAt.Format(time.DateOnly), Symbol: code, Sector: fmt.Sprintf("consumed-sector-%d", index),
			SnapshotHash: candidateID + "-hash", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}
		if err := db.Dao.Create(&candidateRow).Error; err != nil {
			t.Fatalf("seed consumed candidate: %v", err)
		}
		expiresAt := decisionAt.AddDate(0, 0, 3)
		ruleRow := models.RuleSnapshot{
			RuleID: ruleID, RunID: runID, CandidateID: candidateID, StrategyVersion: v150.StrategyVersion,
			TradeDate: decisionAt.Format(time.DateOnly), Symbol: code, RuleType: "entry", Path: string(v150.PathPullback),
			ValidFromAt: decisionAt.Add(time.Minute), ExpiresAt: &expiresAt,
			SnapshotHash: ruleID + "-hash", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}
		if err := db.Dao.Create(&ruleRow).Error; err != nil {
			t.Fatalf("seed consumed rule: %v", err)
		}
		eventRow := models.OrderEvent{
			EventID: ruleID + "|issued", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
			TradeDate: decisionAt.Format(time.DateOnly), Symbol: code, EventType: "rule_issued", Sequence: 1,
			EventAt: decisionAt, SnapshotHash: ruleID + "-event-hash", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}
		if err := db.Dao.Create(&eventRow).Error; err != nil {
			t.Fatalf("seed consumed rule event: %v", err)
		}
	}
	// A contradictory mutable projection must remain irrelevant.
	projectionTime := run.RunContext.DecisionAt
	if err := db.Dao.Create(&models.AiRecommendStocks{
		StockCode: "009999.SZ", SummaryVersion: marketSummaryVersion150,
		ExecutionState: recommendExecutionConditional, ActivationStatus: "pending", DataTime: &projectionTime,
		StrategyRunID: "projection-only", StrategyRuleID: "projection-only-rule",
	}).Error; err != nil {
		t.Fatalf("seed irrelevant mutable projection: %v", err)
	}

	result, err := PersistMarketSummaryV150Recommendations(db.Dao, run, "backend", marketSummaryV150LocalModelSpec)
	if err == nil {
		t.Fatal("stale run selection bypassed the final shared daily quota recheck")
	}
	if result == nil || result.SavedCount != 0 || result.BlockedCount != 1 || len(result.BlockedReasons) != 1 || result.BlockedReasons[0].Reason != marketSummaryV150DailyCapReached {
		t.Fatalf("quota recheck result=%+v err=%v", result, err)
	}
}

func TestMarketSummaryV150RuntimeDecisionPersistsCompleteImmutableBundle(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-v150-runtime.db"))
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() { _ = db.Close() })
	previousMarkDirty := markAiRecommendYieldDirtyCodesForMutationFn
	previousRequestRecalc := requestAiRecommendYieldScopedRecalcForMutationFn
	var markedCodes []string
	var requestedCodes []string
	markAiRecommendYieldDirtyCodesForMutationFn = func(codes []string, reason, mode string) error {
		markedCodes = append([]string(nil), codes...)
		if reason != "V1.5 immutable recommendation created; awaiting event-ledger execution replay" || mode != aiRecommendYieldModeStrict {
			t.Fatalf("unexpected dirty notification reason=%q mode=%q", reason, mode)
		}
		return nil
	}
	requestAiRecommendYieldScopedRecalcForMutationFn = func(force bool, reason string, codes []string) {
		requestedCodes = append([]string(nil), codes...)
		if force || reason != "v150_recommendations_created" {
			t.Fatalf("unexpected recalc notification force=%v reason=%q", force, reason)
		}
	}
	t.Cleanup(func() {
		markAiRecommendYieldDirtyCodesForMutationFn = previousMarkDirty
		requestAiRecommendYieldScopedRecalcForMutationFn = previousRequestRecalc
	})
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("migrate recommendations: %v", err)
	}
	if err := persistence.MigrateStrategyPersistence(db.Dao); err != nil {
		t.Fatalf("migrate strategy persistence: %v", err)
	}
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, cutoff)
	source := marketSummaryV150TestSource(candidate, cutoff)
	dailySourceAt := time.Date(2026, 8, 3, 15, 0, 0, 0, loc)
	dailyAvailableAt := time.Date(2026, 8, 4, 8, 0, 0, 0, loc)
	source.DailyData.SourceAt = dailySourceAt
	source.DailyData.AvailableAt = dailyAvailableAt
	source.QuoteEvidence = &MarketSummaryV150EvidenceTiming{
		EvidenceID: "quote:000001.SZ", EvidenceType: "realtime_quote", SourceAt: cutoff.Add(-time.Minute), AvailableAt: cutoff,
	}
	source.Security.SourceAt = startedAt.Add(-time.Hour).Format(time.RFC3339Nano)
	source.Security.AvailableAt = cutoff.Format(time.RFC3339Nano)
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), []v150.Candidate{candidate}, map[string]MarketSummaryV150SourceCandidate{candidate.Symbol: source})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	run.BenchmarkSource = MarketSummaryV150BenchmarkSource{
		Timing: MarketSummaryV150EvidenceTiming{
			EvidenceID: "benchmark:510300", EvidenceType: "benchmark_adjusted_daily_bar", SourceAt: dailySourceAt, AvailableAt: dailyAvailableAt,
		},
		AdjustmentSource: "tencent_qfq", LatestTradeDate: "2026-08-03", Complete: true,
	}
	verified := []marketSummaryVerifiedCandidate{{StockCode: candidate.Symbol}}
	if err := finalizeMarketSummaryV150Run(run, verified, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := refreshMarketSummaryV150DataHash(run); err != nil {
		t.Fatalf("refresh data hash: %v", err)
	}
	if err := PersistMarketSummaryV150Snapshot(context.Background(), db.Dao, run); err != nil {
		t.Fatalf("persist immutable snapshot: %v", err)
	}
	if len(run.Production) != 1 {
		t.Fatalf("production=%d", len(run.Production))
	}
	item, err := buildMarketSummaryV150RecommendStock(run, run.Production[0], "backend", marketSummaryV150LocalModelSpec)
	if err != nil {
		t.Fatalf("build backend recommendation: %v", err)
	}
	if want := source.QuoteEvidence.SourceAt.In(loc).Format(time.DateTime); item.StockCurrentPriceTime != want {
		t.Fatalf("current-price time=%q, want frozen quote source time %q", item.StockCurrentPriceTime, want)
	}
	tampered := *item
	tampered.RecommendStopProfitPriceMin = 9999
	if err := insertMarketSummaryV150Recommendation(db.Dao, run, run.Production[0], &tampered); err == nil {
		t.Fatal("tampered target passed the V1.5 immutable insert boundary")
	}
	saveResult, err := PersistMarketSummaryV150Recommendations(db.Dao, run, "backend", marketSummaryV150LocalModelSpec)
	if err != nil || saveResult.SavedCount != 1 {
		t.Fatalf("persist recommendation result=%+v err=%v", saveResult, err)
	}
	if want := []string{candidate.Symbol}; !reflect.DeepEqual(markedCodes, want) || !reflect.DeepEqual(requestedCodes, want) {
		t.Fatalf("V1.5 yield notifications marked=%v requested=%v want=%v", markedCodes, requestedCodes, want)
	}

	assertCount := func(model any, want int64) {
		t.Helper()
		var got int64
		if err := db.Dao.Model(model).Count(&got).Error; err != nil {
			t.Fatalf("count %T: %v", model, err)
		}
		if got != want {
			t.Fatalf("count %T=%d, want %d", model, got, want)
		}
	}
	assertCount(&models.StrategyRunSnapshot{}, 1)
	assertCount(&models.CandidateSnapshot{}, 1)
	var persistedCandidate models.CandidateSnapshot
	if err := db.Dao.First(&persistedCandidate).Error; err != nil {
		t.Fatalf("reload candidate snapshot: %v", err)
	}
	if persistedCandidate.PreVerifyRank != 1 || persistedCandidate.FinalRank != 1 || persistedCandidate.Rank != 1 {
		t.Fatalf("candidate stage ranks were not frozen: %+v", persistedCandidate)
	}
	assertCount(&models.RuleSnapshot{}, 1)
	assertCount(&models.OrderEvent{}, 1)
	assertCount(&models.SecurityMasterHistory{}, 1)
	// Production selection snapshots must not manufacture factor=1 corporate
	// actions. Execution-day coverage is persisted separately by the real
	// point-in-time corporate-action observation path.
	assertCount(&models.CorporateActionEvent{}, 0)
	assertCount(&models.AiRecommendStocks{}, 1)
	var persisted models.AiRecommendStocks
	if err := db.Dao.First(&persisted).Error; err != nil {
		t.Fatalf("reload recommendation: %v", err)
	}
	if persisted.ExecutionState != recommendExecutionConditional || persisted.ActivationStatus != "pending" ||
		persisted.RecommendStopProfitPriceMin != run.Production[0].Plan.Target || persisted.StrategyRunID != run.RunContext.RunID {
		t.Fatalf("legacy normalization changed V1.5 output: %+v", persisted)
	}

	riskOffStarted := startedAt.Add(3 * time.Hour)
	riskOffCutoff := riskOffStarted.Add(time.Minute)
	riskOff, err := newMarketSummaryV150Run(riskOffStarted, riskOffCutoff, "13:40", marketSummaryV150TestBenchmark(true), nil, nil)
	if err != nil {
		t.Fatalf("new risk-off run: %v", err)
	}
	riskOff.BenchmarkSource = run.BenchmarkSource
	if err := finalizeMarketSummaryV150Run(riskOff, nil, v150.PortfolioState{}, riskOffCutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize risk-off: %v", err)
	}
	if err := refreshMarketSummaryV150DataHash(riskOff); err != nil {
		t.Fatalf("hash risk-off: %v", err)
	}
	if err := PersistMarketSummaryV150Snapshot(context.Background(), db.Dao, riskOff); err != nil {
		t.Fatalf("persist risk-off no_trade: %v", err)
	}
	var noTrade models.OrderEvent
	if err := db.Dao.Where("run_id = ? AND event_type = ?", riskOff.RunContext.RunID, "no_trade").First(&noTrade).Error; err != nil {
		t.Fatalf("load structured no_trade event: %v", err)
	}
	if noTrade.Reason != v150.RejectRiskOff {
		t.Fatalf("no_trade reason=%q, want %q", noTrade.Reason, v150.RejectRiskOff)
	}

	multiStarted := startedAt.AddDate(0, 0, 1)
	multiCutoff := multiStarted.Add(time.Minute)
	secondCandidate := marketSummaryV150TestCandidate("000002.SZ", "insurance", 0.99, multiCutoff)
	secondSource := marketSummaryV150TestSource(secondCandidate, multiCutoff)
	firstForMulti := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, multiCutoff)
	firstSourceForMulti := marketSummaryV150TestSource(firstForMulti, multiCutoff)
	multi, err := newMarketSummaryV150Run(multiStarted, multiCutoff, "09:40", marketSummaryV150TestBenchmark(false), []v150.Candidate{secondCandidate, firstForMulti}, map[string]MarketSummaryV150SourceCandidate{
		firstForMulti.Symbol: firstSourceForMulti, secondCandidate.Symbol: secondSource,
	})
	if err != nil {
		t.Fatalf("new multi-rule run: %v", err)
	}
	multiBenchmarkSourceAt := time.Date(2026, 8, 4, 15, 0, 0, 0, loc)
	multi.BenchmarkSource = MarketSummaryV150BenchmarkSource{
		Timing:           MarketSummaryV150EvidenceTiming{EvidenceID: "benchmark:510300:2026-08-04", EvidenceType: "benchmark_adjusted_daily_bar", SourceAt: multiBenchmarkSourceAt, AvailableAt: multiStarted.Add(-time.Hour)},
		AdjustmentSource: "tencent_qfq", LatestTradeDate: "2026-08-04", Complete: true,
	}
	if err := finalizeMarketSummaryV150Run(multi, []marketSummaryVerifiedCandidate{{StockCode: firstForMulti.Symbol}, {StockCode: secondCandidate.Symbol}}, v150.PortfolioState{}, multiCutoff.Add(time.Second)); err != nil {
		t.Fatalf("finalize multi-rule: %v", err)
	}
	if len(multi.Production) != 2 {
		t.Fatalf("multi-rule production=%d, want 2", len(multi.Production))
	}
	if err := refreshMarketSummaryV150DataHash(multi); err != nil {
		t.Fatalf("hash multi-rule: %v", err)
	}
	if err := PersistMarketSummaryV150Snapshot(context.Background(), db.Dao, multi); err != nil {
		t.Fatalf("persist multi-rule: %v", err)
	}
	var issued []models.OrderEvent
	if err := db.Dao.Where("run_id = ? AND event_type = ?", multi.RunContext.RunID, "rule_issued").Order("rule_id ASC").Find(&issued).Error; err != nil {
		t.Fatalf("load multi-rule events: %v", err)
	}
	if len(issued) != 2 || issued[0].Sequence != 1 || issued[1].Sequence != 1 || issued[0].RuleID == issued[1].RuleID {
		t.Fatalf("per-rule initial sequences are not independent: %+v", issued)
	}
}
