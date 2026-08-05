package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func initMinuteCacheTestDB(t *testing.T, name string) {
	t.Helper()
	t.Setenv("GO_STOCK_DB_LOG_LEVEL", "silent")
	t.Setenv("GO_STOCK_MINUTE_DUAL_WRITE", "")
	initDatabaseForTest(t, filepath.Join(t.TempDir(), name))
	if err := db.Dao.AutoMigrate(&models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate legacy minute table failed: %v", err)
	}
}

func testMinuteTime(hour, minute int) time.Time {
	return time.Date(2026, 6, 2, hour, minute, 0, int(123*time.Millisecond), cnLocation())
}

func TestMinuteCacheWritesMinuteDBByDefault(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-default.db")
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
	if err != nil {
		t.Fatalf("upsert minute bars failed: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("inserted = %d, want 1", inserted)
	}

	bars, err := listMinuteBarsFromCache("300001.SZ", tradeTime.Add(-time.Minute), tradeTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("list minute bars failed: %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("len(bars) = %d, want 1", len(bars))
	}
	if !bars[0].TradeTime.Equal(normalizeMinuteTime(tradeTime)) {
		t.Fatalf("trade time = %s, want %s", bars[0].TradeTime, normalizeMinuteTime(tradeTime))
	}
	if bars[0].Open != 10.11 || bars[0].High != 10.23 {
		t.Fatalf("rounded OHLC mismatch: %+v", bars[0])
	}

	var legacyCount int64
	if err := db.Dao.Model(&models.AiRecommendMinuteBar{}).Count(&legacyCount).Error; err != nil {
		t.Fatalf("count legacy rows failed: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("legacy rows = %d, want 0", legacyCount)
	}
}

func TestMinuteCacheUpsertOverwritesMinuteDB(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-overwrite.db")
	tradeTime := testMinuteTime(9, 32)

	if _, err := upsertMinuteBarsToCache("300002.SZ", []minuteBar{{TradeTime: tradeTime, Open: 1, High: 2, Low: 1, Close: 2}}, "first"); err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}
	if _, err := upsertMinuteBarsToCache("300002.SZ", []minuteBar{{TradeTime: tradeTime, Open: 3, High: 4, Low: 3, Close: 4}}, "second"); err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	var count int64
	if err := db.MinuteDao.Model(&minuteCacheDBBar{}).Where("stock_code = ?", "300002.SZ").Count(&count).Error; err != nil {
		t.Fatalf("count minute rows failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("minute row count = %d, want 1", count)
	}
	bars, err := listMinuteBarsFromCache("300002.SZ", tradeTime, tradeTime)
	if err != nil {
		t.Fatalf("list minute bars failed: %v", err)
	}
	if len(bars) != 1 || bars[0].Close != 4 {
		t.Fatalf("bars = %+v, want overwritten close=4", bars)
	}
}

func TestMinuteCacheFallbackReadsLegacyTable(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-fallback.db")
	tradeTime := normalizeMinuteTime(testMinuteTime(10, 1))
	if err := db.Dao.Create(&models.AiRecommendMinuteBar{
		StockCode: "300003.SZ",
		TradeTime: tradeTime,
		Open:      5,
		High:      6,
		Low:       4,
		Close:     5.5,
		Volume:    2000,
		Amount:    11000,
		Source:    "legacy",
	}).Error; err != nil {
		t.Fatalf("insert legacy minute row failed: %v", err)
	}

	bars, err := listMinuteBarsFromCache("300003.SZ", tradeTime, tradeTime)
	if err != nil {
		t.Fatalf("list minute bars failed: %v", err)
	}
	if len(bars) != 1 || bars[0].Close != 5.5 {
		t.Fatalf("fallback bars = %+v, want legacy close=5.5", bars)
	}
	start, end, err := getMinuteCacheRange("300003.SZ")
	if err != nil {
		t.Fatalf("get range failed: %v", err)
	}
	if start == nil || end == nil || !start.Equal(tradeTime) || !end.Equal(tradeTime) {
		t.Fatalf("range = %v %v, want %s", start, end, tradeTime)
	}
}

func TestMinuteCacheMergesMinuteDBAndLegacyRows(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-merge-rows.db")
	legacyTime := normalizeMinuteTime(testMinuteTime(9, 31))
	overlapTime := normalizeMinuteTime(testMinuteTime(9, 32))
	primaryTime := normalizeMinuteTime(testMinuteTime(9, 33))

	legacyRows := []models.AiRecommendMinuteBar{
		{StockCode: "300007.SZ", TradeTime: legacyTime, Open: 1, High: 1, Low: 1, Close: 1, Source: "legacy"},
		{StockCode: "300007.SZ", TradeTime: overlapTime, Open: 2, High: 2, Low: 2, Close: 2, Source: "legacy"},
	}
	if err := db.Dao.Create(&legacyRows).Error; err != nil {
		t.Fatalf("insert legacy rows failed: %v", err)
	}
	if _, err := upsertMinuteBarsToCache("300007.SZ", []minuteBar{
		{TradeTime: overlapTime, Open: 20, High: 20, Low: 20, Close: 20},
		{TradeTime: primaryTime, Open: 3, High: 3, Low: 3, Close: 3},
	}, "minute-db"); err != nil {
		t.Fatalf("upsert minute rows failed: %v", err)
	}

	bars, err := listMinuteBarsFromCache("300007.SZ", legacyTime, primaryTime)
	if err != nil {
		t.Fatalf("list merged bars failed: %v", err)
	}
	if len(bars) != 3 {
		t.Fatalf("len(bars) = %d, want 3: %+v", len(bars), bars)
	}
	if !bars[0].TradeTime.Equal(legacyTime) || bars[0].Close != 1 {
		t.Fatalf("legacy-only row mismatch: %+v", bars[0])
	}
	if !bars[1].TradeTime.Equal(overlapTime) || bars[1].Close != 20 {
		t.Fatalf("overlap row should prefer minute-db data: %+v", bars[1])
	}
	if !bars[2].TradeTime.Equal(primaryTime) || bars[2].Close != 3 {
		t.Fatalf("minute-db-only row mismatch: %+v", bars[2])
	}
}

func TestMinuteCacheRangeMergesMinuteDBAndLegacyRange(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-merge-range.db")
	legacyStart := normalizeMinuteTime(testMinuteTime(9, 31).AddDate(0, 0, -2))
	legacyEnd := normalizeMinuteTime(testMinuteTime(9, 32).AddDate(0, 0, -1))
	primaryStart := normalizeMinuteTime(testMinuteTime(9, 33))
	primaryEnd := normalizeMinuteTime(testMinuteTime(9, 34))

	legacyRows := []models.AiRecommendMinuteBar{
		{StockCode: "300008.SZ", TradeTime: legacyStart, Open: 1, High: 1, Low: 1, Close: 1, Source: "legacy"},
		{StockCode: "300008.SZ", TradeTime: legacyEnd, Open: 2, High: 2, Low: 2, Close: 2, Source: "legacy"},
	}
	if err := db.Dao.Create(&legacyRows).Error; err != nil {
		t.Fatalf("insert legacy rows failed: %v", err)
	}
	if _, err := upsertMinuteBarsToCache("300008.SZ", []minuteBar{
		{TradeTime: primaryStart, Open: 3, High: 3, Low: 3, Close: 3},
		{TradeTime: primaryEnd, Open: 4, High: 4, Low: 4, Close: 4},
	}, "minute-db"); err != nil {
		t.Fatalf("upsert minute rows failed: %v", err)
	}

	start, end, err := getMinuteCacheRange("300008.SZ")
	if err != nil {
		t.Fatalf("get merged range failed: %v", err)
	}
	if start == nil || end == nil || !start.Equal(legacyStart) || !end.Equal(primaryEnd) {
		t.Fatalf("range = %v %v, want %s %s", start, end, legacyStart, primaryEnd)
	}

	ranges, err := loadMinuteCacheRangeMapByCodes([]string{"300008.SZ"})
	if err != nil {
		t.Fatalf("load range map failed: %v", err)
	}
	rng := ranges["300008.SZ"]
	if rng.Start == nil || rng.End == nil || !rng.Start.Equal(legacyStart) || !rng.End.Equal(primaryEnd) {
		t.Fatalf("range map = %+v, want %s %s", rng, legacyStart, primaryEnd)
	}
}

func TestMinuteCacheDualWrite(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-dual-write.db")
	t.Setenv("GO_STOCK_MINUTE_DUAL_WRITE", "1")
	tradeTime := testMinuteTime(10, 2)
	if _, err := upsertMinuteBarsToCache("300004.SZ", []minuteBar{{TradeTime: tradeTime, Open: 7, High: 8, Low: 6, Close: 7.5}}, "dual"); err != nil {
		t.Fatalf("upsert minute bars failed: %v", err)
	}
	var legacyCount int64
	if err := db.Dao.Model(&models.AiRecommendMinuteBar{}).Where("stock_code = ?", "300004.SZ").Count(&legacyCount).Error; err != nil {
		t.Fatalf("count legacy rows failed: %v", err)
	}
	if legacyCount != 1 {
		t.Fatalf("legacy rows = %d, want 1", legacyCount)
	}
}

func TestMinuteCacheDeleteCleansMinuteAndLegacy(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-delete.db")
	t.Setenv("GO_STOCK_MINUTE_DUAL_WRITE", "1")
	tradeTime := testMinuteTime(10, 3)
	if _, err := upsertMinuteBarsToCache("300005.SZ", []minuteBar{{TradeTime: tradeTime, Open: 1, High: 1, Low: 1, Close: 1}}, "dual"); err != nil {
		t.Fatalf("upsert minute bars failed: %v", err)
	}
	if err := deleteMinuteBarsCache("300005.SZ"); err != nil {
		t.Fatalf("delete minute cache failed: %v", err)
	}
	bars, err := listMinuteBarsFromCache("300005.SZ", tradeTime, tradeTime)
	if err != nil {
		t.Fatalf("list after delete failed: %v", err)
	}
	if len(bars) != 0 {
		t.Fatalf("bars after delete = %+v, want empty", bars)
	}
}

func TestMinuteCacheMigrationIsIdempotent(t *testing.T) {
	initMinuteCacheTestDB(t, "minute-cache-migration.db")
	first := normalizeMinuteTime(testMinuteTime(9, 31))
	second := normalizeMinuteTime(testMinuteTime(9, 32))
	legacyRows := []models.AiRecommendMinuteBar{
		{StockCode: "300006.SZ", TradeTime: first, Open: 1, High: 2, Low: 1, Close: 2, Volume: 100, Amount: 200, Source: "legacy"},
		{StockCode: "300006.SZ", TradeTime: second, Open: 2, High: 3, Low: 2, Close: 3, Volume: 200, Amount: 600, Source: "legacy"},
	}
	if err := db.Dao.Create(&legacyRows).Error; err != nil {
		t.Fatalf("insert legacy rows failed: %v", err)
	}

	summary, err := MigrateMinuteCacheToMinuteDB()
	if err != nil {
		t.Fatalf("migrate minute cache failed: %v", err)
	}
	if summary.LegacyRows != 2 || summary.MigratedRows != 2 {
		t.Fatalf("summary = %+v, want legacy=2 migrated=2", summary)
	}
	summary, err = MigrateMinuteCacheToMinuteDB()
	if err != nil {
		t.Fatalf("second migrate minute cache failed: %v", err)
	}
	if summary.MinuteDBRows != 2 {
		t.Fatalf("minute db rows after second migration = %d, want 2", summary.MinuteDBRows)
	}
	bars, err := listMinuteBarsFromCache("300006.SZ", first, second)
	if err != nil {
		t.Fatalf("list migrated bars failed: %v", err)
	}
	if len(bars) != 2 || bars[1].Close != 3 {
		t.Fatalf("migrated bars = %+v, want two rows with close=3", bars)
	}
}
