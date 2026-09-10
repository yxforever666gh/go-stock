package research2

import (
	"context"
	"encoding/json"
	"fmt"
	"go-stock/internal/researchevidence"
	"strings"
	"testing"
	"time"
)

func observationScores(at time.Time, scores ...float64) (Evidence, []modelRecommendation) {
	var candidates []researchevidence.StockCandidate
	var values []modelRecommendation
	for i, score := range scores {
		code := fmt.Sprintf("sh600%03d", i+100)
		candidates = append(candidates, researchevidence.StockCandidate{Code: code, Name: code})
		values = append(values, modelRecommendation{Code: code, MarketScore: 10, SectorScore: 10, StockScore: score - 20, FinalScore: score, ReferencePrice: 10, SourceRefs: scoreFixtureRefs(code), ScoreReasons: map[string]string{"market": "市场依据", "sector": "板块依据", "stock": "个股依据", "catalyst": "无新催化", "risk": "无额外风险"}})
	}
	return scoreFixtureEvidence(at, candidates...), values
}

func observationResponse(values []modelRecommendation) string {
	encoded, _ := json.Marshal(modelOutput{TradingDay: true, Conclusion: "按证据评分", Recommendations: values})
	return string(encoded)
}

func TestObservationOnlyRunDoesNotTradeOrStopRefill(t *testing.T) {
	ctx := context.Background()
	r := research2TestRepository(t)
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	evidence, values := observationScores(at, 50, 49, 48)
	ai := &sequenceAI{responses: []string{observationResponse(values)}}
	runner := NewRunner(r, ai, fixedEvidence{value: evidence}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at }, nil)
	run, err := runner.Run(ctx, at)
	if err != nil || run.Status != "no_recommendation" || run.RecommendationCount != 0 || run.PrimaryCount != 0 || run.StandbyCount != 0 || ai.calls != 1 {
		t.Fatalf("run=%+v err=%v calls=%d", run, err, ai.calls)
	}
	items, err := r.RunRecommendations(ctx, run.RunID)
	if err != nil || len(items) != 3 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	for _, item := range items {
		if item.SelectionRole != "observation" || item.Status != "analysis_only" {
			t.Fatalf("item=%+v", item)
		}
		if !strings.Contains(run.ReportMarkdown, item.StockCode) || !strings.Contains(run.ReportMarkdown, "仅观察") {
			t.Fatal("missing score report")
		}
		if err := r.FinalizeMetrics(ctx, item.RecommendationID, true, true, true); err != nil {
			t.Fatal(err)
		}
	}
	due, err := r.DueRecommendations(ctx, at, []string{"buy_pending", "standby"})
	if err != nil || len(due) != 0 {
		t.Fatalf("due=%v %v", due, err)
	}
	active, err := r.ActiveAndPending(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("active=%v %v", active, err)
	}
	exclusions, err := r.ExecutionChainExcludedCodes(ctx, run.ChainID)
	if err != nil || len(exclusions) != 0 {
		t.Fatalf("exclusions=%v %v", exclusions, err)
	}
	chain, err := r.ExecutionChain(ctx, run.ChainID)
	if err != nil || chain.Status != "running" || chain.FilledSlots != 0 {
		t.Fatalf("chain=%v %v", chain, err)
	}
	ready, err := r.ExecutionChainsReadyForRefill(ctx, at.Add(10*time.Minute))
	if err != nil || len(ready) != 0 {
		t.Fatalf("ready=%v %v", ready, err)
	}
	chain, err = r.RefreshExecutionChainFilled(ctx, run.ChainID)
	if err != nil || chain.Status != "completed" || chain.FilledSlots != 0 || chain.StopReason != "主选与候选已满额" {
		t.Fatalf("full observation list not completed: %+v %v", chain, err)
	}
	var account Account
	if err := r.DB().First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	if account.Cash != InitialCash {
		t.Fatal("observations consumed cash")
	}
	// Even erroneous state changes cannot turn a saved observation into a trade.
	item := items[0]
	if err := r.DB().Model(&Recommendation{}).Where("recommendation_id = ?", item.RecommendationID).Update("status", "standby").Error; err != nil {
		t.Fatal(err)
	}
	if err := r.PromoteStandby(ctx, item.RecommendationID, "", ""); err == nil {
		t.Fatal("observation promoted")
	}
	if err := r.DB().Model(&Recommendation{}).Where("recommendation_id = ?", item.RecommendationID).Update("status", "buy_pending").Error; err != nil {
		t.Fatal(err)
	}
	if err := r.RecordBuy(ctx, item.RecommendationID, Trade{TradedAt: at, Quantity: 100, NetCashFlow: -1000}, at.AddDate(0, 0, 1)); err == nil {
		t.Fatal("observation bought")
	}
}

func TestObservationScoresKeepAllRowsButOnlySixCanExecute(t *testing.T) {
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	evidence, values := observationScores(at, 60, 59, 58, 57, 56, 55, 54, 50)
	if prompt := buildPrompt(prepareEvidence(evidence, at.Add(-24*time.Hour)), at); strings.Contains(prompt, "最多输出6只") || !strings.Contains(prompt, "必须逐只覆盖") {
		t.Fatal("prompt still limits scoring to executable shortlist")
	}
	items, warnings := validateRecommendations("run", at, prepareEvidence(evidence, at.Add(-24*time.Hour)), values)
	if len(warnings) != 0 || len(items) != 8 {
		t.Fatalf("items=%d warnings=%v", len(items), warnings)
	}
	assignResearch2SelectionRoles(items, 2)
	for i, item := range items {
		want := "observation"
		if i < 2 {
			want = "primary"
		} else if i < 6 {
			want = "standby"
		}
		if item.SelectionRole != want {
			t.Fatalf("item %d role=%s want=%s", i, item.SelectionRole, want)
		}
	}
}

func TestObservationRepairsMissingCandidateScores(t *testing.T) {
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	evidence, values := observationScores(at, 50, 49)
	ai := &sequenceAI{responses: []string{observationResponse(values[:1]), observationResponse(values)}}
	r := research2TestRepository(t)
	runner := NewRunner(r, ai, fixedEvidence{value: evidence}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at }, nil)
	run, err := runner.Run(context.Background(), at)
	items, listErr := r.RunRecommendations(context.Background(), run.RunID)
	if err != nil || listErr != nil || ai.calls != 2 || len(items) != 2 {
		t.Fatalf("items=%d calls=%d err=%v/%v", len(items), ai.calls, err, listErr)
	}
}
