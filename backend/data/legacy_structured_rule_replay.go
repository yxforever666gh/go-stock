package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

// LegacyStructuredRuleReplayOptions scopes the frozen legacy-rule corpus.
// Empty dates mean the complete corpus. ExpectedRuleCount is an optional
// release-audit assertion; it never changes which rows are read.
type LegacyStructuredRuleReplayOptions struct {
	From              time.Time
	To                time.Time
	ExpectedRuleCount int
}

// LegacyStructuredRuleReplayResult intentionally contains execution facts but
// no return metric. The legacy recommendations are an execution-regression
// corpus, not evidence for the 1.5.0 selection strategy's profitability.
type LegacyStructuredRuleReplayResult struct {
	RecommendID      uint       `json:"recommendId"`
	SummaryVersion   string     `json:"summaryVersion"`
	StockCode        string     `json:"stockCode"`
	RuleVersion      string     `json:"ruleVersion"`
	Outcome          string     `json:"outcome"`
	CacheAvailable   bool       `json:"cacheAvailable"`
	HasExitBoundary  bool       `json:"hasExitBoundary"`
	ActivationStatus string     `json:"activationStatus,omitempty"`
	PositionStatus   string     `json:"positionStatus,omitempty"`
	DataStatus       string     `json:"dataStatus,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	ActivationAt     *time.Time `json:"activationAt,omitempty"`
	ActivationPrice  float64    `json:"activationPrice,omitempty"`
	ExitAt           *time.Time `json:"exitAt,omitempty"`
	ExitPrice        float64    `json:"exitPrice,omitempty"`
	CausalityValid   bool       `json:"causalityValid"`
	TPlusOneValid    bool       `json:"tPlusOneValid"`
	Deterministic    bool       `json:"deterministic"`
	InvariantFailure string     `json:"invariantFailure,omitempty"`
}

// LegacyStructuredRuleReplayReport is a deterministic, cache-only execution
// audit over the pre-1.5 structured activation-rule corpus.
type LegacyStructuredRuleReplayReport struct {
	CacheOnly             bool                               `json:"cacheOnly"`
	ProfitabilityProof    bool                               `json:"profitabilityProof"`
	CalendarSource        string                             `json:"calendarSource"`
	AsOf                  time.Time                          `json:"asOf"`
	TotalRules            int                                `json:"totalRules"`
	ParsedRules           int                                `json:"parsedRules"`
	InvalidRules          int                                `json:"invalidRules"`
	CacheAvailableRules   int                                `json:"cacheAvailableRules"`
	CacheMissingRules     int                                `json:"cacheMissingRules"`
	MissingExitPlanRules  int                                `json:"missingExitPlanRules"`
	ActivatedRules        int                                `json:"activatedRules"`
	ClosedRules           int                                `json:"closedRules"`
	CausalityViolations   int                                `json:"causalityViolations"`
	TPlusOneViolations    int                                `json:"tPlusOneViolations"`
	DeterminismViolations int                                `json:"determinismViolations"`
	Deterministic         bool                               `json:"deterministic"`
	ResultHash            string                             `json:"resultHash"`
	RepeatedResultHash    string                             `json:"repeatedResultHash"`
	SummaryVersionCount   map[string]int                     `json:"summaryVersionCount"`
	OutcomeCount          map[string]int                     `json:"outcomeCount"`
	Results               []LegacyStructuredRuleReplayResult `json:"results"`
}

var legacyStructuredRuleReplayMu sync.Mutex

// ReplayLegacyStructuredRulesCacheOnly runs every selected historical rule
// through the actual recommendation execution builder twice. It performs no
// inserts, updates, deletes, provider calls, or backtest-result persistence.
//
// The database must be the currently configured db.Dao because the production
// execution path deliberately uses the shared cache repositories. CLI callers
// open that database read-only; tests use an isolated database.
func ReplayLegacyStructuredRulesCacheOnly(ctx context.Context, database *gorm.DB, options LegacyStructuredRuleReplayOptions) (LegacyStructuredRuleReplayReport, error) {
	legacyStructuredRuleReplayMu.Lock()
	defer legacyStructuredRuleReplayMu.Unlock()

	empty := LegacyStructuredRuleReplayReport{}
	if database == nil || db.Dao == nil {
		return empty, errors.New("legacy structured-rule replay requires an initialized database")
	}
	if database != db.Dao {
		return empty, errors.New("legacy structured-rule replay database must match db.Dao")
	}
	if !options.From.IsZero() && !options.To.IsZero() && options.To.Before(options.From) {
		return empty, errors.New("legacy structured-rule replay end date precedes start date")
	}

	records, err := loadLegacyStructuredRuleReplayRecords(ctx, database, options)
	if err != nil {
		return empty, err
	}
	if options.ExpectedRuleCount > 0 && len(records) != options.ExpectedRuleCount {
		return empty, fmt.Errorf("legacy structured-rule corpus count = %d, want %d", len(records), options.ExpectedRuleCount)
	}

	asOf, observedDays, err := loadLegacyStructuredRuleReplayClock(ctx, database, records)
	if err != nil {
		return empty, err
	}
	if asOf.IsZero() {
		return empty, errors.New("legacy structured-rule replay has no cached minute-bar clock")
	}

	restoreCalendar := installObservedReplayCalendar(observedDays, records, asOf)
	defer restoreCalendar()

	first := replayLegacyStructuredRulePass(records, asOf)
	second := replayLegacyStructuredRulePass(records, asOf)
	first.RepeatedResultHash = second.ResultHash
	first.Deterministic = first.ResultHash != "" && first.ResultHash == second.ResultHash
	if !first.Deterministic {
		first.DeterminismViolations++
	}
	return first, nil
}

func loadLegacyStructuredRuleReplayRecords(ctx context.Context, database *gorm.DB, options LegacyStructuredRuleReplayOptions) ([]models.AiRecommendStocks, error) {
	rows := make([]models.AiRecommendStocks, 0, 256)
	query := database.WithContext(ctx).Model(&models.AiRecommendStocks{}).
		Where("TRIM(COALESCE(activation_rule_json, '')) <> ''").
		Where("TRIM(COALESCE(summary_version, '')) <> ?", marketSummaryVersion150)
	if !options.From.IsZero() {
		query = query.Where("COALESCE(data_time, created_at) >= ?", options.From)
	}
	if !options.To.IsZero() {
		query = query.Where("COALESCE(data_time, created_at) < ?", options.To.AddDate(0, 0, 1))
	}
	if err := query.Order("COALESCE(data_time, created_at) ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func replayLegacyStructuredRulePass(records []models.AiRecommendStocks, asOf time.Time) LegacyStructuredRuleReplayReport {
	report := LegacyStructuredRuleReplayReport{
		CacheOnly:           true,
		ProfitabilityProof:  false,
		CalendarSource:      "observed_local_minute_cache",
		AsOf:                asOf.UTC(),
		TotalRules:          len(records),
		SummaryVersionCount: map[string]int{},
		OutcomeCount:        map[string]int{},
		Results:             make([]LegacyStructuredRuleReplayResult, 0, len(records)),
	}
	latestTradeDate := tradingDayStart(asOf)
	buildCtx := yieldBuildContext{
		Force:               true,
		Reason:              "legacy_structured_rule_replay",
		Now:                 asOf,
		InTradingSession:    false,
		LatestTradeDate:     latestTradeDate,
		DisableMinuteFetch:  true,
		CurrentPriceMap:     map[string]float64{},
		CurrentPriceTimeMap: map[string]string{},
	}

	for _, rec := range records {
		version := strings.TrimSpace(rec.SummaryVersion)
		report.SummaryVersionCount[version]++
		result := LegacyStructuredRuleReplayResult{
			RecommendID:    rec.ID,
			SummaryVersion: version,
			StockCode:      normalizeRecommendStockCode(rec.StockCode),
			RuleVersion:    strings.TrimSpace(rec.ActivationRuleVersion),
			CausalityValid: true,
			TPlusOneValid:  true,
			Deterministic:  true,
		}
		_, stopProfitOK := parseStopProfitPrice(rec)
		_, stopLossOK := parseStopLossPrice(rec)
		result.HasExitBoundary = stopProfitOK || stopLossOK
		if !result.HasExitBoundary {
			report.MissingExitPlanRules++
		}

		rule, parseErr := parseActivationRuleJSON(rec.ActivationRuleJSON)
		if parseErr != nil {
			result.Outcome = "invalid_rule"
			result.Reason = parseErr.Error()
			result.InvariantFailure = "rule_parse"
			report.InvalidRules++
			report.OutcomeCount[result.Outcome]++
			report.Results = append(report.Results, result)
			continue
		}
		report.ParsedRules++
		if timelineErr := validateActivationRuleTimelineForPaths(rule, rec); timelineErr != nil {
			result.Outcome = "invalid_timeline"
			result.Reason = timelineErr.Error()
			result.CausalityValid = false
			result.InvariantFailure = "rule_timeline"
			report.CausalityViolations++
			report.OutcomeCount[result.Outcome]++
			report.Results = append(report.Results, result)
			continue
		}

		_, cacheEnd, cacheErr := getMinuteCacheRange(result.StockCode)
		if cacheErr != nil {
			result.InvariantFailure = "cache_query"
			result.Reason = cacheErr.Error()
		}
		result.CacheAvailable = cacheEnd != nil && cacheEnd.After(effectiveLegacyReplayStart(rec, rule))
		if result.CacheAvailable {
			report.CacheAvailableRules++
		} else {
			report.CacheMissingRules++
		}
		state := buildYieldRecordStateFromRecommend(rec, nil, buildCtx)
		result.ActivationStatus = strings.TrimSpace(state.ActivationStatus)
		result.PositionStatus = strings.TrimSpace(state.PositionStatus)
		result.DataStatus = strings.TrimSpace(state.DataStatus)
		result.Reason = strings.TrimSpace(state.DataStatusReason)
		if state.BuyTime != nil && !state.BuyTime.IsZero() {
			activationAt := state.BuyTime.UTC()
			result.ActivationAt = &activationAt
			result.ActivationPrice = state.BuyAmount
			report.ActivatedRules++
			causalFloor := effectiveLegacyReplayStart(rec, rule)
			if state.BuyTime.Before(causalFloor) {
				result.CausalityValid = false
				result.InvariantFailure = "activation_before_rule_validity"
				report.CausalityViolations++
			}
		}
		if state.SellTime != nil && !state.SellTime.IsZero() {
			exitAt := state.SellTime.UTC()
			result.ExitAt = &exitAt
			if state.RealizedSellAmount != nil {
				result.ExitPrice = *state.RealizedSellAmount
			}
			report.ClosedRules++
			if state.BuyTime == nil || state.BuyTime.IsZero() || state.SellTime.Before(resolveNextSellEligibleTime(*state.BuyTime)) {
				result.TPlusOneValid = false
				result.InvariantFailure = appendReplayInvariant(result.InvariantFailure, "t_plus_one")
				report.TPlusOneViolations++
			}
		}
		result.Outcome = classifyLegacyStructuredRuleReplayOutcome(result)
		report.OutcomeCount[result.Outcome]++
		report.Results = append(report.Results, result)
	}

	report.ResultHash = hashLegacyStructuredRuleReplay(report)
	return report
}

func effectiveLegacyReplayStart(rec models.AiRecommendStocks, rule *activationRule) time.Time {
	start := recommendRecordTime(rec)
	if actual := resolveRecommendBuyTime(start); actual.After(start) {
		start = actual
	}
	if rule != nil && rule.ValidFrom.After(start) {
		start = rule.ValidFrom
	}
	if rule != nil {
		for _, path := range activationRulePaths(rule) {
			if path.ValidFrom.After(start) {
				start = path.ValidFrom
			}
		}
	}
	return start
}

func classifyLegacyStructuredRuleReplayOutcome(result LegacyStructuredRuleReplayResult) string {
	if result.InvariantFailure != "" {
		return "invariant_violation"
	}
	if result.ActivationAt != nil && result.ExitAt != nil {
		return "closed"
	}
	if result.ActivationAt != nil {
		return "open"
	}
	if result.ActivationStatus == "ineligible" || result.ActivationStatus == "skipped" {
		return "policy_rejected"
	}
	if !result.HasExitBoundary {
		return "incomplete_exit_plan"
	}
	if !result.CacheAvailable {
		return "cache_missing"
	}
	return "not_triggered"
}

func appendReplayInvariant(current, next string) string {
	if strings.TrimSpace(current) == "" {
		return next
	}
	return current + "," + next
}

func hashLegacyStructuredRuleReplay(report LegacyStructuredRuleReplayReport) string {
	payload := struct {
		AsOf                time.Time
		TotalRules          int
		SummaryVersionCount map[string]int
		OutcomeCount        map[string]int
		Results             []LegacyStructuredRuleReplayResult
	}{report.AsOf, report.TotalRules, report.SummaryVersionCount, report.OutcomeCount, report.Results}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func loadLegacyStructuredRuleReplayClock(ctx context.Context, database *gorm.DB, records []models.AiRecommendStocks) (time.Time, []string, error) {
	var legacyLatest models.AiRecommendMinuteBar
	legacyErr := database.WithContext(ctx).Model(&models.AiRecommendMinuteBar{}).Order("trade_time DESC").Limit(1).Find(&legacyLatest).Error
	if legacyErr != nil && !errors.Is(legacyErr, gorm.ErrRecordNotFound) {
		return time.Time{}, nil, legacyErr
	}
	latest := legacyLatest.TradeTime
	days := make([]string, 0, 256)
	if database.Migrator().HasTable(&models.AiRecommendMinuteBar{}) {
		var legacyDays []string
		if err := database.WithContext(ctx).Raw("SELECT DISTINCT substr(CAST(trade_time AS TEXT), 1, 10) AS day FROM ai_recommend_minute_bar WHERE trade_time IS NOT NULL ORDER BY day").Scan(&legacyDays).Error; err != nil {
			return time.Time{}, nil, err
		}
		days = append(days, legacyDays...)
	}

	if db.MinuteDao != nil && db.MinuteDao.Migrator().HasTable(&minuteCacheDBBar{}) {
		var latestMillis int64
		if err := db.MinuteDao.WithContext(ctx).Raw("SELECT COALESCE(MAX(trade_time), 0) FROM minute_bar").Scan(&latestMillis).Error; err != nil {
			return time.Time{}, nil, err
		}
		if minuteLatest := minuteTimeFromMillis(latestMillis); minuteLatest.After(latest) {
			latest = minuteLatest
		}
		var minuteDays []string
		if err := db.MinuteDao.WithContext(ctx).Raw("SELECT DISTINCT date(trade_time / 1000, 'unixepoch', '+8 hours') AS day FROM minute_bar ORDER BY day").Scan(&minuteDays).Error; err != nil {
			return time.Time{}, nil, err
		}
		days = append(days, minuteDays...)
	}

	if latest.IsZero() {
		for _, rec := range records {
			if at := recommendRecordTime(rec); at.After(latest) {
				latest = at
			}
		}
	}
	days = dedupeReplayDays(days)
	return latest.In(cnLocation()), days, nil
}

func dedupeReplayDays(days []string) []string {
	seen := make(map[string]struct{}, len(days))
	out := make([]string, 0, len(days))
	for _, day := range days {
		day = strings.TrimSpace(day)
		if len(day) != len("2006-01-02") {
			continue
		}
		if _, ok := seen[day]; ok {
			continue
		}
		seen[day] = struct{}{}
		out = append(out, day)
	}
	sort.Strings(out)
	return out
}

func installObservedReplayCalendar(days []string, records []models.AiRecommendStocks, asOf time.Time) func() {
	loc := cnLocation()
	start, end := asOf, asOf
	openDays := make(map[string]bool, len(days))
	for _, raw := range days {
		day, err := time.ParseInLocation(time.DateOnly, raw, loc)
		if err != nil {
			continue
		}
		openDays[raw] = true
		if day.Before(start) {
			start = day
		}
		if day.After(end) {
			end = day
		}
	}
	for _, rec := range records {
		if at := recommendRecordTime(rec); !at.IsZero() && at.Before(start) {
			start = at
		}
	}
	start = tradingDayStart(start).AddDate(-1, 0, 0)
	end = tradingDayStart(end).AddDate(1, 0, 0)

	globalCNTradeCalCache.mu.Lock()
	oldStart, oldEnd := globalCNTradeCalCache.startDay, globalCNTradeCalCache.endDay
	oldOpenDays := globalCNTradeCalCache.openDays
	oldLoadedAt, oldLastError := globalCNTradeCalCache.loadedAt, globalCNTradeCalCache.lastError
	globalCNTradeCalCache.startDay = start
	globalCNTradeCalCache.endDay = end
	globalCNTradeCalCache.openDays = openDays
	globalCNTradeCalCache.loadedAt = time.Now()
	globalCNTradeCalCache.lastError = ""
	globalCNTradeCalCache.mu.Unlock()

	return func() {
		globalCNTradeCalCache.mu.Lock()
		globalCNTradeCalCache.startDay = oldStart
		globalCNTradeCalCache.endDay = oldEnd
		globalCNTradeCalCache.openDays = oldOpenDays
		globalCNTradeCalCache.loadedAt = oldLoadedAt
		globalCNTradeCalCache.lastError = oldLastError
		globalCNTradeCalCache.mu.Unlock()
	}
}
