package bootstrap

import (
	"context"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/internal/migrations"
)

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
