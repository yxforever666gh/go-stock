package main

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/agent"
	agenttools "go-stock/backend/agent/tools"
	"go-stock/backend/logger"
	"go-stock/backend/models"

	"github.com/cloudwego/eino/schema"
)

type AgentSession struct {
	Messages   []*schema.Message
	LastActive time.Time
}

func cloneAgentMessages(messages []*schema.Message) []*schema.Message {
	result := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		result = append(result, &schema.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return result
}

func (a *App) ResetAgentSession(sessionId string) {
	if strings.TrimSpace(sessionId) == "" {
		return
	}
	a.agentSessionsMu.Lock()
	delete(a.agentSessions, sessionId)
	a.agentSessionsMu.Unlock()
	_ = a.services.History.DeleteSession(sessionId)
}

func (a *App) resolveAiModelName(aiConfigId int) string {
	return a.services.AI.ResolveAIModelName(aiConfigId)
}

func (a *App) resolveDefaultAiConfigId() int {
	return a.services.AI.ResolveDefaultAIConfigID()
}

func trimTitle(title string, maxLen int) string {
	title = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(title, "\n", " "), "\r", " "))
	if maxLen <= 0 {
		maxLen = 18
	}
	runes := []rune(title)
	if len(runes) > maxLen {
		return strings.TrimSpace(string(runes[:maxLen]))
	}
	return title
}

func (a *App) summarizeAgentSessionTitleByAI(question string, aiConfigId int) string {
	base := trimTitle(question, 18)
	if base == "" {
		return "新对话"
	}
	prompt := "请将以下用户问题总结为一个不超过18字的中文会话标题，只返回标题本身，不要解释和标点：" + base
	for idx, targetAIConfigID := range a.services.AI.ResolveAIFallbackOrder(aiConfigId) {
		stream := a.services.AI.NewSummaryStockNewsStream(a.ctx, targetAIConfigID, prompt, nil, false)

		var titleBuilder strings.Builder
		errs := make([]string, 0)
		for chunk := range stream {
			if normalizeMsgCode(chunk["code"]) == 0 {
				if text, ok := chunk["content"].(string); ok && strings.TrimSpace(text) != "" {
					errs = append(errs, strings.TrimSpace(text))
				}
				continue
			}
			if text, ok := chunk["content"].(string); ok && text != "" {
				titleBuilder.WriteString(text)
			}
		}
		title := trimTitle(titleBuilder.String(), 18)
		if title != "" {
			return title
		}
		if idx < len(a.services.AI.ResolveAIFallbackOrder(aiConfigId))-1 && isLikelyRequestLevelFailure(errs) {
			logger.SugaredLogger.Warnf("会话标题总结失败，切换备用模型。from=%d to=%d errs=%v", targetAIConfigID, a.services.AI.ResolveAIFallbackOrder(aiConfigId)[idx+1], errs)
			continue
		}
		break
	}
	return base
}

func (a *App) CreateAgentSession(aiConfigId int) map[string]any {
	sessionId := fmt.Sprintf("agent-%d-%d", time.Now().UnixNano(), time.Now().Unix()%10000)
	if aiConfigId <= 0 {
		aiConfigId = a.resolveDefaultAiConfigId()
	}
	modelName := a.resolveAiModelName(aiConfigId)
	session, err := a.services.History.EnsureSession(sessionId, "新对话", aiConfigId, modelName)
	if err != nil || session == nil {
		return map[string]any{
			"sessionId":  sessionId,
			"title":      "新对话",
			"aiConfigId": aiConfigId,
			"modelName":  modelName,
		}
	}
	_ = a.services.History.TrimSessions(models.DefaultAgentSessionLimit)
	return map[string]any{
		"sessionId":     session.SessionId,
		"title":         session.Title,
		"aiConfigId":    session.AiConfigId,
		"modelName":     session.ModelName,
		"lastMessageAt": session.LastMessageAt,
		"messageCount":  session.MessageCount,
	}
}

func (a *App) GetAgentSessionList() []models.AgentChatSession {
	list, err := a.services.History.ListRecentSessions(models.DefaultAgentSessionLimit)
	if err != nil {
		return []models.AgentChatSession{}
	}
	return list
}

func (a *App) GetAgentSessionMessages(sessionId string) []models.AgentChatMessage {
	list, err := a.services.History.ListSessionMessages(sessionId, models.DefaultAgentMessageLimit)
	if err != nil {
		return []models.AgentChatMessage{}
	}
	return list
}

func (a *App) DeleteAgentSession(sessionId string) string {
	if err := a.services.History.DeleteSession(sessionId); err != nil {
		return "删除失败"
	}
	a.agentSessionsMu.Lock()
	delete(a.agentSessions, sessionId)
	a.agentSessionsMu.Unlock()
	return "删除成功"
}

func (a *App) SummarizeAgentSessionTitle(sessionId string) string {
	session, err := a.services.History.GetSession(sessionId)
	if err != nil || session == nil {
		return ""
	}
	if strings.TrimSpace(session.Title) != "" && strings.TrimSpace(session.Title) != "新对话" {
		return session.Title
	}
	firstQuestion, err := a.services.History.FirstUserQuestion(sessionId)
	if err != nil || strings.TrimSpace(firstQuestion) == "" {
		return ""
	}
	title := a.summarizeAgentSessionTitleByAI(firstQuestion, int(session.AiConfigId))
	if strings.TrimSpace(title) == "" {
		return ""
	}
	_ = a.services.History.UpdateSessionTitle(sessionId, title)
	return title
}

