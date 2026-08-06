package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"go-stock/backend/marketintel"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"
)

type marketSummaryV150RunnerCompatClock struct {
	times []time.Time
	next  int
}

func (c *marketSummaryV150RunnerCompatClock) Now() time.Time {
	if c == nil || c.next >= len(c.times) {
		return time.Time{}
	}
	value := c.times[c.next]
	c.next++
	return value
}

type marketSummaryV150RunnerCompatMarket struct {
	request  recommendation.MarketRequest
	snapshot recommendation.MarketSnapshot
	err      error
}

func (p *marketSummaryV150RunnerCompatMarket) MarketSnapshot(_ context.Context, request recommendation.MarketRequest) (recommendation.MarketSnapshot, error) {
	p.request = request
	return p.snapshot, p.err
}

type marketSummaryV150RunnerCompatCandidates struct {
	request recommendation.CandidateRequest
	batch   recommendation.CandidateBatch
	err     error
}

func (p *marketSummaryV150RunnerCompatCandidates) Candidates(_ context.Context, request recommendation.CandidateRequest) (recommendation.CandidateBatch, error) {
	p.request = request
	return p.batch, p.err
}

type marketSummaryV150RunnerCompatEvidence struct {
	request  recommendation.EvidenceRequest
	snapshot recommendation.EvidenceSnapshot
	err      error
}

func (p *marketSummaryV150RunnerCompatEvidence) Evidence(_ context.Context, request recommendation.EvidenceRequest) (recommendation.EvidenceSnapshot, error) {
	p.request = request
	return p.snapshot, p.err
}

type marketSummaryV150RunnerCompatVerifier struct {
	calls      []recommendation.EventVerificationCall
	completion recommendation.EventVerificationCompletion
	err        error
}

type marketSummaryV150RunnerCompatFinalQuotes struct {
	calls    int
	request  recommendation.FinalQuoteRequest
	snapshot recommendation.FinalQuoteSnapshot
	err      error
}

func (p *marketSummaryV150RunnerCompatFinalQuotes) FinalQuotes(_ context.Context, request recommendation.FinalQuoteRequest) (recommendation.FinalQuoteSnapshot, error) {
	p.calls++
	p.request = request
	return p.snapshot, p.err
}

func (p *marketSummaryV150RunnerCompatVerifier) Verify(_ context.Context, call recommendation.EventVerificationCall) (recommendation.EventVerificationCompletion, error) {
	p.calls = append(p.calls, call)
	return p.completion, p.err
}

type marketSummaryV150RunnerCompatPortfolio struct {
	request  recommendation.PortfolioRequest
	snapshot recommendation.PortfolioSnapshot
	err      error
}

func (p *marketSummaryV150RunnerCompatPortfolio) PortfolioSnapshot(_ context.Context, request recommendation.PortfolioRequest) (recommendation.PortfolioSnapshot, error) {
	p.request = request
	return p.snapshot, p.err
}

type marketSummaryV150RunnerCompatPublisher struct {
	calls        int
	decision     recommendation.FrozenDecision
	providerName string
	modelName    string
}

func (p *marketSummaryV150RunnerCompatPublisher) PublishDecision(
	_ context.Context,
	decision recommendation.FrozenDecision,
	providerName, modelName string,
) (string, error) {
	p.calls++
	p.decision = decision
	p.providerName = providerName
	p.modelName = modelName
	return "published-once", nil
}

type marketSummaryV150RunnerCompatFixture struct {
	startedAt        time.Time
	marketAt         time.Time
	initialCutoff    time.Time
	eventAt          time.Time
	quoteAvailableAt time.Time
	portfolioAt      time.Time
	finalCutoff      time.Time
	decisionAt       time.Time
	runSlot          string
	providerName     string
	modelName        string
	protocol         string
	benchmark        v150.BenchmarkSnapshot
	benchmarkSrc     MarketSummaryV150BenchmarkSource
	candidate        v150.Candidate
	source           MarketSummaryV150SourceCandidate
	verified         marketSummaryVerifiedCandidate
	portfolio        v150.PortfolioState
	completion       recommendation.EventVerificationCompletion
	finalQuotes      recommendation.FinalQuoteSnapshot
	legacyQuotes     map[string]StockInfo
	finalQuoteErr    error
}

