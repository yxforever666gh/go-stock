package main

import (
	"encoding/json"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"strconv"
	"strings"
	"time"
)

type summaryRunResult struct {
	aiConfigId    int
	text          string
	chatID        string
	modelName     string
	finalQuestion string
	errs          []string
	routeLog      *data.MarketSummaryRouteLogSnapshot
	verified      []data.MarketSummaryVerifiedCandidateSnapshot
}

type marketSummaryRuntimeMeta struct {
	routeLog *data.MarketSummaryRouteLogSnapshot
	verified []data.MarketSummaryVerifiedCandidateSnapshot
}

func (a *App) SummaryStockNews(question string, aiConfigId int, sysPromptId *int, enableTools bool, think bool) {
	_ = a.runSummaryStockNewsTask(question, aiConfigId, sysPromptId, enableTools, think)
}

func (a *App) runSummaryStockNewsTask(question string, aiConfigId int, sysPromptId *int, enableTools bool, think bool) summaryRunResult {
	if !a.tryAcquireSummaryTask() {
		emitEvent(a.ctx, "summaryStockNewsToolStatus", map[string]any{
			"event":  "summaryStockNewsToolStatus",
			"tool":   "market_summary",
			"status": "busy",
			"time":   time.Now().Format(time.DateTime),
		})
		return summaryRunResult{errs: []string{"AI总结正在执行中"}}
	}
	defer a.releaseSummaryTask()
	emitEvent(a.ctx, "summaryStockNewsToolStatus", map[string]any{
		"event":  "summaryStockNewsToolStatus",
		"tool":   "market_summary",
		"status": "running",
		"time":   time.Now().Format(time.DateTime),
	})

	startedAt := time.Now()
	order := a.resolveSummaryFailoverOrder(aiConfigId)
	if len(order) == 0 {
		emitEvent(a.ctx, "summaryStockNews", "DONE")
		return summaryRunResult{errs: []string{"未找到可用的 AI 配置"}}
	}

	res := summaryRunResult{}
	for idx, targetAiConfigId := range order {
		if idx > 0 {
			logger.SugaredLogger.Warnf("市场资讯AI总结切换备用模型重试。from=%d to=%d attempt=%d errs=%v", order[idx-1], targetAiConfigId, idx+1, res.errs)
			go emitEvent(a.ctx, "warnMsg", "市场资讯AI总结已自动切换到备用模型继续重试")
		}
		current := a.runSummaryWithFallback(targetAiConfigId, question, sysPromptId, enableTools, think)
		if current.text != "" {
			res = current
			break
		}
		if idx == 0 || (res.text == "" && len(current.errs) >= len(res.errs)) {
			res = current
		}
		if !shouldSummaryFailover(current) {
			res = current
			break
		}
	}

	a.persistSummaryRunResult(res, startedAt)
	emitEvent(a.ctx, "summaryStockNews", "DONE")
	return res
}

func (a *App) runSummaryWithFallback(targetAiConfigId int, question string, sysPromptId *int, withTools bool, thinking bool) summaryRunResult {
	res := a.runSummaryOnce(targetAiConfigId, question, sysPromptId, withTools, thinking)
	if withTools && (res.text == "" || len(res.errs) > 0) {
		if isLikelyRequestLevelFailure(res.errs) {
			return res
		}
		logger.SugaredLogger.Warnf("市场资讯AI总结(工具模式)失败或不完整，开始回退到无工具模式。aiConfigId=%d errs=%v", targetAiConfigId, res.errs)
		res2 := a.runSummaryOnce(targetAiConfigId, question, sysPromptId, false, false)
		if res2.text != "" {
			res = res2
		}
	}
	return res
}

