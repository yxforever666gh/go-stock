package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestYieldMaintenanceWritesOnlyV150AndPreservesLegacySnapshots(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "yield-version-boundary.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldDirtyCode{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	at := time.Date(2026, 8, 5, 9, 15, 0, 0, cnLocation())
	legacy := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion142, StockCode: "600001.SH", StockName: "legacy",
		DataTime: &at, ActivationStatus: "pending", ExecutionState: recommendExecutionConditional,
	}
	current := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ", StockName: "current",
		DataTime: &at, ActivationStatus: "pending", ExecutionState: recommendExecutionConditional,
	}
	if err := db.Dao.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy recommendation: %v", err)
	}
	if err := db.Dao.Create(&current).Error; err != nil {
		t.Fatalf("seed current recommendation: %v", err)
	}
	legacyState := models.AiRecommendYieldRecordState{
		RecommendID: legacy.ID, StockCode: legacy.StockCode, StockName: legacy.StockName,
		ActivationStatus: "pending", PositionStatus: "legacy-frozen", DataStatusReason: "legacy-original",
	}
	currentState := models.AiRecommendYieldRecordState{
		RecommendID: current.ID, StockCode: current.StockCode, StockName: current.StockName,
		ActivationStatus: "pending", PositionStatus: "current-before", DataStatusReason: "current-original",
	}
	if err := db.Dao.Create(&legacyState).Error; err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	if err := db.Dao.Create(&currentState).Error; err != nil {
		t.Fatalf("seed current state: %v", err)
	}
	aggregate := models.AiRecommendYieldState{
		StockCode: legacy.StockCode, StockName: "frozen aggregate",
		ActivationStatus: "pending", PositionStatus: "aggregate-original", DataStatusReason: "aggregate-original",
	}
	if err := db.Dao.Create(&aggregate).Error; err != nil {
		t.Fatalf("seed legacy aggregate: %v", err)
	}

	if err := upsertYieldRecordStates([]models.AiRecommendYieldRecordState{
		{RecommendID: legacy.ID, StockCode: legacy.StockCode, ActivationStatus: "activated", PositionStatus: "mutated", DataStatusReason: "mutated"},
		{RecommendID: current.ID, StockCode: current.StockCode, ActivationStatus: "activated", PositionStatus: "current-after", DataStatusReason: "current-updated"},
	}); err != nil {
		t.Fatalf("mixed-version record-state upsert: %v", err)
	}
	if err := upsertYieldStates([]models.AiRecommendYieldState{
		{StockCode: legacy.StockCode, ActivationStatus: "activated", PositionStatus: "mutated", DataStatusReason: "mutated"},
	}); err != nil {
		t.Fatalf("aggregate no-op upsert: %v", err)
	}
	if err := cleanRemovedYieldRecordStates([]uint{current.ID}); err != nil {
		t.Fatalf("record cleanup: %v", err)
	}
	if err := cleanRemovedYieldStates([]string{current.StockCode}); err != nil {
		t.Fatalf("aggregate cleanup: %v", err)
	}

	var gotLegacyState, gotCurrentState models.AiRecommendYieldRecordState
	if err := db.Dao.Where("recommend_id = ?", legacy.ID).First(&gotLegacyState).Error; err != nil {
		t.Fatalf("reload legacy state: %v", err)
	}
	if err := db.Dao.Where("recommend_id = ?", current.ID).First(&gotCurrentState).Error; err != nil {
		t.Fatalf("reload current state: %v", err)
	}
	if gotLegacyState.ActivationStatus != "pending" || gotLegacyState.PositionStatus != "legacy-frozen" || gotLegacyState.DataStatusReason != "legacy-original" {
		t.Fatalf("legacy record state changed: %+v", gotLegacyState)
	}
	if gotCurrentState.ActivationStatus != "activated" || gotCurrentState.PositionStatus != "current-after" || gotCurrentState.DataStatusReason != "current-updated" {
		t.Fatalf("V1.5 record state was not updated: %+v", gotCurrentState)
	}
	var gotLegacy, gotCurrent models.AiRecommendStocks
	if err := db.Dao.First(&gotLegacy, legacy.ID).Error; err != nil {
		t.Fatalf("reload legacy recommendation: %v", err)
	}
	if err := db.Dao.First(&gotCurrent, current.ID).Error; err != nil {
		t.Fatalf("reload current recommendation: %v", err)
	}
	if gotLegacy.ActivationStatus != "pending" {
		t.Fatalf("legacy recommendation activation changed: %q", gotLegacy.ActivationStatus)
	}
	if gotCurrent.ActivationStatus != "activated" {
		t.Fatalf("V1.5 recommendation activation = %q, want activated", gotCurrent.ActivationStatus)
	}
	var gotAggregate models.AiRecommendYieldState
	if err := db.Dao.First(&gotAggregate, aggregate.ID).Error; err != nil {
		t.Fatalf("reload aggregate: %v", err)
	}
	if gotAggregate.ActivationStatus != "pending" || gotAggregate.PositionStatus != "aggregate-original" || gotAggregate.DataStatusReason != "aggregate-original" {
		t.Fatalf("versionless legacy aggregate changed: %+v", gotAggregate)
	}
}

