package main

import (
	"go-stock/backend/logger"
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
}

func (a *App) SummaryStockNews(question string, aiConfigId int, sysPromptId *int, enableTools bool, think bool) {
	if !a.tryAcquireSummaryTask() {
		emitEvent(a.ctx, "summaryStockNewsToolStatus", map[string]any{
			"event":  "summaryStockNewsToolStatus",
			"tool":   "market_summary",
			"status": "busy",
			"time":   time.Now().Format(time.DateTime),
		})
		return
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
		return
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

	for msg := range msgs {
		eventName := "summaryStockNews"
		if evt, ok := msg["event"].(string); ok && strings.TrimSpace(evt) != "" {
			eventName = evt
		}
		emitEvent(a.ctx, eventName, msg)

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
	a.services.AI.SaveAIResponseResult(a.ctx, "市场资讯", "市场资讯", res.text, res.chatID, res.finalQuestion, res.aiConfigId)
	if saved, err := a.services.AI.EnsureMarketSummaryRecommendStocksSaved(res.text, res.modelName, startedAt); err != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结补写推荐记录失败: %v", err)
	} else if saved > 0 {
		logger.SugaredLogger.Infof("市场资讯AI总结自动补写推荐记录成功: +%d", saved)
	}
	if saved, err := a.services.AI.EnsureMarketSummaryYieldOverridesSaved(res.text, startedAt); err != nil {
		logger.SugaredLogger.Warnf("市场资讯AI总结补写收益率复审覆盖失败: %v", err)
	} else if saved > 0 {
		logger.SugaredLogger.Infof("市场资讯AI总结自动补写收益率复审覆盖成功: +%d", saved)
	}
}
