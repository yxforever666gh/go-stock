package research2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"go-stock/internal/trading"

	"github.com/google/uuid"
)

func TestSizeResearch2BuyUsesFixedOneFifthAllocation(t *testing.T) {
	const availableCash = 12000.0
	base := availableCash
	limit := base / float64(DailyTargetSlots)

	t.Run("ordinary lot stays strictly below fixed fifth", func(t *testing.T) {
		quantity, cost, err := sizeResearch2Buy("sh600000", 10, availableCash, 5, &base)
		if err != nil || quantity != 200 || -cost.NetCashFlow >= limit {
			t.Fatalf("quantity=%d cost=%f limit=%f err=%v", quantity, -cost.NetCashFlow, limit, err)
		}
	})

	t.Run("expensive lot buys exactly one lot", func(t *testing.T) {
		quantity, cost, err := sizeResearch2Buy("sh600000", 30, availableCash, 5, &base)
		oneLot := -trading.CalculateBuyCost(30, 100).NetCashFlow
		if err != nil || quantity != 100 || math.Abs(-cost.NetCashFlow-oneLot) > 1e-8 || oneLot <= limit {
			t.Fatalf("quantity=%d cost=%f oneLot=%f limit=%f err=%v", quantity, -cost.NetCashFlow, oneLot, limit, err)
		}
	})

	t.Run("one lot equal to fifth is the allowed exception", func(t *testing.T) {
		oneLot := -trading.CalculateBuyCost(10, 100).NetCashFlow
		equalBase := oneLot * float64(DailyTargetSlots)
		quantity, cost, err := sizeResearch2Buy("sh600000", 10, equalBase, 5, &equalBase)
		if err != nil || quantity != 100 || math.Abs(-cost.NetCashFlow-oneLot) > 1e-8 {
			t.Fatalf("quantity=%d cost=%f oneLot=%f err=%v", quantity, -cost.NetCashFlow, oneLot, err)
		}
	})

	t.Run("ordinary order never equals fixed fifth", func(t *testing.T) {
		twoLots := -trading.CalculateBuyCost(10, 200).NetCashFlow
		strictBase := twoLots * float64(DailyTargetSlots)
		quantity, cost, err := sizeResearch2Buy("sh600000", 10, strictBase, 5, &strictBase)
		if err != nil || quantity != 100 || -cost.NetCashFlow >= strictBase/float64(DailyTargetSlots) {
			t.Fatalf("quantity=%d cost=%f limit=%f err=%v", quantity, -cost.NetCashFlow, strictBase/float64(DailyTargetSlots), err)
		}
	})

	t.Run("star market keeps its 200-share lot", func(t *testing.T) {
		quantity, cost, err := sizeResearch2Buy("sh688001", 10, availableCash, 5, &base)
		if err != nil || quantity != 200 || quantity%200 != 0 || -cost.NetCashFlow >= limit {
			t.Fatalf("quantity=%d cost=%f limit=%f err=%v", quantity, -cost.NetCashFlow, limit, err)
		}
	})

	t.Run("one-lot exception still cannot overdraft", func(t *testing.T) {
		oneLot := -trading.CalculateBuyCost(30, 100).NetCashFlow
		if _, _, err := sizeResearch2Buy("sh600000", 30, oneLot-0.01, 5, &base); err == nil {
			t.Fatal("expected insufficient cash")
		}
	})

	t.Run("nil base retains legacy remaining-slot allocation", func(t *testing.T) {
		quantity, _, err := sizeResearch2Buy("sh600000", 12, availableCash, 4, nil)
		if err != nil || quantity != 200 {
			t.Fatalf("quantity=%d err=%v", quantity, err)
		}
		quantity, _, err = sizeResearch2Buy("sh600000", 12, availableCash, 4, &base)
		if err != nil || quantity != 100 {
			t.Fatalf("fixed quantity=%d err=%v", quantity, err)
		}
	})
}

func finalizeAllocationRun(t *testing.T, repository *Repository, at time.Time, scheduledSlot string) (ExecutionChain, AnalysisRun) {
	t.Helper()
	ctx := context.Background()
	repository.now = func() time.Time { return at }
	run := AnalysisRun{
		RunID: uuid.NewString(), TradingDate: at.Format("2006-01-02"), ScheduledSlot: scheduledSlot,
		ScheduledFor: SlotTime(at, scheduledSlot), StartedAt: at.Add(-time.Minute), EvidenceCutoffAt: at,
		Status: "running", TriggerSource: "scheduled", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]",
	}
	if err := repository.CreateRun(ctx, &run); err != nil {
		t.Fatal(err)
	}
	run.Status = "success"
	if err := repository.FinalizeRun(ctx, &run, nil, func() string { return "allocation test" }); err != nil {
		t.Fatal(err)
	}
	if !run.Published || run.Slot == "" {
		t.Fatalf("run=%+v", run)
	}
	chain, exists, err := repository.WithSlot(run.Slot).ExecutionChainForDate(ctx, run.TradingDate)
	if err != nil || !exists {
		t.Fatalf("chain=%+v exists=%t err=%v", chain, exists, err)
	}
	if err := repository.DB().Model(&ExecutionChain{}).Where("chain_id = ?", chain.ChainID).Update("sell_completed_at", at).Error; err != nil {
		t.Fatal(err)
	}
	chain.SellCompletedAt = &at
	return chain, run
}

