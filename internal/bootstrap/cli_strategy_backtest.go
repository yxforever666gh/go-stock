package bootstrap

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/persistence"
	cliports "go-stock/internal/cli/ports"

	"gorm.io/gorm"
)

type frozenBacktestRepository struct {
	main *gorm.DB
}

var _ cliports.FrozenBacktestRepository = (*frozenBacktestRepository)(nil)

// NewProductionFrozenBacktestRepository binds the command's cache-only
// repository to the process-local main database assembled by CLI bootstrap.
func NewProductionFrozenBacktestRepository() (cliports.FrozenBacktestRepository, error) {
	if db.Dao == nil {
		return nil, errors.New("main database is not initialized")
	}
	return &frozenBacktestRepository{main: db.Dao}, nil
}

func (r *frozenBacktestRepository) LoadFrozenStrategyInputs(ctx context.Context, version string, from, to time.Time) (persistence.FrozenStrategyInputs, error) {
	return persistence.LoadFrozenStrategyInputs(ctx, r.main, version, from, to)
}

func (r *frozenBacktestRepository) PersistBacktestResult(ctx context.Context, result persistence.BacktestResult) error {
	_, err := persistence.PersistBacktestResult(ctx, r.main, result)
	return err
}
