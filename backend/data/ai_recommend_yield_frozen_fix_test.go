package data

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestApplyFrozenSellPriceFixCorrectsGapOpenAndMarksVersion(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "frozen-sell-fix.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	meta := &models.AiRecommendYieldMeta{}
	if err := db.Dao.Create(meta).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}

	sellTime := time.Date(2026, 3, 10, 9, 31, 0, 0, loc)
	buyTime := time.Date(2026, 3, 9, 15, 0, 0, 0, loc)
	stopProfit := 110.0
	oldSell := 110.0
	buyAmount := 100.0
	recommend := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion150,
		StockCode:      "300001.SZ",
		StockName:      "test",
		DataTime:       &buyTime,
	}
	if err := db.Dao.Create(&recommend).Error; err != nil {
		t.Fatalf("create v1.5 recommendation failed: %v", err)
	}

	state := models.AiRecommendYieldState{
		StockCode:          "300001.SZ",
		StockName:          "测试股票",
		BuyTime:            &buyTime,
		BuyAmount:          buyAmount,
		StopProfitAmount:   &stopProfit,
		PositionStatus:     "已止盈",
		SellTime:           &sellTime,
		RealizedSellAmount: &oldSell,
		YieldRate:          10,
		YieldRateText:      "+10.00%",
		Frozen:             true,
	}
	if err := db.Dao.Create(&state).Error; err != nil {
		t.Fatalf("create yield state failed: %v", err)
	}

	record := models.AiRecommendYieldRecordState{
		RecommendID:        recommend.ID,
		StockCode:          "300001.SZ",
		StockName:          "测试股票",
		BuyTime:            &buyTime,
		BuyAmount:          buyAmount,
		StopProfitAmount:   &stopProfit,
		PositionStatus:     "已止盈",
		SellTime:           &sellTime,
		RealizedSellAmount: &oldSell,
		YieldRate:          10,
		YieldRateText:      "+10.00%",
		Frozen:             true,
	}
	if err := db.Dao.Create(&record).Error; err != nil {
		t.Fatalf("create yield record state failed: %v", err)
	}

	bars := []minuteBar{{
		TradeTime: sellTime,
		Open:      115.0,
		High:      116.0,
		Low:       114.0,
		Close:     115.5,
	}}
	if _, err := upsertMinuteBarsToCache("300001.SZ", bars, "test"); err != nil {
		t.Fatalf("seed minute cache failed: %v", err)
	}

	if err := applyFrozenSellPriceFix(meta); err != nil {
		t.Fatalf("apply fix failed: %v", err)
	}

	var gotMeta models.AiRecommendYieldMeta
	if err := db.Dao.First(&gotMeta, meta.ID).Error; err != nil {
		t.Fatalf("reload meta failed: %v", err)
	}
	if gotMeta.FrozenSellPriceFixVersion != frozenSellPriceFixVersion {
		t.Fatalf("expected meta version %s, got %s", frozenSellPriceFixVersion, gotMeta.FrozenSellPriceFixVersion)
	}

	var gotState models.AiRecommendYieldState
	if err := db.Dao.First(&gotState, state.ID).Error; err != nil {
		t.Fatalf("reload yield state failed: %v", err)
	}
	if gotState.RealizedSellAmount == nil || math.Abs(*gotState.RealizedSellAmount-oldSell) > 0.0001 {
		t.Fatalf("expected frozen legacy aggregate sell price %.1f, got %+v", oldSell, gotState.RealizedSellAmount)
	}
	if gotState.YieldRateText != "+10.00%" {
		t.Fatalf("expected frozen legacy aggregate yield +10.00%%, got %s", gotState.YieldRateText)
	}

	var gotRecord models.AiRecommendYieldRecordState
	if err := db.Dao.First(&gotRecord, record.ID).Error; err != nil {
		t.Fatalf("reload yield record state failed: %v", err)
	}
	if gotRecord.RealizedSellAmount == nil || math.Abs(*gotRecord.RealizedSellAmount-115.0) > 0.0001 {
		t.Fatalf("expected corrected record sell price 115.0, got %+v", gotRecord.RealizedSellAmount)
	}
	if gotRecord.YieldRateText != "+14.47%" {
		t.Fatalf("expected corrected record yield +14.47%%, got %s", gotRecord.YieldRateText)
	}
}
