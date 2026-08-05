package v150

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrCorporateActionInvalid          = errors.New("invalid corporate action")
	ErrCorporateActionRightsUnresolved = errors.New("corporate action rights subscription is unresolved")
)

// CorporateAction is an already-observed, point-in-time corporate action.
// AdjustmentFactor converts every pre-ex-date price/rule into the ex-date
// price basis (previous adj_factor/current adj_factor). CashDividend is the
// provider's after-tax per-old-share amount. SplitRatio and BonusRatio are
// shares received per old share and therefore change quantity without cash.
//
// A rights issue is deliberately represented but never auto-subscribed. The
// execution caller must have an explicit subscription policy before it can be
// applied; V1.5.0 has no such policy and therefore fails closed.
type CorporateAction struct {
	EventID          string
	Symbol           string
	ExDate           time.Time
	AvailableAt      time.Time
	AdjustmentFactor float64
	CashDividend     float64
	SplitRatio       float64
	BonusRatio       float64
	RightsRatio      float64
	RightsPrice      float64
}

type CorporateActionApplication struct {
	Position Position
	CashFlow float64
	Events   []OrderEvent
}

// ApplyCorporateActionsToPlan converts every absolute price in a still-pending
// frozen plan to the ex-date basis. Ratios, trading-day anchors and volume
// thresholds are dimensionless and therefore remain unchanged.
func ApplyCorporateActionsToPlan(plan TradePlan, actions []CorporateAction, at time.Time) (TradePlan, error) {
	adjusted := plan
	for _, action := range actions {
		if err := validateCorporateAction(plan.Symbol, action, at); err != nil {
			return plan, err
		}
		factor := action.AdjustmentFactor
		adjusted.Support *= factor
		adjusted.EntryMin *= factor
		adjusted.EntryMax *= factor
		adjusted.Trigger *= factor
		adjusted.ReferenceEntry *= factor
		adjusted.TargetResistance *= factor
		adjusted.Stop *= factor
		adjusted.Target *= factor
		adjusted.RiskPerShare *= factor
		adjusted.ATR14 *= factor
	}
	return adjusted, nil
}

// ApplyCorporateActions applies all actions before the ex-date's first bar is
// evaluated. Callers are responsible for selecting exactly one causally
// available observation and invoking this at most once for that trading day.
func ApplyCorporateActions(position Position, actions []CorporateAction, at time.Time) (CorporateActionApplication, error) {
	result := CorporateActionApplication{Position: position}
	if len(actions) == 0 {
		return result, nil
	}
	if position.Quantity <= 0 || strings.TrimSpace(position.Symbol) == "" || at.IsZero() {
		return result, fmt.Errorf("%w: position identity, quantity and application time are required", ErrCorporateActionInvalid)
	}
	ordered := append([]CorporateAction(nil), actions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].ExDate.Equal(ordered[j].ExDate) {
			return ordered[i].ExDate.Before(ordered[j].ExDate)
		}
		return ordered[i].EventID < ordered[j].EventID
	})

	for _, action := range ordered {
		if err := validateCorporateAction(position.Symbol, action, at); err != nil {
			return CorporateActionApplication{Position: position}, err
		}
		oldQuantity := result.Position.Quantity
		shareMultiplier := 1 + action.SplitRatio + action.BonusRatio
		newQuantityFloat := float64(oldQuantity) * shareMultiplier
		newQuantity := int(math.Round(newQuantityFloat))
		if newQuantity <= 0 || math.Abs(float64(newQuantity)-newQuantityFloat) > 1e-7 {
			return CorporateActionApplication{Position: position}, fmt.Errorf("%w: event %s produces fractional/invalid share quantity %.8f", ErrCorporateActionInvalid, action.EventID, newQuantityFloat)
		}

		cash := float64(oldQuantity) * action.CashDividend
		result.Position.Quantity = newQuantity
		result.Position.EntryPrice *= action.AdjustmentFactor
		result.Position.InitialStop *= action.AdjustmentFactor
		result.Position.Target *= action.AdjustmentFactor
		result.Position.RiskPerShare *= action.AdjustmentFactor
		result.Position.ATR14 *= action.AdjustmentFactor
		result.Position.HighestClose *= action.AdjustmentFactor
		result.Position.TrailingStop *= action.AdjustmentFactor
		result.Position.CorporateActionCash += cash
		result.CashFlow += cash
		result.Events = append(result.Events, OrderEvent{
			Type:             EventCorporateAction,
			At:               at,
			Symbol:           position.Symbol,
			Quantity:         newQuantity,
			CashAmount:       cash,
			AdjustmentFactor: action.AdjustmentFactor,
			Reason:           action.EventID,
		})
	}
	return result, nil
}

func validateCorporateAction(symbol string, action CorporateAction, at time.Time) error {
	if strings.TrimSpace(action.EventID) == "" || strings.TrimSpace(action.Symbol) == "" ||
		!strings.EqualFold(strings.TrimSpace(symbol), strings.TrimSpace(action.Symbol)) || action.ExDate.IsZero() {
		return fmt.Errorf("%w: event identity is incomplete", ErrCorporateActionInvalid)
	}
	if action.AvailableAt.IsZero() || action.AvailableAt.After(at) {
		return fmt.Errorf("%w: event %s was not causally available", ErrCorporateActionInvalid, action.EventID)
	}
	if action.AdjustmentFactor <= 0 || math.IsNaN(action.AdjustmentFactor) || math.IsInf(action.AdjustmentFactor, 0) {
		return fmt.Errorf("%w: event %s has no valid price factor", ErrCorporateActionInvalid, action.EventID)
	}
	if action.CashDividend < 0 || action.SplitRatio < 0 || action.BonusRatio < 0 || action.RightsRatio < 0 || action.RightsPrice < 0 {
		return fmt.Errorf("%w: event %s contains a negative entitlement", ErrCorporateActionInvalid, action.EventID)
	}
	if action.RightsRatio > 0 {
		return fmt.Errorf("%w: event %s ratio=%.8f price=%.8f", ErrCorporateActionRightsUnresolved, action.EventID, action.RightsRatio, action.RightsPrice)
	}
	return nil
}
