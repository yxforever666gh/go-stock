package data

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"

	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

const (
	yieldEmailXLSXContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	yieldEmailSheetName       = "收益率列表"
)

var yieldBuyRangeNumberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

type yieldDisplayStatus struct {
	Label string
	Type  string
}

type yieldEmailColumn struct {
	Title string
	Key   string
	Width float64
	Wrap  bool
}

var yieldEmailColumns = []yieldEmailColumn{
	{Title: "股票名称", Key: "stockName", Width: 18},
	{Title: "股票代码", Key: "stockCode", Width: 14},
	{Title: "信号时间", Key: "signalTime", Width: 22},
	{Title: "买入区间", Key: "recommendBuyPrice", Width: 14},
	{Title: "止盈区间", Key: "stopProfitAmount", Width: 12},
	{Title: "止损位", Key: "stopLossAmount", Width: 12},
	{Title: "买入依据", Key: "buySignal", Width: 44, Wrap: true},
	{Title: "失效条件", Key: "invalidSignal", Width: 34, Wrap: true},
	{Title: "晨间复核", Key: "latestOpeningReview", Width: 36, Wrap: true},
	{Title: "激活状态", Key: "activationStatus", Width: 12},
	{Title: "激活时间", Key: "activationTime", Width: 22},
	{Title: "买入价/区间", Key: "buyAmount", Width: 14},
	{Title: "卖出金额", Key: "sellAmount", Width: 16, Wrap: true},
	{Title: "卖出时间", Key: "sellTime", Width: 26, Wrap: true},
	{Title: "当前价格", Key: "currentPrice", Width: 12},
	{Title: "净收益率", Key: "yieldRate", Width: 12},
	{Title: "数据状态", Key: "dataStatus", Width: 34, Wrap: true},
	{Title: "板块概念", Key: "bkName", Width: 24, Wrap: true},
}

func loadYieldEmailItems() ([]models.AiRecommendStocksYieldItem, error) {
	loc := cnLocation()
	now := timeNow().In(loc)
	expectedTradeDate := resolveExpectedYieldTradeDate(now)
	latestTradeDate := expectedTradeDate

	var meta models.AiRecommendYieldMeta
	var metaPtr *models.AiRecommendYieldMeta
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err == nil {
		metaPtr = &meta
		if t, ok := parseYieldTradeDate(meta.CurrentTradeDate); ok {
			latestTradeDate = t
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.SugaredLogger.Warnf("load yield email meta failed: %v", err)
	}
	if expectedTradeDate.After(latestTradeDate) {
		latestTradeDate = expectedTradeDate
	}
	latestTradeDate = time.Date(latestTradeDate.Year(), latestTradeDate.Month(), latestTradeDate.Day(), 0, 0, 0, 0, loc)
	coverableStart := minuteCoverableStartMinute(latestTradeDate)

	query := &models.AiRecommendStocksQuery{YieldMode: aiRecommendYieldModeStrict}
	records, err := listAiRecommendStocksForYield(query, coverableStart)
	if err != nil {
		return nil, err
	}
	rawRepeatCountMap := countRecommendOccurrencesByCode(records)
	records = collapseRecommendRecordsSameDayByCode(records)

	dirtyScope, err := loadDirtyAiRecommendYieldScope(aiRecommendYieldModeStrict)
	if err != nil {
		return nil, err
	}
	_, coverageIssues := computeMinuteDownloadCoverageStatsWithIssues(metaPtr, -1)

	recordStateMap, err := loadYieldRecordStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	stateMap, err := loadYieldStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	overrideMap, err := loadYieldOverrideMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}

	items := buildStrictYieldRecordItems(records, recordStateMap, stateMap, overrideMap, dirtyScope, coverageIssues)
	applyRecommendRepeatCountByCodeMap(items, rawRepeatCountMap)

	if len(items) == 0 {
		return items, nil
	}

	ids := make([]uint, 0, len(items))
	for _, item := range items {
		if item.RecommendID != 0 {
			ids = append(ids, item.RecommendID)
		}
	}
	if len(ids) == 0 {
		return items, nil
	}
	if latestReviewMap, reviewErr := loadLatestOpeningReviewSummaryMap(ids); reviewErr == nil {
		for idx := range items {
			items[idx].LatestOpeningReview = latestReviewMap[items[idx].RecommendID]
		}
	}
	return items, nil
}

