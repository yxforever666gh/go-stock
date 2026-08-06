package data

import (
	"context"
	"time"

	"go-stock/backend/execution"
)

// marketSummaryV150ExecutionEvaluator is local to the compatibility adapter so
// backend/data production files do not depend on backend/execution directly.
type marketSummaryV150ExecutionEvaluator interface {
	Evaluate(execution.ExecutionContext) (execution.EvaluationResult, error)
}

// CompatibilityExecutionMonitor keeps the existing V1.5.0 engine intact
// while removing delivery-layer knowledge of backend/data.
type CompatibilityExecutionMonitor struct {
	orderEvents marketSummaryV150OrderEventStore
	evaluator   marketSummaryV150ExecutionEvaluator
}

func NewCompatibilityExecutionMonitor(
	orderEvents marketSummaryV150OrderEventStore,
	evaluator marketSummaryV150ExecutionEvaluator,
) CompatibilityExecutionMonitor {
	return CompatibilityExecutionMonitor{orderEvents: orderEvents, evaluator: evaluator}
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
	if m.orderEvents == nil || m.evaluator == nil {
		return execution.MonitorResult{ObservedAt: now}, execution.ErrMonitorUnavailable
	}
	result, err := runMarketSummaryV150ExecutionMonitorWithStore(ctx, now, m.orderEvents, m.evaluator)
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
