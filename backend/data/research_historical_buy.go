package data

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/research"
)

type HistoricalBuyMarketEvidence struct {
	EntryQuote     research.Quote                `json:"entryQuote"`
	EntrySource    string                        `json:"entrySource"`
	MarkQuote      *research.Quote               `json:"markQuote,omitempty"`
	ProviderErrors []research.ChartProviderError `json:"providerErrors"`
}

type historicalChartRefresher interface {
	Refresh(context.Context, string, time.Time, time.Time, []string) (research.ChartProviderSnapshot, error)
}

type historicalDailyBar struct {
	Date  string
	Open  float64
	Close float64
}

type historicalDailyLoader func(string) ([]historicalDailyBar, error)

// ResolveHistoricalBuyMarketEvidence follows the configured minute provider
// priority and only falls back to the explicitly unadjusted Sina daily open
// after all minute sources failed to return a valid bar for the requested day.
func ResolveHistoricalBuyMarketEvidence(ctx context.Context, code, name, tradingDate string, now time.Time) (HistoricalBuyMarketEvidence, error) {
	quotes := NewResearchQuoteProvider()
	provider := NewResearchChartProvider(quotes)
	return resolveHistoricalBuyMarketEvidence(ctx, provider, loadSinaHistoricalDailyBars, code, name, tradingDate, now)
}

func resolveHistoricalBuyMarketEvidence(ctx context.Context, provider historicalChartRefresher, daily historicalDailyLoader, code, name, tradingDate string, now time.Time) (HistoricalBuyMarketEvidence, error) {
	normalized, ok := research.NormalizeMainlandCode(code)
	if !ok {
		return HistoricalBuyMarketEvidence{}, errors.New("only Shanghai/Shenzhen A shares are supported")
	}
	day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(tradingDate), cnLocation())
	if err != nil {
		return HistoricalBuyMarketEvidence{}, fmt.Errorf("invalid historical trading date: %w", err)
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, cnLocation())
	end := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, cnLocation())
	snapshot, refreshErr := provider.Refresh(ctx, normalized, start, now, []string{tradingDate})
	errorsOut := append([]research.ChartProviderError(nil), snapshot.ProviderErrors...)
	if refreshErr != nil {
		errorsOut = append(errorsOut, research.ChartProviderError{Provider: "minute_refresh", Message: sanitizeChartError(refreshErr)})
	}
	valid := make([]research.ChartMinuteBar, 0)
	for _, bar := range snapshot.Bars {
		local := bar.At.In(cnLocation())
		if local.Format("2006-01-02") != tradingDate || local.Before(start) || local.After(end) ||
			bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Close <= 0 || strings.TrimSpace(bar.Source) == "" {
			continue
		}
		valid = append(valid, bar)
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].At.Before(valid[j].At) })
	dailyBars, dailyErr := daily(normalized)
	previousClose := historicalPreviousClose(dailyBars, tradingDate)
	market := "SZ"
	if strings.HasPrefix(normalized, "sh") {
		market = "SH"
	}
	result := HistoricalBuyMarketEvidence{ProviderErrors: errorsOut}
	if snapshot.Quote != nil {
		quoteCode, validCode := research.NormalizeMainlandCode(snapshot.Quote.Code)
		if validCode && quoteCode == normalized && strings.TrimSpace(snapshot.Quote.Name) != "" && snapshot.Quote.Price > 0 {
			copyQuote := *snapshot.Quote
			result.MarkQuote = &copyQuote
		}
	}
	if len(valid) > 0 {
		first := valid[0]
		result.EntryQuote = historicalEntryQuote(normalized, name, market, first.Open, previousClose, first.At, false)
		result.EntrySource = first.Source
		return result, nil
	}
	if dailyErr != nil {
		return HistoricalBuyMarketEvidence{}, fmt.Errorf("all minute sources failed and daily open fallback failed: %w", dailyErr)
	}
	for _, bar := range dailyBars {
		if bar.Date != tradingDate || bar.Open <= 0 {
			continue
		}
		at := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, cnLocation())
		result.EntryQuote = historicalEntryQuote(normalized, name, market, bar.Open, previousClose, at, false)
		result.EntrySource = "sina-unadjusted-daily-open"
		result.ProviderErrors = append(result.ProviderErrors, research.ChartProviderError{Provider: "daily_open_fallback", Message: "分钟来源均未返回有效数据，使用新浪未复权日线开盘价"})
		return result, nil
	}
	return HistoricalBuyMarketEvidence{}, fmt.Errorf("all enabled minute sources and Sina daily open returned no valid data for %s", tradingDate)
}

func historicalEntryQuote(code, name, market string, price, previousClose float64, at time.Time, suspended bool) research.Quote {
	limitRate := 0.10
	if strings.HasPrefix(code, "sh68") || strings.HasPrefix(code, "sz30") {
		limitRate = 0.20
	}
	if strings.Contains(strings.ToUpper(name), "ST") {
		limitRate = 0.05
	}
	limitUp, limitDown := false, false
	if previousClose > 0 {
		up := math.Floor(previousClose*(1+limitRate)*100+0.5) / 100
		down := math.Floor(previousClose*(1-limitRate)*100+0.5) / 100
		limitUp, limitDown = price >= up-0.001, price <= down+0.001
	}
	return research.Quote{Code: code, Name: strings.TrimSpace(name), Market: market, Price: price,
		PreviousClose: previousClose, At: at, Suspended: suspended, LimitUp: limitUp, LimitDown: limitDown}
}

func loadSinaHistoricalDailyBars(code string) ([]historicalDailyBar, error) {
	rows := NewStockDataApi().GetKLineData(code, "240", 120)
	if rows == nil || len(*rows) == 0 {
		return nil, errors.New("Sina daily K returned empty data")
	}
	result := make([]historicalDailyBar, 0, len(*rows))
	for _, row := range *rows {
		date := strings.TrimSpace(row.Day)
		if len(date) >= 10 {
			date = date[:10]
		}
		open, openErr := strconv.ParseFloat(strings.TrimSpace(row.Open), 64)
		closePrice, closeErr := strconv.ParseFloat(strings.TrimSpace(row.Close), 64)
		if openErr != nil || closeErr != nil || open <= 0 || closePrice <= 0 {
			continue
		}
		result = append(result, historicalDailyBar{Date: date, Open: open, Close: closePrice})
	}
	if len(result) == 0 {
		return nil, errors.New("Sina daily K contained no valid unadjusted bars")
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Date < result[j].Date })
	return result, nil
}

func historicalPreviousClose(rows []historicalDailyBar, date string) float64 {
	previous := 0.0
	for _, row := range rows {
		if row.Date >= date {
			break
		}
		if row.Close > 0 {
			previous = row.Close
		}
	}
	return previous
}
