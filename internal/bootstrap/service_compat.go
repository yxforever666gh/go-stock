package bootstrap

import (
	"context"

	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

type compatibilityServiceAdapter struct {
	main *gorm.DB
}

func (a *compatibilityServiceAdapter) StrategyRuntime(ctx context.Context, strategyVersion string) governance.StrategyRuntimeStatus {
	return governance.GetStrategyRuntimeStatus(ctx, a.main, strategyVersion)
}

func (a *compatibilityServiceAdapter) LatestMarketSummary(ctx context.Context) (models.AIResponseResult, error) {
	var latest models.AIResponseResult
	err := a.main.WithContext(ctx).
		Model(&models.AIResponseResult{}).
		Where("stock_name = ? OR stock_code = ?", "市场资讯", "市场资讯").
		Order("id desc").
		Limit(1).
		Find(&latest).Error
	return latest, err
}

func newCompatibilityServiceOperations(main *gorm.DB) service.ServiceOperations {
	adapter := &compatibilityServiceAdapter{main: main}
	return service.ServiceOperations{
		AI:        adapter,
		Config:    adapter,
		Fund:      adapter,
		Group:     adapter,
		History:   adapter,
		Market:    adapter,
		Notify:    adapter,
		Recommend: adapter,
		Stock:     adapter,
		System:    adapter,
	}
}

// NewProductionServices assembles compatibility-backed services for legacy
// entry points that have not yet moved behind AppRuntime.
func NewProductionServices() (service.AppServices, error) {
	return service.NewAppServicesWithDependencies(productionRuntimeDependencies().Services)
}
