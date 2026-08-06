package bootstrap

import (
	"context"
	"errors"

	"go-stock/backend/models"
	"go-stock/backend/recommendation"
)

var errMarketSummaryV150RunAssemblerRequired = errors.New("V1.5 recommendation run assembler is required")

// marketSummaryV150RunAssembly contains every run-scoped dependency needed by
// the frozen recommendation runner. Provider-specific compatibility code owns
// this assembly; the executor owns runner construction and publication.
type marketSummaryV150RunAssembly struct {
	Pipeline recommendation.Pipeline
	Ports    recommendation.PipelinePorts

	ProviderName string
	ModelName    string
}

// marketSummaryV150RunAssembler builds one isolated production attempt. It is
// the only replaceable seam around the executor: callers cannot replace or
// bypass recommendation.Runner.
type marketSummaryV150RunAssembler interface {
	Assemble(context.Context) (marketSummaryV150RunAssembly, error)
}

type marketSummaryV150RunnerExecutor struct {
	assembler marketSummaryV150RunAssembler
	publisher recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult]
}

func newMarketSummaryV150RunnerExecutor(
	assembler marketSummaryV150RunAssembler,
	publisher recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult],
) marketSummaryV150RunnerExecutor {
	return marketSummaryV150RunnerExecutor{assembler: assembler, publisher: publisher}
}

// Run constructs the concrete frozen Runner inside the production executor.
// The assembler cannot supply a substitute runner and the publisher remains
// the existing all-or-nothing V1.5 decision transaction boundary.
func (e marketSummaryV150RunnerExecutor) Run(
	ctx context.Context,
) (*models.MarketSummaryRecommendSaveResult, error) {
	if e.assembler == nil {
		return nil, errMarketSummaryV150RunAssemblerRequired
	}
	assembly, err := e.assembler.Assemble(ctx)
	if err != nil {
		return nil, err
	}
	return recommendation.NewRunner(recommendation.RunnerDependencies[*models.MarketSummaryRecommendSaveResult]{
		Pipeline:      assembly.Pipeline,
		Publisher:     e.publisher,
		PipelinePorts: assembly.Ports,
	}).Run(ctx, assembly.ProviderName, assembly.ModelName)
}
