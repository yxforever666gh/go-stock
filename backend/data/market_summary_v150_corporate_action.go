package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

const (
	marketSummaryV150CorporateActionObservationMode = persistence.StrategyRunModeExecutionCorporateActionObservation
	marketSummaryV150CorporateActionSource          = "tushare_dividend+adj_factor"
	marketSummaryV150CorporateActionStatusOK        = "ok"
	marketSummaryV150CorporateActionStatusEmpty     = "empty"
	marketSummaryV150CorporateActionStatusFailed    = "failed"
	marketSummaryV150CorporateActionCoverageType    = "coverage"
	marketSummaryV150CorporateActionDividendType    = "dividend"
	marketSummaryV150CorporateActionRetryInterval   = 30 * time.Second
)

// MarketSummaryV150CorporateActionCoverageError is the structured fail-closed
// reason surfaced by the per-symbol execution monitor and data-health state.
type MarketSummaryV150CorporateActionCoverageError struct {
	Code   string
	Symbol string
	Day    string
	At     time.Time
	Status string
	Cause  string
}

func (err *MarketSummaryV150CorporateActionCoverageError) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%s: symbol=%s day=%s at=%s status=%s cause=%s",
		firstNonEmptyText(strings.TrimSpace(err.Code), "corporate_action_coverage_invalid"),
		normalizeRecommendStockCode(err.Symbol), strings.TrimSpace(err.Day), err.At.Format(time.RFC3339Nano),
		strings.TrimSpace(err.Status), strings.TrimSpace(err.Cause))
}

type marketSummaryV150CorporateActionFact struct {
	Symbol      string
	CoverageDay time.Time
	Status      string
	Source      string
	SourceAt    time.Time
	ErrorCode   string
	Error       string
	Actions     []marketSummaryV150CorporateActionFactAction
}

type marketSummaryV150CorporateActionFactAction struct {
	Core              v150.CorporateAction
	AnnouncedAt       time.Time
	RecordDate        *time.Time
	PayDate           *time.Time
	CashDividendGross float64
	PreviousFactor    float64
	CurrentFactor     float64
}

type marketSummaryV150CorporateActionObservationPayload struct {
	Kind        string                                       `json:"kind"`
	OriginRunID string                                       `json:"originRunId"`
	Symbol      string                                       `json:"symbol"`
	CoverageDay string                                       `json:"coverageDay"`
	Status      string                                       `json:"status"`
	Source      string                                       `json:"source"`
	SourceAt    string                                       `json:"sourceAt"`
	AvailableAt string                                       `json:"availableAt"`
	ErrorCode   string                                       `json:"errorCode,omitempty"`
	Error       string                                       `json:"error,omitempty"`
	Actions     []marketSummaryV150CorporateActionFactAction `json:"actions,omitempty"`
}

var marketSummaryV150CorporateActionNow = time.Now
var fetchMarketSummaryV150CorporateActionFactFn = fetchMarketSummaryV150CorporateActionFact

