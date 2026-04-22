package data

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/models"
)

// activationRuleBacktestResult 激活规则回测结果
type activationRuleBacktestResult struct {
	RuleID                string        `json:"ruleId"`
	TotalSamples          int           `json:"totalSamples"`          // 总样本数
	ActivatedCount        int           `json:"activatedCount"`        // 激活数量
	ActivationRate        float64       `json:"activationRate"`        // 激活率（%）
	AvgActivationDelay    time.Duration `json:"avgActivationDelay"`    // 平均激活延迟
	MedianActivationDelay time.Duration `json:"medianActivationDelay"` // 中位激活延迟
	FalsePositiveRate     float64       `json:"falsePositiveRate"`     // 假阳性率（激活后立即止损的比例）
	AvgYieldRate          float64       `json:"avgYieldRate"`          // 平均收益率
	WinRate               float64       `json:"winRate"`               // 胜率
}

// backtestActivationRule 使用历史数据验证激活规则有效性
func backtestActivationRule(rule *activationRule, historicalData []models.AiRecommendStocks) activationRuleBacktestResult {
	result := activationRuleBacktestResult{
		RuleID:       generateRuleID(rule),
		TotalSamples: len(historicalData),
	}

	if len(historicalData) == 0 {
		return result
	}

	var activationDelays []time.Duration
	var yieldRates []float64
	falsePositiveCount := 0
	winCount := 0

	for _, rec := range historicalData {
		// 检查是否激活
		activated, activationTime, yieldRate := checkHistoricalActivation(rule, rec)
		if !activated {
			continue
		}

		result.ActivatedCount++

		// 计算激活延迟
		recordTime := recommendRecordTime(rec)
		if !recordTime.IsZero() && !activationTime.IsZero() {
			delay := activationTime.Sub(recordTime)
			if delay > 0 {
				activationDelays = append(activationDelays, delay)
			}
		}

		// 统计收益率
		if yieldRate != 0 {
			yieldRates = append(yieldRates, yieldRate)
			if yieldRate > 0 {
				winCount++
			}
		}

		// 检查是否假阳性（激活后立即止损）
		if isFalsePositive(rec, activationTime) {
			falsePositiveCount++
		}
	}

	// 计算激活率
	if result.TotalSamples > 0 {
		result.ActivationRate = round2(float64(result.ActivatedCount) / float64(result.TotalSamples) * 100)
	}

	// 计算平均激活延迟
	if len(activationDelays) > 0 {
		totalDelay := time.Duration(0)
		for _, delay := range activationDelays {
			totalDelay += delay
		}
		result.AvgActivationDelay = totalDelay / time.Duration(len(activationDelays))

		// 计算中位数
		result.MedianActivationDelay = calculateMedianDuration(activationDelays)
	}

	// 计算假阳性率
	if result.ActivatedCount > 0 {
		result.FalsePositiveRate = round2(float64(falsePositiveCount) / float64(result.ActivatedCount) * 100)
	}

	// 计算平均收益率
	if len(yieldRates) > 0 {
		totalYield := 0.0
		for _, y := range yieldRates {
			totalYield += y
		}
		result.AvgYieldRate = round2(totalYield / float64(len(yieldRates)))
	}

	// 计算胜率
	if len(yieldRates) > 0 {
		result.WinRate = round2(float64(winCount) / float64(len(yieldRates)) * 100)
	}

	return result
}

// checkHistoricalActivation 检查历史推荐是否激活
func checkHistoricalActivation(rule *activationRule, rec models.AiRecommendStocks) (bool, time.Time, float64) {
	// 简化实现：检查推荐的激活状态
	activationStatus := strings.TrimSpace(rec.ActivationStatus)
	if activationStatus != "activated" {
		return false, time.Time{}, 0
	}

	// 获取激活时间（使用 UpdatedAt 作为近似）
	activationTime := time.Time{}
	if rec.UpdatedAt.After(rec.CreatedAt) {
		activationTime = rec.UpdatedAt
	}

	// 收益率需要从其他地方获取，这里返回 0
	yieldRate := 0.0

	return true, activationTime, yieldRate
}

// isFalsePositive 检查是否为假阳性（激活后立即止损）
func isFalsePositive(rec models.AiRecommendStocks, activationTime time.Time) bool {
	// 简化实现：检查推荐状态
	// 如果推荐状态包含 "止损" 或 "失效"，可能是假阳性
	status := strings.ToLower(strings.TrimSpace(rec.RecommendStatus))
	if strings.Contains(status, "止损") || strings.Contains(status, "失效") {
		return true
	}
	return false
}

// generateRuleID 生成规则ID
func generateRuleID(rule *activationRule) string {
	if rule == nil {
		return ""
	}
	return fmt.Sprintf("%s_%s_%.2f", rule.SignalType, rule.EvaluationWindow, rule.VolumeRatio)
}

// calculateMedianDuration 计算时间中位数
func calculateMedianDuration(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}

	// 复制并排序
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)

	// 简单冒泡排序
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// 返回中位数
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// parseRecommendSellTime 解析推荐卖出时间
func parseRecommendSellTime(sellTimeStr string) (time.Time, error) {
	if sellTimeStr == "" || sellTimeStr == "持仓中" || sellTimeStr == "待激活" {
		return time.Time{}, fmt.Errorf("无效的卖出时间: %s", sellTimeStr)
	}

	// 尝试多种时间格式
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, sellTimeStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("无法解析卖出时间: %s", sellTimeStr)
}
