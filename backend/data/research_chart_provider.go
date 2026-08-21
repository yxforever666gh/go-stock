package data

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/research"

	"gorm.io/gorm"
)

// ResearchChartProvider is deliberately split from the lifecycle context
// collector. LoadCached only touches minute.db; Refresh is the sole path that
// may call configured upstream providers and the real-time quote service.
type ResearchChartProvider struct {
	quotes   *ResearchQuoteProvider
	minuteDB *gorm.DB
}

func NewResearchChartProvider(quotes *ResearchQuoteProvider) *ResearchChartProvider {
	return NewResearchChartProviderWithStorage(quotes, db.MinuteDao)
}

func NewResearchChartProviderWithStorage(quotes *ResearchQuoteProvider, minuteDB *gorm.DB) *ResearchChartProvider {
	if quotes == nil {
		quotes = NewResearchQuoteProvider()
	}
	return &ResearchChartProvider{quotes: quotes, minuteDB: minuteDB}
}

func (provider *ResearchChartProvider) LoadCached(_ context.Context, code string, start, end time.Time) (research.ChartProviderSnapshot, error) {
	keys, err := chartMinuteCacheKeys(code)
	if err != nil {
		return research.ChartProviderSnapshot{}, err
	}
	rows := make([]minuteBar, 0)
	for _, key := range keys {
		cached, cacheErr := listMinuteBarsFromMinuteDatabase(provider.minuteDB, key, start, end)
		if cacheErr != nil {
			return research.ChartProviderSnapshot{}, cacheErr
		}
		rows = append(rows, cached...)
	}
	rows = dedupeChartMinuteBars(rows)
	result := research.ChartProviderSnapshot{Bars: make([]research.ChartMinuteBar, 0, len(rows)), ProviderErrors: []research.ChartProviderError{}}
	rejected := 0
	for _, row := range rows {
		if !minuteBarSourceProvesUnadjusted(row.Source) {
			rejected++
			continue
		}
		if row.Open <= 0 || row.High <= 0 || row.Low <= 0 || row.Close <= 0 || row.High < row.Low {
			rejected++
			continue
		}
		result.Bars = append(result.Bars, research.ChartMinuteBar{At: row.TradeTime, Open: row.Open, High: row.High,
			Low: row.Low, Close: row.Close, Volume: row.Volume, Amount: row.Amount, Source: strings.TrimSpace(row.Source)})
	}
	if rejected > 0 {
		result.ProviderErrors = append(result.ProviderErrors, research.ChartProviderError{Provider: "cache",
			Message: fmt.Sprintf("已忽略 %d 条无法证明为未复权或价格无效的分钟数据", rejected)})
	}
	result.RefreshedAt = provider.chartCacheUpdatedAt(keys, start, end)
	if result.RefreshedAt.IsZero() {
		result.RefreshedAt = time.Now().In(cnLocation())
	}
	return result, nil
}

