package data

import (
	"math"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
	"gorm.io/gorm"
)

func TestV150DailyLedgerCorporateActionsDoNotCreateMechanicalNAVJump(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	tests := []struct {
		name             string
		day2Price        float64
		quantity         float64
		cash             float64
		adjustmentFactor float64
	}{
		{name: "bonus shares", day2Price: 8, quantity: 1125, adjustmentFactor: .8},
		{name: "after-tax cash dividend", day2Price: 9.9, quantity: 900, cash: 90, adjustmentFactor: .99},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := buildV150DailyCorporateActionTestEvents(t, day1, day2, test.quantity, test.cash, test.adjustmentFactor, day2.Add(9*time.Hour+31*time.Minute))
			entry := yieldDailyOverviewEntry{
				RecommendID: 7, SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ",
				BuyDay: day1, CurrentDay: day2,
			}
			series := &yieldDailyOverviewPriceSeries{Code: entry.StockCode, CloseByDay: map[string]float64{
				day1.Format(time.DateOnly): 10,
				day2.Format(time.DateOnly): test.day2Price,
			}}
			points, warnings := buildYieldDailyOverviewPointsWithV150Ledgers(
				[]yieldDailyOverviewEntry{entry}, []time.Time{day1, day2},
				map[string]*yieldDailyOverviewPriceSeries{entry.StockCode: series}, nil,
				map[uint]v150YieldDailyOrderLedger{entry.RecommendID: {
					RunID: events[0].RunID, RuleID: events[0].RuleID, Symbol: entry.StockCode,
					ReportAsOf: day2.Add(15 * time.Hour), Events: events,
				}},
			)
			if len(warnings) != 0 || len(points) != 2 {
				t.Fatalf("points=%d warnings=%v", len(points), warnings)
			}
			if math.Abs(points[1].PortfolioEquity-points[0].PortfolioEquity) > 0.20 {
				t.Fatalf("corporate action created a mechanical NAV jump: day1=%.2f day2=%.2f", points[0].PortfolioEquity, points[1].PortfolioEquity)
			}
			cfg := v150.FixedStrategyV150Config()
			mark := v150.CalculateTradeCost(v150.SideSell, v150.MarketSZ, test.day2Price, int(test.quantity), cfg.SlippageScenarios()[0], cfg)
			entryCash := events[0].Price*events[0].Quantity + events[0].Fees
			wantEquity := round2(100_000 + mark.CashFlow + test.cash - entryCash)
			if math.Abs(points[1].PortfolioEquity-wantEquity) > 0.01 {
				t.Fatalf("corporate cash/quantity ledger value=%.2f want %.2f", points[1].PortfolioEquity, wantEquity)
			}
			if points[1].HoldingCount != 1 {
				t.Fatalf("adjusted open position disappeared: %+v", points[1])
			}
		})
	}
}

func TestResolveV150StrategyCurrentNetValueUsesLedgerQuantityAndDividendCash(t *testing.T) {
	loc := cnLocation()
	asOf := time.Date(2026, 8, 4, 14, 30, 0, 0, loc)
	originalNow := timeNow
	timeNow = func() time.Time { return asOf }
	t.Cleanup(func() { timeNow = originalNow })
	entry := yieldDailyOverviewEntry{
		SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ",
		CurrentPrice: 8, CurrentPriceTime: asOf.Format(time.DateTime),
		BuyAmount: 10, V150LedgerAccountingReady: true,
		V150LedgerQuantity: 1125, V150LedgerCorporateCash: 90,
	}
	cfg := v150.FixedStrategyV150Config()
	mark := v150.CalculateTradeCost(v150.SideSell, v150.MarketSZ, 8, 1125, cfg.SlippageScenarios()[0], cfg)
	want := round2(mark.CashFlow + 90)
	if got := resolveStrategyCurrentNetValue(entry); got != want {
		t.Fatalf("V1.5 current net value=%.2f want ledger value %.2f", got, want)
	}
}

