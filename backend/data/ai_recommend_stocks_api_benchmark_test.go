package data

import (
	"go-stock/backend/db"
	"go-stock/backend/models"
	"math"
	"testing"
	"time"
)

func TestNormalizeSSEBenchmarkStartOpenDay(t *testing.T) {
	loc := cnLocation()

	// 周末应顺延到下一个开盘日（通常是周一）。
	weekend := time.Date(2026, 3, 7, 10, 0, 0, 0, loc) // Saturday
	gotWeekend := normalizeSSEBenchmarkStartOpenDay(weekend)
	if gotWeekend.Weekday() == time.Saturday || gotWeekend.Weekday() == time.Sunday {
		t.Fatalf("weekend should shift to trading day, got %s", gotWeekend.Format("2006-01-02"))
	}

	// 开盘日应保持原日期。
	tradeDay := time.Date(2026, 3, 4, 15, 0, 0, 0, loc) // Wednesday
	gotTradeDay := normalizeSSEBenchmarkStartOpenDay(tradeDay)
	wantTradeDay := time.Date(2026, 3, 4, 0, 0, 0, 0, loc)
	if !gotTradeDay.Equal(wantTradeDay) {
		t.Fatalf("trade day should keep date, got %s want %s", gotTradeDay.Format("2006-01-02"), wantTradeDay.Format("2006-01-02"))
	}
}

func TestParseKLineDayInCN(t *testing.T) {
	if _, ok := parseKLineDayInCN("2026-03-04"); !ok {
		t.Fatalf("parse day-only kline failed")
	}
	if _, ok := parseKLineDayInCN("2026-03-04 15:00:00"); !ok {
		t.Fatalf("parse datetime kline failed")
	}
	if _, ok := parseKLineDayInCN("bad-day"); ok {
		t.Fatalf("invalid day should not parse")
	}
}

func TestSelectSSEBenchmarkOpenClose(t *testing.T) {
	loc := cnLocation()
	startDay := time.Date(2026, 2, 27, 0, 0, 0, 0, loc)
	kLines := []KLineData{
		{Day: "2026-03-03", Open: "4189.408", Close: "4122.676"},
		{Day: "2026-02-26", Open: "4151.068", Close: "4146.631"},
		{Day: "2026-02-27", Open: "4128.897", Close: "4162.882"},
		{Day: "2026-03-04", Open: "4087.632", Close: "4082.474"},
	}

	startOpen, endClose, ok := selectSSEBenchmarkOpenClose(kLines, startDay)
	if !ok {
		t.Fatalf("selectSSEBenchmarkOpenClose should succeed")
	}
	if startOpen != 4128.897 {
		t.Fatalf("unexpected start open: %.3f", startOpen)
	}
	if endClose != 4082.474 {
		t.Fatalf("unexpected end close: %.3f", endClose)
	}
}

func TestSelectSSEBenchmarkOpenCloseWindow(t *testing.T) {
	loc := cnLocation()
	startDay := time.Date(2026, 2, 27, 0, 0, 0, 0, loc)
	kLines := []KLineData{
		{Day: "2026-03-03", Open: "4189.408", Close: "4122.676"},
		{Day: "2026-02-26", Open: "4151.068", Close: "4146.631"},
		{Day: "2026-02-27", Open: "4128.897", Close: "4162.882"},
		{Day: "2026-03-04", Open: "4087.632", Close: "4082.474"},
	}

	startOpen, endClose, lastCloseDay, ok := selectSSEBenchmarkOpenCloseWindow(kLines, startDay)
	if !ok {
		t.Fatalf("selectSSEBenchmarkOpenCloseWindow should succeed")
	}
	if startOpen != 4128.897 {
		t.Fatalf("unexpected start open: %.3f", startOpen)
	}
	if endClose != 4082.474 {
		t.Fatalf("unexpected end close: %.3f", endClose)
	}
	wantLastCloseDay := time.Date(2026, 3, 4, 0, 0, 0, 0, loc)
	if !lastCloseDay.Equal(wantLastCloseDay) {
		t.Fatalf("unexpected last close day: %s", lastCloseDay.Format("2006-01-02"))
	}
}

func TestResolveSSEBenchmarkEndPriceFromCachedQuoteUsesFreshCache(t *testing.T) {
	loc := cnLocation()
	minDay := time.Date(2026, 4, 2, 0, 0, 0, 0, loc)
	quote := &StockInfo{
		Date:  "2026-04-02",
		Price: "3919.29",
	}
	got := resolveSSEBenchmarkEndPriceFromCachedQuote(3948.55, quote, minDay)
	if got != 3919.29 {
		t.Fatalf("expected cached price, got %.2f", got)
	}
}