func (a *App) runSummaryOnce(targetAiConfigId int, question string, sysPromptId *int, withTools bool, thinking bool) summaryRunResult {
	var msgs <-chan map[string]any
	if withTools {
		msgs = a.services.AI.NewSummaryStockNewsStreamPhased(a.ctx, targetAiConfigId, question, sysPromptId, thinking)
	} else {
		msgs = a.services.AI.NewSummaryStockNewsStream(a.ctx, targetAiConfigId, question, sysPromptId, thinking)
	}

	var summaryText strings.Builder
	chatID := ""
	modelName := ""
	finalQuestion := question
	errs := make([]string, 0)
	resMeta := marketSummaryRuntimeMeta{}

	for msg := range msgs {
		eventName := "summaryStockNews"
		if evt, ok := msg["event"].(string); ok && strings.TrimSpace(evt) != "" {
			eventName = evt
		}
		emitEvent(a.ctx, eventName, msg)
		if eventName == "summaryStockNewsMeta" {
			applyMarketSummaryMetaMessage(&resMeta, msg)
			continue
		}

		if codeAny, ok := msg["code"]; ok {
			code := 1
			switch v := codeAny.(type) {
			case int:
				code = v
			case int64:
				code = int(v)
			case float64:
				code = int(v)
			case string:
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					code = n
				}
			}
			if code == 0 {
				if c, ok := msg["content"].(string); ok && strings.TrimSpace(c) != "" {
					errs = append(errs, strings.TrimSpace(c))
				}
				continue
			}
		}

		if chat, ok := msg["chatId"].(string); ok && chat != "" {
			chatID = chat
		}
		if model, ok := msg["model"].(string); ok && model != "" {
			modelName = model
		}
		if q, ok := msg["question"].(string); ok && q != "" {
			finalQuestion = q
		}
		if content, ok := msg["content"].(string); ok {
			if strings.Contains(content, "开始调用工具：") {
				continue
			}
			summaryText.WriteString(content)
		}
	}

	return summaryRunResult{
		aiConfigId:    targetAiConfigId,
		text:          strings.TrimSpace(summaryText.String()),
		chatID:        chatID,
		modelName:     modelName,
		finalQuestion: finalQuestion,
		errs:          errs,
		routeLog:      resMeta.routeLog,
		verified:      resMeta.verified,
	}
}

func applyMarketSummaryMetaMessage(target *marketSummaryRuntimeMeta, msg map[string]any) {
	if target == nil {
		return
	}
	if raw, ok := msg["routeLog"]; ok {
		b, err := json.Marshal(raw)
		if err == nil {
			var route data.MarketSummaryRouteLogSnapshot
			if json.Unmarshal(b, &route) == nil {
				target.routeLog = &route
			}
		}
	}
	if raw, ok := msg["verifiedCandidates"]; ok {
		b, err := json.Marshal(raw)
		if err == nil {
			var verified []data.MarketSummaryVerifiedCandidateSnapshot
			if json.Unmarshal(b, &verified) == nil {
				target.verified = verified
			}
		}
	}
}

func (a *App) resolveSummaryFailoverOrder(requestedAiConfigId int) []int {
	return a.services.AI.ResolveAIFallbackOrder(requestedAiConfigId)
}

func isLikelyRequestLevelFailure(errs []string) bool {
	for _, raw := range errs {
		msg := strings.ToLower(strings.TrimSpace(raw))
		if msg == "" {
			continue
		}
		if strings.Contains(msg, "client.timeout exceeded while awaiting headers") ||
			strings.Contains(msg, "context deadline exceeded") ||
			strings.Contains(msg, "tls handshake timeout") ||
			strings.Contains(msg, "i/o timeout") ||
			strings.Contains(msg, "connection refused") ||
			strings.Contains(msg, "connection reset by peer") ||
			strings.Contains(msg, "no such host") ||
			strings.Contains(msg, "temporary failure in name resolution") ||
			strings.Contains(msg, "proxyconnect tcp") ||
			strings.Contains(msg, "unexpected eof") ||
			strings.Contains(msg, "dial tcp") {
			return true
		}
		if strings.Contains(msg, "unauthorized") ||
			strings.Contains(msg, "invalid api key") ||
			strings.Contains(msg, "api key") ||
			strings.Contains(msg, "invalid key") ||
			strings.Contains(msg, "forbidden") ||
			strings.Contains(msg, "permission") ||
			strings.Contains(msg, "authentication") ||
			strings.Contains(msg, "model not found") ||
			strings.Contains(msg, "invalid model") ||
			strings.Contains(msg, "insufficient_quota") ||
			strings.Contains(msg, "quota") ||
			strings.Contains(msg, "用量耗尽") ||
			strings.Contains(msg, "额度耗尽") ||
			strings.Contains(msg, "额度不足") ||
			strings.Contains(msg, "余额不足") ||
			strings.Contains(msg, "限流") ||
			strings.Contains(msg, "rate limit") ||
			strings.Contains(msg, "too many requests") {
			return true
		}
	}
	return false
}

