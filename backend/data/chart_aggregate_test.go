package data

import (
	"testing"
	"time"
)

func chartTestBar(at time.Time, price float64) ChartBar {
	return ChartBar{At: at, Open: price, High: price + 1, Low: price - 1, Close: price + .5, Volume: 10, Amount: 100, Source: "fixture"}
}

func TestAggregateChartBarsNeverCrossesLunchAndAbsorbsSessionEndLabels(t *testing.T) {
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, cnLocation())
	bars := []ChartBar{
		chartTestBar(day.Add(9*time.Hour+30*time.Minute), 10),
		chartTestBar(day.Add(10*time.Hour+30*time.Minute), 11),
		chartTestBar(day.Add(11*time.Hour+30*time.Minute), 12),
		chartTestBar(day.Add(13*time.Hour), 20),
		chartTestBar(day.Add(14*time.Hour), 21),
		chartTestBar(day.Add(15*time.Hour), 22),
	}
	got, err := aggregateChartBars(bars, ChartPeriod60Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("60m bars=%+v; want four session-local buckets", got)
	}
	if got[1].At.Hour() != 10 || got[1].At.Minute() != 30 || got[1].Close != 12.5 {
		t.Fatalf("11:30 was not absorbed into morning tail: %+v", got[1])
	}
	if got[2].At.Hour() != 13 || got[1].Volume != 20 {
		t.Fatalf("lunch boundary was crossed: %+v", got)
	}
}

func TestAggregateLongPeriodsUseFirstRealTradingBar(t *testing.T) {
	first := time.Date(2026, 9, 8, 0, 0, 0, 0, cnLocation()) // Tuesday
	bars := []ChartBar{chartTestBar(first, 10), chartTestBar(first.AddDate(0, 0, 2), 11), chartTestBar(first.AddDate(0, 0, 7), 12)}
	weekly, err := aggregateChartBars(bars, ChartPeriodWeek)
	if err != nil {
		t.Fatal(err)
	}
	if len(weekly) != 2 || !weekly[0].At.Equal(first) || !weekly[1].At.Equal(first.AddDate(0, 0, 7)) {
		t.Fatalf("weekly labels=%+v", weekly)
	}
	for _, period := range []string{ChartPeriodMonth, ChartPeriodQuarter, ChartPeriodYear} {
		rows, err := aggregateChartBars(bars[:2], period)
		if err != nil || len(rows) != 1 || !rows[0].At.Equal(first) {
			t.Fatalf("%s labels=%+v err=%v", period, rows, err)
		}
	}
}

func TestChartMissingIntervalsDetectsEdgesWithoutReportingLunch(t *testing.T) {
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, cnLocation())
	from := day.Add(9*time.Hour + 30*time.Minute)
	to := day.Add(13*time.Hour + 15*time.Minute)
	bars := []ChartBar{
		chartTestBar(day.Add(9*time.Hour+35*time.Minute), 10),
		chartTestBar(day.Add(9*time.Hour+45*time.Minute), 11),
		chartTestBar(day.Add(11*time.Hour+30*time.Minute), 12),
		chartTestBar(day.Add(13*time.Hour), 13),
		chartTestBar(day.Add(13*time.Hour+5*time.Minute), 14),
	}
	gaps := chartMissingIntervals(bars, ChartPeriod5Minute, from, to)
	if len(gaps) != 4 {
		t.Fatalf("gaps=%+v; want leading, internal and trailing gaps only", gaps)
	}
	for _, gap := range gaps {
		if gap.From.Hour() == 11 && gap.To.Hour() == 13 {
			t.Fatalf("lunch was reported as a gap: %+v", gap)
		}
	}
}

func TestChartMissingIntervalsDetectsMissingTradingSessionsAcrossWeekend(t *testing.T) {
	loc := cnLocation()
	thursday := time.Date(2026, 8, 27, 0, 0, 0, 0, loc)
	friday := thursday.AddDate(0, 0, 1)
	monday := thursday.AddDate(0, 0, 4)
	from := thursday.Add(9*time.Hour + 30*time.Minute)
	to := monday.Add(15 * time.Hour)
	bars := chartCompleteSessionBars(friday, ChartPeriod5Minute)
	weekdays := func(day time.Time) bool {
		return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
	}
	gaps := chartMissingIntervalsWithCalendar(bars, ChartPeriod5Minute, from, to, weekdays)
	if len(gaps) != 4 {
		t.Fatalf("gaps=%+v; want morning/afternoon gaps for Thursday and Monday", gaps)
	}
	for _, gap := range gaps {
		weekday := gap.From.Weekday()
		if weekday == time.Saturday || weekday == time.Sunday {
			t.Fatalf("weekend was reported as a chart gap: %+v", gap)
		}
		if gap.From.Hour() != 9 && gap.From.Hour() != 13 {
			t.Fatalf("non-trading interval was reported as a chart gap: %+v", gap)
		}
	}
}

func TestChartMissingIntervalsIgnoresWeekendOnlyRange(t *testing.T) {
	loc := cnLocation()
	saturday := time.Date(2026, 8, 29, 9, 0, 0, 0, loc)
	weekdays := func(day time.Time) bool {
		return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
	}
	if gaps := chartMissingIntervalsWithCalendar(nil, ChartPeriod1Minute, saturday, saturday.AddDate(0, 0, 1).Add(8*time.Hour), weekdays); len(gaps) != 0 {
		t.Fatalf("weekend-only range produced gaps: %+v", gaps)
	}
}

func chartCompleteSessionBars(day time.Time, period string) []ChartBar {
	duration := chartPeriods[period]
	result := make([]ChartBar, 0)
	for _, pair := range [][2]time.Time{
		{day.Add(9*time.Hour + 30*time.Minute), day.Add(11*time.Hour + 30*time.Minute)},
		{day.Add(13 * time.Hour), day.Add(15 * time.Hour)},
	} {
		for at := pair[0]; at.Before(pair[1]); at = at.Add(duration) {
			result = append(result, chartTestBar(at, 10))
		}
	}
	return result
}
