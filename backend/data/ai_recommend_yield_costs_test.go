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

func TestCalculateNetYieldForVersion_V150UsesRoundLotsAndActualNotional(t *testing.T) {
	result := calculateNetYieldForVersion("1.5.0", "000001.SZ", 10, 11)
	if !result.Valid {
		t.Fatal("expected valid V1.5.0 result")
	}
	if math.Abs(result.BuyCost-9014.09) > 0.001 {
		t.Fatalf("expected V1.5.0 BuyCost=9014.09, got %.4f", result.BuyCost)
	}
	if math.Abs(result.SellNet-9880.06) > 0.001 {
		t.Fatalf("expected V1.5.0 SellNet=9880.06, got %.4f", result.SellNet)
	}
	if math.Abs(result.YieldRate-9.61) > 0.001 {
		t.Fatalf("expected V1.5.0 YieldRate=9.61, got %.4f", result.YieldRate)
	}

	boardLot := calcBuyTradeCostForVersion("1.5.0", 23, tradingMarketSZ)
	if math.Abs(boardLot.Notional-9209.20) > 0.001 {
		t.Fatalf("expected 400-share V1.5.0 effective notional=9209.20, got %.4f", boardLot.Notional)
	}
}

func TestCalculateNetYieldForVersion_V150RejectsMinimumLot(t *testing.T) {
	result := calculateNetYieldForVersion("1.5.0", "600519.SH", 101, 110)
	if result.Valid || result.YieldText != "--" {
		t.Fatalf("expected price above one-lot budget to be untradeable, got %+v", result)
	}
}

func TestCalculateNetYieldForVersion_PreservesLegacyFrozenCost(t *testing.T) {
	legacy := calculateNetYield("000001.SZ", 10, 11)
	versioned := calculateNetYieldForVersion("1.4.2", "000001.SZ", 10, 11)
	if legacy != versioned {
		t.Fatalf("V1.4.2 cost basis changed: legacy=%+v versioned=%+v", legacy, versioned)
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

func TestV150BenchmarkETFUsesTenBPSBoardLotsAndPreservesUnusedCash(t *testing.T) {
	buy := calcBenchmarkETFBuyTradeForVersion("1.5.0", 10_000, 4)
	if !buy.Valid {
		t.Fatal("V1.5 benchmark buy should be executable")
	}
	if buy.Shares != 2400 || int(buy.Shares)%100 != 0 {
		t.Fatalf("shares=%.0f, want 2400-share board lots", buy.Shares)
	}
	if buy.Slippage != 9.60 || buy.Commission != 5 || buy.CashOut != 9614.60 || buy.UnusedCash != 385.40 {
		t.Fatalf("unexpected V1.5 benchmark buy: %+v", buy)
	}
	if got := round2(buy.CashOut + buy.UnusedCash); got != 10_000 {
		t.Fatalf("cash conservation=%.2f, want 10000", got)
	}

	sell := calcBenchmarkETFSellTradeForVersion("1.5.0", buy.Shares, 4.4)
	if !sell.Valid || sell.Slippage != 10.56 || sell.Commission != 5 || sell.NetAmount != 10544.44 {
		t.Fatalf("unexpected V1.5 benchmark sell: %+v", sell)
	}
	endValue := round2(sell.NetAmount + buy.UnusedCash)
	if endValue != 10929.84 {
		t.Fatalf("benchmark account end value=%.2f, want 10929.84", endValue)
	}
}

func TestV150BenchmarkETFRejectsFractionalOrOddLotSell(t *testing.T) {
	for _, shares := range []float64{0, 1.5, 99, 250} {
		if got := calcBenchmarkETFSellTradeForVersion("1.5.0", shares, 4); got.Valid {
			t.Fatalf("shares %.2f unexpectedly executable: %+v", shares, got)
		}
	}
}
