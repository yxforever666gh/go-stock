package persistence

import (
	"context"

	"go-stock/backend/execution"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

// GORMOrderEventStore persists immutable order events with the existing
// transactional ledger validation.
type GORMOrderEventStore struct {
	database *gorm.DB
}

func NewGORMOrderEventStore(database *gorm.DB) *GORMOrderEventStore {
	return &GORMOrderEventStore{database: database}
}

func (store *GORMOrderEventStore) AppendOrderEvents(ctx context.Context, runID string, events []models.OrderEvent) error {
	return AppendStrategyOrderEvents(ctx, store.database, runID, events)
}

var _ execution.ImmutableOrderEventStore[models.OrderEvent] = (*GORMOrderEventStore)(nil)
