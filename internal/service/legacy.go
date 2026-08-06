package service

import (
	"context"
	"errors"

	"go-stock/backend/legacy"
)

var ErrLegacyReaderUnavailable = errors.New("legacy recommendation reader is unavailable")

// LegacyRecommendationReader is deliberately read-only. A compatibility
// repository with mutation, repair or execution methods cannot widen this
// application boundary.
type LegacyRecommendationReader interface {
	Find(context.Context, uint) (legacy.Recommendation, error)
	List(context.Context, legacy.Query) ([]legacy.Recommendation, error)
}

type LegacyService struct {
	reader LegacyRecommendationReader
}

func NewLegacyService(reader LegacyRecommendationReader) LegacyService {
	return LegacyService{reader: reader}
}

func (s LegacyService) Find(ctx context.Context, cohort StrategyCohort, id uint) (legacy.Recommendation, error) {
	if err := requireStrategyCohort(cohort, StrategyCohortLegacy); err != nil {
		return legacy.Recommendation{}, err
	}
	if s.reader == nil {
		return legacy.Recommendation{}, ErrLegacyReaderUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.reader.Find(ctx, id)
}

func (s LegacyService) List(ctx context.Context, cohort StrategyCohort, query legacy.Query) ([]legacy.Recommendation, error) {
	if err := requireStrategyCohort(cohort, StrategyCohortLegacy); err != nil {
		return nil, err
	}
	if s.reader == nil {
		return nil, ErrLegacyReaderUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.reader.List(ctx, query)
}
