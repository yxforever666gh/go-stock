package research

import (
	"context"
	"testing"
	"time"

	"go-stock/internal/marketquote"
)

type weekdayTradingCalendar struct{}

func (weekdayTradingCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	weekday := ShanghaiTime(value).Weekday()
	return weekday != time.Saturday && weekday != time.Sunday, nil
}

func TestSizeBuyCapsCashOutflowAtFiftyThousand(t *testing.T) {
	quantity, cost, err := sizeResearchBuy("sh600000", 10, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if quantity != 4900 || quantity%100 != 0 {
		t.Fatalf("quantity=%d", quantity)
	}
	if -cost.NetCashFlow > TargetCashPerTrade {
		t.Fatalf("cash outflow %.2f exceeded cap", -cost.NetCashFlow)
	}
}

func TestNextTradingSessionOpenHandlesLunchCloseAndWeekend(t *testing.T) {
	calendar := weekdayTradingCalendar{}
	lunch, err := NextTradingSessionOpen(context.Background(), calendar, time.Date(2026, 8, 14, 11, 31, 0, 0, shanghaiLocation))
	if err != nil || lunch.Format("2006-01-02 15:04") != "2026-08-14 13:00" {
		t.Fatalf("lunch=%s err=%v", lunch, err)
	}
	close, err := NextTradingSessionOpen(context.Background(), calendar, time.Date(2026, 8, 14, 15, 0, 0, 0, shanghaiLocation))
	if err != nil || close.Format("2006-01-02 15:04") != "2026-08-17 09:30" {
		t.Fatalf("close=%s err=%v", close, err)
	}
}

func TestTradingSessionExcludesLunchAndClose(t *testing.T) {
	loc := shanghaiLocation
	for _, minute := range []string{"09:30", "11:30", "13:00", "14:59"} {
		value, _ := time.ParseInLocation("2006-01-02 15:04", "2026-08-14 "+minute, loc)
		if !IsTradingSession(value) {
			t.Fatalf("%s should be a trading session", minute)
		}
	}
	for _, minute := range []string{"09:29", "11:31", "12:59", "15:00"} {
		value, _ := time.ParseInLocation("2006-01-02 15:04", "2026-08-14 "+minute, loc)
		if IsTradingSession(value) {
			t.Fatalf("%s should not be a trading session", minute)
		}
	}
}

func TestFixedSellCheckScheduleAndTPlusOneAnchor(t *testing.T) {
	calendar := weekdayTradingCalendar{}
	first, err := FirstSellCheck(context.Background(), calendar, time.Date(2026, 8, 14, 14, 30, 0, 0, shanghaiLocation))
	if err != nil || first.Format("2006-01-02 15:04") != "2026-08-17 09:50" {
		t.Fatalf("first=%s err=%v", first, err)
	}
	cases := map[string]string{
		"2026-08-17 09:50": "2026-08-17 10:05",
		"2026-08-17 11:20": "2026-08-17 13:00",
		"2026-08-17 14:45": "2026-08-18 09:50",
	}
	for input, want := range cases {
		value, _ := time.ParseInLocation("2006-01-02 15:04", input, shanghaiLocation)
		next, nextErr := NextSellCheck(context.Background(), calendar, value)
		if nextErr != nil || next.Format("2006-01-02 15:04") != want {
			t.Fatalf("after %s next=%s err=%v", input, next, nextErr)
		}
	}
}

func TestCustomSellReviewScheduleControlsStartAndInterval(t *testing.T) {
	calendar := weekdayTradingCalendar{}
	schedule, err := NewSellReviewSchedule("10:00", 20)
	if err != nil {
		t.Fatal(err)
	}
	first, err := FirstSellCheckWithSchedule(context.Background(), calendar, time.Date(2026, 8, 14, 14, 30, 0, 0, shanghaiLocation), schedule)
	if err != nil || first.Format("2006-01-02 15:04") != "2026-08-17 10:00" {
		t.Fatalf("first=%s err=%v", first, err)
	}
	cases := map[string]string{
		"2026-08-17 10:00": "2026-08-17 10:20",
		"2026-08-17 11:20": "2026-08-17 13:00",
		"2026-08-17 14:40": "2026-08-18 10:00",
	}
	for input, want := range cases {
		value, _ := time.ParseInLocation("2006-01-02 15:04", input, shanghaiLocation)
		next, nextErr := NextSellCheckWithSchedule(context.Background(), calendar, value, schedule)
		if nextErr != nil || next.Format("2006-01-02 15:04") != want {
			t.Fatalf("after %s next=%s err=%v", input, next, nextErr)
		}
	}
}

func TestIndependentSellReviewCadenceAndFailureRetryRespectSessions(t *testing.T) {
	calendar := weekdayTradingCalendar{}
	schedule, err := NewSellReviewSchedule("09:55", 15)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		input string
		retry bool
		want  string
	}{
		{name: "independent cadence", input: "2026-08-17 10:07", want: "2026-08-17 10:22"},
		{name: "normal lunch clamp", input: "2026-08-17 11:20", want: "2026-08-17 13:00"},
		{name: "retry five minutes", input: "2026-08-17 10:07", retry: true, want: "2026-08-17 10:12"},
		{name: "retry lunch clamp", input: "2026-08-17 11:28", retry: true, want: "2026-08-17 13:00"},
		{name: "retry after close", input: "2026-08-17 14:58", retry: true, want: "2026-08-18 09:55"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			value, _ := time.ParseInLocation("2006-01-02 15:04", item.input, shanghaiLocation)
			var next time.Time
			var nextErr error
			if item.retry {
				next, nextErr = NextSellReviewRetry(context.Background(), calendar, value, schedule)
			} else {
				next, nextErr = NextSellCheckWithSchedule(context.Background(), calendar, value, schedule)
			}
			if nextErr != nil || next.Format("2006-01-02 15:04") != item.want {
				t.Fatalf("next=%s err=%v want=%s", next, nextErr, item.want)
			}
		})
	}
}

func TestTPlusOne(t *testing.T) {
	loc := shanghaiLocation
	entry := time.Date(2026, 8, 14, 10, 0, 0, 0, loc)
	if isTPlusOne(entry, time.Date(2026, 8, 14, 14, 0, 0, 0, loc)) {
		t.Fatal("same-day sell must be blocked")
	}
	if !isTPlusOne(entry, time.Date(2026, 8, 17, 9, 30, 0, 0, loc)) {
		t.Fatal("later trading date should be eligible")
	}
}

func TestBuyQuoteRejectsSuspensionAndPriceLimits(t *testing.T) {
	base := marketquote.Quote{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, At: time.Now()}
	for _, quote := range []marketquote.Quote{
		{Code: base.Code, Name: base.Name, Market: base.Market, Price: base.Price, At: base.At, Suspended: true},
		{Code: base.Code, Name: base.Name, Market: base.Market, Price: base.Price, At: base.At, LimitUp: true},
		{Code: base.Code, Name: base.Name, Market: base.Market, Price: base.Price, At: base.At, LimitDown: true},
	} {
		if err := validateBuyQuote(quote); err == nil {
			t.Fatalf("quote should be rejected: %+v", quote)
		}
	}
	if err := validateBuyQuote(base); err != nil {
		t.Fatalf("tradable quote rejected: %v", err)
	}
}
