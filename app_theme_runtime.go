package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/logger"
	"go-stock/backend/themes"

	"gorm.io/gorm"
)

const themeLifecycleCronSpec = "0 10 15 * * 1-5"

type themeLifecycleCollector interface {
	CollectAndFreeze(context.Context, time.Time) (data.ThemeLifecycleRunResult, error)
}

type themeLifecycleFactory func() (themeLifecycleCollector, error)

func (a *App) configureThemeLifecycleRuntime(mainDB, minuteDB *gorm.DB) {
	if a == nil {
		return
	}
	a.themeFactory = func() (themeLifecycleCollector, error) {
		if mainDB == nil {
			return nil, errors.New("theme lifecycle main storage is unavailable")
		}
		clock := a.themeClock
		if clock == nil {
			clock = time.Now
		}
		repository := themes.NewRepository(mainDB)
		service := themes.NewService(repository)
		// StockCodes is deliberately empty here. Production collection must not
		// fan out announcement/concept requests across the whole market. A later
		// adapter may populate it only from a bounded watchlist/research pool.
		adapters := data.NewExistingThemeSourceAdapters(data.ExistingThemeSourceOptions{
			Market: data.NewMarketEvidenceServiceWithMinuteDB(minuteDB),
		})
		sources := data.NewThemeSourceAggregator(0, adapters...)
		sources.Now = clock
		return data.NewThemeLifecycleRuntime(sources, service, repository, clock), nil
	}

	runtime, err := a.themeFactory()
	if err != nil {
		a.recordThemeLifecycleError("initialize", err)
		return
	}
	a.themeRuntimeMu.Lock()
	a.themeRuntime = runtime
	a.themeRuntimeMu.Unlock()
}

func (a *App) ensureThemeLifecycleRuntime() (themeLifecycleCollector, error) {
	if a == nil {
		return nil, errors.New("application is unavailable")
	}
	a.themeRuntimeMu.Lock()
	defer a.themeRuntimeMu.Unlock()
	if a.themeRuntime != nil {
		return a.themeRuntime, nil
	}
	if a.themeFactory == nil {
		return nil, errors.New("theme lifecycle runtime factory is unavailable")
	}
	runtime, err := a.themeFactory()
	if err != nil {
		return nil, err
	}
	if runtime == nil {
		return nil, errors.New("theme lifecycle runtime factory returned nil")
	}
	a.themeRuntime = runtime
	return runtime, nil
}

func (a *App) registerThemeLifecycleCron() {
	a.registerThemeLifecycleCronWithSpec(themeLifecycleCronSpec)
}

func (a *App) registerThemeLifecycleCronWithSpec(spec string) {
	a.registerCronTask(themeLifecycleEntryKey, spec, func() {
		a.runThemeLifecycle()
	})
}

func (a *App) runThemeLifecycle() {
	if a == nil || !a.themeRunMu.TryLock() {
		return
	}
	defer a.themeRunMu.Unlock()

	clock := a.themeClock
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	calendar := a.themeOpenTradeDay
	if calendar == nil {
		calendar = data.IsCNOpenTradeDayStrict
	}
	open, err := calendar(now)
	if err != nil {
		a.recordThemeLifecycleError("trade-calendar", err)
		logger.SugaredLogger.Errorf("题材生命周期任务跳过，交易日历不可用: %v", err)
		return
	}
	if !open {
		return
	}

	runtime, err := a.ensureThemeLifecycleRuntime()
	if err != nil {
		a.recordThemeLifecycleError("initialize", err)
		logger.SugaredLogger.Errorf("初始化题材生命周期任务失败: %v", err)
		return
	}
	if _, err := runtime.CollectAndFreeze(a.taskContext(), now); err != nil {
		a.recordThemeLifecycleError("collect-and-freeze", err)
		logger.SugaredLogger.Errorf("题材生命周期采集冻结失败: %v", err)
	}
}

func (a *App) recordThemeLifecycleError(operation string, err error) {
	if a == nil || err == nil {
		return
	}
	failure := fmt.Errorf("theme lifecycle %s: %w", operation, err)
	a.themeErrorsMu.Lock()
	a.themeErrors = append(a.themeErrors, failure)
	a.themeErrorsMu.Unlock()
}

func (a *App) themeLifecycleError() error {
	if a == nil {
		return errors.New("application is unavailable")
	}
	a.themeErrorsMu.Lock()
	defer a.themeErrorsMu.Unlock()
	return errors.Join(a.themeErrors...)
}
