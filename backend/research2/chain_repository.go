package research2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DailyTargetSlots = 5

func deterministicExecutionChainID(tradingDate string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock:research2:execution-chain:"+strings.TrimSpace(tradingDate))).String()
}

func (r *Repository) EnsureExecutionChain(ctx context.Context, tradingDate string, scheduledFor, startedAt time.Time) (ExecutionChain, error) {
	var chain ExecutionChain
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		var err error
		chain, err = ensureExecutionChain(tx, r.accountSlot(), tradingDate, scheduledFor, startedAt)
		return err
	})
	return chain, err
}

func ensureExecutionChain(tx *gorm.DB, slot, tradingDate string, scheduledFor, startedAt time.Time) (ExecutionChain, error) {
	if tradingDate == "" || !ValidSlot(slot) {
		return ExecutionChain{}, errors.New("invalid research2 account day")
	}
	var bought int64
	day, err := time.ParseInLocation("2006-01-02", tradingDate, shanghai())
	if err != nil {
		return ExecutionChain{}, err
	}
	if err := tx.Model(&Recommendation{}).Where("slot = ? AND buy_at >= ? AND buy_at < ?", slot, day, day.AddDate(0, 0, 1)).Count(&bought).Error; err != nil {
		return ExecutionChain{}, err
	}
	chain := ExecutionChain{FilledSlots: int(bought), ChainID: deterministicExecutionChainID(tradingDate + ":" + slot), Slot: slot, TradingDate: tradingDate, ScheduledFor: scheduledFor, Status: "running", TargetSlots: DailyTargetSlots, StartedAt: startedAt, AllocationBaseCash: pendingAllocationBaseCash()}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "trading_date"}, {Name: "slot"}}, DoNothing: true}).Create(&chain).Error; err != nil {
		return chain, err
	}
	err = tx.Where("trading_date = ? AND slot = ?", tradingDate, slot).First(&chain).Error
	return chain, err
}

func (r *Repository) ExecutionChain(ctx context.Context, chainID string) (ExecutionChain, error) {
	var chain ExecutionChain
	err := r.db.WithContext(ctx).Where("chain_id = ?", chainID).First(&chain).Error
	return chain, err
}

func (r *Repository) ExecutionChainForDate(ctx context.Context, tradingDate string) (ExecutionChain, bool, error) {
	var chain ExecutionChain
	err := r.accountQuery(ctx).Where("trading_date = ?", tradingDate).First(&chain).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ExecutionChain{}, false, nil
	}
	return chain, err == nil, err
}

func (r *Repository) AttachRunToExecutionChain(ctx context.Context, chainID, runID string) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		var chain ExecutionChain
		if err := tx.Where("chain_id = ?", chainID).First(&chain).Error; err != nil {
			return err
		}
		updates := map[string]any{"latest_run_id": runID, "status": "running", "stop_reason": "", "completed_at": nil}
		if strings.TrimSpace(chain.RootRunID) == "" {
			updates["root_run_id"] = runID
		}
		return tx.Model(&chain).Where("status IN ?", []string{"running", "failed"}).Updates(updates).Error
	})
}

