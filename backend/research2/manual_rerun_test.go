package research2

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestManualRetryOnlyLatestFailedRunAndPreservesHistory(t *testing.T) {
	ctx := context.Background()
	r := research2TestRepository(t)
	at := time.Date(2026, 9, 9, 9, 50, 0, 0, shanghai())
	firstRunner := NewRunner(r, &sequenceAI{responses: []string{`{}`}}, failingRunEvidence{err: errors.New("provider unavailable")}, testCalendar{})
	firstRunner.ConfigureReplayClock(func() time.Time { return at }, nil)
	first, err := firstRunner.Run(ctx, at)
	if err == nil || first.Status != "failed" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	var original AnalysisRun
	if err = r.DB().First(&original, "run_id = ?", first.RunID).Error; err != nil {
		t.Fatal(err)
	}
	ai := &sequenceAI{responses: []string{`{"tradingDay":true,"conclusion":"empty","recommendations":[]}`}}
	runner := NewRunner(r, ai, fixedEvidence{value: Evidence{AvailableCash: InitialCash, Prompt: `{}`, SourceStatusJSON: `[]`}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at.Add(time.Minute) }, nil)
	second, err := runner.Rerun(ctx, at, first.RunID)
	if err != nil || second.RunID == first.RunID || second.AttemptNo != 2 || second.Status != "no_recommendation" || ai.calls != 0 {
		t.Fatalf("retry=%+v err=%v calls=%d", second, err, ai.calls)
	}
	var saved AnalysisRun
	if err = r.DB().First(&saved, "run_id = ?", first.RunID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, saved) {
		t.Fatal("failed report changed during retry")
	}
	for _, parent := range []string{first.RunID, second.RunID} {
		if _, err = runner.Rerun(ctx, at, parent); !errors.Is(err, ErrExecutionChainClosed) {
			t.Fatalf("invalid manual retry parent %s: %v", parent, err)
		}
	}
	if ai.calls != 0 {
		t.Fatal("rejected retry called AI")
	}
}

func TestConcurrentRunClaimsCreateOnlyOneRunningAttempt(t *testing.T) {
	r := concurrentResearch2Repository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 9, 9, 50, 0, 0, shanghai())
	var wg sync.WaitGroup
	created := make([]bool, 8)
	results := make([]AnalysisRun, 8)
	errs := make([]error, 8)
	for i := range created {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-09-09", Status: "running", StartedAt: at, ScheduledFor: at}
			results[i], created[i], errs[i] = r.CreateRunAttempt(ctx, &run, true)
		}(i)
	}
	wg.Wait()
	count := 0
	id := ""
	for i := range created {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if created[i] {
			count++
			id = results[i].RunID
		}
	}
	if count != 1 {
		t.Fatalf("claims=%d", count)
	}
	for _, run := range results {
		if run.RunID != id {
			t.Fatalf("claims returned different runs: %+v", results)
		}
	}
}
