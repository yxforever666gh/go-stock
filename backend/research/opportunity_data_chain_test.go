package research

import (
	"context"
	"errors"
	"testing"
	"time"

	sharedai "go-stock/backend/ai"
	"go-stock/internal/marketquote"
	"go-stock/internal/researchevidence"
)

type failAtCalendar struct{ at time.Time }

func (calendar failAtCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	if value.Equal(calendar.at) {
		return false, errors.New("calendar unavailable")
	}
	return true, nil
}

func TestClassifyDecisionQuoteEnforcesIdentityAndPointInTimeFreshness(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, shanghaiLocation)
	valid := marketquote.Quote{Code: "sh600000", Name: "Alpha", Price: 10, At: now.Add(-time.Minute)}
	if got := classifyDecisionQuote(now, "sh600000", "Alpha", valid, nil); got.status != "ok" {
		t.Fatalf("valid quote=%+v", got)
	}
	stale := valid
	stale.At = now.Add(-time.Minute - time.Second)
	if got := classifyDecisionQuote(now, "sh600000", "Alpha", stale, nil); got.status != "stale" {
		t.Fatalf("stale quote=%+v", got)
	}
	future := valid
	future.At = now.Add(6 * time.Second)
	if got := classifyDecisionQuote(now, "sh600000", "Alpha", future, nil); got.status != "stale" {
		t.Fatalf("future quote=%+v", got)
	}
	wrong := valid
	wrong.Code = "sz000001"
	if got := classifyDecisionQuote(now, "sh600000", "Alpha", wrong, nil); got.status != "invalid" {
		t.Fatalf("wrong quote=%+v", got)
	}
	if got := classifyDecisionQuote(now, "sh600000", "Alpha", marketquote.Quote{}, errors.New("down")); got.status != "unavailable" {
		t.Fatalf("unavailable quote=%+v", got)
	}
}

func TestStockPromptRevalidatesInternalMarketTimeAtStageBoundary(t *testing.T) {
	cutoff := time.Date(2026, 9, 3, 14, 20, 0, 0, shanghaiLocation)
	available := cutoff
	sources := []researchevidence.SourceDocument{{SourceID: "S1", SourceName: "Tencent分钟K sh600000", CollectedAt: cutoff.Add(-4 * time.Minute), AvailableAt: &available,
		Content: `{"asOf":"2026-09-03T14:16:00+08:00"}`, PromptContent: `{"asOf":"2026-09-03T14:16:00+08:00"}`}}
	filtered := sourcesAvailableAtCutoff(sources, cutoff, false)
	if len(filtered) != 1 || filtered[0].Error == "" || filtered[0].PromptContent != "" {
		t.Fatalf("stale internal market timestamp reached prompt: %+v", filtered)
	}
}

func TestNextOpportunityReanalysisMovesLunchAndCloseToValidWindows(t *testing.T) {
	date := time.Date(2026, 9, 3, 11, 20, 0, 0, shanghaiLocation)
	lunch, err := nextOpportunityReanalysisAt(context.Background(), openCalendar{}, date.Add(30*time.Minute))
	if err != nil || lunch.Hour() != 13 || lunch.Minute() != 0 {
		t.Fatalf("lunch reanalysis=%v err=%v", lunch, err)
	}
	afterClose := time.Date(2026, 9, 3, 14, 10, 0, 0, shanghaiLocation).Add(30 * time.Minute)
	nextDay, err := nextOpportunityReanalysisAt(context.Background(), openCalendar{}, afterClose)
	if err != nil || nextDay.Day() != 4 || nextDay.Hour() != 9 || nextDay.Minute() != 35 {
		t.Fatalf("next-day reanalysis=%v err=%v", nextDay, err)
	}
}

func TestImmediateQuoteCannotBecomeAnOvernightPendingBuy(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 3, 15, 0, 30, 0, shanghaiLocation)
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	recommendation := Recommendation{RecommendationID: "after-close", AnalysisRunID: "run", StockCode: "sh600000", StockName: "Alpha", SignalAt: now}
	quote := marketquote.Quote{Code: "sh600000", Name: "Alpha", Market: "SH", Price: 10, At: now}
	if err := service.EnqueueRecommendation(context.Background(), &recommendation, nil, quote); !errors.Is(err, ErrExecutionWindowClosed) {
		t.Fatalf("after-close immediate admission error=%v", err)
	}
	var count int64
	if err := repo.DB().Model(&Recommendation{}).Where("recommendation_id = ?", recommendation.RecommendationID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("after-close immediate quote created pending recommendation: count=%d err=%v", count, err)
	}
}

