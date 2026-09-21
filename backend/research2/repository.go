package research2

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/internal/sqlitedb"
	"go-stock/internal/trading"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	slot                   string
	now                    func() time.Time
	db                     *gorm.DB
	newPositionsPermission func(context.Context, *gorm.DB) error
}

var (
	errDuplicateBuy         = errors.New("research2 stock already bought or held")
	ErrDailyBuyLimitReached = errors.New("research2 daily buy limit is already reached")
	ErrExecutionChainClosed = errors.New("research2 daily execution target is already closed")
)

// SQLite ignores SELECT FOR UPDATE. Acquire the single-writer lock explicitly
// before reading the research2 cash balance so concurrent schedulers cannot
// race the read/modify/write transaction.
func lockResearch2AccountForWrite(tx *gorm.DB, slots ...string) error {
	slot := DefaultSlot
	if len(slots) > 0 {
		slot = normalizeSlot(slots[0])
	}
	result := tx.Exec("UPDATE research2_accounts SET cash = cash WHERE slot = ?", slot)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("research2 account is unavailable")
	}
	return nil
}

func research2TransactionWithWriteRetry(ctx context.Context, database *gorm.DB, operation func(*gorm.DB) error) error {
	return sqlitedb.Retry(ctx, func() error {
		return database.WithContext(ctx).Transaction(operation)
	}, nil)
}

func NewRepository(database *gorm.DB) *Repository { return &Repository{db: database, now: time.Now} }
func (r *Repository) DB() *gorm.DB                { return r.db }

// ConfigureNewPositionsPermission is called before publishing the runtime.
// The callback must read its setting from the supplied database/transaction.
func (r *Repository) ConfigureNewPositionsPermission(permission func(context.Context, *gorm.DB) error) {
	r.newPositionsPermission = permission
}

func (r *Repository) CheckNewPositionsAllowed(ctx context.Context) error {
	return r.checkNewPositionsAllowed(ctx, r.db.WithContext(ctx))
}

func (r *Repository) checkNewPositionsAllowed(ctx context.Context, database *gorm.DB) error {
	if r.newPositionsPermission == nil {
		return nil
	}
	return r.newPositionsPermission(ctx, database)
}

