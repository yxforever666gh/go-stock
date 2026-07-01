package data

import (
	"go-stock/backend/models"
	"strings"
	"testing"
	"time"
)

func TestNormalizeStrategyCohortV132Aliases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "current142", raw: "1.4.2", want: marketSummaryVersion142},
		{name: "v142", raw: "v1.4.2", want: marketSummaryVersion142},
		{name: "short142", raw: "142", want: marketSummaryVersion142},
		{name: "current141", raw: "1.4.1", want: marketSummaryVersion141},
		{name: "v141", raw: "v1.4.1", want: marketSummaryVersion141},
		{name: "short141", raw: "141", want: marketSummaryVersion141},
		{name: "current140", raw: "1.4.0", want: marketSummaryVersion140},
		{name: "v140", raw: "v1.4.0", want: marketSummaryVersion140},
		{name: "short140", raw: "140", want: marketSummaryVersion140},
		{name: "previous136", raw: "1.3.6", want: marketSummaryVersion136},
		{name: "v136", raw: "v1.3.6", want: marketSummaryVersion136},
		{name: "short136", raw: "136", want: marketSummaryVersion136},
		{name: "v132", raw: "v1.3.2", want: marketSummaryVersionV132},
		{name: "plain132", raw: "1.3.2", want: marketSummaryVersionV132},
		{name: "v131", raw: "1.3.1", want: marketSummaryPhase4Version},
		{name: "current", raw: "current", want: strategyCohortCurrent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeStrategyCohort(tt.raw, strategyCohortAll); got != tt.want {
				t.Fatalf("normalizeStrategyCohort(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}

	if marketSummaryCurrentVersion != marketSummaryVersion142 {
		t.Fatalf("marketSummaryCurrentVersion = %q, want %q", marketSummaryCurrentVersion, marketSummaryVersion142)
	}
}

func TestEvaluateV132ActivationGateOnlyAppliesToV132(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	bars := []minuteBar{{
		TradeTime: activationTime.Add(-time.Minute),
		Open:      10,
		Close:     10,
		Volume:    100,
		Amount:    1000,
	}}

	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryPhase4Version,
		RecommendStopProfitPriceMin: 10.5,
		RecommendStopProfitPrice:    "10.5",
		RecommendStopLossPrice:      "9.9",
		StockCurrentPrice:           "10",
	}

	gate := evaluateV132ActivationGate(rec, activationTime, 10, bars)
	if !gate.Allowed {
		t.Fatalf("non-v1.3.2 record should bypass gate, got blocked: %s", gate.Reason)
	}
}

func TestEvaluateV132ActivationGateBlocksWeakRewardRisk(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersionV132,
		RecommendStopProfitPriceMin: 10.1,
		RecommendStopProfitPrice:    "10.1",
		RecommendStopLossPrice:      "9.9",
		StockCurrentPrice:           "10",
	}

	gate := evaluateV132ActivationGate(rec, activationTime, 10, []minuteBar{{
		TradeTime: activationTime.Add(-time.Minute),
		Open:      10,
		Close:     10,
		Volume:    100,
		Amount:    1000,
	}})
	if gate.Allowed {
		t.Fatalf("v1.3.2 record should be blocked by weak reward/risk")
	}
	if !strings.Contains(gate.Reason, "盈亏比") {
		t.Fatalf("gate reason = %q, want reward/risk reason", gate.Reason)
	}
}

func TestV132VWAPNormalizesHandVolumeAmount(t *testing.T) {
	bars := []minuteBar{
		{Open: 110.6, Close: 110.8, Volume: 100, Amount: 1108000},
		{Open: 111.0, Close: 111.2, Volume: 200, Amount: 2224000},
	}

	got := v132VWAP(bars)
	if got < 110.9 || got > 111.1 {
		t.Fatalf("v132VWAP=%f, want price-level VWAP near 111", got)
	}
}

func TestV132VWAPKeepsShareVolumeAmount(t *testing.T) {
	bars := []minuteBar{
		{Open: 110.6, Close: 110.8, Volume: 10000, Amount: 1108000},
		{Open: 111.0, Close: 111.2, Volume: 20000, Amount: 2224000},
	}

	got := v132VWAP(bars)
	if got < 110.9 || got > 111.1 {
		t.Fatalf("v132VWAP=%f, want existing price-level VWAP near 111", got)
	}
}

func TestV132VWAPFallsBackForUnusableAmountVolumeScale(t *testing.T) {
	bars := []minuteBar{
		{Open: 9.9, Close: 10.0, Volume: 1, Amount: 999999},
		{Open: 10.1, Close: 10.2, Volume: 1, Amount: 999999},
	}

	got := v132VWAP(bars)
	if got < 10.0 || got > 10.2 {
		t.Fatalf("v132VWAP=%f, want fallback price average near 10.1", got)
	}
}

func TestEvaluateV132ActivationGateUsesNormalizedVWAP(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersionV132,
		RecommendStopProfitPriceMin: 130,
		RecommendStopProfitPrice:    "130",
		RecommendStopLossPrice:      "106",
		StockCurrentPrice:           "110",
	}
	bars := []minuteBar{{
		TradeTime: activationTime.Add(-time.Minute),
		Open:      110.6,
		Close:     110.8,
		Volume:    100,
		Amount:    1108000,
	}}

	gate := evaluateV132ActivationGate(rec, activationTime, 111, bars)
	if !gate.Allowed {
		t.Fatalf("normalized VWAP should allow activation, got blocked: %s", gate.Reason)
	}
}