func TestFinalizeRunCapturesAllocationBaseForActualSlot(t *testing.T) {
	repository := research2TestRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 21, 10, 11, 0, 0, shanghai())
	for slot, cash := range map[string]float64{"10:05": 5000, "10:10": 8000} {
		if err := repository.DB().Model(&Account{}).Where("slot = ?", slot).Update("cash", cash).Error; err != nil {
			t.Fatal(err)
		}
	}
	precreated, err := repository.WithSlot("10:10").EnsureExecutionChain(ctx, at.Format("2006-01-02"), SlotTime(at, "10:10"), at)
	if err != nil || !allocationBaseCapturePending(precreated.AllocationBaseCash) {
		t.Fatalf("precreated=%+v err=%v", precreated, err)
	}
	chain, run := finalizeAllocationRun(t, repository, at, "10:05")
	if run.Slot != "10:10" || chain.AllocationBaseCash == nil || *chain.AllocationBaseCash != 8000 {
		t.Fatalf("run=%+v chain=%+v", run, chain)
	}
	if err := repository.DB().Model(&Account{}).Where("slot = ?", "10:10").Update("cash", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.FinalizeRun(ctx, &run, nil, func() string { return "repeat" }); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.ExecutionChain(ctx, chain.ChainID)
	if err != nil || stored.AllocationBaseCash == nil || *stored.AllocationBaseCash != 8000 {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestFixedAllocationBaseSurvivesQuoteRetryAndRepositoryRestart(t *testing.T) {
	repository := research2TestRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 21, 10, 0, 5, 0, shanghai())
	chain, run := finalizeAllocationRun(t, repository, at, "10:00")
	if chain.AllocationBaseCash == nil || *chain.AllocationBaseCash != InitialCash {
		t.Fatalf("chain=%+v", chain)
	}
	items := []Recommendation{
		{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Slot: run.Slot, StockCode: "sh600001", StockName: "expensive", SelectionRank: 1, FinalScore: 80, SignalAt: at, TargetBuyAt: at, Status: "buy_pending"},
		{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Slot: run.Slot, StockCode: "sh600002", StockName: "retry", SelectionRank: 2, FinalScore: 70, SignalAt: at, TargetBuyAt: at, Status: "buy_pending"},
	}
	if err := repository.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	firstMarket := chainMarket{snapshots: map[string]PriceSnapshot{
		items[0].StockCode: {Price: 60, PreviousClose: 60},
	}, errors: map[string]error{items[1].StockCode: errRetryQuote}}
	if err := testTradingService(repository, firstMarket, testCalendar{}).ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	first, err := repository.GetRecommendation(ctx, items[0].RecommendationID)
	if err != nil || first.Recommendation.Quantity != 100 || first.Recommendation.Status != "active" {
		t.Fatalf("first=%+v err=%v", first.Recommendation, err)
	}
	pending, err := repository.GetRecommendation(ctx, items[1].RecommendationID)
	if err != nil || pending.Recommendation.Status != "buy_pending" {
		t.Fatalf("pending=%+v err=%v", pending.Recommendation, err)
	}

	restarted := NewRepository(repository.DB())
	secondMarket := chainMarket{snapshots: map[string]PriceSnapshot{
		items[1].StockCode: {Price: 10, PreviousClose: 10},
	}}
	if err := testTradingService(restarted, secondMarket, testCalendar{}).ProcessDue(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := restarted.GetRecommendation(ctx, items[1].RecommendationID)
	if err != nil || stored.Recommendation.Quantity != 200 || stored.Recommendation.Status != "active" {
		t.Fatalf("stored=%+v err=%v", stored.Recommendation, err)
	}
	chain, err = restarted.ExecutionChain(ctx, chain.ChainID)
	if err != nil || chain.AllocationBaseCash == nil || *chain.AllocationBaseCash != InitialCash {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}
}

func TestFixedAllocationBaseCapsFiveConsecutiveBuys(t *testing.T) {
	repository := research2TestRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 21, 10, 0, 5, 0, shanghai())
	chain, run := finalizeAllocationRun(t, repository, at, "10:00")
	items := make([]Recommendation, 0, DailyTargetSlots+1)
	snapshots := make(map[string]PriceSnapshot, DailyTargetSlots+1)
	for index := 0; index <= DailyTargetSlots; index++ {
		code := fmt.Sprintf("sh6001%02d", index)
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, Slot: run.Slot, StockCode: code, StockName: code, SelectionRank: index + 1, FinalScore: float64(90 - index), SignalAt: at, TargetBuyAt: at, Status: "buy_pending"})
		snapshots[code] = PriceSnapshot{Price: 10, PreviousClose: 10}
	}
	if err := repository.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	if err := testTradingService(repository, chainMarket{snapshots: snapshots}, testCalendar{}).ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.RunRecommendations(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	baseLimit := *chain.AllocationBaseCash / float64(DailyTargetSlots)
	bought := 0
	for _, item := range stored {
		if item.BuyAt == nil {
			if item.SelectionRank != DailyTargetSlots+1 || item.Status != "analysis_only" {
				t.Fatalf("unexpected unbought item: %+v", item)
			}
			continue
		}
		bought++
		cost := -trading.CalculateBuyCost(10, item.Quantity).NetCashFlow
		if item.Quantity != 200 || cost >= baseLimit {
			t.Fatalf("fixed allocation breached: %+v cost=%f limit=%f", item, cost, baseLimit)
		}
	}
	if bought != DailyTargetSlots {
		t.Fatalf("bought=%d rows=%+v", bought, stored)
	}
	storedChain, err := repository.ExecutionChain(ctx, chain.ChainID)
	if err != nil || storedChain.FilledSlots != DailyTargetSlots || storedChain.Status != "completed" {
		t.Fatalf("chain=%+v err=%v", storedChain, err)
	}
	overview, err := repository.WithSlot(run.Slot).Overview(ctx)
	if err != nil || overview.Cash < 0 {
		t.Fatalf("overview=%+v err=%v", overview, err)
	}
}

