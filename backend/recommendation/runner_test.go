package recommendation

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/strategy/v150"
)

type runnerReceipt struct{ ID string }

type runnerClock struct{ now time.Time }

func (c *runnerClock) Now() time.Time { return c.now }

type runnerMarket struct{}

func (*runnerMarket) MarketSnapshot(context.Context, MarketRequest) (MarketSnapshot, error) {
	return MarketSnapshot{}, nil
}

type runnerCandidates struct{}

func (*runnerCandidates) Candidates(context.Context, CandidateRequest) ([]v150.Candidate, error) {
	return nil, nil
}

type runnerEvidence struct{}

func (*runnerEvidence) Evidence(context.Context, EvidenceRequest) (EvidenceSnapshot, error) {
	return EvidenceSnapshot{}, nil
}

type runnerEventVerifier struct{}

func (*runnerEventVerifier) Verify(context.Context, EventVerificationCall) (EventVerificationCompletion, error) {
	return EventVerificationCompletion{}, nil
}

type runnerPortfolio struct{}

func (*runnerPortfolio) PortfolioSnapshot(context.Context, PortfolioRequest) (PortfolioSnapshot, error) {
	return PortfolioSnapshot{}, nil
}

type runnerDecision struct{ version string }

func (d *runnerDecision) MarketSummaryDecisionVersion() string { return d.version }

type recordingRunnerPipeline struct {
	calls    int
	context  context.Context
	request  BuildRequest
	ports    PipelinePorts
	decision FrozenDecision
	produced *ProducedDecision
	err      error
}

func (p *recordingRunnerPipeline) Build(
	ctx context.Context,
	request BuildRequest,
	ports PipelinePorts,
) (ProducedDecision, error) {
	p.calls++
	p.context = ctx
	p.request = request
	p.ports = ports
	if p.produced != nil {
		return *p.produced, p.err
	}
	return ProducedDecision{
		Decision: p.decision, StrategyVersion: v150.StrategyVersion,
		ConfigHash: v150.FixedStrategyV150ConfigHash(),
	}, p.err
}

type recordingRunnerPublisher struct {
	calls        int
	context      context.Context
	decision     FrozenDecision
	providerName string
	modelName    string
	receipt      *runnerReceipt
	err          error
}

func (p *recordingRunnerPublisher) PublishDecision(
	ctx context.Context,
	decision FrozenDecision,
	providerName string,
	modelName string,
) (*runnerReceipt, error) {
	p.calls++
	p.context = ctx
	p.decision = decision
	p.providerName = providerName
	p.modelName = modelName
	return p.receipt, p.err
}

func completeRunnerDependencies(
	pipeline Pipeline,
	publisher DecisionPublisher[*runnerReceipt],
) RunnerDependencies[*runnerReceipt] {
	return RunnerDependencies[*runnerReceipt]{
		Pipeline:  pipeline,
		Publisher: publisher,
		PipelinePorts: PipelinePorts{
			Clock:         &runnerClock{now: time.Date(2026, 8, 7, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))},
			Market:        &runnerMarket{},
			Candidates:    &runnerCandidates{},
			Evidence:      &runnerEvidence{},
			EventVerifier: &runnerEventVerifier{},
			Portfolio:     &runnerPortfolio{},
		},
	}
}

func TestRunnerBuildsAndPublishesFrozenV150DecisionExactlyOnce(t *testing.T) {
	t.Parallel()

	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request"), "same-context")
	decision := &runnerDecision{version: v150.StrategyVersion}
	wantReceipt := &runnerReceipt{ID: "receipt-1"}
	wantErr := errors.New("publisher returned receipt with error")
	pipeline := &recordingRunnerPipeline{decision: decision}
	publisher := &recordingRunnerPublisher{receipt: wantReceipt, err: wantErr}
	dependencies := completeRunnerDependencies(pipeline, publisher)
	runner := NewRunner(dependencies)

	gotReceipt, gotErr := runner.Run(ctx, " provider ", " model ")

	if pipeline.calls != 1 {
		t.Fatalf("Pipeline.Build() calls = %d, want 1", pipeline.calls)
	}
	if publisher.calls != 1 {
		t.Fatalf("Publisher.PublishDecision() calls = %d, want 1", publisher.calls)
	}
	if pipeline.context != ctx || publisher.context != ctx {
		t.Fatal("Runner replaced the caller context")
	}
	if pipeline.request.StrategyVersion != v150.StrategyVersion {
		t.Fatalf("Build strategy = %q, want %q", pipeline.request.StrategyVersion, v150.StrategyVersion)
	}
	if pipeline.request.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		t.Fatalf("Build config hash = %q, want frozen V1.5.0 hash", pipeline.request.ConfigHash)
	}
	if pipeline.ports.Clock != dependencies.Clock ||
		pipeline.ports.Market != dependencies.Market ||
		pipeline.ports.Candidates != dependencies.Candidates ||
		pipeline.ports.Evidence != dependencies.Evidence ||
		pipeline.ports.EventVerifier != dependencies.EventVerifier ||
		pipeline.ports.Portfolio != dependencies.Portfolio {
		t.Fatal("Pipeline did not receive the exact injected ports")
	}
	if publisher.decision != decision {
		t.Fatal("Runner replaced the frozen decision")
	}
	if publisher.providerName != " provider " || publisher.modelName != " model " {
		t.Fatalf("publisher identifiers = %q/%q, want exact inputs", publisher.providerName, publisher.modelName)
	}
	if gotReceipt != wantReceipt {
		t.Fatal("Runner copied or dropped the publisher receipt")
	}
	if gotErr != wantErr {
		t.Fatal("Runner wrapped or replaced the publisher error")
	}
}

