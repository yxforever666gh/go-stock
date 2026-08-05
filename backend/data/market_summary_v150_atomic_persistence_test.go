package data

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

func TestPersistMarketSummaryV150DecisionSerializesFinalQuotaAndLeavesNoOrphanRules(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-v150-atomic.db"))
	enableStrategyProductionForTest(t, db.Dao)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("migrate recommendations: %v", err)
	}
	if err := persistence.MigrateStrategyPersistence(db.Dao); err != nil {
		t.Fatalf("migrate immutable strategy tables: %v", err)
	}

	previousMarkDirty := markAiRecommendYieldDirtyCodesForMutationFn
	previousRequestRecalc := requestAiRecommendYieldScopedRecalcForMutationFn
	markAiRecommendYieldDirtyCodesForMutationFn = func([]string, string, string) error { return nil }
	requestAiRecommendYieldScopedRecalcForMutationFn = func(bool, string, []string) {}
	t.Cleanup(func() {
		markAiRecommendYieldDirtyCodesForMutationFn = previousMarkDirty
		requestAiRecommendYieldScopedRecalcForMutationFn = previousRequestRecalc
	})

	loc := cnLocation()
	base := time.Date(2026, 8, 4, 9, 40, 0, 0, loc)
	buildRun := func(runOrdinal int, firstCode int) *MarketSummaryV150RunSnapshot {
		t.Helper()
		startedAt := base.Add(time.Duration(runOrdinal) * time.Nanosecond)
		cutoff := base.Add(time.Minute + time.Duration(runOrdinal)*time.Nanosecond)
		candidates := []v150.Candidate{
			marketSummaryV150TestCandidate(fmt.Sprintf("%06d.SZ", firstCode), fmt.Sprintf("sector-%d-a", runOrdinal), 1, cutoff),
			marketSummaryV150TestCandidate(fmt.Sprintf("%06d.SZ", firstCode+1), fmt.Sprintf("sector-%d-b", runOrdinal), 0.99, cutoff),
		}
		sources := map[string]MarketSummaryV150SourceCandidate{}
		verified := make([]marketSummaryVerifiedCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			sources[candidate.Symbol] = marketSummaryV150TestSource(candidate, cutoff)
			verified = append(verified, marketSummaryVerifiedCandidate{StockCode: candidate.Symbol})
		}
		run, err := newMarketSummaryV150Run(startedAt, cutoff, "09:40", marketSummaryV150TestBenchmark(false), candidates, sources)
		if err != nil {
			t.Fatalf("new run %d: %v", runOrdinal, err)
		}
		run.BenchmarkSource = MarketSummaryV150BenchmarkSource{
			Timing: MarketSummaryV150EvidenceTiming{
				EvidenceID:   fmt.Sprintf("benchmark-%d", runOrdinal),
				EvidenceType: "benchmark_adjusted_daily_bar",
				SourceAt:     base.AddDate(0, 0, -1),
				AvailableAt:  base.Add(-time.Hour),
			},
			AdjustmentSource: "tencent_qfq",
			LatestTradeDate:  base.AddDate(0, 0, -1).Format(time.DateOnly),
			Complete:         true,
		}
		if err := finalizeMarketSummaryV150Run(run, verified, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
			t.Fatalf("finalize run %d: %v", runOrdinal, err)
		}
		if len(run.Production) != 2 {
			t.Fatalf("pre-release production run %d=%d, want 2", runOrdinal, len(run.Production))
		}
		return run
	}

	runs := []*MarketSummaryV150RunSnapshot{buildRun(0, 1001), buildRun(1, 2001)}
	type outcome struct {
		result *models.MarketSummaryRecommendSaveResult
		err    error
	}
	outcomes := make(chan outcome, len(runs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, run := range runs {
		run := run
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := PersistMarketSummaryV150Decision(context.Background(), db.Dao, run, "backend", marketSummaryV150LocalModelSpec)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	saved := 0
	for item := range outcomes {
		if item.err != nil {
			t.Fatalf("atomic decision persistence: %v (result=%+v)", item.err, item.result)
		}
		if item.result != nil {
			saved += item.result.SavedCount
		}
	}
	if saved != v150.FixedStrategyV150Config().RiskOnDailyCap {
		t.Fatalf("saved recommendations=%d, want shared cap %d", saved, v150.FixedStrategyV150Config().RiskOnDailyCap)
	}

	assertCount := func(model any, want int64) {
		t.Helper()
		var got int64
		if err := db.Dao.Model(model).Count(&got).Error; err != nil {
			t.Fatalf("count %T: %v", model, err)
		}
		if got != want {
			t.Fatalf("count %T=%d, want %d", model, got, want)
		}
	}
	assertCount(&models.AiRecommendStocks{}, 2)
	assertCount(&models.StrategyRunSnapshot{}, 2)
	assertCount(&models.RuleSnapshot{}, 2)

	var issuedCount int64
	if err := db.Dao.Model(&models.OrderEvent{}).Where("event_type = ?", "rule_issued").Count(&issuedCount).Error; err != nil {
		t.Fatalf("count issued events: %v", err)
	}
	if issuedCount != 2 {
		t.Fatalf("rule_issued=%d, want 2", issuedCount)
	}
	var noTradeCount int64
	if err := db.Dao.Model(&models.OrderEvent{}).Where("event_type = ? AND reason = ?", "no_trade", marketSummaryV150DailyCapReached).Count(&noTradeCount).Error; err != nil {
		t.Fatalf("count quota no_trade events: %v", err)
	}
	if noTradeCount != 1 {
		t.Fatalf("quota no_trade=%d, want 1", noTradeCount)
	}

	var orphanRules int64
	if err := db.Dao.Raw(`
		SELECT COUNT(*)
		FROM strategy_rule_snapshot AS rule
		LEFT JOIN ai_recommend_stocks AS recommendation
		  ON recommendation.strategy_rule_id = rule.rule_id
		WHERE recommendation.id IS NULL`).Scan(&orphanRules).Error; err != nil {
		t.Fatalf("audit orphan rules: %v", err)
	}
	if orphanRules != 0 {
		t.Fatalf("orphan executable rules=%d, want 0", orphanRules)
	}
}
