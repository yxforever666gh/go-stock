package data

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type marketSummaryV150RunnerReadSetStageFixture struct {
	prepared *MarketSummaryV150RunSnapshot
	verified []marketSummaryVerifiedCandidate

	discoveryClockReads int
	prepareClockReads   int
	verifyClockReads    int
	cancel              context.CancelFunc
	cancelAfter         string

	discoveryCalls int
	prepareCalls   int
	verifyCalls    int
}

func (s *marketSummaryV150RunnerReadSetStageFixture) Discovery(
	ctx context.Context,
	question string,
	budget marketSummaryRouteBudget,
	logState *marketSummaryRouteLog,
	_ time.Time,
	clock recommendation.Clock,
) (marketSummaryDiscoveryInput, []models.LongTigerRankData, marketSummaryTimeWindow, error) {
	s.discoveryCalls++
	if err := consumeMarketSummaryV150RunnerReadSetClock(clock, s.discoveryClockReads); err != nil {
		return marketSummaryDiscoveryInput{}, nil, marketSummaryTimeWindow{}, err
	}
	logState.NewsWindowStatus = NewsWindowStatusOK
	logState.RunSlot = "morning_open"
	if s.cancelAfter == "discovery" && s.cancel != nil {
		s.cancel()
	}
	return marketSummaryDiscoveryInput{
			Question: question, RunSlot: "morning_open", Budget: budget,
		}, nil, marketSummaryTimeWindow{
			Slot:  marketSummaryRunSlotMorning,
			Start: s.prepared.RunContext.StartedAt.Add(-time.Hour),
			End:   s.prepared.RunContext.StartedAt,
		}, nil
}

func (s *marketSummaryV150RunnerReadSetStageFixture) Prepare(
	_ context.Context,
	_ marketSummaryDiscoveryInput,
	_ time.Time,
	_ *marketSummaryRouteLog,
	clock recommendation.Clock,
) (*MarketSummaryV150RunSnapshot, error) {
	s.prepareCalls++
	if err := consumeMarketSummaryV150RunnerReadSetClock(clock, s.prepareClockReads); err != nil {
		return nil, err
	}
	if s.cancelAfter == "prepare" && s.cancel != nil {
		s.cancel()
	}
	return cloneMarketSummaryV150RunnerPrepared(s.prepared)
}

func (s *marketSummaryV150RunnerReadSetStageFixture) Verify(
	_ context.Context,
	_ *OpenAi,
	_ marketSummaryDiscoveryInput,
	discovery *marketSummaryDiscoveryResult,
	_ []models.LongTigerRankData,
	_ marketSummaryRouteBudget,
	_ *marketSummaryRouteLog,
	clock recommendation.Clock,
) ([]marketSummaryVerifiedCandidate, error) {
	s.verifyCalls++
	if discovery == nil || len(discovery.CandidateStocks) != len(s.prepared.VerificationSymbols) {
		return nil, errors.New("producer did not pass the frozen top set to factual verification")
	}
	if err := consumeMarketSummaryV150RunnerReadSetClock(clock, s.verifyClockReads); err != nil {
		return nil, err
	}
	if s.cancelAfter == "verify" && s.cancel != nil {
		s.cancel()
	}
	return append([]marketSummaryVerifiedCandidate(nil), s.verified...), nil
}

func consumeMarketSummaryV150RunnerReadSetClock(clock recommendation.Clock, count int) error {
	for index := 0; index < count; index++ {
		if clock == nil || clock.Now().IsZero() {
			return errors.New("fixture clock was exhausted")
		}
	}
	return nil
}

