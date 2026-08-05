package data

import (
	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyV150YieldValuationAvailabilityRejectsMissingAndStaleMarks(t *testing.T) {
	loc := cnLocation()
	asOf := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)

	tests := []struct {
		name      string
		item      models.AiRecommendStocksYieldItem
		wantPrice float64
		wantText  string
		wantState string
	}{
		{
			name: "missing V1.5 mark is unavailable",
			item: models.AiRecommendStocksYieldItem{
				SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ", ActivationStatus: "activated",
				BuyAmount: 10, YieldRate: 99, YieldRateText: "+99.00%",
			},
			wantText:  "--",
			wantState: v150YieldValuationUnavailableStatus,
		},
		{
			name: "stale V1.5 mark is unavailable",
			item: models.AiRecommendStocksYieldItem{
				SummaryVersion: marketSummaryVersion150, StockCode: "000002.SZ", ActivationStatus: "activated",
				BuyAmount: 10, CurrentPrice: 11, CurrentPriceTime: "2026-08-05 09:30:00", YieldRate: 9, YieldRateText: "+9.00%",
			},
			wantText:  "--",
			wantState: v150YieldValuationUnavailableStatus,
		},
		{
			name: "fresh V1.5 mark remains usable",
			item: models.AiRecommendStocksYieldItem{
				SummaryVersion: marketSummaryVersion150, StockCode: "000003.SZ", ActivationStatus: "activated",
				BuyAmount: 10, CurrentPrice: 11, CurrentPriceTime: "2026-08-05 09:58:00", YieldRate: 9, YieldRateText: "+9.00%", DataStatus: "正常",
			},
			wantPrice: 11,
			wantText:  "+9.00%",
			wantState: "正常",
		},
		{
			name: "legacy mark keeps frozen fallback semantics",
			item: models.AiRecommendStocksYieldItem{
				SummaryVersion: "1.4.2", StockCode: "000004.SZ", ActivationStatus: "activated",
				BuyAmount: 10, CurrentPrice: 11, CurrentPriceTime: "2026-08-01 15:00:00", YieldRate: 9, YieldRateText: "+9.00%", DataStatus: "正常",
			},
			wantPrice: 11,
			wantText:  "+9.00%",
			wantState: "正常",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyV150YieldValuationAvailability(&tt.item, asOf)
			if tt.item.CurrentPrice != tt.wantPrice || tt.item.YieldRateText != tt.wantText || tt.item.DataStatus != tt.wantState {
				t.Fatalf("valuation state = price %.2f, yield %q, status %q", tt.item.CurrentPrice, tt.item.YieldRateText, tt.item.DataStatus)
			}
		})
	}
}

func TestCalculateYieldTotalByItemsV150DoesNotUseBuyPriceAsCurrentMark(t *testing.T) {
	loc := cnLocation()
	oldNow := timeNow
	timeNow = func() time.Time { return time.Date(2026, 8, 5, 10, 0, 0, 0, loc) }
	t.Cleanup(func() { timeNow = oldNow })

	v150Item := models.AiRecommendStocksYieldItem{
		SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ", ActivationStatus: "activated",
		BacktestEligibility: recommendBacktestEligible, BuyAmount: 10, CurrentPrice: 0,
	}
	if _, text := calculateYieldTotalByItems([]models.AiRecommendStocksYieldItem{v150Item}); text != "--" {
		t.Fatalf("missing V1.5 mark must be unavailable, got %q", text)
	}

	legacyItem := v150Item
	legacyItem.SummaryVersion = "1.4.2"
	if _, text := calculateYieldTotalByItems([]models.AiRecommendStocksYieldItem{legacyItem}); text == "--" {
		t.Fatal("legacy cohort must retain its frozen buy-price fallback")
	}
}

