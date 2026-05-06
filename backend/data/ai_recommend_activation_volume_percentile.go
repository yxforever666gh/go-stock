package data

import (
	"fmt"
	"sort"
)

// calculateVolumePercentileBaseline 计算历史量能分位数基准
// 返回指定分位数对应的量能值
func calculateVolumePercentileBaseline(rule *activationRule, bars []minuteBar, currentIdx int, percentile float64) (float64, error) {
	if rule == nil || len(bars) == 0 || currentIdx < 0 || currentIdx >= len(bars) {
		return 0, fmt.Errorf("无效参数")
	}
	if percentile <= 0 || percentile >= 100 {
		return 0, fmt.Errorf("分位数必须在 0-100 之间")
	}

	// 默认使用近30个交易日的数据
	lookbackDays := 30
	currentBar := bars[currentIdx]
	currentTime := currentBar.TradeTime.In(cnLocation())
	cutoffTime := currentTime.AddDate(0, 0, -lookbackDays)

	// 收集历史同时段的量能数据
	var historicalValues []float64
	currentSlotIndex := currentBar.SlotIndex
	currentSlotWindow := currentBar.SlotWindow

	for i := 0; i < currentIdx; i++ {
		bar := bars[i]
		if bar.TradeTime.Before(cutoffTime) {
			continue
		}

		// 只统计同一时段的数据（如都是10:00-10:05）
		if currentSlotWindow > 1 && bar.SlotWindow == currentSlotWindow && bar.SlotIndex == currentSlotIndex {
			value := minuteMetricValue(rule, bar)
			if value > 0 {
				historicalValues = append(historicalValues, value)
			}
			continue
		}

		// 对于1分钟K线，匹配小时和分钟
		barTime := bar.TradeTime.In(cnLocation())
		if barTime.Hour() == currentTime.Hour() && barTime.Minute() == currentTime.Minute() {
			value := minuteMetricValue(rule, bar)
			if value > 0 {
				historicalValues = append(historicalValues, value)
			}
		}
	}

	if len(historicalValues) < 5 {
		return 0, fmt.Errorf("历史数据不足（需要至少5个样本，当前%d个）", len(historicalValues))
	}

	// 计算分位数
	sort.Float64s(historicalValues)
	index := int(float64(len(historicalValues)) * percentile / 100.0)
	if index >= len(historicalValues) {
		index = len(historicalValues) - 1
	}

	return historicalValues[index], nil
}

// passesVolumeRuleWithPercentile 使用分位数基准检查量能条件
func passesVolumeRuleWithPercentile(rule *activationRule, bars []minuteBar, idx int) (bool, string) {
	if rule == nil || idx < 0 || idx >= len(bars) {
		return false, ""
	}

	currentMetric := minuteMetricValue(rule, bars[idx])
	if currentMetric <= 0 {
		return false, ""
	}

	// 获取分位数配置，默认70%
	percentile := rule.VolumePercentile
	if percentile <= 0 {
		percentile = 70
	}

	baseline, err := calculateVolumePercentileBaseline(rule, bars, idx, percentile)
	if err != nil {
		return passesTraditionalVolumeRule(rule, bars, idx)
	}

	// 应用倍数要求
	volumeRatio := rule.VolumeRatio
	if volumeRatio <= 0 {
		volumeRatio = 1.0
	}

	target := baseline * volumeRatio
	if currentMetric >= target {
		return true, ""
	}

	return false, fmt.Sprintf(
		"已进入主买入区，但%s %.2f 低于近30日同时段P%.0f基准 %.2f 的 %.2f 倍（目标 %.2f）",
		activationRuleMetricLabel(rule),
		round2(currentMetric),
		percentile,
		round2(baseline),
		round2(volumeRatio),
		round2(target),
	)
}
