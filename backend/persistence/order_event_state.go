package persistence

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
)

type ruleLifecycleState struct {
	rule         models.RuleSnapshot
	issuedAt     time.Time
	signalAt     time.Time
	orderAt      time.Time
	fillAt       time.Time
	exitSignalAt time.Time
	exitOrderAt  time.Time
	exitFillAt   time.Time
	fillQuantity float64
	terminal     bool
}

// StrategyRunModeExecutionSecurityObservation identifies an auxiliary,
// append-only point-in-time security observation. It is not a strategy
// decision run and therefore must not manufacture a no_trade order event.
const StrategyRunModeExecutionSecurityObservation = "execution_security_observation"

// StrategyRunModeExecutionCorporateActionObservation identifies a dedicated
// point-in-time coverage snapshot for one symbol/trading day. It contains only
// corporate_action_event rows (including an explicit coverage row) and never
// manufactures a strategy decision or no_trade event.
const StrategyRunModeExecutionCorporateActionObservation = "execution_corporate_action_observation"

func isExecutionSecurityObservationRun(run models.StrategyRunSnapshot) bool {
	return strings.EqualFold(strings.TrimSpace(run.Mode), StrategyRunModeExecutionSecurityObservation)
}

func isExecutionCorporateActionObservationRun(run models.StrategyRunSnapshot) bool {
	return strings.EqualFold(strings.TrimSpace(run.Mode), StrategyRunModeExecutionCorporateActionObservation)
}

func validateOrderEventStateMachine(run models.StrategyRunSnapshot, rules []models.RuleSnapshot, events []models.OrderEvent) error {
	if len(rules) == 0 && len(events) == 0 {
		return fmt.Errorf("%w: a run without executable rules requires one explicit no_trade event", ErrInvalidImmutableRecord)
	}
	ruleByID := make(map[string]models.RuleSnapshot, len(rules))
	stateByRule := make(map[string]*ruleLifecycleState, len(rules))
	for i := range rules {
		rule := rules[i]
		if rule.RunID != run.RunID || strings.TrimSpace(rule.RuleID) == "" {
			return fmt.Errorf("%w: rule %s does not belong to run %s", ErrInvalidImmutableRecord, rule.RuleID, run.RunID)
		}
		if _, duplicate := ruleByID[rule.RuleID]; duplicate {
			return fmt.Errorf("%w: duplicate rule identity %s", ErrInvalidImmutableRecord, rule.RuleID)
		}
		ruleByID[rule.RuleID] = rule
		stateByRule[rule.RuleID] = &ruleLifecycleState{rule: rule}
	}

	eventsByRule := make(map[string][]models.OrderEvent, len(rules))
	seenEventIDs := make(map[string]struct{}, len(events))
	noTradeEvents := make([]models.OrderEvent, 0, 1)
	for i := range events {
		event := events[i]
		if _, duplicate := seenEventIDs[event.EventID]; duplicate {
			return fmt.Errorf("%w: duplicate order event id %s", ErrInvalidImmutableRecord, event.EventID)
		}
		seenEventIDs[event.EventID] = struct{}{}
		if event.RunID != run.RunID || event.StrategyVersion != run.StrategyVersion || event.TradeDate != run.TradeDate {
			return fmt.Errorf("%w: event %s does not belong to run %s", ErrInvalidImmutableRecord, event.EventID, run.RunID)
		}
		if event.Sequence <= 0 || event.EventAt.IsZero() {
			return fmt.Errorf("%w: event %s has invalid sequence/time", ErrInvalidImmutableRecord, event.EventID)
		}
		if event.FrozenAt == nil || event.FrozenAt.Before(event.EventAt) {
			return fmt.Errorf("%w: event %s frozenAt precedes eventAt", ErrInvalidImmutableRecord, event.EventID)
		}

		eventType := normalizedOrderEventType(event.EventType)
		if eventType == "no_trade" {
			noTradeEvents = append(noTradeEvents, event)
			continue
		}
		rule, exists := ruleByID[event.RuleID]
		if !exists || event.RuleID == "" || rule.RunID != run.RunID || event.Symbol != rule.Symbol {
			return fmt.Errorf("%w: event %s does not belong to its run/rule/symbol", ErrInvalidImmutableRecord, event.EventID)
		}
		eventsByRule[event.RuleID] = append(eventsByRule[event.RuleID], event)
	}
	if len(noTradeEvents) != 0 {
		if len(noTradeEvents) != 1 || len(events) != 1 || len(rules) != 0 || strings.TrimSpace(noTradeEvents[0].RuleID) != "" {
			return fmt.Errorf("%w: no_trade must be the sole event of a run without rules", ErrInvalidImmutableRecord)
		}
		if strings.TrimSpace(noTradeEvents[0].Reason) == "" {
			return fmt.Errorf("%w: no_trade must persist a structured reason", ErrInvalidImmutableRecord)
		}
		return nil
	}

	ruleIDs := make([]string, 0, len(stateByRule))
	for ruleID := range stateByRule {
		ruleIDs = append(ruleIDs, ruleID)
	}
	sort.Strings(ruleIDs)
	for _, ruleID := range ruleIDs {
		ordered := append([]models.OrderEvent(nil), eventsByRule[ruleID]...)
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].Sequence != ordered[j].Sequence {
				return ordered[i].Sequence < ordered[j].Sequence
			}
			return ordered[i].EventID < ordered[j].EventID
		})
		lastSequence := 0
		var lastAt time.Time
		state := stateByRule[ruleID]
		for i := range ordered {
			event := ordered[i]
			if event.Sequence <= lastSequence || (!lastAt.IsZero() && event.EventAt.Before(lastAt)) {
				return fmt.Errorf("%w: event %s violates rule %s sequence/time ordering", ErrInvalidImmutableRecord, event.EventID, ruleID)
			}
			lastSequence, lastAt = event.Sequence, event.EventAt
			if err := applyOrderEventTransition(run, state, event); err != nil {
				return fmt.Errorf("%w: event %s: %v", ErrInvalidImmutableRecord, event.EventID, err)
			}
		}
		if state.issuedAt.IsZero() {
			return fmt.Errorf("%w: rule %s has no rule_issued event", ErrInvalidImmutableRecord, state.rule.RuleID)
		}
	}
	return nil
}

