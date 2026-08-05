package service

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/legacy"
	"go-stock/backend/marketdata"
	"go-stock/backend/marketintel"
	"go-stock/backend/news"
	"go-stock/backend/portfolio"
	"go-stock/backend/recommendation"
)

var ErrInvalidDependencies = errors.New("invalid service dependencies")

// Clock is the only wall-clock dependency accepted by use cases. Strategy
// inputs continue to carry explicit timestamps and never read this clock.
type Clock interface {
	Now() time.Time
}

// ApplicationInitializer owns startup-only compatibility initialization.
// The production adapter lives in bootstrap, keeping assembly in one place.
type ApplicationInitializer interface {
	EnsureSettings(context.Context) error
	InitializeSentiment(context.Context) error
}

// ProviderSet records the provider-neutral ports available to new use cases.
// Fields remain optional while the strangler migration is in progress; a use
// case must reject a missing port when it starts consuming that port.
type ProviderSet struct {
	DailyBars     marketdata.DailyBarReader
	MinuteBars    marketdata.MinuteBarReader
	Quotes        marketdata.QuoteReader
	Securities    marketdata.SecurityStateReader
	News          news.Reader
	MarketIntel   marketintel.Reader
	EventVerifier recommendation.EventVerifier
	Ledger        portfolio.ReadOnlyLedger
	Legacy        legacy.Repository
}

type Dependencies struct {
	Clock       Clock
	Initializer ApplicationInitializer
	Providers   ProviderSet
}

func (d Dependencies) Validate() error {
	if d.Clock == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("clock is required"))
	}
	if d.Initializer == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("application initializer is required"))
	}
	return nil
}

type RuntimeService struct {
	clock       Clock
	initializer ApplicationInitializer
	providers   ProviderSet
}

func newRuntimeService(dependencies Dependencies) (RuntimeService, error) {
	if err := dependencies.Validate(); err != nil {
		return RuntimeService{}, err
	}
	return RuntimeService{
		clock:       dependencies.Clock,
		initializer: dependencies.Initializer,
		providers:   dependencies.Providers,
	}, nil
}

func (s RuntimeService) Initialize(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.initializer.EnsureSettings(ctx); err != nil {
		return err
	}
	return s.initializer.InitializeSentiment(ctx)
}

func (s RuntimeService) Now() time.Time {
	return s.clock.Now()
}

func (s RuntimeService) Providers() ProviderSet {
	return s.providers
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type noOpApplicationInitializer struct{}

func (noOpApplicationInitializer) EnsureSettings(context.Context) error      { return nil }
func (noOpApplicationInitializer) InitializeSentiment(context.Context) error { return nil }
