package data

import (
	"errors"
	"strings"
	"time"

	"go-stock/backend/execution"
	"go-stock/backend/marketdata"
	"go-stock/backend/strategy/v150"
)

var errMarketSummaryV150EvaluationResultIncomplete = errors.New("v1.5 execution evaluator returned an incomplete result")

// evaluateMarketSummaryV150ActivationBar is the narrow compatibility adapter
// from the existing cache/replay context to the public execution use case. A
// nil evaluator preserves non-production legacy recalculation behaviour while
// the production monitor always injects backend/execution.Evaluator.
func evaluateMarketSummaryV150ActivationBar(
	evaluator marketSummaryV150ExecutionEvaluator,
	frozen marketSummaryV150FrozenExecutionPlan,
	plan v150.TradePlan,
	previous, current v150.Bar,
	state v150.ActivationState,
) (v150.ActivationSignal, v150.ActivationState, error) {
	if evaluator == nil {
		signal, next := v150.DetectActivation(plan, previous, current, state)
		return signal, next, nil
	}
	previousBar := marketSummaryV150MinuteBar(plan.Symbol, previous)
	result, err := evaluator.Evaluate(execution.ExecutionContext{
		EvaluatedAt:     current.End,
		Rule:            marketSummaryV150EvaluatorFrozenRule(frozen, plan),
		PreviousBar:     &previousBar,
		CurrentBar:      marketSummaryV150MinuteBar(plan.Symbol, current),
		ActivationState: state,
	})
	if err != nil {
		return v150.ActivationSignal{}, state, err
	}
	signal := v150.ActivationSignal{}
	if result.Signal != nil {
		signal = *result.Signal
	}
	return signal, result.ActivationState, nil
}

func evaluateMarketSummaryV150EntryBar(
	evaluator marketSummaryV150ExecutionEvaluator,
	frozen marketSummaryV150FrozenExecutionPlan,
	order v150.EntryOrder,
	current v150.Bar,
	portfolio v150.PortfolioState,
	availableCash float64,
	scenario v150.SlippageScenario,
) (v150.EntryFillResult, error) {
	if evaluator == nil {
		return v150.TryFillEntryOnNextBar(order, current, portfolio, availableCash, scenario, v150.FixedStrategyV150Config()), nil
	}
	result, err := evaluator.Evaluate(execution.ExecutionContext{
		EvaluatedAt:   current.End,
		Rule:          marketSummaryV150EvaluatorFrozenRule(frozen, order.Plan),
		CurrentBar:    marketSummaryV150MinuteBar(order.Symbol, current),
		PendingOrder:  &order,
		Portfolio:     portfolio,
		AvailableCash: availableCash,
		Slippage:      scenario,
	})
	if err != nil {
		return v150.EntryFillResult{}, err
	}
	if result.Fill == nil {
		return v150.EntryFillResult{}, errMarketSummaryV150EvaluationResultIncomplete
	}
	return *result.Fill, nil
}

func evaluateMarketSummaryV150ExitBar(
	evaluator marketSummaryV150ExecutionEvaluator,
	frozen marketSummaryV150FrozenExecutionPlan,
	position v150.Position,
	current v150.Bar,
	scenario v150.SlippageScenario,
) (v150.ExitResult, v150.Position, error) {
	if evaluator == nil {
		result := v150.EvaluateExit(position, current, scenario, v150.FixedStrategyV150Config())
		if !result.Triggered {
			position = v150.AdvanceTrailingStop(position, current, v150.FixedStrategyV150Config())
		}
		return result, position, nil
	}
	result, err := evaluator.Evaluate(execution.ExecutionContext{
		EvaluatedAt: current.End,
		Rule:        marketSummaryV150EvaluatorFrozenRule(frozen, frozen.Plan),
		CurrentBar:  marketSummaryV150MinuteBar(position.Symbol, current),
		Position:    &position,
		Slippage:    scenario,
	})
	if err != nil {
		return v150.ExitResult{}, position, err
	}
	if result.Exit == nil {
		return v150.ExitResult{}, position, errMarketSummaryV150EvaluationResultIncomplete
	}
	if result.Position != nil {
		position = *result.Position
	}
	return *result.Exit, position, nil
}

func marketSummaryV150EvaluatorFrozenRule(
	frozen marketSummaryV150FrozenExecutionPlan,
	plan v150.TradePlan,
) execution.FrozenRule {
	symbol := normalizeRecommendStockCode(plan.Symbol)
	frozenAt := marketSummaryV150EvaluatorFrozenAt(frozen)
	return execution.FrozenRule{
		RunID:           frozen.Run.RunID,
		RuleID:          frozen.Rule.RuleID,
		StrategyVersion: v150.StrategyVersion,
		ConfigHash:      strings.TrimSpace(frozen.Run.ConfigHash),
		Candidate: v150.Candidate{
			Symbol: symbol,
			Sector: frozen.Candidate.Sector,
			Market: v150.ResolveMarket(symbol),
		},
		Plan:     plan,
		FrozenAt: frozenAt,
	}
}

func marketSummaryV150EvaluatorFrozenAt(frozen marketSummaryV150FrozenExecutionPlan) time.Time {
	if frozen.Rule.FrozenAt != nil && !frozen.Rule.FrozenAt.IsZero() {
		return *frozen.Rule.FrozenAt
	}
	if frozen.Run.FrozenAt != nil && !frozen.Run.FrozenAt.IsZero() {
		return *frozen.Run.FrozenAt
	}
	return frozen.Run.DecisionAt
}

func marketSummaryV150MinuteBar(symbol string, source v150.Bar) marketdata.MinuteBar {
	return marketdata.MinuteBar{
		Symbol: symbol, Index: source.Index, TradeDayIndex: source.TradeDayIndex,
		IntervalMinutes: source.IntervalMinutes, Start: source.Start, End: source.End,
		Open: source.Open, High: source.High, Low: source.Low, Close: source.Close,
		Volume: source.Volume, Amount: source.Amount, VolumeRatioSameSlot: source.VolumeRatioSameSlot,
		Completed: source.Completed, Suspended: source.Suspended,
		LimitUpLocked: source.LimitUpLocked, LimitDownLocked: source.LimitDownLocked,
	}
}
