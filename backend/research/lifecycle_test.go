package research

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
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
		if strings.Contains(result.Content, `"action":"卖出"`) && !strings.Contains(result.Content, "sourceRefs") {
			refs := regexp.MustCompile(`OBS-[A-Z0-9]+-[QM]`).FindAllString(request.Prompt, -1)
			if len(refs) >= 2 {
				result.Content = strings.TrimSuffix(result.Content, "}") + `,"sourceRefs":["` + refs[0] + `","` + refs[1] + `"],"dataSufficiency":"充足"}`
			}
		}
	}
	if i < len(m.errors) {
		err = m.errors[i]
	}
	return result, err
}

type scriptedQuotes struct {
	quotes []Quote
	errors []error
	calls  int
}

func (m *scriptedQuotes) CurrentQuote(_ context.Context, _ string) (Quote, error) {
	i := m.calls
	m.calls++
	if i < len(m.errors) && m.errors[i] != nil {
		return Quote{}, m.errors[i]
	}
	if len(m.quotes) == 0 {
		return Quote{}, errors.New("no quote")
	}
	if i >= len(m.quotes) {
		i = len(m.quotes) - 1
	}
	return m.quotes[i], nil
}

type scriptedContexts struct {
	drafts   []LifecycleObservationDraft
	requests []LifecycleContextRequest
}

func (provider *scriptedContexts) CollectLifecycleContext(_ context.Context, request LifecycleContextRequest) (LifecycleObservationDraft, error) {
	provider.requests = append(provider.requests, request)
	index := len(provider.requests) - 1
	if len(provider.drafts) == 0 {
		return LifecycleObservationDraft{}, errors.New("no lifecycle context")
	}
	if index >= len(provider.drafts) {
		index = len(provider.drafts) - 1
	}
	draft := provider.drafts[index]
	for sourceIndex := range draft.Sources {
		suffix := LifecycleQuoteSourceSuffix
		if draft.Sources[sourceIndex].Category == "minute" {
			suffix = LifecycleMinuteSourceSuffix
		}
		if draft.Sources[sourceIndex].ID == "" {
			draft.Sources[sourceIndex].ID = LifecycleSourceID(request.ObservationID, suffix)
		}
	}
	return draft, nil
}

func readyLifecycleDraft(now time.Time, status string) LifecycleObservationDraft {
	return LifecycleObservationDraft{Status: status, Quote: Quote{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: now},
		MinuteSummary: MinuteEvidenceSummary{TradingDate: now.Format("2006-01-02"), LatestAt: now, LatestPrice: 10, TotalBars: 30},
		Sources: []LifecycleEvidenceSource{
			{Name: "实时行情", Category: "quote", Status: "ok", CollectedAt: now, Content: "quote"},
			{Name: "分钟量价", Category: "minute", Status: "ok", CollectedAt: now, Content: "minute"},
		}}
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
	result := a.result
	if strings.Contains(result.Content, `"action":"卖出"`) && !strings.Contains(result.Content, "sourceRefs") {
		refs := regexp.MustCompile(`OBS-[A-Z0-9]+-[QM]`).FindAllString(request.Prompt, -1)
		if len(refs) >= 2 {
			result.Content = strings.TrimSuffix(result.Content, "}") + `,"sourceRefs":["` + refs[0] + `","` + refs[1] + `"],"dataSufficiency":"充足"}`
		}
	}
	return result, nil
}

func researchTestRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&AnalysisRun{}, &Recommendation{}, &LifecycleMessage{}, &DecisionEvent{}, &LifecycleObservation{}, &SimulatedAccount{}, &SimulatedTrade{}, &Position{}); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)
	if err = repo.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repo
}

