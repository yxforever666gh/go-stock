package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
)

const marketSummaryV150ExecutionMonitorGrace = 45 * time.Second

var marketSummaryV150ExecutionMonitorWakeup struct {
	sync.RWMutex
	callback func()
}

// SetMarketSummaryV150ExecutionMonitorWakeup connects immutable recommendation
// publication to the application-owned runtime task. It does not execute a
// rule itself and is safe to replace during process startup/tests.
func SetMarketSummaryV150ExecutionMonitorWakeup(callback func()) {
	marketSummaryV150ExecutionMonitorWakeup.Lock()
	marketSummaryV150ExecutionMonitorWakeup.callback = callback
	marketSummaryV150ExecutionMonitorWakeup.Unlock()
}

func wakeMarketSummaryV150ExecutionMonitor() {
	marketSummaryV150ExecutionMonitorWakeup.RLock()
	callback := marketSummaryV150ExecutionMonitorWakeup.callback
	marketSummaryV150ExecutionMonitorWakeup.RUnlock()
	if callback != nil {
		callback()
	}
}

// MarketSummaryV150ExecutionWindow is the deterministic scheduler identity
// for one online execution pass. SlotAt is used only for de-duplication;
// EvaluationCutoff is the latest 15-minute boundary whose bar may be complete.
type MarketSummaryV150ExecutionWindow struct {
	SlotAt           time.Time
	EvaluationCutoff time.Time
}

// MarketSummaryV150ExecutionMonitorResult describes one independent online
// pass. It deliberately reports execution lifecycle counts rather than yield
// page counts: the immutable order-event ledger remains the source of truth.
type MarketSummaryV150ExecutionMonitorResult struct {
	ObservedAt       time.Time
	EvaluationCutoff time.Time
	PendingCount     int
	OpenCount        int
	ProcessedCount   int
	SkippedCount     int
	Warnings         []string
}

// MarketSummaryV150ExecutionObservationError preserves every per-symbol
// refresh failure while allowing the successful symbols in the same batch to
// complete. The scheduler treats it as retryable and does not advance its slot.
type MarketSummaryV150ExecutionObservationError struct {
	Failures map[string]string
}

func (err *MarketSummaryV150ExecutionObservationError) Error() string {
	if err == nil || len(err.Failures) == 0 {
		return ""
	}
	symbols := make([]string, 0, len(err.Failures))
	for symbol := range err.Failures {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	parts := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		parts = append(parts, symbol+": "+strings.TrimSpace(err.Failures[symbol]))
	}
	return "v1.5 execution security observation failed: " + strings.Join(parts, "; ")
}

func newMarketSummaryV150ExecutionObservationError(failures map[string]error) error {
	if len(failures) == 0 {
		return nil
	}
	structured := &MarketSummaryV150ExecutionObservationError{Failures: make(map[string]string, len(failures))}
	for symbol, err := range failures {
		if err != nil {
			structured.Failures[normalizeRecommendStockCode(symbol)] = err.Error()
		}
	}
	if len(structured.Failures) == 0 {
		return nil
	}
	return structured
}

