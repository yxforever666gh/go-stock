package research

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

const MaxPortfolioExposures = 10

var (
	ErrCapacityReached   = errors.New("portfolio recommendation capacity reached")
	ErrDuplicateExposure = errors.New("stock already has a pending recommendation or open position")
)

// lockAccountForWrite turns SQLite's deferred transaction into a write
// transaction before any capacity or cash reads. SQLite ignores SELECT FOR
// UPDATE, while this no-op UPDATE acquires the database writer lock without
// changing cash or UpdatedAt. It therefore also protects callers using a
// second Repository/Service instance in the same process (or another process).
func lockAccountForWrite(tx *gorm.DB) error {
	result := tx.Exec("UPDATE research_v160_simulated_accounts SET cash = cash WHERE id = ?", 1)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("simulated account is unavailable")
	}
	return nil
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func transactionWithWriteRetry(ctx context.Context, database *gorm.DB, operation func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = database.WithContext(ctx).Transaction(operation)
		if !isSQLiteBusy(err) {
			return err
		}
		delay := time.Duration(20*(1<<attempt)) * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return err
}

// RecommendationCapacity is the admission-control snapshot used before an AI
// run and rechecked transactionally when a recommendation is persisted. A
// queued buy reserves the full per-trade budget so multiple recommendations
// cannot collectively promise more cash than the account owns.
type RecommendationCapacity struct {
	OpenPositions      int64
	PendingBuys        int64
	ExposureCount      int64
	RemainingPositions int
	Cash               float64
	ReservedCash       float64
	UnreservedCash     float64
	AffordableSlots    int
	AllowedNew         int
}

func NewRepository(database *gorm.DB) *Repository { return &Repository{db: database} }

func (r *Repository) DB() *gorm.DB { return r.db }

func (r *Repository) EnsureAccount(ctx context.Context) error {
	account := SimulatedAccount{ID: 1, InitialCash: InitialCash, Cash: InitialCash}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error
}

func (r *Repository) HasOpenPosition(ctx context.Context) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Position{}).Where("status = ?", "open").Count(&count).Error
	return count > 0, err
}

func (r *Repository) HasBlockingExposure(ctx context.Context) (bool, error) {
	capacity, err := r.RecommendationCapacity(ctx)
	return capacity.AllowedNew == 0, err
}

func recommendationCapacity(tx *gorm.DB) (RecommendationCapacity, error) {
	var result RecommendationCapacity
	if err := tx.Model(&Position{}).Where("status = ?", "open").Count(&result.OpenPositions).Error; err != nil {
		return result, err
	}
	if err := tx.Model(&Recommendation{}).Where("status IN ?", []string{"buy_pending", "pending"}).Count(&result.PendingBuys).Error; err != nil {
		return result, err
	}
	var account SimulatedAccount
	if err := tx.First(&account, 1).Error; err != nil {
		return result, err
	}
	result.ExposureCount = result.OpenPositions + result.PendingBuys
	result.RemainingPositions = MaxPortfolioExposures - int(result.ExposureCount)
	if result.RemainingPositions < 0 {
		result.RemainingPositions = 0
	}
	result.Cash = account.Cash
	if err := tx.Model(&Recommendation{}).Where("status IN ?", []string{"buy_pending", "pending"}).
		Select("COALESCE(SUM(reserved_cash), 0)").Scan(&result.ReservedCash).Error; err != nil {
		return result, err
	}
	result.UnreservedCash = math.Max(0, result.Cash-result.ReservedCash)
	if result.UnreservedCash > 1e-7 {
		result.AffordableSlots = result.RemainingPositions
	}
	result.AllowedNew = result.RemainingPositions
	if result.AffordableSlots < result.AllowedNew {
		result.AllowedNew = result.AffordableSlots
	}
	if result.AllowedNew > 2 {
		result.AllowedNew = 2
	}
	return result, nil
}

func (r *Repository) RecommendationCapacity(ctx context.Context) (RecommendationCapacity, error) {
	return recommendationCapacity(r.db.WithContext(ctx))
}

