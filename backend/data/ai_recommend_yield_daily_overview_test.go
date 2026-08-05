package data

import (
	"math"
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

func TestBuildYieldDailyOverviewBenchmarkSeriesDoesNotForwardFillStaleClose(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 7, 31, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day3 := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)

	days, series := buildYieldDailyOverviewBenchmarkSeries([]dailyBar{
		{TradeDate: day2, Close: 0},
		{TradeDate: day1, Close: 4.12},
	}, day1, day3)
	if len(days) != 1 || !days[0].Equal(day1) {
		t.Fatalf("only the observed benchmark close should define a trading day: %#v", days)
	}
	if series == nil || len(series.CloseByDay) != 1 || series.CloseByDay["2026-07-31"] != 4.12 {
		t.Fatalf("unexpected observed benchmark series: %#v", series)
	}
	if _, ok := series.CloseByDay["2026-08-03"]; ok {
		t.Fatal("zero close must not be filled from the previous trading day")
	}
	if _, ok := series.CloseByDay["2026-08-04"]; ok {
		t.Fatal("missing end date must not be synthesised from a stale close")
	}
}

func TestBuildYieldDailyOverviewPoints_SoldPositionStaysFrozen(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 2, 24, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 2, 25, 0, 0, 0, 0, loc)
	day3 := time.Date(2026, 2, 26, 0, 0, 0, 0, loc)

	entry := yieldDailyOverviewEntry{
		StockCode:        "000001.SZ",
		BuyDay:           day1,
		SellDay:          day2,
		BuyAmount:        10,
		SellAmount:       11,
		HasSellAmount:    true,
		BuyCostNet:       round2(calcBuyTradeCost(10, resolveTradingMarket("000001.SZ")).NetAmount),
		RealizedValueNet: round2(calcSellTradeCost(10, 11, resolveTradingMarket("000001.SZ")).NetAmount),
	}
	series := &yieldDailyOverviewPriceSeries{
		Code: "000001.SZ",
		CloseByDay: map[string]float64{
			"2026-02-24": 10,
			"2026-02-25": 12,
			"2026-02-26": 13,
		},
	}

	points := buildYieldDailyOverviewPoints(
		[]yieldDailyOverviewEntry{entry},
		[]time.Time{day1, day2, day3},
		map[string]*yieldDailyOverviewPriceSeries{"000001.SZ": series},
		nil,
	)
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}

	wantCum := round2(entry.RealizedValueNet - entry.BuyCostNet)
	if !floatAlmostEqual(points[1].CumulativeAmountChange, wantCum) {
		t.Fatalf("sell day cumulative mismatch: got %.2f want %.2f", points[1].CumulativeAmountChange, wantCum)
	}
	if !floatAlmostEqual(points[2].CumulativeAmountChange, wantCum) {
		t.Fatalf("post-sell cumulative mismatch: got %.2f want %.2f", points[2].CumulativeAmountChange, wantCum)
	}
	if !floatAlmostEqual(points[1].DailyHoldingCostNet, entry.BuyCostNet) {
		t.Fatalf("sell day daily holding cost mismatch: got %.2f want %.2f", points[1].DailyHoldingCostNet, entry.BuyCostNet)
	}
	if points[2].DailyHoldingCostNet != 0 {
		t.Fatalf("post-sell daily holding cost should be zero, got %.2f", points[2].DailyHoldingCostNet)
	}
	if points[1].HoldingCount != 0 || points[2].HoldingCount != 0 {
		t.Fatalf("sold position should not count as holding after sell day, got day2=%d day3=%d", points[1].HoldingCount, points[2].HoldingCount)
	}
}

