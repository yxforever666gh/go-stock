package data

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"gorm.io/gorm"
)

type MarketSummaryHumanizeCompatFixResult struct {
	ReportsScanned int
	ReportsUpdated int
	RemarksScanned int
	RemarksUpdated int
}

func HumanizeMarketSummaryReport(raw string) string {
	cleaned, _ := humanizeActivationRuleText(raw)
	return cleanHumanizedMarketSummaryReport(cleaned)
}

func HumanizeRecommendRemarks(raw string) string {
	cleaned, _ := humanizeActivationRuleText(raw)
	return cleaned
}

func sanitizeAIResponseResultForDisplay(result *models.AIResponseResult) {
	if result == nil {
		return
	}
	if result.StockCode == "市场资讯" || result.StockName == "市场资讯" {
		result.Question = NormalizeMarketSummaryQuestion(result.Question)
		result.Content = HumanizeMarketSummaryReport(result.Content)
	}
}

func sanitizeAiRecommendStockForDisplay(item *models.AiRecommendStocks) {
	if item == nil {
		return
	}
	item.Remarks = HumanizeRecommendRemarks(item.Remarks)
}

func RunMarketSummaryHumanizeCompatFix() (MarketSummaryHumanizeCompatFixResult, error) {
	result := MarketSummaryHumanizeCompatFixResult{}
	err := db.Dao.Transaction(func(tx *gorm.DB) error {
		var reports []models.AIResponseResult
		if err := tx.Model(&models.AIResponseResult{}).
			Where("(stock_code = ? OR stock_name = ?)", "市场资讯", "市场资讯").
			Where("(content LIKE ? OR content LIKE ?)", "%activationRuleJson%", "%\"signalType\"%").
			Find(&reports).Error; err != nil {
			return err
		}
		result.ReportsScanned = len(reports)
		for idx := range reports {
			cleaned := HumanizeMarketSummaryReport(reports[idx].Content)
			if cleaned == reports[idx].Content {
				continue
			}
			if err := tx.Model(&models.AIResponseResult{}).
				Where("id = ?", reports[idx].ID).
				Update("content", cleaned).Error; err != nil {
				return err
			}
			result.ReportsUpdated++
		}

		var recommends []models.AiRecommendStocks
		if err := tx.Model(&models.AiRecommendStocks{}).
			Where("(remarks LIKE ? OR remarks LIKE ?)", "%activationRuleJson%", "%\"signalType\"%").
			Find(&recommends).Error; err != nil {
			return err
		}
		result.RemarksScanned = len(recommends)
		for idx := range recommends {
			cleaned := HumanizeRecommendRemarks(recommends[idx].Remarks)
			if cleaned == recommends[idx].Remarks {
				continue
			}
			if err := tx.Model(&models.AiRecommendStocks{}).
				Where("id = ?", recommends[idx].ID).
				Update("remarks", cleaned).Error; err != nil {
				return err
			}
			result.RemarksUpdated++
		}
		return nil
	})
	return result, err
}

func extractActivationRuleJSONPayloads(text string) []string {
	segments := findActivationRuleSegments(text)
	if len(segments) == 0 {
		if raw, ok := extractStandaloneActivationRuleJSON(text); ok {
			return []string{raw}
		}
		return nil
	}
	result := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment.RawJSON) == "" {
			continue
		}
		result = append(result, segment.RawJSON)
	}
	return result
}

type activationRuleSegment struct {
	Start   int
	End     int
	RawJSON string
	Summary string
}

func humanizeActivationRuleText(text string) (string, []string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text, nil
	}

	if raw, ok := extractStandaloneActivationRuleJSON(text); ok {
		return buildActivationRuleDisplayFallback(raw), []string{raw}
	}

	segments := findActivationRuleSegments(text)
	if len(segments) == 0 {
		return text, nil
	}

	var builder strings.Builder
	builder.Grow(len(text) + len(segments)*24)
	last := 0
	rawRules := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment.Start < last || segment.End > len(text) {
			continue
		}
		builder.WriteString(text[last:segment.Start])
		builder.WriteString(segmentReplacementText(builder.String(), segment.Summary))
		last = segment.End
		rawRules = append(rawRules, segment.RawJSON)
	}
	builder.WriteString(text[last:])
	return cleanHumanizedActivationRuleText(builder.String()), rawRules
}

