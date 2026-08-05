package data

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestBuildYieldRecordStateFromRecommend_ActivatesWhenPrevDayActivityPasses(t *testing.T) {
	withStubbedMinuteProviders(t)
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "yield-prev-activity-pass.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	rec := buildPrevActivityRecommend(recordTime)
	rec.ID = 3101

	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 3, 9, 9, 31, 0, 0, loc), Open: 9.96, High: 10.00, Low: 9.95, Close: 9.99, Volume: 100, Amount: 1000},
		{TradeTime: time.Date(2026, 3, 9, 9, 32, 0, 0, loc), Open: 10.00, High: 10.04, Low: 9.99, Close: 10.02, Volume: 120, Amount: 1100},
		{TradeTime: time.Date(2026, 3, 10, 9, 31, 0, 0, loc), Open: 9.97, High: 10.01, Low: 9.96, Close: 9.99, Volume: 150, Amount: 1600},
		{TradeTime: time.Date(2026, 3, 10, 9, 32, 0, 0, loc), Open: 10.01, High: 10.08, Low: 9.99, Close: 10.05, Volume: 220, Amount: 2600},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: time.Date(2026, 3, 10, 9, 32, 0, 0, loc)})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s (%s)", state.ActivationStatus, state.DataStatusReason)
	}
	if state.BuyTime == nil || !state.BuyTime.Equal(time.Date(2026, 3, 10, 9, 32, 0, 0, loc)) {
		t.Fatalf("unexpected BuyTime: %v", state.BuyTime)
	}
	if round2(state.BuyAmount) != 10.05 {
		t.Fatalf("expected BuyAmount=10.05, got %.2f", state.BuyAmount)
	}
}

func TestBuildYieldRecordStateFromRecommend_RemainsPendingWhenPrevDayActivityWeaker(t *testing.T) {
	withStubbedMinuteProviders(t)
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "yield-prev-activity-block.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	rec := buildPrevActivityRecommend(recordTime)
	rec.ID = 3102

	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 3, 9, 9, 31, 0, 0, loc), Open: 9.96, High: 10.00, Low: 9.95, Close: 9.99, Volume: 300, Amount: 3000},
		{TradeTime: time.Date(2026, 3, 9, 9, 32, 0, 0, loc), Open: 10.00, High: 10.04, Low: 9.99, Close: 10.02, Volume: 320, Amount: 3200},
		{TradeTime: time.Date(2026, 3, 10, 9, 31, 0, 0, loc), Open: 9.97, High: 10.01, Low: 9.96, Close: 9.99, Volume: 110, Amount: 1200},
		{TradeTime: time.Date(2026, 3, 10, 9, 32, 0, 0, loc), Open: 10.01, High: 10.08, Low: 9.99, Close: 10.05, Volume: 120, Amount: 1300},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: time.Date(2026, 3, 10, 9, 32, 0, 0, loc)})
	if state.ActivationStatus != "pending" {
		t.Fatalf("expected pending, got %s", state.ActivationStatus)
	}
	if state.BuyTime != nil {
		t.Fatalf("expected nil BuyTime, got %v", state.BuyTime)
	}
	if state.DataStatus != "待激活" {
		t.Fatalf("expected 待激活 data status, got %s", state.DataStatus)
	}
	if !strings.Contains(state.DataStatusReason, "5分钟成交额") {
		t.Fatalf("expected amount compare reason, got %s", state.DataStatusReason)
	}
}

func TestBuildYieldRecordStateFromRecommend_BlocksWhenPrevDayActivityMissing(t *testing.T) {
	withStubbedMinuteProviders(t)
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "yield-prev-activity-missing.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	rec := buildPrevActivityRecommend(recordTime)
	rec.ID = 3103

	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 3, 10, 9, 31, 0, 0, loc), Open: 9.97, High: 10.01, Low: 9.96, Close: 9.99, Volume: 150, Amount: 1600},
		{TradeTime: time.Date(2026, 3, 10, 9, 32, 0, 0, loc), Open: 10.01, High: 10.08, Low: 9.99, Close: 10.05, Volume: 220, Amount: 2600},
	})

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: time.Date(2026, 3, 10, 9, 32, 0, 0, loc)})
	if state.ActivationStatus != "pending" {
		t.Fatalf("expected pending, got %s", state.ActivationStatus)
	}
	if !strings.Contains(state.DataStatusReason, "缺少上一交易日活跃度基准") {
		t.Fatalf("expected missing baseline reason, got %s", state.DataStatusReason)
	}
}