// refreshMarketSummaryV150CorporateActionObservation is the sole online I/O
// boundary. Historical/cache-only callers pass allowRefresh=false and neither
// call a provider nor write a row. A failed provider response is itself frozen
// as failed coverage before the typed error is returned.
func refreshMarketSummaryV150CorporateActionObservation(originRunID, symbol string, coverageDay time.Time, allowRefresh bool) (string, error) {
	if !allowRefresh {
		return "", nil
	}
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return "", err
	}
	originRunID = strings.TrimSpace(originRunID)
	symbol = normalizeRecommendStockCode(symbol)
	coverageDay = normalizeDailyTradeDate(coverageDay)
	startedAt := marketSummaryV150CorporateActionNow().In(cnLocation())
	if originRunID == "" || symbol == "" || coverageDay.IsZero() || startedAt.IsZero() {
		return "", errors.New("corporate action observation identity/timeline is incomplete")
	}
	if !coverageDay.Equal(normalizeDailyTradeDate(startedAt)) {
		return "", &MarketSummaryV150CorporateActionCoverageError{
			Code: "corporate_action_historical_refresh_forbidden", Symbol: symbol,
			Day: coverageDay.Format(time.DateOnly), At: startedAt, Status: marketSummaryV150CorporateActionStatusFailed,
			Cause: "online refresh may observe only the wall-clock trading day",
		}
	}
	// A successful or explicitly empty same-day observation is immutable and is
	// reused by later scheduler passes. Failed observations are rate-limited by
	// one monitor interval, then retried so a transient 09:29 provider failure
	// cannot strand every affected symbol for the full trading day.
	if runID, cachedErr, found := loadCachedMarketSummaryV150CorporateActionObservation(symbol, coverageDay, startedAt); found {
		return runID, cachedErr
	}

	fact, fetchErr := fetchMarketSummaryV150CorporateActionFactFn(symbol, coverageDay, startedAt)
	availableAt := marketSummaryV150CorporateActionNow().In(cnLocation())
	if availableAt.Before(startedAt) {
		availableAt = startedAt
	}
	if fetchErr != nil {
		fact = marketSummaryV150CorporateActionFact{
			Symbol: symbol, CoverageDay: coverageDay, Status: marketSummaryV150CorporateActionStatusFailed,
			Source: marketSummaryV150CorporateActionSource, SourceAt: startedAt,
			ErrorCode: "corporate_action_provider_failed", Error: fetchErr.Error(),
		}
	}
	runID, appendErr := appendMarketSummaryV150CorporateActionObservation(originRunID, fact, startedAt, availableAt)
	if appendErr != nil {
		return "", errors.Join(fetchErr, appendErr)
	}
	if fetchErr != nil || strings.EqualFold(fact.Status, marketSummaryV150CorporateActionStatusFailed) {
		cause := fact.Error
		if cause == "" && fetchErr != nil {
			cause = fetchErr.Error()
		}
		return runID, &MarketSummaryV150CorporateActionCoverageError{
			Code: firstNonEmptyText(fact.ErrorCode, "corporate_action_coverage_failed"), Symbol: symbol,
			Day: coverageDay.Format(time.DateOnly), At: availableAt, Status: marketSummaryV150CorporateActionStatusFailed, Cause: cause,
		}
	}
	return runID, nil
}

func loadCachedMarketSummaryV150CorporateActionObservation(symbol string, coverageDay, at time.Time) (string, error, bool) {
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.CorporateActionEvent{}) || !db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) {
		return "", nil, false
	}
	var rows []models.CorporateActionEvent
	if err := db.Dao.Model(&models.CorporateActionEvent{}).
		Where("snapshot_version = ? AND upper(symbol) = ? AND action_type = ? AND frozen_at IS NOT NULL", v150.StrategyVersion, symbol, marketSummaryV150CorporateActionCoverageType).
		Find(&rows).Error; err != nil {
		return "", err, true
	}
	type cachedObservation struct {
		row models.CorporateActionEvent
		run models.StrategyRunSnapshot
	}
	candidates := make([]cachedObservation, 0, len(rows))
	for _, row := range rows {
		if !normalizeDailyTradeDate(row.ExDate).Equal(coverageDay) || row.AvailableAt == nil || row.AvailableAt.After(at) ||
			row.SourceAt == nil || row.SourceAt.IsZero() || row.SourceAt.After(*row.AvailableAt) || row.FrozenAt == nil || row.FrozenAt.After(at) {
			continue
		}
		var run models.StrategyRunSnapshot
		if err := db.Dao.Where("run_id = ? AND strategy_version = ? AND mode = ? AND frozen_at IS NOT NULL", row.RunID, v150.StrategyVersion, marketSummaryV150CorporateActionObservationMode).First(&run).Error; err != nil {
			continue
		}
		if run.FrozenAt == nil || run.FrozenAt.After(at) || run.DataCutoffAt.After(at) || run.DecisionAt.After(at) || run.TradeDate != coverageDay.Format(time.DateOnly) {
			continue
		}
		candidates = append(candidates, cachedObservation{row: row, run: run})
	}
	if len(candidates) == 0 {
		return "", nil, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := *candidates[i].row.AvailableAt, *candidates[j].row.AvailableAt
		if !left.Equal(right) {
			return left.After(right)
		}
		return candidates[i].row.ID > candidates[j].row.ID
	})
	selected := candidates[0].row
	status := strings.ToLower(strings.TrimSpace(selected.ObservationStatus))
	switch status {
	case marketSummaryV150CorporateActionStatusOK, marketSummaryV150CorporateActionStatusEmpty:
		return selected.RunID, nil, true
	case marketSummaryV150CorporateActionStatusFailed:
		var payload marketSummaryV150CorporateActionObservationPayload
		_ = json.Unmarshal([]byte(selected.PayloadJSON), &payload)
		if selected.AvailableAt != nil && at.Sub(*selected.AvailableAt) >= marketSummaryV150CorporateActionRetryInterval {
			return "", nil, false
		}
		return selected.RunID, corporateActionCoverageError(
			firstNonEmptyText(payload.ErrorCode, "corporate_action_coverage_failed"),
			symbol, coverageDay, at, status, firstNonEmptyText(payload.Error, "provider coverage failed"),
		), true
	default:
		return selected.RunID, corporateActionCoverageError(
			"corporate_action_status_invalid", symbol, coverageDay, at, status, "unknown cached coverage status",
		), true
	}
}

