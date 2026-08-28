package data

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/marketdata"

	"gorm.io/gorm"
)

type ChartService struct {
	mainDB          *gorm.DB
	minuteDB        *gorm.DB
	now             func() time.Time
	providerFactory chartProviderFactory
	isTradingDay    chartTradingDayFunc
}

func NewChartService() *ChartService {
	return NewChartServiceWithStorage(db.Dao, db.MinuteDao)
}

func NewChartServiceWithStorage(mainDB, minuteDB *gorm.DB) *ChartService {
	return &ChartService{mainDB: mainDB, minuteDB: minuteDB, now: time.Now, providerFactory: productionChartProviders, isTradingDay: defaultChartTradingDay}
}

func (s *ChartService) missingIntervals(bars []ChartBar, period string, from, to time.Time) []ChartMissingInterval {
	calendar := s.isTradingDay
	if calendar == nil {
		calendar = defaultChartTradingDay
	}
	return chartMissingIntervalsWithCalendar(bars, period, from, to, calendar)
}

// Chart is cache-first. It only performs provider work when the selected
// scope is empty, stale, or contains an observable intraday gap.
func (s *ChartService) Chart(ctx context.Context, request ChartRequest) marketdata.DataEnvelope[ChartData] {
	normalized, err := NormalizeChartRequest(request, s.now())
	if err != nil {
		return chartFailureEnvelope(request, s.now, "validation", err)
	}
	cached := s.LoadCachedChart(ctx, normalized)
	if len(cached.Data.Bars) > 0 && chartCacheFresh(cached.FetchedAt, normalized, s.now()) && len(cached.Data.MissingIntervals) == 0 {
		return cached
	}
	return s.RefreshChart(ctx, normalized)
}

func (s *ChartService) LoadCachedChart(_ context.Context, request ChartRequest) marketdata.DataEnvelope[ChartData] {
	normalized, err := NormalizeChartRequest(request, s.now())
	if err != nil {
		return chartFailureEnvelope(request, s.now, "validation", err)
	}
	snapshot, cacheErr := loadChartBarsFromCache(s.minuteDB, normalized)
	if normalized.Instrument.AssetType == "stock" && normalized.Period == ChartPeriod1Minute && normalized.Adjustment == ChartAdjustmentNone {
		legacy, legacyUpdatedAt, legacyErr := s.loadLegacyResearchMinuteBars(normalized)
		snapshot.Bars = mergeChartBars(snapshot.Bars, legacy)
		if legacyErr != nil && cacheErr == nil {
			cacheErr = legacyErr
		}
		if legacyUpdatedAt.After(snapshot.UpdatedAt) {
			snapshot.UpdatedAt = legacyUpdatedAt
		}
	}
	snapshot.Bars = normalizeChartBars(snapshot.Bars, normalized.From, normalized.To)
	if len(snapshot.Bars) > normalized.Limit {
		snapshot.Bars = snapshot.Bars[len(snapshot.Bars)-normalized.Limit:]
	}
	status := marketdata.StatusOK
	errorsOut := []marketdata.DataError{}
	sources := []marketdata.SourceState{{Provider: "cache", Status: marketdata.StatusOK, AsOf: latestChartBarTime(snapshot.Bars)}}
	if cacheErr != nil {
		status = marketdata.StatusPartial
		errorsOut = append(errorsOut, marketdata.DataError{Provider: "cache", Code: "cache_read_failed", Message: sanitizeChartError(cacheErr)})
		sources[0].Status = marketdata.StatusPartial
		sources[0].Message = sanitizeChartError(cacheErr)
	}
	if len(snapshot.Bars) == 0 {
		status = marketdata.StatusUnavailable
		sources[0].Status = marketdata.StatusUnavailable
	}
	missing := s.missingIntervals(snapshot.Bars, normalized.Period, normalized.From, normalized.To)
	if normalized.Period != baseChartPeriod(normalized.Period) {
		baseMissing, baseErr := s.loadCachedBaseMissing(normalized)
		missing = mergeChartMissingIntervals(missing, baseMissing)
		if baseErr != nil {
			if cacheErr == nil {
				cacheErr = baseErr
			} else {
				cacheErr = fmt.Errorf("%v; base cache: %w", cacheErr, baseErr)
			}
			status = marketdata.StatusPartial
			errorsOut = append(errorsOut, marketdata.DataError{Provider: "cache", Code: "base_cache_read_failed", Message: sanitizeChartError(baseErr)})
			sources[0].Status = marketdata.StatusPartial
		}
	}
	if len(snapshot.Bars) > 0 && len(missing) > 0 {
		status = marketdata.StatusPartial
	}
	fetchedAt := snapshot.UpdatedAt
	if fetchedAt.IsZero() {
		fetchedAt = s.now()
	}
	return marketdata.DataEnvelope[ChartData]{Data: chartData(normalized, snapshot.Bars, missing), Source: "cache", AsOf: latestChartBarTime(snapshot.Bars),
		FetchedAt: fetchedAt, Status: status, Errors: errorsOut, Sources: sources, Warnings: []string{}}
}