func TestV150DailyLedgerLateFrozenCorporateActionOmitsOnlyAffectedDay(t *testing.T) {
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	events := buildV150DailyCorporateActionTestEvents(t, day1, day2, 1125, 0, .8, day2.Add(16*time.Hour))
	entry := yieldDailyOverviewEntry{
		RecommendID: 8, SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ",
		BuyDay: day1, CurrentDay: day2,
	}
	series := &yieldDailyOverviewPriceSeries{Code: entry.StockCode, CloseByDay: map[string]float64{
		day1.Format(time.DateOnly): 10,
		day2.Format(time.DateOnly): 8,
	}}
	points, warnings := buildYieldDailyOverviewPointsWithV150Ledgers(
		[]yieldDailyOverviewEntry{entry}, []time.Time{day1, day2},
		map[string]*yieldDailyOverviewPriceSeries{entry.StockCode: series}, nil,
		map[uint]v150YieldDailyOrderLedger{entry.RecommendID: {
			RunID: events[0].RunID, RuleID: events[0].RuleID, Symbol: entry.StockCode,
			ReportAsOf: day2.Add(17 * time.Hour), Events: events,
		}},
	)
	if len(points) != 1 || points[0].TradeDate != day1.Format(time.DateOnly) {
		t.Fatalf("late-frozen fact leaked into or erased the wrong point: %+v", points)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], v150YieldDailyLedgerAsOfHealthCode) || !strings.HasSuffix(warnings[0], day2.Format(time.DateOnly)) {
		t.Fatalf("late-frozen day warning missing: %v", warnings)
	}
}