func TestLegacyRecommendationRejectsOverrideAndCRUDMutation(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "legacy-write-guards.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldOverride{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldDirtyCode{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	at := time.Date(2026, 8, 5, 9, 15, 0, 0, cnLocation())
	legacy := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion142, StockCode: "600002.SH", StockName: "legacy",
		DataTime: &at, ActivationStatus: "invalid", ExecutionState: recommendExecutionAnalysisOnly,
	}
	current := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion150, StockCode: "000002.SZ", StockName: "current",
		DataTime: &at, ActivationStatus: "pending", ExecutionState: recommendExecutionConditional,
	}
	if err := db.Dao.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	unversionedLegacy := models.AiRecommendStocks{
		StockCode: "600003.SH", StockName: "unversioned-legacy", DataTime: &at,
		ActivationStatus: "pending", ExecutionState: recommendExecutionConditional,
	}
	if err := db.Dao.Create(&unversionedLegacy).Error; err != nil {
		t.Fatalf("seed unversioned legacy: %v", err)
	}
	if err := db.Dao.Create(&current).Error; err != nil {
		t.Fatalf("seed current: %v", err)
	}
	currentAnalysisOnly := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion150, StockCode: "000003.SZ", StockName: "current-analysis",
		DataTime: &at, ActivationStatus: "invalid", ExecutionState: recommendExecutionAnalysisOnly,
	}
	if err := db.Dao.Create(&currentAnalysisOnly).Error; err != nil {
		t.Fatalf("seed current analysis_only: %v", err)
	}
	currentMissingMarketData := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion150, StockCode: "000004.SZ", StockName: "current-missing-data",
		DataTime: &at, ActivationStatus: "invalid", RecommendStatus: "missing_market_data",
	}
	if err := db.Dao.Create(&currentMissingMarketData).Error; err != nil {
		t.Fatalf("seed current missing_market_data: %v", err)
	}

	legacyOverride := models.AiRecommendYieldOverride{RecommendID: legacy.ID, StockCode: legacy.StockCode, ActivationStatusOverride: "pending"}
	if err := upsertAiRecommendYieldOverride(&legacyOverride); err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("legacy override error = %v, want frozen rejection", err)
	}
	currentOverride := models.AiRecommendYieldOverride{RecommendID: current.ID, StockCode: current.StockCode, ActivationStatusOverride: "pending"}
	if err := upsertAiRecommendYieldOverride(&currentOverride); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("V1.5 sealed-ledger override error = %v, want immutable rejection", err)
	}
	var overrideCount int64
	if err := db.Dao.Model(&models.AiRecommendYieldOverride{}).Count(&overrideCount).Error; err != nil {
		t.Fatalf("count overrides: %v", err)
	}
	if overrideCount != 0 {
		t.Fatalf("override count = %d, want no mutable rows", overrideCount)
	}

	service := NewAiRecommendStocksService()
	if err := service.UpdateAiRecommendStocks(legacy.ID, &models.AiRecommendStocks{StockName: "mutated"}); err == nil {
		t.Fatal("legacy update must be rejected")
	}
	if err := service.UpdateAiRecommendStocks(unversionedLegacy.ID, &models.AiRecommendStocks{StockName: "unversioned-updated"}); err == nil {
		t.Fatal("unversioned legacy update must be rejected")
	}
	if err := service.UpdateAiRecommendStocks(currentAnalysisOnly.ID, &models.AiRecommendStocks{ExecutionState: recommendExecutionConditional}); err == nil {
		t.Fatal("V1.5 analysis_only update to conditional must be rejected")
	}
	if err := service.UpdateAiRecommendStocks(currentMissingMarketData.ID, &models.AiRecommendStocks{RecommendStatus: "valid", RecommendCategory: recommendExecutionConditional}); err == nil {
		t.Fatal("status-only analysis_only update to conditional must be rejected")
	}
	if err := service.UpdateAiRecommendStocks(current.ID, &models.AiRecommendStocks{SummaryVersion: marketSummaryVersion142}); err == nil {
		t.Fatal("strategy version mutation must be rejected")
	}
	if err := service.DeleteAiRecommendStocks(legacy.ID); err == nil {
		t.Fatal("legacy delete must be rejected")
	}
	if err := service.DeleteAiRecommendStocks(unversionedLegacy.ID); err == nil {
		t.Fatal("unversioned legacy delete must be rejected")
	}
	if err := service.BatchDeleteAiRecommendStocks([]uint{current.ID, legacy.ID}); err == nil {
		t.Fatal("mixed batch containing a legacy row must be rejected atomically")
	}
	if err := service.CreateAiRecommendStocks(&models.AiRecommendStocks{SummaryVersion: marketSummaryVersion142}); err == nil {
		t.Fatal("creating a new legacy-version record must be rejected")
	}
	unversionedCreate := buildValidAiRecommendForCreate(at.Add(time.Hour), "600004.SH", "unversioned-create")
	unversionedCreate.SummaryVersion = ""
	if err := service.CreateAiRecommendStocks(unversionedCreate); err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("creating an unversioned legacy record error = %v, want frozen rejection", err)
	}
	var gotLegacy, gotUnversioned, gotCurrent models.AiRecommendStocks
	if err := db.Dao.First(&gotLegacy, legacy.ID).Error; err != nil {
		t.Fatalf("legacy row disappeared: %v", err)
	}
	if err := db.Dao.First(&gotCurrent, current.ID).Error; err != nil {
		t.Fatalf("current row disappeared after rejected mixed batch: %v", err)
	}
	if err := db.Dao.First(&gotUnversioned, unversionedLegacy.ID).Error; err != nil {
		t.Fatalf("unversioned legacy row disappeared: %v", err)
	}
	if gotLegacy.StockName != "legacy" || gotLegacy.ExecutionState != recommendExecutionAnalysisOnly {
		t.Fatalf("legacy row changed: %+v", gotLegacy)
	}
	if gotUnversioned.StockName != "unversioned-legacy" {
		t.Fatalf("unversioned legacy row changed: %+v", gotUnversioned)
	}
}