// ResolveMarketSummaryV150ExecutionWindow maps wall-clock time to the latest
// completed A-share 15-minute execution window. The pre-open 09:29 and 12:59
// windows intentionally run before a new continuous session so a causally
// available security observation exists when that session's first bar starts.
func ResolveMarketSummaryV150ExecutionWindow(now time.Time) (MarketSummaryV150ExecutionWindow, bool) {
	if now.IsZero() {
		return MarketSummaryV150ExecutionWindow{}, false
	}
	loc := cnLocation()
	local := now.In(loc)
	day := normalizeDailyTradeDate(local)
	if !isCNOpenTradeDaySafe(day) {
		previous := shiftToPrevCNOpenTradeDaySafe(day.AddDate(0, 0, -1))
		cutoff := marketSummaryV150SessionTime(previous, 15, 0)
		return MarketSummaryV150ExecutionWindow{SlotAt: cutoff, EvaluationCutoff: cutoff}, !cutoff.IsZero()
	}

	previous := shiftToPrevCNOpenTradeDaySafe(day.AddDate(0, 0, -1))
	previousClose := marketSummaryV150SessionTime(previous, 15, 0)
	preOpen := marketSummaryV150SessionTime(day, 9, 29)
	if local.Before(preOpen) {
		return MarketSummaryV150ExecutionWindow{SlotAt: previousClose, EvaluationCutoff: previousClose}, !previousClose.IsZero()
	}

	// Observe the current execution day before its first bar starts. The bar
	// cutoff remains the prior close until the 09:30 bar has had provider grace.
	morningOpen := marketSummaryV150SessionTime(day, 9, 30)
	firstMorningComplete := marketSummaryV150SessionTime(day, 9, 45).Add(marketSummaryV150ExecutionMonitorGrace)
	if local.Before(firstMorningComplete) {
		return MarketSummaryV150ExecutionWindow{SlotAt: morningOpen, EvaluationCutoff: previousClose}, true
	}

	morningCloseWithGrace := marketSummaryV150SessionTime(day, 11, 30).Add(marketSummaryV150ExecutionMonitorGrace)
	if local.Before(morningCloseWithGrace) {
		boundary := marketSummaryV150CompletedBoundary(local.Add(-marketSummaryV150ExecutionMonitorGrace), day, 9, 30, 11, 30)
		return MarketSummaryV150ExecutionWindow{SlotAt: boundary, EvaluationCutoff: boundary}, !boundary.IsZero()
	}

	morningClose := marketSummaryV150SessionTime(day, 11, 30)
	preAfternoon := marketSummaryV150SessionTime(day, 12, 59)
	if local.Before(preAfternoon) {
		return MarketSummaryV150ExecutionWindow{SlotAt: morningClose, EvaluationCutoff: morningClose}, true
	}

	// Like 09:29, this slot refreshes status before the first afternoon bar.
	afternoonOpen := marketSummaryV150SessionTime(day, 13, 0)
	firstAfternoonComplete := marketSummaryV150SessionTime(day, 13, 15).Add(marketSummaryV150ExecutionMonitorGrace)
	if local.Before(firstAfternoonComplete) {
		return MarketSummaryV150ExecutionWindow{SlotAt: afternoonOpen, EvaluationCutoff: morningClose}, true
	}

	closeWithGrace := marketSummaryV150SessionTime(day, 15, 0).Add(marketSummaryV150ExecutionMonitorGrace)
	if local.Before(closeWithGrace) {
		boundary := marketSummaryV150CompletedBoundary(local.Add(-marketSummaryV150ExecutionMonitorGrace), day, 13, 0, 15, 0)
		return MarketSummaryV150ExecutionWindow{SlotAt: boundary, EvaluationCutoff: boundary}, !boundary.IsZero()
	}

	closeAt := marketSummaryV150SessionTime(day, 15, 0)
	return MarketSummaryV150ExecutionWindow{SlotAt: closeAt, EvaluationCutoff: closeAt}, true
}

func marketSummaryV150SessionTime(day time.Time, hour, minute int) time.Time {
	if day.IsZero() {
		return time.Time{}
	}
	local := day.In(cnLocation())
	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, local.Location())
}

func marketSummaryV150CompletedBoundary(now, day time.Time, startHour, startMinute, endHour, endMinute int) time.Time {
	start := marketSummaryV150SessionTime(day, startHour, startMinute)
	end := marketSummaryV150SessionTime(day, endHour, endMinute)
	if now.Before(start) {
		return time.Time{}
	}
	elapsed := now.Sub(start)
	boundary := start.Add((elapsed / (15 * time.Minute)) * 15 * time.Minute)
	if boundary.After(end) {
		boundary = end
	}
	return boundary
}

