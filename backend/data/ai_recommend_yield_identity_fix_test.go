package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestSyncYieldRecordStateIdentityFieldsAlignsWithRecommendRecord(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "yield-identity-fix.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 2, 28, 20, 48, 24, 0, loc)
	rec := models.AiRecommendStocks{
		SummaryVersion:    marketSummaryVersion150,
		StockCode:         "600183.SH",
		StockName:         "生益科技",
		ModelName:         "gpt-5.4",
		BkName:            "PCB",
		RecommendCategory: "",
		DataTime:          &recordTime,
	}
	if err := db.Dao.Create(&rec).Error; err != nil {
		t.Fatalf("create recommend failed: %v", err)
	}

	wrongTime := time.Date(2026, 4, 1, 9, 30, 0, 0, loc)
	state := models.AiRecommendYieldRecordState{
		RecommendID:       rec.ID,
		StockCode:         "600183.SH",
		StockName:         "生益科技",
		ModelName:         "gpt-5.4",
		BkName:            "电子材料",
		RecommendCategory: "low_absorb",
		RecommendTime:     &wrongTime,
		SignalTime:        &wrongTime,
	}
	if err := db.Dao.Create(&state).Error; err != nil {
		t.Fatalf("create record state failed: %v", err)
	}

	if err := syncYieldRecordStateIdentityFields(); err != nil {
		t.Fatalf("sync yield record identity failed: %v", err)
	}

	var got models.AiRecommendYieldRecordState
	if err := db.Dao.First(&got, state.ID).Error; err != nil {
		t.Fatalf("reload record state failed: %v", err)
	}
	if got.RecommendCategory != "" {
		t.Fatalf("expected empty recommend category, got %q", got.RecommendCategory)
	}
	if got.BkName != "PCB" {
		t.Fatalf("expected bk name PCB, got %q", got.BkName)
	}
	if got.RecommendTime == nil || !got.RecommendTime.Equal(recordTime) {
		t.Fatalf("expected recommend time %v, got %v", recordTime, got.RecommendTime)
	}
	if got.SignalTime == nil || !got.SignalTime.Equal(recordTime) {
		t.Fatalf("expected signal time %v, got %v", recordTime, got.SignalTime)
	}
}

func TestSyncYieldRecordStateIdentityFieldsLeavesFrozenLegacyRowUnchanged(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "yield-identity-freeze.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &models.AiRecommendYieldRecordState{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	recordTime := time.Date(2026, 2, 28, 14, 0, 0, 0, cnLocation())
	legacy := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion142,
		StockCode:      "600000.SH", StockName: "legacy", BkName: "new-sector",
		RecommendCategory: "new-category", DataTime: &recordTime,
	}
	if err := db.Dao.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy recommendation: %v", err)
	}
	oldTime := recordTime.Add(-24 * time.Hour)
	state := models.AiRecommendYieldRecordState{
		RecommendID: legacy.ID, StockCode: "600000.SH", StockName: "legacy",
		BkName: "frozen-sector", RecommendCategory: "frozen-category",
		RecommendTime: &oldTime, SignalTime: &oldTime,
	}
	if err := db.Dao.Create(&state).Error; err != nil {
		t.Fatalf("create legacy state: %v", err)
	}
	if err := syncYieldRecordStateIdentityFields(); err != nil {
		t.Fatalf("sync identity fields: %v", err)
	}
	var got models.AiRecommendYieldRecordState
	if err := db.Dao.First(&got, state.ID).Error; err != nil {
		t.Fatalf("reload legacy state: %v", err)
	}
	if got.BkName != "frozen-sector" || got.RecommendCategory != "frozen-category" ||
		got.RecommendTime == nil || !got.RecommendTime.Equal(oldTime) ||
		got.SignalTime == nil || !got.SignalTime.Equal(oldTime) {
		t.Fatalf("frozen legacy state mutated: %+v", got)
	}
}

func TestBuildYieldRecordStateFromRecommendOverridesLegacyIdentityFields(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	recordTime := time.Date(2026, 3, 12, 14, 30, 0, 0, loc)
	legacyTime := time.Date(2026, 4, 1, 9, 30, 0, 0, loc)

	rec := models.AiRecommendStocks{
		StockCode:         "002230.SZ",
		StockName:         "科大讯飞",
		ModelName:         "gpt-5.2",
		BkName:            "人工智能",
		RecommendCategory: "",
		DataTime:          &recordTime,
	}
	rec.ID = 175

	existing := &models.AiRecommendYieldRecordState{
		RecommendID:       rec.ID,
		StockCode:         "002230.SZ",
		StockName:         "科大讯飞",
		ModelName:         "gpt-5.2",
		BkName:            "旧板块",
		RecommendCategory: "right_confirm",
		RecommendTime:     &legacyTime,
		SignalTime:        &legacyTime,
	}

	state := buildYieldRecordStateFromRecommend(rec, existing, yieldBuildContext{Now: recordTime.Add(time.Hour)})
	if state.RecommendCategory != "" {
		t.Fatalf("expected empty recommend category, got %q", state.RecommendCategory)
	}
	if state.RecommendTime == nil || !state.RecommendTime.Equal(recordTime) {
		t.Fatalf("expected recommend time %v, got %v", recordTime, state.RecommendTime)
	}
	if state.SignalTime == nil || !state.SignalTime.Equal(recordTime) {
		t.Fatalf("expected signal time %v, got %v", recordTime, state.SignalTime)
	}
	if state.BkName != "人工智能" {
		t.Fatalf("expected bk name 人工智能, got %q", state.BkName)
	}
}
