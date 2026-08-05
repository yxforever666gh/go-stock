package data

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"go-stock/backend/strategy/v150"
)

func TestMarketSummaryV150RelativeStrengthBoundaries(t *testing.T) {
	cfg := v150.FixedStrategyV150Config()
	asOf := time.Date(2026, 8, 4, 15, 5, 0, 0, cnLocation())
	benchmarkBars := marketSummaryV150ReturnFixture(asOf, cfg.RelativeStrengthLookbackTradeDays+1, 100, 110)
	benchmark, source := marketSummaryV150RelativeStrengthBenchmark(benchmarkBars, false)
	stockSource := MarketSummaryV150DailyDataSource{AdjustmentSource: "tencent_qfq", Complete: true}

	tests := []struct {
		name            string
		stockEnd        float64
		wantRelative    float64
		wantQuality     float64
		wantTrendSignal float64
		wantPoints      int
	}{
		{name: "ten percent outperformance earns full relative component", stockEnd: 121, wantRelative: 0.10, wantQuality: 1, wantTrendSignal: 1, wantPoints: 30},
		{name: "equal benchmark return earns no relative component", stockEnd: 110, wantRelative: 0, wantQuality: 0, wantTrendSignal: 0.60, wantPoints: 18},
		{name: "five percent outperformance earns half relative component", stockEnd: 115.5, wantRelative: 0.05, wantQuality: 0.50, wantTrendSignal: 0.80, wantPoints: 24},
		{name: "underperformance cannot create relative points", stockEnd: 100, wantRelative: 1/1.1 - 1, wantQuality: 0, wantTrendSignal: 0.60, wantPoints: 18},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := marketSummaryV150RelativeStrengthCandidate(asOf, "600000.SH")
			candidate.TrendQuality = 1
			stockBars := marketSummaryV150ReturnFixture(asOf, cfg.RelativeStrengthLookbackTradeDays+1, 100, test.stockEnd)
			got, warnings := applyMarketSummaryV150RelativeStrength(candidate, stockBars, asOf, stockSource, benchmark, source, benchmarkBars, cfg)
			if len(warnings) != 0 || !got.HasRelativeStrengthData {
				t.Fatalf("relative feature rejected: candidate=%+v warnings=%v", got, warnings)
			}
			assertMarketSummaryV150Float(t, "relative return", got.RelativeReturn20, test.wantRelative)
			assertMarketSummaryV150Float(t, "relative quality", got.RelativeStrengthQuality, test.wantQuality)
			assertMarketSummaryV150Float(t, "trend/relative signal", got.Signals.TrendRelativeStrength, test.wantTrendSignal)
			if got.RelativeStrengthStart != benchmark.Return20Start || got.RelativeStrengthEnd != benchmark.Return20End ||
				got.BenchmarkReturn20 != benchmark.Return20 {
				t.Fatalf("audit window not frozen exactly: candidate=%+v benchmark=%+v", got, benchmark)
			}
			if points := v150.ScoreCandidate(v150.RunContext{AsOf: asOf}, got, cfg).TrendRelative; points != test.wantPoints {
				t.Fatalf("trend/relative points=%d, want %d", points, test.wantPoints)
			}
		})
	}
}

