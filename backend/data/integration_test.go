package data

import (
	"context"
	"os"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/governance"
	"gorm.io/gorm"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("GO_STOCK_RUN_INTEGRATION") != "1" {
		t.Skip("skip integration test; set GO_STOCK_RUN_INTEGRATION=1 to enable")
	}
}

// initDatabaseForTest gives every database-backed test ownership of its SQLite
// handles. This is essential on Windows, where t.TempDir cleanup cannot remove
// a database (or its WAL files) while a prepared-statement/replica pool is open.
func initDatabaseForTest(t *testing.T, path string) {
	t.Helper()
	waitForGlobalYieldRecalcIdle(t, 5*time.Second)
	_ = db.Close()
	db.Dao = nil
	db.MinuteDao = nil
	db.Init(path)
	initMinuteSchemaForTest(t)
	enableStrategyProductionForTest(t, db.Dao)
	previousMutationRecalc := requestAiRecommendYieldScopedRecalcForMutationFn
	requestAiRecommendYieldScopedRecalcForMutationFn = func(bool, string, []string) {}
	t.Cleanup(func() {
		requestAiRecommendYieldScopedRecalcForMutationFn = previousMutationRecalc
		waitForGlobalYieldRecalcIdle(t, 5*time.Second)
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
		db.Dao = nil
		db.MinuteDao = nil
	})
}

// Production minute schema creation belongs to numbered migrations. Tests
// that open storage directly must opt in to the same schema explicitly.
func initMinuteSchemaForTest(t *testing.T) {
	t.Helper()
	if db.MinuteDao == nil {
		t.Fatal("minute database is not initialized")
	}
	if err := db.MinuteDao.Exec(`
CREATE TABLE IF NOT EXISTS minute_bar (
  stock_code TEXT NOT NULL,
  trade_time INTEGER NOT NULL,
  open REAL NOT NULL,
  high REAL NOT NULL,
  low REAL NOT NULL,
  close REAL NOT NULL,
  volume REAL,
  amount REAL,
  source TEXT,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (stock_code, trade_time)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_minute_bar_trade_time ON minute_bar(trade_time);
`).Error; err != nil {
		t.Fatalf("initialize test minute schema: %v", err)
	}
}

func enableStrategyProductionForTest(t *testing.T, database *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	if err := governance.InitializeStrategyRuntimeControl(ctx, database, marketSummaryCurrentVersion); err != nil {
		t.Fatalf("initialize strategy runtime control: %v", err)
	}
	if _, err := governance.SetStrategyRuntimeMode(ctx, database, governance.StrategyModeLive, marketSummaryCurrentVersion, "test fixture", "test"); err != nil {
		t.Fatalf("enable strategy production for test: %v", err)
	}
}

func waitForGlobalYieldRecalcIdle(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		globalAiRecommendYieldRecalcManager.mu.Lock()
		running := globalAiRecommendYieldRecalcManager.running
		globalAiRecommendYieldRecalcManager.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for background yield recalculation to stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
