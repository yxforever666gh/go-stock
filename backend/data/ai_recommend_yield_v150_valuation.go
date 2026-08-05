package data

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

const (
	v150YieldValuationUnavailableStatus = "不可估值"
	v150YieldValuationUnavailableReason = "V1.5 持仓现价缺失或陈旧，当前收益不可估值"
	v150YieldValuationHealthCode        = "holding_current_price_missing_or_stale"
	v150YieldDailyPriceHealthCode       = "holding_daily_price_missing"
	v150BenchmarkBuyQuoteHealthCode     = "benchmark_510300_buy_quote_missing_or_unmatched"
	v150BenchmarkExitQuoteHealthCode    = "benchmark_510300_exit_quote_missing_or_unmatched"
	v150BenchmarkDailySeriesHealthCode  = "benchmark_510300_daily_series_missing"
	v150BenchmarkPartialHealthCode      = "benchmark_510300_partial_portfolio_rejected"
)

// applyV150YieldValuationAvailability prevents a persisted price from being
// treated as a current mark indefinitely. Closed trades keep their immutable
// realized value; only an open V1.5 position needs a fresh point-in-time quote.
func applyV150YieldValuationAvailability(item *models.AiRecommendStocksYieldItem, asOf time.Time) {
	if item == nil || !isV150CostVersion(item.SummaryVersion) ||
		strings.TrimSpace(item.ActivationStatus) != "activated" ||
		(item.SellAmount != nil && *item.SellAmount > 0) {
		return
	}
	if item.CurrentPrice > 0 && v150YieldCurrentPriceIsFresh(item.CurrentPriceTime, asOf) {
		return
	}

	item.CurrentPrice = 0
	item.YieldRate = 0
	item.YieldRateText = "--"
	item.BenchmarkYieldRate = 0
	item.BenchmarkYieldRateText = "--"
	item.ExcessYieldRate = 0
	item.ExcessYieldRateText = "--"
	item.DataStatus = v150YieldValuationUnavailableStatus
	item.DataStatusReason = appendYieldHealthReason(item.DataStatusReason, v150YieldValuationUnavailableReason)
}

func applyV150YieldRecordStateValuationAvailability(rec models.AiRecommendStocks, state *models.AiRecommendYieldRecordState, asOf time.Time) {
	if state == nil || !isV150CostVersion(rec.SummaryVersion) ||
		strings.TrimSpace(state.ActivationStatus) != "activated" ||
		(state.RealizedSellAmount != nil && *state.RealizedSellAmount > 0) {
		return
	}
	if state.CurrentPrice > 0 && v150YieldCurrentPriceIsFresh(state.CurrentPriceTime, asOf) {
		return
	}
	state.CurrentPrice = 0
	state.YieldRate = 0
	state.YieldRateText = "--"
	state.DataStatus = v150YieldValuationUnavailableStatus
	state.DataStatusReason = appendYieldHealthReason(state.DataStatusReason, v150YieldValuationUnavailableReason)
}

type v150YieldLedgerProjection struct {
	AccountingReady bool
	ValuationReady  bool
	Closed          bool
	EntryCash       float64
	NetValue        float64
	NetPnL          float64
	ReturnPct       float64
	Quantity        float64
	CorporateCash   float64
}

