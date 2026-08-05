package execution

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/strategy/v150"
)

func TestEvaluatorDelegatesActivationToFrozenV150Core(t *testing.T) {
	start := time.Date(2026, 8, 6, 9, 45, 0, 0, time.UTC)
	plan := v150.TradePlan{
		Symbol: "600000.SH", Path: v150.PathBreakout, Trigger: 10,
		ValidFromAt: start, ValidFromTradeDayIndex: 1, ValidTradeDays: 3,
		MinimumVolumeRatio: 1.2, EvaluationMinutes: 15,
	}
	previous := marketdata.MinuteBar{Index: 1, TradeDayIndex: 1, IntervalMinutes: 15, Start: start, End: start.Add(15 * time.Minute), Close: 9.9, Completed: true}
	current := marketdata.MinuteBar{Index: 2, TradeDayIndex: 1, IntervalMinutes: 15, Start: previous.End, End: previous.End.Add(15 * time.Minute), Close: 10.1, VolumeRatioSameSlot: 1.3, Completed: true}
	rule := FrozenRule{
		RunID: "run-1", RuleID: "rule-1", StrategyVersion: v150.StrategyVersion,
		ConfigHash: v150.FixedStrategyV150ConfigHash(), Candidate: v150.Candidate{Symbol: plan.Symbol},
		Plan: plan, FrozenAt: start.Add(-time.Minute),
	}
	got, err := (Evaluator{}).Evaluate(ExecutionContext{EvaluatedAt: current.End, Rule: rule, PreviousBar: &previous, CurrentBar: current})
	if err != nil {
		t.Fatal(err)
	}
	wantSignal, wantState := v150.DetectActivation(plan, toStrategyBar(previous), toStrategyBar(current), v150.ActivationState{})
	if got.Signal == nil || !reflect.DeepEqual(*got.Signal, wantSignal) || !reflect.DeepEqual(got.ActivationState, wantState) {
		t.Fatalf("activation differs from frozen core: got=%+v/%+v want=%+v/%+v", got.Signal, got.ActivationState, wantSignal, wantState)
	}
	if got.PendingOrder == nil || got.PendingOrder.Symbol != plan.Symbol {
		t.Fatalf("pending order missing: %+v", got.PendingOrder)
	}
}

func TestEvaluatorRejectsVersionOrConfigRouting(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	ctx := ExecutionContext{
		EvaluatedAt: now,
		Rule:        FrozenRule{RunID: "run", RuleID: "rule", StrategyVersion: "future", ConfigHash: "other", FrozenAt: now.Add(-time.Hour), Candidate: v150.Candidate{Symbol: "600000.SH"}, Plan: v150.TradePlan{Symbol: "600000.SH"}},
		CurrentBar:  marketdata.MinuteBar{Start: now.Add(-time.Minute), End: now},
	}
	if _, err := (Evaluator{}).Evaluate(ctx); !errors.Is(err, ErrInvalidExecutionContext) {
		t.Fatalf("Evaluate error = %v, want ErrInvalidExecutionContext", err)
	}
}
