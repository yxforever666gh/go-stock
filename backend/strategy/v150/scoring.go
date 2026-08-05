package v150

import (
	"math"
	"sort"
	"strings"
)

func ScoreCandidate(ctx RunContext, candidate Candidate, cfg StrategyV150Config) ScoreBreakdown {
	result := ScoreBreakdown{
		TrendRelative: weightedPoints(candidate.Signals.TrendRelativeStrength, cfg.TrendRelativeWeight),
		Setup:         weightedPoints(candidate.Signals.SetupQuality, cfg.SetupWeight),
		Sector:        weightedPoints(candidate.Signals.SectorStrength, cfg.SectorWeight),
		LiquidityRisk: weightedPoints(candidate.Signals.LiquidityRiskQuality, cfg.LiquidityRiskWeight),
	}
	if candidate.EventAt != nil && !candidate.EventAt.IsZero() {
		eventCutoff := ctx.DataCutoffAt
		if eventCutoff.IsZero() {
			eventCutoff = ctx.AsOf
		}
		age := eventCutoff.Sub(*candidate.EventAt)
		if age >= 0 && age <= cfg.EventFreshness {
			result.Event = weightedPoints(candidate.Signals.EventStrength, cfg.EventWeight)
		}
	}
	result.Total = result.TrendRelative + result.Setup + result.Sector + result.Event + result.LiquidityRisk
	return result
}

func RankCandidates(ctx RunContext, candidates []Candidate, regime RegimeDecision, cfg StrategyV150Config) []ScoredCandidate {
	result := make([]ScoredCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, ScoredCandidate{
			Candidate:   candidate,
			Score:       ScoreCandidate(ctx, candidate, cfg),
			Eligibility: EvaluateEligibility(ctx, candidate, regime, cfg),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.Score.Total != right.Score.Total {
			return left.Score.Total > right.Score.Total
		}
		if left.Score.TrendRelative != right.Score.TrendRelative {
			return left.Score.TrendRelative > right.Score.TrendRelative
		}
		if left.Score.Setup != right.Score.Setup {
			return left.Score.Setup > right.Score.Setup
		}
		return strings.ToUpper(left.Candidate.Symbol) < strings.ToUpper(right.Candidate.Symbol)
	})
	for index := range result {
		result[index].Rank = index + 1
	}
	return result
}

// TopForVerification ranks the complete input first, then walks that fixed
// order and takes at most 18 eligible names. No LLM output ordering participates.
func TopForVerification(ranked []ScoredCandidate, cfg StrategyV150Config) []ScoredCandidate {
	result := make([]ScoredCandidate, 0, cfg.VerificationLimit)
	for _, candidate := range ranked {
		if !candidate.Eligibility.Eligible {
			continue
		}
		result = append(result, candidate)
		if len(result) == cfg.VerificationLimit {
			break
		}
	}
	return result
}

func SelectProductionCandidates(ranked []ScoredCandidate, verifiedSymbols map[string]bool, regime RegimeDecision, cfg StrategyV150Config) []ScoredCandidate {
	if regime.NoTrade || regime.DailyCap <= 0 {
		return nil
	}
	result := make([]ScoredCandidate, 0, regime.DailyCap)
	for _, candidate := range ranked {
		if !candidate.Eligibility.Eligible || candidate.Score.Total < cfg.ProductionScoreFloor {
			continue
		}
		if !verifiedSymbols[candidate.Candidate.Symbol] {
			continue
		}
		candidate.Verified = true
		result = append(result, candidate)
		if len(result) == regime.DailyCap {
			break
		}
	}
	return result
}

func weightedPoints(value float64, maximum int) int {
	value = math.Max(0, math.Min(1, value))
	return int(math.Round(value * float64(maximum)))
}
