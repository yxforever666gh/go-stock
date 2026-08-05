package service

import (
	"context"
	"fmt"
	"time"

	"go-stock/backend/execution"
)

type ExecutionService struct {
	monitor execution.Monitor
}

func NewExecutionService(monitor execution.Monitor) ExecutionService {
	return ExecutionService{monitor: monitor}
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

func (s ExecutionService) SetWakeup(callback func()) {
	if s.monitor != nil {
		s.monitor.SetWakeup(callback)
	}
}
