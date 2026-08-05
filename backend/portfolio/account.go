package portfolio

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidAccountInput = errors.New("invalid account input")
	ErrInvalidLedgerEvent  = errors.New("invalid ledger event")
	ErrInvalidValuation    = errors.New("invalid valuation")
)

const (
	eventFill            = "fill"
	eventCorporateAction = "corporate_action"
	eventExitFill        = "exit_fill"
)

// ValuationMark is a point-in-time price used only after the sealed ledger has
// established the open positions. A future or stale-by-causality mark is never
// silently substituted with an earlier projection value.
type ValuationMark struct {
	Symbol      string    `json:"symbol"`
	Price       float64   `json:"price"`
	ObservedAt  time.Time `json:"observedAt"`
	AvailableAt time.Time `json:"availableAt"`
	Source      string    `json:"source"`
}

type Position struct {
	RunID               string    `json:"runId"`
	RuleID              string    `json:"ruleId"`
	Symbol              string    `json:"symbol"`
	Quantity            float64   `json:"quantity"`
	EntryPrice          float64   `json:"entryPrice"`
	EntryFees           float64   `json:"entryFees"`
	EntryAt             time.Time `json:"entryAt"`
	CorporateActionCash float64   `json:"corporateActionCash"`
	LastEventAt         time.Time `json:"lastEventAt"`
	MarkPrice           float64   `json:"markPrice"`
	MarketValue         float64   `json:"marketValue"`
	UnrealizedPnL       float64   `json:"unrealizedPnl"`
}

type ClosedTrade struct {
	RunID               string    `json:"runId"`
	RuleID              string    `json:"ruleId"`
	Symbol              string    `json:"symbol"`
	EntryAt             time.Time `json:"entryAt"`
	ExitAt              time.Time `json:"exitAt"`
	Quantity            float64   `json:"quantity"`
	EntryPrice          float64   `json:"entryPrice"`
	ExitPrice           float64   `json:"exitPrice"`
	EntryFees           float64   `json:"entryFees"`
	ExitFees            float64   `json:"exitFees"`
	CorporateActionCash float64   `json:"corporateActionCash"`
	GrossPnL            float64   `json:"grossPnl"`
	NetPnL              float64   `json:"netPnl"`
	NetReturnPct        float64   `json:"netReturnPct"`
	ExitReason          string    `json:"exitReason,omitempty"`
}

type AccountSnapshot struct {
	StrategyVersion  string        `json:"strategyVersion"`
	AsOf             time.Time     `json:"asOf"`
	InitialCash      float64       `json:"initialCash"`
	Cash             float64       `json:"cash"`
	Fees             float64       `json:"fees"`
	CorporateCash    float64       `json:"corporateCash"`
	MarketValue      float64       `json:"marketValue"`
	NAV              float64       `json:"nav"`
	RealizedGrossPnL float64       `json:"realizedGrossPnl"`
	RealizedNetPnL   float64       `json:"realizedNetPnl"`
	UnrealizedPnL    float64       `json:"unrealizedPnl"`
	TotalPnL         float64       `json:"totalPnl"`
	Positions        []Position    `json:"positions"`
	ClosedTrades     []ClosedTrade `json:"closedTrades"`
	LedgerSeal       LedgerSeal    `json:"ledgerSeal"`
}

// Account derives portfolio state from a verified immutable ledger. Projection
// tables are intentionally absent from the method signature.
func (r Reader) Account(ctx context.Context, query LedgerQuery, initialCash float64, marks map[string]ValuationMark) (AccountSnapshot, error) {
	events, seal, err := r.Events(ctx, query)
	if err != nil {
		return AccountSnapshot{}, err
	}
	snapshot, err := deriveAccount(query, initialCash, events, marks)
	if err != nil {
		return AccountSnapshot{}, err
	}
	snapshot.LedgerSeal = seal
	return snapshot, nil
}

