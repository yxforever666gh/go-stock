package research2

import (
	"math"

	"go-stock/internal/trading"
)

// allocationBaseCashPending marks chains created by the current runtime before
// their first winning report claims the actual slot. NULL remains the durable
// marker for chains created before this allocation policy existed.
const allocationBaseCashPending = -1.0

func pendingAllocationBaseCash() *float64 {
	value := allocationBaseCashPending
	return &value
}

func allocationBaseCapturePending(value *float64) bool {
	return value != nil && *value == allocationBaseCashPending
}

func fixedAllocationBase(value *float64) (float64, bool) {
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, false
	}
	return *value, true
}

// sizeResearch2Buy preserves the legacy remaining-cash allocator for NULL
// historical chains. New chains use their persisted starting cash so retries
// and later candidates cannot alter the fifth-of-account limit.
func sizeResearch2Buy(code string, marketPrice, availableCash float64, remainingSlots int, allocationBaseCash *float64) (int64, trading.CostBreakdown, error) {
	if allocationBase, ok := fixedAllocationBase(allocationBaseCash); ok {
		return sizeFixedAllocationBuy(code, marketPrice, availableCash, allocationBase)
	}
	return sizeLegacyResearch2Buy(code, marketPrice, availableCash, remainingSlots)
}

func sizeLegacyResearch2Buy(code string, marketPrice, availableCash float64, remainingSlots int) (int64, trading.CostBreakdown, error) {
	if remainingSlots <= 0 {
		return 0, trading.CostBreakdown{}, trading.ErrInsufficientCash
	}
	lot, err := trading.LotSize(code)
	if err != nil {
		return 0, trading.CostBreakdown{}, err
	}
	lotCost := -trading.CalculateBuyCost(marketPrice, lot).NetCashFlow
	cashCap := math.Min(availableCash, math.Max(availableCash/float64(remainingSlots), lotCost))
	return trading.SizeBuy(code, marketPrice, cashCap)
}

func sizeFixedAllocationBuy(code string, marketPrice, availableCash, allocationBaseCash float64) (int64, trading.CostBreakdown, error) {
	lot, err := trading.LotSize(code)
	if err != nil {
		return 0, trading.CostBreakdown{}, err
	}
	lotCost := trading.CalculateBuyCost(marketPrice, lot)
	lotCash := -lotCost.NetCashFlow
	allocationLimit := allocationBaseCash / float64(DailyTargetSlots)

	// The one-lot exception intentionally includes equality. It still never
	// borrows from the independently scoped account.
	if lotCash >= allocationLimit {
		if lotCash > availableCash+1e-8 {
			return 0, trading.CostBreakdown{}, trading.ErrMinimumOrder
		}
		return lot, lotCost, nil
	}

	quantity, cost, err := trading.SizeBuy(code, marketPrice, availableCash)
	if err != nil {
		return 0, trading.CostBreakdown{}, err
	}
	// SizeBuy deliberately permits equality with its cash argument. Here the
	// fixed allocation ceiling is strict for every non-exceptional order.
	for quantity >= lot && -cost.NetCashFlow >= allocationLimit {
		quantity -= lot
		if quantity < lot {
			return 0, trading.CostBreakdown{}, trading.ErrMinimumOrder
		}
		cost = trading.CalculateBuyCost(marketPrice, quantity)
	}
	return quantity, cost, nil
}

// SizeFixedAllocationBuy exposes the fixed one-fifth sizing rule to the
// durable capital-rebase migration. It deliberately shares the production
// implementation so a historical replay cannot drift from live execution.
func SizeFixedAllocationBuy(code string, marketPrice, availableCash, allocationBaseCash float64) (int64, trading.CostBreakdown, error) {
	return sizeFixedAllocationBuy(code, marketPrice, availableCash, allocationBaseCash)
}
