package research2

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

var (
	ErrDailyBuyLimitReached   = errors.New("research2 daily buy limit is already reached")
	ErrExecutionChainClosed  = errors.New("research2 daily execution target is already closed")
)

// SQLite ignores SELECT FOR UPDATE. Acquire the single-writer lock explicitly
// before reading the research2 cash balance so concurrent schedulers cannot
// race the read/modify/write transaction.
func lockResearch2AccountForWrite(tx *gorm.DB) error {
	result := tx.Exec("UPDATE research2_accounts SET cash = cash WHERE id = ?", 1)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("research2 account is unavailable")
	}
	return nil
}

func isResearch2SQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func research2TransactionWithWriteRetry(ctx context.Context, database *gorm.DB, operation func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = database.WithContext(ctx).Transaction(operation)
		if !isResearch2SQLiteBusy(err) {
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

func NewRepository(database *gorm.DB) *Repository { return &Repository{db: database} }
func (r *Repository) DB() *gorm.DB                { return r.db }

func (r *Repository) EnsureAccount(ctx context.Context) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&Account{ID: 1, InitialCash: InitialCash, Cash: InitialCash}).Error
}

func (r *Repository) CreateRun(ctx context.Context, run *AnalysisRun) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Create(run).Error
	})
}

// CreateRunAttempt atomically claims the next analysis attempt for a trading
// date. The database composite unique key is the final guard against multiple
// scheduler instances creating the same attempt.
func (r *Repository) CreateRunAttempt(ctx context.Context, run *AnalysisRun, allowRetry bool) (AnalysisRun, bool, error) {
	if run == nil {
		return AnalysisRun{}, false, errors.New("research2 analysis run is required")
	}
	var selected AnalysisRun
	created := false
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		selected = AnalysisRun{}
		created = false
		var latest AnalysisRun
		err := tx.Where("trading_date = ?", run.TradingDate).Order("attempt_no DESC, id DESC").First(&latest).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			run.AttemptNo = 1
		case err != nil:
			return err
		case latest.Status == "running":
			selected = latest
			return nil
		case !allowRetry || (run.TriggerSource != "untradable_refill" && latest.Status != "failed" &&
			!(latest.Status == "no_recommendation" && latest.StrategyVersion != run.StrategyVersion)):
			selected = latest
			return nil
		default:
			run.AttemptNo = latest.AttemptNo + 1
		}

		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "trading_date"}, {Name: "attempt_no"}},
			DoNothing: true,
		}).Create(run)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			selected, created = *run, true
			return nil
		}
		return tx.Where("trading_date = ?", run.TradingDate).Order("attempt_no DESC, id DESC").First(&selected).Error
	})
	return selected, created, err
}
func (r *Repository) SaveRun(ctx context.Context, run *AnalysisRun) error {
	if run == nil {
		return errors.New("research2 analysis run is required")
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Save(run).Error
	})
}
func (r *Repository) RunForDate(ctx context.Context, tradingDate string) (AnalysisRun, bool, error) {
	var item AnalysisRun
	err := r.db.WithContext(ctx).Where("trading_date = ?", tradingDate).Order("attempt_no DESC, id DESC").First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AnalysisRun{}, false, nil
	}
	return item, err == nil, err
}
func (r *Repository) CreateRecommendations(ctx context.Context, items []Recommendation) error {
	if len(items) == 0 {
		return nil
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		rows := append([]Recommendation(nil), items...)
		for index := range rows {
			rows[index].ID = 0
		}
		return tx.Create(&rows).Error
	})

}