func isLikelyNetworkOrTimeoutErr(raw string) bool {
	msg := strings.ToLower(strings.TrimSpace(raw))
	if msg == "" {
		return false
	}
	hints := []string{
		"client.timeout exceeded while awaiting headers",
		"context deadline exceeded",
		"tls handshake timeout",
		"i/o timeout",
		"connection refused",
		"connection reset by peer",
		"no such host",
		"temporary failure in name resolution",
		"proxyconnect tcp",
		"unexpected eof",
		"eof",
		"dial tcp",
	}
	for _, hint := range hints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func shouldSummaryFailover(res summaryRunResult) bool {
	if strings.TrimSpace(res.text) != "" {
		return false
	}
	if len(res.errs) == 0 {
		return true
	}
	for _, e := range res.errs {
		low := strings.ToLower(strings.TrimSpace(e))
		if low == "" {
			continue
		}
		if strings.Contains(low, "unauthorized") ||
			strings.Contains(low, "invalid api key") ||
			strings.Contains(low, "api key") ||
			strings.Contains(low, "invalid key") ||
			strings.Contains(low, "forbidden") ||
			strings.Contains(low, "permission") ||
			strings.Contains(low, "model not found") ||
			strings.Contains(low, "not found") ||
			strings.Contains(low, "invalid model") ||
			strings.Contains(low, "incorrect api key") ||
			strings.Contains(low, "authentication") ||
			strings.Contains(low, "insufficient_quota") ||
			strings.Contains(low, "quota") ||
			strings.Contains(low, "用量耗尽") ||
			strings.Contains(low, "额度耗尽") ||
			strings.Contains(low, "额度不足") ||
			strings.Contains(low, "余额不足") ||
			strings.Contains(low, "限流") ||
			strings.Contains(low, "rate limit") ||
			strings.Contains(low, "too many requests") {
			return true
		}
		if isLikelyNetworkOrTimeoutErr(low) {
			return true
		}
	}
	return true
}

func (a *App) persistSummaryRunResult(res summaryRunResult, startedAt time.Time) {
	if res.text == "" {
		return
	}

	preparedText, prepStats, err := data.PrepareMarketSummaryReportForPersistence(res.text, startedAt)
	if err != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结净化失败，回退原始文本保存: %v", err)
		preparedText = res.text
	} else if prepStats.DuplicateRowsOmit > 0 || prepStats.AnalysisOnlyRows > 0 {
		logger.SugaredLogger.Infof(
			"市场资讯AI总结净化完成: rows=%d duplicateOmit=%d analysisOnly=%d kept=%d",
			prepStats.RowsSeen,
			prepStats.DuplicateRowsOmit,
			prepStats.AnalysisOnlyRows,
			prepStats.RecommendationRows,
		)
	}

	reportText := preparedText
	if startedAt.In(time.FixedZone("CST", 8*3600)).Format("15:04") == "09:40" {
		if reviewMarkdown, reviewErr := data.RunMorningOpeningReview(startedAt); reviewErr != nil {
			logger.SugaredLogger.Warnf("09:40 开盘复核生成失败: %v", reviewErr)
		} else if strings.TrimSpace(reviewMarkdown) != "" {
			reportText = strings.TrimSpace(preparedText) + "\n\n" + strings.TrimSpace(reviewMarkdown)
		}
	}

	providerName := resolveAIProviderName(res.aiConfigId, res.modelName)
	report := buildMarketSummaryEmailReport(reportText, res.finalQuestion, providerName, res.modelName, startedAt.Format(time.DateTime))
	if report == nil {
		report = &models.AIResponseResult{
			ProviderName: strings.TrimSpace(providerName),
			StockCode:    "市场资讯",
			StockName:    "市场资讯",
			ModelName:    strings.TrimSpace(res.modelName),
			ChatId:       res.chatID,
			Question:     strings.TrimSpace(res.finalQuestion),
			Content:      data.HumanizeMarketSummaryReport(reportText),
		}
		report.CreatedAt = startedAt
	}
	if report == nil {
		return
	}
	report.ChatId = res.chatID
	if err := db.Dao.Create(report).Error; err != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结保存失败: %v", err)
	}

	saveResult, saveErr := a.services.AI.EnsureMarketSummaryRecommendStocksSavedWithResult(preparedText, providerName, res.modelName, startedAt, res.verified)
	saved := 0
	if saveResult != nil {
		saved = saveResult.SavedCount
	}
	if saveErr != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结补写推荐记录失败: %v", saveErr)
	} else if saveResult != nil && saveResult.SavedCount > 0 {
		logger.SugaredLogger.Infof("市场资讯AI总结自动补写推荐记录成功: +%d", saved)
	}
	saveResult = a.tryRunMarketSummarySupplement(report, reportText, providerName, res, startedAt, saveResult)
	a.persistMarketSummaryDiagnostic(res, startedAt, time.Now(), saveResult)
	if saved, err := a.services.AI.EnsureMarketSummaryYieldOverridesSaved(preparedText, startedAt); err != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结补写收益率复审覆盖失败: %v", err)
	} else if saved > 0 {
		logger.SugaredLogger.Infof("市场资讯AI总结自动补写收益率复审覆盖成功: +%d", saved)
	}

	setting := a.services.Config.GetConfig()
	if setting != nil && setting.Settings != nil && setting.YieldEmailEnable && setting.MarketSummaryEmailEnable {
		if !a.tryAcquireYieldEmailTask() {
			logger.SugaredLogger.Warn("市场资讯AI总结自动发送邮件已跳过: 上一次邮件发送任务仍在执行")
			return
		}
		defer a.releaseYieldEmailTask()
		if err := a.services.AI.SendMarketSummaryEmail("summary_auto", report, summarizeSummaryRunError(res)); err != nil {
			logger.SugaredLogger.Warnf("市场资讯AI总结生成后自动发送邮件失败: %v", err)
		}
	}
}

