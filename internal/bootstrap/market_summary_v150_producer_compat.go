package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"go-stock/backend/data"
	"go-stock/backend/models"
	"go-stock/backend/recommendation"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

var errMarketSummaryV150CompatibilityProducer = errors.New("invalid V1.5 compatibility producer")

// marketSummaryV150CompatibilityProducer is the temporary composition bridge
// from the typed application use case to the frozen recommendation Runner.
// Discovery may read provider/cache data, but Runner remains the only owner of
// decision construction and the publisher remains the only production writer.
type marketSummaryV150CompatibilityProducer struct {
	main      *gorm.DB
	clock     recommendation.Clock
	publisher recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult]
}

func newMarketSummaryV150CompatibilityProducer(
	main *gorm.DB,
	clock recommendation.Clock,
	publisher recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult],
) *marketSummaryV150CompatibilityProducer {
	return &marketSummaryV150CompatibilityProducer{main: main, clock: clock, publisher: publisher}
}

func (p *marketSummaryV150CompatibilityProducer) Produce(
	ctx context.Context,
	request service.MarketSummaryV150ProductionRequest,
) (*service.MarketSummaryV150ProductionResult, error) {
	if err := validateMarketSummaryV150CompatibilityProduction(p, ctx, request); err != nil {
		return nil, err
	}

	assembler := &marketSummaryV150CompatibilityRunAssembler{
		main: p.main, clock: p.clock, request: request,
	}
	publisher := &marketSummaryV150CapturingPublisher{delegate: p.publisher}
	receipt, err := newMarketSummaryV150RunnerExecutor(assembler, publisher).Run(ctx)
	if err != nil {
		return nil, err
	}

	run, providerName, modelName, calls := publisher.published()
	if calls != 1 || run == nil {
		return nil, fmt.Errorf(
			"%w: runner publication count=%d runCaptured=%t",
			errMarketSummaryV150CompatibilityProducer,
			calls,
			run != nil,
		)
	}
	readResult, ok := assembler.readSetResult()
	if !ok {
		return nil, fmt.Errorf("%w: runner input result is unavailable", errMarketSummaryV150CompatibilityProducer)
	}
	if strings.TrimSpace(run.RunContext.RunID) == "" || run.RunContext.StrategyVersion != readResult.ReadSet.Prepared.RunContext.StrategyVersion {
		return nil, fmt.Errorf("%w: published run identity does not match its frozen input", errMarketSummaryV150CompatibilityProducer)
	}

	return &service.MarketSummaryV150ProductionResult{
		RunID:                  strings.TrimSpace(run.RunContext.RunID),
		StrategyVersion:        strings.TrimSpace(run.RunContext.StrategyVersion),
		ReportText:             data.RenderMarketSummaryV150Report(run),
		ProviderName:           strings.TrimSpace(providerName),
		ModelName:              strings.TrimSpace(modelName),
		CandidateCount:         len(run.Candidates),
		VerifiedCandidateCount: readResult.VerifiedCandidateCount,
		ProductionCount:        len(run.Production),
		NoTradeReason:          strings.TrimSpace(run.NoTradeReason),
		RouteLog:               marketSummaryV150ServiceRouteLog(readResult.RouteLog, run),
		SaveResult:             receipt,
	}, nil
}

func validateMarketSummaryV150CompatibilityProduction(
	producer *marketSummaryV150CompatibilityProducer,
	ctx context.Context,
	request service.MarketSummaryV150ProductionRequest,
) error {
	if producer == nil {
		return fmt.Errorf("%w: producer is nil", errMarketSummaryV150CompatibilityProducer)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", errMarketSummaryV150CompatibilityProducer)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if producer.main == nil {
		return fmt.Errorf("%w: main database is required", errMarketSummaryV150CompatibilityProducer)
	}
	if marketSummaryV150CompatibilityDependencyIsNil(producer.clock) {
		return fmt.Errorf("%w: clock is required", errMarketSummaryV150CompatibilityProducer)
	}
	if marketSummaryV150CompatibilityDependencyIsNil(producer.publisher) {
		return fmt.Errorf("%w: publisher is required", errMarketSummaryV150CompatibilityProducer)
	}
	if request.StartedAt.IsZero() {
		return fmt.Errorf("%w: startedAt is required", errMarketSummaryV150CompatibilityProducer)
	}
	return nil
}

type marketSummaryV150CompatibilityRunAssembler struct {
	main    *gorm.DB
	clock   recommendation.Clock
	request service.MarketSummaryV150ProductionRequest

	mu        sync.Mutex
	assembled bool
	readSet   data.MarketSummaryV150RunnerReadSetResult
}

