package data

import (
	"math"
	"testing"
	"time"
)

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
		soldEntry.RealizedValueNet-soldEntry.BuyCostNet+
			calcSellTradeCost(20, 20.4, resolveTradingMarket("000002.SZ")).NetAmount-activeEntry.BuyCostNet,
	)
	day3Cum := round2(
		soldEntry.RealizedValueNet-soldEntry.BuyCostNet+
			calcSellTradeCost(20, 21, resolveTradingMarket("000002.SZ")).NetAmount-activeEntry.BuyCostNet,
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

func floatAlmostEqual(left, right float64) bool {
	return math.Abs(left-right) < 0.01
}