func (r *Repository) EnsureAccount(ctx context.Context) error {
	var count int64
	if err := r.db.WithContext(ctx).Model(&Account{}).Count(&count).Error; err != nil {
		return err
	}
	if count == 24 {
		return nil
	}

	for _, slot := range append([]string{DefaultSlot}, Slots()...) {
		account := Account{Slot: slot, InitialCash: InitialCash, Cash: InitialCash}
		if slot == DefaultSlot {
			account.ID = 1
		}
		if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error; err != nil {
			return err
		}
	}
	return nil
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
	run.ScheduledSlot = normalizeSlot(run.ScheduledSlot)
	var selected AnalysisRun
	created := false
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		selected = AnalysisRun{}
		created = false
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		// Serialize the daily quota across schedulers, manual retries and versions.
		var existing AnalysisRun
		err := tx.Where("trading_date = ? AND scheduled_slot = ? AND status IN ?", run.TradingDate, run.ScheduledSlot, []string{"success", "no_recommendation", "running"}).
			Order("CASE WHEN status = 'running' THEN 1 ELSE 0 END, attempt_no DESC, id DESC").First(&existing).Error
		if err == nil {
			if run.TriggerSource == "manual_rerun" {
				return ErrExecutionChainClosed
			}
			selected = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var latest AnalysisRun
		err = tx.Where("trading_date = ? AND scheduled_slot = ?", run.TradingDate, run.ScheduledSlot).Order("attempt_no DESC, id DESC").First(&latest).Error
		if run.TriggerSource == "manual_rerun" && (err != nil || latest.RunID != run.ParentRunID || latest.Status != "failed") {
			return ErrExecutionChainClosed
		}
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			run.AttemptNo = 1
		case err != nil:
			return err
		case !allowRetry || latest.Status != "failed":
			selected = latest
			return nil
		default:
			run.AttemptNo = latest.AttemptNo + 1
		}

		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "trading_date"}, {Name: "scheduled_slot"}, {Name: "attempt_no"}},
			DoNothing: true,
		}).Create(run)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			selected, created = *run, true
			return nil
		}
		return tx.Where("trading_date = ? AND scheduled_slot = ?", run.TradingDate, run.ScheduledSlot).Order("attempt_no DESC, id DESC").First(&selected).Error
	})
	return selected, created, err
}
func (r *Repository) SaveRun(ctx context.Context, run *AnalysisRun) error {
	if run == nil {
		return errors.New("research2 analysis run is required")
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Omit("archive_reason").Save(run).Error
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
// renderReport must be pure; it observes the final permission-adjusted items.
func (r *Repository) FinalizeRun(ctx context.Context, run *AnalysisRun, items []Recommendation, renderReport func() string) error {
	if run == nil {
		return errors.New("research2 analysis run is required")
	}
	return r.finalizeSlotRun(ctx, run, items, renderReport)
}

func (r *Repository) ListRuns(ctx context.Context, limit, offset int) ([]AnalysisRunSummary, error) {
	var rows []AnalysisRun
	query := r.db.WithContext(ctx)
	if r.slot != "" {
		query = query.Where("slot = ? OR (slot = ? AND scheduled_slot = ?)", r.slot, "", r.slot)
	}
	err := query.Order("scheduled_for DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error
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
		items = append(items, AnalysisRunSummary{ScheduledSlot: row.ScheduledSlot, Slot: row.Slot, Published: row.Published, ArchiveReason: row.ArchiveReason, PersistedAt: row.PersistedAt, RunID: row.RunID, TradingDate: row.TradingDate, AttemptNo: row.AttemptNo, ChainID: row.ChainID, ParentRunID: row.ParentRunID, TriggerSource: row.TriggerSource, RequestedSlots: row.RequestedSlots, PrimaryCount: row.PrimaryCount, StandbyCount: row.StandbyCount, ScheduledFor: row.ScheduledFor, StartedAt: row.StartedAt, EvidenceWindowStartAt: row.EvidenceWindowStartAt, EvidenceCutoffAt: row.EvidenceCutoffAt, EvidenceCoveragePct: row.EvidenceCoveragePct, Degraded: row.Degraded, GeneratedAt: row.GeneratedAt, Status: row.Status, ProviderName: row.ProviderName, ModelName: row.ModelName, StrategyVersion: row.StrategyVersion, EvidenceProfileVersion: row.EvidenceProfileVersion, EvidenceSetID: row.EvidenceSetID, RecommendationCount: row.RecommendationCount, OnTime: row.OnTime, FailureReason: row.FailureReason, EmailDeliveryStatus: delivery.Status, EmailSentAt: delivery.SentAt, EmailAttemptCount: delivery.AttemptCount, EmailLastError: delivery.LastError, ExecutionChain: chain})
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
	if limit < 0 {
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
	if limit <= 0 {
		limit = -1
	}
	query := dailySelectionQuery + ", displayed AS (SELECT * FROM ranked v WHERE " + dailySelectionVisible + " AND (? = '' OR v.slot = ?)" + dailySelectionOrder + " LIMIT ? OFFSET ?)" + dailySelectionProjection + dailySelectionOrder
	err := r.db.WithContext(ctx).Raw(query, DailyTargetSlots, r.slot, r.slot, limit, max(0, offset), DailyTargetSlots).Scan(&items).Error
	for index := range items {
		enrichLiveRecommendation(&items[index])
	}
	return items, err
}
func (r *Repository) GetRecommendation(ctx context.Context, id string) (RecommendationDetail, error) {
	var result RecommendationDetail
	query := r.db.WithContext(ctx).Raw(dailySelectionQuery+", displayed AS (SELECT * FROM ranked WHERE recommendation_id = ?)"+dailySelectionProjection,
		id, DailyTargetSlots).Scan(&result.Recommendation)
	if query.Error != nil {
		return result, query.Error
	}
	if query.RowsAffected == 0 {
		return result, gorm.ErrRecordNotFound
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
	err := query.Order("julianday(coalesce(research2_analysis_runs.generated_at, research2_analysis_runs.started_at)) ASC, research2_recommendations.final_score DESC, research2_recommendations.stock_code ASC, research2_recommendations.id ASC").Find(&items).Error
	return items, err
}

func (r *Repository) RecordBuy(ctx context.Context, recommendationID string, trade Trade, sellAt time.Time) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		if err := r.checkNewPositionsAllowed(ctx, tx); err != nil {
			return err
		}
		var recommendation Recommendation
		if err := tx.Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"buy_pending", "standby"}).First(&recommendation).Error; err != nil {
			return err
		}
		tradeDay := trade.TradedAt.In(shanghai())
		dayStart := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 0, 0, 0, 0, shanghai())
		var dailyBuys int64
		if err := tx.Model(&Recommendation{}).Where("slot = ? AND buy_at >= ? AND buy_at < ?", recommendation.Slot, dayStart, dayStart.AddDate(0, 0, 1)).Count(&dailyBuys).Error; err != nil {
			return err
		}
		if dailyBuys >= DailyTargetSlots {
			return ErrDailyBuyLimitReached
		}
		var duplicates int64
		if err := tx.Model(&Recommendation{}).Where("slot = ? AND stock_code = ? AND (status IN ? OR (buy_at >= ? AND buy_at < ?))", recommendation.Slot, recommendation.StockCode, []string{"active", "sell_pending"}, dayStart, dayStart.AddDate(0, 0, 1)).Count(&duplicates).Error; err != nil {
			return err
		}
		if duplicates > 0 {
			return errDuplicateBuy
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
			if chain.Status != "running" || chain.FilledSlots >= chain.TargetSlots || chain.SellCompletedAt == nil {
				return ErrExecutionChainClosed
			}
		}
		var account Account
		if err := tx.Where("slot = ?", recommendation.Slot).First(&account).Error; err != nil {
			return err
		}
		cost := -trade.NetCashFlow
		if cost <= 0 || account.Cash+1e-7 < cost {
			return trading.ErrInsufficientCash
		}
		result := tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"buy_pending", "standby"}).Updates(map[string]any{
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
		trade.Slot = recommendation.Slot
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
				updates["status"], updates["stop_reason"], updates["completed_at"] = "completed", "已完成本区间当日五笔买入", now
			}
			if err := tx.Model(&ExecutionChain{}).Where("chain_id = ? AND filled_slots = ?", run.ChainID, chain.FilledSlots).Updates(updates).Error; err != nil {
				return err
			}
		}
		return saveCapitalTradeSnapshot(tx, trade)
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
		if err := tx.Where("slot = ?", item.Slot).First(&account).Error; err != nil {
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
		trade.Slot = item.Slot
		if item.BaselineValue != nil {
			value := trade.NetCashFlow - *item.BaselineValue
			if err := tx.Model(&item).Update("period_pn_l", value).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&trade).Error; err != nil {
			return err
		}
		if err := tx.Model(&account).Update("cash", account.Cash+trade.NetCashFlow).Error; err != nil {
			return err
		}
		return saveCapitalTradeSnapshot(tx, trade)
	})
}