func (s *ChartService) loadCachedBaseMissing(request ChartRequest) ([]ChartMissingInterval, error) {
	baseRequest := request
	baseRequest.Period = baseChartPeriod(request.Period)
	baseRequest.Limit = chartBaseBarLimit(request.Period, request.Limit)
	snapshot, cacheErr := loadChartBarsFromCache(s.minuteDB, baseRequest)
	if baseRequest.Instrument.AssetType == "stock" && baseRequest.Period == ChartPeriod1Minute && baseRequest.Adjustment == ChartAdjustmentNone {
		legacy, _, legacyErr := s.loadLegacyResearchMinuteBars(baseRequest)
		snapshot.Bars = mergeChartBars(snapshot.Bars, legacy)
		if cacheErr == nil {
			cacheErr = legacyErr
		}
	}
	snapshot.Bars = normalizeChartBars(snapshot.Bars, baseRequest.From, baseRequest.To)
	return s.missingIntervals(snapshot.Bars, baseRequest.Period, baseRequest.From, baseRequest.To), cacheErr
}

func (s *ChartService) RefreshChart(ctx context.Context, request ChartRequest) marketdata.DataEnvelope[ChartData] {
	normalized, err := NormalizeChartRequest(request, s.now())
	if err != nil {
		return chartFailureEnvelope(request, s.now, "validation", err)
	}
	baseRequest := normalized
	baseRequest.Period = baseChartPeriod(normalized.Period)
	baseRequest.Limit = chartBaseBarLimit(normalized.Period, normalized.Limit)
	if baseRequest.Period == ChartPeriod1Minute && normalized.Adjustment != ChartAdjustmentNone {
		baseRequest.Adjustment = ChartAdjustmentNone
	}
	cachedBase, cachedBaseErr := loadChartBarsFromCache(s.minuteDB, baseRequest)
	if baseRequest.Instrument.AssetType == "stock" && baseRequest.Period == ChartPeriod1Minute && baseRequest.Adjustment == ChartAdjustmentNone {
		legacy, _, legacyErr := s.loadLegacyResearchMinuteBars(baseRequest)
		cachedBase.Bars = mergeChartBars(cachedBase.Bars, legacy)
		if cachedBaseErr == nil {
			cachedBaseErr = legacyErr
		}
	}
	baseBars, source, asOf, sources, errorsOut := s.collectChartProviders(ctx, baseRequest)
	baseBars = mergeChartBars(baseBars, cachedBase.Bars)
	if source == "" && len(cachedBase.Bars) > 0 {
		source = "cache"
	}
	if cachedBaseErr != nil {
		errorsOut = append(errorsOut, marketdata.DataError{Provider: "cache", Code: "cache_read_failed", Message: sanitizeChartError(cachedBaseErr)})
	}
	if baseRequest.Period == ChartPeriod1Minute && normalized.Adjustment != ChartAdjustmentNone && len(baseBars) > 0 {
		// Persist the proven raw observations before deriving a separately
		// scoped adjusted series.
		if rawCacheErr := upsertChartBarsToCache(s.minuteDB, baseRequest, baseBars, s.now()); rawCacheErr != nil {
			errorsOut = append(errorsOut, marketdata.DataError{Provider: "cache", Code: "cache_write_failed", Message: sanitizeChartError(rawCacheErr)})
		}
		adjusted, adjustmentSources, adjustmentErrors := s.adjustMinuteBars(ctx, normalized, baseBars)
		baseBars = adjusted
		sources = append(sources, adjustmentSources...)
		errorsOut = append(errorsOut, adjustmentErrors...)
		baseRequest.Adjustment = normalized.Adjustment
	}
	baseBars = normalizeChartBars(baseBars, normalized.From, normalized.To)
	baseMissing := s.missingIntervals(baseBars, baseRequest.Period, normalized.From, normalized.To)
	cacheErr := error(nil)
	if len(baseBars) > 0 {
		cacheErr = upsertChartBarsToCache(s.minuteDB, baseRequest, baseBars, s.now())
	}
	finalBars, aggregateErr := aggregateChartBars(baseBars, normalized.Period)
	if aggregateErr == nil {
		finalBars = normalizeChartBars(finalBars, normalized.From, normalized.To)
		if len(finalBars) > normalized.Limit {
			finalBars = finalBars[len(finalBars)-normalized.Limit:]
		}
		if normalized.Period != baseRequest.Period && len(finalBars) > 0 {
			if err := upsertChartBarsToCache(s.minuteDB, normalized, finalBars, s.now()); cacheErr == nil {
				cacheErr = err
			}
		}
	}
	if aggregateErr != nil {
		errorsOut = append(errorsOut, marketdata.DataError{Provider: "aggregation", Code: "aggregation_failed", Message: aggregateErr.Error()})
	}
	if cacheErr != nil {
		errorsOut = append(errorsOut, marketdata.DataError{Provider: "cache", Code: "cache_write_failed", Message: sanitizeChartError(cacheErr)})
	}
	if len(finalBars) == 0 {
		cached := s.LoadCachedChart(ctx, normalized)
		if len(cached.Data.Bars) > 0 {
			cached.Status = marketdata.StatusStale
			cached.Errors = append(cached.Errors, errorsOut...)
			cached.Sources = append(cached.Sources, sources...)
			return cached
		}
	}
	missing := s.missingIntervals(finalBars, normalized.Period, normalized.From, normalized.To)
	if normalized.Period != baseRequest.Period {
		missing = mergeChartMissingIntervals(missing, baseMissing)
	}
	status := marketdata.StatusOK
	if len(finalBars) == 0 {
		status = marketdata.StatusUnavailable
	} else if len(errorsOut) > 0 || len(missing) > 0 {
		status = marketdata.StatusPartial
	}
	if source == "" && len(finalBars) > 0 {
		source = "cache"
	}
	if asOf.IsZero() {
		asOf = latestChartBarTime(finalBars)
	}
	return marketdata.DataEnvelope[ChartData]{Data: chartData(normalized, finalBars, missing), Source: source, AsOf: asOf,
		FetchedAt: s.now(), Status: status, Errors: errorsOut, Sources: sources, Warnings: []string{}}
}