func buildYieldXLSXAttachment(items []models.AiRecommendStocksYieldItem) ([]byte, error) {
	file := excelize.NewFile()
	defaultSheet := file.GetSheetName(0)
	if err := file.SetSheetName(defaultSheet, yieldEmailSheetName); err != nil {
		return nil, err
	}

	headerStyle, err := file.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold:  true,
			Color: "#1F2937",
			Size:  11,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#E8F1FF"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
			Vertical:   "center",
		},
		Border: []excelize.Border{
			{Type: "left", Color: "#D9E2F2", Style: 1},
			{Type: "top", Color: "#D9E2F2", Style: 1},
			{Type: "right", Color: "#D9E2F2", Style: 1},
			{Type: "bottom", Color: "#D9E2F2", Style: 1},
		},
	})
	if err != nil {
		return nil, err
	}

	bodyStyleCache := map[string]int{}
	getBodyStyle := func(color string, wrap bool, bold bool) (int, error) {
		key := strings.Join([]string{color, strconv.FormatBool(wrap), strconv.FormatBool(bold)}, "|")
		if styleID, ok := bodyStyleCache[key]; ok {
			return styleID, nil
		}
		styleID, styleErr := file.NewStyle(&excelize.Style{
			Font: &excelize.Font{
				Color: color,
				Bold:  bold,
				Size:  10,
			},
			Alignment: &excelize.Alignment{
				Vertical:   "top",
				WrapText:   wrap,
				Horizontal: "left",
			},
			Border: []excelize.Border{
				{Type: "left", Color: "#E5E7EB", Style: 1},
				{Type: "top", Color: "#E5E7EB", Style: 1},
				{Type: "right", Color: "#E5E7EB", Style: 1},
				{Type: "bottom", Color: "#E5E7EB", Style: 1},
			},
		})
		if styleErr != nil {
			return 0, styleErr
		}
		bodyStyleCache[key] = styleID
		return styleID, nil
	}

	for idx, col := range yieldEmailColumns {
		cell, err := excelize.CoordinatesToCellName(idx+1, 1)
		if err != nil {
			return nil, err
		}
		if err := file.SetCellValue(yieldEmailSheetName, cell, col.Title); err != nil {
			return nil, err
		}
		if err := file.SetCellStyle(yieldEmailSheetName, cell, cell, headerStyle); err != nil {
			return nil, err
		}
		colName, err := excelize.ColumnNumberToName(idx + 1)
		if err != nil {
			return nil, err
		}
		if err := file.SetColWidth(yieldEmailSheetName, colName, colName, col.Width); err != nil {
			return nil, err
		}
	}

	if err := file.SetPanes(yieldEmailSheetName, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	}); err != nil {
		return nil, err
	}

	for rowIdx, item := range items {
		excelRow := rowIdx + 2
		rowValues := yieldEmailRowValues(item)
		rowStyles := yieldEmailRowStyles(item)
		for colIdx, col := range yieldEmailColumns {
			cell, err := excelize.CoordinatesToCellName(colIdx+1, excelRow)
			if err != nil {
				return nil, err
			}
			if err := file.SetCellValue(yieldEmailSheetName, cell, rowValues[col.Key]); err != nil {
				return nil, err
			}
			styleColor := rowStyles[col.Key]
			if styleColor == "" {
				styleColor = "#1F2937"
			}
			styleID, err := getBodyStyle(styleColor, col.Wrap, col.Key == "stockName")
			if err != nil {
				return nil, err
			}
			if err := file.SetCellStyle(yieldEmailSheetName, cell, cell, styleID); err != nil {
				return nil, err
			}
		}
	}

	if len(items) == 0 {
		for idx, col := range yieldEmailColumns {
			cell, err := excelize.CoordinatesToCellName(idx+1, 2)
			if err != nil {
				return nil, err
			}
			value := ""
			if idx == 0 {
				value = "当前暂无收益率记录"
			}
			if err := file.SetCellValue(yieldEmailSheetName, cell, value); err != nil {
				return nil, err
			}
			styleID, err := getBodyStyle("#6B7280", col.Wrap, false)
			if err != nil {
				return nil, err
			}
			if err := file.SetCellStyle(yieldEmailSheetName, cell, cell, styleID); err != nil {
				return nil, err
			}
		}
	}

	buf, err := file.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return bytes.Clone(buf.Bytes()), nil
}

