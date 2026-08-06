package portfolio

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidFrozenRecommendation = errors.New("invalid frozen recommendation")
	ErrInvalidRecommendationLedger = errors.New("invalid recommendation ledger")
)

// RecommendationLifecycleStatus is derived only from the sealed order-event
// prefix for one frozen rule. Display projections deliberately cannot express
// any of these states.
type RecommendationLifecycleStatus string

const (
	RecommendationPending  RecommendationLifecycleStatus = "pending"
	RecommendationOrdered  RecommendationLifecycleStatus = "ordered"
	RecommendationHolding  RecommendationLifecycleStatus = "holding"
	RecommendationClosed   RecommendationLifecycleStatus = "closed"
	RecommendationRejected RecommendationLifecycleStatus = "rejected"
	RecommendationExpired  RecommendationLifecycleStatus = "expired"
)

// FrozenRecommendationIdentity keeps the three immutable snapshot identities
// needed to prove where a current recommendation came from. Lifecycle events
// may be frozen later as they are appended, so their FrozenAt values are
// validated independently.
type FrozenRecommendationIdentity struct {
	RunSnapshotHash       string    `json:"runSnapshotHash"`
	RuleSnapshotHash      string    `json:"ruleSnapshotHash"`
	CandidateSnapshotHash string    `json:"candidateSnapshotHash"`
	RunFrozenAt           time.Time `json:"runFrozenAt"`
	RuleFrozenAt          time.Time `json:"ruleFrozenAt"`
	CandidateFrozenAt     time.Time `json:"candidateFrozenAt"`
}

// FrozenRecommendation is the strategy-owned identity and descriptive data
// for one published rule. It contains no mutable display or yield fields.
type FrozenRecommendation struct {
	RunID           string                       `json:"runId"`
	RuleID          string                       `json:"ruleId"`
	CandidateID     string                       `json:"candidateId"`
	StrategyVersion string                       `json:"strategyVersion"`
	Symbol          string                       `json:"symbol"`
	Name            string                       `json:"name"`
	Sector          string                       `json:"sector"`
	DecisionAt      time.Time                    `json:"decisionAt"`
	ValidFromAt     time.Time                    `json:"validFromAt"`
	Identity        FrozenRecommendationIdentity `json:"identity"`
}

// DisplayMetadata is optional compatibility data. Its type intentionally has
// no identity, activation, position, price, quantity, fee or return fields, so
// a legacy projection cannot alter a frozen recommendation or its lifecycle.
type DisplayMetadata struct {
	RecommendID   uint            `json:"recommendId,omitempty"`
	Provider      string          `json:"provider,omitempty"`
	Model         string          `json:"model,omitempty"`
	OpeningReview json.RawMessage `json:"openingReview,omitempty"`
}

// RecommendationLifecycle is a read model of a single sealed rule ledger. It
// reports facts only; valuation and return calculations belong to the account
// read model and require separate point-in-time marks.
type RecommendationLifecycle struct {
	Status RecommendationLifecycleStatus `json:"status"`
	Reason string                        `json:"reason,omitempty"`

	RuleIssuedAt  time.Time  `json:"ruleIssuedAt"`
	SignalAt      *time.Time `json:"signalAt,omitempty"`
	SignalPrice   float64    `json:"signalPrice,omitempty"`
	OrderAt       *time.Time `json:"orderAt,omitempty"`
	EntryAt       *time.Time `json:"entryAt,omitempty"`
	EntryPrice    float64    `json:"entryPrice,omitempty"`
	EntryQuantity float64    `json:"entryQuantity,omitempty"`
	EntryFees     float64    `json:"entryFees,omitempty"`

	AdjustedEntryPrice   float64 `json:"adjustedEntryPrice,omitempty"`
	AdjustedQuantity     float64 `json:"adjustedQuantity,omitempty"`
	RemainingQuantity    float64 `json:"remainingQuantity,omitempty"`
	CorporateActionCash  float64 `json:"corporateActionCash,omitempty"`
	CorporateActionCount int     `json:"corporateActionCount,omitempty"`

	ExitSignalAt *time.Time `json:"exitSignalAt,omitempty"`
	ExitAt       *time.Time `json:"exitAt,omitempty"`
	ExitPrice    float64    `json:"exitPrice,omitempty"`
	ExitQuantity float64    `json:"exitQuantity,omitempty"`
	ExitFees     float64    `json:"exitFees,omitempty"`
	TotalFees    float64    `json:"totalFees,omitempty"`

	LastEventID string    `json:"lastEventId"`
	LastEventAt time.Time `json:"lastEventAt"`
}

