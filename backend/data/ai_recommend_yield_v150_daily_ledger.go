package data

import (
	"context"
	"encoding/json"
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
	v150YieldDailyLedgerMissingHealthCode       = "daily_order_ledger_missing_or_invalid"
	v150YieldDailyLedgerAsOfHealthCode          = "daily_order_ledger_not_causally_visible"
	v150YieldDailyRawMinutePriceHealthCode      = "holding_daily_raw_minute_close_missing"
	v150YieldDailyRawMinuteProvenanceHealthCode = "holding_daily_raw_minute_provenance_invalid"
	v150YieldDailyCohortReplayID                = "yield-daily-cohort|1.5.0"
)

// v150YieldDailyOrderLedger is a read-only view of one recommendation's
// immutable lifecycle. Events are limited to facts that were already sealed at
// reportAsOf and the complete prefix is hash-verified by the replay engine.
// Each historical portfolio point applies a second, stricter per-day as-of
// filter before replaying that prefix.
type v150YieldDailyOrderLedger struct {
	RunID      string
	RuleID     string
	Symbol     string
	ReportAsOf time.Time
	Events     []models.OrderEvent
	// AllEvents is used only to detect a fact whose event time is already in
	// the point window but whose immutable seal was not yet visible. Such a day
	// is omitted rather than valued with a pre-action quantity or pre-exit state.
	AllEvents []models.OrderEvent
}

type v150YieldDailyLedgerValue struct {
	EntryCash           float64
	NetValue            float64
	Quantity            float64
	CorporateActionCash float64
	Holding             bool
	DailyCostEligible   bool
}

type v150YieldDailyLedgerRequest struct {
	Key         string
	RunID       string
	RuleID      string
	Symbol      string
	WarningKey  string
	RequireFill bool
}

// loadV150YieldDailyOrderLedgersByRule is the only loader used by the V1.5
// daily API. Frozen rules are the enumeration source, so an optional mutable
// display projection (and its numeric RecommendID) cannot hide or alias a
// ledger.
func loadV150YieldDailyOrderLedgersByRule(
	views []v150YieldLedgerView,
	reportAsOf time.Time,
) (map[string]v150YieldDailyOrderLedger, []string) {
	requests := make([]v150YieldDailyLedgerRequest, 0, len(views))
	seen := make(map[string]struct{}, len(views))
	warnings := make([]string, 0)
	for _, view := range views {
		frozen := view.Current.Frozen
		ruleID := strings.TrimSpace(frozen.RuleID)
		runID := strings.TrimSpace(frozen.RunID)
		symbol := normalizeRecommendStockCode(frozen.Symbol)
		warningKey := ruleID
		if warningKey == "" {
			warningKey = symbol
		}
		if ruleID == "" || runID == "" || symbol == "" || frozen.StrategyVersion != v150.StrategyVersion {
			warnings = append(warnings, strings.TrimSpace(warningKey)+":"+v150YieldDailyLedgerMissingHealthCode)
			continue
		}
		if _, duplicate := seen[ruleID]; duplicate {
			warnings = append(warnings, ruleID+":"+v150YieldDailyLedgerMissingHealthCode)
			continue
		}
		seen[ruleID] = struct{}{}
		status := strings.ToLower(strings.TrimSpace(string(view.Current.Lifecycle.Status)))
		requests = append(requests, v150YieldDailyLedgerRequest{
			Key: ruleID, RunID: runID, RuleID: ruleID, Symbol: symbol, WarningKey: ruleID,
			RequireFill: status == "holding" || status == "closed",
		})
	}
	loaded, loadWarnings := loadV150YieldDailyOrderLedgersForRequests(requests, reportAsOf)
	return loaded, dedupeNonEmptyStrings(append(warnings, loadWarnings...), 0)
}

