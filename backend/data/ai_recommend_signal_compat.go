package data

import (
	"errors"
	"fmt"
	"go-stock/backend/models"
	"regexp"
	"strings"
)

func fillSignalDrivenRecommendCompat(recommend *models.AiRecommendStocks, signalDrivenMode bool, structuredMode bool) {
	if recommend == nil || !signalDrivenMode {
		return
	}
	if isAnalysisOnlyRecommend(recommend) {
		return
	}
	if buySignal := normalizeRecommendText(recommend.BuySignal); buySignal != "" &&
		!hasRequiredStructuredPlanSection(buySignal, []string{"价格触发：", "量能触发："}) {
		if strings.TrimSpace(recommend.RecommendReason) == "" && strings.TrimSpace(recommend.BuySignalDetail) == "" {
			recommend.BuySignalDetail = buySignal
		}
		recommend.BuySignal = ""
	}
	if recommend.InvalidSignal == "" {
		recommend.InvalidSignal = firstNonEmptyText(recommend.InvalidCondition, recommend.InvalidSignal)
	}
	if invalidSignal := normalizeRecommendText(recommend.InvalidSignal); invalidSignal != "" &&
		!hasRequiredStructuredPlanSection(invalidSignal, []string{"时间失效：", "价格失效："}) {
		if strings.TrimSpace(recommend.InvalidCondition) == "" {
			recommend.InvalidCondition = invalidSignal
		}
		recommend.InvalidSignal = ""
	}
	if recommend.ExecutionState == "" {
		recommend.ExecutionState = inferExecutionStateFromSignals(recommend.BuySignal, recommend.BuySignalDetail)
	}
	recommend.ExecutionState = recommendExecutionConditional
	if recommend.RecommendCategory != "avoid" {
		recommend.RecommendCategory = recommendExecutionConditional
	}
	if recommend.BuySignal == "" {
		recommend.BuySignal = buildCompatBuySignal(recommend.ExecutionState, recommend.RecommendBuyPrice)
	}
	if recommend.SellSignal == "" {
		recommend.SellSignal = buildCompatSellSignal(recommend.RecommendStopProfitPrice, recommend.RecommendStopLossPrice)
	}
	if recommend.InvalidSignal == "" {
		recommend.InvalidSignal = buildCompatInvalidSignal(recommend.RecommendStopLossPrice)
	}
	if recommend.BuySignalDetail == "" {
		recommend.BuySignalDetail = buildCompatBuySignalDetail(recommend.Remarks, shouldBypassRecommendKeywordInterception(recommend.DataTime))
	}
	if recommend.SellSignalDetail == "" {
		recommend.SellSignalDetail = buildCompatSellSignalDetail(recommend.Remarks, shouldBypassRecommendKeywordInterception(recommend.DataTime))
	}
	if !structuredMode && hasSignalDrivenRecommend(recommend) {
		recommend.RecommendCategory = ""
		recommend.RecommendStatus = ""
		recommend.FocusPrice = ""
	}
}

func inferExecutionStateFromSignals(buySignal, buyDetail string) string {
	text := normalizeRecommendText(strings.TrimSpace(buySignal + "\n" + buyDetail))
	if text == "" {
		return ""
	}
	return recommendExecutionConditional
}

func buildCompatBuySignal(executionState string, buyRange string) string {
	rangeText := strings.TrimSpace(buyRange)
	if rangeText == "" {
		rangeText = "主买入区"
	}
	return "价格触发：未来3-5个交易日内股价进入" + rangeText + "主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍"
}

func buildCompatBuySignalDetail(remarks string, bypassKeywordInterception bool) string {
	lines := strings.Split(normalizeRecommendText(remarks), "\n")
	matched := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if recommendActionKeywordRegexp.MatchString(line) {
			if !bypassKeywordInterception {
				if phrase := findAmbiguousTriggerPhrase(line); phrase != "" && !quantifiedThresholdRegexp.MatchString(line) {
					continue
				}
			}
			if !bypassKeywordInterception && hasVolumeSignal(line) && !hasCompleteVolumeContext(line) {
				continue
			}
			matched = append(matched, line)
		}
	}
	return strings.Join(matched, "\n")
}

func buildCompatSellSignal(stopProfit, stopLoss string) string {
	profit := strings.TrimSpace(stopProfit)
	loss := strings.TrimSpace(stopLoss)
	switch {
	case profit != "" && loss != "":
		return "触及" + profit + "止盈区间卖出；若跌破" + loss + "止损位立即止损"
	case profit != "":
		return "触及" + profit + "止盈区间卖出"
	case loss != "":
		return "跌破" + loss + "止损位立即止损"
	default:
		return ""
	}
}

func buildCompatSellSignalDetail(remarks string, bypassKeywordInterception bool) string {
	return buildCompatBuySignalDetail(remarks, bypassKeywordInterception)
}

func buildCompatInvalidSignal(stopLoss string) string {
	stopLoss = strings.TrimSpace(stopLoss)
	if stopLoss == "" {
		return ""
	}
	return "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破" + stopLoss
}