func yieldEmailRowValues(item models.AiRecommendStocksYieldItem) map[string]string {
	dataStatus := yieldDataSyncStatus(item)
	return map[string]string{
		"stockName":           yieldDefaultText(item.StockName),
		"stockCode":           yieldDefaultText(item.StockCode),
		"signalTime":          yieldSignalTimeDisplay(item),
		"recommendBuyPrice":   yieldFormatRecommendBuyDisplay(item.RecommendBuyPrice),
		"stopProfitAmount":    yieldFormatMoneyPtr(item.StopProfitAmount),
		"stopLossAmount":      yieldFormatMoneyPtr(item.StopLossAmount),
		"buySignal":           yieldBuyBasisPreview(item),
		"invalidSignal":       yieldDefaultText(item.InvalidSignal),
		"latestOpeningReview": yieldOpeningReviewPreviewText(item.LatestOpeningReview),
		"activationStatus":    yieldActivationStatusLabel(item),
		"activationTime":      yieldActivationTimeDisplay(item),
		"buyAmount":           yieldBuyAmountDisplay(item),
		"sellAmount":          yieldSellAmountDisplay(item),
		"sellTime":            yieldSellTimeDisplay(item),
		"currentPrice":        yieldCurrentPriceDisplay(item),
		"yieldRate":           yieldYieldRateDisplay(item),
		"dataStatus":          dataStatus.Label + "\n" + yieldDataSyncReason(item),
		"bkName":              yieldDefaultText(item.BkName),
	}
}

func yieldEmailRowStyles(item models.AiRecommendStocksYieldItem) map[string]string {
	buySellType := yieldResolveBuySellVisualType(item)
	dataStatus := yieldDataSyncStatus(item)
	styles := map[string]string{
		"stockName":           "#2080F0",
		"stockCode":           "#1F2937",
		"signalTime":          "#1F2937",
		"recommendBuyPrice":   "#1F2937",
		"stopProfitAmount":    "#1F2937",
		"stopLossAmount":      "#1F2937",
		"buySignal":           "#1F2937",
		"invalidSignal":       "#1F2937",
		"latestOpeningReview": "#1F2937",
		"activationStatus":    yieldVisualColor(yieldActivationStatusType(item)),
		"activationTime":      "#1F2937",
		"buyAmount":           yieldVisualColor(buySellType),
		"sellAmount":          yieldVisualColor(buySellType),
		"sellTime":            yieldVisualColor(buySellType),
		"currentPrice":        "#1F2937",
		"yieldRate":           yieldVisualColor(yieldRateTextType(item)),
		"dataStatus":          yieldVisualColor(dataStatus.Type),
		"bkName":              "#1F2937",
	}
	if yieldIsStrictRowPending(item) && item.CurrentPrice <= 0 {
		styles["currentPrice"] = "#6B7280"
	}
	return styles
}

func yieldDefaultText(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return "--"
	}
	return text
}

func yieldFormatMoney(value float64) string {
	if math.IsNaN(value) || value <= 0 {
		return "--"
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func yieldFormatMoneyPtr(value *float64) string {
	if value == nil {
		return "--"
	}
	return yieldFormatMoney(*value)
}

func yieldFormatRecommendBuyDisplay(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return "--"
	}
	matches := yieldBuyRangeNumberPattern.FindAllString(text, -1)
	if len(matches) >= 2 {
		first, errFirst := strconv.ParseFloat(matches[0], 64)
		second, errSecond := strconv.ParseFloat(matches[1], 64)
		if errFirst == nil && errSecond == nil {
			values := []float64{first, second}
			sort.Float64s(values)
			minText := strconv.FormatFloat(values[0], 'f', 2, 64)
			maxText := strconv.FormatFloat(values[1], 'f', 2, 64)
			if minText == maxText {
				return minText
			}
			return minText + "-" + maxText
		}
	}
	if len(matches) == 1 {
		single, err := strconv.ParseFloat(matches[0], 64)
		if err == nil {
			return strconv.FormatFloat(single, 'f', 2, 64)
		}
	}
	return text
}

func yieldIsStrictRowPending(item models.AiRecommendStocksYieldItem) bool {
	return !item.StrictReady
}

func yieldStrictPendingReason(item models.AiRecommendStocksYieldItem) string {
	reason := strings.TrimSpace(item.StrictPendingReason)
	if reason != "" {
		return reason
	}
	return "该股票存在待下载或待回算的严格模式任务"
}

func yieldSignalPreview(main, detail string) string {
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(main); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(detail); text != "" {
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "--"
	}
	return strings.Join(parts, "；")
}

func yieldBuyBasisPreview(item models.AiRecommendStocksYieldItem) string {
	return yieldSignalPreview(item.BuySignal, item.BuySignalDetail)
}

func yieldNormalizeSellAmountPart(value string) string {
	text := strings.TrimSpace(value)
	if text == "" || text == "null" || text == "undefined" {
		return "--"
	}
	return text
}

func yieldSellAmountLines(item models.AiRecommendStocksYieldItem) (string, string) {
	if yieldNormalizeActivationStatus(item.ActivationStatus) != "activated" {
		return "--", "--"
	}
	sellAmountText := strings.TrimSpace(item.SellAmountText)
	if sellAmountText != "" {
		parts := strings.Split(sellAmountText, "/")
		if len(parts) >= 2 {
			return yieldNormalizeSellAmountPart(parts[0]), yieldNormalizeSellAmountPart(parts[1])
		}
		if len(parts) == 1 {
			return yieldNormalizeSellAmountPart(parts[0]), "--"
		}
	}
	return yieldFormatMoneyPtr(item.StopProfitAmount), yieldFormatMoneyPtr(item.StopLossAmount)
}

func yieldNormalizeActivationStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "activated":
		return "activated"
	case "skipped":
		return "skipped"
	case "expired":
		return "expired"
	case "ineligible":
		return "ineligible"
	case "invalid":
		return "invalid"
	default:
		return "pending"
	}
}

