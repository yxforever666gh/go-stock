package v150

import (
	"math"
	"strings"
)

const RejectMinimumLot = "untradeable_min_lot"

func SizeRoundLot(price, availableCash float64, cfg StrategyV150Config) PositionSize {
	if price <= 0 || availableCash <= 0 || cfg.RoundLotSize <= 0 {
		return PositionSize{Rejected: true, Reason: RejectMinimumLot}
	}
	budget := math.Min(cfg.TargetCashPerPosition, availableCash)
	lots := int(math.Floor(budget / (price * float64(cfg.RoundLotSize))))
	quantity := lots * cfg.RoundLotSize
	if quantity <= 0 {
		return PositionSize{Rejected: true, Reason: RejectMinimumLot}
	}
	return PositionSize{Quantity: quantity, Notional: float64(quantity) * price}
}

func ResolveMarket(symbol string) Market {
	upper := strings.ToUpper(strings.TrimSpace(symbol))
	switch {
	case strings.HasSuffix(upper, ".SH"):
		return MarketSH
	case strings.HasSuffix(upper, ".SZ"):
		return MarketSZ
	case strings.HasSuffix(upper, ".BJ"):
		return MarketBJ
	}

	// Frozen snapshots normally store Tushare-style codes, but older caches
	// and imported execution events may contain the six-digit code only.  Do
	// not silently classify those rows as fee-free: resolve the A-share venue
	// from its exchange prefix, checking Beijing's 92 prefix before Shanghai's
	// broader 9 prefix.
	if len(upper) == 6 {
		for _, char := range upper {
			if char < '0' || char > '9' {
				return MarketUnknown
			}
		}
		switch {
		case strings.HasPrefix(upper, "92"), strings.HasPrefix(upper, "4"), strings.HasPrefix(upper, "8"):
			return MarketBJ
		case strings.HasPrefix(upper, "5"), strings.HasPrefix(upper, "6"), strings.HasPrefix(upper, "9"):
			return MarketSH
		case strings.HasPrefix(upper, "0"), strings.HasPrefix(upper, "1"), strings.HasPrefix(upper, "2"), strings.HasPrefix(upper, "3"):
			return MarketSZ
		}
	}
	return MarketUnknown
}

func CalculateTradeCost(side Side, market Market, rawPrice float64, quantity int, scenario SlippageScenario, cfg StrategyV150Config) TradeCost {
	if rawPrice <= 0 || quantity <= 0 {
		return TradeCost{Side: side, RawPrice: rawPrice, Quantity: quantity}
	}
	slippageRatio := scenario.BPS / 10_000
	effectivePrice := rawPrice
	if side == SideBuy {
		effectivePrice *= 1 + slippageRatio
	} else {
		effectivePrice *= 1 - slippageRatio
	}
	notional := effectivePrice * float64(quantity)
	commission := math.Max(cfg.MinimumCommission, notional*cfg.CommissionRate)
	transferFee := 0.0
	// ChinaClear charges the A-share transfer fee bilaterally for Shanghai,
	// Shenzhen and Beijing. Unknown markets remain fee-ineligible so callers
	// cannot silently treat an unclassified symbol as a known venue.
	if market == MarketSH || market == MarketSZ || market == MarketBJ {
		transferFee = notional * cfg.TransferFeeRate
	}
	stampDuty := 0.0
	if side == SideSell && (market == MarketSH || market == MarketSZ || market == MarketBJ) {
		stampDuty = notional * cfg.SellStampDutyRate
	}
	slippageCost := math.Abs(effectivePrice-rawPrice) * float64(quantity)
	cashFlow := notional - commission - transferFee - stampDuty
	if side == SideBuy {
		cashFlow = -(notional + commission + transferFee)
	}
	return TradeCost{
		Side:           side,
		RawPrice:       rawPrice,
		EffectivePrice: effectivePrice,
		Quantity:       quantity,
		Notional:       notional,
		Commission:     commission,
		StampDuty:      stampDuty,
		TransferFee:    transferFee,
		SlippageCost:   slippageCost,
		CashFlow:       cashFlow,
	}
}
