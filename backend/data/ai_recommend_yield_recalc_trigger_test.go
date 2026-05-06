package data

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestScanMinuteTriggerFromBars_UseOpenPriceForGapUpStopProfit(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	profit := 110.0
	bars := []minuteBar{{
		TradeTime: time.Date(2026, 3, 9, 9, 31, 0, 0, loc),
		Open:      115.0,
		High:      116.0,
		Low:       114.0,
		Close:     115.5,
	}}

	status, when, price := scanMinuteTriggerFromBars(bars, &profit, nil)
	if status != "已止盈" {
		t.Fatalf("expected 已止盈, got %s", status)
	}
	if !when.Equal(bars[0].TradeTime) {
		t.Fatalf("unexpected trigger time: %v", when)
	}
	if math.Abs(price-115.0) > 0.0001 {
		t.Fatalf("expected open price 115.0, got %.4f", price)
	}
}

func TestScanMinuteTriggerFromBars_UseOpenPriceForGapDownStopLoss(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	loss := 98.0
	bars := []minuteBar{{
		TradeTime: time.Date(2026, 3, 9, 9, 31, 0, 0, loc),
		Open:      95.0,
		High:      96.0,
		Low:       94.5,
		Close:     95.2,
	}}

	status, when, price := scanMinuteTriggerFromBars(bars, nil, &loss)
	if status != "已止损" {
		t.Fatalf("expected 已止损, got %s", status)
	}
	if !when.Equal(bars[0].TradeTime) {
		t.Fatalf("unexpected trigger time: %v", when)
	}
	if math.Abs(price-95.0) > 0.0001 {
		t.Fatalf("expected open price 95.0, got %.4f", price)
	}
}

func TestScanMinuteTriggerFromBars_KeepThresholdPriceForIntrabarTouch(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	profit := 110.0
	loss := 98.0
	bars := []minuteBar{{
		TradeTime: time.Date(2026, 3, 9, 9, 32, 0, 0, loc),
		Open:      100.0,
		High:      111.0,
		Low:       99.0,
		Close:     109.0,
	}}

	status, _, price := scanMinuteTriggerFromBars(bars, &profit, &loss)
	if status != "已止盈" {
		t.Fatalf("expected 已止盈, got %s", status)
	}
	if math.Abs(price-110.0) > 0.0001 {
		t.Fatalf("expected threshold price 110.0, got %.4f", price)
	}
}

func TestScanActivationByBreakoutRule_RequiresCloseConfirmation(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	rule := &activationRule{
		SignalType:       "price_breakout_with_volume",
		ThresholdValue:   22.65,
		ThresholdMax:     22.99,
		Baseline:         "manual_amount",
		VolumeRatio:      1,
		ConfirmBars:      1,
		VolumeWindow:     5,
		VolumeMetric:     "amount",
		EvaluationWindow: "5m",
	}
	bars := []minuteBar{{
		TradeTime: time.Date(2026, 4, 30, 10, 5, 0, 0, loc),
		Open:      22.50,
		High:      22.80,
		Low:       22.40,
		Close:     22.60,
		Volume:    1000,
		Amount:    226000,
	}}

	scan := scanActivationByBreakoutRule(rule, bars)
	if scan.Triggered {
		t.Fatalf("expected no activation when only high touches breakout, got %.2f", scan.Price)
	}
}

func TestScanActivationByBreakoutRule_BlocksCloseAboveMaxEntry(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	rule := &activationRule{
		SignalType:       "price_breakout_with_volume",
		ThresholdValue:   22.65,
		ThresholdMax:     22.99,
		Baseline:         "manual_amount",
		VolumeRatio:      1,
		ConfirmBars:      1,
		VolumeWindow:     5,
		VolumeMetric:     "amount",
		EvaluationWindow: "5m",
	}
	bars := []minuteBar{{
		TradeTime: time.Date(2026, 4, 30, 10, 9, 0, 0, loc),
		Open:      23.10,
		High:      23.40,
		Low:       23.05,
		Close:     23.35,
		Volume:    1000,
		Amount:    233500,
	}}

	scan := scanActivationByBreakoutRule(rule, bars)
	if scan.Triggered {
		t.Fatalf("expected no activation above max entry, got %.2f", scan.Price)
	}
	if !strings.Contains(scan.Reason, "收盘价 23.35 超过追价上限 22.99") {
		t.Fatalf("unexpected reason: %s", scan.Reason)
	}
}

func TestScanActivationByBreakoutRule_ActivatesWithinMaxEntry(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	rule := &activationRule{
		SignalType:       "price_breakout_with_volume",
		ThresholdValue:   22.65,
		ThresholdMax:     22.99,
		Baseline:         "manual_amount",
		VolumeRatio:      1,
		ConfirmBars:      1,
		VolumeWindow:     5,
		VolumeMetric:     "amount",
		EvaluationWindow: "5m",
	}
	bars := []minuteBar{{
		TradeTime: time.Date(2026, 4, 30, 10, 9, 0, 0, loc),
		Open:      22.60,
		High:      22.90,
		Low:       22.55,
		Close:     22.80,
		Volume:    1000,
		Amount:    228000,
	}}

	scan := scanActivationByBreakoutRule(rule, bars)
	if !scan.Triggered {
		t.Fatalf("expected activation within max entry, got reason=%s", scan.Reason)
	}
	if math.Abs(scan.Price-22.80) > 0.0001 {
		t.Fatalf("expected buy price 22.80, got %.4f", scan.Price)
	}
}
