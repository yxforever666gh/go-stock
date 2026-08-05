package v150

import (
	"math"
	"strings"
	"time"
)

const (
	RejectRiskOff                 = "risk_off_no_trade"
	RejectMissingDaily            = "missing_daily_data"
	RejectMissingRelativeStrength = "missing_510300_relative_strength"
	RejectMissingCurrent          = "missing_current_data"
	RejectST                      = "st"
	RejectListingAge              = "listing_age_below_120_calendar_days"
	RejectSuspended               = "suspended"
	RejectLiquidity               = "average_amount20_below_1e8"
	RejectPrice                   = "price_below_3"
	RejectATR                     = "invalid_atr14"
	RejectATRRatio                = "atr14_price_ratio_above_6pct"
	RejectDayChase                = "day_change_above_5pct"
	RejectGapChase                = "absolute_gap_above_4pct"
	RejectMA20Chase               = "price_above_ma20_by_more_than_1_5atr"
	RejectDuplicateOpen           = "duplicate_open_symbol"
	RejectDuplicatePending        = "duplicate_pending_symbol"
	RejectDailyEntryLimit         = "daily_entry_limit"
	RejectPortfolioCapacity       = "maximum_5_open_positions"
	RejectSectorDailyLimit        = "sector_daily_limit_1"
	RejectStopCooldown            = "stop_cooldown_5_trade_days"
	RejectMissingSector           = "missing_sector_classification"
)

func EvaluateEligibility(ctx RunContext, candidate Candidate, regime RegimeDecision, cfg StrategyV150Config) EligibilityResult {
	reasons := make([]string, 0, 8)
	if regime.NoTrade || regime.Regime == RegimeRiskOff {
		reasons = append(reasons, RejectRiskOff)
	}
	if !candidate.HasDailyData {
		reasons = append(reasons, RejectMissingDaily)
	}
	if !candidate.HasRelativeStrengthData {
		reasons = append(reasons, RejectMissingRelativeStrength)
	}
	if !candidate.HasCurrentData || candidate.Price <= 0 {
		reasons = append(reasons, RejectMissingCurrent)
	}
	if candidate.ST {
		reasons = append(reasons, RejectST)
	}
	if candidate.ListedAt.IsZero() || calendarDaysBetween(candidate.ListedAt, ctx.AsOf) < cfg.MinimumListingCalendarDays {
		reasons = append(reasons, RejectListingAge)
	}
	if candidate.Suspended {
		reasons = append(reasons, RejectSuspended)
	}
	if strings.TrimSpace(candidate.Sector) == "" {
		reasons = append(reasons, RejectMissingSector)
	}
	if candidate.AverageAmount20 < cfg.MinimumAverageAmount20 {
		reasons = append(reasons, RejectLiquidity)
	}
	if candidate.Price < cfg.MinimumPrice {
		reasons = append(reasons, RejectPrice)
	}
	if candidate.ATR14 <= 0 {
		reasons = append(reasons, RejectATR)
	} else if candidate.Price > 0 && candidate.ATR14/candidate.Price > cfg.MaximumATRRatio+1e-12 {
		reasons = append(reasons, RejectATRRatio)
	}
	if candidate.DayChangeRatio > cfg.MaximumDayChange+1e-12 {
		reasons = append(reasons, RejectDayChase)
	}
	if math.Abs(candidate.GapRatio) > cfg.MaximumAbsoluteGap+1e-12 {
		reasons = append(reasons, RejectGapChase)
	}
	if candidate.ATR14 > 0 && candidate.MA20 > 0 && candidate.Price-candidate.MA20 > cfg.MaximumDistanceFromMA20ATR*candidate.ATR14+1e-12 {
		reasons = append(reasons, RejectMA20Chase)
	}
	return EligibilityResult{Eligible: len(reasons) == 0, Reasons: reasons}
}

func EvaluatePortfolioEligibility(candidate Candidate, state PortfolioState, cfg StrategyV150Config) EligibilityResult {
	reasons := make([]string, 0, 5)
	sector := strings.TrimSpace(candidate.Sector)
	if sector == "" {
		reasons = append(reasons, RejectMissingSector)
	}
	if state.OpenSymbols[candidate.Symbol] {
		reasons = append(reasons, RejectDuplicateOpen)
	}
	if state.PendingSymbols[candidate.Symbol] {
		reasons = append(reasons, RejectDuplicatePending)
	}
	// The portfolio ceiling is a holdings constraint, not a reservation
	// constraint. Pending rules still block another rule for the same symbol,
	// but they do not consume one of the five position slots until an entry is
	// actually filled. The execution path reloads this state inside its entry
	// critical section, so concurrent fills cannot both claim the final slot.
	if countTrue(state.OpenSymbols) >= cfg.MaximumOpenPositions {
		reasons = append(reasons, RejectPortfolioCapacity)
	}
	dailyCap := cfg.RiskOnDailyCap
	if state.ExecutionDailyCap != nil {
		dailyCap = *state.ExecutionDailyCap
	}
	if dailyCap <= 0 || state.TodayEntries >= dailyCap {
		reasons = append(reasons, RejectDailyEntryLimit)
	}
	if sector != "" && state.TodaySectorEntries[sector] >= cfg.MaximumSectorEntriesDay {
		reasons = append(reasons, RejectSectorDailyLimit)
	}
	if days, exists := state.TradeDaysSinceLastStop[candidate.Symbol]; exists && days < cfg.StopCooldownTradeDays {
		reasons = append(reasons, RejectStopCooldown)
	}
	return EligibilityResult{Eligible: len(reasons) == 0, Reasons: reasons}
}

func countTrue(values map[string]bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func calendarDaysBetween(from, to time.Time) int {
	if from.IsZero() || to.IsZero() {
		return -1
	}
	location := to.Location()
	from = from.In(location)
	to = to.In(location)
	fromDate := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, location)
	toDate := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, location)
	return int(toDate.Sub(fromDate) / (24 * time.Hour))
}
