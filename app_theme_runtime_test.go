package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/themes"
	"go-stock/internal/bootstrap"
	"go-stock/internal/service"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

type themeTestClock struct{ now time.Time }

func (clock themeTestClock) Now() time.Time { return clock.now }

type themeTestCollector struct {
	calls      int
	observedAt time.Time
	err        error
}

func (collector *themeTestCollector) CollectAndFreeze(_ context.Context, observedAt time.Time) (themes.ThemeLifecycleRunResult, error) {
	collector.calls++
	collector.observedAt = observedAt
	return themes.ThemeLifecycleRunResult{}, collector.err
}

func TestNewAppWithRuntimeBuildsProductionThemeLifecycleRuntime(t *testing.T) {
	fixed := time.Date(2026, 8, 28, 15, 10, 0, 0, time.FixedZone("CST", 8*60*60))
	app := NewAppWithRuntime(bootstrap.AppRuntime{
		Storage: bootstrap.Storage{Main: &gorm.DB{}},
		Clock:   themeTestClock{now: fixed},
	})

	runtime, ok := app.themeRuntime.(*themes.ThemeLifecycleRuntime)
	if !ok || runtime == nil {
		t.Fatalf("theme runtime type=%T, want *themes.ThemeLifecycleRuntime", app.themeRuntime)
	}
	if runtime.Repository == nil || runtime.Service == nil || runtime.Sources == nil {
		t.Fatal("production theme runtime dependencies were not assembled")
	}
	if got := runtime.Clock(); !got.Equal(fixed) {
		t.Fatalf("runtime clock=%s want=%s", got, fixed)
	}
	// Three broad news/hot-topic adapters plus one concept fund-flow adapter;
	// no per-stock announcement/concept fan-out is registered.
	if got := len(runtime.Sources.Adapters); got != 4 {
		t.Fatalf("production adapter count=%d want=4 safe broad-market adapters", got)
	}
}

func TestThemeLifecycleCronUsesExactWeekday1510Spec(t *testing.T) {
	if themeLifecycleCronSpec != "0 10 15 * * 1-5" {
		t.Fatalf("theme lifecycle cron spec=%q", themeLifecycleCronSpec)
	}
	app := NewAppWithServices(service.AppServices{})
	app.registerThemeLifecycleCron()
	entryID, ok := app.getCronEntry(themeLifecycleEntryKey)
	if !ok {
		t.Fatal("theme lifecycle cron entry was not registered")
	}

	fridayAfterRun := time.Date(2026, 8, 28, 15, 10, 1, 0, time.Local)
	next := app.cron.Entry(entryID).Schedule.Next(fridayAfterRun)
	if next.Weekday() != time.Monday || next.Hour() != 15 || next.Minute() != 10 || next.Second() != 0 {
		t.Fatalf("next theme lifecycle run=%s, want Monday 15:10:00", next)
	}
}

func TestThemeLifecycleCronRegistrationFailureIsLatched(t *testing.T) {
	app := &App{
		cron:       cron.New(cron.WithSeconds()),
		cronEntrys: make(map[string]cron.EntryID),
	}
	app.registerThemeLifecycleCronWithSpec("invalid-theme-cron")

	err := app.schedulerRegistrationError()
	if err == nil {
		t.Fatal("expected theme lifecycle scheduler registration failure")
	}
	if !strings.Contains(err.Error(), themeLifecycleEntryKey) || !strings.Contains(err.Error(), "invalid-theme-cron") {
		t.Fatalf("unexpected scheduler registration error: %v", err)
	}
	if _, ok := app.getCronEntry(themeLifecycleEntryKey); ok {
		t.Fatal("failed theme lifecycle registration must not create an entry")
	}
}

func TestThemeLifecycleRunChecksStrictCalendarBeforeCollection(t *testing.T) {
	fixed := time.Date(2026, 8, 28, 15, 10, 0, 0, time.FixedZone("CST", 8*60*60))

	t.Run("non-trading day", func(t *testing.T) {
		collector := &themeTestCollector{}
		factoryCalls := 0
		app := NewAppWithServices(service.AppServices{})
		app.themeClock = func() time.Time { return fixed }
		app.themeOpenTradeDay = func(got time.Time) (bool, error) {
			if !got.Equal(fixed) {
				t.Fatalf("calendar day=%s want=%s", got, fixed)
			}
			return false, nil
		}
		app.themeFactory = func() (themeLifecycleCollector, error) {
			factoryCalls++
			return collector, nil
		}

		app.runThemeLifecycle()
		if factoryCalls != 0 || collector.calls != 0 {
			t.Fatalf("non-trading day factory=%d collector=%d, want zero", factoryCalls, collector.calls)
		}
		if err := app.themeLifecycleError(); err != nil {
			t.Fatalf("non-trading day recorded an error: %v", err)
		}
	})

	t.Run("calendar unavailable", func(t *testing.T) {
		collector := &themeTestCollector{}
		calendarErr := errors.New("calendar unavailable")
		app := NewAppWithServices(service.AppServices{})
		app.themeClock = func() time.Time { return fixed }
		app.themeOpenTradeDay = func(time.Time) (bool, error) { return false, calendarErr }
		app.themeFactory = func() (themeLifecycleCollector, error) { return collector, nil }

		app.runThemeLifecycle()
		if collector.calls != 0 {
			t.Fatalf("calendar failure collector calls=%d want=0", collector.calls)
		}
		err := app.themeLifecycleError()
		if err == nil || !strings.Contains(err.Error(), "trade-calendar") || !errors.Is(err, calendarErr) {
			t.Fatalf("calendar failure was not recorded: %v", err)
		}
	})

	t.Run("open trading day", func(t *testing.T) {
		collector := &themeTestCollector{}
		factoryCalls := 0
		app := NewAppWithServices(service.AppServices{})
		app.themeClock = func() time.Time { return fixed }
		app.themeOpenTradeDay = func(time.Time) (bool, error) { return true, nil }
		app.themeFactory = func() (themeLifecycleCollector, error) {
			factoryCalls++
			return collector, nil
		}

		app.runThemeLifecycle()
		if factoryCalls != 1 || collector.calls != 1 {
			t.Fatalf("open trading day factory=%d collector=%d want=1/1", factoryCalls, collector.calls)
		}
		if !collector.observedAt.Equal(fixed) {
			t.Fatalf("collector observedAt=%s want=%s", collector.observedAt, fixed)
		}
	})
}
