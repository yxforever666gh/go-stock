package data

import (
	"go-stock/backend/models"
	"testing"
	"time"
)

func TestExpandYieldReplayQueryWindow_ExtendsPrevAndNextTradeDay(t *testing.T) {
	loc := cnLocation()
	signalAt := time.Date(2026, 4, 2, 9, 30, 0, 0, loc)
	endAt := time.Date(2026, 4, 3, 10, 5, 0, 0, loc)

	gotStart, gotEnd := expandYieldReplayQueryWindow(signalAt, endAt, false)
	wantStart := time.Date(2026, 4, 1, 9, 31, 0, 0, loc)
	wantEnd := time.Date(2026, 4, 6, 15, 0, 0, 0, loc)

	if !gotStart.Equal(wantStart) {
		t.Fatalf("unexpected replay start: got=%v want=%v", gotStart, wantStart)
	}
	if !gotEnd.Equal(wantEnd) {
		t.Fatalf("unexpected replay end: got=%v want=%v", gotEnd, wantEnd)
	}
}

func TestExpandYieldReplayQueryWindow_AfterCloseUsesNextTradeAnchor(t *testing.T) {
	loc := cnLocation()
	signalAt := time.Date(2026, 4, 3, 20, 5, 0, 0, loc) // Friday after close, buy anchor is Monday.
	endAt := time.Date(2026, 4, 7, 9, 35, 0, 0, loc)

	gotStart, gotEnd := expandYieldReplayQueryWindow(signalAt, endAt, false)
	wantStart := time.Date(2026, 4, 3, 9, 31, 0, 0, loc)
	wantEnd := time.Date(2026, 4, 8, 15, 0, 0, 0, loc)

	if !gotStart.Equal(wantStart) {
		t.Fatalf("unexpected replay start: got=%v want=%v", gotStart, wantStart)
	}
	if !gotEnd.Equal(wantEnd) {
		t.Fatalf("unexpected replay end: got=%v want=%v", gotEnd, wantEnd)
	}
}

func TestExpandYieldReplayQueryWindow_HoldingBeforeOpenClampsToLatestClosedMinute(t *testing.T) {
	loc := cnLocation()
	defer func() { timeNow = time.Now }()
	timeNow = func() time.Time {
		return time.Date(2026, 4, 4, 6, 42, 0, 0, loc)
	}

	signalAt := time.Date(2026, 4, 2, 17, 51, 15, 0, loc)
	endAt := time.Date(2026, 4, 3, 16, 14, 51, 0, loc)

	gotStart, gotEnd := expandYieldReplayQueryWindow(signalAt, endAt, true)
	wantStart := time.Date(2026, 4, 2, 9, 31, 0, 0, loc)
	wantEnd := time.Date(2026, 4, 3, 15, 0, 0, 0, loc)

	if !gotStart.Equal(wantStart) {
		t.Fatalf("unexpected replay start: got=%v want=%v", gotStart, wantStart)
	}
	if !gotEnd.Equal(wantEnd) {
		t.Fatalf("unexpected replay end: got=%v want=%v", gotEnd, wantEnd)
	}
}

func TestResolveYieldReplayCurrentTime_NormalizesAfterCloseTimestamp(t *testing.T) {
	loc := cnLocation()
	state := &models.AiRecommendYieldRecordState{
		CurrentPriceTime: "2026-04-03 16:14:51",
	}

	got, ok := resolveYieldReplayCurrentTime(models.AiRecommendStocksYieldItem{}, state)
	if !ok {
		t.Fatal("expected normalized current time")
	}
	want := time.Date(2026, 4, 3, 15, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("unexpected current time: got=%v want=%v", got, want)
	}
}

func TestBuildYieldReplayMarkers_SignalApproximationDoesNotDowngradeChartStatus(t *testing.T) {
	loc := cnLocation()
	bars := []minuteBar{
		{
			TradeTime: time.Date(2026, 4, 3, 9, 30, 0, 0, loc),
			Open:      13.0,
			High:      13.2,
			Low:       12.9,
			Close:     13.1,
		},
		{
			TradeTime: time.Date(2026, 4, 3, 9, 31, 0, 0, loc),
			Open:      13.1,
			High:      13.3,
			Low:       13.0,
			Close:     13.2,
		},
	}
	signalAt := time.Date(2026, 4, 2, 17, 51, 15, 0, loc)

	markers, status, messages := buildYieldReplayMarkers(
		bars,
		signalAt,
		models.AiRecommendStocksYieldItem{
			PositionStatus: "持有",
		},
		nil,
	)

	if status != "ready" {
		t.Fatalf("expected ready status, got %q", status)
	}
	if len(markers) == 0 {
		t.Fatal("expected signal marker")
	}
	if len(messages) != 0 {
		t.Fatalf("expected no signal approximation message, got %v", messages)
	}
}

func TestBuildYieldReplayMarkers_BuyApproximationDoesNotDowngradeChartStatus(t *testing.T) {
	loc := cnLocation()
	bars := []minuteBar{
		{
			TradeTime: time.Date(2026, 4, 3, 9, 31, 0, 0, loc),
			Open:      13.0,
			High:      13.2,
			Low:       12.9,
			Close:     13.1,
		},
		{
			TradeTime: time.Date(2026, 4, 3, 9, 32, 0, 0, loc),
			Open:      13.1,
			High:      13.3,
			Low:       13.0,
			Close:     13.2,
		},
	}
	signalAt := time.Date(2026, 4, 3, 9, 31, 0, 0, loc)
	buyAt := time.Date(2026, 4, 3, 9, 30, 0, 0, loc)

	markers, status, messages := buildYieldReplayMarkers(
		bars,
		signalAt,
		models.AiRecommendStocksYieldItem{
			BuyTime: "2026-04-03 09:30:00",
		},
		&models.AiRecommendYieldRecordState{
			BuyTime: &buyAt,
		},
	)

	if status != "ready" {
		t.Fatalf("expected ready status, got %q", status)
	}
	if len(markers) < 2 {
		t.Fatalf("expected signal and buy markers, got %d", len(markers))
	}
	if len(messages) == 0 {
		t.Fatal("expected buy approximation message")
	}
}