func TestBuildYieldDailyOverviewPoints_NewPositionDoesNotCreateFakeProfit(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 2, 24, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 2, 25, 0, 0, 0, 0, loc)
	day3 := time.Date(2026, 2, 26, 0, 0, 0, 0, loc)

	entryA := yieldDailyOverviewEntry{
		StockCode:    "000001.SZ",
		BuyDay:       day1,
		CurrentDay:   day3,
		BuyAmount:    10,
		CurrentPrice: 11,
		BuyCostNet:   round2(calcBuyTradeCost(10, resolveTradingMarket("000001.SZ")).NetAmount),
	}
	entryB := yieldDailyOverviewEntry{
		StockCode:    "000002.SZ",
		BuyDay:       day2,
		CurrentDay:   day3,
		BuyAmount:    20,
		CurrentPrice: 21,
		BuyCostNet:   round2(calcBuyTradeCost(20, resolveTradingMarket("000002.SZ")).NetAmount),
	}
	priceSeriesMap := map[string]*yieldDailyOverviewPriceSeries{
		"000001.SZ": {
			Code: "000001.SZ",
			CloseByDay: map[string]float64{
				"2026-02-24": 10,
				"2026-02-25": 11,
				"2026-02-26": 11,
			},
		},
		"000002.SZ": {
			Code: "000002.SZ",
			CloseByDay: map[string]float64{
				"2026-02-25": 20,
				"2026-02-26": 21,
			},
		},
	}

	points := buildYieldDailyOverviewPoints(
		[]yieldDailyOverviewEntry{entryA, entryB},
		[]time.Time{day1, day2, day3},
		priceSeriesMap,
		nil,
	)
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}

	day1Net := round2(calcSellTradeCost(10, 10, resolveTradingMarket("000001.SZ")).NetAmount - entryA.BuyCostNet)
	day2ExpectedCum := round2(
		calcSellTradeCost(10, 11, resolveTradingMarket("000001.SZ")).NetAmount - entryA.BuyCostNet +
			calcSellTradeCost(20, 20, resolveTradingMarket("000002.SZ")).NetAmount - entryB.BuyCostNet,
	)
	day2ExpectedDaily := round2(day2ExpectedCum - day1Net)

	if !floatAlmostEqual(points[1].DailyAmountChange, day2ExpectedDaily) {
		t.Fatalf("day2 daily amount mismatch: got %.2f want %.2f", points[1].DailyAmountChange, day2ExpectedDaily)
	}
}

func TestBuildYieldDailyOverviewPoints_UsesCurrentPriceOnCurrentDay(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 4, 13, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 4, 14, 0, 0, 0, 0, loc)

	entry := yieldDailyOverviewEntry{
		StockCode:    "000001.SZ",
		BuyDay:       day1,
		CurrentDay:   day2,
		BuyAmount:    10,
		CurrentPrice: 12,
		BuyCostNet:   round2(calcBuyTradeCost(10, resolveTradingMarket("000001.SZ")).NetAmount),
	}
	series := &yieldDailyOverviewPriceSeries{
		Code: "000001.SZ",
		CloseByDay: map[string]float64{
			"2026-04-13": 10,
			"2026-04-14": 11,
		},
	}

	points := buildYieldDailyOverviewPoints(
		[]yieldDailyOverviewEntry{entry},
		[]time.Time{day1, day2},
		map[string]*yieldDailyOverviewPriceSeries{"000001.SZ": series},
		nil,
	)
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}

	wantCum := round2(calcSellTradeCost(10, 12, resolveTradingMarket("000001.SZ")).NetAmount - entry.BuyCostNet)
	if !floatAlmostEqual(points[1].CumulativeAmountChange, wantCum) {
		t.Fatalf("current-day cumulative mismatch: got %.2f want %.2f", points[1].CumulativeAmountChange, wantCum)
	}

	wantDay1Daily := round2(calcSellTradeCost(10, 10, resolveTradingMarket("000001.SZ")).NetAmount - entry.BuyCostNet)
	if !floatAlmostEqual(points[0].DailyAmountChange, wantDay1Daily) {
		t.Fatalf("first-day daily amount mismatch: got %.2f want %.2f", points[0].DailyAmountChange, wantDay1Daily)
	}
	wantDay1Rate := round2(wantDay1Daily / entry.BuyCostNet * 100)
	if !floatAlmostEqual(points[0].DailyYieldRate, wantDay1Rate) {
		t.Fatalf("first-day daily rate mismatch: got %.2f want %.2f", points[0].DailyYieldRate, wantDay1Rate)
	}
	if !floatAlmostEqual(points[0].DailyHoldingCostNet, entry.BuyCostNet) {
		t.Fatalf("first-day daily holding cost mismatch: got %.2f want %.2f", points[0].DailyHoldingCostNet, entry.BuyCostNet)
	}
}

