package data

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go-stock/backend/models"
	"sort"
	"strconv"
	"strings"
	"time"
)

const legacyDirectActivationCutoffDate = "2026-04-06"

func normalizeRecommendExecutionFields(recommend *models.AiRecommendStocks) {
	if recommend == nil {
		return
	}

	remarks := make([]string, 0, 4)
	appendRemark := func(text string) {
		text = normalizeRecommendText(text)
		if strings.TrimSpace(text) == "" {
			return
		}
		for _, item := range remarks {
			if item == text {
				return
			}
		}
		remarks = append(remarks, text)
	}

	normalizePriceField := func(raw string, minVal, maxVal float64, single bool) (string, float64, float64) {
		text := strings.TrimSpace(raw)
		if recommendActionKeywordRegexp.MatchString(text) {
			appendRemark(text)
		}
		if single {
			value := 0.0
			if minVal > 0 {
				value = minVal
			}
			if value <= 0 && maxVal > 0 {
				value = maxVal
			}
			if value <= 0 {
				value, _ = parsePriceMinFromText(text)
			}
			if value <= 0 {
				return "", 0, 0
			}
			return formatRecommendPrice(value), value, value
		}

		_, normMin, normMax := normalizePriceRangeText(text, minVal, maxVal)
		if normMin <= 0 || normMax <= 0 {
			return "", 0, 0
		}
		normText := ""
		if normMin == normMax {
			normText = formatRecommendPrice(normMin)
		} else {
			normText = formatRecommendPrice(normMin) + "-" + formatRecommendPrice(normMax)
		}
		return normText, normMin, normMax
	}

	observeText, _, _ := normalizePriceField(recommend.ObservePrice, 0, 0, true)
	if observeText != "" {
		recommend.ObservePrice = observeText
	}

	if !hasSignalDrivenRecommend(recommend) {
		focusText, _, _ := normalizePriceField(recommend.FocusPrice, recommend.RecommendBuyPriceMin, recommend.RecommendBuyPriceMax, false)
		if focusText != "" {
			recommend.FocusPrice = focusText
		}
	}

	buyText, buyMin, buyMax := normalizePriceField(recommend.RecommendBuyPrice, recommend.RecommendBuyPriceMin, recommend.RecommendBuyPriceMax, false)
	if buyText != "" {
		recommend.RecommendBuyPrice = buyText
		recommend.RecommendBuyPriceMin = buyMin
		recommend.RecommendBuyPriceMax = buyMax
	}

	if !hasSignalDrivenRecommend(recommend) && recommend.FocusPrice == "" {
		recommend.FocusPrice = recommend.RecommendBuyPrice
	}

	if len(remarks) > 0 {
		merged := append([]string{}, remarks...)
		if strings.TrimSpace(recommend.Remarks) != "" {
			merged = append(merged, normalizeRecommendText(recommend.Remarks))
		}
		recommend.Remarks = strings.Join(merged, "\n")
	}
}

func normalizeRecommendText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func hasEnoughRecommendReason(reason string) bool {
	text := strings.TrimSpace(reason)
	if text == "" {
		return false
	}
	if _, ok := genericRecommendReasonTexts[text]; ok {
		return false
	}
	return len([]rune(text)) >= 12
}

func normalizeRecommendCategory(category string) string {
	raw := strings.TrimSpace(category)
	if raw == "" {
		return ""
	}
	text := strings.ToLower(raw)
	switch text {
	case "observe", "观察", "观察标的", "activated_buy", "activatedbuy", "激活买入", "条件触发", "触发买入":
		return recommendExecutionConditional
	case "low_absorb", "lowabsorb", "低吸", "低吸候选", "immediate_buy", "immediatebuy", "立刻买入":
		return recommendExecutionImmediate
	case "right_confirm", "rightconfirm", "右侧", "右侧确认", "右侧确认候选":
		return recommendExecutionConditional
	case "avoid", "回避", "回避标的":
		return "avoid"
	default:
		compact := strings.NewReplacer(
			" ", "",
			"\t", "",
			"\n", "",
			"\r", "",
			"（", "",
			"）", "",
			"(", "",
			")", "",
		).Replace(text)
		switch {
		case strings.Contains(compact, "回避"):
			return "avoid"
		case strings.Contains(compact, "立刻买入"), strings.Contains(compact, "立即买入"), strings.Contains(compact, "低吸"):
			return recommendExecutionImmediate
		case strings.Contains(compact, "激活买入"), strings.Contains(compact, "条件触发"), strings.Contains(compact, "触发买入"),
			strings.Contains(compact, "观察标的"), strings.Contains(compact, "观察"), strings.Contains(compact, "右侧确认"):
			return recommendExecutionConditional
		default:
			return raw
		}
	}
}

func normalizeRecommendStatus(status string) string {
	text := strings.TrimSpace(strings.ToLower(status))
	switch text {
	case "valid", "有效":
		return "valid"
	case "insufficient_evidence", "证据不足":
		return "insufficient_evidence"
	case "controversial", "争议", "争议状态":
		return "controversial"
	case "avoid", "回避":
		return "avoid"
	case "missing_market_data", "数据缺失", "行情缺失":
		return "missing_market_data"
	case recommendStatusPendingMarketData, "待补分钟线", "待补行情", "等待分钟线":
		return recommendStatusPendingMarketData
	default:
		return text
	}
}

func normalizeRecommendExecutionState(state string) string {
	text := strings.TrimSpace(strings.ToLower(state))
	switch text {
	case recommendExecutionImmediate, "立即买入", "立即执行", "可直接买入", "可直接执行", "立刻买入":
		return recommendExecutionConditional
	case recommendExecutionConditional, "条件触发", "等待触发", "触发后买入", "激活买入", "等待激活":
		return recommendExecutionConditional
	case recommendExecutionAnalysisOnly, "仅分析", "分析观察", "数据缺失", "行情缺失":
		return recommendExecutionAnalysisOnly
	default:
		return ""
	}
}

func isAnalysisOnlyRecommend(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	return normalizeRecommendStatus(recommend.RecommendStatus) == "missing_market_data" ||
		normalizeRecommendExecutionState(recommend.ExecutionState) == recommendExecutionAnalysisOnly
}

