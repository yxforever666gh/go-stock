package data

import (
	"math"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
)

func TestV150YieldDailyOverviewUsesRuleIDLedgerWithoutProjectionAndMatchedBenchmarkMinutes(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 7, 0, 0, 0, 0, loc)
	asOf := day.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	withProjection, fillA := appendV150YieldDailyOverviewFilledRule(
		t, "daily-with-projection", "000001.SZ", "bank", day.Add(10*time.Hour),
	)
	withProjection.projectionID = createV150YieldLedgerViewProjection(t, withProjection, "pending", "999.00", asOf)

	withoutProjection, fillB := appendV150YieldDailyOverviewFilledRule(
		t, "daily-without-projection", "000002.SZ", "software", day.Add(10*time.Hour),
	)

	// Deliberately make the qfq daily close unusable as an execution price. The
	// benchmark must buy at the exact cached fill minute and mark from the raw
	// final minute, never from this daily value.
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day, 99)
	seedV150BenchmarkMinuteBars(t, minuteBar{
		TradeTime: fillA.EventAt, Open: 4, High: 4, Low: 4, Close: 4,
	})
	// Use a non-flat, end-labeled final bucket. The open-position benchmark
	// endpoint must come from the validated raw daily series (5.00), not from a
	// label guess that could accidentally select the 14:59 minute.
	seedV150YieldDailyOverviewRawBucket(t, defaultBenchmarkModelCode, day, 4.5, 5, true, false)
	seedV150YieldDailyOverviewRawClose(t, withProjection.symbol, day, 15, false)
	seedV150YieldDailyOverviewRawClose(t, withoutProjection.symbol, day, 15, false)

	cfg := v150.FixedStrategyV150Config()
	mark := v150.CalculateTradeCost(v150.SideSell, v150.MarketSZ, 15, int(fillA.Quantity), cfg.SlippageScenarios()[0], cfg)
	markB := v150.CalculateTradeCost(v150.SideSell, v150.MarketSZ, 15, int(fillB.Quantity), cfg.SlippageScenarios()[0], cfg)
	wantCost := round2(fillA.Price*fillA.Quantity + fillA.Fees + fillB.Price*fillB.Quantity + fillB.Fees)
	wantEquity := round2(cfg.PortfolioCash + mark.CashFlow - (fillA.Price*fillA.Quantity + fillA.Fees) + markB.CashFlow - (fillB.Price*fillB.Quantity + fillB.Fees))
	entryCashA := round2(fillA.Price*fillA.Quantity + fillA.Fees)
	entryCashB := round2(fillB.Price*fillB.Quantity + fillB.Fees)
	benchmarkBuyA := calcBenchmarkETFBuyTradeForVersion(v150.StrategyVersion, entryCashA, 4)
	benchmarkBuyB := calcBenchmarkETFBuyTradeForVersion(v150.StrategyVersion, entryCashB, 4)
	benchmarkSellA := calcBenchmarkETFSellTradeForVersion(v150.StrategyVersion, benchmarkBuyA.Shares, 5)
	benchmarkSellB := calcBenchmarkETFSellTradeForVersion(v150.StrategyVersion, benchmarkBuyB.Shares, 5)
	wantBenchmarkEquity := round2(cfg.PortfolioCash +
		benchmarkSellA.NetAmount + benchmarkBuyA.UnusedCash - entryCashA +
		benchmarkSellB.NetAmount + benchmarkBuyB.UnusedCash - entryCashB)

	service := NewAiRecommendStocksService()
	var firstBenchmarkNav float64
	for index, cohort := range []string{marketSummaryVersion150, strategyCohortCurrent} {
		got, err := service.GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{StrategyCohort: cohort})
		if err != nil {
			t.Fatalf("cohort %q: %v", cohort, err)
		}
		if got.CalcMode != aiRecommendYieldModeStrict || got.StrategyCohort != marketSummaryVersion150 ||
			got.ValidationStatus != "forward_validation" || got.PortfolioCapital != cfg.PortfolioCash {
			t.Fatalf("cohort %q metadata=%+v", cohort, got)
		}
		if got.TotalRecordCount != 2 || got.IncludedRecordCount != 2 || got.SkippedRecordCount != 0 {
			t.Fatalf("cohort %q counts=%d/%d/%d, want 2/2/0", cohort, got.TotalRecordCount, got.IncludedRecordCount, got.SkippedRecordCount)
		}
		if len(got.Points) != 1 {
			t.Fatalf("cohort %q points=%d warnings=%v health=%v", cohort, len(got.Points), got.Warnings, got.V150HealthWarnings)
		}
		point := got.Points[0]
		if point.HoldingCount != 2 || math.Abs(point.CostBasisNet-wantCost) > 0.01 || math.Abs(point.PortfolioEquity-wantEquity) > 0.01 {
			t.Fatalf("cohort %q did not aggregate two RuleIDs from sealed fills: point=%+v wantCost=%.2f wantEquity=%.2f", cohort, point, wantCost, wantEquity)
		}
		if point.PortfolioEquity > cfg.PortfolioCash+100_000 ||
			math.Abs(point.BenchmarkNav-round4(wantBenchmarkEquity/cfg.PortfolioCash)) > 0.00001 {
			t.Fatalf("cohort %q mutable projection or missing benchmark contaminated point: %+v", cohort, point)
		}
		if point.BenchmarkClose != 5 {
			t.Fatalf("cohort %q used qfq daily close instead of raw closing minute: %+v", cohort, point)
		}
		if index == 0 {
			firstBenchmarkNav = point.BenchmarkNav
			update := db.Dao.Model(&models.AiRecommendDailyBar{}).
				Where("stock_code = ? AND trade_date = ?", defaultBenchmarkModelCode, normalizeDailyTradeDate(day)).
				Updates(map[string]any{"open": 199, "high": 199, "low": 199, "close": 199})
			if update.Error != nil || update.RowsAffected != 1 {
				t.Fatalf("rewrite qfq benchmark row: rows=%d err=%v", update.RowsAffected, update.Error)
			}
		} else if point.BenchmarkNav != firstBenchmarkNav {
			t.Fatalf("qfq daily rewrite changed matched benchmark NAV: first=%.4f second=%.4f", firstBenchmarkNav, point.BenchmarkNav)
		}
	}
}