func TestMarketSummaryV150RunnerCompatMatchesFrozenLegacyDecisionByteForByte(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	legacy := buildFrozenLegacyMarketSummaryV150Decision(t, fixture)
	clock := &marketSummaryV150RunnerCompatClock{times: []time.Time{
		fixture.marketAt, fixture.initialCutoff, fixture.eventAt,
		fixture.quoteAvailableAt, fixture.portfolioAt, fixture.finalCutoff, fixture.decisionAt,
	}}
	market := &marketSummaryV150RunnerCompatMarket{snapshot: recommendation.MarketSnapshot{
		Benchmark: fixture.benchmark,
		Evidence: []marketintel.Evidence{marketSummaryV150RunnerCompatEvidenceFromTiming(
			v150.BenchmarkCode, fixture.benchmarkSrc.Timing,
		)},
		CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.benchmarkSrc),
	}}
	candidates := &marketSummaryV150RunnerCompatCandidates{batch: recommendation.CandidateBatch{Items: []recommendation.CandidateInput{{
		Candidate: fixture.candidate,
		Evidence: []marketintel.Evidence{marketSummaryV150RunnerCompatEvidenceFromTiming(
			fixture.candidate.Symbol, *fixture.source.QuoteEvidence,
		)},
		CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.source),
	}}}}
	evidence := &marketSummaryV150RunnerCompatEvidence{snapshot: recommendation.EvidenceSnapshot{
		Status: recommendation.EvidenceStatusOK,
		Candidates: []recommendation.CandidateEvidence{{
			Symbol: fixture.candidate.Symbol, VerifiedAt: fixture.verified.VerifiedAt,
			CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.verified),
		}},
	}}
	verifier := &marketSummaryV150RunnerCompatVerifier{completion: fixture.completion}
	finalQuotes := &marketSummaryV150RunnerCompatFinalQuotes{snapshot: fixture.finalQuotes, err: fixture.finalQuoteErr}
	portfolio := &marketSummaryV150RunnerCompatPortfolio{snapshot: recommendation.PortfolioSnapshot{State: fixture.portfolio}}
	publisher := &marketSummaryV150RunnerCompatPublisher{}
	pipeline := newMarketSummaryV150RunnerCompatPipeline(fixture.startedAt, fixture.runSlot, fixture.protocol)
	runner := recommendation.NewRunner(recommendation.RunnerDependencies[string]{
		Pipeline: pipeline, Publisher: publisher,
		PipelinePorts: recommendation.PipelinePorts{
			Clock: clock, Market: market, Candidates: candidates, Evidence: evidence,
			EventVerifier: verifier, FinalQuotes: finalQuotes, Portfolio: portfolio,
		},
	})

	receipt, err := runner.Run(context.Background(), fixture.providerName, fixture.modelName)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if receipt != "published-once" || publisher.calls != 1 {
		t.Fatalf("publication receipt/calls = %q/%d, want one atomic publication", receipt, publisher.calls)
	}
	actual, ok := publisher.decision.(*MarketSummaryV150RunSnapshot)
	if !ok {
		t.Fatalf("published decision type = %T", publisher.decision)
	}
	legacyJSON := marketSummaryV150RunnerCompatJSON(t, legacy)
	actualJSON := marketSummaryV150RunnerCompatJSON(t, actual)
	if !bytes.Equal(actualJSON, legacyJSON) {
		t.Fatalf("Runner decision changed frozen legacy bytes:\nactual=%s\nlegacy=%s", actualJSON, legacyJSON)
	}
	if actual.DataHash != legacy.DataHash || actual.ModelHash != legacy.ModelHash ||
		actual.PromptHash != legacy.PromptHash || actual.RunContext.ConfigHash != legacy.RunContext.ConfigHash {
		t.Fatalf(
			"key hashes changed: actual=%s/%s/%s/%s legacy=%s/%s/%s/%s",
			actual.DataHash, actual.ModelHash, actual.PromptHash, actual.RunContext.ConfigHash,
			legacy.DataHash, legacy.ModelHash, legacy.PromptHash, legacy.RunContext.ConfigHash,
		)
	}
	if marketSummaryV150StableHash(string(actualJSON)) != marketSummaryV150StableHash(string(legacyJSON)) {
		t.Fatal("whole frozen decision hash changed")
	}
	if publisher.providerName != fixture.providerName || publisher.modelName != fixture.modelName {
		t.Fatalf("publisher identity = %q/%q", publisher.providerName, publisher.modelName)
	}
	if clock.next != len(clock.times) {
		t.Fatalf("clock observations = %d/%d", clock.next, len(clock.times))
	}
	if market.request.AsOf != fixture.marketAt || market.request.StrategyVersion != v150.StrategyVersion || market.request.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		t.Fatalf("market request changed: %+v", market.request)
	}
	if candidates.request.RunContext.StartedAt != fixture.startedAt || candidates.request.RunContext.AsOf != fixture.marketAt || candidates.request.Benchmark != fixture.benchmark {
		t.Fatalf("candidate request changed: %+v", candidates.request)
	}
	if got := marketSummaryV150RunnerSymbols(evidence.request.Candidates); !reflect.DeepEqual(got, []string{fixture.candidate.Symbol}) {
		t.Fatalf("evidence batch = %v", got)
	}
	if len(verifier.calls) != 1 || verifier.calls[0].Think {
		t.Fatalf("event verifier calls/think = %d/%v", len(verifier.calls), len(verifier.calls) == 1 && verifier.calls[0].Think)
	}
	if finalQuotes.calls != 1 || !reflect.DeepEqual(finalQuotes.request.Symbols, []string{fixture.candidate.Symbol}) || !finalQuotes.request.AsOf.Equal(fixture.eventAt) {
		t.Fatalf("final quote request = calls:%d request:%+v", finalQuotes.calls, finalQuotes.request)
	}
	if !portfolio.request.RunContext.DataCutoffAt.Equal(fixture.portfolioAt) {
		t.Fatalf("portfolio as-of = %v, want %v", portfolio.request.RunContext.DataCutoffAt, fixture.portfolioAt)
	}
}

