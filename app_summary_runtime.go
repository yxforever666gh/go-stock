package main

import (
	"encoding/json"
	"strings"
	"time"

	"go-stock/backend/governance"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
)

type summaryRunResult struct {
	aiConfigId     int
	text           string
	modelName      string
	finalQuestion  string
	errs           []string
	routeLog       *service.MarketSummaryRouteLog
	v150Production *service.MarketSummaryV150ProductionResult
}

const marketSummaryV150BackendMissingReason = "V1.5 后端冻结决策缺失；已禁止回退旧版 Markdown 解析"

func currentMarketSummaryStrategyVersion() string {
	return strings.TrimSpace(releaseinfo.Manifest().CurrentStrategyVersion)
}

func usableMarketSummaryRunResult(res summaryRunResult) bool {
	// The typed result exists only after Runner crossed the atomic publisher
	// boundary. Presentation text may be empty for a structured no_trade.
	return res.v150Production != nil &&
		strings.TrimSpace(res.v150Production.RunID) != "" &&
		strings.TrimSpace(res.v150Production.StrategyVersion) == currentMarketSummaryStrategyVersion()
}

func rejectMissingV150BackendResult(res summaryRunResult) summaryRunResult {
	if usableMarketSummaryRunResult(res) {
		return res
	}
	for _, item := range res.errs {
		if strings.TrimSpace(item) == marketSummaryV150BackendMissingReason {
			return res
		}
	}
	res.errs = append(res.errs, marketSummaryV150BackendMissingReason)
	return res
}

func (a *App) SummaryStockNews(question string, aiConfigId int, sysPromptId *int, enableTools bool, think bool) {
	_ = a.runSummaryStockNewsTask(question, aiConfigId, sysPromptId, enableTools, think)
}

