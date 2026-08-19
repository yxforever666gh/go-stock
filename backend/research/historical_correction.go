package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const historicalCorrectionDecision = "1.7.1 历史资金与买入纠正"

const historicalCorrectionDepositCount = 2

// HistoricalBuyEvidence is market evidence collected before the main database
// transaction. EntryQuote is the first valid unadjusted bar of the intended
// session (or an explicitly sourced unadjusted daily open fallback).
type HistoricalBuyEvidence struct {
	RecommendationID string `json:"recommendationId"`
	EntryQuote       Quote  `json:"entryQuote"`
	EntrySource      string `json:"entrySource"`
	MarkQuote        *Quote `json:"markQuote,omitempty"`
}

type HistoricalMissedCashCorrectionRequest struct {
	FundingEffectiveAt time.Time               `json:"fundingEffectiveAt"`
	BuyTradingDate     string                  `json:"buyTradingDate"`
	FirstSellCheckAt   time.Time               `json:"firstSellCheckAt"`
	AppliedAt          time.Time               `json:"appliedAt"`
	Buys               []HistoricalBuyEvidence `json:"buys"`
}

type HistoricalCorrectedBuy struct {
	RecommendationID string    `json:"recommendationId"`
	StockCode        string    `json:"stockCode"`
	StockName        string    `json:"stockName"`
	TradedAt         time.Time `json:"tradedAt"`
	MarketPrice      float64   `json:"marketPrice"`
	ExecutionPrice   float64   `json:"executionPrice"`
	Quantity         int64     `json:"quantity"`
	TotalFees        float64   `json:"totalFees"`
	NetCashFlow      float64   `json:"netCashFlow"`
	Source           string    `json:"source"`
	BudgetException  bool      `json:"budgetException"`
}

type HistoricalMissedCashCorrectionReceipt struct {
	Status                    string                   `json:"status"`
	FundingEffectiveAt        time.Time                `json:"fundingEffectiveAt"`
	FundingAmount             float64                  `json:"fundingAmount"`
	CumulativeNetContribution float64                  `json:"cumulativeNetContribution"`
	CompletedDeposits         int                      `json:"completedDeposits"`
	RemainingDeposits         int                      `json:"remainingDeposits"`
	CashAfter                 float64                  `json:"cashAfter"`
	Buys                      []HistoricalCorrectedBuy `json:"buys"`
}