func validateSignalDrivenRecommend(recommend *models.AiRecommendStocks) error {
	if recommend == nil || !hasSignalDrivenRecommend(recommend) {
		return nil
	}
	if recommend.ExecutionState != recommendExecutionConditional {
		return errors.New("执行状态只能是 conditional/等待激活")
	}
	if strings.TrimSpace(recommend.BuySignal) == "" {
		return errors.New("买入信号不能为空")
	}
	if strings.TrimSpace(recommend.SellSignal) == "" {
		return errors.New("卖出信号不能为空")
	}
	if strings.TrimSpace(recommend.InvalidSignal) == "" {
		return errors.New("失效信号不能为空")
	}
	buyCombined := normalizeRecommendText(strings.TrimSpace(recommend.BuySignal + "\n" + recommend.BuySignalDetail))
	sellCombined := normalizeRecommendText(strings.TrimSpace(recommend.SellSignal + "\n" + recommend.SellSignalDetail))
	invalidCombined := normalizeRecommendText(strings.TrimSpace(recommend.InvalidSignal + "\n" + recommend.InvalidCondition))
	if !containsConditionalCue(buyCombined) {
		return errors.New("条件触发型记录的买入信号必须明确说明触发条件")
	}
	if !hasRequiredStructuredPlanSection(buyCombined, []string{"价格触发：", "量能触发："}) {
		return errors.New("买入信号必须严格包含“价格触发 / 量能触发”两段")
	}
	if !hasRequiredStructuredPlanSection(invalidCombined, []string{"时间失效：", "价格失效："}) {
		return errors.New("失效信号必须严格包含“时间失效 / 价格失效”两段")
	}
	if hasVolumeSignal(buyCombined) && !hasCompleteVolumeContext(buyCombined) {
		return errors.New("买入信号中的量能条件必须写清锚点价位、比较基准、观察周期和触发阈值")
	}
	if hasVolumeSignal(sellCombined) && !hasCompleteVolumeContext(sellCombined) {
		return errors.New("卖出信号中的量能条件必须写清锚点价位、比较基准、观察周期和触发阈值")
	}
	if hasVolumeSignal(invalidCombined) && !hasCompleteVolumeContext(invalidCombined) && !invalidSignalCanReferenceActivationRule(recommend, invalidCombined) {
		return errors.New("失效信号中的量能条件必须写清锚点价位、比较基准、观察周期和触发阈值")
	}
	if !shouldBypassRecommendKeywordInterception(recommend.DataTime) {
		if phrase := findAmbiguousTriggerPhrase(buyCombined); phrase != "" {
			return fmt.Errorf("买入信号包含未量化表述“%s”，必须改成可机械执行的阈值条件", phrase)
		}
		if phrase := findAmbiguousTriggerPhrase(invalidCombined); phrase != "" {
			return fmt.Errorf("失效信号包含未量化表述“%s”，必须改成可机械执行的阈值条件", phrase)
		}
	}
	return nil
}

func invalidSignalCanReferenceActivationRule(recommend *models.AiRecommendStocks, invalidCombined string) bool {
	if recommend == nil || !hasMachineActivationRule(recommend) {
		return false
	}
	text := normalizeRecommendText(invalidCombined)
	if text == "" {
		return false
	}
	if !strings.Contains(text, "未触发") &&
		!strings.Contains(text, "未同时触发") &&
		!strings.Contains(text, "未同时满足") {
		return false
	}
	return strings.Contains(text, "价格与量能") ||
		strings.Contains(text, "价格和量能") ||
		strings.Contains(text, "价格及量能") ||
		strings.Contains(text, "量能条件")
}

func containsConditionalCue(text string) bool {
	keywords := []string{"若", "如果", "当", "等待", "触发", "进入", "回到", "站上", "站稳", "突破", "回踩", "确认后", "不破", "放量", "缩量", "承接", "企稳", "跌破"}
	return containsAnyKeyword(text, keywords)
}

func containsImmediateCue(text string) bool {
	keywords := []string{"当前可执行", "当前可买", "当前已满足", "立即买入", "可直接买入", "可直接执行", "现价可买", "现在可买"}
	return containsAnyKeyword(text, keywords)
}

func hasVolumeSignal(text string) bool {
	keywords := []string{"放量", "缩量", "量能", "成交量", "量比"}
	return containsAnyKeyword(text, keywords)
}

func hasCompleteVolumeContext(text string) bool {
	anchorPattern := regexp.MustCompile(`\d+(?:\.\d+)?(?:\s*-\s*\d+(?:\.\d+)?)?`)
	hasAnchor := anchorPattern.MatchString(text) || containsAnyKeyword(text, []string{"买入区", "压力线", "支撑线", "前高", "前低", "昨高", "昨低", "均线"})
	hasBaseline := containsAnyKeyword(text, []string{"均量", "量比", "倍", "前5", "前10", "近5", "近10", "较昨日", "较前一日", "对比"})
	hasCycle := containsAnyKeyword(text, []string{"1分钟", "5分钟", "15分钟", "30分钟", "60分钟", "日线", "周线", "小时"})
	hasThreshold := quantifiedThresholdRegexp.MatchString(text)
	return hasAnchor && hasBaseline && hasCycle && hasThreshold
}

func hasRequiredStructuredPlanSection(text string, prefixes []string) bool {
	normalized := normalizeRecommendText(text)
	if normalized == "" {
		return false
	}
	for _, prefix := range prefixes {
		if !strings.Contains(normalized, prefix) {
			return false
		}
	}
	return true
}

func findAmbiguousTriggerPhrase(text string) string {
	normalized := normalizeRecommendText(text)
	if normalized == "" {
		return ""
	}
	for _, phrase := range ambiguousTriggerPhraseList {
		if strings.Contains(normalized, phrase) {
			return phrase
		}
	}
	return ""
}
