package v150

import (
	"math"
	"time"
)

const (
	RejectActivationExpired = "activation_expired_after_3_trade_days"
	RejectActivationAnchor  = "activation_valid_from_trade_day_index_missing"
	RejectNotNextBar        = "entry_not_on_next_bar"
	RejectIncompleteBar     = "bar_not_completed"
	RejectLimitUpBuy        = "limit_up_no_buy"
	RejectLimitDownSell     = "limit_down_no_sell"
	RejectInvalidOpen       = "invalid_open_price"
	RejectInvalidSignal     = "invalid_activation_signal_price"
	RejectEntryGap          = "entry_open_gap_exceeds_4pct"
	RejectSetupInvalidated  = "entry_setup_invalidated_before_fill"
)

func DetectActivation(plan TradePlan, previous, current Bar, state ActivationState) (ActivationSignal, ActivationState) {
	if state.Signaled || !current.Completed {
		return ActivationSignal{}, state
	}
	if !plan.ValidFromAt.IsZero() && current.End.Before(plan.ValidFromAt) {
		return ActivationSignal{}, state
	}
	if plan.ValidFromTradeDayIndex <= 0 {
		return ActivationSignal{Reason: RejectActivationAnchor}, state
	}
	tradeDayOffset := current.TradeDayIndex - plan.ValidFromTradeDayIndex
	if tradeDayOffset < 0 || tradeDayOffset >= plan.ValidTradeDays {
		return ActivationSignal{Reason: RejectActivationExpired}, state
	}

	switch plan.Path {
	case PathPullback:
		if current.IntervalMinutes != plan.EvaluationMinutes {
			return ActivationSignal{}, state
		}
		if rangesOverlap(current.Low, current.High, plan.EntryMin, plan.EntryMax) {
			state.ZoneTouched = true
		}
		if state.ZoneTouched && current.Close >= plan.Support {
			state.Signaled = true
			return ActivationSignal{
				Triggered:     true,
				Path:          PathPullback,
				At:            current.End,
				BarIndex:      current.Index,
				TradeDayIndex: current.TradeDayIndex,
				SignalClose:   current.Close,
				Reason:        "completed_15m_recovery",
			}, state
		}
	case PathBreakout:
		if !previous.Completed || previous.Index+1 != current.Index {
			return ActivationSignal{}, state
		}
		if plan.NoActivationAfterMin > 0 && isAfterMinute(current.End, plan.NoActivationAfterMin) {
			return ActivationSignal{}, state
		}
		if previous.Close <= plan.Trigger && current.Close > plan.Trigger && current.VolumeRatioSameSlot >= plan.MinimumVolumeRatio {
			state.Signaled = true
			return ActivationSignal{
				Triggered:     true,
				Path:          PathBreakout,
				At:            current.End,
				BarIndex:      current.Index,
				TradeDayIndex: current.TradeDayIndex,
				SignalClose:   current.Close,
				Reason:        "true_crossing_with_same_slot_volume",
			}, state
		}
	}
	return ActivationSignal{}, state
}

func NewEntryOrder(signal ActivationSignal, plan TradePlan, candidate Candidate) EntryOrder {
	return EntryOrder{
		Symbol:              candidate.Symbol,
		Sector:              candidate.Sector,
		Market:              candidate.Market,
		Plan:                plan,
		SignalAt:            signal.At,
		SignalBarIndex:      signal.BarIndex,
		SignalTradeDayIndex: signal.TradeDayIndex,
		SignalClose:         signal.SignalClose,
	}
}