func TestCollectV150YieldValuationHealthWarnings(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{{
		RecommendID: 15, SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ",
		ActivationStatus: "activated", BuyAmount: 10, CurrentPrice: 0,
	}}
	warnings := collectV150YieldValuationHealthWarnings(items)
	if len(warnings) != 1 || warnings[0] != "000001.SZ:"+v150YieldValuationHealthCode {
		t.Fatalf("unexpected valuation warnings: %v", warnings)
	}
}

func TestBuildYieldRecordStateFromRecommendV150DoesNotRefreshOldPriceWithTimestampOnly(t *testing.T) {
	loc := cnLocation()
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	buyAt := now.AddDate(0, 0, -1)
	lastRecalc := now
	existing := &models.AiRecommendYieldRecordState{
		RecommendID: 15, StockCode: "000001.SZ", ActivationStatus: "activated",
		ActivationTime: &buyAt, ActivationPrice: 10, BuyTime: &buyAt, BuyAmount: 10,
		PositionStatus: "持有", CurrentPrice: 11, CurrentPriceTime: "2026-08-04 15:00:00",
		YieldRate: 9, YieldRateText: "+9.00%", LastRecalcAt: &lastRecalc,
	}
	rec := models.AiRecommendStocks{
		SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ",
		ExecutionState: recommendExecutionConditional, RecommendCategory: recommendExecutionConditional,
		ActivationRuleJSON: `{"version":"v3","mode":"any_of","paths":[{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":10,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":3}]}`,
	}
	rec.ID = 15
	state := buildYieldRecordStateFromRecommend(rec, existing, yieldBuildContext{
		Now: now, InTradingSession: true, LatestTradeDate: normalizeYieldOverviewTradeDay(now),
		CurrentPriceMap: map[string]float64{}, CurrentPriceTimeMap: map[string]string{"000001.SZ": now.Format(time.DateTime)},
	})
	if state.CurrentPrice != 0 || state.YieldRateText != "--" || state.DataStatus != v150YieldValuationUnavailableStatus {
		t.Fatalf("stale persisted record state survived timestamp-only refresh: %+v", state)
	}
	item := mapRecommendRecordStateToYieldItem(rec, state)
	if yieldCurrentPriceDisplay(item) != "--" || item.YieldRateText != "--" {
		t.Fatalf("export/display leaked unavailable valuation: price=%q yield=%q", yieldCurrentPriceDisplay(item), item.YieldRateText)
	}
}

func TestCurrentPriceSnapshotsKeepPriceAndTimeAtomic(t *testing.T) {
	prices := map[string]float64{"000001.SZ": 10}
	times := map[string]string{"000001.SZ": "2026-08-04 15:00:00"}
	if storeCurrentPriceSnapshot(prices, times, "000001.SZ", "bad", "2026-08-05", "10:00:00") {
		t.Fatal("invalid price must not publish its timestamp")
	}
	if prices["000001.SZ"] != 10 || times["000001.SZ"] != "2026-08-04 15:00:00" {
		t.Fatalf("invalid quote split the atomic snapshot: prices=%v times=%v", prices, times)
	}

	item := models.AiRecommendStocksYieldItem{StockCode: "000001.SZ", CurrentPrice: 10, CurrentPriceTime: "2026-08-04 15:00:00"}
	items := []models.AiRecommendStocksYieldItem{item}
	applyLatestCurrentPriceSnapshot(items, map[string]float64{}, map[string]string{"000001.SZ": "2026-08-05 10:00:00"})
	if items[0].CurrentPrice != 10 || items[0].CurrentPriceTime != "2026-08-04 15:00:00" {
		t.Fatalf("timestamp-only snapshot refreshed an old price: %+v", items[0])
	}
}