// FinalizeRun publishes a completed analysis and its executable recommendations
// in one transaction. A trading poll can therefore never observe recommendations
// belonging to a run that is still running or failed to persist.
func (r *Repository) FinalizeRun(ctx context.Context, run *AnalysisRun, items []Recommendation) error {
	if run == nil {
		return errors.New("research2 analysis run is required")
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Save(run).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		rows := append([]Recommendation(nil), items...)
		for index := range rows {
			rows[index].ID = 0
		}
		return tx.Create(&rows).Error
	})
}
func (r *Repository) ListRuns(ctx context.Context, limit, offset int) ([]AnalysisRunSummary, error) {
	var rows []AnalysisRun
	err := r.db.WithContext(ctx).Order("scheduled_for DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	deliveries, err := r.emailDeliveryMap(ctx, rows)
	if err != nil {
		return nil, err
	}
	chains, err := r.executionChainMap(ctx, rows)
	if err != nil {
		return nil, err
	}
	items := make([]AnalysisRunSummary, 0, len(rows))
	for _, row := range rows {
		delivery := deliveries[row.RunID]
		chain := chains[row.ChainID]
		items = append(items, AnalysisRunSummary{RunID: row.RunID, TradingDate: row.TradingDate, AttemptNo: row.AttemptNo, ChainID: row.ChainID, ParentRunID: row.ParentRunID, TriggerSource: row.TriggerSource, RequestedSlots: row.RequestedSlots, PrimaryCount: row.PrimaryCount, StandbyCount: row.StandbyCount, ScheduledFor: row.ScheduledFor, StartedAt: row.StartedAt, EvidenceWindowStartAt: row.EvidenceWindowStartAt, EvidenceCutoffAt: row.EvidenceCutoffAt, EvidenceCoveragePct: row.EvidenceCoveragePct, Degraded: row.Degraded, GeneratedAt: row.GeneratedAt, Status: row.Status, ProviderName: row.ProviderName, ModelName: row.ModelName, StrategyVersion: row.StrategyVersion, EvidenceProfileVersion: row.EvidenceProfileVersion, EvidenceSetID: row.EvidenceSetID, RecommendationCount: row.RecommendationCount, OnTime: row.OnTime, FailureReason: row.FailureReason, EmailDeliveryStatus: delivery.Status, EmailSentAt: delivery.SentAt, EmailAttemptCount: delivery.AttemptCount, EmailLastError: delivery.LastError, ExecutionChain: chain})
	}
	return items, nil
}
func (r *Repository) GetRun(ctx context.Context, id string) (AnalysisRun, error) {
	var item AnalysisRun
	err := r.db.WithContext(ctx).Where("run_id = ?", id).First(&item).Error
	if err != nil {
		return item, err
	}
	var delivery EmailDelivery
	if deliveryErr := r.db.WithContext(ctx).Where("analysis_run_id = ?", item.RunID).First(&delivery).Error; deliveryErr == nil {
		item.EmailDeliveryStatus = delivery.Status
		item.EmailSentAt = delivery.SentAt
		item.EmailAttemptCount = delivery.AttemptCount
		item.EmailLastError = delivery.LastError
	} else if !errors.Is(deliveryErr, gorm.ErrRecordNotFound) {
		return item, deliveryErr
	}
	if strings.TrimSpace(item.ChainID) != "" {
		var chain ExecutionChain
		if chainErr := r.db.WithContext(ctx).Where("chain_id = ?", item.ChainID).First(&chain).Error; chainErr == nil {
			item.ExecutionChain = &chain
		} else if !errors.Is(chainErr, gorm.ErrRecordNotFound) {
			return item, chainErr
		}
	}
	return item, err
}

func (r *Repository) executionChainMap(ctx context.Context, runs []AnalysisRun) (map[string]*ExecutionChain, error) {
	result := make(map[string]*ExecutionChain)
	ids := make([]string, 0, len(runs))
	seen := make(map[string]struct{}, len(runs))
	for _, run := range runs {
		if id := strings.TrimSpace(run.ChainID); id != "" {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	var chains []ExecutionChain
	if err := r.db.WithContext(ctx).Where("chain_id IN ?", ids).Find(&chains).Error; err != nil {
		return nil, err
	}
	for index := range chains {
		chain := chains[index]
		result[chain.ChainID] = &chain
	}
	return result, nil
}

func (r *Repository) emailDeliveryMap(ctx context.Context, runs []AnalysisRun) (map[string]EmailDelivery, error) {
	result := make(map[string]EmailDelivery, len(runs))
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.RunID)
	}
	if len(ids) == 0 {
		return result, nil
	}
	var deliveries []EmailDelivery
	if err := r.db.WithContext(ctx).Where("analysis_run_id IN ?", ids).Find(&deliveries).Error; err != nil {
		return nil, err
	}
	for _, delivery := range deliveries {
		result[delivery.AnalysisRunID] = delivery
	}
	return result, nil
}

func (r *Repository) CreateEmailDelivery(ctx context.Context, delivery *EmailDelivery) (bool, error) {
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "analysis_run_id"}}, DoNothing: true}).Create(delivery)
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) DueEmailDeliveries(ctx context.Context, now time.Time, limit int) ([]EmailDelivery, error) {
	var items []EmailDelivery
	if limit <= 0 {
		limit = 20
	}
	err := r.db.WithContext(ctx).
		Where("status IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", []string{EmailStatusPending, EmailStatusRetryWait}, now).
		Order("created_at ASC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *Repository) ClaimEmailDelivery(ctx context.Context, id uint) (bool, error) {
	result := r.db.WithContext(ctx).Model(&EmailDelivery{}).Where("id = ? AND status IN ?", id, []string{EmailStatusPending, EmailStatusRetryWait}).Updates(map[string]any{"status": EmailStatusSending, "last_error": ""})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) CompleteEmailDelivery(ctx context.Context, id uint, attempts int, sentAt time.Time) error {
	return r.db.WithContext(ctx).Model(&EmailDelivery{}).Where("id = ?", id).Updates(map[string]any{"status": EmailStatusSent, "attempt_count": attempts, "next_attempt_at": nil, "sent_at": sentAt, "last_error": ""}).Error
}

func (r *Repository) FailEmailDelivery(ctx context.Context, id uint, attempts int, next *time.Time, lastError string) error {
	status := EmailStatusFailed
	if next != nil {
		status = EmailStatusRetryWait
	}
	return r.db.WithContext(ctx).Model(&EmailDelivery{}).Where("id = ?", id).Updates(map[string]any{"status": status, "attempt_count": attempts, "next_attempt_at": next, "last_error": lastError}).Error
}

func (r *Repository) RecoverStaleEmailDeliveries(ctx context.Context, staleBefore, now time.Time) error {
	return r.db.WithContext(ctx).Model(&EmailDelivery{}).Where("status = ? AND updated_at <= ?", EmailStatusSending, staleBefore).Updates(map[string]any{"status": EmailStatusRetryWait, "next_attempt_at": now, "last_error": "上次发送进程中断，已恢复重试"}).Error
}

func (r *Repository) CancelPendingEmailDeliveries(ctx context.Context) error {
	return r.db.WithContext(ctx).Model(&EmailDelivery{}).Where("status IN ?", []string{EmailStatusPending, EmailStatusRetryWait}).Updates(map[string]any{"status": EmailStatusCancelled, "next_attempt_at": nil, "last_error": "邮件开关已关闭"}).Error
}

func (r *Repository) RecordEmailAttempt(ctx context.Context, delivery EmailDelivery, status, errorMessage string, at time.Time) error {
	generated := at
	return r.db.WithContext(ctx).Create(&models.EmailSendLog{
		SendType: "research2_report", TriggeredAt: at, Status: status,
		Recipients: delivery.Recipients, Subject: delivery.Subject, ErrorMessage: errorMessage,
		ReportCreatedAt: &generated, ExtraSummary: "analysisRunId=" + delivery.AnalysisRunID,
	}).Error
}
func (r *Repository) ListRecommendations(ctx context.Context, limit, offset int) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Order("signal_at DESC, final_score DESC, stock_code ASC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
	for index := range items {
		enrichLiveRecommendation(&items[index])
	}
	return items, err
}
func (r *Repository) GetRecommendation(ctx context.Context, id string) (RecommendationDetail, error) {
	var result RecommendationDetail
	if err := r.db.WithContext(ctx).Where("recommendation_id = ?", id).First(&result.Recommendation).Error; err != nil {
		return result, err
	}
	if err := r.db.WithContext(ctx).Where("run_id = ?", result.Recommendation.AnalysisRunID).First(&result.Analysis).Error; err != nil {
		return result, err
	}
	err := r.db.WithContext(ctx).Where("recommendation_id = ?", id).Order("traded_at ASC").Find(&result.Trades).Error
	enrichLiveRecommendation(&result.Recommendation)
	return result, err
}

func (r *Repository) DueRecommendations(ctx context.Context, now time.Time, statuses []string) ([]Recommendation, error) {
	var items []Recommendation
	query := r.db.WithContext(ctx).
		Model(&Recommendation{}).
		Select("research2_recommendations.*").
		Joins("JOIN research2_analysis_runs ON research2_analysis_runs.run_id = research2_recommendations.analysis_run_id AND research2_analysis_runs.status = ?", "success").
		Where("research2_recommendations.status IN ?", statuses)
	buyStatuses := len(statuses) > 0
	for _, status := range statuses {
		if status != "buy_pending" && status != "standby" {
			buyStatuses = false
			break
		}
	}
	if buyStatuses {
		query = query.Where("research2_recommendations.target_buy_at <= ?", now)
	} else {
		query = query.Where("research2_recommendations.target_sell_at IS NOT NULL AND research2_recommendations.target_sell_at <= ?", now)
	}
	err := query.Order("research2_recommendations.analysis_run_id ASC, CASE WHEN research2_recommendations.selection_rank > 0 THEN research2_recommendations.selection_rank ELSE 999999 END ASC, research2_recommendations.final_score DESC, research2_recommendations.stock_code ASC, research2_recommendations.id ASC").Find(&items).Error
	return items, err
}

func (r *Repository) DeferDueBuys(ctx context.Context, dueBefore, target time.Time) error {
	if !target.After(dueBefore) {
		return errors.New("research2 deferred buy target must be after the current due time")
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&Recommendation{}).
			Where("status = ? AND target_buy_at <= ?", "buy_pending", dueBefore).
			Update("target_buy_at", target).Error
	})
}