func TestEventAdmissionHonorsCapitalDeploymentDeadlineInsideTradingSession(t *testing.T) {
	repo := researchTestRepo(t)
	decisionAt := time.Date(2026, 9, 3, 14, 24, 59, 0, shanghaiLocation)
	now := decisionAt.Add(2 * time.Second)
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	recommendation := Recommendation{RecommendationID: "after-policy-cutoff", AnalysisRunID: "run", StockCode: "sh600000", StockName: "Alpha", SignalAt: decisionAt}
	quote := marketquote.Quote{Code: "sh600000", Name: "Alpha", Market: "SH", Price: 10, At: decisionAt}
	err := service.EnqueueRecommendationBefore(context.Background(), &recommendation, nil, capitalDeploymentWindowDeadline(decisionAt), quote)
	if !errors.Is(err, ErrExecutionWindowClosed) {
		t.Fatalf("post-cutoff event admission error=%v", err)
	}
}

func TestActiveWaitQueryOnlyReturnsDueRowsForSuccessorAnalysis(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, shanghaiLocation)
	dueAt, futureAt := now.Add(-time.Minute), now.Add(20*time.Minute)
	rows := []BuyOpportunity{
		{OpportunityID: "due", AnalysisRunID: "old", Action: OpportunityActionWait, StockCode: "sh600000", StockName: "Alpha", Status: "active", ReanalysisAt: &dueAt, CreatedAt: now.Add(-time.Hour)},
		{OpportunityID: "future", AnalysisRunID: "old", Action: OpportunityActionWait, StockCode: "sz000001", StockName: "Bravo", Status: "active", ReanalysisAt: &futureAt, CreatedAt: now.Add(-time.Hour)},
	}
	for index := range rows {
		if err := repo.CreateBuyOpportunity(context.Background(), &rows[index]); err != nil {
			t.Fatal(err)
		}
	}
	due, err := repo.ActiveWaitOpportunities(context.Background(), now, 10)
	if err != nil || len(due) != 1 || due[0].OpportunityID != "due" {
		t.Fatalf("due waits=%+v err=%v", due, err)
	}
	earliest, err := repo.EarliestActiveWaitReanalysis(context.Background())
	if err != nil || earliest == nil || !earliest.Equal(dueAt) {
		t.Fatalf("earliest wait=%v err=%v", earliest, err)
	}
}

func TestWaitOutsideRangeAndBuyOutsideRangeRemainReanalysisCandidates(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, shanghaiLocation)
	sector := `{"analysis":"sector","directions":["bank"],"candidates":[{"code":"sh600000","name":"Alpha"},{"code":"sz000001","name":"Bravo"}]}`
	stock := `{"analysis":"stocks","shortlist":[{"stockName":"Alpha","stockCode":"sh600000","aiSummary":"A","mainRisk":"R","sourceRefs":"S1"},{"stockName":"Bravo","stockCode":"sz000001","aiSummary":"B","mainRisk":"R","sourceRefs":"S2"}]}`
	final := `{"analysis":"wait","opportunities":[{"action":"wait","stockName":"Alpha","stockCode":"sh600000","priceLow":9,"priceHigh":10,"aiSummary":"A","timingReason":"pullback","mainRisk":"R","sourceRefs":"S1"},{"action":"buy_now","stockName":"Bravo","stockCode":"sz000001","priceLow":9,"priceHigh":10,"aiSummary":"B","timingReason":"breakout","mainRisk":"R","sourceRefs":"S2"}]}`
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "market"}, {Content: sector}, {Content: stock}, {Content: final}}}
	quotes := &scriptedQuotes{quotes: []marketquote.Quote{
		{Code: "sh600000", Name: "Alpha", Market: "SH", Price: 12, At: now},
		{Code: "sz000001", Name: "Bravo", Market: "SZ", Price: 12, At: now},
	}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeEvent, ReanalysisInterval: 30 * time.Minute})
	if err != nil || run.WaitCount != 2 || run.RecommendationCount != 0 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	rows, err := repo.BuyOpportunitiesForRun(context.Background(), run.RunID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.Action != OpportunityActionWait || row.Status != "active" || row.DecisionQuoteStatus != "ok" || row.QuotePrice != 12 || row.ReanalysisAt == nil || row.DataProfileVersion != CurrentDataProfileVersion {
			t.Fatalf("wait row=%+v", row)
		}
	}
	if rows[0].RequestedAction != OpportunityActionWait || rows[1].RequestedAction != OpportunityActionBuyNow {
		t.Fatalf("requested actions were not retained: %+v", rows)
	}
}

