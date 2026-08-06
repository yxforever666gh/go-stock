package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/execution"
	"go-stock/backend/governance"
	"go-stock/backend/logger"
	"go-stock/backend/models"
)

type ExecutionService struct {
	monitor   execution.Monitor
	scheduler SchedulerOperations
	system    SystemOperations
}

func NewExecutionService(monitor execution.Monitor, scheduler SchedulerOperations, system SystemOperations) ExecutionService {
	return ExecutionService{monitor: monitor, scheduler: scheduler, system: system}
}

func (s ExecutionService) ResolveWindow(now time.Time) (execution.MonitorWindow, bool) {
	if s.monitor == nil {
		return execution.MonitorWindow{}, false
	}
	return s.monitor.ResolveWindow(now)
}

func (s ExecutionService) Run(ctx context.Context, now time.Time) (execution.MonitorResult, error) {
	if s.monitor == nil {
		return execution.MonitorResult{ObservedAt: now}, fmt.Errorf("%w: service adapter is nil", execution.ErrMonitorUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.monitor.Run(ctx, now)
}

// RunStrategyMonitor applies the durable strategy mode gate and records the
// scheduler audit around a monitor execution. Audit persistence remains
// best-effort, matching the previous runtime behavior: an audit write failure
// warns but never suppresses a legal execution attempt.
func (s ExecutionService) RunStrategyMonitor(ctx context.Context, now time.Time, strategyVersion string) (execution.MonitorResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := requireStrategyLive(ctx, s.system, strategyVersion); err != nil {
		return execution.MonitorResult{ObservedAt: now}, err
	}

	taskRun := &models.CronTaskRun{
		TaskName:    "strategy_v150_execution_monitor",
		TriggeredAt: now,
		Status:      "started",
		Attempts:    1,
	}
	if s.scheduler != nil {
		if err := s.scheduler.CreateTaskRun(ctx, taskRun); err != nil {
			logger.SugaredLogger.Warnf("record v1.5 execution monitor start failed: %v", err)
		}
	}

	result, runErr := s.Run(ctx, now)
	status := "success"
	errorMessage := ""
	if runErr != nil {
		status = "failed"
		errorMessage = runErr.Error()
	} else if len(result.Warnings) > 0 {
		errorMessage = strings.Join(result.Warnings, "; ")
	}
	if taskRun.ID != 0 && s.scheduler != nil {
		taskRun.Status = status
		taskRun.ErrorMessage = errorMessage
		if err := s.scheduler.UpdateTaskRun(ctx, taskRun); err != nil {
			logger.SugaredLogger.Warnf("record v1.5 execution monitor finish failed: %v", err)
		}
	}
	return result, runErr
}

func requireStrategyLive(ctx context.Context, system SystemOperations, strategyVersion string) error {
	if system == nil {
		return fmt.Errorf("%w: system operations are required", governance.ErrStrategyRuntimeUnavailable)
	}
	status := system.StrategyRuntime(ctx, strategyVersion)
	if !status.Ready {
		return fmt.Errorf("%w: %s", governance.ErrStrategyRuntimeUnavailable, status.Reason)
	}
	if status.Mode != governance.StrategyModeLive {
		return fmt.Errorf("%w: %s", governance.ErrStrategyPaused, status.Reason)
	}
	if strings.TrimSpace(status.CurrentStrategyVersion) != strings.TrimSpace(strategyVersion) {
		return fmt.Errorf("%w: runtime strategy=%s expected=%s", governance.ErrStrategyPaused, status.CurrentStrategyVersion, strategyVersion)
	}
	return nil
}

func (s ExecutionService) SetWakeup(callback func()) {
	if s.monitor != nil {
		s.monitor.SetWakeup(callback)
	}
}