func loadV150YieldDailyOrderLedgersForRequests(
	requests []v150YieldDailyLedgerRequest,
	reportAsOf time.Time,
) (map[string]v150YieldDailyOrderLedger, []string) {
	result := make(map[string]v150YieldDailyOrderLedger, len(requests))
	if len(requests) == 0 {
		return result, nil
	}
	if reportAsOf.IsZero() {
		reportAsOf = timeNow().In(cnLocation())
	}
	warnings := make([]string, 0)
	warn := func(request v150YieldDailyLedgerRequest) {
		key := strings.TrimSpace(request.WarningKey)
		if key == "" {
			key = strings.TrimSpace(request.Key)
		}
		warnings = append(warnings, key+":"+v150YieldDailyLedgerMissingHealthCode)
	}
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.OrderEvent{}) {
		for _, request := range requests {
			warn(request)
		}
		return result, dedupeNonEmptyStrings(warnings, 0)
	}
	if !validateV150YieldDailyCohort(reportAsOf) {
		for _, request := range requests {
			warn(request)
		}
		return result, dedupeNonEmptyStrings(warnings, 0)
	}

	runSet := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		runSet[request.RunID] = struct{}{}
	}
	runIDs := make([]string, 0, len(runSet))
	for runID := range runSet {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	rows := make([]models.OrderEvent, 0)
	if err := db.Dao.Model(&models.OrderEvent{}).
		Where("strategy_version = ? AND run_id IN ?", v150.StrategyVersion, runIDs).
		Order("run_id ASC, rule_id ASC, sequence ASC, event_id ASC").
		Find(&rows).Error; err != nil {
		for _, request := range requests {
			warn(request)
		}
		return result, dedupeNonEmptyStrings(warnings, 0)
	}
	rowsByIdentity := make(map[string][]models.OrderEvent)
	for _, row := range rows {
		key := v150YieldDailyLedgerIdentity(row.RunID, row.RuleID, row.Symbol)
		rowsByIdentity[key] = append(rowsByIdentity[key], row)
	}

	for _, request := range requests {
		allRows := rowsByIdentity[v150YieldDailyLedgerIdentity(request.RunID, request.RuleID, request.Symbol)]
		visible := make([]models.OrderEvent, 0, len(allRows))
		for _, row := range allRows {
			if row.EventAt.After(reportAsOf) || row.FrozenAt == nil || row.FrozenAt.After(reportAsOf) {
				continue
			}
			visible = append(visible, row)
		}
		if len(visible) == 0 {
			warn(request)
			continue
		}
		trades, _, _, err := persistence.ReplayFrozenOrderEvents(
			"yield-daily-ledger|"+request.RunID+"|"+request.RuleID,
			v150.StrategyVersion,
			visible,
			reportAsOf,
		)
		if err != nil || (request.RequireFill && len(trades) != 1) {
			warn(request)
			continue
		}
		result[request.Key] = v150YieldDailyOrderLedger{
			RunID: request.RunID, RuleID: request.RuleID, Symbol: request.Symbol, ReportAsOf: reportAsOf,
			Events: append([]models.OrderEvent(nil), visible...), AllEvents: append([]models.OrderEvent(nil), allRows...),
		}
	}
	return result, dedupeNonEmptyStrings(warnings, 0)
}

// validateV150YieldDailyCohort replays every frozen V1.5.0 run from the
// cohort's first trading day through reportAsOf. The query being rendered is
// deliberately not an input: a hidden rule must still be able to invalidate
// the portfolio when it violates a global entry, concentration or cooldown
// constraint. Missing policy metadata is also unsafe even when accounting
// replay itself succeeds.
func validateV150YieldDailyCohort(reportAsOf time.Time) bool {
	if db.Dao == nil || reportAsOf.IsZero() || !db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) {
		return false
	}
	cutoffDay := reportAsOf.In(cnLocation()).Format(time.DateOnly)
	var earliest models.StrategyRunSnapshot
	if err := db.Dao.Model(&models.StrategyRunSnapshot{}).
		Select("trade_date").
		Where("strategy_version = ? AND frozen_at IS NOT NULL AND frozen_at <= ? AND trade_date <= ?", v150.StrategyVersion, reportAsOf, cutoffDay).
		Order("trade_date ASC, run_id ASC").
		Take(&earliest).Error; err != nil {
		return false
	}
	cohortStart, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(earliest.TradeDate), cnLocation())
	if err != nil {
		return false
	}
	inputs, err := persistence.LoadFrozenStrategyInputsAsOf(
		context.Background(),
		db.Dao,
		v150.StrategyVersion,
		cohortStart,
		reportAsOf.In(cnLocation()),
		reportAsOf,
	)
	if err != nil {
		return false
	}
	inputs = v150YieldDailyDecisionCohortInputs(inputs)
	if len(inputs.Runs) == 0 {
		return false
	}
	_, stats, _, err := persistence.ReplayFrozenStrategyInputs(
		v150YieldDailyCohortReplayID,
		v150.StrategyVersion,
		inputs,
		reportAsOf,
	)
	return err == nil && stats.PolicyValidated
}

