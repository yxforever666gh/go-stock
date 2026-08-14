package research

import "time"

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
