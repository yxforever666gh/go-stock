package portfolio

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBuildCurrentRecommendationWithoutDisplayKeepsFrozenRuleVisible(t *testing.T) {
	frozen := lifecycleTestRecommendation()
	events := lifecycleHoldingEvents(frozen)

	got, err := BuildCurrentRecommendation(frozen, nil, events)
	if err != nil {
		t.Fatal(err)
	}
	if got.Display != nil {
		t.Fatalf("display = %+v, want nil", got.Display)
	}
	if got.Frozen.RuleID != frozen.RuleID || got.Frozen.CandidateID != frozen.CandidateID {
		t.Fatalf("frozen identity changed: %+v", got.Frozen)
	}
	if got.Lifecycle.Status != RecommendationHolding || got.Lifecycle.EntryAt == nil || got.Lifecycle.EntryPrice != 10 ||
		got.Lifecycle.EntryQuantity != 1000 || got.Lifecycle.AdjustedQuantity != 1000 || got.Lifecycle.RemainingQuantity != 1000 ||
		got.Lifecycle.EntryFees != 5 || got.Lifecycle.TotalFees != 5 {
		t.Fatalf("lifecycle = %+v, want sealed holding", got.Lifecycle)
	}
}

func TestDeriveRecommendationLifecycleMapsPendingAndOrderedPrefixes(t *testing.T) {
	frozen := lifecycleTestRecommendation()
	events := lifecycleHoldingEvents(frozen)
	tests := []struct {
		name   string
		prefix int
		status RecommendationLifecycleStatus
	}{
		{name: "issued is pending", prefix: 1, status: RecommendationPending},
		{name: "signal remains pending", prefix: 2, status: RecommendationPending},
		{name: "order is ordered", prefix: 3, status: RecommendationOrdered},
		{name: "fill is holding", prefix: 4, status: RecommendationHolding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DeriveRecommendationLifecycle(frozen, events[:test.prefix])
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != test.status {
				t.Fatalf("status = %s, want %s", got.Status, test.status)
			}
		})
	}
}

func TestDisplayMetadataCannotExpressOrOverrideLifecycle(t *testing.T) {
	typeOfDisplay := reflect.TypeOf(DisplayMetadata{})
	for _, forbidden := range []string{
		"RunID", "RuleID", "CandidateID", "StrategyVersion", "Symbol",
		"Status", "ActivationStatus", "PositionStatus", "EntryPrice", "YieldRate",
	} {
		if _, exists := typeOfDisplay.FieldByName(forbidden); exists {
			t.Fatalf("DisplayMetadata unexpectedly exposes authoritative field %s", forbidden)
		}
	}

	frozen := lifecycleTestRecommendation()
	events := lifecycleHoldingEvents(frozen)
	firstReview := json.RawMessage(`{"status":"pending"}`)
	first, err := BuildCurrentRecommendation(frozen, &DisplayMetadata{
		RecommendID: 7, Provider: "projection-says-pending", Model: "model-a", OpeningReview: firstReview,
	}, events)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCurrentRecommendation(frozen, &DisplayMetadata{
		RecommendID: 99, Provider: "projection-says-activated", Model: "model-b",
	}, events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Lifecycle, second.Lifecycle) || first.Lifecycle.Status != RecommendationHolding {
		t.Fatalf("display changed ledger lifecycle: first=%+v second=%+v", first.Lifecycle, second.Lifecycle)
	}
	firstReview[2] = 'X'
	if string(first.Display.OpeningReview) != `{"status":"pending"}` {
		t.Fatalf("display JSON was not copied: %s", first.Display.OpeningReview)
	}
}

func TestDeriveRecommendationLifecycleTracksCorporateActionAndClosedFill(t *testing.T) {
	frozen := lifecycleTestRecommendation()
	events := lifecycleHoldingEvents(frozen)
	actionAt := events[len(events)-1].EventAt.Add(71*time.Hour + 44*time.Minute)
	exitSignalAt := actionAt.Add(4 * time.Hour)
	exitAt := exitSignalAt.Add(time.Minute)
	events = append(events,
		lifecycleTestEvent(frozen, "corporate_action", 5, actionAt, 0, 1200, 0, 80, 0.8, "split-and-dividend"),
		lifecycleTestEvent(frozen, "exit_signal", 6, exitSignalAt, 9.5, 1200, 0, 0, 0, "target"),
		lifecycleTestEvent(frozen, "exit_fill", 7, exitAt, 9.49, 1200, 8, 0, 0, "target"),
	)

	got, err := DeriveRecommendationLifecycle(frozen, events)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != RecommendationClosed || got.ExitAt == nil || !got.ExitAt.Equal(exitAt) || got.ExitPrice != 9.49 ||
		got.ExitQuantity != 1200 || got.ExitFees != 8 || got.TotalFees != 13 || got.CorporateActionCash != 80 ||
		got.CorporateActionCount != 1 || got.AdjustedEntryPrice != 8 || got.AdjustedQuantity != 1200 || got.RemainingQuantity != 0 {
		t.Fatalf("closed lifecycle = %+v", got)
	}
}

