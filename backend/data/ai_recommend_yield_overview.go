package data

import (
	"database/sql"
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
	"sort"
	"strings"
	"sync"
	"time"
)

func (s *AiRecommendStocksService) GetAiRecommendYieldDailyOverview(query *models.AiRecommendStocksQuery) (*models.AiRecommendYieldDailyOverviewData, error) {
	if query == nil {
		query = &models.AiRecommendStocksQuery{}
	}
	query.StrategyCohort = normalizeStrategyCohort(query.StrategyCohort, strategyCohortAll)
	if isV150YieldDailyOverviewCohort(query.StrategyCohort) {
		return s.buildV150YieldDailyOverview(query)
	}

	signature, err := buildYieldDailyOverviewSignature(query)
	if err != nil {
		return nil, err
	}
	useMutableCache := !isV150CostVersion(query.StrategyCohort)
	if useMutableCache {
		if cached, ok := loadYieldDailyOverviewCache(signature); ok {
			return cached, nil
		}
	}

	result, err := s.buildYieldDailyOverview(query)
	if err != nil {
		return nil, err
	}
	if useMutableCache {
		storeYieldDailyOverviewCache(signature, result)
	}
	return cloneYieldDailyOverviewData(result), nil
}

func isV150YieldDailyOverviewCohort(cohort string) bool {
	normalized := normalizeStrategyCohort(cohort, strategyCohortAll)
	return normalized == marketSummaryVersion150 ||
		(normalized == strategyCohortCurrent && marketSummaryCurrentVersion == marketSummaryVersion150)
}

func newV150YieldDailyOverview() *models.AiRecommendYieldDailyOverviewData {
	cfg := v150.FixedStrategyV150Config()
	return &models.AiRecommendYieldDailyOverviewData{
		CalcMode:           aiRecommendYieldModeStrict,
		BenchmarkCode:      defaultBenchmarkModelCode,
		BenchmarkName:      defaultBenchmarkName,
		StrategyCohort:     marketSummaryVersion150,
		ValidationStatus:   "forward_validation",
		PortfolioCapital:   cfg.PortfolioCash,
		Warnings:           []string{},
		V150HealthWarnings: []string{},
		Points:             []models.AiRecommendYieldDailyOverviewPoint{},
	}
}

const (
	v150YieldDailyRawBenchmarkPriceHealthCode = "benchmark_510300_raw_minute_close_missing"
	// The current schema has no authoritative exchange calendar. The daily
	// curve uses the union of cached qfq dates, raw 510300 observations and
	// mandatory fill/exit/valuation dates; a day absent from every cache cannot
	// be proven closed or missing. Keep that limitation observable even when all
	// inferred dates are complete.
	v150YieldDailyCalendarCoverageHealthCode = "trading_calendar_coverage_unverified"
)

func (s *AiRecommendStocksService) buildV150YieldDailyOverview(
	query *models.AiRecommendStocksQuery,
) (*models.AiRecommendYieldDailyOverviewData, error) {
	return s.buildV150YieldDailyOverviewAt(query, timeNow().In(cnLocation()))
}

func (s *AiRecommendStocksService) buildV150YieldDailyOverviewAt(
	query *models.AiRecommendStocksQuery,
	now time.Time,
) (*models.AiRecommendYieldDailyOverviewData, error) {
	if err := validateV150YieldDailyOverviewQuery(query); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, errors.New("V1.5 daily portfolio asOf is required")
	}
	now = now.In(cnLocation())
	result := newV150YieldDailyOverview()
	if runWarnings, runWarningErr := loadV150ImmutableRunHealthWarnings(30); runWarningErr != nil {
		logger.SugaredLogger.Warnf("load v1.5 immutable run health warnings failed: %v", runWarningErr)
	} else {
		appendV150YieldDailyHealthWarnings(result, runWarnings...)
	}
	validation, validationWarnings, validationErr := loadV150ForwardValidationWithHealthAsOf(now)
	applyV150YieldDailyValidation(result, validation, validationWarnings, validationErr)
	// A fixed-capital portfolio is meaningful only for the complete immutable
	// cohort. Row/date filters are rejected above and can never shrink the
	// population used to construct the 100,000-yuan account.
	views, err := loadV150YieldLedgerViews(nil, now)
	if err != nil {
		result.Warnings = append(result.Warnings, "V1.5 frozen rule/order ledger is unavailable; daily curve was not published")
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, v150YieldDailyLedgerMissingHealthCode), 0)
		return result, nil
	}
	result.TotalRecordCount = len(views)
	if len(views) == 0 {
		result.Warnings = append(result.Warnings, "No frozen V1.5 entry rules are available for the requested window")
		return result, nil
	}

	ledgers, ledgerWarnings := loadV150YieldDailyOrderLedgersByRule(views, now)
	if len(ledgerWarnings) > 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "At least one frozen V1.5 rule ledger is missing or invalid; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, ledgerWarnings...)
		return result, nil
	}

	valuationDay := resolveV150YieldDailyLatestCompleteDay(now)
	entries := make([]yieldDailyOverviewEntry, 0, len(views))
	entryWarnings := make([]string, 0)
	for _, view := range views {
		status := strings.ToLower(strings.TrimSpace(string(view.Current.Lifecycle.Status)))
		if status != "holding" && status != "closed" {
			continue
		}
		ruleID := strings.TrimSpace(view.Current.Frozen.RuleID)
		ledger, ok := ledgers[ruleID]
		if !ok {
			entryWarnings = append(entryWarnings, ruleID+":"+v150YieldDailyLedgerMissingHealthCode)
			continue
		}
		entry, ok := buildV150YieldDailyOverviewEntryFromView(view, ledger, valuationDay)
		if !ok {
			entryWarnings = append(entryWarnings, ruleID+":"+v150YieldDailyLedgerMissingHealthCode)
			continue
		}
		entries = append(entries, entry)
	}
	if len(entryWarnings) > 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "At least one filled V1.5 rule could not be reconstructed from its ledger; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, entryWarnings...)
		return result, nil
	}
	result.IncludedRecordCount = len(entries)
	result.SkippedRecordCount = result.TotalRecordCount - result.IncludedRecordCount
	if len(entries) == 0 {
		result.Warnings = append(result.Warnings, "No V1.5 rule has a legal sealed fill in the requested window")
		return result, nil
	}

	startDay, endDay, ok := resolveYieldDailyOverviewWindow(entries)
	if !ok {
		result.Warnings = append(result.Warnings, "The sealed V1.5 ledger does not define a valid daily valuation window")
		appendV150YieldDailyHealthWarnings(result, v150YieldDailyLedgerMissingHealthCode)
		return result, nil
	}
	if valuationDay.After(endDay) {
		endDay = valuationDay
	}
	dailyCalendarDays, _, dailyCalendarErr := loadYieldDailyOverviewTradingDaysFromCache(startDay, endDay)
	rawObservedDays, rawObservedErr := loadV150YieldDailyBenchmarkObservedDays(startDay, endDay)
	if dailyCalendarErr != nil && rawObservedErr != nil {
		result.Warnings = append(result.Warnings, "The cache-only 510300 trading-day sources are unavailable; daily curve was not published")
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, v150BenchmarkDailySeriesHealthCode), 0)
		return result, nil
	}
	tradingDays := mergeV150YieldDailyTradeDays(
		dailyCalendarDays,
		rawObservedDays,
		collectV150YieldDailyRequiredDays(entries, valuationDay),
	)
	if len(tradingDays) == 0 {
		result.Warnings = append(result.Warnings, "No cache-only 510300 trading day is available for the V1.5 valuation window")
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, v150BenchmarkDailySeriesHealthCode), 0)
		return result, nil
	}
	benchmarkSeries, benchmarkCoverageWarnings, err := loadV150YieldDailyRawBenchmarkSeries(tradingDays)
	if err != nil {
		result.Warnings = append(result.Warnings, "Raw 510300 closing minutes are unavailable; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, v150YieldDailyRawBenchmarkPriceHealthCode)
		return result, nil
	}
	if len(benchmarkCoverageWarnings) > 0 {
		result.Warnings = append(result.Warnings, "The raw 510300 14:45-15:00 series has a required-date gap; daily curve was not published")
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, benchmarkCoverageWarnings...), 0)
		return result, nil
	}

	priceSeriesMap, missingCodes, provenanceWarnings, err := loadV150YieldDailyRawMinutePriceSeries(entries, tradingDays)
	if err != nil {
		result.Warnings = append(result.Warnings, "Raw 15-minute holding marks are unavailable; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, v150YieldDailyRawMinutePriceHealthCode)
		return result, nil
	}
	if len(provenanceWarnings) > 0 {
		result.Warnings = append(result.Warnings, "At least one V1.5 holding minute has ambiguous or adjusted provenance; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, provenanceWarnings...)
		return result, nil
	}
	priceWarnings := collectV150YieldDailyPriceGapWarnings(entries, tradingDays, priceSeriesMap)
	for _, code := range missingCodes {
		priceWarnings = append(priceWarnings, normalizeRecommendStockCode(code)+":"+v150YieldDailyRawMinutePriceHealthCode)
	}
	priceWarnings = dedupeNonEmptyStrings(priceWarnings, 0)
	if len(priceWarnings) > 0 {
		result.Warnings = append(result.Warnings, "At least one required raw 14:45-15:00 holding bar is missing; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, priceWarnings...)
		return result, nil
	}
	if !applyV150YieldDailyClosingMarks(entries, priceSeriesMap) {
		result.Warnings = append(result.Warnings, "At least one current V1.5 holding has no complete raw closing mark; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, v150YieldDailyRawMinutePriceHealthCode)
		return result, nil
	}

	benchmarkMatchedSeries, benchmarkWarnings := buildV150YieldDailyBenchmarkSeries(entries, tradingDays, benchmarkSeries)
	if len(benchmarkWarnings) > 0 || benchmarkMatchedSeries == nil {
		result.Warnings = append(result.Warnings, "The cache-only 510300 comparison account is incomplete; daily curve was not published")
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, benchmarkWarnings...), 0)
		return result, nil
	}
	points, pointWarnings := buildYieldDailyOverviewPointsWithV150RuleLedgers(
		entries,
		tradingDays,
		priceSeriesMap,
		benchmarkSeries,
		ledgers,
		benchmarkMatchedSeries,
	)
	if len(pointWarnings) > 0 || len(points) != len(tradingDays) {
		result.Warnings = append(result.Warnings, "The sealed-ledger daily portfolio could not be valued completely; daily curve was not published")
		appendV150YieldDailyHealthWarnings(result, append(pointWarnings, v150YieldDailyLedgerAsOfHealthCode)...)
		return result, nil
	}

	result.RangeStart = points[0].TradeDate
	result.RangeEnd = points[len(points)-1].TradeDate
	result.DataAsOf = resolveV150YieldDailyOverviewDataAsOf(entries, result.RangeEnd)
	result.Points = points
	appendV150YieldDailyHealthWarnings(result, v150YieldDailyCalendarCoverageHealthCode)
	appendV150RollingReturnWarning(result)
	return result, nil
}

func applyV150YieldDailyValidation(
	result *models.AiRecommendYieldDailyOverviewData,
	validation *models.StrategyValidationStatus,
	warnings []string,
	err error,
) {
	if result == nil {
		return
	}
	if err != nil {
		result.Warnings = append(result.Warnings, "The complete V1.5 forward-validation cohort is unavailable; validation remains pending")
		appendV150YieldDailyHealthWarnings(result, v150ForwardValidationCohortHealthCode)
		return
	}
	if validation != nil && strings.TrimSpace(validation.Status) != "" {
		result.ValidationStatus = strings.TrimSpace(validation.Status)
	}
	appendV150YieldDailyHealthWarnings(result, warnings...)
}

func appendV150YieldDailyHealthWarnings(result *models.AiRecommendYieldDailyOverviewData, warnings ...string) {
	if result == nil {
		return
	}
	result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, warnings...), 0)
}

