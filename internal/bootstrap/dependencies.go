package bootstrap

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
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

type legacyApplicationInitializer struct{}

func (legacyApplicationInitializer) EnsureSettings(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return data.EnsureSettingsRecord()
}

func (legacyApplicationInitializer) InitializeSentiment(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data.InitAnalyzeSentiment()
	return nil
}

func productionRuntimeDependencies() RuntimeDependencies {
	marketData := data.NewCompatibilityMarketDataReader(db.Dao, db.MinuteDao)
	return RuntimeDependencies{
		Storage: Storage{Main: db.Dao, Minute: db.MinuteDao},
		Services: service.Dependencies{
			Clock:            systemClock{},
			Initializer:      legacyApplicationInitializer{},
			ExecutionMonitor: data.NewCompatibilityExecutionMonitor(),
			Providers: service.ProviderSet{
				DailyBars:  marketData,
				MinuteBars: marketData,
				Quotes:     marketData,
				Securities: marketData,
				News:       data.NewCompatibilityNewsReader(),
				Ledger:     data.NewCompatibilityPortfolioLedger(db.Dao),
				Legacy:     data.NewCompatibilityLegacyRepository(db.Dao),
			},
		},
	}
}