func TestMarketSummaryV150RunnerCompatRejectsProjectionThatIsNotBackedByNormalizedEvidence(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	clock := &marketSummaryV150RunnerCompatClock{times: []time.Time{fixture.marketAt, fixture.initialCutoff}}
	market := &marketSummaryV150RunnerCompatMarket{snapshot: recommendation.MarketSnapshot{
		Benchmark:               fixture.benchmark,
		Evidence:                []marketintel.Evidence{marketSummaryV150RunnerCompatEvidenceFromTiming(v150.BenchmarkCode, fixture.benchmarkSrc.Timing)},
		CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.benchmarkSrc),
	}}
	badTiming := *fixture.source.QuoteEvidence
	badTiming.EvidenceID = "different-normalized-id"
	candidates := &marketSummaryV150RunnerCompatCandidates{batch: recommendation.CandidateBatch{Items: []recommendation.CandidateInput{{
		Candidate:               fixture.candidate,
		Evidence:                []marketintel.Evidence{marketSummaryV150RunnerCompatEvidenceFromTiming(fixture.candidate.Symbol, badTiming)},
		CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.source),
	}}}}
	publisher := &marketSummaryV150RunnerCompatPublisher{}
	_, err := recommendation.NewRunner(recommendation.RunnerDependencies[string]{
		Pipeline:  newMarketSummaryV150RunnerCompatPipeline(fixture.startedAt, fixture.runSlot, fixture.protocol),
		Publisher: publisher,
		PipelinePorts: recommendation.PipelinePorts{
			Clock: clock, Market: market, Candidates: candidates,
			Evidence: &marketSummaryV150RunnerCompatEvidence{}, EventVerifier: &marketSummaryV150RunnerCompatVerifier{},
			FinalQuotes: &marketSummaryV150RunnerCompatFinalQuotes{}, Portfolio: &marketSummaryV150RunnerCompatPortfolio{},
		},
	}).Run(context.Background(), fixture.providerName, fixture.modelName)
	if !errors.Is(err, errMarketSummaryV150RunnerCompatInput) {
		t.Fatalf("Run error = %v, want compatibility input rejection", err)
	}
	if publisher.calls != 0 {
		t.Fatalf("invalid frozen input reached publisher %d times", publisher.calls)
	}
}

