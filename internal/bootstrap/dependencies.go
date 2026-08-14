package bootstrap

import (
	"errors"
	"time"

	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

var ErrInvalidRuntimeDependencies = errors.New("invalid runtime dependencies")

type Storage struct {
	Main   *gorm.DB
	Minute *gorm.DB
}

func (s Storage) Validate() error {
	if s.Main == nil {
		return errors.Join(ErrInvalidRuntimeDependencies, errors.New("main database is required"))
	}
	if s.Minute == nil {
		return errors.Join(ErrInvalidRuntimeDependencies, errors.New("minute database is required"))
	}
	return nil
}

type RuntimeDependencies struct {
	Storage  Storage
	Services service.Dependencies
}

type StockMasterSeedLoader func() ([]models.StockBasic, models.StockMasterRefreshResult, error)

func AssembleRuntime(cfg appconfig.AppConfig, dependencies RuntimeDependencies) (AppRuntime, error) {
	if err := dependencies.Storage.Validate(); err != nil {
		return AppRuntime{}, err
	}
	services, err := service.NewAppServicesWithDependencies(dependencies.Services)
	if err != nil {
		return AppRuntime{}, errors.Join(ErrInvalidRuntimeDependencies, err)
	}
	return AppRuntime{
		Config:    cfg,
		Storage:   dependencies.Storage,
		Clock:     dependencies.Services.Clock,
		Providers: dependencies.Services.Providers,
		Services:  services,
	}, nil
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
