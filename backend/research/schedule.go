package research

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

type SellReviewSchedule struct {
	StartHour      int
	StartMinute    int
	IntervalMinute int
}

func DefaultSellReviewSchedule() SellReviewSchedule {
	return SellReviewSchedule{StartHour: 9, StartMinute: 50, IntervalMinute: 15}
}

func NewSellReviewSchedule(start string, interval int) (SellReviewSchedule, error) {
	parts := strings.Split(strings.TrimSpace(start), ":")
	if len(parts) != 2 {
		return SellReviewSchedule{}, fmt.Errorf("invalid sell review start time %q", start)
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || minute < 0 || minute > 59 || hour < 0 || hour > 23 {
		return SellReviewSchedule{}, fmt.Errorf("invalid sell review start time %q", start)
	}
	startMinute := hour*60 + minute
	if startMinute < 9*60+30 || startMinute > 11*60+30 {
		return SellReviewSchedule{}, errors.New("sell review start time must be between 09:30 and 11:30")
	}
	if interval < 5 || interval > 120 {
		return SellReviewSchedule{}, errors.New("sell review interval must be between 5 and 120 minutes")
	}
	return SellReviewSchedule{StartHour: hour, StartMinute: minute, IntervalMinute: interval}, nil
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
	return FirstSellCheckWithSchedule(ctx, calendar, entryAt, DefaultSellReviewSchedule())
}

func FirstSellCheckWithSchedule(ctx context.Context, calendar TradingCalendar, entryAt time.Time, schedule SellReviewSchedule) (time.Time, error) {
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
			return time.Date(y, m, d, schedule.StartHour, schedule.StartMinute, 0, 0, shanghaiLocation), nil
		}
	}
	return time.Time{}, errors.New("no next trading day found within calendar scan limit")
}

// NextSellCheck returns the next sell-review time relative to the completion
// of the current stock's review. Each holding therefore keeps an independent
// cadence instead of snapping back to a shared intraday clock grid.
func NextSellCheck(ctx context.Context, calendar TradingCalendar, after time.Time) (time.Time, error) {
	return NextSellCheckWithSchedule(ctx, calendar, after, DefaultSellReviewSchedule())
}

func NextSellCheckWithSchedule(ctx context.Context, calendar TradingCalendar, after time.Time, schedule SellReviewSchedule) (time.Time, error) {
	return nextSellCheckAfter(ctx, calendar, after, schedule, time.Duration(schedule.IntervalMinute)*time.Minute)
}

// NextSellReviewRetry returns the next fixed five-minute retry for a failed
// holding review. When the retry would fall in lunch or after the close, it is
// moved to the next tradable boundary; after the close this becomes the next
// trading day's configured review start.
func NextSellReviewRetry(ctx context.Context, calendar TradingCalendar, after time.Time, schedule SellReviewSchedule) (time.Time, error) {
	return nextSellCheckAfter(ctx, calendar, after, schedule, 5*time.Minute)
}

func nextSellCheckAfter(ctx context.Context, calendar TradingCalendar, after time.Time, schedule SellReviewSchedule, delay time.Duration) (time.Time, error) {
	if calendar == nil {
		return time.Time{}, errors.New("trading calendar is unavailable")
	}
	if schedule.IntervalMinute < 5 || schedule.IntervalMinute > 120 {
		schedule = DefaultSellReviewSchedule()
	}
	local := ShanghaiTime(after)
	target := local.Add(delay)
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
		start := time.Date(y, m, d, schedule.StartHour, schedule.StartMinute, 0, 0, shanghaiLocation)
		if scanned > 0 {
			return start, nil
		}
		morningClose := time.Date(y, m, d, 11, 30, 0, 0, shanghaiLocation)
		afternoonOpen := time.Date(y, m, d, 13, 0, 0, 0, shanghaiLocation)
		closeAt := time.Date(y, m, d, 15, 0, 0, 0, shanghaiLocation)
		if target.Before(start) {
			return start, nil
		}
		if !target.After(morningClose) {
			return target, nil
		}
		if target.Before(afternoonOpen) {
			return afternoonOpen, nil
		}
		if target.Before(closeAt) {
			return target, nil
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