func (r *Repository) MarkStatus(ctx context.Context, id, status, reason string) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		query := tx.Model(&Recommendation{}).Where("recommendation_id = ?", id)
		if status == "analysis_only" {
			query = query.Where("status IN ? AND buy_at IS NULL", []string{"buy_pending", "standby"})
		}
		return query.Updates(map[string]any{"status": status, "failure_reason": reason}).Error
	})
}

func (r *Repository) ActiveAndPending(ctx context.Context) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Where("status IN ?", []string{"active", "sell_pending", "buy_pending"}).Find(&items).Error
	return items, err
}

func (r *Repository) ActiveRecommendations(ctx context.Context) ([]Recommendation, error) {
	var items []Recommendation
	err := r.accountQuery(ctx).Where("status IN ?", []string{"active", "sell_pending"}).Order("stock_code ASC, id ASC").Find(&items).Error
	return items, err
}

func (r *Repository) UpdateCurrentQuote(ctx context.Context, recommendationID string, price float64, at time.Time) error {
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) || at.IsZero() {
		return errors.New("research2 current quote requires a positive finite price and timestamp")
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&Recommendation{}).
			Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"active", "sell_pending"}).
			Where("current_price_at IS NULL OR current_price_at < ?", at).
			Updates(map[string]any{"current_price": price, "current_price_at": at}).Error
	})
}

