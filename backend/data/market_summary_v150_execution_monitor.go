package data

import (
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
	window, ok := ResolveMarketSummaryV150ExecutionWindow(now)
	if !ok {
		return result, errors.New("v1.5 execution monitor window is unavailable")
	}
	result.EvaluationCutoff = window.EvaluationCutoff
	if db.Dao == nil {
		return result, errors.New("v1.5 execution monitor database is unavailable")
	}

	records, pendingCount, openCount, skippedCount, warnings, err := loadMarketSummaryV150ActiveExecutionRecords()
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

func loadMarketSummaryV150ActiveExecutionRecords() ([]models.AiRecommendStocks, int, int, int, []string, error) {
	if db.Dao == nil {
		return nil, 0, 0, 0, nil, errors.New("strategy database is unavailable")
	}
	for _, table := range []any{&models.AiRecommendStocks{}, &models.RuleSnapshot{}, &models.OrderEvent{}} {
		if !db.Dao.Migrator().HasTable(table) {
			return nil, 0, 0, 0, nil, fmt.Errorf("v1.5 execution table %T is unavailable", table)
		}
	}

	var records []models.AiRecommendStocks
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("summary_version = ? AND strategy_run_id <> '' AND strategy_rule_id <> ''", marketSummaryVersion150).
		Order("id ASC").
		Find(&records).Error; err != nil {
		return nil, 0, 0, 0, nil, err
	}
	if len(records) == 0 {
		return []models.AiRecommendStocks{}, 0, 0, 0, []string{}, nil
	}

	ruleIDs := make([]string, 0, len(records))
	for _, record := range records {
		if ruleID := strings.TrimSpace(record.StrategyRuleID); ruleID != "" {
			ruleIDs = append(ruleIDs, ruleID)
		}
	}
	var rules []models.RuleSnapshot
	if err := db.Dao.Model(&models.RuleSnapshot{}).
		Where("rule_id IN ? AND strategy_version = ? AND frozen_at IS NOT NULL", ruleIDs, marketSummaryVersion150).
		Find(&rules).Error; err != nil {
		return nil, 0, 0, 0, nil, err
	}
	frozenRules := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if strings.EqualFold(strings.TrimSpace(rule.RuleType), "entry") {
			frozenRules[strings.TrimSpace(rule.RuleID)] = struct{}{}
		}
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

	active := make([]models.AiRecommendStocks, 0, len(records))
	warnings := make([]string, 0)
	seenRules := make(map[string]struct{}, len(records))
	pendingCount, openCount, skippedCount := 0, 0, 0
	for _, record := range records {
		ruleID := strings.TrimSpace(record.StrategyRuleID)
		if normalizeRecommendExecutionState(record.ExecutionState) == recommendExecutionAnalysisOnly {
			skippedCount++
			warnings = append(warnings, "analysis_only recommendation cannot enter execution "+ruleID)
			continue
		}
		if _, duplicate := seenRules[ruleID]; duplicate {
			skippedCount++
			warnings = append(warnings, "duplicate recommendation for immutable rule "+ruleID)
			continue
		}
		seenRules[ruleID] = struct{}{}
		if _, exists := frozenRules[ruleID]; !exists {
			skippedCount++
			warnings = append(warnings, "missing frozen entry rule "+ruleID)
			continue
		}

		issued, open, terminal := false, false, false
		for _, event := range eventsByRule[ruleID] {
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
		if open {
			openCount++
			active = append(active, record)
			continue
		}
		if terminal {
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
