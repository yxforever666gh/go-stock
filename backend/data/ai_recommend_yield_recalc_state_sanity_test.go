package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestSanitizeYieldSellSnapshotClearsInvalidChronology(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	sellFloorTime := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	sellValue := time.Date(2026, 3, 9, 9, 31, 0, 0, loc)
	sellTime := &sellValue
	sellAmountValue := 148.0
	sellAmount := &sellAmountValue
	status := "已止损"
	frozen := true

	changed := sanitizeYieldSellSnapshot(sellFloorTime, &status, &sellTime, &sellAmount, &frozen)
	if !changed {
		t.Fatal("expected invalid sell snapshot to be sanitized")
	}
	if status != "持有" {
		t.Fatalf("expected status 持有, got %s", status)
	}
	if sellTime != nil {
		t.Fatalf("expected sellTime nil, got %v", sellTime)
	}
	if sellAmount != nil {
		t.Fatalf("expected sellAmount nil, got %v", *sellAmount)
	}
	if frozen {
		t.Fatal("expected frozen=false")
	}
}

func TestBuildYieldStateFromAggregateClearsInvalidExistingSell(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	signalTime := time.Date(2026, 3, 9, 9, 58, 50, 0, loc)
	sellTime := time.Date(2026, 3, 9, 9, 31, 0, 0, loc)
	sellAmount := 148.0

	aggr := &aiRecommendYieldAggregate{
		StockCode:      "300274.SZ",
		StockName:      "阳光电源",
		SignalTime:     signalTime,
		BuyTime:        signalTime,
		BuyAmountSum:   153.2,
		BuyAmountCount: 1,
		RecommendCount: 1,
		BkNames:        []string{"光伏设备"},
		ModelNames:     []string{"gpt-5.4"},
	}
	existing := &models.AiRecommendYieldState{
		StockCode:          "300274.SZ",
		PositionStatus:     "已止损",
		SellTime:           &sellTime,
		RealizedSellAmount: &sellAmount,
		Frozen:             true,
	}

	state := buildYieldStateFromAggregate(aggr, existing, yieldBuildContext{Now: signalTime.Add(time.Hour)})
	if state.PositionStatus != "待激活" {
		t.Fatalf("expected 待激活, got %s", state.PositionStatus)
	}
	if state.SellTime != nil {
		t.Fatalf("expected SellTime nil, got %v", state.SellTime)
	}
	if state.RealizedSellAmount != nil {
		t.Fatalf("expected RealizedSellAmount nil, got %v", *state.RealizedSellAmount)
	}
	if state.Frozen {
		t.Fatal("expected Frozen=false")
	}
	if state.ActivationStatus != "pending" {
		t.Fatalf("expected pending activation, got %s", state.ActivationStatus)
	}
}

func TestBuildYieldRecordStateFromRecommendClearsInvalidExistingSell(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-state-sanity.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate stock basic failed: %v", err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 9, 9, 58, 50, 0, loc)
	sellTime := time.Date(2026, 3, 9, 9, 31, 0, 0, loc)
	sellAmount := 148.0

	rec := models.AiRecommendStocks{
		StockCode:                "300274.SZ",
		StockName:                "阳光电源",
		StockPrice:               "153.20",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "153.00-153.50",
		RecommendBuyPriceMin:     153.0,
		RecommendBuyPriceMax:     153.5,
		RecommendStopProfitPrice: "160-165",
		RecommendStopLossPrice:   "150",
	}
	rec.ID = 133
	existing := &models.AiRecommendYieldRecordState{
		RecommendID:        133,
		StockCode:          "300274.SZ",
		PositionStatus:     "已止损",
		SellTime:           &sellTime,
		RealizedSellAmount: &sellAmount,
		Frozen:             true,
	}

	activationTime := time.Date(2026, 3, 9, 10, 0, 0, 0, loc)
	if err := db.Dao.Create(&models.AiRecommendMinuteBar{
		StockCode: "300274.SZ",
		TradeTime: activationTime,
		Open:      153.2,
		High:      153.4,
		Low:       153.0,
		Close:     153.3,
		Volume:    1000,
		Amount:    153300,
		Source:    "test",
	}).Error; err != nil {
		t.Fatalf("seed minute bar failed: %v", err)
	}

	state := buildYieldRecordStateFromRecommend(rec, existing, yieldBuildContext{Now: recordTime.Add(time.Hour)})
	wantBuyTime := activationTime
	if state.BuyTime == nil || !state.BuyTime.Equal(wantBuyTime) {
		t.Fatalf("expected BuyTime=%v, got %v", wantBuyTime, state.BuyTime)
	}
	if state.PositionStatus != "持有" {
		t.Fatalf("expected 持有, got %s", state.PositionStatus)
	}
	if state.SellTime != nil {
		t.Fatalf("expected SellTime nil, got %v", state.SellTime)
	}
	if state.RealizedSellAmount != nil {
		t.Fatalf("expected RealizedSellAmount nil, got %v", *state.RealizedSellAmount)
	}
	if state.Frozen {
		t.Fatal("expected Frozen=false")
	}
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s", state.ActivationStatus)
	}
}

