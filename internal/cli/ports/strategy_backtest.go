package ports

import (
	"context"
	"time"

	"go-stock/backend/persistence"
)

// FrozenBacktestRepository is the cache-only storage boundary for the
// strategy-backtest command. The command may replay only inputs returned by
// this repository and never receives a database handle.
type FrozenBacktestRepository interface {
	LoadFrozenStrategyInputs(context.Context, string, time.Time, time.Time) (persistence.FrozenStrategyInputs, error)
	PersistBacktestResult(context.Context, persistence.BacktestResult) error
}
