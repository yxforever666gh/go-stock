package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-stock/backend/data"
)

func TestMarketSummaryV150ExecutionRuntimeRunsWithoutUIAndDeduplicatesSlots(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 10, 1, 0, 0, loc)
	calls := 0
	runtime := newMarketSummaryV150ExecutionRuntime(func(observedAt time.Time) (data.MarketSummaryV150ExecutionMonitorResult, error) {
		calls++
		return data.MarketSummaryV150ExecutionMonitorResult{ObservedAt: observedAt}, nil
	})
	runtime.now = func() time.Time { return now }

	// This is the same method used by the startup hook; no page/query callback
	// is involved.
	if !runtime.Tick("startup") {
		t.Fatal("startup did not execute the latest complete window")
	}
	if runtime.Tick("cron") {
		t.Fatal("same 15-minute scheduler slot executed twice")
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}

	now = time.Date(2026, 8, 5, 10, 16, 0, 0, loc)
	if !runtime.Tick("cron") || calls != 2 {
		t.Fatalf("next slot was not executed: ran=%d", calls)
	}
}

func TestMarketSummaryV150ExecutionRuntimeRetriesFailureAndSurvivesPanic(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 10, 1, 0, 0, loc)
	calls := 0
	runtime := newMarketSummaryV150ExecutionRuntime(func(time.Time) (data.MarketSummaryV150ExecutionMonitorResult, error) {
		calls++
		switch calls {
		case 1:
			return data.MarketSummaryV150ExecutionMonitorResult{}, errors.New("temporary provider failure")
		case 2:
			panic("temporary callback panic")
		default:
			return data.MarketSummaryV150ExecutionMonitorResult{}, nil
		}
	})
	runtime.now = func() time.Time { return now }

	if !runtime.Tick("cron") || !runtime.Tick("cron") || !runtime.Tick("cron") {
		t.Fatal("failed/panicked slot was not retried")
	}
	if calls != 3 {
		t.Fatalf("calls=%d, want 3", calls)
	}
	if runtime.Tick("cron") {
		t.Fatal("successful retry did not seal the in-memory slot")
	}
}

func TestMarketSummaryV150ExecutionRuntimePreventsConcurrentReentry(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 10, 1, 0, 0, loc)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	runtime := newMarketSummaryV150ExecutionRuntime(func(time.Time) (data.MarketSummaryV150ExecutionMonitorResult, error) {
		calls.Add(1)
		close(started)
		<-release
		return data.MarketSummaryV150ExecutionMonitorResult{}, nil
	})
	runtime.now = func() time.Time { return now }

	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		if !runtime.Tick("startup") {
			t.Error("first tick did not run")
		}
	}()
	<-started
	if runtime.Tick("cron") {
		t.Fatal("overlapping cron callback re-entered the execution replay")
	}
	close(release)
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", calls.Load())
	}
}

func TestMarketSummaryV150ExecutionRuntimeRestartReplaysLatestSlot(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 14, 46, 0, 0, loc)
	calls := 0
	callback := func(time.Time) (data.MarketSummaryV150ExecutionMonitorResult, error) {
		calls++
		return data.MarketSummaryV150ExecutionMonitorResult{}, nil
	}

	firstProcess := newMarketSummaryV150ExecutionRuntime(callback)
	firstProcess.now = func() time.Time { return now }
	if !firstProcess.Tick("cron") {
		t.Fatal("first process did not run")
	}
	// A fresh coordinator has no volatile checkpoint by design. The durable
	// event writer supplies idempotency, so it must replay after restart.
	restartedProcess := newMarketSummaryV150ExecutionRuntime(callback)
	restartedProcess.now = func() time.Time { return now }
	if !restartedProcess.Tick("startup") {
		t.Fatal("restart did not recover the latest completed slot")
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2 restart-safe attempts", calls)
	}
}

func TestMarketSummaryV150ExecutionRuntimeWakeRunsNewIntrabarRuleInSameSlot(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 9, 40, 0, 0, loc)
	calls := 0
	runtime := newMarketSummaryV150ExecutionRuntime(func(time.Time) (data.MarketSummaryV150ExecutionMonitorResult, error) {
		calls++
		return data.MarketSummaryV150ExecutionMonitorResult{}, nil
	})
	runtime.now = func() time.Time { return now }
	if !runtime.Tick("startup") {
		t.Fatal("initial slot did not run")
	}
	if !runtime.Wake("recommendation_published") {
		t.Fatal("new immutable recommendation did not force a same-slot observation")
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want initial pass plus publication wake", calls)
	}
	if runtime.Tick("cron") {
		t.Fatal("publication generation was not consumed")
	}
}
