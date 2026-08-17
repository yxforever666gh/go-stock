package research

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type scriptedAI struct {
	results  []CompletionResult
	errors   []error
	requests []CompletionRequest
}

func (m *scriptedAI) Complete(_ context.Context, request CompletionRequest) (CompletionResult, error) {
	m.requests = append(m.requests, request)
	i := len(m.requests) - 1
	var result CompletionResult
	var err error
	if i < len(m.results) {
		result = m.results[i]
	}
	if i < len(m.errors) {
		err = m.errors[i]
	}
	return result, err
}

type scriptedQuotes struct {
	quotes []Quote
	calls  int
}

func (m *scriptedQuotes) CurrentQuote(_ context.Context, _ string) (Quote, error) {
	if len(m.quotes) == 0 {
		return Quote{}, errors.New("no quote")
	}
	i := m.calls
	if i >= len(m.quotes) {
		i = len(m.quotes) - 1
	}
	m.calls++
	return m.quotes[i], nil
}

type openCalendar struct{}

func (openCalendar) IsTradingDay(context.Context, time.Time) (bool, error) { return true, nil }

type advancingAI struct {
	clock    *time.Time
	finishAt time.Time
	result   CompletionResult
	requests []CompletionRequest
}

func (a *advancingAI) Complete(_ context.Context, request CompletionRequest) (CompletionResult, error) {
	a.requests = append(a.requests, request)
	*a.clock = a.finishAt
	return a.result, nil
}

func researchTestRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&AnalysisRun{}, &Recommendation{}, &LifecycleMessage{}, &DecisionEvent{}, &SimulatedAccount{}, &SimulatedTrade{}, &Position{}); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)
	if err = repo.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repo
}

func seedRecommendation(t *testing.T, repo *Repository, status string, signal, due time.Time, previous string) Recommendation {
	t.Helper()
	run := AnalysisRun{RunID: newID(), ScheduledFor: signal, StartedAt: signal, Status: "success"}
	if err := repo.CreateAnalysis(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	rec := Recommendation{RecommendationID: newID(), AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "浦发银行", SignalAt: signal, ActivationCondition: "放量转强", Status: status, PreviousResponseID: previous, NextCheckAt: &due}
	initial := []LifecycleMessage{{RecommendationID: rec.RecommendationID, Sequence: 1, Role: "system", Phase: "initial", Content: "ONLY:" + rec.RecommendationID, CreatedAt: signal}}
	if err := repo.CreateRecommendation(context.Background(), &rec, initial); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestLifecycleFallbackUsesOnlyStockHistoryAndActivatesAtRefetchedQuote(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "pending", now.Add(-time.Hour), now, "response-old")
	ai := &scriptedAI{results: []CompletionResult{{}, {Content: `{"action":"激活","reason":"条件满足"}`, ResponseID: "response-new", Model: "gpt-5.6-sol"}}, errors: []error{errors.New("relay rejects previous_response_id"), nil}}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: now}, {Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10.1, PreviousClose: 9.8, At: now.Add(time.Second)}}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 2 || ai.requests[0].PreviousResponseID != "response-old" || ai.requests[1].PreviousResponseID != "" {
		t.Fatalf("requests=%+v", ai.requests)
	}
	if len(ai.requests[1].Messages) == 0 {
		t.Fatal("fallback history missing")
	}
	for _, message := range ai.requests[1].Messages {
		if message.RecommendationID != rec.RecommendationID {
			t.Fatalf("cross-stock memory: %+v", message)
		}
	}
	if !strings.Contains(ai.requests[0].Prompt, "价格：10.000") {
		t.Fatalf("decision prompt lacks quote: %s", ai.requests[0].Prompt)
	}
	updated, err := repo.Recommendation(context.Background(), rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "active" || updated.Quantity%100 != 0 || updated.ActivationPrice <= 10.1 {
		t.Fatalf("updated=%+v", updated)
	}
	if quotes.calls != 2 {
		t.Fatalf("quote calls=%d, want decision+execution refetch", quotes.calls)
	}
}