func TestMarketSummaryV150RunnerCompatMatchesLegacyWhenFinalQuoteRefreshFails(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	fixture.legacyQuotes = nil
	fixture.finalQuotes = recommendation.FinalQuoteSnapshot{}
	fixture.finalQuoteErr = errors.New("provider failed")
	legacy := buildFrozenLegacyMarketSummaryV150Decision(t, fixture)
	actual, _, finalQuotes := buildFrozenRunnerMarketSummaryV150Decision(t, fixture, false)
	if got, want := marketSummaryV150RunnerCompatJSON(t, actual), marketSummaryV150RunnerCompatJSON(t, legacy); !bytes.Equal(got, want) {
		t.Fatalf("failed final-quote refresh changed frozen bytes:\nactual=%s\nlegacy=%s", got, want)
	}
	if actual.DataHash != legacy.DataHash || marketSummaryV150StableHash(string(marketSummaryV150RunnerCompatJSON(t, actual))) != marketSummaryV150StableHash(string(marketSummaryV150RunnerCompatJSON(t, legacy))) {
		t.Fatal("failed final-quote refresh changed data/decision hash")
	}
	if finalQuotes.calls != 1 || actual.Candidates[0].Candidate.HasCurrentData || actual.Candidates[0].Candidate.Price != 0 {
		t.Fatalf("failed quote refresh result = calls:%d candidate:%+v", finalQuotes.calls, actual.Candidates[0].Candidate)
	}
}

func TestMarketSummaryV150RunnerCompatRiskOffSkipsCandidateVerificationAndFinalQuotes(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	fixture.benchmark = marketSummaryV150TestBenchmark(true)
	legacy := buildFrozenLegacyMarketSummaryV150Decision(t, fixture)
	actual, evidence, finalQuotes := buildFrozenRunnerMarketSummaryV150Decision(t, fixture, true)
	if got, want := marketSummaryV150RunnerCompatJSON(t, actual), marketSummaryV150RunnerCompatJSON(t, legacy); !bytes.Equal(got, want) {
		t.Fatalf("risk-off runner changed frozen bytes:\nactual=%s\nlegacy=%s", got, want)
	}
	if !evidence.request.StatusOnly || len(evidence.request.Candidates) != 0 || finalQuotes.calls != 0 {
		t.Fatalf("risk-off work = statusOnly:%v candidates:%d finalQuotes:%d", evidence.request.StatusOnly, len(evidence.request.Candidates), finalQuotes.calls)
	}
}

func buildFrozenRunnerMarketSummaryV150Decision(
	t *testing.T,
	fixture marketSummaryV150RunnerCompatFixture,
	riskOff bool,
) (*MarketSummaryV150RunSnapshot, *marketSummaryV150RunnerCompatEvidence, *marketSummaryV150RunnerCompatFinalQuotes) {
	t.Helper()
	times := []time.Time{fixture.marketAt, fixture.initialCutoff}
	if !riskOff {
		times = append(times, fixture.eventAt, fixture.quoteAvailableAt)
	}
	times = append(times, fixture.portfolioAt, fixture.finalCutoff, fixture.decisionAt)
	clock := &marketSummaryV150RunnerCompatClock{times: times}
	market := &marketSummaryV150RunnerCompatMarket{snapshot: recommendation.MarketSnapshot{
		Benchmark:               fixture.benchmark,
		Evidence:                []marketintel.Evidence{marketSummaryV150RunnerCompatEvidenceFromTiming(v150.BenchmarkCode, fixture.benchmarkSrc.Timing)},
		CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.benchmarkSrc),
	}}
	candidates := &marketSummaryV150RunnerCompatCandidates{batch: recommendation.CandidateBatch{Items: []recommendation.CandidateInput{{
		Candidate:               fixture.candidate,
		Evidence:                []marketintel.Evidence{marketSummaryV150RunnerCompatEvidenceFromTiming(fixture.candidate.Symbol, *fixture.source.QuoteEvidence)},
		CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.source),
	}}}}
	evidenceSnapshot := recommendation.EvidenceSnapshot{Status: recommendation.EvidenceStatusOK}
	if !riskOff {
		evidenceSnapshot.Candidates = []recommendation.CandidateEvidence{{
			Symbol: fixture.candidate.Symbol, VerifiedAt: fixture.verified.VerifiedAt,
			CompatibilityProjection: marketSummaryV150RunnerCompatJSON(t, fixture.verified),
		}}
	}
	evidence := &marketSummaryV150RunnerCompatEvidence{snapshot: evidenceSnapshot}
	verifier := &marketSummaryV150RunnerCompatVerifier{completion: fixture.completion}
	finalQuotes := &marketSummaryV150RunnerCompatFinalQuotes{snapshot: fixture.finalQuotes, err: fixture.finalQuoteErr}
	portfolio := &marketSummaryV150RunnerCompatPortfolio{snapshot: recommendation.PortfolioSnapshot{State: fixture.portfolio}}
	publisher := &marketSummaryV150RunnerCompatPublisher{}
	_, err := recommendation.NewRunner(recommendation.RunnerDependencies[string]{
		Pipeline: newMarketSummaryV150RunnerCompatPipeline(fixture.startedAt, fixture.runSlot, fixture.protocol), Publisher: publisher,
		PipelinePorts: recommendation.PipelinePorts{
			Clock: clock, Market: market, Candidates: candidates, Evidence: evidence,
			EventVerifier: verifier, FinalQuotes: finalQuotes, Portfolio: portfolio,
		},
	}).Run(context.Background(), fixture.providerName, fixture.modelName)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	actual, ok := publisher.decision.(*MarketSummaryV150RunSnapshot)
	if !ok {
		t.Fatalf("published decision type = %T", publisher.decision)
	}
	if riskOff && len(verifier.calls) != 0 {
		t.Fatalf("risk-off invoked event verifier %d times", len(verifier.calls))
	}
	return actual, evidence, finalQuotes
}

