package data

import (
	"bytes"
	"context"
	"testing"
	"time"

	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestProductionRunnerComponentsPreserveFrozenLegacyDecision(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	prepared, err := newMarketSummaryV150Run(
		fixture.startedAt, fixture.initialCutoff, fixture.runSlot, fixture.benchmark,
		[]v150.Candidate{fixture.candidate}, map[string]MarketSummaryV150SourceCandidate{fixture.candidate.Symbol: fixture.source},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared.BenchmarkSource = fixture.benchmarkSrc
	database, err := gorm.Open(sqlite.Open("file:runner-components?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	components, err := NewMarketSummaryV150RunnerComponents(MarketSummaryV150RunnerReadSet{
		Prepared: prepared, EvidenceStatus: recommendation.EvidenceStatusOK,
		Verified: []MarketSummaryVerifiedCandidateSnapshot{fixture.verified}, AIProtocol: fixture.protocol,
	}, database)
	if err != nil {
		t.Fatalf("components: %v", err)
	}

	originalQuoteLoader := loadMarketSummaryV150RealtimeQuotesForRefresh
	quoteCalls := 0
	loadMarketSummaryV150RealtimeQuotesForRefresh = func(_ []marketSummaryIndicatorCandidate) map[string]StockInfo {
		quoteCalls++
		return fixture.legacyQuotes
	}
	t.Cleanup(func() { loadMarketSummaryV150RealtimeQuotesForRefresh = originalQuoteLoader })
	components.Portfolio = &marketSummaryV150RunnerCompatPortfolio{snapshot: recommendation.PortfolioSnapshot{State: fixture.portfolio}}
	clock := &marketSummaryV150RunnerCompatClock{times: []time.Time{
		fixture.marketAt, fixture.initialCutoff, fixture.eventAt, fixture.quoteAvailableAt,
		fixture.portfolioAt, fixture.finalCutoff, fixture.decisionAt,
	}}
	verifier := &marketSummaryV150RunnerCompatVerifier{completion: fixture.completion}
	publisher := &marketSummaryV150RunnerCompatPublisher{}
	_, err = recommendation.NewRunner(recommendation.RunnerDependencies[string]{
		Pipeline: components.Pipeline, Publisher: publisher,
		PipelinePorts: recommendation.PipelinePorts{
			Clock: clock, Market: components.Market, Candidates: components.Candidates,
			Evidence: components.Evidence, EventVerifier: verifier,
			FinalQuotes: components.FinalQuotes, Portfolio: components.Portfolio,
		},
	}).Run(context.Background(), fixture.providerName, fixture.modelName)
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
		t.Fatalf("production ports changed frozen bytes:\nactual=%s\nlegacy=%s", actualJSON, legacyJSON)
	}
	if actual.DataHash != legacy.DataHash || actual.ModelHash != legacy.ModelHash || actual.PromptHash != legacy.PromptHash {
		t.Fatalf("production ports changed hashes: actual=%s/%s/%s legacy=%s/%s/%s", actual.DataHash, actual.ModelHash, actual.PromptHash, legacy.DataHash, legacy.ModelHash, legacy.PromptHash)
	}
	if quoteCalls != 1 || len(verifier.calls) != 1 || publisher.calls != 1 {
		t.Fatalf("calls quote/verifier/publisher = %d/%d/%d", quoteCalls, len(verifier.calls), publisher.calls)
	}
}

func TestRunnerComponentsRejectPostDecisionSnapshot(t *testing.T) {
	fixture := newMarketSummaryV150RunnerCompatFixture(t)
	prepared, err := newMarketSummaryV150Run(
		fixture.startedAt, fixture.initialCutoff, fixture.runSlot, fixture.benchmark,
		[]v150.Candidate{fixture.candidate}, map[string]MarketSummaryV150SourceCandidate{fixture.candidate.Symbol: fixture.source},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared.RunContext.DecisionAt = fixture.decisionAt
	database, err := gorm.Open(sqlite.Open("file:runner-components-reject?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMarketSummaryV150RunnerComponents(MarketSummaryV150RunnerReadSet{
		Prepared: prepared, EvidenceStatus: recommendation.EvidenceStatusOK,
	}, database); err == nil {
		t.Fatal("post-decision snapshot was accepted as a runner read set")
	}
}
