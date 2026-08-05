package portfolio

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var ErrInvalidForwardSample = errors.New("invalid forward-validation sample")

const (
	ForwardValidationPending   = "forward_validation_pending"
	ForwardValidationValidated = "validated"

	minimumForwardTradingDays        = 60
	minimumForwardClosedTrades       = 100
	minimumForwardRecommendationDays = 40
	minimumForwardNetMeanPct         = 0.75
	minimumForwardExcessMeanPct      = 0.50
	minimumForwardProfitFactor       = 1.25
	oneSided90Z                      = 1.2815515655446004
)

type ForwardTradeSample struct {
	StrategyVersion    string   `json:"strategyVersion"`
	RuleID             string   `json:"ruleId"`
	RecommendationDay  string   `json:"recommendationDay"`
	NetReturnPct       float64  `json:"netReturnPct"`
	BenchmarkReturnPct *float64 `json:"benchmarkReturnPct,omitempty"`
	NetPnL             float64  `json:"netPnl"`
}

type ForwardValidationInput struct {
	StrategyVersion    string               `json:"strategyVersion"`
	TradingDays        int                  `json:"tradingDays"`
	RecommendationDays []string             `json:"recommendationDays"`
	Trades             []ForwardTradeSample `json:"trades"`
}

type ForwardValidationMetrics struct {
	StrategyVersion                  string   `json:"strategyVersion"`
	State                            string   `json:"state"`
	TradingDays                      int      `json:"tradingDays"`
	ClosedTrades                     int      `json:"closedTrades"`
	ComparableTrades                 int      `json:"comparableTrades"`
	RecommendationDays               int      `json:"recommendationDays"`
	NetMeanReturnPct                 float64  `json:"netMeanReturnPct"`
	BenchmarkExcessMeanPct           float64  `json:"benchmarkExcessMeanPct"`
	ProfitFactor                     *float64 `json:"profitFactor,omitempty"`
	ProfitFactorInfinite             bool     `json:"profitFactorInfinite"`
	RecommendationDayMeanPct         float64  `json:"recommendationDayMeanPct"`
	RecommendationDayLowerBound90Pct float64  `json:"recommendationDayLowerBound90Pct"`
	RequirementsMet                  bool     `json:"requirementsMet"`
	PerformanceThresholdsMet         bool     `json:"performanceThresholdsMet"`
	PendingReasons                   []string `json:"pendingReasons"`
}

