package bootstrap

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
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
	main            *gorm.DB
	stockMasterSeed func() ([]models.StockBasic, models.StockMasterRefreshResult, error)
}

func (a *compatibilityServiceAdapter) StockMasterHealth(ctx context.Context) (models.StockMasterHealth, error) {
	return data.EvaluateStockMasterHealth(ctx, a.main, time.Now().UTC())
}

func newCompatibilityServiceOperations(main *gorm.DB, seed ...func() ([]models.StockBasic, models.StockMasterRefreshResult, error)) service.ServiceOperations {
	adapter := &compatibilityServiceAdapter{main: main}
	if len(seed) > 0 {
		adapter.stockMasterSeed = seed[0]
	}
	return service.ServiceOperations{
		AI:     adapter,
		Config: adapter,
		Fund:   adapter,
		Group:  adapter,
		Market: adapter,
		Notify: adapter,
		Stock:  adapter,
	}
}

// productionRuntimeDependencies is the compatibility assembly used until all
// callers consume the injected ports directly. Database handles are supplied
// by the composition root instead of being discovered here.
func productionRuntimeDependencies(storage Storage, seed ...StockMasterSeedLoader) RuntimeDependencies {
	var seedLoaders []func() ([]models.StockBasic, models.StockMasterRefreshResult, error)
	if len(seed) > 0 && seed[0] != nil {
		seedLoaders = append(seedLoaders, seed[0])
	}
	return RuntimeDependencies{
		Storage:  storage,
		Services: newCompatibilityServiceDependenciesWithOperations(storage, newCompatibilityServiceOperations(storage.Main, seedLoaders...)),
	}
}

// NewProductionServices assembles compatibility-backed services for legacy
// entry points that have not yet moved behind AppRuntime.
func NewProductionServices() (service.AppServices, error) {
	return NewProductionServicesWithStorage(Storage{Main: db.Dao, Minute: db.MinuteDao})
}

// NewProductionServicesWithStorage is the explicit-storage form used by new
// composition-root code and tests. Legacy callers can keep using
// NewProductionServices while they are migrated.
func NewProductionServicesWithStorage(storage Storage) (service.AppServices, error) {
	if err := storage.Validate(); err != nil {
		return service.AppServices{}, err
	}
	return service.NewAppServicesWithDependencies(productionRuntimeDependencies(storage).Services)
}