func isPendingMarketDataRecommend(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	return normalizeRecommendStatus(recommend.RecommendStatus) == recommendStatusPendingMarketData ||
		strings.TrimSpace(strings.ToLower(recommend.ActivationStatus)) == recommendActivationPendingData
}

func hasSignalDrivenRecommend(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	fields := []string{
		recommend.ExecutionState,
		recommend.BuySignal,
		recommend.BuySignalDetail,
		recommend.SellSignal,
		recommend.SellSignalDetail,
		recommend.InvalidSignal,
	}
	for _, item := range fields {
		if strings.TrimSpace(item) != "" {
			return true
		}
	}
	return false
}

func shouldTrackRecommendInYield(recommend *models.AiRecommendStocks) bool {
	eligibility, _ := resolveRecommendBacktestEligibility(recommend)
	return eligibility == recommendBacktestEligible
}

func appendRecommendInvalidConditionText(base, invalidCondition string) string {
	base = strings.TrimSpace(base)
	invalid := strings.TrimSpace(invalidCondition)
	if invalid == "" {
		return base
	}
	if base == "" {
		return "失效条件：" + invalid
	}
	if strings.Contains(base, invalid) {
		return base
	}
	return base + "；失效条件：" + invalid
}

func shouldDisplayRecommendInYield(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	if eligibility, _ := resolveRecommendBacktestEligibility(recommend); eligibility == recommendBacktestSkipped || eligibility == recommendBacktestIneligible {
		return true
	}
	category := normalizeRecommendCategory(recommend.RecommendCategory)
	switch category {
	case "":
		return true
	case recommendExecutionImmediate, recommendExecutionConditional:
		return true
	case "avoid":
		return false
	default:
		compact := strings.NewReplacer(
			" ", "",
			"\t", "",
			"\n", "",
			"\r", "",
			"（", "",
			"）", "",
			"(", "",
			")", "",
			"/", "",
		).Replace(strings.ToLower(strings.TrimSpace(recommend.RecommendCategory)))
		switch {
		case compact == "":
			return true
		case strings.Contains(compact, "回避"), strings.Contains(compact, "avoid"):
			return false
		case strings.Contains(compact, "激活买入"), strings.Contains(compact, "条件触发"), strings.Contains(compact, "观察"), strings.Contains(compact, "observe"):
			return true
		case strings.Contains(compact, "立刻买入"), strings.Contains(compact, "低吸"), strings.Contains(compact, "low_absorb"), strings.Contains(compact, "lowabsorb"):
			return true
		case strings.Contains(compact, "右侧确认"), strings.Contains(compact, "right_confirm"), strings.Contains(compact, "rightconfirm"):
			return true
		default:
			return false
		}
	}
}

func hasStructuredSignalExecution(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	if normalizeRecommendExecutionState(recommend.ExecutionState) == "" {
		return false
	}
	if hasMachineActivationRule(recommend) {
		return true
	}
	return false
}

func shouldUseStrictBacktestEligibility(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	if hasSignalDrivenRecommend(recommend) {
		return true
	}
	version := strings.TrimSpace(recommend.SummaryVersion)
	switch version {
	case "", "phase1-v1", defaultAiRecommendSummaryVersion, "phase3-v2":
		return false
	case marketSummaryPhase3Version, marketSummaryPhase4Version, marketSummaryVersionV132:
		return true
	default:
		return false
	}
}

func legacyDirectActivationCutoffStart() time.Time {
	loc := cnLocation()
	start, err := time.ParseInLocation("2006-01-02", legacyDirectActivationCutoffDate, loc)
	if err != nil {
		return time.Date(2026, 4, 6, 0, 0, 0, 0, loc)
	}
	return start
}

func shouldUseLegacyDirectActivationAt(recordTime time.Time) bool {
	if recordTime.IsZero() {
		return false
	}
	return recordTime.In(cnLocation()).Before(legacyDirectActivationCutoffStart())
}

func shouldUseLegacyDirectActivation(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	if hasMachineActivationRule(recommend) {
		return false
	}
	if classifyRecommendBacktestCategory(recommend) == "avoid" {
		return false
	}
	return shouldUseLegacyDirectActivationAt(recommendRecordTime(*recommend))
}

func compactRecommendCategoryText(category string) string {
	return strings.NewReplacer(
		" ", "",
		"\t", "",
		"\n", "",
		"\r", "",
		"（", "",
		"）", "",
		"(", "",
		")", "",
		"/", "",
	).Replace(strings.ToLower(strings.TrimSpace(category)))
}

func isLegacyMixedCandidateCategory(category string) bool {
	compact := compactRecommendCategoryText(category)
	if compact == "" {
		return false
	}
	hasLegacyLowAbsorb := strings.Contains(compact, "低吸") || strings.Contains(compact, "low_absorb") || strings.Contains(compact, "lowabsorb")
	hasLegacyConditional := strings.Contains(compact, "右侧确认") ||
		strings.Contains(compact, "right_confirm") ||
		strings.Contains(compact, "rightconfirm") ||
		strings.Contains(compact, "观察") ||
		strings.Contains(compact, "observe")
	return hasLegacyLowAbsorb && hasLegacyConditional
}

func classifyRecommendBacktestCategory(recommend *models.AiRecommendStocks) string {
	if recommend == nil {
		return ""
	}
	if isLegacyMixedCandidateCategory(recommend.RecommendCategory) {
		return recommendExecutionConditional
	}
	if normalized := normalizeRecommendCategory(recommend.RecommendCategory); normalized == recommendExecutionImmediate || normalized == recommendExecutionConditional || normalized == "avoid" {
		return normalized
	}
	compact := compactRecommendCategoryText(recommend.RecommendCategory)
	switch {
	case compact == "":
		return ""
	case strings.Contains(compact, "回避"), strings.Contains(compact, "avoid"):
		return "avoid"
	case strings.Contains(compact, "激活买入"), strings.Contains(compact, "条件触发"), strings.Contains(compact, "观察"), strings.Contains(compact, "observe"),
		strings.Contains(compact, "右侧确认"), strings.Contains(compact, "right_confirm"), strings.Contains(compact, "rightconfirm"):
		return recommendExecutionConditional
	case strings.Contains(compact, "立刻买入"), strings.Contains(compact, "低吸"), strings.Contains(compact, "low_absorb"), strings.Contains(compact, "lowabsorb"):
		return recommendExecutionImmediate
	default:
		return ""
	}
}