func seedRun(t *testing.T, repo *Repository, now time.Time) string {
	t.Helper()
	run := AnalysisRun{RunID: newID(), ScheduledFor: now, StartedAt: now, Status: "success"}
	if err := repo.CreateAnalysis(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	return run.RunID
}

func seedRecommendation(t *testing.T, repo *Repository, status string, signal, due time.Time, previous string) Recommendation {
	t.Helper()
	rec := Recommendation{RecommendationID: newID(), AnalysisRunID: seedRun(t, repo, signal), StockCode: "sh600000", StockName: "浦发银行", SignalAt: signal,
		AISummary: "量价与资金保持强势", MainRisk: "板块退潮", Status: status, PreviousResponseID: previous, NextCheckAt: &due}
	initial := []LifecycleMessage{{RecommendationID: rec.RecommendationID, Sequence: 1, Role: "system", Phase: "initial", Content: "ONLY:" + rec.RecommendationID, CreatedAt: signal}}
	if err := repo.CreateRecommendation(context.Background(), &rec, initial); err != nil {
		t.Fatal(err)
	}
	return rec
}

func seedOpenPosition(t *testing.T, repo *Repository, rec Recommendation, entry time.Time) {
	t.Helper()
	position := Position{RecommendationID: rec.RecommendationID, StockCode: rec.StockCode, StockName: rec.StockName, Market: "SH",
		Quantity: 100, EntryAt: entry, EntryPrice: 10.01, BuyFees: 5, CurrentPrice: 10, Status: "open"}
	if err := repo.DB().Create(&position).Error; err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueRecommendationBuysImmediatelyAndAnchorsNextTradingDay0950(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, shanghaiLocation)
	quote := Quote{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: now}
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{quotes: []Quote{quote}}, weekdayTradingCalendar{})
	service.now = func() time.Time { return now }
	rec := Recommendation{RecommendationID: newID(), AnalysisRunID: seedRun(t, repo, now), StockCode: quote.Code, StockName: quote.Name, SignalAt: now}
	if err := service.EnqueueRecommendation(context.Background(), &rec, nil); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if stored.Status != "active" || stored.Quantity == 0 || stored.NextCheckAt == nil || stored.NextCheckAt.Format("2006-01-02 15:04") != "2026-08-17 09:50" {
		t.Fatalf("stored=%+v", stored)
	}
	var trades, events int64
	_ = repo.DB().Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", rec.RecommendationID, "buy").Count(&trades).Error
	_ = repo.DB().Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", rec.RecommendationID, "模拟买入").Count(&events).Error
	if trades != 1 || events != 1 {
		t.Fatalf("trades=%d events=%d", trades, events)
	}
}

func TestAfterCloseBuyQueuesForNextOpenAndFailsOnlyOnce(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 15, 30, 0, 0, shanghaiLocation)
	quotes := &scriptedQuotes{quotes: []Quote{{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, At: time.Date(2026, 8, 17, 9, 30, 0, 0, shanghaiLocation), LimitUp: true}}}
	service := NewService(repo, &scriptedAI{}, quotes, weekdayTradingCalendar{})
	service.now = func() time.Time { return now }
	rec := Recommendation{RecommendationID: newID(), AnalysisRunID: seedRun(t, repo, now), StockCode: "sh600000", StockName: "浦发银行", SignalAt: now}
	if err := service.EnqueueRecommendation(context.Background(), &rec, nil); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if stored.Status != "buy_pending" || stored.NextCheckAt.Format("2006-01-02 15:04") != "2026-08-17 09:30" || quotes.calls != 0 {
		t.Fatalf("queued=%+v calls=%d", stored, quotes.calls)
	}
	now = time.Date(2026, 8, 17, 9, 30, 0, 0, shanghaiLocation)
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _ = repo.Recommendation(context.Background(), rec.RecommendationID)
	if stored.Status != "missed_untradable" || stored.NextCheckAt != nil || quotes.calls != 1 {
		t.Fatalf("failed=%+v calls=%d", stored, quotes.calls)
	}
	now = now.Add(15 * time.Minute)
	_ = service.ProcessDue(context.Background())
	if quotes.calls != 1 {
		t.Fatalf("one-shot buy retried %d times", quotes.calls)
	}
}

func TestDirectBuyCashCompetitionUsesSignalOrder(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, shanghaiLocation)
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{quotes: []Quote{
		{Code: "sh600000", Name: "甲", Market: "SH", Price: 40, At: now}, {Code: "sz000001", Name: "乙", Market: "SZ", Price: 40, At: now},
		{Code: "sh600001", Name: "丙", Market: "SH", Price: 40, At: now},
	}}, weekdayTradingCalendar{})
	service.now = func() time.Time { return now }
	runID := seedRun(t, repo, now)
	var statuses []string
	for index, code := range []string{"sh600000", "sz000001", "sh600001"} {
		rec := Recommendation{RecommendationID: newID(), AnalysisRunID: runID, StockCode: code, StockName: []string{"甲", "乙", "丙"}[index], SignalAt: now.Add(time.Duration(index) * time.Millisecond)}
		if err := service.EnqueueRecommendation(context.Background(), &rec, nil); err != nil {
			t.Fatal(err)
		}
		stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
		statuses = append(statuses, stored.Status)
	}
	if statuses[0] != "active" || statuses[1] != "active" || statuses[2] != "missed_cash" {
		t.Fatalf("statuses=%v", statuses)
	}
}

