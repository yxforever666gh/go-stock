package data

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type marketSummaryV150SQLitePublisher struct {
	database *gorm.DB
	calls    int
	decision *MarketSummaryV150RunSnapshot
}

func (p *marketSummaryV150SQLitePublisher) PublishDecision(
	ctx context.Context,
	decision recommendation.FrozenDecision,
	providerName string,
	modelName string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	p.calls++
	run, ok := decision.(*MarketSummaryV150RunSnapshot)
	if !ok {
		return nil, fmt.Errorf("unexpected V1.5 decision type %T", decision)
	}
	p.decision = run
	return PersistMarketSummaryV150Decision(ctx, p.database, run, providerName, modelName)
}

type marketSummaryV150PersistenceFixture struct {
	source     marketSummaryV150RunnerCompatFixture
	components MarketSummaryV150RunnerComponents
	clock      *marketSummaryV150RunnerCompatClock
	verifier   *marketSummaryV150RunnerCompatVerifier
	quoteCalls *int
}

func newMarketSummaryV150PersistenceFixture(
	t *testing.T,
	database *gorm.DB,
	riskOff bool,
) marketSummaryV150PersistenceFixture {
	t.Helper()
	source := newMarketSummaryV150RunnerCompatFixture(t)
	if riskOff {
		source.benchmark = marketSummaryV150TestBenchmark(true)
	} else {
		// The shared byte-equivalence fixture intentionally models a neutral
		// event and therefore scores below the production threshold after its
		// final quote refresh. Give this persistence fixture one causal positive
		// event so the real pipeline produces an executable rule.
		evidenceID := "event:" + source.candidate.Symbol + ":positive"
		source.verified.EvidenceSources = []models.AIEvidenceReference{{
			Type: "news", SourceName: "fixture-news", Title: "causal positive event",
			PublishedAt: source.startedAt.Add(-time.Hour).Format(time.RFC3339Nano),
			RawHash:     evidenceID,
		}}
		source.completion.Content = fmt.Sprintf(
			`{"assessments":[{"symbol":%q,"direction":"positive","relevance":1,"importance":1,"confidence":1,"evidenceIds":[%q]}]}`,
			source.candidate.Symbol,
			evidenceID,
		)
	}
	prepared, err := newMarketSummaryV150Run(
		source.startedAt,
		source.initialCutoff,
		source.runSlot,
		source.benchmark,
		[]v150.Candidate{source.candidate},
		map[string]MarketSummaryV150SourceCandidate{source.candidate.Symbol: source.source},
	)
	if err != nil {
		t.Fatalf("prepare V1.5 runner fixture: %v", err)
	}
	prepared.BenchmarkSource = source.benchmarkSrc
	readSet := MarketSummaryV150RunnerReadSet{
		Prepared:       prepared,
		EvidenceStatus: recommendation.EvidenceStatusOK,
		AIProtocol:     source.protocol,
	}
	if !riskOff {
		readSet.Verified = []MarketSummaryVerifiedCandidateSnapshot{source.verified}
	}
	components, err := NewMarketSummaryV150RunnerComponents(readSet, database)
	if err != nil {
		t.Fatalf("assemble V1.5 runner components: %v", err)
	}

	times := []time.Time{
		source.marketAt,
		source.initialCutoff,
		source.eventAt,
		source.quoteAvailableAt,
		source.portfolioAt,
		source.finalCutoff,
		source.decisionAt,
	}
	if riskOff {
		times = []time.Time{
			source.marketAt,
			source.initialCutoff,
			source.portfolioAt,
			source.finalCutoff,
			source.decisionAt,
		}
	}

	quoteCalls := 0
	previousQuoteLoader := loadMarketSummaryV150RealtimeQuotesForRefresh
	loadMarketSummaryV150RealtimeQuotesForRefresh = func(_ []marketSummaryIndicatorCandidate) map[string]StockInfo {
		quoteCalls++
		return source.legacyQuotes
	}
	t.Cleanup(func() { loadMarketSummaryV150RealtimeQuotesForRefresh = previousQuoteLoader })

	return marketSummaryV150PersistenceFixture{
		source: source, components: components,
		clock:      &marketSummaryV150RunnerCompatClock{times: times},
		verifier:   &marketSummaryV150RunnerCompatVerifier{completion: source.completion},
		quoteCalls: &quoteCalls,
	}
}

