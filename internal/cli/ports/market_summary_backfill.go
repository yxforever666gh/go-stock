package ports

import (
	"context"
	"time"
)

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
