package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
)

func initMinuteCacheTestDB(t *testing.T, name string) {
	t.Helper()
	t.Setenv("GO_STOCK_DB_LOG_LEVEL", "silent")
	initDatabaseForTest(t, filepath.Join(t.TempDir(), name))
}

func testMinuteTime(hour, minute int) time.Time {
	return time.Date(2026, 6, 2, hour, minute, 0, int(123*time.Millisecond), cnLocation())
}

func TestMinuteCacheUsesOnlyMinuteDatabase(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-isolated.db")
	tradeTime := testMinuteTime(9, 31)

	inserted, err := upsertMinuteBarsToCache(" 300001.sz ", []minuteBar{{
		TradeTime: tradeTime,
		Open:      10.111,
		High:      10.229,
		Low:       10.01,
		Close:     10.2,
		Volume:    1000,
		Amount:    10200,
	}}, "test")
	if err != nil || inserted != 1 {
		t.Fatalf("upsert minute bars = %d, %v; want 1, nil", inserted, err)
	}

	bars, err := listMinuteBarsFromCache("300001.SZ", tradeTime.Add(-time.Minute), tradeTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("list minute bars failed: %v", err)
	}
	if len(bars) != 1 || bars[0].Open != 10.11 || bars[0].High != 10.23 || bars[0].Source != "test" {
		t.Fatalf("unexpected cached bars: %+v", bars)
	}

	if db.Dao.Migrator().HasTable("ai_recommend_minute_bar") {
		t.Fatal("minute cache recreated the archived main-db strategy table")
	}
}

func TestMinuteCacheDoesNotRecreateArchivedMainTable(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-no-legacy-access.db")
	tradeTime := normalizeMinuteTime(testMinuteTime(10, 1))

	bars, err := listMinuteBarsFromCache("300003.SZ", tradeTime, tradeTime)
	if err != nil {
		t.Fatalf("list minute bars failed: %v", err)
	}
	if len(bars) != 0 {
		t.Fatalf("historical main-db row leaked into 1.6 runtime: %+v", bars)
	}
	if err := deleteMinuteBarsCache("300003.SZ"); err != nil {
		t.Fatalf("delete minute cache failed: %v", err)
	}
	if db.Dao.Migrator().HasTable("ai_recommend_minute_bar") {
		t.Fatal("minute cache access recreated the archived main-db strategy table")
	}
}

func TestMinuteCacheUpsertOverwritesMinuteDatabase(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-overwrite.db")
	tradeTime := testMinuteTime(9, 32)
	if _, err := upsertMinuteBarsToCache("300002.SZ", []minuteBar{{TradeTime: tradeTime, Open: 1, High: 2, Low: 1, Close: 2}}, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := upsertMinuteBarsToCache("300002.SZ", []minuteBar{{TradeTime: tradeTime, Open: 3, High: 4, Low: 3, Close: 4}}, "second"); err != nil {
		t.Fatal(err)
	}
	bars, err := listMinuteBarsFromCache("300002.SZ", tradeTime, tradeTime)
	if err != nil || len(bars) != 1 || bars[0].Close != 4 {
		t.Fatalf("bars = %+v, err = %v; want overwritten close=4", bars, err)
	}
}

func TestMinuteBarSourceProvesUnadjusted(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{source: "sina", want: true},
		{source: "tencent", want: true},
		{source: "diemeng", want: true},
		{source: "diemeng_dump", want: true},
		{source: "akshare:em", want: true},
		{source: "akshare:sina:adjustment=none", want: true},
		{source: "akshare:sina", want: false},
		{source: "akshare:sina:adjustment=qfq", want: false},
		{source: "akshare:sina:adjustment=hfq", want: false},
		{source: "", want: false},
		{source: "unknown", want: false},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			if got := minuteBarSourceProvesUnadjusted(test.source); got != test.want {
				t.Fatalf("minuteBarSourceProvesUnadjusted(%q)=%v want %v", test.source, got, test.want)
			}
		})
	}
}