func TestLifecycleAPIFailureKeepsStateAndRetriesFifteenMinutesLater(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "pending", now.Add(-time.Hour), now, "")
	ai := &scriptedAI{errors: []error{errors.New("timeout")}}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, At: now}}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if updated.Status != "pending" || updated.NextCheckAt == nil || !updated.NextCheckAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("updated=%+v", updated)
	}
	var count int64
	if err := repo.DB().Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", rec.RecommendationID, "错误重试").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("error events=%d err=%v", count, err)
	}
}

func TestInvalidLifecycleReplyIsPersistedAndStateIsRetried(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "pending", now.Add(-time.Hour), now, "")
	ai := &scriptedAI{results: []CompletionResult{{Content: `{"action":"买入","reason":"越权动作"}`, ResponseID: "bad-response"}}}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: rec.StockCode, Name: rec.StockName, Market: "SH", Price: 10, At: now}}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if updated.Status != "pending" || updated.NextCheckAt == nil || !updated.NextCheckAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("invalid response changed lifecycle: %+v", updated)
	}
	messages, err := repo.Messages(context.Background(), rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[2].ResponseID != "bad-response" || !strings.Contains(messages[2].Content, "买入") {
		t.Fatalf("AI reply was not audited: %+v", messages)
	}
	if len(ai.requests) != 1 || len(ai.requests[0].Messages) < 2 {
		t.Fatalf("first remote chain was not seeded with isolated local context: %+v", ai.requests)
	}
}

func TestDueLifecycleDoesNotRunDuringLunch(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "pending", now.Add(-time.Hour), now.Add(-time.Minute), "")
	ai := &scriptedAI{results: []CompletionResult{{Content: `{"action":"激活","reason":"不应调用"}`}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 0 {
		t.Fatalf("lunch lifecycle invoked AI: %+v", ai.requests)
	}
	updated, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if updated.Status != "pending" {
		t.Fatalf("lunch changed status: %+v", updated)
	}
}

func TestPendingCarriesTradingBudgetAcrossDaysAndExpiresAtFourHours(t *testing.T) {
	repo := researchTestRepo(t)
	signal := time.Date(2026, 8, 14, 14, 30, 0, 0, shanghaiLocation)
	now := time.Date(2026, 8, 17, 14, 29, 0, 0, shanghaiLocation)
	due := now.Add(time.Minute)
	rec := seedRecommendation(t, repo, "pending", signal, due, "")
	ai := &scriptedAI{results: []CompletionResult{{Content: `{"action":"激活","reason":"不应调用"}`}}}
	service := NewService(repo, ai, &scriptedQuotes{}, weekdayTradingCalendar{})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if updated.Status != "pending" || len(ai.requests) != 0 {
		t.Fatalf("recommendation expired before four trading hours: %+v requests=%d", updated, len(ai.requests))
	}
	now = now.Add(time.Minute)
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, _ = repo.Recommendation(context.Background(), rec.RecommendationID)
	if updated.Status != "invalidated" || len(ai.requests) != 0 {
		t.Fatalf("recommendation did not expire at four trading hours: %+v requests=%d", updated, len(ai.requests))
	}
}

func TestActivationResponseCrossingCloseIsDeferred(t *testing.T) {
	repo := researchTestRepo(t)
	signal := time.Date(2026, 8, 17, 14, 30, 0, 0, shanghaiLocation)
	now := time.Date(2026, 8, 17, 14, 45, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "pending", signal, now, "")
	ai := &advancingAI{clock: &now, finishAt: time.Date(2026, 8, 17, 15, 5, 0, 0, shanghaiLocation), result: CompletionResult{Content: `{"action":"激活","reason":"条件满足"}`, ResponseID: "late-response"}}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, At: now}}}
	service := NewService(repo, ai, quotes, weekdayTradingCalendar{})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if updated.Status != "pending" || updated.PreviousResponseID != "late-response" || quotes.calls != 1 {
		t.Fatalf("late response was executed: recommendation=%+v quoteCalls=%d", updated, quotes.calls)
	}
	var deferred int64
	if err := repo.DB().Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", rec.RecommendationID, "响应跨休市").Count(&deferred).Error; err != nil || deferred != 1 {
		t.Fatalf("deferred events=%d err=%v", deferred, err)
	}
}