// RunMarketSummaryV150ExecutionMonitor performs one UI-independent execution
// pass. It refreshes the current execution-day observations/minute cache via
// the existing online event-order replay, then materializes the resulting
// record projections. A restart can safely call the same window again because
// lifecycle event identities and immutable appends are idempotent.
func RunMarketSummaryV150ExecutionMonitor(now time.Time) (MarketSummaryV150ExecutionMonitorResult, error) {
	result := MarketSummaryV150ExecutionMonitorResult{ObservedAt: now.In(cnLocation())}
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return result, err
	}
	window, ok := ResolveMarketSummaryV150ExecutionWindow(now)
	if !ok {
		return result, errors.New("v1.5 execution monitor window is unavailable")
	}
	result.EvaluationCutoff = window.EvaluationCutoff
	if db.Dao == nil {
		return result, errors.New("v1.5 execution monitor database is unavailable")
	}

	records, pendingCount, openCount, skippedCount, warnings, err := loadMarketSummaryV150ActiveExecutionRecords(window.EvaluationCutoff)
	result.PendingCount = pendingCount
	result.OpenCount = openCount
	result.SkippedCount = skippedCount
	result.Warnings = warnings
	if err != nil {
		return result, err
	}
	if len(records) == 0 {
		return result, nil
	}

	scheduled := make([]*marketSummaryV150ScheduledRecord, 0, len(records))
	for _, record := range records {
		scheduled = append(scheduled, &marketSummaryV150ScheduledRecord{
			record: record,
			rank:   int(^uint(0) >> 1),
			ruleID: strings.TrimSpace(record.StrategyRuleID),
		})
	}

	meta, err := getOrCreateYieldMeta()
	if err != nil {
		return result, fmt.Errorf("prepare v1.5 execution projection metadata: %w", err)
	}
	crawlTimeout := int64(60)
	if setting := GetSettingConfig(); setting != nil && setting.CrawlTimeOut > 0 {
		crawlTimeout = setting.CrawlTimeOut
	}
	localNow := now.In(cnLocation())
	ctx := yieldBuildContext{
		Force:                             true,
		Reason:                            "v150_execution_monitor",
		Now:                               localNow,
		InTradingSession:                  isCNTradingSession(localNow),
		LatestTradeDate:                   normalizeDailyTradeDate(window.EvaluationCutoff),
		CrawlTimeout:                      crawlTimeout,
		DisableMinuteFetch:                false,
		V150EvaluationCutoff:              window.EvaluationCutoff,
		FailOnV150ObservationRefreshError: true,
	}
	writer := newAiRecommendYieldSnapshotWriter(meta.ID, len(scheduled)+1)
	replayErr := processMarketSummaryV150RecordsInEventOrder(scheduled, ctx, writer)
	flushErr := writer.Flush()
	if replayErr != nil || flushErr != nil {
		return result, errors.Join(
			wrapMarketSummaryV150ExecutionMonitorError("replay v1.5 active execution rules", replayErr),
			wrapMarketSummaryV150ExecutionMonitorError("persist v1.5 execution projections", flushErr),
		)
	}
	result.ProcessedCount = len(scheduled)
	return result, nil
}

