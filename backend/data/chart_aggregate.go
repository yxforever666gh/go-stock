package data

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func normalizeChartBars(bars []ChartBar, from, to time.Time) []ChartBar {
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].At.Before(bars[j].At) })
	seen := make(map[int64]struct{}, len(bars))
	result := make([]ChartBar, 0, len(bars))
	for _, bar := range bars {
		bar.At = chartShanghaiTime(bar.At)
		key := bar.At.UnixMilli()
		if bar.At.IsZero() || bar.At.Before(from) || bar.At.After(to) || !validPublicChartBar(bar) {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, bar)
	}
	return result
}

func validPublicChartBar(bar ChartBar) bool {
	values := []float64{bar.Open, bar.High, bar.Low, bar.Close, bar.Volume, bar.Amount}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return bar.Open > 0 && bar.High > 0 && bar.Low > 0 && bar.Close > 0 && bar.High >= bar.Low && bar.Volume >= 0 && bar.Amount >= 0
}

func aggregateChartBars(bars []ChartBar, period string) ([]ChartBar, error) {
	if !validChartPeriod(period) {
		return nil, fmt.Errorf("unsupported chart period %q", period)
	}
	if period == ChartPeriod1Minute || period == ChartPeriodDay {
		result := append([]ChartBar(nil), bars...)
		sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
		return result, nil
	}
	bars = append([]ChartBar(nil), bars...)
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].At.Before(bars[j].At) })
	type aggregate struct {
		bar     ChartBar
		sources []string
	}
	groups := make(map[string]*aggregate)
	order := make([]string, 0)
	for _, row := range bars {
		key, at, ok := chartAggregationKey(row.At, period)
		if !ok {
			continue
		}
		group := groups[key]
		if group == nil {
			if _, minutePeriod := chartPeriods[period]; !minutePeriod {
				// Long-period labels point at the first real trading bar, never a
				// synthetic calendar boundary such as a holiday Monday/month start.
				at = row.At
			}
			group = &aggregate{bar: ChartBar{At: at, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume, Amount: row.Amount}, sources: []string{row.Source}}
			groups[key] = group
			order = append(order, key)
			continue
		}
		if row.High > group.bar.High {
			group.bar.High = row.High
		}
		if row.Low < group.bar.Low {
			group.bar.Low = row.Low
		}
		group.bar.Close = row.Close
		group.bar.Volume += row.Volume
		group.bar.Amount += row.Amount
		group.sources = append(group.sources, row.Source)
	}
	result := make([]ChartBar, 0, len(order))
	for _, key := range order {
		group := groups[key]
		group.bar.Source = aggregateChartSource(group.sources)
		result = append(result, group.bar)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result, nil
}