// Auxiliary security/corporate-action observation envelopes share the
// strategy persistence tables and version, but they are market-data facts,
// not recommendation decisions. Their payload intentionally has no daily-cap
// policy, so including them in the policy cohort would make every otherwise
// valid production replay report PolicyValidated=false.
func v150YieldDailyDecisionCohortInputs(inputs persistence.FrozenStrategyInputs) persistence.FrozenStrategyInputs {
	excluded := make(map[string]struct{})
	result := persistence.FrozenStrategyInputs{
		Runs: make([]models.StrategyRunSnapshot, 0, len(inputs.Runs)),
	}
	for _, run := range inputs.Runs {
		mode := strings.TrimSpace(run.Mode)
		if strings.EqualFold(mode, persistence.StrategyRunModeExecutionSecurityObservation) ||
			strings.EqualFold(mode, persistence.StrategyRunModeExecutionCorporateActionObservation) {
			excluded[run.RunID] = struct{}{}
			continue
		}
		result.Runs = append(result.Runs, run)
	}
	if len(excluded) == 0 {
		return inputs
	}
	for _, row := range inputs.Candidates {
		if _, skip := excluded[row.RunID]; !skip {
			result.Candidates = append(result.Candidates, row)
		}
	}
	for _, row := range inputs.Rules {
		if _, skip := excluded[row.RunID]; !skip {
			result.Rules = append(result.Rules, row)
		}
	}
	for _, row := range inputs.OrderEvents {
		if _, skip := excluded[row.RunID]; !skip {
			result.OrderEvents = append(result.OrderEvents, row)
		}
	}
	for _, row := range inputs.SecurityMaster {
		if _, skip := excluded[row.RunID]; !skip {
			result.SecurityMaster = append(result.SecurityMaster, row)
		}
	}
	for _, row := range inputs.CorporateActions {
		if _, skip := excluded[row.RunID]; !skip {
			result.CorporateActions = append(result.CorporateActions, row)
		}
	}
	return result
}

func loadV150YieldDailyOrderLedgers(
	records []models.AiRecommendStocks,
	reportAsOf time.Time,
) (map[uint]v150YieldDailyOrderLedger, []string) {
	result := make(map[uint]v150YieldDailyOrderLedger)
	requests := make([]v150YieldDailyLedgerRequest, 0, len(records))
	idByKey := make(map[string]uint, len(records))
	warnings := make([]string, 0)
	for _, record := range records {
		if !isV150CostVersion(record.SummaryVersion) {
			continue
		}
		runID := strings.TrimSpace(record.StrategyRunID)
		ruleID := strings.TrimSpace(record.StrategyRuleID)
		symbol := normalizeRecommendStockCode(record.StockCode)
		if record.ID == 0 || runID == "" || ruleID == "" || symbol == "" {
			warnings = append(warnings, v150YieldDailyLedgerWarning(record, v150YieldDailyLedgerMissingHealthCode))
			continue
		}
		key := fmt.Sprintf("recommend:%d", record.ID)
		idByKey[key] = record.ID
		requests = append(requests, v150YieldDailyLedgerRequest{
			Key: key, RunID: runID, RuleID: ruleID, Symbol: symbol,
			WarningKey: normalizeRecommendStockCode(record.StockCode),
		})
	}
	loaded, loadWarnings := loadV150YieldDailyOrderLedgersForRequests(requests, reportAsOf)
	for key, ledger := range loaded {
		if id := idByKey[key]; id != 0 {
			result[id] = ledger
		}
	}
	return result, dedupeNonEmptyStrings(append(warnings, loadWarnings...), 0)
}

func reconcileV150YieldDailyOverviewEntriesWithLedger(
	entries []yieldDailyOverviewEntry,
	ledgers map[uint]v150YieldDailyOrderLedger,
) []yieldDailyOverviewEntry {
	result := append([]yieldDailyOverviewEntry(nil), entries...)
	for index := range result {
		entry := &result[index]
		if !isV150CostVersion(entry.SummaryVersion) {
			continue
		}
		ledger, ok := ledgers[entry.RecommendID]
		if !ok {
			continue
		}
		applyV150YieldDailyLedgerToEntry(entry, ledger)
	}
	return result
}