func appendMarketSummaryV150CorporateActionObservation(originRunID string, fact marketSummaryV150CorporateActionFact, startedAt, availableAt time.Time) (string, error) {
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return "", err
	}
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) || !db.Dao.Migrator().HasTable(&models.CorporateActionEvent{}) {
		return "", errors.New("immutable corporate action tables are unavailable")
	}
	fact.Symbol = normalizeRecommendStockCode(fact.Symbol)
	fact.CoverageDay = normalizeDailyTradeDate(fact.CoverageDay)
	fact.Status = strings.ToLower(strings.TrimSpace(fact.Status))
	fact.Source = firstNonEmptyText(strings.TrimSpace(fact.Source), marketSummaryV150CorporateActionSource)
	if fact.SourceAt.IsZero() {
		fact.SourceAt = startedAt
	}
	if fact.Symbol == "" || fact.CoverageDay.IsZero() || startedAt.IsZero() || availableAt.IsZero() || availableAt.Before(startedAt) || fact.SourceAt.After(availableAt) {
		return "", errors.New("corporate action observation has an invalid identity/timeline")
	}
	switch fact.Status {
	case marketSummaryV150CorporateActionStatusOK:
		if len(fact.Actions) == 0 {
			return "", errors.New("ok corporate action coverage has no action")
		}
	case marketSummaryV150CorporateActionStatusEmpty:
		if len(fact.Actions) != 0 {
			return "", errors.New("empty corporate action coverage contains actions")
		}
	case marketSummaryV150CorporateActionStatusFailed:
		if strings.TrimSpace(fact.ErrorCode) == "" || strings.TrimSpace(fact.Error) == "" {
			return "", errors.New("failed corporate action coverage lacks a structured error")
		}
	default:
		return "", fmt.Errorf("unknown corporate action coverage status %q", fact.Status)
	}
	for index := range fact.Actions {
		fact.Actions[index].Core.AvailableAt = availableAt
	}

	payload := marketSummaryV150CorporateActionObservationPayload{
		Kind: marketSummaryV150CorporateActionObservationMode, OriginRunID: strings.TrimSpace(originRunID),
		Symbol: fact.Symbol, CoverageDay: fact.CoverageDay.Format(time.DateOnly), Status: fact.Status,
		Source: fact.Source, SourceAt: fact.SourceAt.Format(time.RFC3339Nano), AvailableAt: availableAt.Format(time.RFC3339Nano),
		ErrorCode: fact.ErrorCode, Error: fact.Error, Actions: fact.Actions,
	}
	payloadJSON, inputHash, err := marshalMarketSummaryV150FrozenPayload(payload)
	if err != nil {
		return "", err
	}
	runID := marketSummaryV150CorporateActionObservationMode + "|" + fact.Symbol + "|" + inputHash[:24]
	runPayload, _, err := marshalMarketSummaryV150FrozenPayload(struct {
		Observation marketSummaryV150CorporateActionObservationPayload `json:"observation"`
	}{Observation: payload})
	if err != nil {
		return "", err
	}
	frozenAt := availableAt
	coverageStart, coverageEnd := fact.CoverageDay, fact.CoverageDay
	announcedAt := fact.SourceAt
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: fact.CoverageDay.Format(time.DateOnly),
			RunSlot: marketSummaryV150CorporateActionObservationMode, StartedAt: startedAt, AsOf: startedAt,
			DataCutoffAt: availableAt, DecisionAt: availableAt, GeneratedAt: availableAt,
			Mode: marketSummaryV150CorporateActionObservationMode, ConfigHash: v150.FixedStrategyV150ConfigHash(),
			InputHash: inputHash, PayloadJSON: runPayload, FrozenAt: &frozenAt,
		},
		CorporateActions: []models.CorporateActionEvent{{
			EventID: runID + "|coverage", RunID: runID, SnapshotVersion: v150.StrategyVersion,
			Symbol: fact.Symbol, ActionType: marketSummaryV150CorporateActionCoverageType,
			AnnouncedAt: &announcedAt, SourceAt: &fact.SourceAt, AvailableAt: &availableAt,
			ObservationStatus: fact.Status, CoverageStart: &coverageStart, CoverageEnd: &coverageEnd,
			ExDate: fact.CoverageDay, Currency: "CNY", Source: fact.Source,
			PayloadJSON: payloadJSON, FrozenAt: &frozenAt,
		}},
	}
	for index := range fact.Actions {
		action := fact.Actions[index]
		core := action.Core
		if normalizeRecommendStockCode(core.Symbol) != fact.Symbol || !normalizeDailyTradeDate(core.ExDate).Equal(fact.CoverageDay) || core.AvailableAt.After(availableAt) || core.AdjustmentFactor <= 0 {
			return "", fmt.Errorf("corporate action %d does not match observation coverage", index)
		}
		actionPayload, _, marshalErr := marshalMarketSummaryV150FrozenPayload(action)
		if marshalErr != nil {
			return "", marshalErr
		}
		announced := action.AnnouncedAt
		bundle.CorporateActions = append(bundle.CorporateActions, models.CorporateActionEvent{
			EventID: core.EventID, RunID: runID, SnapshotVersion: v150.StrategyVersion, Symbol: fact.Symbol,
			ActionType: marketSummaryV150CorporateActionDividendType, AnnouncedAt: &announced,
			SourceAt: &fact.SourceAt, AvailableAt: &availableAt, ObservationStatus: marketSummaryV150CorporateActionStatusOK,
			CoverageStart: &coverageStart, CoverageEnd: &coverageEnd, ExDate: core.ExDate,
			RecordDate: action.RecordDate, PayDate: action.PayDate, CashDividend: core.CashDividend,
			SplitRatio: core.SplitRatio, BonusRatio: core.BonusRatio, RightsRatio: core.RightsRatio,
			RightsPrice: core.RightsPrice, AdjustmentFactor: core.AdjustmentFactor,
			Currency: "CNY", Source: fact.Source, PayloadJSON: actionPayload, FrozenAt: &frozenAt,
		})
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		return "", fmt.Errorf("seal corporate action observation: %w", err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		var existing models.StrategyRunSnapshot
		lookupErr := db.Dao.Where("run_id = ? AND strategy_version = ? AND input_hash = ? AND frozen_at IS NOT NULL", runID, v150.StrategyVersion, inputHash).First(&existing).Error
		if lookupErr == nil {
			return runID, nil
		}
		return "", fmt.Errorf("append corporate action observation: %w", err)
	}
	return runID, nil
}

