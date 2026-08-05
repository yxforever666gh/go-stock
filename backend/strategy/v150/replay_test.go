package v150

import (
	"testing"
	"time"
)

func TestPullbackNeedsCompletedRecoveryAndPersistsZoneTouch(t *testing.T) {
	plan := TradePlan{
		Path:                   PathPullback,
		DecisionTradeDayIndex:  100,
		ValidFromTradeDayIndex: 100,
		ValidFromAt:            strategyTestTime(9, 45),
		EvaluationMinutes:      15,
		Support:                10,
		EntryMin:               9.9,
		EntryMax:               10.1,
		ValidTradeDays:         3,
	}
	state := ActivationState{}
	touch := replayBar(1, 100, 9, 30, 9, 44)
	touch.Low, touch.High, touch.Close = 9.85, 10.05, 9.95
	if signal, nextState := DetectActivation(plan, Bar{}, touch, state); signal.Triggered || nextState.ZoneTouched {
		t.Fatalf("pre-valid bar affected state: signal=%+v state=%+v", signal, nextState)
	}

	touch.Start = strategyTestTime(9, 45)
	touch.End = strategyTestTime(9, 59)
	signal, state := DetectActivation(plan, Bar{}, touch, state)
	if signal.Triggered || !state.ZoneTouched {
		t.Fatalf("touch state = signal=%+v state=%+v", signal, state)
	}
	recovery := replayBar(2, 100, 10, 0, 10, 14)
	recovery.Low, recovery.High, recovery.Close = 10.05, 10.2, 10.1
	signal, state = DetectActivation(plan, touch, recovery, state)
	if !signal.Triggered || signal.Reason != "completed_15m_recovery" || !state.Signaled {
		t.Fatalf("recovery signal=%+v state=%+v", signal, state)
	}

	incomplete := recovery
	incomplete.Index = 3
	incomplete.Completed = false
	if signal, _ := DetectActivation(plan, recovery, incomplete, ActivationState{ZoneTouched: true}); signal.Triggered {
		t.Fatal("incomplete bar triggered")
	}
	wrongInterval := recovery
	wrongInterval.IntervalMinutes = 5
	if signal, _ := DetectActivation(plan, touch, wrongInterval, ActivationState{ZoneTouched: true}); signal.Triggered {
		t.Fatal("non-15m bar triggered pullback recovery")
	}
}

func TestActivationExpiresAfterThreeTradeDaysFromValidFrom(t *testing.T) {
	plan := TradePlan{Path: PathPullback, DecisionTradeDayIndex: 100, ValidFromTradeDayIndex: 101, EvaluationMinutes: 15, Support: 10, EntryMin: 9.9, EntryMax: 10.1, ValidTradeDays: 3}
	for offset := 0; offset < plan.ValidTradeDays; offset++ {
		valid := replayBar(offset+1, plan.ValidFromTradeDayIndex+offset, 10, 0, 10, 14)
		valid.Low, valid.High, valid.Close = 9.9, 10.1, 10
		if signal, _ := DetectActivation(plan, Bar{}, valid, ActivationState{}); !signal.Triggered {
			t.Fatalf("validFrom trade day %d should trigger: %+v", offset+1, signal)
		}
	}
	expired := replayBar(4, plan.ValidFromTradeDayIndex+plan.ValidTradeDays, 10, 0, 10, 14)
	expired.Low, expired.High, expired.Close = 9.9, 10.1, 10
	if signal, _ := DetectActivation(plan, Bar{}, expired, ActivationState{}); signal.Triggered || signal.Reason != RejectActivationExpired {
		t.Fatalf("fourth validFrom trade day should be expired: %+v", signal)
	}

	missingAnchor := plan
	missingAnchor.ValidFromTradeDayIndex = 0
	if signal, _ := DetectActivation(missingAnchor, Bar{}, expired, ActivationState{}); signal.Reason != RejectActivationAnchor {
		t.Fatalf("missing validFrom anchor was silently accepted: %+v", signal)
	}
}

