package trading

import (
	"errors"
	"math"
	"regexp"
	"strings"
)

var (
	ErrInsufficientCash     = errors.New("insufficient cash")
	ErrMinimumOrder         = errors.New("insufficient cash for minimum order unit")
	ErrNewPositionsDisabled = errors.New("自动策略已关闭，停止新增买入；已有持仓继续按退出规则管理")
)

const (
	CommissionRate    = 0.0003
	MinimumCommission = 5.0
	StampDutyRate     = 0.0005
	TransferFeeRate   = 0.00001
	SlippageRate      = 0.001
)

var mainlandCodePattern = regexp.MustCompile(`^(sh|sz)?(60|68|00|30)[0-9]{4}$`)

func NormalizeMainlandCode(code string) (string, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	if !mainlandCodePattern.MatchString(code) {
		return "", false
	}
	digits := code
	if strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz") {
		digits = code[2:]
	}
	if strings.HasPrefix(digits, "60") || strings.HasPrefix(digits, "68") {
		return "sh" + digits, true
	}
	return "sz" + digits, true
}

func LotSize(code string) (int64, error) {
	normalized, ok := NormalizeMainlandCode(code)
	if !ok {
		return 0, errors.New("unknown market trading rule")
	}
	if strings.HasPrefix(normalized, "sh68") {
		return 200, nil
	}
	return 100, nil
}

type CostBreakdown struct {
	ExecutionPrice float64
	Notional       float64
	Commission     float64
	StampDuty      float64
	TransferFee    float64
	SlippageAmount float64
	TotalFees      float64
	NetCashFlow    float64
}

func CalculateBuyCost(marketPrice float64, quantity int64) CostBreakdown {
	executionPrice := marketPrice * (1 + SlippageRate)
	notional := executionPrice * float64(quantity)
	commission := math.Max(MinimumCommission, notional*CommissionRate)
	transfer := notional * TransferFeeRate
	return CostBreakdown{
		ExecutionPrice: executionPrice,
		Notional:       notional,
		Commission:     commission,
		TransferFee:    transfer,
		SlippageAmount: (executionPrice - marketPrice) * float64(quantity),
		TotalFees:      commission + transfer,
		NetCashFlow:    -(notional + commission + transfer),
	}
}

func CalculateSellCost(marketPrice float64, quantity int64) CostBreakdown {
	executionPrice := marketPrice * (1 - SlippageRate)
	notional := executionPrice * float64(quantity)
	commission := math.Max(MinimumCommission, notional*CommissionRate)
	stamp := notional * StampDutyRate
	transfer := notional * TransferFeeRate
	return CostBreakdown{
		ExecutionPrice: executionPrice,
		Notional:       notional,
		Commission:     commission,
		StampDuty:      stamp,
		TransferFee:    transfer,
		SlippageAmount: (marketPrice - executionPrice) * float64(quantity),
		TotalFees:      commission + stamp + transfer,
		NetCashFlow:    notional - commission - stamp - transfer,
	}
}

// CalculateAShareBuyCost applies the current Research 2 broker schedule. The
// legacy calculators above remain fixed for frozen Research 1 and migrations.
func CalculateAShareBuyCost(code string, marketPrice float64, quantity int64) (CostBreakdown, error) {
	normalized, ok := NormalizeMainlandCode(code)
	if !ok {
		return CostBreakdown{}, errors.New("unknown market trading rule")
	}
	notional := marketPrice * float64(quantity)
	commission := math.Max(MinimumCommission, notional*0.0002)
	transfer := 0.0
	if strings.HasPrefix(normalized, "sh") {
		transfer = notional * TransferFeeRate
	}
	return CostBreakdown{
		ExecutionPrice: marketPrice, Notional: notional,
		Commission: commission, TransferFee: transfer,
		TotalFees: commission + transfer, NetCashFlow: -(notional + commission + transfer),
	}, nil
}

func CalculateAShareSellCost(code string, marketPrice float64, quantity int64) (CostBreakdown, error) {
	normalized, ok := NormalizeMainlandCode(code)
	if !ok {
		return CostBreakdown{}, errors.New("unknown market trading rule")
	}
	notional := marketPrice * float64(quantity)
	commission := math.Max(MinimumCommission, notional*0.0002)
	stamp := notional * StampDutyRate
	transfer := 0.0
	if strings.HasPrefix(normalized, "sh") {
		transfer = notional * TransferFeeRate
	}
	return CostBreakdown{
		ExecutionPrice: marketPrice, Notional: notional,
		Commission: commission, StampDuty: stamp, TransferFee: transfer,
		TotalFees: commission + stamp + transfer, NetCashFlow: notional - commission - stamp - transfer,
	}, nil
}

func SizeAShareBuy(code string, marketPrice, availableCash float64) (int64, CostBreakdown, error) {
	lot, err := LotSize(code)
	if err != nil {
		return 0, CostBreakdown{}, err
	}
	if availableCash <= 0 || marketPrice <= 0 {
		return 0, CostBreakdown{}, ErrInsufficientCash
	}
	quantity := int64(math.Floor(availableCash/marketPrice/float64(lot))) * lot
	for quantity >= lot {
		cost, err := CalculateAShareBuyCost(code, marketPrice, quantity)
		if err != nil {
			return 0, CostBreakdown{}, err
		}
		if -cost.NetCashFlow <= availableCash+1e-8 {
			return quantity, cost, nil
		}
		quantity -= lot
	}
	return 0, CostBreakdown{}, ErrMinimumOrder
}

func SizeBuy(code string, marketPrice, availableCash float64) (int64, CostBreakdown, error) {
	lot, err := LotSize(code)
	if err != nil {
		return 0, CostBreakdown{}, err
	}
	if availableCash <= 0 || marketPrice <= 0 {
		return 0, CostBreakdown{}, ErrInsufficientCash
	}
	quantity := int64(math.Floor(availableCash/(marketPrice*(1+SlippageRate))/float64(lot))) * lot
	for quantity >= lot {
		cost := CalculateBuyCost(marketPrice, quantity)
		if -cost.NetCashFlow <= availableCash+1e-8 {
			return quantity, cost, nil
		}
		quantity -= lot
	}
	return 0, CostBreakdown{}, ErrMinimumOrder
}
