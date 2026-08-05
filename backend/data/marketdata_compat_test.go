package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestCompatibilityMarketDataReaderHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (CompatibilityMarketDataReader{}).DailyBars(ctx, marketdata.DailyBarsRequest{
		Symbol: "600000.SH", Start: time.Now(), End: time.Now(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DailyBars error = %v, want context.Canceled", err)
	}
}

func TestCompatibilityMarketDataReaderEnforcesPointInTimeVisibility(t *testing.T) {
	mainDB := compatibilityTestDB(t, "marketdata-main")
	minuteDB := compatibilityTestDB(t, "marketdata-minute")
	if err := mainDB.AutoMigrate(&models.AiRecommendDailyBar{}, &StockInfo{}, &StockBasic{}, &models.SecurityMasterHistory{}); err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.AutoMigrate(&minuteCacheDBBar{}); err != nil {
		t.Fatal(err)
	}
	loc := cnLocation()
	asOf := time.Date(2026, 8, 6, 16, 0, 0, 0, loc)
	tradeDay := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	visibleAt := time.Date(2026, 8, 5, 15, 5, 0, 0, loc)
	futureAt := asOf.Add(time.Hour)

	dailyRows := []models.AiRecommendDailyBar{
		{CreatedAt: visibleAt, UpdatedAt: visibleAt, StockCode: "600000.SH", TradeDate: tradeDay, Open: 10, High: 11, Low: 9, Close: 10.5, Source: "tencent_qfq"},
		{CreatedAt: futureAt, UpdatedAt: futureAt, StockCode: "600000.SH", TradeDate: tradeDay.AddDate(0, 0, -1), Open: 8, High: 9, Low: 7, Close: 8.5, Source: "tencent_qfq"},
	}
	if err := mainDB.Create(&dailyRows).Error; err != nil {
		t.Fatal(err)
	}
	minuteRows := []minuteCacheDBBar{
		{StockCode: "600000.SH", TradeTime: minuteTimeMillis(time.Date(2026, 8, 5, 14, 0, 0, 0, loc)), Open: 10, High: 10.2, Low: 9.9, Close: 10.1, Source: "test", UpdatedAt: time.Date(2026, 8, 5, 14, 2, 0, 0, loc).UnixMilli()},
		{StockCode: "600000.SH", TradeTime: minuteTimeMillis(time.Date(2026, 8, 5, 14, 1, 0, 0, loc)), Open: 10.1, High: 10.3, Low: 10, Close: 10.2, Source: "test", UpdatedAt: futureAt.UnixMilli()},
	}
	if err := minuteDB.Create(&minuteRows).Error; err != nil {
		t.Fatal(err)
	}
	quotes := []StockInfo{
		{Model: gorm.Model{CreatedAt: visibleAt, UpdatedAt: visibleAt}, Code: "sh600000", Name: "visible", Date: "2026-08-05", Time: "15:00:00", Price: "10.50", Open: "10", PreClose: "9.80", High: "11", Low: "9.50", Volume: "100", Amount: "1050"},
		{Model: gorm.Model{CreatedAt: futureAt, UpdatedAt: futureAt}, Code: "sh600000", Name: "future", Date: "2026-08-06", Time: "15:00:00", Price: "99"},
	}
	if err := mainDB.Create(&quotes).Error; err != nil {
		t.Fatal(err)
	}
	frozenAt := visibleAt
	securityRows := []models.SecurityMasterHistory{
		{RecordID: "visible", RunID: "run-visible", SnapshotVersion: "1.5.0", Symbol: "600000.SH", Name: "visible", Status: "L", EffectiveFrom: visibleAt.Add(-time.Hour), Source: "snapshot", SnapshotHash: strings.Repeat("a", 64), PayloadJSON: `{}`, FrozenAt: &frozenAt},
		{RecordID: "future", RunID: "run-future", SnapshotVersion: "1.5.0", Symbol: "600000.SH", Name: "future", Status: "D", EffectiveFrom: futureAt, Source: "snapshot", SnapshotHash: strings.Repeat("b", 64), PayloadJSON: `{}`, FrozenAt: &futureAt},
	}
	if err := mainDB.Create(&securityRows).Error; err != nil {
		t.Fatal(err)
	}

	reader := NewCompatibilityMarketDataReader(mainDB, minuteDB)
	daily, err := reader.DailyBars(context.Background(), marketdata.DailyBarsRequest{
		Symbol: "600000.SH", Start: tradeDay.AddDate(0, 0, -2), End: tradeDay.Add(23*time.Hour + 59*time.Minute), AsOf: asOf,
	})
	if err != nil || len(daily) != 1 || daily[0].Close != 10.5 || daily[0].Adjustment != marketdata.AdjustmentForward {
		t.Fatalf("daily = %+v, err = %v", daily, err)
	}
	minutes, err := reader.MinuteBars(context.Background(), marketdata.MinuteBarsRequest{
		Symbol: "600000.SH", Start: time.Date(2026, 8, 5, 13, 59, 0, 0, loc), End: time.Date(2026, 8, 5, 14, 30, 0, 0, loc), AsOf: asOf,
	})
	if err != nil || len(minutes) != 1 || minutes[0].Close != 10.1 {
		t.Fatalf("minutes = %+v, err = %v", minutes, err)
	}
	quote, err := reader.Quote(context.Background(), "600000.SH", asOf)
	if err != nil || quote.Name != "visible" || quote.Price != 10.5 || quote.AvailableAt.After(asOf) {
		var persisted []StockInfo
		_ = mainDB.Find(&persisted).Error
		t.Fatalf("quote = %+v, err = %v, variants=%v rows=%+v", quote, err, compatibilitySymbolVariants("600000.SH"), persisted)
	}
	security, err := reader.SecurityState(context.Background(), "600000.SH", asOf)
	if err != nil || security.Name != "visible" || security.Status != marketdata.TradingStatusTradable || security.AvailableAt.After(asOf) {
		t.Fatalf("security = %+v, err = %v", security, err)
	}
}

func compatibilityTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(t.Name()+"-"+name) + "?mode=memory&cache=shared"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return database
}