func TestBreakoutRequiresTrueCrossingVolumeAndCutoff(t *testing.T) {
	plan := TradePlan{
		Path:                   PathBreakout,
		DecisionTradeDayIndex:  100,
		ValidFromTradeDayIndex: 100,
		Trigger:                12,
		MinimumVolumeRatio:     1.2,
		NoActivationAfterMin:   14 * 60,
		ValidTradeDays:         3,
	}
	previous := replayBar(1, 100, 13, 30, 13, 44)
	previous.Close = 11.99
	current := replayBar(2, 100, 13, 45, 14, 0)
	current.Close = 12.01
	current.VolumeRatioSameSlot = 1.2
	if signal, _ := DetectActivation(plan, previous, current, ActivationState{}); !signal.Triggered {
		t.Fatalf("boundary crossing did not trigger: %+v", signal)
	}

	equalPrevious := previous
	equalPrevious.Close = 12
	if signal, _ := DetectActivation(plan, equalPrevious, current, ActivationState{}); !signal.Triggered {
		t.Fatal("previous close at threshold should permit a strict current-bar up-cross")
	}
	abovePrevious := previous
	abovePrevious.Close = 12.01
	if signal, _ := DetectActivation(plan, abovePrevious, current, ActivationState{}); signal.Triggered {
		t.Fatal("previous close above resistance is not a new crossing")
	}
	lowVolume := current
	lowVolume.VolumeRatioSameSlot = 1.1999
	if signal, _ := DetectActivation(plan, previous, lowVolume, ActivationState{}); signal.Triggered {
		t.Fatal("low volume triggered")
	}
	late := current
	late.End = time.Date(2026, 8, 4, 14, 0, 1, 0, current.End.Location())
	if signal, _ := DetectActivation(plan, previous, late, ActivationState{}); signal.Triggered {
		t.Fatal("activation after 14:00 triggered")
	}
}

