package data

import (
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

func closeManualMinuteCoverageGaps(runtime *aiRecommendYieldRecalcRuntime, codeSet map[string]struct{}) error {
	if runtime == nil || runtime.meta == nil || len(codeSet) == 0 {
		return nil
	}
	deadline := manualMinuteCoverageNow().Add(manualMinuteCoverageRetryBudget)
	round := 0
	for {
		stats, issues := computeMinuteDownloadCoverageStatsWithSuspensionFetch(runtime.meta, -1)
		if stats.Pending == 0 && stats.Uncoverable == 0 {
			_ = runWithSQLiteBusyRetry(func() error {
				return db.Dao.Model(&models.AiRecommendYieldMeta{}).
					Where("id = ?", runtime.meta.ID).
					Update("last_download_error", "").Error
			})
			return nil
		}
		if !manualMinuteCoverageNow().Before(deadline) {
			if err := markMinuteCoverageIssuesUncoverable(runtime.meta, issues, "分钟线数据源在重试时间内仍未补齐缺口"); err != nil {
				return err
			}
			runtime.manualDownloadWarning = buildManualDownloadCoverageFailure(runtime.meta, 5)
			return nil
		}

		nextTasks := buildManualMinuteGapCoverageTasks(codeSet)
		if len(nextTasks) == 0 {
			if err := markMinuteCoverageIssuesUncoverable(runtime.meta, issues, "存在覆盖问题，但没有可执行的缺口下载任务"); err != nil {
				return err
			}
			runtime.manualDownloadWarning = buildManualDownloadCoverageFailure(runtime.meta, 5)
			return nil
		}

		round++
		_ = updateManualMinuteCoverageRetryStatus(runtime.meta.ID, round, deadline, stats, issues)
		runAiRecommendMinuteCoverageTasks(runtime, nextTasks)

		stats, issues = computeMinuteDownloadCoverageStatsWithSuspensionFetch(runtime.meta, -1)
		if stats.Pending == 0 {
			if stats.Uncoverable == 0 {
				continue
			}
		}
		if manualMinuteCoverageMaxRetryRounds > 0 && round >= manualMinuteCoverageMaxRetryRounds {
			if err := markMinuteCoverageIssuesUncoverable(runtime.meta, issues, fmt.Sprintf("分钟线数据源连续%d轮重试后仍未补齐缺口", round)); err != nil {
				return err
			}
			runtime.manualDownloadWarning = buildManualDownloadCoverageFailure(runtime.meta, 5)
			return nil
		}
		if wait := manualMinuteCoverageRetryBackoff(round - 1); wait > 0 {
			if remaining := deadline.Sub(manualMinuteCoverageNow()); remaining > 0 && wait > remaining {
				wait = remaining
			}
			if wait > 0 {
				manualMinuteCoverageSleep(wait)
			}
		}
	}
}

func minuteCoverageGapNeedsHistoricalSource(start, end time.Time) bool {
	start = normalizeMinuteTime(start)
	end = normalizeMinuteTime(end)
	if start.IsZero() || end.IsZero() {
		return false
	}
	return !isSameCNTradeDate(start, end) || !isTodayCN(end)
}

func markPendingMinuteCoverageIssuesUncoverable(meta *models.AiRecommendYieldMeta, issues []minuteCoverageIssue, prefix string) error {
	return markMinuteCoverageIssuesUncoverable(meta, issues, prefix)
}

func markMinuteCoverageIssuesUncoverable(meta *models.AiRecommendYieldMeta, issues []minuteCoverageIssue, prefix string) error {
	if meta == nil || len(issues) == 0 {
		return nil
	}
	pending := make([]minuteCoverageIssue, 0, len(issues))
	recordIDs := make([]uint, 0, len(issues))
	seen := map[uint]struct{}{}
	for _, issue := range issues {
		status := strings.TrimSpace(issue.Status)
		if (status != "待覆盖" && status != "不可覆盖") || issue.RecordID == 0 {
			continue
		}
		pending = append(pending, issue)
		if _, ok := seen[issue.RecordID]; ok {
			continue
		}
		seen[issue.RecordID] = struct{}{}
		recordIDs = append(recordIDs, issue.RecordID)
	}
	if len(pending) == 0 {
		return nil
	}
	sort.Slice(recordIDs, func(i, j int) bool { return recordIDs[i] < recordIDs[j] })
	records := make([]models.AiRecommendStocks, 0, len(recordIDs))
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Where("id IN ?", recordIDs).Find(&records).Error; err != nil {
		return err
	}
	recordMap := make(map[uint]models.AiRecommendStocks, len(records))
	for _, rec := range records {
		recordMap[rec.ID] = rec
	}
	now := time.Now()
	for _, issue := range pending {
		reason := buildUncoverableMinuteIssueReason(issue, prefix)
		if err := upsertMinuteUncoverableRecordState(issue, recordMap[issue.RecordID], reason, now); err != nil {
			return err
		}
	}
	return nil
}

func buildUncoverableMinuteIssueReason(issue minuteCoverageIssue, prefix string) string {
	reason := strings.TrimSpace(issue.RawReason)
	if reason == "" {
		reason = "分钟线缺口未补齐"
	}
	if strings.TrimSpace(prefix) != "" {
		reason = strings.TrimSpace(prefix) + "：" + reason
	}
	return reason
}

func upsertMinuteUncoverableRecordState(issue minuteCoverageIssue, rec models.AiRecommendStocks, reason string, now time.Time) error {
	if issue.RecordID == 0 {
		return nil
	}
	existing := models.AiRecommendYieldRecordState{}
	err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).Where("recommend_id = ?", issue.RecordID).First(&existing).Error
	if err == nil {
		err = db.Dao.Model(&models.AiRecommendYieldRecordState{}).
			Where("recommend_id = ?", issue.RecordID).
			Updates(map[string]any{
				"data_status":        "无法判定",
				"data_status_reason": reason,
				"last_recalc_at":     now,
				"updated_at":         now,
			}).Error
		if err == nil {
			clearMinuteCoverageStatsCache()
		}
		return err
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	recordTime := issue.RecordTime
	if recordTime.IsZero() {
		recordTime = recommendRecordTime(rec)
	}
	code := normalizeRecommendStockCode(issue.StockCode)
	if code == "" {
		code = normalizeRecommendStockCode(rec.StockCode)
	}
	state := models.AiRecommendYieldRecordState{
		RecommendID:       issue.RecordID,
		StockCode:         code,
		StockName:         firstNonEmptyText(issue.StockName, rec.StockName),
		ModelName:         strings.TrimSpace(rec.ModelName),
		BkName:            strings.TrimSpace(rec.BkName),
		RecommendCategory: strings.TrimSpace(rec.RecommendCategory),
		ActivationStatus:  firstNonEmptyText(rec.ActivationStatus, "pending"),
		PositionStatus:    "待激活",
		YieldRateText:     "--",
		DataStatus:        "无法判定",
		DataStatusReason:  reason,
		LastRecalcAt:      &now,
	}
	if !recordTime.IsZero() {
		t := recordTime
		state.RecommendTime = &t
		state.SignalTime = &t
		state.TotalScopeStart = t.In(cnLocation()).Format("2006-01-02")
	}
	if !issue.MissingEnd.IsZero() {
		state.TotalScopeEnd = issue.MissingEnd.In(cnLocation()).Format("2006-01-02")
	}
	err = db.Dao.Create(&state).Error
	if err == nil {
		clearMinuteCoverageStatsCache()
	}
	return err
}

func updateManualMinuteCoverageRetryStatus(metaID uint, round int, deadline time.Time, stats minuteCoverageStats, issues []minuteCoverageIssue) error {
	if metaID == 0 {
		return nil
	}
	parts := manualCoverageIssueParts(issues, "待覆盖", 3)
	message := fmt.Sprintf("正在重试分钟线缺口（第%d轮，待覆盖:%d，不可覆盖:%d）", round, stats.Pending, stats.Uncoverable)
	if len(parts) > 0 {
		message += "：" + strings.Join(parts, "；")
	}
	return runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).
			Where("id = ?", metaID).
			Updates(map[string]any{
				"last_download_error": message,
				"recalc_progress":     manualMinuteCoverageRetryProgress(round, deadline),
				"updated_at":          time.Now(),
			}).Error
	})
}

func manualMinuteCoverageRetryProgress(round int, deadline time.Time) int {
	if round <= 0 {
		return 5
	}
	progress := 5 + round*12
	if !deadline.IsZero() && manualMinuteCoverageRetryBudget > 0 {
		startedAt := deadline.Add(-manualMinuteCoverageRetryBudget)
		elapsed := manualMinuteCoverageNow().Sub(startedAt)
		if elapsed > 0 {
			elapsedProgress := 5 + int(elapsed*85/manualMinuteCoverageRetryBudget)
			if elapsedProgress > progress {
				progress = elapsedProgress
			}
		}
	}
	if progress < 5 {
		return 5
	}
	if progress > 90 {
		return 90
	}
	return progress
}

func manualMinuteCoverageRetryBackoff(round int) time.Duration {
	if len(manualMinuteCoverageRetryBackoffs) == 0 {
		return 0
	}
	if round < 0 {
		round = 0
	}
	if round >= len(manualMinuteCoverageRetryBackoffs) {
		return manualMinuteCoverageRetryBackoffs[len(manualMinuteCoverageRetryBackoffs)-1]
	}
	return manualMinuteCoverageRetryBackoffs[round]
}
