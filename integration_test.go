package main

import (
	"os"
	"testing"

	"go-stock/backend/db"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("GO_STOCK_RUN_INTEGRATION") != "1" {
		t.Skip("skip integration test; set GO_STOCK_RUN_INTEGRATION=1 to enable")
	}
}

func initDatabaseForTest(t *testing.T, path string) {
	t.Helper()
	_ = db.Close()
	db.Dao = nil
	db.MinuteDao = nil
	db.Init(path)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
		db.Dao = nil
		db.MinuteDao = nil
	})
}

func requireDesktopTest(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_DESKTOP_TESTS") != "1" {
		t.Skip("skip desktop test; set RUN_DESKTOP_TESTS=1 to enable")
	}
}
