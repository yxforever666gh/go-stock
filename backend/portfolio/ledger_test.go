package portfolio

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeLedger struct {
	events []LedgerEvent
	seal   LedgerSeal
	err    error
}

func (f fakeLedger) OrderEvents(context.Context, LedgerQuery) ([]LedgerEvent, error) {
	return f.events, nil
}

func (f fakeLedger) LedgerSeal(context.Context, LedgerQuery) (LedgerSeal, error) {
	return f.seal, nil
}

func (f fakeLedger) VerifyLedgerSeal(context.Context, LedgerQuery, LedgerSeal, []LedgerEvent) error {
	return f.err
}

func TestReaderRejectsProjectionLikeUnsealedRows(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	reader := NewReader(fakeLedger{
		events: []LedgerEvent{{EventID: "e1", FrozenAt: now}},
		seal:   LedgerSeal{EventCount: 1, LedgerHash: "hash", SealedThrough: now},
	})
	if _, _, err := reader.Events(context.Background(), LedgerQuery{}); !errors.Is(err, ErrUnsealedLedger) {
		t.Fatalf("Events error = %v, want ErrUnsealedLedger", err)
	}
}

func TestReaderReturnsOnlyVerifiedSealedLedger(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	events := []LedgerEvent{{EventID: "e1", SnapshotHash: "event-hash", FrozenAt: now}}
	reader := NewReader(fakeLedger{events: events, seal: LedgerSeal{EventCount: 1, LedgerHash: "ledger-hash", SealedThrough: now}})
	got, seal, err := reader.Events(context.Background(), LedgerQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || seal.LedgerHash != "ledger-hash" {
		t.Fatalf("unexpected ledger: %+v %+v", got, seal)
	}
}
