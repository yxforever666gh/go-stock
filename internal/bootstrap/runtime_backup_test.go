package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"go-stock/internal/migrations"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBackupMainBeforePendingMigrationSnapshotsExistingDatabase(t *testing.T) {
	directory := t.TempDir()
	database, err := gorm.Open(sqlite.Open(filepath.Join(directory, "stock.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	mainSQL, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mainSQL.Close() })
	if err := migrations.MigrateMain(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Where("id = ?", 21).Delete(&migrations.MigrationRecord{}).Error; err != nil {
		t.Fatal(err)
	}
	destination, err := backupMainBeforePendingMigration(database, filepath.Join(directory, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if destination == "" {
		t.Fatal("pending migration did not create a backup")
	}
	if info, statErr := os.Stat(destination); statErr != nil || info.Size() == 0 {
		t.Fatalf("backup info=%v err=%v", info, statErr)
	}
	backup, err := gorm.Open(sqlite.Open(destination), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	backupSQL, err := backup.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backupSQL.Close() })
	var recordCount int64
	if err := backup.Model(&migrations.MigrationRecord{}).Count(&recordCount).Error; err != nil || recordCount != 20 {
		t.Fatalf("backup migration records=%d err=%v", recordCount, err)
	}
	if err := migrations.MigrateMain(database); err != nil {
		t.Fatal(err)
	}
	if repeated, repeatErr := backupMainBeforePendingMigration(database, filepath.Join(directory, "backups")); repeatErr != nil || repeated != "" {
		t.Fatalf("unexpected backup without pending migration path=%q err=%v", repeated, repeatErr)
	}
}

func TestBackupMainBeforePendingMigrationSkipsFreshDatabase(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "fresh.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if destination, backupErr := backupMainBeforePendingMigration(database, t.TempDir()); backupErr != nil || destination != "" {
		t.Fatalf("fresh database backup=%q err=%v", destination, backupErr)
	}
}
