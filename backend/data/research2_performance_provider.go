package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/instruments"
	"go-stock/backend/research2"
	"go-stock/internal/recommendationchart"

	"gorm.io/gorm"
)

type research2PerformanceProvider struct {
	chart    *ResearchChartProvider
	daily    *ChartService
	calendar research2.Calendar
}

func newResearch2PerformanceProvider(mainDB, minuteDB *gorm.DB, chart *ResearchChartProvider, calendar research2.Calendar) *research2PerformanceProvider {
	return &research2PerformanceProvider{chart: chart, daily: NewChartServiceWithStorage(mainDB, minuteDB), calendar: calendar}
}

func (provider *research2PerformanceProvider) BuyDayData(ctx context.Context, item research2.Recommendation) (research2.BuyDayMarketData, error) {
	if provider == nil || provider.chart == nil || provider.calendar == nil || item.BuyAt == nil {
		return research2.BuyDayMarketData{}, errors.New("buy-day history provider is unavailable")
	}
	buyDay := item.BuyAt.In(cnLocation())
	previousDay, err := provider.previousTradingDay(ctx, buyDay)
	if err != nil {
		return research2.BuyDayMarketData{}, err
	}
	dailyRows, dailySources, dailyErr := provider.DailyCloses(ctx, item.StockCode, buyDay.AddDate(0, 0, -40), buyDay)
	if dailyErr != nil && len(dailyRows) == 0 {
		return research2.BuyDayMarketData{SourceStatusJSON: dailySources}, dailyErr
	}
	from := time.Date(buyDay.Year(), buyDay.Month(), buyDay.Day(), 9, 30, 0, 0, cnLocation())
	to := time.Date(buyDay.Year(), buyDay.Month(), buyDay.Day(), 15, 0, 0, 0, cnLocation())
	keys, keyErr := chartMinuteCacheKeys(item.StockCode)
	if keyErr != nil {
		return research2.BuyDayMarketData{}, keyErr
	}
	var snapshot recommendationchart.ProviderSnapshot
	if provider.chart.chartAnyCacheWindowCovered(keys, from, to) {
		snapshot, err = provider.chart.LoadCached(ctx, item.StockCode, from, to)
	} else {
		snapshot, err = provider.chart.RefreshHistorical(ctx, item.StockCode, from, to, []string{buyDay.Format("2006-01-02")})
	}
	result := research2.BuyDayMarketData{SourceStatusJSON: research2PerformanceSourceJSON(snapshot.ProviderErrors, snapshot.Bars, dailySources)}
	if err != nil {
		return result, err
	}
	for _, row := range dailyRows {
		if row.TradingDate == previousDay.Format("2006-01-02") && row.Close > 0 {
			result.PreviousClose = row.Close
		}
	}
	for _, bar := range snapshot.Bars {
		local := bar.At.In(cnLocation())
		if local.Format("2006-01-02") == buyDay.Format("2006-01-02") {
			result.Bars = append(result.Bars, research2.PerformanceBar{At: bar.At, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, Amount: bar.Amount, Source: bar.Source})
		}
	}
	if result.PreviousClose <= 0 {
		return result, errors.New("verified previous trading-day close is unavailable")
	}
	listingDays := 0
	for _, row := range dailyRows {
		if row.TradingDate <= buyDay.Format("2006-01-02") {
			listingDays++
		}
	}
	rate, limited := research2.MainlandLimitRate(item.StockCode, item.StockName, listingDays)
	if !limited {
		result.NoLimitReason = "fewer than six verified pre-buy daily sessions; price-limit rule is unavailable"
	} else {
		result.LimitRate = rate
	}
	return result, nil
}

func (provider *research2PerformanceProvider) DailyCloses(ctx context.Context, code string, from, to time.Time) ([]research2.DailyClose, string, error) {
	if provider == nil || provider.daily == nil {
		return nil, "[]", errors.New("daily history provider is unavailable")
	}
	instrument, err := instruments.ParseInstrumentID(code, "stock", "")
	if err != nil {
		return nil, "[]", err
	}
	days := int(to.Sub(from).Hours()/24) + 20
	from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, cnLocation())
	to = time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, cnLocation())
	request := ChartRequest{Instrument: instrument, Period: ChartPeriodDay, Adjustment: ChartAdjustmentNone, From: from, To: to, Limit: maxInt(days, 30)}
	envelope := provider.daily.Chart(ctx, request)
	rows := make([]research2.DailyClose, 0, len(envelope.Data.Bars))
	for _, bar := range envelope.Data.Bars {
		if bar.Close > 0 {
			rows = append(rows, research2.DailyClose{TradingDate: bar.At.In(cnLocation()).Format("2006-01-02"), Close: bar.Close, Source: bar.Source})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TradingDate < rows[j].TradingDate })
	status, _ := json.Marshal(map[string]any{"status": envelope.Status, "source": envelope.Source, "sources": envelope.Sources, "errors": envelope.Errors})
	if len(rows) == 0 {
		return rows, string(status), fmt.Errorf("daily close history is unavailable for %s", code)
	}
	return rows, string(status), nil
}

func (provider *research2PerformanceProvider) previousTradingDay(ctx context.Context, day time.Time) (time.Time, error) {
	for candidate, checked := day.AddDate(0, 0, -1), 0; checked < 20; candidate, checked = candidate.AddDate(0, 0, -1), checked+1 {
		ok, err := provider.calendar.IsTradingDay(ctx, candidate)
		if err != nil {
			return time.Time{}, err
		}
		if ok {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("previous trading day was not found")
}

func research2PerformanceSourceJSON(providerErrors []recommendationchart.ProviderError, bars []recommendationchart.MinuteBar, extra ...string) string {
	sources := map[string]struct{}{}
	for _, bar := range bars {
		if source := strings.TrimSpace(bar.Source); source != "" {
			sources[source] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(sources))
	for source := range sources {
		ordered = append(ordered, source)
	}
	sort.Strings(ordered)
	encoded, err := json.Marshal(map[string]any{"sources": ordered, "errors": providerErrors, "daily": extra})
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
