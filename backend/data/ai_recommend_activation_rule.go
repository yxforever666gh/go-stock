package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/models"
)

const activationRuleVersionV1 = "v1"
const activationRuleVersionV2 = "v2"
const activationRuleVersionV3 = "v3"
const activationRuleModeAnyOf = "any_of"
const breakoutMaxEntryChaseRatio = 1.015
const breakoutStopProfitSafetyRatio = 0.995

type activationRule struct {
	Version                    string                   `json:"version,omitempty"`
	Mode                       string                   `json:"mode,omitempty"`
	Name                       string                   `json:"name,omitempty"`
	StrategyRunID              string                   `json:"strategyRunId,omitempty"`
	StrategyRuleID             string                   `json:"strategyRuleId,omitempty"`
	Paths                      []activationRule         `json:"paths,omitempty"`
	OpeningPolicy              *activationOpeningPolicy `json:"openingPolicy,omitempty"`
	SignalType                 string                   `json:"signalType"`
	EvaluationWindow           string                   `json:"evaluationWindow,omitempty"`
	Baseline                   string                   `json:"baseline,omitempty"`
	Operator                   string                   `json:"operator,omitempty"`
	ThresholdValue             float64                  `json:"thresholdValue,omitempty"`
	ThresholdMax               float64                  `json:"thresholdMax,omitempty"`
	Support                    float64                  `json:"support,omitempty"`
	ReferenceEntry             float64                  `json:"referenceEntry,omitempty"`
	Stop                       float64                  `json:"stop,omitempty"`
	Target                     float64                  `json:"target,omitempty"`
	ATR14                      float64                  `json:"atr14,omitempty"`
	RiskPerShare               float64                  `json:"riskPerShare,omitempty"`
	RewardRisk                 float64                  `json:"rewardRisk,omitempty"`
	NegativeOvernightGapRisk60 float64                  `json:"negativeOvernightGapRisk60,omitempty"`
	VolumeRatio                float64                  `json:"volumeRatio,omitempty"`
	ConfirmBars                int                      `json:"confirmBars,omitempty"`
	VolumeWindow               int                      `json:"volumeWindow,omitempty"`
	VolumeMetric               string                   `json:"volumeMetric,omitempty"`
	ExpireTradeDays            int                      `json:"expireTradeDays,omitempty"`
	DecisionTradeDayIndex      int                      `json:"decisionTradeDayIndex,omitempty"`
	ValidFromTradeDayIndex     int                      `json:"validFromTradeDayIndex,omitempty"`
	ValidTradeDays             int                      `json:"validTradeDays,omitempty"`
	MaxHoldTradeDays           int                      `json:"maxHoldTradeDays,omitempty"`
	NoActivationAfterMin       int                      `json:"noActivationAfterMin,omitempty"`
	TrailingActivationR        float64                  `json:"trailingActivationR,omitempty"`
	TrailingATRMultiple        float64                  `json:"trailingATRMultiple,omitempty"`
	// 时间戳字段，用于防止事后拟合
	GeneratedAt    time.Time `json:"generatedAt,omitempty"`    // 规则生成时间
	ValidFrom      time.Time `json:"validFrom,omitempty"`      // 规则生效时间
	DataCutoffTime time.Time `json:"dataCutoffTime,omitempty"` // 数据截止时间
	// 量能条件优化字段
	VolumeBaselineType string  `json:"volumeBaselineType,omitempty"` // "fixed", "percentile", "adaptive"
	VolumePercentile   float64 `json:"volumePercentile,omitempty"`   // 使用的分位数（如75）
}

type activationOpeningPolicy struct {
	MorningBufferUntil                   string  `json:"morningBufferUntil,omitempty"`
	MaxChasePrice                        float64 `json:"maxChasePrice,omitempty"`
	SameDayOnly                          bool    `json:"sameDayOnly,omitempty"`
	GapBelowStopAction                   string  `json:"gapBelowStopAction,omitempty"`
	GapAboveMaxChaseAction               string  `json:"gapAboveMaxChaseAction,omitempty"`
	OpenInsideBuyRangeAction             string  `json:"openInsideBuyRangeAction,omitempty"`
	OpenBetweenRangeAndBreakoutAction    string  `json:"openBetweenRangeAndBreakoutAction,omitempty"`
	OpenBetweenBreakoutAndMaxChaseAction string  `json:"openBetweenBreakoutAndMaxChaseAction,omitempty"`
}

type activationScanResult struct {
	Triggered bool
	Blocked   bool
	Reason    string
	Time      time.Time
	Price     float64
}

var (
	activationVolumeRatioRegexp    = regexp.MustCompile(`(?:近|相对近)\s*(\d+)\s*个\s*(?:\d+\s*分钟)?(?:均量|均额)|近\s*(\d+)\s*个\s*(?:\d+\s*分钟)?(?:均量|均额)|量比\s*[≥>=]+\s*(\d+(?:\.\d+)?)|(?:至少|不低于|大于等于|≥|>=)\s*(\d+(?:\.\d+)?)\s*倍`)
	activationPriceBreakoutRegexps = []*regexp.Regexp{
		regexp.MustCompile(`(?:任一\s*\d+\s*分钟K线)?收盘(?:价)?\s*[≥>=]+\s*(\d+(?:\.\d+)?)`),
		regexp.MustCompile(`(?:突破路径(?:为)?|突破激活(?:价)?|上破确认(?:价)?|突破价|突破位)\s*[:：]?\s*(\d+(?:\.\d+)?)`),
		regexp.MustCompile(`(?:价格)?站上\s*(\d+(?:\.\d+)?)`),
		regexp.MustCompile(`(?:价格)?突破\s*(\d+(?:\.\d+)?)`),
	}
	activationConfirmBarsRegexp = regexp.MustCompile(`连续\s*(\d+)\s*根`)
	activationExpireRegexp      = regexp.MustCompile(`未来\s*(\d+)(?:\s*-\s*(\d+))?\s*个交易日`)
	activationEvalWindowRegexp  = regexp.MustCompile(`(\d+)\s*分钟`)
)

