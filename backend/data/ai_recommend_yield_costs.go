package data

import (
	v150 "go-stock/backend/strategy/v150"
	"strings"
)

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
	UnusedCash float64
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

func isV150CostVersion(summaryVersion string) bool {
	switch strings.ToLower(strings.TrimSpace(summaryVersion)) {
	case "1.5.0", "v1.5.0", "150", "v150":
		return true
	default:
		return false
	}
}

func v150CostMarket(market tradingMarket) v150.Market {
	switch market {
	case tradingMarketSH:
		return v150.MarketSH
	case tradingMarketSZ:
		return v150.MarketSZ
	case tradingMarketBJ:
		return v150.MarketBJ
	default:
		return v150.MarketUnknown
	}
}

// calcBuyTradeCostForVersion preserves every historical cohort's original
// fixed-notional cost basis. Only V1.5.0 uses the 10,000 yuan target position,
// 100-share board lot and 10bp base slippage defined by its frozen config.
func calcBuyTradeCostForVersion(summaryVersion string, price float64, market tradingMarket) tradeCostBreakdown {
	if !isV150CostVersion(summaryVersion) {
		return calcBuyTradeCost(price, market)
	}
	cfg := v150.FixedStrategyV150Config()
	scenario := cfg.SlippageScenarios()[0]
	unitCost := v150.CalculateTradeCost(v150.SideBuy, v150CostMarket(market), price, cfg.RoundLotSize, scenario, cfg)
	size := v150.SizeRoundLot(unitCost.EffectivePrice, cfg.TargetCashPerPosition, cfg)
	if size.Rejected {
		return tradeCostBreakdown{}
	}
	cost := v150.CalculateTradeCost(v150.SideBuy, v150CostMarket(market), price, size.Quantity, scenario, cfg)
	return tradeCostBreakdown{
		Notional:    round2(cost.Notional),
		Commission:  round2(cost.Commission),
		TransferFee: round2(cost.TransferFee),
		StampDuty:   round2(cost.StampDuty),
		Slippage:    round2(cost.SlippageCost),
		NetAmount:   round2(-cost.CashFlow),
	}
}

func calcSellTradeCostForVersion(summaryVersion string, buyPrice, exitPrice float64, market tradingMarket) tradeCostBreakdown {
	if !isV150CostVersion(summaryVersion) {
		return calcSellTradeCost(buyPrice, exitPrice, market)
	}
	cfg := v150.FixedStrategyV150Config()
	scenario := cfg.SlippageScenarios()[0]
	unitCost := v150.CalculateTradeCost(v150.SideBuy, v150CostMarket(market), buyPrice, cfg.RoundLotSize, scenario, cfg)
	size := v150.SizeRoundLot(unitCost.EffectivePrice, cfg.TargetCashPerPosition, cfg)
	if size.Rejected || exitPrice <= 0 {
		return tradeCostBreakdown{}
	}
	cost := v150.CalculateTradeCost(v150.SideSell, v150CostMarket(market), exitPrice, size.Quantity, scenario, cfg)
	return tradeCostBreakdown{
		Notional:    round2(cost.Notional),
		Commission:  round2(cost.Commission),
		TransferFee: round2(cost.TransferFee),
		StampDuty:   round2(cost.StampDuty),
		Slippage:    round2(cost.SlippageCost),
		NetAmount:   round2(cost.CashFlow),
	}
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

func calculateNetYieldForVersion(summaryVersion, stockCode string, buyPrice, exitPrice float64) netYieldResult {
	if !isV150CostVersion(summaryVersion) {
		return calculateNetYield(stockCode, buyPrice, exitPrice)
	}
	if buyPrice <= 0 || exitPrice <= 0 {
		return netYieldResult{YieldText: "--"}
	}
	market := resolveTradingMarket(stockCode)
	buyCost := calcBuyTradeCostForVersion(summaryVersion, buyPrice, market)
	sellNet := calcSellTradeCostForVersion(summaryVersion, buyPrice, exitPrice, market)
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

// V1.5 benchmark legs use the same executable 10bp/100-share convention as
// the strategy cohort. The ETF is commission-bearing but exempt from A-share
// stamp duty and transfer fees; cash that cannot form a board lot remains in
// the virtual account instead of disappearing or becoming fractional shares.
func calcBenchmarkETFBuyTradeForVersion(summaryVersion string, cashBudget, price float64) benchmarkETFTradeCostBreakdown {
	if !isV150CostVersion(summaryVersion) {
		return calcBenchmarkETFBuyTrade(cashBudget, price)
	}
	if cashBudget <= 0 || price <= 0 {
		return benchmarkETFTradeCostBreakdown{}
	}
	cfg := v150.FixedStrategyV150Config()
	slippageRate := cfg.BaseSlippageBPS / 10_000
	effectivePrice := price * (1 + slippageRate)
	budget := cashBudget
	if cfg.TargetCashPerPosition < budget {
		budget = cfg.TargetCashPerPosition
	}
	lots := int(budget / (effectivePrice * float64(cfg.RoundLotSize)))
	for lots > 0 {
		quantity := lots * cfg.RoundLotSize
		notional := effectivePrice * float64(quantity)
		commission := notional * cfg.CommissionRate
		if commission < cfg.MinimumCommission {
			commission = cfg.MinimumCommission
		}
		cashOut := notional + commission
		if cashOut <= cashBudget+1e-9 {
			slippage := (effectivePrice - price) * float64(quantity)
			return benchmarkETFTradeCostBreakdown{
				CashOut:    round2(cashOut),
				Notional:   round2(notional),
				Commission: round2(commission),
				Slippage:   round2(slippage),
				Shares:     float64(quantity),
				UnusedCash: round2(cashBudget - cashOut),
				NetAmount:  round2(notional),
				Valid:      true,
			}
		}
		lots--
	}
	return benchmarkETFTradeCostBreakdown{}
}

func calcBenchmarkETFSellTradeForVersion(summaryVersion string, shares, price float64) benchmarkETFTradeCostBreakdown {
	if !isV150CostVersion(summaryVersion) {
		return calcBenchmarkETFSellTrade(shares, price)
	}
	if shares <= 0 || price <= 0 || shares != float64(int(shares)) || int(shares)%v150.FixedStrategyV150Config().RoundLotSize != 0 {
		return benchmarkETFTradeCostBreakdown{}
	}
	cfg := v150.FixedStrategyV150Config()
	effectivePrice := price * (1 - cfg.BaseSlippageBPS/10_000)
	notional := effectivePrice * shares
	commission := notional * cfg.CommissionRate
	if commission < cfg.MinimumCommission {
		commission = cfg.MinimumCommission
	}
	netAmount := notional - commission
	if netAmount <= 0 {
		return benchmarkETFTradeCostBreakdown{}
	}
	return benchmarkETFTradeCostBreakdown{
		Notional:   round2(notional),
		Commission: round2(commission),
		Slippage:   round2((price - effectivePrice) * shares),
		Shares:     shares,
		NetAmount:  round2(netAmount),
		Valid:      true,
	}
}
