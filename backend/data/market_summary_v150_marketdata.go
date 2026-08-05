package data

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
)

const marketSummaryV150MinimumDailyBars = 65

var loadMarketSummaryV150DailyBarsWithCache = loadDailyBarsWithCache
var loadMarketSummaryV150RealtimeQuotesForRefresh = loadMarketSummaryV150RealtimeQuotes
var marketSummaryV150QuoteRefreshNow = time.Now

func prepareMarketSummaryV150ForPhase(input marketSummaryDiscoveryInput, startedAt time.Time, logState *marketSummaryRouteLog) (*MarketSummaryV150RunSnapshot, error) {
	indicators := input.AllIndicatorCandidates
	if len(indicators) == 0 {
		indicators = input.IndicatorCandidates
	}
	asOf := time.Now()
	benchmark, benchmarkSource, benchmarkBars := loadMarketSummaryV150BenchmarkWithBars(asOf)
	candidates, sources := loadMarketSummaryV150CandidateInputs(indicators, input, asOf, benchmark, benchmarkSource, benchmarkBars)
	dataCutoffAt := time.Now()
	if !benchmarkSource.Complete || !marketSummaryV150EvidenceTimelineValid([]MarketSummaryV150EvidenceTiming{benchmarkSource.Timing}, dataCutoffAt) {
		benchmark.Stale = true
	}
	run, err := newMarketSummaryV150Run(startedAt, dataCutoffAt, input.RunSlot, benchmark, candidates, sources)
	if err != nil {
		return nil, err
	}
	run.BenchmarkSource = benchmarkSource
	if logState != nil {
		logState.addNote(
			"v1.5 deterministic universe=%d ranked=%d topForVerification=%d regime=%s warning=%s",
			len(indicators), len(run.Candidates), len(run.VerificationSymbols), run.Regime.Regime, run.Regime.Warning,
		)
	}
	if warning := strings.TrimSpace(run.Regime.Warning); warning != "" {
		run.Warnings = append(run.Warnings, "benchmark:"+warning)
	}
	if logState != nil {
		if warning := strings.TrimSpace(logState.NewsWindowWarning); warning != "" {
			run.Warnings = append(run.Warnings, "news:"+warning)
		}
		if logState.NewsWindowStatus != "" && logState.NewsWindowStatus != NewsWindowStatusOK {
			run.Warnings = append(run.Warnings, "news_status:"+string(logState.NewsWindowStatus))
			run.Warnings = append(run.Warnings, "event_evidence_degraded:news_"+string(logState.NewsWindowStatus))
		}
	}
	for _, symbol := range sortedMarketSummaryV150SourceSymbols(sources) {
		for _, warning := range sources[symbol].InputWarnings {
			warning = strings.TrimSpace(warning)
			if warning != "" {
				run.Warnings = append(run.Warnings, symbol+":"+warning)
			}
		}
	}
	run.Warnings = dedupeNonEmptyStrings(run.Warnings, 256)
	return run, nil
}

func sortedMarketSummaryV150SourceSymbols(sources map[string]MarketSummaryV150SourceCandidate) []string {
	result := make([]string, 0, len(sources))
	for symbol := range sources {
		result = append(result, normalizeRecommendStockCode(symbol))
	}
	sort.Strings(result)
	return result
}

func loadMarketSummaryV150CandidateInputs(
	indicators []marketSummaryIndicatorCandidate,
	input marketSummaryDiscoveryInput,
	asOf time.Time,
	benchmark v150.BenchmarkSnapshot,
	benchmarkSource MarketSummaryV150BenchmarkSource,
	benchmarkBars []dailyBar,
) ([]v150.Candidate, map[string]MarketSummaryV150SourceCandidate) {
	unique := make([]marketSummaryIndicatorCandidate, 0, len(indicators))
	seen := make(map[string]bool, len(indicators))
	for _, item := range indicators {
		symbol := normalizeRecommendStockCode(item.StockCode)
		if symbol == "" || seen[symbol] {
			continue
		}
		seen[symbol] = true
		item.StockCode = symbol
		unique = append(unique, item)
	}
	// Source iteration order must never influence rank ties.
	sort.SliceStable(unique, func(i, j int) bool { return unique[i].StockCode < unique[j].StockCode })

	basics := loadMarketSummaryV150StockBasics(unique)
	quotes := loadMarketSummaryV150RealtimeQuotes(unique)
	quoteAvailableAt := time.Now()
	candidateAsOf := quoteAvailableAt
	type loadedCandidate struct {
		candidate v150.Candidate
		source    MarketSummaryV150SourceCandidate
	}
	loaded := make([]loadedCandidate, len(unique))
	semaphore := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for index := range unique {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			item := unique[i]
			symbol := normalizeRecommendStockCode(item.StockCode)
			source := marketSummaryV150SourceFromIndicator(item)
			basic := basics[symbol]
			source.Security = marketSummaryV150SecuritySourceFromBasic(basic, candidateAsOf)
			source.QuoteEvidence = marketSummaryV150QuoteEvidence(quotes[symbol], symbol, quoteAvailableAt)
			bars, err := loadMarketSummaryV150DailyBarsWithCache(symbol, toQuoteCode(symbol), candidateAsOf.AddDate(0, 0, -180), candidateAsOf, 130)
			if err != nil {
				source.InputWarnings = append(source.InputWarnings, "daily_cache_error:"+strings.TrimSpace(err.Error()))
			}
			dailySource := loadMarketSummaryV150CompletedDailyDataSource(symbol, bars, candidateAsOf)
			source.DailyData = dailySource
			candidate, warnings, eventEvidence, eventAssessment := buildMarketSummaryV150Candidate(item, basic, quotes[symbol], bars, input, candidateAsOf, dailySource)
			candidate, relativeWarnings := applyMarketSummaryV150RelativeStrength(
				candidate,
				bars,
				candidateAsOf,
				dailySource,
				benchmark,
				benchmarkSource,
				benchmarkBars,
				v150.FixedStrategyV150Config(),
			)
			warnings = append(warnings, relativeWarnings...)
			source.EventEvidence = eventEvidence
			source.EventAssessment = eventAssessment
			source.InputWarnings = append(source.InputWarnings, warnings...)
			loaded[i] = loadedCandidate{candidate: candidate, source: source}
		}(index)
	}
	wg.Wait()

	candidates := make([]v150.Candidate, 0, len(loaded))
	sources := make(map[string]MarketSummaryV150SourceCandidate, len(loaded))
	for _, item := range loaded {
		symbol := normalizeRecommendStockCode(item.candidate.Symbol)
		if symbol == "" {
			continue
		}
		candidates = append(candidates, item.candidate)
		sources[symbol] = item.source
	}
	return candidates, sources
}