func (s *ChartService) collectChartProviders(ctx context.Context, request ChartRequest) ([]ChartBar, string, time.Time, []marketdata.SourceState, []marketdata.DataError) {
	factory := s.providerFactory
	if factory == nil {
		factory = productionChartProviders
	}
	providers := factory(request)
	merged := make([]ChartBar, 0)
	sources := make([]marketdata.SourceState, 0, len(providers))
	errorsOut := make([]marketdata.DataError, 0)
	selectedSource := ""
	var asOf time.Time
	for _, provider := range providers {
		result := provider.Fetch(ctx, request)
		result.Bars = normalizeChartBars(result.Bars, request.From, request.To)
		providerMissing := s.missingIntervals(result.Bars, request.Period, request.From, request.To)
		status := marketdata.StatusOK
		message := ""
		if result.Err != nil {
			message = sanitizeChartError(result.Err)
			if len(result.Bars) > 0 {
				status = marketdata.StatusPartial
			} else {
				status = marketdata.StatusUnavailable
			}
			errorsOut = append(errorsOut, marketdata.DataError{Provider: provider.Name(), Code: "provider_unavailable", Message: message})
		} else if len(result.Bars) == 0 {
			status = marketdata.StatusUnavailable
			message = "数据源返回空数据"
			errorsOut = append(errorsOut, marketdata.DataError{Provider: provider.Name(), Code: "empty_data", Message: message})
		} else if len(providerMissing) > 0 {
			status = marketdata.StatusPartial
			message = "数据源未覆盖完整请求范围，已继续尝试下一来源"
		}
		sources = append(sources, marketdata.SourceState{Provider: provider.Name(), Status: status, AsOf: result.AsOf,
			SourceRef: result.SourceRef, Message: message})
		if len(result.Bars) > 0 && selectedSource == "" {
			selectedSource = provider.Name()
		}
		if result.AsOf.After(asOf) {
			asOf = result.AsOf
		}
		// Existing rows win, so primary-provider bars cannot be overwritten by a
		// fallback for the same timestamp.
		merged = mergeChartBars(merged, result.Bars)
		if result.Err == nil && len(result.Bars) > 0 && len(s.missingIntervals(merged, request.Period, request.From, request.To)) == 0 {
			break
		}
	}
	remaining := s.missingIntervals(merged, request.Period, request.From, request.To)
	if len(merged) > 0 && len(remaining) > 0 {
		errorsOut = append(errorsOut, marketdata.DataError{Provider: "chart", Code: "range_incomplete",
			Message: fmt.Sprintf("所有可用来源仍有 %d 个请求区间未覆盖", len(remaining))})
	}
	return merged, selectedSource, asOf, sources, errorsOut
}

