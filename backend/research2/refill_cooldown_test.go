package research2

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func emptyRefillFixture(t *testing.T) (*Repository, ExecutionChain, AnalysisRun, time.Time) {
	t.Helper()
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 10, 10, 2, 18, 0, shanghai())
	chain, run := createChainRun(t, repository, now)
	run.Status, run.GeneratedAt, run.ReportMarkdown = "no_recommendation", &now, "original empty report"
	if err := repository.SaveRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	chain, err := repository.ExecutionChain(context.Background(), chain.ChainID)
	if err != nil {
		t.Fatal(err)
	}
	return repository, chain, run, now
}

func TestRefillCooldownTimestampFallbacks(t *testing.T) {
	for _, source := range []string{"generated", "updated", "started"} {
		t.Run(source, func(t *testing.T) {
			r, chain, run, now := emptyRefillFixture(t)
			run.StartedAt, run.UpdatedAt = now.Add(-time.Hour), now.Add(-time.Minute)
			if source != "generated" {
				run.GeneratedAt = nil
				run.UpdatedAt = now
			}
			if source == "started" {
				run.UpdatedAt, run.StartedAt = time.Time{}, now
			}
			for _, delta := range []time.Duration{10*time.Minute - time.Nanosecond, 10 * time.Minute} {
				ready, _, err := refillReady(r.DB(), chain, run, now.Add(delta), false)
				if err != nil || ready != (delta == 10*time.Minute) {
					t.Fatalf("source=%s delta=%v ready=%v err=%v", source, delta, ready, err)
				}
			}
		})
	}
}

func TestLegacyEmptyRefillRecoveryPreservesRun(t *testing.T) {
	for _, scenario := range []string{"due", "cooling", "lunch", "cutoff", "prior_day", "disabled", "completed", "failed_run", "pending", "filled", "auto_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			r, chain, run, now := emptyRefillFixture(t)
			status, at := "exhausted", now.Add(15*time.Minute)
			want := scenario == "due" || scenario == "cooling" || scenario == "lunch"
			switch scenario {
			case "auto_disabled":
				configurePermissionFixture(t, r)(false)
			case "cooling":
				at = now.Add(time.Minute)
			case "lunch":
				at = time.Date(2026, 9, 10, 11, 40, 0, 0, shanghai())
			case "cutoff":
				at = time.Date(2026, 9, 10, 11, 50, 0, 0, shanghai())
			case "prior_day":
				at = at.AddDate(0, 0, 1)
			case "disabled", "completed":
				status = scenario
			case "failed_run":
				run.Status = "failed"
				if err := r.SaveRun(context.Background(), &run); err != nil {
					t.Fatal(err)
				}
			case "pending", "filled":
				count := 1
				if scenario == "filled" {
					count = 3
				}
				for i := 0; i < count; i++ {
					item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600011", Status: "buy_pending", SignalAt: now, TargetBuyAt: now}
					if scenario == "filled" {
						item.BuyAt, item.Status = &now, "active"
					}
					if err := r.DB().Create(&item).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := r.DB().Model(&ExecutionChain{}).Where("chain_id = ?", chain.ChainID).Updates(map[string]any{"status": status, "stop_reason": "old stop", "completed_at": now}).Error; err != nil {
				t.Fatal(err)
			}
			before, err := r.AnalysisRunByID(context.Background(), run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := r.RecoverEmptyExecutionChain(context.Background(), at); err != nil {
					t.Fatal(err)
				}
			}
			afterChain, err := r.ExecutionChain(context.Background(), chain.ChainID)
			if err != nil || (afterChain.Status == "running") != want {
				t.Fatalf("chain=%+v err=%v", afterChain, err)
			}
			after, err := r.AnalysisRunByID(context.Background(), run.RunID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("original run changed: %v", err)
			}
			ready, err := r.ExecutionChainsReadyForRefill(context.Background(), at)
			if err != nil || (len(ready) == 1) != (want && scenario != "cooling") {
				t.Fatalf("ready=%+v err=%v", ready, err)
			}
		})
	}
}

func TestRefillAndManualClaimCompeteForSameParent(t *testing.T) {
	r, chain, run, now := emptyRefillFixture(t)
	connection, err := r.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	defer connection.Close()
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, trigger := range []string{"untradable_refill", "manual_rerun"} {
		wg.Add(1)
		go func(trigger string) {
			defer wg.Done()
			candidate := AnalysisRun{RunID: uuid.NewString(), TradingDate: run.TradingDate, ChainID: chain.ChainID, ParentRunID: run.RunID, TriggerSource: trigger, ScheduledFor: now, StartedAt: now.Add(10 * time.Minute), Status: "running"}
			_, created, _ := r.CreateRunAttempt(context.Background(), &candidate, true)
			results <- created
		}(trigger)
	}
	wg.Wait()
	close(results)
	created := 0
	for result := range results {
		if result {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d attempts", created)
	}
	ready, err := r.ExecutionChainsReadyForRefill(context.Background(), now.Add(20*time.Minute))
	if err != nil || len(ready) != 0 {
		t.Fatalf("running attempt allowed refill: %v %v", ready, err)
	}
}

func TestRefillClaimRechecksStateAfterPolling(t *testing.T) {
	for _, scenario := range []string{"pending", "filled", "auto_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			r, chain, run, now := emptyRefillFixture(t)
			at := now.Add(10 * time.Minute)
			ready, err := r.ExecutionChainsReadyForRefill(context.Background(), at)
			if err != nil || len(ready) != 1 {
				t.Fatalf("fixture not ready: %v %v", ready, err)
			}
			if scenario == "auto_disabled" {
				configurePermissionFixture(t, r)(false)
			} else {
				count := 1
				if scenario == "filled" {
					count = 3
				}
				for i := 0; i < count; i++ {
					item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600011", Status: "buy_pending", SignalAt: now, TargetBuyAt: now}
					if scenario == "filled" {
						item.BuyAt, item.Status = &now, "active"
					}
					if err := r.DB().Create(&item).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			candidate := AnalysisRun{RunID: uuid.NewString(), TradingDate: run.TradingDate, ChainID: chain.ChainID, ParentRunID: run.RunID, TriggerSource: "untradable_refill", ScheduledFor: at, StartedAt: at, Status: "running"}
			if _, created, err := r.CreateRunAttempt(context.Background(), &candidate, true); err == nil || created {
				t.Fatalf("stale poll created attempt: %v %v", created, err)
			}
		})
	}
}
