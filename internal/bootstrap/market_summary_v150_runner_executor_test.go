package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"
)

type marketSummaryV150CompositionDecision struct {
	version string
}

func (d *marketSummaryV150CompositionDecision) MarketSummaryDecisionVersion() string {
	return d.version
}

type marketSummaryV150CompositionPorts struct{}

func (*marketSummaryV150CompositionPorts) Now() time.Time {
	return time.Date(2026, 8, 7, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
}

func (*marketSummaryV150CompositionPorts) MarketSnapshot(
	context.Context,
	recommendation.MarketRequest,
) (recommendation.MarketSnapshot, error) {
	return recommendation.MarketSnapshot{}, nil
}

func (*marketSummaryV150CompositionPorts) Candidates(
	context.Context,
	recommendation.CandidateRequest,
) (recommendation.CandidateBatch, error) {
	return recommendation.CandidateBatch{}, nil
}

func (*marketSummaryV150CompositionPorts) Evidence(
	context.Context,
	recommendation.EvidenceRequest,
) (recommendation.EvidenceSnapshot, error) {
	return recommendation.EvidenceSnapshot{}, nil
}

func (*marketSummaryV150CompositionPorts) Verify(
	context.Context,
	recommendation.EventVerificationCall,
) (recommendation.EventVerificationCompletion, error) {
	return recommendation.EventVerificationCompletion{}, nil
}

func (*marketSummaryV150CompositionPorts) FinalQuotes(
	context.Context,
	recommendation.FinalQuoteRequest,
) (recommendation.FinalQuoteSnapshot, error) {
	return recommendation.FinalQuoteSnapshot{}, nil
}

func (*marketSummaryV150CompositionPorts) PortfolioSnapshot(
	context.Context,
	recommendation.PortfolioRequest,
) (recommendation.PortfolioSnapshot, error) {
	return recommendation.PortfolioSnapshot{}, nil
}

type recordingMarketSummaryV150CompositionAssembler struct {
	calls    int
	context  context.Context
	assembly marketSummaryV150RunAssembly
	err      error
}

func (a *recordingMarketSummaryV150CompositionAssembler) Assemble(
	ctx context.Context,
) (marketSummaryV150RunAssembly, error) {
	a.calls++
	a.context = ctx
	return a.assembly, a.err
}

type recordingMarketSummaryV150CompositionPipeline struct {
	calls    int
	context  context.Context
	request  recommendation.BuildRequest
	ports    recommendation.PipelinePorts
	produced recommendation.ProducedDecision
	err      error
}

func (p *recordingMarketSummaryV150CompositionPipeline) Build(
	ctx context.Context,
	request recommendation.BuildRequest,
	ports recommendation.PipelinePorts,
) (recommendation.ProducedDecision, error) {
	p.calls++
	p.context = ctx
	p.request = request
	p.ports = ports
	return p.produced, p.err
}

type recordingMarketSummaryV150CompositionPublisher struct {
	calls        int
	context      context.Context
	decision     recommendation.FrozenDecision
	providerName string
	modelName    string
	receipt      *models.MarketSummaryRecommendSaveResult
	err          error
}

func (p *recordingMarketSummaryV150CompositionPublisher) PublishDecision(
	ctx context.Context,
	decision recommendation.FrozenDecision,
	providerName string,
	modelName string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	p.calls++
	p.context = ctx
	p.decision = decision
	p.providerName = providerName
	p.modelName = modelName
	return p.receipt, p.err
}

func TestMarketSummaryV150ProductionCompositionExecutesFrozenRunner(t *testing.T) {
	t.Parallel()

	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("run"), "same-context")
	decision := &marketSummaryV150CompositionDecision{version: v150.StrategyVersion}
	ports := &marketSummaryV150CompositionPorts{}
	pipeline := &recordingMarketSummaryV150CompositionPipeline{produced: recommendation.ProducedDecision{
		Decision:        decision,
		StrategyVersion: v150.StrategyVersion,
		ConfigHash:      v150.FixedStrategyV150ConfigHash(),
	}}
	wantReceipt := &models.MarketSummaryRecommendSaveResult{SavedCount: 1}
	wantErr := errors.New("atomic publisher returned its receipt with an error")
	publisher := &recordingMarketSummaryV150CompositionPublisher{receipt: wantReceipt, err: wantErr}
	assembler := &recordingMarketSummaryV150CompositionAssembler{assembly: marketSummaryV150RunAssembly{
		Pipeline: pipeline,
		Ports: recommendation.PipelinePorts{
			Clock: ports, Market: ports, Candidates: ports, Evidence: ports,
			EventVerifier: ports, FinalQuotes: ports, Portfolio: ports,
		},
		ProviderName: " provider ",
		ModelName:    " model ",
	}}

	gotReceipt, gotErr := newMarketSummaryV150RunnerExecutor(assembler, publisher).Run(ctx)

	if assembler.calls != 1 || pipeline.calls != 1 || publisher.calls != 1 {
		t.Fatalf(
			"assemble/build/publish calls = %d/%d/%d, want 1/1/1",
			assembler.calls, pipeline.calls, publisher.calls,
		)
	}
	if assembler.context != ctx || pipeline.context != ctx || publisher.context != ctx {
		t.Fatal("production composition replaced the caller context")
	}
	if pipeline.request.StrategyVersion != v150.StrategyVersion {
		t.Fatalf("build strategy = %q, want %q", pipeline.request.StrategyVersion, v150.StrategyVersion)
	}
	if pipeline.request.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		t.Fatalf("build config hash = %q, want frozen V1.5.0 hash", pipeline.request.ConfigHash)
	}
	if pipeline.request.ProviderName != " provider " || pipeline.request.ModelName != " model " {
		t.Fatalf(
			"build provider/model = %q/%q, want exact assembly values",
			pipeline.request.ProviderName, pipeline.request.ModelName,
		)
	}
	if pipeline.ports.Clock != assembler.assembly.Ports.Clock ||
		pipeline.ports.Market != assembler.assembly.Ports.Market ||
		pipeline.ports.Candidates != assembler.assembly.Ports.Candidates ||
		pipeline.ports.Evidence != assembler.assembly.Ports.Evidence ||
		pipeline.ports.EventVerifier != assembler.assembly.Ports.EventVerifier ||
		pipeline.ports.FinalQuotes != assembler.assembly.Ports.FinalQuotes ||
		pipeline.ports.Portfolio != assembler.assembly.Ports.Portfolio {
		t.Fatal("production composition replaced an assembled pipeline port")
	}
	if publisher.decision != decision {
		t.Fatal("atomic publisher did not receive the pipeline's frozen decision")
	}
	if publisher.providerName != " provider " || publisher.modelName != " model " {
		t.Fatalf(
			"publisher provider/model = %q/%q, want exact assembly values",
			publisher.providerName, publisher.modelName,
		)
	}
	if gotReceipt != wantReceipt {
		t.Fatal("production composition copied or dropped the publisher receipt")
	}
	if gotErr != wantErr {
		t.Fatal("production composition wrapped or replaced the publisher error")
	}
}

