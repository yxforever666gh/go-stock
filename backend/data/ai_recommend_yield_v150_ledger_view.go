package data

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/portfolio"
	"go-stock/backend/strategy/v150"
)

const v150YieldLedgerViewHealthCode = "yield_order_ledger_missing_or_invalid"

const v150YieldQuoteMaximumAge = 5 * time.Minute

// v150YieldLedgerView keeps the immutable recommendation and its allowlisted
// display metadata together. Record never inherits lifecycle, price, position
// or yield fields from the compatibility projection.
type v150YieldLedgerView struct {
	Record  models.AiRecommendStocks
	Current portfolio.CurrentRecommendation
}

func isV150YieldListCohort(cohort string) bool {
	normalized := normalizeStrategyCohort(cohort, strategyCohortCurrent)
	return normalized == marketSummaryVersion150 ||
		(normalized == strategyCohortCurrent && marketSummaryCurrentVersion == marketSummaryVersion150)
}

// loadV150YieldLedgerViews enumerates frozen entry rules, not mutable display
// projections. A missing or ambiguous ai_recommend_stocks row therefore never
// hides an immutable recommendation.
func loadV150YieldLedgerViews(
	query *models.AiRecommendStocksQuery,
	asOf time.Time,
) ([]v150YieldLedgerView, error) {
	if db.Dao == nil {
		return nil, fmt.Errorf("strategy database is unavailable")
	}
	if asOf.IsZero() {
		asOf = timeNow().In(cnLocation())
	}
	start, end := v150YieldLedgerQueryWindow(query, asOf)
	currents, err := NewCompatibilityCurrentRecommendationReader(db.Dao).List(
		context.Background(),
		portfolio.RecommendationQuery{
			StrategyVersion: v150.StrategyVersion,
			Start:           start,
			End:             end,
			AsOf:            asOf,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("load V1.5 frozen yield recommendations: %w", err)
	}
	if len(currents) == 0 {
		return []v150YieldLedgerView{}, nil
	}

	rules, err := loadV150YieldRuleSnapshots(currents)
	if err != nil {
		return nil, err
	}
	views := make([]v150YieldLedgerView, 0, len(currents))
	for _, current := range currents {
		rule, ok := rules[current.Frozen.RuleID]
		if !ok {
			return nil, fmt.Errorf("V1.5 frozen yield rule %s disappeared after immutable read", current.Frozen.RuleID)
		}
		if rule.RunID != current.Frozen.RunID || rule.CandidateID != current.Frozen.CandidateID ||
			normalizeRecommendStockCode(rule.Symbol) != current.Frozen.Symbol ||
			rule.SnapshotHash != current.Frozen.Identity.RuleSnapshotHash || rule.FrozenAt == nil ||
			!rule.FrozenAt.Equal(current.Frozen.Identity.RuleFrozenAt) {
			return nil, fmt.Errorf("V1.5 frozen yield rule %s changed during read", current.Frozen.RuleID)
		}
		if !strings.EqualFold(strings.TrimSpace(rule.RuleType), "entry") {
			continue
		}
		record := models.AiRecommendStocks{}
		applyV150FrozenIdentityToYieldRecord(&record, current, rule)
		if !matchesV150YieldLedgerQuery(record, query) {
			continue
		}
		views = append(views, v150YieldLedgerView{Record: record, Current: current})
	}

	sort.Slice(views, func(i, j int) bool {
		left, right := views[i].Current.Frozen, views[j].Current.Frozen
		if !left.DecisionAt.Equal(right.DecisionAt) {
			return left.DecisionAt.After(right.DecisionAt)
		}
		if left.RunID != right.RunID {
			return left.RunID > right.RunID
		}
		return left.RuleID > right.RuleID
	})
	return views, nil
}

func v150YieldLedgerQueryWindow(query *models.AiRecommendStocksQuery, asOf time.Time) (time.Time, time.Time) {
	loc := cnLocation()
	start := time.Date(1970, 1, 1, 0, 0, 0, 0, loc)
	end := time.Date(asOf.In(loc).Year(), asOf.In(loc).Month(), asOf.In(loc).Day(), 0, 0, 0, 0, loc)
	if query == nil || strings.TrimSpace(query.StartDate) == "" {
		return start, end
	}
	parsedStart, err := parseDateTimeWithFallback(normalizeDateTime(query.StartDate))
	if err != nil {
		return start, end
	}
	parsedStart = parsedStart.In(loc)
	start = time.Date(parsedStart.Year(), parsedStart.Month(), parsedStart.Day(), 0, 0, 0, 0, loc)
	end = start
	if strings.TrimSpace(query.EndDate) == "" {
		return start, end
	}
	parsedEnd, err := parseDateTimeWithFallback(normalizeDateTime(query.EndDate))
	if err != nil {
		return start, end
	}
	parsedEnd = parsedEnd.In(loc)
	end = time.Date(parsedEnd.Year(), parsedEnd.Month(), parsedEnd.Day(), 0, 0, 0, 0, loc)
	return start, end
}

func loadV150YieldRuleSnapshots(currents []portfolio.CurrentRecommendation) (map[string]models.RuleSnapshot, error) {
	ids := make([]string, 0, len(currents))
	seen := make(map[string]struct{}, len(currents))
	for _, current := range currents {
		id := strings.TrimSpace(current.Frozen.RuleID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	rows := make([]models.RuleSnapshot, 0, len(ids))
	if len(ids) > 0 {
		if err := db.Dao.Model(&models.RuleSnapshot{}).
			Where("strategy_version = ? AND rule_id IN ?", v150.StrategyVersion, ids).
			Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("load V1.5 frozen yield rules: %w", err)
		}
	}
	result := make(map[string]models.RuleSnapshot, len(rows))
	for _, row := range rows {
		result[row.RuleID] = row
	}
	return result, nil
}

func applyV150FrozenIdentityToYieldRecord(
	record *models.AiRecommendStocks,
	current portfolio.CurrentRecommendation,
	rule models.RuleSnapshot,
) {
	if record == nil {
		return
	}
	frozen := current.Frozen
	decisionAt := frozen.DecisionAt.In(cnLocation())
	record.DataTime = &decisionAt
	record.SummaryVersion = v150.StrategyVersion
	record.StrategyRunID = frozen.RunID
	record.StrategyRuleID = frozen.RuleID
	record.StockCode = normalizeRecommendStockCode(frozen.Symbol)
	if current.Display != nil {
		record.ID = current.Display.RecommendID
	}
	if strings.TrimSpace(frozen.Name) != "" {
		record.StockName = strings.TrimSpace(frozen.Name)
	}
	if strings.TrimSpace(frozen.Sector) != "" {
		record.BkName = strings.TrimSpace(frozen.Sector)
	}
	if current.Display != nil {
		if strings.TrimSpace(record.ProviderName) == "" {
			record.ProviderName = strings.TrimSpace(current.Display.Provider)
		}
		if strings.TrimSpace(record.ModelName) == "" {
			record.ModelName = strings.TrimSpace(current.Display.Model)
		}
	}

	// These fields describe executability/lifecycle and can never be inherited
	// from ai_recommend_stocks. The immutable rule is production by definition.
	record.ExecutionState = recommendExecutionConditional
	record.RecommendCategory = recommendExecutionConditional
	record.RecommendStatus = "valid"
	record.ActivationStatus = "pending"
	record.ActivationInvalidReason = ""
	record.ActivationRuleVersion = rule.RuleVersion
	record.ActivationRuleSource = "strategy_rule_snapshot"
	record.ActivationRuleJSON = rule.PayloadJSON
	applyV150FrozenPlanDisplay(record, rule)
}

func applyV150FrozenPlanDisplay(record *models.AiRecommendStocks, rule models.RuleSnapshot) {
	if record == nil {
		return
	}
	var payload marketSummaryV150RulePayload
	if err := json.Unmarshal([]byte(rule.PayloadJSON), &payload); err != nil {
		return
	}
	plan := payload.Production.Plan
	entryMin, entryMax := marketSummaryV150PlanEntryRange(plan)
	if entryMin <= 0 || entryMax < entryMin || plan.Stop <= 0 || plan.Target <= 0 {
		return
	}
	record.RecommendBuyPriceMin = entryMin
	record.RecommendBuyPriceMax = entryMax
	record.RecommendBuyPrice = fmt.Sprintf("%.2f-%.2f", entryMin, entryMax)
	record.RecommendStopLossPrice = formatMarketSummaryPlanPrice(plan.Stop)
	record.RecommendStopProfitPrice = formatMarketSummaryPlanPrice(plan.Target)
	record.RecommendStopProfitPriceMin = plan.Target
	record.RecommendStopProfitPriceMax = plan.Target
}

func matchesV150YieldLedgerQuery(record models.AiRecommendStocks, query *models.AiRecommendStocksQuery) bool {
	if query == nil {
		return true
	}
	contains := func(value, filter string) bool {
		return strings.Contains(strings.ToLower(strings.TrimSpace(value)), strings.ToLower(strings.TrimSpace(filter)))
	}
	return (strings.TrimSpace(query.StockCode) == "" || contains(record.StockCode, query.StockCode)) &&
		(strings.TrimSpace(query.StockName) == "" || contains(record.StockName, query.StockName)) &&
		(strings.TrimSpace(query.BkName) == "" || contains(record.BkName, query.BkName)) &&
		(strings.TrimSpace(query.ModelName) == "" || contains(record.ModelName, query.ModelName))
}

func buildV150YieldLedgerFirstItems(
	views []v150YieldLedgerView,
	asOf time.Time,
) ([]models.AiRecommendStocksYieldItem, []string) {
	items := make([]models.AiRecommendStocksYieldItem, 0, len(views))
	warnings := make([]string, 0)
	quoteReader := NewCompatibilityMarketDataReader(db.Dao, db.MinuteDao)
	for _, view := range views {
		item := mapRecommendRecordToYieldItem(view.Record, map[string]models.AiRecommendYieldState{})
		item.RowKey = view.Current.Frozen.RuleID
		item.CalcMode = aiRecommendYieldModeStrict
		item.StrictReady = true
		item.StrictPendingReason = ""
		item.BacktestEligibility = recommendBacktestEligible
		item.BacktestEligibilityReason = ""
		item.ExecutionState = recommendExecutionConditional
		item.ExecutionStateLabel = recommendExecutionStateLabel(recommendExecutionConditional)
		item.CurrentPrice = 0
		item.CurrentPriceTime = ""
		resetV150YieldLifecycle(&item)
		applyV150CurrentLifecycle(&item, view.Current.Lifecycle)

		if view.Current.Lifecycle.Status == portfolio.RecommendationHolding {
			quote, quoteErr := quoteReader.Quote(context.Background(), view.Current.Frozen.Symbol, asOf)
			if quoteErr == nil && v150YieldQuoteIsFresh(quote.ObservedAt, asOf) && quote.Price > 0 {
				item.CurrentPrice = round2(quote.Price)
				item.CurrentPriceTime = quote.ObservedAt.In(cnLocation()).Format(time.DateTime)
			} else {
				warnings = append(warnings, v150YieldLedgerViewWarning(view, v150YieldValuationHealthCode))
			}
		}
		if item.ActivationStatus == "activated" {
			if err := attachV150YieldLedgerProjection(&item, view.Record, asOf); err != nil {
				warnings = append(warnings, v150YieldLedgerViewWarning(view, v150YieldLedgerViewHealthCode))
			}
		}
		applyV150YieldValuationAvailability(&item, asOf)
		items = append(items, item)
	}
	applyRecommendRepeatCount(items)
	return items, dedupeNonEmptyStrings(warnings, 0)
}

func v150YieldQuoteIsFresh(observedAt, asOf time.Time) bool {
	if observedAt.IsZero() || asOf.IsZero() {
		return false
	}
	observedAt = observedAt.In(cnLocation())
	asOf = asOf.In(cnLocation())
	if observedAt.After(asOf) || !marketSummaryV150QuoteTimestampIsInTradingSession(observedAt) {
		return false
	}
	latestLegalAt := marketSummaryV150LatestLegalQuoteAt(asOf)
	if latestLegalAt.IsZero() || observedAt.After(latestLegalAt) ||
		!normalizeDailyTradeDate(observedAt).Equal(normalizeDailyTradeDate(latestLegalAt)) {
		return false
	}
	return latestLegalAt.Sub(observedAt) <= v150YieldQuoteMaximumAge
}

func v150YieldHasActivatedRuleWithoutProjection(items []models.AiRecommendStocksYieldItem) bool {
	for _, item := range items {
		if isV150CostVersion(item.SummaryVersion) && item.RecommendID == 0 &&
			strings.TrimSpace(item.ActivationStatus) == "activated" &&
			(strings.TrimSpace(item.BacktestEligibility) == "" || strings.TrimSpace(item.BacktestEligibility) == recommendBacktestEligible) {
			return true
		}
	}
	return false
}

func resetV150YieldLifecycle(item *models.AiRecommendStocksYieldItem) {
	if item == nil {
		return
	}
	item.ActivationStatus = "pending"
	item.ActivationTime = ""
	item.ActivationPrice = 0
	item.BuyTime = ""
	item.BuyAmount = 0
	item.SellTime = "pending"
	item.SellAmount = nil
	item.PositionStatus = "pending"
	item.DataStatus = "pending"
	item.DataStatusReason = ""
	item.YieldRate = 0
	item.YieldRateText = "--"
	item.BenchmarkYieldRate = 0
	item.BenchmarkYieldRateText = "--"
	item.ExcessYieldRate = 0
	item.ExcessYieldRateText = "--"
	item.V150LedgerAccountingReady = false
	item.V150LedgerClosed = false
	item.V150LedgerEntryCash = 0
	item.V150LedgerNetValue = 0
	item.V150LedgerNetPnL = 0
	item.V150LedgerQuantity = 0
	item.V150LedgerCorporateCash = 0
}

func applyV150CurrentLifecycle(item *models.AiRecommendStocksYieldItem, lifecycle portfolio.RecommendationLifecycle) {
	if item == nil {
		return
	}
	if lifecycle.SignalAt != nil {
		item.SignalTime = formatYieldDisplayTime(*lifecycle.SignalAt)
	}
	switch lifecycle.Status {
	case portfolio.RecommendationPending:
		item.DataStatus = "pending"
	case portfolio.RecommendationOrdered:
		item.PositionStatus = "ordered"
		item.SellTime = "ordered"
		item.DataStatus = "ordered"
	case portfolio.RecommendationHolding, portfolio.RecommendationClosed:
		if lifecycle.EntryAt == nil || lifecycle.EntryPrice <= 0 || lifecycle.EntryQuantity <= 0 {
			markV150YieldLifecycleUnavailable(item, "sealed fill is incomplete")
			return
		}
		entryAt := lifecycle.EntryAt.In(cnLocation())
		item.ActivationStatus = "activated"
		item.ActivationTime = formatYieldDisplayTime(entryAt)
		item.ActivationPrice = round2(lifecycle.EntryPrice)
		item.BuyTime = item.ActivationTime
		item.BuyAmount = round2(lifecycle.EntryPrice)
		item.PositionStatus = "holding"
		item.SellTime = "holding"
		item.DataStatus = "normal"
		if lifecycle.Status == portfolio.RecommendationClosed {
			if lifecycle.ExitAt == nil || lifecycle.ExitPrice <= 0 || lifecycle.ExitQuantity <= 0 {
				markV150YieldLifecycleUnavailable(item, "sealed exit fill is incomplete")
				return
			}
			exitAt := lifecycle.ExitAt.In(cnLocation())
			exitPrice := round2(lifecycle.ExitPrice)
			item.SellTime = formatYieldDisplayTime(exitAt)
			item.SellAmount = &exitPrice
			item.PositionStatus = "closed"
		}
	case portfolio.RecommendationRejected:
		item.ActivationStatus = "invalid"
		item.PositionStatus = "rejected"
		item.SellTime = "rejected"
		item.DataStatus = "rejected"
		item.DataStatusReason = strings.TrimSpace(lifecycle.Reason)
	case portfolio.RecommendationExpired:
		item.ActivationStatus = "expired"
		item.PositionStatus = "expired"
		item.SellTime = "expired"
		item.DataStatus = "expired"
		item.DataStatusReason = strings.TrimSpace(lifecycle.Reason)
	default:
		markV150YieldLifecycleUnavailable(item, "unsupported sealed lifecycle")
	}
}

func markV150YieldLifecycleUnavailable(item *models.AiRecommendStocksYieldItem, reason string) {
	resetV150YieldLifecycle(item)
	item.ActivationStatus = "unavailable"
	item.PositionStatus = "unavailable"
	item.SellTime = "unavailable"
	item.DataStatus = "unavailable"
	item.DataStatusReason = appendYieldHealthReason(item.DataStatusReason, reason)
}

func v150YieldLedgerViewWarning(view v150YieldLedgerView, code string) string {
	symbol := normalizeRecommendStockCode(view.Current.Frozen.Symbol)
	if symbol == "" {
		symbol = view.Current.Frozen.RuleID
	}
	return symbol + ":" + strings.TrimSpace(code)
}
