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

func TestBuildYieldStateFromAggregatePreservesExistingSoldLifecycleWhenNewAggregatePending(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	now := time.Date(2026, 4, 8, 15, 0, 0, 0, loc)
	futureSignalTime := now.Add(24 * time.Hour)
	activationTime := time.Date(2026, 3, 27, 9, 34, 0, 0, loc)
	sellTime := time.Date(2026, 3, 30, 13, 13, 0, 0, loc)
	sellAmount := 465.0

	aggr := &aiRecommendYieldAggregate{
		StockCode:       "002371.SZ",
		StockName:       "北方华创",
		SignalTime:      futureSignalTime,
		BuyTime:         futureSignalTime,
		RecommendCount:  2,
		StopProfitSum:   465,
		StopProfitCount: 1,
		StopLossSum:     432,
		StopLossCount:   1,
		BkNames:         []string{"半导体设备"},
		ModelNames:      []string{"gpt-5.4"},
	}
	existing := &models.AiRecommendYieldState{
		StockCode:          "002371.SZ",
		StockName:          "北方华创",
		RecommendCount:     1,
		ActivationStatus:   "activated",
		ActivationTime:     &activationTime,
		ActivationPrice:    442.0,
		BuyTime:            &activationTime,
		BuyAmount:          442.0,
		PositionStatus:     "已止盈",
		SellTime:           &sellTime,
		RealizedSellAmount: &sellAmount,
		Frozen:             true,
		DataStatus:         "正常",
		YieldRateText:      "+5.20%",
	}

	state := buildYieldStateFromAggregate(aggr, existing, yieldBuildContext{Now: now})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated lifecycle to be preserved, got %s", state.ActivationStatus)
	}
	if state.PositionStatus != "已止盈" {
		t.Fatalf("expected 已止盈, got %s", state.PositionStatus)
	}
	if state.SellTime == nil || !state.SellTime.Equal(sellTime) {
		t.Fatalf("expected SellTime=%v, got %v", sellTime, state.SellTime)
	}
	if state.RealizedSellAmount == nil || round2(*state.RealizedSellAmount) != 465.0 {
		t.Fatalf("expected RealizedSellAmount=465.0, got %v", state.RealizedSellAmount)
	}
	if !state.Frozen {
		t.Fatal("expected frozen sold lifecycle to be preserved")
	}
	if state.RecommendCount != 2 {
		t.Fatalf("expected RecommendCount=2, got %d", state.RecommendCount)
	}
}

func TestMergeAggregateYieldStateWithRecordStates_PreferSkippedRecordState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recommendTime := time.Date(2026, 4, 7, 11, 32, 3, 0, loc)
	state := models.AiRecommendYieldState{
		StockCode:         "300124.SZ",
		StockName:         "汇川技术",
		RecommendCount:    1,
		ActivationStatus:  "pending",
		PositionStatus:    "待激活",
		DataStatus:        "待激活",
		DataStatusReason:  "未触发主买入区",
		YieldRateText:     "--",
		TotalScopeStart:   "2026-04-07",
		TotalScopeEnd:     "2026-04-08",
		RecommendCategory: "",
	}
	recordStates := []models.AiRecommendYieldRecordState{
		{
			RecommendID:      246,
			StockCode:        "300124.SZ",
			StockName:        "汇川技术",
			ActivationStatus: "skipped",
			PositionStatus:   "已放弃",
			DataStatus:       "已跳过",
			DataStatusReason: "激活前已跌破止损位",
			YieldRateText:    "--",
			RecommendTime:    &recommendTime,
			SignalTime:       &recommendTime,
		},
	}

	mergeAggregateYieldStateWithRecordStates(&state, recordStates)
	if state.ActivationStatus != "skipped" {
		t.Fatalf("expected skipped, got %s", state.ActivationStatus)
	}
	if state.PositionStatus != "已放弃" {
		t.Fatalf("expected 已放弃, got %s", state.PositionStatus)
	}
	if state.DataStatus != "已跳过" {
		t.Fatalf("expected 已跳过, got %s", state.DataStatus)
	}
	if state.DataStatusReason != "激活前已跌破止损位" {
		t.Fatalf("unexpected data status reason: %s", state.DataStatusReason)
	}
}

