package research2

import (
	"context"
	"math"
	"testing"
	"time"
)

type performanceFixtureProvider struct {
	data       BuyDayMarketData
	daily      map[string][]DailyClose
	calls      int
	dailyCalls int
}

func (provider *performanceFixtureProvider) BuyDayData(context.Context, Recommendation) (BuyDayMarketData, error) {
	provider.calls++
	return provider.data, nil
}

func (provider *performanceFixtureProvider) DailyCloses(_ context.Context, code string, _, _ time.Time) ([]DailyClose, string, error) {
	provider.dailyCalls++
	return append([]DailyClose(nil), provider.daily[code]...), `{"source":"fixture"}`, nil
}

func completeBuyDayBars(buy time.Time, high, close float64) []PerformanceBar {
	first := buy.In(shanghai()).Truncate(time.Minute).Add(2 * time.Minute)
	last := time.Date(buy.Year(), buy.Month(), buy.Day(), 15, 0, 0, 0, shanghai())
	result := make([]PerformanceBar, 0, 240)
	for _, at := range expectedBuyDayMinuteEnds(first, last) {
		result = append(result, PerformanceBar{At: at, Open: 10, High: high, Low: 9.9, Close: close, Source: "fixture"})
	}
	return result
}

func TestBuyDayLimitOutcomeStartsAfterActualBuyAndIsMutuallyExclusive(t *testing.T) {
	buy := time.Date(2026, 9, 22, 9, 52, 30, 0, shanghai())
	item := Recommendation{BuyAt: &buy, StockCode: "sh600001", StockName: "fixture"}
	bars := completeBuyDayBars(buy, 10.9, 10.9)
	// The partially pre-buy 09:53 bucket must never influence classification.
	bars = append(bars, PerformanceBar{At: time.Date(2026, 9, 22, 9, 53, 0, 0, shanghai()), Open: 10, High: 11, Low: 10, Close: 11})
	evaluation, err := ClassifyBuyDayLimitOutcome(item, BuyDayMarketData{PreviousClose: 10, LimitRate: .1, Bars: bars})
	if err != nil || evaluation.Outcome != LimitOutcomeUntouched {
		t.Fatalf("outcome=%+v err=%v", evaluation, err)
	}

	bars[5].High = 11
	bars[len(bars)-2].Close = 11
	evaluation, err = ClassifyBuyDayLimitOutcome(item, BuyDayMarketData{PreviousClose: 10, LimitRate: .1, Bars: bars})
	if err != nil || evaluation.Outcome != LimitOutcomeSealed {
		t.Fatalf("sealed outcome=%+v err=%v", evaluation, err)
	}
	bars[len(bars)-2].Close = 10.95
	evaluation, err = ClassifyBuyDayLimitOutcome(item, BuyDayMarketData{PreviousClose: 10, LimitRate: .1, Bars: bars})
	if err != nil || evaluation.Outcome != LimitOutcomeBroken {
		t.Fatalf("broken outcome=%+v err=%v", evaluation, err)
	}
	if _, err = ClassifyBuyDayLimitOutcome(item, BuyDayMarketData{PreviousClose: 10, LimitRate: .1, Bars: bars[:len(bars)-2]}); err == nil {
		t.Fatal("incomplete close coverage was accepted")
	}
}

func TestMainlandLimitRatesAndCentRounding(t *testing.T) {
	for _, testCase := range []struct {
		code, name string
		days       int
		want       float64
		limited    bool
	}{
		{"sh600001", "普通", 100, .10, true}, {"sz300001", "创业板", 100, .20, true}, {"sh688001", "科创板", 100, .20, true},
		{"bj830001", "北交所", 100, .30, true}, {"sh600001", "*ST示例", 100, .05, true}, {"sh600001", "新股", 5, 0, false},
	} {
		got, limited := MainlandLimitRate(testCase.code, testCase.name, testCase.days)
		if got != testCase.want || limited != testCase.limited {
			t.Fatalf("%+v got=%v limited=%v", testCase, got, limited)
		}
	}
	if got := UpperLimitPrice(10.01, .1); got != 11.01 {
		t.Fatalf("limit price=%v", got)
	}
}