func TestMarketSummaryV150RelativeStrengthFailsClosedWhenDatesDoNotAlign(t *testing.T) {
	cfg := v150.FixedStrategyV150Config()
	asOf := time.Date(2026, 8, 4, 15, 5, 0, 0, cnLocation())
	benchmarkBars := marketSummaryV150ReturnFixture(asOf, cfg.RelativeStrengthLookbackTradeDays+1, 100, 110)
	benchmark, source := marketSummaryV150RelativeStrengthBenchmark(benchmarkBars, false)
	stockBars := marketSummaryV150ReturnFixture(asOf, cfg.RelativeStrengthLookbackTradeDays+1, 100, 121)
	stockBars = append(stockBars[:10], stockBars[11:]...)
	candidate := marketSummaryV150RelativeStrengthCandidate(asOf, "600000.SH")
	candidate.TrendQuality = 1
	stockSource := MarketSummaryV150DailyDataSource{AdjustmentSource: "tencent_qfq", Complete: true}

	got, warnings := applyMarketSummaryV150RelativeStrength(candidate, stockBars, asOf, stockSource, benchmark, source, benchmarkBars, cfg)
	if got.HasRelativeStrengthData || got.Signals.TrendRelativeStrength != 0 ||
		!strings.Contains(strings.Join(warnings, ","), "stock_benchmark_window_not_aligned") {
		t.Fatalf("misaligned window did not fail closed: candidate=%+v warnings=%v", got, warnings)
	}
	eligibility := v150.EvaluateEligibility(
		v150.RunContext{AsOf: asOf},
		got,
		v150.RegimeDecision{Regime: v150.RegimeRiskOn, DailyCap: 2},
		cfg,
	)
	if eligibility.Eligible || !slices.Contains(eligibility.Reasons, v150.RejectMissingRelativeStrength) {
		t.Fatalf("missing relative feature remained eligible: %+v", eligibility)
	}
}

func TestMarketSummaryV150RelativeStrengthIsDeterministicAndRanksBeforeTieBreaks(t *testing.T) {
	cfg := v150.FixedStrategyV150Config()
	asOf := time.Date(2026, 8, 4, 15, 5, 0, 0, cnLocation())
	benchmarkBars := marketSummaryV150ReturnFixture(asOf, cfg.RelativeStrengthLookbackTradeDays+1, 100, 110)
	benchmark, benchmarkSource := marketSummaryV150RelativeStrengthBenchmark(benchmarkBars, true)
	stockSource := MarketSummaryV150DailyDataSource{AdjustmentSource: "tencent_qfq", Complete: true}

	strong := marketSummaryV150RelativeStrengthCandidate(asOf, "600002.SH")
	strong.TrendQuality = 1
	strong, strongWarnings := applyMarketSummaryV150RelativeStrength(
		strong, marketSummaryV150ReturnFixture(asOf, cfg.RelativeStrengthLookbackTradeDays+1, 100, 121), asOf,
		stockSource, benchmark, benchmarkSource, benchmarkBars, cfg,
	)
	weak := marketSummaryV150RelativeStrengthCandidate(asOf, "600001.SH")
	weak.TrendQuality = 1
	weak, weakWarnings := applyMarketSummaryV150RelativeStrength(
		weak, marketSummaryV150ReturnFixture(asOf, cfg.RelativeStrengthLookbackTradeDays+1, 100, 110), asOf,
		stockSource, benchmark, benchmarkSource, benchmarkBars, cfg,
	)
	if !slices.Contains(strongWarnings, "relative_strength_benchmark_stale_aligned_window") ||
		!slices.Contains(weakWarnings, "relative_strength_benchmark_stale_aligned_window") {
		t.Fatalf("stale-but-aligned benchmark warning missing: strong=%v weak=%v", strongWarnings, weakWarnings)
	}

	regime := v150.RegimeDecision{Regime: v150.RegimeNeutral, DailyCap: 1, PullbackOnly: true}
	ctx := v150.RunContext{AsOf: asOf}
	first := v150.RankCandidates(ctx, []v150.Candidate{weak, strong}, regime, cfg)
	second := v150.RankCandidates(ctx, []v150.Candidate{strong, weak}, regime, cfg)
	if len(first) != 2 || len(second) != 2 || first[0].Candidate.Symbol != strong.Symbol || second[0].Candidate.Symbol != strong.Symbol ||
		first[0].Score != second[0].Score || first[1].Score != second[1].Score {
		t.Fatalf("relative ranking depends on input order: first=%+v second=%+v", first, second)
	}

	startedAt := asOf.Add(-2 * time.Minute)
	sources := map[string]MarketSummaryV150SourceCandidate{
		strong.Symbol: {StockCode: strong.Symbol},
		weak.Symbol:   {StockCode: weak.Symbol},
	}
	runA, err := newMarketSummaryV150Run(startedAt, asOf, "post-close", benchmark, []v150.Candidate{weak, strong}, sources)
	if err != nil {
		t.Fatalf("build first deterministic run: %v", err)
	}
	runB, err := newMarketSummaryV150Run(startedAt, asOf, "post-close", benchmark, []v150.Candidate{strong, weak}, sources)
	if err != nil {
		t.Fatalf("build second deterministic run: %v", err)
	}
	if runA.DataHash != runB.DataHash {
		t.Fatalf("frozen relative-strength input hash depends on input order: %s != %s", runA.DataHash, runB.DataHash)
	}
	payload, err := json.Marshal(runA.Candidates[0])
	if err != nil {
		t.Fatalf("marshal frozen candidate: %v", err)
	}
	for _, field := range []string{"Return20", "BenchmarkReturn20", "RelativeReturn20", "RelativeStrengthQuality", "RelativeStrengthStart", "RelativeStrengthEnd"} {
		if !strings.Contains(string(payload), `"`+field+`"`) {
			t.Fatalf("frozen candidate payload omits %s: %s", field, payload)
		}
	}

	mutated := strong
	mutated.RelativeReturn20 += 0.001
	runMutated, err := newMarketSummaryV150Run(startedAt, asOf, "post-close", benchmark, []v150.Candidate{weak, mutated}, sources)
	if err != nil {
		t.Fatalf("build mutated run: %v", err)
	}
	if runMutated.DataHash == runA.DataHash {
		t.Fatal("auditable point-in-time relative return did not participate in the immutable data hash")
	}
}