func (r *Repository) HasRunningAnalysis(ctx context.Context) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&AnalysisRun{}).Where("status = ?", "running").Count(&count).Error
	return count > 0, err
}

func (r *Repository) HasPendingOrPosition(ctx context.Context, code string) (bool, error) {
	var recommendations int64
	if err := r.db.WithContext(ctx).Model(&Recommendation{}).
		Where("stock_code = ? AND status IN ?", code, []string{"buy_pending", "pending", "active", "sell_pending"}).
		Count(&recommendations).Error; err != nil {
		return false, err
	}
	if recommendations > 0 {
		return true, nil
	}
	var positions int64
	err := r.db.WithContext(ctx).Model(&Position{}).Where("stock_code = ? AND status = ?", code, "open").Count(&positions).Error
	return positions > 0, err
}

func (r *Repository) CreateAnalysis(ctx context.Context, run *AnalysisRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

func (r *Repository) SaveAnalysis(ctx context.Context, run *AnalysisRun) error {
	return r.db.WithContext(ctx).Save(run).Error
}

func (r *Repository) UpdateAnalysisAttemptLog(ctx context.Context, runID, value string) error {
	return r.db.WithContext(ctx).Model(&AnalysisRun{}).
		Where("run_id = ?", runID).
		Update("model_attempt_log_json", value).Error
}

func (r *Repository) CreateRecommendation(ctx context.Context, recommendation *Recommendation, initialMessages []LifecycleMessage) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(recommendation).Error; err != nil {
			return err
		}
		if len(initialMessages) > 0 {
			return tx.Create(&initialMessages).Error
		}
		return nil
	})
}

// CreateRecommendationWithinCapacity is the final admission check. The
// recommendation, initial memory and initial lifecycle event are committed as
// one unit so a failed run can never leave an invisible queued buy behind.
func (r *Repository) CreateRecommendationWithinCapacity(ctx context.Context, recommendation *Recommendation, initialMessages []LifecycleMessage, initialDecision *DecisionEvent) error {
	return transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		var duplicateRecommendations int64
		if err := tx.Model(&Recommendation{}).
			Where("stock_code = ? AND status IN ?", recommendation.StockCode, []string{"buy_pending", "pending", "active", "sell_pending"}).
			Count(&duplicateRecommendations).Error; err != nil {
			return err
		}
		var duplicatePositions int64
		if err := tx.Model(&Position{}).Where("stock_code = ? AND status = ?", recommendation.StockCode, "open").
			Count(&duplicatePositions).Error; err != nil {
			return err
		}
		if duplicateRecommendations > 0 || duplicatePositions > 0 {
			return ErrDuplicateExposure
		}
		capacity, err := recommendationCapacity(tx)
		if err != nil {
			return err
		}
		if capacity.AllowedNew < 1 {
			return ErrCapacityReached
		}
		if recommendation.ReservedCash <= 0 || recommendation.ReservedCash > capacity.UnreservedCash+1e-8 {
			return ErrInsufficientCash
		}
		if err := tx.Create(recommendation).Error; err != nil {
			return err
		}
		if len(initialMessages) > 0 {
			if err := tx.Create(&initialMessages).Error; err != nil {
				return err
			}
		}
		if initialDecision != nil {
			return tx.Create(initialDecision).Error
		}
		return nil
	})
}

func (r *Repository) Recommendation(ctx context.Context, id string) (Recommendation, error) {
	var result Recommendation
	err := r.db.WithContext(ctx).Where("recommendation_id = ?", id).First(&result).Error
	return result, err
}

func (r *Repository) DueRecommendations(ctx context.Context, now time.Time) ([]Recommendation, error) {
	var result []Recommendation
	err := r.db.WithContext(ctx).
		Where("status IN ? AND next_check_at IS NOT NULL AND next_check_at <= ?", []string{"buy_pending", "active", "sell_pending"}, now).
		Order("next_check_at ASC, signal_at ASC, id ASC").Find(&result).Error
	return result, err
}

