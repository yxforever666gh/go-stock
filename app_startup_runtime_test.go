package main

import (
	"strings"
	"testing"

	"github.com/robfig/cron/v3"
)

func TestSchedulerRegistrationFailurePreventsReadyAssembly(t *testing.T) {
	app := &App{
		cron:       cron.New(cron.WithSeconds()),
		cronEntrys: make(map[string]cron.EntryID),
	}

	app.registerCronTask("invalid", "not-a-cron-expression", func() {})

	err := app.schedulerRegistrationError()
	if err == nil {
		t.Fatal("expected scheduler registration error")
	}
	if !strings.Contains(err.Error(), "invalid") || !strings.Contains(err.Error(), "not-a-cron-expression") {
		t.Fatalf("unexpected scheduler registration error: %v", err)
	}
	if len(app.snapshotCronEntries()) != 0 {
		t.Fatal("failed scheduler registration must not create an entry")
	}
}