func fetchMarketSummaryV150CorporateActionFact(symbol string, coverageDay, observedAt time.Time) (marketSummaryV150CorporateActionFact, error) {
	fact := marketSummaryV150CorporateActionFact{
		Symbol: normalizeRecommendStockCode(symbol), CoverageDay: normalizeDailyTradeDate(coverageDay),
		Source: marketSummaryV150CorporateActionSource, SourceAt: observedAt,
	}
	setting := GetSettingConfig()
	if setting == nil {
		return fact, errors.New("settings are unavailable")
	}
	timeout := int64(10)
	if setting.CrawlTimeOut > 0 {
		timeout = setting.CrawlTimeOut
	}
	dividends, factors, previousRawClose, err := NewTushareApi(setting).GetCorporateActionInputs(fact.Symbol, fact.CoverageDay, timeout)
	if err != nil {
		return fact, err
	}
	previousFactor, currentFactor, err := marketSummaryV150CorporateActionFactorPair(factors, fact.CoverageDay)
	if err != nil {
		return fact, err
	}
	priceFactor := previousFactor / currentFactor
	if priceFactor <= 0 || math.IsNaN(priceFactor) || math.IsInf(priceFactor, 0) {
		return fact, errors.New("corporate action adjustment factor is invalid")
	}

	if len(dividends) == 0 {
		if math.Abs(priceFactor-1) > 1e-10 {
			fact.Status = marketSummaryV150CorporateActionStatusFailed
			fact.ErrorCode = "corporate_action_unclassified_factor_change"
			fact.Error = fmt.Sprintf("adj_factor changed %.12f -> %.12f without a classifiable dividend/bonus event; rights participation is unresolved", previousFactor, currentFactor)
			return fact, nil
		}
		fact.Status = marketSummaryV150CorporateActionStatusEmpty
		return fact, nil
	}

	action := marketSummaryV150CorporateActionFactAction{PreviousFactor: previousFactor, CurrentFactor: currentFactor}
	for _, dividend := range dividends {
		if !strings.EqualFold(strings.TrimSpace(dividend.TSCode), fact.Symbol) || !normalizeDailyTradeDate(dividend.ExDate).Equal(fact.CoverageDay) {
			return fact, errors.New("tushare dividend response does not match symbol/ex-date")
		}
		if strings.TrimSpace(dividend.Process) != "实施" && !strings.EqualFold(strings.TrimSpace(dividend.Process), "implemented") {
			return fact, fmt.Errorf("dividend implementation status is not final: %s", dividend.Process)
		}
		announced := dividend.ImplementationDate
		if announced.IsZero() {
			announced = dividend.AnnDate
		}
		if announced.IsZero() || announced.After(observedAt) {
			return fact, errors.New("dividend implementation announcement is missing or non-causal")
		}
		if action.AnnouncedAt.IsZero() || announced.After(action.AnnouncedAt) {
			action.AnnouncedAt = announced
		}
		if !dividend.RecordDate.IsZero() {
			value := dividend.RecordDate
			action.RecordDate = &value
		}
		if !dividend.PayDate.IsZero() {
			value := dividend.PayDate
			action.PayDate = &value
		}
		if dividend.CashDividendGross > 0 && dividend.CashDividend <= 0 {
			return fact, errors.New("after-tax cash dividend is unavailable; gross dividend cannot be credited silently")
		}
		action.Core.CashDividend += dividend.CashDividend
		action.CashDividendGross += dividend.CashDividendGross
		bonus, transfer := dividend.BonusRatio, dividend.TransferRatio
		if bonus+transfer <= 0 && dividend.StockDividend > 0 {
			bonus = dividend.StockDividend
		}
		action.Core.BonusRatio += bonus
		action.Core.SplitRatio += transfer
	}
	if action.Core.CashDividend <= 0 && action.Core.BonusRatio <= 0 && action.Core.SplitRatio <= 0 {
		return fact, errors.New("implemented corporate action has no usable entitlement")
	}
	if math.Abs(priceFactor-1) <= 1e-10 {
		return fact, errors.New("implemented corporate action has no ex-date adjustment factor change")
	}
	grossCash := action.CashDividendGross
	if grossCash <= 0 {
		grossCash = action.Core.CashDividend
	}
	shareMultiplier := 1 + action.Core.BonusRatio + action.Core.SplitRatio
	if previousRawClose <= 0 || previousRawClose <= grossCash || shareMultiplier <= 0 {
		return fact, errors.New("corporate action reconciliation lacks a valid prior raw close/entitlement")
	}
	expectedPriceFactor := (previousRawClose - grossCash) / (previousRawClose * shareMultiplier)
	if expectedPriceFactor <= 0 || math.Abs(priceFactor/expectedPriceFactor-1) > 0.002 {
		fact.Status = marketSummaryV150CorporateActionStatusFailed
		fact.ErrorCode = "corporate_action_factor_unreconciled"
		fact.Error = fmt.Sprintf("observed factor %.12f does not reconcile to cash/bonus-only factor %.12f; possible rights/unknown action requires an explicit policy", priceFactor, expectedPriceFactor)
		return fact, nil
	}
	action.Core.Symbol = fact.Symbol
	action.Core.ExDate = fact.CoverageDay
	action.Core.AvailableAt = observedAt
	action.Core.AdjustmentFactor = priceFactor
	action.Core.EventID = marketSummaryV150CorporateActionEventID(fact.Symbol, fact.CoverageDay, action)
	fact.Status = marketSummaryV150CorporateActionStatusOK
	fact.Actions = []marketSummaryV150CorporateActionFactAction{action}
	return fact, nil
}

