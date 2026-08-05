package data

import (
	"os"
	"testing"
	"time"

	"go-stock/backend/db"
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
