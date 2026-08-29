package research

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db                       *gorm.DB
	policyMu                 sync.RWMutex
	targetCapitalUtilization float64
	maxImmediateBuys         int
}

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

// RecommendationCapacity is the cash-admission snapshot used before an AI run
// and rechecked transactionally when a recommendation is persisted. There is
// deliberately no position-count or same-stock limit; queued buys only reserve
// cash so concurrent recommendations cannot collectively overspend.
type RecommendationCapacity struct {
	OpenPositions      int64
	PendingBuys        int64
	ExposureCount      int64
	Cash               float64
	ReservedCash       float64
	UnreservedCash     float64
	NetAssetValue      float64 `json:"netAssetValue"`
	CapitalBuffer      float64 `json:"capitalBuffer"`
	DeployableCash     float64 `json:"deployableCash"`
	AvailableSlots     int     `json:"availableSlots"`
	CapitalUtilization float64 `json:"capitalUtilization"`
}

func NewRepository(database *gorm.DB) *Repository {
	return &Repository{db: database, targetCapitalUtilization: 0.90, maxImmediateBuys: 2}
}

func (r *Repository) SetCapitalDeploymentPolicy(targetUtilization float64, maxImmediate int) {
	if targetUtilization <= 0 || targetUtilization >= 1 {
		targetUtilization = 0.90
	}
	if maxImmediate <= 0 {
		maxImmediate = 2
	}
	r.policyMu.Lock()
	r.targetCapitalUtilization, r.maxImmediateBuys = targetUtilization, maxImmediate
	r.policyMu.Unlock()
}

func (r *Repository) capitalDeploymentPolicy() (float64, int) {
	r.policyMu.RLock()
	defer r.policyMu.RUnlock()
	return r.targetCapitalUtilization, r.maxImmediateBuys
}

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
	return capacity.DeployableCash < TargetCashPerTrade-1e-7, err
}

func (r *Repository) HasStockExposure(ctx context.Context, code string) (bool, error) {
	code, ok := NormalizeMainlandCode(code)
	if !ok {
		return false, nil
	}
	var positions int64
	if err := r.db.WithContext(ctx).Model(&Position{}).Where("stock_code = ? AND status = ?", code, "open").Count(&positions).Error; err != nil {
		return false, err
	}
	var pending int64
	err := r.db.WithContext(ctx).Model(&Recommendation{}).
		Where("stock_code = ? AND status IN ?", code, []string{"buy_pending", "pending"}).Count(&pending).Error
	return positions+pending > 0, err
}

func recommendationCapacity(tx *gorm.DB, targetUtilization float64) (RecommendationCapacity, error) {
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
	result.Cash = account.Cash
	if err := tx.Model(&Recommendation{}).Where("status IN ?", []string{"buy_pending", "pending"}).
		Select("COALESCE(SUM(reserved_cash), 0)").Scan(&result.ReservedCash).Error; err != nil {
		return result, err
	}
	result.UnreservedCash = math.Max(0, result.Cash-result.ReservedCash)
	var positions []Position
	if err := tx.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return result, err
	}
	positionValue := 0.0
	for _, position := range positions {
		price := position.CurrentPrice
		if price <= 0 {
			price = position.EntryPrice
		}
		if price > 0 && position.Quantity > 0 {
			positionValue += CalculateSellCost(price, position.Quantity).NetCashFlow
		}
	}
	result.NetAssetValue = math.Max(0, result.Cash+positionValue)
	bufferRate := 1 - targetUtilization
	if bufferRate <= 0 || bufferRate >= 1 {
		bufferRate = 0.10
	}
	result.CapitalBuffer = math.Max(TargetCashPerTrade, result.NetAssetValue*bufferRate)
	result.DeployableCash = math.Max(0, result.Cash-result.ReservedCash-result.CapitalBuffer)
	result.AvailableSlots = int(math.Floor((result.DeployableCash + 1e-8) / TargetCashPerTrade))
	if result.NetAssetValue > 0 {
		deployed := math.Max(0, result.NetAssetValue-result.Cash+result.ReservedCash)
		result.CapitalUtilization = math.Min(1, deployed/result.NetAssetValue)
	}
	return result, nil
}

func (r *Repository) RecommendationCapacity(ctx context.Context) (RecommendationCapacity, error) {
	target, _ := r.capitalDeploymentPolicy()
	return recommendationCapacity(r.db.WithContext(ctx), target)
}

func (r *Repository) HasRunningAnalysis(ctx context.Context) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&AnalysisRun{}).Where("status = ?", "running").Count(&count).Error
	return count > 0, err
}

