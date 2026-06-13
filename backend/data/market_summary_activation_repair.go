package data

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

const (
	marketSummaryPriceMismatchThreshold  = 0.20
	marketSummaryBreakoutDistanceMax     = 0.12
	marketSummaryRefScanWindowBefore     = 30 * time.Minute
	marketSummaryRefScanWindowAfter      = 2 * time.Hour
	marketSummaryActivationRepairReason  = "market_summary_activation_repair"
	marketSummaryAnalysisOnlySkipReason  = "未生成可交易激活规则，已跳过激活与回测"
	marketSummaryPendingMarketDataReason = "等待本地分钟线补齐后激活与回测"
)

type marketSummaryActivationRepairStats struct {
	Scanned      int
	Downgraded   int
	RuleUpgraded int
	Recovered    int
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
	return detectMarketSummaryPriceMismatchWithFetch(rec, true)
}

func detectMarketSummaryPriceMismatchWithFetch(rec models.AiRecommendStocks, allowFetch bool) (string, bool) {
	snapshot, ok := loadMarketSummaryReferenceSnapshotWithFetch(rec, allowFetch)
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
	return loadMarketSummaryReferenceSnapshotWithFetch(rec, true)
}

func loadMarketSummaryReferenceSnapshotWithFetch(rec models.AiRecommendStocks, allowFetch bool) (marketSummaryReferenceSnapshot, bool) {
	recordTime := recommendRecordTime(rec)
	if recordTime.IsZero() {
		return marketSummaryReferenceSnapshot{}, false
	}
	return loadMarketSummaryReferenceSnapshotByCodeWithFetch(recordTime, normalizeRecommendStockCode(rec.StockCode), allowFetch)
}

func loadMarketSummaryReferenceSnapshotByCode(recordTime time.Time, stockCode string) (marketSummaryReferenceSnapshot, bool) {
	return loadMarketSummaryReferenceSnapshotByCodeWithFetch(recordTime, stockCode, true)
}

