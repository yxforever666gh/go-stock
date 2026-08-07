package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go-stock/backend/data"
	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"
)

type unsupportedMarketSummaryDecision struct{}

func (unsupportedMarketSummaryDecision) MarketSummaryDecisionVersion() string { return "1.5.0" }

var _ recommendation.FrozenDecision = unsupportedMarketSummaryDecision{}

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
	_, err := adapter.PublishDecision(context.Background(), unsupportedMarketSummaryDecision{}, "provider", "model")
	if err == nil || !strings.Contains(err.Error(), "unsupported market summary decision snapshot") {
		t.Fatalf("unsupported decision error = %v", err)
	}
}

func TestMarketSummaryV150SnapshotFromDecisionPreservesTypedSnapshot(t *testing.T) {
	run := validMarketSummaryV150PublicationSnapshot("typed-run")

	restored, err := marketSummaryV150SnapshotFromDecision(run)
	if err != nil {
		t.Fatalf("accept typed snapshot: %v", err)
	}
	if restored != run {
		t.Fatal("typed snapshot was copied or JSON round-tripped before publication")
	}
}

func TestRecommendationCompatibilityAdapterRejectsInvalidTypedSnapshotBeforePersistence(t *testing.T) {
	valid := validMarketSummaryV150PublicationSnapshot("valid-run")
	var nilRun *data.MarketSummaryV150RunSnapshot

	tests := []struct {
		name string
		run  *data.MarketSummaryV150RunSnapshot
		want string
	}{
		{name: "nil", run: nilRun, want: "snapshot is nil"},
		{name: "missing run id", run: func() *data.MarketSummaryV150RunSnapshot {
			copy := *valid
			copy.RunContext.RunID = ""
			return &copy
		}(), want: "run id is required"},
		{name: "wrong strategy version", run: func() *data.MarketSummaryV150RunSnapshot {
			copy := *valid
			copy.RunContext.StrategyVersion = "1.4.2"
			return &copy
		}(), want: "strategy version"},
		{name: "wrong config hash", run: func() *data.MarketSummaryV150RunSnapshot {
			copy := *valid
			copy.RunContext.ConfigHash = "forged-config"
			return &copy
		}(), want: "config hash"},
	}

	// A nil database makes this an explicit ordering test: every malformed
	// typed snapshot must be rejected before the persistence adapter is entered.
	adapter := compatibilityServiceAdapter{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := adapter.PublishDecision(context.Background(), test.run, "provider", "model")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid typed snapshot error = %v, want %q", err, test.want)
			}
		})
	}
}

func validMarketSummaryV150PublicationSnapshot(runID string) *data.MarketSummaryV150RunSnapshot {
	return &data.MarketSummaryV150RunSnapshot{RunContext: v150.RunContext{
		RunID:           runID,
		StrategyVersion: v150.StrategyVersion,
		ConfigHash:      v150.FixedStrategyV150ConfigHash(),
	}}
}