func chartAggregationKey(value time.Time, period string) (string, time.Time, bool) {
	value = chartShanghaiTime(value)
	if value.IsZero() {
		return "", time.Time{}, false
	}
	date := value.Format("2006-01-02")
	if duration, ok := chartPeriods[period]; ok {
		start, end, segment, ok := chartTradingSegmentBounds(value)
		if !ok {
			return "", time.Time{}, false
		}
		bucketValue := value
		if value.Equal(end) {
			// Provider minute bars commonly label the final interval by its end
			// (11:30/15:00). Keep it in the preceding bucket instead of creating
			// a one-point 60-minute tail bucket.
			bucketValue = value.Add(-time.Nanosecond)
		}
		index := int(bucketValue.Sub(start) / duration)
		bucket := start.Add(time.Duration(index) * duration)
		return fmt.Sprintf("%s/%d/%d", date, segment, index), bucket, true
	}
	year, month, _ := value.Date()
	dayStart := time.Date(year, month, value.Day(), 0, 0, 0, 0, value.Location())
	switch period {
	case ChartPeriodWeek:
		isoYear, isoWeek := value.ISOWeek()
		weekday := int(value.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return fmt.Sprintf("%04d-W%02d", isoYear, isoWeek), dayStart.AddDate(0, 0, 1-weekday), true
	case ChartPeriodMonth:
		return fmt.Sprintf("%04d-%02d", year, month), time.Date(year, month, 1, 0, 0, 0, 0, value.Location()), true
	case ChartPeriodQuarter:
		quarter := (int(month)-1)/3 + 1
		startMonth := time.Month((quarter-1)*3 + 1)
		return fmt.Sprintf("%04d-Q%d", year, quarter), time.Date(year, startMonth, 1, 0, 0, 0, 0, value.Location()), true
	case ChartPeriodYear:
		return fmt.Sprintf("%04d", year), time.Date(year, time.January, 1, 0, 0, 0, 0, value.Location()), true
	default:
		return "", time.Time{}, false
	}
}

func chartTradingSegment(value time.Time) (time.Time, int, bool) {
	start, _, segment, ok := chartTradingSegmentBounds(value)
	return start, segment, ok
}

func chartTradingSegmentBounds(value time.Time) (time.Time, time.Time, int, bool) {
	value = chartShanghaiTime(value)
	year, month, day := value.Date()
	morning := time.Date(year, month, day, 9, 30, 0, 0, value.Location())
	morningEnd := time.Date(year, month, day, 11, 30, 0, 0, value.Location())
	afternoon := time.Date(year, month, day, 13, 0, 0, 0, value.Location())
	afternoonEnd := time.Date(year, month, day, 15, 0, 0, 0, value.Location())
	switch {
	case !value.Before(morning) && !value.After(morningEnd):
		return morning, morningEnd, 1, true
	case !value.Before(afternoon) && !value.After(afternoonEnd):
		return afternoon, afternoonEnd, 2, true
	default:
		return time.Time{}, time.Time{}, 0, false
	}
}

func sameChartSegment(left, right time.Time) bool {
	leftStart, leftSegment, leftOK := chartTradingSegment(left)
	rightStart, rightSegment, rightOK := chartTradingSegment(right)
	return leftOK && rightOK && leftSegment == rightSegment && leftStart.Equal(rightStart)
}

type chartTradingDayFunc func(time.Time) bool

type chartCoverageInterval struct {
	key      string
	from, to time.Time
}

const maxChartCoverageScanDays = 20000

func defaultChartTradingDay(day time.Time) bool {
	if open, known := (ResearchTradingCalendar{}).IsTradingDayCached(day); known {
		return open
	}
	return !isWeekendCN(day.In(cnLocation()))
}

func chartMissingIntervals(bars []ChartBar, period string, from, to time.Time) []ChartMissingInterval {
	return chartMissingIntervalsWithCalendar(bars, period, from, to, defaultChartTradingDay)
}

func chartMissingIntervalsWithCalendar(bars []ChartBar, period string, from, to time.Time, isTradingDay chartTradingDayFunc) []ChartMissingInterval {
	from, to = chartShanghaiTime(from), chartShanghaiTime(to)
	if from.IsZero() || to.IsZero() || from.After(to) || !validChartPeriod(period) {
		return []ChartMissingInterval{}
	}
	if isTradingDay == nil {
		isTradingDay = defaultChartTradingDay
	}
	reason := "missing_bars"
	if len(bars) == 0 {
		reason = "no_data"
	}
	scanFrom, prefix := chartCoverageScanStart(from, to, reason)
	result := make([]ChartMissingInterval, 0)
	if prefix != nil {
		result = append(result, *prefix)
	}
	if duration, minutePeriod := chartPeriods[period]; minutePeriod {
		return mergeChartMissingIntervals(result, chartMinuteMissingIntervals(bars, duration, scanFrom, to, reason, isTradingDay))
	}
	return mergeChartMissingIntervals(result, chartPeriodMissingIntervals(bars, period, scanFrom, to, reason, isTradingDay))
}

func chartCoverageScanStart(from, to time.Time, reason string) (time.Time, *ChartMissingInterval) {
	loc := cnLocation()
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	toDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	maximumSpan := time.Duration(maxChartCoverageScanDays-1) * 24 * time.Hour
	if toDay.Sub(fromDay) <= maximumSpan {
		return from, nil
	}
	scanDay := toDay.AddDate(0, 0, -(maxChartCoverageScanDays - 1))
	return scanDay, &ChartMissingInterval{From: from, To: scanDay.Add(-time.Nanosecond), Reason: reason}
}

func chartMinuteMissingIntervals(bars []ChartBar, duration time.Duration, from, to time.Time, reason string, isTradingDay chartTradingDayFunc) []ChartMissingInterval {
	observed := make(map[int64]struct{}, len(bars))
	for _, bar := range bars {
		at := chartShanghaiTime(bar.At)
		if !at.IsZero() {
			observed[at.UnixMilli()] = struct{}{}
		}
	}
	result := make([]ChartMissingInterval, 0)
	for _, session := range chartMinuteCoverageSessions(from, to, isTradingDay) {
		expected := chartExpectedMinuteBuckets(session, duration, from, to)
		if len(expected) == 0 {
			continue
		}
		if duration == time.Minute {
			// Public sources use both start labels (09:30) and end labels
			// (09:31). Treat either convention as covering the session edge.
			if _, ok := observed[session.Start.UnixMilli()]; !ok {
				if _, nextOK := observed[session.Start.Add(time.Minute).UnixMilli()]; nextOK {
					observed[session.Start.UnixMilli()] = struct{}{}
				}
			}
			if _, ok := observed[session.End.UnixMilli()]; !ok {
				if _, previousOK := observed[session.End.Add(-time.Minute).UnixMilli()]; previousOK {
					observed[session.End.UnixMilli()] = struct{}{}
				}
			}
		}
		var runStart, runEnd time.Time
		flush := func() {
			if runStart.IsZero() {
				return
			}
			result = append(result, ChartMissingInterval{From: runStart, To: runEnd, Reason: reason})
			runStart, runEnd = time.Time{}, time.Time{}
		}
		for _, expectedAt := range expected {
			if _, ok := observed[expectedAt.UnixMilli()]; ok {
				flush()
				continue
			}
			if runStart.IsZero() {
				runStart = expectedAt
			}
			runEnd = expectedAt
		}
		flush()
	}
	return result
}

func chartMinuteCoverageSessions(from, to time.Time, isTradingDay chartTradingDayFunc) []minuteCoverageSession {
	loc := cnLocation()
	startDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	endDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	result := make([]minuteCoverageSession, 0)
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		if !isTradingDay(day) {
			continue
		}
		result = append(result,
			minuteCoverageSession{Start: time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc), End: time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc)},
			minuteCoverageSession{Start: time.Date(day.Year(), day.Month(), day.Day(), 13, 0, 0, 0, loc), End: time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)},
		)
	}
	return result
}