func loadMarketSummaryReferenceSnapshotByCodeWithFetch(recordTime time.Time, stockCode string, allowFetch bool) (marketSummaryReferenceSnapshot, bool) {
	stockCode = normalizeRecommendStockCode(stockCode)
	if recordTime.IsZero() || stockCode == "" {
		return marketSummaryReferenceSnapshot{}, false
	}
	loc := cnLocation()
	recordTime = recordTime.In(loc)
	start := recordTime.Add(-marketSummaryRefScanWindowBefore)
	end := recordTime.Add(marketSummaryRefScanWindowAfter)
	bars, _ := syncMinuteBarsWithFetch(stockCode, start, end, 0, false, allowFetch)
	if len(bars) == 0 {
		dayStart := time.Date(recordTime.Year(), recordTime.Month(), recordTime.Day(), 9, 25, 0, 0, loc)
		dayEnd := time.Date(recordTime.Year(), recordTime.Month(), recordTime.Day(), 15, 0, 0, 0, loc)
		bars, _ = syncMinuteBarsWithFetch(stockCode, dayStart, dayEnd, 0, false, allowFetch)
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
	return normalizeMarketSummaryExecutionDataForSaveWithFetch(recommend, true)
}

func normalizeMarketSummaryExecutionDataForSaveWithFetch(recommend *models.AiRecommendStocks, allowFetch bool) error {
	if recommend == nil || !isMarketSummaryActivationSource(recommend.ActivationRuleSource) {
		return nil
	}
	snapshot, ok := loadMarketSummaryReferenceSnapshotWithFetch(*recommend, allowFetch)
	if !ok || snapshot.Price <= 0 || snapshot.Amount <= 0 {
		if hasRecoverableMarketSummaryTradePlan(*recommend) {
			markMarketSummaryRecommendPendingMarketData(recommend, formatMarketSummaryPendingMarketDataReason(recommend))
		} else {
			downgradeMarketSummaryRecommendToAnalysisOnly(recommend, marketSummaryReferenceSnapshot{}, "行情/分钟线数据缺失，且缺少可恢复的交易计划")
		}
		return nil
	}
	if reason, bad := detectMarketSummaryPriceMismatchWithFetch(*recommend, allowFetch); bad {
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
	if isRecoverableMarketSummaryMarketDataGap(rec) {
		return true
	}
	if marketSummaryRecommendMissingExitPlan(rec) {
		return true
	}
	if marketSummaryRecommendHasStaleMissingMarketDataReason(rec) {
		return true
	}
	if strings.Contains(strings.TrimSpace(rec.ActivationInvalidReason), "偏离过大") {
		return true
	}
	if ruleLooksCorruptedForMarketSummary(rec.ActivationRuleJSON) {
		return true
	}
	return false
}

func hasRecoverableMarketSummaryTradePlan(rec models.AiRecommendStocks) bool {
	if !isMarketSummaryActivationSource(rec.ActivationRuleSource) {
		return false
	}
	if strings.TrimSpace(rec.ActivationRuleJSON) != "" {
		return true
	}
	if strings.TrimSpace(rec.RecommendBuyPrice) != "" && strings.TrimSpace(rec.RecommendStopProfitPrice) != "" && strings.TrimSpace(rec.RecommendStopLossPrice) != "" {
		return true
	}
	return false
}

func marketSummaryRecommendMissingExitPlan(rec models.AiRecommendStocks) bool {
	if !isMarketSummaryActivationSource(rec.ActivationRuleSource) {
		return false
	}
	status := normalizeRecommendStatus(rec.RecommendStatus)
	if status != "valid" && status != recommendStatusPendingMarketData && status != "missing_market_data" {
		return false
	}
	if strings.TrimSpace(rec.RecommendStopProfitPrice) != "" && strings.TrimSpace(rec.RecommendStopLossPrice) != "" {
		return false
	}
	return strings.TrimSpace(rec.RecommendBuyPrice) != "" || strings.TrimSpace(rec.ActivationRuleJSON) != ""
}

func marketSummaryRecommendHasStaleMissingMarketDataReason(rec models.AiRecommendStocks) bool {
	if !isMarketSummaryActivationSource(rec.ActivationRuleSource) {
		return false
	}
	if strings.TrimSpace(rec.RecommendBuyPrice) == "" || strings.TrimSpace(rec.RecommendStopProfitPrice) == "" || strings.TrimSpace(rec.RecommendStopLossPrice) == "" {
		return false
	}
	return strings.Contains(rec.InvalidCondition, marketSummaryAnalysisOnlySkipReason) ||
		strings.Contains(rec.InvalidCondition, "缺少真实价格/量能数据") ||
		strings.Contains(rec.ActivationInvalidReason, marketSummaryAnalysisOnlySkipReason) ||
		strings.Contains(rec.ActivationInvalidReason, "缺少真实价格/量能数据")
}

func isRecoverableMarketSummaryMarketDataGap(rec models.AiRecommendStocks) bool {
	if !isMarketSummaryActivationSource(rec.ActivationRuleSource) {
		return false
	}
	status := normalizeRecommendStatus(rec.RecommendStatus)
	activationStatus := strings.TrimSpace(strings.ToLower(rec.ActivationStatus))
	if status != recommendStatusPendingMarketData && status != "missing_market_data" && activationStatus != recommendActivationPendingData && activationStatus != "skipped" {
		return false
	}
	return hasRecoverableMarketSummaryTradePlan(rec)
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
	recoverMarketSummaryTradePlanFromStoredReport(rec)
	signalText := extractRecoverableMarketSummarySignal(*rec)
	if signalText == "" && strings.TrimSpace(rec.ActivationRuleJSON) == "" {
		return false
	}

	if signalText != "" && (isAnalysisOnlyPlaceholderSignal(rec.BuySignal) || strings.TrimSpace(rec.BuySignal) == "") {
		rec.BuySignal = signalText
	}
	if strings.TrimSpace(rec.BuySignalDetail) == "" {
		rec.BuySignalDetail = ""
	}
	if signalText != "" && (strings.TrimSpace(rec.RecommendBuyPrice) == "" || rec.RecommendBuyPriceMin <= 0 || rec.RecommendBuyPriceMax <= 0) {
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
	recoverMarketSummaryExitPlan(rec)

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

func recoverMarketSummaryTradePlanFromStoredReport(rec *models.AiRecommendStocks) {
	if rec == nil || rec.DataTime == nil {
		return
	}
	if strings.TrimSpace(rec.RecommendBuyPrice) != "" &&
		strings.TrimSpace(rec.RecommendStopProfitPrice) != "" &&
		strings.TrimSpace(rec.RecommendStopLossPrice) != "" &&
		!marketSummaryRecommendHasStaleMissingMarketDataReason(*rec) {
		return
	}
	draft := loadMarketSummaryRecommendDraftFromStoredReport(*rec)
	if draft == nil {
		return
	}
	if strings.TrimSpace(rec.RecommendBuyPrice) == "" && strings.TrimSpace(draft.RecommendBuyPrice) != "" {
		rec.RecommendBuyPrice = draft.RecommendBuyPrice
		rec.RecommendBuyPriceMin = draft.RecommendBuyPriceMin
		rec.RecommendBuyPriceMax = draft.RecommendBuyPriceMax
		if strings.TrimSpace(rec.FocusPrice) == "" {
			rec.FocusPrice = draft.RecommendBuyPrice
		}
	}
	if strings.TrimSpace(rec.RecommendStopProfitPrice) == "" && strings.TrimSpace(draft.RecommendStopProfitPrice) != "" {
		rec.RecommendStopProfitPrice = draft.RecommendStopProfitPrice
		rec.RecommendStopProfitPriceMin = draft.RecommendStopProfitPriceMin
		rec.RecommendStopProfitPriceMax = draft.RecommendStopProfitPriceMax
	}
	if strings.TrimSpace(rec.RecommendStopLossPrice) == "" && strings.TrimSpace(draft.RecommendStopLossPrice) != "" {
		rec.RecommendStopLossPrice = draft.RecommendStopLossPrice
	}
	if isAnalysisOnlyPlaceholderSignal(rec.BuySignal) && strings.TrimSpace(draft.BuySignal) != "" {
		rec.BuySignal = draft.BuySignal
		rec.BuySignalDetail = draft.BuySignalDetail
	}
	if (strings.TrimSpace(rec.InvalidCondition) == "" || strings.Contains(rec.InvalidCondition, marketSummaryAnalysisOnlySkipReason) || strings.Contains(rec.InvalidCondition, "缺少真实价格/量能数据")) && strings.TrimSpace(draft.InvalidCondition) != "" {
		rec.InvalidCondition = draft.InvalidCondition
	}
	if strings.TrimSpace(rec.RiskRemarks) == "" && strings.TrimSpace(draft.RiskRemarks) != "" {
		rec.RiskRemarks = draft.RiskRemarks
	}
	if strings.TrimSpace(rec.Remarks) == "" && strings.TrimSpace(draft.Remarks) != "" {
		rec.Remarks = draft.Remarks
	}
}

func loadMarketSummaryRecommendDraftFromStoredReport(rec models.AiRecommendStocks) *marketSummaryRecommendDraft {
	code := normalizeRecommendStockCode(rec.StockCode)
	if code == "" || rec.DataTime == nil {
		return nil
	}
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.AIResponseResult{}) {
		return nil
	}
	start, end := marketSummaryDayBounds(*rec.DataTime)
	var reports []models.AIResponseResult
	if err := db.Dao.Model(&models.AIResponseResult{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Where("(stock_code = ? OR stock_name = ? OR question LIKE ?)", "市场资讯", "市场资讯", "%市场%").
		Order("created_at asc").
		Find(&reports).Error; err != nil {
		return nil
	}
	for idx := range reports {
		report := reports[idx]
		if strings.TrimSpace(report.Content) == "" {
			continue
		}
		if !strings.Contains(report.Content, rec.StockCode) && !strings.Contains(report.Content, rec.StockName) {
			continue
		}
		drafts := parseMarketSummaryRecommendStockDraftsWithVersion(report.Content, report.ProviderName, report.ModelName, *rec.DataTime, rec.SummaryVersion)
		for _, draft := range drafts {
			if draft == nil || normalizeRecommendStockCode(draft.StockCode) != code {
				continue
			}
			return draft
		}
	}
	return nil
}

func recoverMarketSummaryExitPlan(rec *models.AiRecommendStocks) {
	if rec == nil {
		return
	}
	source := normalizeRecommendText(strings.Join([]string{
		rec.RecommendReason,
		rec.SellSignal,
		rec.InvalidSignal,
		rec.InvalidCondition,
		rec.Remarks,
	}, "\n"))
	if strings.TrimSpace(rec.RecommendStopProfitPrice) == "" {
		if text, minVal, maxVal := extractMarketSummaryNamedRange(source, []string{"止盈区间", "止盈", "卖出区间"}); text != "" {
			rec.RecommendStopProfitPrice = text
			rec.RecommendStopProfitPriceMin = minVal
			rec.RecommendStopProfitPriceMax = maxVal
		}
	}
	if strings.TrimSpace(rec.RecommendStopLossPrice) == "" {
		if text := extractMarketSummaryNamedSinglePrice(source, []string{"止损位", "止损"}); text != "" {
			rec.RecommendStopLossPrice = text
		}
	}
}

func extractMarketSummaryNamedRange(text string, labels []string) (string, float64, float64) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", 0, 0
	}
	for _, label := range labels {
		idx := strings.Index(text, label)
		if idx < 0 {
			continue
		}
		fragment := text[idx:]
		if len(fragment) > 120 {
			fragment = fragment[:120]
		}
		if matched := strings.TrimSpace(marketSummaryRangePattern.FindString(fragment)); matched != "" {
			parsed, minVal, maxVal := parseMarketSummaryNumericRange(matched)
			return parsed, minVal, maxVal
		}
	}
	return "", 0, 0
}

func extractMarketSummaryNamedSinglePrice(text string, labels []string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, label := range labels {
		idx := strings.Index(text, label)
		if idx < 0 {
			continue
		}
		fragment := text[idx:]
		if len(fragment) > 80 {
			fragment = fragment[:80]
		}
		if matched := firstNumericValue(fragment); matched != "" {
			return matched
		}
	}
	return ""
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
	recommend.InvalidCondition = reason
	recommend.ActivationInvalidReason = reason
	if recommend.Remarks == "" {
		recommend.Remarks = reason
	} else if !strings.Contains(recommend.Remarks, reason) {
		recommend.Remarks = normalizeRecommendText(recommend.Remarks + "\n" + reason)
	}
}

func markMarketSummaryRecommendPendingMarketData(recommend *models.AiRecommendStocks, reason string) {
	if recommend == nil {
		return
	}
	reason = normalizeRecommendText(reason)
	if reason == "" {
		reason = marketSummaryPendingMarketDataReason
	}
	recommend.RecommendStatus = recommendStatusPendingMarketData
	recommend.ExecutionState = recommendExecutionConditional
	recommend.ActivationStatus = recommendActivationPendingData
	recommend.ActivationInvalidReason = reason
	recommend.InvalidCondition = reason
	if strings.TrimSpace(recommend.BuySignal) == "" || isAnalysisOnlyPlaceholderSignal(recommend.BuySignal) {
		if signal := extractRecoverableMarketSummarySignal(*recommend); signal != "" {
			recommend.BuySignal = signal
		}
	}
	if recommend.Remarks == "" {
		recommend.Remarks = reason
	} else if !strings.Contains(recommend.Remarks, reason) {
		recommend.Remarks = normalizeRecommendText(recommend.Remarks + "\n" + reason)
	}
}

func recoverPendingMarketSummaryRecommendationsForScope(scope map[string]struct{}) ([]string, error) {
	if len(scope) == 0 {
		return nil, nil
	}
	codes := keysFromScopeMap(scope)
	if len(codes) == 0 {
		return nil, nil
	}
	rows := make([]models.AiRecommendStocks, 0, len(codes))
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("stock_code IN ?", codes).
		Where("activation_rule_source IN ?", []string{"market_summary", "market_summary_embedded"}).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	changed := make([]string, 0, len(rows))
	for _, row := range rows {
		if !isRecoverableMarketSummaryMarketDataGap(row) && !marketSummaryRecommendMissingExitPlan(row) && !marketSummaryRecommendHasStaleMissingMarketDataReason(row) {
			continue
		}
		rec := row
		recovered := tryRecoverHistoricalMarketSummaryRule(&rec)
		if err := normalizeMarketSummaryExecutionDataForSaveWithFetch(&rec, false); err != nil {
			return nil, err
		}
		if isPendingMarketDataRecommend(&rec) || isAnalysisOnlyRecommend(&rec) {
			continue
		}
		rec.ActivationRuleJSON = ""
		rec.ActivationRuleVersion = ""
		rec.ActivationInvalidReason = ""
		if err := normalizeActivationRuleForSave(&rec); err != nil {
			continue
		}
		updateMap := map[string]any{
			"stock_price":                     rec.StockPrice,
			"stock_current_price":             rec.StockCurrentPrice,
			"stock_current_price_time":        rec.StockCurrentPriceTime,
			"stock_close_price":               rec.StockClosePrice,
			"stock_pre_price":                 rec.StockPrePrice,
			"observe_price":                   rec.ObservePrice,
			"recommend_status":                rec.RecommendStatus,
			"execution_state":                 rec.ExecutionState,
			"recommend_buy_price":             rec.RecommendBuyPrice,
			"recommend_buy_price_min":         rec.RecommendBuyPriceMin,
			"recommend_buy_price_max":         rec.RecommendBuyPriceMax,
			"recommend_stop_profit_price":     rec.RecommendStopProfitPrice,
			"recommend_stop_profit_price_min": rec.RecommendStopProfitPriceMin,
			"recommend_stop_profit_price_max": rec.RecommendStopProfitPriceMax,
			"recommend_stop_loss_price":       rec.RecommendStopLossPrice,
			"focus_price":                     rec.FocusPrice,
			"buy_signal":                      rec.BuySignal,
			"buy_signal_detail":               rec.BuySignalDetail,
			"sell_signal":                     rec.SellSignal,
			"sell_signal_detail":              rec.SellSignalDetail,
			"invalid_signal":                  rec.InvalidSignal,
			"invalid_condition":               rec.InvalidCondition,
			"activation_status":               rec.ActivationStatus,
			"activation_rule_json":            rec.ActivationRuleJSON,
			"activation_rule_version":         rec.ActivationRuleVersion,
			"activation_invalid_reason":       "",
			"remarks":                         rec.Remarks,
		}
		if !recovered &&
			row.RecommendStatus == rec.RecommendStatus &&
			row.ExecutionState == rec.ExecutionState &&
			row.ActivationStatus == rec.ActivationStatus &&
			row.InvalidCondition == rec.InvalidCondition &&
			row.ActivationInvalidReason == rec.ActivationInvalidReason {
			continue
		}
		if err := db.Dao.Model(&models.AiRecommendStocks{}).Where("id = ?", row.ID).Updates(updateMap).Error; err != nil {
			return nil, err
		}
		changed = append(changed, normalizeRecommendStockCode(row.StockCode))
	}
	if len(changed) > 0 {
		if err := markAiRecommendYieldDirtyCodes(changed, "分钟线补齐后恢复市场总结推荐，等待严格模式回算", aiRecommendYieldModeStrict); err != nil {
			return nil, err
		}
	}
	return changed, nil
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
		Where("summary_version IN ?", []string{marketSummaryPhase3Version, marketSummaryPhase4Version}).
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
		if isPendingMarketDataRecommend(&rec) {
			updateMap["recommend_status"] = rec.RecommendStatus
			updateMap["execution_state"] = rec.ExecutionState
			updateMap["activation_status"] = rec.ActivationStatus
			updateMap["activation_invalid_reason"] = rec.ActivationInvalidReason
			updateMap["invalid_condition"] = rec.InvalidCondition
			updateMap["remarks"] = rec.Remarks
			updateMap["buy_signal"] = rec.BuySignal
			updateMap["buy_signal_detail"] = rec.BuySignalDetail
		} else if isAnalysisOnlyRecommend(&rec) {
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
				rec.RecommendStopProfitPrice != row.RecommendStopProfitPrice ||
				rec.RecommendStopProfitPriceMin != row.RecommendStopProfitPriceMin ||
				rec.RecommendStopProfitPriceMax != row.RecommendStopProfitPriceMax ||
				rec.RecommendStopLossPrice != row.RecommendStopLossPrice ||
				rec.FocusPrice != row.FocusPrice ||
				rec.BuySignal != row.BuySignal ||
				rec.BuySignalDetail != row.BuySignalDetail ||
				rec.InvalidCondition != row.InvalidCondition ||
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
				updateMap["recommend_stop_profit_price"] = rec.RecommendStopProfitPrice
				updateMap["recommend_stop_profit_price_min"] = rec.RecommendStopProfitPriceMin
				updateMap["recommend_stop_profit_price_max"] = rec.RecommendStopProfitPriceMax
				updateMap["recommend_stop_loss_price"] = rec.RecommendStopLossPrice
				updateMap["focus_price"] = rec.FocusPrice
				updateMap["buy_signal"] = rec.BuySignal
				updateMap["buy_signal_detail"] = rec.BuySignalDetail
				updateMap["invalid_condition"] = rec.InvalidCondition
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
