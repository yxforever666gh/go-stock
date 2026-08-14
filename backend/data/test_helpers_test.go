package data

import (
	"os"
	"testing"

	"go-stock/backend/db"
	"go-stock/internal/migrations"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("GO_STOCK_RUN_INTEGRATION") != "1" {
		t.Skip("integration test disabled")
	}
}

func initDatabaseForTest(t *testing.T, path string) {
	t.Helper()
	_ = db.Close()
	db.Init(path)
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
}
