package data

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

const (
	v150ValidationMinimumTradingDays        = 60
	v150ValidationMinimumClosedTrades       = 100
	v150ValidationMinimumRecommendationDays = 40
	v150ValidationMinimumNetMeanPct         = 0.75
	v150ValidationMinimumNetExcessMeanPct   = 0.50
	v150ValidationMinimumProfitFactor       = 1.25
	v150OneSided90Z                         = 1.2815515655446004
)

func calculateV150ForwardValidation(items []models.AiRecommendStocksYieldItem) *models.StrategyValidationStatus {
	result := &models.StrategyValidationStatus{
		Status:          "forward_validation",
		Label:           "前向验证中",
		UnmetConditions: []string{},
	}
	closedReturns := make([]float64, 0, len(items))
	closedExcess := make([]float64, 0, len(items))
	dailyReturns := make(map[string][]float64)
	recommendationDays := make(map[string]struct{})
	var earliest time.Time
	grossProfit := 0.0
	grossLoss := 0.0

	for _, item := range items {
		if !isV150CostVersion(item.SummaryVersion) || normalizeRecommendExecutionState(item.ExecutionState) == recommendExecutionAnalysisOnly {
			continue
		}
		if strings.TrimSpace(item.BacktestEligibility) != "" && strings.TrimSpace(item.BacktestEligibility) != recommendBacktestEligible {
			continue
		}
		recommendAt, ok := parseYieldOverviewDisplayTime(item.RecommendTime)
		if !ok {
			recommendAt, ok = parseYieldOverviewDisplayTime(item.SignalTime)
		}
		if ok {
			day := recommendAt.In(cnLocation()).Format(time.DateOnly)
			recommendationDays[day] = struct{}{}
			if earliest.IsZero() || recommendAt.Before(earliest) {
				earliest = recommendAt
			}
		}
		if item.ActivationStatus != "activated" || !item.V150LedgerAccountingReady || !item.V150LedgerClosed {
			continue
		}
		if math.IsNaN(item.YieldRate) || math.IsInf(item.YieldRate, 0) || math.IsNaN(item.V150LedgerNetPnL) || math.IsInf(item.V150LedgerNetPnL, 0) {
			continue
		}
		closedReturns = append(closedReturns, item.YieldRate)
		if hasDisplayMetricText(item.BenchmarkYieldRateText) && hasDisplayMetricText(item.ExcessYieldRateText) {
			closedExcess = append(closedExcess, item.ExcessYieldRate)
		}
		pnl := item.V150LedgerNetPnL
		if pnl > 0 {
			grossProfit += pnl
		} else if pnl < 0 {
			grossLoss += -pnl
		}
		if ok {
			day := recommendAt.In(cnLocation()).Format(time.DateOnly)
			dailyReturns[day] = append(dailyReturns[day], item.YieldRate)
		}
	}

	result.ClosedTradeCount = len(closedReturns)
	result.ComparableTradeCount = len(closedExcess)
	result.RecommendationDayCount = len(recommendationDays)
	// Elapsed forward time runs from the first 1.5.0 recommendation through
	// the latest cached benchmark session. It must not stop merely because a
	// later session produced an explicit no_trade run.
	result.TradingDayCount = countCachedV150BenchmarkTradingDays(earliest, time.Now())
	result.NetMeanPct = round4(meanFloat64(closedReturns))
	result.NetExcessMeanPct = round4(meanFloat64(closedExcess))
	if grossLoss > 0 {
		result.ProfitFactor = round4(grossProfit / grossLoss)
	} else if grossProfit > 0 {
		result.ProfitFactor = 999
	}

	dayKeys := make([]string, 0, len(dailyReturns))
	for day := range dailyReturns {
		dayKeys = append(dayKeys, day)
	}
	sort.Strings(dayKeys)
	grouped := make([]float64, 0, len(dayKeys))
	for _, day := range dayKeys {
		grouped = append(grouped, meanFloat64(dailyReturns[day]))
	}
	result.DailyLowerBound90Pct = round4(oneSidedLowerBound90(grouped))

	checks := []struct {
		ok     bool
		reason string
	}{
		{result.TradingDayCount >= v150ValidationMinimumTradingDays, fmt.Sprintf("交易日 %d/%d", result.TradingDayCount, v150ValidationMinimumTradingDays)},
		{result.ClosedTradeCount >= v150ValidationMinimumClosedTrades, fmt.Sprintf("平仓 %d/%d", result.ClosedTradeCount, v150ValidationMinimumClosedTrades)},
		{result.ComparableTradeCount >= v150ValidationMinimumClosedTrades, fmt.Sprintf("基准可比平仓 %d/%d", result.ComparableTradeCount, v150ValidationMinimumClosedTrades)},
		{result.RecommendationDayCount >= v150ValidationMinimumRecommendationDays, fmt.Sprintf("独立推荐日 %d/%d", result.RecommendationDayCount, v150ValidationMinimumRecommendationDays)},
		{result.NetMeanPct >= v150ValidationMinimumNetMeanPct, fmt.Sprintf("净均值 %.4f%% < %.2f%%", result.NetMeanPct, v150ValidationMinimumNetMeanPct)},
		{result.NetExcessMeanPct >= v150ValidationMinimumNetExcessMeanPct, fmt.Sprintf("净超额 %.4f%% < %.2f%%", result.NetExcessMeanPct, v150ValidationMinimumNetExcessMeanPct)},
		{result.ProfitFactor >= v150ValidationMinimumProfitFactor, fmt.Sprintf("Profit Factor %.4f < %.2f", result.ProfitFactor, v150ValidationMinimumProfitFactor)},
		{result.DailyLowerBound90Pct > 0, fmt.Sprintf("推荐日单侧90%%下界 %.4f%% <= 0", result.DailyLowerBound90Pct)},
	}
	for _, check := range checks {
		if !check.ok {
			result.UnmetConditions = append(result.UnmetConditions, check.reason)
		}
	}
	if len(result.UnmetConditions) == 0 {
		result.Status = "validated"
		result.Label = "已验证"
	}
	return result
}