func hasMachineActivationRule(rec *models.AiRecommendStocks) bool {
	if rec == nil {
		return false
	}
	return strings.TrimSpace(rec.ActivationRuleJSON) != ""
}

func parseActivationRuleJSON(raw string) (*activationRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("结构化激活规则为空")
	}
	var rule activationRule
	if err := json.Unmarshal([]byte(raw), &rule); err != nil {
		return nil, err
	}
	if err := validateActivationRule(&rule); err != nil {
		return nil, err
	}
	return &rule, nil
}

func validateActivationRule(rule *activationRule) error {
	if rule == nil {
		return errors.New("结构化激活规则为空")
	}
	if len(rule.Paths) > 0 {
		if strings.TrimSpace(rule.Version) == "" {
			rule.Version = activationRuleVersionV2
		}
		if hasActivationOpeningPolicy(rule) && rule.Version == activationRuleVersionV2 {
			rule.Version = activationRuleVersionV3
		}
		if strings.TrimSpace(rule.Mode) == "" {
			rule.Mode = activationRuleModeAnyOf
		}
		if strings.TrimSpace(rule.Mode) != activationRuleModeAnyOf {
			return fmt.Errorf("不支持的 mode: %s", strings.TrimSpace(rule.Mode))
		}
		normalized := make([]activationRule, 0, len(rule.Paths))
		for _, path := range rule.Paths {
			copied := path
			copied.Paths = nil
			copied.Mode = ""
			copied.Version = ""
			if err := validateSingleActivationRule(&copied); err != nil {
				name := strings.TrimSpace(firstNonEmptyText(path.Name, path.SignalType, "unnamed"))
				return fmt.Errorf("路径[%s]无效: %w", name, err)
			}
			normalized = append(normalized, copied)
		}
		rule.Paths = normalized
		return nil
	}
	if strings.TrimSpace(rule.Version) == "" {
		rule.Version = activationRuleVersionV1
	}
	if rule.OpeningPolicy != nil && rule.Version == activationRuleVersionV1 {
		rule.Version = activationRuleVersionV3
	}
	return validateSingleActivationRule(rule)
}

func validateSingleActivationRule(rule *activationRule) error {
	if rule == nil {
		return errors.New("结构化激活规则为空")
	}
	switch strings.TrimSpace(rule.SignalType) {
	case "price_breakout_with_volume", "price_range_with_volume":
	default:
		return fmt.Errorf("不支持的 signalType: %s", strings.TrimSpace(rule.SignalType))
	}
	if rule.ThresholdValue <= 0 {
		return errors.New("结构化激活规则缺少 thresholdValue")
	}
	if rule.VolumeRatio <= 0 {
		rule.VolumeRatio = 1
	}
	if strings.TrimSpace(rule.Baseline) == "" {
		return errors.New("结构化激活规则缺少 baseline")
	}
	rule.EvaluationWindow = normalizeActivationEvaluationWindow(rule.EvaluationWindow)
	if strings.TrimSpace(rule.Operator) == "" {
		rule.Operator = ">="
	}
	if rule.ConfirmBars <= 0 {
		rule.ConfirmBars = 1
	}
	if rule.VolumeWindow <= 0 {
		rule.VolumeWindow = 5
	}
	if rule.ExpireTradeDays <= 0 {
		rule.ExpireTradeDays = recommendPendingActivationMaxTradeDays
	}
	rule.OpeningPolicy = normalizeActivationOpeningPolicy(rule.OpeningPolicy)
	return nil
}

func extractActivationBreakoutThreshold(text string) (float64, bool) {
	normalized := normalizeRecommendText(text)
	if normalized == "" {
		return 0, false
	}
	for _, pattern := range activationPriceBreakoutRegexps {
		indexes := pattern.FindStringSubmatchIndex(normalized)
		if len(indexes) < 4 {
			continue
		}
		groupStart, groupEnd := indexes[2], indexes[3]
		if activationBreakoutTailLooksLikeIndicator(normalized[groupEnd:]) {
			continue
		}
		value, err := strconv.ParseFloat(normalized[groupStart:groupEnd], 64)
		if err != nil || value <= 0 {
			continue
		}
		return round2(value), true
	}
	return 0, false
}