func TestResolveSSEBenchmarkEndPriceFromCachedQuoteIgnoresStaleCache(t *testing.T) {
	loc := cnLocation()
	minDay := time.Date(2026, 4, 2, 0, 0, 0, 0, loc)
	quote := &StockInfo{
		Date:  "2026-04-01",
		Price: "3948.55",
	}
	got := resolveSSEBenchmarkEndPriceFromCachedQuote(3919.28, quote, minDay)
	if got != 3919.28 {
		t.Fatalf("expected fallback close price, got %.2f", got)
	}
}

func TestParseSSEBenchmarkQuoteDay(t *testing.T) {
	loc := cnLocation()
	tests := []struct {
		name      string
		dateText  string
		updatedAt time.Time
		want      time.Time
	}{
		{
			name:     "dash date",
			dateText: "2026-04-02",
			want:     time.Date(2026, 4, 2, 0, 0, 0, 0, loc),
		},
		{
			name:     "compact date",
			dateText: "20260402",
			want:     time.Date(2026, 4, 2, 0, 0, 0, 0, loc),
		},
		{
			name:      "fallback updatedAt",
			updatedAt: time.Date(2026, 4, 2, 15, 1, 2, 0, loc),
			want:      time.Date(2026, 4, 2, 0, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		got, ok := parseSSEBenchmarkQuoteDay(tt.dateText, tt.updatedAt)
		if !ok {
			t.Fatalf("%s should parse", tt.name)
		}
		if !got.Equal(tt.want) {
			t.Fatalf("%s unexpected day: %s", tt.name, got.Format("2006-01-02"))
		}
	}
}

func TestCalculateSSEBenchmarkRateByItems_Integration(t *testing.T) {
	requireIntegration(t)
	db.Init("../../data/stock.db")
	items := []models.AiRecommendStocksYieldItem{
		{
			StockCode:           "600111.SH",
			ActivationStatus:    "activated",
			BacktestEligibility: recommendBacktestEligible,
			ActivationTime:      "2026-03-24 09:30:00",
			BuyAmount:           46.00,
			CurrentPrice:        47.58,
		},
	}
	rate, text := calculateSSEBenchmarkRateByItems(items)
	if text == "--" {
		t.Fatalf("expected benchmark text, got --")
	}
	if rate == 0 {
		t.Fatalf("expected non-zero benchmark rate, got 0")
	}
}

func TestCalculateStrategySummaryByEntries_ProducesStrategyOnlyMetrics(t *testing.T) {
	loc := cnLocation()
	buyTime := time.Date(2026, 4, 14, 9, 35, 0, 0, loc)
	sellTime := time.Date(2026, 4, 18, 14, 55, 0, 0, loc)
	sellAmount := 11.2
	buyAmount := 10.0
	entries := []yieldDailyOverviewEntry{
		{
			RecommendID:      1,
			StockCode:        "000001.SZ",
			BuyTime:          buyTime,
			BuyDay:           time.Date(2026, 4, 14, 0, 0, 0, 0, loc),
			SellDay:          time.Date(2026, 4, 18, 0, 0, 0, 0, loc),
			CurrentDay:       time.Date(2026, 4, 18, 0, 0, 0, 0, loc),
			BuyAmount:        buyAmount,
			SellAmount:       sellAmount,
			HasSellAmount:    true,
			BuyCostNet:       round2(calcBuyTradeCost(buyAmount, resolveTradingMarket("000001.SZ")).NetAmount),
			RealizedValueNet: round2(calcSellTradeCost(buyAmount, sellAmount, resolveTradingMarket("000001.SZ")).NetAmount),
			SellTime:         sellTime.Format("2006-01-02 15:04:05"),
		},
	}

	summary := calculateStrategySummaryByEntries(entries)
	if summary.StrategyXirrText == "--" {
		t.Fatalf("expected strategy xirr text, got --")
	}
}

func TestCalculateCashflowMatchedBenchmark_UsesETFNetTradeCosts(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 4, 14, 0, 0, 0, 0, loc)
	day2 := time.Date(2027, 4, 14, 0, 0, 0, 0, loc)
	buyTime := time.Date(2026, 4, 14, 9, 35, 0, 0, loc)
	sellTime := time.Date(2027, 4, 14, 14, 55, 0, 0, loc)
	entry := yieldDailyOverviewEntry{
		RecommendID:      1,
		StockCode:        "000001.SZ",
		BuyTime:          buyTime,
		BuyDay:           day1,
		SellDay:          day2,
		CurrentDay:       day2,
		BuyAmount:        10,
		SellAmount:       11,
		HasSellAmount:    true,
		BuyCostNet:       round2(calcBuyTradeCost(10, resolveTradingMarket("000001.SZ")).NetAmount),
		RealizedValueNet: round2(calcSellTradeCost(10, 11, resolveTradingMarket("000001.SZ")).NetAmount),
		SellTime:         sellTime.Format("2006-01-02 15:04:05"),
	}
	benchmarkSeries := &yieldDailyOverviewPriceSeries{
		Code: defaultBenchmarkModelCode,
		CloseByDay: map[string]float64{
			"2026-04-14": 5.0,
			"2027-04-14": 5.5,
		},
	}

	series, itemRateMap, _, benchmarkXirr, _, _, _, _, ok := calculateCashflowMatchedBenchmark(
		[]yieldDailyOverviewEntry{entry},
		[]time.Time{day1, day2},
		benchmarkSeries,
	)

	if !ok {
		t.Fatal("expected benchmark calculation to succeed")
	}
	if series.Code != defaultBenchmarkModelCode {
		t.Fatalf("unexpected benchmark code: %s", series.Code)
	}
	if math.Abs(itemRateMap[1]-9.54) > 0.001 {
		t.Fatalf("expected ETF net benchmark rate 9.54, got %.4f", itemRateMap[1])
	}
	if math.Abs(series.ValueByDay["2026-04-14"]-2993.5) > 0.001 {
		t.Fatalf("expected day1 ETF liquidation value 2993.5, got %.4f", series.ValueByDay["2026-04-14"])
	}
	if math.Abs(series.CumulativeAmountByDay["2027-04-14"]-286.85) > 0.001 {
		t.Fatalf("expected day2 ETF net cumulative amount 286.85, got %.4f", series.CumulativeAmountByDay["2027-04-14"])
	}
	if benchmarkXirr == 0 {
		t.Fatal("expected non-zero benchmark XIRR")
	}
}

func TestCalculateStrategyMaxDrawdownByEntries_WithPriceSeries(t *testing.T) {
	loc := cnLocation()
	entries := []yieldDailyOverviewEntry{
		{
			RecommendID:      1,
			StockCode:        "000001.SZ",
			BuyTime:          time.Date(2026, 4, 14, 9, 35, 0, 0, loc),
			BuyDay:           time.Date(2026, 4, 14, 0, 0, 0, 0, loc),
			CurrentDay:       time.Date(2026, 4, 16, 0, 0, 0, 0, loc),
			BuyAmount:        10,
			CurrentPrice:     9.2,
			BuyCostNet:       round2(calcBuyTradeCost(10, resolveTradingMarket("000001.SZ")).NetAmount),
			CurrentPriceTime: "2026-04-16 15:00:00",
		},
	}
	tradingDays := []time.Time{
		time.Date(2026, 4, 14, 0, 0, 0, 0, loc),
		time.Date(2026, 4, 15, 0, 0, 0, 0, loc),
		time.Date(2026, 4, 16, 0, 0, 0, 0, loc),
	}
	priceSeriesMap := map[string]*yieldDailyOverviewPriceSeries{
		"000001.SZ": {
			Code: "000001.SZ",
			CloseByDay: map[string]float64{
				"2026-04-14": 10.0,
				"2026-04-15": 9.6,
				"2026-04-16": 9.2,
			},
		},
	}

	maxDrawdown := calculateMaxDrawdownByDailyRatesWithPriceSeries(entries, tradingDays, priceSeriesMap)
	if math.Abs(maxDrawdown) < 0.001 {
		t.Fatalf("expected non-zero max drawdown")
	}
}

func TestCalculateBenchmarkSummaryByItems_MissingStockPriceSeriesKeepsStrategyXirr(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{
		{
			RecommendID:         1,
			StockCode:           "BAD.CODE",
			BacktestEligibility: recommendBacktestEligible,
			ActivationStatus:    "activated",
			BuyTime:             "2026-04-14 09:35:00",
			ActivationTime:      "2026-04-14 09:35:00",
			BuyAmount:           10,
			CurrentPrice:        11.2,
			CurrentPriceTime:    "2026-04-18 14:55:00",
			YieldRate:           11.6,
			YieldRateText:       "+11.60%",
		},
	}

	result := calculateBenchmarkSummaryByItemsCore(items)
	if result.StrategyXirrText == "--" {
		t.Fatalf("expected strategy xirr text when benchmark fails")
	}
	if result.RateText == "--" {
		t.Fatalf("expected benchmark rate text to still be available")
	}
	if result.BenchmarkXirrText == "--" {
		t.Fatalf("expected benchmark xirr text to still be available")
	}
	if result.MaxDrawdownText != "--" {
		t.Fatalf("expected max drawdown text to remain -- when stock price series are unavailable, got %s", result.MaxDrawdownText)
	}
	if result.ExcessXirrText == "--" {
		t.Fatalf("expected excess xirr text to still be available")
	}
}
