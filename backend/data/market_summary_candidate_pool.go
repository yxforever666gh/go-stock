package data

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

const (
	marketSummaryIndicatorSearchPageSize = 80
	marketSummaryIndicatorCandidateLimit = 120
	marketSummaryIndicatorAIInputLimit   = 50
)

type marketSummaryIndicatorTemplate struct {
	Name   string
	Query  string
	Weight int
}

type marketSummaryIndicatorCandidate struct {
	StockName   string            `json:"stockName"`
	StockCode   string            `json:"stockCode"`
	Direction   string            `json:"direction,omitempty"`
	BkName      string            `json:"bkName,omitempty"`
	Source      string            `json:"source,omitempty"`
	Score       int               `json:"score"`
	ScoreBreakdown map[string]int `json:"scoreBreakdown,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Metrics     map[string]string `json:"metrics,omitempty"`
	SourceNames []string          `json:"sourceNames,omitempty"`
}

var marketSummaryIndicatorTemplates = []marketSummaryIndicatorTemplate{
	{
		Name:   "strong-breakout",
		Query:  "今日涨幅大于等于2%小于等于9%;量比大于等于1.1小于等于5;换手率大于等于2%小于等于20%;成交额大于2亿元;不要ST股及不要退市股;不要北交所",
		Weight: 32,
	},
	{
		Name:   "volume-confirm",
		Query:  "量比大于等于1.5小于等于6;成交额大于2亿元;股价大于5小于80;今日涨幅大于0小于9%;不要ST股及不要退市股;不要北交所",
		Weight: 28,
	},
	{
		Name:   "trend-ma",
		Query:  "5日均线大于10日均线;10日均线大于20日均线;股价在20日均线以上;今日涨幅大于0小于8%;成交额大于1亿元;不要ST股及不要退市股;不要北交所",
		Weight: 26,
	},
	{
		Name:   "pullback-support",
		Query:  "股价在20日线以上;回调到5日线或10日线附近;量比大于1;换手率大于2%;成交额大于1亿元;不要ST股及不要退市股;不要北交所",
		Weight: 24,
	},
	{
		Name:   "sector-leader",
		Query:  "热门板块中涨幅领先的A股;今日涨幅大于2%小于9%;量比大于1;成交额大于2亿元;不要ST股及不要退市股;不要北交所",
		Weight: 22,
	},
}

func buildMarketSummaryIndicatorCandidatePool(limit int, logState *marketSummaryRouteLog) []marketSummaryIndicatorCandidate {
	if limit <= 0 {
		limit = marketSummaryIndicatorCandidateLimit
	}
	type templateResult struct {
		template marketSummaryIndicatorTemplate
		rows     []map[string]any
	}
	results := make([]templateResult, len(marketSummaryIndicatorTemplates))
	var wg sync.WaitGroup
	for idx, tpl := range marketSummaryIndicatorTemplates {
		wg.Add(1)
		go func(i int, item marketSummaryIndicatorTemplate) {
			defer wg.Done()
			res := runWithTimeout(5*time.Second, map[string]any{}, func() map[string]any {
				return NewSearchStockApi(item.Query).SearchStock(marketSummaryIndicatorSearchPageSize)
			})
			results[i] = templateResult{template: item, rows: extractSearchStockRows(res)}
		}(idx, tpl)
	}
	wg.Wait()

	index := map[string]*marketSummaryIndicatorCandidate{}
	sectorStrength := loadMarketSummarySectorStrengthMap()
	recentFailures := loadMarketSummaryRecentFailurePenaltyMap(10)
	for _, result := range results {
		if len(result.rows) == 0 && logState != nil {
			logState.addNote("indicator template %s returned 0 rows", result.template.Name)
		}
		for _, row := range result.rows {
			candidate := buildMarketSummaryIndicatorCandidate(row, result.template)
			if candidate.StockCode == "" || candidate.StockName == "" {
				continue
			}
			applyMarketSummaryCandidateQualityScore(&candidate, sectorStrength, recentFailures)
			existing := index[candidate.StockCode]
			if existing == nil {
				index[candidate.StockCode] = &candidate
				continue
			}
			existing.Score += maxInt(4, result.template.Weight/3)
			existing.SourceNames = dedupeNonEmptyStrings(append(existing.SourceNames, result.template.Name), 8)
			if existing.Direction == "" {
				existing.Direction = candidate.Direction
			}
			if existing.BkName == "" {
				existing.BkName = candidate.BkName
			}
			for key, value := range candidate.Metrics {
				if strings.TrimSpace(existing.Metrics[key]) == "" {
					existing.Metrics[key] = value
				}
			}
			existing.Reason = buildIndicatorCandidateReason(*existing)
		}
	}

	items := make([]marketSummaryIndicatorCandidate, 0, len(index))
	for _, item := range index {
		item.SourceNames = dedupeNonEmptyStrings(item.SourceNames, 8)
		item.Reason = buildIndicatorCandidateReason(*item)
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].StockCode < items[j].StockCode
	})
	if len(items) > limit {
		items = items[:limit]
	}
	if logState != nil {
		logState.addNote("indicator candidate pool size=%d", len(items))
	}
	return items
}

func extractSearchStockRows(res map[string]any) []map[string]any {
	if !isSearchStockSuccessCode(res["code"]) {
		return nil
	}
	dataMap, ok := res["data"].(map[string]any)
	if !ok {
		return nil
	}
	resultMap, ok := dataMap["result"].(map[string]any)
	if !ok {
		return nil
	}
	rawRows, ok := resultMap["dataList"].([]any)
	if !ok {
		return nil
	}
	rows := make([]map[string]any, 0, len(rawRows))
	for _, raw := range rawRows {
		row, ok := raw.(map[string]any)
		if ok {
			rows = append(rows, row)
		}
	}
	return rows
}

func isSearchStockSuccessCode(value any) bool {
	text := strings.TrimSpace(anyToString(value))
	if text == "100" {
		return true
	}
	number, ok := parseLooseFloat(text)
	return ok && int(number) == 100
}

func buildMarketSummaryIndicatorCandidate(row map[string]any, tpl marketSummaryIndicatorTemplate) marketSummaryIndicatorCandidate {
	code := normalizeMarketSummaryIndicatorStockCode(row)
	name := firstNonEmptyText(
		anyToString(row["SECURITY_NAME_ABBR"]),
		anyToString(row["SECURITY_SHORT_NAME"]),
		anyToString(row["SECURITY_NAME"]),
		anyToString(row["股票简称"]),
	)
	direction := firstNonEmptyText(
		anyToString(row["INDUSTRY_NAME"]),
		anyToString(row["BOARD_NAME"]),
		anyToString(row["BK_NAME"]),
		anyToString(row["所属行业"]),
	)
	candidate := marketSummaryIndicatorCandidate{
		StockName:   strings.TrimSpace(name),
		StockCode:   code,
		Direction:   strings.TrimSpace(direction),
		BkName:      strings.TrimSpace(direction),
		Source:      "indicator_pool",
		Score:       tpl.Weight,
		ScoreBreakdown: map[string]int{
			"base": tpl.Weight,
		},
		Metrics:     map[string]string{},
		SourceNames: []string{tpl.Name},
	}
	metricKeys := map[string][]string{
		"changePct":   {"CHANGE_RATE", "涨跌幅", "涨幅", "最新涨跌幅"},
		"volumeRatio": {"VOLUME_RATIO", "量比"},
		"turnover":    {"TURNOVERRATE", "TURNOVER_RATE", "换手率"},
		"amount":      {"AMOUNT", "成交额"},
		"price":       {"NEW_PRICE", "LATEST_PRICE", "最新价", "股价"},
		"marketValue": {"TOTAL_MARKET_CAP", "总市值", "流通市值"},
	}
	for metric, keys := range metricKeys {
		if text := firstRowValue(row, keys...); text != "" {
			candidate.Metrics[metric] = text
		}
	}
	metricScore := scoreIndicatorCandidateMetrics(candidate.Metrics)
	candidate.Score += metricScore
	candidate.ScoreBreakdown["metrics"] = metricScore
	candidate.Reason = buildIndicatorCandidateReason(candidate)
	return candidate
}

func normalizeMarketSummaryIndicatorStockCode(row map[string]any) string {
	raw := firstNonEmptyText(
		anyToString(row["SECURITY_CODE"]),
		anyToString(row["SECURITY_CODE_A"]),
		anyToString(row["SECUCODE"]),
		anyToString(row["代码"]),
	)
	code := normalizeRecommendStockCode(raw)
	if isAShareTsCode(code) {
		return code
	}
	digits := onlyDigits(raw)
	if len(digits) > 6 {
		digits = digits[:6]
	}
	if len(digits) != 6 {
		return ""
	}
	market := strings.ToUpper(firstNonEmptyText(
		anyToString(row["MARKET_SHORT_NAME"]),
		anyToString(row["TRADE_MARKET_CODE"]),
		anyToString(row["市场简称"]),
	))
	switch {
	case strings.Contains(market, "SH"), strings.HasPrefix(digits, "6"):
		return digits + ".SH"
	case strings.Contains(market, "BJ"), strings.HasPrefix(digits, "8"), strings.HasPrefix(digits, "4"):
		return digits + ".BJ"
	default:
		return digits + ".SZ"
	}
}

func firstRowValue(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(anyToString(row[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func scoreIndicatorCandidateMetrics(metrics map[string]string) int {
	score := 0
	changePct, hasChange := parseLooseFloat(metrics["changePct"])
	if hasChange {
		switch {
		case changePct >= 2 && changePct <= 7:
			score += 18
		case changePct > 0 && changePct < 9.5:
			score += 10
		case changePct > 9.5:
			score -= 12
		case changePct < 0:
			score -= 16
		}
	}
	volumeRatio, hasVolumeRatio := parseLooseFloat(metrics["volumeRatio"])
	if hasVolumeRatio {
		switch {
		case volumeRatio >= 1.2 && volumeRatio <= 4.5:
			score += 18
		case volumeRatio > 4.5 && volumeRatio <= 8:
			score += 8
		case volumeRatio < 1:
			score -= 8
		}
	}
	turnover, hasTurnover := parseLooseFloat(metrics["turnover"])
	if hasTurnover {
		switch {
		case turnover >= 3 && turnover <= 18:
			score += 12
		case turnover > 18 && turnover <= 30:
			score += 4
		case turnover > 30:
			score -= 6
		}
	}
	price, hasPrice := parseLooseFloat(metrics["price"])
	if hasPrice {
		if price >= 5 && price <= 80 {
			score += 8
		} else if price > 120 {
			score -= 4
		}
	}
	if strings.TrimSpace(metrics["amount"]) != "" {
		score += 6
	}
	return score
}

func applyMarketSummaryCandidateQualityScore(item *marketSummaryIndicatorCandidate, sectorStrength map[string]int, recentFailures map[string]int) {
	if item == nil {
		return
	}
	if item.ScoreBreakdown == nil {
		item.ScoreBreakdown = map[string]int{}
	}
	sectorScore := sectorStrength[strings.TrimSpace(firstNonEmptyText(item.BkName, item.Direction))]
	failurePenalty := recentFailures[normalizeRecommendStockCode(item.StockCode)]
	completenessScore := scoreMarketSummaryCandidateDataCompleteness(*item)
	item.ScoreBreakdown["sectorStrength"] = sectorScore
	item.ScoreBreakdown["recentFailurePenalty"] = failurePenalty
	item.ScoreBreakdown["dataCompleteness"] = completenessScore
	item.Score += sectorScore + failurePenalty + completenessScore
	item.ScoreBreakdown["total"] = item.Score
}

func scoreMarketSummaryCandidateDataCompleteness(item marketSummaryIndicatorCandidate) int {
	required := []string{"price", "amount", "volumeRatio", "turnover"}
	missing := 0
	for _, key := range required {
		if strings.TrimSpace(item.Metrics[key]) == "" {
			missing++
		}
	}
	if strings.TrimSpace(firstNonEmptyText(item.BkName, item.Direction)) == "" {
		missing++
	}
	switch missing {
	case 0:
		return 8
	case 1:
		return 2
	case 2:
		return -6
	default:
		return -12
	}
}

func loadMarketSummarySectorStrengthMap() map[string]int {
	result := map[string]int{}
	raw := runWithTimeout(3*time.Second, map[string]any{"data": []any{}}, func() map[string]any {
		return NewMarketNewsApi().GetIndustryRank("0", 20)
	})
	rows, _ := raw["data"].([]any)
	for idx, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		name := firstNonEmptyText(anyToString(m["industry_name"]), anyToString(m["name"]), anyToString(m["plate_name"]), anyToString(m["bk_name"]))
		if name == "" {
			continue
		}
		score := 0
		switch {
		case idx < 3:
			score = 14
		case idx < 8:
			score = 9
		case idx < 15:
			score = 5
		default:
			score = 2
		}
		if inflow, ok := parseLooseFloat(firstNonEmptyText(anyToString(m["zlje"]), anyToString(m["net_inflow"]), anyToString(m["main_net_inflow"]))); ok && inflow > 0 {
			score += 4
		}
		result[strings.TrimSpace(name)] = score
	}
	return result
}

func loadMarketSummaryRecentFailurePenaltyMap(tradeDays int) map[string]int {
	if tradeDays <= 0 {
		tradeDays = 10
	}
	result := map[string]int{}
	since := time.Now().AddDate(0, 0, -tradeDays*2)
	rows := make([]models.AiRecommendStocks, 0, 128)
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("data_time >= ?", since).
		Where("(summary_version IN ? OR activation_rule_source IN ?)", marketSummaryKnownVersions(), []string{"market_summary", "market_summary_embedded"}).
		Find(&rows).Error; err != nil {
		return result
	}
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		penalty := 0
		if isAnalysisOnlyRecommend(&row) {
			penalty -= 5
		}
		if strings.Contains(row.InvalidCondition, "止损") || strings.Contains(row.ActivationInvalidReason, "止损") {
			penalty -= 8
		}
		if strings.Contains(row.InvalidCondition, "源头质量") || strings.Contains(row.InvalidCondition, "硬校验") ||
			strings.Contains(row.InvalidCondition, "价格锚点") || strings.Contains(row.InvalidCondition, "盈亏比") {
			penalty -= 6
		}
		result[code] += penalty
		if result[code] < -24 {
			result[code] = -24
		}
	}
	return result
}

func buildIndicatorCandidateReason(item marketSummaryIndicatorCandidate) string {
	parts := []string{
		joinKeyValue("source", strings.Join(item.SourceNames, ",")),
		joinKeyValue("changePct", item.Metrics["changePct"]),
		joinKeyValue("volumeRatio", item.Metrics["volumeRatio"]),
		joinKeyValue("turnover", item.Metrics["turnover"]),
		joinKeyValue("amount", item.Metrics["amount"]),
	}
	return strings.Trim(strings.Join(dedupeNonEmptyStrings(parts, 8), "; "), "; ")
}

func marketSummaryIndicatorCandidatesToRouteCandidates(items []marketSummaryIndicatorCandidate) []marketSummaryRouteCandidate {
	result := make([]marketSummaryRouteCandidate, 0, len(items))
	for _, item := range items {
		result = append(result, marketSummaryRouteCandidate{
			StockName:  item.StockName,
			StockCode:  item.StockCode,
			Direction:  firstNonEmptyText(item.Direction, item.BkName),
			BkName:     firstNonEmptyText(item.BkName, item.Direction),
			Reason:     item.Reason,
			SourceHint: strings.TrimSpace("indicator_pool:" + strings.Join(item.SourceNames, ",")),
		})
	}
	return result
}

func limitMarketSummaryIndicatorCandidates(items []marketSummaryIndicatorCandidate, limit int) []marketSummaryIndicatorCandidate {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return append([]marketSummaryIndicatorCandidate(nil), items[:limit]...)
}

func mergeMarketSummaryDiscoveryCandidates(discovery *marketSummaryDiscoveryResult, indicators []marketSummaryIndicatorCandidate, limit int) {
	if discovery == nil || len(indicators) == 0 {
		return
	}
	if limit <= 0 {
		limit = defaultMarketSummaryRouteBudget().CandidateLimit
	}
	merged := make([]marketSummaryRouteCandidate, 0, limit)
	seen := map[string]struct{}{}
	for _, item := range discovery.CandidateStocks {
		candidate := normalizeMarketSummaryCandidate(item)
		if candidate.StockCode == "" {
			continue
		}
		if _, ok := seen[candidate.StockCode]; ok {
			continue
		}
		seen[candidate.StockCode] = struct{}{}
		merged = append(merged, candidate)
		if len(merged) >= limit {
			discovery.CandidateStocks = merged
			return
		}
	}
	for _, item := range marketSummaryIndicatorCandidatesToRouteCandidates(indicators) {
		candidate := normalizeMarketSummaryCandidate(item)
		if candidate.StockCode == "" {
			continue
		}
		if _, ok := seen[candidate.StockCode]; ok {
			continue
		}
		seen[candidate.StockCode] = struct{}{}
		merged = append(merged, candidate)
		if len(merged) >= limit {
			break
		}
	}
	discovery.CandidateStocks = merged
}

func parseLooseFloat(text string) (float64, bool) {
	cleaned := strings.TrimSpace(text)
	if cleaned == "" {
		return 0, false
	}
	matched := marketSummaryNumberPattern.FindString(cleaned)
	if matched == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(matched, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func onlyDigits(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