func wrapMarketSummaryV150ExecutionMonitorError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func loadMarketSummaryV150ActiveExecutionRecords(observedAt time.Time) ([]models.AiRecommendStocks, int, int, int, []string, error) {
	if db.Dao == nil {
		return nil, 0, 0, 0, nil, errors.New("strategy database is unavailable")
	}
	for _, table := range []any{&models.StrategyRunSnapshot{}, &models.CandidateSnapshot{}, &models.RuleSnapshot{}, &models.OrderEvent{}} {
		if !db.Dao.Migrator().HasTable(table) {
			return nil, 0, 0, 0, nil, fmt.Errorf("v1.5 execution table %T is unavailable", table)
		}
	}

	// Frozen entry rules and their append-only lifecycle events are the only
	// authority for execution enumeration. ai_recommend_stocks is a mutable
	// display projection and is deliberately loaded only after the immutable
	// execution identities have been established.
	var rules []models.RuleSnapshot
	if err := db.Dao.Model(&models.RuleSnapshot{}).
		Where("strategy_version = ? AND frozen_at IS NOT NULL", marketSummaryVersion150).
		Order("rule_id ASC").
		Find(&rules).Error; err != nil {
		return nil, 0, 0, 0, nil, err
	}
	entryRules := rules[:0]
	for _, rule := range rules {
		if strings.EqualFold(strings.TrimSpace(rule.RuleType), "entry") {
			entryRules = append(entryRules, rule)
		}
	}
	if len(entryRules) == 0 {
		return []models.AiRecommendStocks{}, 0, 0, 0, []string{}, nil
	}

	ruleIDs := make([]string, 0, len(entryRules))
	runIDs := make([]string, 0, len(entryRules))
	candidateIDs := make([]string, 0, len(entryRules))
	for _, rule := range entryRules {
		if ruleID := strings.TrimSpace(rule.RuleID); ruleID != "" {
			ruleIDs = append(ruleIDs, ruleID)
		}
		if runID := strings.TrimSpace(rule.RunID); runID != "" {
			runIDs = append(runIDs, runID)
		}
		if candidateID := strings.TrimSpace(rule.CandidateID); candidateID != "" {
			candidateIDs = append(candidateIDs, candidateID)
		}
	}

	var runs []models.StrategyRunSnapshot
	if err := db.Dao.Model(&models.StrategyRunSnapshot{}).
		Where("run_id IN ? AND strategy_version = ? AND frozen_at IS NOT NULL", runIDs, marketSummaryVersion150).
		Find(&runs).Error; err != nil {
		return nil, 0, 0, 0, nil, err
	}
	runsByID := make(map[string]models.StrategyRunSnapshot, len(runs))
	for _, run := range runs {
		runsByID[strings.TrimSpace(run.RunID)] = run
	}

	var candidates []models.CandidateSnapshot
	if len(candidateIDs) > 0 {
		if err := db.Dao.Model(&models.CandidateSnapshot{}).
			Where("candidate_id IN ? AND strategy_version = ? AND frozen_at IS NOT NULL", candidateIDs, marketSummaryVersion150).
			Find(&candidates).Error; err != nil {
			return nil, 0, 0, 0, nil, err
		}
	}
	candidatesByID := make(map[string]models.CandidateSnapshot, len(candidates))
	for _, candidate := range candidates {
		candidatesByID[strings.TrimSpace(candidate.CandidateID)] = candidate
	}

	var events []models.OrderEvent
	if err := db.Dao.Model(&models.OrderEvent{}).
		Where("rule_id IN ? AND strategy_version = ? AND frozen_at IS NOT NULL", ruleIDs, marketSummaryVersion150).
		Order("rule_id ASC, sequence ASC, event_at ASC, event_id ASC").
		Find(&events).Error; err != nil {
		return nil, 0, 0, 0, nil, err
	}
	eventsByRule := make(map[string][]models.OrderEvent, len(ruleIDs))
	for _, event := range events {
		ruleID := strings.TrimSpace(event.RuleID)
		eventsByRule[ruleID] = append(eventsByRule[ruleID], event)
	}

	projectionsByRule := make(map[string][]models.AiRecommendStocks, len(ruleIDs))
	if db.Dao.Migrator().HasTable(&models.AiRecommendStocks{}) {
		var projections []models.AiRecommendStocks
		if err := db.Dao.Model(&models.AiRecommendStocks{}).
			Where("strategy_rule_id IN ? AND summary_version = ?", ruleIDs, marketSummaryVersion150).
			Order("strategy_rule_id ASC, id ASC").
			Find(&projections).Error; err != nil {
			return nil, 0, 0, 0, nil, err
		}
		for _, projection := range projections {
			ruleID := strings.TrimSpace(projection.StrategyRuleID)
			projectionsByRule[ruleID] = append(projectionsByRule[ruleID], projection)
		}
	}

	active := make([]models.AiRecommendStocks, 0, len(entryRules))
	warnings := make([]string, 0)
	pendingCount, openCount, skippedCount := 0, 0, 0
	for _, rule := range entryRules {
		ruleID := strings.TrimSpace(rule.RuleID)
		runID := strings.TrimSpace(rule.RunID)
		candidateID := strings.TrimSpace(rule.CandidateID)
		run, hasRun := runsByID[runID]
		candidate, hasCandidate := candidatesByID[candidateID]
		if ruleID == "" || runID == "" || !hasRun || strings.TrimSpace(run.RunID) != runID {
			skippedCount++
			warnings = append(warnings, "missing frozen strategy run for entry rule "+ruleID)
			continue
		}
		if candidateID == "" || !hasCandidate || strings.TrimSpace(candidate.RunID) != runID ||
			normalizeRecommendStockCode(candidate.Symbol) != normalizeRecommendStockCode(rule.Symbol) {
			skippedCount++
			warnings = append(warnings, "missing or mismatched frozen candidate for entry rule "+ruleID)
			continue
		}

		issued, open, terminal := false, false, false
		for _, event := range eventsByRule[ruleID] {
			if strings.TrimSpace(event.RunID) != runID ||
				normalizeRecommendStockCode(event.Symbol) != normalizeRecommendStockCode(rule.Symbol) {
				warnings = append(warnings, "ignored mismatched immutable order event for rule "+ruleID)
				continue
			}
			switch strings.ToLower(strings.TrimSpace(event.EventType)) {
			case "rule_issued":
				issued = true
			case string(v150.EventFill):
				open = true
				terminal = false
			case string(v150.EventExitFill):
				open = false
				terminal = true
			case string(v150.EventReject), "activation_expired", "expired":
				if !open {
					terminal = true
				}
			}
		}
		if !issued {
			skippedCount++
			warnings = append(warnings, "missing immutable rule_issued event "+ruleID)
			continue
		}
		if terminal {
			continue
		}

		projection, projectionWarnings := selectMarketSummaryV150ExecutionProjection(ruleID, projectionsByRule[ruleID])
		warnings = append(warnings, projectionWarnings...)
		record := buildMarketSummaryV150ExecutionCompatibilityRecord(rule, run, candidate, projection)
		if _, planErr := loadMarketSummaryV150FrozenExecutionPlan(record); planErr != nil {
			skippedCount++
			if open {
				// A corrupt plan cannot safely drive an exit, but an existing fill
				// remains an open ledger fact. Never try to rewrite that lifecycle
				// as an entry rejection or hide it from the monitor count.
				openCount++
				warnings = append(warnings, "open rule has invalid frozen entry plan "+ruleID+": "+planErr.Error())
				continue
			}
			reason := marketSummaryV150DataHealthReject + ": invalid frozen entry plan: " + planErr.Error()
			rejectAt := marketSummaryV150InvalidPlanRejectAt(observedAt, run, eventsByRule[ruleID])
			appendErr := appendMarketSummaryV150OrderEvents(record, run, []v150.OrderEvent{{
				Type: v150.EventReject, At: rejectAt, Symbol: record.StockCode, Reason: reason,
			}}, marketSummaryV150EventAccounting{})
			warning := "rejected invalid frozen entry plan " + ruleID + ": " + planErr.Error()
			if appendErr != nil {
				warning += "; append reject lifecycle: " + appendErr.Error()
			}
			warnings = append(warnings, warning)
			continue
		}
		if open {
			openCount++
			active = append(active, record)
			continue
		}
		pendingCount++
		active = append(active, record)
	}

	sort.SliceStable(active, func(i, j int) bool {
		leftRule := strings.TrimSpace(active[i].StrategyRuleID)
		rightRule := strings.TrimSpace(active[j].StrategyRuleID)
		if leftRule != rightRule {
			return leftRule < rightRule
		}
		return active[i].ID < active[j].ID
	})
	return active, pendingCount, openCount, skippedCount, dedupeNonEmptyStrings(warnings, 64), nil
}