// refreshMarketSummaryV150VerificationQuotes closes the potentially long gap
// between full-universe data loading and the final decision. Only the already
// frozen top-for-verification set is refreshed; membership cannot change, but
// a stale or missing final quote makes that candidate ineligible.
func refreshMarketSummaryV150VerificationQuotes(run *MarketSummaryV150RunSnapshot) (refreshed, failed int) {
	if run == nil || len(run.VerificationSymbols) == 0 {
		return 0, 0
	}
	selected := make(map[string]bool, len(run.VerificationSymbols))
	indicators := make([]marketSummaryIndicatorCandidate, 0, len(run.VerificationSymbols))
	for _, raw := range run.VerificationSymbols {
		selected[normalizeRecommendStockCode(raw)] = true
	}
	for _, row := range run.Candidates {
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		if !selected[symbol] {
			continue
		}
		indicators = append(indicators, marketSummaryIndicatorCandidate{
			StockCode: symbol,
			StockName: row.Candidate.Name,
			BkName:    row.Candidate.Sector,
		})
	}
	quotes := loadMarketSummaryV150RealtimeQuotesForRefresh(indicators)
	availableAt := marketSummaryV150QuoteRefreshNow()
	for index := range run.Candidates {
		row := &run.Candidates[index]
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		if !selected[symbol] {
			continue
		}
		quote := quotes[symbol]
		row.Source.QuoteEvidence = marketSummaryV150QuoteEvidence(quote, symbol, availableAt)
		if currentName := strings.TrimSpace(quote.Name); currentName != "" {
			row.Candidate.Name = currentName
			row.Source.StockName = currentName
			row.Source.Security.Name = currentName
		}
		if marketSummaryV150SecurityNameIsST(quote.Name) {
			row.Candidate.ST = true
			row.Source.InputWarnings = append(row.Source.InputWarnings, "final_security_st")
			run.Warnings = append(run.Warnings, symbol+":final_security_st")
		}
		price, priceOK := parseLooseFloat(quote.Price)
		previousClose, previousOK := parseLooseFloat(quote.PreClose)
		openPrice, openOK := parseLooseFloat(quote.Open)
		amount, amountOK := parseLooseFloat(quote.Amount)
		fresh := priceOK && previousOK && openOK && price > 0 && previousClose > 0 && openPrice > 0 &&
			marketSummaryV150QuoteIsFresh(quote, availableAt)
		row.Candidate.HasCurrentData = fresh
		if !fresh {
			row.Candidate.Price = 0
			row.Candidate.PreviousClose = 0
			row.Candidate.DayChangeRatio = 0
			row.Candidate.GapRatio = 0
			row.Source.InputWarnings = append(row.Source.InputWarnings, "final_current_quote_missing_or_stale")
			run.Warnings = append(run.Warnings, symbol+":final_current_quote_missing_or_stale")
			failed++
			continue
		}
		row.Candidate.Price = price
		row.Candidate.PreviousClose = previousClose
		row.Candidate.DayChangeRatio = price/previousClose - 1
		row.Candidate.GapRatio = openPrice/previousClose - 1
		if !amountOK || amount <= 0 || strings.TrimSpace(quote.Volume) == "" {
			row.Candidate.Suspended = true
		}
		indicator := marketSummaryIndicatorCandidate{
			StockCode: symbol, StockName: row.Source.StockName, BkName: row.Source.BkName,
			Direction: row.Source.Direction, Metrics: row.Source.Metrics,
		}
		row.Candidate.Signals.SetupQuality = marketSummaryV150SetupSignal(row.Candidate, indicator)
		row.Candidate.Signals.LiquidityRiskQuality = marketSummaryV150LiquiditySignal(row.Candidate)
		refreshed++
	}
	return refreshed, failed
}

func loadMarketSummaryV150StockBasics(indicators []marketSummaryIndicatorCandidate) map[string]StockBasic {
	result := make(map[string]StockBasic, len(indicators))
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&StockBasic{}) {
		return result
	}
	codes := make([]string, 0, len(indicators))
	for _, item := range indicators {
		if code := normalizeRecommendStockCode(item.StockCode); code != "" {
			codes = append(codes, code)
		}
	}
	rows := make([]StockBasic, 0, len(codes))
	if err := db.Dao.Model(&StockBasic{}).Where("upper(ts_code) IN ?", codes).Find(&rows).Error; err != nil {
		return result
	}
	for _, row := range rows {
		result[normalizeRecommendStockCode(row.TsCode)] = row
	}
	return result
}

func loadMarketSummaryV150RealtimeQuotes(indicators []marketSummaryIndicatorCandidate) map[string]StockInfo {
	result := make(map[string]StockInfo, len(indicators))
	quoteCodes := make([]string, 0, len(indicators))
	for _, item := range indicators {
		if code := toQuoteCode(item.StockCode); code != "" {
			quoteCodes = append(quoteCodes, code)
		}
	}
	if len(quoteCodes) == 0 {
		return result
	}
	rows := runWithTimeout(8*time.Second, (*[]StockInfo)(nil), func() *[]StockInfo {
		value, _ := NewStockDataApi().GetStockCodeRealTimeData(quoteCodes...)
		return value
	})
	if rows == nil {
		return result
	}
	for _, row := range *rows {
		if code := normalizeRecommendStockCode(row.Code); code != "" {
			result[code] = row
		}
	}
	return result
}

