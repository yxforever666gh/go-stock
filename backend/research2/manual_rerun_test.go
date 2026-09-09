package research2

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestManualRerunPreservesPreviousRunAndRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 9, 11, 20, 0, 0, shanghai())
	ai := &sequenceAI{responses: []string{`{"tradingDay":true,"conclusion":"no eligible stocks","recommendations":[]}`}}
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{Prompt: `{}`, SourceStatusJSON: `[]`}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return now }, nil)
	first, err := runner.Run(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	var original AnalysisRun
	if err = repository.DB().First(&original, "run_id = ?", first.RunID).Error; err != nil {
		t.Fatal(err)
	}
	second, err := runner.Rerun(ctx, now, first.RunID)
	if err != nil || second.RunID == first.RunID || second.AttemptNo != 2 || second.TriggerSource != "manual_rerun" || second.ParentRunID != first.RunID || second.Status != "no_recommendation" || ai.calls != 2 {
		t.Fatalf("run=%+v err=%v calls=%d", second, err, ai.calls)
	}
	var saved AnalysisRun
	if err = repository.DB().First(&saved, "run_id = ?", first.RunID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, saved) {
		t.Fatal("prior report changed")
	}
	if _, err = runner.Rerun(ctx, now, first.RunID); !errors.Is(err, ErrExecutionChainClosed) {
		t.Fatalf("duplicate request accepted: %v", err)
	}
	now = now.Add(2 * time.Hour)
	if _, err = runner.Rerun(ctx, now, second.RunID); !errors.Is(err, ErrOutsideAnalysisStartWindow) {
		t.Fatalf("outside window accepted: %v", err)
	}
	if ai.calls != 2 {
		t.Fatal("rejected request invoked model")
	}
}

func TestManualRerunCannotReopenDisabledOrFilledChain(t *testing.T) {
	for _, status := range []string{"disabled", "completed", "exhausted"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			repository := research2TestRepository(t)
			now := time.Date(2026, 9, 9, 11, 20, 0, 0, shanghai())
			chain, err := repository.EnsureExecutionChain(ctx, "2026-09-09", now, now)
			if err != nil {
				t.Fatal(err)
			}
			first := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-09-09", AttemptNo: 1, ChainID: chain.ChainID, Status: "no_recommendation", ScheduledFor: now, StartedAt: now}
			if err = repository.DB().Create(&first).Error; err != nil {
				t.Fatal(err)
			}
			if err = repository.DB().Model(&ExecutionChain{}).Where("chain_id = ?", chain.ChainID).Updates(map[string]any{"status": status, "filled_slots": 3}).Error; err != nil {
				t.Fatal(err)
			}
			run := AnalysisRun{RunID: uuid.NewString(), TradingDate: first.TradingDate, ParentRunID: first.RunID, TriggerSource: "manual_rerun", StartedAt: now}
			if _, created, err := repository.CreateRunAttempt(ctx, &run, true); err == nil || created {
				t.Fatalf("reopened protected chain: %v", err)
			}
		})
	}
}