// LatestAnalysisForScheduledSlot finds the newest attempt for one configured
// slot. New scheduled runs persist the exact zero-second slot timestamp. The
// one-minute window also recognizes runs written by versions before 1.8.2,
// which stored the cron invocation timestamp instead of the planned slot.
func (r *Repository) LatestAnalysisForScheduledSlot(ctx context.Context, scheduledFor time.Time) (AnalysisRun, bool, error) {
	var result AnalysisRun
	err := r.db.WithContext(ctx).Where("scheduled_for >= ? AND scheduled_for < ?", scheduledFor, scheduledFor.Add(time.Minute)).
		Order("started_at DESC, id DESC").First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AnalysisRun{}, false, nil
	}
	return result, err == nil, err
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

func (r *Repository) CreateBuyOpportunity(ctx context.Context, opportunity *BuyOpportunity) error {
	if opportunity.OpportunityID == "" {
		opportunity.OpportunityID = newID()
	}
	if opportunity.Status == "" {
		opportunity.Status = "active"
	}
	return r.db.WithContext(ctx).Create(opportunity).Error
}

func (r *Repository) UpdateBuyOpportunity(ctx context.Context, opportunityID string, updates map[string]any) error {
	return r.db.WithContext(ctx).Model(&BuyOpportunity{}).Where("opportunity_id = ?", opportunityID).Updates(updates).Error
}

func (r *Repository) BuyOpportunitiesForRun(ctx context.Context, runID string) ([]BuyOpportunity, error) {
	var result []BuyOpportunity
	err := r.db.WithContext(ctx).Where("analysis_run_id = ?", runID).Order("id ASC").Find(&result).Error
	return result, err
}

func (r *Repository) ListBuyOpportunities(ctx context.Context, limit, offset int) ([]BuyOpportunity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var result []BuyOpportunity
	err := r.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&result).Error
	return result, err
}

func (r *Repository) CreateRecommendation(ctx context.Context, recommendation *Recommendation, initialMessages []LifecycleMessage) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(recommendation).Error; err != nil {
			return err
		}
		if recommendation.OpportunityID != "" {
			linked := tx.Model(&BuyOpportunity{}).
				Where("opportunity_id = ? AND analysis_run_id = ?", recommendation.OpportunityID, recommendation.AnalysisRunID).
				Updates(map[string]any{"recommendation_id": recommendation.RecommendationID, "status": "linked"})
			if linked.Error != nil {
				return linked.Error
			}
			if linked.RowsAffected != 1 {
				return errors.New("buy opportunity is unavailable for recommendation")
			}
		}
		if len(initialMessages) > 0 {
			return tx.Create(&initialMessages).Error
		}
		return nil
	})
}

// CreateRecommendationWithinCapacity is the final cash admission check. The
// recommendation, initial memory and initial lifecycle event are committed as
// one unit so a failed run can never leave an invisible queued buy behind.
func (r *Repository) CreateRecommendationWithinCapacity(ctx context.Context, recommendation *Recommendation, initialMessages []LifecycleMessage, initialDecision *DecisionEvent) error {
	return transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		target, _ := r.capitalDeploymentPolicy()
		capacity, err := recommendationCapacity(tx, target)
		if err != nil {
			return err
		}
		if recommendation.ReservedCash <= 0 || recommendation.ReservedCash > TargetCashPerTrade+1e-8 || recommendation.ReservedCash > capacity.DeployableCash+1e-8 {
			return ErrInsufficientCash
		}
		var duplicate int64
		if err := tx.Model(&Position{}).Where("stock_code = ? AND status = ?", recommendation.StockCode, "open").Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate == 0 {
			if err := tx.Model(&Recommendation{}).Where("stock_code = ? AND status IN ?", recommendation.StockCode, []string{"buy_pending", "pending"}).Count(&duplicate).Error; err != nil {
				return err
			}
		}
		if duplicate > 0 {
			return ErrDuplicateStockExposure
		}
		if err := tx.Create(recommendation).Error; err != nil {
			return err
		}
		if recommendation.OpportunityID != "" {
			linked := tx.Model(&BuyOpportunity{}).
				Where("opportunity_id = ? AND analysis_run_id = ?", recommendation.OpportunityID, recommendation.AnalysisRunID).
				Updates(map[string]any{"recommendation_id": recommendation.RecommendationID, "status": "linked"})
			if linked.Error != nil {
				return linked.Error
			}
			if linked.RowsAffected != 1 {
				return errors.New("buy opportunity is unavailable for recommendation")
			}
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
		target, _ := r.capitalDeploymentPolicy()
		capacity, err := recommendationCapacity(tx, target)
		if err != nil {
			return err
		}
		availableCash := math.Max(0, account.Cash-reservedOthers-capacity.CapitalBuffer)
		budget := math.Min(TargetCashPerTrade, recommendation.ReservedCash)
		quantity, cost, err := sizeBuyWithinCashCap(recommendation.StockCode, quote.Price, availableCash, budget)
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
		trade, err := sellInTransaction(tx, recommendationID, quote)
		if err != nil {
			return err
		}
		if err := tx.Create(&DecisionEvent{EventID: newID(), RecommendationID: recommendationID,
			DecisionType: "模拟卖出", DecidedAt: quote.At, Reason: "按最新可交易行情成交",
			QuotePrice: quote.Price, QuoteAt: &quote.At}).Error; err != nil {
			return err
		}
		trigger := AnalysisTrigger{TriggerID: newID(), Source: TriggerSourceSell, SourceKey: trade.TradeID,
			SourceRecommendationID: recommendationID, SourceTradeID: trade.TradeID,
			Reason: "模拟卖出释放资金", Status: TriggerStatusQueued, AvailableAt: quote.At, CoalesceUntil: quote.At.Add(sellTriggerCoalesce)}
		return enqueueTriggerInTransaction(tx, &trigger)
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
		Select("run_id", "scheduled_for", "started_at", "completed_at", "status", "provider_name", "model_name", "recommendation_count", "failure_reason", "source_status_json", "trigger_source", "trigger_reason", "buy_now_count", "wait_count", "reject_count").
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
			TriggerSource: run.TriggerSource, TriggerReason: run.TriggerReason,
			BuyNowCount: run.BuyNowCount, WaitCount: run.WaitCount, RejectCount: run.RejectCount,
		})
	}
	return result, nil
}