func TestMarketSummaryV150ReadSetProducerComponentsRunnerMatchFrozenLegacyDecision(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	prepared := newMarketSummaryV150RunnerReadSetPrepared(t, fixture)
	stages := &marketSummaryV150RunnerReadSetStageFixture{
		prepared: prepared, verified: []marketSummaryVerifiedCandidate{fixture.verified},
		discoveryClockReads: 3, prepareClockReads: 3, verifyClockReads: 2,
	}
	producerClock := &marketSummaryV150RunnerCompatClock{times: []time.Time{
		fixture.startedAt.Add(time.Second), fixture.startedAt.Add(2 * time.Second), fixture.startedAt.Add(3 * time.Second),
		fixture.marketAt, fixture.startedAt.Add(7 * time.Second), fixture.initialCutoff,
		fixture.startedAt.Add(15 * time.Second), fixture.eventAt,
		fixture.eventAt, fixture.quoteAvailableAt, fixture.portfolioAt, fixture.finalCutoff, fixture.decisionAt,
	}}
	producer := newMarketSummaryV150RunnerReadSetProducerWithStages(stages)
	result, err := producer.Produce(MarketSummaryV150RunnerReadSetProducerInput{
		Context: context.Background(),
		OpenAI: &OpenAi{
			ProviderName: fixture.providerName, Model: fixture.modelName, ApiProtocol: fixture.protocol,
		},
		StartedAt: fixture.startedAt,
		Question:  DefaultMarketSummaryQuestion,
		Clock:     producerClock,
	})
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if stages.discoveryCalls != 1 || stages.prepareCalls != 1 || stages.verifyCalls != 1 {
		t.Fatalf("producer stage calls = %d/%d/%d", stages.discoveryCalls, stages.prepareCalls, stages.verifyCalls)
	}
	if result.RouteLog == nil || result.VerifiedCandidateCount != 1 || result.RouteLog.VerifiedCandidateCt != 1 ||
		result.CandidateCount != 1 || result.VerificationSetCount != 1 {
		t.Fatalf("typed producer diagnostics = %+v", result)
	}
	if !result.PreparedAsOf.Equal(prepared.RunContext.AsOf) || !result.PreparedDataCutoffAt.Equal(prepared.RunContext.DataCutoffAt) {
		t.Fatalf("prepared times changed: %v/%v", result.PreparedAsOf, result.PreparedDataCutoffAt)
	}
	if !result.ReadSet.Prepared.RunContext.DecisionAt.IsZero() || !result.ReadSet.Prepared.RunContext.ValidFromAt.IsZero() ||
		len(result.ReadSet.Prepared.Production) != 0 || result.ReadSet.Prepared.PortfolioStateStatus != "" {
		t.Fatalf("producer crossed the pre-decision boundary: %+v", result.ReadSet.Prepared.RunContext)
	}

	database, err := gorm.Open(sqlite.Open("file:readset-producer-runner?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	components, err := NewMarketSummaryV150RunnerComponents(result.ReadSet, database)
	if err != nil {
		t.Fatalf("components: %v", err)
	}
	components.FinalQuotes = &marketSummaryV150RunnerCompatFinalQuotes{snapshot: fixture.finalQuotes}
	components.Portfolio = &marketSummaryV150RunnerCompatPortfolio{snapshot: recommendation.PortfolioSnapshot{State: fixture.portfolio}}
	replayClock, err := NewMarketSummaryV150RunnerReplayClock(result.ReadSet, producerClock)
	if err != nil {
		t.Fatalf("replay clock: %v", err)
	}
	publisher := &marketSummaryV150RunnerCompatPublisher{}
	verifier := &marketSummaryV150RunnerCompatVerifier{completion: fixture.completion}
	_, err = recommendation.NewRunner(recommendation.RunnerDependencies[string]{
		Pipeline: components.Pipeline, Publisher: publisher,
		PipelinePorts: recommendation.PipelinePorts{
			Clock: replayClock, Market: components.Market, Candidates: components.Candidates,
			Evidence: components.Evidence, EventVerifier: verifier,
			FinalQuotes: components.FinalQuotes, Portfolio: components.Portfolio,
		},
	}).Run(context.Background(), result.ProviderName, result.ModelName)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	actual, ok := publisher.decision.(*MarketSummaryV150RunSnapshot)
	if !ok {
		t.Fatalf("decision type = %T", publisher.decision)
	}
	legacy := buildFrozenLegacyMarketSummaryV150Decision(t, fixture)
	actualJSON := marketSummaryV150RunnerCompatJSON(t, actual)
	legacyJSON := marketSummaryV150RunnerCompatJSON(t, legacy)
	if !bytes.Equal(actualJSON, legacyJSON) {
		t.Fatalf("producer replay changed frozen bytes:\nactual=%s\nlegacy=%s", actualJSON, legacyJSON)
	}
	if actual.DataHash != legacy.DataHash || actual.ModelHash != legacy.ModelHash ||
		actual.PromptHash != legacy.PromptHash || actual.RunContext.ConfigHash != legacy.RunContext.ConfigHash {
		t.Fatalf(
			"producer replay hashes changed: actual=%s/%s/%s/%s legacy=%s/%s/%s/%s",
			actual.DataHash, actual.ModelHash, actual.PromptHash, actual.RunContext.ConfigHash,
			legacy.DataHash, legacy.ModelHash, legacy.PromptHash, legacy.RunContext.ConfigHash,
		)
	}
	if producerClock.next != len(producerClock.times) {
		t.Fatalf("explicit clock observations = %d/%d", producerClock.next, len(producerClock.times))
	}
	if got := RenderMarketSummaryV150Report(actual); got != renderMarketSummaryV150Report(actual) {
		t.Fatal("exported typed report projection differs from frozen report")
	}
}

func TestMarketSummaryV150ReadSetProducerCancellationFailsClosed(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	prepared := newMarketSummaryV150RunnerReadSetPrepared(t, fixture)
	for _, cancelAfter := range []string{"pre_canceled", "discovery", "prepare", "verify"} {
		t.Run(cancelAfter, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			stages := &marketSummaryV150RunnerReadSetStageFixture{
				prepared: prepared, verified: []marketSummaryVerifiedCandidate{fixture.verified},
				cancel: cancel, cancelAfter: cancelAfter,
			}
			if cancelAfter == "pre_canceled" {
				cancel()
			}
			producer := newMarketSummaryV150RunnerReadSetProducerWithStages(stages)
			result, err := producer.Produce(MarketSummaryV150RunnerReadSetProducerInput{
				Context: ctx, OpenAI: &OpenAi{}, StartedAt: fixture.startedAt,
				Question: DefaultMarketSummaryQuestion, Clock: &marketSummaryV150RunnerCompatClock{},
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Produce error = %v, want context.Canceled", err)
			}
			if !reflect.DeepEqual(result, MarketSummaryV150RunnerReadSetResult{}) {
				t.Fatalf("canceled producer leaked partial output: %+v", result)
			}
			wantCalls := map[string][3]int{
				"pre_canceled": {0, 0, 0}, "discovery": {1, 0, 0},
				"prepare": {1, 1, 0}, "verify": {1, 1, 1},
			}[cancelAfter]
			if got := [3]int{stages.discoveryCalls, stages.prepareCalls, stages.verifyCalls}; got != wantCalls {
				t.Fatalf("stage calls after cancellation = %v, want %v", got, wantCalls)
			}
		})
	}
}

func TestMarketSummaryV150ReadSetProducerRiskOffSkipsCandidateEvidence(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	fixture.benchmark = marketSummaryV150TestBenchmark(true)
	prepared := newMarketSummaryV150RunnerReadSetPrepared(t, fixture)
	stages := &marketSummaryV150RunnerReadSetStageFixture{prepared: prepared}
	result, err := newMarketSummaryV150RunnerReadSetProducerWithStages(stages).Produce(
		MarketSummaryV150RunnerReadSetProducerInput{
			Context: context.Background(), OpenAI: &OpenAi{}, StartedAt: fixture.startedAt,
			Question: DefaultMarketSummaryQuestion, Clock: &marketSummaryV150RunnerCompatClock{},
		},
	)
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if stages.verifyCalls != 0 || len(result.ReadSet.Verified) != 0 || result.VerifiedCandidateCount != 0 {
		t.Fatalf("risk-off factual work = calls:%d verified:%d/%d", stages.verifyCalls, len(result.ReadSet.Verified), result.VerifiedCandidateCount)
	}
}

func newMarketSummaryV150RunnerReadSetPrepared(
	t *testing.T,
	fixture marketSummaryV150RunnerCompatFixture,
) *MarketSummaryV150RunSnapshot {
	t.Helper()
	prepared, err := newMarketSummaryV150Run(
		fixture.startedAt, fixture.initialCutoff, fixture.runSlot, fixture.benchmark,
		[]v150.Candidate{fixture.candidate},
		map[string]MarketSummaryV150SourceCandidate{fixture.candidate.Symbol: fixture.source},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared.BenchmarkSource = fixture.benchmarkSrc
	return prepared
}

var _ marketSummaryV150RunnerReadSetStages = (*marketSummaryV150RunnerReadSetStageFixture)(nil)
