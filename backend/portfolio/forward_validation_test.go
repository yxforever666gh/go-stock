package portfolio

import (
	"errors"
	"fmt"
	"testing"
)

func TestComputeForwardValidationRequiresSamplesAndPerformance(t *testing.T) {
	trades := make([]ForwardTradeSample, 0, 100)
	for index := 0; index < 100; index++ {
		day := 1 + index%40
		month := 7 + (day-1)/28
		monthDay := 1 + (day-1)%28
		netPnL := 100.0
		if index >= 75 {
			netPnL = -20
		}
		trades = append(trades, ForwardTradeSample{
			StrategyVersion: "1.5.0", RuleID: fmt.Sprintf("rule-%03d", index),
			RecommendationDay: fmt.Sprintf("2026-%02d-%02d", month, monthDay),
			NetReturnPct:      1, BenchmarkReturnPct: float64Pointer(0.2), NetPnL: netPnL,
		})
	}
	got, err := ComputeForwardValidation(ForwardValidationInput{StrategyVersion: "1.5.0", TradingDays: 60, Trades: trades})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ForwardValidationValidated || !got.RequirementsMet || !got.PerformanceThresholdsMet {
		t.Fatalf("expected validated metrics, got %+v", got)
	}
	if got.RecommendationDays != 40 || got.ClosedTrades != 100 || got.ComparableTrades != 100 || got.ProfitFactor == nil || *got.ProfitFactor != 15 {
		t.Fatalf("unexpected aggregate metrics: %+v", got)
	}

	got, err = ComputeForwardValidation(ForwardValidationInput{StrategyVersion: "1.5.0", TradingDays: 59, Trades: trades})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ForwardValidationPending || got.RequirementsMet {
		t.Fatalf("minimum sample gate must remain pending: %+v", got)
	}
}

func TestComputeForwardValidationRejectsCrossCohortAndDuplicateRules(t *testing.T) {
	input := ForwardValidationInput{StrategyVersion: "1.5.0", TradingDays: 60, Trades: []ForwardTradeSample{
		{StrategyVersion: "1.4.2", RuleID: "rule-1", RecommendationDay: "2026-08-01", NetReturnPct: 1, NetPnL: 1},
	}}
	if _, err := ComputeForwardValidation(input); !errors.Is(err, ErrInvalidForwardSample) {
		t.Fatalf("cross-cohort error = %v", err)
	}

	input.Trades = []ForwardTradeSample{
		{StrategyVersion: "1.5.0", RuleID: "rule-1", RecommendationDay: "2026-08-01", NetReturnPct: 1, NetPnL: 1},
		{StrategyVersion: "1.5.0", RuleID: "rule-1", RecommendationDay: "2026-08-02", NetReturnPct: 1, NetPnL: 1},
	}
	if _, err := ComputeForwardValidation(input); !errors.Is(err, ErrInvalidForwardSample) {
		t.Fatalf("duplicate rule error = %v", err)
	}
}

func TestComputeForwardValidationKeepsWeakPerformancePending(t *testing.T) {
	trades := make([]ForwardTradeSample, 0, 100)
	for index := 0; index < 100; index++ {
		trades = append(trades, ForwardTradeSample{
			StrategyVersion: "1.5.0", RuleID: fmt.Sprintf("weak-%03d", index),
			RecommendationDay: fmt.Sprintf("2026-%02d-%02d", 7+(index%40)/28, 1+(index%40)%28),
			NetReturnPct:      0.1, BenchmarkReturnPct: float64Pointer(0.2), NetPnL: -1,
		})
	}
	got, err := ComputeForwardValidation(ForwardValidationInput{StrategyVersion: "1.5.0", TradingDays: 60, Trades: trades})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ForwardValidationPending || !got.RequirementsMet || got.PerformanceThresholdsMet || len(got.PendingReasons) == 0 {
		t.Fatalf("weak performance must remain pending: %+v", got)
	}
}

func TestComputeForwardValidationRequiresOneHundredComparableTrades(t *testing.T) {
	trades := make([]ForwardTradeSample, 0, 100)
	for index := 0; index < 100; index++ {
		trades = append(trades, ForwardTradeSample{
			StrategyVersion: "1.5.0", RuleID: fmt.Sprintf("missing-benchmark-%03d", index),
			RecommendationDay: fmt.Sprintf("2026-%02d-%02d", 7+(index%40)/28, 1+(index%40)%28),
			NetReturnPct:      1, NetPnL: 10,
		})
	}
	got, err := ComputeForwardValidation(ForwardValidationInput{StrategyVersion: "1.5.0", TradingDays: 60, Trades: trades})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ForwardValidationPending || got.ComparableTrades != 0 || got.RequirementsMet {
		t.Fatalf("missing benchmark samples must remain pending: %+v", got)
	}
}

func float64Pointer(value float64) *float64 { return &value }
