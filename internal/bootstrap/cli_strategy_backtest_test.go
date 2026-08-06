package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
)

func TestFrozenBacktestRepositoryRejectsPersistenceWhileStrategyPaused(t *testing.T) {
	database := openSchedulerCompatibilityTestDB(t)
	if err := database.AutoMigrate(&models.BacktestRun{}, &models.Trade{}, &models.Metric{}); err != nil {
		t.Fatalf("migrate backtest tables: %v", err)
	}
	if err := governance.InitializeStrategyRuntimeControl(context.Background(), database, "1.5.0"); err != nil {
		t.Fatalf("initialize strategy runtime: %v", err)
	}

	frozenAt := time.Date(2026, 8, 6, 8, 0, 0, 0, time.UTC)
	result := persistence.BacktestResult{Run: models.BacktestRun{
		BacktestID:      "bt-paused-gate",
		StrategyVersion: "1.5.0",
		StartDate:       "2026-08-05",
		EndDate:         "2026-08-06",
		InputHash:       "frozen-input",
		Status:          "completed",
		SummaryJSON:     "{}",
		StartedAt:       frozenAt,
		CompletedAt:     frozenAt,
		FrozenAt:        &frozenAt,
	}}

	repository := &frozenBacktestRepository{main: database}
	err := repository.PersistBacktestResult(context.Background(), result)
	if !errors.Is(err, governance.ErrStrategyPaused) {
		t.Fatalf("persist error = %v, want paused gate", err)
	}
	var count int64
	if err := database.Model(&models.BacktestRun{}).Count(&count).Error; err != nil {
		t.Fatalf("count backtest runs: %v", err)
	}
	if count != 0 {
		t.Fatalf("backtest rows while paused = %d, want 0", count)
	}
}