func (r *Repository) RecordBuy(ctx context.Context, recommendationID string, trade Trade, sellAt time.Time) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		var recommendation Recommendation
		if err := tx.Where("recommendation_id = ? AND status = ?", recommendationID, "buy_pending").First(&recommendation).Error; err != nil {
			return err
		}
		tradeDay := trade.TradedAt.In(shanghai())
		dayStart := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 0, 0, 0, 0, shanghai())
		var dailyBuys int64
		if err := tx.Model(&Recommendation{}).Where("buy_at >= ? AND buy_at < ?", dayStart, dayStart.AddDate(0, 0, 1)).Count(&dailyBuys).Error; err != nil {
			return err
		}
		if dailyBuys >= DailyTargetSlots {
			return ErrDailyBuyLimitReached
		}
		var run AnalysisRun
		if err := tx.Where("run_id = ?", recommendation.AnalysisRunID).First(&run).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var chain ExecutionChain
		if strings.TrimSpace(run.ChainID) != "" {
			if err := tx.Exec("UPDATE research2_execution_chains SET filled_slots = filled_slots WHERE chain_id = ?", run.ChainID).Error; err != nil {
				return err
			}
			if err := tx.Where("chain_id = ?", run.ChainID).First(&chain).Error; err != nil {
				return err
			}
			if chain.Status != "running" || chain.FilledSlots >= chain.TargetSlots {
				return ErrExecutionChainClosed
			}
		}
		var account Account
		if err := tx.First(&account, 1).Error; err != nil {
			return err
		}
		cost := -trade.NetCashFlow
		if cost <= 0 || account.Cash+1e-7 < cost {
			return errors.New("research2 cash is insufficient")
		}
		result := tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status = ?", recommendationID, "buy_pending").Updates(map[string]any{
			"status": "active", "buy_at": trade.TradedAt, "buy_market_price": trade.MarketPrice, "buy_price": trade.ExecutionPrice,
			"quantity": trade.Quantity, "buy_fees": trade.Commission + trade.TransferFee, "current_price": trade.MarketPrice,
			"current_price_at": trade.TradedAt, "target_sell_at": sellAt, "failure_reason": "",
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("research2 buy is no longer pending")
		}
		if err := tx.Create(&trade).Error; err != nil {
			return err
		}
		if err := tx.Model(&account).Update("cash", account.Cash-cost).Error; err != nil {
			return err
		}
		if strings.TrimSpace(run.ChainID) != "" {
			newFilledSlots := int(dailyBuys) + 1
			updates := map[string]any{"filled_slots": newFilledSlots}
			if newFilledSlots >= chain.TargetSlots {
				now := trade.TradedAt
				updates["status"], updates["stop_reason"], updates["completed_at"] = "completed", "已完成当日三笔买入", now
			}
			if err := tx.Model(&ExecutionChain{}).Where("chain_id = ? AND filled_slots = ?", run.ChainID, chain.FilledSlots).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) RecordSell(ctx context.Context, recommendationID string, trade Trade) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		var item Recommendation
		if err := tx.Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"active", "sell_pending"}).First(&item).Error; err != nil {
			return err
		}
		var account Account
		if err := tx.First(&account, 1).Error; err != nil {
			return err
		}
		buyCost := item.BuyPrice*float64(item.Quantity) + item.BuyFees
		netPnL := trade.NetCashFlow - buyCost
		netRate := 0.0
		if buyCost > 0 {
			netRate = netPnL / buyCost
		}
		result := tx.Model(&item).Updates(map[string]any{"status": "closed", "sell_at": trade.TradedAt, "sell_market_price": trade.MarketPrice, "sell_price": trade.ExecutionPrice, "sell_fees": trade.Commission + trade.StampDuty + trade.TransferFee, "current_price": trade.MarketPrice, "current_price_at": trade.TradedAt, "net_pn_l": netPnL, "net_yield_rate": netRate, "failure_reason": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("research2 position is no longer active")
		}
		if err := tx.Create(&trade).Error; err != nil {
			return err
		}
		return tx.Model(&account).Update("cash", account.Cash+trade.NetCashFlow).Error
	})
}

