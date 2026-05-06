package data

import (
	"math"
	"testing"
)

func TestResolveTradingMarket(t *testing.T) {
	cases := map[string]tradingMarket{
		"600519.SH": tradingMarketSH,
		"000001.SZ": tradingMarketSZ,
		"830799.BJ": tradingMarketBJ,
		"sh600519":  tradingMarketSH,
		"sz000001":  tradingMarketSZ,
		"830799":    tradingMarketSZ,
	}
	for input, want := range cases {
		got := resolveTradingMarket(input)
		if got != want {
			t.Fatalf("resolveTradingMarket(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestCalculateNetYield_UsesMinimumCommissionAndSellEstimate(t *testing.T) {
	result := calculateNetYield("000001.SZ", 10, 11)
	if !result.Valid {
		t.Fatal("expected valid result")
	}
	if math.Abs(result.BuyCost-3006.5) > 0.001 {
		t.Fatalf("expected BuyCost=3006.5, got %.4f", result.BuyCost)
	}
	if math.Abs(result.SellNet-3291.7) > 0.001 {
		t.Fatalf("expected SellNet=3291.7, got %.4f", result.SellNet)
	}
	if math.Abs(result.YieldRate-9.49) > 0.001 {
		t.Fatalf("expected YieldRate=9.49, got %.4f", result.YieldRate)
	}
	if result.YieldText != "+9.49%" {
		t.Fatalf("expected YieldText=+9.49%%, got %s", result.YieldText)
	}
}

func TestCalculateNetYield_MarketSpecificFees(t *testing.T) {
	sh := calculateNetYield("600519.SH", 100, 115)
	sz := calculateNetYield("000001.SZ", 100, 115)
	bj := calculateNetYield("830799.BJ", 100, 115)

	if !sh.Valid || !sz.Valid || !bj.Valid {
		t.Fatal("expected all markets to produce valid yields")
	}
	if math.Abs(sh.YieldRate-14.47) > 0.001 {
		t.Fatalf("expected SH yield 14.47, got %.4f", sh.YieldRate)
	}
	if math.Abs(sz.YieldRate-14.47) > 0.001 {
		t.Fatalf("expected SZ yield 14.47, got %.4f", sz.YieldRate)
	}
	if math.Abs(bj.YieldRate-14.53) > 0.001 {
		t.Fatalf("expected BJ yield 14.53, got %.4f", bj.YieldRate)
	}
	if !(bj.SellNet > sh.SellNet) {
		t.Fatalf("expected BJ sell net > SH sell net, got bj=%.4f sh=%.4f", bj.SellNet, sh.SellNet)
	}
}

func TestCalcBenchmarkETFBuyTrade_UsesCashBudgetAndMinimumCommission(t *testing.T) {
	result := calcBenchmarkETFBuyTrade(3006.5, 5)
	if !result.Valid {
		t.Fatal("expected valid ETF benchmark buy trade")
	}
	if math.Abs(result.CashOut-3006.5) > 0.001 {
		t.Fatalf("expected CashOut=3006.5, got %.4f", result.CashOut)
	}
	if math.Abs(result.Notional-3000) > 0.001 {
		t.Fatalf("expected Notional=3000, got %.4f", result.Notional)
	}
	if math.Abs(result.Commission-5) > 0.001 {
		t.Fatalf("expected Commission=5, got %.4f", result.Commission)
	}
	if math.Abs(result.Slippage-1.5) > 0.001 {
		t.Fatalf("expected Slippage=1.5, got %.4f", result.Slippage)
	}
	if math.Abs(result.Shares-600) > 0.001 {
		t.Fatalf("expected Shares=600, got %.4f", result.Shares)
	}
}

func TestCalcBenchmarkETFSellTrade_DeductsCommissionAndSlippageOnly(t *testing.T) {
	result := calcBenchmarkETFSellTrade(600, 5.5)
	if !result.Valid {
		t.Fatal("expected valid ETF benchmark sell trade")
	}
	if math.Abs(result.Notional-3300) > 0.001 {
		t.Fatalf("expected Notional=3300, got %.4f", result.Notional)
	}
	if math.Abs(result.Commission-5) > 0.001 {
		t.Fatalf("expected Commission=5, got %.4f", result.Commission)
	}
	if math.Abs(result.Slippage-1.65) > 0.001 {
		t.Fatalf("expected Slippage=1.65, got %.4f", result.Slippage)
	}
	if math.Abs(result.NetAmount-3293.35) > 0.001 {
		t.Fatalf("expected NetAmount=3293.35, got %.4f", result.NetAmount)
	}
}