func resolveRecommendBacktestEligibility(recommend *models.AiRecommendStocks) (string, string) {
	if recommend == nil {
		return recommendBacktestIneligible, "推荐记录为空，未纳入回测"
	}
	if isPendingMarketDataRecommend(recommend) {
		return recommendBacktestSkipped, "等待本地分钟线补齐后激活与回测"
	}
	if isAnalysisOnlyRecommend(recommend) {
		return recommendBacktestSkipped, marketSummaryAnalysisOnlySkipReason
	}
	_, _, _, reason, skip := resolveRecommendYieldSkipInfo(recommend)
	if skip {
		return recommendBacktestSkipped, reason
	}
	if !shouldUseStrictBacktestEligibility(recommend) || shouldUseLegacyDirectActivation(recommend) {
		if _, _, buyOK := parseRecommendEntryRange(*recommend); !buyOK {
			return recommendBacktestIneligible, "缺少可解析买入区间，未纳入回测"
		}
		_, stopProfitOK := parseStopProfitPrice(*recommend)
		_, stopLossOK := parseStopLossPrice(*recommend)
		if !stopProfitOK || !stopLossOK {
			return recommendBacktestIneligible, "缺少完整止盈止损计划，未纳入回测"
		}
		return recommendBacktestEligible, ""
	}
	category := classifyRecommendBacktestCategory(recommend)
	switch category {
	case recommendExecutionConditional:
		if !hasMachineActivationRule(recommend) {
			return recommendBacktestIneligible, "缺少结构化激活规则，未纳入回测"
		}
	case recommendExecutionImmediate:
		if hasMachineActivationRule(recommend) {
			return recommendBacktestEligible, ""
		}
		if _, _, buyOK := parseRecommendEntryRange(*recommend); !buyOK {
			return recommendBacktestIneligible, "缺少可解析买入区间，未纳入回测"
		}
		_, stopProfitOK := parseStopProfitPrice(*recommend)
		_, stopLossOK := parseStopLossPrice(*recommend)
		if !stopProfitOK || !stopLossOK {
			return recommendBacktestIneligible, "缺少完整止盈止损计划，未纳入回测"
		}
		return recommendBacktestEligible, ""
	}
	if hasMachineActivationRule(recommend) {
		return recommendBacktestEligible, ""
	}
	if classifyRecommendBacktestCategory(recommend) != "" {
		return recommendBacktestIneligible, "缺少结构化激活规则，未纳入回测"
	}
	if _, _, buyOK := parseRecommendEntryRange(*recommend); !buyOK {
		return recommendBacktestIneligible, "缺少可解析买入区间，未纳入回测"
	}
	_, stopProfitOK := parseStopProfitPrice(*recommend)
	_, stopLossOK := parseStopLossPrice(*recommend)
	if !stopProfitOK || !stopLossOK {
		return recommendBacktestIneligible, "缺少完整止盈止损计划，未纳入回测"
	}
	return recommendBacktestEligible, ""
}

func resolveRecommendYieldSkipInfo(recommend *models.AiRecommendStocks) (string, string, string, string, bool) {
	if recommend == nil {
		return "", "", "", "", false
	}

	status := normalizeRecommendStatus(recommend.RecommendStatus)
	if status == recommendStatusPendingMarketData || isPendingMarketDataRecommend(recommend) {
		return recommendActivationPendingData, "待补分钟线", "待补分钟线", "等待本地分钟线补齐后激活与回测", true
	}
	if status == "missing_market_data" || isAnalysisOnlyRecommend(recommend) {
		return "skipped", "已跳过", "已跳过", marketSummaryAnalysisOnlySkipReason, true
	}
	switch status {
	case "avoid":
		return "skipped", "已放弃", "已跳过", appendRecommendInvalidConditionText("AI 推荐状态为回避，不纳入收益率跟踪", recommend.InvalidCondition), true
	}

	category := normalizeRecommendCategory(recommend.RecommendCategory)
	if category == "avoid" {
		return "skipped", "已放弃", "已跳过", appendRecommendInvalidConditionText("AI 分类为回避标的，不纳入收益率跟踪", recommend.InvalidCondition), true
	}

	rawCategory := strings.TrimSpace(strings.ToLower(recommend.RecommendCategory))
	if rawCategory != "" {
		compact := strings.NewReplacer(
			" ", "",
			"\t", "",
			"\n", "",
			"\r", "",
			"（", "",
			"）", "",
			"(", "",
			")", "",
			"/", "",
		).Replace(rawCategory)
		if strings.Contains(compact, "回避") || strings.Contains(compact, "avoid") {
			return "skipped", "已放弃", "已跳过", appendRecommendInvalidConditionText("AI 分类为回避标的，不纳入收益率跟踪", recommend.InvalidCondition), true
		}
	}

	if shouldUseLegacyPreCutoffSkipRule(recommend) {
		if hasLegacyObservationWord(recommend) {
			return "skipped", "已放弃", "已跳过", appendRecommendInvalidConditionText("买入依据含“观察”，不纳入收益率跟踪", recommend.InvalidCondition), true
		}
		return "", "", "", "", false
	}

	if status == "insufficient_evidence" && !hasExplicitExecutableActivationPlan(recommend) {
		return "skipped", "已放弃", "已跳过", appendRecommendInvalidConditionText("AI 推荐状态为证据不足，暂不纳入收益率跟踪", recommend.InvalidCondition), true
	}

	if shouldSkipObservationStyleRecommend(recommend) {
		return "skipped", "已放弃", "已跳过", appendRecommendInvalidConditionText("买入依据仍含观察/保守口径，不纳入收益率跟踪", recommend.InvalidCondition), true
	}

	return "", "", "", "", false
}

func shouldUseLegacyPreCutoffSkipRule(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	return shouldUseLegacyDirectActivationAt(recommendRecordTime(*recommend))
}

