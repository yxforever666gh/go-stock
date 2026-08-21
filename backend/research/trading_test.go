package research

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type weekdayTradingCalendar struct{}

func (weekdayTradingCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	weekday := ShanghaiTime(value).Weekday()
	return weekday != time.Saturday && weekday != time.Sunday, nil
}

func TestSizeBuyTargetsFirstCashOutflowAboveFiftyThousand(t *testing.T) {
	quantity, cost, err := SizeBuy("sh600000", 10, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if quantity != 5000 || quantity%100 != 0 {
		t.Fatalf("quantity=%d", quantity)
	}
	if -cost.NetCashFlow <= TargetCashPerTrade {
		t.Fatalf("cash outflow %.2f did not strictly exceed target", -cost.NetCashFlow)
	}
	previous := CalculateBuyCost(10, quantity-100)
	if -previous.NetCashFlow > TargetCashPerTrade {
		t.Fatalf("previous lot cash outflow %.2f already exceeded target", -previous.NetCashFlow)
	}
	if cost.Commission < MinimumCommission || cost.TransferFee <= 0 || cost.SlippageAmount <= 0 {
		t.Fatalf("cost=%+v", cost)
	}

	starQuantity, starCost, err := SizeBuy("sh688001", 50, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if starQuantity != 1000 || starQuantity%200 != 0 || -starCost.NetCashFlow <= TargetCashPerTrade {
		t.Fatalf("STAR quantity=%d cost=%+v", starQuantity, starCost)
	}
	if _, _, err := SizeBuy("bj430001", 10, 100000); err == nil {
		t.Fatal("Beijing exchange must be rejected")
	}
}

func TestSizeBuyUsesOneHighPriceLotOrLargestAffordableFallback(t *testing.T) {
	quantity, cost, err := SizeBuy("sz300308", 941.41, 154814.32202159002)
	if err != nil || quantity != 100 || math.Abs(-cost.NetCashFlow-94264.35389371) > 1e-6 {
		t.Fatalf("high-price lot quantity=%d cost=%+v err=%v", quantity, cost, err)
	}

	quantity, cost, err = SizeBuy("sh600000", 10, 40000)
	if err != nil || quantity != 3900 || -cost.NetCashFlow > 40000+1e-8 {
		t.Fatalf("fallback quantity=%d cost=%+v err=%v", quantity, cost, err)
	}
	if next := CalculateBuyCost(10, quantity+100); -next.NetCashFlow <= 40000 {
		t.Fatalf("fallback did not use largest affordable lot: next=%+v", next)
	}

	if _, _, err = SizeBuy("sh600000", 500, 40000); !errors.Is(err, ErrMinimumOrder) {
		t.Fatalf("err=%v, want ErrMinimumOrder", err)
	}
}

func TestSellCostAndNetValuationIncludeAllCosts(t *testing.T) {
	cost := CalculateSellCost(12, 1000)
	if cost.StampDuty <= 0 || cost.Commission < MinimumCommission || cost.TransferFee <= 0 || cost.SlippageAmount <= 0 {
		t.Fatalf("sell cost=%+v", cost)
	}
	want := cost.Notional - cost.Commission - cost.StampDuty - cost.TransferFee
	if math.Abs(cost.NetCashFlow-want) > 1e-8 {
		t.Fatalf("net=%.4f want=%.4f", cost.NetCashFlow, want)
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
	base := Quote{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, At: time.Now()}
	for _, quote := range []Quote{
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