func yieldResolveBuySellVisualType(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "warning"
	}
	activationStatus := yieldNormalizeActivationStatus(item.ActivationStatus)
	if activationStatus == "skipped" || activationStatus == "expired" || activationStatus == "ineligible" {
		return "default"
	}
	if activationStatus == "invalid" {
		return "error"
	}
	if activationStatus != "activated" {
		return "warning"
	}
	sellTime := strings.TrimSpace(item.SellTime)
	if sellTime == "待激活" {
		return "warning"
	}
	if sellTime == "" || sellTime == "持有" {
		return "info"
	}
	if item.YieldRate > 0 {
		return "error"
	}
	if item.YieldRate < 0 {
		return "success"
	}
	return "default"
}

func yieldIsDataFullySynced(item models.AiRecommendStocksYieldItem) bool {
	if yieldIsStrictRowPending(item) {
		return false
	}
	status := strings.TrimSpace(item.DataStatus)
	return status == "" || status == "正常" || status == "已跳过" || status == "已过期" || status == "已失效" || status == "未结构化"
}

func yieldDataSyncStatus(item models.AiRecommendStocksYieldItem) yieldDisplayStatus {
	if yieldIsStrictRowPending(item) {
		return yieldDisplayStatus{Label: "待回算", Type: "warning"}
	}
	if yieldIsDataFullySynced(item) {
		return yieldDisplayStatus{Label: "已完成", Type: "success"}
	}
	return yieldDisplayStatus{Label: "未完成", Type: "warning"}
}

func yieldDataSyncReason(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return yieldStrictPendingReason(item)
	}
	status := strings.TrimSpace(item.DataStatus)
	reason := strings.TrimSpace(item.DataStatusReason)
	if yieldNormalizeActivationStatus(item.ActivationStatus) == "ineligible" {
		if reason != "" {
			return reason
		}
		if fallback := strings.TrimSpace(item.BacktestEligibilityReason); fallback != "" {
			return fallback
		}
		return "该推荐未形成可机械执行交易计划，未纳入回测统计"
	}
	if status == "已跳过" {
		return "未激活结果已同步"
	}
	if status == "已过期" {
		return "过期未触发结果已同步"
	}
	if status == "已失效" {
		return "失效结果已同步"
	}
	if yieldIsDataFullySynced(item) {
		return "分钟线覆盖完整，数据已更新"
	}
	if reason != "" {
		return reason
	}
	switch status {
	case "计算中":
		return "后台任务仍在计算中，请稍后刷新"
	case "待覆盖":
		return "分钟线目标时间段尚未覆盖"
	case "不可覆盖", "无法判定":
		return "当前分钟线无法覆盖目标时间段"
	default:
		return "数据尚未更新完成"
	}
}

func yieldSkippedDisplayReason(item models.AiRecommendStocksYieldItem) string {
	reason := strings.TrimSpace(item.DataStatusReason)
	if reason != "" {
		return reason
	}
	return "未激活，已按规则跳过"
}

