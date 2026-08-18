package research

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

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

func (r *Repository) HasRunningAnalysis(ctx context.Context) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&AnalysisRun{}).Where("status = ?", "running").Count(&count).Error
	return count > 0, err
}

func (r *Repository) HasPendingOrPosition(ctx context.Context, code string) (bool, error) {
	var recommendations int64
	if err := r.db.WithContext(ctx).Model(&Recommendation{}).
		Where("stock_code = ? AND status IN ?", code, []string{"pending", "active", "sell_pending"}).
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

func (r *Repository) Recommendation(ctx context.Context, id string) (Recommendation, error) {
	var result Recommendation
	err := r.db.WithContext(ctx).Where("recommendation_id = ?", id).First(&result).Error
	return result, err
}

func (r *Repository) DueRecommendations(ctx context.Context, now time.Time) ([]Recommendation, error) {
	var result []Recommendation
	err := r.db.WithContext(ctx).
		Where("status IN ? AND next_check_at IS NOT NULL AND next_check_at <= ?", []string{"pending", "active", "sell_pending"}, now).
		Order("next_check_at ASC, id ASC").Find(&result).Error
	return result, err
}

func (r *Repository) PendingRecommendations(ctx context.Context) ([]Recommendation, error) {
	var result []Recommendation
	err := r.db.WithContext(ctx).Where("status = ?", "pending").Order("signal_at ASC, id ASC").Find(&result).Error
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

func (r *Repository) Buy(ctx context.Context, recommendationID string, quote Quote, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var recommendation Recommendation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ?", recommendationID).First(&recommendation).Error; err != nil {
			return err
		}
		if recommendation.Status != "pending" {
			return errors.New("recommendation is no longer pending")
		}
		var account SimulatedAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
			return err
		}
		quantity, cost, err := SizeBuy(recommendation.StockCode, quote.Price, account.Cash)
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
			return errors.New("insufficient cash")
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
		next := NextLifecycleCheck(now)
		return tx.Model(&Recommendation{}).Where("recommendation_id = ?", recommendationID).Updates(map[string]any{
			"status": "active", "activated_at": quote.At, "activation_price": cost.ExecutionPrice,
			"quantity": quantity, "total_fees": cost.TotalFees, "next_check_at": next,
		}).Error
	})
}

func (r *Repository) Sell(ctx context.Context, recommendationID string, quote Quote) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var recommendation Recommendation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ?", recommendationID).First(&recommendation).Error; err != nil {
			return err
		}
		var position Position
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("recommendation_id = ? AND status = ?", recommendationID, "open").First(&position).Error; err != nil {
			return err
		}
		cost := CalculateSellCost(quote.Price, position.Quantity)
		if err := tx.Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", gorm.Expr("cash + ?", cost.NetCashFlow)).Error; err != nil {
			return err
		}
		netPnL := cost.NetCashFlow - (position.EntryPrice*float64(position.Quantity) + position.BuyFees)
		trade := SimulatedTrade{
			TradeID: newID(), RecommendationID: recommendationID, StockCode: recommendation.StockCode, Side: "sell", TradedAt: quote.At,
			MarketPrice: quote.Price, ExecutionPrice: cost.ExecutionPrice, Quantity: position.Quantity, Notional: cost.Notional,
			Commission: cost.Commission, StampDuty: cost.StampDuty, TransferFee: cost.TransferFee,
			SlippageAmount: cost.SlippageAmount, TotalFees: cost.TotalFees, NetCashFlow: cost.NetCashFlow,
		}
		if err := tx.Create(&trade).Error; err != nil {
			return err
		}
		if err := tx.Model(&Position{}).Where("id = ?", position.ID).Updates(map[string]any{
			"status": "closed", "exit_at": quote.At, "exit_price": cost.ExecutionPrice,
			"sell_fees": cost.TotalFees, "current_price": quote.Price, "current_price_at": quote.At, "net_pn_l": netPnL,
		}).Error; err != nil {
			return err
		}
		netYield := 0.0
		invested := position.EntryPrice*float64(position.Quantity) + position.BuyFees
		if invested > 0 {
			netYield = netPnL / invested
		}
		return tx.Model(&Recommendation{}).Where("recommendation_id = ?", recommendationID).Updates(map[string]any{
			"status": "closed", "closed_at": quote.At, "close_price": cost.ExecutionPrice, "next_check_at": nil,
			"total_fees": position.BuyFees + cost.TotalFees, "net_pn_l": netPnL, "net_yield_rate": netYield,
		}).Error
	})
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
	return result, err
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
