package data

import (
	"context"
	"time"

	"go-stock/backend/marketdata"
)

// CompatibilityMarketDataReader exposes existing caches through the new
// provider-neutral read contracts. It is intentionally cache-only.
type CompatibilityMarketDataReader struct{}

func NewCompatibilityMarketDataReader() CompatibilityMarketDataReader {
	return CompatibilityMarketDataReader{}
}

func (CompatibilityMarketDataReader) DailyBars(ctx context.Context, request marketdata.DailyBarsRequest) ([]marketdata.DailyBar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := listDailyBarsFromCache(request.Symbol, request.Start, request.End)
	if err != nil {
		return nil, err
	}
	availableAt := compatibilityObservationTime(request.AsOf, request.End)
	result := make([]marketdata.DailyBar, 0, len(rows))
	for _, row := range rows {
		result = append(result, marketdata.DailyBar{
			Symbol: request.Symbol, TradeDate: row.TradeDate, Open: row.Open, High: row.High,
			Low: row.Low, Close: row.Close, Volume: row.Volume, Amount: row.Amount,
			Adjustment: marketdata.AdjustmentForward, Completed: true, Source: "legacy_daily_cache",
			SourceAt: row.TradeDate, AvailableAt: availableAt,
		})
	}
	return result, nil
}

func (CompatibilityMarketDataReader) MinuteBars(ctx context.Context, request marketdata.MinuteBarsRequest) ([]marketdata.MinuteBar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := listMinuteBarsFromCache(request.Symbol, request.Start, request.End)
	if err != nil {
		return nil, err
	}
	availableAt := compatibilityObservationTime(request.AsOf, request.End)
	result := make([]marketdata.MinuteBar, 0, len(rows))
	for index, row := range rows {
		result = append(result, marketdata.MinuteBar{
			Symbol: request.Symbol, Index: index, IntervalMinutes: 1,
			Start: row.TradeTime, End: row.TradeTime.Add(time.Minute), Open: row.Open, High: row.High,
			Low: row.Low, Close: row.Close, Volume: row.Volume, Amount: row.Amount,
			Completed: true, Source: "legacy_minute_cache", SourceAt: row.TradeTime, AvailableAt: availableAt,
		})
	}
	return result, nil
}

func compatibilityObservationTime(asOf, fallback time.Time) time.Time {
	if !asOf.IsZero() {
		return asOf
	}
	return fallback
}

var (
	_ marketdata.DailyBarReader  = CompatibilityMarketDataReader{}
	_ marketdata.MinuteBarReader = CompatibilityMarketDataReader{}
)