func (r *Repository) Overview(ctx context.Context) (AccountOverview, error) {
	var account Account
	if err := r.accountQuery(ctx).First(&account).Error; err != nil {
		return AccountOverview{}, err
	}
	var active []Recommendation
	if err := r.accountQuery(ctx).Where("status IN ?", []string{"active", "sell_pending"}).Find(&active).Error; err != nil {
		return AccountOverview{}, err
	}
	var pending int64
	if err := r.accountQuery(ctx).Model(&Recommendation{}).Where("status = ?", "buy_pending").Count(&pending).Error; err != nil {
		return AccountOverview{}, err
	}
	positionValue := 0.0
	for _, item := range active {
		positionValue += livePositionValue(item)
	}
	nav := account.Cash + positionValue
	summary, err := research2CapitalSummary(ctx, r.db, r.accountSlot(), account.InitialCash)
	if err != nil {
		return AccountOverview{}, err
	}
	if !summary.ledger && account.BaselineAt != nil && account.BaselineNetAssetValue > 0 {
		summary.external = account.BaselineNetAssetValue
	}
	if err := validateCapitalSummary(summary); err != nil {
		return AccountOverview{}, err
	}
	basis := summary.external
	valuationBasis := CapitalValuationBasisLegacy
	if summary.ledger {
		valuationBasis = CapitalValuationBasisLedger
	}
	profit := summary.profit(nav)
	returnRate := summary.rate(nav)
	return AccountOverview{
		Slot: r.accountSlot(), BaselineAt: account.BaselineAt, BaselineNetAssetValue: basis,
		InitialCash: account.InitialCash, Cash: account.Cash, PositionValue: positionValue, NetAssetValue: nav,
		NetProfit: profit, ReturnRate: returnRate, OpenPositions: int64(len(active)), PendingBuys: pending, LastValuedAt: time.Now(),
		InitialContribution: summary.initial, TopUpContribution: summary.topUp, CumulativeExternalCapital: summary.external,
		NetInternalTransfer: summary.transfer, CumulativeCapitalReturn: returnRate, ValuationBasis: valuationBasis,
	}, nil
}

func (r *Repository) SaveSnapshot(ctx context.Context, kind string, at time.Time) (AccountSnapshot, error) {
	overview, err := r.Overview(ctx)
	if err != nil {
		return AccountSnapshot{}, err
	}
	item := AccountSnapshot{Slot: r.accountSlot(), SnapshotID: uuid.NewString(), ValuedAt: at, TradingDate: at.In(shanghai()).Format("2006-01-02"), SnapshotType: kind, Cash: overview.Cash, PositionValue: overview.PositionValue, NetAssetValue: overview.NetAssetValue, NetProfit: overview.NetProfit, ReturnRate: overview.ReturnRate}
	err = research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		item.ID = 0
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		if overview.ValuationBasis != CapitalValuationBasisLedger || !research2CapitalLedgerAvailable(tx) {
			return nil
		}
		ledger := newAccountLedgerSnapshot(r.accountSlot(), kind, at, overview)
		ledger.SnapshotID = "ledger-" + item.SnapshotID
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ledger).Error
	})
	return item, err
}

