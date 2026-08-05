package v150

import (
	"math"
)

const (
	RejectPlanRiskOff    = "risk_off_no_trade"
	RejectPlanInputs     = "invalid_plan_inputs"
	RejectPlanResistance = "target_resistance_not_above_entry"
	RejectPlanRewardRisk = "achievable_reward_risk_below_1_5"
)

func BuildTradePlans(ctx RunContext, candidate Candidate, regime RegimeDecision, cfg StrategyV150Config) []PlanResult {
	if regime.NoTrade || regime.Regime == RegimeRiskOff {
		return []PlanResult{{Reason: RejectPlanRiskOff}}
	}
	if ctx.ValidFromTradeDayIndex <= 0 || ctx.ValidFromTradeDayIndex < ctx.TradeDayIndex {
		return []PlanResult{{Reason: RejectPlanInputs}}
	}
	results := []PlanResult{buildPullbackPlan(ctx, candidate, cfg)}
	if regime.Regime == RegimeRiskOn && !regime.PullbackOnly {
		results = append(results, buildBreakoutPlan(ctx, candidate, cfg))
	}
	return results
}

func buildPullbackPlan(ctx RunContext, candidate Candidate, cfg StrategyV150Config) PlanResult {
	support := math.Max(candidate.MA10, candidate.MA20)
	if support <= 0 || candidate.ATR14 <= 0 {
		return PlanResult{Reason: RejectPlanInputs}
	}
	overhead := candidate.TargetResistance
	if overhead <= 0 {
		overhead = candidate.Resistance20
	}
	plan := TradePlan{
		Symbol:                     candidate.Symbol,
		Path:                       PathPullback,
		DecisionTradeDayIndex:      ctx.TradeDayIndex,
		ValidFromTradeDayIndex:     ctx.ValidFromTradeDayIndex,
		ValidFromAt:                ctx.ValidFromAt,
		EvaluationMinutes:          cfg.PullbackRecoveryMinutes,
		Support:                    support,
		EntryMin:                   math.Max(0, support-cfg.PullbackZoneATR*candidate.ATR14),
		EntryMax:                   support + cfg.PullbackZoneATR*candidate.ATR14,
		ReferenceEntry:             support,
		TargetResistance:           overhead,
		ATR14:                      candidate.ATR14,
		NegativeOvernightGapRisk60: candidate.NegativeOvernightGapRisk60,
		ValidTradeDays:             cfg.ActivationValidTradeDays,
		MaxHoldTradeDays:           cfg.MaximumHoldTradeDays,
		TrailingActivationR:        cfg.TrailingActivationR,
		TrailingATRMultiple:        cfg.TrailingATRMultiple,
	}
	return priceTradePlan(plan, support, candidate.NegativeOvernightGapRisk60, cfg)
}

func buildBreakoutPlan(ctx RunContext, candidate Candidate, cfg StrategyV150Config) PlanResult {
	if candidate.Resistance20 <= 0 || candidate.ATR14 <= 0 {
		return PlanResult{Reason: RejectPlanInputs}
	}
	overheadResistance := candidate.TargetResistance
	if overheadResistance <= candidate.Resistance20 {
		// A 20-day breakout that is also the 60-day high has no historical
		// resistance above its trigger. Reusing the trigger itself as the target
		// cap would make 0.995 * resistance lower than the planned entry and
		// reject precisely the genuine new-high setup this path is meant to trade.
		overheadResistance = 0
	}
	plan := TradePlan{
		Symbol:                     candidate.Symbol,
		Path:                       PathBreakout,
		DecisionTradeDayIndex:      ctx.TradeDayIndex,
		ValidFromTradeDayIndex:     ctx.ValidFromTradeDayIndex,
		ValidFromAt:                ctx.ValidFromAt,
		EvaluationMinutes:          cfg.PullbackRecoveryMinutes,
		Trigger:                    candidate.Resistance20,
		ReferenceEntry:             candidate.Resistance20,
		TargetResistance:           overheadResistance,
		ATR14:                      candidate.ATR14,
		NegativeOvernightGapRisk60: candidate.NegativeOvernightGapRisk60,
		MinimumVolumeRatio:         cfg.BreakoutMinimumVolumeRatio,
		NoActivationAfterMin:       cfg.BreakoutActivationCutoffMin,
		ValidTradeDays:             cfg.ActivationValidTradeDays,
		MaxHoldTradeDays:           cfg.MaximumHoldTradeDays,
		TrailingActivationR:        cfg.TrailingActivationR,
		TrailingATRMultiple:        cfg.TrailingATRMultiple,
	}
	return priceTradePlan(plan, candidate.Resistance20, candidate.NegativeOvernightGapRisk60, cfg)
}

// RepriceTradePlan binds risk and reward to the actual adverse-slippage fill,
// rather than pretending the activation threshold was the execution price.
func RepriceTradePlan(plan TradePlan, fillPrice float64, candidate Candidate, cfg StrategyV150Config) PlanResult {
	if fillPrice <= 0 {
		return PlanResult{Plan: plan, Reason: RejectPlanInputs}
	}
	return priceTradePlan(plan, fillPrice, candidate.NegativeOvernightGapRisk60, cfg)
}

func priceTradePlan(plan TradePlan, entryPrice, negativeGapRisk float64, cfg StrategyV150Config) PlanResult {
	if entryPrice <= 0 || plan.ATR14 <= 0 {
		return PlanResult{Plan: plan, Reason: RejectPlanInputs}
	}
	atrStopRatio := cfg.StopATRMultiple * plan.ATR14 / entryPrice
	stopRatio := math.Max(atrStopRatio, math.Abs(negativeGapRisk))
	stopRatio = clampFloat(stopRatio, cfg.MinimumStopRatio, cfg.MaximumStopRatio)
	risk := entryPrice * stopRatio
	target := entryPrice + cfg.TargetRiskMultiple*risk

	if plan.TargetResistance > 0 {
		capPrice := plan.TargetResistance * cfg.ResistanceTargetMultiplier
		if capPrice <= entryPrice {
			plan.ReferenceEntry = entryPrice
			plan.Stop = entryPrice - risk
			plan.RiskPerShare = risk
			return PlanResult{Plan: plan, Reason: RejectPlanResistance}
		}
		target = math.Min(target, capPrice)
	}

	rewardRisk := (target - entryPrice) / risk
	plan.ReferenceEntry = entryPrice
	plan.Stop = entryPrice - risk
	plan.Target = target
	plan.RiskPerShare = risk
	plan.RewardRisk = rewardRisk
	if rewardRisk+1e-12 < cfg.MinimumAchievableRiskReward {
		return PlanResult{Plan: plan, Reason: RejectPlanRewardRisk}
	}
	return PlanResult{Plan: plan, Accepted: true}
}

func clampFloat(value, minimum, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}