func TestCalculateCashflowMatchedBenchmarkV150UsesMatchedMinutePrices(t *testing.T) {
	initV150YieldBenchmarkTestDB(t)
	loc := cnLocation()
	buyAt := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	sellAt := time.Date(2026, 8, 4, 14, 45, 0, 0, loc)
	seedV150BenchmarkMinuteBars(t,
		minuteBar{TradeTime: buyAt, Open: 4.1, High: 4.2, Low: 4.1, Close: 4.2},
		minuteBar{TradeTime: sellAt, Open: 4.3, High: 4.4, Low: 4.3, Close: 4.4},
	)

	buyCost := calcBuyTradeCostForVersion(marketSummaryVersion150, 10, tradingMarketSZ).NetAmount
	entry := yieldDailyOverviewEntry{
		RecommendID: 150, SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ",
		BuyTime: buyAt, BuyDay: normalizeYieldOverviewTradeDay(buyAt), SellDay: normalizeYieldOverviewTradeDay(sellAt),
		CurrentDay: normalizeYieldOverviewTradeDay(sellAt), BuyAmount: 10, SellAmount: 11, HasSellAmount: true,
		BuyCostNet: buyCost, RealizedValueNet: calcSellTradeCostForVersion(marketSummaryVersion150, 10, 11, tradingMarketSZ).NetAmount,
		SellTime: sellAt.Format(time.DateTime),
	}
	daily := &yieldDailyOverviewPriceSeries{Code: defaultBenchmarkModelCode, CloseByDay: map[string]float64{
		buyAt.Format(time.DateOnly): 9.0, sellAt.Format(time.DateOnly): 12.0,
	}}
	warnings := make([]string, 0)
	_, rates, _, _, _, _, _, comparableCount, ok := calculateCashflowMatchedBenchmark(
		[]yieldDailyOverviewEntry{entry},
		[]time.Time{normalizeYieldOverviewTradeDay(buyAt), normalizeYieldOverviewTradeDay(sellAt)},
		daily,
		&warnings,
	)
	if !ok || comparableCount != 1 || len(warnings) != 0 {
		t.Fatalf("matched V1.5 benchmark should be comparable: ok=%v count=%d warnings=%v", ok, comparableCount, warnings)
	}
	buy := calcBenchmarkETFBuyTradeForVersion(marketSummaryVersion150, buyCost, 4.1)
	sell := calcBenchmarkETFSellTradeForVersion(marketSummaryVersion150, buy.Shares, 4.3)
	want := round2((sell.NetAmount + buy.UnusedCash - buyCost) / buyCost * 100)
	if rates[entry.RecommendID] != want {
		t.Fatalf("benchmark rate %.2f, want exact-minute rate %.2f (daily closes must not be used)", rates[entry.RecommendID], want)
	}
}

func TestCalculateCashflowMatchedBenchmarkV150MissingMinuteDoesNotFallbackToDaily(t *testing.T) {
	initV150YieldBenchmarkTestDB(t)
	loc := cnLocation()
	buyAt := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	sellAt := time.Date(2026, 8, 4, 14, 45, 0, 0, loc)
	seedV150BenchmarkMinuteBars(t, minuteBar{TradeTime: buyAt, Open: 4, High: 4, Low: 4, Close: 4})

	entry := yieldDailyOverviewEntry{
		RecommendID: 151, SummaryVersion: marketSummaryVersion150, StockCode: "000002.SZ",
		BuyTime: buyAt, BuyDay: normalizeYieldOverviewTradeDay(buyAt), SellDay: normalizeYieldOverviewTradeDay(sellAt),
		CurrentDay: normalizeYieldOverviewTradeDay(sellAt), BuyAmount: 10, SellAmount: 11, HasSellAmount: true,
		BuyCostNet:       calcBuyTradeCostForVersion(marketSummaryVersion150, 10, tradingMarketSZ).NetAmount,
		RealizedValueNet: calcSellTradeCostForVersion(marketSummaryVersion150, 10, 11, tradingMarketSZ).NetAmount,
		SellTime:         sellAt.Format(time.DateTime),
	}
	daily := &yieldDailyOverviewPriceSeries{Code: defaultBenchmarkModelCode, CloseByDay: map[string]float64{
		buyAt.Format(time.DateOnly): 4, sellAt.Format(time.DateOnly): 4.4,
	}}
	warnings := make([]string, 0)
	_, rates, _, _, _, _, _, comparableCount, ok := calculateCashflowMatchedBenchmark(
		[]yieldDailyOverviewEntry{entry},
		[]time.Time{normalizeYieldOverviewTradeDay(buyAt), normalizeYieldOverviewTradeDay(sellAt)},
		daily,
		&warnings,
	)
	if ok || comparableCount != 0 || len(rates) != 0 {
		t.Fatalf("missing matched quote must be excluded: ok=%v count=%d rates=%v", ok, comparableCount, rates)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], v150BenchmarkExitQuoteHealthCode) {
		t.Fatalf("missing exit quote warning not emitted: %v", warnings)
	}

	legacy := entry
	legacy.SummaryVersion = "1.4.2"
	warnings = nil
	_, legacyRates, _, _, _, _, _, legacyComparable, legacyOK := calculateCashflowMatchedBenchmark(
		[]yieldDailyOverviewEntry{legacy},
		[]time.Time{normalizeYieldOverviewTradeDay(buyAt), normalizeYieldOverviewTradeDay(sellAt)},
		daily,
		&warnings,
	)
	if !legacyOK || legacyComparable != 1 || len(legacyRates) != 1 || len(warnings) != 0 {
		t.Fatalf("legacy daily-close convention changed: ok=%v count=%d rates=%v warnings=%v", legacyOK, legacyComparable, legacyRates, warnings)
	}
}

