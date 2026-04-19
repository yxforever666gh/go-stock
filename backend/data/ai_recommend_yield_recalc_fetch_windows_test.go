package data

import (
	"testing"
	"time"
)

func TestBuildMinuteFetchWindows_AutoHeadBackfillForRecentSmallGap(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}

	defer func() { timeNow = time.Now }()
	timeNow = func() time.Time {
		return time.Date(2026, 4, 1, 4, 0, 0, 0, loc)
	}

	start := time.Date(2026, 3, 24, 9, 30, 0, 0, loc)
	end := time.Date(2026, 3, 31, 15, 0, 0, 0, loc)
	cacheStart := time.Date(2026, 3, 25, 9, 30, 0, 0, loc)
	cacheEnd := time.Date(2026, 3, 31, 15, 0, 0, 0, loc)

	windows := buildMinuteFetchWindows(start, end, &cacheStart, &cacheEnd, false)
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if !windows[0].Start.Equal(start) {
		t.Fatalf("unexpected head backfill start: %v", windows[0].Start)
	}
	wantEnd := cacheStart.Add(-time.Minute)
	if !windows[0].End.Equal(wantEnd) {
		t.Fatalf("unexpected head backfill end: got=%v want=%v", windows[0].End, wantEnd)
	}
}

func TestBuildMinuteFetchWindows_NoAutoHeadBackfillForOldGap(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}

	defer func() { timeNow = time.Now }()
	timeNow = func() time.Time {
		return time.Date(2026, 4, 1, 4, 0, 0, 0, loc)
	}

	start := time.Date(2026, 2, 20, 9, 30, 0, 0, loc)
	end := time.Date(2026, 3, 2, 15, 0, 0, 0, loc)
	cacheStart := time.Date(2026, 2, 24, 9, 30, 0, 0, loc)
	cacheEnd := time.Date(2026, 3, 31, 15, 0, 0, 0, loc)

	windows := buildMinuteFetchWindows(start, end, &cacheStart, &cacheEnd, false)
	if len(windows) != 0 {
		t.Fatalf("expected no auto head backfill windows for old gap, got %d", len(windows))
	}
}

func TestBuildMinuteFetchWindows_ManualHeadBackfillStillWorks(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}

	start := time.Date(2026, 2, 20, 9, 30, 0, 0, loc)
	end := time.Date(2026, 3, 2, 15, 0, 0, 0, loc)
	cacheStart := time.Date(2026, 2, 24, 9, 30, 0, 0, loc)
	cacheEnd := time.Date(2026, 3, 31, 15, 0, 0, 0, loc)

	windows := buildMinuteFetchWindows(start, end, &cacheStart, &cacheEnd, true)
	if len(windows) != 1 {
		t.Fatalf("expected manual head backfill window, got %d", len(windows))
	}
	if !windows[0].Start.Equal(start) {
		t.Fatalf("unexpected manual head backfill start: %v", windows[0].Start)
	}
	wantEnd := cacheStart.Add(-time.Minute)
	if wantEnd.After(end) {
		wantEnd = end
	}
	if !windows[0].End.Equal(wantEnd) {
		t.Fatalf("unexpected manual head backfill end: got=%v want=%v", windows[0].End, wantEnd)
	}
}
