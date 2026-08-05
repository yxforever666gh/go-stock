package data

import (
	"testing"
	"time"

	"go-stock/backend/strategy/v150"
)

func TestMarketSummaryV150AfterHoursActivationWindowMatchesRuleExpiry(t *testing.T) {
	loc := cnLocation()
	decisionAt := time.Date(2026, 8, 7, 15, 10, 0, 0, loc) // Friday after close.
	validFrom := nextMarketSummary15MinuteBarStart(decisionAt)
	decisionIndex := marketSummaryV150TradeDayIndex(decisionAt)
	validFromIndex := marketSummaryV150TradeDayIndex(validFrom)
	if !validFrom.After(decisionAt) || validFrom.Weekday() != time.Monday || validFromIndex != decisionIndex+1 {
		t.Fatalf("after-hours timeline decision=%s/%d validFrom=%s/%d", decisionAt, decisionIndex, validFrom, validFromIndex)
	}

	plan := v150.TradePlan{
		Path:                   v150.PathPullback,
		DecisionTradeDayIndex:  decisionIndex,
		ValidFromTradeDayIndex: validFromIndex,
		ValidFromAt:            validFrom,
		EvaluationMinutes:      15,
		Support:                10,
		EntryMin:               9.9,
		EntryMax:               10.1,
		ValidTradeDays:         v150.FixedStrategyV150Config().ActivationValidTradeDays,
	}

	days := make([]time.Time, 0, plan.ValidTradeDays+1)
	day := normalizeDailyTradeDate(validFrom)
	for offset := 0; offset <= plan.ValidTradeDays; offset++ {
		if offset > 0 {
			day = shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
		}
		days = append(days, day)
		start := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc)
		bar := v150.Bar{
			Index:           (validFromIndex + offset) * 16,
			TradeDayIndex:   marketSummaryV150TradeDayIndex(start),
			IntervalMinutes: 15,
			Start:           start,
			End:             start.Add(15*time.Minute - time.Nanosecond),
			Open:            10,
			High:            10.1,
			Low:             9.9,
			Close:           10,
			Completed:       true,
		}
		signal, _ := v150.DetectActivation(plan, v150.Bar{}, bar, v150.ActivationState{})
		if offset < plan.ValidTradeDays {
			if !signal.Triggered {
				t.Fatalf("validFrom trade day %d (index %d) should remain active: %+v", offset+1, bar.TradeDayIndex, signal)
			}
		} else if signal.Triggered || signal.Reason != v150.RejectActivationExpired {
			t.Fatalf("fourth validFrom trade day should be expired: %+v", signal)
		}
	}

	expiresAt := marketSummaryV150PlanExpiresAt(validFrom, plan.ValidTradeDays)
	wantExpiresAt := time.Date(days[plan.ValidTradeDays-1].Year(), days[plan.ValidTradeDays-1].Month(), days[plan.ValidTradeDays-1].Day(), 15, 0, 0, 0, loc)
	if !expiresAt.Equal(wantExpiresAt) || marketSummaryV150TradeDayIndex(expiresAt) != validFromIndex+plan.ValidTradeDays-1 {
		t.Fatalf("rule expiresAt=%s/index%d, want %s/index%d", expiresAt, marketSummaryV150TradeDayIndex(expiresAt), wantExpiresAt, validFromIndex+plan.ValidTradeDays-1)
	}
	dayFourOpen := time.Date(days[plan.ValidTradeDays].Year(), days[plan.ValidTradeDays].Month(), days[plan.ValidTradeDays].Day(), 9, 30, 0, 0, loc)
	if !dayFourOpen.After(expiresAt) {
		t.Fatalf("day-four execution bar %s must be after frozen rule expiry %s", dayFourOpen, expiresAt)
	}
}
