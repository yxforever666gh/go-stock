package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"go-stock/backend/marketintel"
	"go-stock/backend/strategy/v150"
)

var ErrInvalidRunner = errors.New("invalid recommendation runner")

// Clock is the only source of wall-clock time exposed to a recommendation
// pipeline. A pipeline must not read the process clock directly.
type Clock interface {
	Now() time.Time
}

// MarketPort supplies the point-in-time benchmark used by the deterministic
// V1.5.0 decision.
type MarketPort interface {
	MarketSnapshot(context.Context, MarketRequest) (MarketSnapshot, error)
}

type MarketRequest struct {
	AsOf            time.Time
	StrategyVersion string
	ConfigHash      string
}

type MarketSnapshot struct {
	Benchmark               v150.BenchmarkSnapshot `json:"benchmark"`
	Evidence                []marketintel.Evidence `json:"evidence"`
	CompatibilityProjection json.RawMessage        `json:"compatibilityProjection,omitempty"`
}

// CandidatesPort retains the existing typed candidate boundary while giving
// the runner dependency an explicit business name.
type CandidatesPort = CandidateSource

// EvidencePort supplies factual evidence that was available by the run's
// cutoff. EventVerifier remains a separate port because evidence retrieval
// and structured model verification have different failure semantics.
type EvidencePort interface {
	Evidence(context.Context, EvidenceRequest) (EvidenceSnapshot, error)
}

type EvidenceRequest struct {
	RunContext v150.RunContext
	Candidates []v150.ScoredCandidate
	// StatusOnly asks the adapter to return the already-frozen evidence/news
	// availability state without performing candidate verification. Risk-off
	// and empty-top-set runs use this path.
	StatusOnly bool
}

type EvidenceSnapshot struct {
	Status     EvidenceStatus      `json:"status"`
	Warning    string              `json:"warning,omitempty"`
	Candidates []CandidateEvidence `json:"candidates"`
}

type EvidenceStatus string

const (
	EvidenceStatusOK     EvidenceStatus = "ok"
	EvidenceStatusEmpty  EvidenceStatus = "empty"
	EvidenceStatusFailed EvidenceStatus = "failed"
	EvidenceStatusStale  EvidenceStatus = "stale"
)

// CandidateEvidence is the factual, point-in-time evidence admitted for one
// already-ranked candidate. CompatibilityProjection preserves the old display
// snapshot during strangulation; Symbol, VerifiedAt and Items remain the
// strategy-facing source of truth and are checked against that projection.
type CandidateEvidence struct {
	Symbol                  string                 `json:"symbol"`
	VerifiedAt              time.Time              `json:"verifiedAt"`
	Items                   []marketintel.Evidence `json:"items"`
	CompatibilityProjection json.RawMessage        `json:"compatibilityProjection,omitempty"`
}

// FinalQuotePort refreshes only the deterministic top-for-verification set.
// It is deliberately separate from the initial candidate snapshot because a
// long evidence/model stage can make the initial executable price stale.
type FinalQuotePort interface {
	FinalQuotes(context.Context, FinalQuoteRequest) (FinalQuoteSnapshot, error)
}

type FinalQuoteRequest struct {
	RunContext v150.RunContext `json:"runContext"`
	AsOf       time.Time       `json:"asOf"`
	Symbols    []string        `json:"symbols"`
}

type FinalQuoteSnapshot struct {
	Quotes   []FinalQuote `json:"quotes"`
	Warnings []string     `json:"warnings,omitempty"`
}

// FinalQuote is normalized provider output. Has* fields retain the distinction
// between an observed zero and a missing/unparseable provider field. SourceAt
// is the exchange/provider observation time; the pipeline supplies the later
// availability time from its injected Clock after the port returns.
type FinalQuote struct {
	Symbol           string    `json:"symbol"`
	Name             string    `json:"name,omitempty"`
	Price            float64   `json:"price"`
	PreviousClose    float64   `json:"previousClose"`
	Open             float64   `json:"open"`
	Amount           float64   `json:"amount"`
	HasPrice         bool      `json:"hasPrice"`
	HasPreviousClose bool      `json:"hasPreviousClose"`
	HasOpen          bool      `json:"hasOpen"`
	HasAmount        bool      `json:"hasAmount"`
	HasVolume        bool      `json:"hasVolume"`
	SourceAt         time.Time `json:"sourceAt"`
}

// PortfolioPort supplies the frozen portfolio admission state used for final
// shared-quota reconciliation.
type PortfolioPort interface {
	PortfolioSnapshot(context.Context, PortfolioRequest) (PortfolioSnapshot, error)
}

