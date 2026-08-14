package data

import (
	"fmt"
	"go-stock/backend/logger"
	"net/http"
	"strings"
	"sync"
	"time"
)

type diemengSuspensionItem struct {
	StockCode        string  `json:"stock_code"`
	SuspendDate      string  `json:"suspend_date"`
	SuspendStartTime *string `json:"suspend_start_time"`
	SuspendEndTime   *string `json:"suspend_end_time"`
}

type diemengSuspensionData struct {
	Total    int                     `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Items    []diemengSuspensionItem `json:"items"`
	List     []diemengSuspensionItem `json:"list"`
}

type diemengSuspensionCacheEntry struct {
	CheckedAt time.Time
	Items     []diemengSuspensionItem
	Err       error
}

var (
	fetchDiemengSuspensionsFn = fetchDiemengSuspensions
	diemengSuspensionCacheMu  sync.Mutex
	diemengSuspensionCache    = map[string]diemengSuspensionCacheEntry{}
)

const (
	diemengSuspensionCacheTTL    = 6 * time.Hour
	diemengSuspensionErrorTTL    = 5 * time.Minute
	diemengSuspensionMaxPageSize = 10000
)

func fetchDiemengSuspensions(stockCode string, tradeDate time.Time) ([]diemengSuspensionItem, error) {
	stockCode = normalizeRecommendStockCode(stockCode)
	if stockCode == "" {
		return nil, nil
	}
	if !hasDiemengKey() {
		return nil, fmt.Errorf("missing GO_STOCK_DIEMENG_API_KEY")
	}
	if err := diemengCircuitCheck(); err != nil {
		return nil, err
	}
	dayKey := tradeDate.In(cnLocation()).Format("2006-01-02")
	cacheKey := strings.ToUpper(stockCode) + "|" + dayKey
	if cached, ok := getCachedDiemengSuspensions(cacheKey); ok {
		return cached.Items, cached.Err
	}

	client := newDiemengClient()
	apiKey := strings.TrimSpace(diemengAPIKey())
	waitForDiemengFetchWindow()

	var resp diemengResponse[diemengSuspensionData]
	httpResp, err := client.R().
		SetHeader("apiKey", apiKey).
		SetQueryParam("stock_code", stockCode).
		SetQueryParam("trade_date", dayKey).
		SetQueryParam("page", "0").
		SetQueryParam("page_size", fmt.Sprintf("%d", diemengSuspensionMaxPageSize)).
		SetResult(&resp).
		Get("/stock/suspension")
	if err != nil {
		diemengCircuitRecordFailure(err)
		setCachedDiemengSuspensions(cacheKey, nil, err)
		return nil, err
	}
	if httpResp == nil {
		err = fmt.Errorf("empty http response")
		diemengCircuitRecordFailure(err)
		setCachedDiemengSuspensions(cacheKey, nil, err)
		return nil, err
	}
	if httpResp.StatusCode() == http.StatusTooManyRequests {
		err = fmt.Errorf("diemeng suspension rate limited (HTTP 429)")
		diemengCircuitRecordFailure(err)
		setCachedDiemengSuspensions(cacheKey, nil, err)
		return nil, err
	}
	if httpResp.StatusCode() >= 400 {
		err = fmt.Errorf("diemeng suspension http status %d", httpResp.StatusCode())
		diemengCircuitRecordFailure(err)
		setCachedDiemengSuspensions(cacheKey, nil, err)
		return nil, err
	}
	if resp.Code != 200 {
		err = fmt.Errorf("diemeng suspension api error (code=%d): %s", resp.Code, strings.TrimSpace(resp.Msg))
		diemengCircuitRecordFailure(err)
		setCachedDiemengSuspensions(cacheKey, nil, err)
		return nil, err
	}

	items := resp.Data.Items
	if len(items) == 0 {
		items = resp.Data.List
	}
	diemengCircuitRecordSuccess()
	setCachedDiemengSuspensions(cacheKey, items, nil)
	return items, nil
}

func getCachedDiemengSuspensions(cacheKey string) (diemengSuspensionCacheEntry, bool) {
	diemengSuspensionCacheMu.Lock()
	defer diemengSuspensionCacheMu.Unlock()
	entry, ok := diemengSuspensionCache[cacheKey]
	if !ok {
		return diemengSuspensionCacheEntry{}, false
	}
	ttl := diemengSuspensionCacheTTL
	if entry.Err != nil {
		ttl = diemengSuspensionErrorTTL
	}
	if ttl <= 0 || time.Since(entry.CheckedAt) < ttl {
		return entry, true
	}
	delete(diemengSuspensionCache, cacheKey)
	return diemengSuspensionCacheEntry{}, false
}

func cachedDiemengSuspensions(stockCode string, tradeDate time.Time) (diemengSuspensionCacheEntry, bool) {
	stockCode = normalizeRecommendStockCode(stockCode)
	if stockCode == "" {
		return diemengSuspensionCacheEntry{}, false
	}
	dayKey := tradeDate.In(cnLocation()).Format("2006-01-02")
	return getCachedDiemengSuspensions(strings.ToUpper(stockCode) + "|" + dayKey)
}

func setCachedDiemengSuspensions(cacheKey string, items []diemengSuspensionItem, err error) {
	diemengSuspensionCacheMu.Lock()
	diemengSuspensionCache[cacheKey] = diemengSuspensionCacheEntry{
		CheckedAt: time.Now(),
		Items:     append([]diemengSuspensionItem(nil), items...),
		Err:       err,
	}
	diemengSuspensionCacheMu.Unlock()

}

func clearDiemengSuspensionCache() {
	diemengSuspensionCacheMu.Lock()
	diemengSuspensionCache = map[string]diemengSuspensionCacheEntry{}
	diemengSuspensionCacheMu.Unlock()

}

func minuteCoverageGapCoveredBySuspension(stockCode string, start, end time.Time) bool {
	return minuteCoverageGapCoveredBySuspensionWithFetch(stockCode, start, end, false)
}

func minuteCoverageGapCoveredBySuspensionWithFetch(stockCode string, start, end time.Time, allowFetch bool) bool {
	stockCode = normalizeRecommendStockCode(stockCode)
	if stockCode == "" {
		return false
	}
	start = normalizeMinuteTime(start)
	end = normalizeMinuteTime(end)
	if start.IsZero() || end.IsZero() || start.After(end) {
		return false
	}
	sessions := buildMinuteCoverageSessions(start, end)
	if len(sessions) == 0 {
		return false
	}
	for _, session := range sessions {
		if !minuteCoverageSessionCoveredBySuspensionWithFetch(stockCode, session, allowFetch) {
			return false
		}
	}
	return true
}

func minuteCoverageSessionCoveredBySuspension(stockCode string, session minuteCoverageSession) bool {
	return minuteCoverageSessionCoveredBySuspensionWithFetch(stockCode, session, false)
}

func minuteCoverageSessionCoveredBySuspensionWithFetch(stockCode string, session minuteCoverageSession, allowFetch bool) bool {
	if session.Start.IsZero() || session.End.IsZero() || session.Start.After(session.End) {
		return false
	}
	var items []diemengSuspensionItem
	if cached, ok := cachedDiemengSuspensions(stockCode, session.Start); ok {
		if cached.Err != nil {
			return false
		}
		items = cached.Items
	} else {
		if !allowFetch {
			return false
		}
		var err error
		items, err = fetchDiemengSuspensionsFn(stockCode, session.Start)
		if err != nil {
			logger.SugaredLogger.Warnf("query diemeng suspension failed: code=%s date=%s err=%v", stockCode, session.Start.Format("2006-01-02"), err)
			return false
		}
	}
	for _, item := range items {
		if !sameDiemengSuspensionStock(stockCode, item.StockCode) {
			continue
		}
		start, end, ok := diemengSuspensionWindow(item)
		if !ok {
			continue
		}
		if !start.After(session.Start) && !end.Before(session.End) {
			return true
		}
	}
	return false
}

func sameDiemengSuspensionStock(want, got string) bool {
	want = normalizeRecommendStockCode(want)
	got = normalizeRecommendStockCode(got)
	if want == "" || got == "" {
		return false
	}
	if want == got {
		return true
	}
	return extractAShareSymbol(want) == extractAShareSymbol(got)
}

type minuteCoverageSession struct{ Start, End time.Time }

func buildMinuteCoverageSessions(start, end time.Time) []minuteCoverageSession {
	if start.IsZero() || end.IsZero() || start.After(end) {
		return nil
	}
	loc := cnLocation()
	start, end = normalizeMinuteTime(start.In(loc)), normalizeMinuteTime(end.In(loc))
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
	var result []minuteCoverageSession
	for day, guard := startDay, 0; !day.After(endDay) && guard < 370; day, guard = day.AddDate(0, 0, 1), guard+1 {
		if !isCNOpenTradeDay(day) {
			continue
		}
		for _, pair := range [][2]time.Time{
			{time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc), time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc)},
			{time.Date(day.Year(), day.Month(), day.Day(), 13, 1, 0, 0, loc), time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)},
		} {
			a, b := pair[0], pair[1]
			if b.Before(start) || a.After(end) {
				continue
			}
			if a.Before(start) {
				a = start
			}
			if b.After(end) {
				b = end
			}
			if !a.After(b) {
				result = append(result, minuteCoverageSession{normalizeMinuteTime(a), normalizeMinuteTime(b)})
			}
		}
	}
	return result
}

func normalizeRecommendStockCode(stockCode string) string {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return ""
	}
	if strings.Contains(code, ".") {
		return code
	}
	lower := strings.ToLower(code)
	if strings.HasPrefix(lower, "sh") || strings.HasPrefix(lower, "sz") {
		return strings.ToUpper(ConvertStockCodeToTushareCode(lower))
	}
	digits := RemoveAllNonDigitChar(code)
	if len(digits) != 6 {
		return code
	}
	if strings.HasPrefix(digits, "6") || strings.HasPrefix(digits, "9") || strings.HasPrefix(digits, "5") {
		return digits + ".SH"
	}
	return digits + ".SZ"
}

func diemengSuspensionWindow(item diemengSuspensionItem) (time.Time, time.Time, bool) {
	loc := cnLocation()
	day, ok := parseFlexibleCNDate(item.SuspendDate)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	day = day.In(loc)
	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)
	if item.SuspendStartTime != nil && strings.TrimSpace(*item.SuspendStartTime) != "" {
		if t, ok := parseDiemengSuspensionClock(day, *item.SuspendStartTime); ok {
			start = t
		}
	}
	if item.SuspendEndTime != nil && strings.TrimSpace(*item.SuspendEndTime) != "" {
		if t, ok := parseDiemengSuspensionClock(day, *item.SuspendEndTime); ok {
			end = t
		}
	}
	start = normalizeMinuteTime(start)
	end = normalizeMinuteTime(end)
	if start.IsZero() || end.IsZero() || start.After(end) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

func parseFlexibleCNDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	loc := cnLocation()
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseDiemengSuspensionClock(day time.Time, value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	loc := cnLocation()
	day = day.In(loc)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc), true
		}
	}
	return time.Time{}, false
}