func TestMarketSummaryV150ProductionCompositionRejectsWrongConfigBeforePublish(t *testing.T) {
	t.Parallel()

	ports := &marketSummaryV150CompositionPorts{}
	pipeline := &recordingMarketSummaryV150CompositionPipeline{produced: recommendation.ProducedDecision{
		Decision:        &marketSummaryV150CompositionDecision{version: v150.StrategyVersion},
		StrategyVersion: v150.StrategyVersion,
		ConfigHash:      "forged-config-hash",
	}}
	publisher := &recordingMarketSummaryV150CompositionPublisher{}
	assembler := &recordingMarketSummaryV150CompositionAssembler{assembly: marketSummaryV150RunAssembly{
		Pipeline: pipeline,
		Ports: recommendation.PipelinePorts{
			Clock: ports, Market: ports, Candidates: ports, Evidence: ports,
			EventVerifier: ports, FinalQuotes: ports, Portfolio: ports,
		},
		ProviderName: "provider",
		ModelName:    "model",
	}}

	receipt, err := newMarketSummaryV150RunnerExecutor(assembler, publisher).Run(context.Background())

	if receipt != nil {
		t.Fatal("invalid frozen identity returned a publication receipt")
	}
	if !errors.Is(err, recommendation.ErrInvalidRunner) {
		t.Fatalf("Run() error = %v, want recommendation.ErrInvalidRunner", err)
	}
	if assembler.calls != 1 || pipeline.calls != 1 || publisher.calls != 0 {
		t.Fatalf(
			"assemble/build/publish calls = %d/%d/%d, want 1/1/0",
			assembler.calls, pipeline.calls, publisher.calls,
		)
	}
}

var (
	_ recommendation.Clock                                                       = (*marketSummaryV150CompositionPorts)(nil)
	_ recommendation.MarketPort                                                  = (*marketSummaryV150CompositionPorts)(nil)
	_ recommendation.CandidatesPort                                              = (*marketSummaryV150CompositionPorts)(nil)
	_ recommendation.EvidencePort                                                = (*marketSummaryV150CompositionPorts)(nil)
	_ recommendation.EventVerifier                                               = (*marketSummaryV150CompositionPorts)(nil)
	_ recommendation.FinalQuotePort                                              = (*marketSummaryV150CompositionPorts)(nil)
	_ recommendation.PortfolioPort                                               = (*marketSummaryV150CompositionPorts)(nil)
	_ marketSummaryV150RunAssembler                                              = (*recordingMarketSummaryV150CompositionAssembler)(nil)
	_ recommendation.Pipeline                                                    = (*recordingMarketSummaryV150CompositionPipeline)(nil)
	_ recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult] = (*recordingMarketSummaryV150CompositionPublisher)(nil)
	_ recommendation.FrozenDecision                                              = (*marketSummaryV150CompositionDecision)(nil)
)