func (provider *ResearchChartProvider) Refresh(ctx context.Context, code string, start, end time.Time, sessionDates []string) (research.ChartProviderSnapshot, error) {
	keys, err := chartMinuteCacheKeys(code)
	if err != nil {
		return research.ChartProviderSnapshot{}, err
	}
	canonical := keys[0]
	errorsOut := make([]research.ChartProviderError, 0)

	for _, date := range sessionDates {
		day, parseErr := time.ParseInLocation("2006-01-02", date, cnLocation())
		if parseErr != nil {
			continue
		}
		winStart := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, cnLocation())
		winEnd := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, cnLocation())
		if date == end.In(cnLocation()).Format("2006-01-02") && end.Before(winEnd) {
			winEnd = end.In(cnLocation())
		}
		if !winEnd.After(winStart) || provider.chartAnyCacheWindowCovered(keys, winStart, winEnd) {
			continue
		}
		providers := enabledChartMinuteProviders(day)
		if len(providers) == 0 {
			errorsOut = append(errorsOut, research.ChartProviderError{Provider: "configuration",
				Message: date + " 没有已启用且适用于该日期的一分钟数据源"})
			continue
		}
		for providerIndex, item := range providers {
			fallbackSuffix := ""
			if providerIndex < len(providers)-1 {
				fallbackSuffix = "，已继续尝试下一来源"
			}
			bars, source, fetchErr := item.fetch(canonical, winStart, winEnd)
			accepted := 0
			if len(bars) > 0 {
				proven := make([]minuteBar, 0, len(bars))
				for _, bar := range bars {
					barSource := strings.TrimSpace(bar.Source)
					if barSource == "" {
						barSource = source
					}
					if !minuteBarSourceProvesUnadjusted(barSource) {
						continue
					}
					bar.Source = barSource
					proven = append(proven, bar)
				}
				if len(proven) == 0 {
					errorsOut = append(errorsOut, research.ChartProviderError{Provider: item.name,
						Message: date + " 返回的数据无法证明为未复权，已拒绝写入"})
				} else if _, upsertErr := upsertMinuteBarsToCache(canonical, proven, source); upsertErr != nil {
					errorsOut = append(errorsOut, research.ChartProviderError{Provider: item.name, Message: sanitizeChartError(upsertErr)})
				} else {
					accepted = len(proven)
				}
			}
			if fetchErr != nil {
				errorsOut = append(errorsOut, research.ChartProviderError{Provider: item.name,
					Message: date + "：" + sanitizeChartError(fetchErr) + fallbackSuffix})
			} else if len(bars) == 0 {
				errorsOut = append(errorsOut, research.ChartProviderError{Provider: item.name,
					Message: date + " 返回空数据" + fallbackSuffix})
			}
			if provider.chartAnyCacheWindowCovered(keys, winStart, winEnd) {
				break
			}
			if accepted > 0 && fetchErr == nil {
				errorsOut = append(errorsOut, research.ChartProviderError{Provider: item.name,
					Message: fmt.Sprintf("%s 已写入 %d 条分钟数据但覆盖仍不完整%s", date, accepted, fallbackSuffix)})
			}
		}
	}

	result, err := provider.LoadCached(ctx, code, start, end)
	if err != nil {
		return result, err
	}
	if quote, quoteErr := provider.quotes.CurrentQuote(ctx, code); quoteErr == nil {
		result.Quote = &quote
		if result.RefreshedAt.IsZero() || quote.At.After(result.RefreshedAt) {
			result.RefreshedAt = quote.At
		}
	} else if !hasChartProviderError(errorsOut, "realtime_quote") {
		errorsOut = append(errorsOut, research.ChartProviderError{Provider: "realtime_quote", Message: sanitizeChartError(quoteErr)})
	}
	result.ProviderErrors = append(result.ProviderErrors, errorsOut...)
	return result, nil
}

type chartMinuteProvider struct {
	name  string
	fetch func(string, time.Time, time.Time) ([]minuteBar, string, error)
}

func enabledChartMinuteProviders(day time.Time) []chartMinuteProvider {
	settings := minuteProviderSettings()
	available := make(map[string]chartMinuteProvider, 4)
	if settings != nil && settings.PrivateMinuteEnabled &&
		normalizePrivateMinuteLevel(settings.PrivateMinuteLevel) == "1min" &&
		strings.TrimSpace(settings.PrivateMinuteBaseURL) != "" &&
		strings.TrimSpace(settings.PrivateMinuteAPIKey) != "" {
		available["private"] = chartMinuteProvider{name: "diemeng", fetch: fetchMinuteBarsWithDiemeng}
	}
	for id, provider := range availablePublicChartMinuteProviders(settings, day) {
		available[id] = provider
	}
	legacyMode, persistedOrder := "public", ""
	if settings != nil {
		legacyMode, persistedOrder = settings.MinuteProviderMode, settings.MinuteProviderOrder
	}
	order, _ := NormalizeMinuteProviderOrder(splitProviderOrder(persistedOrder), legacyMode)
	result := make([]chartMinuteProvider, 0, len(available))
	for _, id := range order {
		if provider, ok := available[id]; ok {
			result = append(result, provider)
		}
	}
	return result
}