const marketSummarySupplementProductionTarget = 2

func (a *App) tryRunMarketSummarySupplement(report *models.AIResponseResult, reportText, providerName string, res summaryRunResult, startedAt time.Time, firstResult *models.MarketSummaryRecommendSaveResult) *models.MarketSummaryRecommendSaveResult {
	if firstResult == nil {
		return firstResult
	}
	if firstResult.ProductionCount >= marketSummarySupplementProductionTarget {
		firstResult.SupplementText = buildMarketSummarySupplementNote(false, nil, firstResult.BlockedReasons)
		updateMarketSummaryReportSupplementNote(report, reportText, firstResult.SupplementText)
		return firstResult
	}
	remaining := filterRuntimeVerifiedCandidates(res.verified, firstResult.UsedStockCodes)
	if len(remaining) == 0 {
		firstResult.SupplementText = buildMarketSummarySupplementNote(false, nil, firstResult.BlockedReasons)
		updateMarketSummaryReportSupplementNote(report, reportText, firstResult.SupplementText)
		return firstResult
	}
	req := data.MarketSummarySupplementRequest{
		FailureSummary:    firstResult.BlockedReasons,
		RemainingVerified: remaining,
		ExcludedToday:     firstResult.UsedStockCodes,
		TargetProduction:  marketSummarySupplementProductionTarget,
		CurrentProduction: firstResult.ProductionCount,
	}
	supplementText, _, modelName, err := a.services.AI.GenerateMarketSummarySupplementTable(a.ctx, res.aiConfigId, req)
	if err != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结二轮补位生成失败: %v", err)
		firstResult.SupplementText = buildMarketSummarySupplementNote(true, remaining, firstResult.BlockedReasons)
		updateMarketSummaryReportSupplementNote(report, reportText, firstResult.SupplementText)
		return firstResult
	}
	supplementText = strings.TrimSpace(supplementText)
	if supplementText == "" {
		firstResult.SupplementText = buildMarketSummarySupplementNote(true, remaining, firstResult.BlockedReasons)
		updateMarketSummaryReportSupplementNote(report, reportText, firstResult.SupplementText)
		return firstResult
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = res.modelName
	}
	secondResult, saveErr := a.services.AI.EnsureMarketSummaryRecommendStocksSavedWithResult(supplementText, providerName, modelName, startedAt, remaining)
	if saveErr != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结二轮补位保存失败: %v", saveErr)
	}
	mergeMarketSummarySaveResult(firstResult, secondResult)
	firstResult.SupplementTriggered = true
	firstResult.SupplementText = buildMarketSummarySupplementNote(true, remaining, firstResult.BlockedReasons)
	firstResult.SupplementCandidates = collectRuntimeVerifiedCandidateCodes(remaining)
	updateMarketSummaryReportSupplementNote(report, reportText, firstResult.SupplementText)
	if secondResult != nil && secondResult.SavedCount > 0 {
		logger.SugaredLogger.Infof("市场资讯AI总结二轮补位保存成功: +%d production=%d", secondResult.SavedCount, secondResult.ProductionCount)
	}
	return firstResult
}

