package db

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm/logger"
)

func TestConfigureMainDBEnablesWAL(t *testing.T) {
	database, err := openSQLite(filepath.Join(t.TempDir(), "main.db"), logger.Default.LogMode(logger.Silent))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	if err = configureMainDB(database); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err = database.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode=%q want WAL", mode)
	}
}
