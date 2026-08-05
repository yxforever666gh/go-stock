package data

import (
	"context"

	"go-stock/backend/db"
	"go-stock/backend/governance"

	"gorm.io/gorm"
)

// requireStrategyProductionLive is the single fail-closed guard for strategy
// production mutations owned by the legacy data package. Market/news cache
// writes are intentionally outside this boundary and may continue while the
// strategy is paused.
func requireStrategyProductionLive(ctx context.Context, database *gorm.DB) error {
	if database == nil {
		database = db.Dao
	}
	return governance.RequireStrategyLive(ctx, database, marketSummaryCurrentVersion)
}

func strategyProductionIsLive(database *gorm.DB) bool {
	return requireStrategyProductionLive(context.Background(), database) == nil
}
