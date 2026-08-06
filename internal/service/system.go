package service

import (
	"context"

	"go-stock/backend/governance"
	"go-stock/backend/models"
)

// SystemService exposes process-level read models without leaking persistence
// details into HTTP delivery code.
type SystemService struct {
	operations SystemOperations
}

func NewSystemService(operations SystemOperations) SystemService {
	return SystemService{operations: operations}
}

func (s SystemService) StrategyRuntime(ctx context.Context, strategyVersion string) governance.StrategyRuntimeStatus {
	return s.operations.StrategyRuntime(ctx, strategyVersion)
}

func (s SystemService) LatestMarketSummary(ctx context.Context) (models.AIResponseResult, error) {
	return s.operations.LatestMarketSummary(ctx)
}
