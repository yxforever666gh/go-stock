package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const HistoricalSellCorrectionDecision = "1.7.5 历史卖出纠正"

type HistoricalSellCorrectionRequest struct {
	RecommendationID string    `json:"recommendationId"`
	ObservationID    string    `json:"observationId"`
	DecisionEventID  string    `json:"decisionEventId"`
	AppliedAt        time.Time `json:"appliedAt"`
}

type HistoricalSellCorrectionReceipt struct {
	Status           string    `json:"status"`
	RecommendationID string    `json:"recommendationId"`
	StockCode        string    `json:"stockCode"`
	StockName        string    `json:"stockName"`
	ObservationID    string    `json:"observationId"`
	DecisionEventID  string    `json:"decisionEventId"`
	DecisionAt       time.Time `json:"decisionAt"`
	QuoteName        string    `json:"quoteName"`
	QuoteAt          time.Time `json:"quoteAt"`
	TradedAt         time.Time `json:"tradedAt"`
	MarketPrice      float64   `json:"marketPrice"`
	ExecutionPrice   float64   `json:"executionPrice"`
	Quantity         int64     `json:"quantity"`
	TotalFees        float64   `json:"totalFees"`
	NetCashFlow      float64   `json:"netCashFlow"`
	NetPnL           float64   `json:"netPnl"`
	CashAfter        float64   `json:"cashAfter"`
}

// ApplyHistoricalSellCorrection closes one sell-pending simulated position
// using the exact quote snapshot cited by its already-persisted AI sell
// decision. It never obtains new market data and is atomic and idempotent.
func (s *Service) ApplyHistoricalSellCorrection(ctx context.Context, request HistoricalSellCorrectionRequest) (HistoricalSellCorrectionReceipt, error) {
	s.serial.Lock()
	defer s.serial.Unlock()

	if strings.TrimSpace(request.RecommendationID) == "" || strings.TrimSpace(request.ObservationID) == "" ||
		strings.TrimSpace(request.DecisionEventID) == "" || request.AppliedAt.IsZero() {
		return HistoricalSellCorrectionReceipt{}, errors.New("recommendation, observation, decision event and applied time are required")
	}
	receipt := HistoricalSellCorrectionReceipt{}
	err := transactionWithWriteRetry(ctx, s.repository.db, func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		var recommendation Recommendation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ?", request.RecommendationID).First(&recommendation).Error; err != nil {
			return err
		}
		var position Position
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ?", request.RecommendationID).First(&position).Error; err != nil {
			return err
		}
		correctionEventID := historicalSellCorrectionEventID(request.DecisionEventID)
		var correctionCount, sellCount int64
		if err := tx.Model(&DecisionEvent{}).Where("recommendation_id = ? AND event_id = ? AND decision_type = ?", request.RecommendationID, correctionEventID, HistoricalSellCorrectionDecision).Count(&correctionCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", request.RecommendationID, "sell").Count(&sellCount).Error; err != nil {
			return err
		}
		if correctionCount == 1 && sellCount == 1 && recommendation.Status == "closed" && position.Status == "closed" {
			var err error
			receipt, err = populateHistoricalSellCorrectionReceipt(tx, request, "already_applied")
			if err == nil && (!receipt.TradedAt.Equal(receipt.DecisionAt) || receipt.ObservationID != request.ObservationID || receipt.DecisionEventID != request.DecisionEventID) {
				return errors.New("persisted historical sell correction does not match this request")
			}
			return err
		}
		if correctionCount != 0 || sellCount != 0 || recommendation.Status == "closed" || position.Status == "closed" {
			return errors.New("historical sell correction is partially applied or the position was closed elsewhere")
		}
		if recommendation.Status != "sell_pending" || position.Status != "open" {
			return fmt.Errorf("recommendation/position state is %s/%s, expected sell_pending/open", recommendation.Status, position.Status)
		}

		var observation LifecycleObservation
		if err := tx.Where("observation_id = ? AND recommendation_id = ?", request.ObservationID, request.RecommendationID).First(&observation).Error; err != nil {
			return err
		}
		if observation.Phase != "holding" || observation.Status != "ready" || !observation.ModelInvoked ||
			strings.TrimSpace(observation.CriticalFailure) != "" || strings.TrimSpace(observation.ContentFingerprint) == "" {
			return errors.New("historical sell observation is not a model-invoked ready snapshot")
		}
		var decision DecisionEvent
		if err := tx.Where("event_id = ? AND recommendation_id = ?", request.DecisionEventID, request.RecommendationID).First(&decision).Error; err != nil {
			return err
		}
		if decision.DecisionType != "卖出" || decision.DataStatus != "ready" || decision.DecidedAt.Before(observation.ObservedAt) || request.AppliedAt.Before(decision.DecidedAt) {
			return errors.New("historical decision is not a ready sell decision for the observation")
		}
		quoteSourceID := LifecycleSourceID(observation.ObservationID, LifecycleQuoteSourceSuffix)
		var sourceRefs []string
		minuteSourceID := LifecycleSourceID(observation.ObservationID, LifecycleMinuteSourceSuffix)
		if err := json.Unmarshal([]byte(decision.SourceRefs), &sourceRefs); err != nil ||
			!containsString(sourceRefs, quoteSourceID) || !containsString(sourceRefs, minuteSourceID) {
			return errors.New("historical sell decision does not cite its quote and minute observations")
		}
		var quote Quote
		if err := json.Unmarshal([]byte(observation.QuoteJSON), &quote); err != nil {
			return fmt.Errorf("decode historical quote: %w", err)
		}
		if err := validateSellQuoteAt(decision.DecidedAt, &recommendation, quote); err != nil {
			return fmt.Errorf("historical sell quote is invalid: %w", err)
		}
		var closeSnapshots int64
		closeSnapshotID := "daily-close-" + ShanghaiTime(decision.DecidedAt).Format("2006-01-02")
		if err := tx.Model(&AccountValuationSnapshot{}).Where("snapshot_id = ?", closeSnapshotID).Count(&closeSnapshots).Error; err != nil {
			return err
		}
		if closeSnapshots != 0 {
			return errors.New("daily close snapshot already exists; refusing historical sell without snapshot rebuild")
		}
		executionQuote := quote
		executionQuote.At = decision.DecidedAt
		trade, err := sellInTransaction(tx, request.RecommendationID, executionQuote)
		if err != nil {
			return err
		}
		refs, _ := json.Marshal([]string{quoteSourceID})
		correction := DecisionEvent{EventID: correctionEventID, RecommendationID: request.RecommendationID,
			DecisionType: HistoricalSellCorrectionDecision, DecidedAt: request.AppliedAt,
			Reason: fmt.Sprintf("修复除权除息行情名称误判；按 AI 于 %s 作出的卖出决策及其引用行情补记模拟卖出，原行情名称=%s",
				decision.DecidedAt.Format(time.RFC3339Nano), quote.Name),
			QuotePrice: quote.Price, QuoteAt: &quote.At, SourceRefs: string(refs), DataStatus: "complete"}
		if err := tx.Create(&correction).Error; err != nil {
			return err
		}
		var maxSequence int
		if err := tx.Model(&LifecycleMessage{}).Where("recommendation_id = ?", request.RecommendationID).
			Select("COALESCE(MAX(sequence), 0)").Scan(&maxSequence).Error; err != nil {
			return err
		}
		message := LifecycleMessage{RecommendationID: request.RecommendationID, Sequence: maxSequence + 1,
			Role: "system", Phase: "holding", CreatedAt: request.AppliedAt,
			Content: fmt.Sprintf("1.7.5 历史纠正：AI 于 %s 决定卖出，按其引用的 %s 落库行情（市场价 %.3f）补记模拟成交；成交价 %.3f，数量 %d，费用 %.2f。后续失败重试记录保留用于审计。",
				decision.DecidedAt.Format(time.RFC3339Nano), quote.At.Format(time.RFC3339), quote.Price, trade.ExecutionPrice, trade.Quantity, trade.TotalFees)}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		receipt, err = populateHistoricalSellCorrectionReceipt(tx, request, "applied")
		return err
	})
	return receipt, err
}

