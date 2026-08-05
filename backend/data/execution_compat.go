package data

import (
	"context"
	"time"

	"go-stock/backend/execution"
)

// CompatibilityExecutionMonitor keeps the existing V1.5.0 engine intact
// while removing delivery-layer knowledge of backend/data.
type CompatibilityExecutionMonitor struct{}

func NewCompatibilityExecutionMonitor() CompatibilityExecutionMonitor {
	return CompatibilityExecutionMonitor{}
}

func (CompatibilityExecutionMonitor) ResolveWindow(now time.Time) (execution.MonitorWindow, bool) {
	window, ok := ResolveMarketSummaryV150ExecutionWindow(now)
	return execution.MonitorWindow{SlotAt: window.SlotAt, EvaluationCutoff: window.EvaluationCutoff}, ok
}

func (CompatibilityExecutionMonitor) Run(ctx context.Context, now time.Time) (execution.MonitorResult, error) {
	if err := ctx.Err(); err != nil {
		return execution.MonitorResult{ObservedAt: now}, err
	}
	result, err := RunMarketSummaryV150ExecutionMonitor(now)
	return execution.MonitorResult{
		ObservedAt: result.ObservedAt, EvaluationCutoff: result.EvaluationCutoff,
		PendingCount: result.PendingCount, OpenCount: result.OpenCount,
		ProcessedCount: result.ProcessedCount, SkippedCount: result.SkippedCount,
		Warnings: append([]string(nil), result.Warnings...),
	}, err
}

func (CompatibilityExecutionMonitor) SetWakeup(callback func()) {
	SetMarketSummaryV150ExecutionMonitorWakeup(callback)
}

var _ execution.Monitor = CompatibilityExecutionMonitor{}