func TestResolveV150BenchmarkMinutePriceAtSupportsEndLabeledBars(t *testing.T) {
	initV150YieldBenchmarkTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	fillAt := day.Add(10 * time.Hour)
	seedV150BenchmarkMinuteBars(t,
		// The absence of 09:30 and presence of 09:31 identify end labels.
		minuteBar{TradeTime: day.Add(9*time.Hour + 31*time.Minute), Open: 3.9, High: 4, Low: 3.9, Close: 4},
		minuteBar{TradeTime: fillAt.Add(time.Minute), Open: 4.25, High: 4.3, Low: 4.2, Close: 4.28},
	)

	price, ok := resolveV150BenchmarkMinutePriceAt(fillAt, true)
	if !ok || price != 4.25 {
		t.Fatalf("end-labelled fill price = %.2f, ok=%v; want 10:01-labelled bar open 4.25", price, ok)
	}
}

func TestCalculateCashflowMatchedBenchmarkV150NewPositionDoesNotJumpNAV(t *testing.T) {
	initV150YieldBenchmarkTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)
	buy1At := day1.Add(10 * time.Hour)
	buy2At := day2.Add(10 * time.Hour)
	sell1At := day3.Add(14*time.Hour + 44*time.Minute)
	sell2At := day3.Add(14*time.Hour + 45*time.Minute)
	seedV150BenchmarkMinuteBars(t,
		minuteBar{TradeTime: buy1At, Open: 4, High: 4, Low: 4, Close: 4},
		minuteBar{TradeTime: buy2At, Open: 4, High: 4, Low: 4, Close: 4},
		minuteBar{TradeTime: sell1At, Open: 4, High: 4, Low: 4, Close: 4},
		minuteBar{TradeTime: sell2At, Open: 4, High: 4, Low: 4, Close: 4},
	)

	newEntry := func(id uint, buyAt, sellAt time.Time) yieldDailyOverviewEntry {
		buyCost := calcBuyTradeCostForVersion(marketSummaryVersion150, 10, tradingMarketSZ).NetAmount
		return yieldDailyOverviewEntry{
			RecommendID: id, SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ",
			BuyTime: buyAt, BuyDay: normalizeYieldOverviewTradeDay(buyAt),
			SellDay: normalizeYieldOverviewTradeDay(sellAt), CurrentDay: normalizeYieldOverviewTradeDay(sellAt),
			BuyAmount: 10, SellAmount: 10, HasSellAmount: true, BuyCostNet: buyCost,
			RealizedValueNet: calcSellTradeCostForVersion(marketSummaryVersion150, 10, 10, tradingMarketSZ).NetAmount,
			SellTime:         sellAt.Format(time.DateTime),
		}
	}
	entries := []yieldDailyOverviewEntry{newEntry(201, buy1At, sell1At), newEntry(202, buy2At, sell2At)}
	daily := &yieldDailyOverviewPriceSeries{Code: defaultBenchmarkModelCode, CloseByDay: map[string]float64{
		day1.Format(time.DateOnly): 4, day2.Format(time.DateOnly): 4, day3.Format(time.DateOnly): 4,
	}}
	series, _, _, _, _, _, _, comparableCount, ok := calculateCashflowMatchedBenchmark(
		entries, []time.Time{day1, day2, day3}, daily,
	)
	if !ok || comparableCount != 2 {
		t.Fatalf("V1.5 benchmark series unavailable: ok=%v comparable=%d", ok, comparableCount)
	}
	buyCost := entries[0].BuyCostNet
	benchmarkBuy := calcBenchmarkETFBuyTradeForVersion(marketSummaryVersion150, buyCost, 4)
	benchmarkMark := calcBenchmarkETFSellTradeForVersion(marketSummaryVersion150, benchmarkBuy.Shares, 4)
	positionPnL := round2(benchmarkMark.NetAmount + benchmarkBuy.UnusedCash - buyCost)
	wantDay1 := round2(100_000 + positionPnL)
	wantDay2 := round2(100_000 + 2*positionPnL)
	if got := series.ValueByDay[day1.Format(time.DateOnly)]; got != wantDay1 {
		t.Fatalf("day-one equity %.2f, want %.2f", got, wantDay1)
	}
	if got := series.ValueByDay[day2.Format(time.DateOnly)]; got != wantDay2 {
		t.Fatalf("new principal changed equity %.2f, want only PnL %.2f", got, wantDay2)
	}
	if got := series.DailyAmountByDay[day2.Format(time.DateOnly)]; got != positionPnL {
		t.Fatalf("day-two PnL %.2f, want new position mark PnL %.2f", got, positionPnL)
	}
	if got := series.NavByDay[day2.Format(time.DateOnly)]; got != round4(wantDay2/100_000) {
		t.Fatalf("day-two NAV %.4f, want %.4f", got, round4(wantDay2/100_000))
	}
}