func orderEventTypeRank(raw string) int {
	switch normalizedOrderEventType(raw) {
	case "rule_issued":
		return 0
	case "signal":
		return 1
	case "order":
		return 2
	case "fill":
		return 3
	case "corporate_action":
		return 4
	case "exit_signal":
		return 5
	case "exit_order":
		return 6
	case "exit_fill":
		return 7
	case "reject", "activation_expired", "expired":
		return 8
	case "no_trade":
		return 9
	default:
		return 9
	}
}

func orderEventFactLess(left, right models.OrderEvent) bool {
	if !left.EventAt.Equal(right.EventAt) {
		return left.EventAt.Before(right.EventAt)
	}
	leftType, rightType := normalizedOrderEventType(left.EventType), normalizedOrderEventType(right.EventType)
	if leftRank, rightRank := orderEventTypeRank(leftType), orderEventTypeRank(rightType); leftRank != rightRank {
		return leftRank < rightRank
	}
	if leftType != rightType {
		return leftType < rightType
	}
	if left.EventID != right.EventID {
		return left.EventID < right.EventID
	}
	if left.RunID != right.RunID {
		return left.RunID < right.RunID
	}
	if left.RuleID != right.RuleID {
		return left.RuleID < right.RuleID
	}
	return left.Sequence < right.Sequence
}

