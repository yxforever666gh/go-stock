package data

import (
	"strings"
	"testing"
	"time"
)

func TestPassesVolumeRuleWithPercentileFallsBackWithoutRecursion(t *testing.T) {
	loc := cnLocation()
	baseTime := time.Date(2026, 3, 10, 9, 31, 0, 0, loc)
	bars := []minuteBar{
		{TradeTime: baseTime.Add(-5 * time.Minute), Close: 10.00, Amount: 100},
		{TradeTime: baseTime.Add(-4 * time.Minute), Close: 10.01, Amount: 100},
		{TradeTime: baseTime.Add(-3 * time.Minute), Close: 10.02, Amount: 100},
		{TradeTime: baseTime.Add(-2 * time.Minute), Close: 10.03, Amount: 100},
		{TradeTime: baseTime.Add(-1 * time.Minute), Close: 10.04, Amount: 100},
		{TradeTime: baseTime, Close: 10.05, Amount: 1000},
	}

	for _, baselineType := range []string{"percentile", "adaptive"} {
		t.Run(baselineType, func(t *testing.T) {
			rule := &activationRule{
				Baseline:           "avg_amount_5x5m",
				VolumeBaselineType: baselineType,
				VolumePercentile:   70,
				VolumeRatio:        2,
				VolumeWindow:       5,
			}

			passed, reason := passesVolumeRule(rule, bars, len(bars)-1)
			if !passed {
				t.Fatalf("expected fallback traditional volume rule to pass, reason: %s", reason)
			}
			if reason != "" {
				t.Fatalf("expected empty reason, got %q", reason)
			}
		})
	}
}

func TestPassesVolumeRuleWithPercentileUsesHistoricalBaseline(t *testing.T) {
	loc := cnLocation()
	currentTime := time.Date(2026, 3, 10, 9, 31, 0, 0, loc)
	bars := []minuteBar{
		{TradeTime: time.Date(2026, 3, 3, 9, 31, 0, 0, loc), Close: 10.00, Amount: 1000},
		{TradeTime: time.Date(2026, 3, 4, 9, 31, 0, 0, loc), Close: 10.00, Amount: 1000},
		{TradeTime: time.Date(2026, 3, 5, 9, 31, 0, 0, loc), Close: 10.00, Amount: 1000},
		{TradeTime: time.Date(2026, 3, 6, 9, 31, 0, 0, loc), Close: 10.00, Amount: 1000},
		{TradeTime: time.Date(2026, 3, 9, 9, 31, 0, 0, loc), Close: 10.00, Amount: 1000},
		{TradeTime: currentTime.Add(-5 * time.Minute), Close: 10.00, Amount: 100},
		{TradeTime: currentTime.Add(-4 * time.Minute), Close: 10.00, Amount: 100},
		{TradeTime: currentTime.Add(-3 * time.Minute), Close: 10.00, Amount: 100},
		{TradeTime: currentTime.Add(-2 * time.Minute), Close: 10.00, Amount: 100},
		{TradeTime: currentTime.Add(-1 * time.Minute), Close: 10.00, Amount: 100},
		{TradeTime: currentTime, Close: 10.00, Amount: 1500},
	}
	rule := &activationRule{
		Baseline:           "avg_amount_5x5m",
		VolumeBaselineType: "percentile",
		VolumePercentile:   70,
		VolumeRatio:        2,
		VolumeWindow:       5,
	}

	passed, reason := passesVolumeRule(rule, bars, len(bars)-1)
	if passed {
		t.Fatal("expected percentile baseline to block despite traditional average fallback being satisfied")
	}
	if !strings.Contains(reason, "近30日同时段P70基准") {
		t.Fatalf("expected percentile baseline reason, got %q", reason)
	}
}