// ApplyHistoricalMissedCashCorrection performs the one-time 1.7.1 repair. It
// never obtains market data while holding the account writer lock: callers
// must collect and validate source evidence first.
func (s *Service) ApplyHistoricalMissedCashCorrection(ctx context.Context, request HistoricalMissedCashCorrectionRequest) (HistoricalMissedCashCorrectionReceipt, error) {
	s.serial.Lock()
	defer s.serial.Unlock()

	if err := validateHistoricalCorrectionRequest(request); err != nil {
		return HistoricalMissedCashCorrectionReceipt{}, err
	}
	receipt := HistoricalMissedCashCorrectionReceipt{}
	err := transactionWithWriteRetry(ctx, s.repository.db, func(tx *gorm.DB) error {
		receipt = HistoricalMissedCashCorrectionReceipt{FundingEffectiveAt: request.FundingEffectiveAt, FundingAmount: ScheduledDepositAmount * historicalCorrectionDepositCount, Buys: []HistoricalCorrectedBuy{}}
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		already, partial, err := historicalCorrectionState(tx, request)
		if err != nil {
			return err
		}
		if already {
			receipt.Status = "already_applied"
			return populateHistoricalCorrectionReceipt(tx, &receipt)
		}
		if partial {
			return errors.New("historical correction is partially applied; refusing to add cash or trades")
		}

		var account SimulatedAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
			return err
		}
		var plan FundingPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&plan, 1).Error; err != nil {
			return err
		}
		if plan.CompletedDeposits != 0 || plan.PlannedDeposits != ScheduledDepositCount || math.Abs(plan.DepositAmount-ScheduledDepositAmount) > 0.001 {
			return fmt.Errorf("funding plan is not at the expected pre-correction state: completed=%d planned=%d amount=%.2f", plan.CompletedDeposits, plan.PlannedDeposits, plan.DepositAmount)
		}
		var earlierTrades int64
		if err := tx.Model(&SimulatedTrade{}).Where("traded_at < ?", request.FundingEffectiveAt).Count(&earlierTrades).Error; err != nil {
			return err
		}
		if earlierTrades != 0 {
			return fmt.Errorf("cannot reconstruct historical funding NAV: found %d trades before %s", earlierTrades, request.FundingEffectiveAt.Format(time.RFC3339))
		}
		contribution, units, err := fundingLedger(tx)
		if err != nil {
			return err
		}
		if math.Abs(contribution-InitialCash) > 0.001 || math.Abs(units-InitialCash) > 0.001 {
			return fmt.Errorf("initial funding ledger is unexpected: contribution=%.2f units=%.6f", contribution, units)
		}
		beforeNAV := InitialCash
		unitValue := beforeNAV / units
		for sequence := 1; sequence <= historicalCorrectionDepositCount; sequence++ {
			flowBefore := beforeNAV + float64(sequence-1)*ScheduledDepositAmount
			flow := AccountCashFlow{
				FlowID: newID(), Sequence: sequence, Type: "scheduled_deposit", Amount: ScheduledDepositAmount,
				EffectiveAt: request.FundingEffectiveAt, TradingDate: request.BuyTradingDate,
				NetAssetValueBefore: flowBefore, NetAssetValueAfter: flowBefore + ScheduledDepositAmount,
				UnitValueBefore: unitValue, UnitsIssued: ScheduledDepositAmount / unitValue,
			}
			if err := tx.Create(&flow).Error; err != nil {
				return err
			}
		}
		correctionFunding := ScheduledDepositAmount * historicalCorrectionDepositCount
		if err := tx.Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", gorm.Expr("cash + ?", correctionFunding)).Error; err != nil {
			return err
		}
		if err := tx.Model(&FundingPlan{}).Where("id = ?", plan.ID).Updates(map[string]any{
			"completed_deposits": historicalCorrectionDepositCount, "last_deposit_trading_date": request.BuyTradingDate,
		}).Error; err != nil {
			return err
		}
		postContribution := InitialCash + correctionFunding
		for _, snapshot := range []AccountValuationSnapshot{
			newAccountSnapshot("pre_deposit", request.BuyTradingDate, request.FundingEffectiveAt, InitialCash, 0, InitialCash, InitialCash, 1, "complete", "pre-deposit-"+request.BuyTradingDate),
			newAccountSnapshot("post_deposit", request.BuyTradingDate, request.FundingEffectiveAt, postContribution, 0, postContribution, postContribution, 1, "complete", "post-deposit-"+request.BuyTradingDate),
		} {
			if err := tx.Create(&snapshot).Error; err != nil {
				return err
			}
		}

		evidenceByID := make(map[string]HistoricalBuyEvidence, len(request.Buys))
		for _, evidence := range request.Buys {
			evidenceByID[evidence.RecommendationID] = evidence
		}
		var recommendations []Recommendation
		ids := make([]string, 0, len(request.Buys))
		for _, evidence := range request.Buys {
			ids = append(ids, evidence.RecommendationID)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id IN ?", ids).
			Order("signal_at ASC, id ASC").Find(&recommendations).Error; err != nil {
			return err
		}
		if len(recommendations) != len(ids) {
			return errors.New("one or more correction recommendations do not exist")
		}
		for _, recommendation := range recommendations {
			evidence := evidenceByID[recommendation.RecommendationID]
			corrected, err := applyHistoricalBuy(tx, recommendation, evidence, request)
			if err != nil {
				return err
			}
			receipt.Buys = append(receipt.Buys, corrected)
		}
		if err := rebuildCorrectionCloseSnapshot(tx, request.AppliedAt, request.Buys); err != nil {
			return err
		}
		receipt.Status = "applied"
		return populateHistoricalCorrectionReceipt(tx, &receipt)
	})
	return receipt, err
}

