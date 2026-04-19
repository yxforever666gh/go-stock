package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestBuildYieldRecordStateFromRecommendKeepsPendingBeforeEntryRangeTriggered(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-activation.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 3, 9, 9, 58, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "300274.SZ",
		StockName:                "阳光电源",
		StockPrice:               "153.20",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "170.00-171.00",
		RecommendBuyPriceMin:     170.0,
		RecommendBuyPriceMax:     171.0,
		RecommendStopProfitPrice: "176-182",
		RecommendStopLossPrice:   "150",
	}
	rec.ID = 2001

	barTime := time.Date(2026, 3, 9, 10, 0, 0, 0, loc)
	if err := db.Dao.Create(&models.AiRecommendMinuteBar{
		StockCode: "300274.SZ",
		TradeTime: barTime,
		Open:      153.1,
		High:      153.4,
		Low:       152.9,
		Close:     153.2,
		Volume:    1000,
		Amount:    153200,
		Source:    "test",
	}).Error; err != nil {
		t.Fatalf("seed minute bar failed: %v", err)
	}

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: recordTime.Add(2 * time.Hour)})
	if state.ActivationStatus != "pending" {
		t.Fatalf("expected pending activation, got %s", state.ActivationStatus)
	}
	if state.BuyTime != nil {
		t.Fatalf("expected nil BuyTime before activation, got %v", state.BuyTime)
	}
	if state.BuyAmount != 0 {
		t.Fatalf("expected buy amount 0 before activation, got %.2f", state.BuyAmount)
	}
	if state.YieldRateText != "--" {
		t.Fatalf("expected no yield before activation, got %s", state.YieldRateText)
	}
}

func TestBuildYieldRecordStateFromRecommend_BeforeCutoffUsesLegacyDirectActivation(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-legacy-direct-activation.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 3, 9, 58, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "300274.SZ",
		StockName:                "阳光电源",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		StockPrice:               "153.20",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "153.00-154.00",
		RecommendBuyPriceMin:     153.0,
		RecommendBuyPriceMax:     154.0,
		RecommendStopProfitPrice: "160-162",
		RecommendStopLossPrice:   "150",
	}
	rec.ID = 2002

	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 4, 3, 10, 0, 0, 0, loc), Open: 153.1, High: 153.6, Low: 152.9, Close: 153.2, Volume: 1000, Amount: 153200},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: time.Date(2026, 4, 3, 10, 5, 0, 0, loc)})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.BuyTime == nil || !state.BuyTime.Equal(time.Date(2026, 4, 3, 10, 0, 0, 0, loc)) {
		t.Fatalf("unexpected BuyTime: %v", state.BuyTime)
	}
	if round2(state.BuyAmount) != 153.2 {
		t.Fatalf("expected BuyAmount=153.2, got %.2f", state.BuyAmount)
	}
	if state.DataStatus != "正常" {
		t.Fatalf("expected 正常 data status, got %s", state.DataStatus)
	}
}

func TestBuildYieldRecordStateFromRecommend_BeforeCutoffClampsActivationTimeToSignalTime(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-legacy-direct-activation-clamp.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 3, 9, 30, 1, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "000977.SZ",
		StockName:                "浪潮信息",
		RecommendCategory:        "observe",
		StockPrice:               "59.60",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "59.20-60.20",
		RecommendStopProfitPrice: "63-65",
		RecommendStopLossPrice:   "57.2",
	}
	rec.ID = 2003

	seedMinuteBars(t, "000977.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 4, 3, 9, 30, 0, 0, loc), Open: 59.5, High: 59.8, Low: 59.2, Close: 59.6, Volume: 1000, Amount: 59600},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: time.Date(2026, 4, 3, 9, 35, 0, 0, loc)})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.ActivationTime == nil || !state.ActivationTime.Equal(recordTime) {
		t.Fatalf("expected ActivationTime=%v, got %v", recordTime, state.ActivationTime)
	}
	if state.BuyTime == nil || !state.BuyTime.Equal(recordTime) {
		t.Fatalf("expected BuyTime=%v, got %v", recordTime, state.BuyTime)
	}
}

func TestBuildYieldRecordStateFromRecommend_OvernightRecalcKeepsPriorTradeDayActivation(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-overnight-keep-prior-activation.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 9, 9, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		StockPrice:               "680.00",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "650.00-694.00",
		RecommendBuyPriceMin:     650.0,
		RecommendBuyPriceMax:     694.0,
		RecommendStopProfitPrice: "720-760",
		RecommendStopLossPrice:   "630",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","openingPolicy":{"morningBufferUntil":"09:40","maxChasePrice":704.41,"sameDayOnly":true,"gapBelowStopAction":"skip","gapAboveMaxChaseAction":"skip","openInsideBuyRangeAction":"wait_buffer","openBetweenRangeAndBreakoutAction":"wait_buffer","openBetweenBreakoutAndMaxChaseAction":"wait_buffer"},"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":650,"thresholdMax":694,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2501

	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 4, 9, 10, 39, 0, 0, loc), Open: 681.0, High: 686.0, Low: 679.5, Close: 683.2, Volume: 1000, Amount: 683200},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              time.Date(2026, 4, 10, 3, 20, 0, 0, loc),
		LatestTradeDate:  time.Date(2026, 4, 10, 0, 0, 0, 0, loc),
		InTradingSession: false,
	})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	wantActivation := time.Date(2026, 4, 9, 10, 39, 0, 0, loc)
	if state.ActivationTime == nil || !state.ActivationTime.Equal(wantActivation) {
		t.Fatalf("expected ActivationTime=%v, got %v", wantActivation, state.ActivationTime)
	}
	if state.BuyTime == nil || !state.BuyTime.Equal(wantActivation) {
		t.Fatalf("expected BuyTime=%v, got %v", wantActivation, state.BuyTime)
	}
	if round2(state.ActivationPrice) != 683.2 {
		t.Fatalf("expected ActivationPrice=683.2, got %.2f", state.ActivationPrice)
	}
}
