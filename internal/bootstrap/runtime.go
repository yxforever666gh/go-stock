package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-stock/backend/db"
	appconfig "go-stock/internal/config"
	"go-stock/internal/migrations"
	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

type AppRuntime struct {
	Config   appconfig.AppConfig
	Storage  Storage
	Clock    service.Clock
	Services service.AppServices
}

func InitApplication(cfg appconfig.AppConfig, seed ...StockMasterSeedLoader) (AppRuntime, error) {
	EnsureRuntimeDirs(cfg)
	db.Init(cfg.DB.Path)
	storage := Storage{Main: db.Dao, Minute: db.MinuteDao}
	if _, err := backupMainBeforePendingMigration(storage.Main, cfg.RuntimePath("backups")); err != nil {
		releaseinfo.MarkNotReady(err)
		return AppRuntime{}, err
	}
	if err := migrations.MigrateAll(storage.Main, storage.Minute); err != nil {
		releaseinfo.MarkNotReady(err)
		return AppRuntime{}, err
	}
	runtime, err := AssembleRuntime(cfg, productionRuntimeDependencies(storage, seed...))
	if err != nil {
		releaseinfo.MarkNotReady(err)
		return AppRuntime{}, err
	}
	releaseinfo.MarkStorageReady()
	if err := runtime.Services.Runtime.Initialize(context.Background()); err != nil {
		releaseinfo.MarkNotReady(err)
		return AppRuntime{}, err
	}
	releaseinfo.MarkServicesReady()
	return runtime, nil
}

func InitCLIStorage(dataDir, dbPath string) (string, error) {
	if dataDir == "" {
		dataDir = "data"
	}
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "stock.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), os.ModePerm); err != nil {
		return "", err
	}
	db.Init(dbPath)
	if _, err := backupMainBeforePendingMigration(db.Dao, filepath.Join(dataDir, "backups")); err != nil {
		return "", err
	}
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		return "", err
	}
	if err := (legacyApplicationInitializer{}).EnsureSettings(context.Background()); err != nil {
		return "", err
	}
	installSilentCLIStorageSessions()
	return dbPath, nil
}

func backupMainBeforePendingMigration(database *gorm.DB, backupDir string) (string, error) {
	status, err := migrations.StatusMain(database)
	if err != nil {
		return "", fmt.Errorf("inspect main database before migration: %w", err)
	}
	if len(status.Pending) == 0 {
		return "", nil
	}
	var existingTables int64
	if err := database.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> ?`,
		migrations.MigrationRecord{}.TableName()).Scan(&existingTables).Error; err != nil {
		return "", fmt.Errorf("inspect existing main database: %w", err)
	}
	if existingTables == 0 {
		return "", nil
	}
	if strings.TrimSpace(backupDir) == "" {
		backupDir = filepath.Join("runtime", "backups")
	}
	name := fmt.Sprintf("pre-migration-main-v%d-to-v%d-%s.db", status.CurrentVersion, status.ExpectedVersion, time.Now().Format("20060102-150405.000000000"))
	destination := filepath.Join(backupDir, name)
	if err := migrations.Backup(database, destination); err != nil {
		return "", fmt.Errorf("backup main database before migration: %w", err)
	}
	return destination, nil
}

func EnsureRuntimeDirs(cfg appconfig.AppConfig) {
	if cfg.Runtime.Dir != "" {
		checkDir(cfg.Runtime.Dir)
	}
	checkDir(cfg.RuntimePath("data"))
	checkDir(cfg.RuntimePath("logs"))
	checkDir(cfg.ExportBaseDir())
	dbFilePath := strings.TrimSpace(cfg.DBFilePath())
	if dbFilePath != "" && dbFilePath != ":memory:" {
		dbDir := filepath.Dir(dbFilePath)
		if dbDir != "." && dbDir != "" {
			checkDir(dbDir)
		}
	}
	minuteDBFilePath := strings.TrimSpace(cfg.MinuteDBFilePath())
	if minuteDBFilePath != "" && minuteDBFilePath != ":memory:" {
		minuteDBDir := filepath.Dir(minuteDBFilePath)
		if minuteDBDir != "." && minuteDBDir != "" {
			checkDir(minuteDBDir)
		}
	}
}

func checkDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
	}
}