func hasLegacyObservationWord(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	signalView := resolveRecommendSignalView(*recommend)
	text := normalizeRecommendText(strings.Join([]string{
		signalView.BuySignal,
		signalView.BuySignalDetail,
	}, "\n"))
	if text == "" {
		return false
	}
	return strings.Contains(text, "观察")
}

func shouldSkipObservationStyleRecommend(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	if shouldBypassRecommendKeywordInterception(recommend.DataTime) {
		return false
	}
	return hasObservationStyleCue(recommend)
}

func hasObservationStyleCue(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	signalView := resolveRecommendSignalView(*recommend)
	text := strings.ToLower(normalizeRecommendText(strings.Join([]string{
		signalView.BuySignal,
		signalView.BuySignalDetail,
	}, "\n")))
	if text == "" {
		return false
	}
	for _, phrase := range recommendObservationSkipPhrases {
		if strings.Contains(text, strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}

func hasExplicitExecutableActivationPlan(recommend *models.AiRecommendStocks) bool {
	if recommend == nil || !hasMachineActivationRule(recommend) {
		return false
	}
	if _, _, buyOK := parseRecommendEntryRange(*recommend); !buyOK {
		return false
	}
	if _, stopProfitOK := parseStopProfitPrice(*recommend); !stopProfitOK {
		return false
	}
	if _, stopLossOK := parseStopLossPrice(*recommend); !stopLossOK {
		return false
	}
	buyCombined := normalizeRecommendText(strings.TrimSpace(recommend.BuySignal + "\n" + recommend.BuySignalDetail))
	invalidCombined := normalizeRecommendText(strings.TrimSpace(recommend.InvalidSignal + "\n" + recommend.InvalidCondition))
	if !containsConditionalCue(buyCombined) {
		return false
	}
	if len(parsePriceValues(buyCombined+"\n"+invalidCombined)) < 3 {
		return false
	}
	if hasVolumeSignal(buyCombined) && !hasCompleteVolumeContext(buyCombined) {
		return false
	}
	if hasVolumeSignal(invalidCombined) && !hasCompleteVolumeContext(invalidCombined) && !invalidSignalCanReferenceActivationRule(recommend, invalidCombined) {
		return false
	}
	return true
}

func recommendCategoryDisplayLabel(category string) string {
	raw := strings.TrimSpace(category)
	normalized := normalizeRecommendCategory(raw)
	switch normalized {
	case recommendExecutionImmediate:
		return "等待激活"
	case recommendExecutionConditional:
		return "等待激活"
	case "avoid":
		return "回避标的"
	}
	if raw != "" {
		return raw
	}
	return "未标注"
}

func clampConfidenceScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func normalizeEvidenceSourcesText(raw string) string {
	refs := parseEvidenceSourcesJSON(raw, "")
	return marshalEvidenceSources(refs)
}

func normalizeRecommendEvidenceSources(recommend *models.AiRecommendStocks) {
	if recommend == nil {
		return
	}
	refs := parseCombinedEvidenceSources(recommend.EvidenceSources, recommend.KeyEvidence, recommend.StockCode)
	recommend.EvidenceSources = marshalEvidenceSources(refs)
	if recommend.KeyEvidence == "" && len(refs) > 0 {
		recommend.KeyEvidence = evidenceRefsToText(refs)
	}
}

func hasStructuredRecommendPayload(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	fields := []string{
		recommend.ExecutionState,
		recommend.BuySignal,
		recommend.BuySignalDetail,
		recommend.SellSignal,
		recommend.SellSignalDetail,
		recommend.InvalidSignal,
		recommend.RecommendCategory,
		recommend.CoreCatalyst,
		recommend.KeyEvidence,
		recommend.InvalidCondition,
		recommend.ObservePrice,
		recommend.FocusPrice,
		recommend.ExpectedCycle,
		recommend.EvidenceSources,
	}
	for _, item := range fields {
		if strings.TrimSpace(item) != "" {
			return true
		}
	}
	return recommend.EventStrength > 0 || recommend.CapitalConfirmation > 0 || recommend.FundamentalFit > 0 || recommend.TechnicalFit > 0
}

func mergeStructuredRecommendCompatFields(recommend *models.AiRecommendStocks) {
	if recommend == nil {
		return
	}
	if recommend.CoreCatalyst == "" {
		recommend.CoreCatalyst = extractStructuredLine(recommend.RecommendReason, []string{"核心催化：", "核心逻辑："})
	}
	if recommend.KeyEvidence == "" {
		recommend.KeyEvidence = extractStructuredLine(recommend.RecommendReason, []string{"关键证据："})
	}
	if recommend.ExecutionState == "" {
		recommend.ExecutionState = normalizeRecommendExecutionState(extractStructuredLine(recommend.RecommendReason, []string{"执行状态："}))
	}
	if recommend.BuySignal == "" {
		recommend.BuySignal = extractStructuredLine(recommend.RecommendReason, []string{"买入依据：", "买入信号："})
	}
	if recommend.BuySignalDetail == "" {
		recommend.BuySignalDetail = extractStructuredLine(recommend.RecommendReason, []string{"买入补充条件：", "买入条件补充：", "买入信号补充：", "买入补充说明："})
	}
	if recommend.SellSignal == "" {
		recommend.SellSignal = extractStructuredLine(recommend.RecommendReason, []string{"卖出计划：", "卖出信号："})
	}
	if recommend.SellSignalDetail == "" {
		recommend.SellSignalDetail = extractStructuredLine(recommend.RecommendReason, []string{"卖出补充条件：", "卖出条件补充：", "卖出信号补充：", "卖出补充说明："})
	}
	if recommend.InvalidCondition == "" {
		recommend.InvalidCondition = extractStructuredLine(recommend.RecommendReason, []string{"失效条件："})
	}
	if recommend.InvalidSignal == "" {
		recommend.InvalidSignal = extractStructuredLine(recommend.RecommendReason, []string{"失效信号："})
	}
	if !hasSignalDrivenRecommend(recommend) && recommend.FocusPrice == "" {
		recommend.FocusPrice = strings.TrimSpace(recommend.RecommendBuyPrice)
	}
	if recommend.ObservePrice == "" {
		recommend.ObservePrice = firstNumericText(recommend.StockPrice)
	}
	if recommend.KeyEvidence == "" && strings.TrimSpace(recommend.EvidenceSources) != "" {
		recommend.KeyEvidence = evidenceSourcesToText(recommend.EvidenceSources)
	}
	if recommend.EvidenceSources == "" && recommend.KeyEvidence != "" {
		if refs := parseEvidenceSourcesFromText(recommend.KeyEvidence); len(refs) > 0 {
			recommend.EvidenceSources = marshalEvidenceSources(normalizeEvidenceRefs(refs, recommend.StockCode))
		}
	}
	if recommend.RecommendReason == "" {
		recommend.RecommendReason = buildRecommendReasonCompat(recommend)
	}
}

func extractStructuredLine(text string, prefixes []string) string {
	text = normalizeRecommendText(text)
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, prefix := range prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			}
		}
	}
	return ""
}