func validateHistoricalCorrectionRequest(request HistoricalMissedCashCorrectionRequest) error {
	if request.FundingEffectiveAt.IsZero() || request.FirstSellCheckAt.IsZero() || request.AppliedAt.IsZero() {
		return errors.New("funding, sell-check and applied timestamps are required")
	}
	if request.BuyTradingDate != ShanghaiTime(request.FundingEffectiveAt).Format("2006-01-02") {
		return errors.New("buy trading date does not match funding effective date")
	}
	if len(request.Buys) != 2 {
		return errors.New("the 1.7.1 correction requires exactly two recommendations")
	}
	seen := make(map[string]struct{}, len(request.Buys))
	for _, evidence := range request.Buys {
		id := strings.TrimSpace(evidence.RecommendationID)
		if id == "" || strings.TrimSpace(evidence.EntrySource) == "" {
			return errors.New("each historical buy requires a recommendation ID and source")
		}
		if _, exists := seen[id]; exists {
			return errors.New("duplicate recommendation in historical correction")
		}
		seen[id] = struct{}{}
		quote := evidence.EntryQuote
		if err := validateBuyQuote(quote); err != nil {
			return fmt.Errorf("%s historical quote is not tradable: %w", id, err)
		}
		if ShanghaiTime(quote.At).Format("2006-01-02") != request.BuyTradingDate || !IsTradingSession(quote.At) {
			return fmt.Errorf("%s historical quote is outside the requested trading session", id)
		}
	}
	if !request.FirstSellCheckAt.After(request.FundingEffectiveAt) {
		return errors.New("first sell check must be after historical funding")
	}
	return nil
}

func historicalCorrectionState(tx *gorm.DB, request HistoricalMissedCashCorrectionRequest) (bool, bool, error) {
	var flowCount int64
	if err := tx.Model(&AccountCashFlow{}).Where("sequence IN ?", []int{1, 2}).Count(&flowCount).Error; err != nil {
		return false, false, err
	}
	completed := 0
	for _, evidence := range request.Buys {
		var positionCount, tradeCount, eventCount int64
		if err := tx.Model(&Position{}).Where("recommendation_id = ?", evidence.RecommendationID).Count(&positionCount).Error; err != nil {
			return false, false, err
		}
		if err := tx.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", evidence.RecommendationID, "buy").Count(&tradeCount).Error; err != nil {
			return false, false, err
		}
		if err := tx.Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", evidence.RecommendationID, historicalCorrectionDecision).Count(&eventCount).Error; err != nil {
			return false, false, err
		}
		if positionCount == 1 && tradeCount == 1 && eventCount == 1 {
			completed++
		} else if positionCount != 0 || tradeCount != 0 || eventCount != 0 {
			return false, true, nil
		}
	}
	if flowCount == historicalCorrectionDepositCount && completed == len(request.Buys) {
		return true, false, nil
	}
	return false, flowCount != 0 || completed != 0, nil
}

