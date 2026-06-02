package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestLoadScopeCodesForManualDownload_MergesDirtyAndCoverageScopes(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-scope-merge.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AiRecommendYieldOverride{},
		&Settings{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	oldNow := timeNow
	t.Cleanup(func() { timeNow = oldNow })
	loc := cnLocation()
	now := time.Date(2026, 5, 29, 15, 30, 0, 0, loc)
	timeNow = func() time.Time { return now }
	if err := db.Dao.Create(&models.AiRecommendYieldMeta{CurrentTradeDate: "2026-05-29"}).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}

	dirtyRecordTime := time.Date(2026, 5, 29, 9, 30, 0, 0, loc)
	coverageRecordTime := time.Date(2026, 5, 28, 14, 30, 0, 0, loc)
	rows := []models.AiRecommendStocks{
		{
			DataTime:                    &dirtyRecordTime,
			StockCode:                   "300001.SZ",
			StockName:                   "特锐德",
			RecommendBuyPrice:           "10-10.5",
			RecommendStopProfitPrice:    "11-12",
			RecommendStopLossPrice:      "9.6",
			RecommendStatus:             "valid",
			RecommendCategory:           recommendExecutionImmediate,
			ExecutionState:              recommendExecutionImmediate,
			ActivationStatus:            "pending",
			RecommendBuyPriceMin:        10,
			RecommendBuyPriceMax:        10.5,
			RecommendStopProfitPriceMin: 11,
			RecommendStopProfitPriceMax: 12,
		},
		{
			DataTime:                    &coverageRecordTime,
			StockCode:                   "301293.SZ",
			StockName:                   "三博脑科",
			RecommendBuyPrice:           "20-20.5",
			RecommendStopProfitPrice:    "22-23",
			RecommendStopLossPrice:      "19.2",
			RecommendStatus:             "valid",
			RecommendCategory:           recommendExecutionImmediate,
			ExecutionState:              recommendExecutionImmediate,
			ActivationStatus:            "pending",
			RecommendBuyPriceMin:        20,
			RecommendBuyPriceMax:        20.5,
			RecommendStopProfitPriceMin: 22,
			RecommendStopProfitPriceMax: 23,
		},
	}
	for _, row := range rows {
		if err := db.Dao.Create(&row).Error; err != nil {
			t.Fatalf("create recommend failed: %v", err)
		}
	}
	if err := db.Dao.Create(&models.AiRecommendYieldDirtyCode{
		StockCode:  "300001.SZ",
		Reason:     "等待 strict 重算",
		ModeNeeded: aiRecommendYieldModeStrict,
	}).Error; err != nil {
		t.Fatalf("create dirty code failed: %v", err)
	}
	for _, tradeTime := range []time.Time{
		time.Date(2026, 5, 29, 11, 0, 0, 0, loc),
		time.Date(2026, 5, 29, 15, 0, 0, 0, loc),
	} {
		if err := db.Dao.Create(&models.AiRecommendMinuteBar{
			StockCode: "301293.SZ",
			TradeTime: tradeTime,
			Open:      20,
			High:      20,
			Low:       20,
			Close:     20,
			Source:    "test",
		}).Error; err != nil {
			t.Fatalf("create minute bar failed: %v", err)
		}
	}

	got, err := loadScopeCodesForManualDownload()
	if err != nil {
		t.Fatalf("loadScopeCodesForManualDownload failed: %v", err)
	}
	gotSet := normalizeScopeCodes(got)
	for _, code := range []string{"300001.SZ", "301293.SZ"} {
		if _, ok := gotSet[code]; !ok {
			t.Fatalf("expected scope to include %s, got %#v", code, got)
		}
	}
}
