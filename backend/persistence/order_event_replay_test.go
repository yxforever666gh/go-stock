package persistence

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"go-stock/backend/models"
)

func frozenReplayEvent(id, eventType string, sequence int, at time.Time, price, quantity, fees float64) models.OrderEvent {
	frozenAt := at.Add(time.Hour)
	event := models.OrderEvent{
		EventID:         id,
		RunID:           "run-replay-1",
		RuleID:          "rule-replay-1",
		StrategyVersion: "1.5.0",
		TradeDate:       "2026-08-04",
		Symbol:          "000001.SZ",
		EventType:       eventType,
		Sequence:        sequence,
		EventAt:         at,
		Price:           price,
		Quantity:        quantity,
		Fees:            fees,
		PayloadJSON:     `{}`,
		FrozenAt:        &frozenAt,
	}
	sealed := []models.OrderEvent{event}
	if err := SealStrategyOrderEvents(sealed); err != nil {
		panic(err)
	}
	return sealed[0]
}

func resealReplayEvents(t *testing.T, events ...*models.OrderEvent) {
	t.Helper()
	for _, event := range events {
		sealed := []models.OrderEvent{*event}
		if err := SealStrategyOrderEvents(sealed); err != nil {
			t.Fatal(err)
		}
		*event = sealed[0]
	}
}