func applyOrderEventTransition(run models.StrategyRunSnapshot, state *ruleLifecycleState, event models.OrderEvent) error {
	kind := normalizedOrderEventType(event.EventType)
	if state.terminal && kind != "exit_fill" {
		return errorsText("rule lifecycle is already terminal")
	}
	switch kind {
	case "rule_issued":
		if !state.issuedAt.IsZero() {
			return errorsText("duplicate rule_issued")
		}
		// A rule is frozen when the decision is published, then becomes
		// executable at ValidFromAt. Requiring issuance at/after ValidFromAt
		// would force FrozenAt into the future and falsify the audit timeline.
		if event.EventAt.Before(run.DecisionAt) || event.EventAt.After(state.rule.ValidFromAt) {
			return errorsText("rule_issued must be between decision and validFrom")
		}
		state.issuedAt = event.EventAt
	case "signal":
		if state.issuedAt.IsZero() || !state.signalAt.IsZero() || state.terminal {
			return errorsText("signal is orphaned or duplicated")
		}
		if event.EventAt.Before(state.issuedAt) {
			return errorsText("signal precedes rule_issued")
		}
		if !isCNTradingSession(event.EventAt) {
			return errorsText("signal is outside A-share trading hours")
		}
		state.signalAt = event.EventAt
	case "order":
		if state.signalAt.IsZero() || !state.orderAt.IsZero() || !event.EventAt.After(state.signalAt) {
			return errorsText("order must occur strictly after one signal")
		}
		if !isCNTradingSession(event.EventAt) {
			return errorsText("order is outside A-share trading hours")
		}
		if event.Quantity != 0 && (!integerShares(event.Quantity) || int64(event.Quantity)%100 != 0) {
			return errorsText("entry order quantity is not a board lot")
		}
		state.orderAt = event.EventAt
	case "fill":
		if state.orderAt.IsZero() || !state.fillAt.IsZero() || event.EventAt.Before(state.orderAt) {
			return errorsText("fill must follow one order")
		}
		if !isCNTradingSession(event.EventAt) {
			return errorsText("fill is outside A-share trading hours")
		}
		if err := validateFillAccounting(event, true); err != nil {
			return err
		}
		if err := validateV150FillPolicy(event, true); err != nil {
			return err
		}
		state.fillAt, state.fillQuantity = event.EventAt, event.Quantity
	case "corporate_action":
		if state.fillAt.IsZero() || !state.exitSignalAt.IsZero() || !event.EventAt.After(state.fillAt) {
			return errorsText("corporate_action must adjust an open position before exit")
		}
		local := event.EventAt.In(time.FixedZone("Asia/Shanghai", 8*60*60))
		if local.Hour() != 9 || local.Minute() != 30 || local.Second() != 0 || local.Nanosecond() != 0 || !isLaterCNDate(state.fillAt, event.EventAt) {
			return errorsText("corporate_action must be applied exactly once before the ex-date first bar")
		}
		if !integerShares(event.Quantity) || event.AdjustmentFactor <= 0 || math.IsNaN(event.AdjustmentFactor) || math.IsInf(event.AdjustmentFactor, 0) || event.CashAmount < 0 {
			return errorsText("corporate_action quantity/cash/factor is invalid")
		}
		state.fillQuantity = event.Quantity
	case "exit_signal":
		if state.fillAt.IsZero() || !state.exitSignalAt.IsZero() || !event.EventAt.After(state.fillAt) {
			return errorsText("exit_signal must occur strictly after fill")
		}
		if !isCNTradingSession(event.EventAt) {
			return errorsText("exit_signal is outside A-share trading hours")
		}
		state.exitSignalAt = event.EventAt
	case "exit_order":
		if state.exitSignalAt.IsZero() || !state.exitOrderAt.IsZero() || event.EventAt.Before(state.exitSignalAt) {
			return errorsText("exit_order must follow exit_signal")
		}
		if !isCNTradingSession(event.EventAt) {
			return errorsText("exit_order is outside A-share trading hours")
		}
		state.exitOrderAt = event.EventAt
	case "exit_fill":
		anchor := state.exitSignalAt
		if !state.exitOrderAt.IsZero() {
			anchor = state.exitOrderAt
		}
		if state.fillAt.IsZero() || state.exitSignalAt.IsZero() || !state.exitFillAt.IsZero() || event.EventAt.Before(anchor) {
			return errorsText("exit_fill must follow exit_signal/exit_order")
		}
		if !isCNTradingSession(event.EventAt) {
			return errorsText("exit_fill is outside A-share trading hours")
		}
		if !isLaterCNDate(state.fillAt, event.EventAt) {
			return errorsText("exit_fill violates A-share T+1")
		}
		if err := validateFillAccounting(event, false); err != nil {
			return err
		}
		if !nearlyEqual(event.Quantity, state.fillQuantity) {
			return errorsText("exit quantity differs from entry fill")
		}
		if err := validateV150FillPolicy(event, false); err != nil {
			return err
		}
		state.exitFillAt, state.terminal = event.EventAt, true
	case "reject", "activation_expired", "expired":
		if state.issuedAt.IsZero() || !state.fillAt.IsZero() {
			return errorsText("reject must terminate an unfilled issued rule")
		}
		state.terminal = true
	default:
		return fmt.Errorf("unsupported lifecycle event type %q", kind)
	}
	if kind != "rule_issued" && run.ValidFromAt != nil && event.EventAt.Before(*run.ValidFromAt) {
		return errorsText("event precedes run validFrom")
	}
	return nil
}