func applyV150YieldDailyLedgerToEntry(entry *yieldDailyOverviewEntry, ledger v150YieldDailyOrderLedger) bool {
	if entry == nil || strings.TrimSpace(ledger.RuleID) == "" {
		return false
	}
	var fill *models.OrderEvent
	var exit *models.OrderEvent
	corporateCash := 0.0
	ledgerQuantity := 0.0
	for eventIndex := range ledger.Events {
		event := &ledger.Events[eventIndex]
		switch strings.ToLower(strings.TrimSpace(event.EventType)) {
		case string(v150.EventFill):
			if fill == nil {
				fill = event
				ledgerQuantity = event.Quantity
			}
		case string(v150.EventCorporateAction):
			corporateCash += event.CashAmount
			ledgerQuantity = event.Quantity
		case string(v150.EventExitFill):
			exit = event
			ledgerQuantity = event.Quantity
		}
	}
	if fill == nil || fill.EventAt.IsZero() || fill.Price <= 0 || fill.Quantity <= 0 || fill.Fees < 0 {
		return false
	}
	entry.V150RuleID = strings.TrimSpace(ledger.RuleID)
	entry.BuyTime = fill.EventAt.In(cnLocation())
	entry.BuyDay = normalizeYieldOverviewTradeDay(entry.BuyTime)
	entry.BuyAmount = fill.Price
	entry.BuyCostNet = round2(fill.Price*fill.Quantity + fill.Fees)
	entry.V150LedgerAccountingReady = true
	entry.V150LedgerClosed = false
	entry.V150LedgerQuantity = ledgerQuantity
	entry.V150LedgerCorporateCash = corporateCash
	entry.HasSellAmount = false
	entry.SellDay = time.Time{}
	entry.SellTime = ""
	entry.RealizedValueNet = 0
	if exit == nil {
		return true
	}
	if exit.EventAt.IsZero() || exit.Price <= 0 || exit.Quantity <= 0 || exit.Fees < 0 {
		return false
	}
	entry.HasSellAmount = true
	entry.V150LedgerClosed = true
	entry.V150LedgerQuantity = exit.Quantity
	entry.SellAmount = exit.Price
	entry.SellDay = normalizeYieldOverviewTradeDay(exit.EventAt)
	entry.SellTime = exit.EventAt.In(cnLocation()).Format(time.DateTime)
	entry.RealizedValueNet = round2(exit.Price*exit.Quantity - exit.Fees + corporateCash)
	return !entry.SellDay.Before(entry.BuyDay)
}

func buildV150YieldDailyOverviewEntryFromView(
	view v150YieldLedgerView,
	ledger v150YieldDailyOrderLedger,
	valuationDay time.Time,
) (yieldDailyOverviewEntry, bool) {
	frozen := view.Current.Frozen
	status := strings.ToLower(strings.TrimSpace(string(view.Current.Lifecycle.Status)))
	if status != "holding" && status != "closed" {
		return yieldDailyOverviewEntry{}, false
	}
	if strings.TrimSpace(frozen.RuleID) == "" || frozen.RuleID != ledger.RuleID || frozen.RunID != ledger.RunID ||
		normalizeRecommendStockCode(frozen.Symbol) != normalizeRecommendStockCode(ledger.Symbol) {
		return yieldDailyOverviewEntry{}, false
	}
	if valuationDay.IsZero() {
		valuationDay = normalizeYieldOverviewTradeDay(ledger.ReportAsOf)
	}
	entry := yieldDailyOverviewEntry{
		RecommendID: view.Record.ID, V150RuleID: frozen.RuleID,
		SummaryVersion: v150.StrategyVersion, StockCode: normalizeRecommendStockCode(frozen.Symbol),
		StockName: strings.TrimSpace(frozen.Name), CurrentDay: normalizeYieldOverviewTradeDay(valuationDay),
	}
	if !applyV150YieldDailyLedgerToEntry(&entry, ledger) || entry.StockCode == "" || entry.CurrentDay.Before(entry.BuyDay) {
		return yieldDailyOverviewEntry{}, false
	}
	if (status == "closed") != entry.V150LedgerClosed {
		return yieldDailyOverviewEntry{}, false
	}
	return entry, true
}

