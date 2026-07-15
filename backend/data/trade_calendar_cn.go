package data

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type cnTradeCalCache struct {
	mu        sync.Mutex
	startDay  time.Time
	endDay    time.Time
	openDays  map[string]bool
	loadedAt  time.Time
	lastError string
}

var globalCNTradeCalCache = &cnTradeCalCache{}

const (
	cnTradeCalCacheTTL       = 6 * time.Hour
	cnTradeCalFailureTTL     = 5 * time.Minute
	cnTradeCalMaxFetchSecond = int64(3)
)

func (c *cnTradeCalCache) lookup(day time.Time) (bool, bool) {
	loc := cnLocation()
	day = time.Date(day.In(loc).Year(), day.In(loc).Month(), day.In(loc).Day(), 0, 0, 0, 0, loc)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.openDays == nil || c.startDay.IsZero() || c.endDay.IsZero() {
		return false, false
	}
	if day.Before(c.startDay) || day.After(c.endDay) {
		return false, false
	}
	key := day.Format("2006-01-02")
	return c.openDays[key], true
}

func (c *cnTradeCalCache) ensureRange(startDay, endDay time.Time) map[string]bool {
	loc := cnLocation()
	startDay = time.Date(startDay.In(loc).Year(), startDay.In(loc).Month(), startDay.In(loc).Day(), 0, 0, 0, 0, loc)
	endDay = time.Date(endDay.In(loc).Year(), endDay.In(loc).Month(), endDay.In(loc).Day(), 0, 0, 0, 0, loc)
	if endDay.Before(startDay) {
		startDay, endDay = endDay, startDay
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Cache hits: range is covered and not too old.
	if c.openDays != nil &&
		!c.startDay.IsZero() && !c.endDay.IsZero() &&
		!startDay.Before(c.startDay) && !endDay.After(c.endDay) &&
		time.Since(c.loadedAt) < cnTradeCalCacheTTL {
		return c.openDays
	}
	// A calendar outage must not turn a local database query into hundreds of
	// sequential network retries. Keep the failure briefly and let callers use
	// their documented weekday fallback until the cooldown expires.
	if c.openDays == nil && c.lastError != "" && !c.loadedAt.IsZero() &&
		time.Since(c.loadedAt) < cnTradeCalFailureTTL {
		return nil
	}

	setting := GetSettingConfig()
	timeout := int64(60)
	if setting != nil && setting.CrawlTimeOut > 0 {
		timeout = setting.CrawlTimeOut
	}
	if timeout > cnTradeCalMaxFetchSecond {
		timeout = cnTradeCalMaxFetchSecond
	}
	tushare := NewTushareApi(setting)
	openMap, err := tushare.GetTradeCalOpenMap("SSE", startDay, endDay, timeout)
	if err != nil {
		// Fall back to weekday-only behavior if trade calendar cannot be loaded.
		c.openDays = nil
		c.startDay = time.Time{}
		c.endDay = time.Time{}
		c.loadedAt = time.Now()
		c.lastError = err.Error()
		return nil
	}

	c.openDays = openMap
	c.startDay = startDay
	c.endDay = endDay
	c.loadedAt = time.Now()
	c.lastError = ""
	return c.openDays
}

func isCNOpenTradeDay(day time.Time) bool {
	loc := cnLocation()
	day = day.In(loc)
	d0 := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)

	// Pre-filter weekends quickly.
	if isWeekendCN(d0) {
		return false
	}

	if open, ok := globalCNTradeCalCache.lookup(d0); ok {
		return open
	}

	// Ensure a wide, stable range around the day so shifting day-by-day (holiday
	// skipping) stays within the cached window.
	window := 550
	openMap := globalCNTradeCalCache.ensureRange(d0.AddDate(0, 0, -window), d0.AddDate(0, 0, window))
	if openMap == nil {
		// No calendar: best effort weekday-only.
		return !isWeekendCN(d0)
	}
	return openMap[d0.Format("2006-01-02")]
}

// IsCNOpenTradeDay 判断A股(上交所日历)当天是否开盘。
// 该方法会优先使用交易日历（含节假日/调休），在日历不可用时回退到工作日判断。
func IsCNOpenTradeDay(day time.Time) bool {
	return isCNOpenTradeDay(day)
}

// IsCNOpenTradeDayStrict 严格判断A股(上交所日历)当天是否开盘。
// 与 IsCNOpenTradeDay 不同，该方法在交易日历不可用时返回错误，
// 供不能接受“按工作日回退”的调用方使用。
func IsCNOpenTradeDayStrict(day time.Time) (bool, error) {
	loc := cnLocation()
	day = day.In(loc)
	d0 := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	if isWeekendCN(d0) {
		return false, nil
	}

	if open, ok := globalCNTradeCalCache.lookup(d0); ok {
		return open, nil
	}

	window := 550
	openMap := globalCNTradeCalCache.ensureRange(d0.AddDate(0, 0, -window), d0.AddDate(0, 0, window))
	if openMap == nil {
		globalCNTradeCalCache.mu.Lock()
		lastErr := globalCNTradeCalCache.lastError
		globalCNTradeCalCache.mu.Unlock()
		return false, ensureTradeCalReadable(fmt.Errorf("%s", strings.TrimSpace(lastErr)))
	}
	return openMap[d0.Format("2006-01-02")], nil
}

func shiftToNextCNOpenTradeDay(day time.Time) time.Time {
	loc := cnLocation()
	day = time.Date(day.In(loc).Year(), day.In(loc).Month(), day.In(loc).Day(), 0, 0, 0, 0, loc)
	for i := 0; i < 80; i++ {
		if isCNOpenTradeDay(day) {
			return day
		}
		day = day.AddDate(0, 0, 1)
	}
	return day
}

func shiftToPrevCNOpenTradeDay(day time.Time) time.Time {
	loc := cnLocation()
	day = time.Date(day.In(loc).Year(), day.In(loc).Month(), day.In(loc).Day(), 0, 0, 0, 0, loc)
	for i := 0; i < 80; i++ {
		if isCNOpenTradeDay(day) {
			return day
		}
		day = day.AddDate(0, 0, -1)
	}
	return day
}

func ensureTradeCalReadable(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "unknown error"
	}
	return fmt.Errorf("trade calendar unavailable: %s", msg)
}
