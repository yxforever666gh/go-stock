package data

import (
	"fmt"
	"strings"
)

const (
	marketSummaryFeasibleRewardRisk  = 0.80
	marketSummaryFeasibleDownsidePct = 5.00
)

type marketSummaryFeasiblePlan struct {
	Path          string  `json:"path"`
	EntryRange    string  `json:"entryRange,omitempty"`
	WorstEntry    float64 `json:"worstEntry"`
	StopLoss      float64 `json:"stopLoss"`
	TakeProfit    float64 `json:"takeProfit"`
	RewardRisk    float64 `json:"rewardRisk"`
	DownsidePct   float64 `json:"downsidePct"`
	PassHardGate  bool    `json:"passHardGate"`
	FailureReason string  `json:"failureReason,omitempty"`
}

type marketSummaryTradePlanFeasibility struct {
	Score           int
	HasFeasiblePlan bool
	Plans           []marketSummaryFeasiblePlan
	BestRewardRisk  float64
	MinDownsidePct  float64
	FailureReason   string
}

func buildMarketSummaryTradePlanFeasibility(candidate marketSummaryVerifiedCandidate) marketSummaryTradePlanFeasibility {
	anchor, ok := marketSummaryCandidateAnchorPrice(candidate)
	if !ok || anchor <= 0 {
		return marketSummaryTradePlanFeasibility{
			Score:         -45,
			FailureReason: "缺少价格锚点",
		}
	}

	plans := []marketSummaryFeasiblePlan{
		buildMarketSummaryPullbackFeasiblePlan(candidate, anchor),
		buildMarketSummaryBreakoutFeasiblePlan(candidate, anchor),
	}

	result := marketSummaryTradePlanFeasibility{
		Plans:          plans,
		BestRewardRisk: 0,
		MinDownsidePct: 999,
	}
	borderline := false
	for _, plan := range plans {
		if plan.RewardRisk > result.BestRewardRisk {
			result.BestRewardRisk = plan.RewardRisk
		}
		if plan.DownsidePct > 0 && plan.DownsidePct < result.MinDownsidePct {
			result.MinDownsidePct = plan.DownsidePct
		}
		if plan.PassHardGate {
			result.HasFeasiblePlan = true
		}
		if (plan.RewardRisk >= 0.50 && plan.RewardRisk < marketSummaryFeasibleRewardRisk) ||
			(plan.DownsidePct > marketSummaryFeasibleDownsidePct && plan.DownsidePct <= 6.00) {
			borderline = true
		}
	}
	if result.MinDownsidePct == 999 {
		result.MinDownsidePct = 0
	}
	switch {
	case result.HasFeasiblePlan && result.BestRewardRisk >= 1.20 && result.MinDownsidePct <= 4.00:
		result.Score = 42
	case result.HasFeasiblePlan:
		result.Score = 34
	case borderline:
		result.Score = 8
		result.FailureReason = "接近硬规则门槛"
	default:
		result.Score = -28
		result.FailureReason = "未形成满足盈亏比/止损空间的路径"
	}
	return result
}

func buildMarketSummaryPullbackFeasiblePlan(candidate marketSummaryVerifiedCandidate, anchor float64) marketSummaryFeasiblePlan {
	ma5, hasMA5 := parseLooseFloat(candidate.TechnicalMetrics.Ma5)
	ma10, hasMA10 := parseLooseFloat(candidate.TechnicalMetrics.Ma10)
	low5, hasLow5 := parseLooseFloat(candidate.TechnicalMetrics.Low5d)
	high5, hasHigh5 := parseLooseFloat(candidate.TechnicalMetrics.High5d)
	high20, hasHigh20 := parseLooseFloat(candidate.TechnicalMetrics.High20d)

	entryMin := anchor * 0.985
	entryMax := anchor * 0.997
	if hasMA5 && ma5 > 0 && ma5 < anchor*1.02 {
		entryMin = maxMarketSummaryFeasibilityFloat(entryMin, ma5*0.995)
	}
	if hasMA10 && ma10 > 0 && ma10 < anchor*1.01 {
		entryMin = maxMarketSummaryFeasibilityFloat(entryMin, ma10*0.995)
	}
	if entryMin > entryMax {
		entryMin = entryMax * 0.992
	}
	worstEntry := entryMax

	stopLoss := worstEntry * 0.955
	if hasLow5 && low5 > 0 && low5 < worstEntry {
		stopLoss = maxMarketSummaryFeasibilityFloat(stopLoss, low5*0.99)
	}
	if stopLoss >= worstEntry {
		stopLoss = worstEntry * 0.955
	}

	takeProfit := worstEntry * 1.04
	if hasHigh5 && high5 > takeProfit {
		takeProfit = high5
	}
	if hasHigh20 && high20 > takeProfit {
		takeProfit = high20
	}

	return finalizeMarketSummaryFeasiblePlan("pullback", entryMin, entryMax, worstEntry, stopLoss, takeProfit)
}