func marketSummaryV150CorporateActionFactorPair(factors []TushareAdjustmentFactor, day time.Time) (float64, float64, error) {
	day = normalizeDailyTradeDate(day)
	ordered := append([]TushareAdjustmentFactor(nil), factors...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].TradeDate.Before(ordered[j].TradeDate) })
	previous, current := 0.0, 0.0
	for _, row := range ordered {
		rowDay := normalizeDailyTradeDate(row.TradeDate)
		if row.Factor <= 0 || rowDay.After(day) {
			continue
		}
		if rowDay.Equal(day) {
			current = row.Factor
			continue
		}
		previous = row.Factor
	}
	if previous <= 0 || current <= 0 {
		return 0, 0, fmt.Errorf("adj_factor coverage is incomplete for %s (previous=%.8f current=%.8f)", day.Format(time.DateOnly), previous, current)
	}
	return previous, current, nil
}

func marketSummaryV150CorporateActionEventID(symbol string, day time.Time, action marketSummaryV150CorporateActionFactAction) string {
	payload, _ := json.Marshal(action)
	digest := sha256.Sum256(append([]byte(normalizeRecommendStockCode(symbol)+"|"+day.Format(time.DateOnly)+"|"), payload...))
	return "v150-corporate-action-" + hex.EncodeToString(digest[:20])
}

