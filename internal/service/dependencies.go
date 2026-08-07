package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"go-stock/backend/execution"
	"go-stock/backend/legacy"
	"go-stock/backend/marketdata"
	"go-stock/backend/marketintel"
	"go-stock/backend/models"
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
	Clock                       Clock
	Initializer                 ApplicationInitializer
	Providers                   ProviderSet
	Operations                  ServiceOperations
	RecommendationPublisher     recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult]
	MarketSummaryV150Producer   MarketSummaryV150Producer
	ExecutionMonitor            execution.Monitor
	PortfolioReader             PortfolioAccountReader
	LegacyReader                LegacyRecommendationReader
	CurrentRecommendationReader portfolio.CurrentRecommendationReader
	CurrentStrategyVersion      string
	PortfolioInitialCash        float64
	PortfolioMaxQuoteAge        time.Duration
}

func (d Dependencies) Validate() error {
	if d.Clock == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("clock is required"))
	}
	if d.Initializer == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("application initializer is required"))
	}
	if err := d.Operations.Validate(); err != nil {
		return errors.Join(ErrInvalidDependencies, err)
	}
	if d.RecommendationPublisher == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("recommendation publisher is required"))
	}
	if isNilMarketSummaryV150Producer(d.MarketSummaryV150Producer) {
		return errors.Join(ErrInvalidDependencies, errors.New("market summary V1.5 producer is required"))
	}
	if d.PortfolioReader == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("portfolio reader is required"))
	}
	if d.LegacyReader == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("legacy reader is required"))
	}
	if d.CurrentRecommendationReader == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("current recommendation reader is required"))
	}
	if d.Providers.Quotes == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("quote reader is required"))
	}
	if strings.TrimSpace(d.CurrentStrategyVersion) == "" {
		return errors.Join(ErrInvalidDependencies, errors.New("current strategy version is required"))
	}
	if d.PortfolioInitialCash <= 0 || math.IsNaN(d.PortfolioInitialCash) || math.IsInf(d.PortfolioInitialCash, 0) {
		return errors.Join(ErrInvalidDependencies, errors.New("portfolio initial cash must be positive"))
	}
	if d.PortfolioMaxQuoteAge <= 0 {
		return errors.Join(ErrInvalidDependencies, errors.New("portfolio quote freshness policy is required"))
	}
	return nil
}

func operationRequiredError(name string) error {
	return errors.New(name + " operations are required")
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