// loadV150ForwardValidation evaluates the complete persisted 1.5.0 cohort.
// The validation badge must not change when a user narrows the page date
// picker; forward-validation evidence belongs to the frozen strategy version,
// not to the current UI slice.
func loadV150ForwardValidation() (*models.StrategyValidationStatus, error) {
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.AiRecommendStocks{}) {
		return calculateV150ForwardValidation(nil), nil
	}
	records, err := listAiRecommendStocksForYield(&models.AiRecommendStocksQuery{StrategyCohort: marketSummaryVersion150}, time.Time{})
	if err != nil {
		return nil, err
	}
	records = collapseRecommendRecordsSameDayByCode(records)
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
	items := buildStrictYieldRecordItems(records, recordStateMap, stateMap, overrideMap, aiRecommendYieldDirtyScope{}, nil)
	// buildStrictYieldRecordItems reconstructs the strategy-side net return but
	// deliberately leaves benchmark fields empty. Populate per-trade 510300
	// returns from exact cached 510300 minute quotes (with cached daily sessions
	// defining the replay calendar) before evaluating the frozen excess-return
	// gate. Missing quotes keep the whole comparison unavailable; this path never
	// fetches history from the network.
	_ = calculateBenchmarkSummaryByItems(items)
	return calculateV150ForwardValidation(items), nil
}

func countCachedV150BenchmarkTradingDays(from, to time.Time) int {
	if db.Dao == nil || from.IsZero() || to.IsZero() || to.Before(from) {
		return 0
	}
	var count int64
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	end := time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 999999999, to.Location())
	err := db.Dao.Model(&models.AiRecommendDailyBar{}).
		Where("stock_code IN ? AND trade_date >= ? AND trade_date <= ?", []string{"510300", "510300.SH"}, start, end).
		Distinct("trade_date").
		Count(&count).Error
	if err != nil {
		return 0
	}
	return int(count)
}

func meanFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func oneSidedLowerBound90(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean := meanFloat64(values)
	sumSquares := 0.0
	for _, value := range values {
		delta := value - mean
		sumSquares += delta * delta
	}
	standardDeviation := math.Sqrt(sumSquares / float64(len(values)-1))
	return mean - v150OneSided90Z*standardDeviation/math.Sqrt(float64(len(values)))
}