func TestFrozenLegacyPredicateMatchesEveryNonCurrentCohort(t *testing.T) {
	tests := []struct {
		version string
		frozen  bool
	}{
		{version: marketSummaryVersion142, frozen: true},
		{version: "v1.4.2", frozen: true},
		{version: marketSummaryVersion141, frozen: true},
		{version: marketSummaryVersion140, frozen: true},
		{version: marketSummaryVersion136, frozen: true},
		{version: marketSummaryPhase4Version, frozen: true},
		{version: "", frozen: true},
		{version: marketSummaryVersion150, frozen: false},
	}
	for _, test := range tests {
		rec := models.AiRecommendStocks{SummaryVersion: test.version}
		if got := isFrozenLegacyStrategyRecord(&rec); got != test.frozen {
			t.Fatalf("version %q frozen=%t, want %t", test.version, got, test.frozen)
		}
	}
}

func TestLegacyCohortRejectsDerivedProductionRows(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "legacy-derived-write-guards.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendOpeningReview{},
		&models.AiRecommendYieldRecordState{},
		&models.MarketSummaryRunDiagnostic{},
	); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 5, 9, 40, 0, 0, cnLocation())
	legacy := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion142,
		StockCode:      "600005.SH",
		StockName:      "legacy-derived",
		DataTime:       &at,
	}
	if err := db.Dao.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	err := saveOpeningReviews([]models.AiRecommendOpeningReview{{
		RecommendID: legacy.ID,
		StockCode:   legacy.StockCode,
		TradeDate:   at.Format(time.DateOnly),
		ReviewScope: openingReviewScopePending,
		ReviewPhase: openingReviewPhase0940,
	}})
	if err == nil || !strings.Contains(err.Error(), "only be written") {
		t.Fatalf("legacy opening review error = %v", err)
	}
	err = upsertMinuteUncoverableRecordState(minuteCoverageIssue{
		RecordID:  legacy.ID,
		StockCode: legacy.StockCode,
	}, legacy, "legacy must stay frozen", at)
	if err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("legacy yield state error = %v", err)
	}
	err = SaveMarketSummaryRunDiagnostic(&models.MarketSummaryRunDiagnostic{
		RunID:          "legacy-diagnostic",
		SummaryVersion: marketSummaryVersion142,
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("legacy diagnostic error = %v", err)
	}

	assertStrategyTableCount(t, &models.AiRecommendOpeningReview{}, 0)
	assertStrategyTableCount(t, &models.AiRecommendYieldRecordState{}, 0)
	assertStrategyTableCount(t, &models.MarketSummaryRunDiagnostic{}, 0)
}

