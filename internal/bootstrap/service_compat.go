package bootstrap

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

func (a *compatibilityServiceAdapter) CreateTaskRun(ctx context.Context, run *models.CronTaskRun) error {
	if run == nil {
		return errors.New("cron task run is required")
	}
	return a.main.WithContext(ctx).Create(run).Error
}

func (a *compatibilityServiceAdapter) UpdateTaskRun(ctx context.Context, run *models.CronTaskRun) error {
	if run == nil || run.ID == 0 {
		return errors.New("persisted cron task run is required")
	}
	return a.main.WithContext(ctx).
		Model(&models.CronTaskRun{}).
		Where("id = ?", run.ID).
		Updates(map[string]any{
			"status":        run.Status,
			"error_message": run.ErrorMessage,
			"attempts":      run.Attempts,
		}).Error
}

func (a *compatibilityServiceAdapter) LatestAIResponseSince(ctx context.Context, stockName, question string, since time.Time) (models.AIResponseResult, error) {
	var latest models.AIResponseResult
	err := a.main.WithContext(ctx).
		Model(&models.AIResponseResult{}).
		Where("stock_name = ? AND question = ? AND created_at >= ?", stockName, question, since).
		Order("id desc").
		Limit(1).
		Find(&latest).Error
	return latest, err
}

func (a *compatibilityServiceAdapter) EarliestTaskRun(ctx context.Context, taskName string, from, to time.Time, statuses []string) (models.CronTaskRun, error) {
	var earliest models.CronTaskRun
	err := a.main.WithContext(ctx).
		Model(&models.CronTaskRun{}).
		Where("task_name = ? AND triggered_at >= ? AND triggered_at < ? AND status IN ?", taskName, from, to, statuses).
		Order("id asc").
		First(&earliest).Error
	return earliest, err
}

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
		Scheduler: adapter,
		Stock:     adapter,
		System:    adapter,
	}
}

// NewProductionServices assembles compatibility-backed services for legacy
// entry points that have not yet moved behind AppRuntime.
func NewProductionServices() (service.AppServices, error) {
	return service.NewAppServicesWithDependencies(productionRuntimeDependencies().Services)
}
