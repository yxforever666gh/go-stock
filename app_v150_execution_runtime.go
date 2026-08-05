package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
)

const marketSummaryV150ExecutionCronKey = "MarketSummaryV150ExecutionMonitor"

type marketSummaryV150ExecutionCallback func(time.Time) (data.MarketSummaryV150ExecutionMonitorResult, error)

// marketSummaryV150ExecutionRuntime is a small process-local coordinator over
// the durable, idempotent event replay. A new instance intentionally has no
// remembered slot: startup always replays the latest completed window so a
// crash between observation and event append is recovered.
type marketSummaryV150ExecutionRuntime struct {
	mu       sync.Mutex
	busy     bool
	lastSlot time.Time
	wakeGen  uint64
	doneGen  uint64
	now      func() time.Time
	execute  marketSummaryV150ExecutionCallback
}

func newMarketSummaryV150ExecutionRuntime(execute marketSummaryV150ExecutionCallback) *marketSummaryV150ExecutionRuntime {
	return &marketSummaryV150ExecutionRuntime{now: time.Now, execute: execute}
}

// Tick returns true only when this call acquired and ran a new scheduler slot.
// It is synchronous by design: robfig/cron may start overlapping callbacks,
// and the busy guard turns those into no-ops while preserving serial replay.
func (runtime *marketSummaryV150ExecutionRuntime) Tick(trigger string) bool {
	if runtime == nil || runtime.execute == nil {
		return false
	}
	nowFn := runtime.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	window, ok := data.ResolveMarketSummaryV150ExecutionWindow(now)
	if !ok || window.SlotAt.IsZero() {
		return false
	}

	runtime.mu.Lock()
	if runtime.busy {
		runtime.mu.Unlock()
		return false
	}
	if !runtime.lastSlot.IsZero() && !window.SlotAt.After(runtime.lastSlot) && runtime.wakeGen <= runtime.doneGen {
		runtime.mu.Unlock()
		return false
	}
	runtime.busy = true
	startedWakeGen := runtime.wakeGen
	runtime.mu.Unlock()

	var result data.MarketSummaryV150ExecutionMonitorResult
	var runErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("v1.5 execution monitor panic: %v", recovered)
			}
		}()
		result, runErr = runtime.execute(now)
	}()

	runtime.mu.Lock()
	runtime.busy = false
	if runErr == nil {
		runtime.lastSlot = window.SlotAt
		if startedWakeGen > runtime.doneGen {
			runtime.doneGen = startedWakeGen
		}
	}
	pendingWake := runtime.wakeGen > runtime.doneGen
	runtime.mu.Unlock()

	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		trigger = "runtime"
	}
	if runErr != nil {
		logger.SugaredLogger.Warnf(
			"v1.5 execution monitor failed: trigger=%s slot=%s cutoff=%s err=%v",
			trigger,
			window.SlotAt.Format(time.RFC3339),
			window.EvaluationCutoff.Format(time.RFC3339),
			runErr,
		)
		return true
	}
	for _, warning := range result.Warnings {
		if text := strings.TrimSpace(warning); text != "" {
			logger.SugaredLogger.Warnf("v1.5 execution monitor warning: %s", text)
		}
	}
	logger.SugaredLogger.Infof(
		"v1.5 execution monitor completed: trigger=%s slot=%s cutoff=%s pending=%d open=%d processed=%d skipped=%d",
		trigger,
		window.SlotAt.Format(time.RFC3339),
		firstNonZeroExecutionTime(result.EvaluationCutoff, window.EvaluationCutoff).Format(time.RFC3339),
		result.PendingCount,
		result.OpenCount,
		result.ProcessedCount,
		result.SkippedCount,
	)
	if pendingWake {
		go runtime.Tick("pending_recommendation_wakeup")
	}
	return true
}

// Wake requests a replay even when the regular scheduler slot has already
// succeeded. Publication uses this path so a newly issued intrabar rule is
// observed before its first valid bar; overlapping wakes are generation-based
// and collapse into one serial follow-up pass.
func (runtime *marketSummaryV150ExecutionRuntime) Wake(trigger string) bool {
	if runtime == nil {
		return false
	}
	runtime.mu.Lock()
	runtime.wakeGen++
	runtime.mu.Unlock()
	return runtime.Tick(trigger)
}

func firstNonZeroExecutionTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func (a *App) registerMarketSummaryV150ExecutionRuntime() {
	if a == nil {
		return
	}
	a.v150ExecutionOnce.Do(func() {
		a.v150ExecutionTask = newMarketSummaryV150ExecutionRuntime(a.executeMarketSummaryV150ExecutionMonitor)
		data.SetMarketSummaryV150ExecutionMonitorWakeup(func() {
			go a.v150ExecutionTask.Wake("recommendation_published")
		})
		a.registerCronTask(marketSummaryV150ExecutionCronKey, "@every 30s", func() {
			a.v150ExecutionTask.Tick("cron")
		})
		// Recovery is not gated on any page query or front-end action.
		go a.v150ExecutionTask.Tick("startup")
	})
}

func (a *App) executeMarketSummaryV150ExecutionMonitor(now time.Time) (data.MarketSummaryV150ExecutionMonitorResult, error) {
	taskRun := &models.CronTaskRun{
		TaskName:    "strategy_v150_execution_monitor",
		TriggeredAt: now,
		Status:      "started",
		Attempts:    1,
	}
	if db.Dao != nil && db.Dao.Migrator().HasTable(&models.CronTaskRun{}) {
		if err := db.Dao.Create(taskRun).Error; err != nil {
			logger.SugaredLogger.Warnf("record v1.5 execution monitor start failed: %v", err)
		}
	}

	result, runErr := data.RunMarketSummaryV150ExecutionMonitor(now)
	status := "success"
	errorMessage := ""
	if runErr != nil {
		status = "failed"
		errorMessage = runErr.Error()
	} else if len(result.Warnings) > 0 {
		errorMessage = strings.Join(result.Warnings, "; ")
	}
	if taskRun.ID != 0 {
		if err := db.Dao.Model(&models.CronTaskRun{}).Where("id = ?", taskRun.ID).Updates(map[string]any{
			"status":        status,
			"error_message": errorMessage,
		}).Error; err != nil {
			logger.SugaredLogger.Warnf("record v1.5 execution monitor finish failed: %v", err)
		}
	}
	return result, runErr
}
