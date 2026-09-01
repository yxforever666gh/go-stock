package research

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrChartProviderUnavailable = errors.New("recommendation chart provider is unavailable")
	ErrChartRefreshInProgress   = errors.New("recommendation chart refresh is already in progress")
)

type ChartMinuteBar struct {
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

type ChartProviderError struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

type ChartProviderSnapshot struct {
	Bars           []ChartMinuteBar
	Quote          *Quote
	RefreshedAt    time.Time
	ProviderErrors []ChartProviderError
}

type RecommendationChartProvider interface {
	LoadCached(context.Context, string, time.Time, time.Time) (ChartProviderSnapshot, error)
	Refresh(context.Context, string, time.Time, time.Time, []string) (ChartProviderSnapshot, error)
}

// RecommendationChartEngine owns the shared recommendation-chart behavior and
// refresh de-duplication. Both research centers adapt their own recommendation
// records into RecommendationDetail and use this engine, so minute coverage,
// trade markers and cost-aware returns cannot drift between the two pages.
type RecommendationChartEngine struct {
	provider   RecommendationChartProvider
	calendar   TradingCalendar
	now        func() time.Time
	mu         sync.Mutex
	refreshing map[string]struct{}
}

func NewRecommendationChartEngine(provider RecommendationChartProvider, calendar TradingCalendar, clocks ...func() time.Time) *RecommendationChartEngine {
	if calendar == nil {
		calendar = WeekdayCalendar{}
	}
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	return &RecommendationChartEngine{provider: provider, calendar: calendar, now: now, refreshing: make(map[string]struct{})}
}

type RecommendationChartSession struct {
	Date          string  `json:"date"`
	PreviousClose float64 `json:"previousClose"`
	Status        string  `json:"status"`
}

type RecommendationChartTrade struct {
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

type RecommendationChart struct {
	RecommendationID    string                       `json:"recommendationId"`
	StockCode           string                       `json:"stockCode"`
	StockName           string                       `json:"stockName"`
	Status              string                       `json:"status"`
	RangeFrom           time.Time                    `json:"rangeFrom"`
	RangeTo             time.Time                    `json:"rangeTo"`
	RefreshedAt         time.Time                    `json:"refreshedAt"`
	QuoteAt             *time.Time                   `json:"quoteAt,omitempty"`
	CurrentPrice        float64                      `json:"currentPrice"`
	CurrentNetPnL       float64                      `json:"currentNetPnl"`
	CurrentNetYieldRate float64                      `json:"currentNetYieldRate"`
	MissingSessions     []string                     `json:"missingSessions"`
	ProviderErrors      []ChartProviderError         `json:"providerErrors"`
	Sessions            []RecommendationChartSession `json:"sessions"`
	Bars                []ChartMinuteBar             `json:"bars"`
	Trades              []RecommendationChartTrade   `json:"trades"`
}

func (s *Service) RecommendationChart(ctx context.Context, recommendationID string, refresh bool) (RecommendationChart, error) {
	recommendationID = strings.TrimSpace(recommendationID)
	if recommendationID == "" {
		return RecommendationChart{}, errors.New("recommendationId is required")
	}
	s.chartMu.Lock()
	engine := s.chartEngine
	s.chartMu.Unlock()
	if engine == nil {
		return RecommendationChart{}, ErrChartProviderUnavailable
	}
	detail, err := s.repository.Detail(ctx, recommendationID)
	if err != nil {
		return RecommendationChart{}, err
	}
	return engine.Chart(ctx, detail, refresh)
}

func (e *RecommendationChartEngine) Chart(ctx context.Context, detail RecommendationDetail, refresh bool) (RecommendationChart, error) {
	if e == nil || e.provider == nil {
		return RecommendationChart{}, ErrChartProviderUnavailable
	}
	recommendationID := strings.TrimSpace(detail.Recommendation.RecommendationID)
	if recommendationID == "" {
		return RecommendationChart{}, errors.New("recommendationId is required")
	}
	if refresh {
		if !e.beginRefresh(recommendationID) {
			return RecommendationChart{}, ErrChartRefreshInProgress
		}
		defer e.endRefresh(recommendationID)
	}
	now := e.now()
	from, to := chartRange(detail, now)
	sessionDates, err := chartTradingSessions(ctx, e.calendar, from, to, refresh)
	calendarFallback := false
	if err != nil {
		if !refresh {
			return RecommendationChart{}, err
		}
		// A chart refresh is best-effort. The minute providers can still return
		// real cached/upstream bars when the strict holiday calendar is
		// temporarily unavailable; use weekday sessions and expose the degraded
		// state instead of turning the whole refresh into an HTTP 503.
		sessionDates, err = chartTradingSessions(ctx, WeekdayCalendar{}, from, to, false)
		if err != nil {
			return RecommendationChart{}, err
		}
		calendarFallback = true
	}
	// Include a short look-behind only to find the first session's previous
	// close. Bars before rangeFrom are never exposed to clients.
	lookupFrom := from.AddDate(0, 0, -10)
	var snapshot ChartProviderSnapshot
	if refresh {
		snapshot, err = e.provider.Refresh(ctx, detail.Recommendation.StockCode, lookupFrom, to, sessionDates)
	} else {
		snapshot, err = e.provider.LoadCached(ctx, detail.Recommendation.StockCode, lookupFrom, to)
	}
	if err != nil {
		return RecommendationChart{}, err
	}
	if calendarFallback {
		snapshot.ProviderErrors = append(snapshot.ProviderErrors, ChartProviderError{Provider: "trade_calendar",
			Message: "严格交易日历暂时不可用，已按工作日继续补齐；节假日覆盖状态可能不完整"})
	}
	return buildRecommendationChart(detail, snapshot, from, to, sessionDates), nil
}

func (e *RecommendationChartEngine) beginRefresh(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.refreshing[id]; exists {
		return false
	}
	e.refreshing[id] = struct{}{}
	return true
}

func (e *RecommendationChartEngine) endRefresh(id string) {
	e.mu.Lock()
	delete(e.refreshing, id)
	e.mu.Unlock()
}

func chartRange(detail RecommendationDetail, now time.Time) (time.Time, time.Time) {
	location := ShanghaiTime(now).Location()
	anchor := detail.Recommendation.SignalAt
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
	if latestSell, ok := latestChartSell(detail.Trades); ok && detail.Position == nil {
		endAnchor = latestSell.In(location)
	} else if detail.Position != nil && detail.Position.Status == "closed" && detail.Position.ExitAt != nil {
		endAnchor = detail.Position.ExitAt.In(location)
	} else if !hasAnyChartTrade(detail.Trades) {
		endAnchor = anchor
	}
	to := time.Date(endAnchor.Year(), endAnchor.Month(), endAnchor.Day(), 15, 0, 0, 0, location)
	if sameChartDate(endAnchor, now.In(location)) {
		to = normalizeCurrentChartEnd(endAnchor)
	}
	if to.Before(from) {
		// A pre-open manual signal has no future minute coverage yet.
		to = from
	}
	return from, to
}

func normalizeCurrentChartEnd(value time.Time) time.Time {
	value = ShanghaiTime(value)
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

func hasAnyChartTrade(trades []SimulatedTrade) bool { return len(trades) > 0 }

func latestChartSell(trades []SimulatedTrade) (time.Time, bool) {
	var latest time.Time
	for _, trade := range trades {
		if trade.Side == "sell" && trade.TradedAt.After(latest) {
			latest = trade.TradedAt
		}
	}
	return latest, !latest.IsZero()
}

type cachedTradingCalendar interface {
	IsTradingDayCached(time.Time) (bool, bool)
}

func chartTradingSessions(ctx context.Context, calendar TradingCalendar, from, to time.Time, allowNetwork bool) ([]string, error) {
	location := from.Location()
	start := time.Date(from.Year(), from.Month(), from.Day(), 12, 0, 0, 0, location)
	end := time.Date(to.Year(), to.Month(), to.Day(), 12, 0, 0, 0, location)
	result := make([]string, 0, int(end.Sub(start).Hours()/24)+1)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		if sameChartDate(day, to) {
			localTo := ShanghaiTime(to)
			if localTo.Before(time.Date(localTo.Year(), localTo.Month(), localTo.Day(), 9, 30, 0, 0, localTo.Location())) {
				continue
			}
		}
		trading := false
		if !allowNetwork {
			if cached, ok := calendar.(cachedTradingCalendar); ok {
				if value, known := cached.IsTradingDayCached(day); known {
					trading = value
				} else {
					trading = day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
				}
			} else {
				trading = day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
			}
		} else {
			value, err := calendar.IsTradingDay(ctx, day)
			if err != nil {
				return nil, err
			}
			trading = value
		}
		if trading {
			result = append(result, day.Format("2006-01-02"))
		}
	}
	return result, nil
}

func buildRecommendationChart(detail RecommendationDetail, snapshot ChartProviderSnapshot, from, to time.Time, sessionDates []string) RecommendationChart {
	allBars := append([]ChartMinuteBar(nil), snapshot.Bars...)
	sort.SliceStable(allBars, func(i, j int) bool { return allBars[i].At.Before(allBars[j].At) })
	bars := make([]ChartMinuteBar, 0, len(allBars))
	for _, bar := range allBars {
		if bar.At.Before(from) || bar.At.After(to) || !validChartBar(bar) {
			continue
		}
		bars = append(bars, bar)
	}
	trades := append([]SimulatedTrade(nil), detail.Trades...)
	sort.SliceStable(trades, func(i, j int) bool { return trades[i].TradedAt.Before(trades[j].TradedAt) })
	applyChartReturns(bars, trades)

	result := RecommendationChart{RecommendationID: detail.Recommendation.RecommendationID,
		StockCode: detail.Recommendation.StockCode, StockName: detail.Recommendation.StockName,
		RangeFrom: from, RangeTo: to, RefreshedAt: snapshot.RefreshedAt,
		MissingSessions: []string{}, ProviderErrors: snapshot.ProviderErrors, Bars: bars, Trades: chartTrades(trades, bars)}
	if result.RefreshedAt.IsZero() && len(bars) > 0 {
		result.RefreshedAt = bars[len(bars)-1].At
	}
	result.Sessions, result.MissingSessions = chartSessions(sessionDates, allBars, from, to)
	applyQuotePreviousClose(result.Sessions, snapshot.Quote)
	result.Status = chartCoverageStatus(result.Sessions, len(bars))

	if snapshot.Quote != nil && snapshot.Quote.Price > 0 {
		result.CurrentPrice, result.QuoteAt = snapshot.Quote.Price, &snapshot.Quote.At
	} else if len(bars) > 0 {
		latest := bars[len(bars)-1]
		result.CurrentPrice, result.QuoteAt = latest.Close, &latest.At
	} else if detail.Position != nil && detail.Position.CurrentPrice > 0 {
		result.CurrentPrice, result.QuoteAt = detail.Position.CurrentPrice, detail.Position.CurrentPriceAt
	}
	result.CurrentNetPnL, result.CurrentNetYieldRate = chartCurrentReturn(trades, result.CurrentPrice)
	return result
}

func applyQuotePreviousClose(sessions []RecommendationChartSession, quote *Quote) {
	if quote == nil || quote.PreviousClose <= 0 || quote.At.IsZero() {
		return
	}
	quoteDate := ShanghaiTime(quote.At).Format("2006-01-02")
	for index := range sessions {
		if sessions[index].Date == quoteDate && sessions[index].PreviousClose <= 0 {
			sessions[index].PreviousClose = quote.PreviousClose
			return
		}
	}
}

func validChartBar(bar ChartMinuteBar) bool {
	return !bar.At.IsZero() && bar.Open > 0 && bar.High > 0 && bar.Low > 0 && bar.Close > 0 && bar.High >= bar.Low
}

func chartSessions(dates []string, allBars []ChartMinuteBar, from, to time.Time) ([]RecommendationChartSession, []string) {
	byDate := make(map[string][]ChartMinuteBar, len(dates))
	for _, bar := range allBars {
		if validChartBar(bar) {
			key := ShanghaiTime(bar.At).Format("2006-01-02")
			byDate[key] = append(byDate[key], bar)
		}
	}
	result := make([]RecommendationChartSession, 0, len(dates))
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
			expectedEnd := chartSessionExpectedEnd(date, to)
			first, last := ShanghaiTime(rows[0].At), ShanghaiTime(rows[len(rows)-1].At)
			startLimit := time.Date(first.Year(), first.Month(), first.Day(), 9, 36, 0, 0, first.Location())
			if !first.After(startLimit) && !last.Before(expectedEnd.Add(-5*time.Minute)) && !chartSessionHasGap(rows) {
				status = "complete"
			} else {
				status = "partial"
			}
		}
		result = append(result, RecommendationChartSession{Date: date, PreviousClose: previousClose, Status: status})
		if status == "missing" {
			missing = append(missing, date)
		}
		if len(rows) > 0 {
			previousClose = rows[len(rows)-1].Close
		}
	}
	return result, missing
}