// loadMarketSummaryV150CorporateActions reads frozen observations only. The
// selected run must explicitly cover the bar's trading day and have become
// available/frozen no later than the first bar being evaluated.
func loadMarketSummaryV150CorporateActions(symbol string, day, at time.Time) ([]v150.CorporateAction, error) {
	symbol = normalizeRecommendStockCode(symbol)
	day = normalizeDailyTradeDate(day)
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.CorporateActionEvent{}) || !db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) {
		return nil, corporateActionCoverageError("corporate_action_cache_unavailable", symbol, day, at, "missing", "immutable corporate action cache is unavailable")
	}
	if symbol == "" || day.IsZero() || at.IsZero() {
		return nil, corporateActionCoverageError("corporate_action_lookup_invalid", symbol, day, at, "missing", "symbol/day/event time is incomplete")
	}
	var coverageRows []models.CorporateActionEvent
	if err := db.Dao.Model(&models.CorporateActionEvent{}).
		Where("snapshot_version = ? AND upper(symbol) = ? AND action_type = ? AND frozen_at IS NOT NULL", v150.StrategyVersion, symbol, marketSummaryV150CorporateActionCoverageType).
		Find(&coverageRows).Error; err != nil {
		return nil, err
	}
	type candidate struct {
		row models.CorporateActionEvent
		run models.StrategyRunSnapshot
	}
	candidates := make([]candidate, 0, len(coverageRows))
	for _, row := range coverageRows {
		if !normalizeDailyTradeDate(row.ExDate).Equal(day) {
			continue
		}
		if row.AvailableAt == nil || row.AvailableAt.IsZero() || row.AvailableAt.After(at) || row.FrozenAt == nil || row.FrozenAt.After(at) ||
			row.SourceAt == nil || row.SourceAt.IsZero() || row.SourceAt.After(*row.AvailableAt) ||
			row.CoverageStart == nil || row.CoverageEnd == nil || day.Before(*row.CoverageStart) || day.After(*row.CoverageEnd) {
			continue
		}
		var run models.StrategyRunSnapshot
		if err := db.Dao.Where("run_id = ? AND strategy_version = ? AND mode = ? AND frozen_at IS NOT NULL", row.RunID, v150.StrategyVersion, marketSummaryV150CorporateActionObservationMode).First(&run).Error; err != nil {
			continue
		}
		if run.FrozenAt == nil || run.FrozenAt.After(at) || run.DataCutoffAt.After(at) || run.DecisionAt.After(at) || run.TradeDate != day.Format(time.DateOnly) {
			continue
		}
		candidates = append(candidates, candidate{row: row, run: run})
	}
	if len(candidates) == 0 {
		return nil, corporateActionCoverageError("corporate_action_observation_missing", symbol, day, at, "missing", "no causally available same-day coverage observation")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := *candidates[i].row.AvailableAt, *candidates[j].row.AvailableAt
		if !left.Equal(right) {
			return left.After(right)
		}
		return candidates[i].row.ID > candidates[j].row.ID
	})
	selected := candidates[0].row
	status := strings.ToLower(strings.TrimSpace(selected.ObservationStatus))
	if status == marketSummaryV150CorporateActionStatusFailed {
		var payload marketSummaryV150CorporateActionObservationPayload
		_ = json.Unmarshal([]byte(selected.PayloadJSON), &payload)
		return nil, corporateActionCoverageError(firstNonEmptyText(payload.ErrorCode, "corporate_action_coverage_failed"), symbol, day, at, status, firstNonEmptyText(payload.Error, "provider coverage failed"))
	}
	if status == marketSummaryV150CorporateActionStatusEmpty {
		return []v150.CorporateAction{}, nil
	}
	if status != marketSummaryV150CorporateActionStatusOK {
		return nil, corporateActionCoverageError("corporate_action_status_invalid", symbol, day, at, status, "unknown coverage status")
	}
	var rows []models.CorporateActionEvent
	if err := db.Dao.Model(&models.CorporateActionEvent{}).
		Where("run_id = ? AND action_type <> ? AND frozen_at IS NOT NULL", selected.RunID, marketSummaryV150CorporateActionCoverageType).
		Order("event_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, corporateActionCoverageError("corporate_action_rows_missing", symbol, day, at, status, "ok coverage has no immutable action row")
	}
	actions := make([]v150.CorporateAction, 0, len(rows))
	for _, row := range rows {
		if !normalizeDailyTradeDate(row.ExDate).Equal(day) {
			continue
		}
		if row.AvailableAt == nil || row.AvailableAt.After(at) || row.FrozenAt == nil || row.FrozenAt.After(at) || row.AdjustmentFactor <= 0 {
			return nil, corporateActionCoverageError("corporate_action_row_noncausal", symbol, day, at, status, "action row is late or lacks a real factor")
		}
		actions = append(actions, v150.CorporateAction{
			EventID: row.EventID, Symbol: row.Symbol, ExDate: row.ExDate, AvailableAt: *row.AvailableAt,
			AdjustmentFactor: row.AdjustmentFactor, CashDividend: row.CashDividend,
			SplitRatio: row.SplitRatio, BonusRatio: row.BonusRatio, RightsRatio: row.RightsRatio, RightsPrice: row.RightsPrice,
		})
	}
	if len(actions) == 0 {
		return nil, corporateActionCoverageError("corporate_action_rows_missing", symbol, day, at, status, "ok coverage has no immutable ex-date action row")
	}
	return actions, nil
}

func corporateActionCoverageError(code, symbol string, day, at time.Time, status, cause string) error {
	return &MarketSummaryV150CorporateActionCoverageError{
		Code: code, Symbol: symbol, Day: day.Format(time.DateOnly), At: at, Status: status, Cause: cause,
	}
}