func (r *Repository) Messages(ctx context.Context, recommendationID string) ([]LifecycleMessage, error) {
	var result []LifecycleMessage
	err := r.db.WithContext(ctx).Where("recommendation_id = ?", recommendationID).Order("sequence ASC").Find(&result).Error
	return result, err
}

func (r *Repository) AppendMessage(ctx context.Context, message *LifecycleMessage) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var next int
		if err := tx.Model(&LifecycleMessage{}).Where("recommendation_id = ?", message.RecommendationID).
			Select("COALESCE(MAX(sequence), 0) + 1").Scan(&next).Error; err != nil {
			return err
		}
		message.Sequence = next
		return tx.Create(message).Error
	})
}

func (r *Repository) AppendDecision(ctx context.Context, event *DecisionEvent) error {
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *Repository) AppendObservation(ctx context.Context, observation *LifecycleObservation) error {
	return r.db.WithContext(ctx).Create(observation).Error
}

func (r *Repository) MarkObservationModelInvoked(ctx context.Context, observationID string) error {
	return r.db.WithContext(ctx).Model(&LifecycleObservation{}).
		Where("observation_id = ?", observationID).Update("model_invoked", true).Error
}

func (r *Repository) LastUsableObservation(ctx context.Context, recommendationID string) (LifecycleObservation, error) {
	var result LifecycleObservation
	err := r.db.WithContext(ctx).Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"ready", "partial"}).
		Order("observed_at DESC, id DESC").First(&result).Error
	return result, err
}

func (r *Repository) ObservationFingerprints(ctx context.Context, recommendationID string, limit int) (map[string]struct{}, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows []LifecycleObservation
	err := r.db.WithContext(ctx).Where("recommendation_id = ?", recommendationID).
		Order("observed_at DESC, id DESC").Limit(limit).Find(&rows).Error
	result := make(map[string]struct{}, len(rows)*4)
	for _, row := range rows {
		if row.ContentFingerprint != "" {
			result[row.ContentFingerprint] = struct{}{}
		}
		for _, source := range ParseLifecycleEvidence(row) {
			if source.Fingerprint != "" {
				result[source.Fingerprint] = struct{}{}
			}
		}
	}
	return result, err
}

func (r *Repository) UpdateRecommendation(ctx context.Context, id string, updates map[string]any) error {
	return r.db.WithContext(ctx).Model(&Recommendation{}).Where("recommendation_id = ?", id).Updates(updates).Error
}

func (r *Repository) Position(ctx context.Context, recommendationID string) (Position, error) {
	var result Position
	err := r.db.WithContext(ctx).Where("recommendation_id = ?", recommendationID).First(&result).Error
	return result, err
}

