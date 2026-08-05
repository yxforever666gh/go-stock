package bootstrap

import (
	"path/filepath"
	"testing"

	"go-stock/backend/db"
)

func TestInitCLIStorageInstallsImmutableStrategySchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "strategy-migration.db")
	if _, err := InitCLIStorage(filepath.Dir(databasePath), databasePath); err != nil {
		t.Fatalf("InitCLIStorage: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	for _, object := range []struct {
		kind string
		name string
	}{
		{kind: "trigger", name: "immutable_strategy_run_snapshot_update"},
		{kind: "trigger", name: "immutable_strategy_order_event_delete"},
		{kind: "index", name: "idx_strategy_order_run_rule_sequence"},
		{kind: "index", name: "idx_strategy_order_no_trade_run"},
	} {
		var count int64
		if err := db.Dao.Raw(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?",
			object.kind,
			object.name,
		).Scan(&count).Error; err != nil {
			t.Fatalf("query sqlite object %s: %v", object.name, err)
		}
		if count != 1 {
			t.Fatalf("sqlite %s %s count = %d, want 1", object.kind, object.name, count)
		}
	}
}
