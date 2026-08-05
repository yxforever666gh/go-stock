package v150

import (
	"testing"
	"time"
)

func TestFixedStrategyV150ConfigExactThresholds(t *testing.T) {
	cfg := FixedStrategyV150Config()
	if cfg.Version != "1.5.0" || cfg.Benchmark != "510300" {
		t.Fatalf("identity = %s/%s", cfg.Version, cfg.Benchmark)
	}
	if cfg.RiskOnDailyCap != 2 || cfg.NeutralDailyCap != 1 || cfg.RiskOffDailyCap != 0 {
		t.Fatalf("regime caps = %d/%d/%d", cfg.RiskOnDailyCap, cfg.NeutralDailyCap, cfg.RiskOffDailyCap)
	}
	if cfg.MinimumListingCalendarDays != 120 || cfg.MinimumAverageAmount20 != 100_000_000 || cfg.MinimumPrice != 3 {
		t.Fatal("eligibility thresholds changed")
	}
	if cfg.MaximumATRRatio != 0.06 || cfg.MaximumDayChange != 0.05 || cfg.MaximumAbsoluteGap != 0.04 || cfg.MaximumDistanceFromMA20ATR != 1.5 {
		t.Fatal("chase/risk thresholds changed")
	}
	if cfg.MaximumRealtimeQuoteLag != 5*time.Minute {
		t.Fatal("realtime quote freshness threshold changed")
	}
	if cfg.TrendRelativeWeight != 30 || cfg.SetupWeight != 25 || cfg.SectorWeight != 15 || cfg.EventWeight != 20 || cfg.LiquidityRiskWeight != 10 {
		t.Fatal("score weights changed")
	}
	if cfg.TrendComponentShare != 0.60 || cfg.RelativeComponentShare != 0.40 || cfg.RelativeStrengthLookbackTradeDays != 20 || cfg.RelativeStrengthFullScoreReturn != 0.10 {
		t.Fatal("trend/relative thresholds changed")
	}
	if cfg.EventFreshness != 48*time.Hour || cfg.ProductionScoreFloor != 70 || cfg.VerificationLimit != 18 {
		t.Fatal("score gates changed")
	}
	if cfg.PullbackZoneATR != 0.25 || cfg.PullbackRecoveryMinutes != 15 || cfg.BreakoutMinimumVolumeRatio != 1.2 || cfg.BreakoutActivationCutoffMin != 14*60 {
		t.Fatal("activation thresholds changed")
	}
	if cfg.ActivationValidTradeDays != 3 || cfg.MaximumHoldTradeDays != 10 || cfg.StopATRMultiple != 1.5 || cfg.MinimumStopRatio != 0.03 || cfg.MaximumStopRatio != 0.06 {
		t.Fatal("holding/stop thresholds changed")
	}
	if cfg.TargetRiskMultiple != 2 || cfg.MinimumAchievableRiskReward != 1.5 || cfg.ResistanceTargetMultiplier != 0.995 || cfg.TrailingActivationR != 1 || cfg.TrailingATRMultiple != 1.5 || cfg.TimeExitMinute != 14*60+45 {
		t.Fatal("target/trailing thresholds changed")
	}
	if cfg.PortfolioCash != 100_000 || cfg.TargetCashPerPosition != 10_000 || cfg.RoundLotSize != 100 || cfg.MaximumOpenPositions != 5 || cfg.MaximumSectorEntriesDay != 1 || cfg.StopCooldownTradeDays != 5 {
		t.Fatal("portfolio thresholds changed")
	}
	if cfg.CommissionRate != 0.0003 || cfg.MinimumCommission != 5 || cfg.SellStampDutyRate != 0.0005 || cfg.TransferFeeRate != 0.00001 || cfg.BaseSlippageBPS != 10 || cfg.StressSlippageBPS != [2]float64{20, 50} {
		t.Fatal("cost thresholds changed")
	}
}

func TestRunContextTimeline(t *testing.T) {
	base := strategyTestTime(9, 40)
	ctx := RunContext{
		StartedAt:              base.Add(-time.Minute),
		AsOf:                   base.Add(time.Minute),
		DataCutoffAt:           base,
		DecisionAt:             base.Add(time.Minute),
		GeneratedAt:            base.Add(time.Minute),
		ValidFromAt:            base.Add(5 * time.Minute),
		TradeDayIndex:          100,
		ValidFromTradeDayIndex: 100,
	}
	if !ctx.ValidTimeline() {
		t.Fatal("expected valid causal timeline")
	}
	ctx.DataCutoffAt = ctx.DecisionAt.Add(time.Second)
	if ctx.ValidTimeline() {
		t.Fatal("future cutoff must fail")
	}
}

func TestFixedStrategyV150ConfigHashIsStableAndSensitive(t *testing.T) {
	first := FixedStrategyV150ConfigHash()
	second := FixedStrategyV150Config().Hash()
	if first == "" || first != second || len(first) != 64 {
		t.Fatalf("unexpected fixed config hash: %q / %q", first, second)
	}
	mutated := FixedStrategyV150Config()
	mutated.RiskOnDailyCap++
	if mutated.Hash() == first {
		t.Fatal("config mutation did not change hash")
	}
}

func TestClassifyMarketRegime(t *testing.T) {
	cfg := FixedStrategyV150Config()
	tests := []struct {
		name     string
		input    BenchmarkSnapshot
		regime   MarketRegime
		cap      int
		pullback bool
		noTrade  bool
		warning  bool
	}{
		{"risk on", BenchmarkSnapshot{DataPresent: true, Close: 110, MA20: 105, MA60: 100, MA20FiveDaysAgo: 104}, RegimeRiskOn, 2, false, false, false},
		{"neutral equality", BenchmarkSnapshot{DataPresent: true, Close: 100, MA20: 99, MA60: 100, MA20FiveDaysAgo: 98}, RegimeNeutral, 1, true, false, false},
		{"neutral flat slope", BenchmarkSnapshot{DataPresent: true, Close: 110, MA20: 105, MA60: 100, MA20FiveDaysAgo: 105}, RegimeNeutral, 1, true, false, false},
		{"risk off", BenchmarkSnapshot{DataPresent: true, Close: 99, MA20: 101, MA60: 100, MA20FiveDaysAgo: 102}, RegimeRiskOff, 0, false, true, false},
		{"stale fails safe", BenchmarkSnapshot{DataPresent: true, Stale: true, Close: 110, MA20: 105, MA60: 100, MA20FiveDaysAgo: 104}, RegimeNeutral, 1, true, false, true},
		{"missing fails safe", BenchmarkSnapshot{}, RegimeNeutral, 1, true, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyMarketRegime(test.input, cfg)
			if got.Regime != test.regime || got.DailyCap != test.cap || got.PullbackOnly != test.pullback || got.NoTrade != test.noTrade || (got.Warning != "") != test.warning {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func strategyTestTime(hour, minute int) time.Time {
	return time.Date(2026, 8, 4, hour, minute, 0, 0, time.FixedZone("CST", 8*60*60))
}
