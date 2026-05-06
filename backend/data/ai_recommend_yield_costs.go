package data

import "strings"

type tradingMarket string

const (
	tradingMarketUnknown tradingMarket = ""
	tradingMarketSH      tradingMarket = "SH"
	tradingMarketSZ      tradingMarket = "SZ"
	tradingMarketBJ      tradingMarket = "BJ"
)

const (
	defaultCommissionRate = 0.0003
	defaultCommissionMin  = 5.0
	defaultStampDutyRate  = 0.0005
	defaultTransferRate   = 0.00001
	defaultSlippageRate   = 0.0005
)

type tradeCostBreakdown struct {
	Notional    float64
	Commission  float64
	TransferFee float64
	StampDuty   float64
	Slippage    float64
	NetAmount   float64
}

type netYieldResult struct {
	BuyCost   float64
	SellNet   float64
	YieldRate float64
	YieldText string
	Valid     bool
}

type benchmarkETFTradeCostBreakdown struct {
	CashOut    float64
	Notional   float64
	Commission float64
	Slippage   float64
	Shares     float64
	NetAmount  float64
	Valid      bool
}

func resolveTradingMarket(stockCode string) tradingMarket {
	code := strings.ToUpper(strings.TrimSpace(normalizeRecommendStockCode(stockCode)))
	switch {
	case strings.HasSuffix(code, ".SH"):
		return tradingMarketSH
	case strings.HasSuffix(code, ".SZ"):
		return tradingMarketSZ
	case strings.HasSuffix(code, ".BJ"):
		return tradingMarketBJ
	default:
		return tradingMarketUnknown
	}
}

func tradeTransferRate(market tradingMarket) float64 {
	switch market {
	case tradingMarketSH, tradingMarketBJ:
		return defaultTransferRate
	default:
		return 0
	}
}

func tradeStampDutyRate(market tradingMarket) float64 {
	switch market {
	case tradingMarketSH, tradingMarketSZ:
		return defaultStampDutyRate
	default:
		return 0
	}
}

func estimatePositionNotional(price float64) float64 {
	if price <= 0 {
		return 0
	}
	return aiRecommendEqualPositionCapital
}

func calcBuyTradeCost(price float64, market tradingMarket) tradeCostBreakdown {
	notional := estimatePositionNotional(price)
	if price <= 0 || notional <= 0 {
		return tradeCostBreakdown{}
	}
	commission := notional * defaultCommissionRate
	if commission < defaultCommissionMin {
		commission = defaultCommissionMin
	}
	transferFee := notional * tradeTransferRate(market)
	slippage := notional * defaultSlippageRate
	netAmount := notional + commission + transferFee + slippage
	return tradeCostBreakdown{
		Notional:    round2(notional),
		Commission:  round2(commission),
		TransferFee: round2(transferFee),
		Slippage:    round2(slippage),
		NetAmount:   round2(netAmount),
	}
}

func calcSellTradeCost(buyPrice, exitPrice float64, market tradingMarket) tradeCostBreakdown {
	notional := estimatePositionNotional(buyPrice)
	if buyPrice <= 0 || exitPrice <= 0 || notional <= 0 {
		return tradeCostBreakdown{}
	}
	gross := notional * exitPrice / buyPrice
	commission := gross * defaultCommissionRate
	if commission < defaultCommissionMin {
		commission = defaultCommissionMin
	}
	transferFee := gross * tradeTransferRate(market)
	stampDuty := gross * tradeStampDutyRate(market)
	slippage := gross * defaultSlippageRate
	netAmount := gross - commission - transferFee - stampDuty - slippage
	return tradeCostBreakdown{
		Notional:    round2(gross),
		Commission:  round2(commission),
		TransferFee: round2(transferFee),
		StampDuty:   round2(stampDuty),
		Slippage:    round2(slippage),
		NetAmount:   round2(netAmount),
	}
}

func calculateNetYield(stockCode string, buyPrice float64, exitPrice float64) netYieldResult {
	if buyPrice <= 0 || exitPrice <= 0 {
		return netYieldResult{
			YieldText: "--",
		}
	}
	market := resolveTradingMarket(stockCode)
	buyCost := calcBuyTradeCost(buyPrice, market)
	sellNet := calcSellTradeCost(buyPrice, exitPrice, market)
	if buyCost.NetAmount <= 0 || sellNet.NetAmount <= 0 {
		return netYieldResult{
			BuyCost:   buyCost.NetAmount,
			SellNet:   sellNet.NetAmount,
			YieldText: "--",
		}
	}
	yieldRate := round2((sellNet.NetAmount - buyCost.NetAmount) / buyCost.NetAmount * 100)
	return netYieldResult{
		BuyCost:   buyCost.NetAmount,
		SellNet:   sellNet.NetAmount,
		YieldRate: yieldRate,
		YieldText: formatSignedPercent(yieldRate),
		Valid:     true,
	}
}

func calcBenchmarkETFBuyTrade(cashBudget, price float64) benchmarkETFTradeCostBreakdown {
	if cashBudget <= defaultCommissionMin || price <= 0 {
		return benchmarkETFTradeCostBreakdown{}
	}

	notional := (cashBudget - defaultCommissionMin) / (1 + defaultSlippageRate)
	commission := defaultCommissionMin
	if notional <= 0 {
		return benchmarkETFTradeCostBreakdown{}
	}
	if rateCommission := notional * defaultCommissionRate; rateCommission > defaultCommissionMin {
		notional = cashBudget / (1 + defaultCommissionRate + defaultSlippageRate)
		commission = notional * defaultCommissionRate
	}
	slippage := notional * defaultSlippageRate
	cashOut := notional + commission + slippage
	shares := notional / price
	if cashOut <= 0 || shares <= 0 {
		return benchmarkETFTradeCostBreakdown{}
	}
	return benchmarkETFTradeCostBreakdown{
		CashOut:    round2(cashOut),
		Notional:   round2(notional),
		Commission: round2(commission),
		Slippage:   round2(slippage),
		Shares:     shares,
		NetAmount:  round2(notional),
		Valid:      true,
	}
}

func calcBenchmarkETFSellTrade(shares, price float64) benchmarkETFTradeCostBreakdown {
	if shares <= 0 || price <= 0 {
		return benchmarkETFTradeCostBreakdown{}
	}
	gross := shares * price
	commission := gross * defaultCommissionRate
	if commission < defaultCommissionMin {
		commission = defaultCommissionMin
	}
	slippage := gross * defaultSlippageRate
	netAmount := gross - commission - slippage
	if netAmount <= 0 {
		return benchmarkETFTradeCostBreakdown{}
	}
	return benchmarkETFTradeCostBreakdown{
		Notional:   round2(gross),
		Commission: round2(commission),
		Slippage:   round2(slippage),
		NetAmount:  round2(netAmount),
		Valid:      true,
	}
}