func buildMarketSummaryV150Candidate(
	item marketSummaryIndicatorCandidate,
	basic StockBasic,
	quote StockInfo,
	bars []dailyBar,
	input marketSummaryDiscoveryInput,
	asOf time.Time,
	dailySource MarketSummaryV150DailyDataSource,
) (v150.Candidate, []string, []MarketSummaryV150EvidenceTiming, MarketSummaryV150EventAssessment) {
	symbol := normalizeRecommendStockCode(item.StockCode)
	candidate := v150.Candidate{
		Symbol: symbol,
		Name:   firstNonEmptyText(strings.TrimSpace(quote.Name), strings.TrimSpace(basic.Name), strings.TrimSpace(item.StockName)),
		Sector: firstNonEmptyText(strings.TrimSpace(basic.Industry), strings.TrimSpace(item.BkName), strings.TrimSpace(item.Direction)),
		Market: marketSummaryV150Market(symbol),
	}
	warnings := make([]string, 0, 4)
	if listedAt, ok := parseMarketSummaryV150ListDate(basic.ListDate); ok {
		candidate.ListedAt = listedAt
	} else {
		warnings = append(warnings, "listing_date_missing")
	}
	// The realtime quote is the current exchange-facing security name. Keep
	// cached master data as a second source, but never let a stale basic row
	// hide a newly applied ST marker.
	candidate.ST = marketSummaryV150SecurityNameIsST(quote.Name) || marketSummaryV150SecurityNameIsST(basic.Name)
	if status := strings.ToUpper(strings.TrimSpace(basic.ListStatus)); status != "" && status != "L" {
		candidate.Suspended = true
	}

	price, priceOK := parseLooseFloat(quote.Price)
	previousClose, previousOK := parseLooseFloat(quote.PreClose)
	openPrice, openOK := parseLooseFloat(quote.Open)
	amount, amountOK := parseLooseFloat(quote.Amount)
	quoteFresh := marketSummaryV150QuoteIsFresh(quote, asOf)
	candidate.HasCurrentData = priceOK && previousOK && openOK && price > 0 && previousClose > 0 && openPrice > 0 && quoteFresh
	if candidate.HasCurrentData {
		candidate.Price = price
		candidate.PreviousClose = previousClose
		candidate.DayChangeRatio = price/previousClose - 1
		candidate.GapRatio = openPrice/previousClose - 1
	} else {
		warnings = append(warnings, "current_quote_missing_or_stale")
	}
	if !amountOK || amount <= 0 || strings.TrimSpace(quote.Volume) == "" {
		candidate.Suspended = true
	}

	completed := marketSummaryV150CompletedDailyBars(bars, asOf)
	if len(completed) < marketSummaryV150MinimumDailyBars {
		warnings = append(warnings, "daily_history_below_65")
		return candidate, warnings, nil, MarketSummaryV150EventAssessment{Direction: "neutral", Verifier: marketSummaryV150LocalModelSpec}
	}
	requiredLatestDay := marketSummaryV150RequiredLatestDailyBar(asOf)
	latestDailyDay := normalizeDailyTradeDate(completed[len(completed)-1].TradeDate)
	dailyBarsContinuous := marketSummaryV150DailyBarsContinuous(completed)
	if requiredLatestDay.IsZero() || latestDailyDay.IsZero() || !latestDailyDay.Equal(requiredLatestDay) {
		warnings = append(warnings, fmt.Sprintf("daily_bar_stale:latest=%s required=%s", latestDailyDay.Format(time.DateOnly), requiredLatestDay.Format(time.DateOnly)))
	}
	if !dailyBarsContinuous {
		warnings = append(warnings, "daily_bar_gap_detected")
	}
	if !dailySource.Complete || !strings.Contains(strings.ToLower(dailySource.AdjustmentSource), "qfq") {
		warnings = append(warnings, "corporate_action_adjustment_provenance_missing")
	}
	closes := make([]float64, 0, len(completed))
	for _, bar := range completed {
		if bar.Close <= 0 || bar.High <= 0 || bar.Low <= 0 {
			warnings = append(warnings, "daily_ohlc_incomplete")
			return candidate, warnings, nil, MarketSummaryV150EventAssessment{Direction: "neutral", Verifier: marketSummaryV150LocalModelSpec}
		}
		closes = append(closes, bar.Close)
	}
	candidate.MA10 = meanLast(closes, 10)
	candidate.MA20 = meanLast(closes, 20)
	candidate.MA60 = meanLast(closes, 60)
	candidate.ATR14 = marketSummaryV150ATR(completed, 14)
	candidate.Resistance20 = marketSummaryV150MaxHigh(completed, 20)
	candidate.TargetResistance = marketSummaryV150MaxHigh(completed, 60)
	candidate.NegativeOvernightGapRisk60 = marketSummaryV150NegativeGapRisk(completed, 60)
	averageAmount, derived, ok := marketSummaryV150AverageAmount(completed, 20)
	if !ok {
		warnings = append(warnings, "average_amount20_missing")
		return candidate, warnings, nil, MarketSummaryV150EventAssessment{Direction: "neutral", Verifier: marketSummaryV150LocalModelSpec}
	}
	if derived {
		warnings = append(warnings, "daily_amount_derived_from_observed_ohlcv")
	}
	candidate.AverageAmount20 = averageAmount
	candidate.HasDailyData = candidate.MA10 > 0 && candidate.MA20 > 0 && candidate.MA60 > 0 && candidate.ATR14 > 0 && candidate.Resistance20 > 0 &&
		!requiredLatestDay.IsZero() && latestDailyDay.Equal(requiredLatestDay) && dailyBarsContinuous && dailySource.Complete && strings.Contains(strings.ToLower(dailySource.AdjustmentSource), "qfq")
	var eventEvidence []MarketSummaryV150EvidenceTiming
	var eventAssessment MarketSummaryV150EventAssessment
	candidate.EventAt, candidate.Signals.EventStrength, eventEvidence, eventAssessment, warnings = marketSummaryV150EventSignal(item, input, asOf, warnings)
	candidate.TrendQuality = marketSummaryV150TrendSignal(candidate, closes)
	// The combined trend/relative signal is deliberately left at zero until
	// applyMarketSummaryV150RelativeStrength aligns this stock with 510300.
	// This prevents any caller from accidentally treating stock-only trend as
	// the complete 30-point feature.
	candidate.Signals.TrendRelativeStrength = 0
	candidate.Signals.SetupQuality = marketSummaryV150SetupSignal(candidate, item)
	candidate.Signals.SectorStrength = marketSummaryV150SectorSignal(item)
	candidate.Signals.LiquidityRiskQuality = marketSummaryV150LiquiditySignal(candidate)
	return candidate, warnings, eventEvidence, eventAssessment
}

func marketSummaryV150DailyBarsContinuous(bars []dailyBar) bool {
	if len(bars) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(bars))
	first := normalizeDailyTradeDate(bars[0].TradeDate)
	last := first
	for _, bar := range bars {
		day := normalizeDailyTradeDate(bar.TradeDate)
		if day.IsZero() {
			return false
		}
		seen[day.Format(time.DateOnly)] = struct{}{}
		if day.Before(first) {
			first = day
		}
		if day.After(last) {
			last = day
		}
	}
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		if !isCNOpenTradeDaySafe(day) {
			continue
		}
		if _, ok := seen[day.Format(time.DateOnly)]; !ok {
			return false
		}
	}
	return true
}

func marketSummaryV150SecuritySourceFromBasic(basic StockBasic, observedAt time.Time) MarketSummaryV150SecuritySource {
	source := "stock_basic"
	if strings.TrimSpace(basic.TsCode) == "" {
		source = "data_missing"
	}
	sourceAt := basic.UpdatedAt
	if sourceAt.IsZero() {
		sourceAt = basic.CreatedAt
	}
	if sourceAt.IsZero() {
		sourceAt = observedAt
	}
	return MarketSummaryV150SecuritySource{
		Name:        strings.TrimSpace(basic.Name),
		Market:      strings.TrimSpace(basic.Market),
		Exchange:    strings.TrimSpace(basic.Exchange),
		Board:       strings.TrimSpace(basic.Market),
		Industry:    strings.TrimSpace(basic.Industry),
		Currency:    firstNonEmptyText(strings.TrimSpace(basic.CurrType), "CNY"),
		ListStatus:  strings.TrimSpace(basic.ListStatus),
		ListDate:    strings.TrimSpace(basic.ListDate),
		DelistDate:  strings.TrimSpace(basic.DelistDate),
		ObservedAt:  observedAt.Format(time.RFC3339Nano),
		SourceAt:    sourceAt.Format(time.RFC3339Nano),
		AvailableAt: observedAt.Format(time.RFC3339Nano),
		Source:      source,
	}
}

