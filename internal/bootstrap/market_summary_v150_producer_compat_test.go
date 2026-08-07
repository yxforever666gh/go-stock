package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/models"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

type mutatingMarketSummaryV150Publisher struct {
	calls   int
	receipt *models.MarketSummaryRecommendSaveResult
}

func (p *mutatingMarketSummaryV150Publisher) PublishDecision(
	_ context.Context,
	decision recommendation.FrozenDecision,
	_ string,
	_ string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	p.calls++
	run, ok := decision.(*data.MarketSummaryV150RunSnapshot)
	if !ok {
		return nil, errors.New("unexpected decision type")
	}
	// The real publisher performs final quota reconciliation in place. Keep
	// this mutation explicit so the capture test proves it observes post-write
	// state instead of an earlier copy.
	run.NoTradeReason = "final_quota_reconciled"
	return p.receipt, nil
}

func TestMarketSummaryV150CapturingPublisherPreservesPostPublicationRunAndPublishesOnce(t *testing.T) {
	receipt := &models.MarketSummaryRecommendSaveResult{SavedCount: 1}
	delegate := &mutatingMarketSummaryV150Publisher{receipt: receipt}
	publisher := &marketSummaryV150CapturingPublisher{delegate: delegate}
	run := &data.MarketSummaryV150RunSnapshot{RunContext: v150.RunContext{
		RunID: "captured-run", StrategyVersion: v150.StrategyVersion, ConfigHash: v150.FixedStrategyV150ConfigHash(),
	}}

	got, err := publisher.PublishDecision(context.Background(), run, "provider", "model")
	if err != nil {
		t.Fatalf("publish decision: %v", err)
	}
	if got != receipt || delegate.calls != 1 {
		t.Fatalf("receipt/calls = %p/%d, want %p/1", got, delegate.calls, receipt)
	}
	captured, providerName, modelName, calls := publisher.published()
	if captured != run || captured.NoTradeReason != "final_quota_reconciled" || providerName != "provider" || modelName != "model" || calls != 1 {
		t.Fatalf("captured publication changed: run=%p reason=%q provider=%q model=%q calls=%d", captured, captured.NoTradeReason, providerName, modelName, calls)
	}

	if _, err := publisher.PublishDecision(context.Background(), run, "provider", "model"); err == nil {
		t.Fatal("second publication was accepted")
	}
	if delegate.calls != 1 {
		t.Fatalf("delegate calls after duplicate = %d, want 1", delegate.calls)
	}
}

func TestMarketSummaryV150CompatibilityProducerFailsClosedBeforeAssembly(t *testing.T) {
	startedAt := time.Date(2026, 8, 7, 9, 40, 0, 0, time.FixedZone("CST", 8*60*60))
	request := service.MarketSummaryV150ProductionRequest{StartedAt: startedAt}
	clock := fixedClock{now: startedAt}
	publisher := &mutatingMarketSummaryV150Publisher{}

	tests := []struct {
		name     string
		producer *marketSummaryV150CompatibilityProducer
		ctx      context.Context
	}{
		{name: "nil producer", ctx: context.Background()},
		{name: "nil context", producer: newMarketSummaryV150CompatibilityProducer(&gormDBPlaceholder, clock, publisher)},
		{name: "missing database", producer: newMarketSummaryV150CompatibilityProducer(nil, clock, publisher), ctx: context.Background()},
		{name: "missing clock", producer: newMarketSummaryV150CompatibilityProducer(&gormDBPlaceholder, nil, publisher), ctx: context.Background()},
		{name: "missing publisher", producer: newMarketSummaryV150CompatibilityProducer(&gormDBPlaceholder, clock, nil), ctx: context.Background()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.producer.Produce(test.ctx, request); !errors.Is(err, errMarketSummaryV150CompatibilityProducer) {
				t.Fatalf("error = %v, want compatibility producer error", err)
			}
		})
	}

	valid := newMarketSummaryV150CompatibilityProducer(&gormDBPlaceholder, clock, publisher)
	if _, err := valid.Produce(context.Background(), service.MarketSummaryV150ProductionRequest{}); !errors.Is(err, errMarketSummaryV150CompatibilityProducer) {
		t.Fatalf("zero startedAt error = %v", err)
	}
}

// Validation only checks pointer identity before any database operation.
var gormDBPlaceholder gorm.DB