func (a *App) runSummaryStockNewsTask(question string, aiConfigId int, sysPromptId *int, enableTools bool, think bool) summaryRunResult {
	if err := a.services.Recommend.RequireStrategyLive(a.ctx, currentMarketSummaryStrategyVersion()); err != nil {
		logger.SugaredLogger.Warnf("market summary production blocked: %v", err)
		emitEvent(a.ctx, "summaryStockNewsToolStatus", map[string]any{
			"event":  "summaryStockNewsToolStatus",
			"tool":   "market_summary",
			"status": governance.StrategyModePaused,
			"reason": err.Error(),
			"time":   time.Now().Format(time.DateTime),
		})
		return summaryRunResult{errs: []string{err.Error()}}
	}
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

	// Kept in the public compatibility signature until the Web contract is
	// generated. V1.5 always uses the typed, evidence-backed producer.
	_ = enableTools
	res := summaryRunResult{}
	for idx, targetAiConfigId := range order {
		if idx > 0 {
			logger.SugaredLogger.Warnf(
				"市场资讯 AI 总结切换备用模型重试。from=%d to=%d attempt=%d errs=%v",
				order[idx-1], targetAiConfigId, idx+1, res.errs,
			)
			go emitEvent(a.ctx, "warnMsg", "市场资讯 AI 总结已自动切换到备用模型继续重试")
		}
		current := a.runMarketSummaryV150Once(targetAiConfigId, question, sysPromptId, think, startedAt)
		if usableMarketSummaryRunResult(current) {
			res = current
			break
		}
		if idx == 0 || len(current.errs) >= len(res.errs) {
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

func (a *App) runMarketSummaryV150Once(targetAiConfigId int, question string, sysPromptId *int, thinking bool, startedAt time.Time) summaryRunResult {
	production, err := a.services.Recommend.RunMarketSummaryV150(a.ctx, service.MarketSummaryV150ProductionRequest{
		AIConfigID:  targetAiConfigId,
		Question:    question,
		SysPromptID: sysPromptId,
		Think:       thinking,
		StartedAt:   startedAt,
	})
	res := summaryRunResult{
		aiConfigId:     targetAiConfigId,
		finalQuestion:  question,
		v150Production: production,
	}
	if production != nil {
		res.text = strings.TrimSpace(production.ReportText)
		res.modelName = strings.TrimSpace(production.ModelName)
		res.routeLog = production.RouteLog
		if res.routeLog == nil {
			res.routeLog = &service.MarketSummaryRouteLog{
				DiscoveryCandidateCt: production.CandidateCount,
				VerifiedCandidateCt:  production.VerifiedCandidateCount,
			}
		}
	}
	if err != nil {
		res.errs = append(res.errs, err.Error())
	}
	return rejectMissingV150BackendResult(res)
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

func shouldSummaryFailover(res summaryRunResult) bool {
	return !usableMarketSummaryRunResult(res)
}

func (a *App) persistSummaryRunResult(res summaryRunResult, startedAt time.Time) {
	if usableMarketSummaryRunResult(res) {
		a.persistMarketSummaryV150RunResult(res, startedAt)
		return
	}

	res = rejectMissingV150BackendResult(res)
	reason := marketSummaryV150BackendMissingReason
	if strings.TrimSpace(res.text) == "" {
		reason = "V1.5 后端冻结决策与展示报告均缺失；本轮未生产"
	}
	logger.SugaredLogger.Warn(reason)
	a.persistMarketSummaryDiagnostic(res, startedAt, time.Now(), &models.MarketSummaryRecommendSaveResult{
		BlockedCount:   1,
		BlockedReasons: []models.MarketSummaryBlockedReasonItem{{Reason: reason, Count: 1}},
	})
	go emitEvent(a.ctx, "warnMsg", reason)
}

// persistMarketSummaryV150RunResult handles delivery-only side effects after
// the typed producer has already committed the frozen decision atomically.
func (a *App) persistMarketSummaryV150RunResult(res summaryRunResult, startedAt time.Time) {
	production := res.v150Production
	if production == nil {
		return
	}
	providerName := strings.TrimSpace(production.ProviderName)
	if providerName == "" {
		providerName = a.resolveAIProviderName(res.aiConfigId, res.modelName)
	}
	modelName := firstNonEmptyRuntimeText(production.ModelName, res.modelName)
	reportText := strings.TrimSpace(production.ReportText)
	report := &models.AIResponseResult{
		ProviderName: strings.TrimSpace(providerName),
		StockCode:    "市场资讯",
		StockName:    "市场资讯",
		ModelName:    strings.TrimSpace(modelName),
		ChatId:       strings.TrimSpace(production.RunID),
		Question:     strings.TrimSpace(res.finalQuestion),
		Content:      a.services.AI.HumanizeMarketSummaryReport(reportText),
	}
	report.CreatedAt = startedAt
	if err := a.services.Recommend.CreateAIResponseReport(a.ctx, report); err != nil {
		logger.SugaredLogger.Warnf("V1.5 市场报告保存失败: %v", err)
	}

	saveResult := production.SaveResult
	if saveResult != nil {
		logger.SugaredLogger.Infof(
			"V1.5 后端决策已原子发布 run=%s candidates=%d production=%d saved=%d noTrade=%s",
			production.RunID,
			production.CandidateCount,
			production.ProductionCount,
			saveResult.SavedCount,
			production.NoTradeReason,
		)
	}
	a.persistMarketSummaryDiagnostic(res, startedAt, time.Now(), saveResult)

	setting := a.services.Config.GetConfig()
	if setting != nil && setting.Settings != nil && setting.YieldEmailEnable && setting.MarketSummaryEmailEnable {
		if !a.tryAcquireYieldEmailTask() {
			logger.SugaredLogger.Warn("V1.5 市场报告邮件已跳过：上一封邮件仍在发送")
			return
		}
		defer a.releaseYieldEmailTask()
		if err := a.services.AI.SendMarketSummaryEmail("summary_auto", report, summarizeSummaryRunError(res)); err != nil {
			logger.SugaredLogger.Warnf("V1.5 市场报告邮件发送失败: %v", err)
		}
	}
}

func firstNonEmptyRuntimeText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (a *App) persistMarketSummaryDiagnostic(res summaryRunResult, startedAt, finishedAt time.Time, saveResult *models.MarketSummaryRecommendSaveResult) {
	item := &models.MarketSummaryRunDiagnostic{
		RunID:          "market-summary-" + startedAt.Format("20060102150405.000000000"),
		SummaryVersion: currentMarketSummaryStrategyVersion(),
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
		item.BlockedReasonTop = a.services.Recommend.EncodeMarketSummaryBlockedReasons(saveResult.BlockedReasons)
		item.ProductionDowngradeReasonTop = a.services.Recommend.EncodeMarketSummaryBlockedReasons(saveResult.ProductionDowngradeReasons)
	} else {
		item.BlockedReasonTop = "[]"
		item.ProductionDowngradeReasonTop = "[]"
	}
	if item.IndicatorCandidateCount == 0 && item.VerifiedCandidateCount == 0 && item.BlockedCount == 0 && strings.TrimSpace(res.text) == "" {
		item.BlockedCount = 1
		item.BlockedReasonTop = a.services.Recommend.EncodeMarketSummaryBlockedReasons([]models.MarketSummaryBlockedReasonItem{{Reason: "候选池为空", Count: 1}})
	}
	if err := a.services.Recommend.SaveMarketSummaryRunDiagnostic(item); err != nil {
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
	if !usableMarketSummaryRunResult(res) {
		return "未生成可持久化的 V1.5 决策"
	}
	return ""
}