func TestCalculateBenchmarkSummaryByItemsCoreV150UsesReusablePortfolioCapital(t *testing.T) {
	initV150YieldBenchmarkTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)
	buy1At := day1.Add(10 * time.Hour)
	sell1At := day2.Add(10*time.Hour + 30*time.Minute)
	buy2At := day2.Add(13 * time.Hour)
	sell2At := day3.Add(14*time.Hour + 45*time.Minute)
	seedV150BenchmarkDailyBars(t, day1, day2, day3)
	seedV150BenchmarkMinuteBars(t,
		minuteBar{TradeTime: buy1At, Open: 4.00, High: 4.00, Low: 4.00, Close: 4.00},
		minuteBar{TradeTime: sell1At, Open: 4.20, High: 4.20, Low: 4.20, Close: 4.20},
		minuteBar{TradeTime: buy2At, Open: 4.20, High: 4.20, Low: 4.20, Close: 4.20},
		minuteBar{TradeTime: sell2At, Open: 4.40, High: 4.40, Low: 4.40, Close: 4.40},
	)
	items := []models.AiRecommendStocksYieldItem{
		v150ClosedBenchmarkItem(501, buy1At, sell1At, 9_000, 450),
		v150ClosedBenchmarkItem(502, buy2At, sell2At, 9_500, 475),
	}
	firstBuy := calcBenchmarkETFBuyTradeForVersion(marketSummaryVersion150, items[0].V150LedgerEntryCash, 4.00)
	firstSell := calcBenchmarkETFSellTradeForVersion(marketSummaryVersion150, firstBuy.Shares, 4.20)
	secondBuy := calcBenchmarkETFBuyTradeForVersion(marketSummaryVersion150, items[1].V150LedgerEntryCash, 4.20)
	secondSell := calcBenchmarkETFSellTradeForVersion(marketSummaryVersion150, secondBuy.Shares, 4.40)
	wantBenchmarkPnL := round2(firstSell.NetAmount+firstBuy.UnusedCash-items[0].V150LedgerEntryCash) +
		round2(secondSell.NetAmount+secondBuy.UnusedCash-items[1].V150LedgerEntryCash)
	wantRate := round2(wantBenchmarkPnL / v150.FixedStrategyV150Config().PortfolioCash * 100)

	result := calculateBenchmarkSummaryByItemsCore(items)
	if result.Rate != wantRate || result.RateText != formatSignedPercent(wantRate) {
		t.Fatalf("V1.5 sequential benchmark rate=%v/%q want fixed-capital %v", result.Rate, result.RateText, wantRate)
	}
	if len(result.ItemRateByRecommendID) != 2 || result.ExcessYieldRateText == "--" {
		t.Fatalf("complete V1.5 benchmark lost item/excess metrics: %+v", result)
	}
}

