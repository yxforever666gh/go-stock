package data

import (
	"database/sql"
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
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

	signature, err := buildYieldDailyOverviewSignature(query)
	if err != nil {
		return nil, err
	}
	if cached, ok := loadYieldDailyOverviewCache(signature); ok {
		return cached, nil
	}

	result, err := s.buildYieldDailyOverview(query)
	if err != nil {
		return nil, err
	}
	storeYieldDailyOverviewCache(signature, result)
	return cloneYieldDailyOverviewData(result), nil
}

func (s *AiRecommendStocksService) buildYieldDailyOverview(query *models.AiRecommendStocksQuery) (*models.AiRecommendYieldDailyOverviewData, error) {
	loc := cnLocation()
	now := timeNow().In(loc)
	expectedTradeDate := resolveExpectedYieldTradeDate(now)
	latestTradeDate := expectedTradeDate
	meta := models.AiRecommendYieldMeta{}
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err == nil {
		if t, ok := parseYieldTradeDate(meta.CurrentTradeDate); ok {
			latestTradeDate = t
		}
	}
	if expectedTradeDate.After(latestTradeDate) {
		latestTradeDate = expectedTradeDate
	}
	latestTradeDate = time.Date(latestTradeDate.Year(), latestTradeDate.Month(), latestTradeDate.Day(), 0, 0, 0, 0, loc)
	coverableStart := minuteCoverableStartMinute(latestTradeDate)

	records, err := listAiRecommendStocksForYield(query, coverableStart)
	if err != nil {
		return nil, err
	}
	records = collapseRecommendRecordsSameDayByCode(records)

	result := &models.AiRecommendYieldDailyOverviewData{
		CalcMode:         aiRecommendYieldModeStrict,
		BenchmarkCode:    defaultBenchmarkModelCode,
		BenchmarkName:    defaultBenchmarkName,
		StrategyCohort:   query.StrategyCohort,
		TotalRecordCount: len(records),
		Warnings:         []string{},
		Points:           []models.AiRecommendYieldDailyOverviewPoint{},
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
	dirtyMap, err := loadDirtyAiRecommendYieldCodeSet(aiRecommendYieldModeStrict)
	if err != nil {
		return nil, err
	}

	items := buildStrictYieldRecordItems(records, recordStateMap, stateMap, overrideMap, dirtyMap, nil)
	entries := make([]yieldDailyOverviewEntry, 0, len(items))
	inactiveSkipped := 0
	for _, item := range items {
		entry, ok := buildYieldDailyOverviewEntry(item)
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

	startDay, endDay, ok := resolveYieldDailyOverviewWindow(entries)
	if !ok {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "无法确定全库收益走势的起止时间")
		return result, nil
	}

	tradingDays, benchmarkSeries, err := loadYieldDailyOverviewTradingDays(startDay, endDay)
	if err != nil {
		return nil, err
	}
	if len(tradingDays) == 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "当前窗口没有可用交易日，无法生成收益走势")
		return result, nil
	}

	priceSeriesMap, missingCodes, err := loadYieldDailyOverviewPriceSeries(entries, tradingDays)
	if err != nil {
		return nil, err
	}

	filteredEntries := make([]yieldDailyOverviewEntry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := priceSeriesMap[entry.StockCode]; !ok {
			continue
		}
		filteredEntries = append(filteredEntries, entry)
	}
	if len(missingCodes) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d 只股票缺少日线数据，已从全库走势中跳过：%s", len(missingCodes), strings.Join(missingCodes, "、")))
	}
	if len(filteredEntries) == 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "全部候选股票都缺少可用日线数据")
		return result, nil
	}

	points := buildYieldDailyOverviewPoints(filteredEntries, tradingDays, priceSeriesMap, benchmarkSeries)
	if len(points) == 0 {
		result.SkippedRecordCount = result.TotalRecordCount
		result.Warnings = append(result.Warnings, "未能生成有效的按交易日收益点位")
		return result, nil
	}

	result.RangeStart = points[0].TradeDate
	result.RangeEnd = points[len(points)-1].TradeDate
	result.DataAsOf = resolveYieldDailyOverviewDataAsOf(filteredEntries, result.RangeEnd)
	result.IncludedRecordCount = len(filteredEntries)
	result.SkippedRecordCount = result.TotalRecordCount - result.IncludedRecordCount
	result.Points = points
	return result, nil
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
	return fmt.Sprintf(
		"cohort:%s|record:%d:%d|recommend:%d:%d|override:%d:%d",
		normalizeStrategyCohort(query.StrategyCohort, strategyCohortAll),
		recordStamp.Count,
		recordStamp.MaxAt.UnixNano(),
		recommendStamp.Count,
		recommendStamp.MaxAt.UnixNano(),
		overrideStamp.Count,
		overrideStamp.MaxAt.UnixNano(),
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
	buyCost := calcBuyTradeCost(item.BuyAmount, market)
	if buyCost.NetAmount <= 0 {
		return yieldDailyOverviewEntry{}, false
	}

	entry := yieldDailyOverviewEntry{
		RecommendID:      item.RecommendID,
		StockCode:        normalizeRecommendStockCode(item.StockCode),
		StockName:        strings.TrimSpace(item.StockName),
		BuyTime:          buyTime,
		BuyDay:           normalizeYieldOverviewTradeDay(buyTime),
		BuyAmount:        round2(item.BuyAmount),
		CurrentPrice:     round2(item.CurrentPrice),
		BuyCostNet:       round2(buyCost.NetAmount),
		CurrentPriceTime: strings.TrimSpace(item.CurrentPriceTime),
		SellTime:         strings.TrimSpace(item.SellTime),
	}

	if item.SellAmount != nil && *item.SellAmount > 0 {
		entry.SellAmount = round2(*item.SellAmount)
		entry.HasSellAmount = true
		sellNet := calcSellTradeCost(item.BuyAmount, *item.SellAmount, market)
		entry.RealizedValueNet = round2(sellNet.NetAmount)
		if sellTime, ok := parseYieldOverviewDisplayTime(item.SellTime); ok {
			entry.SellDay = normalizeYieldOverviewTradeDay(sellTime)
		}
	}
	if currentTime, ok := parseYieldOverviewDisplayTime(item.CurrentPriceTime); ok {
		entry.CurrentDay = normalizeYieldOverviewTradeDay(currentTime)
	}
	if entry.CurrentDay.IsZero() {
		entry.CurrentDay = normalizeYieldOverviewTradeDay(timeNow().In(cnLocation()))
	}
	if !entry.HasSellAmount && entry.CurrentPrice <= 0 {
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
	klineDays := estimateYieldDailyOverviewKlineDays(startDay, endDay)
	bars, err := loadDailyBarsWithCache(defaultBenchmarkModelCode, defaultBenchmarkCode, startDay, endDay, klineDays)
	if err != nil {
		return nil, nil, err
	}
	if len(bars) == 0 {
		return nil, nil, errors.New("读取沪深300ETF日线失败")
	}

	loc := cnLocation()
	days := make([]time.Time, 0, len(bars))
	rawCloseByDay := make(map[string]float64, len(bars))
	for _, bar := range bars {
		day := normalizeDailyTradeDate(bar.TradeDate)
		if day.IsZero() || day.Before(startDay) || day.After(endDay) {
			continue
		}
		days = append(days, day)
		if bar.Close > 0 {
			rawCloseByDay[day.Format("2006-01-02")] = round2(bar.Close)
		}
	}
	if len(days) == 0 {
		return nil, nil, nil
	}

	lastDay := days[len(days)-1]
	if endDay.After(lastDay) && endDay.Weekday() != time.Saturday && endDay.Weekday() != time.Sunday {
		endCandidate := time.Date(endDay.In(loc).Year(), endDay.In(loc).Month(), endDay.In(loc).Day(), 0, 0, 0, 0, loc)
		if endCandidate.After(lastDay) {
			days = append(days, endCandidate)
		}
	}
	closeByDay := make(map[string]float64, len(days))
	lastClose := 0.0
	for _, day := range days {
		key := day.Format("2006-01-02")
		if closePrice, ok := rawCloseByDay[key]; ok && closePrice > 0 {
			lastClose = closePrice
		}
		if lastClose > 0 {
			closeByDay[key] = round2(lastClose)
		}
	}
	return days, &yieldDailyOverviewPriceSeries{
		Code:       defaultBenchmarkModelCode,
		CloseByDay: closeByDay,
	}, nil
}

func loadYieldDailyOverviewPriceSeries(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
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

			series, err := loadYieldDailyOverviewPriceSeriesByCode(stockCode, startDay, endDay, tradingDays)
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
) (*yieldDailyOverviewPriceSeries, error) {
	quoteCode := toQuoteCode(stockCode)
	if quoteCode == "" {
		return nil, nil
	}

	klineDays := estimateYieldDailyOverviewKlineDays(startDay, endDay)
	bars, err := loadDailyBarsWithCache(stockCode, quoteCode, startDay, endDay, klineDays)
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

	closeByDay := make(map[string]float64, len(tradingDays))
	lastClose := 0.0
	for _, day := range tradingDays {
		key := day.Format("2006-01-02")
		if closePrice, ok := rawCloseByDay[key]; ok && closePrice > 0 {
			lastClose = closePrice
		}
		if lastClose > 0 {
			closeByDay[key] = round2(lastClose)
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

func buildYieldDailyOverviewPoints(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
	benchmarkSeries *yieldDailyOverviewPriceSeries,
) []models.AiRecommendYieldDailyOverviewPoint {
	var benchmarkMatchedSeries *benchmarkDailySeries
	if benchmarkSeries != nil {
		if series, _, _, _, _, _, _, _, ok := calculateCashflowMatchedBenchmark(entries, tradingDays, benchmarkSeries); ok {
			benchmarkMatchedSeries = series
		}
	}
	points := make([]models.AiRecommendYieldDailyOverviewPoint, 0, len(tradingDays))
	prevCumulativeAmount := 0.0
	strategyNav := 1.0
	for _, tradeDay := range tradingDays {
		tradeDate := tradeDay.Format("2006-01-02")
		costBasisNet := 0.0
		dailyHoldingCostNet := 0.0
		cumulativeAmount := 0.0
		holdingCount := 0
		for _, entry := range entries {
			if tradeDay.Before(entry.BuyDay) {
				continue
			}
			series := priceSeriesMap[entry.StockCode]
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
		benchmarkClose := 0.0
		benchmarkCumulativeAmount := 0.0
		benchmarkDailyAmount := 0.0
		benchmarkCumulativeRate := 0.0
		benchmarkDailyRate := 0.0
		benchmarkNav := 1.0
		if benchmarkMatchedSeries != nil {
			benchmarkClose = round2(benchmarkSeries.CloseByDay[tradeDate])
			benchmarkCumulativeAmount = round2(benchmarkMatchedSeries.CumulativeAmountByDay[tradeDate])
			benchmarkDailyAmount = round2(benchmarkMatchedSeries.DailyAmountByDay[tradeDate])
			benchmarkCumulativeRate = round2(benchmarkMatchedSeries.CumulativeRateByDay[tradeDate])
			benchmarkDailyRate = round2(benchmarkMatchedSeries.DailyRateByDay[tradeDate])
			benchmarkNav = round4(benchmarkMatchedSeries.NavByDay[tradeDate])
		}
		excessCumulativeAmount := round2(cumulativeAmount - benchmarkCumulativeAmount)
		excessDailyAmount := round2(dailyAmount - benchmarkDailyAmount)
		excessCumulativeRate := round2(cumulativeYieldRate - benchmarkCumulativeRate)
		excessDailyRate := round2(dailyYieldRate - benchmarkDailyRate)
		points = append(points, models.AiRecommendYieldDailyOverviewPoint{
			TradeDate:                       tradeDate,
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
	return points
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
		price = entry.BuyAmount
	}
	sellNet := calcSellTradeCost(entry.BuyAmount, price, resolveTradingMarket(entry.StockCode))
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
	if text == "" || text == "持有" || text == "待激活" || text == "已跳过" || text == "未纳入回测" || text == "无法回算" {
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