func validateV150YieldDailyOverviewQuery(query *models.AiRecommendStocksQuery) error {
	if query == nil {
		return nil
	}
	if strings.TrimSpace(query.ModelName) != "" || strings.TrimSpace(query.StockCode) != "" ||
		strings.TrimSpace(query.StockName) != "" || strings.TrimSpace(query.BkCode) != "" ||
		strings.TrimSpace(query.BkName) != "" || strings.TrimSpace(query.StartDate) != "" ||
		strings.TrimSpace(query.EndDate) != "" {
		return errors.New("V1.5 daily portfolio requires the complete immutable cohort; row and date filters are not supported")
	}
	return nil
}

func resolveV150YieldDailyLatestCompleteDay(now time.Time) time.Time {
	loc := cnLocation()
	local := now.In(loc)
	day := normalizeYieldOverviewTradeDay(resolveYieldReadTradeDate(local, nil))
	marketClose := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)
	if local.Before(marketClose) {
		day = shiftToPrevWeekday(day.AddDate(0, 0, -1))
	}
	return normalizeYieldOverviewTradeDay(day)
}

func (s *AiRecommendStocksService) buildYieldDailyOverview(query *models.AiRecommendStocksQuery) (*models.AiRecommendYieldDailyOverviewData, error) {
	loc := cnLocation()
	now := timeNow().In(loc)
	meta := models.AiRecommendYieldMeta{}
	latestTradeDate := resolveYieldReadTradeDate(now, nil)
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err == nil {
		latestTradeDate = resolveYieldReadTradeDate(now, &meta)
	}
	latestTradeDate = time.Date(latestTradeDate.Year(), latestTradeDate.Month(), latestTradeDate.Day(), 0, 0, 0, 0, loc)
	coverableStart := minuteCoverableStartMinute(latestTradeDate)

	// V1.5 is reconstructed from its immutable order ledger and cache-only
	// prices. The legacy minute download horizon must not hide an older sealed
	// position from the 100,000-yuan portfolio.
	recordCoverableStart := coverableStart
	if isV150CostVersion(query.StrategyCohort) {
		recordCoverableStart = time.Time{}
	}
	records, err := listAiRecommendStocksForYield(query, recordCoverableStart)
	if err != nil {
		return nil, err
	}
	records = collapseRecommendRecordsSameDayByCode(records)

	result := &models.AiRecommendYieldDailyOverviewData{
		CalcMode:           aiRecommendYieldModeStrict,
		BenchmarkCode:      defaultBenchmarkModelCode,
		BenchmarkName:      defaultBenchmarkName,
		StrategyCohort:     query.StrategyCohort,
		TotalRecordCount:   len(records),
		Warnings:           []string{},
		V150HealthWarnings: []string{},
		Points:             []models.AiRecommendYieldDailyOverviewPoint{},
	}
	if isV150CostVersion(query.StrategyCohort) {
		if warnings, warningErr := loadV150ImmutableRunHealthWarnings(30); warningErr != nil {
			logger.SugaredLogger.Warnf("load v1.5 immutable run health warnings failed: %v", warningErr)
		} else {
			result.V150HealthWarnings = warnings
		}
		result.ValidationStatus = "forward_validation"
		if validation, validationErr := loadV150ForwardValidation(); validationErr == nil && validation != nil {
			result.ValidationStatus = validation.Status
		}
		result.PortfolioCapital = 100_000
	}
	if len(records) == 0 {
		result.Warnings = append(result.Warnings, "当前没有可用于统计的推荐记录")
		return result, nil
	}

	recordStateMap, err := loadYieldRecordStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	stateMap, err := loadYieldStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	overrideMap, err := loadYieldOverrideMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	dirtyScope, err := loadDirtyAiRecommendYieldScope(aiRecommendYieldModeStrict)
	if err != nil {
		return nil, err
	}

	items := buildStrictYieldRecordItems(records, recordStateMap, stateMap, overrideMap, dirtyScope, nil)
	v150DailyLedgers := map[uint]v150YieldDailyOrderLedger{}
	if isV150CostVersion(query.StrategyCohort) {
		var ledgerWarnings []string
		v150DailyLedgers, ledgerWarnings = loadV150YieldDailyOrderLedgers(records, now)
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(
			append(result.V150HealthWarnings, ledgerWarnings...),
			collectV150YieldValuationHealthWarnings(items)...,
		), 0)
	}
	entries := make([]yieldDailyOverviewEntry, 0, len(items))
	inactiveSkipped := 0
	for _, item := range items {
		entry, ok := buildYieldDailyOverviewEntry(item)
		if !ok && isV150CostVersion(item.SummaryVersion) {
			if ledger, exists := v150DailyLedgers[item.RecommendID]; exists {
				entry, ok = buildV150YieldDailyOverviewEntryFromLedger(item, ledger)
			}
		}
		if !ok {
			inactiveSkipped += 1
			continue
		}
		entries = append(entries, entry)
	}
	if inactiveSkipped > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("已跳过 %d 条未激活或不可执行记录", inactiveSkipped))
	}
	if len(entries) == 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "当前没有可用于绘图的已激活严格记录")
		return result, nil
	}
	if isV150CostVersion(query.StrategyCohort) {
		entries = reconcileV150YieldDailyOverviewEntriesWithLedger(entries, v150DailyLedgers)
	}
	if isV150CostVersion(query.StrategyCohort) {
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(
			result.V150HealthWarnings,
			collectV150BenchmarkHealthWarnings(entries)...,
		), 0)
	}

	startDay, endDay, ok := resolveYieldDailyOverviewWindow(entries)
	if !ok {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "无法确定全库收益走势的起止时间")
		return result, nil
	}

	var tradingDays []time.Time
	var benchmarkSeries *yieldDailyOverviewPriceSeries
	tradingDays, benchmarkSeries, err = loadYieldDailyOverviewTradingDaysFromCache(startDay, endDay)
	if err != nil {
		if isV150CostVersion(query.StrategyCohort) {
			result.SkippedRecordCount = result.TotalRecordCount
			result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, v150BenchmarkDailySeriesHealthCode), 0)
			result.Warnings = append(result.Warnings, "V1.5 基准缓存不可用，未生成组合净值")
			return result, nil
		}
		return nil, err
	}
	if len(tradingDays) == 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "当前窗口没有可用交易日，无法生成收益走势")
		return result, nil
	}

	var priceSeriesMap map[string]*yieldDailyOverviewPriceSeries
	var missingCodes []string
	var provenanceWarnings []string
	if isV150CostVersion(query.StrategyCohort) {
		priceSeriesMap, missingCodes, provenanceWarnings, err = loadV150YieldDailyRawMinutePriceSeries(entries, tradingDays)
	} else {
		priceSeriesMap, missingCodes, err = loadYieldDailyOverviewPriceSeriesFromCache(entries, tradingDays)
	}
	if err != nil {
		return nil, err
	}
	if len(provenanceWarnings) > 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "V1.5 minute provenance is ambiguous or adjusted; portfolio NAV was not published")
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, provenanceWarnings...), 0)
		return result, nil
	}

	filteredEntries := make([]yieldDailyOverviewEntry, 0, len(entries))
	for _, entry := range entries {
		// A V1.5 position remains part of the fixed 100,000-yuan account even
		// when its mark is missing. The point builder must omit that entire day;
		// dropping the position here would publish a deceptively partial NAV.
		if _, ok := priceSeriesMap[entry.StockCode]; !ok && !isV150CostVersion(entry.SummaryVersion) {
			continue
		}
		filteredEntries = append(filteredEntries, entry)
	}
	if len(missingCodes) > 0 {
		if isV150CostVersion(query.StrategyCohort) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%d 只股票缺少完整原始分钟收盘；受影响的组合交易日已省略：%s", len(missingCodes), strings.Join(missingCodes, "、")))
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%d 只股票缺少日线数据，已从全库走势中跳过：%s", len(missingCodes), strings.Join(missingCodes, "、")))
		}
	}
	if len(filteredEntries) == 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "全部候选股票都缺少可用日线数据")
		return result, nil
	}
	if gapCount := countYieldDailyOverviewPriceGaps(filteredEntries, tradingDays, priceSeriesMap); gapCount > 0 {
		if isV150CostVersion(query.StrategyCohort) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("原始分钟收盘存在 %d 个点时缺口；受影响组合日已省略", gapCount))
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf("日线存在 %d 个点时缺口；已拒绝使用旧收盘价静默前填", gapCount))
		}
	}

	if isV150CostVersion(query.StrategyCohort) {
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(
			result.V150HealthWarnings,
			collectV150YieldDailyPriceGapWarnings(filteredEntries, tradingDays, priceSeriesMap)...,
		), 0)
	}

	points, pointWarnings := buildYieldDailyOverviewPointsWithV150Ledgers(
		filteredEntries,
		tradingDays,
		priceSeriesMap,
		benchmarkSeries,
		v150DailyLedgers,
	)
	if isV150CostVersion(query.StrategyCohort) {
		result.V150HealthWarnings = dedupeNonEmptyStrings(append(result.V150HealthWarnings, pointWarnings...), 0)
	}
	if len(points) == 0 {
		if isV150CostVersion(query.StrategyCohort) {
			result.IncludedRecordCount = len(filteredEntries)
			result.SkippedRecordCount = result.TotalRecordCount - result.IncludedRecordCount
		} else {
			result.SkippedRecordCount = result.TotalRecordCount
		}
		result.Warnings = append(result.Warnings, "未能生成有效的按交易日收益点位")
		return result, nil
	}

	result.RangeStart = points[0].TradeDate
	result.RangeEnd = points[len(points)-1].TradeDate
	result.DataAsOf = resolveYieldDailyOverviewDataAsOf(filteredEntries, result.RangeEnd)
	result.IncludedRecordCount = len(filteredEntries)
	result.SkippedRecordCount = result.TotalRecordCount - result.IncludedRecordCount
	result.Points = points
	appendV150RollingReturnWarning(result)
	return result, nil
}

