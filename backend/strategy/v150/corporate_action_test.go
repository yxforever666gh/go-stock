package v150

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestApplyCorporateActionsAdjustsSharesCashAndEveryPriceRule(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	exDate := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	at := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	position := Position{
		Symbol: "000001.SZ", Quantity: 1000, EntryPrice: 10, InitialStop: 9,
		Target: 12, RiskPerShare: 1, ATR14: .5, HighestClose: 11,
		TrailingStop: 10.25,
	}
	result, err := ApplyCorporateActions(position, []CorporateAction{{
		EventID: "dividend-1", Symbol: position.Symbol, ExDate: exDate,
		AvailableAt: at.Add(-time.Minute), AdjustmentFactor: .8,
		CashDividend: .12, BonusRatio: .25,
	}}, at)
	if err != nil {
		t.Fatalf("apply action: %v", err)
	}
	if result.Position.Quantity != 1250 {
		t.Fatalf("quantity=%d want=1250", result.Position.Quantity)
	}
	if math.Abs(result.CashFlow-120) > 1e-9 || math.Abs(result.Position.CorporateActionCash-120) > 1e-9 {
		t.Fatalf("cash=%v positionCash=%v want=120", result.CashFlow, result.Position.CorporateActionCash)
	}
	checks := map[string][2]float64{
		"entry": {result.Position.EntryPrice, 8}, "stop": {result.Position.InitialStop, 7.2},
		"target": {result.Position.Target, 9.6}, "risk": {result.Position.RiskPerShare, .8},
		"atr": {result.Position.ATR14, .4}, "highest": {result.Position.HighestClose, 8.8},
		"trailing": {result.Position.TrailingStop, 8.2},
	}
	for name, values := range checks {
		if math.Abs(values[0]-values[1]) > 1e-9 {
			t.Fatalf("%s=%v want=%v", name, values[0], values[1])
		}
	}
	if len(result.Events) != 1 || result.Events[0].Type != EventCorporateAction || result.Events[0].Quantity != 1250 || result.Events[0].CashAmount != 120 {
		t.Fatalf("unexpected ledger event: %+v", result.Events)
	}
}

func TestApplyCorporateActionsRejectsUnresolvedRightsWithoutMutatingPosition(t *testing.T) {
	at := time.Date(2026, 8, 5, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	position := Position{Symbol: "000001.SZ", Quantity: 1000, EntryPrice: 10}
	result, err := ApplyCorporateActions(position, []CorporateAction{{
		EventID: "rights-1", Symbol: position.Symbol, ExDate: at, AvailableAt: at.Add(-time.Minute),
		AdjustmentFactor: .95, RightsRatio: .2, RightsPrice: 6,
	}}, at)
	if !errors.Is(err, ErrCorporateActionRightsUnresolved) {
		t.Fatalf("err=%v want unresolved rights", err)
	}
	if result.Position.Quantity != position.Quantity || result.Position.EntryPrice != position.EntryPrice {
		t.Fatalf("position mutated on rejected rights: %+v", result.Position)
	}
}

func TestApplyCorporateActionsRejectsLateObservation(t *testing.T) {
	at := time.Date(2026, 8, 5, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	_, err := ApplyCorporateActions(Position{Symbol: "000001.SZ", Quantity: 100}, []CorporateAction{{
		EventID: "late", Symbol: "000001.SZ", ExDate: at, AvailableAt: at.Add(time.Minute), AdjustmentFactor: 1,
	}}, at)
	if !errors.Is(err, ErrCorporateActionInvalid) {
		t.Fatalf("err=%v want invalid causal observation", err)
	}
}

func TestCorporateActionAdjustmentPreventsMechanicalExDateStop(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	at := time.Date(2026, 8, 5, 9, 30, 0, 0, loc)
	position := Position{
		Symbol: "000001.SZ", Market: MarketSZ, Quantity: 100,
		EntryAt: at.AddDate(0, 0, -1), EntryTradeDayIndex: 10,
		EntryPrice: 10, InitialStop: 9, Target: 12, RiskPerShare: 1, ATR14: .5,
	}
	action := CorporateAction{
		EventID: "bonus", Symbol: position.Symbol, ExDate: at, AvailableAt: at.Add(-time.Minute),
		AdjustmentFactor: .8, BonusRatio: .25,
	}
	applied, err := ApplyCorporateActions(position, []CorporateAction{action}, at)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	bar := Bar{Start: at, End: at.Add(15*time.Minute - time.Nanosecond), TradeDayIndex: 11, Completed: true, Open: 8, High: 8.3, Low: 7.5, Close: 8.1}
	if raw := EvaluateExit(position, bar, SlippageScenario{Name: "base", BPS: 10}, FixedStrategyV150Config()); !raw.Triggered || raw.Reason != ExitStop {
		t.Fatalf("unadjusted position should mechanically stop: %+v", raw)
	}
	if adjusted := EvaluateExit(applied.Position, bar, SlippageScenario{Name: "base", BPS: 10}, FixedStrategyV150Config()); adjusted.Triggered {
		t.Fatalf("adjusted position must not stop on mechanical ex-gap: %+v", adjusted)
	}
}

func TestApplyCorporateActionsToPlanAdjustsAllAbsoluteRules(t *testing.T) {
	at := time.Date(2026, 8, 5, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	plan := TradePlan{Symbol: "000001.SZ", Support: 10, EntryMin: 9.8, EntryMax: 10.2, Trigger: 11, ReferenceEntry: 10, TargetResistance: 13, Stop: 9, Target: 12, RiskPerShare: 1, ATR14: .5}
	adjusted, err := ApplyCorporateActionsToPlan(plan, []CorporateAction{{EventID: "factor", Symbol: plan.Symbol, ExDate: at, AvailableAt: at.Add(-time.Minute), AdjustmentFactor: .8}}, at)
	if err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	checks := [][2]float64{{adjusted.Support, 8}, {adjusted.EntryMin, 7.84}, {adjusted.EntryMax, 8.16}, {adjusted.Trigger, 8.8}, {adjusted.ReferenceEntry, 8}, {adjusted.TargetResistance, 10.4}, {adjusted.Stop, 7.2}, {adjusted.Target, 9.6}, {adjusted.RiskPerShare, .8}, {adjusted.ATR14, .4}}
	for _, check := range checks {
		if math.Abs(check[0]-check[1]) > 1e-9 {
			t.Fatalf("not every price rule was adjusted: %+v", adjusted)
		}
	}
}
