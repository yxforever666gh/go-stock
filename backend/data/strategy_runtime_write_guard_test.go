package data

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
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

func TestYieldReadEndpointsAreSideEffectFreeWhenStrategyIsLive(t *testing.T) {
	prepareYieldReadOnlyTestDB(t, "live-yield-reads")
	fixedNow := time.Date(2026, 8, 6, 16, 0, 0, 0, cnLocation())
	previousNow := timeNow
	timeNow = func() time.Time { return fixedNow }
	t.Cleanup(func() { timeNow = previousNow })

	meta := models.AiRecommendYieldMeta{
		CurrentTradeDate: "2026-08-05",
		RecalcInProgress: true,
		RecalcProgress:   37,
		DownloadTotal:    11,
		DownloadDone:     4,
	}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	staleUpdatedAt := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", meta.ID).UpdateColumn("updated_at", staleUpdatedAt).Error; err != nil {
		t.Fatal(err)
	}
	clearMinuteCoverageStatsCache()

	writes := registerYieldReadWriteCounter(t, db.Dao)
	queryRecalcCalls := atomic.Int64{}
	scopedRecalcCalls := atomic.Int64{}
	previousQueryRecalc := requestAiRecommendYieldRecalcForQueryFn
	previousScopedRecalc := requestAiRecommendYieldScopedRecalcForQueryFn
	requestAiRecommendYieldRecalcForQueryFn = func(bool, string) { queryRecalcCalls.Add(1) }
	requestAiRecommendYieldScopedRecalcForQueryFn = func(bool, string, []string) { scopedRecalcCalls.Add(1) }
	t.Cleanup(func() {
		requestAiRecommendYieldRecalcForQueryFn = previousQueryRecalc
		requestAiRecommendYieldScopedRecalcForQueryFn = previousScopedRecalc
	})

	service := NewAiRecommendStocksService()
	query := &models.AiRecommendStocksQuery{StrategyCohort: marketSummaryVersion150, YieldMode: aiRecommendYieldModeStrict}
	if _, err := service.GetAiRecommendStocksYieldList(query); err != nil {
		t.Fatalf("read yield list: %v", err)
	}
	if _, err := service.GetAiRecommendYieldTaskStatus(); err != nil {
		t.Fatalf("read yield task status: %v", err)
	}
	if _, err := service.GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{StrategyCohort: marketSummaryVersion150}); err != nil {
		t.Fatalf("read yield daily overview: %v", err)
	}

	if got := writes.Load(); got != 0 {
		t.Fatalf("yield reads executed %d database write callback(s)", got)
	}
	if queryRecalcCalls.Load() != 0 || scopedRecalcCalls.Load() != 0 {
		t.Fatalf("yield reads scheduled recalculation: full=%d scoped=%d", queryRecalcCalls.Load(), scopedRecalcCalls.Load())
	}
	var persisted models.AiRecommendYieldMeta
	if err := db.Dao.First(&persisted, meta.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !persisted.RecalcInProgress || persisted.RecalcProgress != 37 || persisted.CurrentTradeDate != "2026-08-05" || !persisted.UpdatedAt.Equal(staleUpdatedAt) {
		t.Fatalf("yield read mutated stale persisted metadata: %+v", persisted)
	}
}

func TestYieldStatusReadDoesNotCreateMissingMetadata(t *testing.T) {
	prepareYieldReadOnlyTestDB(t, "missing-yield-meta-read")
	clearMinuteCoverageStatsCache()
	writes := registerYieldReadWriteCounter(t, db.Dao)

	status, err := NewAiRecommendStocksService().GetAiRecommendYieldTaskStatus()
	if err != nil {
		t.Fatalf("read missing yield task status: %v", err)
	}
	if status == nil || status.RecalcInProgress || status.DownloadInProgress || status.MinuteDownloadTotal != 0 {
		t.Fatalf("missing metadata status = %+v, want zero read model", status)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("missing metadata read executed %d database write callback(s)", got)
	}
	assertStrategyTableCount(t, &models.AiRecommendYieldMeta{}, 0)
}

func prepareYieldReadOnlyTestDB(t *testing.T, name string) {
	t.Helper()
	initDatabaseForTest(t, filepath.Join(t.TempDir(), name+".db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AiRecommendYieldOverride{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendDailyBar{},
	); err != nil {
		t.Fatal(err)
	}
	if err := persistence.MigrateStrategyPersistence(db.Dao); err != nil {
		t.Fatal(err)
	}
	if err := governance.InitializeStrategyRuntimeControl(context.Background(), db.Dao, marketSummaryCurrentVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := governance.SetStrategyRuntimeMode(context.Background(), db.Dao, governance.StrategyModeLive, marketSummaryCurrentVersion, "read-only test", "test"); err != nil {
		t.Fatal(err)
	}
}

func registerYieldReadWriteCounter(t *testing.T, database *gorm.DB) *atomic.Int64 {
	t.Helper()
	counter := &atomic.Int64{}
	name := "test:yield-read-write-counter"
	count := func(*gorm.DB) { counter.Add(1) }
	if err := database.Callback().Create().Before("gorm:create").Register(name, count); err != nil {
		t.Fatal(err)
	}
	if err := database.Callback().Update().Before("gorm:update").Register(name, count); err != nil {
		t.Fatal(err)
	}
	if err := database.Callback().Delete().Before("gorm:delete").Register(name, count); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.Callback().Create().Remove(name)
		database.Callback().Update().Remove(name)
		database.Callback().Delete().Remove(name)
	})
	return counter
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