func marketSummaryV150RelativeStrengthCandidate(asOf time.Time, symbol string) v150.Candidate {
	return v150.Candidate{
		Symbol: symbol, Name: symbol, Sector: "bank", Market: v150.MarketSH,
		ListedAt: asOf.AddDate(-2, 0, 0), HasDailyData: true, HasCurrentData: true,
		Price: 10, PreviousClose: 9.9, MA10: 9.9, MA20: 9.8, MA60: 9.5,
		ATR14: 0.2, AverageAmount20: 200_000_000, DayChangeRatio: 0.01, GapRatio: 0.01,
		Signals: v150.ScoreSignals{SetupQuality: 1, SectorStrength: 1, LiquidityRiskQuality: 1},
	}
}

func marketSummaryV150RelativeStrengthBenchmark(bars []dailyBar, stale bool) (v150.BenchmarkSnapshot, MarketSummaryV150BenchmarkSource) {
	start := bars[0]
	end := bars[len(bars)-1]
	return v150.BenchmarkSnapshot{
		Code: v150.BenchmarkCode, Close: end.Close, MA20: end.Close - 1, MA60: end.Close - 2, MA20FiveDaysAgo: end.Close - 1.5,
		Return20: end.Close/start.Close - 1, Return20Start: normalizeDailyTradeDate(start.TradeDate).Format(time.DateOnly),
		Return20End: normalizeDailyTradeDate(end.TradeDate).Format(time.DateOnly), HasReturn20Data: true, DataPresent: true, Stale: stale,
	}, MarketSummaryV150BenchmarkSource{AdjustmentSource: "tencent_qfq", Complete: true}
}

func marketSummaryV150ReturnFixture(lastDay time.Time, count int, startClose, endClose float64) []dailyBar {
	days := make([]time.Time, 0, count)
	for day := normalizeDailyTradeDate(lastDay); len(days) < count; day = day.AddDate(0, 0, -1) {
		if isCNOpenTradeDaySafe(day) {
			days = append(days, day)
		}
	}
	slices.Reverse(days)
	bars := make([]dailyBar, 0, len(days))
	for index, day := range days {
		fraction := float64(index) / float64(len(days)-1)
		closePrice := startClose + (endClose-startClose)*fraction
		bars = append(bars, dailyBar{TradeDate: day, Open: closePrice, High: closePrice, Low: closePrice, Close: closePrice})
	}
	return bars
}

func assertMarketSummaryV150Float(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("%s=%.15f, want %.15f", name, got, want)
	}
}