func updateMarketSummaryReportSupplementNote(report *models.AIResponseResult, reportText, note string) {
	if report != nil {
		report.Content = data.HumanizeMarketSummaryReport(strings.TrimSpace(reportText) + "\n\n" + strings.TrimSpace(note))
		if err := db.Dao.Save(report).Error; err != nil {
			logger.SugaredLogger.Warnf("市场资讯AI总结二轮补位说明更新失败: %v", err)
		}
	}
}

func filterRuntimeVerifiedCandidates(candidates []data.MarketSummaryVerifiedCandidateSnapshot, usedCodes []string) []data.MarketSummaryVerifiedCandidateSnapshot {
	used := make(map[string]struct{}, len(usedCodes))
	for _, raw := range usedCodes {
		code := normalizeRuntimeStockCode(raw)
		if code != "" {
			used[code] = struct{}{}
		}
	}
	result := make([]data.MarketSummaryVerifiedCandidateSnapshot, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, item := range candidates {
		code := normalizeRuntimeStockCode(item.StockCode)
		if code == "" {
			continue
		}
		if _, ok := used[code]; ok {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		item.StockCode = code
		result = append(result, item)
		seen[code] = struct{}{}
	}
	return result
}

func collectRuntimeVerifiedCandidateCodes(candidates []data.MarketSummaryVerifiedCandidateSnapshot) []string {
	codes := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, item := range candidates {
		code := normalizeRuntimeStockCode(item.StockCode)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		codes = append(codes, code)
		seen[code] = struct{}{}
	}
	return codes
}

func normalizeRuntimeStockCode(raw string) string {
	text := strings.ToUpper(strings.TrimSpace(raw))
	text = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(text, ".SH"), ".SZ"), ".BJ")
	return text
}

func mergeMarketSummarySaveResult(target, extra *models.MarketSummaryRecommendSaveResult) {
	if target == nil || extra == nil {
		return
	}
	target.SavedCount += extra.SavedCount
	target.ProductionCount += extra.ProductionCount
	target.AnalysisOnlyCount += extra.AnalysisOnlyCount
	target.BlockedCount += extra.BlockedCount
	target.AIOutputCountSecond += extra.AIOutputCount
	target.UsedStockCodes = mergeRuntimeStringSet(target.UsedStockCodes, extra.UsedStockCodes)
	target.RemainingCandidateStocks = extra.RemainingCandidateStocks
	target.BlockedReasons = mergeMarketSummaryBlockedReasons(target.BlockedReasons, extra.BlockedReasons)
}

func mergeRuntimeStringSet(left, right []string) []string {
	result := make([]string, 0, len(left)+len(right))
	seen := map[string]struct{}{}
	for _, raw := range append(left, right...) {
		code := normalizeRuntimeStockCode(raw)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		result = append(result, code)
		seen[code] = struct{}{}
	}
	return result
}

