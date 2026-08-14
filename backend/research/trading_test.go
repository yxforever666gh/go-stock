package research

import (
	"math"
	"testing"
	"time"
)

func TestSizeBuyHonorsCashCapCostsAndMarketLot(t *testing.T) {
	quantity, cost, err := SizeBuy("sh600000", 10, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if quantity%100 != 0 || quantity <= 0 {
		t.Fatalf("quantity=%d", quantity)
	}
	if -cost.NetCashFlow > MaxCashPerTrade+1e-8 {
		t.Fatalf("cash outflow %.2f exceeds cap", -cost.NetCashFlow)
	}
	if cost.Commission < MinimumCommission || cost.TransferFee <= 0 || cost.SlippageAmount <= 0 {
		t.Fatalf("cost=%+v", cost)
	}

	starQuantity, _, err := SizeBuy("sh688001", 50, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if starQuantity%200 != 0 {
		t.Fatalf("STAR quantity=%d, want 200-share unit", starQuantity)
	}
	if _, _, err := SizeBuy("bj430001", 10, 100000); err == nil {
		t.Fatal("Beijing exchange must be rejected")
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

func TestNextLifecycleCheckHandlesLunchAndClose(t *testing.T) {
	loc := shanghaiLocation
	lunch := NextLifecycleCheck(time.Date(2026, 8, 14, 11, 25, 0, 0, loc))
	if got := lunch.Format("2006-01-02 15:04"); got != "2026-08-14 13:00" {
		t.Fatalf("lunch=%s", got)
	}
	close := NextLifecycleCheck(time.Date(2026, 8, 14, 14, 55, 0, 0, loc))
	if got := close.Format("2006-01-02 15:04"); got != "2026-08-17 09:30" {
		t.Fatalf("close=%s", got)
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
