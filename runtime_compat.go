package main

import (
	"go-stock/backend/db"
	"go-stock/internal/migrations"
)

// AutoMigrate remains as a synchronous test compatibility hook. Production
// startup invokes the numbered migration runner directly and never launches
// this function in a goroutine.
func AutoMigrate() {
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		panic(err)
	}
}