func loadMarketSummaryV150DailyDataSource(symbol string, bars []dailyBar) MarketSummaryV150DailyDataSource {
	result := MarketSummaryV150DailyDataSource{}
	if len(bars) == 0 {
		result.AdjustmentSource = "data_missing"
		return result
	}
	latest := normalizeDailyTradeDate(bars[len(bars)-1].TradeDate)
	result.LatestTradeDate = latest.Format(time.DateOnly)
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.AiRecommendDailyBar{}) {
		result.AdjustmentSource = "data_missing"
		return result
	}
	var rows []models.AiRecommendDailyBar
	start := normalizeDailyTradeDate(bars[0].TradeDate)
	if err := db.Dao.Model(&models.AiRecommendDailyBar{}).
		Where("stock_code = ? AND trade_date >= ? AND trade_date <= ?", normalizeRecommendStockCode(symbol), start, latest).
		Order("trade_date ASC").
		Find(&rows).Error; err != nil || len(rows) < len(bars) {
		result.AdjustmentSource = "data_missing"
		return result
	}
	sources := make(map[string]struct{})
	for _, row := range rows {
		source, ok := normalizeMarketSummaryV150AdjustmentSource(row.Source)
		if !ok {
			result.AdjustmentSource = "data_missing"
			return result
		}
		sources[source] = struct{}{}
	}
	latestRow := rows[len(rows)-1]
	result.SourceAt = time.Date(latest.Year(), latest.Month(), latest.Day(), 15, 0, 0, 0, cnLocation())
	result.AvailableAt = latestRow.UpdatedAt
	if result.AvailableAt.IsZero() {
		result.AvailableAt = latestRow.CreatedAt
	}
	if result.AvailableAt.IsZero() || result.SourceAt.After(result.AvailableAt) {
		result.AdjustmentSource = "data_missing"
		return result
	}
	ordered := make([]string, 0, len(sources))
	for source := range sources {
		ordered = append(ordered, source)
	}
	sort.Strings(ordered)
	result.AdjustmentSource = strings.Join(ordered, "+")
	result.AdjustmentFactor = 1 // provider qfq is normalized to one at the latest observation
	result.Complete = len(ordered) > 0
	return result
}

func loadMarketSummaryV150CompletedDailyDataSource(symbol string, bars []dailyBar, asOf time.Time) MarketSummaryV150DailyDataSource {
	return loadMarketSummaryV150DailyDataSource(symbol, marketSummaryV150CompletedDailyBars(bars, asOf))
}

func normalizeMarketSummaryV150AdjustmentSource(raw string) (string, bool) {
	source := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(source, "qfq"):
		return source, true
	case source == "sina":
		// Before 1.5.0 the same Tencent GetKLineData(qfq) fetch path was
		// historically labelled "sina" in ai_recommend_daily_bar. Preserve the
		// row and freeze an explicit mapping; do not rewrite 1.4.2 data.
		return "legacy_sina_label_mapped_to_tencent_qfq", true
	default:
		return "", false
	}
}

func marketSummaryV150QuoteEvidence(quote StockInfo, symbol string, availableAt time.Time) *MarketSummaryV150EvidenceTiming {
	raw := strings.TrimSpace(strings.TrimSpace(quote.Date) + " " + strings.TrimSpace(quote.Time))
	var sourceAt time.Time
	for _, layout := range []string{time.DateTime, "2006-01-02 15:04", "20060102 150405", "20060102 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, raw, cnLocation()); err == nil {
			sourceAt = parsed
			break
		}
	}
	if sourceAt.IsZero() {
		if day, ok := parseBenchmarkQuoteDay(quote.Date, quote.UpdatedAt); ok {
			sourceAt = day
		}
	}
	return &MarketSummaryV150EvidenceTiming{
		EvidenceID:   "realtime-quote:" + normalizeRecommendStockCode(symbol) + ":" + sourceAt.Format(time.RFC3339Nano),
		EvidenceType: "realtime_quote",
		SourceAt:     sourceAt,
		AvailableAt:  availableAt,
	}
}

func marketSummaryV150RequiredLatestDailyBar(asOf time.Time) time.Time {
	if asOf.IsZero() {
		return time.Time{}
	}
	local := asOf.In(cnLocation())
	day := normalizeDailyTradeDate(local)
	marketClose := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, local.Location())
	if local.Before(marketClose) || !isCNOpenTradeDaySafe(day) {
		day = day.AddDate(0, 0, -1)
	}
	for attempts := 0; attempts < 14; attempts++ {
		if isCNOpenTradeDaySafe(day) {
			return normalizeDailyTradeDate(day)
		}
		day = day.AddDate(0, 0, -1)
	}
	return time.Time{}
}

func loadMarketSummaryV150Benchmark(asOf time.Time) (v150.BenchmarkSnapshot, MarketSummaryV150BenchmarkSource) {
	benchmark, source, _ := loadMarketSummaryV150BenchmarkWithBars(asOf)
	return benchmark, source
}

func loadMarketSummaryV150BenchmarkWithBars(asOf time.Time) (v150.BenchmarkSnapshot, MarketSummaryV150BenchmarkSource, []dailyBar) {
	result := v150.BenchmarkSnapshot{Code: v150.BenchmarkCode, Stale: true}
	source := MarketSummaryV150BenchmarkSource{
		Timing:           MarketSummaryV150EvidenceTiming{EvidenceID: "benchmark-qfq:" + v150.BenchmarkCode + ":data_missing", EvidenceType: "benchmark_adjusted_daily_bar"},
		AdjustmentSource: "data_missing",
	}
	bars, err := loadMarketSummaryV150DailyBarsWithCache(defaultBenchmarkModelCode, defaultBenchmarkCode, asOf.AddDate(0, 0, -180), asOf, 130)
	if err != nil {
		return result, source, nil
	}
	completed := marketSummaryV150CompletedDailyBars(bars, asOf)
	if len(completed) < marketSummaryV150MinimumDailyBars {
		return result, source, completed
	}
	closes := make([]float64, 0, len(completed))
	for _, bar := range completed {
		if bar.Close <= 0 {
			return result, source, completed
		}
		closes = append(closes, bar.Close)
	}
	latest := completed[len(completed)-1]
	result.Close = latest.Close
	result.MA20 = meanLast(closes, 20)
	result.MA60 = meanLast(closes, 60)
	result.MA20FiveDaysAgo = meanRange(closes, len(closes)-25, len(closes)-5)
	cfg := v150.FixedStrategyV150Config()
	if cfg.RelativeStrengthLookbackTradeDays > 0 && len(completed) >= cfg.RelativeStrengthLookbackTradeDays+1 {
		start := completed[len(completed)-cfg.RelativeStrengthLookbackTradeDays-1]
		end := completed[len(completed)-1]
		if start.Close > 0 && end.Close > 0 {
			result.Return20 = end.Close/start.Close - 1
			result.Return20Start = normalizeDailyTradeDate(start.TradeDate).Format(time.DateOnly)
			result.Return20End = normalizeDailyTradeDate(end.TradeDate).Format(time.DateOnly)
			result.HasReturn20Data = true
		}
	}
	result.DataPresent = result.Close > 0 && result.MA20 > 0 && result.MA60 > 0 && result.MA20FiveDaysAgo > 0
	latestDay := normalizeDailyTradeDate(latest.TradeDate)
	requiredLatestDay := marketSummaryV150RequiredLatestDailyBar(asOf)
	provenance := loadMarketSummaryV150DailyDataSource(defaultBenchmarkModelCode, completed)
	source = MarketSummaryV150BenchmarkSource{
		Timing: MarketSummaryV150EvidenceTiming{
			EvidenceID:   "benchmark-qfq:" + v150.BenchmarkCode + ":" + provenance.LatestTradeDate,
			EvidenceType: "benchmark_adjusted_daily_bar", SourceAt: provenance.SourceAt, AvailableAt: provenance.AvailableAt,
		},
		AdjustmentSource: provenance.AdjustmentSource,
		LatestTradeDate:  provenance.LatestTradeDate,
		Complete:         provenance.Complete,
	}
	result.Stale = latestDay.IsZero() || requiredLatestDay.IsZero() || !latestDay.Equal(requiredLatestDay) || !provenance.Complete
	result.HasReturn20Data = result.HasReturn20Data && source.Complete && strings.Contains(strings.ToLower(source.AdjustmentSource), "qfq")
	return result, source, completed
}