func findActivationRuleSegments(text string) []activationRuleSegment {
	lower := strings.ToLower(text)
	const key = "activationrulejson"
	segments := make([]activationRuleSegment, 0, 2)
	searchFrom := 0
	for {
		rel := strings.Index(lower[searchFrom:], key)
		if rel < 0 {
			break
		}
		start := searchFrom + rel
		cursor := start + len(key)
		for cursor < len(text) {
			if strings.HasPrefix(text[cursor:], "：") {
				cursor += len("：")
				continue
			}
			switch text[cursor] {
			case ' ', '\t', '\r', '\n', ':', '=', '`':
				cursor++
				continue
			}
			break
		}
		if cursor >= len(text) || text[cursor] != '{' {
			searchFrom = cursor
			continue
		}
		end := findJSONObjectEnd(text, cursor)
		if end <= cursor {
			searchFrom = cursor + 1
			continue
		}
		rawJSON := text[cursor:end]
		segments = append(segments, activationRuleSegment{
			Start:   start,
			End:     consumeActivationRuleTrailingWrapper(text, end),
			RawJSON: rawJSON,
			Summary: buildActivationRuleDisplayFallback(rawJSON),
		})
		searchFrom = end
	}
	return segments
}

func extractStandaloneActivationRuleJSON(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, "\"signalType\"") {
		return "", false
	}
	end := findJSONObjectEnd(trimmed, 0)
	if end != len(trimmed) {
		return "", false
	}
	return trimmed, true
}

func findJSONObjectEnd(text string, start int) int {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for idx := start; idx < len(text); idx++ {
		ch := text[idx]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return idx + 1
			}
		}
	}
	return -1
}

func consumeActivationRuleTrailingWrapper(text string, end int) int {
	cursor := end
	for cursor < len(text) && (text[cursor] == '`' || text[cursor] == ' ') {
		cursor++
	}
	return cursor
}

func buildActivationRuleDisplayFallback(raw string) string {
	if rule, err := parseActivationRuleJSON(raw); err == nil && rule != nil {
		return "激活条件：" + describeActivationRuleForHuman(rule)
	}
	return "激活条件已单独保存为机器规则，展示时不再显示 JSON"
}

func describeActivationRuleForHuman(rule *activationRule) string {
	if rule == nil {
		return "已结构化保存"
	}
	if len(rule.Paths) > 0 {
		parts := make([]string, 0, len(rule.Paths))
		for _, path := range rule.Paths {
			desc := describeActivationRuleForHuman(&path)
			if strings.TrimSpace(desc) == "" {
				continue
			}
			label := strings.TrimSpace(path.Name)
			if label != "" {
				parts = append(parts, label+"："+desc)
			} else {
				parts = append(parts, desc)
			}
		}
		if len(parts) == 0 {
			return "已结构化保存"
		}
		return strings.Join(parts, "；或 ")
	}
	parts := make([]string, 0, 4)
	switch strings.TrimSpace(rule.SignalType) {
	case "price_breakout_with_volume":
		parts = append(parts, "价格站上"+formatRecommendPrice(rule.ThresholdValue))
	default:
		minPrice := rule.ThresholdValue
		maxPrice := rule.ThresholdMax
		if maxPrice <= 0 {
			maxPrice = minPrice
		}
		if minPrice > 0 && maxPrice > 0 && round2(minPrice) != round2(maxPrice) {
			parts = append(parts, fmt.Sprintf("价格进入%s-%s区间", formatRecommendPrice(minPrice), formatRecommendPrice(maxPrice)))
		} else if minPrice > 0 {
			parts = append(parts, "价格到达"+formatRecommendPrice(minPrice)+"附近")
		}
	}
	if volumeText := describeActivationRuleVolume(rule); volumeText != "" {
		parts = append(parts, volumeText)
	}
	if confirmText := describeActivationRuleConfirm(rule); confirmText != "" {
		parts = append(parts, confirmText)
	}
	if expireText := describeActivationRuleExpire(rule); expireText != "" {
		parts = append(parts, expireText)
	}
	if len(parts) == 0 {
		return "已结构化保存"
	}
	return strings.Join(parts, "，")
}

