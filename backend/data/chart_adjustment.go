package data

import (
	"context"
	"fmt"
	"time"

	"go-stock/backend/marketdata"
)

func (s *ChartService) adjustMinuteBars(ctx context.Context, request ChartRequest, raw []ChartBar) ([]ChartBar, []marketdata.SourceState, []marketdata.DataError) {
	if request.Adjustment == ChartAdjustmentNone {
		return raw, []marketdata.SourceState{}, []marketdata.DataError{}
	}
	dailyRaw := request
	dailyRaw.Period = ChartPeriodDay
	dailyRaw.Adjustment = ChartAdjustmentNone
	dailyRaw.Limit = maxInt(request.Limit, 300)
	dailyRaw.From = time.Date(request.From.Year(), request.From.Month(), request.From.Day(), 0, 0, 0, 0, request.From.Location())
	dailyRaw.To = time.Date(request.To.Year(), request.To.Month(), request.To.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), request.To.Location())
	rawDaily, _, _, rawSources, rawErrors := s.collectChartProviders(ctx, dailyRaw)
	dailyAdjusted := dailyRaw
	dailyAdjusted.Adjustment = request.Adjustment
	adjustedDaily, _, _, adjustedSources, adjustedErrors := s.collectChartProviders(ctx, dailyAdjusted)
	sources := append(rawSources, adjustedSources...)
	errorsOut := append(rawErrors, adjustedErrors...)
	rawClose := make(map[string]float64, len(rawDaily))
	adjustedClose := make(map[string]float64, len(adjustedDaily))
	for _, bar := range rawDaily {
		rawClose[chartShanghaiTime(bar.At).Format(time.DateOnly)] = bar.Close
	}
	for _, bar := range adjustedDaily {
		adjustedClose[chartShanghaiTime(bar.At).Format(time.DateOnly)] = bar.Close
	}
	result := make([]ChartBar, 0, len(raw))
	missingDates := make(map[string]struct{})
	for _, bar := range raw {
		date := chartShanghaiTime(bar.At).Format(time.DateOnly)
		base, baseOK := rawClose[date]
		adjusted, adjustedOK := adjustedClose[date]
		if !baseOK || !adjustedOK || base <= 0 || adjusted <= 0 {
			missingDates[date] = struct{}{}
			continue
		}
		ratio := adjusted / base
		bar.Open *= ratio
		bar.High *= ratio
		bar.Low *= ratio
		bar.Close *= ratio
		bar.Source = fmt.Sprintf("%s|adjustment=%s", bar.Source, request.Adjustment)
		result = append(result, bar)
	}
	for date := range missingDates {
		errorsOut = append(errorsOut, marketdata.DataError{Provider: "adjustment", Code: "factor_unavailable", Message: date + " 缺少可验证的复权比例"})
	}
	return result, sources, errorsOut
}
