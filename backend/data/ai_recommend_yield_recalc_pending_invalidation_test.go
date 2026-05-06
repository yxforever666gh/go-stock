package data

import (
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
)

func TestParseExpectedCycleTradeDays(t *testing.T) {
	tests := []struct {
		input string
		want  int
		ok    bool
	}{
		{input: "1-2周", want: 10, ok: true},
		{input: "3-7个交易日", want: 7, ok: true},
		{input: "1-3天", want: 3, ok: true},
		{input: "1月", want: 21, ok: true},
		{input: "长期", want: 0, ok: false},
	}

	for _, tt := range tests {
		got, ok := parseExpectedCycleTradeDays(tt.input)
		if ok != tt.ok {
			t.Fatalf("%s: expected ok=%v, got %v", tt.input, tt.ok, ok)
		}
		if got != tt.want {
			t.Fatalf("%s: expected %d days, got %d", tt.input, tt.want, got)
		}
	}
}

func TestResolveRecommendExpectedCycleExpiry(t *testing.T) {
	loc := cnLocation()
	recordTime := time.Date(2026, 3, 20, 11, 30, 0, 0, loc)

	expiry, ok := resolveRecommendExpectedCycleExpiry(recordTime, "1-2周")
	if !ok {
		t.Fatal("expected expiry to be resolved")
	}
	if expiry.Before(recordTime) {
		t.Fatalf("expected expiry after record time, got %s", expiry.Format("2006-01-02 15:04:05"))
	}
	if expiry.Hour() != 15 || expiry.Minute() != 0 {
		t.Fatalf("expected expiry at close, got %s", expiry.Format("2006-01-02 15:04:05"))
	}
}

func TestResolvePendingRecommendInvalidation_StopLossTriggered(t *testing.T) {
	loc := cnLocation()
	recordTime := time.Date(2026, 3, 20, 9, 30, 0, 0, loc)
	evalEnd := time.Date(2026, 3, 24, 15, 0, 0, 0, loc)
	rec := models.AiRecommendStocks{
		RecommendStopLossPrice: "84.8",
		InvalidCondition:       "跌破84.8并持续放量走弱",
	}
	bars := []minuteBar{
		{TradeTime: time.Date(2026, 3, 21, 9, 31, 0, 0, loc), Open: 85.2, High: 85.6, Low: 85.0, Close: 85.3},
		{TradeTime: time.Date(2026, 3, 24, 9, 35, 0, 0, loc), Open: 84.9, High: 85.1, Low: 84.6, Close: 84.7},
	}

	reason, status, done := resolvePendingRecommendInvalidation(rec, recordTime, evalEnd, bars, true)
	if !done {
		t.Fatal("expected pending recommendation to be invalid after stop loss breach")
	}
	if status != "invalid" {
		t.Fatalf("expected invalid status, got %s", status)
	}
	if !strings.Contains(reason, "跌破止损/失效位") {
		t.Fatalf("expected stop loss reason, got %s", reason)
	}
}

func TestResolvePendingRecommendInvalidation_ExpectedCycleExpired(t *testing.T) {
	loc := cnLocation()
	recordTime := time.Date(2026, 3, 20, 9, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		RecommendStopLossPrice: "84.8",
		ExpectedCycle:          "1-3天",
		InvalidCondition:       "若3天内无法触发则失效",
	}
	bars := []minuteBar{
		{TradeTime: time.Date(2026, 3, 20, 9, 31, 0, 0, loc), Open: 86.0, High: 86.2, Low: 85.5, Close: 85.8},
		{TradeTime: time.Date(2026, 3, 21, 9, 31, 0, 0, loc), Open: 85.9, High: 86.1, Low: 85.2, Close: 85.5},
	}
	expiry, ok := resolveRecommendExpectedCycleExpiry(recordTime, rec.ExpectedCycle)
	if !ok {
		t.Fatal("expected cycle expiry")
	}

	reason, status, done := resolvePendingRecommendInvalidation(rec, recordTime, expiry, bars, true)
	if !done {
		t.Fatal("expected pending recommendation to be expired after expected cycle expiry")
	}
	if status != "expired" {
		t.Fatalf("expected expired status, got %s", status)
	}
	if !strings.Contains(reason, "超过待激活有效期") {
		t.Fatalf("expected expiry reason, got %s", reason)
	}
}

func TestResolveRecommendPendingActivationExpiry_CapsToFiveTradeDays(t *testing.T) {
	loc := cnLocation()
	recordTime := time.Date(2026, 3, 20, 9, 30, 0, 0, loc)

	expiry, label, ok := resolveRecommendPendingActivationExpiry(recordTime, "1-2周")
	if !ok {
		t.Fatal("expected pending activation expiry to be resolved")
	}
	if label != "5个交易日" {
		t.Fatalf("expected capped label 5个交易日, got %s", label)
	}
	want, ok := resolveRecommendTradeDayExpiry(recordTime, 5)
	if !ok {
		t.Fatal("expected trade-day expiry helper to resolve")
	}
	if !expiry.Equal(want) {
		t.Fatalf("expected capped expiry %s, got %s", want.Format("2006-01-02 15:04:05"), expiry.Format("2006-01-02 15:04:05"))
	}
}
