package recommendationchart

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testProvider struct {
	cached       ProviderSnapshot
	refreshed    ProviderSnapshot
	cachedCalls  int
	refreshCalls int
	started      chan struct{}
	release      chan struct{}
	once         sync.Once
}

func (provider *testProvider) LoadCached(context.Context, string, time.Time, time.Time) (ProviderSnapshot, error) {
	provider.cachedCalls++
	return provider.cached, nil
}

func (provider *testProvider) Refresh(context.Context, string, time.Time, time.Time, []string) (ProviderSnapshot, error) {
	provider.refreshCalls++
	if provider.started != nil {
		provider.once.Do(func() { close(provider.started) })
		<-provider.release
	}
	return provider.refreshed, nil
}

type testCalendar struct {
	open       map[string]bool
	err        error
	strictCall int
	cachedCall int
}

func (calendar *testCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	calendar.strictCall++
	if calendar.err != nil {
		return false, calendar.err
	}
	return calendar.open[value.Format("2006-01-02")], nil
}

func (calendar *testCalendar) IsTradingDayCached(value time.Time) (bool, bool) {
	calendar.cachedCall++
	open, known := calendar.open[value.Format("2006-01-02")]
	return open, known
}

func TestEngineRejectsConcurrentRefreshForSameRecommendation(t *testing.T) {
	now := time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation)
	provider := &testProvider{started: make(chan struct{}), release: make(chan struct{}), refreshed: ProviderSnapshot{RefreshedAt: now}}
	calendar := &testCalendar{open: map[string]bool{"2026-08-19": true}}
	engine := NewEngine(provider, calendar, func() time.Time { return now })
	detail := Detail{RecommendationID: "refresh", StockCode: "sh600000", SignalAt: now}
	finished := make(chan error, 1)
	go func() {
		_, err := engine.Chart(context.Background(), detail, true)
		finished <- err
	}()
	<-provider.started
	if _, err := engine.Chart(context.Background(), detail, true); !errors.Is(err, ErrRefreshInProgress) {
		t.Fatalf("concurrent refresh error = %v", err)
	}
	close(provider.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestEngineRefreshFallsBackWhenStrictCalendarFails(t *testing.T) {
	now := time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation)
	provider := &testProvider{refreshed: ProviderSnapshot{RefreshedAt: now}}
	calendar := &testCalendar{err: errors.New("calendar upstream unavailable")}
	engine := NewEngine(provider, calendar, func() time.Time { return now })
	chart, err := engine.Chart(context.Background(), Detail{RecommendationID: "fallback", StockCode: "sh600000", SignalAt: now}, true)
	if err != nil {
		t.Fatal(err)
	}
	if provider.refreshCalls != 1 || len(chart.ProviderErrors) != 1 || chart.ProviderErrors[0].Provider != "trade_calendar" {
		t.Fatalf("calendar fallback was not exposed: calls=%d errors=%+v", provider.refreshCalls, chart.ProviderErrors)
	}
}

func TestNearestMarkerFiveMinuteAndTradingSegmentBoundaries(t *testing.T) {
	tradeAt := time.Date(2026, 8, 19, 11, 25, 0, 0, shanghaiLocation)
	exactBoundary := tradeAt.Add(5 * time.Minute)
	if got, ok := nearestMarker(tradeAt, []MinuteBar{{At: exactBoundary}}); !ok || !got.Equal(exactBoundary) {
		t.Fatalf("five minute boundary should snap: %v %v", got, ok)
	}
	if _, ok := nearestMarker(tradeAt, []MinuteBar{{At: tradeAt.Add(5*time.Minute + time.Nanosecond)}}); ok {
		t.Fatal("marker beyond five minutes must not snap")
	}
	if _, ok := nearestMarker(time.Date(2026, 8, 19, 11, 30, 0, 0, shanghaiLocation),
		[]MinuteBar{{At: time.Date(2026, 8, 19, 13, 0, 0, 0, shanghaiLocation)}}); ok {
		t.Fatal("marker must not cross the lunch break")
	}
}

