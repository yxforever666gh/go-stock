package research

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go-stock/internal/marketquote"
	"go-stock/internal/trading"
	"gorm.io/gorm"
)

var ErrFrozen = fmt.Errorf("%w: 研究中心1已清仓冻结，暂停研究及交易", trading.ErrNewPositionsDisabled)

const FreezeReason = "用户要求暂停研究中心1，全部清仓并冻结"

func (r *Repository) Frozen(ctx context.Context) (bool, error) {
	return accountFrozen(r.db.WithContext(ctx))
}

func accountFrozen(db *gorm.DB) (bool, error) {
	var account SimulatedAccount
	err := db.Select("id", "frozen").First(&account, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return account.Frozen, err
}

func (r *Repository) CheckResearchAllowed(ctx context.Context) error {
	return checkAccountUnfrozen(r.db.WithContext(ctx))
}

func checkAccountUnfrozen(db *gorm.DB) error {
	frozen, err := accountFrozen(db)
	if err != nil {
		return err
	}
	if frozen {
		return ErrFrozen
	}
	return nil
}

// FreezeAndLiquidate is used during stopped-service schema maintenance. The
// whole liquidation, fees, pending-work retirement and freeze flag commit once.
// It does not enqueue sell-triggered research and never calls a market provider.
func (r *Repository) FreezeAndLiquidate(ctx context.Context, at time.Time) error {
	return transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockAccountForWrite(tx); err != nil {
			return err
		}
		var account SimulatedAccount
		if err := tx.First(&account, 1).Error; err != nil {
			return err
		}
		if account.Frozen {
			return nil
		}
		var positions []Position
		if err := tx.Where("status = ?", "open").Order("id").Find(&positions).Error; err != nil {
			return err
		}
		for _, position := range positions {
			price, source := position.CurrentPrice, "最近保存的有效行情"
			quotedAt := position.CurrentPriceAt
			if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
				price = position.EntryPrice
				source = "原始买入成交价"
				quotedAt = &position.EntryAt
			}
			if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) || position.Quantity <= 0 {
				return fmt.Errorf("持仓%s缺少有效清仓价格或数量", position.RecommendationID)
			}
			quote := marketquote.Quote{Code: position.StockCode, Price: price, At: at}
			if _, err := sellInTransaction(tx, position.RecommendationID, quote); err != nil {
				return err
			}
			event := DecisionEvent{EventID: newID(), RecommendationID: position.RecommendationID, DecisionType: "冻结清仓", DecidedAt: at, Reason: FreezeReason + "；模拟卖出价格来源：" + source, QuotePrice: price, QuoteAt: quotedAt, DecisionPolicyVersion: CurrentDecisionPolicyVersion}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&Recommendation{}).Where("status IN ?", []string{"pending", "buy_pending"}).Updates(map[string]any{"status": "missed_window", "reserved_cash": 0, "next_check_at": nil, "last_decision": "冻结取消", "last_decision_at": at}).Error; err != nil {
			return err
		}
		if err := tx.Model(&AnalysisTrigger{}).Where("status IN ?", []string{"queued", "running"}).Updates(map[string]any{"status": "failed", "completed_at": at, "lease_owner": "", "lease_expires_at": nil, "last_error": FreezeReason}).Error; err != nil {
			return err
		}
		if err := tx.Model(&AnalysisRun{}).Where("status IN ?", []string{"queued", "running"}).Updates(map[string]any{"status": "failed", "completed_at": at, "lease_owner": "", "lease_expires_at": nil, "failure_reason": FreezeReason}).Error; err != nil {
			return err
		}
		if err := tx.Model(&BuyOpportunity{}).Where("status = ?", "active").Updates(map[string]any{"status": "expired", "expires_at": at, "timing_reason": FreezeReason}).Error; err != nil {
			return err
		}
		if err := tx.Model(&SimulatedAccount{}).Where("id = ?", 1).Updates(map[string]any{"frozen": true, "frozen_at": at, "frozen_reason": FreezeReason}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&AccountValuationSnapshot{}) {
			var final SimulatedAccount
			if err := tx.First(&final, 1).Error; err != nil {
				return err
			}
			contribution, units, err := fundingLedger(tx)
			if err != nil {
				return err
			}
			snapshot := newAccountSnapshot("frozen", ShanghaiTime(at).Format("2006-01-02"), at, final.Cash, 0, final.Cash, contribution, safeUnitValue(final.Cash, units), "frozen", "research1-freeze-4.0.1")
			if err := tx.Create(&snapshot).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
