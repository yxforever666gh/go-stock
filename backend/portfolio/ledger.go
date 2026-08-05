// Package portfolio derives views from the sealed append-only order ledger.
package portfolio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrUnsealedLedger = errors.New("order ledger is not sealed")

type LedgerEvent struct {
	EventID          string    `json:"eventId"`
	RunID            string    `json:"runId"`
	RuleID           string    `json:"ruleId,omitempty"`
	StrategyVersion  string    `json:"strategyVersion"`
	TradeDate        string    `json:"tradeDate"`
	Symbol           string    `json:"symbol"`
	EventType        string    `json:"eventType"`
	Sequence         int       `json:"sequence"`
	EventAt          time.Time `json:"eventAt"`
	Price            float64   `json:"price"`
	Quantity         float64   `json:"quantity"`
	CashAmount       float64   `json:"cashAmount"`
	AdjustmentFactor float64   `json:"adjustmentFactor"`
	Fees             float64   `json:"fees"`
	Reason           string    `json:"reason,omitempty"`
	SnapshotHash     string    `json:"snapshotHash"`
	FrozenAt         time.Time `json:"frozenAt"`
}

type LedgerQuery struct {
	StrategyVersion string
	RunIDs          []string
	Start           time.Time
	End             time.Time
	AsOf            time.Time
}

type LedgerSeal struct {
	StrategyVersion string    `json:"strategyVersion"`
	RunIDs          []string  `json:"runIds"`
	EventCount      int       `json:"eventCount"`
	LedgerHash      string    `json:"ledgerHash"`
	SealedThrough   time.Time `json:"sealedThrough"`
}

type OrderEventReader interface {
	OrderEvents(context.Context, LedgerQuery) ([]LedgerEvent, error)
}

type LedgerSealReader interface {
	LedgerSeal(context.Context, LedgerQuery) (LedgerSeal, error)
}

type LedgerSealVerifier interface {
	VerifyLedgerSeal(context.Context, LedgerQuery, LedgerSeal, []LedgerEvent) error
}

// ReadOnlyLedger is the only persistence dependency accepted by portfolio use
// cases. Projection tables intentionally cannot satisfy this interface.
type ReadOnlyLedger interface {
	OrderEventReader
	LedgerSealReader
	LedgerSealVerifier
}

type Reader struct {
	ledger ReadOnlyLedger
}

func NewReader(ledger ReadOnlyLedger) Reader {
	return Reader{ledger: ledger}
}

func (r Reader) Events(ctx context.Context, query LedgerQuery) ([]LedgerEvent, LedgerSeal, error) {
	if r.ledger == nil {
		return nil, LedgerSeal{}, fmt.Errorf("%w: reader is nil", ErrUnsealedLedger)
	}
	events, err := r.ledger.OrderEvents(ctx, query)
	if err != nil {
		return nil, LedgerSeal{}, err
	}
	seal, err := r.ledger.LedgerSeal(ctx, query)
	if err != nil {
		return nil, LedgerSeal{}, err
	}
	if strings.TrimSpace(seal.LedgerHash) == "" || seal.EventCount != len(events) || seal.SealedThrough.IsZero() {
		return nil, LedgerSeal{}, fmt.Errorf("%w: missing or inconsistent seal", ErrUnsealedLedger)
	}
	for i, event := range events {
		if event.FrozenAt.IsZero() || strings.TrimSpace(event.SnapshotHash) == "" {
			return nil, LedgerSeal{}, fmt.Errorf("%w: event %d is mutable", ErrUnsealedLedger, i)
		}
	}
	if err := r.ledger.VerifyLedgerSeal(ctx, query, seal, events); err != nil {
		return nil, LedgerSeal{}, fmt.Errorf("%w: %v", ErrUnsealedLedger, err)
	}
	return events, seal, nil
}