func marketSummaryV150InvalidPlanRejectAt(observedAt time.Time, run models.StrategyRunSnapshot, events []models.OrderEvent) time.Time {
	rejectAt := observedAt
	if rejectAt.IsZero() || (!run.DecisionAt.IsZero() && rejectAt.Before(run.DecisionAt)) {
		rejectAt = run.DecisionAt
	}
	for _, event := range events {
		if event.EventAt.After(rejectAt) {
			rejectAt = event.EventAt
		}
	}
	return rejectAt
}

// selectMarketSummaryV150ExecutionProjection chooses at most one mutable row
// to receive compatibility projections. Missing, duplicate, or terminal
// analysis-only rows never remove the immutable rule from execution.
func selectMarketSummaryV150ExecutionProjection(ruleID string, rows []models.AiRecommendStocks) (*models.AiRecommendStocks, []string) {
	warnings := make([]string, 0, 3)
	if len(rows) == 0 {
		return nil, []string{"missing display projection for immutable rule " + ruleID}
	}
	if len(rows) > 1 {
		warnings = append(warnings, "duplicate display projections ignored for immutable rule "+ruleID)
	}
	var selected *models.AiRecommendStocks
	for index := range rows {
		if normalizeRecommendExecutionState(rows[index].ExecutionState) == recommendExecutionAnalysisOnly ||
			isAnalysisOnlyRecommend(&rows[index]) {
			warnings = append(warnings, "analysis_only display projection ignored for immutable rule "+ruleID)
			continue
		}
		if selected == nil {
			projection := rows[index]
			selected = &projection
		}
	}
	return selected, warnings
}

