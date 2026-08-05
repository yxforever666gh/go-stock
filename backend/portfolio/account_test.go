package portfolio

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestAccountDerivesOpenPositionAndNAVFromSealedEvents(t *testing.T) {
	at := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	events := []LedgerEvent{ledgerTestEvent("fill", 1, at, 10, 1000, 15)}
	reader := NewReader(sealedFakeLedger(events, at))

	got, err := reader.Account(context.Background(), LedgerQuery{StrategyVersion: "1.5.0", AsOf: at.Add(time.Hour)}, 100000, map[string]ValuationMark{
		"000001.SZ": {Symbol: "000001.SZ", Price: 11, ObservedAt: at.Add(30 * time.Minute), AvailableAt: at.Add(31 * time.Minute), Source: "cache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAccountNear(t, got.Cash, 89985)
	assertAccountNear(t, got.MarketValue, 11000)
	assertAccountNear(t, got.NAV, 100985)
	assertAccountNear(t, got.TotalPnL, 985)
	assertAccountNear(t, got.UnrealizedPnL, 985)
	if len(got.Positions) != 1 || len(got.ClosedTrades) != 0 || got.LedgerSeal.LedgerHash != "sealed" {
		t.Fatalf("unexpected account snapshot: %+v", got)
	}
}

func TestAccountHandlesCorporateActionAndClosedTrade(t *testing.T) {
	at := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	fill := ledgerTestEvent("fill", 1, at, 10, 1000, 15)
	action := ledgerTestEvent("corporate_action", 2, at.Add(24*time.Hour), 0, 1100, 0)
	action.CashAmount = 100
	action.AdjustmentFactor = 10.0 / 11.0
	exit := ledgerTestEvent("exit_fill", 3, at.Add(48*time.Hour), 11, 1100, 20)
	exit.Reason = "target"
	reader := NewReader(sealedFakeLedger([]LedgerEvent{exit, fill, action}, at.Add(48*time.Hour)))

	got, err := reader.Account(context.Background(), LedgerQuery{StrategyVersion: "1.5.0", AsOf: at.Add(49 * time.Hour)}, 100000, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertAccountNear(t, got.Cash, 102165)
	assertAccountNear(t, got.NAV, 102165)
	assertAccountNear(t, got.TotalPnL, 2165)
	assertAccountNear(t, got.RealizedGrossPnL, 2200)
	assertAccountNear(t, got.RealizedNetPnL, 2165)
	if len(got.Positions) != 0 || len(got.ClosedTrades) != 1 {
		t.Fatalf("unexpected account snapshot: %+v", got)
	}
	trade := got.ClosedTrades[0]
	assertAccountNear(t, trade.NetPnL, 2165)
	assertAccountNear(t, trade.CorporateActionCash, 100)
}

func TestAccountRejectsFutureMarkAndBrokenEventSequence(t *testing.T) {
	at := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	fill := ledgerTestEvent("fill", 1, at, 10, 1000, 15)
	reader := NewReader(sealedFakeLedger([]LedgerEvent{fill}, at))
	_, err := reader.Account(context.Background(), LedgerQuery{StrategyVersion: "1.5.0", AsOf: at.Add(time.Hour)}, 100000, map[string]ValuationMark{
		"000001.SZ": {Symbol: "000001.SZ", Price: 11, ObservedAt: at, AvailableAt: at.Add(2 * time.Hour)},
	})
	if !errors.Is(err, ErrInvalidValuation) {
		t.Fatalf("future mark error = %v, want ErrInvalidValuation", err)
	}

	first := ledgerTestEvent("signal", 1, at.Add(-time.Minute), 0, 0, 0)
	third := ledgerTestEvent("fill", 3, at, 10, 1000, 15)
	reader = NewReader(sealedFakeLedger([]LedgerEvent{first, third}, at))
	_, err = reader.Account(context.Background(), LedgerQuery{StrategyVersion: "1.5.0", AsOf: at.Add(time.Hour)}, 100000, nil)
	if !errors.Is(err, ErrInvalidLedgerEvent) {
		t.Fatalf("sequence error = %v, want ErrInvalidLedgerEvent", err)
	}
}

func TestAccountRejectsPartialLedgerWindow(t *testing.T) {
	at := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	reader := NewReader(sealedFakeLedger(nil, at))
	_, err := reader.Account(context.Background(), LedgerQuery{
		StrategyVersion: "1.5.0", Start: at.Add(-time.Hour), AsOf: at,
	}, 100000, nil)
	if !errors.Is(err, ErrInvalidAccountInput) {
		t.Fatalf("partial window error = %v, want ErrInvalidAccountInput", err)
	}
}

func ledgerTestEvent(kind string, sequence int, at time.Time, price, quantity, fees float64) LedgerEvent {
	return LedgerEvent{
		EventID: "event-" + kind, RunID: "run-1", RuleID: "rule-1", StrategyVersion: "1.5.0",
		TradeDate: at.Format(time.DateOnly), Symbol: "000001.SZ", EventType: kind, Sequence: sequence,
		EventAt: at, Price: price, Quantity: quantity, Fees: fees, SnapshotHash: "hash-" + kind, FrozenAt: at,
	}
}

func sealedFakeLedger(events []LedgerEvent, sealedThrough time.Time) fakeLedger {
	return fakeLedger{events: events, seal: LedgerSeal{
		StrategyVersion: "1.5.0", RunIDs: []string{"run-1"}, EventCount: len(events), LedgerHash: "sealed", SealedThrough: sealedThrough,
	}}
}

func assertAccountNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-8 {
		t.Fatalf("got %.10f, want %.10f", got, want)
	}
}
