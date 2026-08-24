package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	HistoricalPostSellBuyCorrectionDecision = "1.7.6 历史卖出后买入纠正"
	historicalPostSellBuyRecommendationID   = "d78113c2-fbd9-42c5-ad0e-f203b90ffc61"
	historicalPostSellBuyFailureEventID     = "8f2523d2-3e76-4650-9c99-e677ec3b8665"
	historicalPostSellBuySellRecommendation = "053e7c47-a538-4d6d-9dbd-61e9897d8285"
)

var (
	historicalPostSellBuySignalAt = time.Date(2026, 8, 21, 14, 35, 48, 479560300, shanghaiLocation)
	historicalPostSellBuyQuoteAt  = time.Date(2026, 8, 21, 14, 35, 42, 0, shanghaiLocation)
	historicalPostSellBuyNextSell = time.Date(2026, 8, 24, 9, 55, 0, 0, shanghaiLocation)
)

// HistoricalPostSellBuyCorrectionRequest supplies the already-collected close
// mark. Market I/O must happen before calling the correction, never while its
// account transaction is holding the writer lock.
type HistoricalPostSellBuyCorrectionRequest struct {
	MarkQuote Quote     `json:"markQuote"`
	AppliedAt time.Time `json:"appliedAt"`
}

type HistoricalPostSellBuyCorrectionReceipt struct {
	Status           string    `json:"status"`
	RecommendationID string    `json:"recommendationId"`
	TradedAt         time.Time `json:"tradedAt"`
	QuoteAt          time.Time `json:"quoteAt"`
	MarkAt           time.Time `json:"markAt"`
	MarketPrice      float64   `json:"marketPrice"`
	ExecutionPrice   float64   `json:"executionPrice"`
	Quantity         int64     `json:"quantity"`
	TotalFees        float64   `json:"totalFees"`
	NetCashFlow      float64   `json:"netCashFlow"`
	CashAfter        float64   `json:"cashAfter"`
}

