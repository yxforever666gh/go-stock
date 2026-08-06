package bootstrap

import (
	"context"
	"errors"

	"go-stock/backend/db"
	"go-stock/backend/governance"
	cliports "go-stock/internal/cli/ports"

	"gorm.io/gorm"
)

type strategyRuntimeController struct {
	main *gorm.DB
}

var _ cliports.StrategyRuntimeController = (*strategyRuntimeController)(nil)

func NewProductionStrategyRuntimeController(dbPath string) (cliports.StrategyRuntimeController, error) {
	db.Init(dbPath)
	if db.Dao == nil {
		return nil, errors.New("main database is not initialized")
	}
	return &strategyRuntimeController{main: db.Dao}, nil
}

func (c *strategyRuntimeController) Status(ctx context.Context, strategyVersion string) governance.StrategyRuntimeStatus {
	return governance.GetStrategyRuntimeStatus(ctx, c.main, strategyVersion)
}

func (c *strategyRuntimeController) SetMode(ctx context.Context, mode, strategyVersion, reason, changedBy string) (governance.StrategyRuntimeStatus, error) {
	return governance.SetStrategyRuntimeMode(ctx, c.main, mode, strategyVersion, reason, changedBy)
}

func (*strategyRuntimeController) Close() error {
	return db.Close()
}