func (r *Repository) MarkStatus(ctx context.Context, id, status, reason string) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&Recommendation{}).Where("recommendation_id = ?", id).Updates(map[string]any{"status": status, "failure_reason": reason}).Error
	})
}

func (r *Repository) ActiveAndPending(ctx context.Context) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Where("status IN ?", []string{"active", "sell_pending", "buy_pending"}).Find(&items).Error
	return items, err
}

func (r *Repository) ActiveRecommendations(ctx context.Context) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Where("status IN ?", []string{"active", "sell_pending"}).Order("stock_code ASC, id ASC").Find(&items).Error
	return items, err
}

func (r *Repository) UpdateCurrentQuote(ctx context.Context, recommendationID string, price float64, at time.Time) error {
	if price <= 0 {
		return errors.New("research2 current quote price must be positive")
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&Recommendation{}).
			Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"active", "sell_pending"}).
			Updates(map[string]any{"current_price": price, "current_price_at": at}).Error
	})
}

func (r *Repository) Overview(ctx context.Context) (AccountOverview, error) {
	var account Account
	if err := r.db.WithContext(ctx).First(&account, 1).Error; err != nil {
		return AccountOverview{}, err
	}
	var active []Recommendation
	if err := r.db.WithContext(ctx).Where("status IN ?", []string{"active", "sell_pending"}).Find(&active).Error; err != nil {
		return AccountOverview{}, err
	}
	var pending int64
	if err := r.db.WithContext(ctx).Model(&Recommendation{}).Where("status = ?", "buy_pending").Count(&pending).Error; err != nil {
		return AccountOverview{}, err
	}
	positionValue := 0.0
	for _, item := range active {
		positionValue += livePositionValue(item)
	}
	nav := account.Cash + positionValue
	returnRate := 0.0
	if account.InitialCash > 0 {
		returnRate = (nav - account.InitialCash) / account.InitialCash
	}
	return AccountOverview{InitialCash: account.InitialCash, Cash: account.Cash, PositionValue: positionValue, NetAssetValue: nav, NetProfit: nav - account.InitialCash, ReturnRate: returnRate, OpenPositions: int64(len(active)), PendingBuys: pending, LastValuedAt: time.Now()}, nil
}