// ApplyHistoricalPostSellBuyCorrection books the single approved 1.7.6
// correction. Its narrow, deterministic guardrails make it safe to expose as
// an operational repair command without turning it into a general backfill.
func (s *Service) ApplyHistoricalPostSellBuyCorrection(ctx context.Context, request HistoricalPostSellBuyCorrectionRequest) (HistoricalPostSellBuyCorrectionReceipt, error) {
	s.serial.Lock()
	defer s.serial.Unlock()
	if request.AppliedAt.IsZero() {
		return HistoricalPostSellBuyCorrectionReceipt{}, errors.New("applied timestamp is required")
	}
	if err := validateHistoricalPostSellBuyMark(request.MarkQuote); err != nil {
		return HistoricalPostSellBuyCorrectionReceipt{}, err
	}
	var receipt HistoricalPostSellBuyCorrectionReceipt
	err := transactionWithWriteRetry(ctx, s.repository.db, func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		var recommendation Recommendation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).First(&recommendation).Error; err != nil {
			return err
		}
		correctionEventID := historicalPostSellBuyEventID()
		var correctionEvents, buys, positions int64
		if err := tx.Model(&DecisionEvent{}).Where("recommendation_id = ? AND event_id = ? AND decision_type = ?", recommendation.RecommendationID, correctionEventID, HistoricalPostSellBuyCorrectionDecision).Count(&correctionEvents).Error; err != nil {
			return err
		}
		if err := tx.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", recommendation.RecommendationID, "buy").Count(&buys).Error; err != nil {
			return err
		}
		if err := tx.Model(&Position{}).Where("recommendation_id = ?", recommendation.RecommendationID).Count(&positions).Error; err != nil {
			return err
		}
		if correctionEvents == 1 && buys == 1 && positions == 1 && recommendation.Status == "active" {
			var err error
			receipt, err = populateHistoricalPostSellBuyReceipt(tx, "already_applied")
			return err
		}
		if correctionEvents != 0 || buys != 0 || positions != 0 || recommendation.Status != "missed_cash" {
			return errors.New("historical post-sell buy correction is partially applied or recommendation state is unexpected")
		}
		if recommendation.StockCode != "sz300308" || !sameStockName(recommendation.StockName, "中际旭创") || !recommendation.SignalAt.Equal(historicalPostSellBuySignalAt) {
			return errors.New("approved post-sell buy recommendation does not match its immutable evidence")
		}
		if err := validateHistoricalSellPrerequisite(tx); err != nil {
			return err
		}
		var failure DecisionEvent
		if err := tx.Where("event_id = ? AND recommendation_id = ?", historicalPostSellBuyFailureEventID, recommendation.RecommendationID).First(&failure).Error; err != nil {
			return err
		}
		if failure.DecisionType != "错过—资金不足" || !failure.DecidedAt.Equal(historicalPostSellBuySignalAt) || failure.QuoteAt == nil ||
			!failure.QuoteAt.Equal(historicalPostSellBuyQuoteAt) || math.Abs(failure.QuotePrice-941.41) > 1e-8 ||
			(!strings.Contains(failure.Reason, "minimum order unit") && !strings.Contains(failure.Reason, "最小")) {
			return errors.New("original missed-cash event is not the expected minimum-order failure")
		}
		capacity, err := recommendationCapacity(tx)
		if err != nil {
			return err
		}
		entry := Quote{Code: "sz300308", Name: "中际旭创", Market: "SZ", Price: 941.41, At: historicalPostSellBuyQuoteAt}
		quantity, cost, err := SizeBuy(entry.Code, entry.Price, capacity.UnreservedCash)
		if err != nil {
			return err
		}
		if quantity != 100 || math.Abs(cost.ExecutionPrice-942.35141) > 1e-8 || math.Abs(-cost.NetCashFlow-94264.35389371) > 1e-6 {
			return errors.New("historical post-sell buy cost constants no longer match approved correction")
		}
		if capacity.UnreservedCash+1e-8 < -cost.NetCashFlow {
			return ErrInsufficientCash
		}
		result := tx.Model(&SimulatedAccount{}).Where("id = ? AND cash >= ?", 1, -cost.NetCashFlow).Update("cash", gorm.Expr("cash - ?", -cost.NetCashFlow))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInsufficientCash
		}
		position := Position{RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode, StockName: recommendation.StockName, Market: "SZ", Quantity: quantity,
			EntryAt: historicalPostSellBuySignalAt, EntryPrice: cost.ExecutionPrice, BuyFees: cost.TotalFees, CurrentPrice: request.MarkQuote.Price, CurrentPriceAt: &request.MarkQuote.At, Status: "open"}
		if err := tx.Create(&position).Error; err != nil {
			return err
		}
		trade := SimulatedTrade{TradeID: newID(), RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode, Side: "buy", TradedAt: historicalPostSellBuySignalAt,
			MarketPrice: entry.Price, ExecutionPrice: cost.ExecutionPrice, Quantity: 100, Notional: cost.Notional, Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, TotalFees: cost.TotalFees, NetCashFlow: cost.NetCashFlow}
		if err := tx.Create(&trade).Error; err != nil {
			return err
		}
		if err := tx.Model(&Recommendation{}).Where("recommendation_id = ?", recommendation.RecommendationID).Updates(map[string]any{
			"status": "active", "activated_at": historicalPostSellBuySignalAt, "activation_price": cost.ExecutionPrice, "quantity": quantity, "total_fees": cost.TotalFees,
			"next_check_at": historicalPostSellBuyNextSell, "last_decision": HistoricalPostSellBuyCorrectionDecision, "last_decision_at": request.AppliedAt, "reserved_cash": 0, "previous_response_id": "",
		}).Error; err != nil {
			return err
		}
		quoteAt := entry.At
		refs, _ := json.Marshal([]string{"historical-quote:2026-08-21T14:35:42+08:00", "historical-close-mark:" + request.MarkQuote.At.Format(time.RFC3339)})
		if err := tx.Create(&DecisionEvent{EventID: correctionEventID, RecommendationID: recommendation.RecommendationID, DecisionType: HistoricalPostSellBuyCorrectionDecision, DecidedAt: request.AppliedAt,
			Reason: "修复 5 万元目标金额旧上限误判；按推荐节点及其引用落库行情补记 100 股模拟买入。", QuotePrice: entry.Price, QuoteAt: &quoteAt, SourceRefs: string(refs), DataStatus: "complete"}).Error; err != nil {
			return err
		}
		var sequence int
		if err := tx.Model(&LifecycleMessage{}).Where("recommendation_id = ?", recommendation.RecommendationID).Select("COALESCE(MAX(sequence), 0)").Scan(&sequence).Error; err != nil {
			return err
		}
		if err := tx.Create(&LifecycleMessage{RecommendationID: recommendation.RecommendationID, Sequence: sequence + 1, Role: "system", Phase: "holding", CreatedAt: request.AppliedAt,
			Content: fmt.Sprintf("1.7.6 历史纠正：按 %s 的推荐节点及 %s 落库行情（市场价 %.3f）补记模拟买入 %d 股；成交价 %.5f，费用 %.6f。原“错过—资金不足”记录保留用于审计。", historicalPostSellBuySignalAt.Format(time.RFC3339Nano), entry.At.Format(time.RFC3339), entry.Price, quantity, cost.ExecutionPrice, cost.TotalFees)}).Error; err != nil {
			return err
		}
		if err := rebuildHistoricalPostSellBuyCloseSnapshot(tx, request.MarkQuote); err != nil {
			return err
		}
		receipt, err = populateHistoricalPostSellBuyReceipt(tx, "applied")
		return err
	})
	return receipt, err
}