func loadMarketSummaryV150PortfolioState(database *gorm.DB, asOf time.Time) (v150.PortfolioState, error) {
	return loadMarketSummaryV150PortfolioStateWithIngestionPolicy(database, asOf, "", true)
}

// loadMarketSummaryV150PublicationPortfolioState is used only at the final
// serialized write boundary. It includes immutable facts already ingested even
// when a historical decision's event time precedes frozen_at, and excludes the
// run whose legacy projection is currently being written so its own rules do
// not consume quota twice.
func loadMarketSummaryV150PublicationPortfolioState(database *gorm.DB, asOf time.Time, excludedRunID string) (v150.PortfolioState, error) {
	return loadMarketSummaryV150PortfolioStateWithIngestionPolicy(database, asOf, excludedRunID, false)
}

func loadMarketSummaryV150PortfolioStateWithIngestionPolicy(
	database *gorm.DB,
	asOf time.Time,
	excludedRunID string,
	requireFrozenByAsOf bool,
) (v150.PortfolioState, error) {
	state, err := loadMarketSummaryV150ExecutionPortfolioStateWithIngestionPolicy(database, "", excludedRunID, asOf, requireFrozenByAsOf)
	if err != nil {
		return state, err
	}

	// Daily recommendation capacity is based on immutable rules issued for the
	// decision date, not on the mutable recommendation projection or eventual
	// fills. Reset the execution-time fill counters and derive the shared slots
	// from the frozen rule/candidate ledger.
	state.TodayEntries = 0
	state.TodaySectorEntries = map[string]int{}
	var rules []models.RuleSnapshot
	if err := database.Model(&models.RuleSnapshot{}).
		Where("strategy_version = ? AND frozen_at IS NOT NULL", v150.StrategyVersion).
		Find(&rules).Error; err != nil {
		return state, err
	}
	var candidates []models.CandidateSnapshot
	if err := database.Model(&models.CandidateSnapshot{}).
		Where("strategy_version = ? AND frozen_at IS NOT NULL", v150.StrategyVersion).
		Find(&candidates).Error; err != nil {
		return state, err
	}
	sectorByCandidate := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		if candidate.FrozenAt == nil || (requireFrozenByAsOf && candidate.FrozenAt.After(asOf)) ||
			strings.TrimSpace(candidate.RunID) == strings.TrimSpace(excludedRunID) {
			continue
		}
		sectorByCandidate[strings.TrimSpace(candidate.CandidateID)] = strings.TrimSpace(candidate.Sector)
	}
	decisionDate := asOf.In(cnLocation()).Format(time.DateOnly)
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if rule.FrozenAt == nil || (requireFrozenByAsOf && rule.FrozenAt.After(asOf)) ||
			strings.TrimSpace(rule.RunID) == strings.TrimSpace(excludedRunID) ||
			!strings.EqualFold(strings.TrimSpace(rule.RuleType), "entry") ||
			strings.TrimSpace(rule.TradeDate) != decisionDate {
			continue
		}
		symbol := normalizeRecommendStockCode(rule.Symbol)
		key := strings.TrimSpace(rule.RunID) + "|" + symbol
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		sector := sectorByCandidate[strings.TrimSpace(rule.CandidateID)]
		if sector == "" {
			return state, fmt.Errorf("frozen entry rule %s has no sector classification", strings.TrimSpace(rule.RuleID))
		}
		seen[key] = struct{}{}
		state.TodayEntries++
		state.TodaySectorEntries[sector]++
	}
	return state, nil
}

func marketSummaryV150IsStopState(raw string) bool {
	text := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(text, "止损") || strings.Contains(text, "stop") || strings.Contains(text, "姝㈡崯")
}

func marketSummaryV150CompletedDailyBars(bars []dailyBar, asOf time.Time) []dailyBar {
	latestCompletedDay := marketSummaryV150RequiredLatestDailyBar(asOf)
	result := make([]dailyBar, 0, len(bars))
	for _, bar := range bars {
		day := normalizeDailyTradeDate(bar.TradeDate)
		if day.IsZero() || latestCompletedDay.IsZero() || day.After(latestCompletedDay) {
			continue
		}
		result = append(result, bar)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].TradeDate.Before(result[j].TradeDate) })
	return result
}

func marketSummaryV150QuoteIsFresh(quote StockInfo, asOf time.Time) bool {
	if asOf.IsZero() {
		return false
	}
	quoteAt, ok := parseMarketSummaryV150QuoteTimestamp(quote)
	if !ok {
		return false
	}
	return marketSummaryV150QuoteTimestampIsFresh(quoteAt, asOf)
}

func marketSummaryV150QuoteTimestampIsFresh(quoteAt, asOf time.Time) bool {
	if quoteAt.IsZero() || asOf.IsZero() {
		return false
	}
	asOf = asOf.In(cnLocation())
	quoteAt = quoteAt.In(cnLocation())
	// Provider clock skew must not leak future information into a decision.
	if quoteAt.After(asOf) {
		return false
	}
	latestLegalAt := marketSummaryV150LatestLegalQuoteAt(asOf)
	if latestLegalAt.IsZero() || quoteAt.After(latestLegalAt) ||
		!normalizeDailyTradeDate(quoteAt).Equal(normalizeDailyTradeDate(latestLegalAt)) {
		return false
	}
	if !marketSummaryV150QuoteTimestampIsInTradingSession(quoteAt) {
		return false
	}
	maximumLag := v150.FixedStrategyV150Config().MaximumRealtimeQuoteLag
	return maximumLag > 0 && latestLegalAt.Sub(quoteAt) <= maximumLag
}

func marketSummaryV150QuoteTimestampIsInTradingSession(value time.Time) bool {
	local := value.In(cnLocation())
	day := normalizeDailyTradeDate(local)
	auctionStart := day.Add(9*time.Hour + 15*time.Minute)
	auctionEnd := day.Add(9*time.Hour + 25*time.Minute)
	morningStart := day.Add(9*time.Hour + 30*time.Minute)
	morningEnd := day.Add(11*time.Hour + 30*time.Minute)
	afternoonStart := day.Add(13 * time.Hour)
	marketClose := day.Add(15 * time.Hour)
	return (!local.Before(auctionStart) && !local.After(auctionEnd)) ||
		(!local.Before(morningStart) && !local.After(morningEnd)) ||
		(!local.Before(afternoonStart) && !local.After(marketClose))
}