func appendV150RollingReturnWarning(result *models.AiRecommendYieldDailyOverviewData) {
	if result == nil || !isV150CostVersion(result.StrategyCohort) || len(result.Points) < 21 {
		return
	}
	start := result.Points[len(result.Points)-21].PortfolioEquity
	end := result.Points[len(result.Points)-1].PortfolioEquity
	if start <= 0 || end >= start {
		return
	}
	changePct := (end/start - 1) * 100
	result.Warnings = append(result.Warnings, fmt.Sprintf("V1.5.0 最近20个交易日组合净收益为 %.2f%%，已触发滚动负收益告警", changePct))
}

func buildYieldDailyOverviewSignature(query *models.AiRecommendStocksQuery) (string, error) {
	type tableStamp struct {
		Count int64
		MaxAt time.Time
	}

	loadStamp := func(model any) (tableStamp, error) {
		stamp := tableStamp{}
		if err := db.Dao.Model(model).Count(&stamp.Count).Error; err != nil {
			return stamp, err
		}
		var maxAtRaw sql.NullString
		if err := db.Dao.Model(model).Select("MAX(updated_at)").Scan(&maxAtRaw).Error; err != nil {
			return stamp, err
		}
		if ts, ok := parseYieldDailyOverviewTimestamp(maxAtRaw.String); ok {
			stamp.MaxAt = ts
		}
		return stamp, nil
	}

	recordStamp, err := loadStamp(&models.AiRecommendYieldRecordState{})
	if err != nil {
		return "", err
	}
	recommendStamp, err := loadStamp(&models.AiRecommendStocks{})
	if err != nil {
		return "", err
	}
	overrideStamp, err := loadStamp(&models.AiRecommendYieldOverride{})
	if err != nil {
		return "", err
	}
	strategyRunStamp := tableStamp{}
	if isV150CostVersion(query.StrategyCohort) && db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) {
		strategyRuns := db.Dao.Model(&models.StrategyRunSnapshot{}).
			Where("strategy_version = ? AND mode = ? AND frozen_at IS NOT NULL", marketSummaryVersion150, "production")
		if err := strategyRuns.Count(&strategyRunStamp.Count).Error; err != nil {
			return "", err
		}
		var maxDecisionAt sql.NullString
		if err := strategyRuns.Select("MAX(decision_at)").Scan(&maxDecisionAt).Error; err != nil {
			return "", err
		}
		if ts, ok := parseYieldDailyOverviewTimestamp(maxDecisionAt.String); ok {
			strategyRunStamp.MaxAt = ts
		}
	}
	return fmt.Sprintf(
		"cohort:%s|record:%d:%d|recommend:%d:%d|override:%d:%d|v150run:%d:%d",
		normalizeStrategyCohort(query.StrategyCohort, strategyCohortAll),
		recordStamp.Count,
		recordStamp.MaxAt.UnixNano(),
		recommendStamp.Count,
		recommendStamp.MaxAt.UnixNano(),
		overrideStamp.Count,
		overrideStamp.MaxAt.UnixNano(),
		strategyRunStamp.Count,
		strategyRunStamp.MaxAt.UnixNano(),
	), nil
}

func loadYieldDailyOverviewCache(signature string) (*models.AiRecommendYieldDailyOverviewData, bool) {
	aiRecommendYieldDailyOverviewCache.mu.RLock()
	defer aiRecommendYieldDailyOverviewCache.mu.RUnlock()
	if aiRecommendYieldDailyOverviewCache.signature != signature || aiRecommendYieldDailyOverviewCache.data == nil {
		return nil, false
	}
	return cloneYieldDailyOverviewData(aiRecommendYieldDailyOverviewCache.data), true
}

func storeYieldDailyOverviewCache(signature string, data *models.AiRecommendYieldDailyOverviewData) {
	aiRecommendYieldDailyOverviewCache.mu.Lock()
	defer aiRecommendYieldDailyOverviewCache.mu.Unlock()
	aiRecommendYieldDailyOverviewCache.signature = signature
	aiRecommendYieldDailyOverviewCache.data = cloneYieldDailyOverviewData(data)
}

func cloneYieldDailyOverviewData(data *models.AiRecommendYieldDailyOverviewData) *models.AiRecommendYieldDailyOverviewData {
	if data == nil {
		return nil
	}
	cloned := *data
	if data.Warnings != nil {
		cloned.Warnings = append([]string(nil), data.Warnings...)
	}
	if data.V150HealthWarnings != nil {
		cloned.V150HealthWarnings = append([]string(nil), data.V150HealthWarnings...)
	}
	if data.Points != nil {
		cloned.Points = append([]models.AiRecommendYieldDailyOverviewPoint(nil), data.Points...)
	}
	return &cloned
}

