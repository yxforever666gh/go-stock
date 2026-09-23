package research2

import "go-stock/internal/trading"

func testAShareBuyCost(code string, price float64, quantity int64) trading.CostBreakdown {
	cost, err := trading.CalculateAShareBuyCost(code, price, quantity)
	if err != nil {
		panic(err)
	}
	return cost
}

func testAShareSellCost(code string, price float64, quantity int64) trading.CostBreakdown {
	cost, err := trading.CalculateAShareSellCost(code, price, quantity)
	if err != nil {
		panic(err)
	}
	return cost
}