func TestChartSessionsReportsInternalMinuteGapAsPartial(t *testing.T) {
	day := "2026-08-19"
	to := time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation)
	bars := []MinuteBar{
		{At: time.Date(2026, 8, 19, 9, 31, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
		{At: time.Date(2026, 8, 19, 9, 32, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
		{At: time.Date(2026, 8, 19, 9, 45, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
		{At: time.Date(2026, 8, 19, 10, 30, 0, 0, shanghaiLocation), Open: 10, High: 10, Low: 10, Close: 10},
	}
	sessions, missing := chartSessions([]string{day}, bars, bars[0].At, to)
	if len(sessions) != 1 || sessions[0].Status != "partial" || len(missing) != 0 {
		t.Fatalf("sessions=%+v missing=%v", sessions, missing)
	}
}

func TestChartSessionsDoesNotRequirePreviousCloseForCompleteCoverage(t *testing.T) {
	day := "2026-09-01"
	from := time.Date(2026, 9, 1, 9, 30, 0, 0, shanghaiLocation)
	to := time.Date(2026, 9, 1, 11, 19, 0, 0, shanghaiLocation)
	bars := make([]MinuteBar, 0, 106)
	for at := from; !at.After(time.Date(2026, 9, 1, 11, 15, 0, 0, shanghaiLocation)); at = at.Add(time.Minute) {
		bars = append(bars, MinuteBar{At: at, Open: 10, High: 10, Low: 10, Close: 10})
	}
	sessions, missing := chartSessions([]string{day}, bars, from, to)
	if len(sessions) != 1 || sessions[0].Status != "complete" || sessions[0].PreviousClose != 0 || len(missing) != 0 {
		t.Fatalf("sessions=%+v missing=%v", sessions, missing)
	}
}

func TestChartSessionsKeepsPartialAndMissingDatesDistinct(t *testing.T) {
	from := time.Date(2026, 8, 31, 9, 30, 0, 0, shanghaiLocation)
	to := time.Date(2026, 9, 1, 11, 0, 0, 0, shanghaiLocation)
	bars := []MinuteBar{
		{At: from, Open: 10, High: 10, Low: 10, Close: 10},
		{At: from.Add(30 * time.Minute), Open: 10, High: 10, Low: 10, Close: 10},
	}
	sessions, missing := chartSessions([]string{"2026-08-31", "2026-09-01"}, bars, from, to)
	if len(sessions) != 2 || sessions[0].Status != "partial" || sessions[1].Status != "missing" {
		t.Fatalf("sessions=%+v", sessions)
	}
	if len(missing) != 1 || missing[0] != "2026-09-01" {
		t.Fatalf("missing=%v", missing)
	}
}

func TestBuildChartUsesCurrentQuotePreviousClose(t *testing.T) {
	from := time.Date(2026, 9, 1, 9, 30, 0, 0, shanghaiLocation)
	to := from.Add(time.Minute)
	quoteAt := time.Date(2026, 9, 1, 11, 15, 0, 0, shanghaiLocation)
	snapshot := ProviderSnapshot{
		Bars: []MinuteBar{
			{At: from, Open: 8.8, High: 8.8, Low: 8.8, Close: 8.8},
			{At: to, Open: 8.8, High: 8.9, Low: 8.8, Close: 8.9},
		},
		Quote: &Quote{At: quoteAt, Price: 9.08, PreviousClose: 8.25},
	}
	chart := buildChart(Detail{RecommendationID: "chart-quote", StockCode: "sh600551", StockName: "时代出版"}, snapshot, from, to, []string{"2026-09-01"})
	if len(chart.Sessions) != 1 || chart.Sessions[0].PreviousClose != 8.25 {
		t.Fatalf("sessions=%+v", chart.Sessions)
	}
}

func TestChartRangeClampsLunchAndPreOpen(t *testing.T) {
	entry := time.Date(2026, 8, 18, 14, 35, 0, 0, shanghaiLocation)
	detail := Detail{SignalAt: entry, Trades: []Trade{{Side: "buy", TradedAt: entry}}}
	lunchNow := time.Date(2026, 8, 19, 12, 15, 0, 0, shanghaiLocation)
	_, lunchEnd := chartRange(detail, lunchNow)
	if got := lunchEnd.Format("2006-01-02 15:04"); got != "2026-08-19 11:30" {
		t.Fatalf("lunch range end = %s", got)
	}
	calendar := &testCalendar{open: map[string]bool{"2026-08-18": true, "2026-08-19": true}}
	preOpenNow := time.Date(2026, 8, 19, 9, 0, 0, 0, shanghaiLocation)
	from, preOpenEnd := chartRange(detail, preOpenNow)
	sessions, err := chartTradingSessions(context.Background(), calendar, from, preOpenEnd, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := preOpenEnd.Format("2006-01-02 15:04"); got != "2026-08-19 09:00" {
		t.Fatalf("pre-open range end = %s", got)
	}
	if len(sessions) != 1 || sessions[0] != "2026-08-18" {
		t.Fatalf("pre-open sessions = %v", sessions)
	}
}