func TestRunnerBuildFailureDoesNotPublish(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("build failed")
	pipeline := &recordingRunnerPipeline{err: wantErr}
	publisher := &recordingRunnerPublisher{}
	runner := NewRunner(completeRunnerDependencies(pipeline, publisher))

	receipt, err := runner.Run(context.Background(), "provider", "model")

	if receipt != nil {
		t.Fatal("Run() returned a receipt for a failed build")
	}
	if err != wantErr {
		t.Fatal("Run() wrapped or replaced the build error")
	}
	if pipeline.calls != 1 {
		t.Fatalf("Pipeline.Build() calls = %d, want 1", pipeline.calls)
	}
	if publisher.calls != 0 {
		t.Fatalf("Publisher.PublishDecision() calls = %d, want 0", publisher.calls)
	}
}

func TestRunnerRejectsEveryNilDependencyBeforeBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		remove func(*RunnerDependencies[*runnerReceipt])
	}{
		{name: "pipeline", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.Pipeline = nil }},
		{name: "publisher", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.Publisher = nil }},
		{name: "clock", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.Clock = nil }},
		{name: "market", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.Market = nil }},
		{name: "candidates", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.Candidates = nil }},
		{name: "evidence", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.Evidence = nil }},
		{name: "event verifier", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.EventVerifier = nil }},
		{name: "portfolio", remove: func(d *RunnerDependencies[*runnerReceipt]) { d.Portfolio = nil }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pipeline := &recordingRunnerPipeline{decision: &runnerDecision{version: v150.StrategyVersion}}
			publisher := &recordingRunnerPublisher{}
			dependencies := completeRunnerDependencies(pipeline, publisher)
			test.remove(&dependencies)

			_, err := NewRunner(dependencies).Run(context.Background(), "provider", "model")

			if !errors.Is(err, ErrInvalidRunner) {
				t.Fatalf("Run() error = %v, want ErrInvalidRunner", err)
			}
			if pipeline.calls != 0 || publisher.calls != 0 {
				t.Fatalf("invalid runner called pipeline/publisher = %d/%d", pipeline.calls, publisher.calls)
			}
		})
	}
}

func TestRunnerRejectsTypedNilDependencyBeforeBuild(t *testing.T) {
	t.Parallel()

	pipeline := &recordingRunnerPipeline{decision: &runnerDecision{version: v150.StrategyVersion}}
	publisher := &recordingRunnerPublisher{}
	dependencies := completeRunnerDependencies(pipeline, publisher)
	var market *runnerMarket
	dependencies.Market = market

	_, err := NewRunner(dependencies).Run(context.Background(), "provider", "model")

	if !errors.Is(err, ErrInvalidRunner) {
		t.Fatalf("Run() error = %v, want ErrInvalidRunner", err)
	}
	if pipeline.calls != 0 || publisher.calls != 0 {
		t.Fatalf("typed-nil runner called pipeline/publisher = %d/%d", pipeline.calls, publisher.calls)
	}
}

func TestRunnerRejectsMissingOrWrongStrategyDecisionWithoutPublishing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decision FrozenDecision
	}{
		{name: "nil", decision: nil},
		{name: "typed nil", decision: (*runnerDecision)(nil)},
		{name: "wrong version", decision: &runnerDecision{version: "1.4.2"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pipeline := &recordingRunnerPipeline{decision: test.decision}
			publisher := &recordingRunnerPublisher{}
			runner := NewRunner(completeRunnerDependencies(pipeline, publisher))

			_, err := runner.Run(context.Background(), "provider", "model")

			if !errors.Is(err, ErrInvalidRunner) {
				t.Fatalf("Run() error = %v, want ErrInvalidRunner", err)
			}
			if pipeline.calls != 1 || publisher.calls != 0 {
				t.Fatalf("build/publish calls = %d/%d, want 1/0", pipeline.calls, publisher.calls)
			}
		})
	}
}

func TestRunnerRejectsMismatchedProducedIdentityWithoutPublishing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		produced ProducedDecision
	}{
		{
			name: "wrong envelope version",
			produced: ProducedDecision{
				Decision:        &runnerDecision{version: v150.StrategyVersion},
				StrategyVersion: "1.4.2", ConfigHash: v150.FixedStrategyV150ConfigHash(),
			},
		},
		{
			name: "wrong config hash",
			produced: ProducedDecision{
				Decision:        &runnerDecision{version: v150.StrategyVersion},
				StrategyVersion: v150.StrategyVersion, ConfigHash: "forged-config",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pipeline := &recordingRunnerPipeline{produced: &test.produced}
			publisher := &recordingRunnerPublisher{}
			_, err := NewRunner(completeRunnerDependencies(pipeline, publisher)).Run(
				context.Background(), "provider", "model",
			)
			if !errors.Is(err, ErrInvalidRunner) {
				t.Fatalf("Run() error = %v, want ErrInvalidRunner", err)
			}
			if pipeline.calls != 1 || publisher.calls != 0 {
				t.Fatalf("build/publish calls = %d/%d, want 1/0", pipeline.calls, publisher.calls)
			}
		})
	}
}

var (
	_ Clock                             = (*runnerClock)(nil)
	_ MarketPort                        = (*runnerMarket)(nil)
	_ CandidatesPort                    = (*runnerCandidates)(nil)
	_ EvidencePort                      = (*runnerEvidence)(nil)
	_ EventVerifier                     = (*runnerEventVerifier)(nil)
	_ PortfolioPort                     = (*runnerPortfolio)(nil)
	_ Pipeline                          = (*recordingRunnerPipeline)(nil)
	_ DecisionPublisher[*runnerReceipt] = (*recordingRunnerPublisher)(nil)
	_ FrozenDecision                    = (*runnerDecision)(nil)
)