func (s *ChartService) loadLegacyResearchMinuteBars(request ChartRequest) ([]ChartBar, time.Time, error) {
	keys, err := chartMinuteCacheKeys(request.Instrument.Code)
	if err != nil {
		return nil, time.Time{}, err
	}
	rows := make([]minuteBar, 0)
	for _, key := range keys {
		cached, cacheErr := listMinuteBarsFromMinuteDatabase(s.minuteDB, key, request.From, request.To)
		if cacheErr != nil {
			return nil, time.Time{}, cacheErr
		}
		rows = append(rows, cached...)
	}
	proven, rejected := dedupeProvenResearchChartBars(rows)
	result := make([]ChartBar, 0, len(proven))
	for _, row := range proven {
		result = append(result, ChartBar{At: row.TradeTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close,
			Volume: row.Volume, Amount: row.Amount, Source: row.Source})
	}
	if rejected > 0 {
		return result, legacyMinuteCacheUpdatedAt(s.minuteDB, keys, request.From, request.To), fmt.Errorf("ignored %d adjusted or invalid legacy minute bars", rejected)
	}
	return result, legacyMinuteCacheUpdatedAt(s.minuteDB, keys, request.From, request.To), nil
}

func legacyMinuteCacheUpdatedAt(database *gorm.DB, keys []string, from, to time.Time) time.Time {
	if database == nil || len(keys) == 0 {
		return time.Time{}
	}
	type updatedRow struct {
		UpdatedAt *int64 `gorm:"column:updated_at"`
	}
	row := updatedRow{}
	if err := database.Model(&minuteCacheDBBar{}).Select("MAX(updated_at) AS updated_at").
		Where("stock_code IN ? AND trade_time >= ? AND trade_time <= ?", keys, from.UnixMilli(), to.UnixMilli()).Scan(&row).Error; err != nil || row.UpdatedAt == nil {
		return time.Time{}
	}
	return time.UnixMilli(*row.UpdatedAt).In(cnLocation())
}

func chartData(request ChartRequest, bars []ChartBar, missing []ChartMissingInterval) ChartData {
	if bars == nil {
		bars = []ChartBar{}
	}
	if missing == nil {
		missing = []ChartMissingInterval{}
	}
	return ChartData{Instrument: request.Instrument, Period: request.Period, Adjustment: request.Adjustment, Timezone: "Asia/Shanghai",
		RangeFrom: request.From, RangeTo: request.To, Bars: bars, MissingIntervals: missing}
}

func chartFailureEnvelope(request ChartRequest, now func() time.Time, code string, err error) marketdata.DataEnvelope[ChartData] {
	if now == nil {
		now = time.Now
	}
	data := ChartData{Instrument: request.Instrument, Period: request.Period, Adjustment: request.Adjustment, Timezone: "Asia/Shanghai",
		RangeFrom: request.From, RangeTo: request.To, Bars: []ChartBar{}, MissingIntervals: []ChartMissingInterval{}}
	return marketdata.DataEnvelope[ChartData]{Data: data, FetchedAt: now(), Status: marketdata.StatusUnavailable,
		Errors: []marketdata.DataError{{Provider: "chart", Code: code, Message: err.Error()}}, Sources: []marketdata.SourceState{}, Warnings: []string{}}
}

func chartCacheFresh(fetchedAt time.Time, request ChartRequest, now time.Time) bool {
	if fetchedAt.IsZero() {
		return false
	}
	now = chartShanghaiTime(now)
	if request.To.Format(time.DateOnly) != now.Format(time.DateOnly) {
		return true
	}
	ttl := 5 * time.Minute
	if _, minute := chartPeriods[request.Period]; minute {
		ttl = 30 * time.Second
	}
	return now.Sub(fetchedAt) <= ttl
}

func mergeChartBars(primary, fallback []ChartBar) []ChartBar {
	result := append([]ChartBar(nil), primary...)
	seen := make(map[int64]struct{}, len(result))
	for _, bar := range result {
		seen[bar.At.UnixMilli()] = struct{}{}
	}
	for _, bar := range fallback {
		if _, ok := seen[bar.At.UnixMilli()]; ok {
			continue
		}
		seen[bar.At.UnixMilli()] = struct{}{}
		result = append(result, bar)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result
}

func dataErrorsMessage(items []marketdata.DataError) string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		if value := strings.TrimSpace(item.Message); value != "" {
			values = append(values, item.Provider+": "+value)
		}
	}
	return strings.Join(values, "; ")
}

func chartEnvelopeError(envelope marketdata.DataEnvelope[ChartData]) error {
	if envelope.Status == marketdata.StatusUnavailable && len(envelope.Data.Bars) == 0 {
		message := dataErrorsMessage(envelope.Errors)
		if message == "" {
			message = "chart data is unavailable"
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}
