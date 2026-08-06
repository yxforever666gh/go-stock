package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/recommendation"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/service"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

var _ service.MarketSummaryDecisionSnapshot = (*service.MarketSummaryV150DecisionEnvelope)(nil)
var _ recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult] = (*compatibilityServiceAdapter)(nil)

func NewProductionCommandAIResolver() (cliports.CommandAIResolver, error) {
	if db.Dao == nil {
		return nil, errors.New("main database is not initialized")
	}
	return &compatibilityServiceAdapter{main: db.Dao}, nil
}

func (a *compatibilityServiceAdapter) ResolveCommandAI(ctx context.Context, opts cliports.CommandAIOptions) (cliports.CommandAIClient, error) {
	cfg, err := resolveCommandAIConfig(ctx, a.main, opts)
	if err != nil {
		return nil, err
	}
	return data.NewOpenAiWithConfig(ctx, cfg), nil
}

func resolveCommandAIConfig(ctx context.Context, main *gorm.DB, opts cliports.CommandAIOptions) (*models.AIConfig, error) {
	allFromFlags := opts.BaseURL != "" && opts.APIKey != "" && opts.Model != ""
	anyFromFlags := opts.BaseURL != "" || opts.APIKey != "" || opts.Model != ""
	if anyFromFlags && !allFromFlags {
		return nil, errors.New("参数模式下必须同时提供 --base-url、--api-key、--model")
	}
	if allFromFlags {
		return &models.AIConfig{
			BaseUrl:     opts.BaseURL,
			ApiKey:      opts.APIKey,
			ModelName:   opts.Model,
			MaxTokens:   opts.MaxTokens,
			Temperature: opts.Temperature,
			TimeOut:     opts.Timeout,
		}, nil
	}
	if main == nil {
		return nil, errors.New("main database is not initialized")
	}

	cfg := &models.AIConfig{}
	tx := main.WithContext(ctx).Model(&models.AIConfig{})
	var err error
	if opts.AIConfigID > 0 {
		err = tx.Where("id = ?", opts.AIConfigID).First(cfg).Error
	} else {
		err = tx.Order("id asc").First(cfg).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("未找到可用 AI 配置，请使用参数模式或先写入 ai_config 表")
		}
		return nil, fmt.Errorf("读取 AI 配置失败: %w", err)
	}
	if cfg.BaseUrl == "" || cfg.ApiKey == "" || cfg.ModelName == "" {
		return nil, errors.New("数据库 AI 配置不完整，请检查 base_url/api_key/model_name")
	}
	return cfg, nil
}

func (*compatibilityServiceAdapter) TestAIConfig(ctx context.Context, aiConfigID int) *models.AIModelTestResult {
	startedAt := time.Now()
	result := &models.AIModelTestResult{
		Message:  "\u6d4b\u8bd5\u5931\u8d25",
		Protocol: models.AIAPIProtocolChatCompletions,
	}
	if aiConfigID <= 0 {
		result.Message = "\u8bf7\u5148\u4fdd\u5b58 AI \u914d\u7f6e\u540e\u518d\u6d4b\u8bd5"
		return result
	}
	setting := data.GetSettingConfig()
	if setting == nil || len(setting.AiConfigs) == 0 {
		result.Message = "\u672a\u627e\u5230 AI \u914d\u7f6e"
		return result
	}
	var aiConfig *models.AIConfig
	for _, item := range setting.AiConfigs {
		if item != nil && int(item.ID) == aiConfigID {
			aiConfig = item
			break
		}
	}
	if aiConfig == nil {
		result.Message = "\u672a\u627e\u5230\u6307\u5b9a AI \u914d\u7f6e\uff0c\u8bf7\u4fdd\u5b58\u540e\u91cd\u8bd5"
		return result
	}
	result.Protocol = models.NormalizeAIAPIProtocol(aiConfig.ApiProtocol)
	result.Model = strings.TrimSpace(aiConfig.ModelName)
	if strings.TrimSpace(aiConfig.BaseUrl) == "" || strings.TrimSpace(aiConfig.ApiKey) == "" || result.Model == "" {
		result.Message = "\u8bf7\u5b8c\u6574\u586b\u5199\u63a5\u53e3\u5730\u5740\u3001API Key \u548c\u6a21\u578b\u540d\u79f0"
		return result
	}

	content, _, modelName, err := data.NewOpenAiWithConfig(ctx, aiConfig).CompleteChat([]map[string]any{
		{"role": "user", "content": "\u8bf7\u53ea\u56de\u590d OK"},
	}, false)
	result.LatencyMs = time.Since(startedAt).Milliseconds()
	if strings.TrimSpace(modelName) != "" {
		result.Model = strings.TrimSpace(modelName)
	}
	if err != nil {
		result.Message = err.Error()
		return result
	}
	content = strings.TrimSpace(content)
	if content == "" {
		result.Message = "\u6a21\u578b\u8fd4\u56de\u5185\u5bb9\u4e3a\u7a7a"
		return result
	}
	result.Success = true
	result.Message = "\u6d4b\u8bd5\u6210\u529f"
	if runes := []rune(content); len(runes) > 120 {
		content = string(runes[:120])
	}
	result.ContentPreview = content
	return result
}

func (*compatibilityServiceAdapter) HumanizeMarketSummaryReport(raw string) string {
	return data.HumanizeMarketSummaryReport(raw)
}

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

