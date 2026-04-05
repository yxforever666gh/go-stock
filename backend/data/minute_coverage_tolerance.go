package data

import "time"

// minuteStartCovered reports whether cacheStart is acceptable for requiredStart.
// Most minute providers label the first morning bar as 09:31 (covering 09:30-09:31).
// Business logic may require 09:30 as the buy marker; in that case 09:31 should be
// treated as covered instead of a permanent 1-minute gap.
func minuteStartCovered(requiredStart, cacheStart time.Time) bool {
	if requiredStart.IsZero() || cacheStart.IsZero() {
		return false
	}
	loc := cnLocation()
	requiredStart = normalizeMinuteTime(requiredStart.In(loc))
	cacheStart = normalizeMinuteTime(cacheStart.In(loc))

	// Normal case: cache starts at or before required start.
	if !cacheStart.After(requiredStart) {
		return true
	}

	// Tolerance case: provider's first bar time is one minute later at session open.
	if cacheStart.Sub(requiredStart) != time.Minute {
		return false
	}
	day := time.Date(requiredStart.Year(), requiredStart.Month(), requiredStart.Day(), 0, 0, 0, 0, loc)
	morningOpen := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc)
	afternoonOpen := time.Date(day.Year(), day.Month(), day.Day(), 13, 0, 0, 0, loc)
	return requiredStart.Equal(morningOpen) || requiredStart.Equal(afternoonOpen)
}
