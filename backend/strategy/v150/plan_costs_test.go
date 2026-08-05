package v150

import (
	"math"
	"testing"
)

func TestBuildPathsByRegime(t *testing.T) {
	cfg := FixedStrategyV150Config()
	ctx := validTestContext()
	candidate := validTestCandidate(ctx.AsOf)
	candidate.MA10 = 10
	candidate.MA20 = 9.8
	candidate.ATR14 = 0.4
	candidate.Resistance20 = 12
	candidate.TargetResistance = 14

	riskOn := BuildTradePlans(ctx, candidate, RegimeDecision{Regime: RegimeRiskOn, DailyCap: 2}, cfg)
	if len(riskOn) != 2 || !riskOn[0].Accepted || riskOn[0].Plan.Path != PathPullback || !riskOn[1].Accepted || riskOn[1].Plan.Path != PathBreakout {
		t.Fatalf("risk-on plans = %+v", riskOn)
	}
	pullback := riskOn[0].Plan
	assertNear(t, pullback.Support, 10)
	assertNear(t, pullback.EntryMin, 9.9)
	assertNear(t, pullback.EntryMax, 10.1)
	if pullback.EvaluationMinutes != 15 || pullback.ValidTradeDays != 3 || pullback.MaxHoldTradeDays != 10 {
		t.Fatalf("pullback policy = %+v", pullback)
	}
	breakout := riskOn[1].Plan
	if breakout.Trigger != 12 || breakout.MinimumVolumeRatio != 1.2 || breakout.NoActivationAfterMin != 14*60 {
		t.Fatalf("breakout policy = %+v", breakout)
	}

	neutral := BuildTradePlans(ctx, candidate, RegimeDecision{Regime: RegimeNeutral, DailyCap: 1, PullbackOnly: true}, cfg)
	if len(neutral) != 1 || neutral[0].Plan.Path != PathPullback {
		t.Fatalf("neutral plans = %+v", neutral)
	}
	riskOff := BuildTradePlans(ctx, candidate, RegimeDecision{Regime: RegimeRiskOff, NoTrade: true}, cfg)
	if len(riskOff) != 1 || riskOff[0].Accepted || riskOff[0].Reason != RejectPlanRiskOff {
		t.Fatalf("risk-off plans = %+v", riskOff)
	}
}

func TestBreakoutAtSixtyDayHighKeepsTwoRWithoutArtificialResistance(t *testing.T) {
	cfg := FixedStrategyV150Config()
	ctx := validTestContext()
	candidate := validTestCandidate(ctx.AsOf)
	candidate.ATR14 = 0.4
	candidate.Resistance20 = 12
	candidate.TargetResistance = 12

	result := buildBreakoutPlan(ctx, candidate, cfg)
	if !result.Accepted {
		t.Fatalf("new-high breakout was rejected: %+v", result)
	}
	if result.Plan.TargetResistance != 0 {
		t.Fatalf("trigger high was retained as overhead resistance: %+v", result.Plan)
	}
	assertNear(t, result.Plan.RewardRisk, cfg.TargetRiskMultiple)
	assertNear(t, result.Plan.Target, result.Plan.ReferenceEntry+cfg.TargetRiskMultiple*result.Plan.RiskPerShare)
}

func TestStopDistanceClampAndResistanceCap(t *testing.T) {
	cfg := FixedStrategyV150Config()
	base := TradePlan{Symbol: "600000.SH", Path: PathPullback, ATR14: 0.1, TargetResistance: 20}
	minimum := priceTradePlan(base, 10, 0.01, cfg)
	if !minimum.Accepted {
		t.Fatalf("minimum plan rejected: %+v", minimum)
	}
	assertNear(t, minimum.Plan.RiskPerShare, 0.3)
	assertNear(t, minimum.Plan.Stop, 9.7)
	assertNear(t, minimum.Plan.RewardRisk, 2)

	maximum := priceTradePlan(base, 10, 0.20, cfg)
	if !maximum.Accepted {
		t.Fatalf("maximum plan rejected: %+v", maximum)
	}
	assertNear(t, maximum.Plan.RiskPerShare, 0.6)
	assertNear(t, maximum.Plan.Stop, 9.4)

	// ATR stop is 3%; resistance caps reward to less than 1.5R.
	capped := base
	capped.ATR14 = 0.2
	capped.TargetResistance = 10.4
	rejected := priceTradePlan(capped, 10, 0, cfg)
	if rejected.Accepted || rejected.Reason != RejectPlanRewardRisk || !(rejected.Plan.RewardRisk < 1.5) {
		t.Fatalf("capped result = %+v", rejected)
	}

	// The exact 1.5R boundary remains eligible.
	risk := 0.3
	capped.TargetResistance = (10 + 1.5*risk) / cfg.ResistanceTargetMultiplier
	boundary := priceTradePlan(capped, 10, 0, cfg)
	if !boundary.Accepted {
		t.Fatalf("1.5R boundary rejected: %+v", boundary)
	}
	assertNear(t, boundary.Plan.RewardRisk, 1.5)
}