func (*compatibilityServiceAdapter) ResolveMarketSummaryRecommendationCountPolicy(question string) service.MarketSummaryRecommendationCountPolicy {
	policy := data.ResolveMarketSummaryRecommendationCountPolicy(question)
	return service.MarketSummaryRecommendationCountPolicy{
		MinimumOutput: policy.MinimumOutput, MaximumOutput: policy.MaximumOutput, ProductionTarget: policy.ProductionTarget,
		RequestedMinimum: policy.RequestedMinimum, RequestedMaximum: policy.RequestedMaximum, Source: policy.Source,
		Custom: policy.Custom, Clamped: policy.Clamped,
	}
}

func (*compatibilityServiceAdapter) PrepareMarketSummaryReportForPersistence(summaryText string, startedAt time.Time, outputLimit int) (string, service.MarketSummaryReportPrepareStats, error) {
	prepared, stats, err := data.PrepareMarketSummaryReportForPersistenceWithLimit(summaryText, startedAt, outputLimit)
	return prepared, service.MarketSummaryReportPrepareStats{
		RowsSeen: stats.RowsSeen, DuplicateRowsOmit: stats.DuplicateRowsOmit, OutputRowsOmit: stats.OutputRowsOmit,
		AnalysisOnlyRows: stats.AnalysisOnlyRows, RecommendationRows: stats.RecommendationRows,
	}, err
}

func (*compatibilityServiceAdapter) RunMorningOpeningReview(now time.Time) (string, error) {
	return data.RunMorningOpeningReview(now)
}

func (*compatibilityServiceAdapter) MergeMarketSummarySupplementReport(baseText, supplementText string, acceptedCodes []string, maximumOutput int) (string, service.MarketSummaryReportMergeStats) {
	merged, stats := data.MergeMarketSummarySupplementReport(baseText, supplementText, acceptedCodes, maximumOutput)
	return merged, service.MarketSummaryReportMergeStats{
		BaseTableFound: stats.BaseTableFound, SupplementTableFound: stats.SupplementTableFound, MaximumOutput: stats.MaximumOutput,
		AcceptedCodeCount: stats.AcceptedCodeCount, BaseRecommendationRows: stats.BaseRecommendationRows, SupplementRecommendationRows: stats.SupplementRecommendationRows,
		DuplicateRowsOmitted: stats.DuplicateRowsOmitted, UnconfirmedRowsOmitted: stats.UnconfirmedRowsOmitted, OutputRowsOmitted: stats.OutputRowsOmitted,
		ReplacedCodes: append([]string(nil), stats.ReplacedCodes...), AppendedCodes: append([]string(nil), stats.AppendedCodes...), VisibleCodes: append([]string(nil), stats.VisibleCodes...),
		UnconfirmedCodes: append([]string(nil), stats.UnconfirmedCodes...), MissingAcceptedCodes: append([]string(nil), stats.MissingAcceptedCodes...), OmittedByLimitCodes: append([]string(nil), stats.OmittedByLimitCodes...),
	}
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

func (a *compatibilityServiceAdapter) RequireStrategyLive(ctx context.Context, strategyVersion string) error {
	return governance.RequireStrategyLive(ctx, a.main, strategyVersion)
}

func (*compatibilityServiceAdapter) EncodeMarketSummaryBlockedReasons(items []models.MarketSummaryBlockedReasonItem) string {
	return data.EncodeMarketSummaryBlockedReasons(items)
}

func (*compatibilityServiceAdapter) SaveMarketSummaryRunDiagnostic(item *models.MarketSummaryRunDiagnostic) error {
	return data.SaveMarketSummaryRunDiagnostic(item)
}

func (a *compatibilityServiceAdapter) CreateAIResponseReport(ctx context.Context, result *models.AIResponseResult) error {
	if a.main == nil {
		return errors.New("main database is not initialized")
	}
	if result == nil {
		return errors.New("AI response result is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return a.main.WithContext(ctx).Create(result).Error
}

func (a *compatibilityServiceAdapter) PersistAIResponseReport(ctx context.Context, result *models.AIResponseResult) error {
	if a.main == nil {
		return errors.New("main database is not initialized")
	}
	if result == nil {
		return errors.New("AI response result is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return a.main.WithContext(ctx).Save(result).Error
}

func (a *compatibilityServiceAdapter) PublishDecision(
	ctx context.Context,
	decision service.MarketSummaryDecisionSnapshot,
	providerName, modelName string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	envelope, ok := decision.(*service.MarketSummaryV150DecisionEnvelope)
	if !ok || envelope == nil {
		return nil, fmt.Errorf("unsupported market summary decision snapshot %T", decision)
	}
	run, err := marketSummaryV150SnapshotFromEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	return data.PersistMarketSummaryV150Decision(ctx, a.main, run, providerName, modelName)
}

func marketSummaryV150SnapshotFromEnvelope(envelope *service.MarketSummaryV150DecisionEnvelope) (*data.MarketSummaryV150RunSnapshot, error) {
	if envelope == nil || len(envelope.RawJSON) == 0 {
		return nil, errors.New("market summary V1.5 decision envelope is incomplete")
	}
	var run data.MarketSummaryV150RunSnapshot
	if err := json.Unmarshal(envelope.RawJSON, &run); err != nil {
		return nil, fmt.Errorf("decode market summary V1.5 decision envelope: %w", err)
	}
	if strings.TrimSpace(run.RunContext.RunID) != strings.TrimSpace(envelope.RunID) ||
		strings.TrimSpace(run.RunContext.StrategyVersion) != envelope.MarketSummaryDecisionVersion() {
		return nil, errors.New("market summary V1.5 decision envelope identity mismatch")
	}
	if len(run.Candidates) != envelope.CandidateCount || len(run.Production) != envelope.ProductionCount ||
		strings.TrimSpace(run.NoTradeReason) != strings.TrimSpace(envelope.NoTradeReason) {
		return nil, errors.New("market summary V1.5 decision envelope payload mismatch")
	}
	return &run, nil
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
