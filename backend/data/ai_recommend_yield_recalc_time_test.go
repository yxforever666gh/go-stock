package data

import (
	"go-stock/backend/models"
	"testing"
	"time"
)

func TestRecommendRecordTimePrefersDataTime(t *testing.T) {
	created := time.Date(2026, 3, 1, 9, 30, 0, 0, time.Local)
	data := time.Date(2026, 2, 28, 14, 5, 0, 0, time.Local)
	item := models.AiRecommendStocks{DataTime: &data}
	item.CreatedAt = created

	got := recommendRecordTime(item)
	if !got.Equal(data) {
		t.Fatalf("expected data_time, got=%v", got)
	}
}

func TestRecommendRecordTimeFallbackCreatedAt(t *testing.T) {
	created := time.Date(2026, 3, 1, 9, 30, 0, 0, time.Local)
	item := models.AiRecommendStocks{}
	item.CreatedAt = created

	got := recommendRecordTime(item)
	if !got.Equal(created) {
		t.Fatalf("expected created_at, got=%v", got)
	}
}

func TestResolveRecommendBuyTime_BeforeOpenUsesSameDayOpen(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 9, 8, 45, 0, 0, loc)

	got := resolveRecommendBuyTime(recordTime)
	want := time.Date(2026, 3, 9, 9, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("unexpected buy time: got=%v want=%v", got, want)
	}
}

func TestResolveRecommendBuyTime_DuringTradingUsesRecommendMinute(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 9, 10, 16, 42, 0, loc)

	got := resolveRecommendBuyTime(recordTime)
	want := time.Date(2026, 3, 9, 10, 16, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("unexpected buy time: got=%v want=%v", got, want)
	}
}

func TestResolveRecommendBuyTime_LunchBreakUsesAfternoonOpen(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 9, 12, 8, 0, 0, loc)

	got := resolveRecommendBuyTime(recordTime)
	want := time.Date(2026, 3, 9, 13, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("unexpected buy time: got=%v want=%v", got, want)
	}
}

func TestResolveRecommendBuyTime_AfterCloseUsesNextTradeOpen(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 6, 20, 5, 0, 0, loc) // Friday after close

	got := resolveRecommendBuyTime(recordTime)
	want := time.Date(2026, 3, 9, 9, 30, 0, 0, loc) // Monday open
	if !got.Equal(want) {
		t.Fatalf("unexpected buy time: got=%v want=%v", got, want)
	}
}

func TestResolveRecommendSellEligibleTime_UsesBuyNextTradeOpen(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 9, 10, 16, 42, 0, loc) // Monday during trading

	got := resolveRecommendSellEligibleTime(recordTime)
	want := time.Date(2026, 3, 10, 9, 30, 0, 0, loc) // T+1 Tuesday open
	if !got.Equal(want) {
		t.Fatalf("unexpected sell eligible time: got=%v want=%v", got, want)
	}
}