func chartSessionHasGap(rows []ChartMinuteBar) bool {
	if len(rows) < 2 {
		return false
	}
	previous := ShanghaiTime(rows[0].At)
	for _, row := range rows[1:] {
		current := ShanghaiTime(row.At)
		if sameChartTradingSegment(previous, current) && current.Sub(previous) > 5*time.Minute {
			return true
		}
		previous = current
	}
	return false
}

func previousCloseBefore(bars []ChartMinuteBar, date string) float64 {
	var latest time.Time
	value := 0.0
	for _, bar := range bars {
		if ShanghaiTime(bar.At).Format("2006-01-02") >= date || !validChartBar(bar) {
			continue
		}
		if bar.At.After(latest) {
			latest, value = bar.At, bar.Close
		}
	}
	return value
}

func chartSessionExpectedEnd(date string, rangeTo time.Time) time.Time {
	location := rangeTo.Location()
	day, _ := time.ParseInLocation("2006-01-02", date, location)
	expected := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, location)
	if date == rangeTo.In(location).Format("2006-01-02") && rangeTo.Before(expected) {
		expected = rangeTo
	}
	return expected
}

func chartCoverageStatus(sessions []RecommendationChartSession, bars int) string {
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

func applyChartReturns(bars []ChartMinuteBar, trades []SimulatedTrade) {
	for index := range bars {
		barEnd := bars[index].At.Truncate(time.Minute).Add(time.Minute)
		buyOut, soldIn, quantity := chartCashState(trades, barEnd)
		if buyOut <= 0 {
			continue
		}
		value := soldIn
		if quantity > 0 {
			value += CalculateSellCost(bars[index].Close, quantity).NetCashFlow
		}
		bars[index].NetPnL = value - buyOut
		bars[index].NetYieldRate = bars[index].NetPnL / buyOut
	}
}

func chartCurrentReturn(trades []SimulatedTrade, price float64) (float64, float64) {
	buyOut, soldIn, quantity := chartCashState(trades, time.Time{})
	if buyOut <= 0 {
		return 0, 0
	}
	if quantity > 0 && price <= 0 {
		return 0, 0
	}
	value := soldIn
	if quantity > 0 && price > 0 {
		value += CalculateSellCost(price, quantity).NetCashFlow
	}
	pnl := value - buyOut
	return pnl, pnl / buyOut
}

func chartCashState(trades []SimulatedTrade, before time.Time) (float64, float64, int64) {
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

func chartTrades(trades []SimulatedTrade, bars []ChartMinuteBar) []RecommendationChartTrade {
	result := make([]RecommendationChartTrade, 0, len(trades))
	for _, trade := range trades {
		item := RecommendationChartTrade{Side: trade.Side, TradedAt: trade.TradedAt, MarketPrice: trade.MarketPrice,
			ExecutionPrice: trade.ExecutionPrice, Quantity: trade.Quantity, TotalFees: trade.TotalFees, NetCashFlow: trade.NetCashFlow}
		if marker, ok := nearestChartMarker(trade.TradedAt, bars); ok {
			item.MarkerAt = &marker
			item.MarkerSnapped = !marker.Equal(trade.TradedAt)
		}
		result = append(result, item)
	}
	return result
}

func nearestChartMarker(tradedAt time.Time, bars []ChartMinuteBar) (time.Time, bool) {
	var nearest time.Time
	best := 5*time.Minute + time.Nanosecond
	for _, bar := range bars {
		if !sameChartTradingSegment(tradedAt, bar.At) {
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

func sameChartTradingSegment(left, right time.Time) bool {
	left, right = ShanghaiTime(left), ShanghaiTime(right)
	if !sameChartDate(left, right) {
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

func sameChartDate(left, right time.Time) bool {
	left, right = ShanghaiTime(left), ShanghaiTime(right)
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	return ly == ry && lm == rm && ld == rd
}

func (chart RecommendationChart) Validate() error {
	if chart.Status != "complete" && chart.Status != "partial" && chart.Status != "empty" {
		return fmt.Errorf("invalid chart status %q", chart.Status)
	}
	return nil
}