func evidenceSourcesToText(raw string) string {
	return evidenceRefsToText(parseEvidenceSourcesJSON(raw, ""))
}

func evidenceRefsToText(refs []aiEvidenceReference) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Type) == "" || strings.TrimSpace(ref.Summary) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", ref.Type, ref.Summary))
	}
	return strings.Join(parts, "\n")
}

func parseEvidenceSourcesFromText(text string) []aiEvidenceReference {
	text = normalizeRecommendText(text)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	refs := make([]aiEvidenceReference, 0, len(lines))
	seen := map[string]struct{}{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matches := evidenceTagRegexp.FindAllStringSubmatchIndex(line, -1)
		if len(matches) == 0 {
			ref := aiEvidenceReference{Type: "市场资讯", Summary: strings.TrimSpace(line)}
			key := ref.Type + "|" + ref.Summary
			if ref.Summary == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, ref)
			continue
		}
		for idx, match := range matches {
			typeName := strings.TrimSpace(line[match[2]:match[3]])
			contentStart := match[1]
			contentEnd := len(line)
			if idx+1 < len(matches) {
				contentEnd = matches[idx+1][0]
			}
			summary := strings.TrimSpace(line[contentStart:contentEnd])
			summary = strings.Trim(summary, "；;，,。 ")
			if typeName == "" || summary == "" {
				continue
			}
			ref := aiEvidenceReference{Type: typeName, Summary: summary}
			key := normalizeEvidenceType(ref.Type) + "|" + ref.Summary
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

func parseEvidenceSourcesJSON(raw string, entityCode string) []aiEvidenceReference {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var refs []aiEvidenceReference
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	return normalizeEvidenceRefs(refs, entityCode)
}

func parseCombinedEvidenceSources(rawJSON, keyEvidence, entityCode string) []aiEvidenceReference {
	refs := parseEvidenceSourcesJSON(rawJSON, entityCode)
	if len(refs) == 0 && strings.TrimSpace(keyEvidence) != "" {
		refs = normalizeEvidenceRefs(parseEvidenceSourcesFromText(keyEvidence), entityCode)
		return refs
	}
	if strings.TrimSpace(keyEvidence) != "" {
		refs = append(refs, parseEvidenceSourcesFromText(keyEvidence)...)
	}
	return normalizeEvidenceRefs(refs, entityCode)
}

func normalizeEvidenceRefs(refs []aiEvidenceReference, entityCode string) []aiEvidenceReference {
	filtered := make([]aiEvidenceReference, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = normalizeEvidenceRef(ref, entityCode)
		if ref.Type == "" || ref.Summary == "" {
			continue
		}
		key := ref.DedupeKey
		if key == "" {
			key = buildEvidenceHash(ref.Type + "|" + ref.Summary + "|" + ref.SourceName + "|" + ref.EntityCode)
		}
		if ref.RawHash != "" {
			key = key + "|" + ref.RawHash
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, ref)
	}
	if len(filtered) == 0 {
		return nil
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].TrustLevel != filtered[j].TrustLevel {
			return evidenceTrustRank(filtered[i].TrustLevel) > evidenceTrustRank(filtered[j].TrustLevel)
		}
		if filtered[i].Type != filtered[j].Type {
			return filtered[i].Type < filtered[j].Type
		}
		return filtered[i].Summary < filtered[j].Summary
	})
	return filtered
}

func normalizeEvidenceRef(ref aiEvidenceReference, entityCode string) aiEvidenceReference {
	ref.Type = normalizeEvidenceType(ref.Type)
	ref.Summary = normalizeRecommendText(ref.Summary)
	ref.SourceName = strings.TrimSpace(ref.SourceName)
	ref.SourceType = normalizeEvidenceSourceType(ref.SourceType)
	ref.TrustLevel = normalizeEvidenceTrustLevel(ref.TrustLevel)
	ref.LatencyLevel = normalizeEvidenceLatencyLevel(ref.LatencyLevel)
	ref.Title = normalizeRecommendText(ref.Title)
	ref.URL = strings.TrimSpace(ref.URL)
	ref.PublishedAt = strings.TrimSpace(ref.PublishedAt)
	ref.EntityType = normalizeEvidenceEntityType(ref.EntityType)
	ref.EntityCode = normalizeRecommendStockCode(firstNonEmptyText(ref.EntityCode, entityCode))
	ref.DedupeKey = strings.TrimSpace(ref.DedupeKey)
	ref.RawHash = strings.TrimSpace(ref.RawHash)

	if inferred := inferEvidenceTypeFromSummary(ref.Summary); ref.Type == "市场资讯" && inferred != "市场资讯" {
		ref.Type = inferred
	}
	defaultSourceName, defaultSourceType, defaultTrustLevel, defaultLatencyLevel := inferEvidenceGovernanceDefaults(ref)
	if ref.SourceName == "" {
		ref.SourceName = defaultSourceName
	}
	if ref.SourceType == "" {
		ref.SourceType = defaultSourceType
	}
	if ref.TrustLevel == "" {
		ref.TrustLevel = defaultTrustLevel
	}
	if ref.LatencyLevel == "" {
		ref.LatencyLevel = defaultLatencyLevel
	}
	if ref.EntityType == "" {
		if ref.EntityCode != "" {
			ref.EntityType = "stock"
		} else {
			ref.EntityType = "market"
		}
	}
	if ref.RawHash == "" {
		ref.RawHash = buildEvidenceHash(strings.Join([]string{ref.Type, ref.Title, ref.Summary, ref.URL}, "|"))
	}
	if ref.DedupeKey == "" {
		dateKey := ref.PublishedAt
		if len(dateKey) > 10 {
			dateKey = dateKey[:10]
		}
		titleOrSummary := ref.Title
		if titleOrSummary == "" {
			titleOrSummary = ref.Summary
		}
		ref.DedupeKey = buildEvidenceHash(strings.Join([]string{ref.EntityCode, ref.Type, normalizeConflictText(titleOrSummary), ref.SourceType, dateKey}, "|"))
	}
	return ref
}

