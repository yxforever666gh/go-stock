package data

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fixtureChartProvider struct {
	name     string
	result   chartProviderResult
	calls    *int
	requests *[]ChartRequest
}

func (p fixtureChartProvider) Name() string { return p.name }
func (p fixtureChartProvider) Fetch(_ context.Context, request ChartRequest) chartProviderResult {
	if p.calls != nil {
		(*p.calls)++
	}
	if p.requests != nil {
		*p.requests = append(*p.requests, request)
	}
	return p.result
}

func TestChartServiceFallbackAndCacheAdjustmentIsolation(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "chart-service.db"))
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, cnLocation())
	instrument, _ := ParseInstrumentID("159915", "etf", "SZ")
	request := ChartRequest{Instrument: instrument, Period: ChartPeriodDay, Adjustment: ChartAdjustmentNone,
		From: now.AddDate(0, 0, -2), To: now, Limit: 20}
	barAt := time.Date(2026, 8, 28, 0, 0, 0, 0, cnLocation())
	service := NewChartService()
	service.now = func() time.Time { return now }
	service.providerFactory = func(ChartRequest) []chartBarProvider {
		return []chartBarProvider{
			fixtureChartProvider{name: "primary", result: chartProviderResult{Bars: []ChartBar{}, Err: errors.New("timeout api_key=secret")}},
			fixtureChartProvider{name: "fallback", result: chartProviderResult{Bars: []ChartBar{chartTestBar(barAt, 10)}, AsOf: barAt}},
		}
	}
	envelope := service.RefreshChart(context.Background(), request)
	if len(envelope.Data.Bars) != 1 || envelope.Source != "fallback" || envelope.Status != "partial" || len(envelope.Sources) != 2 {
		t.Fatalf("fallback envelope=%+v", envelope)
	}
	if len(envelope.Errors) == 0 || envelope.Errors[0].Message == "" || envelope.Errors[0].Message == "timeout api_key=secret" {
		t.Fatalf("provider error was not sanitized: %+v", envelope.Errors)
	}
	qfq := request
	qfq.Adjustment = ChartAdjustmentQFQ
	if err := upsertChartBarsToCache(service.minuteDB, qfq, []ChartBar{chartTestBar(barAt, 99.123456)}, now); err != nil {
		t.Fatal(err)
	}
	noneCached := service.LoadCachedChart(context.Background(), request)
	qfqCached := service.LoadCachedChart(context.Background(), qfq)
	if noneCached.Data.Bars[0].Close == qfqCached.Data.Bars[0].Close || qfqCached.Data.Bars[0].Open != 99.123456 {
		t.Fatalf("adjustment scopes crossed or precision was lost: none=%+v qfq=%+v", noneCached.Data.Bars, qfqCached.Data.Bars)
	}
}

func TestAdjustedMinuteBarsUseVerifiedDailyRatioWithoutChangingVolume(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, cnLocation())
	instrument, _ := ParseInstrumentID("600000", "stock", "SH")
	service := &ChartService{now: func() time.Time { return now }}
	service.providerFactory = func(request ChartRequest) []chartBarProvider {
		price := 10.0
		if request.Adjustment == ChartAdjustmentQFQ {
			price = 5
		}
		return []chartBarProvider{fixtureChartProvider{name: "daily", result: chartProviderResult{Bars: []ChartBar{{At: time.Date(2026, 8, 28, 0, 0, 0, 0, cnLocation()), Open: price, High: price, Low: price, Close: price, Source: "daily"}}}}}
	}
	request := ChartRequest{Instrument: instrument, Period: ChartPeriod1Minute, Adjustment: ChartAdjustmentQFQ,
		From: now.Add(-time.Hour), To: now, Limit: 20}
	raw := []ChartBar{{At: now, Open: 10, High: 12, Low: 8, Close: 11, Volume: 100, Amount: 1000, Source: "tencent"}}
	adjusted, _, errorsOut := service.adjustMinuteBars(context.Background(), request, raw)
	if len(errorsOut) != 0 || len(adjusted) != 1 {
		t.Fatalf("adjusted=%+v errors=%+v", adjusted, errorsOut)
	}
	if adjusted[0].Open != 5 || adjusted[0].High != 6 || adjusted[0].Low != 4 || adjusted[0].Close != 5.5 || adjusted[0].Volume != 100 || adjusted[0].Amount != 1000 {
		t.Fatalf("unexpected adjusted minute bar=%+v", adjusted[0])
	}
}