func activationBreakoutTailLooksLikeIndicator(tail string) bool {
	trimmed := strings.TrimSpace(tail)
	for _, prefix := range []string{"日线", "周线", "月线", "年线", "均线", "分钟线", "小时线"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func normalizeActivationRuleForSave(recommend *models.AiRecommendStocks) error {
	if recommend == nil {
		return nil
	}
	recommend.ActivationRuleJSON = strings.TrimSpace(recommend.ActivationRuleJSON)
	recommend.ActivationRuleVersion = strings.TrimSpace(recommend.ActivationRuleVersion)
	recommend.ActivationRuleSource = strings.TrimSpace(recommend.ActivationRuleSource)
	recommend.ActivationInvalidReason = normalizeRecommendText(recommend.ActivationInvalidReason)

	requireRule := hasSignalDrivenRecommend(recommend) || shouldUseStrictBacktestEligibility(recommend)
	if shouldUseLegacyDirectActivation(recommend) {
		requireRule = false
	}
	if !requireRule && recommend.ActivationRuleJSON == "" {
		recommend.ActivationStatus = ""
		recommend.ActivationInvalidReason = ""
		return nil
	}

	if recommend.ActivationRuleJSON != "" && isMarketSummaryActivationSource(recommend.ActivationRuleSource) {
		existingRule, parseErr := parseActivationRuleJSON(recommend.ActivationRuleJSON)
		if parseErr == nil && existingRule != nil && ((existingRule.Version != activationRuleVersionV2 && existingRule.Version != activationRuleVersionV3) || len(existingRule.Paths) == 0) {
			recommend.ActivationRuleJSON = ""
			recommend.ActivationRuleVersion = ""
		}
	}

	if recommend.ActivationRuleJSON == "" {
		rule, err := buildActivationRuleFromRecommend(recommend)
		if err != nil {
			recommend.ActivationStatus = "invalid"
			recommend.ActivationInvalidReason = err.Error()
			return err
		}
		// 设置时间戳
		setActivationRuleTimestamps(rule, *recommend, time.Now())
		raw, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		recommend.ActivationRuleJSON = string(raw)
		if recommend.ActivationRuleSource == "" {
			recommend.ActivationRuleSource = "derived"
		}
	}

	rule, err := parseActivationRuleJSON(recommend.ActivationRuleJSON)
	if err != nil {
		recommend.ActivationStatus = "invalid"
		recommend.ActivationInvalidReason = "结构化激活规则无效：" + err.Error()
		return err
	}
	if err := normalizeActivationRuleEntryBoundsForRecommend(recommend, rule); err != nil {
		recommend.ActivationStatus = "invalid"
		recommend.ActivationInvalidReason = "结构化激活规则价格边界无效：" + err.Error()
		return err
	}

	// 验证时间线（防止事后拟合）
	if err := validateActivationRuleTimelineForPaths(rule, *recommend); err != nil {
		recommend.ActivationStatus = "invalid"
		recommend.ActivationInvalidReason = "规则时间线验证失败：" + err.Error()
		return err
	}

	raw, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	recommend.ActivationRuleJSON = string(raw)
	recommend.ActivationRuleVersion = firstNonEmptyText(rule.Version, activationRuleVersionV1)
	if recommend.ActivationRuleSource == "" {
		recommend.ActivationRuleSource = "manual"
	}
	if strings.TrimSpace(recommend.ActivationStatus) == "" || strings.EqualFold(strings.TrimSpace(recommend.ActivationStatus), "invalid") {
		recommend.ActivationStatus = "pending"
	}
	recommend.ActivationInvalidReason = ""
	return nil
}

func normalizeActivationRuleEntryBoundsForRecommend(recommend *models.AiRecommendStocks, rule *activationRule) error {
	if recommend == nil || rule == nil {
		return nil
	}
	stopProfit, hasStopProfit := parseStopProfitPrice(*recommend)
	stopLoss, hasStopLoss := parseStopLossPrice(*recommend)
	normalizePath := func(path *activationRule) (bool, error) {
		if path == nil {
			return false, nil
		}
		switch strings.TrimSpace(path.SignalType) {
		case "price_range_with_volume":
			if path.ThresholdMax <= 0 {
				path.ThresholdMax = path.ThresholdValue
			}
			if path.ThresholdMax < path.ThresholdValue {
				path.ThresholdValue, path.ThresholdMax = path.ThresholdMax, path.ThresholdValue
			}
			if hasStopLoss && stopLoss > 0 && path.ThresholdValue <= stopLoss {
				return false, fmt.Errorf("回踩路径下沿 %.2f 不高于止损/失效位 %.2f", round2(path.ThresholdValue), round2(stopLoss))
			}
			if hasStopProfit && stopProfit > 0 && path.ThresholdMax >= stopProfit {
				return false, fmt.Errorf("回踩路径上沿 %.2f 不低于止盈触发价 %.2f", round2(path.ThresholdMax), round2(stopProfit))
			}
			return true, nil
		case "price_breakout_with_volume":
			if path.ThresholdValue <= 0 {
				return false, nil
			}
			if hasStopProfit && stopProfit > 0 && path.ThresholdValue >= stopProfit {
				return false, nil
			}
			maxEntry, ok := resolveBreakoutMaxEntryPrice(path.ThresholdValue, stopProfit)
			if !ok {
				return false, nil
			}
			if path.ThresholdMax <= 0 || path.ThresholdMax < path.ThresholdValue || (hasStopProfit && path.ThresholdMax >= stopProfit) {
				path.ThresholdMax = maxEntry
			}
			if path.ThresholdMax < path.ThresholdValue {
				return false, nil
			}
			if hasStopProfit && stopProfit > 0 && path.ThresholdMax >= stopProfit {
				return false, nil
			}
			if hasStopLoss && stopLoss > 0 && path.ThresholdValue <= stopLoss {
				return false, nil
			}
			return true, nil
		default:
			return false, fmt.Errorf("不支持的 signalType: %s", strings.TrimSpace(path.SignalType))
		}
	}

	if len(rule.Paths) > 0 {
		normalized := make([]activationRule, 0, len(rule.Paths))
		var lastErr error
		for i := range rule.Paths {
			path := rule.Paths[i]
			keep, err := normalizePath(&path)
			if err != nil {
				lastErr = err
				continue
			}
			if keep {
				normalized = append(normalized, path)
			}
		}
		if len(normalized) == 0 {
			if lastErr != nil {
				return lastErr
			}
			return errors.New("没有可执行的激活路径")
		}
		rule.Paths = normalized
		return nil
	}
	keep, err := normalizePath(rule)
	if err != nil {
		return err
	}
	if !keep {
		return errors.New("激活规则没有可执行价格空间")
	}
	return nil
}

func resolveBreakoutMaxEntryPrice(threshold, stopProfit float64) (float64, bool) {
	threshold = round2(threshold)
	if threshold <= 0 {
		return 0, false
	}
	maxEntry := threshold * breakoutMaxEntryChaseRatio
	if stopProfit > 0 {
		stopProfitLimit := stopProfit * breakoutStopProfitSafetyRatio
		if stopProfitLimit < maxEntry {
			maxEntry = stopProfitLimit
		}
	}
	maxEntry = round2(maxEntry)
	if maxEntry < threshold {
		return 0, false
	}
	return maxEntry, true
}

func buildActivationRuleFromRecommend(recommend *models.AiRecommendStocks) (*activationRule, error) {
	if recommend == nil {
		return nil, errors.New("推荐记录为空")
	}
	combined := normalizeRecommendText(strings.Join([]string{
		recommend.BuySignal,
		recommend.BuySignalDetail,
	}, "\n"))
	if combined == "" {
		return nil, errors.New("缺少买入信号，无法生成结构化激活规则")
	}
	if !shouldBypassRecommendKeywordInterception(recommend.DataTime) {
		if phrase := findAmbiguousTriggerPhrase(combined); phrase != "" {
			return nil, fmt.Errorf("买入信号包含未量化表述“%s”，必须改成可机械执行的阈值条件", phrase)
		}
	}
	minPrice, maxPrice, ok := parseRecommendEntryRange(*recommend)
	if !ok || minPrice <= 0 || maxPrice <= 0 {
		return nil, errors.New("缺少可解析买入区间，无法生成结构化激活规则")
	}
	evaluationWindow := resolveActivationEvaluationWindow(combined)
	rule := &activationRule{
		Version:            activationRuleVersionV1,
		SignalType:         "price_range_with_volume",
		EvaluationWindow:   evaluationWindow,
		Baseline:           "avg_amount_5x5m",
		Operator:           ">=",
		ThresholdValue:     1,
		ThresholdMax:       round2(maxPrice),
		VolumeRatio:        1,
		ConfirmBars:        1,
		VolumeWindow:       5,
		VolumeMetric:       "amount",
		ExpireTradeDays:    recommendPendingActivationMaxTradeDays,
		VolumeBaselineType: "percentile", // 默认使用分位数方法
		VolumePercentile:   70,           // 默认70%分位数
	}
	rule.ThresholdValue = round2(minPrice)
	if isMarketSummaryActivationSource(recommend.ActivationRuleSource) {
		rule.Version = activationRuleVersionV3
		rule.OpeningPolicy = buildDefaultActivationOpeningPolicy(recommend, minPrice, maxPrice)
	}

	if matches := activationVolumeRatioRegexp.FindAllStringSubmatch(combined, -1); len(matches) > 0 {
		for _, match := range matches {
			if len(match) < 5 {
				continue
			}
			if v := firstPositiveFloat(match[3], match[4]); v > 0 {
				rule.Baseline = buildActivationBaselineFromText(combined)
				rule.Operator = ">="
				rule.VolumeMetric = resolveActivationVolumeMetric(combined)
				rule.VolumeWindow = firstPositiveInt(match[1], match[2], "5")
				rule.ConfirmBars = resolveActivationConfirmBars(combined)
				rule.EvaluationWindow = evaluationWindow
				rule.ExpireTradeDays = resolveActivationExpireTradeDays(combined)
				rule.SignalType = "price_range_with_volume"
				rule.VolumeRatio = round2(v)
				break
			}
		}
	}

	if strings.Contains(combined, "上一交易日") || strings.Contains(combined, "前一交易日") {
		rule.Baseline = "prev_day_same_slot_amount"
	}
	if v, ok := extractActivationBreakoutThreshold(combined); ok {
		rule.SignalType = "price_breakout_with_volume"
		rule.ThresholdValue = v
		rule.ThresholdMax = 0
	}
	rule.ConfirmBars = resolveActivationConfirmBars(combined)
	rule.ExpireTradeDays = resolveActivationExpireTradeDays(combined)
	if isMarketSummaryActivationSource(recommend.ActivationRuleSource) {
		return buildMarketSummaryDualActivationRule(recommend, combined, *rule, minPrice, maxPrice), nil
	}
	return rule, nil
}

func buildMarketSummaryDualActivationRule(recommend *models.AiRecommendStocks, combined string, pullback activationRule, buyMin, buyMax float64) *activationRule {
	sourceVolumeRatio := pullback.VolumeRatio
	defaultOpeningPolicy := buildDefaultActivationOpeningPolicy(recommend, buyMin, buyMax)
	pullback.Name = "pullback"
	pullback.Version = ""
	pullback.Mode = ""
	pullback.Paths = nil
	pullback.OpeningPolicy = defaultOpeningPolicy
	pullback.SignalType = "price_range_with_volume"
	pullback.ThresholdValue = round2(buyMin)
	pullback.ThresholdMax = round2(buyMax)
	if pullback.ConfirmBars > 1 {
		pullback.ConfirmBars = 1
	}
	if pullback.VolumeRatio <= 0 {
		pullback.VolumeRatio = 1
	}
	if pullback.VolumeRatio > 1.15 {
		pullback.VolumeRatio = 1.15
	}

	breakoutThreshold := buyMax
	if v, ok := extractActivationBreakoutThreshold(combined); ok {
		breakoutThreshold = v
	}
	refPrice := resolveRecommendReferencePrice(*recommend)
	if refPrice > 0 && breakoutThreshold < refPrice {
		breakoutThreshold = round2(refPrice)
	}
	if breakoutThreshold < buyMax {
		breakoutThreshold = buyMax
	}
	if breakoutThreshold <= 0 {
		breakoutThreshold = buyMax
	}

	breakout := activationRule{
		Name:             "breakout",
		OpeningPolicy:    defaultOpeningPolicy,
		SignalType:       "price_breakout_with_volume",
		EvaluationWindow: pullback.EvaluationWindow,
		Baseline:         pullback.Baseline,
		Operator:         ">=",
		ThresholdValue:   round2(breakoutThreshold),
		VolumeRatio:      sourceVolumeRatio,
		ConfirmBars:      1,
		VolumeWindow:     pullback.VolumeWindow,
		VolumeMetric:     pullback.VolumeMetric,
		ExpireTradeDays:  pullback.ExpireTradeDays,
	}
	if breakout.VolumeRatio < 1.05 {
		breakout.VolumeRatio = 1.05
	}
	if breakout.VolumeRatio > 1.2 {
		breakout.VolumeRatio = 1.2
	}

	if refPrice > 0 && breakout.ThresholdValue > refPrice*1.12 {
		breakout.ThresholdValue = round2(refPrice * 1.12)
	}
	if breakout.ThresholdValue < buyMax {
		breakout.ThresholdValue = buyMax
	}
	if stopProfit, ok := parseStopProfitPrice(*recommend); ok && stopProfit > 0 {
		if maxEntry, ok := resolveBreakoutMaxEntryPrice(breakout.ThresholdValue, stopProfit); ok {
			breakout.ThresholdMax = maxEntry
		}
	}

	paths := []activationRule{pullback}
	if breakout.ThresholdMax >= breakout.ThresholdValue {
		paths = append(paths, breakout)
	}
	return &activationRule{
		Version: activationRuleVersionV3,
		Mode:    activationRuleModeAnyOf,
		Paths:   paths,
	}
}

func normalizeActivationOpeningPolicy(policy *activationOpeningPolicy) *activationOpeningPolicy {
	if policy == nil {
		return nil
	}
	normalized := *policy
	if strings.TrimSpace(normalized.MorningBufferUntil) == "" {
		normalized.MorningBufferUntil = openingReviewPhase0940
	}
	if strings.TrimSpace(normalized.GapBelowStopAction) == "" {
		normalized.GapBelowStopAction = "skip"
	}
	if strings.TrimSpace(normalized.GapAboveMaxChaseAction) == "" {
		normalized.GapAboveMaxChaseAction = "skip"
	}
	if strings.TrimSpace(normalized.OpenInsideBuyRangeAction) == "" {
		normalized.OpenInsideBuyRangeAction = "wait_buffer"
	}
	if strings.TrimSpace(normalized.OpenBetweenRangeAndBreakoutAction) == "" {
		normalized.OpenBetweenRangeAndBreakoutAction = "wait_buffer"
	}
	if strings.TrimSpace(normalized.OpenBetweenBreakoutAndMaxChaseAction) == "" {
		normalized.OpenBetweenBreakoutAndMaxChaseAction = "wait_buffer"
	}
	return &normalized
}

func buildDefaultActivationOpeningPolicy(recommend *models.AiRecommendStocks, buyMin, buyMax float64) *activationOpeningPolicy {
	refPrice := 0.0
	if recommend != nil {
		refPrice = resolveRecommendReferencePrice(*recommend)
	}
	maxChase := buyMax
	if refPrice > 0 && refPrice*1.03 > maxChase {
		maxChase = refPrice * 1.03
	}
	if buyMax > 0 && buyMax*1.015 > maxChase {
		maxChase = buyMax * 1.015
	}
	if maxChase <= 0 && buyMin > 0 {
		maxChase = buyMin * 1.015
	}
	return normalizeActivationOpeningPolicy(&activationOpeningPolicy{
		MorningBufferUntil:                   openingReviewPhase0940,
		MaxChasePrice:                        round2(maxChase),
		SameDayOnly:                          true,
		GapBelowStopAction:                   "skip",
		GapAboveMaxChaseAction:               "skip",
		OpenInsideBuyRangeAction:             "wait_buffer",
		OpenBetweenRangeAndBreakoutAction:    "wait_buffer",
		OpenBetweenBreakoutAndMaxChaseAction: "wait_buffer",
	})
}

func hasActivationOpeningPolicy(rule *activationRule) bool {
	if rule == nil {
		return false
	}
	if rule.OpeningPolicy != nil {
		return true
	}
	for _, path := range rule.Paths {
		if path.OpeningPolicy != nil {
			return true
		}
	}
	return false
}

func resolveActivationOpeningPolicy(rule *activationRule) *activationOpeningPolicy {
	if rule == nil {
		return nil
	}
	if rule.OpeningPolicy != nil {
		return normalizeActivationOpeningPolicy(rule.OpeningPolicy)
	}
	for _, path := range activationRulePaths(rule) {
		if path.OpeningPolicy != nil {
			return normalizeActivationOpeningPolicy(path.OpeningPolicy)
		}
	}
	return nil
}

func buildActivationBaselineFromText(text string) string {
	switch {
	case strings.Contains(text, "上一交易日"), strings.Contains(text, "前一交易日"):
		return "prev_day_same_slot_amount"
	case strings.Contains(text, "均额"):
		return "avg_amount_5x5m"
	default:
		return "avg_volume_5x5m"
	}
}

func resolveActivationVolumeMetric(text string) string {
	if strings.Contains(text, "成交额") || strings.Contains(text, "均额") {
		return "amount"
	}
	return "volume"
}

func resolveActivationEvaluationWindow(text string) string {
	matches := activationEvalWindowRegexp.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		switch strings.TrimSpace(match[1]) {
		case "1":
			return "1m"
		case "5":
			return "5m"
		case "15":
			return "15m"
		case "30":
			return "30m"
		case "60":
			return "60m"
		}
	}
	return "5m"
}

func normalizeActivationEvaluationWindow(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "1m", "1min", "1minute":
		return "1m"
	case "5m", "5min", "5minute":
		return "5m"
	case "15m", "15min", "15minute":
		return "15m"
	case "30m", "30min", "30minute":
		return "30m"
	case "60m", "60min", "60minute":
		return "60m"
	default:
		return "5m"
	}
}

