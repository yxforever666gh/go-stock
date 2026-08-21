package bootstrap

import (
	"fmt"
	"strings"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

func legacyCommandResult(message string) (string, error) {
	trimmed := strings.TrimSpace(message)
	switch {
	case strings.Contains(trimmed, "不存在"):
		return trimmed, fmt.Errorf("%w: %s", service.ErrNotFound, trimmed)
	case strings.Contains(trimmed, "已经"), strings.Contains(trimmed, "最多"):
		return trimmed, fmt.Errorf("%w: %s", service.ErrConflict, trimmed)
	case strings.Contains(trimmed, "失败"), trimmed == "":
		return trimmed, fmt.Errorf("%w: %s", service.ErrOperationFailed, trimmed)
	default:
		return trimmed, nil
	}
}

type aiConfigAdapter struct {
	main *gorm.DB
}

type fundAdapter struct{}

type groupAdapter struct {
	main *gorm.DB
}

type marketAdapter struct {
	main *gorm.DB
}

type stockAdapter struct {
	main            *gorm.DB
	stockMasterSeed func() ([]models.StockBasic, models.StockMasterRefreshResult, error)
}

func newCompatibilityServiceDependencies(main *gorm.DB, seed ...func() ([]models.StockBasic, models.StockMasterRefreshResult, error)) service.Dependencies {
	stock := &stockAdapter{main: main}
	if len(seed) > 0 {
		stock.stockMasterSeed = seed[0]
	}
	return service.Dependencies{
		Clock:       systemClock{},
		Initializer: legacyApplicationInitializer{},
		AI:          &aiConfigAdapter{main: main},
		Config:      &aiConfigAdapter{main: main},
		Fund:        &fundAdapter{},
		Group:       &groupAdapter{main: main},
		Market:      &marketAdapter{main: main},
		Stock:       stock,
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
		Services: newCompatibilityServiceDependencies(storage.Main, seedLoaders...),
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