func TestV150YieldDailyOverviewFailsClosedOnAnyRawMinuteGap(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 7, 0, 0, 0, 0, loc)
	asOf := day2.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	fixture, fill := appendV150YieldDailyOverviewFilledRule(t, "daily-minute-gap", "000001.SZ", "bank", day1.Add(10*time.Hour))
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day1, 4)
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day2, 4.1)
	seedV150BenchmarkMinuteBars(t, minuteBar{TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4})
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day1, 4, false)
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day2, 4.1, false)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day1, 10.2, false)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day2, 10.3, true)

	got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 {
		t.Fatalf("partial strategy curve was published despite a required raw-minute gap: %+v", got.Points)
	}
	want := fixture.symbol + ":" + v150YieldDailyRawMinutePriceHealthCode + ":" + day2.Format(time.DateOnly)
	if !v150YieldDailyOverviewContainsWarning(got.V150HealthWarnings, want) {
		t.Fatalf("raw-minute gap warning %q missing: %v", want, got.V150HealthWarnings)
	}
}

func TestV150YieldDailyOverviewFailsClosedOnAdjustedHoldingMinuteProvenance(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 7, 0, 0, 0, 0, loc)
	asOf := day.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	fixture, fill := appendV150YieldDailyOverviewFilledRule(t, "daily-adjusted-holding", "000001.SZ", "bank", day.Add(10*time.Hour))
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day, 4)
	seedV150BenchmarkMinuteBars(t, minuteBar{TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4})
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day, 4.1, false)
	adjusted := marketSummaryV150TestMinuteBucket(day.Add(14*time.Hour+45*time.Minute), 10.2, 10.2, 100, false)
	if _, err := upsertMinuteBarsToCache(fixture.symbol, adjusted, "akshare:sina:adjustment=qfq"); err != nil {
		t.Fatal(err)
	}

	got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 {
		t.Fatalf("adjusted holding minute entered V1.5 NAV: %+v", got.Points)
	}
	want := fixture.symbol + ":" + v150YieldDailyRawMinuteProvenanceHealthCode + ":" + day.Format(time.DateOnly)
	if !v150YieldDailyOverviewContainsWarning(got.V150HealthWarnings, want) {
		t.Fatalf("holding provenance warning %q missing: %v", want, got.V150HealthWarnings)
	}
}

func TestV150YieldDailyOverviewFailsClosedOnAnyBenchmarkGap(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 7, 0, 0, 0, 0, loc)
	asOf := day2.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	fixture, fill := appendV150YieldDailyOverviewFilledRule(t, "daily-benchmark-gap", "000001.SZ", "bank", day1.Add(10*time.Hour))
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day1, 4)
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day2, 4.1)
	seedV150BenchmarkMinuteBars(t, minuteBar{TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4})
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day1, 4, false)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day1, 10.2, false)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day2, 10.3, false)

	got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 {
		t.Fatalf("partial strategy curve was published despite a required benchmark gap: %+v", got.Points)
	}
	if !v150YieldDailyOverviewContainsWarning(got.V150HealthWarnings, v150YieldDailyRawBenchmarkPriceHealthCode) {
		t.Fatalf("benchmark gap warning missing: %v", got.V150HealthWarnings)
	}
}

