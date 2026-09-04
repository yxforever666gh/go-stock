package research

import (
	"errors"

	"go-stock/internal/trading"
)

var (
	ErrDuplicateStockExposure = errors.New("stock already has an open or pending exposure")
	ErrExecutionWindowClosed  = errors.New("immediate execution window is closed")
)

func sizeResearchBuy(code string, marketPrice, availableCash float64) (int64, trading.CostBreakdown, error) {
	return trading.SizeBuy(code, marketPrice, min(availableCash, TargetCashPerTrade))
}
