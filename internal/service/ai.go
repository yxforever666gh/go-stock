package service

import (
	"context"

	"go-stock/backend/models"
)

type AIService struct {
	operations AIOperations
}

func NewAIService(operations AIOperations) AIService {
	return AIService{operations: operations}
}

func (s AIService) TestAIConfig(ctx context.Context, aiConfigID int) *models.AIModelTestResult {
	return s.operations.TestAIConfig(ctx, aiConfigID)
}

func (s AIService) HumanizeMarketSummaryReport(raw string) string {
	return s.operations.HumanizeMarketSummaryReport(raw)
}

func (s AIService) AnalyzeSentiment(text string) models.SentimentResult {
	return s.operations.AnalyzeSentiment(text)
}

func (s AIService) AnalyzeSentimentWithFreqWeight(text string) map[string]any {
	return s.operations.AnalyzeSentimentWithFreqWeight(text)
}

func (s AIService) GetAIResponseResult(ctx context.Context, stockCode string) *models.AIResponseResult {
	return s.operations.GetAIResponseResult(ctx, stockCode)
}

func (s AIService) SaveAIResponseResult(ctx context.Context, stockCode, stockName, result, chatID, question string, aiConfigID int) {
	s.operations.SaveAIResponseResult(ctx, stockCode, stockName, result, chatID, question, aiConfigID)
}

func (s AIService) GetPromptTemplates(name, promptType string) *[]models.PromptTemplate {
	return s.operations.GetPromptTemplates(name, promptType)
}

func (s AIService) AddPrompt(prompt models.PromptTemplate) string {
	return s.operations.AddPrompt(prompt)
}

func (s AIService) DelPrompt(id uint) string {
	return s.operations.DelPrompt(id)
}

func (s AIService) GetAiConfigs() []*models.AIConfig {
	return s.operations.GetAIConfigs()
}

func (s AIService) ResolveDefaultAIConfigID() int {
	return s.operations.ResolveDefaultAIConfigID()
}

func (s AIService) ResolveAIFallbackOrder(aiConfigID int) []int {
	return s.operations.ResolveAIFallbackOrder(aiConfigID)
}

func (s AIService) ResolveAIModelName(aiConfigID int) string {
	return s.operations.ResolveAIModelName(aiConfigID)
}

func (s AIService) NewChatStream(ctx context.Context, stock, stockCode, question string, aiConfigID int, sysPromptID *int, tools []models.Tool, think bool) <-chan map[string]any {
	return s.operations.NewChatStream(ctx, stock, stockCode, question, aiConfigID, sysPromptID, tools, think)
}

func (s AIService) NewSummaryStockNewsStream(ctx context.Context, aiConfigID int, question string, sysPromptID *int, think bool) <-chan map[string]any {
	return s.operations.NewSummaryStockNewsStream(ctx, aiConfigID, question, sysPromptID, think)
}

func (s AIService) NormalizeMarketSummaryQuestion(question string) string {
	return s.operations.NormalizeMarketSummaryQuestion(question)
}

func (s AIService) SendYieldEmailTestMessage() error {
	return s.operations.SendYieldEmailTestMessage()
}

func (s AIService) SendYieldEmailXLSXNow() (int, error) {
	return s.operations.SendYieldEmailXLSXNow()
}

func (s AIService) SendLatestAIAnalysisReportEmail() (*models.AIResponseResult, error) {
	return s.operations.SendLatestAIAnalysisReportEmail()
}

func (s AIService) SendLatestAIAnalysisReportEmailForCron() (*models.AIResponseResult, error) {
	return s.operations.SendLatestAIAnalysisReportEmailForCron()
}

func (s AIService) SendMarketSummaryEmail(sendType string, report *models.AIResponseResult, failureReason string) error {
	return s.operations.SendMarketSummaryEmail(sendType, report, failureReason)
}
