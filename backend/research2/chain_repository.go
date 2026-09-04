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

const DailyTargetSlots = 3

var hardExecutionFailureCodes = []string{
	"limit_up",
	"near_limit_up",
	"limit_down",
	"suspended",
	"invalid_price",
}

func deterministicExecutionChainID(tradingDate string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock:research2:execution-chain:"+strings.TrimSpace(tradingDate))).String()
}

func (r *Repository) EnsureExecutionChain(ctx context.Context, tradingDate string, scheduledFor, startedAt time.Time) (ExecutionChain, error) {
	if strings.TrimSpace(tradingDate) == "" {
		return ExecutionChain{}, errors.New("research2 execution chain trading date is required")
	}
	dayStart, parseErr := time.ParseInLocation("2006-01-02", tradingDate, shanghai())
	if parseErr != nil {
		return ExecutionChain{}, fmt.Errorf("invalid research2 execution chain trading date: %w", parseErr)
	}
	chain := ExecutionChain{
		ChainID: deterministicExecutionChainID(tradingDate), TradingDate: tradingDate,
		ScheduledFor: scheduledFor, Status: "running", TargetSlots: DailyTargetSlots,
		StartedAt: startedAt,
	}
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		var existingBuys int64
		if err := tx.Model(&Recommendation{}).Where("buy_at >= ? AND buy_at < ?", dayStart, dayStart.AddDate(0, 0, 1)).Count(&existingBuys).Error; err != nil {
			return err
		}
		chain.FilledSlots = min(DailyTargetSlots, int(existingBuys))
		if chain.FilledSlots >= chain.TargetSlots {
			completed := startedAt
			chain.Status, chain.StopReason, chain.CompletedAt = "completed", "当日已有三笔买入，无需补位", &completed
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "trading_date"}}, DoNothing: true}).Create(&chain).Error; err != nil {
			return err
		}
		return tx.Where("trading_date = ?", tradingDate).First(&chain).Error
	})
	return chain, err
}

func (r *Repository) ExecutionChain(ctx context.Context, chainID string) (ExecutionChain, error) {
	var chain ExecutionChain
	err := r.db.WithContext(ctx).Where("chain_id = ?", chainID).First(&chain).Error
	return chain, err
}

func (r *Repository) ExecutionChainForDate(ctx context.Context, tradingDate string) (ExecutionChain, bool, error) {
	var chain ExecutionChain
	err := r.db.WithContext(ctx).Where("trading_date = ?", tradingDate).First(&chain).Error
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
		return tx.Model(&chain).Updates(updates).Error
	})
}

func (r *Repository) RefreshExecutionChainFilled(ctx context.Context, chainID string) (ExecutionChain, error) {
	var chain ExecutionChain
	err := research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Where("chain_id = ?", chainID).First(&chain).Error; err != nil {
			return err
		}
		dayStart, err := time.ParseInLocation("2006-01-02", chain.TradingDate, shanghai())
		if err != nil {
			return err
		}
		var filled int64
		if err := tx.Model(&Recommendation{}).Where("buy_at >= ? AND buy_at < ?", dayStart, dayStart.AddDate(0, 0, 1)).Count(&filled).Error; err != nil {
			return err
		}
		chain.FilledSlots = int(filled)
		if chain.FilledSlots >= chain.TargetSlots && chain.Status == "running" {
			now := time.Now().In(shanghai())
			chain.Status, chain.StopReason, chain.CompletedAt = "completed", "已完成当日三笔买入", &now
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

func (r *Repository) ExecutionChainExcludedCodes(ctx context.Context, chainID string) (map[string]struct{}, error) {
	var codes []string
	err := r.db.WithContext(ctx).Table("research2_recommendations AS recommendations").Distinct("recommendations.stock_code").
		Joins("JOIN research2_analysis_runs AS runs ON runs.run_id = recommendations.analysis_run_id").
		Where("runs.chain_id = ? AND (recommendations.buy_at IS NOT NULL OR recommendations.status IN ? OR recommendations.execution_failure_code IN ?)", chainID, []string{"missed_cash", "missed_untradable"}, hardExecutionFailureCodes).
		Pluck("recommendations.stock_code", &codes).Error
	if err != nil {
		return nil, err
	}
	var held []string
	if err = r.db.WithContext(ctx).Model(&Recommendation{}).Distinct("stock_code").
		Where("status IN ?", []string{"active", "sell_pending"}).Pluck("stock_code", &held).Error; err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(codes)+len(held))
	for _, code := range append(codes, held...) {
		if normalized, ok := normalizeResearch2Code(code); ok {
			result[normalized] = struct{}{}
		}
	}
	return result, nil
}

func normalizeResearch2Code(code string) (string, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	if len(code) != 8 || !(strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz")) {
		return "", false
	}
	return code, true
}

// ExecutionChainsReadyForRefill returns durable chains that have no remaining
// due or future buy candidates. Quote-source failures deliberately keep a
// candidate pending, so they are retried by the trading poll instead of
// spawning duplicate model calls.
func (r *Repository) ExecutionChainsReadyForRefill(ctx context.Context, now time.Time) ([]ExecutionChain, error) {
	local := now.In(shanghai())
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 13, 0, 0, 0, shanghai())
	if !local.Before(cutoff) {
		return nil, nil
	}
	var chains []ExecutionChain
	if err := r.db.WithContext(ctx).Where("trading_date = ? AND status = ? AND filled_slots < target_slots", local.Format("2006-01-02"), "running").Find(&chains).Error; err != nil {
		return nil, err
	}
	ready := make([]ExecutionChain, 0, len(chains))
	for _, chain := range chains {
		if strings.TrimSpace(chain.LatestRunID) == "" {
			continue
		}
		var latest AnalysisRun
		if err := r.db.WithContext(ctx).Where("run_id = ?", chain.LatestRunID).First(&latest).Error; err != nil {
			return nil, err
		}
		if latest.Status != "success" && latest.Status != "no_recommendation" {
			continue
		}
		var pending int64
		if err := r.db.WithContext(ctx).Table("research2_recommendations AS recommendations").
			Joins("JOIN research2_analysis_runs AS runs ON runs.run_id = recommendations.analysis_run_id").
			Where("runs.chain_id = ? AND recommendations.status IN ?", chain.ChainID, []string{"buy_pending", "standby"}).
			Count(&pending).Error; err != nil {
			return nil, err
		}
		var running int64
		if err := r.db.WithContext(ctx).Model(&AnalysisRun{}).Where("chain_id = ? AND status = ?", chain.ChainID, "running").Count(&running).Error; err != nil {
			return nil, err
		}
		if pending == 0 && running == 0 {
			ready = append(ready, chain)
		}
	}
	return ready, nil
}

