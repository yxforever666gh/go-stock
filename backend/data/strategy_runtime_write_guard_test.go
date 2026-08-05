package data

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

func TestPausedStrategyRejectsEveryProductionWriteBoundary(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "paused-production-writes.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendOpeningReview{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendDailyBar{},
		&models.MarketSummaryRunDiagnostic{},
	); err != nil {
		t.Fatal(err)
	}
	if err := persistence.MigrateStrategyPersistence(db.Dao); err != nil {
		t.Fatal(err)
	}

	decisionAt := time.Date(2026, 8, 6, 9, 40, 0, 0, cnLocation())
	recommend := models.AiRecommendStocks{
		StockCode: "000001.SZ", StockName: "frozen-current", BkName: "bank",
		SummaryVersion: marketSummaryCurrentVersion, DataTime: &decisionAt,
		ExecutionState: recommendExecutionConditional, ActivationStatus: "pending",
	}
	if err := db.Dao.Create(&recommend).Error; err != nil {
		t.Fatal(err)
	}
	meta := models.AiRecommendYieldMeta{CurrentTradeDate: "2026-08-05", DownloadInProgress: true}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := governance.SetStrategyRuntimeMode(context.Background(), db.Dao, governance.StrategyModePaused, marketSummaryCurrentVersion, "governance refactor", "test"); err != nil {
		t.Fatal(err)
	}

	assertPaused := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, governance.ErrStrategyPaused) {
			t.Errorf("%s error = %v, want ErrStrategyPaused", name, err)
		}
	}

	service := NewAiRecommendStocksService()
	assertPaused("create recommendation", service.CreateAiRecommendStocks(&models.AiRecommendStocks{}))
	assertPaused("update recommendation", service.UpdateAiRecommendStocks(recommend.ID, &models.AiRecommendStocks{StockName: "changed"}))
	assertPaused("delete recommendation", service.DeleteAiRecommendStocks(recommend.ID))
	assertPaused("batch delete recommendation", service.BatchDeleteAiRecommendStocks([]uint{recommend.ID}))
	_, err := EnsureMarketSummaryRecommendStocksSaved("ignored", "test", "test", decisionAt)
	assertPaused("recommendation backfill", err)
	_, err = EnsureMarketSummaryYieldOverridesSaved("ignored", decisionAt)
	assertPaused("yield override backfill", err)
	_, err = RunMorningOpeningReview(decisionAt)
	assertPaused("opening review", err)
	assertPaused("run diagnostic", SaveMarketSummaryRunDiagnostic(&models.MarketSummaryRunDiagnostic{RunID: "paused-run"}))
	_, err = RepairHistoricalLegacySkippedRecommendations(decisionAt)
	assertPaused("skip repair", err)
	_, err = RepairHistoricalMarketSummaryActivationIssues(decisionAt)
	assertPaused("activation repair", err)
	_, err = RunMarketSummaryV150ExecutionMonitor(decisionAt)
	assertPaused("execution monitor", err)
	assertPaused("yield projection", upsertYieldRecordStates([]models.AiRecommendYieldRecordState{{RecommendID: recommend.ID, StockCode: recommend.StockCode}}))
	assertPaused("dirty marker", markAiRecommendYieldDirtyCodes([]string{recommend.StockCode}, "paused test", aiRecommendYieldModeStrict))
	assertPaused("immutable snapshot", PersistMarketSummaryV150Snapshot(context.Background(), db.Dao, nil))
	assertPaused("order event", appendMarketSummaryV150OrderEvents(recommend, models.StrategyRunSnapshot{}, []v150.OrderEvent{{Type: v150.EventSignal, At: decisionAt, Symbol: recommend.StockCode}}, marketSummaryV150EventAccounting{}))

	if triggerYieldQueryRecalcIfStale(&meta, decisionAt, decisionAt) {
		t.Error("paused query unexpectedly triggered a yield recalculation")
	}
	if triggerYieldPendingIntradayRecalcIfStale(&meta, decisionAt, decisionAt, []models.AiRecommendStocks{recommend}, nil) {
		t.Error("paused query unexpectedly triggered an intraday yield recalculation")
	}
	ResetInterruptedAiRecommendYieldTasksOnStartup()

	var got models.AiRecommendStocks
	if err := db.Dao.First(&got, recommend.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.StockName != "frozen-current" || got.ActivationStatus != "pending" {
		t.Fatalf("paused recommendation changed: %+v", got)
	}
	assertStrategyTableCount(t, &models.AiRecommendStocks{}, 1)
	assertStrategyTableCount(t, &models.AiRecommendOpeningReview{}, 0)
	assertStrategyTableCount(t, &models.AiRecommendYieldRecordState{}, 0)
	assertStrategyTableCount(t, &models.AiRecommendYieldDirtyCode{}, 0)
	assertStrategyTableCount(t, &models.MarketSummaryRunDiagnostic{}, 0)
	assertStrategyTableCount(t, &models.StrategyRunSnapshot{}, 0)
	assertStrategyTableCount(t, &models.OrderEvent{}, 0)

	var gotMeta models.AiRecommendYieldMeta
	if err := db.Dao.First(&gotMeta, meta.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotMeta.LastQueryRecalcAt != nil || gotMeta.QueryCooldownUntil != nil || gotMeta.RecalcInProgress || !gotMeta.DownloadInProgress {
		t.Fatalf("paused yield metadata changed: %+v", gotMeta)
	}
}

func TestStrategyProductionWriteFailsClosedWithoutRuntimeControl(t *testing.T) {
	waitForGlobalYieldRecalcIdle(t, 5*time.Second)
	_ = db.Close()
	db.Dao = nil
	db.MinuteDao = nil
	db.Init(filepath.Join(t.TempDir(), "missing-runtime-control.db"))
	t.Cleanup(func() {
		_ = db.Close()
		db.Dao = nil
		db.MinuteDao = nil
	})
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}

	err := NewAiRecommendStocksService().CreateAiRecommendStocks(&models.AiRecommendStocks{})
	if !errors.Is(err, governance.ErrStrategyRuntimeUnavailable) {
		t.Fatalf("create error = %v, want ErrStrategyRuntimeUnavailable", err)
	}
	assertStrategyTableCount(t, &models.AiRecommendStocks{}, 0)
}

func assertStrategyTableCount(t *testing.T, model any, want int64) {
	t.Helper()
	var got int64
	if err := db.Dao.Model(model).Count(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("table %T count = %d, want %d", model, got, want)
	}
}
