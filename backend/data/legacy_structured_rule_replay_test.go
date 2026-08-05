package data

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupLegacyStructuredRuleReplayTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:legacy-rule-replay-%d?mode=memory&cache=shared", time.Now().UnixNano())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendDailyBar{},
		&models.AiRecommendYieldRecordState{},
	); err != nil {
		t.Fatal(err)
	}
	oldDao, oldMinuteDao := db.Dao, db.MinuteDao
	db.Dao, db.MinuteDao = database, nil
	t.Cleanup(func() {
		db.Dao, db.MinuteDao = oldDao, oldMinuteDao
		if sqlDB, closeErr := database.DB(); closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return database
}

func TestReplayLegacyStructuredRulesCacheOnlyUsesExecutionLifecycleAndIsDeterministic(t *testing.T) {
	database := setupLegacyStructuredRuleReplayTestDB(t)
	loc := cnLocation()
	recommendAt := time.Date(2026, 8, 3, 9, 30, 0, 0, loc)
	nextDay := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	ruleJSON := fmt.Sprintf(`{"version":"v3","signalType":"price_range_with_volume","evaluationWindow":"1m","baseline":"manual_amount","operator":">=","thresholdValue":10,"thresholdMax":10.2,"volumeRatio":1,"confirmBars":1,"volumeWindow":1,"volumeMetric":"amount","expireTradeDays":3,"generatedAt":%q,"validFrom":%q,"dataCutoffTime":%q}`,
		recommendAt.Format(time.RFC3339Nano), recommendAt.Format(time.RFC3339Nano), recommendAt.Format(time.RFC3339Nano))
	rec := models.AiRecommendStocks{
		DataTime:                    &recommendAt,
		StockCode:                   "000001.SZ",
		StockName:                   "fixture",
		RecommendCategory:           recommendExecutionConditional,
		ExecutionState:              "waiting_activation",
		RecommendBuyPrice:           "10.00-10.20",
		RecommendBuyPriceMin:        10,
		RecommendBuyPriceMax:        10.2,
		RecommendStopProfitPrice:    "10.50",
		RecommendStopProfitPriceMin: 10.5,
		RecommendStopProfitPriceMax: 10.5,
		RecommendStopLossPrice:      "9.50",
		SummaryVersion:              "phase3-v3",
		ActivationRuleJSON:          ruleJSON,
		ActivationRuleVersion:       activationRuleVersionV3,
		ActivationRuleSource:        "market_summary",
	}
	if err := database.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	bars := []models.AiRecommendMinuteBar{
		{StockCode: rec.StockCode, TradeTime: recommendAt.Add(time.Minute), Open: 10, High: 10.1, Low: 9.99, Close: 10.05, Volume: 1000, Amount: 10000, Source: "fixture"},
		{StockCode: rec.StockCode, TradeTime: time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 31, 0, 0, loc), Open: 10.6, High: 10.7, Low: 10.55, Close: 10.65, Volume: 1000, Amount: 10600, Source: "fixture"},
	}
	if err := database.Create(&bars).Error; err != nil {
		t.Fatal(err)
	}

	oldSuspensionFetch := fetchDiemengSuspensionsFn
	suspensionFetches := 0
	fetchDiemengSuspensionsFn = func(string, time.Time) ([]diemengSuspensionItem, error) {
		suspensionFetches++
		return nil, fmt.Errorf("network lookup must not run")
	}
	t.Cleanup(func() { fetchDiemengSuspensionsFn = oldSuspensionFetch })

	report, err := ReplayLegacyStructuredRulesCacheOnly(context.Background(), database, LegacyStructuredRuleReplayOptions{ExpectedRuleCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if suspensionFetches != 0 {
		t.Fatalf("cache-only replay attempted %d suspension fetches", suspensionFetches)
	}
	if !report.CacheOnly || report.ProfitabilityProof || !report.Deterministic || report.ResultHash == "" || report.ResultHash != report.RepeatedResultHash {
		t.Fatalf("unexpected replay identity: %+v", report)
	}
	if report.TotalRules != 1 || report.ParsedRules != 1 || report.InvalidRules != 0 || report.CacheAvailableRules != 1 || report.ActivatedRules != 1 || report.ClosedRules != 1 {
		t.Fatalf("unexpected replay counts: %+v", report)
	}
	if report.CausalityViolations != 0 || report.TPlusOneViolations != 0 || len(report.Results) != 1 {
		t.Fatalf("unexpected invariant result: %+v", report)
	}
	result := report.Results[0]
	if result.Outcome != "closed" || result.ActivationAt == nil || result.ExitAt == nil || !result.ExitAt.After(*result.ActivationAt) {
		t.Fatalf("execution lifecycle was not replayed: %+v", result)
	}
}

func TestReplayLegacyStructuredRulesCacheOnlyRejectsCountDrift(t *testing.T) {
	database := setupLegacyStructuredRuleReplayTestDB(t)
	recommendAt := time.Date(2026, 8, 3, 9, 30, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		DataTime:              &recommendAt,
		StockCode:             "000001.SZ",
		SummaryVersion:        "phase3-v3",
		ActivationRuleJSON:    `{"signalType":"price_range_with_volume","baseline":"manual_amount","thresholdValue":10,"volumeRatio":1}`,
		ActivationRuleVersion: activationRuleVersionV1,
	}
	if err := database.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	_, err := ReplayLegacyStructuredRulesCacheOnly(context.Background(), database, LegacyStructuredRuleReplayOptions{ExpectedRuleCount: 226})
	if err == nil {
		t.Fatal("expected corpus-count drift to fail")
	}
}
