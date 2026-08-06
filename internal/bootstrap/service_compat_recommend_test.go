package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/internal/service"
)

type unsupportedMarketSummaryDecision struct{}

func (unsupportedMarketSummaryDecision) MarketSummaryDecisionVersion() string { return "1.5.0" }

var _ service.MarketSummaryDecisionSnapshot = unsupportedMarketSummaryDecision{}

func TestRecommendationCompatibilityAdapterPersistsAIResponseReports(t *testing.T) {
	database := openSchedulerCompatibilityTestDB(t)
	adapter := compatibilityServiceAdapter{main: database}
	report := &models.AIResponseResult{StockCode: "market-summary", Content: "initial"}

	if err := adapter.CreateAIResponseReport(nil, report); err != nil {
		t.Fatalf("create AI response report: %v", err)
	}
	if report.ID == 0 {
		t.Fatal("created AI response report did not receive an ID")
	}
	report.Content = "updated"
	if err := adapter.PersistAIResponseReport(context.Background(), report); err != nil {
		t.Fatalf("persist AI response report: %v", err)
	}
	var persisted models.AIResponseResult
	if err := database.First(&persisted, report.ID).Error; err != nil {
		t.Fatalf("read AI response report: %v", err)
	}
	if persisted.Content != "updated" {
		t.Fatalf("persisted content = %q", persisted.Content)
	}
}

func TestRecommendationCompatibilityAdapterUsesPersistedStrategyGate(t *testing.T) {
	database := openSchedulerCompatibilityTestDB(t)
	adapter := compatibilityServiceAdapter{main: database}
	ctx := context.Background()
	if err := governance.InitializeStrategyRuntimeControl(ctx, database, "1.5.0"); err != nil {
		t.Fatalf("initialize strategy runtime: %v", err)
	}
	if err := adapter.RequireStrategyLive(ctx, "1.5.0"); !errors.Is(err, governance.ErrStrategyPaused) {
		t.Fatalf("paused gate error = %v", err)
	}
	if _, err := governance.SetStrategyRuntimeMode(ctx, database, governance.StrategyModeLive, "1.5.0", "test", "test"); err != nil {
		t.Fatalf("resume strategy runtime: %v", err)
	}
	if err := adapter.RequireStrategyLive(ctx, "1.5.0"); err != nil {
		t.Fatalf("live gate: %v", err)
	}
}

func TestRecommendationCompatibilityAdapterRejectsUnknownDecisionType(t *testing.T) {
	adapter := compatibilityServiceAdapter{main: openSchedulerCompatibilityTestDB(t)}
	_, err := adapter.PersistMarketSummaryV150Decision(context.Background(), unsupportedMarketSummaryDecision{}, "provider", "model")
	if err == nil || !strings.Contains(err.Error(), "unsupported market summary decision snapshot") {
		t.Fatalf("unsupported decision error = %v", err)
	}
}