func (r *Repository) Buy(ctx context.Context, recommendationID string, quote Quote, nextCheck time.Time, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		var recommendation Recommendation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ?", recommendationID).First(&recommendation).Error; err != nil {
			return err
		}
		if recommendation.Status != "buy_pending" {
			return errors.New("recommendation is no longer awaiting direct buy")
		}
		var account SimulatedAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
			return err
		}
		var reservedOthers float64
		if err := tx.Model(&Recommendation{}).Where("status IN ? AND recommendation_id <> ?", []string{"buy_pending", "pending"}, recommendationID).
			Select("COALESCE(SUM(reserved_cash), 0)").Scan(&reservedOthers).Error; err != nil {
			return err
		}
		availableCash := math.Max(0, account.Cash-reservedOthers)
		quantity, cost, err := SizeBuy(recommendation.StockCode, quote.Price, availableCash)
		if err != nil {
			return err
		}
		cashNeeded := -cost.NetCashFlow
		result := tx.Model(&SimulatedAccount{}).Where("id = ? AND cash >= ?", 1, cashNeeded).
			Update("cash", gorm.Expr("cash - ?", cashNeeded))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInsufficientCash
		}
		position := Position{
			RecommendationID: recommendationID, StockCode: recommendation.StockCode, StockName: recommendation.StockName,
			Market: quote.Market, Quantity: quantity, EntryAt: quote.At, EntryPrice: cost.ExecutionPrice,
			BuyFees: cost.TotalFees, CurrentPrice: quote.Price, CurrentPriceAt: &quote.At, Status: "open",
		}
		if err := tx.Create(&position).Error; err != nil {
			return err
		}
		trade := SimulatedTrade{
			TradeID: newID(), RecommendationID: recommendationID, StockCode: recommendation.StockCode, Side: "buy", TradedAt: quote.At,
			MarketPrice: quote.Price, ExecutionPrice: cost.ExecutionPrice, Quantity: quantity, Notional: cost.Notional,
			Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount,
			TotalFees: cost.TotalFees, NetCashFlow: cost.NetCashFlow,
		}
		if err := tx.Create(&trade).Error; err != nil {
			return err
		}
		if err := tx.Model(&Recommendation{}).Where("recommendation_id = ?", recommendationID).Updates(map[string]any{
			"status": "active", "activated_at": quote.At, "activation_price": cost.ExecutionPrice,
			"quantity": quantity, "total_fees": cost.TotalFees, "next_check_at": nextCheck,
			"last_decision": "模拟买入", "last_decision_at": now, "reserved_cash": 0,
		}).Error; err != nil {
			return err
		}
		return tx.Create(&DecisionEvent{EventID: newID(), RecommendationID: recommendationID,
			DecisionType: "模拟买入", DecidedAt: now, Reason: "AI 推荐后按最新可交易行情直接成交",
			QuotePrice: quote.Price, QuoteAt: &quote.At}).Error
	})
}

func (r *Repository) FailBuy(ctx context.Context, recommendationID, status, decisionType, reason string, now time.Time, quote *Quote) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		result := tx.Model(&Recommendation{}).
			Where("recommendation_id = ? AND status = ?", recommendationID, "buy_pending").
			Updates(map[string]any{"status": status, "next_check_at": nil, "last_decision": decisionType, "last_decision_at": now, "reserved_cash": 0})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("recommendation is no longer awaiting direct buy")
		}
		event := DecisionEvent{EventID: newID(), RecommendationID: recommendationID,
			DecisionType: decisionType, DecidedAt: now, Reason: reason}
		if quote != nil {
			event.QuotePrice, event.QuoteAt = quote.Price, &quote.At
		}
		return tx.Create(&event).Error
	})
}

func (r *Repository) DeferBuyProcessingError(ctx context.Context, recommendationID, reason string, next, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		result := tx.Model(&Recommendation{}).
			Where("recommendation_id = ? AND status = ?", recommendationID, "buy_pending").
			Updates(map[string]any{"next_check_at": next, "last_decision": "买入处理重试", "last_decision_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("recommendation is no longer awaiting direct buy")
		}
		return tx.Create(&DecisionEvent{EventID: newID(), RecommendationID: recommendationID,
			DecisionType: "买入处理重试", DecidedAt: now, Reason: reason, DataStatus: "internal_error"}).Error
	})
}

func (r *Repository) Sell(ctx context.Context, recommendationID string, quote Quote) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := sellInTransaction(tx, recommendationID, quote)
		return err
	})
}

