package data

import (
	"reflect"
	"testing"
	"time"

	"go-stock/backend/execution"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
)

func TestMarketSummaryV150ExecutionEvaluatorAdapterMatchesLegacyCore(t *testing.T) {
	loc := cnLocation()
	validFrom := time.Date(2026, 8, 6, 9, 30, 0, 0, loc)
	tradeDayIndex := marketSummaryV150TradeDayIndex(validFrom)
	plan := marketSummaryV150TestBreakoutPlan(validFrom)
	plan.ValidFromTradeDayIndex = tradeDayIndex
	frozenAt := validFrom.Add(-time.Minute)
	frozen := marketSummaryV150FrozenExecutionPlan{
		Run: models.StrategyRunSnapshot{
			RunID: "run-evaluator-equivalence", StrategyVersion: v150.StrategyVersion,
			DecisionAt: validFrom.Add(-30 * time.Minute), ConfigHash: v150.FixedStrategyV150ConfigHash(), FrozenAt: &frozenAt,
		},
		Rule: models.RuleSnapshot{
			RuleID: "rule-evaluator-equivalence", RunID: "run-evaluator-equivalence",
			StrategyVersion: v150.StrategyVersion, Symbol: plan.Symbol, FrozenAt: &frozenAt,
		},
		Candidate: models.CandidateSnapshot{Symbol: plan.Symbol, Sector: "bank"},
		Plan:      plan,
	}
	previous := v150.Bar{
		Index: 1, TradeDayIndex: tradeDayIndex, IntervalMinutes: 15,
		Start: validFrom, End: validFrom.Add(15*time.Minute - time.Second),
		Open: 9.90, High: 9.98, Low: 9.88, Close: 9.95, Completed: true,
	}
	current := v150.Bar{
		Index: 2, TradeDayIndex: tradeDayIndex, IntervalMinutes: 15,
		Start: previous.End.Add(time.Second), End: previous.End.Add(15 * time.Minute),
		Open: 9.96, High: 10.15, Low: 9.94, Close: 10.10,
		VolumeRatioSameSlot: 1.30, Completed: true,
	}

	legacySignal, legacyState, err := evaluateMarketSummaryV150ActivationBar(nil, frozen, plan, previous, current, v150.ActivationState{})
	if err != nil {
		t.Fatal(err)
	}
	actualSignal, actualState, err := evaluateMarketSummaryV150ActivationBar(execution.Evaluator{}, frozen, plan, previous, current, v150.ActivationState{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualSignal, legacySignal) || !reflect.DeepEqual(actualState, legacyState) {
		t.Fatalf("activation changed: actual=%+v/%+v legacy=%+v/%+v", actualSignal, actualState, legacySignal, legacyState)
	}
	if !legacySignal.Triggered {
		t.Fatal("fixture did not trigger activation")
	}

	order := v150.NewEntryOrder(legacySignal, plan, v150.Candidate{Symbol: plan.Symbol, Sector: frozen.Candidate.Sector, Market: v150.ResolveMarket(plan.Symbol)})
	next := v150.Bar{
		Index: 3, TradeDayIndex: tradeDayIndex, IntervalMinutes: 15,
		Start: current.End.Add(time.Second), End: current.End.Add(15 * time.Minute),
		Open: 10.12, High: 10.20, Low: 10.05, Close: 10.16, Completed: true,
	}
	portfolio := v150.PortfolioState{}
	cfg := v150.FixedStrategyV150Config()
	scenario := cfg.SlippageScenarios()[0]
	legacyFill, err := evaluateMarketSummaryV150EntryBar(nil, frozen, order, next, portfolio, cfg.PortfolioCash, scenario)
	if err != nil {
		t.Fatal(err)
	}
	actualFill, err := evaluateMarketSummaryV150EntryBar(execution.Evaluator{}, frozen, order, next, portfolio, cfg.PortfolioCash, scenario)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualFill, legacyFill) {
		t.Fatalf("entry changed: actual=%+v legacy=%+v", actualFill, legacyFill)
	}
	if legacyFill.Status != v150.FillFilled {
		t.Fatalf("fixture did not fill: %+v", legacyFill)
	}

	exitBar := v150.Bar{
		Index: 20, TradeDayIndex: tradeDayIndex + 1, IntervalMinutes: 15,
		Start: validFrom.Add(24 * time.Hour), End: validFrom.Add(24*time.Hour + 15*time.Minute),
		Open: legacyFill.Position.Target + 0.10, High: legacyFill.Position.Target + 0.20,
		Low: legacyFill.Position.Target, Close: legacyFill.Position.Target + 0.15, Completed: true,
	}
	legacyExit, legacyPosition, err := evaluateMarketSummaryV150ExitBar(nil, frozen, legacyFill.Position, exitBar, scenario)
	if err != nil {
		t.Fatal(err)
	}
	actualExit, actualPosition, err := evaluateMarketSummaryV150ExitBar(execution.Evaluator{}, frozen, legacyFill.Position, exitBar, scenario)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualExit, legacyExit) || !reflect.DeepEqual(actualPosition, legacyPosition) {
		t.Fatalf("exit changed: actual=%+v/%+v legacy=%+v/%+v", actualExit, actualPosition, legacyExit, legacyPosition)
	}
	if !legacyExit.Triggered {
		t.Fatal("fixture did not trigger exit")
	}
}
