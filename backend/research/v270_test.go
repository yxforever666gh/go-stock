package research

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

type clockAdvancingAI struct {
	results        []CompletionResult
	calls          int
	now            *time.Time
	advanceAtPhase string
	advanceTo      time.Time
}

func (client *clockAdvancingAI) Complete(_ context.Context, request CompletionRequest) (CompletionResult, error) {
	index := client.calls
	client.calls++
	if request.Phase == client.advanceAtPhase && client.now != nil {
		*client.now = client.advanceTo
	}
	if index >= len(client.results) {
		return CompletionResult{}, errors.New("missing scripted result")
	}
	return client.results[index], nil
}

func TestCapitalDeploymentCapacityKeepsConfiguredBuffer(t *testing.T) {
	repo := researchTestRepo(t)
	ctx := context.Background()
	if err := repo.DB().Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 500000.0).Error; err != nil {
		t.Fatal(err)
	}
	capacity, err := repo.RecommendationCapacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.NetAssetValue != 500000 || capacity.CapitalBuffer != 50000 || capacity.DeployableCash != 450000 || capacity.AvailableSlots != 9 {
		t.Fatalf("default capacity=%+v", capacity)
	}
	repo.SetCapitalDeploymentPolicy(0.80, 1)
	capacity, err = repo.RecommendationCapacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(capacity.CapitalBuffer-100000) > 1e-7 || capacity.DeployableCash != 400000 || capacity.AvailableSlots != 8 {
		t.Fatalf("configured capacity=%+v", capacity)
	}
}

func TestAnalysisTriggerBatchDebouncesSalesAndClaimsOnce(t *testing.T) {
	repo := researchTestRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiLocation)
	if _, err := repo.EnqueueAnalysisTrigger(ctx, TriggerSourceSell, "trade-1", "first sale", base); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnqueueAnalysisTrigger(ctx, TriggerSourceSell, "trade-2", "second sale", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnqueueAnalysisTrigger(ctx, TriggerSourceSell, "trade-3", "third sale", base.Add(150*time.Second)); err != nil {
		t.Fatal(err)
	}
	if claim, ok, err := repo.ClaimAnalysisTriggerBatch(ctx, base.Add(3*time.Minute), "runtime-a", time.Minute); err != nil || ok {
		t.Fatalf("debounce claim=%+v ok=%t err=%v", claim, ok, err)
	}
	claim, ok, err := repo.ClaimAnalysisTriggerBatch(ctx, base.Add(270*time.Second), "runtime-a", time.Minute)
	if err != nil || !ok || len(claim.Triggers) != 3 || claim.Run.Status != "queued" {
		t.Fatalf("claim=%+v ok=%t err=%v", claim, ok, err)
	}
	if second, secondOK, secondErr := repo.ClaimAnalysisTriggerBatch(ctx, base.Add(270*time.Second), "runtime-b", time.Minute); secondErr != nil || secondOK {
		t.Fatalf("second claim=%+v ok=%t err=%v", second, secondOK, secondErr)
	}
	if err := repo.CompleteAnalysisTriggerBatch(ctx, claim.Run.RunID, "runtime-a", base.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var completed int64
	if err := repo.DB().Model(&AnalysisTrigger{}).Where("status = ?", TriggerStatusCompleted).Count(&completed).Error; err != nil || completed != 3 {
		t.Fatalf("completed=%d err=%v", completed, err)
	}
}

func TestAnalysisTriggerTechnicalFailureBackoffAndTerminalAttempt(t *testing.T) {
	repo := researchTestRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiLocation)
	if _, err := repo.EnqueueAnalysisTrigger(ctx, TriggerSourceCapitalGap, "gap-1", "cash gap", base); err != nil {
		t.Fatal(err)
	}
	now := base
	for attempt, delay := range []time.Duration{5 * time.Minute, 10 * time.Minute, 0} {
		claim, ok, err := repo.ClaimAnalysisTriggerBatch(ctx, now, "runtime", time.Minute)
		if err != nil || !ok {
			t.Fatalf("attempt %d claim ok=%t err=%v", attempt+1, ok, err)
		}
		failureAt := now.Add(time.Second)
		if err := repo.FailAnalysisTriggerBatch(ctx, claim.Run.RunID, "runtime", failureAt, errors.New("provider timeout")); err != nil {
			t.Fatal(err)
		}
		var trigger AnalysisTrigger
		if err := repo.DB().Where("source = ? AND source_key = ?", TriggerSourceCapitalGap, "gap-1").First(&trigger).Error; err != nil {
			t.Fatal(err)
		}
		if attempt < 2 {
			if trigger.Status != TriggerStatusQueued || !trigger.AvailableAt.Equal(failureAt.Add(delay)) {
				t.Fatalf("attempt %d trigger=%+v", attempt+1, trigger)
			}
			now = trigger.AvailableAt
		} else if trigger.Status != TriggerStatusFailed || trigger.CompletedAt == nil {
			t.Fatalf("terminal trigger=%+v", trigger)
		}
	}
}