func normalizeEvidenceType(typeName string) string {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return "市场资讯"
	}
	if alias, ok := evidenceTypeAliasMap[typeName]; ok {
		return alias
	}
	return typeName
}

func normalizeEvidenceSourceType(sourceType string) string {
	sourceType = strings.TrimSpace(strings.ToLower(sourceType))
	switch sourceType {
	case "原始披露", "original", "original_disclosure":
		return "原始披露"
	case "聚合媒体", "aggregate", "media", "aggregated_media":
		return "聚合媒体"
	case "数据接口", "data", "api", "data_api":
		return "数据接口"
	case "高频指标", "high_freq", "high_frequency":
		return "高频指标"
	default:
		return ""
	}
}

func normalizeEvidenceTrustLevel(level string) string {
	level = strings.TrimSpace(strings.ToLower(level))
	switch level {
	case "高", "high":
		return "high"
	case "中", "medium", "mid":
		return "medium"
	case "低", "low":
		return "low"
	default:
		return ""
	}
}

func normalizeEvidenceLatencyLevel(level string) string {
	level = strings.TrimSpace(strings.ToLower(level))
	switch level {
	case "实时", "realtime", "real_time":
		return "realtime"
	case "日级", "daily":
		return "daily"
	case "周期级", "periodic":
		return "periodic"
	default:
		return ""
	}
}

func normalizeEvidenceEntityType(entityType string) string {
	entityType = strings.TrimSpace(strings.ToLower(entityType))
	switch entityType {
	case "market", "sector", "stock":
		return entityType
	default:
		return ""
	}
}

func inferEvidenceTypeFromSummary(summary string) string {
	summary = normalizeRecommendText(summary)
	switch {
	case containsAnyKeyword(summary, []string{"公告", "问询函", "监管函", "停牌", "复牌", "异常波动", "业绩预告", "业绩快报"}):
		return "一级披露"
	case containsAnyKeyword(summary, []string{"北向", "南向", "龙虎榜", "ETF份额", "申赎", "资金结构"}):
		return "资金结构"
	case containsAnyKeyword(summary, []string{"股东户数", "解禁", "回购", "减持", "增持", "机构持仓"}):
		return "股东/筹码"
	case containsAnyKeyword(summary, []string{"原油", "黄金", "美元指数", "纳指", "中概", "美债"}):
		return "海外风险"
	case containsAnyKeyword(summary, []string{"价格", "景气", "运价", "报价"}):
		return "产业高频"
	default:
		return "市场资讯"
	}
}

func inferEvidenceGovernanceDefaults(ref aiEvidenceReference) (string, string, string, string) {
	typeName := normalizeEvidenceType(ref.Type)
	switch typeName {
	case "一级披露":
		return "交易所/巨潮公告", "原始披露", "high", "realtime"
	case "互动易":
		return "巨潮互动易", "原始披露", "high", "realtime"
	case "财报/财务":
		return "东方财富财务数据", "数据接口", "high", "periodic"
	case "资金结构":
		return "资金结构数据接口", "数据接口", "medium", "daily"
	case "股东/筹码":
		return "股东筹码数据接口", "数据接口", "medium", "periodic"
	case "技术/资金/形态":
		return "行情与资金数据接口", "数据接口", "medium", "realtime"
	case "行业研报":
		return "研报聚合", "聚合媒体", "medium", "periodic"
	case "产业高频":
		return "产业高频指标", "高频指标", "medium", "daily"
	case "海外风险":
		return "海外市场数据接口", "数据接口", "medium", "realtime"
	case "个股新闻":
		return "个股新闻聚合", "聚合媒体", "medium", "realtime"
	default:
		return "市场资讯聚合", "聚合媒体", "low", "realtime"
	}
}

func evidenceTrustRank(level string) int {
	switch normalizeEvidenceTrustLevel(level) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func buildEvidenceHash(text string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:8])
}

func marshalEvidenceSources(refs []aiEvidenceReference) string {
	if len(refs) == 0 {
		return ""
	}
	b, _ := json.Marshal(refs)
	return string(b)
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func collectRecommendEvidenceRefs(recommend *models.AiRecommendStocks) []aiEvidenceReference {
	if recommend == nil {
		return nil
	}
	return parseCombinedEvidenceSources(recommend.EvidenceSources, recommend.KeyEvidence, recommend.StockCode)
}

func evidenceTypeCount(recommend *models.AiRecommendStocks) int {
	if recommend == nil {
		return 0
	}
	types := map[string]struct{}{}
	for _, ref := range collectRecommendEvidenceRefs(recommend) {
		if strings.TrimSpace(ref.Type) == "" {
			continue
		}
		types[ref.Type] = struct{}{}
	}
	return len(types)
}

func highTrustEvidenceCount(recommend *models.AiRecommendStocks) int {
	count := 0
	for _, ref := range collectRecommendEvidenceRefs(recommend) {
		if normalizeEvidenceTrustLevel(ref.TrustLevel) == "high" {
			count++
		}
	}
	return count
}

func hasConflictingEvidence(recommend *models.AiRecommendStocks) bool {
	refs := collectRecommendEvidenceRefs(recommend)
	if len(refs) < 2 {
		return false
	}
	groups := map[string][]aiEvidenceReference{}
	for _, ref := range refs {
		key := normalizeConflictText(firstNonEmptyText(ref.Title, ref.DedupeKey))
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], ref)
	}
	for _, items := range groups {
		highPositive := false
		highNegative := false
		mediaPositive := false
		mediaNegative := false
		for _, ref := range items {
			polarity := evidencePolarity(strings.TrimSpace(ref.Title + " " + ref.Summary))
			if polarity == 0 {
				continue
			}
			if normalizeEvidenceTrustLevel(ref.TrustLevel) == "high" || ref.SourceType == "原始披露" {
				if polarity > 0 {
					highPositive = true
				} else {
					highNegative = true
				}
			}
			if ref.SourceType == "聚合媒体" {
				if polarity > 0 {
					mediaPositive = true
				} else {
					mediaNegative = true
				}
			}
		}
		if (highPositive && mediaNegative) || (highNegative && mediaPositive) {
			return true
		}
	}
	return false
}

