package data

import (
	"path/filepath"
	"strings"
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

func TestBuildYieldRecordStateFromRecommend_DoesNotActivateBreakoutAboveMaxEntry(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-activation-stop-profit-guard.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                   "002297.SZ",
		StockName:                   "博云新材",
		RecommendCategory:           recommendExecutionConditional,
		SummaryVersion:              marketSummaryPhase3Version,
		DataTime:                    &recordTime,
		RecommendBuyPrice:           "21.30-21.90",
		RecommendBuyPriceMin:        21.3,
		RecommendBuyPriceMax:        21.9,
		RecommendStopProfitPrice:    "23.30-24.20",
		RecommendStopProfitPriceMin: 23.3,
		RecommendStopProfitPriceMax: 24.2,
		RecommendStopLossPrice:      "20.80",
		InvalidCondition:            "价格失效：5分钟收盘价低于20.80",
		ActivationRuleSource:        "market_summary",
		ActivationRuleJSON:          `{"version":"v3","mode":"any_of","paths":[{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":22.65,"thresholdMax":22.99,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2602

	seedMinuteBars(t, "002297.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 4, 30, 10, 9, 0, 0, loc), Open: 23.10, High: 23.40, Low: 23.05, Close: 23.35, Volume: 1000, Amount: 233500},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              time.Date(2026, 4, 30, 10, 10, 0, 0, loc),
		LatestTradeDate:  time.Date(2026, 4, 30, 0, 0, 0, 0, loc),
		InTradingSession: true,
	})
	if state.ActivationStatus != "pending" {
		t.Fatalf("expected pending, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.BuyTime != nil || state.BuyAmount != 0 || state.ActivationPrice != 0 {
		t.Fatalf("expected no buy snapshot, got buyTime=%v buy=%.2f activation=%.2f", state.BuyTime, state.BuyAmount, state.ActivationPrice)
	}
	if state.DataStatus != "待激活" {
		t.Fatalf("expected 待激活 data status, got %s", state.DataStatus)
	}
	if !strings.Contains(state.DataStatusReason, "收盘价 23.35 超过追价上限 22.99") {
		t.Fatalf("unexpected data status reason: %s", state.DataStatusReason)
	}
}

func TestBuildYieldRecordStateFromRecommend_IntradayOpeningPolicyDoesNotBlockSameDayActivation(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-intraday-opening-policy.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 5, 6, 14, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "002085.SZ",
		StockName:                "万丰奥威",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "13.70-13.90",
		RecommendBuyPriceMin:     13.7,
		RecommendBuyPriceMax:     13.9,
		RecommendStopProfitPrice: "14.50-15.00",
		RecommendStopLossPrice:   "13.30",
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","openingPolicy":{"morningBufferUntil":"09:40","maxChasePrice":14.1,"sameDayOnly":true,"gapBelowStopAction":"skip","gapAboveMaxChaseAction":"skip","openInsideBuyRangeAction":"wait_buffer","openBetweenRangeAndBreakoutAction":"wait_buffer","openBetweenBreakoutAndMaxChaseAction":"wait_buffer"},"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":13.7,"thresholdMax":13.9,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2701

	wantActivation := time.Date(2026, 5, 6, 14, 31, 0, 0, loc)
	seedMinuteBars(t, "002085.SZ", []minuteBar{
		{TradeTime: wantActivation, Open: 13.82, High: 13.9, Low: 13.75, Close: 13.84, Volume: 1000, Amount: 13840},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              time.Date(2026, 5, 6, 14, 33, 0, 0, loc),
		LatestTradeDate:  time.Date(2026, 5, 6, 0, 0, 0, 0, loc),
		InTradingSession: true,
	})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.ActivationTime == nil || !state.ActivationTime.Equal(wantActivation) {
		t.Fatalf("expected ActivationTime=%v, got %v", wantActivation, state.ActivationTime)
	}
}

func TestBuildYieldRecordStateFromRecommend_At0940DoesNotWaitForNextOpeningReview(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-0940-opening-policy.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 5, 6, 9, 40, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "002050.SZ",
		StockName:                "三花智控",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "46.80-47.20",
		RecommendBuyPriceMin:     46.8,
		RecommendBuyPriceMax:     47.2,
		RecommendStopProfitPrice: "49.00-50.00",
		RecommendStopLossPrice:   "45.80",
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","openingPolicy":{"morningBufferUntil":"09:40","maxChasePrice":47.9,"sameDayOnly":true,"gapBelowStopAction":"skip","gapAboveMaxChaseAction":"skip","openInsideBuyRangeAction":"wait_buffer","openBetweenRangeAndBreakoutAction":"wait_buffer","openBetweenBreakoutAndMaxChaseAction":"wait_buffer"},"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":46.8,"thresholdMax":47.2,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2702

	wantActivation := time.Date(2026, 5, 6, 9, 41, 0, 0, loc)
	seedMinuteBars(t, "002050.SZ", []minuteBar{
		{TradeTime: wantActivation, Open: 47.0, High: 47.1, Low: 46.9, Close: 47.02, Volume: 1000, Amount: 47020},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              time.Date(2026, 5, 6, 9, 42, 0, 0, loc),
		LatestTradeDate:  time.Date(2026, 5, 6, 0, 0, 0, 0, loc),
		InTradingSession: true,
	})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.ActivationTime == nil || !state.ActivationTime.Equal(wantActivation) {
		t.Fatalf("expected ActivationTime=%v, got %v", wantActivation, state.ActivationTime)
	}
}

func TestBuildYieldRecordStateFromRecommend_At1130ScansSingleMinuteBar(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-1130-single-minute.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 5, 6, 11, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "001696.SZ",
		StockName:                "宗申动力",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "17.30-17.55",
		RecommendBuyPriceMin:     17.3,
		RecommendBuyPriceMax:     17.55,
		RecommendStopProfitPrice: "18.20-18.80",
		RecommendStopLossPrice:   "16.90",
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","openingPolicy":{"morningBufferUntil":"09:40","maxChasePrice":17.8,"sameDayOnly":true,"gapBelowStopAction":"skip","gapAboveMaxChaseAction":"skip","openInsideBuyRangeAction":"wait_buffer","openBetweenRangeAndBreakoutAction":"wait_buffer","openBetweenBreakoutAndMaxChaseAction":"wait_buffer"},"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":17.3,"thresholdMax":17.55,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2703

	wantActivation := time.Date(2026, 5, 6, 11, 30, 0, 0, loc)
	seedMinuteBars(t, "001696.SZ", []minuteBar{
		{TradeTime: wantActivation, Open: 17.53, High: 17.55, Low: 17.53, Close: 17.54, Volume: 1000, Amount: 17540},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              wantActivation,
		LatestTradeDate:  time.Date(2026, 5, 6, 0, 0, 0, 0, loc),
		InTradingSession: true,
	})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.ActivationTime == nil || !state.ActivationTime.Equal(wantActivation) {
		t.Fatalf("expected ActivationTime=%v, got %v", wantActivation, state.ActivationTime)
	}
}

func TestListMinuteBarsFromCacheAllowsSingleMinuteWindow(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "minute-cache-single-window.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	loc := cnLocation()
	barTime := time.Date(2026, 5, 6, 11, 30, 0, 0, loc)
	seedMinuteBars(t, "001696.SZ", []minuteBar{
		{TradeTime: barTime, Open: 17.53, High: 17.55, Low: 17.53, Close: 17.54, Volume: 1000, Amount: 17540},
	})

	bars, err := listMinuteBarsFromCache("001696.SZ", barTime, barTime)
	if err != nil {
		t.Fatalf("list minute bars failed: %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected 1 bar, got %d", len(bars))
	}
	if !bars[0].TradeTime.Equal(barTime) {
		t.Fatalf("expected bar time %v, got %v", barTime, bars[0].TradeTime)
	}
}

func TestResolveActivationRuleScanNormalizesLegacyBreakoutThresholdMax(t *testing.T) {
	loc := cnLocation()
	barTime := time.Date(2026, 5, 6, 14, 31, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "002230.SZ",
		StockName:                "科大讯飞",
		RecommendBuyPrice:        "48.70-49.10",
		RecommendBuyPriceMin:     48.7,
		RecommendBuyPriceMax:     49.1,
		RecommendStopProfitPrice: "50.50-52.00",
		RecommendStopLossPrice:   "47.80",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":49.45,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	scan := resolveActivationRuleScan(rec, []minuteBar{
		{TradeTime: barTime, Open: 49.5, High: 49.8, Low: 49.4, Close: 49.6, Volume: 1000, Amount: 49600},
	})
	if !scan.Triggered {
		t.Fatalf("expected triggered after legacy thresholdMax normalization, got reason=%s", scan.Reason)
	}
	if round2(scan.Price) != 49.6 {
		t.Fatalf("expected activation price 49.60, got %.2f", scan.Price)
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

func TestBuildYieldRecordStateFromRecommend_SameDayOnlyAllowsHistoricalNextDayActivation(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-samedayonly-historical.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 14, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "300274.SZ",
		StockName:                "阳光电源",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		StockPrice:               "100.00",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "100.00-102.00",
		RecommendBuyPriceMin:     100,
		RecommendBuyPriceMax:     102,
		RecommendStopProfitPrice: "110-112",
		RecommendStopLossPrice:   "95",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","openingPolicy":{"morningBufferUntil":"09:40","maxChasePrice":105,"sameDayOnly":true,"gapBelowStopAction":"skip","gapAboveMaxChaseAction":"skip","openInsideBuyRangeAction":"wait_buffer","openBetweenRangeAndBreakoutAction":"wait_buffer","openBetweenBreakoutAndMaxChaseAction":"wait_buffer"},"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":100,"thresholdMax":102,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2601

	wantActivation := time.Date(2026, 4, 30, 10, 0, 0, 0, loc)
	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: wantActivation, Open: 101, High: 102, Low: 100.5, Close: 101.2, Volume: 1000, Amount: 101200},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              time.Date(2026, 5, 6, 16, 0, 0, 0, loc),
		LatestTradeDate:  time.Date(2026, 5, 6, 0, 0, 0, 0, loc),
		InTradingSession: false,
	})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.ActivationTime == nil || !state.ActivationTime.Equal(wantActivation) {
		t.Fatalf("expected ActivationTime=%v, got %v", wantActivation, state.ActivationTime)
	}
}

func TestBuildYieldRecordStateFromRecommend_OpeningGapAboveMaxChaseSkipsOpenOnly(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-opening-high-skip-open-only.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 14, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "300274.SZ",
		StockName:                "阳光电源",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		StockPrice:               "100.00",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "100.00-102.00",
		RecommendBuyPriceMin:     100,
		RecommendBuyPriceMax:     102,
		RecommendStopProfitPrice: "110-112",
		RecommendStopLossPrice:   "95",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","openingPolicy":{"morningBufferUntil":"09:40","maxChasePrice":105,"sameDayOnly":true,"gapBelowStopAction":"skip","gapAboveMaxChaseAction":"skip","openInsideBuyRangeAction":"wait_buffer","openBetweenRangeAndBreakoutAction":"wait_buffer","openBetweenBreakoutAndMaxChaseAction":"wait_buffer"},"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":100,"thresholdMax":102,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2602

	wantActivation := time.Date(2026, 4, 30, 10, 0, 0, 0, loc)
	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 4, 30, 9, 30, 0, 0, loc), Open: 120, High: 121, Low: 119, Close: 120, Volume: 1000, Amount: 120000},
		{TradeTime: wantActivation, Open: 101, High: 102, Low: 100.5, Close: 101.2, Volume: 1000, Amount: 101200},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              time.Date(2026, 5, 6, 16, 0, 0, 0, loc),
		LatestTradeDate:  time.Date(2026, 5, 6, 0, 0, 0, 0, loc),
		InTradingSession: false,
	})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated after opening chase window, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.ActivationTime == nil || !state.ActivationTime.Equal(wantActivation) {
		t.Fatalf("expected ActivationTime=%v, got %v", wantActivation, state.ActivationTime)
	}
}

func TestBuildYieldRecordStateFromRecommend_OpeningGapBelowStopInvalidates(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "yield-opening-low-invalid.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 14, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "300274.SZ",
		StockName:                "阳光电源",
		RecommendCategory:        recommendExecutionConditional,
		SummaryVersion:           marketSummaryPhase3Version,
		StockPrice:               "100.00",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "100.00-102.00",
		RecommendBuyPriceMin:     100,
		RecommendBuyPriceMax:     102,
		RecommendStopProfitPrice: "110-112",
		RecommendStopLossPrice:   "95",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","openingPolicy":{"morningBufferUntil":"09:40","maxChasePrice":105,"sameDayOnly":true,"gapBelowStopAction":"skip","gapAboveMaxChaseAction":"skip","openInsideBuyRangeAction":"wait_buffer","openBetweenRangeAndBreakoutAction":"wait_buffer","openBetweenBreakoutAndMaxChaseAction":"wait_buffer"},"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":100,"thresholdMax":102,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}],"signalType":""}`,
	}
	rec.ID = 2603

	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 4, 30, 9, 30, 0, 0, loc), Open: 94, High: 95, Low: 93.5, Close: 94.2, Volume: 1000, Amount: 94200},
		{TradeTime: time.Date(2026, 4, 30, 10, 0, 0, 0, loc), Open: 101, High: 102, Low: 100.5, Close: 101.2, Volume: 1000, Amount: 101200},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{
		Now:              time.Date(2026, 5, 6, 16, 0, 0, 0, loc),
		LatestTradeDate:  time.Date(2026, 5, 6, 0, 0, 0, 0, loc),
		InTradingSession: false,
	})
	if state.ActivationStatus != "invalid" {
		t.Fatalf("expected invalid, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.ActivationTime != nil {
		t.Fatalf("expected no activation time, got %v", state.ActivationTime)
	}
	if state.DataStatus != "已失效" {
		t.Fatalf("expected 已失效 data status, got %s", state.DataStatus)
	}
}
