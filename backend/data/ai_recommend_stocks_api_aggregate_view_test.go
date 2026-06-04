package data

import (
	"testing"
	"time"

	"go-stock/backend/models"
)

func TestPickRepresentativeRecommendRecordForYieldAggregate_PreferStateRecommendTimeMatch(t *testing.T) {
	loc := cnLocation()
	older := time.Date(2026, 3, 24, 14, 30, 0, 0, loc)
	newer := time.Date(2026, 4, 7, 11, 32, 3, 0, loc)

	records := []models.AiRecommendStocks{
		{DataTime: &newer},
		{DataTime: &older},
	}
	records[0].ID = 2407
	records[0].StockCode = "002371.SZ"
	records[0].StockName = "北方华创"
	records[1].ID = 2324
	records[1].StockCode = "002371.SZ"
	records[1].StockName = "北方华创"

	state := models.AiRecommendYieldState{
		StockCode:        "002371.SZ",
		RecommendTime:    &older,
		SignalTime:       &older,
		ActivationStatus: "activated",
	}

	got := pickRepresentativeRecommendRecordForYieldAggregate(records, true, state)
	if got.ID != 2324 {
		t.Fatalf("expected representative record id 2324, got %d", got.ID)
	}
}

func TestBuildStrictAggregateYieldItems_PreferAggregateLifecycleForListButKeepMetricPendingRecord(t *testing.T) {
	loc := cnLocation()
	older := time.Date(2026, 3, 24, 14, 30, 0, 0, loc)
	newer := time.Date(2026, 4, 7, 11, 32, 3, 0, loc)
	activation := time.Date(2026, 3, 27, 9, 34, 0, 0, loc)
	sellTime := time.Date(2026, 3, 30, 13, 13, 0, 0, loc)
	sellAmount := 465.0

	records := []models.AiRecommendStocks{
		{
			StockCode:                "002371.SZ",
			StockName:                "北方华创",
			ModelName:                "gpt-5.4",
			BkName:                   "半导体设备",
			DataTime:                 &newer,
			RecommendBuyPrice:        "355.00-365.00",
			RecommendStopProfitPrice: "382.00",
			RecommendStopLossPrice:   "346.00",
		},
		{
			StockCode:                "002371.SZ",
			StockName:                "北方华创",
			ModelName:                "gpt-5.4",
			BkName:                   "半导体设备",
			DataTime:                 &older,
			RecommendBuyPrice:        "430.00-445.00",
			RecommendStopProfitPrice: "465.00",
			RecommendStopLossPrice:   "432.00",
		},
	}
	records[0].ID = 2407
	records[1].ID = 2324

	recordStateMap := map[uint]models.AiRecommendYieldRecordState{
		2407: {
			RecommendID:      2407,
			StockCode:        "002371.SZ",
			StockName:        "北方华创",
			ActivationStatus: "pending",
			PositionStatus:   "待激活",
			DataStatus:       "正常",
			DataStatusReason: "未触发主买入区",
			RecommendTime:    &newer,
			SignalTime:       &newer,
		},
		2324: {
			RecommendID:      2324,
			StockCode:        "002371.SZ",
			StockName:        "北方华创",
			ActivationStatus: "pending",
			PositionStatus:   "待激活",
			DataStatus:       "正常",
			DataStatusReason: "等待激活",
			RecommendTime:    &older,
			SignalTime:       &older,
		},
	}

	stateMap := map[string]models.AiRecommendYieldState{
		"002371.SZ": {
			StockCode:          "002371.SZ",
			StockName:          "北方华创",
			ModelNames:         "gpt-5.4",
			BkName:             "半导体设备",
			RecommendCount:     2,
			RecommendTime:      &older,
			SignalTime:         &older,
			ActivationStatus:   "activated",
			ActivationTime:     &activation,
			ActivationPrice:    442.0,
			BuyTime:            &activation,
			BuyAmount:          442.0,
			StopProfitAmount:   floatPtr(465.0),
			StopLossAmount:     floatPtr(432.0),
			PositionStatus:     "已止盈",
			SellTime:           &sellTime,
			RealizedSellAmount: &sellAmount,
			CurrentPrice:       424.08,
			CurrentPriceTime:   "2026-04-07 16:14:42",
			YieldRate:          4.7,
			YieldRateText:      "+4.70%",
			DataStatus:         "正常",
		},
	}

	listItems, metricItems := buildStrictAggregateYieldItems(
		records,
		recordStateMap,
		stateMap,
		map[uint]models.AiRecommendYieldOverride{},
		aiRecommendYieldDirtyScope{
			Code:   map[string]models.AiRecommendYieldDirtyCode{},
			Record: map[uint]models.AiRecommendYieldDirtyCode{},
		},
		nil,
	)

	if len(listItems) != 1 {
		t.Fatalf("expected 1 aggregate list item, got %d", len(listItems))
	}
	if listItems[0].ActivationStatus != "activated" {
		t.Fatalf("expected aggregate list activation=activated, got %s", listItems[0].ActivationStatus)
	}
	if listItems[0].SignalTime != "2026-04-07 11:32:03" {
		t.Fatalf("expected latest report signal time, got %s", listItems[0].SignalTime)
	}
	if listItems[0].RecommendTime != "2026-04-07 11:32:03" {
		t.Fatalf("expected latest report recommend time, got %s", listItems[0].RecommendTime)
	}
	if listItems[0].RecommendBuyPrice != "355.00-365.00" && listItems[0].RecommendBuyPrice != "355-365" {
		t.Fatalf("expected latest report buy range, got %s", listItems[0].RecommendBuyPrice)
	}
	if listItems[0].PositionStatus != "已止盈" {
		t.Fatalf("expected aggregate list position=已止盈, got %s", listItems[0].PositionStatus)
	}
	if listItems[0].SellTime != "2026-03-30 13:13:00" {
		t.Fatalf("expected aggregate sell time preserved, got %s", listItems[0].SellTime)
	}
	if listItems[0].RecommendCount != 2 {
		t.Fatalf("expected aggregate recommend count=2, got %d", listItems[0].RecommendCount)
	}
	if listItems[0].RecommendID != 0 {
		t.Fatalf("expected aggregate list recommendId=0, got %d", listItems[0].RecommendID)
	}

	if len(metricItems) != 2 {
		t.Fatalf("expected 2 metric items, got %d", len(metricItems))
	}
	activatedCount := 0
	pendingCount := 0
	for _, item := range metricItems {
		switch item.ActivationStatus {
		case "activated":
			activatedCount++
		case "pending":
			pendingCount++
		}
	}
	if activatedCount != 1 {
		t.Fatalf("expected exactly 1 activated metric item, got %d", activatedCount)
	}
	if pendingCount != 1 {
		t.Fatalf("expected exactly 1 pending metric item, got %d", pendingCount)
	}
}

