package main

import (
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"go-stock/backend/models"
	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
)

func TestMarketNewsPollingSupportsResearchAndHasSafeMinimumInterval(t *testing.T) {
	disabled := false
	if marketNewsPollingEnabled(&models.SettingConfig{Settings: &models.Settings{}, AIAnalysisAutoEnabled: &disabled}) {
		t.Fatal("market news polling should be disabled when both news and research are disabled")
	}
	if !marketNewsPollingEnabled(&models.SettingConfig{Settings: &models.Settings{AIAnalysisEnabled: true}}) {
		t.Fatal("AI research must keep market news polling enabled")
	}
	if got := marketNewsPollingInterval(1); got != marketNewsPollingMinimumInterval {
		t.Fatalf("short polling interval=%s want=%s", got, marketNewsPollingMinimumInterval)
	}
	if got := marketNewsPollingInterval(600); got != 610*time.Second {
		t.Fatalf("configured polling interval=%s want=610s", got)
	}
}

func TestSchedulerRegistrationFailurePreventsReadyAssembly(t *testing.T) {
	app := &App{
		cron:       cron.New(cron.WithSeconds()),
		cronEntrys: make(map[string]cron.EntryID),
	}
	validEntry, err := app.cron.AddFunc("@every 1h", func() {})
	if err != nil {
		t.Fatal(err)
	}

	app.registerCronTask("invalid", "not-a-cron-expression", func() {})

	err = app.startSchedulerAfterAssembly()
	if err == nil {
		t.Fatal("expected scheduler registration error")
	}
	if !strings.Contains(err.Error(), "invalid") || !strings.Contains(err.Error(), "not-a-cron-expression") {
		t.Fatalf("unexpected scheduler registration error: %v", err)
	}
	if len(app.snapshotCronEntries()) != 0 {
		t.Fatal("failed scheduler registration must not create an entry")
	}
	if next := app.cron.Entry(validEntry).Next; !next.IsZero() {
		t.Fatalf("scheduler started despite failed assembly: next=%s", next)
	}
	readiness := releaseinfo.Readiness()
	if readiness.Scheduler || !strings.Contains(readiness.Error, "invalid") {
		t.Fatalf("scheduler failure was not latched in readiness: %+v", readiness)
	}
}

func TestAppSchedulerStartsOnlyAfterSuccessfulAssembly(t *testing.T) {
	app := NewAppWithServices(
		service.AppServices{},
	)
	entryID, err := app.cron.AddFunc("@every 1h", func() {})
	if err != nil {
		t.Fatal(err)
	}
	if next := app.cron.Entry(entryID).Next; !next.IsZero() {
		app.cron.Stop()
		t.Fatalf("scheduler started during app construction: next=%s", next)
	}

	if err := app.startSchedulerAfterAssembly(); err != nil {
		t.Fatal(err)
	}
	defer app.cron.Stop()
	deadline := time.Now().Add(time.Second)
	for app.cron.Entry(entryID).Next.IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if next := app.cron.Entry(entryID).Next; next.IsZero() {
		t.Fatal("scheduler did not start after successful assembly")
	}
}
