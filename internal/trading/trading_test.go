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

func TestAShareBrokerCostsWithoutSlippage(t *testing.T) {
	for _, test := range []struct {
		code         string
		transferBuy  float64
		transferSell float64
	}{
		{code: "sh600000", transferBuy: 0.1, transferSell: 0.12},
		{code: "sz000001", transferBuy: 0, transferSell: 0},
	} {
		buy, err := CalculateAShareBuyCost(test.code, 10, 1000)
		if err != nil || buy.ExecutionPrice != 10 || buy.SlippageAmount != 0 || buy.Commission != 5 || math.Abs(buy.TransferFee-test.transferBuy) > 1e-9 || buy.StampDuty != 0 || math.Abs(buy.NetCashFlow+10005+test.transferBuy) > 1e-9 {
			t.Fatalf("%s buy=%+v err=%v", test.code, buy, err)
		}
		sell, err := CalculateAShareSellCost(test.code, 12, 1000)
		if err != nil || sell.ExecutionPrice != 12 || sell.SlippageAmount != 0 || sell.Commission != 5 || sell.StampDuty != 6 || math.Abs(sell.TransferFee-test.transferSell) > 1e-9 || math.Abs(sell.NetCashFlow-(12000-5-6-test.transferSell)) > 1e-9 {
			t.Fatalf("%s sell=%+v err=%v", test.code, sell, err)
		}
	}
	large, err := CalculateAShareBuyCost("sh600000", 10, 10000)
	if err != nil || large.Commission != 20 || large.TransferFee != 1 {
		t.Fatalf("large trade=%+v err=%v", large, err)
	}
	if _, err := CalculateAShareSellCost("bj430001", 10, 100); err == nil {
		t.Fatal("unsupported market accepted")
	}
}

func TestSizeAShareBuyIncludesExactBrokerCosts(t *testing.T) {
	quantity, cost, err := SizeAShareBuy("sh600000", 10, 1005.01)
	if err != nil || quantity != 100 || math.Abs(cost.NetCashFlow+1005.01) > 1e-9 {
		t.Fatalf("Shanghai boundary quantity=%d cost=%+v err=%v", quantity, cost, err)
	}
	if _, _, err := SizeAShareBuy("sh600000", 10, 1005); !errors.Is(err, ErrMinimumOrder) {
		t.Fatalf("Shanghai insufficient cash err=%v", err)
	}
	quantity, cost, err = SizeAShareBuy("sz000001", 10, 1005)
	if err != nil || quantity != 100 || cost.NetCashFlow != -1005 {
		t.Fatalf("Shenzhen boundary quantity=%d cost=%+v err=%v", quantity, cost, err)
	}
}
