package v150

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestEligibilityRulesAndBoundaries(t *testing.T) {
	cfg := FixedStrategyV150Config()
	ctx := validTestContext()
	regime := RegimeDecision{Regime: RegimeRiskOn, DailyCap: 2}
	base := validTestCandidate(ctx.AsOf)

	boundary := base
	boundary.ListedAt = ctx.AsOf.AddDate(0, 0, -120)
	boundary.AverageAmount20 = 100_000_000
	boundary.Price = 10
	boundary.ATR14 = 0.6
	boundary.DayChangeRatio = 0.05
	boundary.GapRatio = -0.04
	boundary.MA20 = boundary.Price - 1.5*boundary.ATR14
	if got := EvaluateEligibility(ctx, boundary, regime, cfg); !got.Eligible {
		t.Fatalf("exact boundaries should pass: %v", got.Reasons)
	}

	tests := []struct {
		name   string
		mutate func(*Candidate)
		reason string
	}{
		{"missing daily", func(c *Candidate) { c.HasDailyData = false }, RejectMissingDaily},
		{"missing relative strength", func(c *Candidate) { c.HasRelativeStrengthData = false }, RejectMissingRelativeStrength},
		{"missing current", func(c *Candidate) { c.HasCurrentData = false }, RejectMissingCurrent},
		{"ST", func(c *Candidate) { c.ST = true }, RejectST},
		{"young listing", func(c *Candidate) { c.ListedAt = ctx.AsOf.AddDate(0, 0, -119) }, RejectListingAge},
		{"suspended", func(c *Candidate) { c.Suspended = true }, RejectSuspended},
		{"missing sector", func(c *Candidate) { c.Sector = "  " }, RejectMissingSector},
		{"illiquid", func(c *Candidate) { c.AverageAmount20 = 99_999_999 }, RejectLiquidity},
		{"penny", func(c *Candidate) { c.Price = 2.99 }, RejectPrice},
		{"invalid ATR", func(c *Candidate) { c.ATR14 = 0 }, RejectATR},
		{"high ATR", func(c *Candidate) { c.ATR14 = c.Price * 0.06001 }, RejectATRRatio},
		{"day chase", func(c *Candidate) { c.DayChangeRatio = 0.05001 }, RejectDayChase},
		{"positive gap chase", func(c *Candidate) { c.GapRatio = 0.04001 }, RejectGapChase},
		{"negative gap chase", func(c *Candidate) { c.GapRatio = -0.04001 }, RejectGapChase},
		{"MA20 chase", func(c *Candidate) { c.MA20 = c.Price - 1.5001*c.ATR14 }, RejectMA20Chase},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			got := EvaluateEligibility(ctx, candidate, regime, cfg)
			if got.Eligible || !slices.Contains(got.Reasons, test.reason) {
				t.Fatalf("eligibility = %+v, want %s", got, test.reason)
			}
		})
	}

	riskOff := EvaluateEligibility(ctx, base, RegimeDecision{Regime: RegimeRiskOff, NoTrade: true}, cfg)
	if riskOff.Eligible || !slices.Contains(riskOff.Reasons, RejectRiskOff) {
		t.Fatalf("risk-off = %+v", riskOff)
	}
}

