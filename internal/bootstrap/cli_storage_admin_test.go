package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"go-stock/backend/db"
	"go-stock/backend/governance"
	"go-stock/internal/migrations"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestInitCLIStorageReadOnlyAllowsMissingMinuteDatabase(t *testing.T) {
	mainPath := filepath.Join(t.TempDir(), "stock.db")
	database, err := gorm.Open(sqlite.Open(mainPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("create main database: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	resolved, err := InitCLIStorageReadOnly("", mainPath)
	if err != nil {
		t.Fatalf("open read-only CLI storage: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.Dao = nil
		db.MinuteDao = nil
	})
	want, err := filepath.Abs(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("resolved path = %q, want %q", resolved, want)
	}
	if db.Dao == nil || db.MinuteDao != nil {
		t.Fatalf("storage handles = main:%v minute:%v", db.Dao != nil, db.MinuteDao != nil)
	}
}

func TestReadOnlyStrategyRuntimeControllerRejectsModeChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stock.db")
	db.Init(path)
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db.Dao, db.MinuteDao = nil, nil
	t.Cleanup(func() {
		_ = db.Close()
		db.Dao, db.MinuteDao = nil, nil
	})

	controller, err := NewProductionReadOnlyStrategyRuntimeController(path)
	if err != nil {
		t.Fatalf("open read-only strategy controller: %v", err)
	}
	status := controller.Status(context.Background(), "1.5.0")
	if !status.Ready || status.Mode != governance.StrategyModePaused {
		t.Fatalf("status = %+v", status)
	}
	if _, err := controller.SetMode(context.Background(), governance.StrategyModeLive, "1.5.0", "must fail", "test"); err == nil {
		t.Fatal("read-only strategy controller accepted a mode change")
	}
}