func TestV150YieldDailyOverviewMissingMatchedBenchmarkMinuteDoesNotFallbackToDaily(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 7, 0, 0, 0, 0, loc)
	asOf := day.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	fixture, _ := appendV150YieldDailyOverviewFilledRule(t, "daily-benchmark-exact-missing", "000001.SZ", "bank", day.Add(10*time.Hour))
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day, 88)
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day, 4.2, false)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day, 10.2, false)

	got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 {
		t.Fatalf("daily close was used after the exact benchmark buy minute was missing: %+v", got.Points)
	}
	want := fixture.ruleID + ":" + v150BenchmarkBuyQuoteHealthCode
	if !v150YieldDailyOverviewContainsWarning(got.V150HealthWarnings, want) {
		t.Fatalf("exact benchmark minute warning %q missing: %v", want, got.V150HealthWarnings)
	}
}

func TestV150YieldDailyOverviewRejectsAdjustedMatchedBenchmarkMinute(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 7, 0, 0, 0, 0, loc)
	asOf := day.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	fixture, fill := appendV150YieldDailyOverviewFilledRule(t, "daily-adjusted-benchmark-fill", "000001.SZ", "bank", day.Add(10*time.Hour))
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day, 99)
	if _, err := upsertMinuteBarsToCache(defaultBenchmarkModelCode, []minuteBar{{
		TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4,
	}}, "akshare:sina:adjustment=qfq"); err != nil {
		t.Fatal(err)
	}
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day, 4.2, false)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day, 10.2, false)

	got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 {
		t.Fatalf("adjusted exact benchmark minute entered V1.5 comparison: %+v", got.Points)
	}
	want := fixture.ruleID + ":" + v150BenchmarkMinuteProvenanceHealthCode
	if !v150YieldDailyOverviewContainsWarning(got.V150HealthWarnings, want) {
		t.Fatalf("matched benchmark provenance warning %q missing: %v", want, got.V150HealthWarnings)
	}
}

func TestV150YieldDailyOverviewMissingMatchedBenchmarkExitMinuteDoesNotFallbackToDaily(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	asOf := day2.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	fixture, fill := appendV150YieldDailyOverviewFilledRule(
		t, "daily-benchmark-exit-exact-missing", "000001.SZ", "bank", day1.Add(10*time.Hour),
	)
	appendV150YieldLedgerViewExit(t, fixture, fill, 11)
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day1, 88)
	seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day2, 89)
	seedV150BenchmarkMinuteBars(t, minuteBar{TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4})
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day1, 4.1, false)
	seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day2, 4.2, false)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day1, 10.2, false)

	got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 {
		t.Fatalf("daily close was used after the exact benchmark exit minute was missing: %+v", got.Points)
	}
	want := fixture.ruleID + ":" + v150BenchmarkExitQuoteHealthCode
	if !v150YieldDailyOverviewContainsWarning(got.V150HealthWarnings, want) {
		t.Fatalf("exact benchmark exit warning %q missing: %v", want, got.V150HealthWarnings)
	}
}

func TestV150YieldDailyOverviewExtendsClosedPortfolioThroughLatestCompleteDay(t *testing.T) {
	initV150YieldLedgerViewTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)
	asOf := day3.Add(15*time.Hour + 5*time.Minute)
	restoreNow := setV150YieldDailyOverviewTestNow(asOf)
	t.Cleanup(restoreNow)

	fixture, fill := appendV150YieldDailyOverviewFilledRule(
		t, "daily-closed-extension", "000001.SZ", "bank", day1.Add(10*time.Hour),
	)
	exit := appendV150YieldLedgerViewExit(t, fixture, fill, 11)
	for index, day := range []time.Time{day1, day2, day3} {
		seedMarketSummaryV150DailyClose(t, defaultBenchmarkModelCode, day, 90+float64(index))
		seedV150YieldDailyOverviewRawClose(t, defaultBenchmarkModelCode, day, 4+float64(index)/10, false)
	}
	seedV150BenchmarkMinuteBars(t,
		minuteBar{TradeTime: fill.EventAt, Open: 4, High: 4, Low: 4, Close: 4},
		minuteBar{TradeTime: exit.EventAt, Open: 4.1, High: 4.1, Low: 4.1, Close: 4.1},
	)
	seedV150YieldDailyOverviewRawClose(t, fixture.symbol, day1, 10.5, false)

	got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&models.AiRecommendStocksQuery{
		StrategyCohort: marketSummaryVersion150,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 3 || got.RangeEnd != day3.Format(time.DateOnly) ||
		got.DataAsOf != day3.Add(15*time.Hour).Format(time.DateTime) {
		t.Fatalf("closed portfolio was not extended to the latest complete valuation day: range=%s..%s points=%+v warnings=%v health=%v", got.RangeStart, got.RangeEnd, got.Points, got.Warnings, got.V150HealthWarnings)
	}
	exitPoint := got.Points[1]
	tail := got.Points[2]
	if exitPoint.HoldingCount != 0 || tail.HoldingCount != 0 ||
		tail.PortfolioEquity != exitPoint.PortfolioEquity ||
		tail.CumulativeAmountChange != exitPoint.CumulativeAmountChange ||
		tail.StrategyNav != exitPoint.StrategyNav ||
		tail.BenchmarkNav != exitPoint.BenchmarkNav ||
		tail.DailyAmountChange != 0 || tail.BenchmarkDailyAmountChange != 0 {
		t.Fatalf("closed strategy/benchmark tail did not remain frozen: exit=%+v tail=%+v", exitPoint, tail)
	}
}

