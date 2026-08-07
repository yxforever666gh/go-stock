package data

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/recommendation"
)

var ErrMarketSummaryV150RunnerReadSetProducer = errors.New("invalid V1.5 runner read-set production")

// MarketSummaryV150RunnerReadSetProducerInput owns one factual, run-scoped
// read. OpenAI supplies provider identity only; this stage never invokes the
// event model. Context and Clock are explicit so cancellation and point-in-time
// provenance cannot fall back to process globals.
type MarketSummaryV150RunnerReadSetProducerInput struct {
	Context   context.Context
	OpenAI    *OpenAi
	StartedAt time.Time
	Question  string
	Clock     recommendation.Clock
}

// MarketSummaryV150RunnerDiscoveryWindow is the display/diagnostic projection
// of the event-time window used by discovery.
type MarketSummaryV150RunnerDiscoveryWindow struct {
	RunSlot string    `json:"runSlot"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
}

// MarketSummaryV150RunnerReadSetResult contains only frozen pre-decision data
// and typed delivery metadata. It deliberately has no event-model response,
// final quote, portfolio, production plan, persistence receipt, or RawJSON.
type MarketSummaryV150RunnerReadSetResult struct {
	ReadSet MarketSummaryV150RunnerReadSet `json:"readSet"`

	DisplayQuestion        string                                 `json:"displayQuestion"`
	RouteLog               *MarketSummaryRouteLogSnapshot         `json:"routeLog"`
	DiscoveryWindow        MarketSummaryV150RunnerDiscoveryWindow `json:"discoveryWindow"`
	ProviderName           string                                 `json:"providerName"`
	ModelName              string                                 `json:"modelName"`
	AIProtocol             string                                 `json:"aiProtocol"`
	PreparedAsOf           time.Time                              `json:"preparedAsOf"`
	PreparedDataCutoffAt   time.Time                              `json:"preparedDataCutoffAt"`
	CandidateCount         int                                    `json:"candidateCount"`
	VerificationSetCount   int                                    `json:"verificationSetCount"`
	VerifiedCandidateCount int                                    `json:"verifiedCandidateCount"`
}

type marketSummaryV150RunnerReadSetStages interface {
	Discovery(
		context.Context,
		string,
		marketSummaryRouteBudget,
		*marketSummaryRouteLog,
		time.Time,
		recommendation.Clock,
	) (marketSummaryDiscoveryInput, []models.LongTigerRankData, marketSummaryTimeWindow, error)
	Prepare(
		context.Context,
		marketSummaryDiscoveryInput,
		time.Time,
		*marketSummaryRouteLog,
		recommendation.Clock,
	) (*MarketSummaryV150RunSnapshot, error)
	Verify(
		context.Context,
		*OpenAi,
		marketSummaryDiscoveryInput,
		*marketSummaryDiscoveryResult,
		[]models.LongTigerRankData,
		marketSummaryRouteBudget,
		*marketSummaryRouteLog,
		recommendation.Clock,
	) ([]marketSummaryVerifiedCandidate, error)
}

type marketSummaryV150RunnerReadSetProductionStages struct{}

func (marketSummaryV150RunnerReadSetProductionStages) Discovery(
	ctx context.Context,
	question string,
	budget marketSummaryRouteBudget,
	logState *marketSummaryRouteLog,
	startedAt time.Time,
	clock recommendation.Clock,
) (marketSummaryDiscoveryInput, []models.LongTigerRankData, marketSummaryTimeWindow, error) {
	discoveryAt, err := marketSummaryV150RunnerClockAt(clock, "discovery start", startedAt)
	if err != nil {
		return marketSummaryDiscoveryInput{}, nil, marketSummaryTimeWindow{}, err
	}
	return buildMarketSummaryDiscoveryInputScoped(ctx, question, budget, logState, discoveryAt, clock)
}

func (marketSummaryV150RunnerReadSetProductionStages) Prepare(
	ctx context.Context,
	input marketSummaryDiscoveryInput,
	startedAt time.Time,
	logState *marketSummaryRouteLog,
	clock recommendation.Clock,
) (*MarketSummaryV150RunSnapshot, error) {
	return prepareMarketSummaryV150ForPhaseScoped(
		ctx, input, startedAt, logState, clock, loadMarketSummaryV150DailyBarsWithCache,
	)
}

func (marketSummaryV150RunnerReadSetProductionStages) Verify(
	ctx context.Context,
	_ *OpenAi,
	input marketSummaryDiscoveryInput,
	discovery *marketSummaryDiscoveryResult,
	longTigerRaw []models.LongTigerRankData,
	budget marketSummaryRouteBudget,
	logState *marketSummaryRouteLog,
	clock recommendation.Clock,
) ([]marketSummaryVerifiedCandidate, error) {
	return verifyMarketSummaryCandidatesScoped(ctx, input, discovery, longTigerRaw, budget, logState, clock)
}

// MarketSummaryV150RunnerReadSetProducer stops immediately after factual
// top-set verification. Its only production implementation is deliberately
// private; the interface exists to make stage ordering and cancellation
// independently testable without network access.
type MarketSummaryV150RunnerReadSetProducer struct {
	stages marketSummaryV150RunnerReadSetStages
}

func NewMarketSummaryV150RunnerReadSetProducer() MarketSummaryV150RunnerReadSetProducer {
	return MarketSummaryV150RunnerReadSetProducer{stages: marketSummaryV150RunnerReadSetProductionStages{}}
}

func newMarketSummaryV150RunnerReadSetProducerWithStages(
	stages marketSummaryV150RunnerReadSetStages,
) MarketSummaryV150RunnerReadSetProducer {
	return MarketSummaryV150RunnerReadSetProducer{stages: stages}
}

// ProduceMarketSummaryV150RunnerReadSet is the function-style composition
// entry point. The method on OpenAi below is provided for the existing provider
// compatibility adapter; both execute the same producer.
func ProduceMarketSummaryV150RunnerReadSet(
	input MarketSummaryV150RunnerReadSetProducerInput,
) (MarketSummaryV150RunnerReadSetResult, error) {
	return NewMarketSummaryV150RunnerReadSetProducer().Produce(input)
}

func (o *OpenAi) ProduceMarketSummaryV150RunnerReadSet(
	ctx context.Context,
	startedAt time.Time,
	question string,
	clock recommendation.Clock,
) (MarketSummaryV150RunnerReadSetResult, error) {
	return ProduceMarketSummaryV150RunnerReadSet(MarketSummaryV150RunnerReadSetProducerInput{
		Context: ctx, OpenAI: o, StartedAt: startedAt, Question: question, Clock: clock,
	})
}

func (p MarketSummaryV150RunnerReadSetProducer) Produce(
	input MarketSummaryV150RunnerReadSetProducerInput,
) (MarketSummaryV150RunnerReadSetResult, error) {
	if err := validateMarketSummaryV150RunnerReadSetProducerInput(p.stages, input); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	if err := input.Context.Err(); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}

	displayQuestion := NormalizeMarketSummaryQuestion(input.Question)
	if strings.TrimSpace(displayQuestion) == "" {
		displayQuestion = DefaultMarketSummaryQuestion
	}
	budget := marketSummaryV150RunnerReadSetBudget()
	logState := &marketSummaryRouteLog{
		Version: marketSummaryVersion150, StartedAt: input.StartedAt.Format(time.DateTime), Budget: budget,
		PerCategoryCalls: map[string]int{},
	}
	discoveryInput, longTigerRaw, window, err := p.stages.Discovery(
		input.Context, displayQuestion, budget, logState, input.StartedAt, input.Clock,
	)
	if err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	if err := input.Context.Err(); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}

	prepared, err := p.stages.Prepare(input.Context, discoveryInput, input.StartedAt, logState, input.Clock)
	if err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	if err := input.Context.Err(); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	if prepared == nil {
		return MarketSummaryV150RunnerReadSetResult{}, fmt.Errorf("%w: deterministic preparation returned nil", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	routes := buildMarketSummaryV150VerificationRoutes(prepared)
	logState.DiscoveryCandidateCt = len(prepared.Candidates)
	logState.addNote(
		"v1.5 backend rank complete candidates=%d eligibleTop18=%d",
		len(prepared.Candidates), len(routes),
	)

	verified := []marketSummaryVerifiedCandidate(nil)
	if prepared.Regime.NoTrade {
		logState.addNote("v1.5 risk_off: factual candidate verification skipped")
	} else if len(routes) > 0 {
		verificationDiscovery := &marketSummaryDiscoveryResult{CandidateStocks: routes}
		verified, err = p.stages.Verify(
			input.Context, input.OpenAI, discoveryInput, verificationDiscovery, longTigerRaw,
			budget, logState, input.Clock,
		)
		if err != nil {
			return MarketSummaryV150RunnerReadSetResult{}, err
		}
	}
	if err := input.Context.Err(); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	logState.VerifiedCandidateCt = len(verified)

	preparedCopy, err := cloneMarketSummaryV150RunnerPrepared(prepared)
	if err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	readSet := MarketSummaryV150RunnerReadSet{
		Prepared:        preparedCopy,
		EvidenceStatus:  marketSummaryV150RunnerEvidenceStatus(logState.NewsWindowStatus),
		EvidenceWarning: strings.TrimSpace(logState.NewsWindowWarning),
		Verified:        append([]MarketSummaryVerifiedCandidateSnapshot(nil), verified...),
		AIProtocol:      NormalizeAIAPIProtocol(input.OpenAI.ApiProtocol),
	}
	if err := validateMarketSummaryV150RunnerReadSet(preparedCopy, readSet); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	if err := input.Context.Err(); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}

	routeLog := cloneMarketSummaryV150RunnerRouteLog(logState)
	result := MarketSummaryV150RunnerReadSetResult{
		ReadSet: readSet, DisplayQuestion: displayQuestion, RouteLog: routeLog,
		DiscoveryWindow: MarketSummaryV150RunnerDiscoveryWindow{
			RunSlot: string(window.Slot), Start: window.Start, End: window.End,
		},
		ProviderName:           strings.TrimSpace(input.OpenAI.ProviderName),
		ModelName:              strings.TrimSpace(input.OpenAI.Model),
		AIProtocol:             readSet.AIProtocol,
		PreparedAsOf:           preparedCopy.RunContext.AsOf,
		PreparedDataCutoffAt:   preparedCopy.RunContext.DataCutoffAt,
		CandidateCount:         len(preparedCopy.Candidates),
		VerificationSetCount:   len(preparedCopy.VerificationSymbols),
		VerifiedCandidateCount: len(verified),
	}
	if err := input.Context.Err(); err != nil {
		return MarketSummaryV150RunnerReadSetResult{}, err
	}
	return result, nil
}

func validateMarketSummaryV150RunnerReadSetProducerInput(
	stages marketSummaryV150RunnerReadSetStages,
	input MarketSummaryV150RunnerReadSetProducerInput,
) error {
	if marketSummaryV150RunnerNilInterface(stages) {
		return fmt.Errorf("%w: producer stages are required", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	if input.Context == nil {
		return fmt.Errorf("%w: context is required", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	if input.OpenAI == nil {
		return fmt.Errorf("%w: OpenAI instance is required", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	if input.StartedAt.IsZero() {
		return fmt.Errorf("%w: startedAt is required", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	if marketSummaryV150RunnerNilInterface(input.Clock) {
		return fmt.Errorf("%w: clock is required", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	return nil
}

func marketSummaryV150RunnerReadSetBudget() marketSummaryRouteBudget {
	// This literal is part of the frozen 1.5.0 producer contract. It must not
	// inherit a later app version's mutable default budget.
	return marketSummaryRouteBudget{
		TotalCallLimit: 40, DiscoveryFetchLimit: 8, DiscoveryModelLimit: 1,
		CandidateLimit: 36, PerStockFetchLimit: 4, GenerateModelLimit: 1,
		VerificationStockLimit: 18,
	}
}

func marketSummaryV150RunnerEvidenceStatus(status NewsWindowStatus) recommendation.EvidenceStatus {
	switch status {
	case NewsWindowStatusOK:
		return recommendation.EvidenceStatusOK
	case NewsWindowStatusEmpty:
		return recommendation.EvidenceStatusEmpty
	case NewsWindowStatusStale:
		return recommendation.EvidenceStatusStale
	default:
		return recommendation.EvidenceStatusFailed
	}
}

func cloneMarketSummaryV150RunnerRouteLog(source *marketSummaryRouteLog) *MarketSummaryRouteLogSnapshot {
	if source == nil {
		return nil
	}
	result := *source
	result.PerCategoryCalls = make(map[string]int, len(source.PerCategoryCalls))
	for key, value := range source.PerCategoryCalls {
		result.PerCategoryCalls[key] = value
	}
	result.DroppedCandidates = append([]string(nil), source.DroppedCandidates...)
	result.Notes = append([]string(nil), source.Notes...)
	return &result
}

type marketSummaryV150RunnerReplayClock struct {
	mu       sync.Mutex
	prefix   [2]time.Time
	next     int
	delegate recommendation.Clock
}

// NewMarketSummaryV150RunnerReplayClock replays the exact market AsOf and
// initial candidate cutoff captured by the producer, then delegates every
// later observation (event, quote, portfolio, final cutoff, decision) to the
// caller's explicit live clock. This prevents Components+Runner from silently
// rebuilding the frozen candidate set at a later wall-clock time.
func NewMarketSummaryV150RunnerReplayClock(
	readSet MarketSummaryV150RunnerReadSet,
	delegate recommendation.Clock,
) (recommendation.Clock, error) {
	if marketSummaryV150RunnerNilInterface(delegate) {
		return nil, fmt.Errorf("%w: replay clock delegate is required", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	if readSet.Prepared == nil || readSet.Prepared.RunContext.AsOf.IsZero() || readSet.Prepared.RunContext.DataCutoffAt.IsZero() ||
		readSet.Prepared.RunContext.DataCutoffAt.Before(readSet.Prepared.RunContext.AsOf) {
		return nil, fmt.Errorf("%w: replay clock requires a valid prepared timeline", ErrMarketSummaryV150RunnerReadSetProducer)
	}
	return &marketSummaryV150RunnerReplayClock{
		prefix:   [2]time.Time{readSet.Prepared.RunContext.AsOf, readSet.Prepared.RunContext.DataCutoffAt},
		delegate: delegate,
	}, nil
}

func (c *marketSummaryV150RunnerReplayClock) Now() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next < len(c.prefix) {
		value := c.prefix[c.next]
		c.next++
		return value
	}
	if marketSummaryV150RunnerNilInterface(c.delegate) {
		return time.Time{}
	}
	return c.delegate.Now()
}

func marketSummaryV150RunnerNilInterface(value any) bool {
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

// RenderMarketSummaryV150Report exposes the typed report projection needed by
// the Web delivery layer without serializing the decision through RawJSON.
func RenderMarketSummaryV150Report(run *MarketSummaryV150RunSnapshot) string {
	return renderMarketSummaryV150Report(run)
}

var _ recommendation.Clock = (*marketSummaryV150RunnerReplayClock)(nil)
