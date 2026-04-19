package data

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

const (
	marketSummaryPriceMismatchThreshold = 0.20
	marketSummaryBreakoutDistanceMax    = 0.12
	marketSummaryRefScanWindowBefore    = 30 * time.Minute
	marketSummaryRefScanWindowAfter     = 2 * time.Hour
	marketSummaryActivationRepairReason = "market_summary_activation_repair"
	marketSummaryAnalysisOnlySkipReason = "缺少真实价格/量能数据，已跳过激活与回测"
)

type marketSummaryActivationRepairStats struct {
	Scanned      int
	Downgraded   int
	RuleUpgraded int
	SkippedNoRef int
}

type marketSummaryReferenceSnapshot struct {
	Price     float64
	Amount    float64
	Source    string
	TradeTime time.Time
}

func isMarketSummaryActivationSource(source string) bool {
	switch strings.TrimSpace(source) {
	case "market_summary", "market_summary_embedded":
		return true
	default:
		return false
	}
}

func validateMarketSummaryRecommendForSave(recommend *models.AiRecommendStocks) error {
	if recommend == nil || !isMarketSummaryActivationSource(recommend.ActivationRuleSource) {
		return nil
	}
	if isAnalysisOnlyRecommend(recommend) {
		return nil
	}
	if reason, bad := detectMarketSummaryPriceMismatch(*recommend); bad {
		return fmt.Errorf("市场资讯推荐价格口径异常：%s", reason)
	}
	return nil
}

func detectMarketSummaryPriceMismatch(rec models.AiRecommendStocks) (string, bool) {
	snapshot, ok := loadMarketSummaryReferenceSnapshot(rec)
	if !ok || snapshot.Price <= 0 {
		return "", false
	}

	anchorPrice := resolveRecommendReferencePrice(rec)
	if anchorPrice > 0 {
		if exceedsMarketSummaryPriceThreshold(anchorPrice, snapshot.Price, marketSummaryPriceMismatchThreshold) {
			return fmt.Sprintf("价格锚点%.2f与参考价%.2f(%s)偏离过大", anchorPrice, snapshot.Price, snapshot.Source), true
		}
	}

	buyMin, buyMax, buyOK := parseRecommendEntryRange(rec)
	if buyOK && buyMin > 0 && buyMax > 0 {
		if exceedsMarketSummaryPriceThreshold(buyMin, snapshot.Price, marketSummaryPriceMismatchThreshold) &&
			exceedsMarketSummaryPriceThreshold(buyMax, snapshot.Price, marketSummaryPriceMismatchThreshold) {
			return fmt.Sprintf("买入区间%.2f-%.2f与参考价%.2f(%s)偏离过大", buyMin, buyMax, snapshot.Price, snapshot.Source), true
		}
	}

	rule, err := parseActivationRuleJSON(rec.ActivationRuleJSON)
	if err == nil && rule != nil {
		for _, path := range activationRulePaths(rule) {
			if strings.TrimSpace(path.SignalType) != "price_breakout_with_volume" || path.ThresholdValue <= 0 {
				continue
			}
			if exceedsMarketSummaryPriceThreshold(path.ThresholdValue, snapshot.Price, marketSummaryBreakoutDistanceMax) {
				return fmt.Sprintf("突破价%.2f与参考价%.2f(%s)偏离过大", path.ThresholdValue, snapshot.Price, snapshot.Source), true
			}
		}
	}

	return "", false
}

func loadMarketSummaryReferenceSnapshot(rec models.AiRecommendStocks) (marketSummaryReferenceSnapshot, bool) {
	recordTime := recommendRecordTime(rec)
	if recordTime.IsZero() {
		return marketSummaryReferenceSnapshot{}, false
	}
	return loadMarketSummaryReferenceSnapshotByCode(recordTime, normalizeRecommendStockCode(rec.StockCode))
}

