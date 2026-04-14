package service

import (
	"context"
	"go-stock/backend/data"
	"go-stock/backend/models"
	"strings"
	"time"
)

type AIService struct{}

func NewAIService() AIService {
	return AIService{}
}

func (s AIService) AnalyzeSentiment(text string) models.SentimentResult {
	return data.AnalyzeSentiment(text)
}

func (s AIService) AnalyzeSentimentWithFreqWeight(text string) map[string]any {
	result, cleanFrequencies := data.NewsAnalyze(text, false)
	return map[string]any{
		"result":      result,
		"frequencies": cleanFrequencies,
	}
}

func (s AIService) GetAIResponseResult(ctx context.Context, stockCode string) *models.AIResponseResult {
	return data.NewDeepSeekOpenAi(ctx, 0).GetAIResponseResult(stockCode)
}

func (s AIService) SaveAIResponseResult(ctx context.Context, stockCode, stockName, result, chatID, question string, aiConfigID int) {
	data.NewDeepSeekOpenAi(ctx, aiConfigID).SaveAIResponseResult(stockCode, stockName, result, chatID, question)
}

func (s AIService) GetPromptTemplates(name, promptType string) *[]models.PromptTemplate {
	return data.NewPromptTemplateApi().GetPromptTemplates(name, promptType)
}

func (s AIService) AddPrompt(prompt models.PromptTemplate) string {
	return data.NewPromptTemplateApi().AddPrompt(prompt)
}

func (s AIService) DelPrompt(id uint) string {
	return data.NewPromptTemplateApi().DelPrompt(id)
}

func (s AIService) GetAiConfigs() []*data.AIConfig {
	cfg := data.GetSettingConfig()
	if cfg == nil {
		return []*data.AIConfig{}
	}
	return cfg.AiConfigs
}

func (s AIService) ResolveDefaultAIConfigID() int {
	return data.SelectPrimaryAIConfigID(data.GetSettingConfig())
}

func (s AIService) ResolveAIFallbackOrder(aiConfigID int) []int {
	return data.ResolveAIFallbackOrder(data.GetSettingConfig(), aiConfigID)
}

func (s AIService) ResolveAIModelName(aiConfigID int) string {
	cfg := data.GetSettingConfig()
	if cfg == nil || len(cfg.AiConfigs) == 0 {
		return ""
	}
	if aiConfigID <= 0 {
		if primary := data.SelectPrimaryAIConfig(cfg.AiConfigs); primary != nil {
			return strings.TrimSpace(primary.ModelName)
		}
		return ""
	}
	for _, item := range cfg.AiConfigs {
		if item != nil && int(item.ID) == aiConfigID {
			return strings.TrimSpace(item.ModelName)
		}
	}
	if primary := data.SelectPrimaryAIConfig(cfg.AiConfigs); primary != nil {
		return strings.TrimSpace(primary.ModelName)
	}
	return ""
}

func (s AIService) NewChatStream(ctx context.Context, stock, stockCode, question string, aiConfigID int, sysPromptID *int, tools []data.Tool, think bool) <-chan map[string]any {
	return data.NewDeepSeekOpenAi(ctx, aiConfigID).NewChatStream(stock, stockCode, question, sysPromptID, tools, think)
}

func (s AIService) NewSummaryStockNewsStream(ctx context.Context, aiConfigID int, question string, sysPromptID *int, think bool) <-chan map[string]any {
	return data.NewDeepSeekOpenAi(ctx, aiConfigID).NewSummaryStockNewsStream(question, sysPromptID, think)
}

func (s AIService) NewSummaryStockNewsStreamPhased(ctx context.Context, aiConfigID int, question string, sysPromptID *int, think bool) <-chan map[string]any {
	return data.NewDeepSeekOpenAi(ctx, aiConfigID).NewSummaryStockNewsStreamPhased(question, sysPromptID, think)
}

func (s AIService) NormalizeMarketSummaryQuestion(question string) string {
	return data.NormalizeMarketSummaryQuestion(question)
}

func (s AIService) EnsureMarketSummaryRecommendStocksSaved(summaryText, providerName, modelName string, startedAt time.Time) (int, error) {
	return data.EnsureMarketSummaryRecommendStocksSaved(summaryText, providerName, modelName, startedAt)
}

func (s AIService) EnsureMarketSummaryYieldOverridesSaved(summaryText string, startedAt time.Time) (int, error) {
	return data.EnsureMarketSummaryYieldOverridesSaved(summaryText, startedAt)
}

func (s AIService) SendYieldEmailTestMessage() error {
	return data.SendYieldEmailTestMessage()
}

func (s AIService) SendYieldEmailCSVNow() (int, error) {
	return data.SendYieldEmailCSVNow()
}

func (s AIService) SendLatestAIAnalysisReportEmail() (*models.AIResponseResult, error) {
	return data.SendLatestAIAnalysisReportEmail()
}

func (s AIService) SendLatestAIAnalysisReportEmailForCron() (*models.AIResponseResult, error) {
	return data.SendLatestAIAnalysisReportEmailForCron()
}

func (s AIService) SendMarketSummaryEmail(sendType string, report *models.AIResponseResult, failureReason string) error {
	return data.SendMarketSummaryEmail(sendType, report, failureReason)
}