func validateHistoricalPostSellBuyMark(mark Quote) error {
	code, ok := NormalizeMainlandCode(mark.Code)
	if !ok || code != "sz300308" || !sameStockName(mark.Name, "中际旭创") || mark.Price <= 0 {
		return errors.New("historical close mark does not match 中际旭创")
	}
	local := ShanghaiTime(mark.At)
	if local.Format("2006-01-02") != "2026-08-21" || local.Hour() < 14 || (local.Hour() == 14 && local.Minute() < 30) || local.Hour() >= 16 {
		return errors.New("historical close mark is not from the 2026-08-21 closing session")
	}
	return nil
}

func validateHistoricalSellPrerequisite(tx *gorm.DB) error {
	var recommendation Recommendation
	if err := tx.Where("recommendation_id = ?", historicalPostSellBuySellRecommendation).First(&recommendation).Error; err != nil {
		return err
	}
	if recommendation.Status != "closed" {
		return errors.New("1.7.5 sell correction recommendation is not closed")
	}
	var positions, sells, events int64
	if err := tx.Model(&Position{}).Where("recommendation_id = ? AND status = ?", historicalPostSellBuySellRecommendation, "closed").Count(&positions).Error; err != nil {
		return err
	}
	if err := tx.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", historicalPostSellBuySellRecommendation, "sell").Count(&sells).Error; err != nil {
		return err
	}
	if err := tx.Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", historicalPostSellBuySellRecommendation, HistoricalSellCorrectionDecision).Count(&events).Error; err != nil {
		return err
	}
	if positions != 1 || sells != 1 || events != 1 {
		return errors.New("1.7.5 sell correction prerequisite is incomplete")
	}
	return nil
}

func rebuildHistoricalPostSellBuyCloseSnapshot(tx *gorm.DB, mark Quote) error {
	date := "2026-08-21"
	var snapshot AccountValuationSnapshot
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("snapshot_id = ?", "daily-close-"+date).First(&snapshot).Error; err != nil {
		return errors.New("daily close snapshot is required before historical post-sell buy correction")
	}
	account, value, status, err := storedAccountValuation(tx)
	if err != nil {
		return err
	}
	contribution, units, err := fundingLedger(tx)
	if err != nil {
		return err
	}
	nav := account.Cash + value
	updates := map[string]any{"valued_at": mark.At, "cash": account.Cash, "position_value": value, "net_asset_value": nav, "cumulative_net_contribution": contribution, "unit_value": safeUnitValue(nav, units), "time_weighted_return": safeUnitValue(nav, units) - 1, "valuation_status": status}
	return tx.Model(&AccountValuationSnapshot{}).Where("id = ?", snapshot.ID).Updates(updates).Error
}

func historicalPostSellBuyEventID() string { return "fix-d78113c2fbd942c5ad0ef203b90ffc61" }

func populateHistoricalPostSellBuyReceipt(tx *gorm.DB, status string) (HistoricalPostSellBuyCorrectionReceipt, error) {
	var trade SimulatedTrade
	if err := tx.Where("recommendation_id = ? AND side = ?", historicalPostSellBuyRecommendationID, "buy").First(&trade).Error; err != nil {
		return HistoricalPostSellBuyCorrectionReceipt{}, err
	}
	var position Position
	if err := tx.Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).First(&position).Error; err != nil {
		return HistoricalPostSellBuyCorrectionReceipt{}, err
	}
	var account SimulatedAccount
	if err := tx.First(&account, 1).Error; err != nil {
		return HistoricalPostSellBuyCorrectionReceipt{}, err
	}
	markAt := time.Time{}
	if position.CurrentPriceAt != nil {
		markAt = *position.CurrentPriceAt
	}
	return HistoricalPostSellBuyCorrectionReceipt{Status: status, RecommendationID: historicalPostSellBuyRecommendationID, TradedAt: trade.TradedAt, QuoteAt: historicalPostSellBuyQuoteAt, MarkAt: markAt, MarketPrice: trade.MarketPrice, ExecutionPrice: trade.ExecutionPrice, Quantity: trade.Quantity, TotalFees: trade.TotalFees, NetCashFlow: trade.NetCashFlow, CashAfter: account.Cash}, nil
}
