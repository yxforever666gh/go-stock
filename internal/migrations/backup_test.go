package migrations

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBackupUsesOnlineSnapshotAndIncludesWAL(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	destinationPath := filepath.Join(directory, "backup", "stock.db")
	database, err := gorm.Open(sqlite.Open(sourcePath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestDatabase(t, database)
	if err := database.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("PRAGMA wal_autocheckpoint=0").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("CREATE TABLE sample (id INTEGER PRIMARY KEY, value TEXT NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("INSERT INTO sample(value) VALUES (?)", "committed-in-wal").Error; err != nil {
		t.Fatal(err)
	}

	if err := Backup(database, destinationPath); err != nil {
		t.Fatal(err)
	}
	backupDB, err := openSQLiteForVerification(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if sqlDB, sqlErr := backupDB.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}()
	var value string
	if err := backupDB.Raw("SELECT value FROM sample WHERE id = 1").Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	if value != "committed-in-wal" {
		t.Fatalf("backup value = %q", value)
	}
	if got := quickCheck(backupDB); got != "ok" {
		t.Fatalf("quick_check = %q", got)
	}
}

func TestBackupRefusesExistingDestination(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	database, err := gorm.Open(sqlite.Open(filepath.Join(directory, "source.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestDatabase(t, database)
	destination := filepath.Join(directory, "backup.db")
	if err := Backup(database, destination); err != nil {
		t.Fatal(err)
	}
	if err := Backup(database, destination); err == nil {
		t.Fatal("expected an existing destination error")
	}
}

func closeTestDatabase(t *testing.T, database *gorm.DB) {
	t.Helper()
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}
