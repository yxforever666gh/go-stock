package data

import (
	"go-stock/backend/models"
	"math"
	"strings"
	"testing"
	"time"
)

func TestResolveMinuteCoverageScope_FallbackToStockCacheRange(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	cacheStart := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	cacheEnd := time.Date(2026, 3, 11, 15, 0, 0, 0, loc)
	state := &models.AiRecommendYieldRecordState{
		StockCode: "688017.SH",
	}
	cacheRanges := map[string]minuteCacheRange{
		"688017.SH": {
			Start: &cacheStart,
			End:   &cacheEnd,
		},
	}

	start, end, ok := resolveMinuteCoverageScope(state, state.StockCode, cacheRanges)
	if !ok {
		t.Fatalf("expected fallback cache scope")
	}
	if !start.Equal(cacheStart) {
		t.Fatalf("unexpected cache start: %v", start)
	}
	if !end.Equal(cacheEnd) {
		t.Fatalf("unexpected cache end: %v", end)
	}
}

func TestResolveMinuteCoverageScope_MergesRecordAndStockScope(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordStart := time.Date(2026, 3, 11, 9, 30, 0, 0, loc)
	recordEnd := time.Date(2026, 3, 12, 15, 0, 0, 0, loc)
	stockStart := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)
	stockEnd := time.Date(2026, 3, 11, 15, 0, 0, 0, loc)
	state := &models.AiRecommendYieldRecordState{
		StockCode:        "002747.SZ",
		MinuteCacheStart: &recordStart,
		MinuteCacheEnd:   &recordEnd,
	}
	cacheRanges := map[string]minuteCacheRange{
		"002747.SZ": {
			Start: &stockStart,
			End:   &stockEnd,
		},
	}

	start, end, ok := resolveMinuteCoverageScope(state, state.StockCode, cacheRanges)
	if !ok {
		t.Fatalf("expected merged cache scope")
	}
	if !start.Equal(stockStart) {
		t.Fatalf("expected merged cache start, got %v", start)
	}
	if !end.Equal(recordEnd) {
		t.Fatalf("expected merged cache end, got %v", end)
	}
}

func TestMapRecommendRecordToYieldItem_SkipStaleSellState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 2, 27, 8, 33, 57, 0, loc)
	sellTime := time.Date(2026, 2, 26, 14, 4, 0, 0, loc)
	sellAmount := 34.0

	rec := models.AiRecommendStocks{
		StockCode:  "300398.SZ",
		StockName:  "飞凯材料",
		StockPrice: "33.42",
		DataTime:   &recordTime,
	}
	stateMap := map[string]models.AiRecommendYieldState{
		normalizeRecommendStockCode("300398.SZ"): {
			StockCode:          "300398.SZ",
			StockName:          "飞凯材料",
			PositionStatus:     "已止盈",
			SellTime:           &sellTime,
			RealizedSellAmount: &sellAmount,
			CurrentPrice:       28.83,
			YieldRate:          9.01,
			YieldRateText:      "+9.01%",
			DataStatus:         "正常",
		},
	}

	item := mapRecommendRecordToYieldItem(rec, stateMap)
	if item.BuyTime != "" {
		t.Fatalf("expected empty BuyTime before activation, got %s", item.BuyTime)
	}
	if item.SellTime != "未纳入回测" {
		t.Fatalf("expected SellTime=未纳入回测, got %s", item.SellTime)
	}
	if item.SellAmount != nil {
		t.Fatalf("expected SellAmount=nil, got %v", *item.SellAmount)
	}
	if item.PositionStatus != "未纳入回测" {
		t.Fatalf("expected PositionStatus=未纳入回测, got %s", item.PositionStatus)
	}
	if item.ActivationStatus != "ineligible" {
		t.Fatalf("expected ActivationStatus=ineligible, got %s", item.ActivationStatus)
	}
	if item.YieldRateText != "--" {
		t.Fatalf("expected YieldRateText=-- before activation, got %s", item.YieldRateText)
	}
	if math.Abs(item.YieldRate) > 0.001 {
		t.Fatalf("expected YieldRate=0 before activation, got %.4f", item.YieldRate)
	}
	if item.BacktestEligibility != recommendBacktestIneligible {
		t.Fatalf("expected BacktestEligibility=ineligible, got %s", item.BacktestEligibility)
	}
}

