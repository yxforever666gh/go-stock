// Package recommendationchart builds recommendation price and return charts
// from domain-neutral recommendation, trade, position, and market snapshots.
package recommendationchart

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/internal/trading"
)

var (
	ErrProviderUnavailable = errors.New("recommendation chart provider is unavailable")
	ErrRefreshInProgress   = errors.New("recommendation chart refresh is already in progress")
)

var shanghaiLocation = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}()

type MinuteBar struct {
	At           time.Time `json:"at"`
	Open         float64   `json:"open"`
	High         float64   `json:"high"`
	Low          float64   `json:"low"`
	Close        float64   `json:"close"`
	Volume       float64   `json:"volume"`
	Amount       float64   `json:"amount"`
	Source       string    `json:"source"`
	NetPnL       float64   `json:"netPnl"`
	NetYieldRate float64   `json:"netYieldRate"`
}

type ProviderError struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

type Quote struct {
	Price         float64
	PreviousClose float64
	At            time.Time
}

type ProviderSnapshot struct {
	Bars           []MinuteBar
	Quote          *Quote
	RefreshedAt    time.Time
	ProviderErrors []ProviderError
}

type Provider interface {
	LoadCached(context.Context, string, time.Time, time.Time) (ProviderSnapshot, error)
	Refresh(context.Context, string, time.Time, time.Time, []string) (ProviderSnapshot, error)
}

type Calendar interface {
	IsTradingDay(context.Context, time.Time) (bool, error)
}

type WeekdayCalendar struct{}

func (WeekdayCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	weekday := shanghaiTime(value).Weekday()
	return weekday != time.Saturday && weekday != time.Sunday, nil
}

type Detail struct {
	RecommendationID string
	StockCode        string
	StockName        string
	SignalAt         time.Time
	Trades           []Trade
	Position         *Position
}

type Trade struct {
	Side           string     `json:"side"`
	TradedAt       time.Time  `json:"tradedAt"`
	MarketPrice    float64    `json:"marketPrice"`
	ExecutionPrice float64    `json:"executionPrice"`
	Quantity       int64      `json:"quantity"`
	TotalFees      float64    `json:"totalFees"`
	NetCashFlow    float64    `json:"netCashFlow"`
	MarkerAt       *time.Time `json:"markerAt,omitempty"`
	MarkerSnapped  bool       `json:"markerSnapped"`
}

type Position struct {
	CurrentPrice   float64
	CurrentPriceAt *time.Time
	Status         string
	ExitAt         *time.Time
}

type Session struct {
	Date          string  `json:"date"`
	PreviousClose float64 `json:"previousClose"`
	Status        string  `json:"status"`
}

type Chart struct {
	RecommendationID    string          `json:"recommendationId"`
	StockCode           string          `json:"stockCode"`
	StockName           string          `json:"stockName"`
	Status              string          `json:"status"`
	RangeFrom           time.Time       `json:"rangeFrom"`
	RangeTo             time.Time       `json:"rangeTo"`
	RefreshedAt         time.Time       `json:"refreshedAt"`
	QuoteAt             *time.Time      `json:"quoteAt,omitempty"`
	CurrentPrice        float64         `json:"currentPrice"`
	CurrentNetPnL       float64         `json:"currentNetPnl"`
	CurrentNetYieldRate float64         `json:"currentNetYieldRate"`
	MissingSessions     []string        `json:"missingSessions"`
	ProviderErrors      []ProviderError `json:"providerErrors"`
	Sessions            []Session       `json:"sessions"`
	Bars                []MinuteBar     `json:"bars"`
	Trades              []Trade         `json:"trades"`
}

type Engine struct {
	provider   Provider
	calendar   Calendar
	now        func() time.Time
	mu         sync.Mutex
	refreshing map[string]struct{}
}

func NewEngine(provider Provider, calendar Calendar, clocks ...func() time.Time) *Engine {
	if calendar == nil {
		calendar = WeekdayCalendar{}
	}
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	return &Engine{provider: provider, calendar: calendar, now: now, refreshing: make(map[string]struct{})}
}