func TestSellAtomicallyCreatesExecutionDecisionAndFundingTrigger(t *testing.T) {
	repo := researchTestRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiLocation)
	runID := seedRun(t, repo, now.AddDate(0, 0, -2))
	recommendation := Recommendation{RecommendationID: "sell-atomic", AnalysisRunID: runID, StockCode: "sh600000", StockName: "浦发银行",
		SignalAt: now.AddDate(0, 0, -2), Status: "active"}
	if err := repo.DB().Create(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	position := Position{RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode, StockName: recommendation.StockName,
		Market: "SH", Quantity: 1000, EntryAt: now.AddDate(0, 0, -2), EntryPrice: 10, BuyFees: 5, CurrentPrice: 10, Status: "open"}
	if err := repo.DB().Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	quote := Quote{Code: recommendation.StockCode, Name: recommendation.StockName, Market: "SH", Price: 11, PreviousClose: 10.5, At: now}
	if err := repo.Sell(ctx, recommendation.RecommendationID, quote); err != nil {
		t.Fatal(err)
	}
	var decisions, triggers, trades int64
	_ = repo.DB().Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", recommendation.RecommendationID, "模拟卖出").Count(&decisions).Error
	_ = repo.DB().Model(&AnalysisTrigger{}).Where("source = ? AND source_recommendation_id = ?", TriggerSourceSell, recommendation.RecommendationID).Count(&triggers).Error
	_ = repo.DB().Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", recommendation.RecommendationID, "sell").Count(&trades).Error
	if decisions != 1 || triggers != 1 || trades != 1 {
		t.Fatalf("decisions=%d triggers=%d trades=%d", decisions, triggers, trades)
	}
}

func TestFinalDecisionStrictActionsLimitsAndDeduplication(t *testing.T) {
	valid := `{"analysis":"ok","opportunities":[{"action":"buy_now","stockName":"浦发银行","stockCode":"sh600000","priceLow":9,"priceHigh":11,"aiSummary":"a","timingReason":"now","mainRisk":"r","sourceRefs":"S1"},{"action":"wait","stockName":"平安银行","stockCode":"sz000001","priceLow":10,"priceHigh":12,"aiSummary":"b","timingReason":"later","mainRisk":"r","sourceRefs":"S2"}]}`
	if result, err := parseFinalDecision(valid, 2, 5); err != nil || len(result.Opportunities) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	duplicate := `{"analysis":"bad","opportunities":[{"action":"wait","stockName":"浦发银行","stockCode":"sh600000","priceLow":9,"priceHigh":11,"aiSummary":"a","timingReason":"later","mainRisk":"r","sourceRefs":"S1"},{"action":"reject","stockName":"浦发银行","stockCode":"600000","priceLow":0,"priceHigh":0,"aiSummary":"b","timingReason":"no","mainRisk":"r","sourceRefs":"S2"}]}`
	if _, err := parseFinalDecision(duplicate, 2, 5); err == nil {
		t.Fatal("duplicate normalized stock must be rejected")
	}
	overLimit := strings.Replace(valid, `"action":"wait"`, `"action":"buy_now"`, 1)
	if _, err := parseFinalDecision(overLimit, 1, 5); err == nil {
		t.Fatal("buy_now limit must be enforced")
	}
	if _, err := parseFinalDecision("```json\n"+valid+"\n```", 2, 5); err == nil {
		t.Fatal("final decision must reject code fences")
	}
}