func (f marketSummaryV150PersistenceFixture) run(
	ctx context.Context,
	publisher *marketSummaryV150SQLitePublisher,
) (*models.MarketSummaryRecommendSaveResult, error) {
	return recommendation.NewRunner(recommendation.RunnerDependencies[*models.MarketSummaryRecommendSaveResult]{
		Pipeline:  f.components.Pipeline,
		Publisher: publisher,
		PipelinePorts: recommendation.PipelinePorts{
			Clock: f.clock, Market: f.components.Market, Candidates: f.components.Candidates,
			Evidence: f.components.Evidence, EventVerifier: f.verifier,
			FinalQuotes: f.components.FinalQuotes, Portfolio: f.components.Portfolio,
		},
	}).Run(ctx, f.source.providerName, f.source.modelName)
}

type marketSummaryV150PersistenceCounts struct {
	runs             int64
	candidates       int64
	rules            int64
	orderEvents      int64
	securityHistory  int64
	corporateActions int64
	projections      int64
}

func loadMarketSummaryV150PersistenceCounts(
	t *testing.T,
	database *gorm.DB,
	runID string,
) marketSummaryV150PersistenceCounts {
	t.Helper()
	count := func(model any, column string) int64 {
		t.Helper()
		var result int64
		if err := database.Model(model).Where(column+" = ?", runID).Count(&result).Error; err != nil {
			t.Fatalf("count %T for run %s: %v", model, runID, err)
		}
		return result
	}
	return marketSummaryV150PersistenceCounts{
		runs:             count(&models.StrategyRunSnapshot{}, "run_id"),
		candidates:       count(&models.CandidateSnapshot{}, "run_id"),
		rules:            count(&models.RuleSnapshot{}, "run_id"),
		orderEvents:      count(&models.OrderEvent{}, "run_id"),
		securityHistory:  count(&models.SecurityMasterHistory{}, "run_id"),
		corporateActions: count(&models.CorporateActionEvent{}, "run_id"),
		projections:      count(&models.AiRecommendStocks{}, "strategy_run_id"),
	}
}

func newMarketSummaryV150PersistenceDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "v150-runner-persistence.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open V1.5 persistence database: %v", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatalf("access V1.5 persistence database pool: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDatabase.Close(); closeErr != nil {
			t.Errorf("close V1.5 persistence database: %v", closeErr)
		}
	})
	if err := database.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("migrate recommendation projection: %v", err)
	}
	if err := persistence.MigrateStrategyPersistence(database); err != nil {
		t.Fatalf("migrate immutable strategy persistence: %v", err)
	}
	enableStrategyProductionForTest(t, database)

	previousMarkDirty := markAiRecommendYieldDirtyCodesForMutationFn
	previousRequestRecalc := requestAiRecommendYieldScopedRecalcForMutationFn
	markAiRecommendYieldDirtyCodesForMutationFn = func([]string, string, string) error { return nil }
	requestAiRecommendYieldScopedRecalcForMutationFn = func(bool, string, []string) {}
	t.Cleanup(func() {
		markAiRecommendYieldDirtyCodesForMutationFn = previousMarkDirty
		requestAiRecommendYieldScopedRecalcForMutationFn = previousRequestRecalc
	})
	return database
}

