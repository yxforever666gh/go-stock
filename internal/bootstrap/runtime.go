package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"go-stock/backend/db"
	appconfig "go-stock/internal/config"
	"go-stock/internal/migrations"
	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
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
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		return "", err
	}
	if err := (legacyApplicationInitializer{}).EnsureSettings(context.Background()); err != nil {
		return "", err
	}
	installSilentCLIStorageSessions()
	return dbPath, nil
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