func applyHistoricalBuy(tx *gorm.DB, recommendation Recommendation, evidence HistoricalBuyEvidence, request HistoricalMissedCashCorrectionRequest) (HistoricalCorrectedBuy, error) {
	if recommendation.Status != "missed_cash" {
		return HistoricalCorrectedBuy{}, fmt.Errorf("recommendation %s status is %s, expected missed_cash", recommendation.RecommendationID, recommendation.Status)
	}
	quoteCode, ok := NormalizeMainlandCode(evidence.EntryQuote.Code)
	if !ok || quoteCode != recommendation.StockCode || !sameStockName(evidence.EntryQuote.Name, recommendation.StockName) {
		return HistoricalCorrectedBuy{}, fmt.Errorf("historical quote does not match recommendation %s", recommendation.RecommendationID)
	}
	var account SimulatedAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
		return HistoricalCorrectedBuy{}, err
	}
	quantity, cost, budgetException, err := sizeHistoricalCorrectionBuy(recommendation.StockCode, evidence.EntryQuote.Price, account.Cash)
	if err != nil {
		return HistoricalCorrectedBuy{}, fmt.Errorf("size historical buy %s: %w", recommendation.RecommendationID, err)
	}
	cashNeeded := -cost.NetCashFlow
	result := tx.Model(&SimulatedAccount{}).Where("id = ? AND cash >= ?", 1, cashNeeded).
		Update("cash", gorm.Expr("cash - ?", cashNeeded))
	if result.Error != nil {
		return HistoricalCorrectedBuy{}, result.Error
	}
	if result.RowsAffected != 1 {
		return HistoricalCorrectedBuy{}, ErrInsufficientCash
	}
	mark := evidence.EntryQuote
	markFresh := false
	if evidence.MarkQuote != nil && evidence.MarkQuote.Price > 0 && !evidence.MarkQuote.At.IsZero() &&
		ShanghaiTime(evidence.MarkQuote.At).Format("2006-01-02") == ShanghaiTime(request.AppliedAt).Format("2006-01-02") {
		markCode, valid := NormalizeMainlandCode(evidence.MarkQuote.Code)
		if valid && markCode == recommendation.StockCode && sameStockName(evidence.MarkQuote.Name, recommendation.StockName) {
			mark = *evidence.MarkQuote
			markFresh = true
		}
	}
	position := Position{RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode, StockName: recommendation.StockName,
		Market: evidence.EntryQuote.Market, Quantity: quantity, EntryAt: evidence.EntryQuote.At, EntryPrice: cost.ExecutionPrice,
		BuyFees: cost.TotalFees, CurrentPrice: mark.Price, CurrentPriceAt: &mark.At, Status: "open"}
	if err := tx.Create(&position).Error; err != nil {
		return HistoricalCorrectedBuy{}, err
	}
	trade := SimulatedTrade{TradeID: newID(), RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode,
		Side: "buy", TradedAt: evidence.EntryQuote.At, MarketPrice: evidence.EntryQuote.Price, ExecutionPrice: cost.ExecutionPrice,
		Quantity: quantity, Notional: cost.Notional, Commission: cost.Commission, TransferFee: cost.TransferFee,
		SlippageAmount: cost.SlippageAmount, TotalFees: cost.TotalFees, NetCashFlow: cost.NetCashFlow}
	if err := tx.Create(&trade).Error; err != nil {
		return HistoricalCorrectedBuy{}, err
	}
	if err := tx.Model(&Recommendation{}).Where("recommendation_id = ?", recommendation.RecommendationID).Updates(map[string]any{
		"status": "active", "activated_at": evidence.EntryQuote.At, "activation_price": cost.ExecutionPrice,
		"quantity": quantity, "total_fees": cost.TotalFees, "next_check_at": request.FirstSellCheckAt,
		"last_decision": historicalCorrectionDecision, "last_decision_at": request.AppliedAt,
		"reserved_cash": 0, "previous_response_id": "",
	}).Error; err != nil {
		return HistoricalCorrectedBuy{}, err
	}
	refs, _ := json.Marshal([]string{"historical-open:" + strings.TrimSpace(evidence.EntrySource)})
	status := "complete"
	if !markFresh {
		status = "partial"
	}
	quoteAt := evidence.EntryQuote.At
	event := DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID,
		DecisionType: historicalCorrectionDecision, DecidedAt: request.AppliedAt,
		Reason:     fmt.Sprintf("信号在收盘后发出；按下一交易日首个有效未复权行情补记模拟买入，来源=%s，最小申报单位预算例外=%t", evidence.EntrySource, budgetException),
		QuotePrice: evidence.EntryQuote.Price, QuoteAt: &quoteAt, SourceRefs: string(refs), DataStatus: status}
	if err := tx.Create(&event).Error; err != nil {
		return HistoricalCorrectedBuy{}, err
	}
	var maxSequence int
	if err := tx.Model(&LifecycleMessage{}).Where("recommendation_id = ?", recommendation.RecommendationID).
		Select("COALESCE(MAX(sequence), 0)").Scan(&maxSequence).Error; err != nil {
		return HistoricalCorrectedBuy{}, err
	}
	message := LifecycleMessage{RecommendationID: recommendation.RecommendationID, Sequence: maxSequence + 1,
		Role: "system", Phase: "holding", CreatedAt: request.AppliedAt,
		Content: fmt.Sprintf("1.7.1 历史纠正：该股票已于 %s 按 %s 来源补记买入，市场价 %.3f，成交价 %.3f，数量 %d。后续只进入持有/卖出判断。", evidence.EntryQuote.At.Format(time.RFC3339), evidence.EntrySource, evidence.EntryQuote.Price, cost.ExecutionPrice, quantity)}
	if err := tx.Create(&message).Error; err != nil {
		return HistoricalCorrectedBuy{}, err
	}
	return HistoricalCorrectedBuy{RecommendationID: recommendation.RecommendationID, StockCode: recommendation.StockCode,
		StockName: recommendation.StockName, TradedAt: evidence.EntryQuote.At, MarketPrice: evidence.EntryQuote.Price,
		ExecutionPrice: cost.ExecutionPrice, Quantity: quantity, TotalFees: cost.TotalFees,
		NetCashFlow: cost.NetCashFlow, Source: evidence.EntrySource, BudgetException: budgetException}, nil
}

