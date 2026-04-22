package data

import (
	"errors"
	"fmt"
	"time"

	"go-stock/backend/models"
)

// validateActivationRuleTimeline 验证激活规则的时间线，防止使用未来数据
func validateActivationRuleTimeline(rule *activationRule, rec models.AiRecommendStocks) error {
	if rule == nil {
		return errors.New("激活规则为空")
	}

	// 历史数据豁免（没有 GeneratedAt 的旧规则）
	if rule.GeneratedAt.IsZero() {
		return nil
	}

	recordTime := getRecommendRecordTime(rec)
	if recordTime.IsZero() {
		return errors.New("推荐时间缺失")
	}

	// 规则生成时间不能晚于推荐时间
	if rule.GeneratedAt.After(recordTime) {
		return fmt.Errorf("规则生成时间晚于推荐时间：规则生成 %s，推荐时间 %s",
			rule.GeneratedAt.Format("2006-01-02 15:04:05"),
			recordTime.Format("2006-01-02 15:04:05"))
	}

	// 规则不能使用推荐时间之后的数据
	if !rule.DataCutoffTime.IsZero() && rule.DataCutoffTime.After(recordTime) {
		return fmt.Errorf("规则使用了未来数据：数据截止 %s，推荐时间 %s",
			rule.DataCutoffTime.Format("2006-01-02 15:04:05"),
			recordTime.Format("2006-01-02 15:04:05"))
	}

	// 规则生效时间应该在推荐时间之后或同时
	if !rule.ValidFrom.IsZero() && rule.ValidFrom.Before(recordTime) {
		return fmt.Errorf("规则生效时间早于推荐时间：生效时间 %s，推荐时间 %s",
			rule.ValidFrom.Format("2006-01-02 15:04:05"),
			recordTime.Format("2006-01-02 15:04:05"))
	}

	return nil
}

// validateActivationRuleTimelineForPaths 验证多路径规则的时间线
func validateActivationRuleTimelineForPaths(rule *activationRule, rec models.AiRecommendStocks) error {
	if rule == nil {
		return errors.New("激活规则为空")
	}

	// 验证顶层规则
	if err := validateActivationRuleTimeline(rule, rec); err != nil {
		return fmt.Errorf("顶层规则验证失败: %w", err)
	}

	// 验证每条路径
	for i, path := range rule.Paths {
		if err := validateActivationRuleTimeline(&path, rec); err != nil {
			return fmt.Errorf("路径 %d (%s) 验证失败: %w", i, path.Name, err)
		}
	}

	return nil
}

// setActivationRuleTimestamps 为新生成的规则设置时间戳
func setActivationRuleTimestamps(rule *activationRule, rec models.AiRecommendStocks, now time.Time) {
	if rule == nil {
		return
	}

	recordTime := recommendRecordTime(rec)
	if recordTime.IsZero() {
		recordTime = now
	}

	// 设置生成时间为当前时间
	rule.GeneratedAt = now

	// 设置数据截止时间为推荐时间（确保不使用未来数据）
	rule.DataCutoffTime = recordTime

	// 设置生效时间为推荐时间
	rule.ValidFrom = recordTime

	// 递归设置路径的时间戳
	for i := range rule.Paths {
		setActivationRuleTimestamps(&rule.Paths[i], rec, now)
	}
}

// getRecommendRecordTime 获取推荐记录时间
func getRecommendRecordTime(rec models.AiRecommendStocks) time.Time {
	if rec.DataTime != nil && !rec.DataTime.IsZero() {
		return *rec.DataTime
	}
	if !rec.CreatedAt.IsZero() {
		return rec.CreatedAt
	}
	return time.Time{}
}