func buildYieldDailyOverviewEntry(item models.AiRecommendStocksYieldItem) (yieldDailyOverviewEntry, bool) {
	if strings.TrimSpace(item.BacktestEligibility) != "" && strings.TrimSpace(item.BacktestEligibility) != recommendBacktestEligible {
		return yieldDailyOverviewEntry{}, false
	}
	if strings.TrimSpace(item.ActivationStatus) != "activated" {
		return yieldDailyOverviewEntry{}, false
	}
	if item.BuyAmount <= 0 {
		return yieldDailyOverviewEntry{}, false
	}
	buyTime, ok := resolveYieldDailyOverviewBuyTime(item)
	if !ok {
		return yieldDailyOverviewEntry{}, false
	}
	market := resolveTradingMarket(item.StockCode)
	buyCost := calcBuyTradeCostForVersion(item.SummaryVersion, item.BuyAmount, market)
	if buyCost.NetAmount <= 0 {
		return yieldDailyOverviewEntry{}, false
	}

	entry := yieldDailyOverviewEntry{
		RecommendID:               item.RecommendID,
		SummaryVersion:            strings.TrimSpace(item.SummaryVersion),
		StockCode:                 normalizeRecommendStockCode(item.StockCode),
		StockName:                 strings.TrimSpace(item.StockName),
		BuyTime:                   buyTime,
		BuyDay:                    normalizeYieldOverviewTradeDay(buyTime),
		BuyAmount:                 round2(item.BuyAmount),
		CurrentPrice:              round2(item.CurrentPrice),
		BuyCostNet:                round2(buyCost.NetAmount),
		CurrentPriceTime:          strings.TrimSpace(item.CurrentPriceTime),
		SellTime:                  strings.TrimSpace(item.SellTime),
		V150LedgerAccountingReady: item.V150LedgerAccountingReady,
		V150LedgerClosed:          item.V150LedgerClosed,
		V150LedgerQuantity:        item.V150LedgerQuantity,
		V150LedgerCorporateCash:   item.V150LedgerCorporateCash,
	}
	if item.SellAmount != nil && *item.SellAmount > 0 {
		entry.SellAmount = round2(*item.SellAmount)
		entry.HasSellAmount = true
		sellNet := calcSellTradeCostForVersion(item.SummaryVersion, item.BuyAmount, *item.SellAmount, market)
		entry.RealizedValueNet = round2(sellNet.NetAmount)
		if sellTime, ok := parseYieldOverviewDisplayTime(item.SellTime); ok {
			entry.SellDay = normalizeYieldOverviewTradeDay(sellTime)
		}
	}
	if isV150CostVersion(item.SummaryVersion) && item.V150LedgerAccountingReady {
		if item.V150LedgerEntryCash > 0 {
			entry.BuyCostNet = round2(item.V150LedgerEntryCash)
		}
		if item.V150LedgerClosed && item.V150LedgerNetValue > 0 {
			entry.RealizedValueNet = round2(item.V150LedgerNetValue)
		}
	}
	if currentTime, ok := parseYieldOverviewDisplayTime(item.CurrentPriceTime); ok {
		entry.CurrentDay = normalizeYieldOverviewTradeDay(currentTime)
	}
	if entry.CurrentDay.IsZero() {
		entry.CurrentDay = normalizeYieldOverviewTradeDay(timeNow().In(cnLocation()))
	}
	if !entry.HasSellAmount && entry.CurrentPrice <= 0 && !isV150CostVersion(entry.SummaryVersion) {
		entry.CurrentPrice = entry.BuyAmount
	}
	if !entry.SellDay.IsZero() && entry.SellDay.Before(entry.BuyDay) {
		return yieldDailyOverviewEntry{}, false
	}
	if entry.StockCode == "" {
		return yieldDailyOverviewEntry{}, false
	}
	return entry, true
}

func resolveYieldDailyOverviewBuyTime(item models.AiRecommendStocksYieldItem) (time.Time, bool) {
	if buyTime, ok := parseYieldOverviewDisplayTime(item.BuyTime); ok {
		return buyTime, true
	}
	if buyTime, ok := parseYieldOverviewDisplayTime(item.ActivationTime); ok {
		return buyTime, true
	}
	return parseYieldOverviewDisplayTime(item.SignalTime)
}

func resolveYieldDailyOverviewWindow(entries []yieldDailyOverviewEntry) (time.Time, time.Time, bool) {
	if len(entries) == 0 {
		return time.Time{}, time.Time{}, false
	}
	var startDay time.Time
	var endDay time.Time
	for _, entry := range entries {
		if startDay.IsZero() || entry.BuyDay.Before(startDay) {
			startDay = entry.BuyDay
		}
		candidate := entry.CurrentDay
		if entry.HasSellAmount && !entry.SellDay.IsZero() {
			candidate = entry.SellDay
		}
		if candidate.IsZero() {
			candidate = entry.BuyDay
		}
		if endDay.IsZero() || candidate.After(endDay) {
			endDay = candidate
		}
	}
	if startDay.IsZero() || endDay.IsZero() || endDay.Before(startDay) {
		return time.Time{}, time.Time{}, false
	}
	return startDay, endDay, true
}

func loadYieldDailyOverviewTradingDays(startDay, endDay time.Time) ([]time.Time, *yieldDailyOverviewPriceSeries, error) {
	return loadYieldDailyOverviewTradingDaysWithRemote(startDay, endDay, true)
}

func loadYieldDailyOverviewTradingDaysFromCache(startDay, endDay time.Time) ([]time.Time, *yieldDailyOverviewPriceSeries, error) {
	return loadYieldDailyOverviewTradingDaysWithRemote(startDay, endDay, false)
}

func loadYieldDailyOverviewTradingDaysWithRemote(startDay, endDay time.Time, allowRemote bool) ([]time.Time, *yieldDailyOverviewPriceSeries, error) {
	var bars []dailyBar
	var err error
	if allowRemote {
		klineDays := estimateYieldDailyOverviewKlineDays(startDay, endDay)
		bars, err = loadDailyBarsWithCache(defaultBenchmarkModelCode, defaultBenchmarkCode, startDay, endDay, klineDays)
	} else {
		bars, err = listDailyBarsFromCache(defaultBenchmarkModelCode, startDay, endDay)
	}
	if err != nil {
		return nil, nil, err
	}
	if len(bars) == 0 {
		return nil, nil, errors.New("读取沪深300ETF日线失败")
	}

	days, benchmark := buildYieldDailyOverviewBenchmarkSeries(bars, startDay, endDay)
	return days, benchmark, nil
}

// buildYieldDailyOverviewBenchmarkSeries intentionally keeps only dates backed
// by an observed benchmark close. Synthesising a weekday or carrying the prior
// close forward would make stale 510300 data look current and contaminate the
// portfolio curve and V1.5.0 forward-validation excess returns.
func buildYieldDailyOverviewBenchmarkSeries(
	bars []dailyBar,
	startDay, endDay time.Time,
) ([]time.Time, *yieldDailyOverviewPriceSeries) {
	days := make([]time.Time, 0, len(bars))
	dayByKey := make(map[string]time.Time, len(bars))
	closeByDay := make(map[string]float64, len(bars))
	for _, bar := range bars {
		day := normalizeDailyTradeDate(bar.TradeDate)
		if day.IsZero() || day.Before(startDay) || day.After(endDay) || bar.Close <= 0 {
			continue
		}
		key := day.Format("2006-01-02")
		dayByKey[key] = day
		closeByDay[key] = round2(bar.Close)
	}
	if len(dayByKey) == 0 {
		return nil, nil
	}
	for _, day := range dayByKey {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	return days, &yieldDailyOverviewPriceSeries{
		Code:       defaultBenchmarkModelCode,
		CloseByDay: closeByDay,
	}
}

func loadYieldDailyOverviewPriceSeries(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
) (map[string]*yieldDailyOverviewPriceSeries, []string, error) {
	return loadYieldDailyOverviewPriceSeriesWithRemote(entries, tradingDays, true)
}

func loadYieldDailyOverviewPriceSeriesFromCache(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
) (map[string]*yieldDailyOverviewPriceSeries, []string, error) {
	return loadYieldDailyOverviewPriceSeriesWithRemote(entries, tradingDays, false)
}

func loadYieldDailyOverviewPriceSeriesWithRemote(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	allowRemote bool,
) (map[string]*yieldDailyOverviewPriceSeries, []string, error) {
	codeSet := make(map[string]struct{}, len(entries))
	codes := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.StockCode == "" {
			continue
		}
		if _, exists := codeSet[entry.StockCode]; exists {
			continue
		}
		codeSet[entry.StockCode] = struct{}{}
		codes = append(codes, entry.StockCode)
	}
	sort.Strings(codes)

	priceSeriesMap := make(map[string]*yieldDailyOverviewPriceSeries, len(codes))
	missingCodes := make([]string, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	sem := make(chan struct{}, 6)
	startDay := tradingDays[0]
	endDay := tradingDays[len(tradingDays)-1]

	for _, code := range codes {
		wg.Add(1)
		go func(stockCode string) {
			defer wg.Done()

			// Acquire semaphore before processing
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-time.After(30 * time.Second):
				// Timeout to prevent permanent blocking
				select {
				case errCh <- fmt.Errorf("semaphore acquire timeout for stock %s", stockCode):
				default:
				}
				return
			}

			series, err := loadYieldDailyOverviewPriceSeriesByCode(stockCode, startDay, endDay, tradingDays, allowRemote)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if series == nil {
				missingCodes = append(missingCodes, stockCode)
				return
			}
			priceSeriesMap[stockCode] = series
		}(code)
	}
	wg.Wait()

	select {
	case err := <-errCh:
		return nil, nil, err
	default:
	}

	sort.Strings(missingCodes)
	return priceSeriesMap, missingCodes, nil
}