func (e *Engine) Chart(ctx context.Context, detail Detail, refresh bool) (Chart, error) {
	if e == nil || e.provider == nil {
		return Chart{}, ErrProviderUnavailable
	}
	recommendationID := strings.TrimSpace(detail.RecommendationID)
	if recommendationID == "" {
		return Chart{}, errors.New("recommendationId is required")
	}
	if refresh {
		if !e.beginRefresh(recommendationID) {
			return Chart{}, ErrRefreshInProgress
		}
		defer e.endRefresh(recommendationID)
	}
	now := e.now()
	from, to := chartRange(detail, now)
	sessionDates, err := chartTradingSessions(ctx, e.calendar, from, to, refresh)
	calendarFallback := false
	if err != nil {
		if !refresh {
			return Chart{}, err
		}
		sessionDates, err = chartTradingSessions(ctx, WeekdayCalendar{}, from, to, false)
		if err != nil {
			return Chart{}, err
		}
		calendarFallback = true
	}
	lookupFrom := from.AddDate(0, 0, -10)
	var snapshot ProviderSnapshot
	if refresh {
		snapshot, err = e.provider.Refresh(ctx, detail.StockCode, lookupFrom, to, sessionDates)
	} else {
		snapshot, err = e.provider.LoadCached(ctx, detail.StockCode, lookupFrom, to)
	}
	if err != nil {
		return Chart{}, err
	}
	if calendarFallback {
		snapshot.ProviderErrors = append(snapshot.ProviderErrors, ProviderError{Provider: "trade_calendar",
			Message: "严格交易日历暂时不可用，已按工作日继续补齐；节假日覆盖状态可能不完整"})
	}
	return buildChart(detail, snapshot, from, to, sessionDates), nil
}

func (e *Engine) beginRefresh(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.refreshing[id]; exists {
		return false
	}
	e.refreshing[id] = struct{}{}
	return true
}

func (e *Engine) endRefresh(id string) {
	e.mu.Lock()
	delete(e.refreshing, id)
	e.mu.Unlock()
}

func chartRange(detail Detail, now time.Time) (time.Time, time.Time) {
	location := shanghaiTime(now).Location()
	anchor := detail.SignalAt
	foundBuy := false
	for _, trade := range detail.Trades {
		if trade.Side == "buy" && (!foundBuy || trade.TradedAt.Before(anchor)) {
			anchor = trade.TradedAt
			foundBuy = true
		}
	}
	if anchor.IsZero() {
		anchor = now
	}
	anchor = anchor.In(location)
	from := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 9, 30, 0, 0, location)
	endAnchor := now.In(location)
	if latestSell, ok := latestSell(detail.Trades); ok && detail.Position == nil {
		endAnchor = latestSell.In(location)
	} else if detail.Position != nil && detail.Position.Status == "closed" && detail.Position.ExitAt != nil {
		endAnchor = detail.Position.ExitAt.In(location)
	} else if len(detail.Trades) == 0 {
		endAnchor = anchor
	}
	to := time.Date(endAnchor.Year(), endAnchor.Month(), endAnchor.Day(), 15, 0, 0, 0, location)
	if sameDate(endAnchor, now.In(location)) {
		to = normalizeCurrentEnd(endAnchor)
	}
	if to.Before(from) {
		to = from
	}
	return from, to
}

func normalizeCurrentEnd(value time.Time) time.Time {
	value = shanghaiTime(value)
	year, month, day := value.Date()
	at := func(hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, value.Location())
	}
	switch {
	case value.Before(at(9, 30)):
		return value
	case !value.After(at(11, 30)):
		return value
	case value.Before(at(13, 0)):
		return at(11, 30)
	case !value.After(at(15, 0)):
		return value
	default:
		return at(15, 0)
	}
}

func latestSell(trades []Trade) (time.Time, bool) {
	var latest time.Time
	for _, trade := range trades {
		if trade.Side == "sell" && trade.TradedAt.After(latest) {
			latest = trade.TradedAt
		}
	}
	return latest, !latest.IsZero()
}

type cachedCalendar interface {
	IsTradingDayCached(time.Time) (bool, bool)
}

func chartTradingSessions(ctx context.Context, calendar Calendar, from, to time.Time, allowNetwork bool) ([]string, error) {
	location := from.Location()
	start := time.Date(from.Year(), from.Month(), from.Day(), 12, 0, 0, 0, location)
	end := time.Date(to.Year(), to.Month(), to.Day(), 12, 0, 0, 0, location)
	result := make([]string, 0, int(end.Sub(start).Hours()/24)+1)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		if sameDate(day, to) {
			localTo := shanghaiTime(to)
			if localTo.Before(time.Date(localTo.Year(), localTo.Month(), localTo.Day(), 9, 30, 0, 0, localTo.Location())) {
				continue
			}
		}
		tradingDay := false
		if !allowNetwork {
			if cached, ok := calendar.(cachedCalendar); ok {
				if value, known := cached.IsTradingDayCached(day); known {
					tradingDay = value
				} else {
					tradingDay = day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
				}
			} else {
				tradingDay = day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
			}
		} else {
			value, err := calendar.IsTradingDay(ctx, day)
			if err != nil {
				return nil, err
			}
			tradingDay = value
		}
		if tradingDay {
			result = append(result, day.Format("2006-01-02"))
		}
	}
	return result, nil
}