func (a *marketSummaryV150CompatibilityRunAssembler) Assemble(
	ctx context.Context,
) (marketSummaryV150RunAssembly, error) {
	a.mu.Lock()
	if a.assembled {
		a.mu.Unlock()
		return marketSummaryV150RunAssembly{}, fmt.Errorf("%w: run input was assembled more than once", errMarketSummaryV150CompatibilityProducer)
	}
	a.assembled = true
	a.mu.Unlock()

	openAI := data.NewDeepSeekOpenAi(ctx, a.request.AIConfigID)
	readResult, err := openAI.ProduceMarketSummaryV150RunnerReadSet(
		ctx,
		a.request.StartedAt,
		a.request.Question,
		a.clock,
	)
	if err != nil {
		return marketSummaryV150RunAssembly{}, err
	}
	replayClock, err := data.NewMarketSummaryV150RunnerReplayClock(readResult.ReadSet, a.clock)
	if err != nil {
		return marketSummaryV150RunAssembly{}, err
	}
	components, err := data.NewMarketSummaryV150RunnerComponents(readResult.ReadSet, a.main)
	if err != nil {
		return marketSummaryV150RunAssembly{}, err
	}

	a.mu.Lock()
	a.readSet = readResult
	a.mu.Unlock()
	return marketSummaryV150RunAssembly{
		Pipeline: components.Pipeline,
		Ports: recommendation.PipelinePorts{
			Clock:         replayClock,
			Market:        components.Market,
			Candidates:    components.Candidates,
			Evidence:      components.Evidence,
			EventVerifier: &marketSummaryEventVerifierCompatibilityAdapter{openAI: openAI},
			FinalQuotes:   components.FinalQuotes,
			Portfolio:     components.Portfolio,
		},
		ProviderName: readResult.ProviderName,
		ModelName:    readResult.ModelName,
	}, nil
}

func (a *marketSummaryV150CompatibilityRunAssembler) readSetResult() (data.MarketSummaryV150RunnerReadSetResult, bool) {
	if a == nil {
		return data.MarketSummaryV150RunnerReadSetResult{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.readSet.ReadSet.Prepared == nil {
		return data.MarketSummaryV150RunnerReadSetResult{}, false
	}
	return a.readSet, true
}

// marketSummaryV150CapturingPublisher retains the same typed run pointer that
// crosses the atomic publisher. Persistence may perform its final quota
// reconciliation in place, so report/result projections must be derived from
// this post-publication object rather than the earlier prepared read set.
type marketSummaryV150CapturingPublisher struct {
	delegate recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult]

	mu           sync.Mutex
	calls        int
	run          *data.MarketSummaryV150RunSnapshot
	providerName string
	modelName    string
}

func (p *marketSummaryV150CapturingPublisher) PublishDecision(
	ctx context.Context,
	decision recommendation.FrozenDecision,
	providerName string,
	modelName string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	if p == nil || marketSummaryV150CompatibilityDependencyIsNil(p.delegate) {
		return nil, fmt.Errorf("%w: capturing publisher delegate is required", errMarketSummaryV150CompatibilityProducer)
	}
	run, ok := decision.(*data.MarketSummaryV150RunSnapshot)
	if !ok || run == nil {
		return nil, fmt.Errorf("%w: runner returned unsupported decision %T", errMarketSummaryV150CompatibilityProducer, decision)
	}

	p.mu.Lock()
	if p.calls != 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: runner attempted to publish more than once", errMarketSummaryV150CompatibilityProducer)
	}
	p.calls = 1
	p.run = run
	p.providerName = providerName
	p.modelName = modelName
	p.mu.Unlock()

	return p.delegate.PublishDecision(ctx, run, providerName, modelName)
}

func (p *marketSummaryV150CapturingPublisher) published() (*data.MarketSummaryV150RunSnapshot, string, string, int) {
	if p == nil {
		return nil, "", "", 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.run, p.providerName, p.modelName, p.calls
}

func marketSummaryV150ServiceRouteLog(
	source *data.MarketSummaryRouteLogSnapshot,
	run *data.MarketSummaryV150RunSnapshot,
) *service.MarketSummaryRouteLog {
	if source == nil {
		return nil
	}
	result := &service.MarketSummaryRouteLog{
		RunSlot:              strings.TrimSpace(source.RunSlot),
		IndicatorCandidateCt: source.IndicatorCandidateCt,
		IndicatorAIInputCt:   source.IndicatorAIInputCt,
		DiscoveryCandidateCt: source.DiscoveryCandidateCt,
		VerifiedCandidateCt:  source.VerifiedCandidateCt,
		Notes:                append([]string(nil), source.Notes...),
	}
	if run == nil {
		return result
	}
	for _, warning := range run.Warnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			result.Notes = append(result.Notes, "health_warning:"+warning)
		}
	}
	result.Notes = append(result.Notes, fmt.Sprintf(
		"v1.5 decision complete production=%d noTrade=%s configHash=%s dataHash=%s modelHash=%s promptHash=%s warnings=%d",
		len(run.Production), strings.TrimSpace(run.NoTradeReason), run.RunContext.ConfigHash,
		run.DataHash, run.ModelHash, run.PromptHash, len(run.Warnings),
	))
	return result
}

func marketSummaryV150CompatibilityDependencyIsNil(value any) bool {
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

var (
	_ service.MarketSummaryV150Producer                                          = (*marketSummaryV150CompatibilityProducer)(nil)
	_ marketSummaryV150RunAssembler                                              = (*marketSummaryV150CompatibilityRunAssembler)(nil)
	_ recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult] = (*marketSummaryV150CapturingPublisher)(nil)
)