func TryFillEntryOnNextBar(order EntryOrder, next Bar, portfolio PortfolioState, availableCash float64, scenario SlippageScenario, cfg StrategyV150Config) EntryFillResult {
	if next.Index <= order.SignalBarIndex {
		return EntryFillResult{Status: FillPending}
	}
	if next.Index != order.SignalBarIndex+1 {
		return rejectedEntry(order, next.Start, FillExpired, RejectNotNextBar)
	}
	if !next.Start.After(order.SignalAt) {
		return rejectedEntry(order, next.Start, FillRejected, RejectNotNextBar)
	}
	if !next.Completed {
		return rejectedEntry(order, next.Start, FillRejected, RejectIncompleteBar)
	}
	if next.Suspended {
		return rejectedEntry(order, next.Start, FillRejected, RejectSuspended)
	}
	if next.LimitUpLocked {
		return rejectedEntry(order, next.Start, FillRejected, RejectLimitUpBuy)
	}
	if next.Open <= 0 {
		return rejectedEntry(order, next.Start, FillRejected, RejectInvalidOpen)
	}
	if order.SignalClose <= 0 {
		return rejectedEntry(order, next.Start, FillRejected, RejectInvalidSignal)
	}
	if math.Abs(next.Open/order.SignalClose-1) > cfg.MaximumAbsoluteGap+1e-12 {
		return rejectedEntry(order, next.Start, FillRejected, RejectEntryGap)
	}
	switch order.Plan.Path {
	case PathPullback:
		if order.Plan.Support > 0 && next.Open < order.Plan.Support {
			return rejectedEntry(order, next.Start, FillRejected, RejectSetupInvalidated)
		}
	case PathBreakout:
		if order.Plan.Trigger > 0 && next.Open <= order.Plan.Trigger {
			return rejectedEntry(order, next.Start, FillRejected, RejectSetupInvalidated)
		}
	}
	portfolioEligibility := EvaluatePortfolioEligibility(Candidate{Symbol: order.Symbol, Sector: order.Sector}, portfolio, cfg)
	if !portfolioEligibility.Eligible {
		return rejectedEntry(order, next.Start, FillRejected, portfolioEligibility.Reasons[0])
	}

	market := order.Market
	if market == MarketUnknown {
		market = ResolveMarket(order.Symbol)
	}
	unitCost := CalculateTradeCost(SideBuy, market, next.Open, cfg.RoundLotSize, scenario, cfg)
	size := SizeRoundLot(unitCost.EffectivePrice, availableCash, cfg)
	if size.Rejected {
		return rejectedEntry(order, next.Start, FillRejected, size.Reason)
	}
	cost := CalculateTradeCost(SideBuy, market, next.Open, size.Quantity, scenario, cfg)
	if -cost.CashFlow > availableCash {
		return rejectedEntry(order, next.Start, FillRejected, "insufficient_cash_after_costs")
	}
	priced := priceTradePlan(order.Plan, cost.EffectivePrice, order.Plan.NegativeOvernightGapRisk60, cfg)
	if !priced.Accepted {
		return rejectedEntry(order, next.Start, FillRejected, priced.Reason)
	}
	position := Position{
		Symbol:             order.Symbol,
		Sector:             order.Sector,
		Market:             market,
		Quantity:           size.Quantity,
		EntryAt:            next.Start,
		EntryTradeDayIndex: next.TradeDayIndex,
		EntryPrice:         cost.EffectivePrice,
		InitialStop:        priced.Plan.Stop,
		Target:             priced.Plan.Target,
		RiskPerShare:       priced.Plan.RiskPerShare,
		ATR14:              priced.Plan.ATR14,
		HighestClose:       cost.EffectivePrice,
		MaxHoldTradeDays:   priced.Plan.MaxHoldTradeDays,
	}
	events := []OrderEvent{
		{Type: EventSignal, At: order.SignalAt, Symbol: order.Symbol, Reason: string(order.Plan.Path)},
		{Type: EventOrder, At: next.Start, Symbol: order.Symbol, Reason: "next_bar_market_order"},
		{Type: EventFill, At: next.Start, Symbol: order.Symbol, Price: cost.EffectivePrice, Quantity: size.Quantity},
	}
	return EntryFillResult{
		Status:   FillFilled,
		At:       next.Start,
		Cost:     cost,
		Plan:     priced.Plan,
		Position: position,
		Events:   events,
	}
}

