package bootstrap

import (
	"context"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/internal/migrations"
)

type MinuteCacheMigrationSummary struct {
	LegacyRows   int64
	MinuteDBRows int64
	MigratedRows int64
	StockCount   int64
}

// ConfigureRuntimeEventEmitter keeps the legacy AI event bridge inside the
// composition root while Web and desktop event delivery are being separated.
func ConfigureRuntimeEventEmitter(emitter func(context.Context, string, any)) {
	data.SetRuntimeEventEmitter(emitter)
}

// MigrateCompatibilityStorage is retained only for package-level test hooks
// that historically called AutoMigrate. Production startup uses InitApplication.
func MigrateCompatibilityStorage() error {
	return migrations.MigrateAll(db.Dao, db.MinuteDao)
}

// MigrateLegacyMinuteCache runs the explicit legacy maintenance command. It
// is never called during normal application startup.
func MigrateLegacyMinuteCache(dbPath string) (MinuteCacheMigrationSummary, error) {
	db.Init(dbPath)
	defer func() { _ = db.Close() }()

	summary, err := data.MigrateMinuteCacheToMinuteDB()
	if err != nil {
		return MinuteCacheMigrationSummary{}, err
	}
	if err := data.OptimizeMinuteCacheDB(); err != nil {
		logger.SugaredLogger.Warnf("minute db optimize failed: %v", err)
	}
	return MinuteCacheMigrationSummary{
		LegacyRows:   summary.LegacyRows,
		MinuteDBRows: summary.MinuteDBRows,
		MigratedRows: summary.MigratedRows,
		StockCount:   summary.StockCount,
	}, nil
}
