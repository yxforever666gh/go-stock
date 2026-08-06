package bootstrap

import (
	"path/filepath"
	"testing"

	"go-stock/backend/db"

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