func TestChartServiceContinuesFallbackUntilMultiDayRangeIsCovered(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "chart-range-fallback.db"))
	loc := cnLocation()
	thursday := time.Date(2026, 8, 27, 0, 0, 0, 0, loc)
	friday := thursday.AddDate(0, 0, 1)
	monday := thursday.AddDate(0, 0, 4)
	instrument, _ := ParseInstrumentID("159915", "etf", "SZ")
	request := ChartRequest{Instrument: instrument, Period: ChartPeriodDay, Adjustment: ChartAdjustmentNone, From: thursday, To: monday, Limit: 10}
	primaryCalls, fallbackCalls := 0, 0
	service := NewChartService()
	service.now = func() time.Time { return monday.Add(16 * time.Hour) }
	service.isTradingDay = chartWeekdayFixture
	service.providerFactory = func(ChartRequest) []chartBarProvider {
		return []chartBarProvider{
			fixtureChartProvider{name: "primary", calls: &primaryCalls, result: chartProviderResult{Bars: []ChartBar{chartTestBar(monday, 12)}, AsOf: monday}},
			fixtureChartProvider{name: "fallback", calls: &fallbackCalls, result: chartProviderResult{Bars: []ChartBar{chartTestBar(thursday, 10), chartTestBar(friday, 11)}, AsOf: friday}},
		}
	}
	envelope := service.RefreshChart(context.Background(), request)
	if primaryCalls != 1 || fallbackCalls != 1 || len(envelope.Data.Bars) != 3 || len(envelope.Data.MissingIntervals) != 0 || envelope.Status != "ok" {
		t.Fatalf("fallback did not complete range: calls=%d/%d envelope=%+v", primaryCalls, fallbackCalls, envelope)
	}
	for _, item := range envelope.Errors {
		if item.Code == "range_incomplete" {
			t.Fatalf("completed fallback retained range error: %+v", envelope.Errors)
		}
	}
}

func TestChartServiceMarksRangePartialWhenAllSourcesRemainIncomplete(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "chart-range-partial.db"))
	loc := cnLocation()
	thursday := time.Date(2026, 8, 27, 0, 0, 0, 0, loc)
	friday := thursday.AddDate(0, 0, 1)
	monday := thursday.AddDate(0, 0, 4)
	instrument, _ := ParseInstrumentID("159915", "etf", "SZ")
	request := ChartRequest{Instrument: instrument, Period: ChartPeriodDay, Adjustment: ChartAdjustmentNone, From: thursday, To: monday, Limit: 10}
	service := NewChartService()
	service.now = func() time.Time { return monday.Add(16 * time.Hour) }
	service.isTradingDay = chartWeekdayFixture
	service.providerFactory = func(ChartRequest) []chartBarProvider {
		return []chartBarProvider{
			fixtureChartProvider{name: "primary", result: chartProviderResult{Bars: []ChartBar{chartTestBar(monday, 12)}, AsOf: monday}},
			fixtureChartProvider{name: "fallback", result: chartProviderResult{Bars: []ChartBar{chartTestBar(friday, 11)}, AsOf: friday}},
		}
	}
	envelope := service.RefreshChart(context.Background(), request)
	foundRangeError := false
	for _, item := range envelope.Errors {
		foundRangeError = foundRangeError || item.Code == "range_incomplete"
	}
	if envelope.Status != "partial" || len(envelope.Data.MissingIntervals) == 0 || !foundRangeError {
		t.Fatalf("incomplete range was not exposed: %+v", envelope)
	}
}

func TestChartServiceExpandsLongPeriodProviderLimit(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "chart-long-limit.db"))
	loc := cnLocation()
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, loc)
	to := time.Date(2026, 8, 31, 0, 0, 0, 0, loc)
	instrument, _ := ParseInstrumentID("159915", "etf", "SZ")
	request := ChartRequest{Instrument: instrument, Period: ChartPeriodMonth, Adjustment: ChartAdjustmentNone, From: from, To: to, Limit: 100}
	requests := make([]ChartRequest, 0)
	service := NewChartService()
	service.now = func() time.Time { return to.Add(16 * time.Hour) }
	service.isTradingDay = chartWeekdayFixture
	service.providerFactory = func(ChartRequest) []chartBarProvider {
		return []chartBarProvider{fixtureChartProvider{name: "daily", requests: &requests, result: chartProviderResult{Bars: chartDailyWeekdayBars(from, to)}}}
	}
	envelope := service.RefreshChart(context.Background(), request)
	if len(requests) != 1 || requests[0].Period != ChartPeriodDay || requests[0].Limit != 2300 {
		t.Fatalf("long-period base request=%+v", requests)
	}
	if envelope.Status != "ok" || len(envelope.Data.Bars) != 1 || len(envelope.Data.MissingIntervals) != 0 {
		t.Fatalf("long-period envelope=%+v", envelope)
	}
}

func chartWeekdayFixture(day time.Time) bool {
	return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
}

func chartDailyWeekdayBars(from, to time.Time) []ChartBar {
	result := make([]ChartBar, 0)
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		if chartWeekdayFixture(day) {
			result = append(result, chartTestBar(day, 10))
		}
	}
	return result
}