// loadV150YieldLedgerProjection is the single read-only accounting boundary
// for online V1.5 metrics. It accepts only events that were both effective and
// frozen by asOf, verifies their immutable seals through the replay engine,
// and never reconstructs a corporate action from raw display prices.
func loadV150YieldLedgerProjection(record models.AiRecommendStocks, markPrice float64, asOf time.Time) (v150YieldLedgerProjection, error) {
	projection := v150YieldLedgerProjection{}
	if !isV150CostVersion(record.SummaryVersion) {
		return projection, fmt.Errorf("record is not V1.5")
	}
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.OrderEvent{}) {
		return projection, fmt.Errorf("strategy_order_event cache is unavailable")
	}
	runID, ruleID := strings.TrimSpace(record.StrategyRunID), strings.TrimSpace(record.StrategyRuleID)
	symbol := normalizeRecommendStockCode(record.StockCode)
	if runID == "" || ruleID == "" || symbol == "" {
		return projection, fmt.Errorf("strategy run/rule/symbol identity is incomplete")
	}
	if asOf.IsZero() {
		asOf = timeNow().In(cnLocation())
	}
	var events []models.OrderEvent
	if err := db.Dao.Model(&models.OrderEvent{}).
		Where("run_id = ? AND rule_id = ? AND upper(symbol) = ? AND strategy_version = ? AND event_at <= ? AND frozen_at <= ?", runID, ruleID, symbol, v150.StrategyVersion, asOf, asOf).
		Order("sequence ASC, event_id ASC").Find(&events).Error; err != nil {
		return projection, fmt.Errorf("load sealed order ledger: %w", err)
	}
	if len(events) == 0 {
		return projection, fmt.Errorf("sealed order ledger is empty")
	}
	trades, _, _, err := persistence.ReplayFrozenOrderEvents(
		"yield-projection|"+runID+"|"+ruleID,
		v150.StrategyVersion,
		events,
		asOf,
	)
	if err != nil {
		return projection, fmt.Errorf("replay sealed order ledger: %w", err)
	}
	if len(trades) != 1 {
		return projection, fmt.Errorf("sealed order ledger produced %d positions", len(trades))
	}
	trade := trades[0]
	var payload struct {
		EntryCash           float64 `json:"entryCash"`
		CorporateActionCash float64 `json:"corporateActionCash"`
	}
	if err := json.Unmarshal([]byte(trade.PayloadJSON), &payload); err != nil || payload.EntryCash <= 0 || payload.CorporateActionCash < 0 ||
		math.IsNaN(payload.EntryCash) || math.IsInf(payload.EntryCash, 0) || math.IsNaN(payload.CorporateActionCash) || math.IsInf(payload.CorporateActionCash, 0) {
		return projection, fmt.Errorf("replayed position lacks valid entry/corporate-action cash")
	}
	projection.AccountingReady = true
	projection.EntryCash = payload.EntryCash
	projection.Quantity = trade.Quantity
	projection.CorporateCash = payload.CorporateActionCash
	if trade.ExitAt != nil {
		projection.ValuationReady = true
		projection.Closed = true
		projection.NetPnL = trade.NetPnL
		projection.NetValue = payload.EntryCash + trade.NetPnL
		projection.ReturnPct = trade.ReturnPct
		return projection, nil
	}
	if markPrice <= 0 || trade.Quantity <= 0 || math.Trunc(trade.Quantity) != trade.Quantity {
		return projection, nil
	}
	cfg := v150.FixedStrategyV150Config()
	sell := v150.CalculateTradeCost(
		v150.SideSell,
		v150.ResolveMarket(symbol),
		markPrice,
		int(trade.Quantity),
		cfg.SlippageScenarios()[0],
		cfg,
	)
	if sell.CashFlow <= 0 {
		return projection, fmt.Errorf("current mark cannot produce an executable sell cash flow")
	}
	projection.NetValue = sell.CashFlow + payload.CorporateActionCash
	projection.NetPnL = projection.NetValue - payload.EntryCash
	rate := projection.NetPnL / payload.EntryCash * 100
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return projection, fmt.Errorf("ledger yield is non-finite")
	}
	projection.ValuationReady = true
	projection.ReturnPct = rate
	return projection, nil
}

// fillV150YieldRecordMetricsFromLedger projects one V1.5 position exclusively
// from its sealed order-event ledger. Corporate-action cash and the adjusted
// share quantity are therefore part of both realized and open-position PnL.
// Historical cohorts never enter this path.
func fillV150YieldRecordMetricsFromLedger(state *models.AiRecommendYieldRecordState, metricContext yieldRecordMetricContext) (bool, error) {
	if state == nil || !isV150CostVersion(metricContext.Record.SummaryVersion) {
		return false, nil
	}
	if strings.TrimSpace(state.ActivationStatus) != "activated" || state.BuyAmount <= 0 {
		return true, nil
	}
	projection, err := loadV150YieldLedgerProjection(metricContext.Record, state.CurrentPrice, metricContext.AsOf)
	if err != nil {
		return true, err
	}
	if !projection.ValuationReady {
		return true, nil
	}
	state.YieldRate = round2(projection.ReturnPct)
	state.YieldRateText = formatSignedPercent(state.YieldRate)
	return true, nil
}