func buildV150YieldDailyOverviewEntryFromLedger(
	item models.AiRecommendStocksYieldItem,
	ledger v150YieldDailyOrderLedger,
) (yieldDailyOverviewEntry, bool) {
	if !isV150CostVersion(item.SummaryVersion) ||
		(strings.TrimSpace(item.BacktestEligibility) != "" && strings.TrimSpace(item.BacktestEligibility) != recommendBacktestEligible) {
		return yieldDailyOverviewEntry{}, false
	}
	var fill *models.OrderEvent
	for index := range ledger.Events {
		if strings.EqualFold(strings.TrimSpace(ledger.Events[index].EventType), string(v150.EventFill)) {
			fill = &ledger.Events[index]
			break
		}
	}
	if fill == nil || fill.EventAt.IsZero() || fill.Price <= 0 || fill.Quantity <= 0 || fill.Fees < 0 {
		return yieldDailyOverviewEntry{}, false
	}
	entry := yieldDailyOverviewEntry{
		RecommendID: item.RecommendID, SummaryVersion: v150.StrategyVersion,
		StockCode: normalizeRecommendStockCode(fill.Symbol), StockName: strings.TrimSpace(item.StockName),
		BuyTime: fill.EventAt.In(cnLocation()), BuyDay: normalizeYieldOverviewTradeDay(fill.EventAt),
		BuyAmount: fill.Price, BuyCostNet: round2(fill.Price*fill.Quantity + fill.Fees),
		CurrentPrice: round2(item.CurrentPrice), CurrentPriceTime: strings.TrimSpace(item.CurrentPriceTime),
		V150LedgerAccountingReady: true, V150LedgerQuantity: fill.Quantity,
	}
	if currentAt, ok := parseYieldOverviewDisplayTime(item.CurrentPriceTime); ok {
		entry.CurrentDay = normalizeYieldOverviewTradeDay(currentAt)
	}
	if entry.CurrentDay.IsZero() {
		entry.CurrentDay = normalizeYieldOverviewTradeDay(ledger.ReportAsOf)
	}
	if !applyV150YieldDailyLedgerToEntry(&entry, ledger) {
		return yieldDailyOverviewEntry{}, false
	}
	return entry, true
}

// loadV150YieldDailyRawMinutePriceSeries values each historical holding day
// from the final complete 15-minute raw bar (14:45-15:00). Unlike Tencent qfq
// daily rows, these execution-cache bars are never retroactively rewritten
// after an ex-date, so applying the ledger's as-of quantity and dividend cash
// cannot double count a split or cash distribution.
func loadV150YieldDailyRawMinutePriceSeries(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
) (map[string]*yieldDailyOverviewPriceSeries, []string, []string, error) {
	result := make(map[string]*yieldDailyOverviewPriceSeries)
	codeSet := make(map[string]struct{})
	for _, entry := range entries {
		if !isV150CostVersion(entry.SummaryVersion) {
			continue
		}
		code := normalizeRecommendStockCode(entry.StockCode)
		if code != "" {
			codeSet[code] = struct{}{}
		}
	}
	codes := make([]string, 0, len(codeSet))
	for code := range codeSet {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	missingCodes := make([]string, 0)
	provenanceWarnings := make([]string, 0)
	for _, code := range codes {
		series := &yieldDailyOverviewPriceSeries{Code: code, CloseByDay: make(map[string]float64)}
		for _, day := range tradingDays {
			localDay := normalizeYieldOverviewTradeDay(day)
			bucketStart := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 14, 45, 0, 0, cnLocation())
			marketClose := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 15, 0, 0, 0, cnLocation())
			raw, err := listMinuteBarsFromCache(code, bucketStart, marketClose)
			if err != nil {
				return nil, nil, nil, err
			}
			provenanceOK := true
			for _, bar := range raw {
				if !minuteBarSourceProvesUnadjusted(bar.Source) {
					provenanceOK = false
					break
				}
			}
			if !provenanceOK {
				provenanceWarnings = append(
					provenanceWarnings,
					code+":"+v150YieldDailyRawMinuteProvenanceHealthCode+":"+localDay.Format(time.DateOnly),
				)
				continue
			}
			completed, _ := buildMarketSummaryV150CompletedBars(raw, marketClose, bucketStart)
			for _, bar := range completed {
				if bar.Completed && bar.Start.Equal(bucketStart) && bar.Close > 0 {
					series.CloseByDay[localDay.Format(time.DateOnly)] = bar.Close
					break
				}
			}
		}
		// A fresh current quote is itself a raw point-in-time observation. Keep
		// the series entry even when the historical final bar is not cached; the
		// point builder will still reject every other missing day explicitly.
		for _, entry := range entries {
			if normalizeRecommendStockCode(entry.StockCode) == code && entry.CurrentPrice > 0 && !entry.CurrentDay.IsZero() {
				series.CloseByDay[entry.CurrentDay.Format(time.DateOnly)] = entry.CurrentPrice
			}
		}
		if len(series.CloseByDay) == 0 {
			missingCodes = append(missingCodes, code)
			continue
		}
		result[code] = series
	}
	return result, missingCodes, dedupeNonEmptyStrings(provenanceWarnings, 0), nil
}