func populateHistoricalSellCorrectionReceipt(tx *gorm.DB, request HistoricalSellCorrectionRequest, status string) (HistoricalSellCorrectionReceipt, error) {
	var recommendation Recommendation
	if err := tx.Where("recommendation_id = ?", request.RecommendationID).First(&recommendation).Error; err != nil {
		return HistoricalSellCorrectionReceipt{}, err
	}
	var observation LifecycleObservation
	if err := tx.Where("observation_id = ? AND recommendation_id = ?", request.ObservationID, request.RecommendationID).First(&observation).Error; err != nil {
		return HistoricalSellCorrectionReceipt{}, err
	}
	var quote Quote
	if err := json.Unmarshal([]byte(observation.QuoteJSON), &quote); err != nil {
		return HistoricalSellCorrectionReceipt{}, err
	}
	var decision DecisionEvent
	if err := tx.Where("event_id = ? AND recommendation_id = ?", request.DecisionEventID, request.RecommendationID).First(&decision).Error; err != nil {
		return HistoricalSellCorrectionReceipt{}, err
	}
	var trade SimulatedTrade
	if err := tx.Where("recommendation_id = ? AND side = ?", request.RecommendationID, "sell").First(&trade).Error; err != nil {
		return HistoricalSellCorrectionReceipt{}, err
	}
	var account SimulatedAccount
	if err := tx.First(&account, 1).Error; err != nil {
		return HistoricalSellCorrectionReceipt{}, err
	}
	return HistoricalSellCorrectionReceipt{Status: status, RecommendationID: recommendation.RecommendationID,
		StockCode: recommendation.StockCode, StockName: recommendation.StockName, ObservationID: observation.ObservationID,
		DecisionEventID: decision.EventID, DecisionAt: decision.DecidedAt, QuoteName: quote.Name, QuoteAt: quote.At,
		TradedAt: trade.TradedAt, MarketPrice: trade.MarketPrice, ExecutionPrice: trade.ExecutionPrice, Quantity: trade.Quantity,
		TotalFees: trade.TotalFees, NetCashFlow: trade.NetCashFlow, NetPnL: recommendation.NetPnL, CashAfter: account.Cash}, nil
}

func historicalSellCorrectionEventID(decisionEventID string) string {
	compact := strings.ReplaceAll(strings.TrimSpace(decisionEventID), "-", "")
	if len(compact) > 32 {
		compact = compact[:32]
	}
	return "fix-" + compact
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