func TestPortfolioEligibilityRules(t *testing.T) {
	cfg := FixedStrategyV150Config()
	candidate := Candidate{Symbol: "600000.SH", Sector: "bank"}
	neutralCap := cfg.NeutralDailyCap
	tests := []struct {
		name   string
		state  PortfolioState
		reason string
	}{
		{"open duplicate", PortfolioState{OpenSymbols: map[string]bool{candidate.Symbol: true}}, RejectDuplicateOpen},
		{"pending duplicate", PortfolioState{PendingSymbols: map[string]bool{candidate.Symbol: true}}, RejectDuplicatePending},
		{"risk-on daily cap", PortfolioState{TodayEntries: cfg.RiskOnDailyCap}, RejectDailyEntryLimit},
		{"frozen neutral daily cap", PortfolioState{TodayEntries: neutralCap, ExecutionDailyCap: &neutralCap}, RejectDailyEntryLimit},
		{"five open", PortfolioState{OpenSymbols: map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true}}, RejectPortfolioCapacity},
		{"sector already used", PortfolioState{TodaySectorEntries: map[string]int{"bank": 1}}, RejectSectorDailyLimit},
		{"cooldown day four", PortfolioState{TradeDaysSinceLastStop: map[string]int{candidate.Symbol: 4}}, RejectStopCooldown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := EvaluatePortfolioEligibility(candidate, test.state, cfg)
			if got.Eligible || !slices.Contains(got.Reasons, test.reason) {
				t.Fatalf("eligibility = %+v", got)
			}
		})
	}
	allowed := EvaluatePortfolioEligibility(candidate, PortfolioState{TradeDaysSinceLastStop: map[string]int{candidate.Symbol: 5}}, cfg)
	if !allowed.Eligible {
		t.Fatalf("fifth completed cooldown day should pass: %v", allowed.Reasons)
	}
	pendingDoesNotReserveCapacity := EvaluatePortfolioEligibility(candidate, PortfolioState{
		OpenSymbols:    map[string]bool{"1": true, "2": true, "3": true, "4": true, "not-open": false},
		PendingSymbols: map[string]bool{"5": true, "6": true, "7": true},
	}, cfg)
	if !pendingDoesNotReserveCapacity.Eligible {
		t.Fatalf("four holdings plus unrelated pending rules should pass: %v", pendingDoesNotReserveCapacity.Reasons)
	}
}

func TestScoreWeightsFreshnessAndFloor(t *testing.T) {
	cfg := FixedStrategyV150Config()
	ctx := validTestContext()
	event := ctx.DataCutoffAt.Add(-48 * time.Hour)
	candidate := validTestCandidate(ctx.AsOf)
	candidate.EventAt = &event
	candidate.Signals = ScoreSignals{1, 1, 1, 1, 1}
	got := ScoreCandidate(ctx, candidate, cfg)
	if got != (ScoreBreakdown{TrendRelative: 30, Setup: 25, Sector: 15, Event: 20, LiquidityRisk: 10, Total: 100}) {
		t.Fatalf("score = %+v", got)
	}
	oldEvent := ctx.DataCutoffAt.Add(-48*time.Hour - time.Nanosecond)
	candidate.EventAt = &oldEvent
	got = ScoreCandidate(ctx, candidate, cfg)
	if got.Event != 0 || got.Total != 80 {
		t.Fatalf("stale event score = %+v", got)
	}
	future := ctx.DataCutoffAt.Add(time.Second)
	candidate.EventAt = &future
	if got := ScoreCandidate(ctx, candidate, cfg); got.Event != 0 {
		t.Fatalf("future event must score zero: %+v", got)
	}
	ctx.AsOf = ctx.DataCutoffAt.Add(-time.Hour)
	availableAfterFeatureSnapshot := ctx.DataCutoffAt.Add(-time.Minute)
	candidate.EventAt = &availableAfterFeatureSnapshot
	if got := ScoreCandidate(ctx, candidate, cfg); got.Event != cfg.EventWeight {
		t.Fatalf("event freshness did not use the frozen data cutoff: %+v", got)
	}
}