func buildMarketSummaryBreakoutFeasiblePlan(candidate marketSummaryVerifiedCandidate, anchor float64) marketSummaryFeasiblePlan {
	high3, hasHigh3 := parseLooseFloat(candidate.TechnicalMetrics.High3d)
	high5, hasHigh5 := parseLooseFloat(candidate.TechnicalMetrics.High5d)
	high20, hasHigh20 := parseLooseFloat(candidate.TechnicalMetrics.High20d)

	trigger := anchor * 1.01
	if hasHigh3 && high3 > 0 && high3 < anchor*1.10 {
		trigger = maxMarketSummaryFeasibilityFloat(trigger, high3*1.002)
	}
	if hasHigh5 && high5 > 0 && high5 < anchor*1.12 {
		trigger = maxMarketSummaryFeasibilityFloat(trigger, high5*1.002)
	}
	entryMin := trigger
	entryMax := trigger * 1.006
	worstEntry := entryMax
	stopLoss := maxMarketSummaryFeasibilityFloat(anchor*0.985, worstEntry*0.954)
	if stopLoss >= worstEntry {
		stopLoss = worstEntry * 0.954
	}
	takeProfit := worstEntry * 1.045
	if hasHigh20 && high20 > takeProfit {
		takeProfit = high20
	}
	return finalizeMarketSummaryFeasiblePlan("breakout", entryMin, entryMax, worstEntry, stopLoss, takeProfit)
}

func finalizeMarketSummaryFeasiblePlan(path string, entryMin, entryMax, worstEntry, stopLoss, takeProfit float64) marketSummaryFeasiblePlan {
	plan := marketSummaryFeasiblePlan{
		Path:       path,
		EntryRange: fmt.Sprintf("%.2f-%.2f", round2(entryMin), round2(entryMax)),
		WorstEntry: round2(worstEntry),
		StopLoss:   round2(stopLoss),
		TakeProfit: round2(takeProfit),
	}
	if plan.WorstEntry <= 0 || plan.StopLoss <= 0 || plan.TakeProfit <= 0 || plan.StopLoss >= plan.WorstEntry || plan.TakeProfit <= plan.WorstEntry {
		plan.FailureReason = "缺少有效买入/止盈/止损结构"
		return plan
	}
	downside := plan.WorstEntry - plan.StopLoss
	plan.RewardRisk = round2((plan.TakeProfit - plan.WorstEntry) / downside)
	plan.DownsidePct = round2(downside / plan.WorstEntry * 100)
	plan.PassHardGate = plan.RewardRisk >= marketSummaryFeasibleRewardRisk
	if !plan.PassHardGate {
		switch {
		case plan.RewardRisk < marketSummaryFeasibleRewardRisk:
			plan.FailureReason = fmt.Sprintf("盈亏比 %.2f 低于 0.80", plan.RewardRisk)
		}
	}
	return plan
}

func marketSummaryCandidateAnchorPrice(candidate marketSummaryVerifiedCandidate) (float64, bool) {
	for _, raw := range []string{
		candidate.AuctionPrice,
		candidate.MinutePrice,
		candidate.CurrentPrice,
	} {
		if v, ok := parseLooseFloat(raw); ok && v > 0 {
			return v, true
		}
	}
	return 0, false
}

func scoreMarketSummaryIndicatorTradePlanFeasibility(item marketSummaryIndicatorCandidate) int {
	price, ok := parseLooseFloat(item.Metrics["price"])
	if !ok || price <= 0 {
		return -20
	}
	changePct, _ := parseLooseFloat(item.Metrics["changePct"])
	volumeRatio, _ := parseLooseFloat(item.Metrics["volumeRatio"])
	score := 0
	switch {
	case changePct >= 1 && changePct <= 6:
		score += 16
	case changePct > 6 && changePct <= 9:
		score += 6
	case changePct > 9:
		score -= 10
	case changePct < -1:
		score -= 12
	}
	switch {
	case volumeRatio >= 1.2 && volumeRatio <= 4:
		score += 14
	case volumeRatio > 4 && volumeRatio <= 7:
		score += 6
	case volumeRatio < 0.8:
		score -= 8
	}
	if strings.TrimSpace(item.Metrics["amount"]) != "" {
		score += 6
	}
	return score
}

func maxMarketSummaryFeasibilityFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