func TestEvaluateV136ActivationGateAllowsRewardRiskGrayZoneWithStrengthConfirm(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersion136,
		RecommendStopProfitPriceMin: 10.1,
		RecommendStopProfitPrice:    "10.1",
		RecommendStopLossPrice:      "9.9",
		StockCurrentPrice:           "10",
	}
	bars := make([]minuteBar, 0, 20)
	for i := 20; i > 0; i-- {
		bars = append(bars, minuteBar{
			TradeTime: activationTime.Add(-time.Duration(i) * time.Minute),
			Open:      10,
			Close:     10,
			Volume:    100,
			Amount:    1000,
		})
	}

	gate := evaluateV132ActivationGate(rec, activationTime, 10, bars)
	if !gate.Allowed {
		t.Fatalf("v1.3.6 gray reward/risk should pass after strength confirm, got blocked: %s", gate.Reason)
	}
}

func TestEvaluateV136ActivationGateBlocksBelowHardRewardRiskFloor(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersion136,
		RecommendStopProfitPriceMin: 10.05,
		RecommendStopProfitPrice:    "10.05",
		RecommendStopLossPrice:      "9.9",
		StockCurrentPrice:           "10",
	}
	bars := make([]minuteBar, 0, 20)
	for i := 20; i > 0; i-- {
		bars = append(bars, minuteBar{
			TradeTime: activationTime.Add(-time.Duration(i) * time.Minute),
			Open:      10,
			Close:     10,
			Volume:    100,
			Amount:    1000,
		})
	}

	gate := evaluateV132ActivationGate(rec, activationTime, 10, bars)
	if gate.Allowed {
		t.Fatalf("v1.3.6 record should be blocked below hard reward/risk floor")
	}
	if !strings.Contains(gate.Reason, "硬底线") {
		t.Fatalf("gate reason = %q, want hard floor reason", gate.Reason)
	}
}

func TestEvaluateV140ActivationGateReusesV136HardRules(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersion140,
		RecommendStopProfitPriceMin: 10.05,
		RecommendStopProfitPrice:    "10.05",
		RecommendStopLossPrice:      "9.9",
		StockCurrentPrice:           "10",
	}
	bars := make([]minuteBar, 0, 20)
	for i := 20; i > 0; i-- {
		bars = append(bars, minuteBar{
			TradeTime: activationTime.Add(-time.Duration(i) * time.Minute),
			Open:      10,
			Close:     10,
			Volume:    100,
			Amount:    1000,
		})
	}

	gate := evaluateV132ActivationGate(rec, activationTime, 10, bars)
	if gate.Allowed {
		t.Fatalf("v1.4.0 record should reuse v1.3.6 hard reward/risk floor")
	}
	if gate.Kind != "reward_risk" {
		t.Fatalf("gate kind = %q, want reward_risk", gate.Kind)
	}
}

func TestEvaluateV141ActivationGateReusesV136HardRules(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersion141,
		RecommendStopProfitPriceMin: 10.05,
		RecommendStopProfitPrice:    "10.05",
		RecommendStopLossPrice:      "9.9",
		StockCurrentPrice:           "10",
	}
	bars := make([]minuteBar, 0, 20)
	for i := 20; i > 0; i-- {
		bars = append(bars, minuteBar{
			TradeTime: activationTime.Add(-time.Duration(i) * time.Minute),
			Open:      10,
			Close:     10,
			Volume:    100,
			Amount:    1000,
		})
	}

	gate := evaluateV132ActivationGate(rec, activationTime, 10, bars)
	if gate.Allowed {
		t.Fatalf("v1.4.1 record should reuse v1.3.6 hard reward/risk floor")
	}
	if gate.Kind != "reward_risk" {
		t.Fatalf("gate kind = %q, want reward_risk", gate.Kind)
	}
}

func TestEvaluateV136ActivationGateRequiresTwentyMinuteStrengthConfirm(t *testing.T) {
	activationTime := time.Date(2026, 5, 25, 10, 0, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersion136,
		RecommendStopProfitPriceMin: 11,
		RecommendStopProfitPrice:    "11",
		RecommendStopLossPrice:      "9.8",
		StockCurrentPrice:           "10",
	}
	bars := make([]minuteBar, 0, 19)
	for i := 19; i > 0; i-- {
		bars = append(bars, minuteBar{
			TradeTime: activationTime.Add(-time.Duration(i) * time.Minute),
			Open:      10,
			Close:     10,
			Volume:    100,
			Amount:    1000,
		})
	}

	gate := evaluateV132ActivationGate(rec, activationTime, 10, bars)
	if gate.Allowed {
		t.Fatalf("v1.3.6 record should wait for at least 20 same-day bars")
	}
	if !strings.Contains(gate.Reason, "少于20根") {
		t.Fatalf("gate reason = %q, want 20-bar confirm reason", gate.Reason)
	}
}