func (r *Repository) RefreshExecutionChainFilled(ctx context.Context, chainID string) (ExecutionChain, error) {
	var chain ExecutionChain
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		if err := tx.Where("chain_id = ?", chainID).First(&chain).Error; err != nil {
			return err
		}
		dayStart, err := time.ParseInLocation("2006-01-02", chain.TradingDate, shanghai())
		if err != nil {
			return err
		}
		var filled int64
		if err := tx.Model(&Recommendation{}).Where("slot = ? AND buy_at >= ? AND buy_at < ?", chain.Slot, dayStart, dayStart.AddDate(0, 0, 1)).Count(&filled).Error; err != nil {
			return err
		}
		chain.FilledSlots = int(filled)
		if chain.FilledSlots >= chain.TargetSlots && chain.Status == "running" {
			now := time.Now().In(shanghai())
			chain.Status, chain.StopReason, chain.CompletedAt = "completed", "已完成本区间当日五笔买入", &now
		}
		if chain.Status == "running" {
			var pending, reports int64
			if err := tx.Model(&Recommendation{}).Where("analysis_run_id IN (?) AND status IN ?",
				tx.Model(&AnalysisRun{}).Select("run_id").Where("chain_id = ?", chain.ChainID), []string{"buy_pending", "standby"}).Count(&pending).Error; err != nil {
				return err
			}
			if err := tx.Model(&AnalysisRun{}).Where("chain_id = ? AND published = ? AND status IN ?", chain.ChainID, true, []string{"success", "no_recommendation"}).Count(&reports).Error; err != nil {
				return err
			}
			if reports > 0 && pending == 0 {
				now := time.Now().In(shanghai())
				chain.Status, chain.StopReason, chain.CompletedAt = "completed", "当日有效报告的执行名单已处理完毕", &now
			}
		}

		return tx.Model(&chain).Updates(map[string]any{
			"filled_slots": chain.FilledSlots, "status": chain.Status,
			"stop_reason": chain.StopReason, "completed_at": chain.CompletedAt,
		}).Error
	})
	return chain, err
}

func (r *Repository) CompleteExecutionChain(ctx context.Context, chainID, status, reason string, completedAt time.Time) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&ExecutionChain{}).Where("chain_id = ? AND status = ?", chainID, "running").Updates(map[string]any{
			"status": status, "stop_reason": reason, "completed_at": completedAt,
		}).Error
	})
}

func (r *Repository) ExpireExecutionChainsAtCutoff(ctx context.Context, now time.Time) error {
	local := now.In(shanghai())
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 11, 30, 0, 0, shanghai())
	if local.Before(cutoff) {
		return nil
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		// Also retire imported chainless pending rows; no lunchtime deferral.
		if err := tx.Model(&Recommendation{}).Where("status IN ? AND signal_at < ?", []string{"buy_pending", "standby"}, cutoff.AddDate(0, 0, 1)).Updates(map[string]any{"status": "analysis_only", "failure_reason": "上午11:30买入窗口已截止"}).Error; err != nil {
			return err
		}
		var chains []ExecutionChain
		if err := tx.Where("trading_date = ? AND status = ?", local.Format("2006-01-02"), "running").Find(&chains).Error; err != nil {
			return err
		}
		for _, chain := range chains {
			if err := tx.Model(&chain).Updates(map[string]any{"status": "cutoff", "stop_reason": "上午11:30买入窗口截止", "completed_at": local}).Error; err != nil {
				return err
			}
			if err := tx.Table("research2_recommendations").Where("analysis_run_id IN (?) AND status IN ?",
				tx.Model(&AnalysisRun{}).Select("run_id").Where("chain_id = ?", chain.ChainID), []string{"buy_pending", "standby"}).
				Updates(map[string]any{"status": "analysis_only", "failure_reason": "上午11:30买入窗口已截止，仅保留分析"}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) RecoverInterruptedRunsForDate(ctx context.Context, tradingDate string, recoveredAt time.Time) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&AnalysisRun{}).Where("trading_date = ? AND status = ?", tradingDate, "running").Updates(map[string]any{
			"status": "failed", "generated_at": recoveredAt,
			"failure_reason": "服务重启时发现上次分析未完成，已恢复为新的分析轮次",
		}).Error
	})
}