func resolveV150YieldDailyLedgerValue(
	entry yieldDailyOverviewEntry,
	ledger v150YieldDailyOrderLedger,
	tradeDate string,
	tradeDay time.Time,
	series *yieldDailyOverviewPriceSeries,
) (v150YieldDailyLedgerValue, string) {
	pointAsOf := v150YieldDailyPointAsOf(entry, tradeDay)
	visible := make([]models.OrderEvent, 0, len(ledger.Events))
	lateVisibleFact := false
	allEvents := ledger.AllEvents
	if len(allEvents) == 0 {
		allEvents = ledger.Events
	}
	for _, event := range allEvents {
		if !event.EventAt.After(pointAsOf) && (event.FrozenAt == nil || event.FrozenAt.After(pointAsOf)) {
			lateVisibleFact = true
			break
		}
	}
	for _, event := range ledger.Events {
		if event.EventAt.After(pointAsOf) {
			continue
		}
		if event.FrozenAt == nil || event.FrozenAt.After(pointAsOf) {
			continue
		}
		visible = append(visible, event)
	}
	if lateVisibleFact || len(visible) == 0 {
		return v150YieldDailyLedgerValue{}, v150YieldDailyLedgerAsOfHealthCode
	}
	trades, _, _, err := persistence.ReplayFrozenOrderEvents(
		"yield-daily-point|"+ledger.RunID+"|"+ledger.RuleID+"|"+tradeDate,
		v150.StrategyVersion,
		visible,
		pointAsOf,
	)
	if err != nil || len(trades) != 1 {
		return v150YieldDailyLedgerValue{}, v150YieldDailyLedgerAsOfHealthCode
	}
	trade := trades[0]
	var payload struct {
		EntryCash           float64 `json:"entryCash"`
		CorporateActionCash float64 `json:"corporateActionCash"`
	}
	if json.Unmarshal([]byte(trade.PayloadJSON), &payload) != nil ||
		payload.EntryCash <= 0 || payload.CorporateActionCash < 0 ||
		!v150YieldDailyFinite(payload.EntryCash) || !v150YieldDailyFinite(payload.CorporateActionCash) {
		return v150YieldDailyLedgerValue{}, v150YieldDailyLedgerAsOfHealthCode
	}
	value := v150YieldDailyLedgerValue{
		EntryCash: payload.EntryCash, Quantity: trade.Quantity,
		CorporateActionCash: payload.CorporateActionCash,
	}
	if trade.ExitAt != nil {
		value.NetValue = payload.EntryCash + trade.NetPnL
		value.DailyCostEligible = !normalizeYieldOverviewTradeDay(*trade.ExitAt).Before(tradeDay)
		return value, ""
	}
	if trade.Quantity <= 0 || math.Trunc(trade.Quantity) != trade.Quantity {
		return v150YieldDailyLedgerValue{}, v150YieldDailyLedgerAsOfHealthCode
	}
	markPrice := 0.0
	if !entry.CurrentDay.IsZero() && tradeDay.Equal(entry.CurrentDay) && entry.CurrentPrice > 0 {
		markPrice = entry.CurrentPrice
	}
	if markPrice <= 0 && series != nil {
		markPrice = series.CloseByDay[tradeDate]
	}
	if markPrice <= 0 {
		return v150YieldDailyLedgerValue{}, v150YieldDailyRawMinutePriceHealthCode
	}
	cfg := v150.FixedStrategyV150Config()
	mark := v150.CalculateTradeCost(
		v150.SideSell,
		v150.ResolveMarket(entry.StockCode),
		markPrice,
		int(trade.Quantity),
		cfg.SlippageScenarios()[0],
		cfg,
	)
	if mark.CashFlow <= 0 || !v150YieldDailyFinite(mark.CashFlow) {
		return v150YieldDailyLedgerValue{}, v150YieldDailyRawMinutePriceHealthCode
	}
	value.NetValue = mark.CashFlow + payload.CorporateActionCash
	value.Holding = true
	value.DailyCostEligible = true
	return value, ""
}

