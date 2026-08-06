package bootstrap

import (
	"context"
	"errors"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/models"
	"go-stock/internal/service"

	"github.com/cloudwego/eino/schema"
)

func (*compatibilityServiceAdapter) AnalyzeSentiment(text string) models.SentimentResult {
	return data.AnalyzeSentiment(text)
}

func (*compatibilityServiceAdapter) AnalyzeSentimentWithFreqWeight(text string) map[string]any {
	result, frequencies := data.NewsAnalyze(text, false)
	return map[string]any{"result": result, "frequencies": frequencies}
}

func (*compatibilityServiceAdapter) GetAIResponseResult(ctx context.Context, stockCode string) *models.AIResponseResult {
	return data.NewDeepSeekOpenAi(ctx, 0).GetAIResponseResult(stockCode)
}

func (*compatibilityServiceAdapter) SaveAIResponseResult(ctx context.Context, stockCode, stockName, result, chatID, question string, aiConfigID int) {
	data.NewDeepSeekOpenAi(ctx, aiConfigID).SaveAIResponseResult(stockCode, stockName, result, chatID, question)
}

func (*compatibilityServiceAdapter) GetPromptTemplates(name, promptType string) *[]models.PromptTemplate {
	return data.NewPromptTemplateApi().GetPromptTemplates(name, promptType)
}

func (*compatibilityServiceAdapter) AddPrompt(prompt models.PromptTemplate) string {
	return data.NewPromptTemplateApi().AddPrompt(prompt)
}

func (*compatibilityServiceAdapter) DelPrompt(id uint) string {
	return data.NewPromptTemplateApi().DelPrompt(id)
}

func (*compatibilityServiceAdapter) GetAIConfigs() []*models.AIConfig {
	cfg := data.GetSettingConfig()
	if cfg == nil {
		return []*models.AIConfig{}
	}
	return cfg.AiConfigs
}

func (*compatibilityServiceAdapter) ResolveDefaultAIConfigID() int {
	return data.SelectPrimaryAIConfigID(data.GetSettingConfig())
}

func (*compatibilityServiceAdapter) ResolveAIFallbackOrder(aiConfigID int) []int {
	return data.ResolveAIFallbackOrder(data.GetSettingConfig(), aiConfigID)
}