func (r *Repository) DisableRunningExecutionChains(ctx context.Context, tradingDate string, disabledAt time.Time) ([]ExecutionChain, error) {
	var stopped []ExecutionChain
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Model(&AnalysisRun{}).Where("trading_date = ? AND status = ?", tradingDate, "running").Update("archive_reason", "自动策略已关闭，仅保留报告").Error; err != nil {
			return err
		}

		if err := tx.Where("trading_date = ? AND status = ?", tradingDate, "running").Find(&stopped).Error; err != nil {
			return err
		}
		for index := range stopped {
			if err := tx.Model(&stopped[index]).Updates(map[string]any{"status": "disabled", "stop_reason": "研究中心2自动策略已关闭", "completed_at": disabledAt}).Error; err != nil {
				return err
			}
			stopped[index].Status, stopped[index].StopReason, stopped[index].CompletedAt = "disabled", "研究中心2自动策略已关闭", &disabledAt
			if err := tx.Table("research2_recommendations").Where("analysis_run_id IN (?) AND status IN ?",
				tx.Model(&AnalysisRun{}).Select("run_id").Where("chain_id = ?", stopped[index].ChainID), []string{"buy_pending", "standby"}).
				Updates(map[string]any{"status": "analysis_only", "failure_reason": "自动策略已关闭，仅保留分析"}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return stopped, err
}

func (r *Repository) ExpireStaleExecutionChains(ctx context.Context, currentTradingDate string, recoveredAt time.Time) ([]ExecutionChain, error) {
	var expired []ExecutionChain
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Where("status = ? AND trading_date < ?", "running", currentTradingDate).Order("trading_date ASC").Find(&expired).Error; err != nil {
			return err
		}
		for index := range expired {
			reason := "服务恢复时已跨过原交易日，旧补位链终止"
			if err := tx.Model(&expired[index]).Updates(map[string]any{"status": "cutoff", "stop_reason": reason, "completed_at": recoveredAt}).Error; err != nil {
				return err
			}
			expired[index].Status, expired[index].StopReason, expired[index].CompletedAt = "cutoff", reason, &recoveredAt
			if err := tx.Model(&AnalysisRun{}).Where("chain_id = ? AND status = ?", expired[index].ChainID, "running").Updates(map[string]any{"status": "failed", "generated_at": recoveredAt, "failure_reason": reason}).Error; err != nil {
				return err
			}
			if err := tx.Table("research2_recommendations").Where("analysis_run_id IN (?) AND status IN ?",
				tx.Model(&AnalysisRun{}).Select("run_id").Where("chain_id = ?", expired[index].ChainID), []string{"buy_pending", "standby"}).
				Updates(map[string]any{"status": "analysis_only", "failure_reason": reason}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return expired, err
}

func (r *Repository) RecordExecutionFailure(ctx context.Context, recommendationID, code, reason string, snapshot PriceSnapshot, limitPrice float64, distancePct *float64) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		updates := map[string]any{
			"status": "missed_untradable", "failure_reason": reason,
			"execution_failure_code": code, "execution_quote_price": snapshot.Price,
			"execution_limit_price": limitPrice,
		}
		if !snapshot.At.IsZero() {
			updates["execution_quote_at"] = snapshot.At
		}
		if distancePct != nil {
			updates["execution_limit_distance_pct"] = *distancePct
		}
		return tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"buy_pending", "standby"}).Updates(updates).Error
	})
}

func (r *Repository) RecordExecutionQuotePending(ctx context.Context, recommendationID, reason string, snapshot *PriceSnapshot) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		updates := map[string]any{"failure_reason": reason, "execution_failure_code": "quote_retry"}
		if snapshot != nil {
			updates["execution_quote_price"] = snapshot.Price
			if !snapshot.At.IsZero() {
				updates["execution_quote_at"] = snapshot.At
			}
		}
		return tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"buy_pending", "standby"}).Updates(updates).Error
	})
}

func (r *Repository) RecordExecutionQuote(ctx context.Context, recommendationID string, snapshot PriceSnapshot, limitPrice float64, distancePct *float64) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		updates := map[string]any{
			"execution_quote_price": snapshot.Price, "execution_limit_price": limitPrice,
			"execution_failure_code": "", "failure_reason": "",
		}
		if !snapshot.At.IsZero() {
			updates["execution_quote_at"] = snapshot.At
		}
		if distancePct != nil {
			updates["execution_limit_distance_pct"] = *distancePct
		}
		return tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status IN ?", recommendationID, []string{"buy_pending", "standby"}).Updates(updates).Error
	})
}