func TestListRecommendationsUsesNetCashAmounts(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, shanghaiLocation)
	openRecommendation := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -1), now, "")
	if err := repo.DB().Model(&Recommendation{}).Where("recommendation_id = ?", openRecommendation.RecommendationID).
		Updates(map[string]any{"activated_at": now.AddDate(0, 0, -1), "activation_price": 10.0, "quantity": 100}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB().Create(&SimulatedTrade{TradeID: newID(), RecommendationID: openRecommendation.RecommendationID,
		StockCode: openRecommendation.StockCode, Side: "buy", TradedAt: now.AddDate(0, 0, -1), Quantity: 100, NetCashFlow: -1005}).Error; err != nil {
		t.Fatal(err)
	}
	seedOpenPosition(t, repo, openRecommendation, now.AddDate(0, 0, -1))
	if err := repo.DB().Model(&Position{}).Where("recommendation_id = ?", openRecommendation.RecommendationID).
		Update("current_price", 11.0).Error; err != nil {
		t.Fatal(err)
	}

	closedRecommendation := seedRecommendation(t, repo, "closed", now.AddDate(0, 0, -2), now, "")
	closedAt := now.Add(-time.Hour)
	if err := repo.DB().Model(&Recommendation{}).Where("recommendation_id = ?", closedRecommendation.RecommendationID).
		Updates(map[string]any{"activated_at": now.AddDate(0, 0, -2), "activation_price": 10.0, "quantity": 100, "closed_at": closedAt}).Error; err != nil {
		t.Fatal(err)
	}
	closedTrades := []SimulatedTrade{
		{TradeID: newID(), RecommendationID: closedRecommendation.RecommendationID, StockCode: closedRecommendation.StockCode,
			Side: "buy", TradedAt: now.AddDate(0, 0, -2), Quantity: 100, NetCashFlow: -1005},
		{TradeID: newID(), RecommendationID: closedRecommendation.RecommendationID, StockCode: closedRecommendation.StockCode,
			Side: "sell", TradedAt: closedAt, Quantity: 100, NetCashFlow: 1088},
	}
	if err := repo.DB().Create(&closedTrades).Error; err != nil {
		t.Fatal(err)
	}

	rows, err := repo.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Recommendation, len(rows))
	for _, row := range rows {
		byID[row.RecommendationID] = row
	}
	openRow := byID[openRecommendation.RecommendationID]
	wantCurrent := CalculateSellCost(11, 100).NetCashFlow
	if openRow.BuyAmount != 1005 || math.Abs(openRow.CurrentAmount-wantCurrent) > 0.001 || openRow.SellAmount != 0 {
		t.Fatalf("open amounts=%+v wantCurrent=%.4f", openRow, wantCurrent)
	}
	if math.Abs(openRow.NetYieldRate-(wantCurrent-1005)/1005) > 0.000001 {
		t.Fatalf("open net yield=%f", openRow.NetYieldRate)
	}
	closedRow := byID[closedRecommendation.RecommendationID]
	if closedRow.BuyAmount != 1005 || closedRow.SellAmount != 1088 || closedRow.CurrentAmount != 0 || closedRow.NetPnL != 83 {
		t.Fatalf("closed amounts=%+v", closedRow)
	}
	if math.Abs(closedRow.NetYieldRate-83.0/1005.0) > 0.000001 {
		t.Fatalf("closed net yield=%f", closedRow.NetYieldRate)
	}

	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{quotes: []Quote{{
		Code: openRecommendation.StockCode, Name: openRecommendation.StockName, Market: "SH", Price: 11, At: now,
	}}}, weekdayTradingCalendar{})
	openDetail, err := service.Detail(context.Background(), openRecommendation.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if openDetail.Recommendation.BuyAmount != openRow.BuyAmount ||
		math.Abs(openDetail.Recommendation.CurrentAmount-openRow.CurrentAmount) > 0.001 ||
		math.Abs(openDetail.Recommendation.NetYieldRate-openRow.NetYieldRate) > 0.000001 {
		t.Fatalf("open detail/list mismatch: detail=%+v list=%+v", openDetail.Recommendation, openRow)
	}
	closedDetail, err := service.Detail(context.Background(), closedRecommendation.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if closedDetail.Recommendation.BuyAmount != closedRow.BuyAmount ||
		closedDetail.Recommendation.SellAmount != closedRow.SellAmount ||
		math.Abs(closedDetail.Recommendation.NetYieldRate-closedRow.NetYieldRate) > 0.000001 {
		t.Fatalf("closed detail/list mismatch: detail=%+v list=%+v", closedDetail.Recommendation, closedRow)
	}
}

func TestHoldingFallbackUsesOnlyStockHistoryAndFixedNextSlot(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 17, 9, 50, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -3), now, "response-old")
	seedOpenPosition(t, repo, rec, now.AddDate(0, 0, -3))
	ai := &scriptedAI{results: []CompletionResult{{}, {Content: `{"action":"持有","reason":"量价仍稳","sourceRefs":[],"dataSufficiency":"充足"}`, ResponseID: "response-new"}}, errors: []error{errors.New("relay rejects previous_response_id"), nil}}
	contexts := &scriptedContexts{drafts: []LifecycleObservationDraft{readyLifecycleDraft(now, "ready")}}
	service := NewService(repo, ai, &scriptedQuotes{}, weekdayTradingCalendar{}, contexts)
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 2 || ai.requests[0].PreviousResponseID != "response-old" || ai.requests[1].PreviousResponseID != "" || len(ai.requests[1].Messages) == 0 {
		t.Fatalf("requests=%+v", ai.requests)
	}
	for _, message := range ai.requests[1].Messages {
		if message.RecommendationID != rec.RecommendationID {
			t.Fatalf("cross-stock memory: %+v", message)
		}
	}
	stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if stored.Status != "active" || stored.NextCheckAt.Format("15:04") != "10:05" {
		t.Fatalf("stored=%+v", stored)
	}
}