func TestCalculateBenchmarkSummaryByItemsCoreV150RejectsPartialPortfolio(t *testing.T) {
	initV150YieldBenchmarkTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)
	buy1At := day1.Add(10 * time.Hour)
	sell1At := day2.Add(10*time.Hour + 30*time.Minute)
	buy2At := day2.Add(13 * time.Hour)
	sell2At := day3.Add(14*time.Hour + 45*time.Minute)
	seedV150BenchmarkDailyBars(t, day1, day2, day3)
	seedV150BenchmarkMinuteBars(t,
		minuteBar{TradeTime: buy1At, Open: 4.00, High: 4.00, Low: 4.00, Close: 4.00},
		minuteBar{TradeTime: sell1At, Open: 4.20, High: 4.20, Low: 4.20, Close: 4.20},
		minuteBar{TradeTime: buy2At, Open: 4.20, High: 4.20, Low: 4.20, Close: 4.20},
		// The second exit quote is intentionally absent. Daily close must not fill it.
	)
	items := []models.AiRecommendStocksYieldItem{
		v150ClosedBenchmarkItem(511, buy1At, sell1At, 9_000, 450),
		v150ClosedBenchmarkItem(512, buy2At, sell2At, 9_500, 475),
	}
	result := calculateBenchmarkSummaryByItemsCore(items)
	if result.RateText != "--" || result.ExcessYieldRateText != "--" || result.BenchmarkXirrText != "--" || len(result.ItemRateByRecommendID) != 0 {
		t.Fatalf("partial V1.5 portfolio published benchmark/excess metrics: %+v", result)
	}
	joined := strings.Join(result.Warnings, "|")
	if !strings.Contains(joined, v150BenchmarkExitQuoteHealthCode) || !strings.Contains(joined, v150BenchmarkPartialHealthCode) {
		t.Fatalf("partial V1.5 benchmark warnings=%v", result.Warnings)
	}
}

func seedV150BenchmarkDailyBars(t *testing.T, days ...time.Time) {
	t.Helper()
	if err := db.Dao.AutoMigrate(&models.AiRecommendDailyBar{}); err != nil {
		t.Fatalf("migrate V1.5 benchmark daily bars: %v", err)
	}
	bars := make([]dailyBar, 0, len(days))
	for index, day := range days {
		price := 4.0 + float64(index)*0.2
		bars = append(bars, dailyBar{TradeDate: day, Open: price, High: price, Low: price, Close: price})
	}
	if _, err := upsertDailyBarsToCache(defaultBenchmarkModelCode, bars, "v150-yield-test"); err != nil {
		t.Fatalf("seed V1.5 benchmark daily bars: %v", err)
	}
}

