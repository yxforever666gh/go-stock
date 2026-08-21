package service

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidDependencies = errors.New("invalid service dependencies")

type Clock interface{ Now() time.Time }

type ApplicationInitializer interface {
	EnsureSettings(context.Context) error
	InitializeSentiment(context.Context) error
}

type Dependencies struct {
	Clock       Clock
	Initializer ApplicationInitializer
	AI          AIService
	Config      ConfigService
	Fund        FundService
	Group       GroupService
	Market      MarketService
	Stock       StockService
}

func (d Dependencies) Validate() error {
	if d.Clock == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("clock is required"))
	}
	if d.Initializer == nil {
		return errors.Join(ErrInvalidDependencies, errors.New("application initializer is required"))
	}
	if err := validateServices(d.AI, d.Config, d.Fund, d.Group, d.Market, d.Stock); err != nil {
		return errors.Join(ErrInvalidDependencies, err)
	}
	return nil
}

func operationRequiredError(name string) error { return errors.New(name + " operations are required") }

type RuntimeService struct {
	clock       Clock
	initializer ApplicationInitializer
}

func newRuntimeService(dependencies Dependencies) (RuntimeService, error) {
	if err := dependencies.Validate(); err != nil {
		return RuntimeService{}, err
	}
	return RuntimeService{clock: dependencies.Clock, initializer: dependencies.Initializer}, nil
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
	if s.clock == nil {
		return time.Now()
	}
	return s.clock.Now()
}