// A historical correction must not fabricate an invalid odd-lot order. When
// the 5万元 cap cannot buy one legal lot, this repair alone may buy exactly one
// lot if the account (including the approved contribution) can cover it. The
// normal strategy continues to use SizeBuy and its unchanged 5万元 cap.
func sizeHistoricalCorrectionBuy(code string, marketPrice, availableCash float64) (int64, CostBreakdown, bool, error) {
	quantity, cost, err := SizeBuy(code, marketPrice, availableCash)
	if err == nil {
		return quantity, cost, false, nil
	}
	if !errors.Is(err, ErrMinimumOrder) {
		return 0, CostBreakdown{}, false, err
	}
	lot, lotErr := LotSize(code)
	if lotErr != nil {
		return 0, CostBreakdown{}, false, lotErr
	}
	cost = CalculateBuyCost(marketPrice, lot)
	if -cost.NetCashFlow > availableCash+1e-8 {
		return 0, CostBreakdown{}, false, ErrInsufficientCash
	}
	return lot, cost, true, nil
}

func rebuildCorrectionCloseSnapshot(tx *gorm.DB, appliedAt time.Time, evidence []HistoricalBuyEvidence) error {
	account, positionValue, valuationStatus, err := storedAccountValuation(tx)
	if err != nil {
		return err
	}
	for _, item := range evidence {
		if item.MarkQuote == nil || item.MarkQuote.Price <= 0 {
			valuationStatus = "partial"
			break
		}
	}
	contribution, units, err := fundingLedger(tx)
	if err != nil {
		return err
	}
	nav := account.Cash + positionValue
	unitValue := safeUnitValue(nav, units)
	local := ShanghaiTime(appliedAt)
	date := local.Format("2006-01-02")
	snapshot := newAccountSnapshot("daily_close", date, local, account.Cash, positionValue, nav, contribution, unitValue, valuationStatus, "daily-close-"+date)
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "snapshot_id"}}, DoUpdates: clause.AssignmentColumns([]string{
		"snapshot_type", "trading_date", "valued_at", "cash", "position_value", "net_asset_value",
		"cumulative_net_contribution", "unit_value", "time_weighted_return", "valuation_status",
	})}).Create(&snapshot).Error
}

func populateHistoricalCorrectionReceipt(tx *gorm.DB, receipt *HistoricalMissedCashCorrectionReceipt) error {
	var account SimulatedAccount
	if err := tx.First(&account, 1).Error; err != nil {
		return err
	}
	var plan FundingPlan
	if err := tx.First(&plan, 1).Error; err != nil {
		return err
	}
	contribution, _, err := fundingLedger(tx)
	if err != nil {
		return err
	}
	receipt.CashAfter = account.Cash
	receipt.CumulativeNetContribution = contribution
	receipt.CompletedDeposits = plan.CompletedDeposits
	receipt.RemainingDeposits = maxInt(0, plan.PlannedDeposits-plan.CompletedDeposits)
	if receipt.Status == "already_applied" {
		receipt.Buys = receipt.Buys[:0]
		ids := make([]string, 0)
		// Query correction events first so this remains generic for the two IDs.
		var events []DecisionEvent
		if err := tx.Where("decision_type = ?", historicalCorrectionDecision).Order("quote_at ASC, id ASC").Find(&events).Error; err != nil {
			return err
		}
		for _, event := range events {
			ids = append(ids, event.RecommendationID)
		}
		var trades []SimulatedTrade
		if len(ids) > 0 {
			if err := tx.Where("recommendation_id IN ? AND side = ?", ids, "buy").Order("traded_at ASC, id ASC").Find(&trades).Error; err != nil {
				return err
			}
		}
		for _, trade := range trades {
			var recommendation Recommendation
			if err := tx.Where("recommendation_id = ?", trade.RecommendationID).First(&recommendation).Error; err != nil {
				return err
			}
			receipt.Buys = append(receipt.Buys, HistoricalCorrectedBuy{RecommendationID: trade.RecommendationID,
				StockCode: trade.StockCode, StockName: recommendation.StockName, TradedAt: trade.TradedAt,
				MarketPrice: trade.MarketPrice, ExecutionPrice: trade.ExecutionPrice, Quantity: trade.Quantity,
				TotalFees: trade.TotalFees, NetCashFlow: trade.NetCashFlow, Source: "persisted-correction"})
		}
		sort.SliceStable(receipt.Buys, func(i, j int) bool { return receipt.Buys[i].TradedAt.Before(receipt.Buys[j].TradedAt) })
	}
	return nil
}