func loadYieldDailyOverviewPriceSeriesByCode(
	stockCode string,
	startDay time.Time,
	endDay time.Time,
	tradingDays []time.Time,
	allowRemote bool,
) (*yieldDailyOverviewPriceSeries, error) {
	quoteCode := toQuoteCode(stockCode)
	if quoteCode == "" {
		return nil, nil
	}

	var bars []dailyBar
	var err error
	if allowRemote {
		klineDays := estimateYieldDailyOverviewKlineDays(startDay, endDay)
		bars, err = loadDailyBarsWithCache(stockCode, quoteCode, startDay, endDay, klineDays)
	} else {
		bars, err = listDailyBarsFromCache(stockCode, startDay, endDay)
	}
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		return nil, nil
	}

	rawCloseByDay := make(map[string]float64, len(bars))
	for _, bar := range bars {
		day := normalizeDailyTradeDate(bar.TradeDate)
		if day.IsZero() || day.Before(startDay) || day.After(endDay) {
			continue
		}
		if bar.Close <= 0 {
			continue
		}
		rawCloseByDay[day.Format("2006-01-02")] = round2(bar.Close)
	}
	if len(rawCloseByDay) == 0 {
		return nil, nil
	}

	closeByDay := make(map[string]float64, len(rawCloseByDay))
	for _, day := range tradingDays {
		key := day.Format("2006-01-02")
		if closePrice, ok := rawCloseByDay[key]; ok && closePrice > 0 {
			closeByDay[key] = round2(closePrice)
		}
	}
	if len(closeByDay) == 0 {
		return nil, nil
	}
	return &yieldDailyOverviewPriceSeries{
		Code:       stockCode,
		CloseByDay: closeByDay,
	}, nil
}

func countYieldDailyOverviewPriceGaps(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
) int {
	count := 0
	for _, entry := range entries {
		series := priceSeriesMap[entry.StockCode]
		if series == nil {
			continue
		}
		for _, tradeDay := range tradingDays {
			if tradeDay.Before(entry.BuyDay) || (entry.HasSellAmount && !entry.SellDay.IsZero() && !tradeDay.Before(entry.SellDay)) {
				continue
			}
			if !entry.CurrentDay.IsZero() && tradeDay.Equal(entry.CurrentDay) && entry.CurrentPrice > 0 {
				continue
			}
			if series.CloseByDay[tradeDay.Format("2006-01-02")] <= 0 {
				count++
			}
		}
	}
	return count
}

func collectV150YieldDailyPriceGapWarnings(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
) []string {
	warnings := make([]string, 0)
	for _, entry := range entries {
		if !isV150CostVersion(entry.SummaryVersion) {
			continue
		}
		series := priceSeriesMap[entry.StockCode]
		for _, tradeDay := range tradingDays {
			if tradeDay.Before(entry.BuyDay) || (entry.HasSellAmount && !entry.SellDay.IsZero() && !tradeDay.Before(entry.SellDay)) {
				continue
			}
			tradeDate := tradeDay.Format(time.DateOnly)
			if !entry.CurrentDay.IsZero() && tradeDay.Equal(entry.CurrentDay) && entry.CurrentPrice > 0 {
				continue
			}
			if series != nil && series.CloseByDay[tradeDate] > 0 {
				continue
			}
			code := normalizeRecommendStockCode(entry.StockCode)
			if code == "" {
				code = fmt.Sprintf("recommend:%d", entry.RecommendID)
			}
			warnings = append(warnings, code+":"+v150YieldDailyRawMinutePriceHealthCode+":"+tradeDate)
		}
	}
	return dedupeNonEmptyStrings(warnings, 0)
}

func loadV150YieldDailyBenchmarkObservedDays(startDay, endDay time.Time) ([]time.Time, error) {
	startDay = normalizeYieldOverviewTradeDay(startDay)
	endDay = normalizeYieldOverviewTradeDay(endDay)
	if startDay.IsZero() || endDay.IsZero() || endDay.Before(startDay) {
		return nil, errors.New("invalid V1.5 benchmark observation window")
	}
	queryStart := startDay.Add(9*time.Hour + 30*time.Minute)
	queryEnd := endDay.Add(15 * time.Hour)
	bars, err := listMinuteBarsFromCache(defaultBenchmarkModelCode, queryStart, queryEnd)
	if err != nil {
		return nil, err
	}
	byDate := make(map[string]time.Time)
	for _, bar := range bars {
		if bar.TradeTime.IsZero() || !marketSummaryV150QuoteTimestampIsInTradingSession(bar.TradeTime) {
			continue
		}
		day := normalizeYieldOverviewTradeDay(bar.TradeTime)
		if day.Before(startDay) || day.After(endDay) {
			continue
		}
		byDate[day.Format(time.DateOnly)] = day
	}
	result := make([]time.Time, 0, len(byDate))
	for _, day := range byDate {
		result = append(result, day)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	return result, nil
}

func collectV150YieldDailyRequiredDays(entries []yieldDailyOverviewEntry, valuationDay time.Time) []time.Time {
	result := make([]time.Time, 0, len(entries)*2+1)
	if !valuationDay.IsZero() {
		result = append(result, normalizeYieldOverviewTradeDay(valuationDay))
	}
	for _, entry := range entries {
		if !entry.BuyDay.IsZero() {
			result = append(result, normalizeYieldOverviewTradeDay(entry.BuyDay))
		}
		if entry.HasSellAmount && !entry.SellDay.IsZero() {
			result = append(result, normalizeYieldOverviewTradeDay(entry.SellDay))
		}
	}
	return result
}

func mergeV150YieldDailyTradeDays(sources ...[]time.Time) []time.Time {
	byDate := make(map[string]time.Time)
	for _, source := range sources {
		for _, raw := range source {
			day := normalizeYieldOverviewTradeDay(raw)
			if day.IsZero() {
				continue
			}
			byDate[day.Format(time.DateOnly)] = day
		}
	}
	result := make([]time.Time, 0, len(byDate))
	for _, day := range byDate {
		result = append(result, day)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	return result
}

func loadV150YieldDailyRawBenchmarkSeries(
	tradingDays []time.Time,
) (*yieldDailyOverviewPriceSeries, []string, error) {
	if len(tradingDays) == 0 {
		return nil, []string{v150YieldDailyRawBenchmarkPriceHealthCode}, nil
	}
	entries := []yieldDailyOverviewEntry{{
		SummaryVersion: v150.StrategyVersion,
		StockCode:      defaultBenchmarkModelCode,
	}}
	seriesByCode, missingCodes, provenanceWarnings, err := loadV150YieldDailyRawMinutePriceSeries(entries, tradingDays)
	if err != nil {
		return nil, nil, err
	}
	series := seriesByCode[defaultBenchmarkModelCode]
	warnings := make([]string, 0)
	for _, code := range missingCodes {
		warnings = append(warnings, normalizeRecommendStockCode(code)+":"+v150YieldDailyRawBenchmarkPriceHealthCode)
	}
	for _, warning := range provenanceWarnings {
		warnings = append(warnings, strings.Replace(
			warning,
			v150YieldDailyRawMinuteProvenanceHealthCode,
			v150BenchmarkMinuteProvenanceHealthCode,
			1,
		))
	}
	for _, day := range tradingDays {
		date := normalizeYieldOverviewTradeDay(day).Format(time.DateOnly)
		if series != nil && series.CloseByDay[date] > 0 {
			continue
		}
		warnings = append(warnings, defaultBenchmarkModelCode+":"+v150YieldDailyRawBenchmarkPriceHealthCode+":"+date)
	}
	return series, dedupeNonEmptyStrings(warnings, 0), nil
}

func applyV150YieldDailyClosingMarks(
	entries []yieldDailyOverviewEntry,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
) bool {
	for index := range entries {
		entry := &entries[index]
		if entry.HasSellAmount {
			continue
		}
		date := entry.CurrentDay.Format(time.DateOnly)
		series := priceSeriesMap[entry.StockCode]
		if series == nil || series.CloseByDay[date] <= 0 {
			return false
		}
		entry.CurrentPrice = series.CloseByDay[date]
		entry.CurrentPriceTime = date + " 15:00:00"
	}
	return true
}

type v150YieldDailyBenchmarkPosition struct {
	RuleID        string
	BuyDay        time.Time
	EndDay        time.Time
	InvestedNet   float64
	Shares        float64
	CashRemainder float64
	EndPrice      float64
}

// buildV150YieldDailyBenchmarkSeries sizes each matched account from the exact
// cached fill/exit minute and marks open benchmark positions from complete raw
// closing minutes. It deliberately avoids both qfq daily prices and the legacy
// RecommendID-indexed path, so rules without a display row remain deterministic.
func buildV150YieldDailyBenchmarkSeries(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeries *yieldDailyOverviewPriceSeries,
) (*benchmarkDailySeries, []string) {
	if len(entries) == 0 || len(tradingDays) == 0 || priceSeries == nil {
		return nil, []string{v150BenchmarkDailySeriesHealthCode}
	}
	warnings := make([]string, 0)
	positions := make([]v150YieldDailyBenchmarkPosition, 0, len(entries))
	seenRules := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		ruleID := strings.TrimSpace(entry.V150RuleID)
		buyPrice, endPrice, endTime, matchReason := resolveV150YieldDailyBenchmarkPrices(entry, priceSeries)
		endDay := normalizeYieldOverviewTradeDay(endTime)
		if matchReason != "" {
			warnings = append(warnings, ruleID+":"+matchReason)
			continue
		}
		if ruleID == "" || entry.BuyCostNet <= 0 || buyPrice <= 0 || endPrice <= 0 || endDay.Before(entry.BuyDay) {
			warnings = append(warnings, ruleID+":"+v150BenchmarkDailySeriesHealthCode)
			continue
		}
		if _, duplicate := seenRules[ruleID]; duplicate {
			warnings = append(warnings, ruleID+":"+v150BenchmarkPartialHealthCode)
			continue
		}
		seenRules[ruleID] = struct{}{}
		buy := calcBenchmarkETFBuyTradeForVersion(v150.StrategyVersion, entry.BuyCostNet, buyPrice)
		if !buy.Valid || buy.Shares <= 0 {
			warnings = append(warnings, ruleID+":"+v150BenchmarkDailySeriesHealthCode)
			continue
		}
		positions = append(positions, v150YieldDailyBenchmarkPosition{
			RuleID: ruleID, BuyDay: entry.BuyDay, EndDay: endDay,
			InvestedNet: entry.BuyCostNet, Shares: buy.Shares,
			CashRemainder: buy.UnusedCash, EndPrice: endPrice,
		})
	}
	if len(warnings) > 0 || len(positions) != len(entries) {
		return nil, dedupeNonEmptyStrings(warnings, 0)
	}

	valueByDay := make(map[string]float64, len(tradingDays))
	cumulativeAmountByDay := make(map[string]float64, len(tradingDays))
	dailyAmountByDay := make(map[string]float64, len(tradingDays))
	cumulativeRateByDay := make(map[string]float64, len(tradingDays))
	dailyRateByDay := make(map[string]float64, len(tradingDays))
	navByDay := make(map[string]float64, len(tradingDays))
	previousCumulativeAmount := 0.0
	for _, tradeDay := range tradingDays {
		tradeDate := tradeDay.Format(time.DateOnly)
		closePrice := priceSeries.CloseByDay[tradeDate]
		if closePrice <= 0 {
			return nil, []string{v150BenchmarkDailySeriesHealthCode + ":" + tradeDate}
		}
		totalValue := 0.0
		costBasisNet := 0.0
		for _, position := range positions {
			if tradeDay.Before(position.BuyDay) {
				continue
			}
			costBasisNet += position.InvestedNet
			markPrice := closePrice
			if !tradeDay.Before(position.EndDay) {
				markPrice = position.EndPrice
			}
			mark := calcBenchmarkETFSellTradeForVersion(v150.StrategyVersion, position.Shares, markPrice)
			if !mark.Valid || mark.NetAmount <= 0 {
				return nil, []string{position.RuleID + ":" + v150BenchmarkDailySeriesHealthCode + ":" + tradeDate}
			}
			totalValue += mark.NetAmount + position.CashRemainder
		}
		cumulativeAmount := round2(totalValue - costBasisNet)
		dailyAmount := round2(cumulativeAmount - previousCumulativeAmount)
		previousEquity := v150.FixedStrategyV150Config().PortfolioCash + previousCumulativeAmount
		equity := round2(v150.FixedStrategyV150Config().PortfolioCash + cumulativeAmount)
		dailyRate := 0.0
		if previousEquity > 0 {
			dailyRate = round2(dailyAmount / previousEquity * 100)
		}
		valueByDay[tradeDate] = equity
		cumulativeAmountByDay[tradeDate] = cumulativeAmount
		dailyAmountByDay[tradeDate] = dailyAmount
		cumulativeRateByDay[tradeDate] = round2(cumulativeAmount / v150.FixedStrategyV150Config().PortfolioCash * 100)
		dailyRateByDay[tradeDate] = dailyRate
		navByDay[tradeDate] = round4(equity / v150.FixedStrategyV150Config().PortfolioCash)
		previousCumulativeAmount = cumulativeAmount
	}
	return &benchmarkDailySeries{
		Code: defaultBenchmarkModelCode, Name: defaultBenchmarkName, PositionCount: len(positions),
		CloseByDay: priceSeries.CloseByDay, ValueByDay: valueByDay,
		CumulativeAmountByDay: cumulativeAmountByDay, DailyAmountByDay: dailyAmountByDay,
		CumulativeRateByDay: cumulativeRateByDay, DailyRateByDay: dailyRateByDay, NavByDay: navByDay,
	}, nil
}

func resolveV150YieldDailyBenchmarkPrices(
	entry yieldDailyOverviewEntry,
	priceSeries *yieldDailyOverviewPriceSeries,
) (float64, float64, time.Time, string) {
	buyTime := entry.BuyTime.In(cnLocation())
	buyPrice, buyHealth := resolveV150BenchmarkMinutePriceAtWithHealth(buyTime, true)
	if buyPrice <= 0 {
		if buyHealth != "" {
			return 0, 0, time.Time{}, buyHealth
		}
		return 0, 0, time.Time{}, v150BenchmarkBuyQuoteHealthCode
	}
	if entry.HasSellAmount {
		endTime, parsed := parseYieldOverviewDisplayTime(entry.SellTime)
		if !parsed || endTime.Before(buyTime) {
			return 0, 0, time.Time{}, v150BenchmarkExitQuoteHealthCode
		}
		endTime = endTime.In(cnLocation())
		endPrice, endHealth := resolveV150BenchmarkMinutePriceAtWithHealth(endTime, true)
		if endPrice <= 0 {
			if endHealth != "" {
				return 0, 0, time.Time{}, endHealth
			}
			return 0, 0, time.Time{}, v150BenchmarkExitQuoteHealthCode
		}
		return buyPrice, endPrice, endTime, ""
	}

	endDay := normalizeYieldOverviewTradeDay(entry.CurrentDay)
	if endDay.IsZero() {
		if currentAt, parsed := parseYieldOverviewDisplayTime(entry.CurrentPriceTime); parsed {
			endDay = normalizeYieldOverviewTradeDay(currentAt)
		}
	}
	if endDay.IsZero() || endDay.Before(entry.BuyDay) || priceSeries == nil {
		return 0, 0, time.Time{}, v150BenchmarkExitQuoteHealthCode
	}
	endPrice := priceSeries.CloseByDay[endDay.Format(time.DateOnly)]
	if endPrice <= 0 {
		return 0, 0, time.Time{}, v150YieldDailyRawBenchmarkPriceHealthCode
	}
	endTime := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, cnLocation())
	return buyPrice, endPrice, endTime, ""
}

