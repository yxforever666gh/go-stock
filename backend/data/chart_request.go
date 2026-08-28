package data

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxChartBars           = 5000
	maxChartBaseDailyBars  = 12000
	maxChartBaseMinuteBars = 350000
)

var chartPeriods = map[string]time.Duration{
	ChartPeriod1Minute:  time.Minute,
	ChartPeriod5Minute:  5 * time.Minute,
	ChartPeriod15Minute: 15 * time.Minute,
	ChartPeriod30Minute: 30 * time.Minute,
	ChartPeriod60Minute: 60 * time.Minute,
}

func NormalizeChartRequest(request ChartRequest, now time.Time) (ChartRequest, error) {
	if request.Instrument.AssetType == "" || request.Instrument.Code == "" {
		return ChartRequest{}, errors.New("instrument is required")
	}
	instrument, err := ParseInstrumentID(request.Instrument.Code, request.Instrument.AssetType, request.Instrument.Market)
	if err != nil {
		return ChartRequest{}, err
	}
	request.Instrument = instrument
	request.Period = strings.ToLower(strings.TrimSpace(request.Period))
	if request.Period == "" {
		request.Period = ChartPeriodDay
	}
	if !validChartPeriod(request.Period) {
		return ChartRequest{}, fmt.Errorf("unsupported chart period %q", request.Period)
	}
	request.Adjustment = strings.ToLower(strings.TrimSpace(request.Adjustment))
	if request.Adjustment == "" {
		if instrument.AssetType == "stock" {
			request.Adjustment = ChartAdjustmentQFQ
		} else {
			request.Adjustment = ChartAdjustmentNone
		}
	}
	if request.Adjustment != ChartAdjustmentNone && request.Adjustment != ChartAdjustmentQFQ && request.Adjustment != ChartAdjustmentHFQ {
		return ChartRequest{}, fmt.Errorf("unsupported chart adjustment %q", request.Adjustment)
	}
	if instrument.AssetType == "index" && request.Adjustment != ChartAdjustmentNone {
		return ChartRequest{}, errors.New("indexes only support adjustment=none")
	}
	if request.Limit <= 0 {
		request.Limit = 500
	}
	if request.Limit > maxChartBars {
		return ChartRequest{}, fmt.Errorf("limit must not exceed %d", maxChartBars)
	}
	if now.IsZero() {
		now = time.Now()
	}
	request.To = chartShanghaiTime(request.To)
	if request.To.IsZero() {
		request.To = chartShanghaiTime(now)
	}
	request.From = chartShanghaiTime(request.From)
	if request.From.IsZero() {
		request.From = defaultChartFrom(request.To, request.Period, request.Limit)
	}
	if request.From.After(request.To) {
		return ChartRequest{}, errors.New("from must not be after to")
	}
	return request, nil
}

func chartShanghaiTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.In(cnLocation())
}

func validChartPeriod(period string) bool {
	if _, ok := chartPeriods[period]; ok {
		return true
	}
	switch period {
	case ChartPeriodDay, ChartPeriodWeek, ChartPeriodMonth, ChartPeriodQuarter, ChartPeriodYear:
		return true
	default:
		return false
	}
}

func baseChartPeriod(period string) string {
	if _, ok := chartPeriods[period]; ok {
		return ChartPeriod1Minute
	}
	return ChartPeriodDay
}

// chartBaseBarLimit converts the requested output-bar limit into the amount
// of raw observations needed by the base provider/cache. The public limit is
// still capped at maxChartBars; these larger internal limits are bounded so a
// long-period request cannot open an unbounded provider pull.
func chartBaseBarLimit(period string, target int) int {
	if target <= 0 {
		target = 500
	}
	if duration, minutePeriod := chartPeriods[period]; minutePeriod {
		factor := int(duration / time.Minute)
		return boundedChartScale(target, factor, maxChartBaseMinuteBars)
	}
	factor := 1
	switch period {
	case ChartPeriodWeek:
		factor = 6
	case ChartPeriodMonth:
		factor = 23
	case ChartPeriodQuarter:
		factor = 66
	case ChartPeriodYear:
		factor = 260
	}
	return boundedChartScale(target, factor, maxChartBaseDailyBars)
}

func boundedChartScale(value, factor, maximum int) int {
	if value <= 0 || factor <= 0 {
		return value
	}
	if value > maximum/factor {
		return maximum
	}
	scaled := value * factor
	if scaled > maximum {
		return maximum
	}
	return scaled
}

func defaultChartFrom(to time.Time, period string, limit int) time.Time {
	if duration, ok := chartPeriods[period]; ok {
		// Include lunch/weekend slack without opening an unbounded provider pull.
		return to.Add(-duration * time.Duration(limit*3))
	}
	switch period {
	case ChartPeriodWeek:
		return to.AddDate(0, 0, -7*limit)
	case ChartPeriodMonth:
		return to.AddDate(0, -limit, 0)
	case ChartPeriodQuarter:
		return to.AddDate(0, -3*limit, 0)
	case ChartPeriodYear:
		return to.AddDate(-limit, 0, 0)
	default:
		return to.AddDate(0, 0, -limit*2)
	}
}