func (a *App) ChatWithAgent(question string, aiConfigId int, sysPromptId *int, sessionId string) {
	question = strings.TrimSpace(question)
	if question == "" {
		return
	}
	if aiConfigId <= 0 {
		aiConfigId = a.resolveDefaultAiConfigId()
	}
	if strings.TrimSpace(sessionId) == "" {
		sessionId = fmt.Sprintf("agent-%d", time.Now().UnixNano())
	}
	modelName := a.resolveAiModelName(aiConfigId)

	session, _ := a.services.History.EnsureSession(sessionId, "新对话", aiConfigId, modelName)
	_ = a.services.History.TrimSessions(models.DefaultAgentSessionLimit)

	history, err := a.services.History.ListSessionMessagesForAgent(sessionId, models.DefaultAgentMessageLimit)
	if err != nil {
		history = nil
	}
	requestMessages := append(history, &schema.Message{
		Role:    schema.User,
		Content: question,
	})

	runOrder := a.services.AI.ResolveAIFallbackOrder(aiConfigId)
	assistantMessages := make([]*schema.Message, 0, 128)
	var assistantContent strings.Builder
	var assistantReasoning strings.Builder
	for idx, targetAIConfigID := range runOrder {
		messages, answer, reasoning, shouldFailover := runAgentWithFallback(a.agentToolData, a.agentConfiguration, requestMessages, targetAIConfigID, sysPromptId)
		assistantMessages = messages
		assistantContent.Reset()
		assistantContent.WriteString(answer)
		assistantReasoning.Reset()
		assistantReasoning.WriteString(reasoning)
		modelName = a.resolveAiModelName(targetAIConfigID)
		aiConfigId = targetAIConfigID
		if !shouldFailover || idx == len(runOrder)-1 {
			break
		}
		logger.SugaredLogger.Warnf("AI智能体请求失败，自动切换备用模型。from=%d to=%d attempt=%d", targetAIConfigID, runOrder[idx+1], idx+2)
		go emitEvent(a.ctx, "warnMsg", "AI智能体已自动切换到备用模型继续重试")
	}
	for _, msg := range assistantMessages {
		emitEvent(a.ctx, "agent-message", msg)
	}
	answer := strings.TrimSpace(assistantContent.String())
	if answer == "" {
		return
	}

	_ = a.services.History.UpdateSessionModel(sessionId, aiConfigId, modelName)
	_ = a.services.History.AppendMessage(sessionId, "user", question, "")
	_ = a.services.History.AppendMessage(sessionId, "assistant", answer, assistantReasoning.String())
	_ = a.services.History.TrimSessionMessages(sessionId, models.DefaultAgentMessageLimit)
	_ = a.services.History.TrimSessions(models.DefaultAgentSessionLimit)

	if session != nil && strings.TrimSpace(session.Title) == "新对话" {
		go func() {
			title := a.summarizeAgentSessionTitleByAI(question, aiConfigId)
			if strings.TrimSpace(title) != "" {
				_ = a.services.History.UpdateSessionTitle(sessionId, title)
			}
		}()
	}
}

func runAgentWithFallback(toolDataProvider agenttools.ToolDataProvider, configuration agent.ConfigurationProvider, messages []*schema.Message, aiConfigId int, sysPromptId *int) ([]*schema.Message, string, string, bool) {
	ch := agent.NewStockAiAgentApi(toolDataProvider, configuration).ChatWithMessages(messages, aiConfigId, sysPromptId)
	result := make([]*schema.Message, 0, 128)
	var assistantContent strings.Builder
	var assistantReasoning strings.Builder
	errs := make([]string, 0)
	for msg := range ch {
		if msg == nil {
			continue
		}
		result = append(result, msg)
		if msg.Role != schema.Assistant {
			continue
		}
		if strings.TrimSpace(msg.Content) != "" {
			if isAgentFailureText(msg.Content) {
				errs = append(errs, strings.TrimSpace(msg.Content))
			} else {
				assistantContent.WriteString(msg.Content)
			}
		}
		if strings.TrimSpace(msg.ReasoningContent) != "" {
			assistantReasoning.WriteString(msg.ReasoningContent)
		}
	}
	answer := strings.TrimSpace(assistantContent.String())
	if answer != "" {
		return result, answer, strings.TrimSpace(assistantReasoning.String()), false
	}
	if len(errs) == 0 {
		return result, answer, strings.TrimSpace(assistantReasoning.String()), true
	}
	return result, answer, strings.TrimSpace(assistantReasoning.String()), true
}

func isAgentFailureText(content string) bool {
	text := strings.TrimSpace(content)
	if text == "" {
		return false
	}
	return strings.Contains(text, "AI智能体初始化失败") ||
		strings.Contains(text, "AI智能体请求失败") ||
		strings.Contains(text, "AI智能体响应中断")
}