func (r *Repository) ExpireExecutionChainsAtCutoff(ctx context.Context, now time.Time) error {
	local := now.In(shanghai())
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 13, 0, 0, 0, shanghai())
	if local.Before(cutoff) {
		return nil
	}
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		var chains []ExecutionChain
		if err := tx.Where("trading_date = ? AND status = ?", local.Format("2006-01-02"), "running").Find(&chains).Error; err != nil {
			return err
		}
		for _, chain := range chains {
			if err := tx.Model(&chain).Updates(map[string]any{"status": "cutoff", "stop_reason": "13:00前未补足三笔买入", "completed_at": local}).Error; err != nil {
				return err
			}
			if err := tx.Table("research2_recommendations").Where("analysis_run_id IN (?) AND status IN ?",
				tx.Model(&AnalysisRun{}).Select("run_id").Where("chain_id = ?", chain.ChainID), []string{"buy_pending", "standby"}).
				Updates(map[string]any{"status": "analysis_only", "failure_reason": "补位链已到13:00截止，仅保留分析"}).Error; err != nil {
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
		if err := tx.Where("trading_date = ? AND status = ?", tradingDate, "running").Find(&stopped).Error; err != nil {
			return err
		}
		for index := range stopped {
			if err := tx.Model(&stopped[index]).Updates(map[string]any{"status": "disabled", "stop_reason": "研究中心2自动策略已关闭", "completed_at": disabledAt}).Error; err != nil {
				return err
			}
			stopped[index].Status, stopped[index].StopReason, stopped[index].CompletedAt = "disabled", "研究中心2自动策略已关闭", &disabledAt
			if err := tx.Model(&AnalysisRun{}).Where("chain_id = ? AND status = ?", stopped[index].ChainID, "running").Updates(map[string]any{"status": "failed", "generated_at": disabledAt, "failure_reason": stopped[index].StopReason}).Error; err != nil {
				return err
			}
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

func (r *Repository) PromoteStandby(ctx context.Context, recommendationID, replacesRecommendationID, reason string) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status = ?", recommendationID, "standby").Updates(map[string]any{
			"status": "buy_pending", "replaces_recommendation_id": replacesRecommendationID,
			"promotion_reason": reason, "failure_reason": "",
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("research2 standby is no longer promotable")
		}
		return nil
	})
}

func (r *Repository) MarkStandbyNotUsed(ctx context.Context, recommendationID string) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&Recommendation{}).Where("recommendation_id = ? AND status = ?", recommendationID, "standby").Updates(map[string]any{
			"status": "standby_not_used", "failure_reason": "当轮主选及更高优先级备选已满足剩余席位",
		}).Error
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
	summary.WriteString("\n\n## 当日补位执行汇总\n\n")
	summary.WriteString(fmt.Sprintf("- 补位状态：%s\n- 分析轮次：%d\n- 目标买入：%d\n- 实际买入：%d\n- 剩余席位：%d\n", chain.Status, len(runs), chain.TargetSlots, chain.FilledSlots, max(0, chain.TargetSlots-chain.FilledSlots)))
	if strings.TrimSpace(chain.StopReason) != "" {
		summary.WriteString("- 结束原因：" + strings.TrimSpace(chain.StopReason) + "\n")
	}
	if len(recommendations) > 0 {
		summary.WriteString("\n### 全部候选与执行结果\n\n")
		for _, item := range recommendations {
			line := fmt.Sprintf("- 第%d轮 #%d %s %s（%s）：%s", attemptForRun(runs, item.AnalysisRunID), item.SelectionRank, item.StockCode, item.StockName, item.SelectionRole, item.Status)
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