func buildChart(detail Detail, snapshot ProviderSnapshot, from, to time.Time, sessionDates []string) Chart {
	allBars := append([]MinuteBar(nil), snapshot.Bars...)
	sort.SliceStable(allBars, func(i, j int) bool { return allBars[i].At.Before(allBars[j].At) })
	bars := make([]MinuteBar, 0, len(allBars))
	for _, bar := range allBars {
		if bar.At.Before(from) || bar.At.After(to) || !validBar(bar) {
			continue
		}
		bars = append(bars, bar)
	}
	trades := append([]Trade(nil), detail.Trades...)
	sort.SliceStable(trades, func(i, j int) bool { return trades[i].TradedAt.Before(trades[j].TradedAt) })
	applyReturns(bars, trades)

	result := Chart{RecommendationID: detail.RecommendationID, StockCode: detail.StockCode, StockName: detail.StockName,
		RangeFrom: from, RangeTo: to, RefreshedAt: snapshot.RefreshedAt, MissingSessions: []string{},
		ProviderErrors: snapshot.ProviderErrors, Bars: bars, Trades: chartTrades(trades, bars)}
	if result.RefreshedAt.IsZero() && len(bars) > 0 {
		result.RefreshedAt = bars[len(bars)-1].At
	}
	result.Sessions, result.MissingSessions = chartSessions(sessionDates, allBars, from, to)
	applyQuotePreviousClose(result.Sessions, snapshot.Quote)
	result.Status = coverageStatus(result.Sessions, len(bars))

	if snapshot.Quote != nil && snapshot.Quote.Price > 0 {
		result.CurrentPrice, result.QuoteAt = snapshot.Quote.Price, &snapshot.Quote.At
	} else if len(bars) > 0 {
		latest := bars[len(bars)-1]
		result.CurrentPrice, result.QuoteAt = latest.Close, &latest.At
	} else if detail.Position != nil && detail.Position.CurrentPrice > 0 {
		result.CurrentPrice, result.QuoteAt = detail.Position.CurrentPrice, detail.Position.CurrentPriceAt
	}
	result.CurrentNetPnL, result.CurrentNetYieldRate = currentReturn(trades, result.CurrentPrice)
	return result
}

func applyQuotePreviousClose(sessions []Session, quote *Quote) {
	if quote == nil || quote.PreviousClose <= 0 || quote.At.IsZero() {
		return
	}
	quoteDate := shanghaiTime(quote.At).Format("2006-01-02")
	for index := range sessions {
		if sessions[index].Date == quoteDate && sessions[index].PreviousClose <= 0 {
			sessions[index].PreviousClose = quote.PreviousClose
			return
		}
	}
}

func validBar(bar MinuteBar) bool {
	return !bar.At.IsZero() && bar.Open > 0 && bar.High > 0 && bar.Low > 0 && bar.Close > 0 && bar.High >= bar.Low
}

func chartSessions(dates []string, allBars []MinuteBar, from, to time.Time) ([]Session, []string) {
	byDate := make(map[string][]MinuteBar, len(dates))
	for _, bar := range allBars {
		if validBar(bar) {
			key := shanghaiTime(bar.At).Format("2006-01-02")
			byDate[key] = append(byDate[key], bar)
		}
	}
	result := make([]Session, 0, len(dates))
	missing := make([]string, 0)
	var previousClose float64
	for _, date := range dates {
		rows := byDate[date]
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].At.Before(rows[j].At) })
		if previousClose == 0 {
			previousClose = previousCloseBefore(allBars, date)
		}
		status := "missing"
		if len(rows) > 0 {
			expectedEnd := sessionExpectedEnd(date, to)
			first, last := shanghaiTime(rows[0].At), shanghaiTime(rows[len(rows)-1].At)
			startLimit := time.Date(first.Year(), first.Month(), first.Day(), 9, 36, 0, 0, first.Location())
			if !first.After(startLimit) && !last.Before(expectedEnd.Add(-5*time.Minute)) && !sessionHasGap(rows) {
				status = "complete"
			} else {
				status = "partial"
			}
		}
		result = append(result, Session{Date: date, PreviousClose: previousClose, Status: status})
		if status == "missing" {
			missing = append(missing, date)
		}
		if len(rows) > 0 {
			previousClose = rows[len(rows)-1].Close
		}
	}
	return result, missing
}

func sessionHasGap(rows []MinuteBar) bool {
	if len(rows) < 2 {
		return false
	}
	previous := shanghaiTime(rows[0].At)
	for _, row := range rows[1:] {
		current := shanghaiTime(row.At)
		if sameTradingSegment(previous, current) && current.Sub(previous) > 5*time.Minute {
			return true
		}
		previous = current
	}
	return false
}

