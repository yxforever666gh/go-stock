package portfolio

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidRecommendationQuery = errors.New("invalid current recommendation query")

// RecommendationQuery defines the point-in-time cohort window accepted by a
// current recommendation source. Start and End select frozen run trade dates;
// AsOf limits which immutable snapshots and ledger events are observable.
type RecommendationQuery struct {
	StrategyVersion string
	Start           time.Time
	End             time.Time
	AsOf            time.Time
}

// CurrentRecommendationReader is the read-only persistence boundary consumed
// by recommendation use cases. It cannot mutate projections or the ledger.
type CurrentRecommendationReader interface {
	List(context.Context, RecommendationQuery) ([]CurrentRecommendation, error)
}