func (r *Repository) RunRecommendations(ctx context.Context, runID string) ([]Recommendation, error) {
	var items []Recommendation
	err := r.db.WithContext(ctx).Where("analysis_run_id = ?", runID).Order("selection_rank ASC, id ASC").Find(&items).Error
	return items, err
}

func (r *Repository) AnalysisRunByID(ctx context.Context, runID string) (AnalysisRun, error) {
	var run AnalysisRun
	err := r.db.WithContext(ctx).Where("run_id = ?", runID).First(&run).Error
	return run, err
}

func (r *Repository) ExecutionChainEmailRun(ctx context.Context, chainID string) (AnalysisRun, error) {
	chain, err := r.ExecutionChain(ctx, chainID)
	if err != nil {
		return AnalysisRun{}, err
	}
	var run AnalysisRun
	if strings.TrimSpace(chain.LatestRunID) != "" {
		run, err = r.AnalysisRunByID(ctx, chain.LatestRunID)
	}
	if strings.TrimSpace(chain.LatestRunID) == "" || errors.Is(err, gorm.ErrRecordNotFound) {
		err = r.db.WithContext(ctx).Where("chain_id = ?", chainID).Order("attempt_no DESC, id DESC").First(&run).Error
	}
	if err != nil {
		return AnalysisRun{}, err
	}
	var runs []AnalysisRun
	if err = r.db.WithContext(ctx).Where("chain_id = ?", chainID).Order("attempt_no ASC, id ASC").Find(&runs).Error; err != nil {
		return AnalysisRun{}, err
	}
	var recommendations []Recommendation
	if err = r.db.WithContext(ctx).Table("research2_recommendations AS recommendations").Select("recommendations.*").
		Joins("JOIN research2_analysis_runs AS runs ON runs.run_id = recommendations.analysis_run_id").
		Where("runs.chain_id = ?", chainID).
		Order("runs.attempt_no ASC, recommendations.selection_rank ASC, recommendations.id ASC").Find(&recommendations).Error; err != nil {
		return AnalysisRun{}, err
	}
	var summary strings.Builder
	summary.WriteString(strings.TrimSpace(run.ReportMarkdown))
	summary.WriteString("\n\n## 当日报告执行汇总\n\n")
	summary.WriteString(fmt.Sprintf("- 执行状态：%s\n- 分析轮次：%d\n- 目标买入：%d\n- 实际买入：%d\n- 剩余席位：%d\n", chain.Status, len(runs), chain.TargetSlots, chain.FilledSlots, max(0, chain.TargetSlots-chain.FilledSlots)))
	if strings.TrimSpace(chain.StopReason) != "" {
		summary.WriteString("- 结束原因：" + strings.TrimSpace(chain.StopReason) + "\n")
	}
	if len(recommendations) > 0 {
		summary.WriteString("\n### 全部候选与执行结果\n\n")
		for _, item := range recommendations {
			line := fmt.Sprintf("- 第%d次 #%d %s %s：%s", attemptForRun(runs, item.AnalysisRunID), item.SelectionRank, item.StockCode, item.StockName, item.Status)
			if strings.TrimSpace(item.ExecutionFailureCode) != "" {
				line += " / " + item.ExecutionFailureCode
			}
			if strings.TrimSpace(item.FailureReason) != "" {
				line += " / " + strings.TrimSpace(item.FailureReason)
			}
			summary.WriteString(line + "\n")
		}
	}
	run.ReportMarkdown = summary.String()
	run.RecommendationCount = chain.FilledSlots
	run.FailureReason = chain.StopReason
	return run, nil
}

func attemptForRun(runs []AnalysisRun, runID string) int {
	for _, run := range runs {
		if run.RunID == runID {
			return run.AttemptNo
		}
	}
	return 0
}