type PortfolioRequest struct {
	RunContext v150.RunContext
}

type PortfolioSnapshot struct {
	State v150.PortfolioState
}

// PipelinePorts is the complete read/verification capability set available to
// the temporary decision-building pipeline. It intentionally excludes the
// publisher, so a build can never perform a partial production write.
type PipelinePorts struct {
	Clock         Clock
	Market        MarketPort
	Candidates    CandidatesPort
	Evidence      EvidencePort
	EventVerifier EventVerifier
	FinalQuotes   FinalQuotePort
	Portfolio     PortfolioPort
}

// BuildRequest pins the frozen strategy identity. Runner constructs this value
// itself; callers cannot route another strategy or replace the V1.5.0 config.
type BuildRequest struct {
	StrategyVersion string
	ConfigHash      string
	ProviderName    string
	ModelName       string
}

// ProducedDecision carries the immutable identity proven by the pipeline in
// addition to the opaque compatibility decision. Runner validates both fields
// before the atomic publisher can observe the decision.
type ProducedDecision struct {
	Decision        FrozenDecision
	StrategyVersion string
	ConfigHash      string
}

// Pipeline is a temporary seam around the existing recommendation build. Its
// replacement can be split into typed stages without changing the atomic
// FrozenDecision/DecisionPublisher boundary.
type Pipeline interface {
	Build(context.Context, BuildRequest, PipelinePorts) (ProducedDecision, error)
}

type RunnerDependencies[Receipt any] struct {
	Pipeline  Pipeline
	Publisher DecisionPublisher[Receipt]
	PipelinePorts
}

// Runner owns one V1.5.0 production attempt: build a frozen decision and hand
// it exactly once to the existing atomic decision publisher.
type Runner[Receipt any] struct {
	dependencies RunnerDependencies[Receipt]
}

func NewRunner[Receipt any](dependencies RunnerDependencies[Receipt]) Runner[Receipt] {
	return Runner[Receipt]{dependencies: dependencies}
}

// Run preserves the caller context and the provider/model identifiers. The
// publisher's receipt and error are returned without wrapping or replacement.
func (r Runner[Receipt]) Run(
	ctx context.Context,
	providerName string,
	modelName string,
) (Receipt, error) {
	var zero Receipt
	if err := validateRunnerDependencies(r.dependencies); err != nil {
		return zero, err
	}

	produced, err := r.dependencies.Pipeline.Build(ctx, BuildRequest{
		StrategyVersion: v150.StrategyVersion,
		ConfigHash:      v150.FixedStrategyV150ConfigHash(),
		ProviderName:    providerName,
		ModelName:       modelName,
	}, r.dependencies.PipelinePorts)
	if err != nil {
		return zero, err
	}
	decision := produced.Decision
	if isNilDependency(decision) {
		return zero, fmt.Errorf("%w: pipeline returned a nil frozen decision", ErrInvalidRunner)
	}
	if produced.StrategyVersion != v150.StrategyVersion || decision.MarketSummaryDecisionVersion() != v150.StrategyVersion {
		return zero, fmt.Errorf(
			"%w: pipeline returned strategy %q/%q, want %q",
			ErrInvalidRunner,
			produced.StrategyVersion,
			decision.MarketSummaryDecisionVersion(),
			v150.StrategyVersion,
		)
	}
	if produced.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		return zero, fmt.Errorf("%w: pipeline returned a non-frozen V1.5.0 config hash", ErrInvalidRunner)
	}

	return r.dependencies.Publisher.PublishDecision(ctx, decision, providerName, modelName)
}

func validateRunnerDependencies[Receipt any](dependencies RunnerDependencies[Receipt]) error {
	checks := []struct {
		name  string
		value any
	}{
		{name: "pipeline", value: dependencies.Pipeline},
		{name: "publisher", value: dependencies.Publisher},
		{name: "clock", value: dependencies.Clock},
		{name: "market", value: dependencies.Market},
		{name: "candidates", value: dependencies.Candidates},
		{name: "evidence", value: dependencies.Evidence},
		{name: "event verifier", value: dependencies.EventVerifier},
		{name: "final quotes", value: dependencies.FinalQuotes},
		{name: "portfolio", value: dependencies.Portfolio},
	}
	for _, check := range checks {
		if isNilDependency(check.value) {
			return fmt.Errorf("%w: %s dependency is nil", ErrInvalidRunner, check.name)
		}
	}
	return nil
}

// Interfaces containing typed nil pointers are non-nil interface values. They
// must still fail closed before a production build or publication is invoked.
func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