func v150YieldDailyPointAsOf(entry yieldDailyOverviewEntry, tradeDay time.Time) time.Time {
	day := normalizeYieldOverviewTradeDay(tradeDay)
	pointAsOf := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, cnLocation())
	if !entry.CurrentDay.IsZero() && day.Equal(entry.CurrentDay) && entry.CurrentPrice > 0 {
		if quoteAt, ok := parseYieldOverviewDisplayTime(entry.CurrentPriceTime); ok && normalizeYieldOverviewTradeDay(quoteAt).Equal(day) {
			pointAsOf = quoteAt.In(cnLocation())
		}
	}
	return pointAsOf
}

func v150YieldDailyLedgerWarning(record models.AiRecommendStocks, code string) string {
	symbol := normalizeRecommendStockCode(record.StockCode)
	if symbol == "" {
		symbol = fmt.Sprintf("recommend:%d", record.ID)
	}
	return symbol + ":" + strings.TrimSpace(code)
}

func v150YieldDailyPointWarning(entry yieldDailyOverviewEntry, tradeDate, code string) string {
	symbol := normalizeRecommendStockCode(entry.StockCode)
	if symbol == "" {
		symbol = fmt.Sprintf("recommend:%d", entry.RecommendID)
	}
	return symbol + ":" + strings.TrimSpace(code) + ":" + strings.TrimSpace(tradeDate)
}

func v150YieldDailyLedgerIdentity(runID, ruleID, symbol string) string {
	return strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(ruleID) + "\x00" + normalizeRecommendStockCode(symbol)
}

func v150YieldDailyFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// calculateV150StrategyMaxDrawdownByEntries derives drawdown from the same
// sealed-ledger/raw-minute portfolio curve used by the daily overview. It
// intentionally refuses a partial curve, because dropping one missing holding
// would understate portfolio drawdown.
func calculateV150StrategyMaxDrawdownByEntries(entries []yieldDailyOverviewEntry) (float64, bool) {
	if len(entries) == 0 || db.Dao == nil || !db.Dao.Migrator().HasTable(&models.AiRecommendStocks{}) {
		return 0, false
	}
	ids := make([]uint, 0, len(entries))
	seen := make(map[uint]struct{}, len(entries))
	for _, entry := range entries {
		if !isV150CostVersion(entry.SummaryVersion) || entry.RecommendID == 0 {
			return 0, false
		}
		if _, ok := seen[entry.RecommendID]; ok {
			continue
		}
		seen[entry.RecommendID] = struct{}{}
		ids = append(ids, entry.RecommendID)
	}
	records := make([]models.AiRecommendStocks, 0, len(ids))
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Where("id IN ?", ids).Find(&records).Error; err != nil || len(records) != len(ids) {
		return 0, false
	}
	ledgers, warnings := loadV150YieldDailyOrderLedgers(records, timeNow().In(cnLocation()))
	if len(warnings) > 0 || len(ledgers) != len(ids) {
		return 0, false
	}
	entries = reconcileV150YieldDailyOverviewEntriesWithLedger(entries, ledgers)
	startDay, endDay, ok := resolveYieldDailyOverviewWindow(entries)
	if !ok {
		return 0, false
	}
	tradingDays, _, err := loadYieldDailyOverviewTradingDaysFromCache(startDay, endDay)
	if err != nil || len(tradingDays) == 0 {
		return 0, false
	}
	priceSeries, _, provenanceWarnings, err := loadV150YieldDailyRawMinutePriceSeries(entries, tradingDays)
	if err != nil || len(provenanceWarnings) > 0 {
		return 0, false
	}
	points, pointWarnings := buildYieldDailyOverviewPointsWithV150Ledgers(entries, tradingDays, priceSeries, nil, ledgers)
	if len(pointWarnings) > 0 || len(points) != len(tradingDays) {
		return 0, false
	}
	peak := v150.FixedStrategyV150Config().PortfolioCash
	if peak <= 0 {
		return 0, false
	}
	maxDrawdown := 0.0
	for _, point := range points {
		equity := point.PortfolioEquity
		if equity <= 0 {
			return 0, false
		}
		if equity > peak {
			peak = equity
		}
		drawdown := round2((equity - peak) / peak * 100)
		if drawdown < maxDrawdown {
			maxDrawdown = drawdown
		}
	}
	return maxDrawdown, true
}
