package data

import (
	"time"

	appconfig "go-stock/internal/config"
)

// defaultMinuteCoverableTradeDays controls the default yield/minute scope.
// 0 means "no limit": keep all "股票推荐记录" in "股票收益率".
const defaultMinuteCoverableTradeDays = 0

func minuteCoverableTradeDays() int {
	return appconfig.Load().Minute.CoverTradeDays
}

func isWeekendCN(t time.Time) bool {
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// subtractTradingDaysByWeekday subtracts N trading days ignoring holidays.
// If n=0, returns the input day.
func subtractTradingDaysByWeekday(day time.Time, n int) time.Time {
	if n <= 0 {
		return day
	}
	loc := day.Location()
	cur := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	remain := n
	for remain > 0 {
		cur = cur.AddDate(0, 0, -1)
		if isWeekendCN(cur) {
			continue
		}
		remain--
	}
	return cur
}

func minuteCoverableStartMinute(latestTradeDate time.Time) time.Time {
	days := minuteCoverableTradeDays()
	if days == 0 {
		return time.Time{}
	}
	loc := cnLocation()
	d := time.Date(latestTradeDate.Year(), latestTradeDate.Month(), latestTradeDate.Day(), 0, 0, 0, 0, loc)
	// Include "today" as day 1. E.g. 5 trading days => today + previous 4.
	startDay := subtractTradingDaysByWeekday(d, days-1)
	return time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 9, 30, 0, 0, loc)
}