type errorsText string

func (e errorsText) Error() string { return string(e) }

func integerShares(quantity float64) bool {
	return finitePositive(quantity) && math.Trunc(quantity) == quantity
}

func isCNTradingSession(at time.Time) bool {
	cn := at.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	if cn.Weekday() == time.Saturday || cn.Weekday() == time.Sunday {
		return false
	}
	minute := cn.Hour()*60 + cn.Minute()
	return (minute >= 9*60+30 && minute <= 11*60+30) || (minute >= 13*60 && minute <= 15*60)
}

func isLaterCNDate(entry, exit time.Time) bool {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	a, b := entry.In(zone), exit.In(zone)
	entryDay := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, zone)
	exitDay := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, zone)
	return exitDay.After(entryDay)
}

func validateV150FillPolicy(event models.OrderEvent, buy bool) error {
	if event.StrategyVersion != v150.StrategyVersion {
		return nil
	}
	cfg := v150.FixedStrategyV150Config()
	quantity := int(event.Quantity)
	if buy {
		expected := v150.SizeRoundLot(event.Price, cfg.TargetCashPerPosition, cfg)
		if expected.Rejected || expected.Quantity != quantity {
			return fmt.Errorf("entry quantity %d does not match fixed 10000 CNY round-lot size %d", quantity, expected.Quantity)
		}
	}
	market := replayMarket(event.Symbol)
	rawPrice := event.Price
	if buy {
		rawPrice /= 1 + cfg.BaseSlippageBPS/10_000
	} else {
		rawPrice /= 1 - cfg.BaseSlippageBPS/10_000
	}
	side := v150.SideSell
	if buy {
		side = v150.SideBuy
	}
	cost := v150.CalculateTradeCost(side, market, rawPrice, quantity, cfg.SlippageScenarios()[0], cfg)
	expectedFees := cost.Commission + cost.TransferFee + cost.StampDuty
	if math.Abs(event.Fees-expectedFees) > math.Max(0.02, expectedFees*1e-6) {
		return fmt.Errorf("persisted fees %.8f do not match V1.5 fixed fees %.8f (symbol=%s market=%s)", event.Fees, expectedFees, event.Symbol, market)
	}
	return nil
}

func replayMarket(symbol string) v150.Market {
	if market := v150.ResolveMarket(symbol); market != v150.MarketUnknown {
		return market
	}
	code := strings.ToUpper(strings.TrimSpace(symbol))
	if dot := strings.IndexByte(code, '.'); dot >= 0 {
		code = code[:dot]
	}
	switch {
	case strings.HasPrefix(code, "6"), strings.HasPrefix(code, "5"), strings.HasPrefix(code, "9"):
		return v150.MarketSH
	case strings.HasPrefix(code, "0"), strings.HasPrefix(code, "3"):
		return v150.MarketSZ
	case strings.HasPrefix(code, "4"), strings.HasPrefix(code, "8"):
		return v150.MarketBJ
	default:
		return v150.MarketUnknown
	}
}