func TestMigratedPositionNominalWeekendDueRunsAtStrictMonday0950(t *testing.T) {
	repo := researchTestRepo(t)
	entry := time.Date(2026, 8, 14, 14, 30, 0, 0, shanghaiLocation) // Friday.
	nominalSaturday := time.Date(2026, 8, 15, 9, 50, 0, 0, shanghaiLocation)
	now := time.Date(2026, 8, 17, 9, 50, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", entry, nominalSaturday, "")
	seedOpenPosition(t, repo, rec, entry)
	ai := &scriptedAI{results: []CompletionResult{{Content: `{"action":"持有","reason":"量价仍稳","sourceRefs":[],"dataSufficiency":"充足"}`}}}
	contexts := &scriptedContexts{drafts: []LifecycleObservationDraft{readyLifecycleDraft(now, "ready")}}
	service := NewService(repo, ai, &scriptedQuotes{}, weekdayTradingCalendar{}, contexts)
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ai.requests) != 1 {
		t.Fatalf("strict Monday 09:50 should run exactly once, requests=%d", len(ai.requests))
	}
	stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if stored.NextCheckAt == nil || stored.NextCheckAt.Format("2006-01-02 15:04") != "2026-08-17 10:05" {
		t.Fatalf("next=%v", stored.NextCheckAt)
	}
}

func TestCriticalHoldingDataSkipsAIAndUsesNextFixedSlot(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 18, 10, 5, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -1), now, "")
	seedOpenPosition(t, repo, rec, now.AddDate(0, 0, -1))
	contexts := &scriptedContexts{drafts: []LifecycleObservationDraft{{Status: "critical_failed", CriticalFailure: "分钟量价不可用"}}}
	ai := &scriptedAI{}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{}, contexts)
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if len(ai.requests) != 0 || stored.NextCheckAt.Format("15:04") != "10:20" {
		t.Fatalf("requests=%d stored=%+v", len(ai.requests), stored)
	}
}

func TestSaleRequiresLatestQuoteAndMinuteCitations(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 5, 0, 0, shanghaiLocation)
	request := LifecycleContextRequest{ObservationID: "12345678-1234-1234-1234-123456789012", Recommendation: Recommendation{RecommendationID: "rec"}, Phase: "holding", Now: now, WindowFrom: now}
	observation, err := NewLifecycleObservation(request, readyLifecycleDraft(now, "ready"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"卖出": true}
	if _, err = parseLifecycleDecision(`{"action":"卖出","reason":"转弱","sourceRefs":[],"dataSufficiency":"充足"}`, allowed, observation); err == nil {
		t.Fatal("sale without citations was accepted")
	}
	quoteID := LifecycleSourceID(request.ObservationID, LifecycleQuoteSourceSuffix)
	minuteID := LifecycleSourceID(request.ObservationID, LifecycleMinuteSourceSuffix)
	content := fmt.Sprintf(`{"action":"卖出","reason":"转弱","sourceRefs":[%q,%q],"dataSufficiency":"充足"}`, quoteID, minuteID)
	if _, err = parseLifecycleDecision(content, allowed, observation); err != nil {
		t.Fatalf("valid citations rejected: %v", err)
	}
}

