package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/internal/migrations"

	"gorm.io/gorm"
)

func initializeAuditStorage(dataDir, dbPath string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = "data"
	}
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "stock.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), os.ModePerm); err != nil {
		return "", err
	}
	db.InitSilent(dbPath)
	if err := backupAuditDatabaseBeforeMigration(db.Dao, filepath.Join(dataDir, "backups")); err != nil {
		_ = db.Close()
		return "", err
	}
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		_ = db.Close()
		return "", err
	}
	if err := data.EnsureSettingsRecord(); err != nil {
		_ = db.Close()
		return "", err
	}
	return dbPath, nil
}

func backupAuditDatabaseBeforeMigration(database *gorm.DB, backupDir string) error {
	status, err := migrations.StatusMain(database)
	if err != nil {
		return fmt.Errorf("inspect main database before migration: %w", err)
	}
	if len(status.Pending) == 0 {
		return nil
	}
	var existingTables int64
	err = database.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> ?`,
		migrations.MigrationRecord{}.TableName()).Scan(&existingTables).Error
	if err != nil {
		return fmt.Errorf("inspect existing main database: %w", err)
	}
	if existingTables == 0 {
		return nil
	}
	if err := os.MkdirAll(backupDir, os.ModePerm); err != nil {
		return err
	}
	name := fmt.Sprintf("pre-migration-main-v%d-to-v%d-%s.db", status.CurrentVersion, status.ExpectedVersion, time.Now().Format("20060102-150405.000000000"))
	if err := migrations.Backup(database, filepath.Join(backupDir, name)); err != nil {
		return fmt.Errorf("backup main database before migration: %w", err)
	}
	return nil
}
