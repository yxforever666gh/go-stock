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

func TestMarketSummaryV150SnapshotFromEnvelopeRestoresFrozenJSON(t *testing.T) {
	envelope, err := service.DecodeMarketSummaryV150DecisionEnvelope(map[string]any{
		"runContext":    map[string]any{"runId": "run-restore", "strategyVersion": "1.5.0"},
		"candidates":    []map[string]any{{"candidate": map[string]any{"symbol": "000001.SZ"}}},
		"production":    []map[string]any{{"symbol": "000001.SZ"}},
		"noTradeReason": "",
	})
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	run, err := marketSummaryV150SnapshotFromEnvelope(envelope)
	if err != nil {
		t.Fatalf("restore envelope: %v", err)
	}
	if run.RunContext.RunID != envelope.RunID || run.RunContext.StrategyVersion != envelope.StrategyVersion || len(run.Candidates) != envelope.CandidateCount || len(run.Production) != envelope.ProductionCount {
		t.Fatalf("restored snapshot does not preserve envelope identity: %#v", run)
	}
}

func TestMarketSummaryV150SnapshotFromEnvelopeRejectsPayloadMismatch(t *testing.T) {
	envelope, err := service.DecodeMarketSummaryV150DecisionEnvelope(map[string]any{
		"runContext": map[string]any{"runId": "run-mismatch", "strategyVersion": "1.5.0"},
	})
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	envelope.ProductionCount = 1
	if run, err := marketSummaryV150SnapshotFromEnvelope(envelope); err == nil || run != nil {
		t.Fatalf("run=%#v err=%v, want envelope mismatch", run, err)
	}
}
