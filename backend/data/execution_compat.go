package data

import (
	"context"
	"time"

	"go-stock/backend/execution"
)

// CompatibilityExecutionMonitor keeps the existing V1.5.0 engine intact
// while removing delivery-layer knowledge of backend/data.
type CompatibilityExecutionMonitor struct {
	orderEvents marketSummaryV150OrderEventStore
}

func NewCompatibilityExecutionMonitor(orderEvents marketSummaryV150OrderEventStore) CompatibilityExecutionMonitor {
	return CompatibilityExecutionMonitor{orderEvents: orderEvents}
}

func (CompatibilityExecutionMonitor) ResolveWindow(now time.Time) (execution.MonitorWindow, bool) {
	window, ok := ResolveMarketSummaryV150ExecutionWindow(now)
	return execution.MonitorWindow{SlotAt: window.SlotAt, EvaluationCutoff: window.EvaluationCutoff}, ok
}

func (m CompatibilityExecutionMonitor) Run(ctx context.Context, now time.Time) (execution.MonitorResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return execution.MonitorResult{ObservedAt: now}, err
	}
	if m.orderEvents == nil {
		return execution.MonitorResult{ObservedAt: now}, execution.ErrMonitorUnavailable
	}
	result, err := runMarketSummaryV150ExecutionMonitorWithStore(ctx, now, m.orderEvents)
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