func (r *Repository) SaveSnapshot(ctx context.Context, kind string, at time.Time) (AccountSnapshot, error) {
	overview, err := r.Overview(ctx)
	if err != nil {
		return AccountSnapshot{}, err
	}
	item := AccountSnapshot{SnapshotID: uuid.NewString(), ValuedAt: at, TradingDate: at.In(shanghai()).Format("2006-01-02"), SnapshotType: kind, Cash: overview.Cash, PositionValue: overview.PositionValue, NetAssetValue: overview.NetAssetValue, NetProfit: overview.NetProfit, ReturnRate: overview.ReturnRate}
	err = research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		item.ID = 0
		return tx.Create(&item).Error
	})
	return item, err
}

func (r *Repository) Performance(ctx context.Context) (Performance, error) {
	overview, err := r.Overview(ctx)
	if err != nil {
		return Performance{}, err
	}
	result := Performance{AccountOverview: overview}
	base := r.db.WithContext(ctx).Model(&Recommendation{}).Where("status = ?", "closed")
	if err = base.Count(&result.ClosedTrades).Error; err != nil {
		return result, err
	}
	if err = r.db.WithContext(ctx).Model(&Recommendation{}).Where("status = ? AND net_pn_l > 0", "closed").Count(&result.WinningTrades).Error; err != nil {
		return result, err
	}
	if result.ClosedTrades > 0 {
		value := float64(result.WinningTrades) / float64(result.ClosedTrades)
		result.WinRate = &value
	}
	if err = r.db.WithContext(ctx).Model(&Trade{}).Select("COALESCE(SUM(commission + stamp_duty + transfer_fee), 0)").Scan(&result.TotalFees).Error; err != nil {
		return result, err
	}
	_ = r.db.WithContext(ctx).Model(&Recommendation{}).Where("hit_five_before_sell = ?", true).Count(&result.HitFiveCount).Error
	_ = r.db.WithContext(ctx).Model(&Recommendation{}).Where("hit_limit_up_full_day = ?", true).Count(&result.HitLimitUpCount).Error
	_ = r.db.WithContext(ctx).Model(&Recommendation{}).Where("hit_minus_three = ?", true).Count(&result.HitMinusThreeCount).Error
	_ = r.db.WithContext(ctx).Model(&AnalysisRun{}).Where("on_time = ? AND status IN ?", true, []string{"success", "no_recommendation"}).Count(&result.OnTimeReports).Error
	_ = r.db.WithContext(ctx).Model(&AnalysisRun{}).Where("on_time = ? AND status IN ?", false, []string{"success", "no_recommendation"}).Count(&result.LateReports).Error
	if err = r.db.WithContext(ctx).Order("valued_at ASC").Limit(500).Find(&result.Curve).Error; err != nil {
		return result, err
	}
	result.Curve = append(result.Curve, AccountSnapshot{ValuedAt: overview.LastValuedAt, TradingDate: overview.LastValuedAt.In(shanghai()).Format("2006-01-02"), SnapshotType: "current", Cash: overview.Cash, PositionValue: overview.PositionValue, NetAssetValue: overview.NetAssetValue, NetProfit: overview.NetProfit, ReturnRate: overview.ReturnRate})
	peak, maxDrawdown := 0.0, 0.0
	for _, point := range result.Curve {
		if point.NetAssetValue > peak {
			peak = point.NetAssetValue
		}
		if peak > 0 {
			drawdown := (peak - point.NetAssetValue) / peak
			if drawdown > maxDrawdown {
				maxDrawdown = drawdown
			}
		}
	}
	if len(result.Curve) > 0 {
		result.MaxDrawdown = &maxDrawdown
	}
	return result, nil
}