func TestBuildStrictYieldRecordItems_PreserveRecordOrderAndNoFolding(t *testing.T) {
	loc := cnLocation()
	timeA1 := time.Date(2026, 4, 7, 11, 32, 3, 0, loc)
	timeB := time.Date(2026, 4, 6, 9, 45, 0, 0, loc)
	timeA2 := time.Date(2026, 3, 24, 14, 30, 0, 0, loc)
	activation := time.Date(2026, 3, 27, 9, 34, 0, 0, loc)

	records := []models.AiRecommendStocks{
		{
			StockCode:                "300308.SZ",
			StockName:                "中际旭创",
			DataTime:                 &timeA1,
			RecommendBuyPrice:        "88.00-92.00",
			RecommendStopProfitPrice: "98.00",
			RecommendStopLossPrice:   "84.00",
			ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":88,"thresholdMax":92,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
		},
		{
			StockCode:         "300124.SZ",
			StockName:         "汇川技术",
			DataTime:          &timeB,
			RecommendBuyPrice: "66.00-68.00",
			RecommendStatus:   "avoid",
		},
		{
			StockCode:                "300308.SZ",
			StockName:                "中际旭创",
			DataTime:                 &timeA2,
			RecommendBuyPrice:        "80.00-82.00",
			RecommendStopProfitPrice: "88.00",
			RecommendStopLossPrice:   "76.00",
			ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":80,"thresholdMax":82,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
		},
	}
	records[0].ID = 247
	records[1].ID = 246
	records[2].ID = 217

	recordStateMap := map[uint]models.AiRecommendYieldRecordState{
		247: {
			RecommendID:      247,
			StockCode:        "300308.SZ",
			StockName:        "中际旭创",
			ActivationStatus: "pending",
			PositionStatus:   "待激活",
			DataStatus:       "正常",
			DataStatusReason: "未触发主买入区",
			RecommendTime:    &timeA1,
			SignalTime:       &timeA1,
		},
		246: {
			RecommendID:      246,
			StockCode:        "300124.SZ",
			StockName:        "汇川技术",
			ActivationStatus: "skipped",
			PositionStatus:   "已放弃",
			DataStatus:       "已跳过",
			DataStatusReason: "激活前已跌破止损位",
			RecommendTime:    &timeB,
			SignalTime:       &timeB,
		},
	}

	stateMap := map[string]models.AiRecommendYieldState{
		"300308.SZ": {
			StockCode:        "300308.SZ",
			StockName:        "中际旭创",
			RecommendTime:    &timeA2,
			SignalTime:       &timeA2,
			ActivationStatus: "activated",
			ActivationTime:   &activation,
			ActivationPrice:  90.5,
			BuyTime:          &activation,
			BuyAmount:        90.5,
			CurrentPrice:     96.84,
			CurrentPriceTime: "2026-04-08 15:00:00",
			YieldRate:        7.0,
			YieldRateText:    "+7.00%",
			PositionStatus:   "持有",
			DataStatus:       "正常",
		},
		"300124.SZ": {
			StockCode:        "300124.SZ",
			StockName:        "汇川技术",
			ActivationStatus: "skipped",
			PositionStatus:   "已放弃",
			DataStatus:       "已跳过",
			DataStatusReason: "激活前已跌破止损位",
		},
	}

	items := buildStrictYieldRecordItems(
		records,
		recordStateMap,
		stateMap,
		map[uint]models.AiRecommendYieldOverride{},
		aiRecommendYieldDirtyScope{
			Code:   map[string]models.AiRecommendYieldDirtyCode{},
			Record: map[uint]models.AiRecommendYieldDirtyCode{},
		},
		nil,
	)

	if len(items) != 3 {
		t.Fatalf("expected 3 record items, got %d", len(items))
	}
	if items[0].RecommendID != 247 || items[1].RecommendID != 246 || items[2].RecommendID != 217 {
		t.Fatalf("expected record order [247 246 217], got [%d %d %d]", items[0].RecommendID, items[1].RecommendID, items[2].RecommendID)
	}
	if items[0].ActivationStatus != "pending" {
		t.Fatalf("expected latest duplicated stock to stay pending when aggregate lifecycle belongs to older record, got %s", items[0].ActivationStatus)
	}
	if items[0].RecommendCount != 2 {
		t.Fatalf("expected duplicated stock badge count=2, got %d", items[0].RecommendCount)
	}
	if items[0].SignalTime != "2026-04-07 11:32:03" {
		t.Fatalf("expected latest report signal time to be preserved, got %s", items[0].SignalTime)
	}
	if items[0].ActivationTime != "" {
		t.Fatalf("expected latest duplicated stock to not borrow older activation time, got %s", items[0].ActivationTime)
	}
	if items[1].ActivationStatus != "skipped" {
		t.Fatalf("expected skipped record state to be preserved, got %s", items[1].ActivationStatus)
	}
	if items[1].RecommendCount != 0 {
		t.Fatalf("expected single stock row badge hidden with count=0, got %d", items[1].RecommendCount)
	}
	if items[2].ActivationStatus != "activated" {
		t.Fatalf("expected historical duplicate to remain visible and activated, got %s", items[2].ActivationStatus)
	}
	if items[2].RecommendCount != 2 {
		t.Fatalf("expected historical duplicate badge count=2, got %d", items[2].RecommendCount)
	}
	if items[2].SignalTime != "2026-03-24 14:30:00" {
		t.Fatalf("expected historical record signal time to be preserved, got %s", items[2].SignalTime)
	}
	if items[2].ActivationTime != "2026-03-27 09:34:00" {
		t.Fatalf("expected matched historical record to keep aggregate activation time, got %s", items[2].ActivationTime)
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