func TestRepricePlanUsesActualFill(t *testing.T) {
	cfg := FixedStrategyV150Config()
	candidate := validTestCandidate(validTestContext().AsOf)
	plan := TradePlan{Symbol: candidate.Symbol, Path: PathBreakout, ATR14: 0.4, TargetResistance: 14}
	result := RepriceTradePlan(plan, 12.25, candidate, cfg)
	if !result.Accepted || result.Plan.ReferenceEntry != 12.25 {
		t.Fatalf("repriced plan = %+v", result)
	}
	assertNear(t, result.Plan.Stop, 12.25-result.Plan.RiskPerShare)
}

func TestRoundLotSizing(t *testing.T) {
	cfg := FixedStrategyV150Config()
	got := SizeRoundLot(33, cfg.PortfolioCash, cfg)
	if got.Rejected || got.Quantity != 300 || got.Notional != 9_900 {
		t.Fatalf("size = %+v", got)
	}
	untradeable := SizeRoundLot(101, cfg.PortfolioCash, cfg)
	if !untradeable.Rejected || untradeable.Reason != RejectMinimumLot {
		t.Fatalf("untradeable = %+v", untradeable)
	}
}

func TestTransactionCostsAndSlippageScenarios(t *testing.T) {
	cfg := FixedStrategyV150Config()
	scenarios := cfg.SlippageScenarios()
	if len(scenarios) != 3 || scenarios[0] != (SlippageScenario{Name: "base", BPS: 10}) || scenarios[1].BPS != 20 || scenarios[2].BPS != 50 {
		t.Fatalf("scenarios = %+v", scenarios)
	}

	buy := CalculateTradeCost(SideBuy, MarketSH, 10, 1_000, scenarios[0], cfg)
	assertNear(t, buy.EffectivePrice, 10.01)
	assertNear(t, buy.Notional, 10_010)
	assertNear(t, buy.Commission, 5)
	assertNear(t, buy.TransferFee, 0.1001)
	assertNear(t, buy.StampDuty, 0)
	assertNear(t, buy.SlippageCost, 10)
	if buy.CashFlow >= 0 {
		t.Fatalf("buy cash flow = %f", buy.CashFlow)
	}

	sell := CalculateTradeCost(SideSell, MarketSH, 10, 1_000, scenarios[0], cfg)
	assertNear(t, sell.EffectivePrice, 9.99)
	assertNear(t, sell.Notional, 9_990)
	assertNear(t, sell.Commission, 5)
	assertNear(t, sell.TransferFee, 0.0999)
	assertNear(t, sell.StampDuty, 4.995)
	assertNear(t, sell.SlippageCost, 10)
	if sell.CashFlow <= 0 {
		t.Fatalf("sell cash flow = %f", sell.CashFlow)
	}

	bjSell := CalculateTradeCost(SideSell, MarketBJ, 10, 1_000, scenarios[0], cfg)
	if bjSell.TransferFee <= 0 || bjSell.StampDuty <= 0 {
		t.Fatalf("BJ sell must include transfer fee and stamp duty: %+v", bjSell)
	}
	szSell := CalculateTradeCost(SideSell, MarketSZ, 10, 1_000, scenarios[0], cfg)
	if szSell.TransferFee <= 0 || szSell.StampDuty <= 0 {
		t.Fatalf("SZ sell must include transfer fee and stamp duty: %+v", szSell)
	}
}

func TestResolveMarket(t *testing.T) {
	if ResolveMarket("600000.sh") != MarketSH || ResolveMarket("000001.SZ") != MarketSZ || ResolveMarket("830000.BJ") != MarketBJ || ResolveMarket("bad") != MarketUnknown {
		t.Fatal("market resolution changed")
	}
	bareCodes := map[string]Market{
		"600000": MarketSH,
		"510300": MarketSH,
		"000001": MarketSZ,
		"300750": MarketSZ,
		"830000": MarketBJ,
		"920001": MarketBJ,
	}
	for code, want := range bareCodes {
		if got := ResolveMarket(code); got != want {
			t.Fatalf("ResolveMarket(%q) = %q, want %q", code, got, want)
		}
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}