func TestEntryFillsOnlyNextBarWithRoundLotAndAdverseSlippage(t *testing.T) {
	cfg := FixedStrategyV150Config()
	ctx := validTestContext()
	candidate := validTestCandidate(ctx.AsOf)
	candidate.MA10 = 10
	candidate.MA20 = 9.8
	planResult := BuildTradePlans(ctx, candidate, RegimeDecision{Regime: RegimeRiskOn}, cfg)[0]
	if !planResult.Accepted {
		t.Fatalf("plan rejected: %+v", planResult)
	}
	signal := ActivationSignal{Triggered: true, Path: PathPullback, At: time.Date(2026, 8, 4, 9, 44, 59, 0, ctx.AsOf.Location()), BarIndex: 1, TradeDayIndex: 100, SignalClose: 10}
	order := NewEntryOrder(signal, planResult.Plan, candidate)

	tooEarly := replayBar(1, 100, 9, 44, 9, 44)
	if got := TryFillEntryOnNextBar(order, tooEarly, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillPending {
		t.Fatalf("same bar status = %s", got.Status)
	}

	next := replayBar(2, 100, 9, 45, 9, 59)
	next.Open, next.High, next.Low, next.Close = 10, 10.2, 9.9, 10.1
	got := TryFillEntryOnNextBar(order, next, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg)
	if got.Status != FillFilled || got.Position.Quantity != 900 || len(got.Events) != 3 {
		t.Fatalf("entry fill = %+v", got)
	}
	if !got.Events[0].At.Before(got.Events[1].At) || got.Events[1].At.After(got.Events[2].At) {
		t.Fatalf("event causality changed: %+v", got.Events)
	}
	assertNear(t, got.Position.EntryPrice, 10.01)
	if got.Plan.ReferenceEntry != got.Position.EntryPrice {
		t.Fatalf("plan was not repriced: %+v", got.Plan)
	}

	skipped := next
	skipped.Index = 3
	if got := TryFillEntryOnNextBar(order, skipped, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillExpired || got.Reason != RejectNotNextBar {
		t.Fatalf("skipped-next result = %+v", got)
	}
}

func TestEntryRejectsIncompleteSuspendedLimitUpAndPortfolioConflicts(t *testing.T) {
	cfg := FixedStrategyV150Config()
	candidate := validTestCandidate(validTestContext().AsOf)
	plan := TradePlan{Path: PathPullback, ATR14: 0.4, TargetResistance: 14, NegativeOvernightGapRisk60: 0.03, MaxHoldTradeDays: 10}
	signal := ActivationSignal{Triggered: true, At: time.Date(2026, 8, 4, 9, 44, 59, 0, candidate.ListedAt.Location()), BarIndex: 1, TradeDayIndex: 100, SignalClose: 10}
	order := NewEntryOrder(signal, plan, candidate)
	base := replayBar(2, 100, 9, 45, 9, 59)
	base.Open, base.High, base.Low, base.Close = 10, 10, 10, 10

	tests := []struct {
		name   string
		bar    Bar
		state  PortfolioState
		reason string
	}{
		{"incomplete", func() Bar { b := base; b.Completed = false; return b }(), PortfolioState{}, RejectIncompleteBar},
		{"suspended", func() Bar { b := base; b.Suspended = true; return b }(), PortfolioState{}, RejectSuspended},
		{"limit up", func() Bar { b := base; b.LimitUpLocked = true; return b }(), PortfolioState{}, RejectLimitUpBuy},
		{"duplicate", base, PortfolioState{PendingSymbols: map[string]bool{candidate.Symbol: true}}, RejectDuplicatePending},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := TryFillEntryOnNextBar(order, test.bar, test.state, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg)
			if got.Status != FillRejected || got.Reason != test.reason {
				t.Fatalf("result = %+v", got)
			}
		})
	}

	missingSignal := order
	missingSignal.SignalClose = 0
	if got := TryFillEntryOnNextBar(missingSignal, base, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillRejected || got.Reason != RejectInvalidSignal {
		t.Fatalf("missing activation price was not rejected: %+v", got)
	}
	highGap := base
	highGap.Open = 10.4001
	if got := TryFillEntryOnNextBar(order, highGap, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillRejected || got.Reason != RejectEntryGap {
		t.Fatalf("entry gap chase was not rejected: %+v", got)
	}
	downGap := base
	downGap.Open = 9.5999
	if got := TryFillEntryOnNextBar(order, downGap, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillRejected || got.Reason != RejectEntryGap {
		t.Fatalf("adverse entry gap was not rejected: %+v", got)
	}
	pullbackInvalidated := order
	pullbackInvalidated.Plan.Support = 10.1
	if got := TryFillEntryOnNextBar(pullbackInvalidated, base, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillRejected || got.Reason != RejectSetupInvalidated {
		t.Fatalf("failed pullback recovery was not rejected: %+v", got)
	}
	breakoutInvalidated := order
	breakoutInvalidated.Plan.Path = PathBreakout
	breakoutInvalidated.Plan.Trigger = 10
	breakoutInvalidated.SignalClose = 10.1
	if got := TryFillEntryOnNextBar(breakoutInvalidated, base, PortfolioState{}, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillRejected || got.Reason != RejectSetupInvalidated {
		t.Fatalf("failed breakout was not rejected: %+v", got)
	}
}

func TestEntryCapacityCountsFilledHoldingsNotPendingRules(t *testing.T) {
	cfg := FixedStrategyV150Config()
	candidate := validTestCandidate(validTestContext().AsOf)
	plan := TradePlan{Path: PathPullback, ATR14: 0.4, TargetResistance: 14, NegativeOvernightGapRisk60: 0.03, MaxHoldTradeDays: 10}
	signal := ActivationSignal{Triggered: true, At: time.Date(2026, 8, 4, 9, 44, 59, 0, candidate.ListedAt.Location()), BarIndex: 1, TradeDayIndex: 100, SignalClose: 10}
	order := NewEntryOrder(signal, plan, candidate)
	next := replayBar(2, 100, 9, 45, 9, 59)
	next.Open, next.High, next.Low, next.Close = 10, 10, 10, 10

	fourOpenWithPending := PortfolioState{
		OpenSymbols:    map[string]bool{"000001.SZ": true, "000002.SZ": true, "000003.SZ": true, "000004.SZ": true},
		PendingSymbols: map[string]bool{"000005.SZ": true, "000006.SZ": true, "000007.SZ": true},
	}
	if got := TryFillEntryOnNextBar(order, next, fourOpenWithPending, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillFilled {
		t.Fatalf("pending rules reserved the fifth holdings slot: %+v", got)
	}

	fiveOpen := fourOpenWithPending
	fiveOpen.OpenSymbols = map[string]bool{"000001.SZ": true, "000002.SZ": true, "000003.SZ": true, "000004.SZ": true, "000005.SZ": true}
	if got := TryFillEntryOnNextBar(order, next, fiveOpen, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg); got.Status != FillRejected || got.Reason != RejectPortfolioCapacity {
		t.Fatalf("sixth filled holding was not rejected: %+v", got)
	}
}

func TestT1SameBarStopFirstGapAndLimitDown(t *testing.T) {
	cfg := FixedStrategyV150Config()
	position := Position{
		Symbol:             "600000.SH",
		Market:             MarketSH,
		Quantity:           100,
		EntryTradeDayIndex: 100,
		EntryPrice:         10,
		InitialStop:        9,
		Target:             11,
		RiskPerShare:       1,
		ATR14:              0.4,
		MaxHoldTradeDays:   10,
	}
	both := replayBar(1, 100, 10, 0, 10, 14)
	both.Open, both.Low, both.High, both.Close = 10, 8, 12, 10
	if got := EvaluateExit(position, both, cfg.SlippageScenarios()[0], cfg); got.Triggered {
		t.Fatal("T+0 exit was allowed")
	}
	both.TradeDayIndex = 101
	got := EvaluateExit(position, both, cfg.SlippageScenarios()[0], cfg)
	if !got.Triggered || got.Reason != ExitStop || got.Cost.RawPrice != 9 {
		t.Fatalf("same-bar result = %+v", got)
	}

	gap := both
	gap.Open, gap.Low, gap.High = 8.5, 8.4, 8.8
	got = EvaluateExit(position, gap, cfg.SlippageScenarios()[0], cfg)
	if !got.Triggered || got.Reason != ExitStop || got.Cost.RawPrice != 8.5 || !(got.Cost.EffectivePrice < 8.5) {
		t.Fatalf("gap stop = %+v", got)
	}
	gap.LimitDownLocked = true
	if got := EvaluateExit(position, gap, cfg.SlippageScenarios()[0], cfg); got.Triggered {
		t.Fatalf("limit-down sell should wait: %+v", got)
	}
	gap.LimitDownLocked = false
	gap.Suspended = true
	if got := EvaluateExit(position, gap, cfg.SlippageScenarios()[0], cfg); got.Triggered {
		t.Fatalf("suspended sell should wait: %+v", got)
	}
}

func TestTrailingStartsAtOneRAndUsesHighestCloseMinusOnePointFiveATR(t *testing.T) {
	cfg := FixedStrategyV150Config()
	position := Position{EntryPrice: 10, RiskPerShare: 0.5, ATR14: 0.4, InitialStop: 9.5, HighestClose: 10}
	below := replayBar(1, 100, 10, 0, 10, 14)
	below.Close = 10.49
	position = AdvanceTrailingStop(position, below, cfg)
	if position.TrailingActive || position.TrailingStop != 0 {
		t.Fatalf("trailing activated early: %+v", position)
	}
	atOneR := below
	atOneR.Index = 2
	atOneR.Close = 10.5
	position = AdvanceTrailingStop(position, atOneR, cfg)
	if !position.TrailingActive {
		t.Fatal("trailing did not activate at +1R")
	}
	assertNear(t, position.TrailingStop, 9.9)
	higher := atOneR
	higher.Index = 3
	higher.Close = 11
	position = AdvanceTrailingStop(position, higher, cfg)
	assertNear(t, position.TrailingStop, 10.4)
}

func TestDay10TimeExitAtFirstBarAtOrAfter1445(t *testing.T) {
	cfg := FixedStrategyV150Config()
	position := Position{
		Symbol: "600000.SH", Market: MarketSH, Quantity: 100,
		EntryTradeDayIndex: 100, EntryPrice: 10, InitialStop: 9, Target: 12,
		MaxHoldTradeDays: 10,
	}
	tooSoon := replayBar(1, 109, 14, 44, 14, 44)
	tooSoon.Open, tooSoon.Low, tooSoon.High, tooSoon.Close = 10, 9.8, 10.2, 10
	if got := EvaluateExit(position, tooSoon, cfg.SlippageScenarios()[0], cfg); got.Triggered {
		t.Fatalf("14:44 exited: %+v", got)
	}
	exitBar := replayBar(2, 109, 14, 45, 14, 59)
	exitBar.Open, exitBar.Low, exitBar.High, exitBar.Close = 10.2, 10, 10.3, 10.1
	got := EvaluateExit(position, exitBar, cfg.SlippageScenarios()[0], cfg)
	if !got.Triggered || got.Reason != ExitTime || got.Cost.RawPrice != 10.2 {
		t.Fatalf("day-10 exit = %+v", got)
	}
}

func TestDay10TimeExitPendingSellsAtNextTradableBar(t *testing.T) {
	cfg := FixedStrategyV150Config()
	position := Position{
		Symbol: "600000.SH", Market: MarketSH, Quantity: 100,
		EntryTradeDayIndex: 100, EntryPrice: 10, InitialStop: 9, Target: 12,
		MaxHoldTradeDays: 10,
	}
	blocked := replayBar(1, 109, 14, 45, 14, 59)
	blocked.Open, blocked.Low, blocked.High, blocked.Close = 10.2, 10, 10.3, 10.1
	blocked.LimitDownLocked = true
	if got := EvaluateExit(position, blocked, cfg.SlippageScenarios()[0], cfg); got.Triggered {
		t.Fatalf("untradable day-10 bar exited: %+v", got)
	}

	nextTradable := replayBar(2, 110, 9, 30, 9, 44)
	// Even if this completed bar later spans both stop and target, the pending
	// time-exit order was already executable at its open.
	nextTradable.Open, nextTradable.Low, nextTradable.High, nextTradable.Close = 10.1, 8.8, 12.2, 10
	got := EvaluateExit(position, nextTradable, cfg.SlippageScenarios()[0], cfg)
	if !got.Triggered || got.Reason != ExitTime || got.Cost.RawPrice != 10.1 || !got.At.Equal(nextTradable.Start) {
		t.Fatalf("pending time exit did not use next tradable bar: %+v", got)
	}
}

func replayBar(index, tradeDay, startHour, startMinute, endHour, endMinute int) Bar {
	return Bar{
		Index: index, TradeDayIndex: tradeDay, IntervalMinutes: 15,
		Start: strategyTestTime(startHour, startMinute),
		End:   strategyTestTime(endHour, endMinute),
		Open:  10, High: 10, Low: 10, Close: 10,
		Completed: true,
	}
}