func attachV150YieldLedgerProjection(item *models.AiRecommendStocksYieldItem, record models.AiRecommendStocks, asOf time.Time) error {
	if item == nil || !isV150CostVersion(record.SummaryVersion) || strings.TrimSpace(item.ActivationStatus) != "activated" {
		return nil
	}
	projection, err := loadV150YieldLedgerProjection(record, item.CurrentPrice, asOf)
	if err != nil {
		item.DataStatus = v150YieldValuationUnavailableStatus
		item.DataStatusReason = appendYieldHealthReason(item.DataStatusReason, "V1.5 immutable order ledger accounting unavailable: "+err.Error())
		item.YieldRate, item.YieldRateText = 0, "--"
		return err
	}
	item.V150LedgerAccountingReady = projection.ValuationReady
	item.V150LedgerClosed = projection.Closed
	item.V150LedgerEntryCash = projection.EntryCash
	item.V150LedgerNetValue = projection.NetValue
	item.V150LedgerNetPnL = projection.NetPnL
	item.V150LedgerQuantity = projection.Quantity
	item.V150LedgerCorporateCash = projection.CorporateCash
	if projection.ValuationReady {
		item.YieldRate = round2(projection.ReturnPct)
		item.YieldRateText = formatSignedPercent(item.YieldRate)
	}
	return nil
}

func v150YieldCurrentPriceIsFresh(raw string, asOf time.Time) bool {
	quoteAt, ok := parseYieldOverviewDisplayTime(raw)
	if !ok || asOf.IsZero() {
		return false
	}
	quoteAt = quoteAt.In(cnLocation())
	return marketSummaryV150QuoteIsFresh(StockInfo{
		Date: quoteAt.Format(time.DateOnly),
		Time: quoteAt.Format(time.TimeOnly),
	}, asOf)
}

func v150YieldItemHasUsableExitValue(item models.AiRecommendStocksYieldItem, asOf time.Time) bool {
	if !isV150CostVersion(item.SummaryVersion) {
		return true
	}
	if item.SellAmount != nil && *item.SellAmount > 0 {
		return true
	}
	return item.CurrentPrice > 0 && v150YieldCurrentPriceIsFresh(item.CurrentPriceTime, asOf)
}

func appendYieldHealthReason(existing, reason string) string {
	existing = strings.TrimSpace(existing)
	reason = strings.TrimSpace(reason)
	if reason == "" || strings.Contains(existing, reason) {
		return existing
	}
	if existing == "" {
		return reason
	}
	return existing + "；" + reason
}

// storeCurrentPriceSnapshot keeps price and observation time atomic. Accepting
// a timestamp without its price would make an older persisted price look fresh.
func storeCurrentPriceSnapshot(priceMap map[string]float64, timeMap map[string]string, code, priceText, dateText, timeText string) bool {
	code = normalizeRecommendStockCode(code)
	price, priceOK := parseBuyPrice(strings.TrimSpace(priceText))
	observedAt := strings.TrimSpace(strings.TrimSpace(dateText) + " " + strings.TrimSpace(timeText))
	if code == "" || !priceOK || price <= 0 || observedAt == "" {
		return false
	}
	priceMap[code] = round2(price)
	timeMap[code] = observedAt
	return true
}

func collectV150YieldValuationHealthWarnings(items []models.AiRecommendStocksYieldItem) []string {
	warnings := make([]string, 0)
	for _, item := range items {
		if !isV150CostVersion(item.SummaryVersion) ||
			strings.TrimSpace(item.ActivationStatus) != "activated" ||
			(item.SellAmount != nil && *item.SellAmount > 0) ||
			item.CurrentPrice > 0 {
			continue
		}
		code := normalizeRecommendStockCode(item.StockCode)
		if code == "" {
			code = fmt.Sprintf("recommend:%d", item.RecommendID)
		}
		warnings = append(warnings, code+":"+v150YieldValuationHealthCode)
	}
	return dedupeNonEmptyStrings(warnings, 0)
}