func buildYieldDailyOverviewPoints(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
	benchmarkSeries *yieldDailyOverviewPriceSeries,
) []models.AiRecommendYieldDailyOverviewPoint {
	points, _ := buildYieldDailyOverviewPointsWithV150Ledgers(entries, tradingDays, priceSeriesMap, benchmarkSeries, nil)
	return points
}

func buildYieldDailyOverviewPointsWithV150Ledgers(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
	benchmarkSeries *yieldDailyOverviewPriceSeries,
	v150DailyLedgers map[uint]v150YieldDailyOrderLedger,
) ([]models.AiRecommendYieldDailyOverviewPoint, []string) {
	return buildYieldDailyOverviewPointsWithLedgerLookup(
		entries,
		tradingDays,
		priceSeriesMap,
		benchmarkSeries,
		func(entry yieldDailyOverviewEntry) (v150YieldDailyOrderLedger, bool) {
			ledger, ok := v150DailyLedgers[entry.RecommendID]
			return ledger, ok
		},
		nil,
		false,
	)
}

func buildYieldDailyOverviewPointsWithV150RuleLedgers(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
	benchmarkSeries *yieldDailyOverviewPriceSeries,
	v150DailyLedgers map[string]v150YieldDailyOrderLedger,
	benchmarkMatchedSeries *benchmarkDailySeries,
) ([]models.AiRecommendYieldDailyOverviewPoint, []string) {
	return buildYieldDailyOverviewPointsWithLedgerLookup(
		entries,
		tradingDays,
		priceSeriesMap,
		benchmarkSeries,
		func(entry yieldDailyOverviewEntry) (v150YieldDailyOrderLedger, bool) {
			ledger, ok := v150DailyLedgers[strings.TrimSpace(entry.V150RuleID)]
			return ledger, ok
		},
		benchmarkMatchedSeries,
		true,
	)
}