func EvaluateExit(position Position, bar Bar, scenario SlippageScenario, cfg StrategyV150Config) ExitResult {
	if !bar.Completed || !T1SellEligible(position, bar) || bar.Suspended || bar.LimitDownLocked {
		return ExitResult{}
	}
	stop := math.Max(position.InitialStop, position.TrailingStop)
	holdingTradeDay := HoldingTradeDay(position, bar)
	maximumTradeDays := maximumHoldTradeDays(position, cfg)
	timeExitDue := holdingTradeDay > maximumTradeDays ||
		(holdingTradeDay == maximumTradeDays && minuteOfDay(bar.Start) >= cfg.TimeExitMinute)
	reason := ExitNone
	rawPrice := 0.0
	fillAt := bar.End

	switch {
	case stop > 0 && bar.Open <= stop:
		reason, rawPrice, fillAt = ExitStop, bar.Open, bar.Start
	case position.Target > 0 && bar.Open >= position.Target:
		reason, rawPrice, fillAt = ExitTarget, bar.Open, bar.Start
	case timeExitDue:
		// The time-exit order is effective at the first tradable bar's open.
		// Once day 10 14:45 has passed, a blocked bar keeps that intent alive;
		// later intrabar stop/target ranges cannot occur before this open fill.
		reason, rawPrice, fillAt = ExitTime, bar.Open, bar.Start
	case stop > 0 && position.Target > 0 && bar.Low <= stop && bar.High >= position.Target:
		// Conservative ordering for ambiguous OHLC bars.
		reason, rawPrice = ExitStop, stop
	case stop > 0 && bar.Low <= stop:
		reason, rawPrice = ExitStop, stop
	case position.Target > 0 && bar.High >= position.Target:
		reason, rawPrice = ExitTarget, position.Target
	default:
		return ExitResult{}
	}

	cost := CalculateTradeCost(SideSell, position.Market, rawPrice, position.Quantity, scenario, cfg)
	events := []OrderEvent{
		{Type: EventExitSignal, At: fillAt, Symbol: position.Symbol, Price: rawPrice, Quantity: position.Quantity, Reason: string(reason)},
		{Type: EventExitFill, At: fillAt, Symbol: position.Symbol, Price: cost.EffectivePrice, Quantity: position.Quantity, Reason: string(reason)},
	}
	return ExitResult{Triggered: true, Reason: reason, At: fillAt, Cost: cost, Events: events}
}

// AdvanceTrailingStop consumes a completed bar after exit evaluation. The
// resulting stop therefore becomes effective on the following bar only.
func AdvanceTrailingStop(position Position, completed Bar, cfg StrategyV150Config) Position {
	if !completed.Completed || completed.Close <= 0 {
		return position
	}
	if completed.Close > position.HighestClose {
		position.HighestClose = completed.Close
	}
	if position.HighestClose >= position.EntryPrice+cfg.TrailingActivationR*position.RiskPerShare {
		position.TrailingActive = true
		candidateStop := position.HighestClose - cfg.TrailingATRMultiple*position.ATR14
		position.TrailingStop = math.Max(position.InitialStop, math.Max(position.TrailingStop, candidateStop))
	}
	return position
}

func T1SellEligible(position Position, bar Bar) bool {
	return bar.TradeDayIndex > position.EntryTradeDayIndex
}

func HoldingTradeDay(position Position, bar Bar) int {
	if bar.TradeDayIndex < position.EntryTradeDayIndex {
		return 0
	}
	return bar.TradeDayIndex - position.EntryTradeDayIndex + 1
}

func rejectedEntry(order EntryOrder, at time.Time, status FillStatus, reason string) EntryFillResult {
	return EntryFillResult{
		Status: status,
		Reason: reason,
		At:     at,
		Events: []OrderEvent{{Type: EventReject, At: at, Symbol: order.Symbol, Reason: reason}},
	}
}

func rangesOverlap(lowA, highA, lowB, highB float64) bool {
	return highA >= lowB && highB >= lowA
}

func minuteOfDay(value time.Time) int {
	return value.Hour()*60 + value.Minute()
}

func isAfterMinute(value time.Time, cutoff int) bool {
	minute := minuteOfDay(value)
	return minute > cutoff || (minute == cutoff && (value.Second() > 0 || value.Nanosecond() > 0))
}

func maximumHoldTradeDays(position Position, cfg StrategyV150Config) int {
	if position.MaxHoldTradeDays > 0 {
		return position.MaxHoldTradeDays
	}
	return cfg.MaximumHoldTradeDays
}
