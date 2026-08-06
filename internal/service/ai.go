package service

import (
	"context"
	"time"

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

func (s AIService) NewSummaryStockNewsStreamPhased(ctx context.Context, aiConfigID int, question string, sysPromptID *int, think bool) <-chan map[string]any {
	return s.operations.NewSummaryStockNewsStreamPhased(ctx, aiConfigID, question, sysPromptID, think)
}

func (s AIService) GenerateMarketSummarySupplementTable(ctx context.Context, aiConfigID int, req models.MarketSummarySupplementRequest) (string, string, string, error) {
	return s.operations.GenerateMarketSummarySupplementTable(ctx, aiConfigID, req)
}

func (s AIService) NormalizeMarketSummaryQuestion(question string) string {
	return s.operations.NormalizeMarketSummaryQuestion(question)
}

func (s AIService) ResolveMarketSummaryRecommendationCountPolicy(question string) MarketSummaryRecommendationCountPolicy {
	return s.operations.ResolveMarketSummaryRecommendationCountPolicy(question)
}

func (s AIService) PrepareMarketSummaryReportForPersistence(summaryText string, startedAt time.Time, outputLimit int) (string, MarketSummaryReportPrepareStats, error) {
	return s.operations.PrepareMarketSummaryReportForPersistence(summaryText, startedAt, outputLimit)
}

func (s AIService) RunMorningOpeningReview(now time.Time) (string, error) {
	return s.operations.RunMorningOpeningReview(now)
}

func (s AIService) MergeMarketSummarySupplementReport(baseText, supplementText string, acceptedCodes []string, maximumOutput int) (string, MarketSummaryReportMergeStats) {
	return s.operations.MergeMarketSummarySupplementReport(baseText, supplementText, acceptedCodes, maximumOutput)
}

func (s AIService) EnsureMarketSummaryRecommendStocksSaved(summaryText, providerName, modelName string, startedAt time.Time) (int, error) {
	return s.operations.EnsureMarketSummaryRecommendStocksSaved(summaryText, providerName, modelName, startedAt)
}

func (s AIService) EnsureMarketSummaryRecommendStocksSavedWithResult(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []models.MarketSummaryVerifiedCandidateSnapshot) (*models.MarketSummaryRecommendSaveResult, error) {
	return s.operations.EnsureMarketSummaryRecommendStocksSavedWithResult(summaryText, providerName, modelName, startedAt, verifiedCandidates)
}

func (s AIService) EnsureMarketSummaryRecommendStocksSavedWithResultLimit(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []models.MarketSummaryVerifiedCandidateSnapshot, productionLimit int) (*models.MarketSummaryRecommendSaveResult, error) {
	return s.operations.EnsureMarketSummaryRecommendStocksSavedWithResultLimit(summaryText, providerName, modelName, startedAt, verifiedCandidates, productionLimit)
}

func (s AIService) EnsureMarketSummaryRecommendStocksSavedWithResultLimits(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []models.MarketSummaryVerifiedCandidateSnapshot, outputLimit, productionLimit int) (*models.MarketSummaryRecommendSaveResult, error) {
	return s.operations.EnsureMarketSummaryRecommendStocksSavedWithResultLimits(summaryText, providerName, modelName, startedAt, verifiedCandidates, outputLimit, productionLimit)
}

func (s AIService) EnsureMarketSummaryRecommendStocksSavedWithResultOptions(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []models.MarketSummaryVerifiedCandidateSnapshot, options models.MarketSummaryRecommendSaveOptions) (*models.MarketSummaryRecommendSaveResult, error) {
	return s.operations.EnsureMarketSummaryRecommendStocksSavedWithResultOptions(summaryText, providerName, modelName, startedAt, verifiedCandidates, options)
}

func (s AIService) EnsureMarketSummaryYieldOverridesSaved(summaryText string, startedAt time.Time) (int, error) {
	return s.operations.EnsureMarketSummaryYieldOverridesSaved(summaryText, startedAt)
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
