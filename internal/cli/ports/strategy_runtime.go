package ports

import (
	"context"

	"go-stock/backend/governance"
)

// StrategyRuntimeController is the CLI-owned control plane for the persisted
// strategy production gate. Implementations live in the composition root.
type StrategyRuntimeController interface {
	Status(context.Context, string) governance.StrategyRuntimeStatus
	SetMode(context.Context, string, string, string, string) (governance.StrategyRuntimeStatus, error)
	Close() error
}
