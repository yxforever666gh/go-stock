package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestRepairHistoricalLegacySkippedRecommendations_ReopensOnlyPreCutoffNonObservation(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "legacy-skip-repair.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldOverride{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	beforeCutoff := time.Date(2026, 4, 5, 10, 0, 0, 0, loc)
	afterCutoff := time.Date(2026, 4, 6, 10, 0, 0, 0, loc)

	reopen := models.AiRecommendStocks{
		SummaryVersion:           marketSummaryVersion150,
		DataTime:                 &beforeCutoff,
		ModelName:                "test-model",
		StockCode:                "002112.SZ",
		StockName:                "三变科技",
		RecommendCategory:        "observe",
		RecommendStatus:          "insufficient_evidence",
		RecommendBuyPrice:        "25.50-26.00放量站稳再看主买入区",
		RecommendStopProfitPrice: "27.50-28.20",
		RecommendStopLossPrice:   "24.30",
		BuySignal:                "价格触发：未来3-5个交易日内股价进入25.5-26.0放量站稳再看主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍",
		InvalidCondition:         "板块不联动或跌破24.30",
	}
	observation := models.AiRecommendStocks{
		SummaryVersion:           marketSummaryVersion150,
		DataTime:                 &beforeCutoff,
		ModelName:                "test-model",
		StockCode:                "300081.SZ",
		StockName:                "恒信东方",
		RecommendCategory:        "observe",
		RecommendStatus:          "insufficient_evidence",
		RecommendBuyPrice:        "9.80-10.20",
		RecommendStopProfitPrice: "10.80-11.20",
		RecommendStopLossPrice:   "9.30",
		BuySignal:                "价格触发：未来3-5个交易日内仅观察9.80-10.20能否重新站稳；未站稳前不建议主动追买",
		InvalidCondition:         "跌破9.30",
	}
	avoid := models.AiRecommendStocks{
		SummaryVersion:    marketSummaryVersion150,
		DataTime:          &beforeCutoff,
		ModelName:         "test-model",
		StockCode:         "603019.SH",
		StockName:         "中科曙光",
		RecommendCategory: "observe",
		RecommendStatus:   "avoid",
		InvalidCondition:  "跌破关键支撑",
	}
	manualSkipped := models.AiRecommendStocks{
		SummaryVersion:           marketSummaryVersion150,
		DataTime:                 &beforeCutoff,
		ModelName:                "test-model",
		StockCode:                "002384.SZ",
		StockName:                "东山精密",
		RecommendCategory:        "observe",
		RecommendStatus:          "insufficient_evidence",
		RecommendBuyPrice:        "31.20-31.80放量站稳再看主买入区",
		RecommendStopProfitPrice: "33.50-34.80",
		RecommendStopLossPrice:   "30.20",
		BuySignal:                "价格触发：未来3-5个交易日内股价进入31.20-31.80放量站稳再看主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍",
		InvalidCondition:         "跌破30.20",
	}
	postCutoff := models.AiRecommendStocks{
		SummaryVersion:           marketSummaryVersion150,
		DataTime:                 &afterCutoff,
		ModelName:                "test-model",
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		RecommendCategory:        "observe",
		RecommendStatus:          "insufficient_evidence",
		RecommendBuyPrice:        "168-172放量站稳再看主买入区",
		RecommendStopProfitPrice: "180-188",
		RecommendStopLossPrice:   "163",
		BuySignal:                "价格触发：未来3-5个交易日内股价进入168-172放量站稳再看主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍",
		InvalidCondition:         "跌破163",
	}

	for idx, rec := range []*models.AiRecommendStocks{&reopen, &observation, &avoid, &manualSkipped, &postCutoff} {
		if err := db.Dao.Create(rec).Error; err != nil {
			t.Fatalf("create rec[%d] failed: %v", idx, err)
		}
	}

	recordStates := []models.AiRecommendYieldRecordState{
		{RecommendID: reopen.ID, StockCode: reopen.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "证据不足"},
		{RecommendID: observation.ID, StockCode: observation.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "买入依据含观察"},
		{RecommendID: avoid.ID, StockCode: avoid.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "回避"},
		{RecommendID: manualSkipped.ID, StockCode: manualSkipped.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "证据不足"},
		{RecommendID: postCutoff.ID, StockCode: postCutoff.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "证据不足"},
	}
	if err := db.Dao.Create(&recordStates).Error; err != nil {
		t.Fatalf("create record states failed: %v", err)
	}

	aggregateStates := []models.AiRecommendYieldState{
		{StockCode: reopen.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "证据不足"},
		{StockCode: observation.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "观察"},
		{StockCode: avoid.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "回避"},
		{StockCode: manualSkipped.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "人工复审跳过"},
		{StockCode: postCutoff.StockCode, ActivationStatus: "skipped", PositionStatus: "已放弃", DataStatus: "已跳过", DataStatusReason: "证据不足"},
	}
	if err := db.Dao.Create(&aggregateStates).Error; err != nil {
		t.Fatalf("create aggregate states failed: %v", err)
	}

	override := models.AiRecommendYieldOverride{
		RecommendID:              manualSkipped.ID,
		StockCode:                manualSkipped.StockCode,
		ActivationStatusOverride: "skipped",
		ReviewSource:             yieldOverrideSourceMarketSummaryRejudge,
	}
	if err := db.Dao.Create(&override).Error; err != nil {
		t.Fatalf("create override failed: %v", err)
	}

	stats, err := RepairHistoricalLegacySkippedRecommendations(time.Date(2026, 4, 9, 10, 0, 0, 0, loc))
	if err != nil {
		t.Fatalf("RepairHistoricalLegacySkippedRecommendations failed: %v", err)
	}
	if stats.Scanned != 4 {
		t.Fatalf("scanned = %d, want 4", stats.Scanned)
	}
	if stats.StillSkipped != 2 {
		t.Fatalf("still skipped = %d, want 2", stats.StillSkipped)
	}
	if stats.OverrideKept != 1 {
		t.Fatalf("override kept = %d, want 1", stats.OverrideKept)
	}
	if stats.RecordStatesReset != 1 {
		t.Fatalf("record reset = %d, want 1", stats.RecordStatesReset)
	}
	if stats.AggregateReset != 0 {
		t.Fatalf("aggregate reset = %d, want 0 (versionless aggregate is frozen)", stats.AggregateReset)
	}
	if stats.RecalcQueuedCodes != 1 {
		t.Fatalf("queued codes = %d, want 1", stats.RecalcQueuedCodes)
	}

	assertStatus := func(table string, query any, want string) {
		t.Helper()
		switch table {
		case "record":
			var state models.AiRecommendYieldRecordState
			if err := db.Dao.First(&state, query).Error; err != nil {
				t.Fatalf("load record state failed: %v", err)
			}
			if state.ActivationStatus != want {
				t.Fatalf("record state %v activation = %s, want %s", query, state.ActivationStatus, want)
			}
		case "aggregate":
			var state models.AiRecommendYieldState
			if err := db.Dao.Where("stock_code = ?", query).First(&state).Error; err != nil {
				t.Fatalf("load aggregate state failed: %v", err)
			}
			if state.ActivationStatus != want {
				t.Fatalf("aggregate state %v activation = %s, want %s", query, state.ActivationStatus, want)
			}
		}
	}

	assertStatus("record", reopen.ID, "pending")
	assertStatus("aggregate", reopen.StockCode, "skipped")
	assertStatus("record", observation.ID, "skipped")
	assertStatus("aggregate", observation.StockCode, "skipped")
	assertStatus("record", avoid.ID, "skipped")
	assertStatus("aggregate", avoid.StockCode, "skipped")
	assertStatus("record", manualSkipped.ID, "skipped")
	assertStatus("aggregate", manualSkipped.StockCode, "skipped")
	assertStatus("record", postCutoff.ID, "skipped")
	assertStatus("aggregate", postCutoff.StockCode, "skipped")
}
