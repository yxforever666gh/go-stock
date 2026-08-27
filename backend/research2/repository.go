package research2

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(database *gorm.DB) *Repository { return &Repository{db: database} }
func (r *Repository) DB() *gorm.DB                { return r.db }

func (r *Repository) EnsureAccount(ctx context.Context) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&Account{ID: 1, InitialCash: InitialCash, Cash: InitialCash}).Error
}

func (r *Repository) CreateRun(ctx context.Context, run *AnalysisRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}
func (r *Repository) SaveRun(ctx context.Context, run *AnalysisRun) error {
	return r.db.WithContext(ctx).Save(run).Error
}
func (r *Repository) RunForDate(ctx context.Context, tradingDate string) (AnalysisRun, bool, error) {
	var item AnalysisRun
	err := r.db.WithContext(ctx).Where("trading_date = ?", tradingDate).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AnalysisRun{}, false, nil
	}
	return item, err == nil, err
}
func (r *Repository) CreateRecommendations(ctx context.Context, items []Recommendation) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}
func (r *Repository) ListRuns(ctx context.Context, limit, offset int) ([]AnalysisRunSummary, error) {
	var rows []AnalysisRun
	err := r.db.WithContext(ctx).Order("scheduled_for DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error
	items := make([]AnalysisRunSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, AnalysisRunSummary{RunID: row.RunID, TradingDate: row.TradingDate, ScheduledFor: row.ScheduledFor, EvidenceCutoffAt: row.EvidenceCutoffAt, GeneratedAt: row.GeneratedAt, Status: row.Status, ProviderName: row.ProviderName, ModelName: row.ModelName, RecommendationCount: row.RecommendationCount, OnTime: row.OnTime, FailureReason: row.FailureReason})
	}
	return items, err
}
func (r *Repository) GetRun(ctx context.Context, id string) (AnalysisRun, error) {
	var item AnalysisRun
	err := r.db.WithContext(ctx).Where("run_id = ?", id).First(&item).Error
	return item, err
}
func (r *Repository) ListRecommendations(ctx context.Context, limit, offset int) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Order("signal_at DESC, rank ASC, id DESC").Limit(limit).Offset(offset).Find(&items).Error
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
	return result, err
}

func (r *Repository) DueRecommendations(ctx context.Context, now time.Time, statuses []string) ([]Recommendation, error) {
	var items []Recommendation
	query := r.db.WithContext(ctx).Where("status IN ?", statuses)
	if len(statuses) == 1 && statuses[0] == "buy_pending" {
		query = query.Where("target_buy_at <= ?", now)
	} else {
		query = query.Where("target_sell_at IS NOT NULL AND target_sell_at <= ?", now)
	}
	err := query.Order("analysis_run_id ASC, rank ASC").Find(&items).Error
	return items, err
}

func (r *Repository) RecordBuy(ctx context.Context, recommendationID string, trade Trade, sellAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
			return err
		}
		cost := -trade.NetCashFlow
		if cost <= 0 || account.Cash+1e-7 < cost {
			return errors.New("research2 cash is insufficient")
		}
		result := tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status = ?", recommendationID, "buy_pending").Updates(map[string]any{
			"status": "active", "buy_at": trade.TradedAt, "buy_market_price": trade.MarketPrice, "buy_price": trade.ExecutionPrice,
			"quantity": trade.Quantity, "buy_fees": trade.Commission + trade.TransferFee, "target_sell_at": sellAt, "failure_reason": "",
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
		return tx.Model(&account).Update("cash", account.Cash-cost).Error
	})
}

func (r *Repository) RecordSell(ctx context.Context, recommendationID string, trade Trade) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item Recommendation
		if err := tx.Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"active", "sell_pending"}).First(&item).Error; err != nil {
			return err
		}
		var account Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, 1).Error; err != nil {
			return err
		}
		buyCost := item.BuyPrice*float64(item.Quantity) + item.BuyFees
		netPnL := trade.NetCashFlow - buyCost
		netRate := 0.0
		if buyCost > 0 {
			netRate = netPnL / buyCost
		}
		result := tx.Model(&item).Updates(map[string]any{"status": "closed", "sell_at": trade.TradedAt, "sell_market_price": trade.MarketPrice, "sell_price": trade.ExecutionPrice, "sell_fees": trade.Commission + trade.StampDuty + trade.TransferFee, "net_pn_l": netPnL, "net_yield_rate": netRate, "failure_reason": ""})
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
	return r.db.WithContext(ctx).Model(&Recommendation{}).Where("recommendation_id = ?", id).Updates(map[string]any{"status": status, "failure_reason": reason}).Error
}

func (r *Repository) ActiveAndPending(ctx context.Context) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Where("status IN ?", []string{"active", "sell_pending", "buy_pending"}).Find(&items).Error
	return items, err
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
		positionValue += item.BuyPrice * float64(item.Quantity)
	}
	nav := account.Cash + positionValue
	return AccountOverview{InitialCash: account.InitialCash, Cash: account.Cash, PositionValue: positionValue, NetAssetValue: nav, NetProfit: nav - account.InitialCash, ReturnRate: (nav - account.InitialCash) / account.InitialCash, OpenPositions: int64(len(active)), PendingBuys: pending, LastValuedAt: time.Now()}, nil
}

func (r *Repository) SaveSnapshot(ctx context.Context, kind string, at time.Time) (AccountSnapshot, error) {
	overview, err := r.Overview(ctx)
	if err != nil {
		return AccountSnapshot{}, err
	}
	item := AccountSnapshot{SnapshotID: uuid.NewString(), ValuedAt: at, TradingDate: at.In(shanghai()).Format("2006-01-02"), SnapshotType: kind, Cash: overview.Cash, PositionValue: overview.PositionValue, NetAssetValue: overview.NetAssetValue, NetProfit: overview.NetProfit, ReturnRate: overview.ReturnRate}
	err = r.db.WithContext(ctx).Create(&item).Error
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