func TestBuildYieldDailyOverviewPoints_DailyRateIgnoresSoldHistoryCost(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 2, 24, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 2, 25, 0, 0, 0, 0, loc)
	day3 := time.Date(2026, 2, 26, 0, 0, 0, 0, loc)

	soldEntry := yieldDailyOverviewEntry{
		StockCode:        "000001.SZ",
		BuyDay:           day1,
		SellDay:          day2,
		BuyAmount:        10,
		SellAmount:       11,
		HasSellAmount:    true,
		BuyCostNet:       round2(calcBuyTradeCost(10, resolveTradingMarket("000001.SZ")).NetAmount),
		RealizedValueNet: round2(calcSellTradeCost(10, 11, resolveTradingMarket("000001.SZ")).NetAmount),
	}
	activeEntry := yieldDailyOverviewEntry{
		StockCode:    "000002.SZ",
		BuyDay:       day1,
		CurrentDay:   day3,
		BuyAmount:    20,
		CurrentPrice: 21,
		BuyCostNet:   round2(calcBuyTradeCost(20, resolveTradingMarket("000002.SZ")).NetAmount),
	}

	priceSeriesMap := map[string]*yieldDailyOverviewPriceSeries{
		"000001.SZ": {
			Code: "000001.SZ",
			CloseByDay: map[string]float64{
				"2026-02-24": 10,
				"2026-02-25": 11,
				"2026-02-26": 11,
			},
		},
		"000002.SZ": {
			Code: "000002.SZ",
			CloseByDay: map[string]float64{
				"2026-02-24": 20,
				"2026-02-25": 20.4,
				"2026-02-26": 21,
			},
		},
	}

	points := buildYieldDailyOverviewPoints(
		[]yieldDailyOverviewEntry{soldEntry, activeEntry},
		[]time.Time{day1, day2, day3},
		priceSeriesMap,
		nil,
	)
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}

	day2Cum := round2(
		soldEntry.RealizedValueNet - soldEntry.BuyCostNet +
			calcSellTradeCost(20, 20.4, resolveTradingMarket("000002.SZ")).NetAmount - activeEntry.BuyCostNet,
	)
	day3Cum := round2(
		soldEntry.RealizedValueNet - soldEntry.BuyCostNet +
			calcSellTradeCost(20, 21, resolveTradingMarket("000002.SZ")).NetAmount - activeEntry.BuyCostNet,
	)
	wantDay3Daily := round2(day3Cum - day2Cum)
	wantDay3Rate := round2(wantDay3Daily / activeEntry.BuyCostNet * 100)

	if !floatAlmostEqual(points[2].DailyAmountChange, wantDay3Daily) {
		t.Fatalf("day3 daily amount mismatch: got %.2f want %.2f", points[2].DailyAmountChange, wantDay3Daily)
	}
	if !floatAlmostEqual(points[2].DailyHoldingCostNet, activeEntry.BuyCostNet) {
		t.Fatalf("day3 daily holding cost mismatch: got %.2f want %.2f", points[2].DailyHoldingCostNet, activeEntry.BuyCostNet)
	}
	if !floatAlmostEqual(points[2].DailyYieldRate, wantDay3Rate) {
		t.Fatalf("day3 daily rate mismatch: got %.2f want %.2f", points[2].DailyYieldRate, wantDay3Rate)
	}
}