func loadMarketSummaryReferenceSnapshotByCode(recordTime time.Time, stockCode string) (marketSummaryReferenceSnapshot, bool) {
	stockCode = normalizeRecommendStockCode(stockCode)
	if recordTime.IsZero() || stockCode == "" {
		return marketSummaryReferenceSnapshot{}, false
	}
	loc := cnLocation()
	recordTime = recordTime.In(loc)
	start := recordTime.Add(-marketSummaryRefScanWindowBefore)
	end := recordTime.Add(marketSummaryRefScanWindowAfter)
	bars, _ := syncMinuteBars(stockCode, start, end, 0, false)
	if len(bars) == 0 {
		dayStart := time.Date(recordTime.Year(), recordTime.Month(), recordTime.Day(), 9, 25, 0, 0, loc)
		dayEnd := time.Date(recordTime.Year(), recordTime.Month(), recordTime.Day(), 15, 0, 0, 0, loc)
		bars, _ = syncMinuteBars(stockCode, dayStart, dayEnd, 0, false)
		if len(bars) == 0 {
			return marketSummaryReferenceSnapshot{}, false
		}
	}
	best := bars[0]
	bestGap := absDuration(best.TradeTime.Sub(recordTime))
	for _, bar := range bars[1:] {
		gap := absDuration(bar.TradeTime.Sub(recordTime))
		bestAmount := round2(best.Amount)
		barAmount := round2(bar.Amount)
		if barAmount > 0 && bestAmount <= 0 {
			best = bar
			bestGap = gap
			continue
		}
		if gap < bestGap {
			best = bar
			bestGap = gap
		}
	}
	price := best.Close
	if price <= 0 {
		price = best.Open
	}
	if price <= 0 {
		return marketSummaryReferenceSnapshot{}, false
	}
	return marketSummaryReferenceSnapshot{
		Price:     round2(price),
		Amount:    round2(best.Amount),
		Source:    "minute_bar",
		TradeTime: best.TradeTime,
	}, true
}

func normalizeMarketSummaryExecutionDataForSave(recommend *models.AiRecommendStocks) error {
	if recommend == nil || !isMarketSummaryActivationSource(recommend.ActivationRuleSource) {
		return nil
	}
	snapshot, ok := loadMarketSummaryReferenceSnapshot(*recommend)
	if !ok || snapshot.Price <= 0 || snapshot.Amount <= 0 {
		downgradeMarketSummaryRecommendToAnalysisOnly(recommend, marketSummaryReferenceSnapshot{}, marketSummaryAnalysisOnlySkipReason)
		return nil
	}
	if reason, bad := detectMarketSummaryPriceMismatch(*recommend); bad {
		downgradeMarketSummaryRecommendToAnalysisOnly(recommend, snapshot, reason+"，已降级为仅分析并跳过激活")
		return nil
	}
	applyMarketSummaryReferenceSnapshot(recommend, snapshot)
	return nil
}

func shouldAttemptRecoverHistoricalMarketSummaryRule(rec models.AiRecommendStocks) bool {
	if !isMarketSummaryActivationSource(rec.ActivationRuleSource) {
		return false
	}
	if strings.Contains(strings.TrimSpace(rec.ActivationInvalidReason), "偏离过大") {
		return true
	}
	if ruleLooksCorruptedForMarketSummary(rec.ActivationRuleJSON) {
		return true
	}
	return false
}

func ruleLooksCorruptedForMarketSummary(raw string) bool {
	rule, err := parseActivationRuleJSON(raw)
	if err != nil || rule == nil {
		return strings.TrimSpace(raw) != ""
	}
	for _, path := range activationRulePaths(rule) {
		name := strings.ToLower(strings.TrimSpace(path.Name))
		if name == "pullback" && strings.TrimSpace(path.SignalType) != "price_range_with_volume" {
			return true
		}
	}
	return false
}