func sellInTransaction(tx *gorm.DB, recommendationID string, quote Quote) (SimulatedTrade, error) {
	if err := lockAccountForWrite(tx); err != nil {
		return SimulatedTrade{}, err
	}
	var recommendation Recommendation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ?", recommendationID).First(&recommendation).Error; err != nil {
		return SimulatedTrade{}, err
	}
	var position Position
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ? AND status = ?", recommendationID, "open").First(&position).Error; err != nil {
		return SimulatedTrade{}, err
	}
	cost := CalculateSellCost(quote.Price, position.Quantity)
	if err := tx.Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", gorm.Expr("cash + ?", cost.NetCashFlow)).Error; err != nil {
		return SimulatedTrade{}, err
	}
	netPnL := cost.NetCashFlow - (position.EntryPrice*float64(position.Quantity) + position.BuyFees)
	trade := SimulatedTrade{
		TradeID: newID(), RecommendationID: recommendationID, StockCode: recommendation.StockCode, Side: "sell", TradedAt: quote.At,
		MarketPrice: quote.Price, ExecutionPrice: cost.ExecutionPrice, Quantity: position.Quantity, Notional: cost.Notional,
		Commission: cost.Commission, StampDuty: cost.StampDuty, TransferFee: cost.TransferFee,
		SlippageAmount: cost.SlippageAmount, TotalFees: cost.TotalFees, NetCashFlow: cost.NetCashFlow,
	}
	if err := tx.Create(&trade).Error; err != nil {
		return SimulatedTrade{}, err
	}
	if err := tx.Model(&Position{}).Where("id = ?", position.ID).Updates(map[string]any{
		"status": "closed", "exit_at": quote.At, "exit_price": cost.ExecutionPrice,
		"sell_fees": cost.TotalFees, "current_price": quote.Price, "current_price_at": quote.At, "net_pn_l": netPnL,
	}).Error; err != nil {
		return SimulatedTrade{}, err
	}
	netYield := 0.0
	invested := position.EntryPrice*float64(position.Quantity) + position.BuyFees
	if invested > 0 {
		netYield = netPnL / invested
	}
	if err := tx.Model(&Recommendation{}).Where("recommendation_id = ?", recommendationID).Updates(map[string]any{
		"status": "closed", "closed_at": quote.At, "close_price": cost.ExecutionPrice, "next_check_at": nil,
		"total_fees": position.BuyFees + cost.TotalFees, "net_pn_l": netPnL, "net_yield_rate": netYield,
	}).Error; err != nil {
		return SimulatedTrade{}, err
	}
	return trade, nil
}

func (r *Repository) ListAnalysis(ctx context.Context, limit, offset int) ([]AnalysisRunSummary, error) {
	var runs []AnalysisRun
	err := r.db.WithContext(ctx).
		Select("run_id", "scheduled_for", "started_at", "completed_at", "status", "provider_name", "model_name", "recommendation_count", "failure_reason", "source_status_json").
		Order("started_at DESC, id DESC").Limit(limit).Offset(offset).Find(&runs).Error
	if err != nil {
		return nil, err
	}
	result := make([]AnalysisRunSummary, 0, len(runs))
	for _, run := range runs {
		var sources []struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal([]byte(run.SourceStatusJSON), &sources)
		failed := 0
		for _, source := range sources {
			if source.Error != "" {
				failed++
			}
		}
		result = append(result, AnalysisRunSummary{
			RunID: run.RunID, ScheduledFor: run.ScheduledFor, StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
			Status: run.Status, ProviderName: run.ProviderName, ModelName: run.ModelName,
			RecommendationCount: run.RecommendationCount, FailureReason: run.FailureReason,
			SourceCount: len(sources), FailedSourceCount: failed,
		})
	}
	return result, nil
}

func (r *Repository) Analysis(ctx context.Context, runID string) (AnalysisRun, error) {
	var result AnalysisRun
	err := r.db.WithContext(ctx).Where("run_id = ?", runID).First(&result).Error
	return result, err
}

func (r *Repository) ListRecommendations(ctx context.Context, limit, offset int) ([]Recommendation, error) {
	var result []Recommendation
	err := r.db.WithContext(ctx).Order("signal_at DESC, id DESC").Limit(limit).Offset(offset).Find(&result).Error
	if err != nil || len(result) == 0 {
		return result, err
	}
	ids := make([]string, 0, len(result))
	for _, recommendation := range result {
		ids = append(ids, recommendation.RecommendationID)
	}
	var trades []SimulatedTrade
	if err = r.db.WithContext(ctx).Where("recommendation_id IN ?", ids).Order("traded_at ASC, id ASC").Find(&trades).Error; err != nil {
		return nil, err
	}
	var positions []Position
	if err = r.db.WithContext(ctx).Where("recommendation_id IN ?", ids).Find(&positions).Error; err != nil {
		return nil, err
	}
	tradesByRecommendation := make(map[string][]SimulatedTrade, len(result))
	for _, trade := range trades {
		tradesByRecommendation[trade.RecommendationID] = append(tradesByRecommendation[trade.RecommendationID], trade)
	}
	positionsByRecommendation := make(map[string]*Position, len(positions))
	for index := range positions {
		positionsByRecommendation[positions[index].RecommendationID] = &positions[index]
	}
	for index := range result {
		enrichRecommendationAmounts(&result[index], tradesByRecommendation[result[index].RecommendationID], positionsByRecommendation[result[index].RecommendationID])
	}
	return result, nil
}