func (*compatibilityServiceAdapter) ResolveAIModelName(aiConfigID int) string {
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

func (*compatibilityServiceAdapter) NewChatStream(ctx context.Context, stock, stockCode, question string, aiConfigID int, sysPromptID *int, tools []models.Tool, think bool) <-chan map[string]any {
	return data.NewDeepSeekOpenAi(ctx, aiConfigID).NewChatStream(stock, stockCode, question, sysPromptID, tools, think)
}

func (*compatibilityServiceAdapter) NewSummaryStockNewsStream(ctx context.Context, aiConfigID int, question string, sysPromptID *int, think bool) <-chan map[string]any {
	return data.NewDeepSeekOpenAi(ctx, aiConfigID).NewSummaryStockNewsStream(question, sysPromptID, think)
}

func (*compatibilityServiceAdapter) NewSummaryStockNewsStreamPhased(ctx context.Context, aiConfigID int, question string, sysPromptID *int, think bool) <-chan map[string]any {
	return data.NewDeepSeekOpenAi(ctx, aiConfigID).NewSummaryStockNewsStreamPhased(question, sysPromptID, think)
}

func (*compatibilityServiceAdapter) GenerateMarketSummarySupplementTable(ctx context.Context, aiConfigID int, req models.MarketSummarySupplementRequest) (string, string, string, error) {
	return data.NewDeepSeekOpenAi(ctx, aiConfigID).GenerateMarketSummarySupplementTable(req)
}

func (*compatibilityServiceAdapter) NormalizeMarketSummaryQuestion(question string) string {
	return data.NormalizeMarketSummaryQuestion(question)
}

func (*compatibilityServiceAdapter) EnsureMarketSummaryRecommendStocksSaved(summaryText, providerName, modelName string, startedAt time.Time) (int, error) {
	return data.EnsureMarketSummaryRecommendStocksSaved(summaryText, providerName, modelName, startedAt)
}

func (*compatibilityServiceAdapter) EnsureMarketSummaryRecommendStocksSavedWithResult(summaryText, providerName, modelName string, startedAt time.Time, verified []models.MarketSummaryVerifiedCandidateSnapshot) (*models.MarketSummaryRecommendSaveResult, error) {
	return data.EnsureMarketSummaryRecommendStocksSavedWithResult(summaryText, providerName, modelName, startedAt, verified)
}

func (*compatibilityServiceAdapter) EnsureMarketSummaryRecommendStocksSavedWithResultLimit(summaryText, providerName, modelName string, startedAt time.Time, verified []models.MarketSummaryVerifiedCandidateSnapshot, productionLimit int) (*models.MarketSummaryRecommendSaveResult, error) {
	return data.EnsureMarketSummaryRecommendStocksSavedWithResultLimit(summaryText, providerName, modelName, startedAt, verified, productionLimit)
}

func (*compatibilityServiceAdapter) EnsureMarketSummaryRecommendStocksSavedWithResultLimits(summaryText, providerName, modelName string, startedAt time.Time, verified []models.MarketSummaryVerifiedCandidateSnapshot, outputLimit, productionLimit int) (*models.MarketSummaryRecommendSaveResult, error) {
	return data.EnsureMarketSummaryRecommendStocksSavedWithResultLimits(summaryText, providerName, modelName, startedAt, verified, outputLimit, productionLimit)
}

func (*compatibilityServiceAdapter) EnsureMarketSummaryRecommendStocksSavedWithResultOptions(summaryText, providerName, modelName string, startedAt time.Time, verified []models.MarketSummaryVerifiedCandidateSnapshot, options models.MarketSummaryRecommendSaveOptions) (*models.MarketSummaryRecommendSaveResult, error) {
	return data.EnsureMarketSummaryRecommendStocksSavedWithResultOptions(summaryText, providerName, modelName, startedAt, verified, options)
}

func (*compatibilityServiceAdapter) EnsureMarketSummaryYieldOverridesSaved(summaryText string, startedAt time.Time) (int, error) {
	return data.EnsureMarketSummaryYieldOverridesSaved(summaryText, startedAt)
}

func (*compatibilityServiceAdapter) SendYieldEmailTestMessage() error {
	return data.SendYieldEmailTestMessage()
}

func (*compatibilityServiceAdapter) SendYieldEmailXLSXNow() (int, error) {
	return data.SendYieldEmailXLSXNow()
}

func (*compatibilityServiceAdapter) SendLatestAIAnalysisReportEmail() (*models.AIResponseResult, error) {
	return data.SendLatestAIAnalysisReportEmail()
}

func (*compatibilityServiceAdapter) SendLatestAIAnalysisReportEmailForCron() (*models.AIResponseResult, error) {
	return data.SendLatestAIAnalysisReportEmailForCron()
}

func (*compatibilityServiceAdapter) SendMarketSummaryEmail(sendType string, report *models.AIResponseResult, failureReason string) error {
	return data.SendMarketSummaryEmail(sendType, report, failureReason)
}

func (*compatibilityServiceAdapter) GetConfig() *models.SettingConfig {
	return data.GetSettingConfig()
}

func (*compatibilityServiceAdapter) ExportConfig() string {
	return data.NewSettingsApi().Export()
}

func (*compatibilityServiceAdapter) UpdateConfig(config *models.SettingConfig) string {
	return data.UpdateConfig(config)
}

func (*compatibilityServiceAdapter) ResolveFingerprint() (string, error) {
	settings := data.GetSettingConfig()
	if settings != nil && settings.Settings != nil && strings.TrimSpace(settings.QgqpBId) != "" {
		return strings.TrimSpace(settings.QgqpBId), nil
	}
	return "", errors.New("missing qgqp_b_id")
}

func (*compatibilityServiceAdapter) DeleteSession(sessionID string) error {
	return data.NewAgentChatHistoryService().DeleteSession(sessionID)
}

func (*compatibilityServiceAdapter) EnsureSession(sessionID, title string, aiConfigID int, modelName string) (*models.AgentChatSession, error) {
	return data.NewAgentChatHistoryService().EnsureSession(sessionID, title, aiConfigID, modelName)
}

func (*compatibilityServiceAdapter) TrimSessions(maxSessions int) error {
	return data.NewAgentChatHistoryService().TrimSessions(maxSessions)
}

func (*compatibilityServiceAdapter) ListRecentSessions(limit int) ([]models.AgentChatSession, error) {
	return data.NewAgentChatHistoryService().ListRecentSessions(limit)
}

func (*compatibilityServiceAdapter) ListSessionMessages(sessionID string, limit int) ([]models.AgentChatMessage, error) {
	return data.NewAgentChatHistoryService().ListSessionMessages(sessionID, limit)
}

func (*compatibilityServiceAdapter) GetSession(sessionID string) (*models.AgentChatSession, error) {
	return data.NewAgentChatHistoryService().GetSession(sessionID)
}

func (*compatibilityServiceAdapter) FirstUserQuestion(sessionID string) (string, error) {
	return data.NewAgentChatHistoryService().FirstUserQuestion(sessionID)
}

func (*compatibilityServiceAdapter) UpdateSessionTitle(sessionID, title string) error {
	return data.NewAgentChatHistoryService().UpdateSessionTitle(sessionID, title)
}

func (*compatibilityServiceAdapter) UpdateSessionModel(sessionID string, aiConfigID int, modelName string) error {
	return data.NewAgentChatHistoryService().UpdateSessionModel(sessionID, aiConfigID, modelName)
}

func (*compatibilityServiceAdapter) ListSessionMessagesForAgent(sessionID string, limit int) ([]*schema.Message, error) {
	return data.NewAgentChatHistoryService().ListSessionMessagesForAgent(sessionID, limit)
}

func (*compatibilityServiceAdapter) AppendMessage(sessionID, role, content, reasoning string) error {
	return data.NewAgentChatHistoryService().AppendMessage(sessionID, role, content, reasoning)
}

func (*compatibilityServiceAdapter) TrimSessionMessages(sessionID string, maxMessages int) error {
	return data.NewAgentChatHistoryService().TrimSessionMessages(sessionID, maxMessages)
}

func (*compatibilityServiceAdapter) GetAIResponseResultList(query models.AIResponseResultQuery) (*models.AIResponseResultPageData, error) {
	return data.NewAIResponseResultService().GetAIResponseResultList(query)
}

func (*compatibilityServiceAdapter) GetEmailSendLogList(query models.EmailSendLogQuery) (*models.EmailSendLogPageData, error) {
	return data.NewEmailSendLogService().GetEmailSendLogList(query)
}

func (*compatibilityServiceAdapter) DeleteAIResponseResult(id uint) error {
	return data.NewAIResponseResultService().DeleteAIResponseResult(id)
}

func (*compatibilityServiceAdapter) BatchDeleteAIResponseResult(ids []uint) error {
	return data.NewAIResponseResultService().BatchDeleteAIResponseResult(ids)
}

func (*compatibilityServiceAdapter) GetAiRecommendStocksList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendStocksList(query)
}