func resolveActivationConfirmBars(text string) int {
	matches := activationConfirmBarsRegexp.FindStringSubmatch(text)
	if len(matches) >= 2 {
		if v, err := strconv.Atoi(matches[1]); err == nil && v > 0 {
			return v
		}
	}
	return 1
}

func resolveActivationExpireTradeDays(text string) int {
	matches := activationExpireRegexp.FindStringSubmatch(text)
	if len(matches) >= 3 {
		return firstPositiveInt(matches[2], matches[1], strconv.Itoa(recommendPendingActivationMaxTradeDays))
	}
	return recommendPendingActivationMaxTradeDays
}

func firstPositiveFloat(values ...string) float64 {
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err == nil && v > 0 {
			return v
		}
	}
	return 0
}

func firstPositiveInt(values ...string) int {
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err == nil && v > 0 {
			return v
		}
	}
	return 0
}

func resolveActivationRuleScan(rec models.AiRecommendStocks, bars []minuteBar) activationScanResult {
	rule, err := parseActivationRuleJSON(rec.ActivationRuleJSON)
	if err != nil {
		return activationScanResult{Reason: "结构化激活规则无效：" + err.Error()}
	}
	if err := normalizeActivationRuleEntryBoundsForRecommend(&rec, rule); err != nil {
		return activationScanResult{Reason: "结构化激活规则价格边界无效：" + err.Error()}
	}
	paths := activationRulePaths(rule)
	if len(paths) == 0 {
		return activationScanResult{Reason: "结构化激活规则为空"}
	}
	best := activationScanResult{}
	reasons := make([]string, 0, len(paths))
	for _, path := range paths {
		scan := resolveSingleActivationRuleScan(rec, &path, bars)
		if scan.Triggered {
			if !best.Triggered || scan.Time.Before(best.Time) {
				best = scan
			}
			continue
		}
		if strings.TrimSpace(scan.Reason) != "" {
			reasons = append(reasons, scan.Reason)
		}
	}
	if best.Triggered {
		return best
	}
	return activationScanResult{Reason: firstNonEmptyText(strings.Join(dedupeNonEmptyStrings(reasons, 3), "；"), "未触发结构化激活规则")}
}

