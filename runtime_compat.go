package main

import "go-stock/internal/bootstrap"

// AutoMigrate remains as a synchronous test compatibility hook. Production
// startup invokes the numbered migration runner directly and never launches
// this function in a goroutine.
func AutoMigrate() {
	if err := bootstrap.MigrateCompatibilityStorage(); err != nil {
		panic(err)
	}
}