func newMarketSummaryV150RunnerCompatFixture(t *testing.T) marketSummaryV150RunnerCompatFixture {
	t.Helper()
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	marketAt := startedAt.Add(5 * time.Second)
	initialCutoff := startedAt.Add(10 * time.Second)
	eventAt := startedAt.Add(20 * time.Second)
	quoteAvailableAt := startedAt.Add(30 * time.Second)
	portfolioAt := startedAt.Add(40 * time.Second)
	finalCutoff := startedAt.Add(50 * time.Second)
	decisionAt := startedAt.Add(time.Minute)
	candidate := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, marketAt)
	source := marketSummaryV150TestSource(candidate, marketAt)
	benchmarkSource := MarketSummaryV150BenchmarkSource{
		Timing: MarketSummaryV150EvidenceTiming{
			EvidenceID: "benchmark:510300:2026-08-03", EvidenceType: "benchmark_adjusted_daily_bar",
			SourceAt: startedAt.Add(-18 * time.Hour), AvailableAt: marketAt,
		},
		AdjustmentSource: "tencent_qfq", LatestTradeDate: "2026-08-03", Complete: true,
	}
	verified := marketSummaryVerifiedCandidate{
		StockName: candidate.Name, StockCode: candidate.Symbol, BkName: candidate.Sector,
		Reason: "frozen factual verification", PositiveSignals: []string{"point-in-time evidence complete"},
		VerdictHints: []string{"technical candidate"}, VerifiedAt: eventAt,
	}
	quoteSourceAt := startedAt.Add(19 * time.Second)
	finalQuote := recommendation.FinalQuote{
		Symbol: candidate.Symbol, Name: candidate.Name, Price: 9.92, PreviousClose: 9.9, Open: 9.91,
		Amount: 200_000_000, HasPrice: true, HasPreviousClose: true, HasOpen: true,
		HasAmount: true, HasVolume: true, SourceAt: quoteSourceAt,
	}
	legacyQuote := StockInfo{
		Name: candidate.Name, Price: "9.92", PreClose: "9.9", Open: "9.91", Amount: "200000000", Volume: "1000000",
		Date: quoteSourceAt.Format(time.DateOnly), Time: quoteSourceAt.Format(time.TimeOnly),
	}
	return marketSummaryV150RunnerCompatFixture{
		startedAt: startedAt, marketAt: marketAt, initialCutoff: initialCutoff, eventAt: eventAt,
		quoteAvailableAt: quoteAvailableAt, portfolioAt: portfolioAt, finalCutoff: finalCutoff, decisionAt: decisionAt,
		runSlot: "09:40", providerName: "provider-x", modelName: "configured-model",
		protocol:  AIAPIProtocolChatCompletions,
		benchmark: marketSummaryV150TestBenchmark(false), benchmarkSrc: benchmarkSource,
		candidate: candidate, source: source, verified: verified, portfolio: v150.PortfolioState{},
		completion: recommendation.EventVerificationCompletion{
			Content:    `{"assessments":[{"symbol":"000001.SZ","direction":"neutral","relevance":0,"importance":0,"confidence":1,"evidenceIds":[]}]}`,
			ResponseID: "response-1", Model: "model-x",
		},
		finalQuotes:  recommendation.FinalQuoteSnapshot{Quotes: []recommendation.FinalQuote{finalQuote}},
		legacyQuotes: map[string]StockInfo{candidate.Symbol: legacyQuote},
	}
}