func TestBuildYieldStateFromAggregate_BlocksWhenPrevDayActivityMissing(t *testing.T) {
	withStubbedMinuteProviders(t)
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "yield-prev-activity-aggregate.db"))
	if err := db.Dao.AutoMigrate(&Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	signalTime := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	stopProfit := 10.80
	stopLoss := 9.70
	aggr := &aiRecommendYieldAggregate{
		StockCode:                    "300274.SZ",
		StockName:                    "阳光电源",
		SignalTime:                   signalTime,
		BuyTime:                      signalTime,
		BuyAmountSum:                 10.05,
		BuyAmountCount:               1,
		StopProfitSum:                stopProfit,
		StopProfitCount:              1,
		StopLossSum:                  stopLoss,
		StopLossCount:                1,
		RecommendCount:               1,
		RequirePrevDayActivityFilter: true,
		BkNames:                      []string{"光伏设备"},
		ModelNames:                   []string{"gpt-5.4"},
	}

	seedMinuteBars(t, "300274.SZ", []minuteBar{
		{TradeTime: time.Date(2026, 3, 10, 9, 31, 0, 0, loc), Open: 9.97, High: 10.01, Low: 9.96, Close: 9.99, Volume: 150, Amount: 1600},
		{TradeTime: time.Date(2026, 3, 10, 9, 32, 0, 0, loc), Open: 10.01, High: 10.08, Low: 9.99, Close: 10.05, Volume: 220, Amount: 2600},
	})

	state := buildYieldStateFromAggregate(aggr, nil, yieldBuildContext{Now: time.Date(2026, 3, 10, 9, 32, 0, 0, loc)})
	if state.ActivationStatus != "pending" {
		t.Fatalf("expected pending, got %s", state.ActivationStatus)
	}
	if !strings.Contains(state.DataStatusReason, "缺少上一交易日活跃度基准") {
		t.Fatalf("expected missing baseline reason, got %s", state.DataStatusReason)
	}
}

func buildPrevActivityRecommend(recordTime time.Time) models.AiRecommendStocks {
	return models.AiRecommendStocks{
		StockCode:                "300274.SZ",
		StockName:                "阳光电源",
		StockPrice:               "10.00",
		StockCurrentPrice:        "10.05",
		StockCurrentPriceTime:    recordTime.Format("2006-01-02 15:04:05"),
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "10.00-10.08",
		RecommendBuyPriceMin:     10.00,
		RecommendBuyPriceMax:     10.08,
		RecommendStopProfitPrice: "10.80-11.20",
		RecommendStopLossPrice:   "9.70",
		ExecutionState:           "conditional",
		BuySignal:                "价格位置：回到10.00-10.08主买入区；量能确认：5分钟成交额不能低于上一交易日同一时刻活跃度",
		ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"prev_day_same_slot_amount","operator":">=","thresholdValue":10,"thresholdMax":10.08,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
	}
}

func seedMinuteBars(t *testing.T, stockCode string, bars []minuteBar) {
	t.Helper()
	rows := make([]models.AiRecommendMinuteBar, 0, len(bars))
	for _, bar := range bars {
		rows = append(rows, models.AiRecommendMinuteBar{
			StockCode: stockCode,
			TradeTime: bar.TradeTime,
			Open:      bar.Open,
			High:      bar.High,
			Low:       bar.Low,
			Close:     bar.Close,
			Volume:    bar.Volume,
			Amount:    bar.Amount,
			Source:    "test",
		})
	}
	if err := db.Dao.Create(&rows).Error; err != nil {
		t.Fatalf("seed minute bars failed: %v", err)
	}
}

func withStubbedMinuteProviders(t *testing.T) {
	t.Helper()
	oldTencent := fetchMinuteBarsWithTencentFn
	oldAkshare := fetchMinuteBarsWithAkShareFn
	oldSina := fetchMinuteBarsWithSinaFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	fetchMinuteBarsWithTencentFn = func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		return nil, "test", fmt.Errorf("test provider disabled")
	}
	fetchMinuteBarsWithAkShareFn = func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		return nil, "test", fmt.Errorf("test provider disabled")
	}
	fetchMinuteBarsWithSinaFn = func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		return nil, "test", fmt.Errorf("test provider disabled")
	}
	fetchMinuteBarsWithDiemengFn = func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		return nil, "test", fmt.Errorf("test provider disabled")
	}
	t.Cleanup(func() {
		fetchMinuteBarsWithTencentFn = oldTencent
		fetchMinuteBarsWithAkShareFn = oldAkshare
		fetchMinuteBarsWithSinaFn = oldSina
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	})
}