func TestEventRunCrossingCutoffClosesBuyDecisionWithoutReservation(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 28, 14, 24, 0, 0, shanghaiLocation)
	sector := `{"analysis":"银行","directions":["银行"],"candidates":[{"code":"sh600000","name":"浦发银行"}]}`
	stock := `{"analysis":"量价改善","shortlist":[{"stockName":"浦发银行","stockCode":"sh600000","aiSummary":"改善","mainRisk":"回落","sourceRefs":"S001"}]}`
	final := `{"analysis":"可买但已临近截止","opportunities":[{"action":"buy_now","stockName":"浦发银行","stockCode":"sh600000","priceLow":9,"priceHigh":11,"aiSummary":"改善","timingReason":"当前","mainRisk":"回落","sourceRefs":"S001"}]}`
	ai := &clockAdvancingAI{results: []CompletionResult{{Content: "大盘"}, {Content: sector}, {Content: stock}, {Content: final}},
		now: &now, advanceAtPhase: "final_decision", advanceTo: time.Date(2026, 8, 28, 14, 26, 0, 0, shanghaiLocation)}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: "sh600000", Name: "浦发银行", Price: 10, At: now}}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeEvent, TriggerIDs: []string{"event-1"}, TriggerSource: TriggerSourceCapitalGap})
	if err != nil || run.RecommendationCount != 0 || run.WaitCount != 1 || quotes.calls != 0 {
		t.Fatalf("run=%+v quoteCalls=%d err=%v", run, quotes.calls, err)
	}
	opportunities, err := repo.BuyOpportunitiesForRun(context.Background(), run.RunID)
	if err != nil || len(opportunities) != 1 || opportunities[0].Action != OpportunityActionWait || opportunities[0].Status != "closed" {
		t.Fatalf("opportunities=%+v err=%v", opportunities, err)
	}
	capacity, _ := repo.RecommendationCapacity(context.Background())
	if capacity.ReservedCash != 0 {
		t.Fatalf("reserved cash=%v", capacity.ReservedCash)
	}
}

func TestWaitAndRejectPersistWithoutRecommendationOrCashReservation(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiLocation)
	sector := `{"analysis":"观察","directions":["银行"],"candidates":[{"code":"sh600000","name":"浦发银行"},{"code":"sz000001","name":"平安银行"}]}`
	stock := `{"analysis":"暂不追高","shortlist":[{"stockName":"浦发银行","stockCode":"sh600000","aiSummary":"等待回踩","mainRisk":"回落","sourceRefs":"S001"},{"stockName":"平安银行","stockCode":"sz000001","aiSummary":"信号不足","mainRisk":"量能","sourceRefs":"S002"}]}`
	final := `{"analysis":"保持选择性","opportunities":[{"action":"wait","stockName":"浦发银行","stockCode":"sh600000","priceLow":9,"priceHigh":11,"aiSummary":"等待回踩","timingReason":"位置偏高","mainRisk":"回落","sourceRefs":"S001"},{"action":"reject","stockName":"平安银行","stockCode":"sz000001","priceLow":0,"priceHigh":0,"aiSummary":"信号不足","timingReason":"本轮放弃","mainRisk":"量能","sourceRefs":"S002"}]}`
	ai := &scriptedAI{results: []CompletionResult{{Content: "大盘"}, {Content: sector}, {Content: stock}, {Content: final}}}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: "sh600000", Name: "浦发银行", Price: 10, At: now}}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{
		Mode: AnalysisModeEvent, TriggerIDs: []string{"event-wait-reject"}, TriggerSource: TriggerSourceCapitalGap,
	})
	if err != nil || run.RecommendationCount != 0 || run.WaitCount != 1 || run.RejectCount != 1 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	var recommendations int64
	if err := repo.DB().Model(&Recommendation{}).Where("analysis_run_id = ?", run.RunID).Count(&recommendations).Error; err != nil || recommendations != 0 {
		t.Fatalf("recommendations=%d err=%v", recommendations, err)
	}
	opportunities, err := repo.BuyOpportunitiesForRun(context.Background(), run.RunID)
	if err != nil || len(opportunities) != 2 || opportunities[0].RecommendationID != "" || opportunities[1].RecommendationID != "" {
		t.Fatalf("opportunities=%+v err=%v", opportunities, err)
	}
	detail, err := repo.Analysis(context.Background(), run.RunID)
	if err != nil || len(detail.Opportunities) != 2 {
		t.Fatalf("analysis detail opportunities=%+v err=%v", detail.Opportunities, err)
	}
	capacity, err := repo.RecommendationCapacity(context.Background())
	if err != nil || capacity.ReservedCash != 0 || capacity.PendingBuys != 0 {
		t.Fatalf("capacity=%+v err=%v", capacity, err)
	}
}