func (r *Repository) Performance(ctx context.Context) (Performance, error) {
	overview, err := r.Overview(ctx)
	if err != nil {
		return Performance{}, err
	}
	result := Performance{AccountOverview: overview}
	period := r.accountQuery(ctx)
	ledgerBacked := overview.ValuationBasis == CapitalValuationBasisLedger
	if !ledgerBacked && overview.BaselineAt != nil {
		period = period.Where("sell_at >= ?", *overview.BaselineAt)
	}
	trades := r.accountQuery(ctx)
	curve := r.accountQuery(ctx)
	reports := r.db.WithContext(ctx).Where("slot = ? AND published = ?", r.accountSlot(), true)
	if !ledgerBacked && overview.BaselineAt != nil {
		trades = trades.Where("traded_at >= ?", *overview.BaselineAt)
		curve = curve.Where("valued_at >= ?", *overview.BaselineAt)
		reports = reports.Where("persisted_at >= ?", *overview.BaselineAt)
	}
	base := period.Session(&gorm.Session{}).Model(&Recommendation{}).Where("status = ?", "closed")
	if err = base.Count(&result.ClosedTrades).Error; err != nil {
		return result, err
	}
	profitColumn := "coalesce(period_pn_l,net_pn_l)"
	if ledgerBacked {
		profitColumn = "net_pn_l"
	}
	if err = period.Session(&gorm.Session{}).Model(&Recommendation{}).Where("status = ? AND "+profitColumn+" > 0", "closed").Count(&result.WinningTrades).Error; err != nil {
		return result, err
	}
	if result.ClosedTrades > 0 {
		value := float64(result.WinningTrades) / float64(result.ClosedTrades)
		result.WinRate = &value
	}
	if err = trades.Model(&Trade{}).Select("COALESCE(SUM(commission + stamp_duty + transfer_fee), 0)").Scan(&result.TotalFees).Error; err != nil {
		return result, err
	}
	_ = period.Session(&gorm.Session{}).Model(&Recommendation{}).Where("hit_five_before_sell = ?", true).Count(&result.HitFiveCount).Error
	_ = period.Session(&gorm.Session{}).Model(&Recommendation{}).Where("hit_limit_up_full_day = ?", true).Count(&result.HitLimitUpCount).Error
	_ = period.Session(&gorm.Session{}).Model(&Recommendation{}).Where("hit_minus_three = ?", true).Count(&result.HitMinusThreeCount).Error
	_ = reports.Session(&gorm.Session{}).Model(&AnalysisRun{}).Where("on_time = ? AND status IN ?", true, []string{"success", "no_recommendation"}).Count(&result.OnTimeReports).Error
	_ = reports.Session(&gorm.Session{}).Model(&AnalysisRun{}).Where("on_time = ? AND status IN ?", false, []string{"success", "no_recommendation"}).Count(&result.LateReports).Error
	if ledgerBacked && research2CapitalLedgerAvailable(r.db) {
		if err = r.accountQuery(ctx).Where("snapshot_type IN ?", []string{"trade", CapitalEventInitial, CapitalEventTopUp, CapitalEventLegacyPoolTransfer}).Order("valued_at ASC, id ASC").Find(&result.Curve).Error; err != nil {
			return result, err
		}
	} else {
		var raw []AccountSnapshot
		if err = curve.Order("valued_at ASC, id ASC").Limit(500).Find(&raw).Error; err != nil {
			return result, err
		}
		result.Curve = make([]AccountLedgerSnapshot, 0, len(raw))
		for _, item := range raw {
			result.Curve = append(result.Curve, accountLedgerSnapshotFromRaw(item))
		}
	}
	current := newAccountLedgerSnapshot(r.accountSlot(), "current", overview.LastValuedAt, overview)
	current.SnapshotID = "current-" + r.accountSlot()
	result.Curve = append(result.Curve, current)
	for index := range result.Curve {
		result.Curve[index].ReturnRate = result.Curve[index].CumulativeCapitalReturn
	}
	if ledgerBacked {
		maxDrawdown := capitalEventDrawdown(result.Curve)
		result.MaxDrawdown = &maxDrawdown
		return result, nil
	}
	peak, maxDrawdown := 0.0, 0.0
	for _, point := range result.Curve {
		wealth := 1 + point.CumulativeCapitalReturn
		if wealth <= 0 {
			wealth = 1e-12
		}
		if wealth > peak {
			peak = wealth
		}
		if peak > 0 {
			drawdown := (peak - wealth) / peak
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
	item.NetPnL = trading.CalculateSellCost(price, item.Quantity).NetCashFlow - buyCost
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
	return trading.CalculateSellCost(price, item.Quantity).NetCashFlow
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
	return r.db.WithContext(ctx).Model(&Recommendation{}).Where("recommendation_id = ? AND metrics_finalized = ? AND status = ?", id, false, "closed").Updates(map[string]any{"hit_five_before_sell": five, "hit_limit_up_full_day": limitUp, "hit_minus_three": minusThree, "metrics_finalized": true}).Error
}

func shanghai() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}
func roundMoney(value float64) float64 { return math.Round(value*100) / 100 }

func (r *Repository) RemainingBuySlots(ctx context.Context, at time.Time) (int, error) {
	local := at.In(shanghai())
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghai())
	var count int64
	err := r.accountQuery(ctx).Model(&Recommendation{}).Where("buy_at >= ? AND buy_at < ?", start, start.AddDate(0, 0, 1)).Count(&count).Error
	return max(0, DailyTargetSlots-int(count)), err
}

func (r *Repository) FinishPendingBuy(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&Recommendation{}).Where("recommendation_id = ? AND status IN ?", id, []string{"buy_pending", "standby"}).Updates(map[string]any{"status": "analysis_only", "failure_reason": "本区间当日已完成五笔买入，剩余评分仅保留分析"}).Error
}