func isAnalysisOnlyPlaceholderSignal(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	return strings.Contains(trimmed, "仅保留逻辑分析") || strings.Contains(trimmed, "缺少可信实时价格/量能数据")
}

func extractRecoverableMarketSummarySignal(rec models.AiRecommendStocks) string {
	candidates := []string{
		normalizeRecommendText(strings.Join([]string{
			strings.TrimSpace(rec.BuySignal),
			strings.TrimSpace(rec.BuySignalDetail),
		}, "\n")),
	}
	for _, line := range strings.Split(strings.TrimSpace(rec.Remarks), "\n") {
		line = normalizeRecommendText(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "偏离过大") || strings.Contains(line, "仅保留逻辑分析") {
			continue
		}
		if strings.Contains(line, "激活条件") || strings.Contains(line, "回踩") || strings.Contains(line, "breakout") || strings.Contains(line, "突破") {
			candidates = append(candidates, line)
		}
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || isAnalysisOnlyPlaceholderSignal(candidate) {
			continue
		}
		if _, ok := extractActivationBreakoutThreshold(candidate); ok {
			return candidate
		}
		if text, minVal, maxVal := parseMarketSummaryBuyRange(candidate); text != "" && minVal > 0 && maxVal > 0 {
			return candidate
		}
	}
	return ""
}

func tryRecoverHistoricalMarketSummaryRule(rec *models.AiRecommendStocks) bool {
	if rec == nil || !shouldAttemptRecoverHistoricalMarketSummaryRule(*rec) {
		return false
	}
	signalText := extractRecoverableMarketSummarySignal(*rec)
	if signalText == "" {
		return false
	}

	if isAnalysisOnlyPlaceholderSignal(rec.BuySignal) || strings.TrimSpace(rec.BuySignal) == "" {
		rec.BuySignal = signalText
	}
	if strings.TrimSpace(rec.BuySignalDetail) == "" {
		rec.BuySignalDetail = ""
	}
	if strings.TrimSpace(rec.RecommendBuyPrice) == "" || rec.RecommendBuyPriceMin <= 0 || rec.RecommendBuyPriceMax <= 0 {
		if text, minVal, maxVal := parseMarketSummaryBuyRange(signalText); text != "" && minVal > 0 && maxVal > 0 {
			rec.RecommendBuyPrice = text
			rec.RecommendBuyPriceMin = minVal
			rec.RecommendBuyPriceMax = maxVal
			if strings.TrimSpace(rec.FocusPrice) == "" {
				rec.FocusPrice = text
			}
		}
	}
	if strings.TrimSpace(rec.RecommendBuyPrice) == "" || rec.RecommendBuyPriceMin <= 0 || rec.RecommendBuyPriceMax <= 0 {
		return false
	}

	rec.RecommendStatus = "valid"
	rec.ExecutionState = recommendExecutionConditional
	rec.ActivationStatus = "pending"
	rec.ActivationInvalidReason = ""
	rec.ActivationRuleJSON = ""
	rec.ActivationRuleVersion = ""

	if err := normalizeActivationRuleForSave(rec); err != nil {
		return false
	}
	return true
}

func applyMarketSummaryReferenceSnapshot(recommend *models.AiRecommendStocks, snapshot marketSummaryReferenceSnapshot) {
	if recommend == nil || snapshot.Price <= 0 {
		return
	}
	priceText := formatRecommendPrice(snapshot.Price)
	recommend.StockPrice = priceText
	recommend.StockCurrentPrice = priceText
	recommend.StockClosePrice = priceText
	recommend.StockPrePrice = priceText
	recommend.ObservePrice = priceText
	if !snapshot.TradeTime.IsZero() {
		recommend.StockCurrentPriceTime = snapshot.TradeTime.In(cnLocation()).Format(time.DateTime)
	}
}