func enrichLiveRecommendation(item *Recommendation) {
	if item == nil || (item.Status != "active" && item.Status != "sell_pending") || item.Quantity <= 0 {
		return
	}
	price := livePrice(*item)
	if price <= 0 {
		return
	}
	item.CurrentPrice = price
	buyCost := item.BuyPrice*float64(item.Quantity) + item.BuyFees
	item.NetPnL = research.CalculateSellCost(price, item.Quantity).NetCashFlow - buyCost
	item.NetYieldRate = 0
	if buyCost > 0 {
		item.NetYieldRate = item.NetPnL / buyCost
	}
}

func livePositionValue(item Recommendation) float64 {
	price := livePrice(item)
	if price <= 0 || item.Quantity <= 0 {
		return 0
	}
	return research.CalculateSellCost(price, item.Quantity).NetCashFlow
}

func livePrice(item Recommendation) float64 {
	if item.CurrentPrice > 0 {
		return item.CurrentPrice
	}
	if item.BuyMarketPrice > 0 {
		return item.BuyMarketPrice
	}
	return item.BuyPrice
}

func (r *Repository) UnfinalizedMetrics(ctx context.Context) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Where("status = ? AND metrics_finalized = ?", "closed", false).Find(&items).Error
	return items, err
}

func (r *Repository) FinalizeMetrics(ctx context.Context, id string, five, limitUp, minusThree bool) error {
	return r.db.WithContext(ctx).Model(&Recommendation{}).Where("recommendation_id = ?", id).Updates(map[string]any{"hit_five_before_sell": five, "hit_limit_up_full_day": limitUp, "hit_minus_three": minusThree, "metrics_finalized": true}).Error
}

func shanghai() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}
func roundMoney(value float64) float64 { return math.Round(value*100) / 100 }