func TestV150YieldDailyOverviewRejectsPopulationFilters(t *testing.T) {
	tests := []struct {
		name  string
		query models.AiRecommendStocksQuery
	}{
		{name: "stock code", query: models.AiRecommendStocksQuery{StockCode: "000001"}},
		{name: "stock name", query: models.AiRecommendStocksQuery{StockName: "example"}},
		{name: "sector code", query: models.AiRecommendStocksQuery{BkCode: "BK001"}},
		{name: "sector name", query: models.AiRecommendStocksQuery{BkName: "bank"}},
		{name: "model", query: models.AiRecommendStocksQuery{ModelName: "model"}},
		{name: "start date", query: models.AiRecommendStocksQuery{StartDate: "2026-08-01"}},
		{name: "end date", query: models.AiRecommendStocksQuery{EndDate: "2026-08-07"}},
	}
	for _, test := range tests {
		for _, cohort := range []string{marketSummaryVersion150, strategyCohortCurrent} {
			t.Run(test.name+"/"+cohort, func(t *testing.T) {
				query := test.query
				query.StrategyCohort = cohort
				got, err := NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(&query)
				if err == nil || got != nil || !strings.Contains(err.Error(), "complete immutable cohort") {
					t.Fatalf("filter was not rejected before shrinking the fixed-capital cohort: got=%+v err=%v", got, err)
				}
			})
		}
	}
}

func appendV150YieldDailyOverviewFilledRule(
	t *testing.T,
	suffix, symbol, sector string,
	fillAt time.Time,
) (v150YieldLedgerViewFixture, models.OrderEvent) {
	t.Helper()
	request := appendV150DailyCohortReplayFixture(t, suffix, symbol, sector, fillAt)
	var fill models.OrderEvent
	if err := db.Dao.Model(&models.OrderEvent{}).
		Where("run_id = ? AND rule_id = ? AND event_type = ?", request.RunID, request.RuleID, string(v150.EventFill)).
		Take(&fill).Error; err != nil {
		t.Fatal(err)
	}
	day := normalizeYieldOverviewTradeDay(fillAt)
	return v150YieldLedgerViewFixture{
		runID: request.RunID, ruleID: request.RuleID, candidateID: "daily-cohort-candidate-" + suffix,
		symbol: normalizeRecommendStockCode(symbol), decisionAt: day.Add(9 * time.Hour),
		validFromAt: day.Add(9*time.Hour + 30*time.Minute), tradeDate: day.Format(time.DateOnly),
	}, fill
}

func setV150YieldDailyOverviewTestNow(now time.Time) func() {
	original := timeNow
	timeNow = func() time.Time { return now }
	return func() { timeNow = original }
}

func seedV150YieldDailyOverviewRawClose(t *testing.T, symbol string, day time.Time, close float64, omitLast bool) {
	t.Helper()
	seedV150YieldDailyOverviewRawBucket(t, symbol, day, close, close, false, omitLast)
}

func seedV150YieldDailyOverviewRawBucket(
	t *testing.T,
	symbol string,
	day time.Time,
	open, close float64,
	endLabeled, omitLast bool,
) {
	t.Helper()
	day = normalizeYieldOverviewTradeDay(day)
	rows := marketSummaryV150TestMinuteBucket(day.Add(14*time.Hour+45*time.Minute), open, close, 100, endLabeled)
	if omitLast {
		rows = rows[:len(rows)-1]
	}
	seedMarketSummaryV150Minutes(t, symbol, rows)
}

func v150YieldDailyOverviewContainsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