func previousCloseBefore(bars []MinuteBar, date string) float64 {
	var latest time.Time
	value := 0.0
	for _, bar := range bars {
		if shanghaiTime(bar.At).Format("2006-01-02") >= date || !validBar(bar) {
			continue
		}
		if bar.At.After(latest) {
			latest, value = bar.At, bar.Close
		}
	}
	return value
}

func sessionExpectedEnd(date string, rangeTo time.Time) time.Time {
	location := rangeTo.Location()
	day, _ := time.ParseInLocation("2006-01-02", date, location)
	expected := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, location)
	if date == rangeTo.In(location).Format("2006-01-02") && rangeTo.Before(expected) {
		expected = rangeTo
	}
	return expected
}

func coverageStatus(sessions []Session, bars int) string {
	if bars == 0 {
		return "empty"
	}
	for _, session := range sessions {
		if session.Status != "complete" {
			return "partial"
		}
	}
	return "complete"
}

func applyReturns(bars []MinuteBar, trades []Trade) {
	for index := range bars {
		barEnd := bars[index].At.Truncate(time.Minute).Add(time.Minute)
		buyOut, soldIn, quantity := cashState(trades, barEnd)
		if buyOut <= 0 {
			continue
		}
		value := soldIn
		if quantity > 0 {
			value += trading.CalculateSellCost(bars[index].Close, quantity).NetCashFlow
		}
		bars[index].NetPnL = value - buyOut
		bars[index].NetYieldRate = bars[index].NetPnL / buyOut
	}
}

func currentReturn(trades []Trade, price float64) (float64, float64) {
	buyOut, soldIn, quantity := cashState(trades, time.Time{})
	if buyOut <= 0 {
		return 0, 0
	}
	if quantity > 0 && price <= 0 {
		return 0, 0
	}
	value := soldIn
	if quantity > 0 && price > 0 {
		value += trading.CalculateSellCost(price, quantity).NetCashFlow
	}
	pnl := value - buyOut
	return pnl, pnl / buyOut
}

func cashState(trades []Trade, before time.Time) (float64, float64, int64) {
	buyOut, soldIn := 0.0, 0.0
	quantity := int64(0)
	for _, trade := range trades {
		if !before.IsZero() && !trade.TradedAt.Before(before) {
			continue
		}
		switch trade.Side {
		case "buy":
			buyOut += math.Abs(trade.NetCashFlow)
			quantity += trade.Quantity
		case "sell":
			soldIn += trade.NetCashFlow
			quantity -= trade.Quantity
		}
	}
	if quantity < 0 {
		quantity = 0
	}
	return buyOut, soldIn, quantity
}

func chartTrades(trades []Trade, bars []MinuteBar) []Trade {
	result := make([]Trade, 0, len(trades))
	for _, trade := range trades {
		trade.MarkerAt = nil
		trade.MarkerSnapped = false
		if marker, ok := nearestMarker(trade.TradedAt, bars); ok {
			trade.MarkerAt = &marker
			trade.MarkerSnapped = !marker.Equal(trade.TradedAt)
		}
		result = append(result, trade)
	}
	return result
}

func nearestMarker(tradedAt time.Time, bars []MinuteBar) (time.Time, bool) {
	var nearest time.Time
	best := 5*time.Minute + time.Nanosecond
	for _, bar := range bars {
		if !sameTradingSegment(tradedAt, bar.At) {
			continue
		}
		delta := bar.At.Sub(tradedAt)
		if delta < 0 {
			delta = -delta
		}
		if delta <= 5*time.Minute && delta < best {
			best, nearest = delta, bar.At
		}
	}
	return nearest, !nearest.IsZero()
}

func sameTradingSegment(left, right time.Time) bool {
	left, right = shanghaiTime(left), shanghaiTime(right)
	if !sameDate(left, right) {
		return false
	}
	segment := func(value time.Time) int {
		clock := value.Hour()*60 + value.Minute()
		if clock >= 9*60+30 && clock <= 11*60+30 {
			return 1
		}
		if clock >= 13*60 && clock <= 15*60 {
			return 2
		}
		return 0
	}
	return segment(left) != 0 && segment(left) == segment(right)
}

func sameDate(left, right time.Time) bool {
	left, right = shanghaiTime(left), shanghaiTime(right)
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	return ly == ry && lm == rm && ld == rd
}

func shanghaiTime(value time.Time) time.Time { return value.In(shanghaiLocation) }

func (chart Chart) Validate() error {
	if chart.Status != "complete" && chart.Status != "partial" && chart.Status != "empty" {
		return fmt.Errorf("invalid chart status %q", chart.Status)
	}
	return nil
}