func TestMapRecommendRecordToYieldItem_KeepValidSellState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 2, 24, 22, 11, 4, 0, loc)
	sellTime := time.Date(2026, 2, 26, 14, 4, 0, 0, loc)
	sellAmount := 34.0

	rec := models.AiRecommendStocks{
		StockCode:  "300398.SZ",
		StockName:  "飞凯材料",
		StockPrice: "33.42",
		DataTime:   &recordTime,
	}
	stateMap := map[string]models.AiRecommendYieldState{
		normalizeRecommendStockCode("300398.SZ"): {
			StockCode:          "300398.SZ",
			StockName:          "飞凯材料",
			PositionStatus:     "已止盈",
			SellTime:           &sellTime,
			RealizedSellAmount: &sellAmount,
			CurrentPrice:       28.83,
			YieldRate:          9.01,
			YieldRateText:      "+9.01%",
			DataStatus:         "正常",
		},
	}

	item := mapRecommendRecordToYieldItem(rec, stateMap)
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
		t.Fatalf("expected ActivationStatus=ineligible, got %s", item.ActivationStatus)
	}
	if item.YieldRateText != "--" {
		t.Fatalf("expected YieldRateText=-- before activation, got %s", item.YieldRateText)
	}
	if math.Abs(item.YieldRate) > 0.001 {
		t.Fatalf("expected YieldRate=0 before activation, got %.4f", item.YieldRate)
	}
	if item.BacktestEligibility != recommendBacktestIneligible {
		t.Fatalf("expected BacktestEligibility=ineligible, got %s", item.BacktestEligibility)
	}
}

func TestMapRecommendRecordToYieldItemWithRecordState_SkippedStatus(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 12, 10, 15, 0, 0, loc)

	rec := models.AiRecommendStocks{
		StockCode:         "002230.SZ",
		StockName:         "科大讯飞",
		ModelName:         "gpt-5.4",
		RecommendCategory: "observe",
		RecommendStatus:   "avoid",
		InvalidCondition:  "跌破支撑",
		DataTime:          &recordTime,
	}
	rec.ID = 2001

	recordStateMap := map[uint]models.AiRecommendYieldRecordState{
		rec.ID: {
			RecommendID:      rec.ID,
			StockCode:        "002230.SZ",
			StockName:        "科大讯飞",
			ModelName:        "gpt-5.4",
			ActivationStatus: "skipped",
			PositionStatus:   "已放弃",
			DataStatus:       "已跳过",
			DataStatusReason: "AI 推荐状态为回避，不纳入收益率跟踪；失效条件：跌破支撑",
			CurrentPrice:     46.2,
		},
	}

	item := mapRecommendRecordToYieldItemWithRecordState(rec, recordStateMap, map[string]models.AiRecommendYieldState{})
	if item.ActivationStatus != "skipped" {
		t.Fatalf("expected skipped activation status, got %s", item.ActivationStatus)
	}
	if item.PositionStatus != "已放弃" {
		t.Fatalf("expected 已放弃 position, got %s", item.PositionStatus)
	}
	if item.SellTime != "已跳过" {
		t.Fatalf("expected 已跳过 sell time, got %s", item.SellTime)
	}
	if item.DataStatus != "已跳过" {
		t.Fatalf("expected 已跳过 data status, got %s", item.DataStatus)
	}
	if item.BuyTime != "" || item.BuyAmount != 0 {
		t.Fatalf("expected no buy info for skipped item, got buyTime=%q buyAmount=%.2f", item.BuyTime, item.BuyAmount)
	}
}

func TestMapRecommendRecordToYieldItemWithRecordState_SkipOverridesActivatedState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 4, 6, 9, 30, 0, 0, loc)
	activationTime := time.Date(2026, 3, 9, 10, 18, 0, 0, loc)

	rec := models.AiRecommendStocks{
		StockCode:         "000651.SZ",
		StockName:         "格力电器",
		ModelName:         "gpt-5",
		RecommendCategory: "observe",
		RecommendStatus:   "insufficient_evidence",
		InvalidCondition:  "股价有效跌破35.8元且连续两日站不上5日线",
		DataTime:          &recordTime,
	}
	rec.ID = 119

	recordStateMap := map[uint]models.AiRecommendYieldRecordState{
		rec.ID: {
			RecommendID:      rec.ID,
			StockCode:        "000651.SZ",
			StockName:        "格力电器",
			ModelName:        "gpt-5",
			ActivationStatus: "activated",
			ActivationTime:   &activationTime,
			ActivationPrice:  37.4,
			BuyTime:          &activationTime,
			BuyAmount:        37.4,
			PositionStatus:   "持有",
			CurrentPrice:     37.82,
			DataStatus:       "正常",
		},
	}

	item := mapRecommendRecordToYieldItemWithRecordState(rec, recordStateMap, map[string]models.AiRecommendYieldState{})
	if item.ActivationStatus != "skipped" {
		t.Fatalf("expected skipped activation status, got %s", item.ActivationStatus)
	}
	if item.PositionStatus != "已放弃" {
		t.Fatalf("expected 已放弃 position, got %s", item.PositionStatus)
	}
	if item.SellTime != "已跳过" {
		t.Fatalf("expected 已跳过 sell time, got %s", item.SellTime)
	}
	if item.BuyTime != "" || item.BuyAmount != 0 {
		t.Fatalf("expected stale activated snapshot to be cleared, got buyTime=%q buyAmount=%.2f", item.BuyTime, item.BuyAmount)
	}
	if item.YieldRateText != "--" || math.Abs(item.YieldRate) > 0.001 {
		t.Fatalf("expected skipped item to clear yield, got %s %.4f", item.YieldRateText, item.YieldRate)
	}
	if !strings.Contains(item.DataStatusReason, "证据不足") {
		t.Fatalf("expected skip reason to mention 证据不足, got %s", item.DataStatusReason)
	}
}