func v150ClosedBenchmarkItem(id uint, buyAt, sellAt time.Time, entryCash, netPnL float64) models.AiRecommendStocksYieldItem {
	sellDisplay := 10.5
	rate := round2(netPnL / entryCash * 100)
	return models.AiRecommendStocksYieldItem{
		RecommendID: id, SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ",
		BacktestEligibility: recommendBacktestEligible, ActivationStatus: "activated",
		BuyTime: buyAt.Format(time.DateTime), ActivationTime: buyAt.Format(time.DateTime), BuyAmount: 10,
		SellTime: sellAt.Format(time.DateTime), SellAmount: &sellDisplay, CurrentPrice: sellDisplay,
		CurrentPriceTime: sellAt.Format(time.DateTime), YieldRate: rate, YieldRateText: formatSignedPercent(rate),
		V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: entryCash,
		V150LedgerNetValue: entryCash + netPnL, V150LedgerNetPnL: netPnL, V150LedgerQuantity: 900,
	}
}

func TestV150StaleCurrentMarkDoesNotRewriteHistoricalPortfolioPoints(t *testing.T) {
	loc := cnLocation()
	asOf := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)
	base := models.AiRecommendStocksYieldItem{
		RecommendID: 301, SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ",
		ActivationStatus: "activated", BacktestEligibility: recommendBacktestEligible,
		BuyTime: day1.Add(10 * time.Hour).Format(time.DateTime), BuyAmount: 10,
	}
	fresh := base
	fresh.CurrentPrice = 12
	fresh.CurrentPriceTime = day3.Add(9*time.Hour + 58*time.Minute).Format(time.DateTime)
	applyV150YieldValuationAvailability(&fresh, asOf)
	freshEntry, ok := buildYieldDailyOverviewEntry(fresh)
	if !ok {
		t.Fatal("fresh V1.5 holding should build a historical entry")
	}

	stale := base
	stale.CurrentPrice = 11
	stale.CurrentPriceTime = day2.Add(15 * time.Hour).Format(time.DateTime)
	applyV150YieldValuationAvailability(&stale, asOf)
	if stale.CurrentPrice != 0 || stale.YieldRateText != "--" {
		t.Fatalf("stale current mark was not made unavailable: %+v", stale)
	}
	staleEntry, ok := buildYieldDailyOverviewEntry(stale)
	if !ok {
		t.Fatal("stale current mark must not delete an activated holding from history")
	}

	prices := map[string]*yieldDailyOverviewPriceSeries{"000001.SZ": {
		Code: "000001.SZ", CloseByDay: map[string]float64{
			day1.Format(time.DateOnly): 10, day2.Format(time.DateOnly): 11, day3.Format(time.DateOnly): 12,
		},
	}}
	fill := buildV150DailyFillTestEvent(t, "stale-current-history", base.StockCode, day1, base.BuyAmount)
	ledgers := map[uint]v150YieldDailyOrderLedger{base.RecommendID: {
		RunID: fill.RunID, RuleID: fill.RuleID, Symbol: fill.Symbol,
		ReportAsOf: asOf, Events: []models.OrderEvent{fill},
	}}
	freshPoints, _ := buildYieldDailyOverviewPointsWithV150Ledgers(
		[]yieldDailyOverviewEntry{freshEntry}, []time.Time{day1, day2, day3}, prices, nil, ledgers,
	)
	stalePoints, _ := buildYieldDailyOverviewPointsWithV150Ledgers(
		[]yieldDailyOverviewEntry{staleEntry}, []time.Time{day1, day2}, prices, nil, ledgers,
	)
	if len(freshPoints) != 3 || len(stalePoints) != 2 {
		t.Fatalf("unexpected point counts: fresh=%d stale=%d", len(freshPoints), len(stalePoints))
	}
	for idx := range stalePoints {
		if stalePoints[idx].TradeDate != freshPoints[idx].TradeDate ||
			stalePoints[idx].PortfolioEquity != freshPoints[idx].PortfolioEquity ||
			stalePoints[idx].CumulativeAmountChange != freshPoints[idx].CumulativeAmountChange ||
			stalePoints[idx].StrategyNav != freshPoints[idx].StrategyNav {
			t.Fatalf("stale current mark rewrote historical point %d: stale=%+v fresh=%+v", idx, stalePoints[idx], freshPoints[idx])
		}
	}
}

