package research2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSelectedPrimariesKeepLabelsThroughExecution(t *testing.T) {
	for initialBuys := 0; initialBuys <= 2; initialBuys++ {
		t.Run(fmt.Sprint(initialBuys), func(t *testing.T) {
			r, chain, run, at := emptyRefillFixture(t)
			ctx := context.Background()
			if err := r.DB().Model(&AnalysisRun{}).Where("run_id = ?", run.RunID).Update("status", "success").Error; err != nil {
				t.Fatal(err)
			}
			var items []Recommendation
			for i := 0; i < 3; i++ {
				selected := at.Add(time.Duration(i) * time.Second)
				item := Recommendation{RecommendationID: fmt.Sprintf("primary%d", i), AnalysisRunID: run.RunID, StockCode: fmt.Sprintf("sh60000%d", i), SelectionRole: "primary", SelectionRank: i + 1, FinalScore: 57, Status: "buy_pending", SignalAt: selected, TargetBuyAt: at}
				if i < initialBuys {
					item.BuyAt = &selected
					item.Status = "active"
				}
				items = append(items, item)
			}
			items = append(items, Recommendation{RecommendationID: "low", AnalysisRunID: run.RunID, StockCode: "sh600099", SelectionRole: "observation", FinalScore: 49, Status: "analysis_only", SignalAt: at})
			if err := r.CreateRecommendations(ctx, items); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				rows, err := r.ListRecommendations(ctx, 20, 0)
				if err != nil || len(rows) != 3 {
					t.Fatalf("rows=%v err=%v", rows, err)
				}
				for i, row := range rows {
					if row.RecommendationID != fmt.Sprintf("primary%d", i) || row.DisplaySelectionRole != "primary" || row.DisplaySelectionRank != i+1 {
						t.Fatalf("row=%+v", row)
					}
				}
			}
			check()
			refreshed, err := r.RefreshExecutionChainFilled(ctx, chain.ChainID)
			if err != nil || refreshed.Status != "running" {
				t.Fatalf("pending buys cancelled: %+v %v", refreshed, err)
			}
			// A later selected primary can settle first without renumbering other primaries.
			if err := r.DB().Model(&Recommendation{}).Where("recommendation_id = ?", "primary2").Updates(map[string]any{"status": "active", "buy_at": at.Add(time.Minute)}).Error; err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}

func TestFullSelectionStopsClaimsAndLegacyRecovery(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			r, chain, run, at := emptyRefillFixture(t)
			ctx := context.Background()
			for i, score := range []float64{49, 48, 48} {
				item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: fmt.Sprintf("sh60000%d", i), SelectionRole: "observation", FinalScore: score, Status: "analysis_only", SignalAt: at}
				if err := r.DB().Create(&item).Error; err != nil {
					t.Fatal(err)
				}
			}
			if legacy {
				if err := r.DB().Model(&ExecutionChain{}).Where("chain_id = ?", chain.ChainID).Update("status", "exhausted").Error; err != nil {
					t.Fatal(err)
				}
				if err := r.RecoverEmptyExecutionChain(ctx, at.Add(20*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			ready, err := r.ExecutionChainsReadyForRefill(ctx, at.Add(20*time.Minute))
			if err != nil || len(ready) != 0 {
				t.Fatalf("full selection refilled: %+v %v", ready, err)
			}
			candidate := AnalysisRun{RunID: uuid.NewString(), TradingDate: run.TradingDate, ParentRunID: run.RunID, ChainID: chain.ChainID, TriggerSource: "untradable_refill", StartedAt: at.Add(20 * time.Minute)}
			if _, created, err := r.CreateRunAttempt(ctx, &candidate, true); err == nil || created {
				t.Fatal("full selection claimed another run")
			}
			refreshed, err := r.RefreshExecutionChainFilled(ctx, chain.ChainID)
			if err != nil || refreshed.Status != "completed" || refreshed.FilledSlots != 0 || refreshed.StopReason != "主选与候选已满额" {
				t.Fatalf("completion=%+v %v", refreshed, err)
			}
			emailRun, err := r.ExecutionChainEmailRun(ctx, chain.ChainID)
			if err != nil || emailRun.RecommendationCount != 0 || emailRun.FailureReason != "主选与候选已满额" {
				t.Fatalf("email=%+v %v", emailRun, err)
			}
		})
	}
}

func TestUnderfilledSuccessWaitsTenMinutesAndStopsAt1150(t *testing.T) {
	r, chain, run, at := emptyRefillFixture(t)
	ctx := context.Background()
	if err := r.DB().Model(&AnalysisRun{}).Where("run_id = ?", run.RunID).Update("status", "success").Error; err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600001", Status: "active", SignalAt: at, BuyAt: &at}
	if err := r.DB().Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		at    time.Time
		count int
	}{
		{at.Add(10*time.Minute - time.Nanosecond), 0}, {at.Add(10 * time.Minute), 1},
		{time.Date(2026, 9, 10, 11, 49, 59, 0, shanghai()), 1}, {time.Date(2026, 9, 10, 11, 50, 0, 0, shanghai()), 0},
	} {
		ready, err := r.ExecutionChainsReadyForRefill(ctx, sample.at)
		if err != nil || len(ready) != sample.count {
			t.Fatalf("at=%v ready=%v err=%v", sample.at, ready, err)
		}
	}
	_ = chain
}

