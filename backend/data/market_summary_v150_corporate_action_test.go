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

func TestMarketSummaryV150CorporateActionEmptyCoverageIsExplicitAndCacheOnly(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	availableAt := time.Date(2026, 8, 5, 9, 29, 0, 0, loc)
	seedMarketSummaryV150CorporateActionObservation(t, "origin", "600000.SH", day, availableAt, marketSummaryV150CorporateActionStatusEmpty, nil, "", "")

	originalFetch := fetchMarketSummaryV150CorporateActionFactFn
	fetchCalls := 0
	fetchMarketSummaryV150CorporateActionFactFn = func(symbol string, coverageDay, observedAt time.Time) (marketSummaryV150CorporateActionFact, error) {
		fetchCalls++
		return marketSummaryV150CorporateActionFact{}, errors.New("provider must not run")
	}
	t.Cleanup(func() { fetchMarketSummaryV150CorporateActionFactFn = originalFetch })
	if _, err := refreshMarketSummaryV150CorporateActionObservation("origin", "600000.SH", day, false); err != nil {
		t.Fatalf("cache-only refresh: %v", err)
	}
	actions, err := loadMarketSummaryV150CorporateActions("600000.SH", day, time.Date(2026, 8, 5, 9, 30, 0, 0, loc))
	if err != nil {
		t.Fatalf("load empty coverage: %v", err)
	}
	if fetchCalls != 0 || len(actions) != 0 {
		t.Fatalf("fetchCalls=%d actions=%+v", fetchCalls, actions)
	}
}

func TestMarketSummaryV150CorporateActionOnlineRefreshReusesSameDayObservation(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	observedAt := time.Date(2026, 8, 5, 9, 29, 0, 0, loc)
	originalNow := marketSummaryV150CorporateActionNow
	originalFetch := fetchMarketSummaryV150CorporateActionFactFn
	marketSummaryV150CorporateActionNow = func() time.Time { return observedAt }
	fetchCalls := 0
	fetchMarketSummaryV150CorporateActionFactFn = func(symbol string, coverageDay, at time.Time) (marketSummaryV150CorporateActionFact, error) {
		fetchCalls++
		return marketSummaryV150CorporateActionFact{
			Symbol: symbol, CoverageDay: coverageDay, Status: marketSummaryV150CorporateActionStatusEmpty,
			Source: marketSummaryV150CorporateActionSource, SourceAt: at,
		}, nil
	}
	t.Cleanup(func() {
		marketSummaryV150CorporateActionNow = originalNow
		fetchMarketSummaryV150CorporateActionFactFn = originalFetch
	})

	firstRun, err := refreshMarketSummaryV150CorporateActionObservation("origin", "600000.SH", day, true)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	secondRun, err := refreshMarketSummaryV150CorporateActionObservation("origin", "600000.SH", day, true)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if fetchCalls != 1 || firstRun == "" || secondRun != firstRun {
		t.Fatalf("same-day observation was not reused: calls=%d first=%q second=%q", fetchCalls, firstRun, secondRun)
	}
}

func TestMarketSummaryV150CorporateActionFailedObservationIsThrottledThenRetried(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	failedAt := time.Date(2026, 8, 5, 9, 29, 0, 0, loc)
	failedRun := seedMarketSummaryV150CorporateActionObservation(
		t, "origin", "600000.SH", day, failedAt,
		marketSummaryV150CorporateActionStatusFailed, nil,
		"corporate_action_provider_failed", "temporary provider outage",
	)

	originalNow := marketSummaryV150CorporateActionNow
	originalFetch := fetchMarketSummaryV150CorporateActionFactFn
	now := failedAt.Add(10 * time.Second)
	marketSummaryV150CorporateActionNow = func() time.Time { return now }
	fetchCalls := 0
	fetchMarketSummaryV150CorporateActionFactFn = func(symbol string, coverageDay, observedAt time.Time) (marketSummaryV150CorporateActionFact, error) {
		fetchCalls++
		return marketSummaryV150CorporateActionFact{
			Symbol: symbol, CoverageDay: coverageDay, Status: marketSummaryV150CorporateActionStatusEmpty,
			Source: marketSummaryV150CorporateActionSource, SourceAt: observedAt,
		}, nil
	}
	t.Cleanup(func() {
		marketSummaryV150CorporateActionNow = originalNow
		fetchMarketSummaryV150CorporateActionFactFn = originalFetch
	})

	runID, err := refreshMarketSummaryV150CorporateActionObservation("origin", "600000.SH", day, true)
	var coverageErr *MarketSummaryV150CorporateActionCoverageError
	if runID != failedRun || !errors.As(err, &coverageErr) || fetchCalls != 0 {
		t.Fatalf("failure was not throttled: run=%q err=%v calls=%d", runID, err, fetchCalls)
	}

	now = failedAt.Add(marketSummaryV150CorporateActionRetryInterval + time.Second)
	retriedRun, err := refreshMarketSummaryV150CorporateActionObservation("origin", "600000.SH", day, true)
	if err != nil || fetchCalls != 1 || retriedRun == "" || retriedRun == failedRun {
		t.Fatalf("failed observation was not retried: run=%q err=%v calls=%d", retriedRun, err, fetchCalls)
	}
	// The newly frozen empty coverage is now stable and does not issue another
	// provider request during the same or any later monitor slot that day.
	if reusedRun, reuseErr := refreshMarketSummaryV150CorporateActionObservation("origin", "600000.SH", day, true); reuseErr != nil || reusedRun != retriedRun || fetchCalls != 1 {
		t.Fatalf("retried coverage was not reused: run=%q err=%v calls=%d", reusedRun, reuseErr, fetchCalls)
	}
}