func resolveActivationRuleScanWithActivationGate(rec models.AiRecommendStocks, bars []minuteBar) activationScanResult {
	// 1.5.0 has its own deterministic execution engine.  Its activation,
	// sizing and portfolio checks are performed by strategy/v150 and must not
	// be passed through the legacy V1.3.6 VWAP/reward-risk gate.
	if strings.TrimSpace(rec.SummaryVersion) == marketSummaryVersion150 {
		return resolveActivationRuleScan(rec, bars)
	}
	remaining := bars
	reasons := make([]string, 0, 4)
	for len(remaining) > 0 {
		scan := resolveActivationRuleScan(rec, remaining)
		if !scan.Triggered {
			if strings.TrimSpace(scan.Reason) != "" {
				reasons = append(reasons, scan.Reason)
			}
			break
		}
		gate := evaluateV132ActivationGate(rec, scan.Time, scan.Price, bars)
		if gate.Allowed {
			return scan
		}
		if shouldContinueActivationScanAfterGate(rec, gate) {
			reasons = append(reasons, gate.Reason)
			next := minuteBarsAfterTime(remaining, scan.Time)
			if len(next) >= len(remaining) {
				break
			}
			remaining = next
			continue
		}
		scan.Triggered = false
		scan.Blocked = true
		scan.Reason = gate.Reason
		return scan
	}
	return activationScanResult{Reason: firstNonEmptyText(strings.Join(dedupeNonEmptyStrings(reasons, 3), "；"), "未触发结构化激活规则")}
}