func buildYieldDailyOverviewPointsWithLedgerLookup(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
	benchmarkSeries *yieldDailyOverviewPriceSeries,
	ledgerLookup func(yieldDailyOverviewEntry) (v150YieldDailyOrderLedger, bool),
	benchmarkMatchedSeries *benchmarkDailySeries,
	benchmarkPrecomputed bool,
) ([]models.AiRecommendYieldDailyOverviewPoint, []string) {
	v150Portfolio := len(entries) > 0
	for _, entry := range entries {
		if !isV150CostVersion(entry.SummaryVersion) {
			v150Portfolio = false
			break
		}
	}
	if !benchmarkPrecomputed && benchmarkSeries != nil {
		if series, _, _, _, _, _, _, _, ok := calculateCashflowMatchedBenchmark(entries, tradingDays, benchmarkSeries); ok {
			if !v150Portfolio || series.PositionCount == len(entries) {
				benchmarkMatchedSeries = series
			}
		}
	}
	points := make([]models.AiRecommendYieldDailyOverviewPoint, 0, len(tradingDays))
	warnings := make([]string, 0)
	prevCumulativeAmount := 0.0
	strategyNav := 1.0
	for _, tradeDay := range tradingDays {
		tradeDate := tradeDay.Format("2006-01-02")
		costBasisNet := 0.0
		dailyHoldingCostNet := 0.0
		cumulativeAmount := 0.0
		holdingCount := 0
		completePoint := true
		for _, entry := range entries {
			if tradeDay.Before(entry.BuyDay) {
				continue
			}
			series := priceSeriesMap[entry.StockCode]
			if isV150CostVersion(entry.SummaryVersion) {
				ledger, ledgerOK := v150YieldDailyOrderLedger{}, false
				if ledgerLookup != nil {
					ledger, ledgerOK = ledgerLookup(entry)
				}
				if !ledgerOK {
					if v150Portfolio {
						completePoint = false
					}
					warnings = append(warnings, v150YieldDailyPointWarning(entry, tradeDate, v150YieldDailyLedgerMissingHealthCode))
					continue
				}
				value, reason := resolveV150YieldDailyLedgerValue(entry, ledger, tradeDate, tradeDay, series)
				if reason != "" {
					if v150Portfolio {
						completePoint = false
					}
					warnings = append(warnings, v150YieldDailyPointWarning(entry, tradeDate, reason))
					continue
				}
				costBasisNet += value.EntryCash
				cumulativeAmount += value.NetValue - value.EntryCash
				if value.DailyCostEligible {
					dailyHoldingCostNet += value.EntryCash
				}
				if value.Holding {
					holdingCount++
				}
				continue
			}
			if series == nil {
				continue
			}
			valueNet, holding, ok := resolveYieldDailyOverviewNetValue(entry, tradeDate, tradeDay, series)
			if !ok {
				continue
			}
			costBasisNet += entry.BuyCostNet
			cumulativeAmount += valueNet - entry.BuyCostNet
			if shouldIncludeYieldDailyOverviewEntryInDailyCost(entry, tradeDay) {
				dailyHoldingCostNet += entry.BuyCostNet
			}
			if holding {
				holdingCount += 1
			}
		}
		// Never publish a partial V1.5 portfolio point. Omitting only the
		// unavailable day preserves prior immutable points and avoids treating a
		// missing holding price as zero PnL or as a vanished position.
		if v150Portfolio && !completePoint {
			continue
		}

		dailyAmount := cumulativeAmount - prevCumulativeAmount
		cumulativeYieldRate := 0.0
		dailyYieldRate := 0.0
		if costBasisNet > 0 {
			cumulativeYieldRate = round2(cumulativeAmount / costBasisNet * 100)
		}
		if dailyHoldingCostNet > 0 {
			dailyYieldRate = round2(dailyAmount / dailyHoldingCostNet * 100)
			strategyNav = round4(strategyNav * (1 + dailyYieldRate/100))
		}
		portfolioEquity := 0.0
		if v150Portfolio {
			portfolioEquity = round2(100_000 + cumulativeAmount)
			previousEquity := 100_000 + prevCumulativeAmount
			cumulativeYieldRate = round2(cumulativeAmount / 100_000 * 100)
			if previousEquity > 0 {
				dailyYieldRate = round2(dailyAmount / previousEquity * 100)
			}
			strategyNav = round4(portfolioEquity / 100_000)
		}
		benchmarkClose := 0.0
		benchmarkCumulativeAmount := 0.0
		benchmarkDailyAmount := 0.0
		benchmarkCumulativeRate := 0.0
		benchmarkDailyRate := 0.0
		benchmarkNav := 1.0
		benchmarkDayAvailable := false
		if v150Portfolio {
			benchmarkNav = 0
		}
		if benchmarkMatchedSeries != nil {
			if navValue, ok := benchmarkMatchedSeries.NavByDay[tradeDate]; ok {
				benchmarkDayAvailable = true
				benchmarkClose = round2(benchmarkSeries.CloseByDay[tradeDate])
				benchmarkCumulativeAmount = round2(benchmarkMatchedSeries.CumulativeAmountByDay[tradeDate])
				benchmarkDailyAmount = round2(benchmarkMatchedSeries.DailyAmountByDay[tradeDate])
				benchmarkCumulativeRate = round2(benchmarkMatchedSeries.CumulativeRateByDay[tradeDate])
				benchmarkDailyRate = round2(benchmarkMatchedSeries.DailyRateByDay[tradeDate])
				benchmarkNav = round4(navValue)
			}
		}
		excessCumulativeAmount := 0.0
		excessDailyAmount := 0.0
		excessCumulativeRate := 0.0
		excessDailyRate := 0.0
		if benchmarkDayAvailable || !v150Portfolio {
			excessCumulativeAmount = round2(cumulativeAmount - benchmarkCumulativeAmount)
			excessDailyAmount = round2(dailyAmount - benchmarkDailyAmount)
			excessCumulativeRate = round2(cumulativeYieldRate - benchmarkCumulativeRate)
			excessDailyRate = round2(dailyYieldRate - benchmarkDailyRate)
		}
		points = append(points, models.AiRecommendYieldDailyOverviewPoint{
			TradeDate:                       tradeDate,
			PortfolioEquity:                 portfolioEquity,
			CostBasisNet:                    round2(costBasisNet),
			DailyHoldingCostNet:             round2(dailyHoldingCostNet),
			HoldingCount:                    holdingCount,
			CumulativeAmountChange:          round2(cumulativeAmount),
			CumulativeYieldRate:             cumulativeYieldRate,
			DailyAmountChange:               round2(dailyAmount),
			DailyYieldRate:                  dailyYieldRate,
			BenchmarkClose:                  benchmarkClose,
			BenchmarkCumulativeAmountChange: benchmarkCumulativeAmount,
			BenchmarkDailyAmountChange:      benchmarkDailyAmount,
			BenchmarkCumulativeRate:         benchmarkCumulativeRate,
			BenchmarkDailyRate:              benchmarkDailyRate,
			ExcessCumulativeAmountChange:    excessCumulativeAmount,
			ExcessDailyAmountChange:         excessDailyAmount,
			ExcessCumulativeRate:            excessCumulativeRate,
			ExcessDailyRate:                 excessDailyRate,
			StrategyNav:                     round4(strategyNav),
			BenchmarkNav:                    benchmarkNav,
		})
		prevCumulativeAmount = cumulativeAmount
	}
	return points, dedupeNonEmptyStrings(warnings, 0)
}

func shouldIncludeYieldDailyOverviewEntryInDailyCost(entry yieldDailyOverviewEntry, tradeDay time.Time) bool {
	if tradeDay.Before(entry.BuyDay) {
		return false
	}
	if !entry.SellDay.IsZero() && entry.SellDay.Before(tradeDay) {
		return false
	}
	return entry.BuyCostNet > 0
}

func resolveYieldDailyOverviewNetValue(
	entry yieldDailyOverviewEntry,
	tradeDate string,
	tradeDay time.Time,
	series *yieldDailyOverviewPriceSeries,
) (float64, bool, bool) {
	if entry.HasSellAmount && !entry.SellDay.IsZero() && !tradeDay.Before(entry.SellDay) {
		return entry.RealizedValueNet, false, entry.RealizedValueNet > 0
	}

	price := 0.0
	if !entry.CurrentDay.IsZero() && tradeDay.Equal(entry.CurrentDay) && entry.CurrentPrice > 0 {
		price = entry.CurrentPrice
	}
	if price <= 0 {
		price = series.CloseByDay[tradeDate]
	}
	if price <= 0 {
		return 0, false, false
	}
	sellNet := calcSellTradeCostForVersion(entry.SummaryVersion, entry.BuyAmount, price, resolveTradingMarket(entry.StockCode))
	if sellNet.NetAmount <= 0 {
		return 0, false, false
	}
	holding := entry.SellDay.IsZero() || tradeDay.Before(entry.SellDay)
	return round2(sellNet.NetAmount), holding, true
}

func resolveYieldDailyOverviewDataAsOf(entries []yieldDailyOverviewEntry, rangeEnd string) string {
	var latest time.Time
	for _, entry := range entries {
		if ts, ok := parseYieldOverviewDisplayTime(entry.CurrentPriceTime); ok && ts.After(latest) {
			latest = ts
		}
		if ts, ok := parseYieldOverviewDisplayTime(entry.SellTime); ok && ts.After(latest) {
			latest = ts
		}
	}
	if !latest.IsZero() {
		return latest.In(cnLocation()).Format("2006-01-02 15:04:05")
	}
	if strings.TrimSpace(rangeEnd) == "" {
		return ""
	}
	return strings.TrimSpace(rangeEnd) + " 15:00:00"
}

func resolveV150YieldDailyOverviewDataAsOf(entries []yieldDailyOverviewEntry, rangeEnd string) string {
	base := resolveYieldDailyOverviewDataAsOf(entries, rangeEnd)
	latest, latestOK := parseYieldOverviewDisplayTime(base)
	endDay, endOK := parseYieldOverviewTradeDay(rangeEnd)
	if !endOK {
		return base
	}
	endAt := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, cnLocation())
	if !latestOK || endAt.After(latest) {
		return endAt.Format(time.DateTime)
	}
	return latest.In(cnLocation()).Format(time.DateTime)
}

func estimateYieldDailyOverviewKlineDays(startDay, endDay time.Time) int64 {
	if startDay.IsZero() || endDay.IsZero() || endDay.Before(startDay) {
		return 90
	}
	diffDays := int(endDay.Sub(startDay).Hours()/24) + 1
	if diffDays < 30 {
		diffDays = 30
	}
	return int64(diffDays + 20)
}

func parseYieldOverviewDisplayTime(raw string) (time.Time, bool) {
	return parseYieldReplayTime(raw)
}

