package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestV150IntradayDailyBarFixtureUsesOnlyCompletedProvenance(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "v150-intraday-daily-provenance.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendDailyBar{}); err != nil {
		t.Fatalf("migrate daily cache: %v", err)
	}

	loc := cnLocation()
	asOf := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	previousDay := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	currentDay := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	rows := []models.AiRecommendDailyBar{
		{
			StockCode: "000001.SZ", TradeDate: previousDay,
			Open: 10, High: 10.3, Low: 9.9, Close: 10.2, Source: "tencent_qfq",
			CreatedAt: previousDay.Add(16 * time.Hour), UpdatedAt: previousDay.Add(16 * time.Hour),
		},
		{
			StockCode: "000001.SZ", TradeDate: currentDay,
			Open: 10.2, High: 10.4, Low: 10.1, Close: 10.3, Source: "tencent_qfq",
			CreatedAt: asOf.Add(-20 * time.Minute), UpdatedAt: asOf.Add(-20 * time.Minute),
		},
	}
	if err := db.Dao.Create(&rows).Error; err != nil {
		t.Fatalf("seed intraday daily fixture: %v", err)
	}
	bars := []dailyBar{
		{TradeDate: previousDay, Open: 10, High: 10.3, Low: 9.9, Close: 10.2},
		{TradeDate: currentDay, Open: 10.2, High: 10.4, Low: 10.1, Close: 10.3},
	}

	unfiltered := loadMarketSummaryV150DailyDataSource("000001.SZ", bars)
	if unfiltered.Complete {
		t.Fatalf("forming current-day bar must not be accepted as complete provenance: %+v", unfiltered)
	}
	completed := loadMarketSummaryV150CompletedDailyDataSource("000001.SZ", bars, asOf)
	if !completed.Complete || completed.LatestTradeDate != previousDay.Format(time.DateOnly) {
		t.Fatalf("completed provenance = %+v, want prior close and complete qfq source", completed)
	}
	if !completed.SourceAt.Equal(previousDay.Add(15*time.Hour)) || completed.AvailableAt.Before(completed.SourceAt) {
		t.Fatalf("completed provenance timing is not causal: %+v", completed)
	}
}

func TestDailyCacheIntradayFixtureDropsFormingBarAndForcesCurrentDayRefresh(t *testing.T) {
	loc := cnLocation()
	previousDay := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	currentDay := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	bars := []dailyBar{
		{TradeDate: previousDay, Close: 10.2},
		{TradeDate: currentDay, Close: 10.3},
	}

	intraday := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	cacheable := cacheableCompletedDailyBars(bars, intraday)
	if len(cacheable) != 1 || !normalizeDailyTradeDate(cacheable[0].TradeDate).Equal(previousDay) {
		t.Fatalf("intraday cacheable bars = %+v, want only prior completed day", cacheable)
	}
	if dailyBarsCoverTradingWindowAt(bars, previousDay, currentDay, intraday) {
		t.Fatal("a cached current-day date must remain refreshable during the session")
	}

	afterClose := time.Date(2026, 8, 5, 15, 5, 0, 0, loc)
	if got := cacheableCompletedDailyBars(bars, afterClose); len(got) != 2 {
		t.Fatalf("post-close cacheable bars = %d, want final current day included", len(got))
	}
	if dailyBarsCoverTradingWindowAt(bars, previousDay, currentDay, afterClose) {
		t.Fatal("a legacy partial current-day row must not suppress the post-close refresh")
	}
	if !dailyBarsCoverTradingWindowAt(bars[:1], previousDay, previousDay, intraday) {
		t.Fatal("a completed historical window should remain a cache hit")
	}
}
