package main

import (
	"context"
	"testing"
	"time"

	"go-stock/backend/research"
)

func TestLatestConfiguredAnalysisSlotUsesOnlyMostRecentNode(t *testing.T) {
	service := research.NewService(nil, nil, nil, research.WeekdayCalendar{})
	now := time.Date(2026, 8, 17, 14, 40, 0, 0, time.FixedZone("test", 8*60*60))
	slot, err := latestConfiguredAnalysisSlot(context.Background(), service, now, []string{"09:55", "14:30"})
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.Format("2006-01-02 15:04"); got != "2026-08-17 14:30" {
		t.Fatalf("slot=%s", got)
	}

	beforeFirst := time.Date(2026, 8, 17, 9, 40, 0, 0, now.Location())
	slot, err = latestConfiguredAnalysisSlot(context.Background(), service, beforeFirst, []string{"09:55", "14:30"})
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.Format("2006-01-02 15:04"); got != "2026-08-14 14:30" {
		t.Fatalf("previous trading slot=%s", got)
	}
}

func TestScheduledAnalysisRecoveryDue(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 10, 0, 0, time.FixedZone("test", 8*60*60))
	if !scheduledAnalysisRecoveryDue(now, research.AnalysisRun{}, false) {
		t.Fatal("a genuinely missed slot must run immediately")
	}
	completed := now.Add(-scheduledAnalysisRetryInterval)
	failed := research.AnalysisRun{Status: "failed", StartedAt: now.Add(-10 * time.Minute), CompletedAt: &completed}
	if !scheduledAnalysisRecoveryDue(now, failed, true) {
		t.Fatal("failed slot must retry after five minutes")
	}
	almost := now.Add(-scheduledAnalysisRetryInterval + time.Second)
	failed.CompletedAt = &almost
	if scheduledAnalysisRecoveryDue(now, failed, true) {
		t.Fatal("failed slot retried before five minutes")
	}
	previousDay := failed
	previousDay.StartedAt = previousDay.StartedAt.AddDate(0, 0, -1)
	previousDay.CompletedAt = &completed
	if scheduledAnalysisRecoveryDue(now, previousDay, true) {
		t.Fatal("failed slot rolled into another trading day")
	}
	for _, status := range []string{"running", "success", "no_recommendation", "skipped_cash"} {
		terminal := research.AnalysisRun{Status: status, StartedAt: now, CompletedAt: &completed}
		if scheduledAnalysisRecoveryDue(now, terminal, true) {
			t.Fatalf("status %s unexpectedly retried", status)
		}
	}
}

func TestScheduledAnalysisSlotIsExactAndManualTimestampDiffers(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 30, 17, 123, time.FixedZone("test", 8*60*60))
	slot := scheduledAnalysisSlot(now, "14:30")
	if slot.Second() != 0 || slot.Nanosecond() != 0 || slot.Format("2006-01-02 15:04") != "2026-08-17 14:30" {
		t.Fatalf("slot=%s", slot.Format(time.RFC3339Nano))
	}
	if slot.Equal(now) {
		t.Fatal("manual invocation timestamp must not equal the exact scheduled slot")
	}
}