func mergeMarketSummaryBlockedReasons(left, right []models.MarketSummaryBlockedReasonItem) []models.MarketSummaryBlockedReasonItem {
	counts := map[string]int{}
	for _, item := range append(left, right...) {
		reason := strings.TrimSpace(item.Reason)
		if reason == "" || item.Count <= 0 {
			continue
		}
		counts[reason] += item.Count
	}
	result := make([]models.MarketSummaryBlockedReasonItem, 0, len(counts))
	for reason, count := range counts {
		result = append(result, models.MarketSummaryBlockedReasonItem{Reason: reason, Count: count})
	}
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].Count > result[i].Count || (result[j].Count == result[i].Count && result[j].Reason < result[i].Reason) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	if len(result) > 5 {
		result = result[:5]
	}
	return result
}

func buildMarketSummarySupplementNote(triggered bool, candidates []data.MarketSummaryVerifiedCandidateSnapshot, reasons []models.MarketSummaryBlockedReasonItem) string {
	candidateCodes := strings.Join(collectRuntimeVerifiedCandidateCodes(candidates), "、")
	if candidateCodes == "" {
		candidateCodes = "无剩余核验候选"
	}
	reasonText := marketSummaryBlockedReasonText(reasons)
	if !triggered {
		return strings.TrimSpace("# 补位说明\n\n本轮未触发二轮补位；原因：" + reasonText + "。")
	}
	return strings.TrimSpace("# 补位说明\n\n第一轮生产候选不足 2 只，已触发一次二轮补位。补位候选：" + candidateCodes + "。仍被拦截的主要原因：" + reasonText + "。")
}

func marketSummaryBlockedReasonText(items []models.MarketSummaryBlockedReasonItem) string {
	if len(items) == 0 {
		return "暂无拦截原因"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			continue
		}
		if item.Count > 0 {
			parts = append(parts, reason+" "+strconv.Itoa(item.Count))
		} else {
			parts = append(parts, reason)
		}
	}
	if len(parts) == 0 {
		return "暂无拦截原因"
	}
	return strings.Join(parts, "；")
}

func (a *App) persistMarketSummaryDiagnostic(res summaryRunResult, startedAt, finishedAt time.Time, saveResult *models.MarketSummaryRecommendSaveResult) {
	item := &models.MarketSummaryRunDiagnostic{
		RunID:          "market-summary-" + startedAt.Format("20060102150405.000000000"),
		SummaryVersion: data.MarketSummaryCurrentVersion(),
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
	}
	if res.routeLog != nil {
		item.RunSlot = res.routeLog.RunSlot
		item.IndicatorCandidateCount = res.routeLog.IndicatorCandidateCt
		item.IndicatorAIInputCount = res.routeLog.IndicatorAIInputCt
		item.DiscoveryCandidateCount = res.routeLog.DiscoveryCandidateCt
		item.VerifiedCandidateCount = res.routeLog.VerifiedCandidateCt
		item.NotesJSON = mustJSONForRuntime(res.routeLog.Notes)
	}
	if saveResult != nil {
		item.AIOutputCountFirst = saveResult.AIOutputCount
		item.AIOutputCountSecond = saveResult.AIOutputCountSecond
		item.SavedCount = saveResult.SavedCount
		item.ProductionCount = saveResult.ProductionCount
		item.AnalysisOnlyCount = saveResult.AnalysisOnlyCount
		item.BlockedCount = saveResult.BlockedCount
		item.BlockedReasonTop = data.EncodeMarketSummaryBlockedReasons(saveResult.BlockedReasons)
	} else {
		item.BlockedReasonTop = "[]"
	}
	if item.IndicatorCandidateCount == 0 && item.VerifiedCandidateCount == 0 && item.BlockedCount == 0 && strings.TrimSpace(res.text) == "" {
		item.BlockedCount = 1
		item.BlockedReasonTop = data.EncodeMarketSummaryBlockedReasons([]models.MarketSummaryBlockedReasonItem{{Reason: "候选池为空", Count: 1}})
	}
	if err := data.SaveMarketSummaryRunDiagnostic(item); err != nil {
		logger.SugaredLogger.Warnf("save market summary diagnostic failed: %v", err)
	}
}

func mustJSONForRuntime(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func summarizeSummaryRunError(res summaryRunResult) string {
	for _, item := range res.errs {
		text := strings.TrimSpace(item)
		if text != "" {
			return text
		}
	}
	if strings.TrimSpace(res.text) == "" {
		return "未生成可保存的总结内容"
	}
	return ""
}