func deriveAccount(query LedgerQuery, initialCash float64, source []LedgerEvent, marks map[string]ValuationMark) (AccountSnapshot, error) {
	if query.AsOf.IsZero() || initialCash <= 0 || math.IsNaN(initialCash) || math.IsInf(initialCash, 0) {
		return AccountSnapshot{}, fmt.Errorf("%w: positive initial cash and asOf are required", ErrInvalidAccountInput)
	}
	if !query.Start.IsZero() || (!query.End.IsZero() && !query.End.Equal(query.AsOf)) {
		return AccountSnapshot{}, fmt.Errorf("%w: account derivation requires the complete ledger through asOf", ErrInvalidAccountInput)
	}
	result := AccountSnapshot{
		StrategyVersion: strings.TrimSpace(query.StrategyVersion),
		AsOf:            query.AsOf,
		InitialCash:     initialCash,
		Cash:            initialCash,
		Positions:       []Position{},
		ClosedTrades:    []ClosedTrade{},
	}
	events := append([]LedgerEvent(nil), source...)
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].EventAt.Equal(events[j].EventAt) {
			return events[i].EventAt.Before(events[j].EventAt)
		}
		if events[i].RunID != events[j].RunID {
			return events[i].RunID < events[j].RunID
		}
		if events[i].RuleID != events[j].RuleID {
			return events[i].RuleID < events[j].RuleID
		}
		if events[i].Sequence != events[j].Sequence {
			return events[i].Sequence < events[j].Sequence
		}
		return events[i].EventID < events[j].EventID
	})

	positions := make(map[string]Position)
	lastSequence := make(map[string]int)
	lastEventAt := make(map[string]time.Time)
	seenEventIDs := make(map[string]bool, len(events))
	for index, event := range events {
		if err := validateAccountEvent(query, index, event, seenEventIDs, lastSequence, lastEventAt); err != nil {
			return AccountSnapshot{}, err
		}
		seenEventIDs[event.EventID] = true
		if event.RuleID != "" {
			lastSequence[event.RuleID] = event.Sequence
			lastEventAt[event.RuleID] = event.EventAt
		}

		switch strings.ToLower(strings.TrimSpace(event.EventType)) {
		case eventFill:
			if _, exists := positions[event.RuleID]; exists {
				return AccountSnapshot{}, fmt.Errorf("%w: rule %s has more than one open fill", ErrInvalidLedgerEvent, event.RuleID)
			}
			if err := validateMoneyEvent(event); err != nil {
				return AccountSnapshot{}, err
			}
			position := Position{
				RunID: event.RunID, RuleID: event.RuleID, Symbol: event.Symbol,
				Quantity: event.Quantity, EntryPrice: event.Price, EntryFees: event.Fees,
				EntryAt: event.EventAt, LastEventAt: event.EventAt,
			}
			positions[event.RuleID] = position
			result.Cash -= event.Price*event.Quantity + event.Fees
			result.Fees += event.Fees

		case eventCorporateAction:
			position, exists := positions[event.RuleID]
			if !exists {
				return AccountSnapshot{}, fmt.Errorf("%w: corporate action for unopened rule %s", ErrInvalidLedgerEvent, event.RuleID)
			}
			if event.Quantity <= 0 || event.AdjustmentFactor <= 0 || event.CashAmount < 0 {
				return AccountSnapshot{}, fmt.Errorf("%w: invalid corporate action for rule %s", ErrInvalidLedgerEvent, event.RuleID)
			}
			position.Quantity = event.Quantity
			position.EntryPrice *= event.AdjustmentFactor
			position.CorporateActionCash += event.CashAmount
			position.LastEventAt = event.EventAt
			positions[event.RuleID] = position
			result.Cash += event.CashAmount
			result.CorporateCash += event.CashAmount

		case eventExitFill:
			position, exists := positions[event.RuleID]
			if !exists {
				return AccountSnapshot{}, fmt.Errorf("%w: exit for unopened rule %s", ErrInvalidLedgerEvent, event.RuleID)
			}
			if err := validateMoneyEvent(event); err != nil {
				return AccountSnapshot{}, err
			}
			if event.Quantity-position.Quantity > 1e-8 {
				return AccountSnapshot{}, fmt.Errorf("%w: exit quantity %.8f exceeds position %.8f for rule %s", ErrInvalidLedgerEvent, event.Quantity, position.Quantity, event.RuleID)
			}
			fraction := event.Quantity / position.Quantity
			entryFees := position.EntryFees * fraction
			corporateCash := position.CorporateActionCash * fraction
			entryNotional := position.EntryPrice * event.Quantity
			grossPnL := (event.Price-position.EntryPrice)*event.Quantity + corporateCash
			netPnL := grossPnL - entryFees - event.Fees
			returnBase := entryNotional + entryFees
			netReturn := 0.0
			if returnBase > 0 {
				netReturn = netPnL / returnBase * 100
			}
			result.ClosedTrades = append(result.ClosedTrades, ClosedTrade{
				RunID: event.RunID, RuleID: event.RuleID, Symbol: event.Symbol,
				EntryAt: position.EntryAt, ExitAt: event.EventAt, Quantity: event.Quantity,
				EntryPrice: position.EntryPrice, ExitPrice: event.Price,
				EntryFees: entryFees, ExitFees: event.Fees, CorporateActionCash: corporateCash,
				GrossPnL: grossPnL, NetPnL: netPnL, NetReturnPct: netReturn, ExitReason: event.Reason,
			})
			result.Cash += event.Price*event.Quantity - event.Fees
			result.Fees += event.Fees
			result.RealizedGrossPnL += grossPnL
			result.RealizedNetPnL += netPnL
			position.Quantity -= event.Quantity
			position.EntryFees -= entryFees
			position.CorporateActionCash -= corporateCash
			position.LastEventAt = event.EventAt
			if position.Quantity <= 1e-8 {
				delete(positions, event.RuleID)
			} else {
				positions[event.RuleID] = position
			}
		}
	}

	keys := make([]string, 0, len(positions))
	for ruleID := range positions {
		keys = append(keys, ruleID)
	}
	sort.Strings(keys)
	for _, ruleID := range keys {
		position := positions[ruleID]
		mark, ok := marks[normalizeSymbol(position.Symbol)]
		if !ok {
			return AccountSnapshot{}, fmt.Errorf("%w: missing mark for open symbol %s", ErrInvalidValuation, position.Symbol)
		}
		if err := validateMark(mark, position.Symbol, query.AsOf); err != nil {
			return AccountSnapshot{}, err
		}
		position.MarkPrice = mark.Price
		position.MarketValue = mark.Price * position.Quantity
		position.UnrealizedPnL = (mark.Price-position.EntryPrice)*position.Quantity - position.EntryFees
		result.MarketValue += position.MarketValue
		result.UnrealizedPnL += position.UnrealizedPnL
		result.Positions = append(result.Positions, position)
	}
	result.NAV = result.Cash + result.MarketValue
	result.TotalPnL = result.NAV - result.InitialCash
	return result, nil
}

