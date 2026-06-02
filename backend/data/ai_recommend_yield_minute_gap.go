package data

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"strings"
	"time"
)

func closeManualMinuteCoverageGaps(runtime *aiRecommendYieldRecalcRuntime, codeSet map[string]struct{}) error {
	if runtime == nil || runtime.meta == nil || len(codeSet) == 0 {
		return nil
	}
	deadline := manualMinuteCoverageNow().Add(manualMinuteCoverageRetryBudget)
	round := 0
	for {
		stats, issues := computeMinuteDownloadCoverageStatsWithIssues(runtime.meta, -1)
		if stats.Pending == 0 && stats.Uncoverable == 0 {
			_ = runWithSQLiteBusyRetry(func() error {
				return db.Dao.Model(&models.AiRecommendYieldMeta{}).
					Where("id = ?", runtime.meta.ID).
					Update("last_download_error", "").Error
			})
			return nil
		}
		if !manualMinuteCoverageNow().Before(deadline) {
			failure := buildManualDownloadCoverageFailure(runtime.meta, 5)
			if failure == "" {
				failure = "分钟线缺口未补齐"
			}
			return fmt.Errorf("分钟线补齐失败：15分钟内仍未全部连续覆盖；%s", failure)
		}

		nextTasks := buildManualMinuteGapCoverageTasks(codeSet)
		if len(nextTasks) == 0 {
			failure := buildManualDownloadCoverageFailure(runtime.meta, 5)
			if failure == "" {
				failure = "存在覆盖问题，但没有可执行的缺口下载任务"
			}
			return fmt.Errorf("分钟线补齐失败：%s", failure)
		}

		round++
		_ = updateManualMinuteCoverageRetryStatus(runtime.meta.ID, round, stats, issues)
		runAiRecommendMinuteCoverageTasks(runtime, nextTasks)

		stats, _ = computeMinuteDownloadCoverageStatsWithIssues(runtime.meta, 0)
		if stats.Pending == 0 && stats.Uncoverable == 0 {
			continue
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

func updateManualMinuteCoverageRetryStatus(metaID uint, round int, stats minuteCoverageStats, issues []minuteCoverageIssue) error {
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
			Update("last_download_error", message).Error
	})
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
