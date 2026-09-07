package research

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sharedai "go-stock/backend/ai"
	"go-stock/internal/marketquote"
)

type lifecycleQuoteFunc func(context.Context, string) (marketquote.Quote, error)

func (f lifecycleQuoteFunc) CurrentQuote(ctx context.Context, code string) (marketquote.Quote, error) {
	return f(ctx, code)
}

func TestLifecycleExecutionUsesQuoteCompletionTime(t *testing.T) {
	for _, action := range []string{"buy", "sell"} {
		t.Run(action, func(t *testing.T) {
			repo := researchTestRepo(t)
			started := time.Date(2026, 9, 7, 10, 4, 53, 0, shanghaiLocation)
			clock := started
			status := "buy_pending"
			if action == "sell" {
				status = "sell_pending"
			}
			rec := seedRecommendation(t, repo, status, started.AddDate(0, 0, -1), started, "")
			if action == "sell" {
				seedOpenPosition(t, repo, rec, started.AddDate(0, 0, -1))
			}
			quotes := lifecycleQuoteFunc(func(context.Context, string) (marketquote.Quote, error) {
				clock = started.Add(12 * time.Second)
				return marketquote.Quote{Code: rec.StockCode, Name: rec.StockName, Market: "SH", Price: 10, At: clock}, nil
			})
			service := NewService(repo, &scriptedAI{}, quotes, openCalendar{})
			service.now = func() time.Time { return clock }
			var err error
			if action == "buy" {
				err = service.attemptBuy(context.Background(), &rec, started)
			} else {
				err = service.trySell(context.Background(), &rec, started)
			}
			if err != nil {
				t.Fatal(err)
			}
			var count int64
			if err = repo.DB().Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", rec.RecommendationID, action).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("slow successful quote did not execute %s: count=%d", action, count)
			}
		})
	}
}

func TestLifecycleExecutionDoesNotTradeAfterQuoteCrossesClose(t *testing.T) {
	for _, action := range []string{"buy", "sell"} {
		t.Run(action, func(t *testing.T) {
			repo := researchTestRepo(t)
			started := time.Date(2026, 9, 7, 14, 59, 59, 0, shanghaiLocation)
			clock := started
			status := "buy_pending"
			if action == "sell" {
				status = "sell_pending"
			}
			rec := seedRecommendation(t, repo, status, started.AddDate(0, 0, -1), started, "")
			if action == "sell" {
				seedOpenPosition(t, repo, rec, started.AddDate(0, 0, -1))
			}
			quotes := lifecycleQuoteFunc(func(context.Context, string) (marketquote.Quote, error) {
				clock = started.Add(3 * time.Second)
				return marketquote.Quote{Code: rec.StockCode, Name: rec.StockName, Market: "SH", Price: 10, At: clock}, nil
			})
			service := NewService(repo, &scriptedAI{}, quotes, openCalendar{})
			service.now = func() time.Time { return clock }
			var err error
			if action == "buy" {
				err = service.attemptBuy(context.Background(), &rec, started)
			} else {
				err = service.trySell(context.Background(), &rec, started)
			}
			if err != nil {
				t.Fatal(err)
			}
			var count int64
			if err = repo.DB().Model(&SimulatedTrade{}).Where("recommendation_id = ?", rec.RecommendationID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("trade crossed close: %d", count)
			}
		})
	}
}

func TestLifecycleAcceptedSellSurvivesQuoteFailureAndRestart(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 7, 10, 5, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -1), now, "")
	seedOpenPosition(t, repo, rec, now.AddDate(0, 0, -1))
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: `{"action":"卖出","reason":"趋势转弱"}`}}}
	contexts := &scriptedContexts{drafts: []LifecycleObservationDraft{readyLifecycleDraft(now, "ready")}}
	service := NewService(repo, ai, &scriptedQuotes{errors: []error{errors.New("quote transport failed")}}, openCalendar{}, contexts)
	service.now = func() time.Time { return now }
	_ = service.ProcessDue(context.Background())
	stored, err := repo.Recommendation(context.Background(), rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "sell_pending" || stored.LastDecision != "卖出" {
		t.Fatalf("accepted sell lost: %+v", stored)
	}
	if len(ai.requests) != 1 {
		t.Fatalf("AI calls=%d", len(ai.requests))
	}
	now = *stored.NextCheckAt
	restartedAI := &scriptedAI{}
	restarted := NewService(repo, restartedAI, &scriptedQuotes{quotes: []marketquote.Quote{{Code: rec.StockCode, Name: rec.StockName, Market: "SH", Price: 10, At: now}}}, openCalendar{})
	restarted.now = func() time.Time { return now }
	if err = restarted.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var trades int64
	if err = repo.DB().Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", rec.RecommendationID, "sell").Count(&trades).Error; err != nil {
		t.Fatal(err)
	}
	if len(restartedAI.requests) != 0 || trades != 1 {
		t.Fatalf("AI=%d sells=%d", len(restartedAI.requests), trades)
	}
}