func TestRankAllThenVerifyTop18AndProductionCaps(t *testing.T) {
	cfg := FixedStrategyV150Config()
	ctx := validTestContext()
	regime := RegimeDecision{Regime: RegimeRiskOn, DailyCap: 2}
	candidates := make([]Candidate, 20)
	for index := range candidates {
		candidate := validTestCandidate(ctx.AsOf)
		candidate.Symbol = fmt.Sprintf("%06d.SH", index+1)
		strength := float64(20-index) / 20
		candidate.Signals = ScoreSignals{strength, 1, 1, 1, 1}
		candidates[index] = candidate
	}
	// The highest-scored name remains ranked, but eligibility filtering happens
	// only when selecting the 18 names to verify.
	candidates[0].ST = true
	ranked := RankCandidates(ctx, candidates, regime, cfg)
	if ranked[0].Candidate.Symbol != "000001.SH" || ranked[0].Eligibility.Eligible {
		t.Fatalf("rank-all semantics changed: %+v", ranked[0])
	}
	verify := TopForVerification(ranked, cfg)
	if len(verify) != 18 || verify[0].Candidate.Symbol != "000002.SH" || verify[17].Candidate.Symbol != "000019.SH" {
		t.Fatalf("verification selection = %d, first=%s last=%s", len(verify), verify[0].Candidate.Symbol, verify[len(verify)-1].Candidate.Symbol)
	}
	verified := map[string]bool{}
	for _, item := range verify {
		verified[item.Candidate.Symbol] = true
	}
	production := SelectProductionCandidates(ranked, verified, regime, cfg)
	if len(production) != 2 {
		t.Fatalf("risk-on production count = %d", len(production))
	}
	neutral := SelectProductionCandidates(ranked, verified, RegimeDecision{Regime: RegimeNeutral, DailyCap: 1, PullbackOnly: true}, cfg)
	if len(neutral) != 1 {
		t.Fatalf("neutral production count = %d", len(neutral))
	}
	if got := SelectProductionCandidates(ranked, verified, RegimeDecision{Regime: RegimeRiskOff, NoTrade: true}, cfg); len(got) != 0 {
		t.Fatalf("risk-off selected %d", len(got))
	}
}

func TestProductionScoreFloorBoundary(t *testing.T) {
	cfg := FixedStrategyV150Config()
	regime := RegimeDecision{Regime: RegimeRiskOn, DailyCap: 2}
	verified := map[string]bool{"below": true, "at": true}
	ranked := []ScoredCandidate{
		{Candidate: Candidate{Symbol: "below"}, Score: ScoreBreakdown{Total: 69}, Eligibility: EligibilityResult{Eligible: true}},
		{Candidate: Candidate{Symbol: "at"}, Score: ScoreBreakdown{Total: 70}, Eligibility: EligibilityResult{Eligible: true}},
	}
	got := SelectProductionCandidates(ranked, verified, regime, cfg)
	if len(got) != 1 || got[0].Candidate.Symbol != "at" {
		t.Fatalf("floor selection = %+v", got)
	}
}

func validTestContext() RunContext {
	asOf := strategyTestTime(9, 40)
	return RunContext{
		RunID:                  "test-run",
		StartedAt:              asOf.Add(-time.Minute),
		AsOf:                   asOf.Add(time.Minute),
		DataCutoffAt:           asOf,
		DecisionAt:             asOf.Add(time.Minute),
		GeneratedAt:            asOf.Add(time.Minute),
		ValidFromAt:            asOf.Add(5 * time.Minute),
		StrategyVersion:        StrategyVersion,
		TradeDayIndex:          100,
		ValidFromTradeDayIndex: 100,
	}
}

func validTestCandidate(asOf time.Time) Candidate {
	event := asOf.Add(-time.Hour)
	return Candidate{
		Symbol:                     "600000.SH",
		Name:                       "test",
		Sector:                     "bank",
		Market:                     MarketSH,
		ListedAt:                   asOf.AddDate(-2, 0, 0),
		HasDailyData:               true,
		HasCurrentData:             true,
		HasRelativeStrengthData:    true,
		Price:                      10,
		PreviousClose:              9.9,
		MA10:                       9.8,
		MA20:                       9.5,
		MA60:                       9,
		ATR14:                      0.4,
		AverageAmount20:            200_000_000,
		DayChangeRatio:             0.02,
		GapRatio:                   0.01,
		Resistance20:               12,
		TargetResistance:           14,
		NegativeOvernightGapRisk60: 0.035,
		EventAt:                    &event,
		Signals:                    ScoreSignals{1, 1, 1, 1, 1},
	}
}
