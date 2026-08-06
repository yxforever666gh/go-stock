package bootstrap

import (
	"context"
	"errors"
	"testing"

	cliports "go-stock/internal/cli/ports"
)

func TestHistoricalRecommendationBackfillAdapterRejectsWrites(t *testing.T) {
	adapter := &marketSummaryRecommendationBackfillAdapter{}
	_, err := adapter.SaveRecommendations(context.Background(), cliports.MarketSummaryReport{})
	if !errors.Is(err, cliports.ErrHistoricalRecommendationBackfillDisabled) {
		t.Fatalf("SaveRecommendations() error = %v, want historical-write gate", err)
	}
}
