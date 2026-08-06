package ports

import (
	"context"
	"errors"
	"time"
)

// ErrHistoricalRecommendationBackfillDisabled prevents creating recommendations
// for reports that may have fallen in a paused strategy window. Runtime mode
// history is intentionally not inferred or reconstructed from current state.
var ErrHistoricalRecommendationBackfillDisabled = errors.New("historical recommendation backfill is disabled; use --dry-run for audit only")

// MarketSummaryReport is the narrow historical report view needed by the
// explicit recommendation backfill command.
type MarketSummaryReport struct {
	ID           uint
	CreatedAt    time.Time
	ProviderName string
	ModelName    string
	Content      string
}

// MarketSummaryRecommendationBackfill owns the legacy storage query and
// recommendation projection. The command itself has no database dependency.
type MarketSummaryRecommendationBackfill interface {
	ListReports(context.Context, time.Time, time.Time) ([]MarketSummaryReport, error)
	SaveRecommendations(context.Context, MarketSummaryReport) (int, error)
}