func TestMarketSummaryV150CorporateActionLateObservationFailsClosed(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	seedMarketSummaryV150CorporateActionObservation(t, "origin", "600000.SH", day, time.Date(2026, 8, 5, 9, 31, 0, 0, loc), marketSummaryV150CorporateActionStatusEmpty, nil, "", "")
	_, err := loadMarketSummaryV150CorporateActions("600000.SH", day, time.Date(2026, 8, 5, 9, 30, 0, 0, loc))
	var coverageErr *MarketSummaryV150CorporateActionCoverageError
	if !errors.As(err, &coverageErr) || coverageErr.Code != "corporate_action_observation_missing" {
		t.Fatalf("err=%v want causal missing observation", err)
	}
}

func TestMarketSummaryV150CorporateActionFailedCoveragePreservesStructuredReason(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	availableAt := time.Date(2026, 8, 5, 9, 29, 0, 0, loc)
	seedMarketSummaryV150CorporateActionObservation(t, "origin", "600000.SH", day, availableAt, marketSummaryV150CorporateActionStatusFailed, nil, "corporate_action_factor_unreconciled", "possible rights event")
	_, err := loadMarketSummaryV150CorporateActions("600000.SH", day, time.Date(2026, 8, 5, 9, 30, 0, 0, loc))
	var coverageErr *MarketSummaryV150CorporateActionCoverageError
	if !errors.As(err, &coverageErr) || coverageErr.Code != "corporate_action_factor_unreconciled" || !strings.Contains(coverageErr.Cause, "rights") {
		t.Fatalf("unexpected structured error: %#v / %v", coverageErr, err)
	}
}

func TestMarketSummaryV150CorporateActionLoadsRealEntitlements(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	availableAt := time.Date(2026, 8, 5, 9, 29, 0, 0, loc)
	action := marketSummaryV150CorporateActionFactAction{
		Core: v150.CorporateAction{
			EventID: "real-dividend", Symbol: "600000.SH", ExDate: day, AvailableAt: availableAt,
			AdjustmentFactor: .8, CashDividend: .12, BonusRatio: .25,
		},
		AnnouncedAt: availableAt.AddDate(0, 0, -3), PreviousFactor: 1, CurrentFactor: 1.25,
	}
	seedMarketSummaryV150CorporateActionObservation(t, "origin", "600000.SH", day, availableAt, marketSummaryV150CorporateActionStatusOK, []marketSummaryV150CorporateActionFactAction{action}, "", "")
	actions, err := loadMarketSummaryV150CorporateActions("600000.SH", day, time.Date(2026, 8, 5, 9, 30, 0, 0, loc))
	if err != nil {
		t.Fatalf("load action: %v", err)
	}
	if len(actions) != 1 || actions[0].EventID != "real-dividend" || actions[0].AdjustmentFactor != .8 || actions[0].CashDividend != .12 || actions[0].BonusRatio != .25 {
		t.Fatalf("unexpected actions: %+v", actions)
	}
}

func TestMarketSummaryV150CorporateActionFactorPairRequiresCurrentAndPrevious(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	previous, current, err := marketSummaryV150CorporateActionFactorPair([]TushareAdjustmentFactor{
		{TradeDate: day.AddDate(0, 0, -1), Factor: 10}, {TradeDate: day, Factor: 12.5},
	}, day)
	if err != nil || previous != 10 || current != 12.5 {
		t.Fatalf("pair previous=%v current=%v err=%v", previous, current, err)
	}
	if _, _, err := marketSummaryV150CorporateActionFactorPair([]TushareAdjustmentFactor{{TradeDate: day.AddDate(0, 0, -1), Factor: 10}}, day); err == nil {
		t.Fatal("missing current-day factor must fail closed")
	}
}

func seedMarketSummaryV150CorporateActionObservation(
	t *testing.T,
	originRunID, symbol string,
	day, availableAt time.Time,
	status string,
	actions []marketSummaryV150CorporateActionFactAction,
	errorCode, errorText string,
) string {
	t.Helper()
	startedAt := availableAt.Add(-time.Minute)
	fact := marketSummaryV150CorporateActionFact{
		Symbol: symbol, CoverageDay: day, Status: status, Source: "test_corporate_action",
		SourceAt: startedAt, ErrorCode: errorCode, Error: errorText, Actions: actions,
	}
	runID, err := appendMarketSummaryV150CorporateActionObservation(originRunID, fact, startedAt, availableAt)
	if err != nil {
		t.Fatalf("append corporate action observation: %v", err)
	}
	var rows int64
	if err := db.Dao.Model(&models.CorporateActionEvent{}).Where("run_id = ?", runID).Count(&rows).Error; err != nil || rows == 0 {
		t.Fatalf("corporate action rows=%d err=%v", rows, err)
	}
	return runID
}