func TestFixedAllocationBasesStayWithinTheirOwnSlots(t *testing.T) {
	repository := research2TestRepository(t)
	ctx := context.Background()
	firstAt := time.Date(2026, 9, 21, 10, 0, 5, 0, shanghai())
	firstChain, firstRun := finalizeAllocationRun(t, repository, firstAt, "10:00")
	secondAt := time.Date(2026, 9, 21, 10, 5, 5, 0, shanghai())
	if err := repository.DB().Model(&Account{}).Where("slot = ?", "10:05").Update("cash", 6000).Error; err != nil {
		t.Fatal(err)
	}
	secondChain, secondRun := finalizeAllocationRun(t, repository, secondAt, "10:05")
	if firstChain.AllocationBaseCash == nil || *firstChain.AllocationBaseCash != 12000 || secondChain.AllocationBaseCash == nil || *secondChain.AllocationBaseCash != 6000 {
		t.Fatalf("first=%+v second=%+v", firstChain, secondChain)
	}
	items := []Recommendation{
		{RecommendationID: uuid.NewString(), AnalysisRunID: firstRun.RunID, Slot: firstRun.Slot, StockCode: "sh600101", StockName: "first", SelectionRank: 1, FinalScore: 80, SignalAt: firstAt, TargetBuyAt: firstAt, Status: "buy_pending"},
		{RecommendationID: uuid.NewString(), AnalysisRunID: secondRun.RunID, Slot: secondRun.Slot, StockCode: "sh600102", StockName: "second", SelectionRank: 1, FinalScore: 80, SignalAt: secondAt, TargetBuyAt: secondAt, Status: "buy_pending"},
	}
	if err := repository.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	if err := testTradingService(repository, chainMarket{snapshots: map[string]PriceSnapshot{
		items[0].StockCode: {Price: 10, PreviousClose: 10, At: secondAt},
		items[1].StockCode: {Price: 10, PreviousClose: 10},
	}}, testCalendar{}).ProcessDue(ctx, secondAt); err != nil {
		t.Fatal(err)
	}
	first, err := repository.GetRecommendation(ctx, items[0].RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.GetRecommendation(ctx, items[1].RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Recommendation.Quantity != 200 || second.Recommendation.Quantity != 100 || len(first.Trades) != 1 || len(second.Trades) != 1 || first.Trades[0].Slot != firstRun.Slot || second.Trades[0].Slot != secondRun.Slot {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	firstOverview, err := repository.WithSlot(firstRun.Slot).Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondOverview, err := repository.WithSlot(secondRun.Slot).Overview(ctx)
	if err != nil || firstOverview.Cash >= 12000 || secondOverview.Cash >= 6000 {
		t.Fatalf("first=%+v second=%+v err=%v", firstOverview, secondOverview, err)
	}
}

var errRetryQuote = errors.New("retry quote")