func availablePublicChartMinuteProviders(settings *Settings, day time.Time) map[string]chartMinuteProvider {
	akshare, sina, tencent := true, true, true
	if settings != nil {
		akshare, sina, tencent = settings.AkshareEnabled, settings.SinaMinuteEnabled, settings.TencentMinuteEnabled
	}
	today := time.Now().In(cnLocation())
	isToday := day.Format("2006-01-02") == today.Format("2006-01-02")
	recent := today.Sub(day) <= 7*24*time.Hour && !day.After(today)
	result := make(map[string]chartMinuteProvider, 3)
	if isToday && tencent {
		result["tencent"] = chartMinuteProvider{name: "tencent", fetch: fetchMinuteBarsWithTencent}
	}
	if isToday && sina {
		result["sina"] = chartMinuteProvider{name: "sina", fetch: fetchMinuteBarsWithSina}
	}
	if !isToday && recent && tencent {
		result["tencent"] = chartMinuteProvider{name: "tencent", fetch: fetchMinuteBarsWithTencent}
	}
	if akshare {
		result["akshare"] = chartMinuteProvider{name: "akshare", fetch: fetchMinuteBarsWithAkShare}
	}
	return result
}

func chartMinuteCacheKeys(code string) ([]string, error) {
	normalized, ok := research.NormalizeMainlandCode(code)
	if !ok {
		return nil, fmt.Errorf("only Shanghai/Shenzhen A shares are supported")
	}
	digits := normalized[2:]
	exchange := "SZ"
	if strings.HasPrefix(normalized, "sh") {
		exchange = "SH"
	}
	canonical := digits + "." + exchange
	return []string{canonical, strings.ToUpper(normalized), digits}, nil
}

func dedupeChartMinuteBars(rows []minuteBar) []minuteBar {
	if len(rows) == 0 {
		return rows
	}
	// Prefer the canonical cache row encountered first for each minute.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TradeTime.Before(rows[j].TradeTime) })
	seen := make(map[int64]struct{}, len(rows))
	result := make([]minuteBar, 0, len(rows))
	for _, row := range rows {
		key := normalizeMinuteTime(row.TradeTime).UnixMilli()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, row)
	}
	return result
}

func (provider *ResearchChartProvider) chartCacheWindowCovered(code string, start, end time.Time) bool {
	rows, err := listMinuteBarsFromMinuteDatabase(provider.minuteDB, code, start, end)
	if err != nil || len(rows) == 0 {
		return false
	}
	valid := make([]minuteBar, 0, len(rows))
	for _, row := range rows {
		if minuteBarSourceProvesUnadjusted(row.Source) && row.Close > 0 {
			valid = append(valid, row)
		}
	}
	if len(valid) == 0 {
		return false
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].TradeTime.Before(valid[j].TradeTime) })
	return minuteBarsCoverTradingSessionsForStockWithSuspensionFetch(code, valid, start, end, false)
}

func (provider *ResearchChartProvider) chartAnyCacheWindowCovered(keys []string, start, end time.Time) bool {
	for _, key := range keys {
		if provider.chartCacheWindowCovered(key, start, end) {
			return true
		}
	}
	return false
}

func (provider *ResearchChartProvider) chartCacheUpdatedAt(keys []string, start, end time.Time) time.Time {
	if provider.minuteDB == nil || len(keys) == 0 {
		return time.Time{}
	}
	type row struct {
		UpdatedAt *int64 `gorm:"column:updated_at"`
	}
	value := row{}
	if err := provider.minuteDB.Model(&minuteCacheDBBar{}).Select("MAX(updated_at) AS updated_at").
		Where("stock_code IN ? AND trade_time >= ? AND trade_time <= ?", keys, minuteTimeMillis(start), minuteTimeMillis(end)).Scan(&value).Error; err != nil || value.UpdatedAt == nil {
		return time.Time{}
	}
	return time.UnixMilli(*value.UpdatedAt).In(cnLocation())
}

var chartSecretPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|authorization)=([^&\s]+)`)

func sanitizeChartError(err error) string {
	if err == nil {
		return ""
	}
	message := chartSecretPattern.ReplaceAllString(strings.Join(strings.Fields(err.Error()), " "), "$1=[REDACTED]")
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500]) + "…"
	}
	return message
}

func hasChartProviderError(items []research.ChartProviderError, provider string) bool {
	for _, item := range items {
		if item.Provider == provider {
			return true
		}
	}
	return false
}
