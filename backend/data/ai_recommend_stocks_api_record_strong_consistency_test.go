package data

import (
	"testing"
	"time"

	"go-stock/backend/models"
)

func TestMapRecommendRecordToYieldItemWithRecordState_PreferRecordState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 2, 27, 8, 33, 57, 0, loc)
	sellTime := time.Date(2026, 2, 28, 10, 5, 0, 0, loc)
	sellAmount := 34.00

	rec := models.AiRecommendStocks{
		StockCode:  "300398.SZ",
		StockName:  "飞凯材料",
		ModelName:  "deepseek",
		StockPrice: "33.42",
		DataTime:   &recordTime,
	}
	rec.ID = 1001

	recordStateMap := map[uint]models.AiRecommendYieldRecordState{
		rec.ID: {
			RecommendID:        rec.ID,
			StockCode:          "300398.SZ",
			StockName:          "飞凯材料",
			ModelName:          "deepseek",
			PositionStatus:     "已止盈",
			SellTime:           &sellTime,
			RealizedSellAmount: &sellAmount,
			CurrentPrice:       28.83,
			DataStatus:         "正常",
		},
	}
	legacySellTime := time.Date(2026, 2, 26, 14, 4, 0, 0, loc)
	legacySellAmount := 31.11
	legacyMap := map[string]models.AiRecommendYieldState{
		normalizeRecommendStockCode("300398.SZ"): {
			StockCode:          "300398.SZ",
			PositionStatus:     "已止损",
			SellTime:           &legacySellTime,
			RealizedSellAmount: &legacySellAmount,
		},
	}

	item := mapRecommendRecordToYieldItemWithRecordState(rec, recordStateMap, legacyMap)
	if item.RecommendTime != "2026-02-27 08:33:57" {
		t.Fatalf("expected RecommendTime=2026-02-27 08:33:57, got %s", item.RecommendTime)
	}
	if item.BuyTime != "" {
		t.Fatalf("expected empty BuyTime before activation, got %s", item.BuyTime)
	}
	if item.SellTime != "未纳入回测" {
		t.Fatalf("expected SellTime=未纳入回测 before activation, got %s", item.SellTime)
	}
	if item.SellAmount != nil {
		t.Fatalf("expected SellAmount nil before activation, got %.4f", *item.SellAmount)
	}
	if item.PositionStatus != "未纳入回测" {
		t.Fatalf("expected PositionStatus=未纳入回测, got %s", item.PositionStatus)
	}
	if item.ActivationStatus != "ineligible" {
		t.Fatalf("expected ActivationStatus=ineligible for non-structured plan, got %s", item.ActivationStatus)
	}
	if item.YieldRateText != "--" {
		t.Fatalf("expected YieldRateText=-- without activation, got %s", item.YieldRateText)
	}
	if item.BacktestEligibility != recommendBacktestIneligible {
		t.Fatalf("expected BacktestEligibility=ineligible, got %s", item.BacktestEligibility)
	}
}

func TestMapRecommendRecordToYieldItemWithRecordState_FallbackLegacyState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 2, 27, 8, 33, 57, 0, loc)
	legacySellTime := time.Date(2026, 2, 28, 9, 40, 0, 0, loc)
	legacySellAmount := 33.00

	rec := models.AiRecommendStocks{
		StockCode:  "300398.SZ",
		StockName:  "飞凯材料",
		StockPrice: "33.42",
		DataTime:   &recordTime,
	}
	rec.ID = 1002

	legacyMap := map[string]models.AiRecommendYieldState{
		normalizeRecommendStockCode("300398.SZ"): {
			StockCode:          "300398.SZ",
			PositionStatus:     "已止损",
			SellTime:           &legacySellTime,
			RealizedSellAmount: &legacySellAmount,
			CurrentPrice:       28.83,
			DataStatus:         "正常",
		},
	}

	item := mapRecommendRecordToYieldItemWithRecordState(rec, map[uint]models.AiRecommendYieldRecordState{}, legacyMap)
	if item.RecommendTime != "2026-02-27 08:33:57" {
		t.Fatalf("expected RecommendTime=2026-02-27 08:33:57, got %s", item.RecommendTime)
	}
	if item.BuyTime != "" {
		t.Fatalf("expected empty BuyTime before activation, got %s", item.BuyTime)
	}
	if item.SellTime != "未纳入回测" {
		t.Fatalf("expected SellTime=未纳入回测 before activation, got %s", item.SellTime)
	}
	if item.SellAmount != nil {
		t.Fatalf("expected SellAmount nil before activation, got %.4f", *item.SellAmount)
	}
	if item.ActivationStatus != "ineligible" {
		t.Fatalf("expected ActivationStatus=ineligible, got %s", item.ActivationStatus)
	}
	if item.PositionStatus != "未纳入回测" {
		t.Fatalf("expected PositionStatus=未纳入回测, got %s", item.PositionStatus)
	}
	if item.BacktestEligibility != recommendBacktestIneligible {
		t.Fatalf("expected BacktestEligibility=ineligible, got %s", item.BacktestEligibility)
	}
}