func ComputeForwardValidation(input ForwardValidationInput) (ForwardValidationMetrics, error) {
	version := strings.TrimSpace(input.StrategyVersion)
	if version == "" || input.TradingDays < 0 {
		return ForwardValidationMetrics{}, fmt.Errorf("%w: strategy version and non-negative trading days are required", ErrInvalidForwardSample)
	}
	result := ForwardValidationMetrics{
		StrategyVersion: version,
		State:           ForwardValidationPending,
		TradingDays:     input.TradingDays,
		ClosedTrades:    len(input.Trades),
		PendingReasons:  []string{},
	}
	seenRules := make(map[string]bool, len(input.Trades))
	dailyReturns := make(map[string][]float64)
	recommendationDays, err := validateRecommendationDays(input.RecommendationDays)
	if err != nil {
		return ForwardValidationMetrics{}, err
	}
	grossProfits := 0.0
	grossLosses := 0.0
	for index, sample := range input.Trades {
		if err := validateForwardTradeSample(version, index, sample, seenRules); err != nil {
			return ForwardValidationMetrics{}, err
		}
		seenRules[sample.RuleID] = true
		result.NetMeanReturnPct += sample.NetReturnPct
		if len(input.RecommendationDays) == 0 {
			recommendationDays[sample.RecommendationDay] = true
		}
		if sample.BenchmarkReturnPct != nil {
			result.BenchmarkExcessMeanPct += sample.NetReturnPct - *sample.BenchmarkReturnPct
			result.ComparableTrades++
		}
		dailyReturns[sample.RecommendationDay] = append(dailyReturns[sample.RecommendationDay], sample.NetReturnPct)
		if sample.NetPnL > 0 {
			grossProfits += sample.NetPnL
		} else if sample.NetPnL < 0 {
			grossLosses += -sample.NetPnL
		}
	}
	if result.ClosedTrades > 0 {
		result.NetMeanReturnPct /= float64(result.ClosedTrades)
	}
	if result.ComparableTrades > 0 {
		result.BenchmarkExcessMeanPct /= float64(result.ComparableTrades)
	}
	if grossLosses > 0 {
		profitFactor := grossProfits / grossLosses
		result.ProfitFactor = &profitFactor
	} else if grossProfits > 0 {
		result.ProfitFactorInfinite = true
	}

	dayKeys := make([]string, 0, len(dailyReturns))
	for day := range dailyReturns {
		dayKeys = append(dayKeys, day)
	}
	sort.Strings(dayKeys)
	dayMeans := make([]float64, 0, len(dayKeys))
	for _, day := range dayKeys {
		values := dailyReturns[day]
		sum := 0.0
		for _, value := range values {
			sum += value
		}
		dayMeans = append(dayMeans, sum/float64(len(values)))
	}
	result.RecommendationDays = len(recommendationDays)
	result.RecommendationDayMeanPct, result.RecommendationDayLowerBound90Pct = oneSided90LowerBound(dayMeans)

	if result.TradingDays < minimumForwardTradingDays {
		result.PendingReasons = append(result.PendingReasons, "requires_60_trading_days")
	}
	if result.ClosedTrades < minimumForwardClosedTrades {
		result.PendingReasons = append(result.PendingReasons, "requires_100_closed_trades")
	}
	if result.ComparableTrades < minimumForwardClosedTrades {
		result.PendingReasons = append(result.PendingReasons, "requires_100_benchmark_comparable_trades")
	}
	if result.RecommendationDays < minimumForwardRecommendationDays {
		result.PendingReasons = append(result.PendingReasons, "requires_40_recommendation_days")
	}
	result.RequirementsMet = len(result.PendingReasons) == 0

	profitFactorPass := result.ProfitFactorInfinite || (result.ProfitFactor != nil && *result.ProfitFactor >= minimumForwardProfitFactor)
	if result.NetMeanReturnPct < minimumForwardNetMeanPct {
		result.PendingReasons = append(result.PendingReasons, "net_mean_below_0.75_pct")
	}
	if result.BenchmarkExcessMeanPct < minimumForwardExcessMeanPct {
		result.PendingReasons = append(result.PendingReasons, "benchmark_excess_below_0.50_pct")
	}
	if !profitFactorPass {
		result.PendingReasons = append(result.PendingReasons, "profit_factor_below_1.25")
	}
	if result.RecommendationDayLowerBound90Pct <= 0 {
		result.PendingReasons = append(result.PendingReasons, "recommendation_day_lower_bound_not_positive")
	}
	result.PerformanceThresholdsMet = result.NetMeanReturnPct >= minimumForwardNetMeanPct &&
		result.BenchmarkExcessMeanPct >= minimumForwardExcessMeanPct && profitFactorPass &&
		result.RecommendationDayLowerBound90Pct > 0
	if result.RequirementsMet && result.PerformanceThresholdsMet {
		result.State = ForwardValidationValidated
		result.PendingReasons = []string{}
	}
	return result, nil
}

func validateForwardTradeSample(version string, index int, sample ForwardTradeSample, seenRules map[string]bool) error {
	if strings.TrimSpace(sample.StrategyVersion) != version || strings.TrimSpace(sample.RuleID) == "" || seenRules[sample.RuleID] {
		return fmt.Errorf("%w: trade %d has a missing, duplicate, or cross-cohort identity", ErrInvalidForwardSample, index)
	}
	parsedDay, err := time.Parse(time.DateOnly, strings.TrimSpace(sample.RecommendationDay))
	if err != nil || parsedDay.Format(time.DateOnly) != strings.TrimSpace(sample.RecommendationDay) {
		return fmt.Errorf("%w: trade %d has invalid recommendation day", ErrInvalidForwardSample, index)
	}
	values := []float64{sample.NetReturnPct, sample.NetPnL}
	if sample.BenchmarkReturnPct != nil {
		values = append(values, *sample.BenchmarkReturnPct)
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%w: trade %d contains a non-finite metric", ErrInvalidForwardSample, index)
		}
	}
	return nil
}

func validateRecommendationDays(days []string) (map[string]bool, error) {
	result := make(map[string]bool, len(days))
	for index, raw := range days {
		day := strings.TrimSpace(raw)
		parsed, err := time.Parse(time.DateOnly, day)
		if err != nil || parsed.Format(time.DateOnly) != day || result[day] {
			return nil, fmt.Errorf("%w: recommendation day %d is invalid or duplicated", ErrInvalidForwardSample, index)
		}
		result[day] = true
	}
	return result, nil
}

func oneSided90LowerBound(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	if len(values) == 1 {
		return mean, 0
	}
	variance := 0.0
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	variance /= float64(len(values) - 1)
	lower := mean - oneSided90Z*math.Sqrt(variance/float64(len(values)))
	return mean, lower
}
