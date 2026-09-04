package research2

import "math"

const (
	SelectionLimitDistancePct = 1.5
	ExecutionLimitDistancePct = 1.0
)

// MainBoardLimitPrice returns the ordinary 10% mainland main-board upper
// limit rounded to the exchange's RMB 0.01 price tick.
func MainBoardLimitPrice(previousClose float64) float64 {
	if previousClose <= 0 || math.IsNaN(previousClose) || math.IsInf(previousClose, 0) {
		return 0
	}
	previousCloseCents := int64(math.Floor(previousClose*100 + 0.5))
	limitCents := (previousCloseCents*110 + 50) / 100
	return float64(limitCents) / 100
}

// LimitDistancePct measures the remaining distance as a percentage of the
// limit price. A zero distance means the stock is already at its upper limit.
func LimitDistancePct(price, limitPrice float64) (float64, bool) {
	if price <= 0 || limitPrice <= 0 || math.IsNaN(price) || math.IsNaN(limitPrice) || math.IsInf(price, 0) || math.IsInf(limitPrice, 0) {
		return 0, false
	}
	return (limitPrice - price) / limitPrice * 100, true
}

func IsInsideLimitBuffer(price, previousClose, minimumDistancePct float64) (limitPrice, distancePct float64, blocked bool) {
	limitPrice = MainBoardLimitPrice(previousClose)
	distancePct, ok := LimitDistancePct(price, limitPrice)
	if !ok {
		return limitPrice, 0, false
	}
	return limitPrice, distancePct, distancePct+1e-9 < minimumDistancePct
}
