package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/execution"
	"go-stock/backend/governance"
	"go-stock/backend/models"
)

type recordingExecutionMonitor struct {
	runs   int
	result execution.MonitorResult
	err    error
}

func (m *recordingExecutionMonitor) ResolveWindow(time.Time) (execution.MonitorWindow, bool) {
	return execution.MonitorWindow{}, false
}
func (m *recordingExecutionMonitor) Run(context.Context, time.Time) (execution.MonitorResult, error) {
	m.runs++
	return m.result, m.err
}
func (*recordingExecutionMonitor) SetWakeup(func()) {}

type recordingSchedulerOperations struct {
	SchedulerOperations
	created *models.CronTaskRun
	updated *models.CronTaskRun
}

func (o *recordingSchedulerOperations) CreateTaskRun(_ context.Context, run *models.CronTaskRun) error {
	run.ID = 42
	o.created = cloneTaskRun(run)
	return nil
}
func (o *recordingSchedulerOperations) UpdateTaskRun(_ context.Context, run *models.CronTaskRun) error {
	o.updated = cloneTaskRun(run)
	return nil
}

type recordingSystemOperations struct {
	SystemOperations
	status governance.StrategyRuntimeStatus
}

func (o recordingSystemOperations) StrategyRuntime(context.Context, string) governance.StrategyRuntimeStatus {
	return o.status
}

func cloneTaskRun(run *models.CronTaskRun) *models.CronTaskRun {
	if run == nil {
		return nil
	}
	copy := *run
	return &copy
}

func TestExecutionServiceRejectsPausedStrategyBeforeAuditOrMonitor(t *testing.T) {
	monitor := &recordingExecutionMonitor{}
	scheduler := &recordingSchedulerOperations{}
	service := NewExecutionService(monitor, scheduler, recordingSystemOperations{status: governance.StrategyRuntimeStatus{
		Ready: true, Mode: governance.StrategyModePaused, CurrentStrategyVersion: "1.5.0", Reason: "maintenance",
	}})

	result, err := service.RunStrategyMonitor(context.Background(), time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC), "1.5.0")
	if !errors.Is(err, governance.ErrStrategyPaused) {
		t.Fatalf("error = %v", err)
	}
	if !result.ObservedAt.Equal(time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)) || monitor.runs != 0 || scheduler.created != nil {
		t.Fatalf("result=%+v monitor=%d audit=%+v", result, monitor.runs, scheduler.created)
	}
}

func TestExecutionServiceAuditsSuccessfulMonitorWarnings(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	monitor := &recordingExecutionMonitor{result: execution.MonitorResult{ObservedAt: now, Warnings: []string{"stale quote"}}}
	scheduler := &recordingSchedulerOperations{}
	service := NewExecutionService(monitor, scheduler, recordingSystemOperations{status: governance.StrategyRuntimeStatus{
		Ready: true, Mode: governance.StrategyModeLive, CurrentStrategyVersion: "1.5.0",
	}})

	if _, err := service.RunStrategyMonitor(context.Background(), now, "1.5.0"); err != nil {
		t.Fatalf("run monitor: %v", err)
	}
	if scheduler.created == nil || scheduler.created.TaskName != "strategy_v150_execution_monitor" || scheduler.created.Status != "started" {
		t.Fatalf("created audit = %+v", scheduler.created)
	}
	if scheduler.updated == nil || scheduler.updated.Status != "success" || scheduler.updated.ErrorMessage != "stale quote" {
		t.Fatalf("completed audit = %+v", scheduler.updated)
	}
}

func TestExecutionServiceAuditsMonitorFailure(t *testing.T) {
	monitor := &recordingExecutionMonitor{err: errors.New("quote unavailable")}
	scheduler := &recordingSchedulerOperations{}
	service := NewExecutionService(monitor, scheduler, recordingSystemOperations{status: governance.StrategyRuntimeStatus{
		Ready: true, Mode: governance.StrategyModeLive, CurrentStrategyVersion: "1.5.0",
	}})

	if _, err := service.RunStrategyMonitor(context.Background(), time.Now(), "1.5.0"); err == nil {
		t.Fatal("expected monitor failure")
	}
	if scheduler.updated == nil || scheduler.updated.Status != "failed" || scheduler.updated.ErrorMessage != "quote unavailable" {
		t.Fatalf("failed audit = %+v", scheduler.updated)
	}
}