func (*compatibilityServiceAdapter) GetAiRecommendStocksDateRange() (string, string, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendStocksDateRange()
}

func (*compatibilityServiceAdapter) GetAiRecommendStocksYieldList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksYieldPageData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendStocksYieldList(query)
}

func (*compatibilityServiceAdapter) GetAiRecommendYieldMinuteChart(recommendID uint) (*models.AiRecommendYieldMinuteChartData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldMinuteChart(recommendID)
}

func (*compatibilityServiceAdapter) GetAiRecommendYieldDailyOverview(query *models.AiRecommendStocksQuery) (*models.AiRecommendYieldDailyOverviewData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(query)
}

func (*compatibilityServiceAdapter) StartAiRecommendMinuteDownload() (map[string]any, error) {
	return data.NewAiRecommendStocksService().StartAiRecommendMinuteDownload()
}

func (*compatibilityServiceAdapter) GetAiRecommendYieldTaskStatus() (*models.AiRecommendStocksYieldPageData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldTaskStatus()
}

func (*compatibilityServiceAdapter) GetAiRecommendYieldErrorLogs(limit int) ([]map[string]string, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldErrorLogs(limit)
}

func (*compatibilityServiceAdapter) GetMarketSummaryRunDiagnostics(query models.MarketSummaryRunDiagnosticQuery) (models.MarketSummaryRunDiagnosticSummary, error) {
	return data.GetMarketSummaryRunDiagnostics(query)
}

func (*compatibilityServiceAdapter) GetMarketSummaryEmptyRunCount(query models.MarketSummaryRunDiagnosticQuery) (int64, error) {
	return data.GetMarketSummaryEmptyRunCount(query)
}

func (*compatibilityServiceAdapter) GetMarketSummaryBlockedReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	return data.GetMarketSummaryBlockedReasonTop(query)
}

func (*compatibilityServiceAdapter) GetMarketSummaryProductionDowngradeReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	return data.GetMarketSummaryProductionDowngradeReasonTop(query)
}

func (*compatibilityServiceAdapter) DeleteAiRecommendStocks(id uint) error {
	return data.NewAiRecommendStocksService().DeleteAiRecommendStocks(id)
}

func (*compatibilityServiceAdapter) RepairHistoricalMarketSummaryActivationIssues(now time.Time) (service.MarketSummaryActivationRepairResult, error) {
	result, err := data.RepairHistoricalMarketSummaryActivationIssues(now)
	if err != nil {
		return service.MarketSummaryActivationRepairResult{}, err
	}
	return service.MarketSummaryActivationRepairResult{
		Scanned:      result.Scanned,
		Downgraded:   result.Downgraded,
		RuleUpgraded: result.RuleUpgraded,
		SkippedNoRef: result.SkippedNoRef,
	}, nil
}