func TestMarketSummaryV150TypedRunnerPublishesOneConsistentSQLiteTransaction(t *testing.T) {
	database := newMarketSummaryV150PersistenceDatabase(t)
	fixture := newMarketSummaryV150PersistenceFixture(t, database, false)
	publisher := &marketSummaryV150SQLitePublisher{database: database}

	receipt, err := fixture.run(context.Background(), publisher)
	if err != nil {
		t.Fatalf("run and publish V1.5 decision: %v", err)
	}
	if publisher.calls != 1 {
		t.Fatalf("publisher calls = %d, want 1", publisher.calls)
	}
	if publisher.decision == nil || len(publisher.decision.Candidates) != 1 || len(publisher.decision.Production) != 1 {
		t.Fatalf("published typed decision = %+v", publisher.decision)
	}
	if receipt == nil || receipt.SavedCount != 1 || receipt.ProductionCount != 1 || receipt.BlockedCount != 0 {
		t.Fatalf("publication receipt = %+v", receipt)
	}
	if *fixture.quoteCalls != 1 || len(fixture.verifier.calls) != 1 {
		t.Fatalf("quote/verifier calls = %d/%d, want 1/1", *fixture.quoteCalls, len(fixture.verifier.calls))
	}

	runID := publisher.decision.RunContext.RunID
	wantCounts := marketSummaryV150PersistenceCounts{
		runs: 1, candidates: 1, rules: 1, orderEvents: 1,
		securityHistory: 1, corporateActions: 0, projections: 1,
	}
	if got := loadMarketSummaryV150PersistenceCounts(t, database, runID); got != wantCounts {
		t.Fatalf("persisted transaction counts = %+v, want %+v", got, wantCounts)
	}

	var run models.StrategyRunSnapshot
	if err := database.Where("run_id = ?", runID).First(&run).Error; err != nil {
		t.Fatalf("load persisted run: %v", err)
	}
	var candidate models.CandidateSnapshot
	if err := database.Where("run_id = ?", runID).First(&candidate).Error; err != nil {
		t.Fatalf("load persisted candidate: %v", err)
	}
	var rule models.RuleSnapshot
	if err := database.Where("run_id = ?", runID).First(&rule).Error; err != nil {
		t.Fatalf("load persisted rule: %v", err)
	}
	var event models.OrderEvent
	if err := database.Where("run_id = ?", runID).First(&event).Error; err != nil {
		t.Fatalf("load persisted order event: %v", err)
	}
	var projection models.AiRecommendStocks
	if err := database.Where("strategy_run_id = ?", runID).First(&projection).Error; err != nil {
		t.Fatalf("load recommendation projection: %v", err)
	}

	if run.CandidateCount != 1 || run.RuleCount != 1 || run.OrderEventCount != 1 || run.SecuritySnapshotCount != 1 {
		t.Fatalf("persisted run child counts = %+v", run)
	}
	if rule.CandidateID != candidate.CandidateID || event.EventType != "rule_issued" || event.RuleID != rule.RuleID ||
		projection.StrategyRunID != runID || projection.StrategyRuleID != rule.RuleID {
		t.Fatalf("transaction identity chain candidate=%+v rule=%+v event=%+v projection=%+v", candidate, rule, event, projection)
	}
}

func TestMarketSummaryV150TypedRunnerRollsBackSnapshotsWhenProjectionInsertFails(t *testing.T) {
	database := newMarketSummaryV150PersistenceDatabase(t)
	if err := database.Exec(`
CREATE TRIGGER fail_v150_projection_insert
BEFORE INSERT ON ai_recommend_stocks
WHEN NEW.summary_version = '1.5.0'
BEGIN
  SELECT RAISE(ABORT, 'forced V1.5 projection failure');
END;
`).Error; err != nil {
		t.Fatalf("install projection failure trigger: %v", err)
	}
	fixture := newMarketSummaryV150PersistenceFixture(t, database, false)
	publisher := &marketSummaryV150SQLitePublisher{database: database}

	receipt, err := fixture.run(context.Background(), publisher)
	if err == nil {
		t.Fatal("projection insert failure did not fail the typed publication")
	}
	if publisher.calls != 1 || publisher.decision == nil {
		t.Fatalf("publisher calls/decision = %d/%+v, want one typed attempt", publisher.calls, publisher.decision)
	}
	if receipt == nil || receipt.SavedCount != 0 || receipt.BlockedCount != 1 {
		t.Fatalf("failed publication receipt = %+v", receipt)
	}
	want := marketSummaryV150PersistenceCounts{}
	if got := loadMarketSummaryV150PersistenceCounts(t, database, publisher.decision.RunContext.RunID); got != want {
		t.Fatalf("projection failure left partial transaction rows = %+v", got)
	}
}