func TestLifecycleAcceptedSellSurvivesTradeStorageFailure(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 7, 10, 5, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -1), now, "")
	seedOpenPosition(t, repo, rec, now.AddDate(0, 0, -1))
	if err := repo.DB().Exec(`CREATE TRIGGER reject_trade BEFORE INSERT ON research_v160_simulated_trades BEGIN SELECT RAISE(ABORT, 'fixture storage failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: `{"action":"卖出","reason":"趋势转弱"}`}}}
	quotes := lifecycleQuoteFunc(func(context.Context, string) (marketquote.Quote, error) {
		return marketquote.Quote{Code: rec.StockCode, Name: rec.StockName, Market: "SH", Price: 10, At: now}, nil
	})
	service := NewService(repo, ai, quotes, openCalendar{}, &scriptedContexts{drafts: []LifecycleObservationDraft{readyLifecycleDraft(now, "ready")}})
	service.now = func() time.Time { return now }
	_ = service.ProcessDue(context.Background())
	stored, err := repo.Recommendation(context.Background(), rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "sell_pending" {
		t.Fatalf("storage failure lost pending sell: %+v", stored)
	}
	var events []DecisionEvent
	if err = repo.DB().Where("recommendation_id = ? AND data_status = ?", rec.RecommendationID, "storage_error").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("storage error was misclassified: %+v", events)
	}
	if err = repo.DB().Exec("DROP TRIGGER reject_trade").Error; err != nil {
		t.Fatal(err)
	}
	now = *stored.NextCheckAt
	if err = service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 1 {
		t.Fatalf("storage retry asked AI again: %d", len(ai.requests))
	}
}

func TestLifecycleDecisionAndPendingStateAreAtomic(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 7, 10, 5, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -1), now, "")
	if err := repo.DB().Exec(`CREATE TRIGGER reject_decision BEFORE INSERT ON research_v160_decision_events BEGIN SELECT RAISE(ABORT, 'fixture storage failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	event := DecisionEvent{EventID: newID(), RecommendationID: rec.RecommendationID, DecisionType: "卖出", DecidedAt: now}
	if err := repo.RecordLifecycleDecision(context.Background(), &event, "response", now.Add(time.Minute)); err == nil {
		t.Fatal("expected failed audit insert")
	}
	stored, err := repo.Recommendation(context.Background(), rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "active" || stored.LastDecision != "" || stored.PreviousResponseID != "" {
		t.Fatalf("partial decision persisted: %+v", stored)
	}
}

func TestLifecycleNewsCoverageOnlyAdvancesAfterSourceSuccess(t *testing.T) {
	repo := researchTestRepo(t)
	start := time.Date(2026, 9, 7, 10, 0, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", start.AddDate(0, 0, -1), start, "")
	for index, status := range []string{"ok", "failed", "partial", "empty"} {
		at := start.Add(time.Duration(index) * 15 * time.Minute)
		payload, _ := json.Marshal(map[string]any{"to": at, "coverageComplete": status != "partial"})
		observation, err := NewLifecycleObservation(LifecycleContextRequest{ObservationID: newID(), Recommendation: rec, Now: at}, LifecycleObservationDraft{Status: "partial", Sources: []LifecycleEvidenceSource{{Category: "news", Status: status, Content: string(payload), CollectedAt: at.Add(time.Second)}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = repo.AppendObservation(context.Background(), &observation); err != nil {
			t.Fatal(err)
		}
		covered, _, err := repo.LifecycleNewsCoverage(context.Background(), rec.RecommendationID)
		if err != nil {
			t.Fatal(err)
		}
		want := start
		if status == "empty" {
			want = at
		}
		if !covered.Equal(want) {
			t.Fatalf("status=%s advanced to %v want %v", status, covered, want)
		}
	}
}

func TestLifecyclePromptDoesNotCutAlreadyBudgetedNewsRecords(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, shanghaiLocation)
	sources := make([]LifecycleEvidenceSource, 0, 20)
	for index := 0; index < 20; index++ {
		sources = append(sources, LifecycleEvidenceSource{Category: "money", Content: "data", CollectedAt: now})
	}
	content := `{"items":[{"id":42,"summary":"` + strings.Repeat("x", 2300) + `尾部事件"}]}`
	sources = append(sources, LifecycleEvidenceSource{Category: "news", Status: "ok", Content: content, CollectedAt: now})
	request := LifecycleContextRequest{ObservationID: newID(), Now: now}
	observation, err := NewLifecycleObservation(request, LifecycleObservationDraft{Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	if prompt := lifecyclePrompt(Recommendation{}, now, observation, nil); !strings.Contains(prompt, content) {
		t.Fatal("news records were cut after assigning coverage")
	}
}

func TestLifecyclePositionQuoteNeverMovesBackward(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -1), now, "")
	seedOpenPosition(t, repo, rec, now.AddDate(0, 0, -1))
	position, err := repo.Position(context.Background(), rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, quote := range []marketquote.Quote{{Price: 11, At: now}, {Price: 99, At: now.Add(-time.Second)}, {Price: 77, At: now}} {
		if err = repo.UpdatePositionQuote(context.Background(), position.ID, quote); err != nil {
			t.Fatal(err)
		}
	}
	position, err = repo.Position(context.Background(), rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if position.CurrentPrice != 11 || position.CurrentPriceAt == nil || !position.CurrentPriceAt.Equal(now) {
		t.Fatalf("quote regressed: %+v", position)
	}
}
