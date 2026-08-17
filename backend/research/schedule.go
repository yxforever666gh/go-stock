package research

import (
	"context"
	"time"
)

const ActivationTradingWindow = 4 * time.Hour

var shanghaiLocation = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}()

func ShanghaiTime(value time.Time) time.Time { return value.In(shanghaiLocation) }

func NextLifecycleCheck(after time.Time) time.Time {
	next := ShanghaiTime(after).Add(DefaultCheckMins * time.Minute)
	year, month, day := next.Date()
	morningOpen := time.Date(year, month, day, 9, 30, 0, 0, shanghaiLocation)
	afternoonOpen := time.Date(year, month, day, 13, 0, 0, 0, shanghaiLocation)
	closeAt := time.Date(year, month, day, 15, 0, 0, 0, shanghaiLocation)
	if next.Before(morningOpen) {
		return morningOpen
	}
	if next.After(time.Date(year, month, day, 11, 30, 0, 0, shanghaiLocation)) && next.Before(afternoonOpen) {
		return afternoonOpen
	}
	if !next.Before(closeAt) {
		following := next.AddDate(0, 0, 1)
		for following.Weekday() == time.Saturday || following.Weekday() == time.Sunday {
			following = following.AddDate(0, 0, 1)
		}
		y, m, d := following.Date()
		return time.Date(y, m, d, 9, 30, 0, 0, shanghaiLocation)
	}
	return next
}

func IsAfterMarketClose(value time.Time) bool {
	local := ShanghaiTime(value)
	y, m, d := local.Date()
	return !local.Before(time.Date(y, m, d, 15, 0, 0, 0, shanghaiLocation))
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

// AccumulatedTradingTime returns only the time that overlaps an actual Shanghai
// Stock Exchange trading session. Lunch, nights and closed calendar days do not
// consume the activation budget. The calculation stops at cap when cap is
// positive so stale recommendations cannot cause an unbounded calendar scan.
func AccumulatedTradingTime(ctx context.Context, calendar TradingCalendar, start, end time.Time, cap time.Duration) (time.Duration, error) {
	start, end = ShanghaiTime(start), ShanghaiTime(end)
	if !end.After(start) {
		return 0, nil
	}
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, shanghaiLocation)
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, shanghaiLocation)
	total := time.Duration(0)
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		trading, err := calendar.IsTradingDay(ctx, day)
		if err != nil {
			return total, err
		}
		if !trading {
			continue
		}
		year, month, date := day.Date()
		sessions := [][2]time.Time{
			{time.Date(year, month, date, 9, 30, 0, 0, shanghaiLocation), time.Date(year, month, date, 11, 30, 0, 0, shanghaiLocation)},
			{time.Date(year, month, date, 13, 0, 0, 0, shanghaiLocation), time.Date(year, month, date, 15, 0, 0, 0, shanghaiLocation)},
		}
		for _, session := range sessions {
			from, to := session[0], session[1]
			if start.After(from) {
				from = start
			}
			if end.Before(to) {
				to = end
			}
			if to.After(from) {
				total += to.Sub(from)
				if cap > 0 && total >= cap {
					return cap, nil
				}
			}
		}
	}
	return total, nil
}