func TestV150DailyPortfolioDoesNotPublishPartialPointWhenOneHoldingPriceIsMissing(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	leftEvents := []models.OrderEvent{buildV150DailyFillTestEvent(t, "partial-left", "000001.SZ", day, 10)}
	rightEvents := []models.OrderEvent{buildV150DailyFillTestEvent(t, "partial-right", "000002.SZ", day, 10)}
	entries := []yieldDailyOverviewEntry{
		{RecommendID: 11, SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ", BuyDay: day, CurrentDay: day},
		{RecommendID: 12, SummaryVersion: v150.StrategyVersion, StockCode: "000002.SZ", BuyDay: day, CurrentDay: day},
	}
	points, warnings := buildYieldDailyOverviewPointsWithV150Ledgers(
		entries,
		[]time.Time{day},
		map[string]*yieldDailyOverviewPriceSeries{
			"000001.SZ": {Code: "000001.SZ", CloseByDay: map[string]float64{day.Format(time.DateOnly): 10}},
		},
		nil,
		map[uint]v150YieldDailyOrderLedger{
			11: {RunID: leftEvents[0].RunID, RuleID: leftEvents[0].RuleID, Symbol: leftEvents[0].Symbol, ReportAsOf: day.Add(15 * time.Hour), Events: leftEvents},
			12: {RunID: rightEvents[0].RunID, RuleID: rightEvents[0].RuleID, Symbol: rightEvents[0].Symbol, ReportAsOf: day.Add(15 * time.Hour), Events: rightEvents},
		},
	)
	if len(points) != 0 {
		t.Fatalf("partial V1.5 portfolio point was published: %+v", points)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "000002.SZ:"+v150YieldDailyRawMinutePriceHealthCode) {
		t.Fatalf("missing holding warning=%v", warnings)
	}
}

func TestLoadV150DailyRawMinutePriceIgnoresRetroactivelyRewrittenQFQ(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	symbol := "000001.SZ"
	if err := db.Dao.Create([]models.AiRecommendDailyBar{
		{StockCode: symbol, TradeDate: day1, Open: 8, High: 8, Low: 8, Close: 8, Source: "tencent_qfq"},
		{StockCode: symbol, TradeDate: day2, Open: 8, High: 8, Low: 8, Close: 8, Source: "tencent_qfq"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	seedMarketSummaryV150Minutes(t, symbol, marketSummaryV150TestMinuteBucket(day1.Add(14*time.Hour+45*time.Minute), 10, 10, 100, false))
	seedMarketSummaryV150Minutes(t, symbol, marketSummaryV150TestMinuteBucket(day2.Add(14*time.Hour+45*time.Minute), 8, 8, 100, true))

	seriesMap, missing, provenanceWarnings, err := loadV150YieldDailyRawMinutePriceSeries(
		[]yieldDailyOverviewEntry{{RecommendID: 9, SummaryVersion: v150.StrategyVersion, StockCode: symbol, BuyDay: day1, CurrentDay: day2}},
		[]time.Time{day1, day2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(provenanceWarnings) != 0 {
		t.Fatalf("unexpected raw-minute provenance warnings: %v", provenanceWarnings)
	}
	series := seriesMap[symbol]
	if len(missing) != 0 || series == nil {
		t.Fatalf("raw minute series unavailable: missing=%v series=%+v", missing, series)
	}
	if got := series.CloseByDay[day1.Format(time.DateOnly)]; got != 10 {
		t.Fatalf("pre-ex qfq rewrite leaked into NAV mark: got %.2f want raw minute close 10.00", got)
	}
	if got := series.CloseByDay[day2.Format(time.DateOnly)]; got != 8 {
		t.Fatalf("end-labelled ex-date raw close=%.2f want 8.00", got)
	}
}

func TestLoadV150DailyRawMinutePriceRejectsAdjustedOrAmbiguousProvenance(t *testing.T) {
	for _, test := range []struct {
		name       string
		source     string
		wantHealth bool
	}{
		{name: "explicit raw", source: "akshare:sina:adjustment=none", wantHealth: false},
		{name: "qfq", source: "akshare:sina:adjustment=qfq", wantHealth: true},
		{name: "hfq", source: "akshare:sina:adjustment=hfq", wantHealth: true},
		{name: "legacy ambiguous akshare sina", source: "akshare:sina", wantHealth: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			initMarketSummaryV150ExecutionTestDB(t)
			loc := cnLocation()
			day := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
			symbol := "000001.SZ"
			bars := marketSummaryV150TestMinuteBucket(day.Add(14*time.Hour+45*time.Minute), 10, 10, 100, false)
			if _, err := upsertMinuteBarsToCache(symbol, bars, test.source); err != nil {
				t.Fatal(err)
			}

			seriesMap, _, provenanceWarnings, err := loadV150YieldDailyRawMinutePriceSeries(
				[]yieldDailyOverviewEntry{{SummaryVersion: v150.StrategyVersion, StockCode: symbol, BuyDay: day, CurrentDay: day}},
				[]time.Time{day},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !test.wantHealth {
				if len(provenanceWarnings) != 0 || seriesMap[symbol] == nil || seriesMap[symbol].CloseByDay[day.Format(time.DateOnly)] != 10 {
					t.Fatalf("proved-raw minute was rejected: series=%+v warnings=%v", seriesMap, provenanceWarnings)
				}
				return
			}
			want := symbol + ":" + v150YieldDailyRawMinuteProvenanceHealthCode + ":" + day.Format(time.DateOnly)
			if len(provenanceWarnings) != 1 || provenanceWarnings[0] != want {
				t.Fatalf("provenance warnings=%v want %q", provenanceWarnings, want)
			}
			if series := seriesMap[symbol]; series != nil && series.CloseByDay[day.Format(time.DateOnly)] > 0 {
				t.Fatalf("adjusted/ambiguous minute entered raw NAV series: %+v", series)
			}
		})
	}
}

func TestCalculateV150MaxDrawdownUsesLedgerAndRawMinuteCurveAcrossExDate(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}
	loc := cnLocation()
	day1 := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	reportNow := day2.Add(17 * time.Hour)
	originalNow := timeNow
	timeNow = func() time.Time { return reportNow }
	t.Cleanup(func() { timeNow = originalNow })

	request := appendV150DailyCohortReplayCorporateActionFixture(
		t, "drawdown-corporate-action", "000001.SZ", "bank",
		day1, day2, 1125, 0, .8, day2.Add(9*time.Hour+31*time.Minute),
	)
	record := models.AiRecommendStocks{
		SummaryVersion: v150.StrategyVersion, StockCode: request.Symbol,
		StrategyRunID: request.RunID, StrategyRuleID: request.RuleID,
	}
	if err := db.Dao.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Create([]models.AiRecommendDailyBar{
		{StockCode: defaultBenchmarkModelCode, TradeDate: day1, Open: 4, High: 4, Low: 4, Close: 4, Source: "tencent_qfq"},
		{StockCode: defaultBenchmarkModelCode, TradeDate: day2, Open: 4, High: 4, Low: 4, Close: 4, Source: "tencent_qfq"},
		// This deliberately represents Tencent's post-ex-date retroactive rewrite.
		// The V1.5 drawdown path must never consume it.
		{StockCode: record.StockCode, TradeDate: day1, Open: 8, High: 8, Low: 8, Close: 8, Source: "tencent_qfq"},
		{StockCode: record.StockCode, TradeDate: day2, Open: 8, High: 8, Low: 8, Close: 8, Source: "tencent_qfq"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	seedMarketSummaryV150Minutes(t, record.StockCode, marketSummaryV150TestMinuteBucket(day1.Add(14*time.Hour+45*time.Minute), 10, 10, 100, false))
	seedMarketSummaryV150Minutes(t, record.StockCode, marketSummaryV150TestMinuteBucket(day2.Add(14*time.Hour+45*time.Minute), 8, 8, 100, true))

	entry := yieldDailyOverviewEntry{
		RecommendID: record.ID, SummaryVersion: v150.StrategyVersion, StockCode: record.StockCode,
		BuyDay: day1, CurrentDay: day2,
	}
	maxDrawdown, ok := calculateV150StrategyMaxDrawdownByEntries([]yieldDailyOverviewEntry{entry})
	if !ok {
		t.Fatal("sealed V1.5 raw-minute drawdown curve was unavailable")
	}
	if maxDrawdown < -0.10 || maxDrawdown > 0 {
		t.Fatalf("ex-date created an impossible drawdown: %.2f%%", maxDrawdown)
	}
}

func TestLoadV150DailyOrderLedgerRejectsOrphanEventsWithoutFrozenCohort(t *testing.T) {
	initMarketSummaryV150ExecutionTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	events := buildV150DailyCorporateActionTestEvents(t, day.AddDate(0, 0, -1), day, 1125, 0, .8, day.Add(16*time.Hour))
	if err := db.Dao.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	record := models.AiRecommendStocks{
		Model: gorm.Model{ID: 10}, SummaryVersion: v150.StrategyVersion, StockCode: "000001.SZ",
		StrategyRunID: events[0].RunID, StrategyRuleID: events[0].RuleID,
	}
	ledgers, warnings := loadV150YieldDailyOrderLedgers([]models.AiRecommendStocks{record}, day.Add(15*time.Hour))
	if len(ledgers) != 0 {
		t.Fatalf("orphan order events published without frozen cohort metadata: %+v", ledgers)
	}
	wantWarning := record.StockCode + ":" + v150YieldDailyLedgerMissingHealthCode
	if len(warnings) != 1 || warnings[0] != wantWarning {
		t.Fatalf("orphan ledger warning=%v want %q", warnings, wantWarning)
	}
}

func buildV150DailyCorporateActionTestEvents(
	t *testing.T,
	entryDay, actionDay time.Time,
	adjustedQuantity, cash, factor float64,
	actionFrozenAt time.Time,
) []models.OrderEvent {
	t.Helper()
	cfg := v150.FixedStrategyV150Config()
	entryCost := v150.CalculateTradeCost(v150.SideBuy, v150.MarketSZ, 10, 900, cfg.SlippageScenarios()[0], cfg)
	fillAt := entryDay.Add(10 * time.Hour)
	fillFrozenAt := fillAt.Add(time.Second)
	events := []models.OrderEvent{
		{
			EventID: "daily-corp-fill-" + actionDay.Format(time.DateOnly), RunID: "daily-corp-run-" + actionDay.Format(time.DateOnly), RuleID: "daily-corp-rule",
			StrategyVersion: v150.StrategyVersion, TradeDate: entryDay.Format(time.DateOnly), Symbol: "000001.SZ",
			EventType: string(v150.EventFill), Sequence: 1, EventAt: fillAt,
			Price: entryCost.EffectivePrice, Quantity: 900,
			Fees:        entryCost.Commission + entryCost.TransferFee + entryCost.StampDuty,
			PayloadJSON: `{}`, FrozenAt: &fillFrozenAt,
		},
		{
			EventID: "daily-corp-action-" + actionDay.Format(time.DateOnly), RunID: "daily-corp-run-" + actionDay.Format(time.DateOnly), RuleID: "daily-corp-rule",
			StrategyVersion: v150.StrategyVersion, TradeDate: entryDay.Format(time.DateOnly), Symbol: "000001.SZ",
			EventType: string(v150.EventCorporateAction), Sequence: 2, EventAt: actionDay.Add(9*time.Hour + 30*time.Minute),
			Quantity: adjustedQuantity, CashAmount: cash, AdjustmentFactor: factor,
			PayloadJSON: `{}`, FrozenAt: &actionFrozenAt,
		},
	}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	return events
}

func buildV150DailyFillTestEvent(t *testing.T, suffix, symbol string, day time.Time, rawPrice float64) models.OrderEvent {
	t.Helper()
	cfg := v150.FixedStrategyV150Config()
	unit := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket(symbol), rawPrice, cfg.RoundLotSize, cfg.SlippageScenarios()[0], cfg)
	size := v150.SizeRoundLot(unit.EffectivePrice, cfg.TargetCashPerPosition, cfg)
	if size.Rejected {
		t.Fatalf("test position rejected: %+v", size)
	}
	cost := v150.CalculateTradeCost(v150.SideBuy, v150.ResolveMarket(symbol), rawPrice, size.Quantity, cfg.SlippageScenarios()[0], cfg)
	eventAt := day.Add(10 * time.Hour)
	frozenAt := eventAt.Add(time.Second)
	event := models.OrderEvent{
		EventID: "daily-fill-" + suffix, RunID: "daily-run-" + suffix, RuleID: "daily-rule-" + suffix,
		StrategyVersion: v150.StrategyVersion, TradeDate: day.Format(time.DateOnly), Symbol: symbol,
		EventType: string(v150.EventFill), Sequence: 1, EventAt: eventAt,
		Price: cost.EffectivePrice, Quantity: float64(size.Quantity),
		Fees:        cost.Commission + cost.TransferFee + cost.StampDuty,
		PayloadJSON: `{}`, FrozenAt: &frozenAt,
	}
	events := []models.OrderEvent{event}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	return events[0]
}
