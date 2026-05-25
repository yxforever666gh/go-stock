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

	if marketSummaryCurrentVersion != marketSummaryVersionV132 {
		t.Fatalf("marketSummaryCurrentVersion = %q, want %q", marketSummaryCurrentVersion, marketSummaryVersionV132)
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