func (r *Repository) Analysis(ctx context.Context, runID string) (AnalysisRun, error) {
	var result AnalysisRun
	if err := r.db.WithContext(ctx).Where("run_id = ?", runID).First(&result).Error; err != nil {
		return result, err
	}
	// Schema-20 databases and deliberately minimal compatibility fixtures do not
	// have the 2.7 opportunity table yet. The analysis report itself must remain
	// readable while the startup migrator is preparing the schema-21 upgrade.
	if !r.db.WithContext(ctx).Migrator().HasTable(&BuyOpportunity{}) {
		return result, nil
	}
	opportunities, err := r.BuyOpportunitiesForRun(ctx, runID)
	if err != nil {
		return result, err
	}
	result.Opportunities = opportunities
	return result, nil
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
		enrichRecommendationMetrics(&result[index], tradesByRecommendation[result[index].RecommendationID], positionsByRecommendation[result[index].RecommendationID])
	}
	return result, nil
}

func (r *Repository) RecentRecommendationHistory(ctx context.Context, since, before time.Time, limit int) ([]RecommendationHistoryItem, error) {
	if limit <= 0 {
		return []RecommendationHistoryItem{}, nil
	}
	if limit > 20 {
		limit = 20
	}
	result := make([]RecommendationHistoryItem, 0, limit)
	err := r.db.WithContext(ctx).Model(&Recommendation{}).
		Select("stock_code", "stock_name", "signal_at", "status", "ai_summary", "main_risk").
		Where("signal_at >= ? AND signal_at < ?", since, before).
		Order("signal_at DESC, id DESC").Limit(limit).Scan(&result).Error
	return result, err
}

func enrichRecommendationMetrics(recommendation *Recommendation, trades []SimulatedTrade, position *Position) {
	if recommendation == nil {
		return
	}
	recommendation.BuyAmount, recommendation.SellAmount, recommendation.CurrentAmount = 0, 0, 0
	recommendation.BuyPrice, recommendation.SellPrice, recommendation.CurrentPrice = 0, 0, 0
	var buyQuantity, sellQuantity int64
	var buyExecutionValue, sellExecutionValue float64
	for _, trade := range trades {
		switch trade.Side {
		case "buy":
			recommendation.BuyAmount += -trade.NetCashFlow
			if trade.Quantity > 0 && trade.ExecutionPrice > 0 {
				buyQuantity += trade.Quantity
				buyExecutionValue += trade.ExecutionPrice * float64(trade.Quantity)
			}
		case "sell":
			recommendation.SellAmount += trade.NetCashFlow
			if trade.Quantity > 0 && trade.ExecutionPrice > 0 {
				sellQuantity += trade.Quantity
				sellExecutionValue += trade.ExecutionPrice * float64(trade.Quantity)
			}
		}
	}
	if buyQuantity > 0 {
		recommendation.BuyPrice = buyExecutionValue / float64(buyQuantity)
	} else if recommendation.ActivationPrice > 0 {
		recommendation.BuyPrice = recommendation.ActivationPrice
	} else if position != nil {
		recommendation.BuyPrice = position.EntryPrice
	}
	if sellQuantity > 0 {
		recommendation.SellPrice = sellExecutionValue / float64(sellQuantity)
	} else if recommendation.ClosePrice > 0 {
		recommendation.SellPrice = recommendation.ClosePrice
	} else if position != nil && position.Status == "closed" {
		recommendation.SellPrice = position.ExitPrice
	}
	if position != nil && position.Status == "open" {
		enrichPositionValue(position)
		recommendation.CurrentAmount = position.NetSellValue
		recommendation.CurrentPrice = position.CurrentPrice
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
