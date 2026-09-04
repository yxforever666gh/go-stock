package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"go-stock/backend/models"
	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
)

type startupStockCounter struct {
	service.StockService
	master atomic.Int32
	index  atomic.Int32
}

func (s *startupStockCounter) RefreshStockBaseInfo(context.Context) (models.StockMasterRefreshResult, error) {
	s.master.Add(1)
	return models.StockMasterRefreshResult{}, nil
}

func (s *startupStockCounter) RefreshIndexBaseInfo() {
	s.index.Add(1)
}

func TestStartupBasicInfoRunsMasterAndIndexExactlyOnce(t *testing.T) {
	stock := &startupStockCounter{}
	app := NewAppWithServices(service.AppServices{Stock: stock})
	app.startMaintenanceRuntime(&models.SettingConfig{Settings: &models.Settings{UpdateBasicInfoOnStart: true}})
	if !app.runtime.Shutdown(time.Second) {
		t.Fatal("startup tasks did not finish")
	}
	if stock.master.Load() != 1 || stock.index.Load() != 1 {
		t.Fatalf("startup refresh counts = master %d index %d; want 1 each", stock.master.Load(), stock.index.Load())
	}

	disabled := &startupStockCounter{}
	app = NewAppWithServices(service.AppServices{Stock: disabled})
	app.startMaintenanceRuntime(&models.SettingConfig{Settings: &models.Settings{}})
	if !app.runtime.Shutdown(time.Second) {
		t.Fatal("disabled startup tasks did not finish")
	}
	if disabled.master.Load() != 0 || disabled.index.Load() != 0 {
		t.Fatalf("disabled refresh counts = master %d index %d; want 0", disabled.master.Load(), disabled.index.Load())
	}
}

func TestMarketNewsPollingSupportsResearchAndHasSafeMinimumInterval(t *testing.T) {
	if marketNewsPollingEnabled(&models.SettingConfig{Settings: &models.Settings{}}) {
		t.Fatal("market news polling should be disabled when both news and research are disabled")
	}
	if !marketNewsPollingEnabled(&models.SettingConfig{Settings: &models.Settings{AICapitalDeploymentEnabled: true}}) {
		t.Fatal("capital deployment research must keep market news polling enabled")
	}
	if got := marketNewsPollingInterval(1); got != marketNewsPollingMinimumInterval {
		t.Fatalf("short polling interval=%s want=%s", got, marketNewsPollingMinimumInterval)
	}
	if got := marketNewsPollingInterval(600); got != 610*time.Second {
		t.Fatalf("configured polling interval=%s want=610s", got)
	}
}

func TestMarketNewsPollingReloadFollowsCapitalDeploymentSwitch(t *testing.T) {
	app := NewAppWithServices(service.AppServices{})
	enabled := &models.SettingConfig{Settings: &models.Settings{AICapitalDeploymentEnabled: true, RefreshInterval: 1}}
	app.reloadMarketNewsPolling(enabled, false)
	for _, key := range []string{"GetNewTelegraph", "newSinaNews", "tradingViewNews"} {
		if _, exists := app.getCronEntry(key); !exists {
			t.Fatalf("enabled capital deployment did not register %s", key)
		}
	}
	disabled := &models.SettingConfig{Settings: &models.Settings{}}
	app.reloadMarketNewsPolling(disabled, false)
	for _, key := range []string{"GetNewTelegraph", "newSinaNews", "tradingViewNews"} {
		if _, exists := app.getCronEntry(key); exists {
			t.Fatalf("disabled news and capital deployment retained %s", key)
		}
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