func TestReplayFrozenOrderEventsUsesPersistedAccounting(t *testing.T) {
	entryAt := time.Date(2026, 8, 4, 1, 45, 0, 0, time.UTC)
	exitAt := time.Date(2026, 8, 5, 6, 45, 0, 0, time.UTC)
	frozenAt := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	entry := frozenReplayEvent("fill-1", "fill", 3, entryAt, 10, 1000, 5.1)
	exit := frozenReplayEvent("exit-1", "exit_fill", 7, exitAt, 10.5, 1000, 10.355)
	exit.Reason = "target"
	resealReplayEvents(t, &exit)

	trades, stats, resultHash, err := ReplayFrozenOrderEvents("bt-replay", "1.5.0", []models.OrderEvent{exit, entry}, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || resultHash == "" {
		t.Fatalf("unexpected replay output: trades=%+v hash=%q", trades, resultHash)
	}
	trade := trades[0]
	if trade.EntryPrice != 10 || trade.ExitPrice != 10.5 || trade.Quantity != 1000 || trade.Fees != 15.455 {
		t.Fatalf("persisted accounting fields not retained: %+v", trade)
	}
	if trade.GrossPnL != 500 || trade.NetPnL != 484.545 {
		t.Fatalf("pnl = gross %.8f net %.8f, want 500/484.545", trade.GrossPnL, trade.NetPnL)
	}
	wantReturn := roundedReplayValue(484.545 / 10005.1 * 100)
	if trade.ReturnPct != wantReturn {
		t.Fatalf("return = %.8f, want %.8f", trade.ReturnPct, wantReturn)
	}
	if stats.TradeCount != 1 || stats.ClosedTradeCount != 1 || stats.EndingEquity != 100484.545 || stats.NetPnL != 484.545 || stats.NetMeanReturnPct != wantReturn || stats.ProfitFactor != nil || stats.ProfitFactorText != "+Inf" || stats.Stress20EndingEquity >= stats.EndingEquity || stats.Stress50EndingEquity >= stats.Stress20EndingEquity {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	againTrades, againStats, againHash, err := ReplayFrozenOrderEvents("bt-replay", "1.5.0", []models.OrderEvent{entry, exit}, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if resultHash != againHash || !reflect.DeepEqual(trades, againTrades) || !reflect.DeepEqual(stats, againStats) {
		t.Fatalf("replay is not deterministic:\nfirst=%+v %+v %s\nsecond=%+v %+v %s", trades, stats, resultHash, againTrades, againStats, againHash)
	}
}

func TestReplayFrozenOrderEventsProfitFactorUsesNetPnL(t *testing.T) {
	day1 := time.Date(2026, 8, 4, 1, 45, 0, 0, time.UTC)
	day2 := time.Date(2026, 8, 5, 6, 45, 0, 0, time.UTC)
	frozenAt := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	events := []models.OrderEvent{
		frozenReplayEvent("fill-win", "fill", 1, day1, 10, 1000, 5.1),
		frozenReplayEvent("exit-win", "exit_fill", 2, day2, 11.1, 1000, 10.661),
	}
	secondEntryAt := day1.Add(24 * time.Hour)
	secondEntry := frozenReplayEvent("fill-loss", "fill", 3, secondEntryAt, 20, 500, 5.1)
	secondEntry.RunID = "run-replay-2"
	secondEntry.Sequence = 1
	secondEntry.Symbol = "600000.SH"
	secondEntry.RuleID = "rule-replay-2"
	secondExit := frozenReplayEvent("exit-loss", "exit_fill", 4, secondEntryAt.Add(24*time.Hour), 19.5, 500, 9.9725)
	secondExit.RunID = secondEntry.RunID
	secondExit.Sequence = 2
	secondExit.Symbol = secondEntry.Symbol
	secondExit.RuleID = secondEntry.RuleID
	resealReplayEvents(t, &secondEntry, &secondExit)
	events = append(events, secondEntry, secondExit)

	_, stats, _, err := ReplayFrozenOrderEvents("bt-pf", "1.5.0", events, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	// Winner: +1100 gross - 15.55 fees. Loser: -250 gross - 15.0775 fees.
	winner := 1084.239
	loss := 265.0725
	if stats.ProfitFactor == nil || *stats.ProfitFactor != roundedReplayValue(winner/loss) {
		t.Fatalf("profit factor = %+v, want %.8f", stats.ProfitFactor, winner/loss)
	}
	if stats.GrossProfit != winner || stats.GrossLoss != loss || stats.NetPnL != roundedReplayValue(winner-loss) || stats.TradeCount != 2 {
		t.Fatalf("unexpected net stats: %+v", stats)
	}
}

func TestReplayFrozenOrderEventsRejectsMissingAndCausalErrors(t *testing.T) {
	entryAt := time.Date(2026, 8, 4, 1, 45, 0, 0, time.UTC)
	exitAt := time.Date(2026, 8, 5, 6, 45, 0, 0, time.UTC)
	frozenAt := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	entry := frozenReplayEvent("fill-1", "fill", 1, entryAt, 10, 1000, 5.1)
	exit := frozenReplayEvent("exit-1", "exit_fill", 2, exitAt, 9, 1000, 9.59)

	tests := []struct {
		name   string
		events []models.OrderEvent
	}{
		{name: "unmatched exit", events: []models.OrderEvent{exit}},
		{name: "exit before entry", events: func() []models.OrderEvent {
			bad := exit
			bad.EventAt = entryAt.Add(-time.Minute)
			return []models.OrderEvent{entry, bad}
		}()},
		{name: "quantity mismatch", events: func() []models.OrderEvent { bad := exit; bad.Quantity = 200; return []models.OrderEvent{entry, bad} }()},
		{name: "duplicate sequence", events: func() []models.OrderEvent { bad := exit; bad.Sequence = 1; return []models.OrderEvent{entry, bad} }()},
		{name: "non board lot entry", events: func() []models.OrderEvent { bad := entry; bad.Quantity = 99; return []models.OrderEvent{bad, exit} }()},
		{name: "negative fee", events: func() []models.OrderEvent { bad := entry; bad.Fees = -1; return []models.OrderEvent{bad, exit} }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := ReplayFrozenOrderEvents("bt-invalid", "1.5.0", tt.events, frozenAt)
			if !errors.Is(err, ErrInvalidOrderEventReplay) {
				t.Fatalf("error = %v, want ErrInvalidOrderEventReplay", err)
			}
		})
	}
}

func TestReplayFrozenOrderEventsKeepsOpenPositionAtCohortEnd(t *testing.T) {
	entryAt := time.Date(2026, 8, 4, 1, 45, 0, 0, time.UTC)
	frozenAt := entryAt.Add(24 * time.Hour)
	entry := frozenReplayEvent("fill-open", "fill", 1, entryAt, 10, 1000, 5.1)
	trades, stats, _, err := ReplayFrozenOrderEvents("bt-open", "1.5.0", []models.OrderEvent{entry}, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].ExitAt != nil || trades[0].ExitReason != "open_at_end" || stats.OpenPositionCount != 1 || stats.ClosedTradeCount != 0 || !(stats.EndingEquity < 100000) {
		t.Fatalf("open position was not retained/accounted: trades=%+v stats=%+v", trades, stats)
	}
}

func TestReplayFrozenOrderEventsEnforcesPortfolioCaps(t *testing.T) {
	cn := time.FixedZone("Asia/Shanghai", 8*60*60)
	makeFill := func(index int, at time.Time) models.OrderEvent {
		event := frozenReplayEvent(
			fmt.Sprintf("fill-cap-%d", index),
			"fill",
			1,
			at,
			10,
			1000,
			5.1,
		)
		event.RunID = fmt.Sprintf("run-cap-%d", index)
		event.RuleID = fmt.Sprintf("rule-cap-%d", index)
		event.Symbol = fmt.Sprintf("%06d.SZ", index+1)
		resealReplayEvents(t, &event)
		return event
	}
	t.Run("daily entry cap", func(t *testing.T) {
		at := time.Date(2026, 8, 4, 10, 0, 0, 0, cn)
		events := []models.OrderEvent{makeFill(1, at), makeFill(2, at.Add(time.Minute)), makeFill(3, at.Add(2*time.Minute))}
		_, _, _, err := ReplayFrozenOrderEvents("bt-daily-cap", "1.5.0", events, at.Add(24*time.Hour))
		if !errors.Is(err, ErrInvalidOrderEventReplay) {
			t.Fatalf("daily cap error = %v", err)
		}
	})
	t.Run("maximum five open positions", func(t *testing.T) {
		events := make([]models.OrderEvent, 0, 6)
		for i := 0; i < 6; i++ {
			dayOffset := i / 2
			minuteOffset := i % 2
			at := time.Date(2026, 8, 4+dayOffset, 10, minuteOffset, 0, 0, cn)
			events = append(events, makeFill(i+10, at))
		}
		_, _, _, err := ReplayFrozenOrderEvents("bt-open-cap", "1.5.0", events, time.Date(2026, 8, 10, 0, 0, 0, 0, cn))
		if !errors.Is(err, ErrInvalidOrderEventReplay) {
			t.Fatalf("open-position cap error = %v", err)
		}
	})
	t.Run("same symbol cannot be opened twice", func(t *testing.T) {
		at := time.Date(2026, 8, 4, 10, 0, 0, 0, cn)
		first, second := makeFill(31, at), makeFill(32, at.Add(time.Minute))
		second.Symbol = first.Symbol
		resealReplayEvents(t, &second)
		_, _, _, err := ReplayFrozenOrderEvents("bt-symbol-cap", "1.5.0", []models.OrderEvent{first, second}, at.Add(24*time.Hour))
		if !errors.Is(err, ErrInvalidOrderEventReplay) {
			t.Fatalf("duplicate-symbol error = %v", err)
		}
	})
}

func TestReplayFrozenOrderEventsEnforcesFrozenRegimeSectorAndCooldownPolicy(t *testing.T) {
	cn := time.FixedZone("Asia/Shanghai", 8*60*60)
	makeFill := func(id, runID, ruleID, symbol string, at time.Time) models.OrderEvent {
		event := frozenReplayEvent(id, "fill", 1, at, 10, 1000, 5.1)
		event.RunID, event.RuleID, event.Symbol = runID, ruleID, symbol
		resealReplayEvents(t, &event)
		return event
	}

	t.Run("neutral cap one", func(t *testing.T) {
		at := time.Date(2026, 8, 4, 10, 0, 0, 0, cn)
		events := []models.OrderEvent{
			makeFill("neutral-a", "neutral-run", "neutral-rule-a", "000001.SZ", at),
			makeFill("neutral-b", "neutral-run", "neutral-rule-b", "000002.SZ", at.Add(time.Minute)),
		}
		policy := orderEventReplayPolicy{dailyCapByRun: map[string]int{"neutral-run": 1}, sectorByRule: map[string]string{}, metadataComplete: true}
		_, _, _, err := replayFrozenOrderEvents("bt-neutral", "1.5.0", events, at.Add(24*time.Hour), policy)
		if !errors.Is(err, ErrInvalidOrderEventReplay) {
			t.Fatalf("neutral daily cap error=%v", err)
		}
	})

	t.Run("one sector entry per day", func(t *testing.T) {
		at := time.Date(2026, 8, 4, 10, 0, 0, 0, cn)
		events := []models.OrderEvent{
			makeFill("sector-a", "risk-run", "sector-rule-a", "000011.SZ", at),
			makeFill("sector-b", "risk-run", "sector-rule-b", "000012.SZ", at.Add(time.Minute)),
		}
		policy := orderEventReplayPolicy{
			dailyCapByRun: map[string]int{"risk-run": 2},
			sectorByRule: map[string]string{
				"risk-run\x00sector-rule-a": "bank",
				"risk-run\x00sector-rule-b": "bank",
			},
			metadataComplete: true,
		}
		_, _, _, err := replayFrozenOrderEvents("bt-sector", "1.5.0", events, at.Add(24*time.Hour), policy)
		if !errors.Is(err, ErrInvalidOrderEventReplay) {
			t.Fatalf("sector cap error=%v", err)
		}
	})

	t.Run("five trading day stop cooldown", func(t *testing.T) {
		entryAt := time.Date(2026, 8, 4, 10, 0, 0, 0, cn)
		exitAt := time.Date(2026, 8, 5, 10, 0, 0, 0, cn)
		reentryAt := time.Date(2026, 8, 7, 10, 0, 0, 0, cn)
		entry := makeFill("cooldown-entry", "old-run", "old-rule", "000021.SZ", entryAt)
		exit := frozenReplayEvent("cooldown-stop", "exit_fill", 2, exitAt, 9.5, 1000, 9.845)
		exit.RunID, exit.RuleID, exit.Symbol, exit.Reason = entry.RunID, entry.RuleID, entry.Symbol, "stop"
		resealReplayEvents(t, &exit)
		reentry := makeFill("cooldown-reentry", "new-run", "new-rule", entry.Symbol, reentryAt)
		policy := orderEventReplayPolicy{
			dailyCapByRun: map[string]int{"old-run": 2, "new-run": 2},
			sectorByRule: map[string]string{
				"old-run\x00old-rule": "technology",
				"new-run\x00new-rule": "technology",
			},
			metadataComplete: true,
		}
		_, _, _, err := replayFrozenOrderEvents("bt-cooldown", "1.5.0", []models.OrderEvent{entry, exit, reentry}, reentryAt.Add(24*time.Hour), policy)
		if !errors.Is(err, ErrInvalidOrderEventReplay) {
			t.Fatalf("cooldown error=%v", err)
		}
	})
}

func TestBuildOrderEventReplayPolicyReadsRunCapAndCandidateSector(t *testing.T) {
	inputs := FrozenStrategyInputs{
		Runs:       []models.StrategyRunSnapshot{{RunID: "neutral-run", RuleCount: 1, PayloadJSON: `{"run":{"regime":{"dailyCap":1}}}`}},
		Candidates: []models.CandidateSnapshot{{CandidateID: "candidate-a", RunID: "neutral-run", Symbol: "000001.SZ", Sector: "bank"}},
		Rules:      []models.RuleSnapshot{{RuleID: "rule-a", RunID: "neutral-run", CandidateID: "candidate-a", Symbol: "000001.SZ"}},
	}
	policy, err := buildOrderEventReplayPolicy(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.metadataComplete || policy.dailyCapByRun["neutral-run"] != 1 || policy.sectorByRule["neutral-run\x00rule-a"] != "bank" {
		t.Fatalf("unexpected replay policy: %+v", policy)
	}
}

func TestReplayFrozenOrderEventsRejectsFeePolicyMismatchAfterValidSeal(t *testing.T) {
	at := time.Date(2026, 8, 4, 1, 45, 0, 0, time.UTC)
	event := frozenReplayEvent("fill-wrong-fee", "fill", 1, at, 10, 1000, 99)
	_, _, _, err := ReplayFrozenOrderEvents("bt-wrong-fee", "1.5.0", []models.OrderEvent{event}, at.Add(24*time.Hour))
	if !errors.Is(err, ErrInvalidOrderEventReplay) {
		t.Fatalf("fixed-fee mismatch error = %v", err)
	}
}

func TestReplayFrozenOrderEventsAllowsExplicitNoTradeEvent(t *testing.T) {
	at := time.Date(2026, 8, 4, 7, 0, 0, 0, time.UTC)
	frozenAt := time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)
	noTrade := frozenReplayEvent("no-trade-1", "no_trade", 1, at, 0, 0, 0)
	trades, stats, hash, err := ReplayFrozenOrderEvents("bt-no-trade", "1.5.0", []models.OrderEvent{noTrade}, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 || stats.TradeCount != 0 || hash == "" {
		t.Fatalf("unexpected no-trade replay: trades=%v stats=%+v hash=%q", trades, stats, hash)
	}
}