func yieldOpeningReviewActionText(review *models.AiRecommendOpeningReviewSummary) string {
	if review == nil {
		return "--"
	}
	action := strings.TrimSpace(review.Action)
	switch action {
	case "continue_plan":
		return "继续按原计划执行"
	case "observe_only":
		return "继续观察，不提前激活"
	case "cancel_plan":
		return "取消隔夜计划"
	case "hold":
		return "继续持有"
	case "reduce_risk":
		return "优先风控/止盈"
	default:
		if action == "" {
			return "--"
		}
		return action
	}
}

func yieldOpeningReviewPreviewText(review *models.AiRecommendOpeningReviewSummary) string {
	if review == nil {
		return "--"
	}
	action := yieldOpeningReviewActionText(review)
	reason := strings.TrimSpace(review.Reason)
	if reason == "" {
		reason = strings.TrimSpace(review.RawSummary)
	}
	if reason == "" {
		return action
	}
	return action + "；" + reason
}

func yieldActivationStatusLabel(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "待回算"
	}
	switch yieldNormalizeActivationStatus(item.ActivationStatus) {
	case "activated":
		return "已激活"
	case "skipped":
		return "已跳过"
	case "expired":
		return "过期未触发"
	case "invalid":
		return "无法回算"
	case "ineligible":
		return "未纳入回测"
	default:
		return "待激活"
	}
}

func yieldActivationStatusType(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "warning"
	}
	switch yieldNormalizeActivationStatus(item.ActivationStatus) {
	case "activated":
		return "success"
	case "skipped", "expired", "ineligible":
		return "default"
	case "invalid":
		return "error"
	default:
		return "warning"
	}
}

func yieldSignalTimeDisplay(item models.AiRecommendStocksYieldItem) string {
	if text := strings.TrimSpace(item.SignalTime); text != "" {
		return text
	}
	if text := strings.TrimSpace(item.RecommendTime); text != "" {
		return text
	}
	return "--"
}

func yieldActivationTimeDisplay(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "--"
	}
	if text := strings.TrimSpace(item.ActivationTime); text != "" {
		return text
	}
	if text := strings.TrimSpace(item.BuyTime); text != "" {
		return text
	}
	return "--"
}

func yieldBuyAmountDisplay(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "--"
	}
	if yieldNormalizeActivationStatus(item.ActivationStatus) == "activated" {
		return yieldFormatMoney(item.BuyAmount)
	}
	return yieldFormatRecommendBuyDisplay(item.RecommendBuyPrice)
}

func yieldSellAmountDisplay(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "待回算"
	}
	profit, loss := yieldSellAmountLines(item)
	return fmt.Sprintf("止盈: %s\n止损: %s", profit, loss)
}

func yieldSellTimeDisplay(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "待回算"
	}
	switch yieldNormalizeActivationStatus(item.ActivationStatus) {
	case "skipped":
		return "已跳过\n" + yieldSkippedDisplayReason(item)
	case "expired":
		return "过期未触发\n" + yieldSkippedDisplayReason(item)
	case "ineligible":
		return "未纳入回测"
	case "invalid":
		return "无法回算"
	case "pending":
		return "待激活"
	}
	sellTime := strings.TrimSpace(item.SellTime)
	if sellTime == "" || sellTime == "持有" {
		return "持有"
	}
	if sellTime == "待激活" {
		return "待激活"
	}
	return sellTime
}

func yieldCurrentPriceDisplay(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) && item.CurrentPrice <= 0 {
		return "--"
	}
	return yieldFormatMoney(item.CurrentPrice)
}

func yieldRateTextType(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "warning"
	}
	text := strings.TrimSpace(item.YieldRateText)
	if text == "" || text == "--" {
		return "default"
	}
	if item.YieldRate > 0 {
		return "error"
	}
	if item.YieldRate < 0 {
		return "success"
	}
	return "default"
}

func yieldYieldRateDisplay(item models.AiRecommendStocksYieldItem) string {
	if yieldIsStrictRowPending(item) {
		return "待回算"
	}
	text := strings.TrimSpace(item.YieldRateText)
	if text == "" {
		return "--"
	}
	return text
}

func yieldVisualColor(visualType string) string {
	switch visualType {
	case "success":
		return "#18A058"
	case "warning":
		return "#F0A020"
	case "error":
		return "#D03050"
	case "info":
		return "#2080F0"
	default:
		return "#1F2937"
	}
}