func normalizeConflictText(text string) string {
	text = strings.TrimSpace(strings.ToLower(text))
	text = strings.ReplaceAll(text, " ", "")
	text = strings.ReplaceAll(text, "\n", "")
	return text
}

func evidencePolarity(text string) int {
	if containsAnyKeyword(text, evidencePositiveKeywords) {
		return 1
	}
	if containsAnyKeyword(text, evidenceNegativeKeywords) {
		return -1
	}
	return 0
}

func containsAnyKeyword(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func inferRecommendCategory(recommend *models.AiRecommendStocks) string {
	if recommend == nil {
		return ""
	}
	text := strings.Join([]string{recommend.Remarks, recommend.RecommendReason, recommend.KeyEvidence}, "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.Contains(text, "回避") || strings.Contains(text, "不参与") {
		return "avoid"
	}
	if strings.Contains(text, "右侧") || strings.Contains(text, "放量") || strings.Contains(text, "突破") || strings.Contains(text, "确认") {
		return recommendExecutionConditional
	}
	if strings.Contains(text, "立刻买入") || strings.Contains(text, "立即买入") || strings.Contains(text, "低吸") || strings.Contains(text, "回踩") || strings.Contains(text, "埋伏") {
		return recommendExecutionImmediate
	}
	return recommendExecutionConditional
}

func applyStructuredRecommendRules(recommend *models.AiRecommendStocks) {
	if recommend == nil {
		return
	}
	missing := make([]string, 0, 10)
	if recommend.CoreCatalyst == "" {
		missing = append(missing, "核心催化")
	}
	if recommend.KeyEvidence == "" {
		missing = append(missing, "关键证据")
	}
	if recommend.InvalidCondition == "" {
		missing = append(missing, "失效条件")
	}
	if recommend.ObservePrice == "" {
		missing = append(missing, "观察价")
	}
	if !hasSignalDrivenRecommend(recommend) && strings.TrimSpace(recommend.FocusPrice) == "" {
		missing = append(missing, "关注位")
	}
	if strings.TrimSpace(recommend.ExpectedCycle) == "" {
		missing = append(missing, "预期周期")
	}
	if recommend.EventStrength <= 0 {
		missing = append(missing, "事件强度")
	}
	if recommend.CapitalConfirmation <= 0 {
		missing = append(missing, "资金确认度")
	}
	if recommend.FundamentalFit <= 0 {
		missing = append(missing, "基本面匹配度")
	}
	if recommend.TechnicalFit <= 0 {
		missing = append(missing, "技术面匹配度")
	}
	if evidenceTypeCount(recommend) < 2 {
		missing = append(missing, "至少两类证据")
	}
	if highTrustEvidenceCount(recommend) < 1 {
		missing = append(missing, "至少1条高信任证据")
	}
	if recommend.RecommendCategory == recommendExecutionImmediate || recommend.RecommendCategory == recommendExecutionConditional {
		if hasConflictingEvidence(recommend) {
			recommend.RecommendCategory = recommendExecutionConditional
			recommend.RecommendStatus = "controversial"
			notice := "证据存在冲突，已统一收敛为等待激活：高信任源与聚合媒体结论不一致"
			if recommend.Remarks == "" {
				recommend.Remarks = notice
			} else if !strings.Contains(recommend.Remarks, notice) {
				recommend.Remarks = recommend.Remarks + "\n" + notice
			}
			return
		}
		if len(missing) > 0 {
			recommend.RecommendCategory = recommendExecutionConditional
			recommend.RecommendStatus = "insufficient_evidence"
			notice := "证据不足，已统一收敛为等待激活：" + strings.Join(missing, "、")
			if recommend.Remarks == "" {
				recommend.Remarks = notice
			} else if !strings.Contains(recommend.Remarks, notice) {
				recommend.Remarks = recommend.Remarks + "\n" + notice
			}
		}
	}
	if recommend.RecommendCategory == "avoid" {
		recommend.RecommendStatus = "avoid"
	}
}

func appendRecommendRemarkNotice(base, notice string) string {
	base = strings.TrimSpace(base)
	notice = strings.TrimSpace(notice)
	if notice == "" {
		return base
	}
	if base == "" {
		return notice
	}
	if strings.Contains(base, notice) {
		return base
	}
	return base + "\n" + notice
}

func shouldApplyTimeAwareRecommendRules(recommend *models.AiRecommendStocks) bool {
	if recommend == nil {
		return false
	}
	if hasSignalDrivenRecommend(recommend) {
		return true
	}
	switch strings.TrimSpace(recommend.SummaryVersion) {
	case marketSummaryPhase3Version, marketSummaryPhase4Version, marketSummaryVersionV132:
		return true
	default:
		return false
	}
}

func isCNContinuousTradingSession(now time.Time) bool {
	loc := cnLocation()
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(day) {
		return false
	}
	minutes := now.Hour()*60 + now.Minute()
	morningOpen := 9*60 + 30
	morningClose := 11*60 + 30
	afternoonOpen := 13 * 60
	close1500 := 15 * 60
	return (minutes >= morningOpen && minutes <= morningClose) || (minutes >= afternoonOpen && minutes <= close1500)
}

func describeCNMarketTiming(now time.Time) string {
	if now.IsZero() {
		return "未知时段"
	}
	loc := cnLocation()
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(day) {
		return "非交易日"
	}
	minutes := now.Hour()*60 + now.Minute()
	switch {
	case minutes < 9*60+30:
		return "盘前"
	case minutes <= 11*60+30:
		return "盘中"
	case minutes < 13*60:
		return "午间休市"
	case minutes <= 15*60:
		return "盘中"
	default:
		return "盘后"
	}
}

func isImmediateRecommendTimeAllowed(recordTime time.Time) bool {
	return !recordTime.IsZero() && isCNContinuousTradingSession(recordTime)
}

func applyRecommendTimingRules(recommend *models.AiRecommendStocks) {
	if recommend == nil || !shouldApplyTimeAwareRecommendRules(recommend) {
		return
	}
	recordTime := recommendRecordTime(*recommend)
	if recordTime.IsZero() || isImmediateRecommendTimeAllowed(recordTime) {
		return
	}
	downgraded := false
	if recommend.ExecutionState == recommendExecutionImmediate {
		recommend.ExecutionState = recommendExecutionConditional
		downgraded = true
	}
	if recommend.RecommendCategory == recommendExecutionImmediate {
		recommend.RecommendCategory = recommendExecutionConditional
		downgraded = true
	}
	if downgraded {
		buyCombined := normalizeRecommendText(strings.TrimSpace(recommend.BuySignal + "\n" + recommend.BuySignalDetail))
		if hasSignalDrivenRecommend(recommend) && !containsConditionalCue(buyCombined) {
			buyRange := strings.TrimSpace(recommend.RecommendBuyPrice)
			if buyRange == "" {
				buyRange = "主买入区"
			}
			recommend.BuySignal = fmt.Sprintf("若未来%d个交易日内股价重新进入%s并完成量价确认后再买入", recommendPendingActivationMaxTradeDays, buyRange)
		}
		recommend.Remarks = appendRecommendRemarkNotice(
			recommend.Remarks,
			fmt.Sprintf(
				"当前生成时间为%s，统一按等待激活处理；仅允许未来%d个交易日内触发。",
				describeCNMarketTiming(recordTime),
				recommendPendingActivationMaxTradeDays,
			),
		)
	}
}

func buildRecommendReasonCompat(recommend *models.AiRecommendStocks) string {
	if recommend == nil {
		return ""
	}
	parts := make([]string, 0, 9)
	if recommend.CoreCatalyst != "" {
		parts = append(parts, "核心催化："+recommend.CoreCatalyst)
	}
	if recommend.KeyEvidence != "" {
		parts = append(parts, "关键证据："+recommend.KeyEvidence)
	}
	if recommend.ObservePrice != "" {
		parts = append(parts, "价格锚点："+recommend.ObservePrice)
	}
	if recommend.RecommendBuyPrice != "" {
		parts = append(parts, "买入区间："+recommend.RecommendBuyPrice)
	}
	if recommend.RecommendStopProfitPrice != "" {
		parts = append(parts, "止盈区间："+recommend.RecommendStopProfitPrice)
	}
	if recommend.RecommendStopLossPrice != "" {
		parts = append(parts, "止损位："+recommend.RecommendStopLossPrice)
	}
	if recommend.BuySignal != "" {
		parts = append(parts, "买入依据："+recommend.BuySignal)
	}
	if recommend.BuySignalDetail != "" {
		parts = append(parts, "买入补充说明："+recommend.BuySignalDetail)
	}
	if recommend.SellSignal != "" {
		parts = append(parts, "卖出计划："+recommend.SellSignal)
	}
	if recommend.SellSignalDetail != "" {
		parts = append(parts, "卖出补充说明："+recommend.SellSignalDetail)
	}
	if recommend.InvalidCondition != "" {
		parts = append(parts, "失效条件："+recommend.InvalidCondition)
	} else if recommend.InvalidSignal != "" {
		parts = append(parts, "失效条件："+recommend.InvalidSignal)
	}
	if recommend.InvalidSignal != "" && recommend.InvalidSignal != recommend.InvalidCondition {
		parts = append(parts, "失效信号："+recommend.InvalidSignal)
	}
	return strings.Join(parts, "\n")
}

func buildDefaultRemarks(recommend *models.AiRecommendStocks) string {
	if recommend == nil {
		return defaultAiRecommendRemarks
	}
	parts := make([]string, 0, 3)
	if hasSignalDrivenRecommend(recommend) {
		parts = append(parts, "执行方式："+recommendExecutionStateLabel(recommend.ExecutionState))
	} else {
		label := recommendCategoryDisplayLabel(recommend.RecommendCategory)
		if label != "" {
			parts = append(parts, "执行方式："+label)
		}
	}
	if recommend.ExpectedCycle != "" {
		parts = append(parts, "预期周期："+recommend.ExpectedCycle)
	}
	if len(parts) == 0 {
		return defaultAiRecommendRemarks
	}
	return strings.Join(parts, "\n")
}

func recommendExecutionStateLabel(state string) string {
	switch normalizeRecommendExecutionState(state) {
	case recommendExecutionImmediate:
		return "等待激活"
	case recommendExecutionConditional:
		return "等待激活"
	case recommendExecutionAnalysisOnly:
		return "仅分析"
	default:
		return "等待激活"
	}
}

func normalizePriceRangeText(raw string, min float64, max float64) (string, float64, float64) {
	if min <= 0 || max <= 0 {
		parsedMin, okMin := parsePriceMinFromText(raw)
		parsedMax, okMax := parsePriceMaxFromText(raw)
		if okMin && min <= 0 {
			min = parsedMin
		}
		if okMax && max <= 0 {
			max = parsedMax
		}
	}
	if min > 0 && max > 0 && min > max {
		min, max = max, min
	}
	if min <= 0 || max <= 0 {
		return strings.TrimSpace(raw), min, max
	}
	if strings.TrimSpace(raw) == "" {
		if min == max {
			return formatRecommendPrice(min), min, max
		}
		return formatRecommendPrice(min) + "-" + formatRecommendPrice(max), min, max
	}
	return strings.TrimSpace(raw), min, max
}

func normalizeSinglePriceText(raw string) (string, float64) {
	value := 0.0
	if parsed, ok := parseStopLossPrice(models.AiRecommendStocks{RecommendStopLossPrice: raw}); ok {
		value = parsed
	}
	if strings.TrimSpace(raw) == "" && value > 0 {
		return formatRecommendPrice(value), value
	}
	return strings.TrimSpace(raw), value
}

func formatRecommendPrice(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(round2(v), 'f', -1, 64)
}
