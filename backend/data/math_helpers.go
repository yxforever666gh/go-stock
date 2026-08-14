package data

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func round2(value float64) float64 { return math.Round(value*100) / 100 }

func parseYieldOverviewTradeDay(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", "20060102", time.DateTime, time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, value, cnLocation()); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// The 1.6.0 research flow has no legacy yield-coverage cache.
func clearMinuteCoverageStatsCache() {}

func mergeSyncErr(base, current error) error {
	if current == nil {
		return base
	}
	if base == nil {
		return current
	}
	if strings.Contains(base.Error(), current.Error()) {
		return base
	}
	return fmt.Errorf("%v; %v", base, current)
}

func minuteProviderSettings() *Settings {
	cfg := GetSettingConfig()
	if cfg == nil {
		return nil
	}
	return cfg.Settings
}

func minutePublicSinaEnabled() bool {
	settings := minuteProviderSettings()
	return settings == nil || normalizeMinuteProviderMode(settings.MinuteProviderMode) != "public" || settings.SinaMinuteEnabled
}

func minutePublicAkshareEnabled() bool {
	settings := minuteProviderSettings()
	return settings == nil || normalizeMinuteProviderMode(settings.MinuteProviderMode) != "public" || settings.AkshareEnabled
}

func normalizeMinuteCoverageEnd(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	loc := cnLocation()
	t = t.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	if t.Before(time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc)) {
		return time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc)
	}
	if t.After(time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)) {
		return time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)
	}
	return normalizeMinuteTime(t)
}