func TestSameDaySellBecomesPendingWithoutTrade(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.Add(-time.Hour), now, "")
	position := Position{RecommendationID: rec.RecommendationID, StockCode: rec.StockCode, StockName: rec.StockName, Market: "SH", Quantity: 100, EntryAt: now.Add(-time.Hour), EntryPrice: 10, BuyFees: 5, CurrentPrice: 10, Status: "open"}
	if err := repo.DB().Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{results: []CompletionResult{{Content: `{"action":"卖出","reason":"走弱"}`}}}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: rec.StockCode, Name: rec.StockName, Market: "SH", Price: 9.8, At: now}}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if updated.Status != "sell_pending" {
		t.Fatalf("status=%s", updated.Status)
	}
	var trades int64
	_ = repo.DB().Model(&SimulatedTrade{}).Where("recommendation_id = ?", rec.RecommendationID).Count(&trades).Error
	if trades != 0 {
		t.Fatalf("same-day sell created %d trades", trades)
	}
}

func TestCashCompetitionIsFirstComeFirstServed(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, shanghaiLocation)
	run := AnalysisRun{RunID: newID(), ScheduledFor: now, StartedAt: now, Status: "success"}
	if err := repo.CreateAnalysis(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	codes := []string{"sh600000", "sz000001", "sh600001"}
	recommendations := make([]Recommendation, 0, len(codes))
	for _, code := range codes {
		recommendation := Recommendation{RecommendationID: newID(), AnalysisRunID: run.RunID, StockCode: code, StockName: code, SignalAt: now, Status: "pending"}
		if err := repo.CreateRecommendation(context.Background(), &recommendation, nil); err != nil {
			t.Fatal(err)
		}
		recommendations = append(recommendations, recommendation)
	}
	for index := 0; index < 2; index++ {
		quote := Quote{Code: codes[index], Name: codes[index], Market: "SH", Price: 40, At: now.Add(time.Duration(index) * time.Second)}
		if err := repo.Buy(context.Background(), recommendations[index].RecommendationID, quote, now); err != nil {
			t.Fatalf("buy %d: %v", index, err)
		}
	}
	thirdQuote := Quote{Code: codes[2], Name: codes[2], Market: "SH", Price: 40, At: now.Add(2 * time.Second)}
	if err := repo.Buy(context.Background(), recommendations[2].RecommendationID, thirdQuote, now); err == nil || !strings.Contains(err.Error(), "insufficient cash") {
		t.Fatalf("third buy must lose deterministic cash race: %v", err)
	}
	account, err := repo.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Cash < 0 {
		t.Fatalf("account overdrawn: %.2f", account.Cash)
	}
}

func TestAccountOverviewUsesNetSellValue(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, shanghaiLocation)
	position := Position{RecommendationID: newID(), StockCode: "sh600000", StockName: "浦发银行", Market: "SH", Quantity: 1000, EntryAt: now.AddDate(0, 0, -1), EntryPrice: 10, CurrentPrice: 10, Status: "open"}
	if err := repo.DB().Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB().Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 90000.0).Error; err != nil {
		t.Fatal(err)
	}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: position.StockCode, Name: position.StockName, Market: "SH", Price: 11, At: now}}}
	service := NewService(repo, &scriptedAI{}, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	overview, err := service.AccountOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantPositionValue := CalculateSellCost(11, 1000).NetCashFlow
	wantNAV := 90000 + wantPositionValue
	if math.Abs(overview.PositionValue-wantPositionValue) > 1e-8 || math.Abs(overview.NetAssetValue-wantNAV) > 1e-8 {
		t.Fatalf("overview=%+v want position %.4f nav %.4f", overview, wantPositionValue, wantNAV)
	}
	if math.Abs(overview.NetYieldRate-(wantNAV-InitialCash)/InitialCash) > 1e-8 {
		t.Fatalf("net yield=%f", overview.NetYieldRate)
	}
}
