package data

import (
	"strings"

	"go-stock/backend/models"
)

// calculateComprehensiveStats 计算综合统计（消除幸存者偏差）
func calculateComprehensiveStats(allRecommends []models.AiRecommendStocks) models.AiRecommendYieldComprehensiveStats {
	stats := models.AiRecommendYieldComprehensiveStats{
		TotalRecommendCount: len(allRecommends),
	}

	if len(allRecommends) == 0 {
		return stats
	}

	var activatedRecommends []models.AiRecommendStocks
	var skippedRecommends []models.AiRecommendStocks
	var pendingRecommends []models.AiRecommendStocks
	var ineligibleRecommends []models.AiRecommendStocks

	// 分类统计
	for _, rec := range allRecommends {
		status := strings.TrimSpace(rec.ActivationStatus)
		switch status {
		case "activated":
			activatedRecommends = append(activatedRecommends, rec)
		case "skipped":
			skippedRecommends = append(skippedRecommends, rec)
		case "pending", "":
			pendingRecommends = append(pendingRecommends, rec)
		case "invalid", "ineligible":
			ineligibleRecommends = append(ineligibleRecommends, rec)
		}
	}

	stats.ActivatedCount = len(activatedRecommends)
	stats.SkippedCount = len(skippedRecommends)
	stats.PendingCount = len(pendingRecommends)
	stats.IneligibleCount = len(ineligibleRecommends)

	// 计算激活率
	if stats.TotalRecommendCount > 0 {
		stats.ActivationRate = round2(float64(stats.ActivatedCount) / float64(stats.TotalRecommendCount) * 100)
		stats.ActivationRateText = formatSignedPercent(stats.ActivationRate)
	}

	// 计算已激活推荐的收益率
	if len(activatedRecommends) > 0 {
		// 这里需要实际的收益率计算逻辑
		// 暂时使用简化版本
		stats.ActivatedYieldRate = 0
		stats.ActivatedYieldRateText = "--"
	}

	// 计算机会成本
	if len(skippedRecommends) > 0 {
		opportunityCost, costText := calculateOpportunityCost(skippedRecommends, 5)
		stats.SkippedOpportunityCost = opportunityCost
		stats.SkippedOpportunityCostText = costText
	}

	// 计算偏差调整后的收益率
	// 简化公式：(激活数 * 激活收益率 + 跳过数 * 机会成本) / 总数
	if stats.TotalRecommendCount > 0 {
		totalWeightedYield := float64(stats.ActivatedCount)*stats.ActivatedYieldRate +
			float64(stats.SkippedCount)*stats.SkippedOpportunityCost
		stats.BiasAdjustedYieldRate = round2(totalWeightedYield / float64(stats.TotalRecommendCount))
		stats.BiasAdjustedYieldRateText = formatSignedPercent(stats.BiasAdjustedYieldRate)
	}

	return stats
}

// shouldExcludeFromStats 判断是否应该从统计中排除
func shouldExcludeFromStats(item models.AiRecommendStocksYieldItem, strictMode bool) bool {
	eligibility := strings.TrimSpace(item.BacktestEligibility)
	status := strings.TrimSpace(item.ActivationStatus)

	// 不符合回测条件的排除
	if eligibility != "" && eligibility != recommendBacktestEligible {
		return true
	}

	// 严格模式：只统计 activated
	if strictMode {
		return status != "activated"
	}

	// 宽松模式：统计所有（除了 ineligible）
	return false
}
