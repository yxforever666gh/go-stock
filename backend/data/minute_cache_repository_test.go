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

	var mainRows int64
	if err := db.Dao.Table("ai_recommend_minute_bar").Count(&mainRows).Error; err != nil {
		t.Fatalf("count historical main-db rows failed: %v", err)
	}
	if mainRows != 0 {
		t.Fatalf("main-db historical cache received %d writes, want 0", mainRows)
	}
}

func TestMinuteCacheIgnoresAndPreservesHistoricalMainTable(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-no-legacy-access.db")
	tradeTime := normalizeMinuteTime(testMinuteTime(10, 1))
	if err := db.Dao.Exec(`INSERT INTO ai_recommend_minute_bar
		(stock_code, trade_time, open, high, low, close, volume, amount, source)
		VALUES (?, ?, 5, 6, 4, 5.5, 2000, 11000, 'historical')`, "300003.SZ", tradeTime).Error; err != nil {
		t.Fatalf("insert historical row failed: %v", err)
	}

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
	var historicalRows int64
	if err := db.Dao.Table("ai_recommend_minute_bar").Where("stock_code = ?", "300003.SZ").Count(&historicalRows).Error; err != nil {
		t.Fatalf("count historical rows failed: %v", err)
	}
	if historicalRows != 1 {
		t.Fatalf("historical main-db row count = %d, want preserved row", historicalRows)
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