func shouldContinueActivationScanAfterGate(rec models.AiRecommendStocks, gate v132ActivationGateResult) bool {
	if !isV136Recommend(rec) {
		return false
	}
	return strings.TrimSpace(gate.Kind) == "strength"
}

func minuteBarsAfterTime(bars []minuteBar, cutoff time.Time) []minuteBar {
	if cutoff.IsZero() {
		return nil
	}
	for idx, bar := range bars {
		if bar.TradeTime.After(cutoff) {
			return bars[idx:]
		}
	}
	return nil
}

func resolveSingleActivationRuleScan(rec models.AiRecommendStocks, rule *activationRule, bars []minuteBar) activationScanResult {
	if rule == nil {
		return activationScanResult{Reason: "结构化激活规则为空"}
	}
	grouped := aggregateMinuteBarsByWindow(bars, rule.EvaluationWindow)
	switch rule.SignalType {
	case "price_breakout_with_volume":
		scan := scanActivationByBreakoutRule(rule, grouped)
		if scan.Triggered && strings.TrimSpace(rule.Name) != "" {
			scan.Reason = rule.Name
		}
		return scan
	case "price_range_with_volume":
		if strings.TrimSpace(rec.SummaryVersion) == marketSummaryVersion150 && strings.EqualFold(strings.TrimSpace(rule.Name), "pullback") {
			return scanMarketSummaryV150PullbackRule(rule, grouped)
		}
		triggerMin := rule.ThresholdValue
		triggerMax := rule.ThresholdMax
		if triggerMin > 0 {
			if triggerMax <= 0 {
				triggerMax = triggerMin
			}
			rawText := strings.TrimSpace(rec.RecommendBuyPrice)
			if shouldPreferTextResolvedBuyRange(rawText, triggerMin, triggerMax) {
				textMin, okMin := parsePriceMinFromText(rawText)
				textMax, okMax := parsePriceMaxFromText(rawText)
				if okMin && okMax && textMin > 0 && textMax > 0 {
					if textMin > textMax {
						textMin, textMax = textMax, textMin
					}
					triggerMin = textMin
					triggerMax = textMax
				}
			}
		}
		scan := scanActivationByRangeRule(rule, grouped, triggerMin, triggerMax)
		if scan.Triggered && strings.TrimSpace(rule.Name) != "" {
			scan.Reason = rule.Name
		}
		return scan
	default:
		return activationScanResult{Reason: "暂不支持的结构化激活规则类型"}
	}
}