func TestHistoricalOverrideCannotReviveAnalysisOnlyInMemory(t *testing.T) {
	rec := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion142, ExecutionState: recommendExecutionAnalysisOnly,
		RecommendCategory: "observe", RecommendStatus: "insufficient_evidence", ActivationStatus: "invalid",
	}
	override := models.AiRecommendYieldOverride{
		ActivationStatusOverride: "pending", RecommendBuyPrice: "10.00-10.20",
	}
	applyYieldOverrideToRecommend(&rec, &override)
	if rec.ExecutionState != recommendExecutionAnalysisOnly || rec.RecommendCategory != "observe" || rec.RecommendStatus != "insufficient_evidence" {
		t.Fatalf("historical override revived analysis_only recommendation: %+v", rec)
	}

	item := models.AiRecommendStocksYieldItem{
		ExecutionState: recommendExecutionAnalysisOnly, ActivationStatus: "invalid", DataStatus: "not_tradeable",
	}
	applyYieldOverrideToYieldItem(&item, &override)
	if item.ActivationStatus != "invalid" || item.DataStatus != "not_tradeable" {
		t.Fatalf("historical override revived analysis_only yield item: %+v", item)
	}
}

func TestV150YieldOverrideCannotReplaceLedgerExecutionState(t *testing.T) {
	rec := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion150, ExecutionState: recommendExecutionConditional,
		RecommendCategory: "conditional", RecommendStatus: "valid", ActivationStatus: "activated",
		ActivationRuleJSON: `{"version":"1.5.0","path":"pullback"}`,
		RecommendBuyPrice:  "10.00", RecommendBuyPriceMin: 10, RecommendBuyPriceMax: 10,
		RecommendStopProfitPrice: "11.00", RecommendStopProfitPriceMin: 11, RecommendStopProfitPriceMax: 11,
		RecommendStopLossPrice: "9.50",
	}
	item := models.AiRecommendStocksYieldItem{
		SummaryVersion: marketSummaryVersion150, ActivationStatus: "activated",
		ActivationRule: "frozen-rule", RecommendBuyPrice: "10.00", YieldRate: 1.25, YieldRateText: "+1.25%",
	}
	override := models.AiRecommendYieldOverride{
		ActivationStatusOverride: "pending", ActivationRuleJSON: `{"tampered":true}`,
		RecommendBuyPrice: "99.00", DataStatusReason: "mutable override",
	}
	applyYieldOverrideToRecommend(&rec, &override)
	if rec.ExecutionState != recommendExecutionConditional || rec.ActivationStatus != "activated" ||
		rec.ActivationRuleJSON != `{"version":"1.5.0","path":"pullback"}` || rec.RecommendBuyPrice != "10.00" ||
		rec.RecommendBuyPriceMin != 10 || rec.RecommendBuyPriceMax != 10 || rec.RecommendStopProfitPrice != "11.00" ||
		rec.RecommendStopLossPrice != "9.50" || rec.RecommendCategory != "conditional" || rec.RecommendStatus != "valid" {
		t.Fatalf("V1.5 mutable override changed frozen recommendation: %+v", rec)
	}
	applyYieldOverrideToYieldItem(&item, &override)
	if item.ActivationStatus != "activated" || item.ActivationRule != "frozen-rule" || item.RecommendBuyPrice != "10.00" || item.YieldRate != 1.25 {
		t.Fatalf("V1.5 mutable override replaced sealed ledger state: %+v", item)
	}
}

func TestMarketDataRepairCannotReviveAnalysisOnly(t *testing.T) {
	rec := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion142, ExecutionState: recommendExecutionAnalysisOnly,
		RecommendCategory: "observe", RecommendStatus: "missing_market_data", ActivationStatus: "invalid",
		ActivationRuleSource: "market_summary",
		ActivationRuleJSON:   `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","thresholdValue":10,"thresholdMax":10.2}]}`,
		RecommendBuyPrice:    "10.00-10.20", RecommendStopProfitPrice: "11.00", RecommendStopLossPrice: "9.50",
	}
	if err := normalizeMarketSummaryExecutionDataForSaveWithFetch(&rec, false); err != nil {
		t.Fatalf("normalize analysis_only repair: %v", err)
	}
	if rec.ExecutionState != recommendExecutionAnalysisOnly || rec.RecommendStatus != "missing_market_data" || rec.ActivationStatus != "invalid" {
		t.Fatalf("market-data normalization revived analysis_only: %+v", rec)
	}
	if shouldAttemptRecoverHistoricalMarketSummaryRule(rec) {
		t.Fatal("analysis_only must not enter historical rule recovery")
	}
	markMarketSummaryRecommendPendingMarketData(&rec, "later data arrived")
	if rec.ExecutionState != recommendExecutionAnalysisOnly || rec.ActivationStatus != "invalid" {
		t.Fatalf("pending-data marker revived analysis_only: %+v", rec)
	}
}
