package research2

import (
	"math"
	"strings"

	"go-stock/internal/trading"
)

const (
	AllocationPolicyLegacyRecorded     = "legacy_recorded"
	AllocationPolicyRemainingCashSlots = "remaining_cash_by_open_slots"
)

func fixedAllocationBase(value *float64) (float64, bool) {
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, false
	}
	return *value, true
}

// sizeResearch2Buy keeps the recorded fixed-base behavior only for chains that
// have not yet been replayed. Current and replayed chains divide the remaining
// cash by their remaining successful-buy slots.
func sizeResearch2Buy(code string, marketPrice, availableCash float64, remainingSlots int, allocationPolicy string, allocationBaseCash *float64) (int64, trading.CostBreakdown, error) {
	if strings.TrimSpace(allocationPolicy) == AllocationPolicyRemainingCashSlots {
		return sizeRemainingCashBuy(code, marketPrice, availableCash, remainingSlots)
	}
	if allocationBase, ok := fixedAllocationBase(allocationBaseCash); ok {
		return sizeFixedAllocationBuy(code, marketPrice, availableCash, allocationBase, false)
	}
	return sizeRemainingCashBuy(code, marketPrice, availableCash, remainingSlots)
}

func sizeRemainingCashBuy(code string, marketPrice, availableCash float64, remainingSlots int) (int64, trading.CostBreakdown, error) {
	if remainingSlots <= 0 {
		return 0, trading.CostBreakdown{}, trading.ErrInsufficientCash
	}
	lot, err := trading.LotSize(code)
	if err != nil {
		return 0, trading.CostBreakdown{}, err
	}
	lotCostBreakdown, err := trading.CalculateAShareBuyCost(code, marketPrice, lot)
	if err != nil {
		return 0, trading.CostBreakdown{}, err
	}
	lotCost := -lotCostBreakdown.NetCashFlow
	cashCap := math.Min(availableCash, math.Max(availableCash/float64(remainingSlots), lotCost))
	return trading.SizeAShareBuy(code, marketPrice, cashCap)
}

// SizeRemainingCashBuy exposes the production remaining-cash allocator to the
// deterministic full-history replay.
func SizeRemainingCashBuy(code string, marketPrice, availableCash float64, remainingSlots int) (int64, trading.CostBreakdown, error) {
	return sizeRemainingCashBuy(code, marketPrice, availableCash, remainingSlots)
}

func sizeFixedAllocationBuy(code string, marketPrice, availableCash, allocationBaseCash float64, legacy bool) (int64, trading.CostBreakdown, error) {
	lot, err := trading.LotSize(code)
	if err != nil {
		return 0, trading.CostBreakdown{}, err
	}
	buyCost := func(quantity int64) (trading.CostBreakdown, error) {
		if legacy {
			return trading.CalculateBuyCost(marketPrice, quantity), nil
		}
		return trading.CalculateAShareBuyCost(code, marketPrice, quantity)
	}
	lotCost, err := buyCost(lot)
	if err != nil {
		return 0, trading.CostBreakdown{}, err
	}
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

	var quantity int64
	var cost trading.CostBreakdown
	if legacy {
		quantity, cost, err = trading.SizeBuy(code, marketPrice, availableCash)
	} else {
		quantity, cost, err = trading.SizeAShareBuy(code, marketPrice, availableCash)
	}
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
		cost, err = buyCost(quantity)
		if err != nil {
			return 0, trading.CostBreakdown{}, err
		}
	}
	return quantity, cost, nil
}

// SizeFixedAllocationBuy preserves the original fixed one-fifth rule and cost
// schedule for the already released capital-rebase migration. Live execution
// and the full-history replay use the current A-share schedule instead.
func SizeFixedAllocationBuy(code string, marketPrice, availableCash, allocationBaseCash float64) (int64, trading.CostBreakdown, error) {
	return sizeFixedAllocationBuy(code, marketPrice, availableCash, allocationBaseCash, true)
}