func TestLifecyclePromptRetainsSourcesAndOmitsActivation(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 5, 0, 0, shanghaiLocation)
	request := LifecycleContextRequest{ObservationID: "abcdef12-1234-1234-1234-123456789012", Recommendation: Recommendation{RecommendationID: "rec"}, Phase: "holding", Now: now, WindowFrom: now.Add(-15 * time.Minute)}
	draft := readyLifecycleDraft(now, "ready")
	for index := 0; index < 12; index++ {
		draft.Sources = append(draft.Sources, LifecycleEvidenceSource{ID: fmt.Sprintf("OPTIONAL-%02d", index), Name: "来源", Category: "news", Status: "ok", Content: strings.Repeat("资讯", 5000), CollectedAt: now})
	}
	observation, err := NewLifecycleObservation(request, draft)
	if err != nil {
		t.Fatal(err)
	}
	prompt := lifecyclePrompt(Recommendation{StockCode: "sh600000", StockName: "浦发银行", ActivationCondition: "旧条件"}, now, observation, &Position{})
	for _, source := range ParseLifecycleEvidence(observation) {
		if !strings.Contains(prompt, "["+source.ID+"]") {
			t.Fatalf("prompt omitted source %s", source.ID)
		}
	}
	if strings.Contains(prompt, "旧条件") || strings.Contains(prompt, "激活条件") {
		t.Fatalf("prompt leaked activation: %s", prompt)
	}
}

func TestLateHoldingResponseCrossingCloseIsDeferred(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 17, 14, 45, 0, 0, shanghaiLocation)
	rec := seedRecommendation(t, repo, "active", now.AddDate(0, 0, -3), now, "")
	seedOpenPosition(t, repo, rec, now.AddDate(0, 0, -3))
	ai := &advancingAI{clock: &now, finishAt: time.Date(2026, 8, 17, 15, 5, 0, 0, shanghaiLocation), result: CompletionResult{Content: `{"action":"卖出","reason":"走弱"}`, ResponseID: "late-response"}}
	service := NewService(repo, ai, &scriptedQuotes{}, weekdayTradingCalendar{}, &scriptedContexts{drafts: []LifecycleObservationDraft{readyLifecycleDraft(now, "ready")}})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if stored.Status != "active" || stored.PreviousResponseID != "late-response" || stored.NextCheckAt.Format("2006-01-02 15:04") != "2026-08-18 09:50" {
		t.Fatalf("stored=%+v", stored)
	}
}

func TestStaleOverdueSellCheckIsNotReplayed(t *testing.T) {
	repo := researchTestRepo(t)
	due := time.Date(2026, 8, 18, 10, 5, 0, 0, shanghaiLocation)
	now := due.Add(3 * time.Minute)
	rec := seedRecommendation(t, repo, "active", due.AddDate(0, 0, -1), due, "")
	seedOpenPosition(t, repo, rec, due.AddDate(0, 0, -1))
	ai := &scriptedAI{results: []CompletionResult{{Content: `{"action":"卖出","reason":"不应补跑"}`}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{}, &scriptedContexts{drafts: []LifecycleObservationDraft{readyLifecycleDraft(now, "ready")}})
	service.now = func() time.Time { return now }
	if err := service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Recommendation(context.Background(), rec.RecommendationID)
	if len(ai.requests) != 0 || stored.NextCheckAt.Format("15:04") != "10:20" {
		t.Fatalf("requests=%d stored=%+v", len(ai.requests), stored)
	}
}

func TestAccountOverviewUsesNetSellValue(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, shanghaiLocation)
	position := Position{RecommendationID: newID(), StockCode: "sh600000", StockName: "浦发银行", Market: "SH", Quantity: 1000,
		EntryAt: now.AddDate(0, 0, -1), EntryPrice: 10, CurrentPrice: 10, Status: "open"}
	if err := repo.DB().Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	_ = repo.DB().Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 90000.0).Error
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{quotes: []Quote{{Code: position.StockCode, Name: position.StockName, Market: "SH", Price: 11, At: now}}}, openCalendar{})
	service.now = func() time.Time { return now }
	overview, err := service.AccountOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantPositionValue := CalculateSellCost(11, 1000).NetCashFlow
	if math.Abs(overview.PositionValue-wantPositionValue) > 1e-8 || math.Abs(overview.NetAssetValue-(90000+wantPositionValue)) > 1e-8 {
		t.Fatalf("overview=%+v", overview)
	}
}