func validateAccountEvent(query LedgerQuery, index int, event LedgerEvent, seen map[string]bool, sequences map[string]int, eventTimes map[string]time.Time) error {
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.RunID) == "" || event.EventAt.IsZero() || event.FrozenAt.IsZero() || strings.TrimSpace(event.SnapshotHash) == "" {
		return fmt.Errorf("%w: event %d has incomplete immutable identity", ErrInvalidLedgerEvent, index)
	}
	if seen[event.EventID] {
		return fmt.Errorf("%w: duplicate event id %s", ErrInvalidLedgerEvent, event.EventID)
	}
	if event.EventAt.After(query.AsOf) || event.FrozenAt.After(query.AsOf) || event.EventAt.After(event.FrozenAt) {
		return fmt.Errorf("%w: event %s violates eventAt <= frozenAt <= asOf", ErrInvalidLedgerEvent, event.EventID)
	}
	if query.StrategyVersion != "" && event.StrategyVersion != query.StrategyVersion {
		return fmt.Errorf("%w: event %s belongs to strategy %s", ErrInvalidLedgerEvent, event.EventID, event.StrategyVersion)
	}
	typeName := strings.ToLower(strings.TrimSpace(event.EventType))
	if typeName == eventFill || typeName == eventCorporateAction || typeName == eventExitFill {
		if strings.TrimSpace(event.RuleID) == "" || strings.TrimSpace(event.Symbol) == "" || event.Sequence <= 0 {
			return fmt.Errorf("%w: lifecycle event %s has incomplete rule identity", ErrInvalidLedgerEvent, event.EventID)
		}
	}
	if event.RuleID != "" {
		previous, exists := sequences[event.RuleID]
		if !exists && event.Sequence != 1 {
			return fmt.Errorf("%w: rule %s ledger starts at sequence %d", ErrInvalidLedgerEvent, event.RuleID, event.Sequence)
		}
		if exists && event.Sequence != previous+1 {
			return fmt.Errorf("%w: rule %s sequence %d does not follow %d", ErrInvalidLedgerEvent, event.RuleID, event.Sequence, previous)
		}
		if previousAt := eventTimes[event.RuleID]; !previousAt.IsZero() && event.EventAt.Before(previousAt) {
			return fmt.Errorf("%w: rule %s event time moved backwards", ErrInvalidLedgerEvent, event.RuleID)
		}
	}
	return nil
}

func validateMoneyEvent(event LedgerEvent) error {
	if event.Price <= 0 || event.Quantity <= 0 || event.Fees < 0 ||
		math.IsNaN(event.Price) || math.IsInf(event.Price, 0) ||
		math.IsNaN(event.Quantity) || math.IsInf(event.Quantity, 0) ||
		math.IsNaN(event.Fees) || math.IsInf(event.Fees, 0) {
		return fmt.Errorf("%w: invalid price, quantity or fees for event %s", ErrInvalidLedgerEvent, event.EventID)
	}
	return nil
}

func validateMark(mark ValuationMark, symbol string, asOf time.Time) error {
	if !strings.EqualFold(strings.TrimSpace(mark.Symbol), strings.TrimSpace(symbol)) || mark.Price <= 0 ||
		mark.ObservedAt.IsZero() || mark.AvailableAt.IsZero() || mark.ObservedAt.After(mark.AvailableAt) || mark.AvailableAt.After(asOf) ||
		math.IsNaN(mark.Price) || math.IsInf(mark.Price, 0) {
		return fmt.Errorf("%w: invalid point-in-time mark for %s", ErrInvalidValuation, symbol)
	}
	return nil
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}