// CurrentRecommendation keeps optional display metadata structurally separate
// from the frozen identity and sealed-ledger lifecycle.
type CurrentRecommendation struct {
	Frozen    FrozenRecommendation    `json:"frozen"`
	Display   *DisplayMetadata        `json:"display,omitempty"`
	Lifecycle RecommendationLifecycle `json:"lifecycle"`
}

// BuildCurrentRecommendation combines immutable identity and ledger facts.
// Display metadata is copied only after lifecycle derivation succeeds.
func BuildCurrentRecommendation(
	frozen FrozenRecommendation,
	display *DisplayMetadata,
	events []LedgerEvent,
) (CurrentRecommendation, error) {
	lifecycle, err := DeriveRecommendationLifecycle(frozen, events)
	if err != nil {
		return CurrentRecommendation{}, err
	}
	return CurrentRecommendation{
		Frozen:    frozen,
		Display:   cloneDisplayMetadata(display),
		Lifecycle: lifecycle,
	}, nil
}

// DeriveRecommendationLifecycle validates and folds one rule's complete sealed
// event prefix. The caller must not omit earlier events: sequence one must be a
// causal rule_issued fact and all later sequence numbers must be contiguous.
func DeriveRecommendationLifecycle(frozen FrozenRecommendation, events []LedgerEvent) (RecommendationLifecycle, error) {
	if err := validateFrozenRecommendation(frozen); err != nil {
		return RecommendationLifecycle{}, err
	}
	if len(events) == 0 {
		return RecommendationLifecycle{}, fmt.Errorf("%w: rule %s has no rule_issued event", ErrInvalidRecommendationLedger, frozen.RuleID)
	}

	result := RecommendationLifecycle{Status: RecommendationPending}
	seenEventIDs := make(map[string]struct{}, len(events))
	var previousAt, previousFrozenAt time.Time
	var signaled, ordered, filled, exitSignaled, exitOrdered, terminal bool
	var exitOrderAt *time.Time

	for index, event := range events {
		if err := validateRecommendationLedgerEvent(frozen, event, index, previousAt, previousFrozenAt, seenEventIDs); err != nil {
			return RecommendationLifecycle{}, err
		}
		seenEventIDs[event.EventID] = struct{}{}
		previousAt, previousFrozenAt = event.EventAt, event.FrozenAt
		kind := strings.ToLower(strings.TrimSpace(event.EventType))
		if terminal {
			return RecommendationLifecycle{}, fmt.Errorf("%w: event %s follows terminal lifecycle", ErrInvalidRecommendationLedger, event.EventID)
		}

		switch kind {
		case "rule_issued":
			if index != 0 || !result.RuleIssuedAt.IsZero() || event.EventAt.Before(frozen.DecisionAt) || event.EventAt.After(frozen.ValidFromAt) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: rule_issued must be the causal prefix between decision and validFrom", ErrInvalidRecommendationLedger)
			}
			result.RuleIssuedAt = event.EventAt

		case "signal":
			if result.RuleIssuedAt.IsZero() || signaled || ordered || filled || event.EventAt.Before(frozen.ValidFromAt) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: signal %s is orphaned, duplicated or too early", ErrInvalidRecommendationLedger, event.EventID)
			}
			if !finiteNonNegativeLifecycle(event.Price) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: signal %s has an invalid price", ErrInvalidRecommendationLedger, event.EventID)
			}
			signaled = true
			result.SignalAt = timePointer(event.EventAt)
			result.SignalPrice = event.Price

		case "order":
			if !signaled || ordered || filled || result.SignalAt == nil || !event.EventAt.After(*result.SignalAt) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: order %s must follow exactly one signal", ErrInvalidRecommendationLedger, event.EventID)
			}
			if event.Quantity != 0 && !positiveIntegerLifecycle(event.Quantity) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: order %s has an invalid quantity", ErrInvalidRecommendationLedger, event.EventID)
			}
			ordered = true
			result.OrderAt = timePointer(event.EventAt)
			result.Status = RecommendationOrdered

		case "fill":
			if !ordered || filled || result.OrderAt == nil || event.EventAt.Before(*result.OrderAt) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: fill %s must follow exactly one order", ErrInvalidRecommendationLedger, event.EventID)
			}
			if !finitePositiveLifecycle(event.Price) || !positiveIntegerLifecycle(event.Quantity) || !finiteNonNegativeLifecycle(event.Fees) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: fill %s has invalid price, quantity or fees", ErrInvalidRecommendationLedger, event.EventID)
			}
			filled = true
			result.EntryAt = timePointer(event.EventAt)
			result.EntryPrice = event.Price
			result.EntryQuantity = event.Quantity
			result.EntryFees = event.Fees
			result.TotalFees = event.Fees
			result.AdjustedEntryPrice = event.Price
			result.AdjustedQuantity = event.Quantity
			result.RemainingQuantity = event.Quantity
			result.Status = RecommendationHolding

		case "corporate_action":
			if !filled || exitSignaled || result.EntryAt == nil || !event.EventAt.After(*result.EntryAt) ||
				!positiveIntegerLifecycle(event.Quantity) || !finitePositiveLifecycle(event.AdjustmentFactor) || !finiteNonNegativeLifecycle(event.CashAmount) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: corporate action %s does not validly adjust an open position", ErrInvalidRecommendationLedger, event.EventID)
			}
			result.AdjustedEntryPrice *= event.AdjustmentFactor
			result.AdjustedQuantity = event.Quantity
			result.RemainingQuantity = event.Quantity
			result.CorporateActionCash += event.CashAmount
			result.CorporateActionCount++

		case "exit_signal":
			if !filled || exitSignaled || result.EntryAt == nil || !event.EventAt.After(*result.EntryAt) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: exit signal %s does not follow an open fill", ErrInvalidRecommendationLedger, event.EventID)
			}
			exitSignaled = true
			result.ExitSignalAt = timePointer(event.EventAt)

		case "exit_order":
			if !exitSignaled || exitOrdered || result.ExitSignalAt == nil || event.EventAt.Before(*result.ExitSignalAt) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: exit order %s must follow exactly one exit signal", ErrInvalidRecommendationLedger, event.EventID)
			}
			exitOrdered = true
			exitOrderAt = timePointer(event.EventAt)

		case "exit_fill":
			anchor := result.ExitSignalAt
			if exitOrderAt != nil {
				anchor = exitOrderAt
			}
			if !filled || !exitSignaled || result.ExitAt != nil || anchor == nil {
				return RecommendationLifecycle{}, fmt.Errorf("%w: exit fill %s has no open signaled position", ErrInvalidRecommendationLedger, event.EventID)
			}
			if event.EventAt.Before(*anchor) || !finitePositiveLifecycle(event.Price) || !positiveIntegerLifecycle(event.Quantity) ||
				!nearlyEqualLifecycle(event.Quantity, result.AdjustedQuantity) || !finiteNonNegativeLifecycle(event.Fees) {
				return RecommendationLifecycle{}, fmt.Errorf("%w: exit fill %s has invalid time, price, quantity or fees", ErrInvalidRecommendationLedger, event.EventID)
			}
			result.ExitAt = timePointer(event.EventAt)
			result.ExitPrice = event.Price
			result.ExitQuantity = event.Quantity
			result.ExitFees = event.Fees
			result.TotalFees += event.Fees
			result.RemainingQuantity = 0
			result.Reason = strings.TrimSpace(event.Reason)
			result.Status = RecommendationClosed
			terminal = true

		case "reject", "activation_expired", "expired":
			if result.RuleIssuedAt.IsZero() || filled {
				return RecommendationLifecycle{}, fmt.Errorf("%w: terminal event %s must reject an unfilled issued rule", ErrInvalidRecommendationLedger, event.EventID)
			}
			result.Reason = strings.TrimSpace(event.Reason)
			result.Status = RecommendationRejected
			if kind == "activation_expired" || kind == "expired" {
				result.Status = RecommendationExpired
			}
			terminal = true

		default:
			return RecommendationLifecycle{}, fmt.Errorf("%w: unsupported event type %q", ErrInvalidRecommendationLedger, kind)
		}

		result.LastEventID = event.EventID
		result.LastEventAt = event.EventAt
	}

	if result.RuleIssuedAt.IsZero() {
		return RecommendationLifecycle{}, fmt.Errorf("%w: rule %s has no rule_issued prefix", ErrInvalidRecommendationLedger, frozen.RuleID)
	}
	return result, nil
}