func TestShouldTrackRecommendInYield_UsesLegacyCompatibilityForHistoricalCategoryVariants(t *testing.T) {
	trackable := []models.AiRecommendStocks{
		{
			RecommendStatus:          "valid",
			RecommendCategory:        "右侧确认候选 / 低吸候选",
			RecommendBuyPrice:        "10-10.5",
			RecommendStopProfitPrice: "11-12",
			RecommendStopLossPrice:   "9.6",
		},
		{
			RecommendBuyPrice:        "10-10.5",
			RecommendStopProfitPrice: "11-12",
			RecommendStopLossPrice:   "9.6",
		},
	}
	for _, item := range trackable {
		if !shouldTrackRecommendInYield(&item) {
			t.Fatalf("expected item with category %q to be tracked", item.RecommendCategory)
		}
	}

	notTrackable := []models.AiRecommendStocks{
		{
			RecommendStatus:          "valid",
			RecommendCategory:        "右侧确认候选 / 低吸候选",
			SummaryVersion:           marketSummaryPhase3Version,
			RecommendBuyPrice:        "10-10.5",
			RecommendStopProfitPrice: "11-12",
			RecommendStopLossPrice:   "9.6",
		},
		{
			RecommendStatus:          "valid",
			RecommendCategory:        "观察标的（等待止跌/确认）",
			SummaryVersion:           marketSummaryPhase3Version,
			RecommendBuyPrice:        "20-21",
			RecommendStopProfitPrice: "23-24",
			RecommendStopLossPrice:   "18.8",
		},
		{RecommendStatus: "valid", RecommendCategory: "回避标的（高位分歧）"},
	}
	for _, item := range notTrackable {
		if shouldTrackRecommendInYield(&item) {
			t.Fatalf("expected category %q without executable plan to be filtered", item.RecommendCategory)
		}
	}
}

func TestMapRecommendRecordToYieldItemWithRecordState_NormalizesCategoryLabel(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 12, 14, 30, 0, 0, loc)

	rec := models.AiRecommendStocks{
		StockCode:         "002230.SZ",
		StockName:         "科大讯飞",
		ModelName:         "gpt-5.2",
		StockPrice:        "58.60",
		RecommendCategory: "观察标的（等待止跌/确认）",
		DataTime:          &recordTime,
	}
	rec.ID = 175

	item := mapRecommendRecordToYieldItemWithRecordState(rec, map[uint]models.AiRecommendYieldRecordState{}, map[string]models.AiRecommendYieldState{})
	if item.RecommendTime != "2026-03-12 14:30:00" {
		t.Fatalf("unexpected recommend time: %s", item.RecommendTime)
	}
	if item.BuyTime != "" {
		t.Fatalf("expected empty buy time before activation, got %s", item.BuyTime)
	}
	if item.RecommendCategory != "观察标的（等待止跌/确认）" {
		t.Fatalf("unexpected raw category: %s", item.RecommendCategory)
	}
	if item.RecommendCategoryLabel != "等待激活" {
		t.Fatalf("unexpected category label: %s", item.RecommendCategoryLabel)
	}
	if item.ActivationStatus != "ineligible" {
		t.Fatalf("expected ineligible activation, got %s", item.ActivationStatus)
	}
	if item.BacktestEligibility != recommendBacktestIneligible {
		t.Fatalf("expected ineligible backtest eligibility, got %s", item.BacktestEligibility)
	}
}
