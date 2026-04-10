package data

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestBuildYieldRecordStateFromRecommend_RecomputesFrozenSellWithOpenGap(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	db.Init(filepath.Join(t.TempDir(), "yield-frozen-recompute.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendYieldRecordState{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	recordTime := time.Date(2026, 3, 9, 14, 59, 0, 0, loc)
	sellTime := time.Date(2026, 3, 10, 9, 31, 0, 0, loc)
	stopProfit := 110.0
	stopLoss := 96.0
	oldSell := 110.0

	rec := models.AiRecommendStocks{
		StockCode:                   "300001.SZ",
		StockName:                   "测试股票",
		StockPrice:                  "100.00",
		RecommendBuyPrice:           "100.00-100.00",
		RecommendBuyPriceMin:        100.0,
		RecommendBuyPriceMax:        100.0,
		RecommendStopProfitPrice:    "110.00",
		RecommendStopProfitPriceMin: 110.0,
		RecommendStopProfitPriceMax: 110.0,
		RecommendStopLossPrice:      "96.00",
		DataTime:                    &recordTime,
		ActivationRuleJSON:          `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":100,"thresholdMax":100,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
	}
	rec.ID = 99
	existing := &models.AiRecommendYieldRecordState{
		RecommendID:        99,
		StockCode:          "300001.SZ",
		BuyTime:            &recordTime,
		BuyAmount:          100.0,
		StopProfitAmount:   &stopProfit,
		StopLossAmount:     &stopLoss,
		PositionStatus:     "已止盈",
		SellTime:           &sellTime,
		RealizedSellAmount: &oldSell,
		Frozen:             true,
	}
	activationBar := minuteBar{
		TradeTime: time.Date(2026, 3, 9, 14, 59, 0, 0, loc),
		Open:      100.0,
		High:      100.1,
		Low:       99.9,
		Close:     100.0,
		Volume:    1000,
		Amount:    100000,
	}
	sellBar := minuteBar{
		TradeTime: sellTime,
		Open:      115.0,
		High:      116.0,
		Low:       114.0,
		Close:     115.5,
		Volume:    1200,
		Amount:    138600,
	}
	if _, err := upsertMinuteBarsToCache("300001.SZ", []minuteBar{activationBar, sellBar}, "test"); err != nil {
		t.Fatalf("seed minute cache failed: %v", err)
	}

	state := buildYieldRecordStateFromRecommend(rec, existing, yieldBuildContext{
		Now:              sellTime,
		InTradingSession: true,
		LatestTradeDate:  time.Date(2026, 3, 10, 0, 0, 0, 0, loc),
	})
	if state.PositionStatus != "已止盈" {
		t.Fatalf("expected 已止盈, got %s", state.PositionStatus)
	}
	if state.SellTime == nil || !state.SellTime.Equal(sellTime) {
		t.Fatalf("unexpected sell time: %+v", state.SellTime)
	}
	if state.RealizedSellAmount == nil || math.Abs(*state.RealizedSellAmount-115.0) > 0.0001 {
		t.Fatalf("expected sell amount 115.0, got %+v", state.RealizedSellAmount)
	}
	if state.YieldRateText != "+14.47%" {
		t.Fatalf("expected +14.47%%, got %s", state.YieldRateText)
	}
	if !state.Frozen {
		t.Fatal("expected frozen=true")
	}
}