func validateFrozenRecommendation(frozen FrozenRecommendation) error {
	if strings.TrimSpace(frozen.RunID) == "" || strings.TrimSpace(frozen.RuleID) == "" || strings.TrimSpace(frozen.CandidateID) == "" ||
		strings.TrimSpace(frozen.StrategyVersion) == "" || strings.TrimSpace(frozen.Symbol) == "" || frozen.DecisionAt.IsZero() ||
		frozen.ValidFromAt.IsZero() || !frozen.ValidFromAt.After(frozen.DecisionAt) {
		return fmt.Errorf("%w: run, rule, candidate, version, symbol and causal decision times are required", ErrInvalidFrozenRecommendation)
	}
	identity := frozen.Identity
	if strings.TrimSpace(identity.RunSnapshotHash) == "" || strings.TrimSpace(identity.RuleSnapshotHash) == "" ||
		strings.TrimSpace(identity.CandidateSnapshotHash) == "" || identity.RunFrozenAt.IsZero() || identity.RuleFrozenAt.IsZero() ||
		identity.CandidateFrozenAt.IsZero() || identity.RunFrozenAt.Before(frozen.DecisionAt) ||
		identity.RuleFrozenAt.Before(frozen.DecisionAt) || identity.CandidateFrozenAt.Before(frozen.DecisionAt) {
		return fmt.Errorf("%w: complete snapshot seals frozen no earlier than decision are required", ErrInvalidFrozenRecommendation)
	}
	return nil
}