func scanMarketSummaryV150PullbackRule(rule *activationRule, bars []minuteBar) activationScanResult {
	if rule == nil {
		return activationScanResult{Reason: "v1.5 pullback rule is unavailable"}
	}
	if normalizeActivationEvaluationWindow(rule.EvaluationWindow) != "15m" {
		return activationScanResult{Reason: "v1.5 pullback rule requires a 15m evaluation window"}
	}
	entryMin, entryMax := rule.ThresholdValue, rule.ThresholdMax
	if entryMin <= 0 || entryMax <= 0 || entryMax < entryMin || rule.Support <= 0 {
		return activationScanResult{Reason: "v1.5 pullback rule requires explicit entry bounds and support"}
	}

	zoneTouched := false
	for _, bar := range bars {
		if bar.TradeTime.IsZero() || (!rule.ValidFrom.IsZero() && bar.TradeTime.Before(rule.ValidFrom)) {
			continue
		}
		if bar.High >= entryMin && entryMax >= bar.Low {
			zoneTouched = true
		}
		if !zoneTouched || bar.Close < rule.Support {
			continue
		}
		return activationScanResult{
			Triggered: true,
			Time:      bar.TradeTime,
			Price:     round2(bar.Close),
			Reason:    "completed_15m_recovery",
		}
	}
	return activationScanResult{Reason: "v1.5 pullback has not completed zone-touch and support recovery"}
}

func activationRulePaths(rule *activationRule) []activationRule {
	if rule == nil {
		return nil
	}
	if len(rule.Paths) > 0 {
		return rule.Paths
	}
	return []activationRule{*rule}
}

func scanActivationByRangeRule(rule *activationRule, bars []minuteBar, triggerMin, triggerMax float64) activationScanResult {
	if rule == nil || len(bars) == 0 {
		return activationScanResult{}
	}
	if triggerMin <= 0 {
		return activationScanResult{Reason: "结构化激活规则缺少主买入区下沿"}
	}
	if triggerMax <= 0 {
		triggerMax = triggerMin
	}
	confirmNeed := rule.ConfirmBars
	if confirmNeed <= 0 {
		confirmNeed = 1
	}
	streak := 0
	lastVolumeReason := ""
	for idx, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		if bar.Low > triggerMax || bar.High < triggerMin {
			streak = 0
			continue
		}
		passed, reason := passesVolumeRule(rule, bars, idx)
		if !passed {
			streak = 0
			if strings.TrimSpace(reason) != "" {
				lastVolumeReason = reason
			}
			continue
		}
		streak++
		if streak < confirmNeed {
			continue
		}
		price := bar.Close
		if price <= 0 {
			price = bar.Open
		}
		if price < triggerMin {
			price = triggerMin
		}
		if price > triggerMax {
			price = triggerMax
		}
		return activationScanResult{
			Triggered: true,
			Time:      bar.TradeTime,
			Price:     round2(price),
		}
	}
	if strings.TrimSpace(lastVolumeReason) != "" {
		return activationScanResult{Reason: lastVolumeReason}
	}
	return activationScanResult{Reason: "未触发结构化激活规则"}
}

func scanActivationByBreakoutRule(rule *activationRule, bars []minuteBar) activationScanResult {
	if rule == nil || len(bars) == 0 {
		return activationScanResult{}
	}
	maxEntry := round2(rule.ThresholdMax)
	confirmNeed := rule.ConfirmBars
	if confirmNeed <= 0 {
		confirmNeed = 1
	}
	streak := 0
	lastVolumeReason := ""
	lastChaseReason := ""
	for idx, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		price := bar.Close
		if price <= 0 {
			price = bar.Open
		}
		if price < rule.ThresholdValue {
			streak = 0
			continue
		}
		if maxEntry > 0 && price > maxEntry {
			streak = 0
			lastChaseReason = fmt.Sprintf(
				"突破已发生但收盘价 %.2f 超过追价上限 %.2f",
				round2(price),
				round2(maxEntry),
			)
			continue
		}
		passed, reason := passesVolumeRule(rule, bars, idx)
		if !passed {
			streak = 0
			if strings.TrimSpace(reason) != "" {
				lastVolumeReason = reason
			}
			continue
		}
		streak++
		if streak < confirmNeed {
			continue
		}
		if price < rule.ThresholdValue {
			price = rule.ThresholdValue
		}
		return activationScanResult{
			Triggered: true,
			Time:      bar.TradeTime,
			Price:     round2(price),
		}
	}
	if strings.TrimSpace(lastVolumeReason) != "" {
		return activationScanResult{Reason: lastVolumeReason}
	}
	if strings.TrimSpace(lastChaseReason) != "" {
		return activationScanResult{Reason: lastChaseReason}
	}
	return activationScanResult{Reason: "未触发结构化激活规则"}
}

func passesVolumeRule(rule *activationRule, bars []minuteBar, idx int) (bool, string) {
	if rule == nil {
		return false, ""
	}

	baselineType := strings.TrimSpace(rule.VolumeBaselineType)
	if baselineType == "percentile" || baselineType == "adaptive" {
		return passesVolumeRuleWithPercentile(rule, bars, idx)
	}
	return passesTraditionalVolumeRule(rule, bars, idx)
}