// buildMarketSummaryV150ExecutionCompatibilityRecord exposes only the legacy
// shape required by the existing event replay and projection writer. Every
// execution-relevant value is rebuilt from immutable run/rule/candidate data;
// the optional mutable projection contributes only its database identity and
// non-behavioural provider/model labels.
func buildMarketSummaryV150ExecutionCompatibilityRecord(
	rule models.RuleSnapshot,
	run models.StrategyRunSnapshot,
	candidate models.CandidateSnapshot,
	projection *models.AiRecommendStocks,
) models.AiRecommendStocks {
	record := models.AiRecommendStocks{}
	if projection != nil {
		record.ID = projection.ID
		record.ProviderName = strings.TrimSpace(projection.ProviderName)
		record.ModelName = strings.TrimSpace(projection.ModelName)
	}
	decisionAt := run.DecisionAt
	record.DataTime = &decisionAt
	record.StockCode = normalizeRecommendStockCode(rule.Symbol)
	record.StockName = firstNonEmptyText(strings.TrimSpace(candidate.Name), record.StockCode)
	record.BkName = strings.TrimSpace(candidate.Sector)
	record.SummaryVersion = marketSummaryVersion150
	record.StrategyRunID = strings.TrimSpace(rule.RunID)
	record.StrategyRuleID = strings.TrimSpace(rule.RuleID)
	record.ExecutionState = recommendExecutionConditional
	record.RecommendCategory = recommendExecutionConditional
	record.RecommendStatus = "valid"
	record.ActivationStatus = "pending"
	record.ActivationRuleJSON = `{}`
	record.ActivationRuleVersion = marketSummaryVersion150
	record.ActivationRuleSource = "frozen_rule_snapshot"

	if plan, ok := marketSummaryV150ExecutionPlanProjection(rule); ok {
		entryMin, entryMax := marketSummaryV150PlanEntryRange(plan)
		record.RecommendBuyPriceMin = entryMin
		record.RecommendBuyPriceMax = entryMax
		if entryMin > 0 && entryMax > 0 {
			record.RecommendBuyPrice = fmt.Sprintf("%.2f-%.2f", entryMin, entryMax)
		}
		record.RecommendStopLossPrice = formatMarketSummaryPlanPrice(plan.Stop)
		record.RecommendStopProfitPrice = formatMarketSummaryPlanPrice(plan.Target)
		record.RecommendStopProfitPriceMin = plan.Target
		record.RecommendStopProfitPriceMax = plan.Target
	}
	return record
}

func marketSummaryV150ExecutionPlanProjection(rule models.RuleSnapshot) (v150.TradePlan, bool) {
	var payload struct {
		Production struct {
			Plan v150.TradePlan `json:"plan"`
		} `json:"production"`
	}
	if err := json.Unmarshal([]byte(rule.PayloadJSON), &payload); err != nil {
		return v150.TradePlan{}, false
	}
	plan := payload.Production.Plan
	if normalizeRecommendStockCode(plan.Symbol) == "" ||
		normalizeRecommendStockCode(plan.Symbol) != normalizeRecommendStockCode(rule.Symbol) {
		return v150.TradePlan{}, false
	}
	return plan, true
}