func describeActivationRuleVolume(rule *activationRule) string {
	if rule == nil {
		return ""
	}
	windowLabel := describeActivationEvaluationWindow(rule.EvaluationWindow)
	metric := "成交额"
	switch strings.TrimSpace(rule.VolumeMetric) {
	case "volume":
		metric = "成交量"
	}
	operator := describeActivationOperator(rule.Operator)
	baseline := describeActivationBaseline(rule)
	if baseline == "" {
		return ""
	}
	if round2(rule.VolumeRatio) <= 1 {
		return fmt.Sprintf("%s%s%s%s", windowLabel, metric, operator, baseline)
	}
	return fmt.Sprintf("%s%s%s%s的%s倍", windowLabel, metric, operator, baseline, formatActivationRuleNumber(rule.VolumeRatio))
}

func describeActivationRuleConfirm(rule *activationRule) string {
	if rule == nil {
		return ""
	}
	windowLabel := describeActivationEvaluationWindow(rule.EvaluationWindow)
	if rule.ConfirmBars <= 1 {
		return "1根" + windowLabel + "K线确认"
	}
	return fmt.Sprintf("连续%d根%sK线确认", rule.ConfirmBars, windowLabel)
}

func describeActivationRuleExpire(rule *activationRule) string {
	if rule == nil || rule.ExpireTradeDays <= 0 {
		return ""
	}
	return fmt.Sprintf("%d个交易日内有效", rule.ExpireTradeDays)
}

func describeActivationEvaluationWindow(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "1m":
		return "1分钟"
	case "5m":
		return "5分钟"
	case "15m":
		return "15分钟"
	case "30m":
		return "30分钟"
	case "60m", "1h":
		return "60分钟"
	default:
		return "5分钟"
	}
}

func describeActivationOperator(raw string) string {
	switch strings.TrimSpace(raw) {
	case ">", "gt":
		return "高于"
	case "<", "lt":
		return "低于"
	case "<=", "le":
		return "不高于"
	case "=":
		return "等于"
	default:
		return "不低于"
	}
}

func describeActivationBaseline(rule *activationRule) string {
	if rule == nil {
		return ""
	}
	window := rule.VolumeWindow
	if window <= 0 {
		window = 5
	}
	evaluationWindow := describeActivationEvaluationWindow(rule.EvaluationWindow)
	switch strings.TrimSpace(rule.Baseline) {
	case "avg_amount_5x5m":
		return fmt.Sprintf("近%d个%s平均成交额", window, evaluationWindow)
	case "avg_volume_5x5m":
		return fmt.Sprintf("近%d个%s平均成交量", window, evaluationWindow)
	case "prev_day_same_slot_amount":
		return "上一交易日同一时刻成交额"
	case "prev_day_same_slot_volume":
		return "上一交易日同一时刻成交量"
	case "manual_amount":
		return "预设成交额阈值"
	case "manual_volume":
		return "预设成交量阈值"
	default:
		return "结构化量能阈值"
	}
}

func formatActivationRuleNumber(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(round2(v), 'f', -1, 64)
}

func segmentReplacementText(prefix string, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	if strings.TrimSpace(prefix) == "" {
		return summary
	}
	trimmedPrefix := strings.TrimRight(prefix, " \t")
	if trimmedPrefix == "" {
		return summary
	}
	last, _ := utf8.DecodeLastRuneInString(trimmedPrefix)
	switch last {
	case '\n', '\r', '|', '：', ':', '，', ',', '；', ';', '（', '(':
		return summary
	default:
		return "；" + summary
	}
}

func cleanHumanizedActivationRuleText(text string) string {
	cleaned := strings.TrimSpace(text)
	replacements := []string{
		"  ", " ",
		" ；", "；",
		" ，", "，",
		"；；", "；",
		"，，", "，",
		"；，", "；",
		"，；", "；",
	}
	for i := 0; i < len(replacements); i += 2 {
		for strings.Contains(cleaned, replacements[i]) {
			cleaned = strings.ReplaceAll(cleaned, replacements[i], replacements[i+1])
		}
	}
	cleaned = strings.ReplaceAll(cleaned, "\n \n", "\n\n")
	return cleaned
}

func cleanHumanizedMarketSummaryReport(text string) string {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			filtered = append(filtered, line)
			continue
		}
		if strings.Contains(trimmed, "activationRuleJson") {
			continue
		}
		if strings.Contains(trimmed, "机器可读规则草案") {
			continue
		}
		filtered = append(filtered, line)
	}
	return cleanHumanizedActivationRuleText(strings.Join(filtered, "\n"))
}