func TestMergeAggregateYieldStateWithRecordStates_PreferActivatedRecordState(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	activationTime := time.Date(2026, 3, 30, 13, 13, 0, 0, loc)
	sellTime := time.Date(2026, 3, 30, 13, 20, 0, 0, loc)
	sellAmount := 465.0
	state := models.AiRecommendYieldState{
		StockCode:        "002371.SZ",
		StockName:        "北方华创",
		RecommendCount:   2,
		ActivationStatus: "pending",
		PositionStatus:   "待激活",
		DataStatus:       "待激活",
		YieldRateText:    "--",
	}
	recordStates := []models.AiRecommendYieldRecordState{
		{
			RecommendID:        229,
			StockCode:          "002371.SZ",
			StockName:          "北方华创",
			ActivationStatus:   "activated",
			ActivationTime:     &activationTime,
			ActivationPrice:    442.0,
			BuyTime:            &activationTime,
			BuyAmount:          442.0,
			PositionStatus:     "已止盈",
			SellTime:           &sellTime,
			RealizedSellAmount: &sellAmount,
			YieldRate:          5.2,
			YieldRateText:      "+5.20%",
			DataStatus:         "正常",
			Frozen:             true,
		},
	}

	mergeAggregateYieldStateWithRecordStates(&state, recordStates)
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s", state.ActivationStatus)
	}
	if state.PositionStatus != "已止盈" {
		t.Fatalf("expected 已止盈, got %s", state.PositionStatus)
	}
	if state.SellTime == nil || !state.SellTime.Equal(sellTime) {
		t.Fatalf("expected sell time %v, got %v", sellTime, state.SellTime)
	}
	if state.YieldRateText != "+5.20%" {
		t.Fatalf("unexpected yield text: %s", state.YieldRateText)
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
		ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":153,"thresholdMax":153.5,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
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
		ActivationRuleJSON:   `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":18.6,"thresholdMax":18.6,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
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
	recordTime := time.Date(2026, 4, 7, 9, 25, 0, 0, loc)
	activationTime := time.Date(2026, 4, 7, 9, 35, 0, 0, loc)
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
		ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":18.6,"thresholdMax":18.6,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
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
	if state.BuyTime == nil || state.BuyTime.IsZero() {
		t.Fatalf("expected BuyTime to be set, got %v", state.BuyTime)
	}
	if state.BuyAmount < 18.60 || state.BuyAmount > 18.95 {
		t.Fatalf("expected BuyAmount within parsed text range, got %.2f", state.BuyAmount)
	}
	if round2(state.ActivationPrice) != round2(state.BuyAmount) {
		t.Fatalf("expected ActivationPrice to match BuyAmount, got activation=%.2f buy=%.2f", state.ActivationPrice, state.BuyAmount)
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

func TestBuildYieldRecordStateFromRecommend_BeforeCutoffInsufficientEvidenceWithoutObservationNotSkipped(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-state-before-cutoff-insufficient-evidence.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate stock basic failed: %v", err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 8, 4, 21, 29, 0, loc)
	activationTime := time.Date(2026, 3, 9, 9, 35, 0, 0, loc)
	rec := models.AiRecommendStocks{
		StockCode:                "002112.SZ",
		StockName:                "三变科技",
		ModelName:                "gpt-5.4",
		RecommendCategory:        "observe",
		RecommendStatus:          "insufficient_evidence",
		StockPrice:               "25.80",
		DataTime:                 &recordTime,
		RecommendBuyPrice:        "25.50-26.00放量站稳再看主买入区",
		RecommendStopProfitPrice: "27.50-28.20",
		RecommendStopLossPrice:   "24.30",
		BuySignal:                "价格触发：未来3-5个交易日内股价进入25.5-26.0放量站稳再看主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍",
		InvalidCondition:         "大会前后板块不联动；股价跌破24.30元前收附近且放量走弱；龙虎榜次日承接明显不足",
	}
	rec.ID = 128

	if err := db.Dao.Create(&models.AiRecommendMinuteBar{
		StockCode: "002112.SZ",
		TradeTime: activationTime,
		Open:      25.60,
		High:      25.82,
		Low:       25.55,
		Close:     25.76,
		Volume:    1800,
		Amount:    46368,
		Source:    "test",
	}).Error; err != nil {
		t.Fatalf("seed minute bar failed: %v", err)
	}

	state := buildYieldRecordStateFromRecommend(rec, nil, yieldBuildContext{Now: recordTime.Add(48 * time.Hour)})
	if state.ActivationStatus != "activated" {
		t.Fatalf("expected activated, got %s", state.ActivationStatus)
	}
	if state.PositionStatus != "持有" {
		t.Fatalf("expected 持有, got %s", state.PositionStatus)
	}
	if state.DataStatus != "正常" {
		t.Fatalf("expected 正常 data status, got %s", state.DataStatus)
	}
	if state.DataStatusReason != "" {
		t.Fatalf("expected empty data status reason, got %s", state.DataStatusReason)
	}
	if state.BuyTime == nil || !state.BuyTime.Equal(activationTime) {
		t.Fatalf("expected BuyTime=%v, got %v", activationTime, state.BuyTime)
	}
	if round2(state.BuyAmount) != 25.76 {
		t.Fatalf("expected BuyAmount=25.76, got %.2f", state.BuyAmount)
	}
}

func TestSyncRecommendActivationStatusFromRecordStates(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-sync-recommend-activation.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &models.AiRecommendYieldRecordState{}, &models.AiRecommendYieldDirtyCode{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	rows := []models.AiRecommendStocks{
		{ActivationStatus: "pending", ActivationInvalidReason: "旧原因"},
		{ActivationStatus: "pending", ActivationInvalidReason: "应清空"},
		{ActivationStatus: "pending", ActivationInvalidReason: "应保留为空"},
	}
	for i := range rows {
		if err := db.Dao.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed recommend row %d failed: %v", i, err)
		}
	}

	err := syncRecommendActivationStatusFromRecordStates([]models.AiRecommendYieldRecordState{
		{
			RecommendID:      rows[0].ID,
			ActivationStatus: "activated",
			DataStatusReason: "不应保留",
		},
		{
			RecommendID:      rows[1].ID,
			ActivationStatus: "invalid",
			DataStatusReason: "分钟线缺失",
		},
		{
			RecommendID:      rows[2].ID,
			ActivationStatus: "skipped",
			DataStatusReason: "已跳过",
		},
	})
	if err != nil {
		t.Fatalf("sync recommend activation status failed: %v", err)
	}

	var synced []models.AiRecommendStocks
	if err := db.Dao.Order("id asc").Find(&synced).Error; err != nil {
		t.Fatalf("load synced rows failed: %v", err)
	}
	if len(synced) != 3 {
		t.Fatalf("expected 3 synced rows, got %d", len(synced))
	}

	if synced[0].ActivationStatus != "activated" {
		t.Fatalf("expected row0 activated, got %s", synced[0].ActivationStatus)
	}
	if synced[0].ActivationInvalidReason != "" {
		t.Fatalf("expected row0 invalid reason cleared, got %s", synced[0].ActivationInvalidReason)
	}

	if synced[1].ActivationStatus != "invalid" {
		t.Fatalf("expected row1 invalid, got %s", synced[1].ActivationStatus)
	}
	if synced[1].ActivationInvalidReason != "分钟线缺失" {
		t.Fatalf("expected row1 invalid reason synced, got %s", synced[1].ActivationInvalidReason)
	}

	if synced[2].ActivationStatus != "skipped" {
		t.Fatalf("expected row2 skipped, got %s", synced[2].ActivationStatus)
	}
	if synced[2].ActivationInvalidReason != "" {
		t.Fatalf("expected row2 invalid reason cleared, got %s", synced[2].ActivationInvalidReason)
	}
}

func TestApplyStrictPendingStateToYieldItem_DoesNotOverrideTerminalOrAnalysisOnlyState(t *testing.T) {
	dirtyMap := map[string]models.AiRecommendYieldDirtyCode{
		"002328.SZ": {
			StockCode: "002328.SZ",
			Reason:    "跳过复审覆盖后等待严格模式回算",
		},
	}

	invalidItem := models.AiRecommendStocksYieldItem{
		StockCode:        "002328.SZ",
		ActivationStatus: "invalid",
		ExecutionState:   recommendExecutionConditional,
		DataStatus:       "无法判定",
	}
	applyStrictPendingStateToYieldItem(&invalidItem, dirtyMap)
	if !invalidItem.StrictReady {
		t.Fatal("expected invalid terminal item to stay strict-ready")
	}
	if invalidItem.ActivationStatus != "invalid" {
		t.Fatalf("expected invalid activation status to be preserved, got %s", invalidItem.ActivationStatus)
	}
	if invalidItem.DataStatus != "无法判定" {
		t.Fatalf("expected invalid data status to be preserved, got %s", invalidItem.DataStatus)
	}

	analysisOnlyItem := models.AiRecommendStocksYieldItem{
		StockCode:        "002328.SZ",
		ActivationStatus: "pending",
		ExecutionState:   recommendExecutionAnalysisOnly,
		DataStatus:       "未结构化",
	}
	applyStrictPendingStateToYieldItem(&analysisOnlyItem, dirtyMap)
	if !analysisOnlyItem.StrictReady {
		t.Fatal("expected analysis-only item to stay strict-ready")
	}
	if analysisOnlyItem.ActivationStatus != "pending" {
		t.Fatalf("expected analysis-only activation status unchanged, got %s", analysisOnlyItem.ActivationStatus)
	}
	if analysisOnlyItem.DataStatus != "未结构化" {
		t.Fatalf("expected analysis-only data status unchanged, got %s", analysisOnlyItem.DataStatus)
	}
}

func TestUpsertYieldRecordStates_ClearsDirtyForTerminalRecordStates(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-clear-dirty-terminal.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &models.AiRecommendYieldRecordState{}, &models.AiRecommendYieldDirtyCode{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	rec := models.AiRecommendStocks{
		StockCode:          "002328.SZ",
		StockName:          "新朋股份",
		ActivationStatus:   "pending",
		ExecutionState:     recommendExecutionAnalysisOnly,
		RecommendStatus:    "missing_market_data",
		ActivationRuleJSON: "",
	}
	if err := db.Dao.Create(&rec).Error; err != nil {
		t.Fatalf("seed recommend failed: %v", err)
	}
	if err := db.Dao.Create(&models.AiRecommendYieldDirtyCode{
		StockCode:  "002328.SZ",
		Reason:     "跳过复审覆盖后等待严格模式回算",
		ModeNeeded: aiRecommendYieldModeStrict,
	}).Error; err != nil {
		t.Fatalf("seed dirty failed: %v", err)
	}

	if err := upsertYieldRecordStates([]models.AiRecommendYieldRecordState{
		{
			RecommendID:      rec.ID,
			StockCode:        "002328.SZ",
			StockName:        "新朋股份",
			ActivationStatus: "invalid",
			DataStatus:       "无法判定",
			DataStatusReason: "缺少可信实时价格/量能数据",
		},
	}); err != nil {
		t.Fatalf("upsert yield record states failed: %v", err)
	}

	var dirtyCount int64
	if err := db.Dao.Model(&models.AiRecommendYieldDirtyCode{}).
		Where("stock_code = ?", "002328.SZ").
		Count(&dirtyCount).Error; err != nil {
		t.Fatalf("count dirty failed: %v", err)
	}
	if dirtyCount != 0 {
		t.Fatalf("expected terminal record state to clear dirty flag, got %d", dirtyCount)
	}
}
