package data

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go-stock/backend/logger"
	appconfig "go-stock/internal/config"
)

var (
	sinaIntradayWarmMu   sync.Mutex
	sinaIntradayWarmSeen = map[string]time.Time{} // YYYY-MM-DD -> last attempt
)

func warmupSinaMinuteBarsIntraday(now time.Time, codes []string) error {
	// Only warm up in "diemeng/auto" provider modes. If user explicitly sets
	// GO_STOCK_MINUTE_PROVIDER=sina, the per-stock sync path will already hit Sina.
	provider := appconfig.Load().Minute.Provider
	if provider == "akshare" || provider == "sina" {
		return nil
	}

	if len(codes) == 0 {
		return nil
	}

	loc := cnLocation()
	cur := now.In(loc)
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
	dayKey := day.Format("2006-01-02")

	// Avoid repeated intraday pulls when user clicks multiple times.
	sinaIntradayWarmMu.Lock()
	if last, ok := sinaIntradayWarmSeen[dayKey]; ok && time.Since(last) < 45*time.Second {
		sinaIntradayWarmMu.Unlock()
		return nil
	}
	sinaIntradayWarmSeen[dayKey] = time.Now()
	sinaIntradayWarmMu.Unlock()

	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc)
	end := normalizeMinuteCoverageEnd(cur)
	if !start.Before(end) {
		return nil
	}

	normalized := make([]string, 0, len(codes))
	seen := map[string]struct{}{}
	for _, raw := range codes {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		normalized = append(normalized, code)
	}
	if len(normalized) == 0 {
		return nil
	}

	logger.SugaredLogger.Infof("sina intraday warmup codes=%d %s~%s", len(normalized), start.Format("15:04"), end.Format("15:04"))

	var mergedErr error
	for _, code := range normalized {
		bars, _, err := fetchMinuteBarsWithSina(code, start, end)
		if err != nil {
			mergedErr = mergeSyncErr(mergedErr, err)
			continue
		}
		if len(bars) == 0 {
			continue
		}
		if _, upsertErr := upsertMinuteBarsToCache(code, bars, "sina"); upsertErr != nil {
			mergedErr = mergeSyncErr(mergedErr, fmt.Errorf("upsert minute bars failed (code=%s): %w", code, upsertErr))
			continue
		}
	}
	return mergedErr
}