func parseMarketSummaryV150QuoteTimestamp(quote StockInfo) (time.Time, bool) {
	dateText := strings.TrimSpace(quote.Date)
	timeText := strings.TrimSpace(quote.Time)
	if dateText == "" || timeText == "" {
		return time.Time{}, false
	}
	raw := dateText + " " + timeText
	for _, layout := range []string{
		time.DateTime,
		"2006-01-02 15:04",
		"2006-01-02 150405",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"20060102 150405",
		"20060102 15:04:05",
	} {
		if parsed, err := time.ParseInLocation(layout, raw, cnLocation()); err == nil && !parsed.IsZero() {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// marketSummaryV150LatestLegalQuoteAt maps wall-clock time to the most recent
// legal A-share quote timestamp. During active sessions it is the decision
// time itself; during auction pauses, lunch, after close and closed days it is
// the preceding session endpoint. Freshness is then measured from this anchor
// rather than merely comparing calendar dates.
func marketSummaryV150LatestLegalQuoteAt(asOf time.Time) time.Time {
	if asOf.IsZero() {
		return time.Time{}
	}
	local := asOf.In(cnLocation())
	requiredDay := marketSummaryV150RequiredQuoteDay(local)
	if requiredDay.IsZero() {
		return time.Time{}
	}
	currentDay := normalizeDailyTradeDate(local)
	if !requiredDay.Equal(currentDay) {
		return requiredDay.Add(15 * time.Hour)
	}

	auctionStart := currentDay.Add(9*time.Hour + 15*time.Minute)
	auctionEnd := currentDay.Add(9*time.Hour + 25*time.Minute)
	morningStart := currentDay.Add(9*time.Hour + 30*time.Minute)
	morningEnd := currentDay.Add(11*time.Hour + 30*time.Minute)
	afternoonStart := currentDay.Add(13 * time.Hour)
	marketClose := currentDay.Add(15 * time.Hour)
	switch {
	case !local.Before(auctionStart) && !local.After(auctionEnd):
		return local
	case local.After(auctionEnd) && local.Before(morningStart):
		return auctionEnd
	case !local.Before(morningStart) && !local.After(morningEnd):
		return local
	case local.After(morningEnd) && local.Before(afternoonStart):
		return morningEnd
	case !local.Before(afternoonStart) && !local.After(marketClose):
		return local
	case local.After(marketClose):
		return marketClose
	default:
		// Before the call auction marketSummaryV150RequiredQuoteDay resolves
		// to the prior open day, so this is only a defensive fail-closed path.
		return time.Time{}
	}
}

func marketSummaryV150RequiredQuoteDay(asOf time.Time) time.Time {
	if asOf.IsZero() {
		return time.Time{}
	}
	local := asOf.In(cnLocation())
	day := normalizeDailyTradeDate(local)
	marketDataExpectedToday := local.Hour() > 9 || (local.Hour() == 9 && local.Minute() >= 15)
	if isCNOpenTradeDaySafe(day) && marketDataExpectedToday {
		return day
	}
	day = day.AddDate(0, 0, -1)
	for attempts := 0; attempts < 14; attempts++ {
		if isCNOpenTradeDaySafe(day) {
			return normalizeDailyTradeDate(day)
		}
		day = day.AddDate(0, 0, -1)
	}
	return time.Time{}
}

func parseMarketSummaryV150ListDate(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	for _, layout := range []string{"20060102", "2006-01-02", "2006/01/02"} {
		if value, err := time.ParseInLocation(layout, text, cnLocation()); err == nil {
			return value, true
		}
	}
	return time.Time{}, false
}

func marketSummaryV150Market(symbol string) v150.Market {
	switch {
	case strings.HasSuffix(strings.ToUpper(symbol), ".SH"):
		return v150.MarketSH
	case strings.HasSuffix(strings.ToUpper(symbol), ".SZ"):
		return v150.MarketSZ
	case strings.HasSuffix(strings.ToUpper(symbol), ".BJ"):
		return v150.MarketBJ
	default:
		return v150.MarketUnknown
	}
}

func meanLast(values []float64, count int) float64 {
	if count <= 0 || len(values) < count {
		return 0
	}
	return meanRange(values, len(values)-count, len(values))
}

func meanRange(values []float64, start, end int) float64 {
	if start < 0 || end > len(values) || start >= end {
		return 0
	}
	total := 0.0
	for _, value := range values[start:end] {
		if value <= 0 {
			return 0
		}
		total += value
	}
	return total / float64(end-start)
}

func marketSummaryV150ATR(bars []dailyBar, count int) float64 {
	if count <= 0 || len(bars) < count+1 {
		return 0
	}
	total := 0.0
	start := len(bars) - count
	for index := start; index < len(bars); index++ {
		bar := bars[index]
		previousClose := bars[index-1].Close
		if bar.High <= 0 || bar.Low <= 0 || previousClose <= 0 {
			return 0
		}
		trueRange := math.Max(bar.High-bar.Low, math.Max(math.Abs(bar.High-previousClose), math.Abs(bar.Low-previousClose)))
		total += trueRange
	}
	return total / float64(count)
}

func marketSummaryV150MaxHigh(bars []dailyBar, count int) float64 {
	if count <= 0 || len(bars) < count {
		return 0
	}
	maximum := 0.0
	for _, bar := range bars[len(bars)-count:] {
		maximum = math.Max(maximum, bar.High)
	}
	return maximum
}

func marketSummaryV150NegativeGapRisk(bars []dailyBar, count int) float64 {
	if len(bars) < 2 {
		return 0
	}
	start := len(bars) - count
	if start < 1 {
		start = 1
	}
	minimum := 0.0
	for index := start; index < len(bars); index++ {
		if bars[index].Open <= 0 || bars[index-1].Close <= 0 {
			continue
		}
		gap := bars[index].Open/bars[index-1].Close - 1
		if gap < minimum {
			minimum = gap
		}
	}
	return minimum
}

func marketSummaryV150AverageAmount(bars []dailyBar, count int) (float64, bool, bool) {
	if count <= 0 || len(bars) < count {
		return 0, false, false
	}
	total := 0.0
	derived := false
	for _, bar := range bars[len(bars)-count:] {
		amount := bar.Amount
		if amount <= 0 && bar.Volume > 0 && bar.High > 0 && bar.Low > 0 && bar.Close > 0 {
			// Sina supplies observed OHLCV but no turnover field. Typical price ×
			// observed shares is a deterministic derived feature, not a made-up
			// default; if either source value is absent the candidate fails closed.
			amount = bar.Volume * (bar.High + bar.Low + bar.Close) / 3
			derived = true
		}
		if amount <= 0 {
			return 0, derived, false
		}
		total += amount
	}
	return total / float64(count), derived, true
}

func marketSummaryV150TrendSignal(candidate v150.Candidate, closes []float64) float64 {
	value := 0.0
	if candidate.Price >= candidate.MA20 {
		value += 0.35
	}
	if candidate.MA20 >= candidate.MA60 {
		value += 0.30
	}
	if len(closes) >= 25 && candidate.MA20 > meanRange(closes, len(closes)-25, len(closes)-5) {
		value += 0.20
	}
	if candidate.DayChangeRatio > 0 {
		value += 0.15
	}
	return clamp01(value)
}

type marketSummaryV150RelativeStrengthFeature struct {
	stockReturn     float64
	benchmarkReturn float64
	relativeReturn  float64
	windowStart     string
	windowEnd       string
}

// applyMarketSummaryV150RelativeStrength completes the 30-point
// TrendRelative input. It never substitutes a stock-only trend score when the
// benchmark window is unavailable: the candidate remains explicitly
// ineligible and carries a structured warning into its immutable snapshot.
func applyMarketSummaryV150RelativeStrength(
	candidate v150.Candidate,
	stockBars []dailyBar,
	asOf time.Time,
	stockSource MarketSummaryV150DailyDataSource,
	benchmark v150.BenchmarkSnapshot,
	benchmarkSource MarketSummaryV150BenchmarkSource,
	benchmarkBars []dailyBar,
	cfg v150.StrategyV150Config,
) (v150.Candidate, []string) {
	candidate.HasRelativeStrengthData = false
	candidate.Return20 = 0
	candidate.BenchmarkReturn20 = 0
	candidate.RelativeReturn20 = 0
	candidate.RelativeStrengthQuality = 0
	candidate.RelativeStrengthStart = ""
	candidate.RelativeStrengthEnd = ""
	candidate.Signals.TrendRelativeStrength = 0

	reject := func(reason string) (v150.Candidate, []string) {
		return candidate, []string{"relative_strength_unavailable:" + reason}
	}
	if !candidate.HasDailyData || !stockSource.Complete || !strings.Contains(strings.ToLower(stockSource.AdjustmentSource), "qfq") {
		return reject("stock_adjusted_daily_data_missing")
	}
	if !benchmark.DataPresent || !benchmark.HasReturn20Data || !benchmarkSource.Complete ||
		!strings.Contains(strings.ToLower(benchmarkSource.AdjustmentSource), "qfq") {
		return reject("benchmark_adjusted_daily_data_missing")
	}
	if cfg.RelativeStrengthLookbackTradeDays <= 0 || cfg.RelativeStrengthFullScoreReturn <= 0 ||
		cfg.TrendComponentShare < 0 || cfg.RelativeComponentShare < 0 ||
		math.Abs(cfg.TrendComponentShare+cfg.RelativeComponentShare-1) > 1e-12 {
		return reject("invalid_frozen_config")
	}

	feature, reason := marketSummaryV150AlignedRelativeStrength(
		marketSummaryV150CompletedDailyBars(stockBars, asOf),
		benchmarkBars,
		cfg.RelativeStrengthLookbackTradeDays,
	)
	if reason != "" {
		return reject(reason)
	}
	if feature.windowStart != benchmark.Return20Start || feature.windowEnd != benchmark.Return20End ||
		math.Abs(feature.benchmarkReturn-benchmark.Return20) > 1e-12 {
		return reject("benchmark_snapshot_window_mismatch")
	}

	candidate.HasRelativeStrengthData = true
	candidate.Return20 = feature.stockReturn
	candidate.BenchmarkReturn20 = feature.benchmarkReturn
	candidate.RelativeReturn20 = feature.relativeReturn
	candidate.RelativeStrengthQuality = clamp01(feature.relativeReturn / cfg.RelativeStrengthFullScoreReturn)
	candidate.RelativeStrengthStart = feature.windowStart
	candidate.RelativeStrengthEnd = feature.windowEnd
	candidate.Signals.TrendRelativeStrength = clamp01(
		cfg.TrendComponentShare*candidate.TrendQuality +
			cfg.RelativeComponentShare*candidate.RelativeStrengthQuality,
	)

	warnings := []string(nil)
	if benchmark.Stale {
		// Staleness still routes the market regime to neutral, but a complete
		// common historical window remains usable and is frozen explicitly.
		warnings = append(warnings, "relative_strength_benchmark_stale_aligned_window")
	}
	return candidate, warnings
}

// marketSummaryV150AlignedRelativeStrength uses the benchmark's last N+1
// completed sessions as the canonical window and requires the stock to have
// an observation on every exact date. Relative return is the price-relative
// ratio (stock gross return / benchmark gross return - 1), not an absolute
// return or an input-universe percentile.
func marketSummaryV150AlignedRelativeStrength(stockBars, benchmarkBars []dailyBar, lookback int) (marketSummaryV150RelativeStrengthFeature, string) {
	if lookback <= 0 || len(benchmarkBars) < lookback+1 {
		return marketSummaryV150RelativeStrengthFeature{}, "benchmark_history_below_lookback"
	}
	stockByDay := make(map[string]dailyBar, len(stockBars))
	for _, bar := range stockBars {
		day := normalizeDailyTradeDate(bar.TradeDate).Format(time.DateOnly)
		if day == "0001-01-01" || bar.Close <= 0 {
			return marketSummaryV150RelativeStrengthFeature{}, "stock_window_invalid_close"
		}
		if _, duplicate := stockByDay[day]; duplicate {
			return marketSummaryV150RelativeStrengthFeature{}, "stock_window_duplicate_date"
		}
		stockByDay[day] = bar
	}

	window := benchmarkBars[len(benchmarkBars)-lookback-1:]
	benchmarkDays := make(map[string]struct{}, len(window))
	for _, bar := range window {
		day := normalizeDailyTradeDate(bar.TradeDate).Format(time.DateOnly)
		if day == "0001-01-01" || bar.Close <= 0 {
			return marketSummaryV150RelativeStrengthFeature{}, "benchmark_window_invalid_close"
		}
		if _, duplicate := benchmarkDays[day]; duplicate {
			return marketSummaryV150RelativeStrengthFeature{}, "benchmark_window_duplicate_date"
		}
		benchmarkDays[day] = struct{}{}
		if stock, ok := stockByDay[day]; !ok || stock.Close <= 0 {
			return marketSummaryV150RelativeStrengthFeature{}, "stock_benchmark_window_not_aligned"
		}
	}

	startDay := normalizeDailyTradeDate(window[0].TradeDate).Format(time.DateOnly)
	endDay := normalizeDailyTradeDate(window[len(window)-1].TradeDate).Format(time.DateOnly)
	stockStart := stockByDay[startDay].Close
	stockEnd := stockByDay[endDay].Close
	benchmarkStart := window[0].Close
	benchmarkEnd := window[len(window)-1].Close
	if stockStart <= 0 || stockEnd <= 0 || benchmarkStart <= 0 || benchmarkEnd <= 0 {
		return marketSummaryV150RelativeStrengthFeature{}, "aligned_window_invalid_close"
	}
	stockReturn := stockEnd/stockStart - 1
	benchmarkReturn := benchmarkEnd/benchmarkStart - 1
	relativeReturn := (stockEnd/stockStart)/(benchmarkEnd/benchmarkStart) - 1
	if math.IsNaN(stockReturn) || math.IsInf(stockReturn, 0) ||
		math.IsNaN(benchmarkReturn) || math.IsInf(benchmarkReturn, 0) ||
		math.IsNaN(relativeReturn) || math.IsInf(relativeReturn, 0) {
		return marketSummaryV150RelativeStrengthFeature{}, "aligned_window_non_finite_return"
	}
	return marketSummaryV150RelativeStrengthFeature{
		stockReturn: stockReturn, benchmarkReturn: benchmarkReturn, relativeReturn: relativeReturn,
		windowStart: startDay, windowEnd: endDay,
	}, ""
}

func marketSummaryV150SetupSignal(candidate v150.Candidate, item marketSummaryIndicatorCandidate) float64 {
	if candidate.ATR14 <= 0 || candidate.MA20 <= 0 || candidate.Price <= 0 {
		return 0
	}
	distance := math.Abs(candidate.Price-math.Max(candidate.MA10, candidate.MA20)) / candidate.ATR14
	proximity := clamp01(1 - distance/1.5)
	volumeRatio, ok := parseLooseFloat(item.Metrics["volumeRatio"])
	volumeQuality := 0.0
	if ok {
		volumeQuality = clamp01((volumeRatio - 0.8) / 1.7)
	}
	return clamp01(0.75*proximity + 0.25*volumeQuality)
}

func marketSummaryV150SectorSignal(item marketSummaryIndicatorCandidate) float64 {
	value := item.ScoreBreakdown["sectorStrength"]
	if value <= 0 {
		return 0
	}
	return clamp01(float64(value) / 15)
}

func marketSummaryV150LiquiditySignal(candidate v150.Candidate) float64 {
	if candidate.AverageAmount20 <= 0 || candidate.Price <= 0 || candidate.ATR14 <= 0 {
		return 0
	}
	liquidity := clamp01((candidate.AverageAmount20 - 100_000_000) / 400_000_000)
	volatility := clamp01(1 - candidate.ATR14/candidate.Price/0.06)
	return clamp01(0.55*liquidity + 0.45*volatility)
}

func marketSummaryV150EventSignal(item marketSummaryIndicatorCandidate, input marketSummaryDiscoveryInput, asOf time.Time, warnings []string) (*time.Time, float64, []MarketSummaryV150EvidenceTiming, MarketSummaryV150EventAssessment, []string) {
	name := strings.TrimSpace(item.StockName)
	code := onlyDigits(item.StockCode)
	sector := strings.TrimSpace(firstNonEmptyText(item.BkName, item.Direction))
	var newest time.Time
	strength := 0.0
	negativeFound := false
	assessment := MarketSummaryV150EventAssessment{Direction: "neutral", Verifier: marketSummaryV150LocalModelSpec}
	timeline := make([]MarketSummaryV150EvidenceTiming, 0, 4)
	for _, snippet := range append(append([]marketSummaryDiscoverySnippet(nil), input.MarketNews...), input.EventCalendar...) {
		text := strings.TrimSpace(snippet.Title + " " + snippet.Summary)
		matchedStrength := 0.0
		switch {
		case name != "" && strings.Contains(text, name):
			matchedStrength = 1
		case code != "" && strings.Contains(onlyDigits(text), code):
			matchedStrength = 1
		case sector != "" && strings.Contains(text, sector):
			matchedStrength = 0.6
		}
		if matchedStrength == 0 {
			continue
		}
		evidenceID := strings.TrimSpace(snippet.EvidenceID)
		if evidenceID == "" {
			evidenceID = "event:" + marketSummaryV150StableHash(snippet.Source+"|"+snippet.Title+"|"+snippet.SourceAt)
		}
		publishedAt, sourceOK := parseMarketSummaryV150EvidenceTime(snippet.SourceAt)
		availableAt, availableOK := parseMarketSummaryV150EvidenceTime(snippet.AvailableAt)
		evidence := MarketSummaryV150EvidenceTiming{
			EvidenceID: evidenceID, EvidenceType: firstNonEmptyText(snippet.Source, "event"), SourceAt: publishedAt, AvailableAt: availableAt,
		}
		timeline = append(timeline, evidence)
		assessment.EvidenceIDs = append(assessment.EvidenceIDs, evidenceID)
		if !sourceOK || !availableOK || publishedAt.After(availableAt) || availableAt.After(asOf) {
			warnings = append(warnings, "event_causality_invalid:"+evidenceID)
			continue
		}
		if publishedAt.After(asOf) || asOf.Sub(publishedAt) > v150.FixedStrategyV150Config().EventFreshness {
			continue
		}
		direction := classifyMarketSummaryV150EventDirection(text)
		importance := 0.6
		confidence := 0.75
		if matchedStrength >= 1 {
			importance = 0.9
			confidence = 0.9
		}
		if direction == "negative" {
			negativeFound = true
			assessment.Direction = "negative"
			assessment.Relevance = math.Max(assessment.Relevance, matchedStrength)
			assessment.Importance = math.Max(assessment.Importance, importance)
			assessment.Confidence = math.Max(assessment.Confidence, confidence)
			continue
		}
		if direction != "positive" || negativeFound {
			continue
		}
		candidateStrength := matchedStrength * importance * confidence
		if candidateStrength > strength || (candidateStrength == strength && (newest.IsZero() || publishedAt.After(newest))) {
			newest = publishedAt
			strength = candidateStrength
			assessment.Direction = "positive"
			assessment.Relevance = matchedStrength
			assessment.Importance = importance
			assessment.Confidence = confidence
		}
	}
	assessment.EvidenceIDs = dedupeNonEmptyStrings(assessment.EvidenceIDs, 16)
	if negativeFound {
		return nil, 0, timeline, assessment, warnings
	}
	if newest.IsZero() || assessment.Direction != "positive" {
		return nil, 0, timeline, assessment, warnings
	}
	return &newest, clamp01(strength), timeline, assessment, warnings
}

func classifyMarketSummaryV150EventDirection(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, keyword := range []string{"减持", "处罚", "立案", "亏损", "下调", "终止", "违约", "诉讼", "调查", "停产", "退市", "暴雷", "风险警示", "跌停"} {
		if strings.Contains(text, keyword) {
			return "negative"
		}
	}
	for _, keyword := range []string{"中标", "增持", "回购", "获批", "签约", "订单", "突破", "上调", "预增", "扭亏", "投产", "盈利"} {
		if strings.Contains(text, keyword) {
			return "positive"
		}
	}
	return "neutral"
}

func parseMarketSummaryV150EvidenceTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil && !parsed.IsZero() {
		return parsed, true
	}
	if parsed, err := time.ParseInLocation(time.DateTime, raw, cnLocation()); err == nil && !parsed.IsZero() {
		return parsed, true
	}
	return time.Time{}, false
}

func marketSummaryV150OpenTradeDaysBetween(from, to time.Time) int {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return 0
	}
	fromDay := normalizeDailyTradeDate(from)
	toDay := normalizeDailyTradeDate(to)
	count := 0
	for day := fromDay.AddDate(0, 0, 1); !day.After(toDay); day = day.AddDate(0, 0, 1) {
		if isCNOpenTradeDaySafe(day) {
			count++
		}
	}
	return count
}

func clamp01(value float64) float64 { return math.Max(0, math.Min(1, value)) }

// Keep strconv imported through a compile-time checked helper: quote fields
// occasionally contain scientific notation and ParseFloat is the final
// fail-closed parser after the project's loose parser.
func parseMarketSummaryV150Float(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}
