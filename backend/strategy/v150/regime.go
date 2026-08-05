package v150

func ClassifyMarketRegime(snapshot BenchmarkSnapshot, cfg StrategyV150Config) RegimeDecision {
	if !snapshot.DataPresent || snapshot.Stale || snapshot.Close <= 0 || snapshot.MA20 <= 0 || snapshot.MA60 <= 0 || snapshot.MA20FiveDaysAgo <= 0 {
		warning := "benchmark_stale"
		if !snapshot.DataPresent || snapshot.Close <= 0 || snapshot.MA20 <= 0 || snapshot.MA60 <= 0 || snapshot.MA20FiveDaysAgo <= 0 {
			warning = "benchmark_data_missing"
		}
		return RegimeDecision{
			Regime:       RegimeNeutral,
			DailyCap:     cfg.NeutralDailyCap,
			PullbackOnly: true,
			Warning:      warning,
		}
	}

	if snapshot.Close > snapshot.MA20 && snapshot.MA20 > snapshot.MA60 && snapshot.MA20 > snapshot.MA20FiveDaysAgo {
		return RegimeDecision{Regime: RegimeRiskOn, DailyCap: cfg.RiskOnDailyCap}
	}
	if snapshot.Close >= snapshot.MA60 {
		return RegimeDecision{
			Regime:       RegimeNeutral,
			DailyCap:     cfg.NeutralDailyCap,
			PullbackOnly: true,
		}
	}
	return RegimeDecision{
		Regime:   RegimeRiskOff,
		DailyCap: cfg.RiskOffDailyCap,
		NoTrade:  true,
	}
}