func TestDeriveRecommendationLifecycleDoesNotCreatePositionWithoutFill(t *testing.T) {
	frozen := lifecycleTestRecommendation()
	issuedAt := frozen.DecisionAt.Add(10 * time.Second)
	signalAt := frozen.ValidFromAt.Add(15 * time.Minute)
	orderAt := signalAt.Add(time.Minute)

	tests := []struct {
		name   string
		kind   string
		status RecommendationLifecycleStatus
	}{
		{name: "rejected", kind: "reject", status: RecommendationRejected},
		{name: "expired", kind: "activation_expired", status: RecommendationExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := []LedgerEvent{
				lifecycleTestEvent(frozen, "rule_issued", 1, issuedAt, 0, 0, 0, 0, 0, "published"),
				lifecycleTestEvent(frozen, "signal", 2, signalAt, 10, 0, 0, 0, 0, "pullback"),
				lifecycleTestEvent(frozen, "order", 3, orderAt, 0, 0, 0, 0, 0, "next_bar"),
				lifecycleTestEvent(frozen, test.kind, 4, orderAt.Add(time.Minute), 0, 0, 0, 0, 0, test.name),
			}
			got, err := DeriveRecommendationLifecycle(frozen, events)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != test.status || got.EntryAt != nil || got.EntryPrice != 0 || got.EntryQuantity != 0 ||
				got.AdjustedQuantity != 0 || got.RemainingQuantity != 0 || got.TotalFees != 0 {
				t.Fatalf("unfilled lifecycle = %+v", got)
			}
		})
	}
}

func TestDeriveRecommendationLifecycleRejectsBrokenSealIdentityAndSequence(t *testing.T) {
	frozen := lifecycleTestRecommendation()
	valid := lifecycleHoldingEvents(frozen)

	tests := []struct {
		name   string
		mutate func([]LedgerEvent) []LedgerEvent
	}{
		{name: "missing event seal", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[2].SnapshotHash = ""
			return events
		}},
		{name: "frozen before event", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[2].FrozenAt = events[2].EventAt.Add(-time.Second)
			return events
		}},
		{name: "sequence gap", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[2].Sequence = 4
			return events
		}},
		{name: "timeline regression", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[2].EventAt = events[1].EventAt.Add(-time.Second)
			return events
		}},
		{name: "wrong cohort", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[1].StrategyVersion = "1.4.2"
			return events
		}},
		{name: "wrong rule", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[1].RuleID = "forged-rule"
			return events
		}},
		{name: "duplicate event", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[2].EventID = events[1].EventID
			return events
		}},
		{name: "missing rule issued prefix", mutate: func(events []LedgerEvent) []LedgerEvent {
			events[0].EventType = "signal"
			return events
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := append([]LedgerEvent(nil), valid...)
			_, err := DeriveRecommendationLifecycle(frozen, test.mutate(events))
			if !errors.Is(err, ErrInvalidRecommendationLedger) {
				t.Fatalf("error = %v, want ErrInvalidRecommendationLedger", err)
			}
		})
	}
}

func TestDeriveRecommendationLifecycleRejectsUnsealedFrozenIdentity(t *testing.T) {
	frozen := lifecycleTestRecommendation()
	frozen.Identity.CandidateSnapshotHash = ""
	_, err := DeriveRecommendationLifecycle(frozen, lifecycleHoldingEvents(frozen))
	if !errors.Is(err, ErrInvalidFrozenRecommendation) {
		t.Fatalf("error = %v, want ErrInvalidFrozenRecommendation", err)
	}
}

func lifecycleTestRecommendation() FrozenRecommendation {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	decisionAt := time.Date(2026, 8, 7, 9, 29, 0, 0, zone)
	return FrozenRecommendation{
		RunID: "run-1", RuleID: "rule-1", CandidateID: "candidate-1", StrategyVersion: "1.5.0",
		Symbol: "000001.SZ", Name: "sample", Sector: "bank", DecisionAt: decisionAt,
		ValidFromAt: decisionAt.Add(time.Minute),
		Identity: FrozenRecommendationIdentity{
			RunSnapshotHash: "run-hash", RuleSnapshotHash: "rule-hash", CandidateSnapshotHash: "candidate-hash",
			RunFrozenAt: decisionAt.Add(20 * time.Second), RuleFrozenAt: decisionAt.Add(20 * time.Second),
			CandidateFrozenAt: decisionAt.Add(20 * time.Second),
		},
	}
}

func lifecycleHoldingEvents(frozen FrozenRecommendation) []LedgerEvent {
	issuedAt := frozen.DecisionAt.Add(10 * time.Second)
	signalAt := frozen.ValidFromAt.Add(15 * time.Minute)
	orderAt := signalAt.Add(time.Minute)
	return []LedgerEvent{
		lifecycleTestEvent(frozen, "rule_issued", 1, issuedAt, 0, 0, 0, 0, 0, "published"),
		lifecycleTestEvent(frozen, "signal", 2, signalAt, 10, 0, 0, 0, 0, "pullback"),
		lifecycleTestEvent(frozen, "order", 3, orderAt, 0, 0, 0, 0, 0, "next_bar"),
		lifecycleTestEvent(frozen, "fill", 4, orderAt, 10, 1000, 5, 0, 0, "filled"),
	}
}

func lifecycleTestEvent(
	frozen FrozenRecommendation,
	kind string,
	sequence int,
	at time.Time,
	price, quantity, fees, cashAmount, adjustmentFactor float64,
	reason string,
) LedgerEvent {
	return LedgerEvent{
		EventID: "event-" + kind, RunID: frozen.RunID, RuleID: frozen.RuleID,
		StrategyVersion: frozen.StrategyVersion, TradeDate: frozen.DecisionAt.Format(time.DateOnly), Symbol: frozen.Symbol,
		EventType: kind, Sequence: sequence, EventAt: at, Price: price, Quantity: quantity, Fees: fees,
		CashAmount: cashAmount, AdjustmentFactor: adjustmentFactor, Reason: reason,
		SnapshotHash: "hash-" + kind, FrozenAt: at.Add(time.Second),
	}
}
