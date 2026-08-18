package research

import (
	"context"
	"errors"
	"time"
)

var shanghaiLocation = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}()

func ShanghaiTime(value time.Time) time.Time { return value.In(shanghaiLocation) }

var sellCheckClock = [][2]int{
	{9, 50}, {10, 5}, {10, 20}, {10, 35}, {10, 50}, {11, 5}, {11, 20},
	{13, 0}, {13, 15}, {13, 30}, {13, 45}, {14, 0}, {14, 15}, {14, 30}, {14, 45},
}

// NextTradingSessionOpen returns the next point at which a queued direct buy
// may be attempted. It keeps an in-session time unchanged, moves lunch to
// 13:00, and consults the strict calendar before selecting a later 09:30 open.
func NextTradingSessionOpen(ctx context.Context, calendar TradingCalendar, after time.Time) (time.Time, error) {
	if calendar == nil {
		return time.Time{}, errors.New("trading calendar is unavailable")
	}
	local := ShanghaiTime(after)
	for scanned := 0; scanned < 740; scanned++ {
		day := local.AddDate(0, 0, scanned)
		trading, err := calendar.IsTradingDay(ctx, day)
		if err != nil {
			return time.Time{}, err
		}
		if !trading {
			continue
		}
		y, m, d := day.Date()
		morning := time.Date(y, m, d, 9, 30, 0, 0, shanghaiLocation)
		afternoon := time.Date(y, m, d, 13, 0, 0, 0, shanghaiLocation)
		closeAt := time.Date(y, m, d, 15, 0, 0, 0, shanghaiLocation)
		if scanned > 0 || local.Before(morning) {
			return morning, nil
		}
		if IsTradingSession(local) {
			return local, nil
		}
		if local.Before(afternoon) {
			return afternoon, nil
		}
		if local.Before(closeAt) {
			return local, nil
		}
	}
	return time.Time{}, errors.New("no trading session found within calendar scan limit")
}

// FirstSellCheck anchors a new position to 09:50 on the strict next trading
// day, regardless of the intraday entry time.
func FirstSellCheck(ctx context.Context, calendar TradingCalendar, entryAt time.Time) (time.Time, error) {
	if calendar == nil {
		return time.Time{}, errors.New("trading calendar is unavailable")
	}
	entry := ShanghaiTime(entryAt)
	for scanned := 1; scanned < 740; scanned++ {
		day := entry.AddDate(0, 0, scanned)
		trading, err := calendar.IsTradingDay(ctx, day)
		if err != nil {
			return time.Time{}, err
		}
		if trading {
			y, m, d := day.Date()
			return time.Date(y, m, d, 9, 50, 0, 0, shanghaiLocation), nil
		}
	}
	return time.Time{}, errors.New("no next trading day found within calendar scan limit")
}

// NextSellCheck returns the next fixed 1.6.5 sell-review slot. Slots never
// drift with model latency and missed slots are not replayed.
func NextSellCheck(ctx context.Context, calendar TradingCalendar, after time.Time) (time.Time, error) {
	if calendar == nil {
		return time.Time{}, errors.New("trading calendar is unavailable")
	}
	local := ShanghaiTime(after)
	for scanned := 0; scanned < 740; scanned++ {
		day := local.AddDate(0, 0, scanned)
		trading, err := calendar.IsTradingDay(ctx, day)
		if err != nil {
			return time.Time{}, err
		}
		if !trading {
			continue
		}
		y, m, d := day.Date()
		for _, clock := range sellCheckClock {
			candidate := time.Date(y, m, d, clock[0], clock[1], 0, 0, shanghaiLocation)
			if candidate.After(local) {
				return candidate, nil
			}
		}
	}
	return time.Time{}, errors.New("no sell check found within calendar scan limit")
}

func IsTradingSession(value time.Time) bool {
	local := ShanghaiTime(value)
	y, m, d := local.Date()
	morningOpen := time.Date(y, m, d, 9, 30, 0, 0, shanghaiLocation)
	morningClose := time.Date(y, m, d, 11, 30, 0, 0, shanghaiLocation)
	afternoonOpen := time.Date(y, m, d, 13, 0, 0, 0, shanghaiLocation)
	afternoonClose := time.Date(y, m, d, 15, 0, 0, 0, shanghaiLocation)
	return (!local.Before(morningOpen) && !local.After(morningClose)) ||
		(!local.Before(afternoonOpen) && local.Before(afternoonClose))
}