func TestMarketSummaryV150TypedRunnerPersistsStructuredNoTradeWithoutProjection(t *testing.T) {
	database := newMarketSummaryV150PersistenceDatabase(t)
	fixture := newMarketSummaryV150PersistenceFixture(t, database, true)
	publisher := &marketSummaryV150SQLitePublisher{database: database}

	receipt, err := fixture.run(context.Background(), publisher)
	if err != nil {
		t.Fatalf("run and publish risk-off no_trade: %v", err)
	}
	if publisher.calls != 1 || publisher.decision == nil {
		t.Fatalf("publisher calls/decision = %d/%+v, want one typed attempt", publisher.calls, publisher.decision)
	}
	if len(fixture.verifier.calls) != 0 || *fixture.quoteCalls != 0 {
		t.Fatalf("risk-off verifier/quote calls = %d/%d, want 0/0", len(fixture.verifier.calls), *fixture.quoteCalls)
	}
	if publisher.decision.NoTradeReason != v150.RejectRiskOff || len(publisher.decision.Production) != 0 {
		t.Fatalf("risk-off typed decision = %+v", publisher.decision)
	}
	if receipt == nil || receipt.SavedCount != 0 || receipt.ProductionCount != 0 || receipt.BlockedCount != 1 {
		t.Fatalf("risk-off publication receipt = %+v", receipt)
	}

	runID := publisher.decision.RunContext.RunID
	wantCounts := marketSummaryV150PersistenceCounts{
		runs: 1, candidates: 1, rules: 0, orderEvents: 1,
		securityHistory: 1, corporateActions: 0, projections: 0,
	}
	if got := loadMarketSummaryV150PersistenceCounts(t, database, runID); got != wantCounts {
		t.Fatalf("risk-off transaction counts = %+v, want %+v", got, wantCounts)
	}
	var event models.OrderEvent
	if err := database.Where("run_id = ?", runID).First(&event).Error; err != nil {
		t.Fatalf("load no_trade event: %v", err)
	}
	if event.EventType != "no_trade" || event.RuleID != "" || event.Reason != v150.RejectRiskOff {
		t.Fatalf("structured no_trade event = %+v", event)
	}
}

func TestMarketSummaryV150TypedPublisherRejectsDuplicateRunIDWithoutChangingRows(t *testing.T) {
	database := newMarketSummaryV150PersistenceDatabase(t)
	fixture := newMarketSummaryV150PersistenceFixture(t, database, false)
	publisher := &marketSummaryV150SQLitePublisher{database: database}

	if _, err := fixture.run(context.Background(), publisher); err != nil {
		t.Fatalf("publish initial V1.5 decision: %v", err)
	}
	if publisher.calls != 1 || publisher.decision == nil {
		t.Fatalf("initial publisher calls/decision = %d/%+v", publisher.calls, publisher.decision)
	}
	runID := publisher.decision.RunContext.RunID
	before := loadMarketSummaryV150PersistenceCounts(t, database, runID)

	_, err := publisher.PublishDecision(
		context.Background(),
		publisher.decision,
		fixture.source.providerName,
		fixture.source.modelName,
	)
	if err == nil {
		t.Fatal("duplicate RunID publication unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "append strategy run") || !strings.Contains(err.Error(), runID) {
		t.Fatalf("duplicate RunID error did not identify the immutable run conflict: %v", err)
	}
	if publisher.calls != 2 {
		t.Fatalf("publisher calls after duplicate attempt = %d, want 2", publisher.calls)
	}
	if after := loadMarketSummaryV150PersistenceCounts(t, database, runID); after != before {
		t.Fatalf("duplicate RunID changed persisted rows: before=%+v after=%+v", before, after)
	}
}

var _ recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult] = (*marketSummaryV150SQLitePublisher)(nil)