func TestReservedEventRunClearsRunLeaseOnSuccessfulSave(t *testing.T) {
	repo := researchTestRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiLocation)
	if _, err := repo.EnqueueAnalysisTrigger(ctx, TriggerSourceCapitalGap, "lease-clear", "gap", now); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.ClaimAnalysisTriggerBatch(ctx, now, "runtime", 10*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim=%+v ok=%t err=%v", claim, ok, err)
	}
	ai := &scriptedAI{results: []CompletionResult{{Content: "大盘"},
		{Content: `{"analysis":"无方向","directions":[],"candidates":[]}`},
		{Content: `{"analysis":"空仓","opportunities":[]}`}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(ctx, AnalysisRequest{Mode: AnalysisModeEvent,
		ReservedRunID: claim.Run.RunID, LeaseOwner: "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Analysis(ctx, run.RunID)
	if err != nil || stored.LeaseOwner != "" || stored.LeaseExpiresAt != nil || stored.Status != "no_recommendation" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if err := repo.CompleteAnalysisTriggerBatch(ctx, run.RunID, "runtime", now); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredAnalysisLeaseRecoversAcrossRuntimeReplacementAndIsIdempotent(t *testing.T) {
	repo := researchTestRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiLocation)
	legacy := AnalysisRun{RunID: "legacy-unleased-running", ScheduledFor: base.AddDate(0, 0, -1), StartedAt: base.AddDate(0, 0, -1),
		Status: "running", ModelAttemptLogJSON: "[]"}
	if err := repo.DB().Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnqueueAnalysisTrigger(ctx, TriggerSourceStartup, "startup-runtime", "startup gap", base); err != nil {
		t.Fatal(err)
	}
	// Repeated startup registration must not duplicate the durable event.
	if _, err := repo.EnqueueAnalysisTrigger(ctx, TriggerSourceStartup, "startup-runtime", "startup gap", base); err != nil {
		t.Fatal(err)
	}
	var eventCount int64
	if err := repo.DB().Model(&AnalysisTrigger{}).Where("source = ? AND source_key = ?", TriggerSourceStartup, "startup-runtime").Count(&eventCount).Error; err != nil || eventCount != 1 {
		t.Fatalf("events=%d err=%v", eventCount, err)
	}
	claim, ok, err := repo.ClaimAnalysisTriggerBatch(ctx, base, "runtime-before-crash", time.Minute)
	if err != nil || !ok {
		t.Fatalf("unleased pre-2.7 running row blocked claim=%+v ok=%t err=%v", claim, ok, err)
	}
	recoveredAt := base.Add(2 * time.Minute)
	replacement := NewRepository(repo.DB())
	recovered, err := replacement.RecoverExpiredAnalysisLeases(ctx, recoveredAt)
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	if repeated, repeatErr := replacement.RecoverExpiredAnalysisLeases(ctx, recoveredAt); repeatErr != nil || repeated != 0 {
		t.Fatalf("repeated=%d err=%v", repeated, repeatErr)
	}
	failedRun, err := replacement.Analysis(ctx, claim.Run.RunID)
	if err != nil || failedRun.Status != "failed" || failedRun.LeaseOwner != "" || failedRun.LeaseExpiresAt != nil {
		t.Fatalf("failed run=%+v err=%v", failedRun, err)
	}
	var trigger AnalysisTrigger
	if err := replacement.DB().Where("source = ? AND source_key = ?", TriggerSourceStartup, "startup-runtime").First(&trigger).Error; err != nil {
		t.Fatal(err)
	}
	if trigger.Status != TriggerStatusQueued || !trigger.AvailableAt.Equal(recoveredAt.Add(5*time.Minute)) {
		t.Fatalf("recovered trigger=%+v", trigger)
	}
	if early, claimed, earlyErr := replacement.ClaimAnalysisTriggerBatch(ctx, trigger.AvailableAt.Add(-time.Second), "runtime-after-crash", time.Minute); earlyErr != nil || claimed {
		t.Fatalf("early claim=%+v claimed=%t err=%v", early, claimed, earlyErr)
	}
	if retried, claimed, retryErr := replacement.ClaimAnalysisTriggerBatch(ctx, trigger.AvailableAt, "runtime-after-crash", time.Minute); retryErr != nil || !claimed || retried.Run.RunID == claim.Run.RunID {
		t.Fatalf("retry=%+v claimed=%t err=%v", retried, claimed, retryErr)
	}
}