func parseYieldOverviewTradeDay(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", text, cnLocation())
	if err != nil {
		return time.Time{}, false
	}
	return normalizeYieldOverviewTradeDay(t), true
}

func parseYieldDailyOverviewTimestamp(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, text); err == nil {
			return ts, true
		}
		if ts, err := time.ParseInLocation(layout, text, cnLocation()); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func normalizeYieldOverviewTradeDay(ts time.Time) time.Time {
	loc := cnLocation()
	at := ts.In(loc)
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
}

func resolveYieldReplaySignalTime(rec models.AiRecommendStocks, state *models.AiRecommendYieldRecordState) time.Time {
	if state != nil && state.SignalTime != nil && !state.SignalTime.IsZero() {
		return state.SignalTime.In(cnLocation())
	}
	return recommendRecordTime(rec)
}

func resolveYieldReplayRangeEnd(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, string) {
	if state != nil && state.SellTime != nil && !state.SellTime.IsZero() {
		return state.SellTime.In(cnLocation()), ""
	}
	if sellAt, ok := parseYieldReplayTime(item.SellTime); ok {
		return sellAt, ""
	}

	positionStatus := strings.TrimSpace(item.PositionStatus)
	sellTimeText := strings.TrimSpace(item.SellTime)
	if positionStatus == "持有" || sellTimeText == "持有" {
		if currentAt, ok := resolveYieldReplayCurrentTime(item, state); ok {
			return currentAt, ""
		}
		if state != nil && state.LastMinuteTs != nil && !state.LastMinuteTs.IsZero() {
			return state.LastMinuteTs.In(cnLocation()), ""
		}
		return time.Time{}, "当前仍在持有，但缺少 currentPriceTime/LastMinuteTs，无法确定回放终点"
	}

	if currentAt, ok := resolveYieldReplayCurrentTime(item, state); ok && strings.TrimSpace(item.ActivationStatus) == "activated" {
		return currentAt, ""
	}

	return time.Time{}, "该记录未形成可回放终点"
}

func isYieldReplayHolding(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) bool {
	if state != nil {
		if strings.TrimSpace(state.PositionStatus) == "持有" && (state.SellTime == nil || state.SellTime.IsZero()) {
			return true
		}
	}
	positionStatus := strings.TrimSpace(item.PositionStatus)
	sellTimeText := strings.TrimSpace(item.SellTime)
	return positionStatus == "持有" || sellTimeText == "持有"
}

func resolveYieldReplayCurrentTime(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, bool) {
	if state != nil {
		if currentAt, ok := parseYieldReplayTime(state.CurrentPriceTime); ok {
			return normalizeMinuteCoverageEnd(currentAt), true
		}
	}
	if currentAt, ok := parseYieldReplayTime(item.CurrentPriceTime); ok {
		return normalizeMinuteCoverageEnd(currentAt), true
	}
	return time.Time{}, false
}

func parseYieldReplayTime(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	if text == "" || text == "持有" || text == "待激活" || text == "已跳过" || text == "未纳入回测" || text == "无法回算" || text == "未激活失效" {
		return time.Time{}, false
	}
	t, err := parseDateTimeWithFallback(normalizeDateTime(text))
	if err != nil {
		return time.Time{}, false
	}
	return t.In(cnLocation()), true
}

func buildYieldReplayBars(bars []minuteBar) []models.AiRecommendYieldMinuteBarDTO {
	result := make([]models.AiRecommendYieldMinuteBarDTO, 0, len(bars))
	for _, bar := range bars {
		result = append(result, models.AiRecommendYieldMinuteBarDTO{
			TradeTime: formatYieldDisplayTime(bar.TradeTime),
			Open:      round2(bar.Open),
			High:      round2(bar.High),
			Low:       round2(bar.Low),
			Close:     round2(bar.Close),
			Volume:    bar.Volume,
			Amount:    bar.Amount,
		})
	}
	return result
}

func buildYieldReplayMarkers(
	bars []minuteBar,
	signalAt time.Time,
	item models.AiRecommendStocksYieldItem,
	state *models.AiRecommendYieldRecordState,
) ([]models.AiRecommendYieldChartMarker, string, []string) {
	markers := make([]models.AiRecommendYieldChartMarker, 0, 4)
	messages := make([]string, 0, 4)
	status := "ready"

	appendMarker := func(markerType, label string, target time.Time, price float64) {
		if target.IsZero() {
			return
		}
		marker, ok, exact := locateYieldReplayMarker(bars, markerType, label, target, price)
		if !ok {
			if markerType != "signal" {
				status = "partial"
			}
			appendYieldReplayMessage(&messages, label+"点未在分钟线中定位到")
			return
		}
		if !exact {
			if msg := buildYieldReplayMarkerApproxMessage(markerType, label, marker.Time); msg != "" {
				appendYieldReplayMessage(&messages, msg)
			}
		}
		markers = append(markers, marker)
	}

	appendMarker("signal", "信号", signalAt, 0)

	if buyAt, ok := resolveYieldReplayBuyTime(item, state); ok {
		appendMarker("buy", "买入", buyAt, resolveYieldReplayBuyPrice(item, state))
	}

	if sellAt, ok := resolveYieldReplaySellTime(item, state); ok {
		appendMarker("sell", "卖出", sellAt, resolveYieldReplaySellPrice(item, state))
	} else if currentAt, ok := resolveYieldReplayCurrentTime(item, state); ok && (strings.TrimSpace(item.PositionStatus) == "持有" || strings.TrimSpace(item.SellTime) == "持有") {
		appendMarker("current", "当前", currentAt, resolveYieldReplayCurrentPrice(item, state))
	}

	return markers, status, messages
}

func resolveYieldReplayBuyTime(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, bool) {
	if state != nil && state.BuyTime != nil && !state.BuyTime.IsZero() {
		return state.BuyTime.In(cnLocation()), true
	}
	return parseYieldReplayTime(item.BuyTime)
}

func resolveYieldReplayBuyPrice(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) float64 {
	if state != nil && state.BuyAmount > 0 {
		return round2(state.BuyAmount)
	}
	if item.BuyAmount > 0 {
		return round2(item.BuyAmount)
	}
	return 0
}

func resolveYieldReplaySellTime(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, bool) {
	if state != nil && state.SellTime != nil && !state.SellTime.IsZero() {
		return state.SellTime.In(cnLocation()), true
	}
	return parseYieldReplayTime(item.SellTime)
}

func resolveYieldReplaySellPrice(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) float64 {
	if state != nil && state.RealizedSellAmount != nil && *state.RealizedSellAmount > 0 {
		return round2(*state.RealizedSellAmount)
	}
	if item.SellAmount != nil && *item.SellAmount > 0 {
		return round2(*item.SellAmount)
	}
	return 0
}

func resolveYieldReplayCurrentPrice(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) float64 {
	if state != nil && state.CurrentPrice > 0 {
		return round2(state.CurrentPrice)
	}
	if item.CurrentPrice > 0 {
		return round2(item.CurrentPrice)
	}
	return 0
}

func locateYieldReplayMarker(bars []minuteBar, markerType, label string, target time.Time, preferredPrice float64) (models.AiRecommendYieldChartMarker, bool, bool) {
	if len(bars) == 0 || target.IsZero() {
		return models.AiRecommendYieldChartMarker{}, false, false
	}
	target = normalizeMinuteTime(target.In(cnLocation()))
	for _, bar := range bars {
		barTime := normalizeMinuteTime(bar.TradeTime.In(cnLocation()))
		if barTime.Before(target) {
			continue
		}
		price := round2(bar.Close)
		if preferredPrice > 0 {
			price = round2(preferredPrice)
		}
		return models.AiRecommendYieldChartMarker{
			Type:   markerType,
			Time:   formatYieldDisplayTime(bar.TradeTime),
			Price:  price,
			Label:  label,
			Status: yieldReplayMarkerLocateStatus(barTime.Equal(target)),
		}, true, barTime.Equal(target)
	}
	return models.AiRecommendYieldChartMarker{}, false, false
}

func yieldReplayMarkerLocateStatus(exact bool) string {
	if exact {
		return "exact"
	}
	return "approximated"
}

func buildYieldReplayMarkerApproxMessage(markerType, label, resolvedTime string) string {
	if markerType == "signal" {
		return ""
	}
	timeText := strings.TrimSpace(resolvedTime)
	if timeText == "" {
		return label + "点不在交易分钟，已顺延到下一根可用分钟线显示"
	}
	return fmt.Sprintf("%s点不在交易分钟，已对齐到 %s 显示", label, timeText)
}

func appendYieldReplayMessage(messages *[]string, raw string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return
	}
	for _, msg := range *messages {
		if msg == text {
			return
		}
	}
	*messages = append(*messages, text)
}

func replayChartStatusHint(dataStatus, dataStatusReason string) string {
	status := strings.TrimSpace(dataStatus)
	reason := strings.TrimSpace(dataStatusReason)
	switch status {
	case "待覆盖":
		if reason != "" {
			return reason
		}
		return "分钟线尚未完全覆盖该时间段"
	case "不可覆盖", "无法判定":
		if reason != "" {
			return reason
		}
		return "当前分钟线无法完整覆盖该时间段"
	default:
		if status != "" && status != "正常" && status != "已跳过" && status != "未结构化" && reason != "" {
			return reason
		}
	}
	return ""
}