func TestFullSelectionRetainsLunchBuy(t *testing.T) {
	r, chain, run, at := emptyRefillFixture(t)
	at = time.Date(2026, 9, 10, 11, 35, 0, 0, shanghai())
	ctx := context.Background()
	if err := r.DB().Model(&AnalysisRun{}).Where("run_id = ?", run.RunID).Update("status", "success").Error; err != nil {
		t.Fatal(err)
	}
	open := time.Date(2026, 9, 10, 13, 0, 0, 0, shanghai())
	primary := Recommendation{RecommendationID: "lunch-primary", AnalysisRunID: run.RunID, StockCode: "sh600001", SelectionRole: "primary", SelectionRank: 1, FinalScore: 57, Status: "buy_pending", SignalAt: at, TargetBuyAt: open}
	items := []Recommendation{primary}
	for i := 0; i < 2; i++ {
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: fmt.Sprintf("sh60000%d", i+2), SelectionRole: "observation", FinalScore: 49, Status: "analysis_only", SignalAt: at})
	}
	if err := r.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	refreshed, err := r.RefreshExecutionChainFilled(ctx, chain.ChainID)
	if err != nil || refreshed.Status != "running" {
		t.Fatalf("pending chain=%+v %v", refreshed, err)
	}
	market := chainMarket{snapshots: map[string]PriceSnapshot{"sh600001": {Price: 10, PreviousClose: 10}}}
	if err := testTradingService(r, market, testCalendar{}).ProcessDue(ctx, open); err != nil {
		t.Fatal(err)
	}
	refreshed, err = r.RefreshExecutionChainFilled(ctx, chain.ChainID)
	if err != nil || refreshed.Status != "completed" || refreshed.FilledSlots != 1 {
		t.Fatalf("after lunch=%+v %v", refreshed, err)
	}
}

func TestSuccessfulReplacementDoesNotRestartFullSelection(t *testing.T) {
	r, chain, run, at := emptyRefillFixture(t)
	ctx := context.Background()
	failed := Recommendation{RecommendationID: "failed-primary", AnalysisRunID: run.RunID, StockCode: "sh600001", SelectionRole: "primary", Status: "missed_untradable", SignalAt: at}
	replacement := Recommendation{RecommendationID: "replacement", AnalysisRunID: run.RunID, StockCode: "sh600002", SelectionRole: "standby", Status: "active", BuyAt: &at, SignalAt: at, ReplacesRecommendationID: failed.RecommendationID}
	rows := []Recommendation{failed, replacement}
	for i := 0; i < 2; i++ {
		rows = append(rows, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: fmt.Sprintf("sh60000%d", i+3), SelectionRole: "observation", Status: "analysis_only", FinalScore: 49, SignalAt: at})
	}
	if err := r.CreateRecommendations(ctx, rows); err != nil {
		t.Fatal(err)
	}
	refreshed, err := r.RefreshExecutionChainFilled(ctx, chain.ChainID)
	if err != nil || refreshed.Status != "completed" || refreshed.FilledSlots != 1 {
		t.Fatalf("resolved failure refilled: %+v %v", refreshed, err)
	}
}