func downgradeMarketSummaryRecommendToAnalysisOnly(recommend *models.AiRecommendStocks, snapshot marketSummaryReferenceSnapshot, reason string) {
	if recommend == nil {
		return
	}
	reason = normalizeRecommendText(reason)
	if reason == "" {
		reason = marketSummaryAnalysisOnlySkipReason
	}
	applyMarketSummaryReferenceSnapshot(recommend, snapshot)
	recommend.RecommendStatus = "missing_market_data"
	recommend.ExecutionState = recommendExecutionAnalysisOnly
	recommend.ActivationStatus = "skipped"
	recommend.RecommendBuyPrice = ""
	recommend.RecommendBuyPriceMin = 0
	recommend.RecommendBuyPriceMax = 0
	recommend.RecommendStopProfitPrice = ""
	recommend.RecommendStopProfitPriceMin = 0
	recommend.RecommendStopProfitPriceMax = 0
	recommend.RecommendStopLossPrice = ""
	recommend.FocusPrice = ""
	recommend.BuySignal = "缺少可信实时价格/量能数据，本次仅保留逻辑分析，不生成交易计划"
	recommend.BuySignalDetail = ""
	recommend.SellSignal = ""
	recommend.SellSignalDetail = ""
	recommend.InvalidSignal = ""
	recommend.InvalidCondition = marketSummaryAnalysisOnlySkipReason
	recommend.ActivationInvalidReason = reason
	if recommend.Remarks == "" {
		recommend.Remarks = reason
	} else if !strings.Contains(recommend.Remarks, reason) {
		recommend.Remarks = normalizeRecommendText(recommend.Remarks + "\n" + reason)
	}
}

func exceedsMarketSummaryPriceThreshold(left, right, threshold float64) bool {
	if left <= 0 || right <= 0 || threshold <= 0 {
		return false
	}
	diff := left - right
	if diff < 0 {
		diff = -diff
	}
	return diff/right > threshold
}

func absDuration(v time.Duration) time.Duration {
	if v < 0 {
		return -v
	}
	return v
}

