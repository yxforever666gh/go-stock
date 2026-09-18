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

func TestAllValidatedScoresRemainExecutable(t *testing.T) {
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	evidence, values := observationScores(at, 60, 59, 58, 57, 56, 55, 50, 49)
	if prompt := buildPrompt(prepareEvidence(evidence, at.Add(-24*time.Hour)), at); strings.Contains(prompt, "最多输出6只") || !strings.Contains(prompt, "必须逐只覆盖") {
		t.Fatal("prompt restricts complete candidate scoring")
	}
	items, warnings := validateRecommendations("run", at, prepareEvidence(evidence, at.Add(-24*time.Hour)), values)
	if len(warnings) != 0 || len(items) != 8 {
		t.Fatalf("items=%d warnings=%v", len(items), warnings)
	}
	assignResearch2SelectionRanks(items)
	for i, item := range items {
		if item.SelectionRole != "" || item.SelectionRank != i+1 || item.Status != "buy_pending" {
			t.Fatalf("candidate %d excluded by score or rank: %+v", i, item)
		}
	}
}

func TestLowScoreReportIsEffectiveAndDoesNotRerun(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 9, 50, 0, 0, shanghai())
	evidence, values := observationScores(at, 50, 49, 48)
	ai := &sequenceAI{responses: []string{observationResponse(values)}}
	r := research2TestRepository(t)
	runner := NewRunner(r, ai, fixedEvidence{value: evidence}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at }, nil)
	run, err := runner.Run(ctx, at)
	if err != nil || run.Status != "success" || run.RecommendationCount != 3 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	items, err := r.RunRecommendations(ctx, run.RunID)
	if err != nil || len(items) != 3 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	for _, item := range items {
		if item.Status != "buy_pending" || (item.SelectionRole != "" && item.SelectionRole != "legacy-unversioned") {
			t.Fatalf("low score became analysis only: %+v", item)
		}
	}
	later, err := runner.Run(ctx, at)
	if err != nil || later.RunID != run.RunID || ai.calls != 1 {
		t.Fatalf("valid report reran: %+v err=%v calls=%d", later, err, ai.calls)
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
