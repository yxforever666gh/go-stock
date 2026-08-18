package bootstrap

import (
	"context"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/internal/migrations"
)

// ConfigureRuntimeEventEmitter connects service events to the WebSocket hub at
// the composition root.
func ConfigureRuntimeEventEmitter(emitter func(context.Context, string, any)) {
	data.SetRuntimeEventEmitter(emitter)
}

// MigrateCompatibilityStorage is retained only for package-level test hooks
// that historically called AutoMigrate. Production startup uses InitApplication.
func MigrateCompatibilityStorage() error {
	return migrations.MigrateAll(db.Dao, db.MinuteDao)
}