func TestPortfolioPerformanceEqualWeightsAccountTWRAndPoolsOutcomes(t *testing.T) {
	repository := research2TestRepository(t)
	if err := repository.db.AutoMigrate(&AccountDailyValuation{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	day1 := time.Date(2026, 9, 21, 10, 0, 0, 0, shanghai())
	day2 := day1.AddDate(0, 0, 1)
	closed := day2
	items := []Recommendation{
		{RecommendationID: "a", AnalysisRunID: "run-a", Slot: "09:30", StockCode: "sh600001", StockName: "a", SignalAt: day1, TargetBuyAt: day1, BuyAt: &day1, BuyPrice: 10, Quantity: 100, Status: "closed", SellAt: &closed, NetPnL: 100, BuyDayLimitStatus: LimitOutcomeComplete, BuyDayLimitOutcome: LimitOutcomeSealed},
		{RecommendationID: "b", AnalysisRunID: "run-b", Slot: "09:35", StockCode: "sh600002", StockName: "b", SignalAt: day1, TargetBuyAt: day1, BuyAt: &day1, BuyPrice: 10, Quantity: 100, Status: "closed", SellAt: &closed, NetPnL: -20, BuyDayLimitStatus: LimitOutcomeComplete, BuyDayLimitOutcome: LimitOutcomeBroken},
		{RecommendationID: "c", AnalysisRunID: "run-c", Slot: "09:35", StockCode: "sh600003", StockName: "c", SignalAt: day2, TargetBuyAt: day2, BuyAt: &day2, BuyPrice: 10, Quantity: 100, Status: "active", BuyDayLimitStatus: LimitOutcomePending},
	}
	if err := repository.db.Create(&items).Error; err != nil {
		t.Fatal(err)
	}
	r1, r2, zero, minus := .10, .10, 0.0, -.10
	valuations := []AccountDailyValuation{
		{ValuationID: "a1", Slot: "09:30", TradingDate: "2026-09-21", ValuedAt: day1, NetAssetValue: 11000, DailyReturn: &r1, DataStatus: DailyValuationComplete},
		{ValuationID: "a2", Slot: "09:30", TradingDate: "2026-09-22", ValuedAt: day2, NetAssetValue: 12100, DailyReturn: &r2, DataStatus: DailyValuationComplete},
		{ValuationID: "b1", Slot: "09:35", TradingDate: "2026-09-21", ValuedAt: day1, NetAssetValue: 10000, DailyReturn: &zero, DataStatus: DailyValuationComplete},
		{ValuationID: "b2", Slot: "09:35", TradingDate: "2026-09-22", ValuedAt: day2, NetAssetValue: 9000, DailyReturn: &minus, DataStatus: DailyValuationComplete},
	}
	if err := repository.db.Create(&valuations).Error; err != nil {
		t.Fatal(err)
	}
	result, err := repository.PortfolioPerformance(ctx, PortfolioQuery{Slots: []string{"09:40", "09:35", "09:30"}, From: "2026-09-21", To: "2026-09-22"})
	if err != nil {
		t.Fatal(err)
	}
	if result.EffectiveAccountCount != 2 || result.NoActivityAccountCount != 1 || result.PeriodReturn == nil || math.Abs(*result.PeriodReturn-.055) > 1e-9 {
		t.Fatalf("portfolio=%+v", result)
	}
	if result.ClosedTrades != 2 || result.WinningTrades != 1 || result.WinRate == nil || *result.WinRate != .5 || result.Sealed.Count != 1 || result.Broken.Count != 1 || result.PendingOutcomeCount != 1 {
		t.Fatalf("statistics=%+v", result)
	}
	if len(result.Curve) != 2 || math.Abs(result.Curve[0].ReturnRate-.05) > 1e-9 || math.Abs(result.Curve[1].ReturnRate-.055) > 1e-9 {
		t.Fatalf("curve=%+v", result.Curve)
	}
}

func TestOutcomeBackfillIncludesOpenPositionsAndIsIdempotent(t *testing.T) {
	repository := research2TestRepository(t)
	buy := time.Date(2026, 9, 22, 9, 52, 30, 0, shanghai())
	item := Recommendation{RecommendationID: "open", AnalysisRunID: "run", Slot: "09:50", StockCode: "sh600001", StockName: "open", SignalAt: buy, TargetBuyAt: buy, BuyAt: &buy, BuyPrice: 10, Quantity: 100, Status: "active"}
	if err := repository.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	bars := completeBuyDayBars(buy, 10.9, 10.9)
	bars[3].High = 11
	bars[len(bars)-1].Close = 11
	provider := &performanceFixtureProvider{data: BuyDayMarketData{PreviousClose: 10, LimitRate: .1, Bars: bars}}
	service := NewPerformanceBackfillService(repository, provider, testCalendar{})
	service.now = func() time.Time { return time.Date(2026, 9, 22, 15, 5, 0, 0, shanghai()) }
	result, err := service.FinalizeBuyDayOutcomes(context.Background(), "2026-09-22")
	if err != nil || result.OutcomesCompleted != 1 || provider.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, provider.calls, err)
	}
	if _, err = service.FinalizeBuyDayOutcomes(context.Background(), "2026-09-22"); err != nil || provider.calls != 1 {
		t.Fatalf("idempotent calls=%d err=%v", provider.calls, err)
	}
	var stored Recommendation
	if err = repository.db.Where("recommendation_id = ?", item.RecommendationID).First(&stored).Error; err != nil || stored.BuyDayLimitStatus != LimitOutcomeComplete || stored.BuyDayLimitOutcome != LimitOutcomeSealed || stored.BuyDayLimitAttemptCount != 1 {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestPerformanceBackfillCreatesIdempotentDailyValuationsWithoutChangingCash(t *testing.T) {
	repository := research2TestRepository(t)
	if err := repository.db.AutoMigrate(&AccountCapitalEvent{}, &AccountDailyValuation{}); err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 9, 21, 10, 0, 0, 0, shanghai())
	buy := day1
	item := Recommendation{RecommendationID: "valuation", AnalysisRunID: "run", Slot: "09:50", StockCode: "sh600001", StockName: "valuation", SignalAt: buy, TargetBuyAt: buy, BuyAt: &buy, BuyPrice: 10, Quantity: 100, Status: "active"}
	if err := repository.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	event := AccountCapitalEvent{EventID: "initial", Slot: "09:50", EventType: CapitalEventInitial, Amount: 10000, External: true, Source: "fixture", EffectiveAt: day1.AddDate(0, 0, -1), TradingDate: "2026-09-20"}
	trade := Trade{TradeID: "buy", Slot: "09:50", RecommendationID: item.RecommendationID, Side: "buy", TradedAt: buy, MarketPrice: 10, ExecutionPrice: 10, Quantity: 100, NetCashFlow: -1005}
	if err := repository.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.db.Create(&trade).Error; err != nil {
		t.Fatal(err)
	}
	provider := &performanceFixtureProvider{data: BuyDayMarketData{PreviousClose: 10, LimitRate: .1, Bars: completeBuyDayBars(buy, 10.8, 10.8)}, daily: map[string][]DailyClose{"sh600001": {
		{TradingDate: "2026-09-21", Close: 11, Source: "fixture"}, {TradingDate: "2026-09-22", Close: 12, Source: "fixture"},
	}}}
	service := NewPerformanceBackfillService(repository, provider, testCalendar{})
	service.now = func() time.Time { return time.Date(2026, 9, 22, 16, 0, 0, 0, shanghai()) }
	var accountBefore Account
	if err := repository.db.Where("slot = ?", "09:50").First(&accountBefore).Error; err != nil {
		t.Fatal(err)
	}
	first, err := service.BackfillAll(context.Background())
	if err != nil || first.ValuationsCompleted < 2 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := service.BackfillAll(context.Background())
	if err != nil || second.ValuationsCompleted < 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	var count int64
	if err := repository.db.Model(&AccountDailyValuation{}).Where("slot = ?", "09:50").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("valuation count=%d err=%v", count, err)
	}
	var accountAfter Account
	if err := repository.db.Where("slot = ?", "09:50").First(&accountAfter).Error; err != nil || accountAfter.Cash != accountBefore.Cash {
		t.Fatalf("cash changed: before=%v after=%v err=%v", accountBefore.Cash, accountAfter.Cash, err)
	}
}
