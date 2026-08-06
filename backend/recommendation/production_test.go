package recommendation

import (
	"context"
	"errors"
	"testing"

	"go-stock/backend/models"
)

type frozenDecisionStub struct {
	version string
}

func (d *frozenDecisionStub) MarketSummaryDecisionVersion() string {
	return d.version
}

type recordingDecisionPublisher struct {
	context      context.Context
	decision     FrozenDecision
	providerName string
	modelName    string
	result       *models.MarketSummaryRecommendSaveResult
	err          error
}

func (p *recordingDecisionPublisher) PublishDecision(
	ctx context.Context,
	decision FrozenDecision,
	providerName string,
	modelName string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	p.context = ctx
	p.decision = decision
	p.providerName = providerName
	p.modelName = modelName
	return p.result, p.err
}

var _ FrozenDecision = (*frozenDecisionStub)(nil)
var _ DecisionPublisher[*models.MarketSummaryRecommendSaveResult] = (*recordingDecisionPublisher)(nil)

func TestProductionServicePublishDecisionPassesArgumentsAndResultThrough(t *testing.T) {
	t.Parallel()

	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request"), "same-context")
	decision := &frozenDecisionStub{version: "1.5.0"}
	wantResult := &models.MarketSummaryRecommendSaveResult{SavedCount: 2}
	publisher := &recordingDecisionPublisher{result: wantResult}
	service := NewProductionService(publisher)

	gotResult, gotErr := service.PublishDecision(ctx, decision, " provider ", " model ")

	if gotErr != nil {
		t.Fatalf("PublishDecision() error = %v, want nil", gotErr)
	}
	if publisher.context != ctx {
		t.Fatal("PublishDecision() replaced the caller context")
	}
	if publisher.decision != decision {
		t.Fatal("PublishDecision() replaced the frozen decision")
	}
	if publisher.providerName != " provider " {
		t.Fatalf("PublishDecision() provider = %q, want exact input", publisher.providerName)
	}
	if publisher.modelName != " model " {
		t.Fatalf("PublishDecision() model = %q, want exact input", publisher.modelName)
	}
	if gotResult != wantResult {
		t.Fatal("PublishDecision() copied or replaced the publisher result")
	}
}

func TestProductionServicePublishDecisionPassesResultAndErrorThroughTogether(t *testing.T) {
	t.Parallel()

	wantResult := &models.MarketSummaryRecommendSaveResult{BlockedCount: 1}
	wantErr := errors.New("publisher failure")
	publisher := &recordingDecisionPublisher{result: wantResult, err: wantErr}
	service := NewProductionService(publisher)

	gotResult, gotErr := service.PublishDecision(
		context.Background(),
		&frozenDecisionStub{version: "1.5.0"},
		"provider",
		"model",
	)

	if gotResult != wantResult {
		t.Fatal("PublishDecision() copied or dropped a non-nil result returned with an error")
	}
	if gotErr != wantErr {
		t.Fatal("PublishDecision() wrapped or replaced the publisher error")
	}
}