func resolveV150BenchmarkMinutePriceAt(eventAt time.Time, atBarOpen bool) (float64, bool) {
	if eventAt.IsZero() || !marketSummaryV150QuoteTimestampIsInTradingSession(eventAt) {
		return 0, false
	}
	eventAt = eventAt.In(cnLocation())
	minuteAt := normalizeMinuteTime(eventAt)
	day := normalizeYieldOverviewTradeDay(eventAt)
	sessionStart := day.Add(9*time.Hour + 30*time.Minute)
	if minuteAt.Before(sessionStart) {
		return 0, false
	}
	// Providers disagree on whether a one-minute bar is labelled by its start
	// or end. Load the session anchor and reuse the execution engine's detector:
	// a fill at 10:00 maps to 10:00.Open for start labels and 10:01.Open for
	// end labels. A current mark at 10:00 maps to the last completed minute,
	// respectively 09:59.Close or 10:00.Close.
	queryEnd := minuteAt.Add(time.Minute)
	bars, err := listMinuteBarsFromCache(defaultBenchmarkModelCode, sessionStart, queryEnd)
	if err != nil {
		return 0, false
	}
	endLabeled := detectMarketSummaryV150EndLabeledDays(bars, minuteAt)[day.Format(time.DateOnly)]
	targetAt := minuteAt
	if atBarOpen && endLabeled {
		targetAt = minuteAt.Add(time.Minute)
	} else if !atBarOpen && !endLabeled {
		targetAt = minuteAt.Add(-time.Minute)
	}
	for _, bar := range bars {
		barAt := normalizeMinuteTime(bar.TradeTime.In(cnLocation()))
		if !barAt.Equal(targetAt) {
			continue
		}
		price := bar.Close
		if atBarOpen {
			price = bar.Open
		}
		if price > 0 {
			return price, true
		}
	}
	return 0, false
}

func resolveV150BenchmarkMatchedPrices(entry yieldDailyOverviewEntry) (float64, float64, time.Time, string) {
	buyTime := entry.BuyTime.In(cnLocation())
	buyPrice, ok := resolveV150BenchmarkMinutePriceAt(buyTime, true)
	if !ok {
		return 0, 0, time.Time{}, v150BenchmarkBuyQuoteHealthCode
	}

	var endTime time.Time
	if entry.HasSellAmount {
		parsed, parsedOK := parseYieldOverviewDisplayTime(entry.SellTime)
		if !parsedOK {
			return 0, 0, time.Time{}, v150BenchmarkExitQuoteHealthCode
		}
		endTime = parsed.In(cnLocation())
	} else {
		parsed, parsedOK := parseYieldOverviewDisplayTime(entry.CurrentPriceTime)
		if !parsedOK {
			return 0, 0, time.Time{}, v150BenchmarkExitQuoteHealthCode
		}
		endTime = parsed.In(cnLocation())
	}
	if endTime.Before(buyTime) {
		return 0, 0, time.Time{}, v150BenchmarkExitQuoteHealthCode
	}
	endPrice, ok := resolveV150BenchmarkMinutePriceAt(endTime, entry.HasSellAmount)
	if !ok {
		return 0, 0, time.Time{}, v150BenchmarkExitQuoteHealthCode
	}
	return buyPrice, endPrice, endTime, ""
}

func v150BenchmarkHealthWarning(entry yieldDailyOverviewEntry, reason string) string {
	code := normalizeRecommendStockCode(entry.StockCode)
	if code == "" {
		code = fmt.Sprintf("recommend:%d", entry.RecommendID)
	}
	return code + ":" + strings.TrimSpace(reason)
}

func collectV150BenchmarkHealthWarnings(entries []yieldDailyOverviewEntry) []string {
	warnings := make([]string, 0)
	for _, entry := range entries {
		if !isV150CostVersion(entry.SummaryVersion) {
			continue
		}
		_, _, _, reason := resolveV150BenchmarkMatchedPrices(entry)
		if reason != "" {
			warnings = append(warnings, v150BenchmarkHealthWarning(entry, reason))
		}
	}
	return dedupeNonEmptyStrings(warnings, 0)
}