func TestMapRecommendRecordToYieldItem_SkipByRecommendStatusWithoutState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 4, 6, 10, 15, 0, 0, loc)

	rec := models.AiRecommendStocks{
		StockCode:         "002230.SZ",
		StockName:         "科大讯飞",
		ModelName:         "gpt-5.4",
		RecommendCategory: "observe",
		RecommendStatus:   "insufficient_evidence",
		InvalidCondition:  "跌破支撑",
		DataTime:          &recordTime,
	}

	item := mapRecommendRecordToYieldItem(rec, map[string]models.AiRecommendYieldState{})
	if item.ActivationStatus != "skipped" {
		t.Fatalf("expected skipped activation status, got %s", item.ActivationStatus)
	}
	if item.PositionStatus != "已放弃" {
		t.Fatalf("expected 已放弃 position, got %s", item.PositionStatus)
	}
	if item.SellTime != "已跳过" {
		t.Fatalf("expected 已跳过 sell time, got %s", item.SellTime)
	}
	if item.DataStatus != "已跳过" {
		t.Fatalf("expected 已跳过 data status, got %s", item.DataStatus)
	}
	if !strings.Contains(item.DataStatusReason, "证据不足") {
		t.Fatalf("expected skip reason to mention 证据不足, got %s", item.DataStatusReason)
	}
}

func TestMapRecommendRecordToYieldItem_SkipOverridesLegacyActivatedState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 4, 6, 9, 30, 0, 0, loc)
	activationTime := time.Date(2026, 3, 9, 10, 18, 0, 0, loc)

	rec := models.AiRecommendStocks{
		StockCode:         "000651.SZ",
		StockName:         "格力电器",
		ModelName:         "gpt-5",
		RecommendCategory: "observe",
		RecommendStatus:   "insufficient_evidence",
		InvalidCondition:  "股价有效跌破35.8元且连续两日站不上5日线",
		DataTime:          &recordTime,
	}

	stateMap := map[string]models.AiRecommendYieldState{
		normalizeRecommendStockCode("000651.SZ"): {
			StockCode:        "000651.SZ",
			StockName:        "格力电器",
			ActivationStatus: "activated",
			ActivationTime:   &activationTime,
			ActivationPrice:  37.4,
			BuyTime:          &activationTime,
			BuyAmount:        37.4,
			PositionStatus:   "持有",
			CurrentPrice:     37.82,
			DataStatus:       "正常",
		},
	}

	item := mapRecommendRecordToYieldItem(rec, stateMap)
	if item.ActivationStatus != "skipped" {
		t.Fatalf("expected skipped activation status, got %s", item.ActivationStatus)
	}
	if item.PositionStatus != "已放弃" {
		t.Fatalf("expected 已放弃 position, got %s", item.PositionStatus)
	}
	if item.SellTime != "已跳过" {
		t.Fatalf("expected 已跳过 sell time, got %s", item.SellTime)
	}
	if item.BuyTime != "" || item.BuyAmount != 0 {
		t.Fatalf("expected stale activated snapshot to be cleared, got buyTime=%q buyAmount=%.2f", item.BuyTime, item.BuyAmount)
	}
	if item.YieldRateText != "--" || math.Abs(item.YieldRate) > 0.001 {
		t.Fatalf("expected skipped item to clear yield, got %s %.4f", item.YieldRateText, item.YieldRate)
	}
	if !strings.Contains(item.DataStatusReason, "证据不足") {
		t.Fatalf("expected skip reason to mention 证据不足, got %s", item.DataStatusReason)
	}
}
