package trading

import (
	"errors"
	"math"
	"testing"
)

func TestNormalizeMainlandCodeAndLotSize(t *testing.T) {
	tests := []struct {
		input     string
		wantCode  string
		wantLot   int64
		wantValid bool
	}{
		{input: "600000", wantCode: "sh600000", wantLot: 100, wantValid: true},
		{input: " SZ300001 ", wantCode: "sz300001", wantLot: 100, wantValid: true},
		{input: "sh688001", wantCode: "sh688001", wantLot: 200, wantValid: true},
		{input: "bj430001", wantValid: false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			code, valid := NormalizeMainlandCode(test.input)
			if code != test.wantCode || valid != test.wantValid {
				t.Fatalf("NormalizeMainlandCode(%q) = %q, %v; want %q, %v", test.input, code, valid, test.wantCode, test.wantValid)
			}
			lot, err := LotSize(test.input)
			if !test.wantValid {
				if err == nil {
					t.Fatal("invalid code must not have a lot size")
				}
				return
			}
			if err != nil || lot != test.wantLot {
				t.Fatalf("LotSize(%q) = %d, %v; want %d", test.input, lot, err, test.wantLot)
			}
		})
	}
}

func TestSizeBuyUsesLargestAffordableLot(t *testing.T) {
	quantity, cost, err := SizeBuy("sh600000", 10, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if quantity != 4900 || quantity%100 != 0 || -cost.NetCashFlow > 50000 {
		t.Fatalf("quantity=%d cost=%+v", quantity, cost)
	}
	if next := CalculateBuyCost(10, quantity+100); -next.NetCashFlow <= 50000 {
		t.Fatalf("next lot cash outflow %.2f still fits", -next.NetCashFlow)
	}

	starQuantity, starCost, err := SizeBuy("sh688001", 50, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if starQuantity != 800 || starQuantity%200 != 0 || -starCost.NetCashFlow > 50000 {
		t.Fatalf("STAR quantity=%d cost=%+v", starQuantity, starCost)
	}
	if _, _, err := SizeBuy("bj430001", 10, 50000); err == nil {
		t.Fatal("Beijing exchange must be rejected")
	}
}

func TestSizeBuyRejectsInvalidCashAndUnaffordableLot(t *testing.T) {
	if _, _, err := SizeBuy("sh600000", 0, 40000); !errors.Is(err, ErrInsufficientCash) {
		t.Fatalf("err=%v, want ErrInsufficientCash", err)
	}
	if _, _, err := SizeBuy("sh600000", 500, 40000); !errors.Is(err, ErrMinimumOrder) {
		t.Fatalf("err=%v, want ErrMinimumOrder", err)
	}
}

func TestBuyAndSellCostsIncludeAllCharges(t *testing.T) {
	buy := CalculateBuyCost(10, 1000)
	if buy.Commission < MinimumCommission || buy.TransferFee <= 0 || buy.StampDuty != 0 || buy.SlippageAmount <= 0 {
		t.Fatalf("buy cost=%+v", buy)
	}
	if want := -(buy.Notional + buy.Commission + buy.TransferFee); math.Abs(buy.NetCashFlow-want) > 1e-8 {
		t.Fatalf("buy net=%.4f want=%.4f", buy.NetCashFlow, want)
	}

	sell := CalculateSellCost(12, 1000)
	if sell.StampDuty <= 0 || sell.Commission < MinimumCommission || sell.TransferFee <= 0 || sell.SlippageAmount <= 0 {
		t.Fatalf("sell cost=%+v", sell)
	}
	if want := sell.Notional - sell.Commission - sell.StampDuty - sell.TransferFee; math.Abs(sell.NetCashFlow-want) > 1e-8 {
		t.Fatalf("sell net=%.4f want=%.4f", sell.NetCashFlow, want)
	}
}