func TestParseRecommendEntryRange_PrefersTextRangeWhenStructuredValueCollapsed(t *testing.T) {
	rec := models.AiRecommendStocks{
		RecommendBuyPrice:    "18.60-18.95分批观察，若回到19.10上方并放量可加关注",
		RecommendBuyPriceMin: 18.6,
		RecommendBuyPriceMax: 18.6,
	}

	minPrice, maxPrice, ok := parseRecommendEntryRange(rec)
	if !ok {
		t.Fatal("expected buy range to be parsed")
	}
	if round2(minPrice) != 18.60 || round2(maxPrice) != 18.95 {
		t.Fatalf("expected text range 18.60-18.95, got %.2f-%.2f", minPrice, maxPrice)
	}
}

func TestBuildYieldRecordStateFromRecommend_UsesTextRangeWhenStructuredValueCollapsed(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-state-text-range.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate stock basic failed: %v", err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 4, 1, 9, 25, 0, 0, loc)
	activationTime := time.Date(2026, 4, 1, 9, 35, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "000636.SZ",
		StockName:                "风华高科",
		StockPrice:               "19.44",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "18.60-18.95分批观察，若回到19.10上方并放量可加关注",
		RecommendBuyPriceMin:     18.6,
		RecommendBuyPriceMax:     18.6,
		RecommendStopProfitPrice: "20.20-20.80",
		RecommendStopLossPrice:   "18.20",
	}
	rec.ID = 239
	if err := db.Dao.Create(&models.AiRecommendMinuteBar{
		StockCode: "000636.SZ",
		TradeTime: activationTime,
		Open:      18.82,
		High:      18.90,
		Low:       18.78,
		Close:     18.88,
		Volume:    1200,
		Amount:    22656,
		Source:    "test",
	}).Error; err != nil {
		t.Fatalf("seed minute bar failed: %v", err)
	}

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: recordTime.Add(time.Hour)})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s", state.ActivationStatus)
	}
	if state.PositionStatus != "持有" {
		t.Fatalf("expected 持有, got %s", state.PositionStatus)
	}
	if state.BuyTime == nil || !state.BuyTime.Equal(activationTime) {
		t.Fatalf("expected BuyTime=%v, got %v", activationTime, state.BuyTime)
	}
	if round2(state.BuyAmount) != 18.88 {
		t.Fatalf("expected BuyAmount=18.88, got %.2f", state.BuyAmount)
	}
	if round2(state.ActivationPrice) != 18.88 {
		t.Fatalf("expected ActivationPrice=18.88, got %.2f", state.ActivationPrice)
	}
}

func TestBuildYieldRecordStateFromRecommend_SkipWhenRecommendStatusAvoid(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 20, 9, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "603019.SH",
		StockName:                "中科曙光",
		ModelName:                "gpt-5.4",
		RecommendCategory:        "observe",
		RecommendStatus:          "avoid",
		InvalidCondition:         "跌破关键支撑",
		RecommendStopProfitPrice: "89.5-92.0",
		RecommendStopLossPrice:   "84.8",
		DataTime:                 &recordTime,
	}
	rec.ID = 221

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: recordTime.Add(2 * time.Hour)})
	if state.ActivationStatus != "skipped" {
		t.Fatalf("expected skipped activation status, got %s", state.ActivationStatus)
	}
	if state.PositionStatus != "已放弃" {
		t.Fatalf("expected 已放弃 position, got %s", state.PositionStatus)
	}
	if state.DataStatus != "已跳过" {
		t.Fatalf("expected 已跳过 data status, got %s", state.DataStatus)
	}
	if state.BuyTime != nil || state.SellTime != nil {
		t.Fatalf("expected nil buy/sell time, got buy=%v sell=%v", state.BuyTime, state.SellTime)
	}
	if state.BuyAmount != 0 || state.ActivationPrice != 0 {
		t.Fatalf("expected zero activation/buy price, got activation=%.2f buy=%.2f", state.ActivationPrice, state.BuyAmount)
	}
}