func TestBuildYieldDailyOverviewPointsV150OmitsOnlyMissingPriceDay(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)
	day3 := day2.AddDate(0, 0, 1)
	buyCost := calcBuyTradeCostForVersion(marketSummaryVersion150, 10, tradingMarketSZ).NetAmount
	entry := yieldDailyOverviewEntry{
		RecommendID: 401, SummaryVersion: marketSummaryVersion150, StockCode: "000001.SZ",
		BuyTime: day1.Add(10 * time.Hour), BuyDay: day1, CurrentDay: day3,
		BuyAmount: 10, CurrentPrice: 12, CurrentPriceTime: day3.Add(10 * time.Hour).Format(time.DateTime),
		BuyCostNet: buyCost,
	}
	prices := map[string]*yieldDailyOverviewPriceSeries{"000001.SZ": {
		Code: "000001.SZ", CloseByDay: map[string]float64{
			day1.Format(time.DateOnly): 10, day3.Format(time.DateOnly): 12,
		},
	}}
	fill := buildV150DailyFillTestEvent(t, "missing-price-day", entry.StockCode, day1, entry.BuyAmount)
	points, pointWarnings := buildYieldDailyOverviewPointsWithV150Ledgers(
		[]yieldDailyOverviewEntry{entry}, []time.Time{day1, day2, day3}, prices, nil,
		map[uint]v150YieldDailyOrderLedger{entry.RecommendID: {
			RunID: fill.RunID, RuleID: fill.RuleID, Symbol: fill.Symbol,
			ReportAsOf: day3.Add(15 * time.Hour), Events: []models.OrderEvent{fill},
		}},
	)
	if len(points) != 2 || points[0].TradeDate != day1.Format(time.DateOnly) || points[1].TradeDate != day3.Format(time.DateOnly) {
		t.Fatalf("missing-price day must be omitted without deleting valid history: %+v", points)
	}
	if len(pointWarnings) != 1 || pointWarnings[0] != "000001.SZ:"+v150YieldDailyRawMinutePriceHealthCode+":"+day2.Format(time.DateOnly) {
		t.Fatalf("missing-price point warning=%v", pointWarnings)
	}
	warnings := collectV150YieldDailyPriceGapWarnings(
		[]yieldDailyOverviewEntry{entry}, []time.Time{day1, day2, day3}, prices,
	)
	wantWarning := "000001.SZ:" + v150YieldDailyRawMinutePriceHealthCode + ":" + day2.Format(time.DateOnly)
	if len(warnings) != 1 || warnings[0] != wantWarning {
		t.Fatalf("missing-price health warning=%v, want %q", warnings, wantWarning)
	}
}

func initV150YieldBenchmarkTestDB(t *testing.T) {
	t.Helper()
	oldDB := db.Dao
	oldMinuteDB := db.MinuteDao
	db.Init(filepath.Join(t.TempDir(), "v150-yield-benchmark.db"))
	initMinuteSchemaForTest(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close V1.5 yield benchmark test database: %v", err)
		}
		db.Dao = oldDB
		db.MinuteDao = oldMinuteDB
	})
}

func seedV150BenchmarkMinuteBars(t *testing.T, bars ...minuteBar) {
	t.Helper()
	if _, err := upsertMinuteBarsToCache(defaultBenchmarkModelCode, bars, "v150-yield-test"); err != nil {
		t.Fatalf("seed V1.5 benchmark minute bars: %v", err)
	}
}