func chartExpectedMinuteBuckets(session minuteCoverageSession, duration time.Duration, from, to time.Time) []time.Time {
	result := make([]time.Time, 0)
	for at := session.Start; ; at = at.Add(duration) {
		if duration == time.Minute {
			if at.After(session.End) {
				break
			}
		} else if !at.Before(session.End) {
			break
		}
		if !at.Before(from) && !at.After(to) {
			result = append(result, at)
		}
	}
	return result
}

func chartPeriodMissingIntervals(bars []ChartBar, period string, from, to time.Time, reason string, isTradingDay chartTradingDayFunc) []ChartMissingInterval {
	expected := make(map[string]*chartCoverageInterval)
	order := make([]string, 0)
	loc := cnLocation()
	startDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	endDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		if day.Before(from) || day.After(to) || !isTradingDay(day) {
			continue
		}
		key, ok := chartCoverageKey(day, period)
		if !ok {
			continue
		}
		item := expected[key]
		if item == nil {
			item = &chartCoverageInterval{key: key, from: day, to: day}
			expected[key] = item
			order = append(order, key)
		} else {
			item.to = day
		}
	}
	observed := make(map[string]struct{}, len(bars))
	for _, bar := range bars {
		if key, ok := chartCoverageKey(chartShanghaiTime(bar.At), period); ok {
			observed[key] = struct{}{}
		}
	}
	result := make([]ChartMissingInterval, 0)
	for _, key := range order {
		if _, ok := observed[key]; ok {
			continue
		}
		item := expected[key]
		result = append(result, ChartMissingInterval{From: item.from, To: item.to, Reason: reason})
	}
	return result
}

func chartCoverageKey(value time.Time, period string) (string, bool) {
	if period == ChartPeriodDay {
		return chartShanghaiTime(value).Format(time.DateOnly), true
	}
	key, _, ok := chartAggregationKey(value, period)
	return key, ok
}

func mergeChartMissingIntervals(groups ...[]ChartMissingInterval) []ChartMissingInterval {
	seen := make(map[string]struct{})
	result := make([]ChartMissingInterval, 0)
	for _, group := range groups {
		for _, item := range group {
			key := fmt.Sprintf("%d/%d/%s", item.From.UnixNano(), item.To.UnixNano(), item.Reason)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, item)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].From.Equal(result[j].From) {
			return result[i].To.Before(result[j].To)
		}
		return result[i].From.Before(result[j].From)
	})
	return result
}

func aggregateChartSource(values []string) string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return strings.Join(unique, "+")
}