func validateRecommendationLedgerEvent(
	frozen FrozenRecommendation,
	event LedgerEvent,
	index int,
	previousAt, previousFrozenAt time.Time,
	seen map[string]struct{},
) error {
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.RunID) == "" || strings.TrimSpace(event.RuleID) == "" ||
		strings.TrimSpace(event.StrategyVersion) == "" || strings.TrimSpace(event.Symbol) == "" || strings.TrimSpace(event.EventType) == "" ||
		strings.TrimSpace(event.SnapshotHash) == "" || event.EventAt.IsZero() || event.FrozenAt.IsZero() || event.EventAt.After(event.FrozenAt) {
		return fmt.Errorf("%w: event %d has an incomplete or unsealed identity", ErrInvalidRecommendationLedger, index)
	}
	if event.RunID != frozen.RunID || event.RuleID != frozen.RuleID || event.StrategyVersion != frozen.StrategyVersion ||
		!strings.EqualFold(strings.TrimSpace(event.Symbol), strings.TrimSpace(frozen.Symbol)) {
		return fmt.Errorf("%w: event %s does not belong to the frozen cohort/run/rule/symbol", ErrInvalidRecommendationLedger, event.EventID)
	}
	if event.Sequence != index+1 {
		return fmt.Errorf("%w: event %s sequence %d does not form the complete prefix at %d", ErrInvalidRecommendationLedger, event.EventID, event.Sequence, index+1)
	}
	if _, duplicate := seen[event.EventID]; duplicate {
		return fmt.Errorf("%w: duplicate event id %s", ErrInvalidRecommendationLedger, event.EventID)
	}
	if !previousAt.IsZero() && event.EventAt.Before(previousAt) {
		return fmt.Errorf("%w: event %s regresses event time", ErrInvalidRecommendationLedger, event.EventID)
	}
	if !previousFrozenAt.IsZero() && event.FrozenAt.Before(previousFrozenAt) {
		return fmt.Errorf("%w: event %s regresses frozen time", ErrInvalidRecommendationLedger, event.EventID)
	}
	if index > 0 && event.EventAt.Before(frozen.ValidFromAt) {
		return fmt.Errorf("%w: event %s precedes validFrom", ErrInvalidRecommendationLedger, event.EventID)
	}
	return nil
}

func cloneDisplayMetadata(source *DisplayMetadata) *DisplayMetadata {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.OpeningReview = append(json.RawMessage(nil), source.OpeningReview...)
	return &cloned
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func finitePositiveLifecycle(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteNonNegativeLifecycle(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func positiveIntegerLifecycle(value float64) bool {
	return finitePositiveLifecycle(value) && math.Trunc(value) == value
}

func nearlyEqualLifecycle(left, right float64) bool {
	return math.Abs(left-right) <= 1e-8
}