func RepairHistoricalMarketSummaryActivationIssues(now time.Time) (marketSummaryActivationRepairStats, error) {
	stats := marketSummaryActivationRepairStats{}
	var rows []models.AiRecommendStocks
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("summary_version = ?", marketSummaryPhase3Version).
		Where("activation_rule_source IN ?", []string{"market_summary", "market_summary_embedded"}).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return stats, err
	}
	changedCodes := make([]string, 0, len(rows))
	for _, row := range rows {
		stats.Scanned++
		updateMap := map[string]any{}
		rec := row
		recovered := tryRecoverHistoricalMarketSummaryRule(&rec)
		if err := normalizeMarketSummaryExecutionDataForSave(&rec); err != nil {
			return stats, err
		}
		if isAnalysisOnlyRecommend(&rec) {
			updateMap["stock_price"] = rec.StockPrice
			updateMap["stock_current_price"] = rec.StockCurrentPrice
			updateMap["stock_current_price_time"] = rec.StockCurrentPriceTime
			updateMap["stock_close_price"] = rec.StockClosePrice
			updateMap["stock_pre_price"] = rec.StockPrePrice
			updateMap["observe_price"] = rec.ObservePrice
			updateMap["recommend_status"] = rec.RecommendStatus
			updateMap["execution_state"] = rec.ExecutionState
			updateMap["recommend_buy_price"] = ""
			updateMap["recommend_buy_price_min"] = 0
			updateMap["recommend_buy_price_max"] = 0
			updateMap["recommend_stop_profit_price"] = ""
			updateMap["recommend_stop_profit_price_min"] = 0
			updateMap["recommend_stop_profit_price_max"] = 0
			updateMap["recommend_stop_loss_price"] = ""
			updateMap["focus_price"] = ""
			updateMap["buy_signal"] = rec.BuySignal
			updateMap["buy_signal_detail"] = ""
			updateMap["sell_signal"] = ""
			updateMap["sell_signal_detail"] = ""
			updateMap["invalid_signal"] = ""
			updateMap["invalid_condition"] = rec.InvalidCondition
			updateMap["activation_rule_json"] = ""
			updateMap["activation_rule_version"] = ""
			updateMap["activation_status"] = "invalid"
			updateMap["activation_invalid_reason"] = rec.ActivationInvalidReason
			updateMap["remarks"] = rec.Remarks
			stats.Downgraded++
		} else {
			rec.ActivationRuleJSON = ""
			rec.ActivationRuleVersion = ""
			rec.ActivationInvalidReason = ""
			if err := normalizeActivationRuleForSave(&rec); err != nil {
				downgradeMarketSummaryRecommendToAnalysisOnly(&rec, marketSummaryReferenceSnapshot{}, "结构化激活规则重建失败："+err.Error())
				updateMap["recommend_status"] = rec.RecommendStatus
				updateMap["execution_state"] = rec.ExecutionState
				updateMap["activation_status"] = "invalid"
				updateMap["activation_invalid_reason"] = rec.ActivationInvalidReason
				updateMap["remarks"] = rec.Remarks
				stats.Downgraded++
			} else if recovered ||
				rec.ActivationRuleJSON != row.ActivationRuleJSON ||
				rec.ActivationRuleVersion != row.ActivationRuleVersion ||
				rec.StockPrice != row.StockPrice ||
				rec.StockCurrentPrice != row.StockCurrentPrice ||
				rec.StockCurrentPriceTime != row.StockCurrentPriceTime ||
				rec.RecommendStatus != row.RecommendStatus ||
				rec.ExecutionState != row.ExecutionState ||
				rec.RecommendBuyPrice != row.RecommendBuyPrice ||
				rec.RecommendBuyPriceMin != row.RecommendBuyPriceMin ||
				rec.RecommendBuyPriceMax != row.RecommendBuyPriceMax ||
				rec.FocusPrice != row.FocusPrice ||
				rec.BuySignal != row.BuySignal ||
				rec.BuySignalDetail != row.BuySignalDetail ||
				rec.ActivationStatus != row.ActivationStatus {
				updateMap["stock_price"] = rec.StockPrice
				updateMap["stock_current_price"] = rec.StockCurrentPrice
				updateMap["stock_current_price_time"] = rec.StockCurrentPriceTime
				updateMap["stock_close_price"] = rec.StockClosePrice
				updateMap["stock_pre_price"] = rec.StockPrePrice
				updateMap["observe_price"] = rec.ObservePrice
				updateMap["recommend_status"] = rec.RecommendStatus
				updateMap["execution_state"] = rec.ExecutionState
				updateMap["recommend_buy_price"] = rec.RecommendBuyPrice
				updateMap["recommend_buy_price_min"] = rec.RecommendBuyPriceMin
				updateMap["recommend_buy_price_max"] = rec.RecommendBuyPriceMax
				updateMap["focus_price"] = rec.FocusPrice
				updateMap["buy_signal"] = rec.BuySignal
				updateMap["buy_signal_detail"] = rec.BuySignalDetail
				updateMap["activation_status"] = rec.ActivationStatus
				updateMap["activation_rule_json"] = rec.ActivationRuleJSON
				updateMap["activation_rule_version"] = rec.ActivationRuleVersion
				updateMap["activation_invalid_reason"] = ""
				stats.RuleUpgraded++
			}
		}
		if len(updateMap) == 0 {
			stats.SkippedNoRef++
			continue
		}
		if err := db.Dao.Model(&models.AiRecommendStocks{}).Where("id = ?", row.ID).Updates(updateMap).Error; err != nil {
			return stats, err
		}
		changedCodes = append(changedCodes, normalizeRecommendStockCode(row.StockCode))
	}
	if len(changedCodes) > 0 {
		requestAiRecommendYieldRecalcWithScope(true, marketSummaryActivationRepairReason, changedCodes)
	}
	return stats, nil
}