func TestFailedSuccessorKeepsWaitAndSuccessfulSuccessorSupersedesIt(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, shanghaiLocation)
	prior := BuyOpportunity{OpportunityID: "prior", AnalysisRunID: "old", RequestedAction: OpportunityActionWait,
		Action: OpportunityActionWait, StockCode: "sh600000", StockName: "Alpha", Status: "active", CreatedAt: now.Add(-time.Hour)}
	if err := repo.CreateBuyOpportunity(context.Background(), &prior); err != nil {
		t.Fatal(err)
	}
	failedService := NewService(repo, &scriptedAI{errors: []error{errors.New("model down")}}, &scriptedQuotes{}, openCalendar{})
	failedService.now = func() time.Time { return now }
	if _, err := NewAnalysisRunner(failedService, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeEvent}); err == nil {
		t.Fatal("failed successor unexpectedly succeeded")
	}
	active, _ := repo.ActiveWaitOpportunities(context.Background(), time.Time{}, 10)
	if len(active) != 1 || active[0].OpportunityID != "prior" {
		t.Fatalf("failed successor consumed wait: %+v", active)
	}

	sector := `{"analysis":"none","directions":[],"candidates":[]}`
	stock := `{"analysis":"old wait invalid","shortlist":[]}`
	final := `{"analysis":"no buy","opportunities":[]}`
	successAI := &scriptedAI{results: []sharedai.CompletionResult{{Content: "market"}, {Content: sector}, {Content: stock}, {Content: final}}}
	successService := NewService(repo, successAI, &scriptedQuotes{}, openCalendar{})
	successService.now = func() time.Time { return now.Add(time.Minute) }
	run, err := NewAnalysisRunner(successService, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeEvent})
	if err != nil || run.Status != "no_recommendation" {
		t.Fatalf("successor=%+v err=%v", run, err)
	}
	var stored BuyOpportunity
	if err := repo.DB().Where("opportunity_id = ?", "prior").First(&stored).Error; err != nil || stored.Status != "superseded" || stored.SupersededByRunID != run.RunID {
		t.Fatalf("prior wait=%+v err=%v", stored, err)
	}
}

func TestSoftFailedStockBatchDoesNotSupersedeUnreviewedWait(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, shanghaiLocation)
	prior := BuyOpportunity{OpportunityID: "unreviewed", AnalysisRunID: "old", RequestedAction: OpportunityActionWait,
		Action: OpportunityActionWait, StockCode: "sh600000", StockName: "Alpha", Status: "active", CreatedAt: now.Add(-time.Hour)}
	if err := repo.CreateBuyOpportunity(context.Background(), &prior); err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{
		results: []sharedai.CompletionResult{{Content: "market"}, {Content: `{"analysis":"none","directions":[],"candidates":[]}`}, {}, {Content: `{"analysis":"no buy","opportunities":[]}`}},
		errors:  []error{nil, nil, errors.New("stock model down"), nil},
	}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeEvent})
	if err != nil || run.Status != "no_recommendation" {
		t.Fatalf("soft-failed run=%+v err=%v", run, err)
	}
	active, err := repo.ActiveWaitOpportunities(context.Background(), time.Time{}, 10)
	if err != nil || len(active) != 1 || active[0].OpportunityID != "unreviewed" {
		t.Fatalf("unreviewed wait was consumed: %+v err=%v", active, err)
	}
}

func TestExecutionFailureAfterBuyPersistsPartialSuccessInsteadOfFailedRun(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, shanghaiLocation)
	sector := `{"analysis":"sector","directions":["bank"],"candidates":[{"code":"sh600000","name":"Alpha"},{"code":"sz000001","name":"Bravo"}]}`
	stock := `{"analysis":"stocks","shortlist":[{"stockName":"Alpha","stockCode":"sh600000","aiSummary":"A","mainRisk":"R","sourceRefs":"S1"},{"stockName":"Bravo","stockCode":"sz000001","aiSummary":"B","mainRisk":"R","sourceRefs":"S2"}]}`
	final := `{"analysis":"mixed","opportunities":[{"action":"buy_now","stockName":"Alpha","stockCode":"sh600000","priceLow":9,"priceHigh":11,"aiSummary":"A","timingReason":"now","mainRisk":"R","sourceRefs":"S1"},{"action":"wait","stockName":"Bravo","stockCode":"sz000001","priceLow":9,"priceHigh":11,"aiSummary":"B","timingReason":"later","mainRisk":"R","sourceRefs":"S2"}]}`
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "market"}, {Content: sector}, {Content: stock}, {Content: final}}}
	quotes := &scriptedQuotes{quotes: []marketquote.Quote{{Code: "sh600000", Name: "Alpha", Market: "SH", Price: 10, At: now}, {Code: "sz000001", Name: "Bravo", Market: "SZ", Price: 10, At: now}}}
	calendar := failAtCalendar{at: now.Add(30 * time.Minute)}
	service := NewService(repo, ai, quotes, calendar)
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeManual})
	if err != nil || run.Status != "partial_success" || run.RecommendationCount != 1 {
		t.Fatalf("partial run=%+v err=%v", run, err)
	}
	var positions int64
	if err := repo.DB().Model(&Position{}).Where("status = ?", "open").Count(&positions).Error; err != nil || positions != 1 {
		t.Fatalf("persisted positions=%d err=%v", positions, err)
	}
}