func TestBuildYieldDailyOverviewPoints_V150UsesRealPortfolioEquity(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	fillAt := day.Add(10 * time.Hour)
	frozenAt := fillAt.Add(time.Second)
	cfg := v150.FixedStrategyV150Config()
	entryCost := v150.CalculateTradeCost(v150.SideBuy, v150.MarketSZ, 10, 900, cfg.SlippageScenarios()[0], cfg)
	fill := models.OrderEvent{
		EventID: "daily-v150-fill", RunID: "daily-v150-run", RuleID: "daily-v150-rule",
		StrategyVersion: v150.StrategyVersion, TradeDate: day.Format(time.DateOnly), Symbol: "000001.SZ",
		EventType: string(v150.EventFill), Sequence: 1, EventAt: fillAt,
		Price: entryCost.EffectivePrice, Quantity: 900,
		Fees:        entryCost.Commission + entryCost.TransferFee + entryCost.StampDuty,
		PayloadJSON: `{}`, FrozenAt: &frozenAt,
	}
	events := []models.OrderEvent{fill}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	entry := yieldDailyOverviewEntry{
		RecommendID:      1,
		SummaryVersion:   "1.5.0",
		StockCode:        "000001.SZ",
		BuyDay:           day,
		CurrentDay:       day,
		BuyAmount:        10,
		CurrentPrice:     11,
		CurrentPriceTime: day.Add(14*time.Hour + 30*time.Minute).Format(time.DateTime),
		BuyCostNet:       calcBuyTradeCostForVersion("1.5.0", 10, tradingMarketSZ).NetAmount,
	}
	series := &yieldDailyOverviewPriceSeries{
		Code: "000001.SZ",
		CloseByDay: map[string]float64{
			"2026-08-04": 11,
		},
	}

	points, warnings := buildYieldDailyOverviewPointsWithV150Ledgers(
		[]yieldDailyOverviewEntry{entry},
		[]time.Time{day},
		map[string]*yieldDailyOverviewPriceSeries{"000001.SZ": series},
		nil,
		map[uint]v150YieldDailyOrderLedger{1: {
			RunID: "daily-v150-run", RuleID: "daily-v150-rule", Symbol: "000001.SZ",
			ReportAsOf: day.Add(15 * time.Hour), Events: events,
		}},
	)
	if len(warnings) != 0 {
		t.Fatalf("unexpected V1.5 ledger warnings: %v", warnings)
	}
	if len(points) != 1 {
		t.Fatalf("expected one V1.5.0 point, got %d", len(points))
	}
	mark := v150.CalculateTradeCost(v150.SideSell, v150.MarketSZ, 11, 900, cfg.SlippageScenarios()[0], cfg)
	entryCash := entryCost.EffectivePrice*900 + fill.Fees
	wantPnL := round2(mark.CashFlow - entryCash)
	wantEquity := round2(100_000 + wantPnL)
	if !floatAlmostEqual(points[0].PortfolioEquity, wantEquity) {
		t.Fatalf("portfolio equity mismatch: got %.2f want %.2f", points[0].PortfolioEquity, wantEquity)
	}
	if math.Abs(points[0].StrategyNav-round4(wantEquity/100_000)) > 0.00001 {
		t.Fatalf("portfolio NAV mismatch: got %.4f want %.4f", points[0].StrategyNav, round4(wantEquity/100_000))
	}
}

func TestYieldDailyOverviewRejectsSilentPriceForwardFill(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	entry := yieldDailyOverviewEntry{
		StockCode:  "000001.SZ",
		BuyDay:     day1,
		BuyAmount:  10,
		BuyCostNet: calcBuyTradeCost(10, tradingMarketSZ).NetAmount,
	}
	series := &yieldDailyOverviewPriceSeries{
		Code: "000001.SZ",
		CloseByDay: map[string]float64{
			"2026-08-03": 10,
		},
	}
	if gaps := countYieldDailyOverviewPriceGaps(
		[]yieldDailyOverviewEntry{entry},
		[]time.Time{day1, day2},
		map[string]*yieldDailyOverviewPriceSeries{"000001.SZ": series},
	); gaps != 1 {
		t.Fatalf("expected one explicit price gap, got %d", gaps)
	}
	if value, holding, ok := resolveYieldDailyOverviewNetValue(entry, "2026-08-04", day2, series); ok || holding || value != 0 {
		t.Fatalf("missing day was silently valued: value=%.2f holding=%v ok=%v", value, holding, ok)
	}
}

func TestAppendV150RollingReturnWarning(t *testing.T) {
	points := make([]models.AiRecommendYieldDailyOverviewPoint, 21)
	for index := range points {
		points[index].PortfolioEquity = 100_000 - float64(index*100)
	}
	data := &models.AiRecommendYieldDailyOverviewData{
		StrategyCohort: "1.5.0",
		Points:         points,
		Warnings:       []string{},
	}
	appendV150RollingReturnWarning(data)
	if len(data.Warnings) != 1 || !strings.Contains(data.Warnings[0], "滚动负收益") {
		t.Fatalf("rolling loss warning missing: %#v", data.Warnings)
	}
	data.Points[20].PortfolioEquity = 101_000
	data.Warnings = nil
	appendV150RollingReturnWarning(data)
	if len(data.Warnings) != 0 {
		t.Fatalf("positive rolling return should not warn: %#v", data.Warnings)
	}
}

func floatAlmostEqual(left, right float64) bool {
	return math.Abs(left-right) < 0.01
}