func enrichRecommendationAmounts(recommendation *Recommendation, trades []SimulatedTrade, position *Position) {
	if recommendation == nil {
		return
	}
	recommendation.BuyAmount, recommendation.SellAmount, recommendation.CurrentAmount = 0, 0, 0
	for _, trade := range trades {
		switch trade.Side {
		case "buy":
			recommendation.BuyAmount += -trade.NetCashFlow
		case "sell":
			recommendation.SellAmount += trade.NetCashFlow
		}
	}
	if position != nil && position.Status == "open" {
		enrichPositionValue(position)
		recommendation.CurrentAmount = position.NetSellValue
	}
	comparisonAmount := recommendation.SellAmount + recommendation.CurrentAmount
	if recommendation.BuyAmount > 0 && comparisonAmount > 0 {
		recommendation.NetPnL = comparisonAmount - recommendation.BuyAmount
		recommendation.NetYieldRate = recommendation.NetPnL / recommendation.BuyAmount
	}
}

func (r *Repository) Detail(ctx context.Context, recommendationID string) (RecommendationDetail, error) {
	recommendation, err := r.Recommendation(ctx, recommendationID)
	if err != nil {
		return RecommendationDetail{}, err
	}
	analysis, err := r.Analysis(ctx, recommendation.AnalysisRunID)
	if err != nil {
		return RecommendationDetail{}, err
	}
	detail := RecommendationDetail{Recommendation: recommendation, Analysis: analysis}
	if err = r.db.WithContext(ctx).Where("recommendation_id = ?", recommendationID).Order("sequence ASC").Find(&detail.Messages).Error; err != nil {
		return RecommendationDetail{}, err
	}
	if err = r.db.WithContext(ctx).Where("recommendation_id = ?", recommendationID).Order("decided_at ASC, id ASC").Find(&detail.Decisions).Error; err != nil {
		return RecommendationDetail{}, err
	}
	if err = r.db.WithContext(ctx).Where("recommendation_id = ?", recommendationID).Order("observed_at ASC, id ASC").Find(&detail.Observations).Error; err != nil {
		return RecommendationDetail{}, err
	}
	if err = r.db.WithContext(ctx).Where("recommendation_id = ?", recommendationID).Order("traded_at ASC, id ASC").Find(&detail.Trades).Error; err != nil {
		return RecommendationDetail{}, err
	}
	position, positionErr := r.Position(ctx, recommendationID)
	if positionErr == nil {
		detail.Position = &position
	} else if !errors.Is(positionErr, gorm.ErrRecordNotFound) {
		return RecommendationDetail{}, positionErr
	}
	return detail, nil
}

func (r *Repository) OpenPositions(ctx context.Context) ([]Position, error) {
	var positions []Position
	err := r.db.WithContext(ctx).Where("status = ?", "open").Order("entry_at ASC, id ASC").Find(&positions).Error
	return positions, err
}

func (r *Repository) Account(ctx context.Context) (SimulatedAccount, error) {
	var account SimulatedAccount
	err := r.db.WithContext(ctx).First(&account, 1).Error
	return account, err
}

func (r *Repository) UpdatePositionQuote(ctx context.Context, id uint, quote Quote) error {
	return r.db.WithContext(ctx).Model(&Position{}).Where("id = ? AND status = ?", id, "open").Updates(map[string]any{
		"current_price": quote.Price, "current_price_at": quote.At,
	}).Error
}