func passesTraditionalVolumeRule(rule *activationRule, bars []minuteBar, idx int) (bool, string) {
	if rule == nil {
		return false, ""
	}

	window := rule.VolumeWindow
	if window <= 0 {
		window = 5
	}
	if idx < 0 || idx >= len(bars) {
		return false, ""
	}
	currentMetric := minuteMetricValue(rule, bars[idx])
	if currentMetric <= 0 {
		return false, ""
	}
	switch strings.TrimSpace(rule.Baseline) {
	case "prev_day_same_slot_amount":
		prevValue := findPrevTradingSameSlotMetric(rule, bars, idx)
		if prevValue <= 0 {
			return false, "已进入主买入区，但缺少上一交易日活跃度基准"
		}
		target := prevValue * maxFloat(rule.VolumeRatio, 1)
		if currentMetric >= target {
			return true, ""
		}
		return false, fmt.Sprintf(
			"已进入主买入区，但%s %.2f 低于上一交易日同一时刻 %.2f",
			activationRuleMetricLabel(rule),
			round2(currentMetric),
			round2(target),
		)
	case "avg_amount_5x5m", "avg_volume_5x5m":
		start := idx - window
		if start < 0 {
			start = 0
		}
		sum := 0.0
		count := 0
		for i := start; i < idx; i++ {
			value := minuteMetricValue(rule, bars[i])
			if value <= 0 {
				continue
			}
			sum += value
			count++
		}
		if count <= 0 {
			return false, ""
		}
		avg := sum / float64(count)
		ratio := currentMetric / avg
		if ratio >= rule.VolumeRatio {
			return true, ""
		}
		return false, fmt.Sprintf(
			"%s %.2f 未达到近%d个%s均值 %.2f 的 %.2f 倍",
			activationRuleMetricLabel(rule),
			round2(currentMetric),
			count,
			normalizeActivationEvaluationWindow(rule.EvaluationWindow),
			round2(avg),
			round2(rule.VolumeRatio),
		)
	default:
		return currentMetric > 0, ""
	}
}

func findPrevTradingSameSlotMetric(rule *activationRule, bars []minuteBar, idx int) float64 {
	if idx < 0 || idx >= len(bars) {
		return 0
	}
	current := bars[idx].TradeTime.In(cnLocation())
	prevTradeDay := shiftToPrevCNOpenTradeDay(time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, cnLocation()).AddDate(0, 0, -1))
	currentSlotIndex := bars[idx].SlotIndex
	currentSlotWindow := bars[idx].SlotWindow
	for i := idx - 1; i >= 0; i-- {
		candidate := bars[i].TradeTime.In(cnLocation())
		if candidate.Year() != prevTradeDay.Year() || candidate.Month() != prevTradeDay.Month() || candidate.Day() != prevTradeDay.Day() {
			continue
		}
		if currentSlotWindow > 1 && bars[i].SlotWindow == currentSlotWindow && bars[i].SlotIndex == currentSlotIndex {
			return minuteMetricValue(rule, bars[i])
		}
		if candidate.Hour() == current.Hour() && candidate.Minute() == current.Minute() {
			return minuteMetricValue(rule, bars[i])
		}
	}
	return 0
}

func minuteMetricValue(rule *activationRule, bar minuteBar) float64 {
	if rule != nil && strings.TrimSpace(rule.VolumeMetric) == "volume" {
		return bar.Volume
	}
	if bar.Amount > 0 {
		return bar.Amount
	}
	return bar.Volume
}

func aggregateMinuteBarsByWindow(bars []minuteBar, window string) []minuteBar {
	size := activationWindowMinutes(window)
	if size <= 1 || len(bars) == 0 {
		return bars
	}
	type bucketKey struct {
		day  string
		slot int
	}
	result := make([]minuteBar, 0, len(bars))
	index := map[bucketKey]int{}
	loc := cnLocation()
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		t := bar.TradeTime.In(loc)
		minuteOfDay := t.Hour()*60 + t.Minute()
		key := bucketKey{
			day:  t.Format("2006-01-02"),
			slot: minuteOfDay / size,
		}
		if pos, ok := index[key]; ok {
			current := &result[pos]
			if current.Open <= 0 {
				current.Open = bar.Open
			}
			if bar.High > current.High {
				current.High = bar.High
			}
			if current.Low <= 0 || (bar.Low > 0 && bar.Low < current.Low) {
				current.Low = bar.Low
			}
			current.Close = bar.Close
			current.SlotIndex = key.slot
			current.SlotWindow = size
			if bar.TradeTime.After(current.TradeTime) {
				current.TradeTime = bar.TradeTime
			}
			current.Volume += bar.Volume
			current.Amount += bar.Amount
			continue
		}
		index[key] = len(result)
		result = append(result, minuteBar{
			TradeTime:  bar.TradeTime,
			Open:       bar.Open,
			High:       bar.High,
			Low:        bar.Low,
			Close:      bar.Close,
			Volume:     bar.Volume,
			Amount:     bar.Amount,
			SlotIndex:  key.slot,
			SlotWindow: size,
		})
	}
	return result
}

func activationRuleMetricLabel(rule *activationRule) string {
	window := normalizeActivationEvaluationWindow("")
	if rule != nil {
		window = normalizeActivationEvaluationWindow(rule.EvaluationWindow)
	}
	windowLabel := activationWindowLabel(window)
	if rule != nil && strings.TrimSpace(rule.VolumeMetric) == "volume" {
		return windowLabel + "成交量"
	}
	return windowLabel + "成交额"
}

func activationWindowLabel(window string) string {
	switch normalizeActivationEvaluationWindow(window) {
	case "1m":
		return "1分钟"
	case "5m":
		return "5分钟"
	case "15m":
		return "15分钟"
	case "30m":
		return "30分钟"
	case "60m":
		return "60分钟"
	default:
		return "分钟"
	}
}

func activationWindowMinutes(raw string) int {
	switch normalizeActivationEvaluationWindow(raw) {
	case "1m":
		return 1
	case "5m":
		return 5
	case "15m":
		return 15
	case "30m":
		return 30
	case "60m":
		return 60
	default:
		return 5
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
