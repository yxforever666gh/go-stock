package data

import (
	"math"
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