func buildFrozenLegacyMarketSummaryV150Decision(t *testing.T, fixture marketSummaryV150RunnerCompatFixture) *MarketSummaryV150RunSnapshot {
	t.Helper()
	run, err := newMarketSummaryV150Run(
		fixture.startedAt, fixture.initialCutoff, fixture.runSlot, fixture.benchmark,
		[]v150.Candidate{fixture.candidate}, map[string]MarketSummaryV150SourceCandidate{fixture.candidate.Symbol: fixture.source},
	)
	if err != nil {
		t.Fatalf("legacy new run: %v", err)
	}
	run.BenchmarkSource = fixture.benchmarkSrc
	verified := []marketSummaryVerifiedCandidate{fixture.verified}
	if run.Regime.NoTrade {
		verified = nil
		_, _ = applyMarketSummaryV150NewsEventGate(run, NewsWindowStatusOK)
	} else {
		if allowed, reason := applyMarketSummaryV150NewsEventGate(run, NewsWindowStatusOK); !allowed || reason != "" {
			t.Fatalf("legacy event gate = %v/%q", allowed, reason)
		}
		request := buildMarketSummaryV150RunnerEventRequest(run, verified, fixture.eventAt)
		run.PromptHash = marketSummaryV150StableHash(marketSummaryV150EventModelSystemPrompt + "\n" + marketSummaryV150EventModelSchemaPrompt)
		run.ModelHash = marketSummaryV150StableHash(fixture.providerName + "|" + fixture.modelName + "|" + fixture.completion.Model + "|" + NormalizeAIAPIProtocol(fixture.protocol))
		if err := applyMarketSummaryV150EventModelResponse(run, request, fixture.completion.Content, fixture.completion.Model); err != nil {
			t.Fatalf("legacy event response: %v", err)
		}
		originalLoader := loadMarketSummaryV150RealtimeQuotesForRefresh
		originalNow := marketSummaryV150QuoteRefreshNow
		loadMarketSummaryV150RealtimeQuotesForRefresh = func(_ []marketSummaryIndicatorCandidate) map[string]StockInfo {
			return fixture.legacyQuotes
		}
		marketSummaryV150QuoteRefreshNow = func() time.Time { return fixture.quoteAvailableAt }
		_, _ = refreshMarketSummaryV150VerificationQuotes(run)
		loadMarketSummaryV150RealtimeQuotesForRefresh = originalLoader
		marketSummaryV150QuoteRefreshNow = originalNow
	}
	run.PortfolioStateStatus = "ok"
	run.PortfolioBefore = cloneV150PortfolioState(fixture.portfolio)
	run.RunContext.DataCutoffAt = fixture.finalCutoff
	if err := finalizeMarketSummaryV150Run(run, verified, fixture.portfolio, fixture.decisionAt); err != nil {
		t.Fatalf("legacy finalize: %v", err)
	}
	if err := refreshMarketSummaryV150DataHash(run); err != nil {
		t.Fatalf("legacy data hash: %v", err)
	}
	run.Warnings = dedupeNonEmptyStrings(run.Warnings, 256)
	return run
}

func marketSummaryV150RunnerCompatEvidenceFromTiming(symbol string, timing MarketSummaryV150EvidenceTiming) marketintel.Evidence {
	return marketintel.Evidence{
		ID: timing.EvidenceID, Type: marketintel.EvidenceType(timing.EvidenceType), Symbol: symbol,
		Source: "frozen-cache", SourceAt: timing.SourceAt, AvailableAt: timing.AvailableAt,
	}
}

func marketSummaryV150RunnerCompatJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return payload
}

func marketSummaryV150RunnerSymbols(items []v150.ScoredCandidate) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, normalizeRecommendStockCode(item.Candidate.Symbol))
	}
	return result
}

var (
	_ recommendation.Clock                     = (*marketSummaryV150RunnerCompatClock)(nil)
	_ recommendation.MarketPort                = (*marketSummaryV150RunnerCompatMarket)(nil)
	_ recommendation.CandidatesPort            = (*marketSummaryV150RunnerCompatCandidates)(nil)
	_ recommendation.EvidencePort              = (*marketSummaryV150RunnerCompatEvidence)(nil)
	_ recommendation.EventVerifier             = (*marketSummaryV150RunnerCompatVerifier)(nil)
	_ recommendation.FinalQuotePort            = (*marketSummaryV150RunnerCompatFinalQuotes)(nil)
	_ recommendation.PortfolioPort             = (*marketSummaryV150RunnerCompatPortfolio)(nil)
	_ recommendation.DecisionPublisher[string] = (*marketSummaryV150RunnerCompatPublisher)(nil)
)
