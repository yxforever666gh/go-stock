package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/legacy"
	"go-stock/backend/marketdata"
	"go-stock/backend/portfolio"
	appconfig "go-stock/internal/config"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type unavailableQuoteReader struct{}

func (unavailableQuoteReader) Quote(context.Context, string, time.Time) (marketdata.Quote, error) {
	return marketdata.Quote{}, marketdata.ErrObservationUnavailable
}

type emptyCurrentRecommendationReader struct{}

func (emptyCurrentRecommendationReader) List(context.Context, portfolio.RecommendationQuery) ([]portfolio.CurrentRecommendation, error) {
	return []portfolio.CurrentRecommendation{}, nil
}

type recordingInitializer struct {
	settingsCalls  int
	sentimentCalls int
}

func (i *recordingInitializer) EnsureSettings(context.Context) error {
	i.settingsCalls++
	return nil
}

func (i *recordingInitializer) InitializeSentiment(context.Context) error {
	i.sentimentCalls++
	return nil
}

func TestAssembleRuntimeInjectsStorageClockAndLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 6, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	mainDB := &gorm.DB{}
	minuteDB := &gorm.DB{}
	initializer := &recordingInitializer{}
	publisher := &compatibilityServiceAdapter{main: mainDB}

	runtime, err := AssembleRuntime(appconfig.AppConfig{}, RuntimeDependencies{
		Storage: Storage{Main: mainDB, Minute: minuteDB},
		Services: service.Dependencies{
			Clock:                       fixedClock{now: now},
			Initializer:                 initializer,
			Operations:                  newCompatibilityServiceOperations(mainDB),
			RecommendationPublisher:     publisher,
			MarketSummaryV150Producer:   newMarketSummaryV150CompatibilityProducer(mainDB, fixedClock{now: now}, publisher),
			Providers:                   service.ProviderSet{Quotes: unavailableQuoteReader{}},
			PortfolioReader:             portfolio.NewReader(nil),
			LegacyReader:                legacy.NewService(nil, nil),
			CurrentRecommendationReader: emptyCurrentRecommendationReader{},
			CurrentStrategyVersion:      "1.5.0",
			PortfolioInitialCash:        100000,
			PortfolioMaxQuoteAge:        5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("AssembleRuntime: %v", err)
	}
	if runtime.Storage.Main != mainDB || runtime.Storage.Minute != minuteDB {
		t.Fatal("assembled runtime did not retain the injected database handles")
	}
	if got := runtime.Services.Runtime.Now(); !got.Equal(now) {
		t.Fatalf("runtime clock = %s, want %s", got, now)
	}
	if err := runtime.Services.Runtime.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize services: %v", err)
	}
	if initializer.settingsCalls != 1 || initializer.sentimentCalls != 1 {
		t.Fatalf("initializer calls = settings:%d sentiment:%d, want 1/1", initializer.settingsCalls, initializer.sentimentCalls)
	}
}

func TestCompatibilityDependenciesRequireCurrentRecommendationReader(t *testing.T) {
	deps := newCompatibilityServiceDependencies(Storage{Main: &gorm.DB{}, Minute: &gorm.DB{}})
	deps.CurrentRecommendationReader = nil
	if err := deps.Validate(); !errors.Is(err, service.ErrInvalidDependencies) {
		t.Fatalf("error = %v, want ErrInvalidDependencies", err)
	}
}

func TestCompatibilityDependenciesRequireMarketSummaryV150Producer(t *testing.T) {
	deps := newCompatibilityServiceDependencies(Storage{Main: &gorm.DB{}, Minute: &gorm.DB{}})
	deps.MarketSummaryV150Producer = nil
	if err := deps.Validate(); !errors.Is(err, service.ErrInvalidDependencies) {
		t.Fatalf("error = %v, want ErrInvalidDependencies", err)
	}
}

func TestAssembleRuntimeRejectsMissingRequiredDependencies(t *testing.T) {
	tests := []struct {
		name string
		deps RuntimeDependencies
	}{
		{name: "storage"},
		{
			name: "clock",
			deps: RuntimeDependencies{
				Storage: Storage{Main: &gorm.DB{}, Minute: &gorm.DB{}},
				Services: service.Dependencies{
					Initializer: &recordingInitializer{},
					Operations:  newCompatibilityServiceOperations(&gorm.DB{}),
				},
			},
		},
		{
			name: "initializer",
			deps: RuntimeDependencies{
				Storage: Storage{Main: &gorm.DB{}, Minute: &gorm.DB{}},
				Services: service.Dependencies{
					Clock:      fixedClock{},
					Operations: newCompatibilityServiceOperations(&gorm.DB{}),
				},
			},
		},
		{
			name: "operations",
			deps: RuntimeDependencies{
				Storage: Storage{Main: &gorm.DB{}, Minute: &gorm.DB{}},
				Services: service.Dependencies{
					Clock:       fixedClock{},
					Initializer: &recordingInitializer{},
				},
			},
		},
		{
			name: "recommendation publisher",
			deps: RuntimeDependencies{
				Storage: Storage{Main: &gorm.DB{}, Minute: &gorm.DB{}},
				Services: service.Dependencies{
					Clock:       fixedClock{},
					Initializer: &recordingInitializer{},
					Operations:  newCompatibilityServiceOperations(&gorm.DB{}),
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AssembleRuntime(appconfig.AppConfig{}, test.deps)
			if !errors.Is(err, ErrInvalidRuntimeDependencies) {
				t.Fatalf("error = %v, want ErrInvalidRuntimeDependencies", err)
			}
		})
	}
}

func TestCompatibilityDependenciesInjectAllProviderNeutralPorts(t *testing.T) {
	deps := newCompatibilityServiceDependencies(Storage{Main: &gorm.DB{}, Minute: &gorm.DB{}})
	providers := deps.Providers
	if providers.DailyBars == nil || providers.MinuteBars == nil || providers.Quotes == nil || providers.Securities == nil ||
		providers.News == nil || providers.MarketIntel == nil || providers.Ledger == nil || providers.Legacy == nil ||
		deps.RecommendationPublisher == nil || deps.MarketSummaryV150Producer == nil || deps.PortfolioReader == nil || deps.LegacyReader == nil || deps.CurrentRecommendationReader == nil || deps.CurrentStrategyVersion != "1.5.0" ||
		deps.PortfolioInitialCash != 100000 || deps.PortfolioMaxQuoteAge != 5*time.Minute {
		t.Fatalf("compatibility provider set has an unconfigured port: %+v", providers)
	}
	producer, ok := deps.MarketSummaryV150Producer.(*marketSummaryV150CompatibilityProducer)
	if !ok || producer.publisher != deps.RecommendationPublisher {
		t.Fatal("V1.5 typed producer does not share the composition root's atomic publisher")
	}
}
