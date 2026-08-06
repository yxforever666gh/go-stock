package service

import (
	"context"

	"go-stock/backend/models"
)

// MarketSummaryDecisionSnapshot is the consumer-owned boundary for publishing
// a frozen strategy decision. Compatibility adapters may accept their concrete
// snapshot type, but the use-case layer does not depend on backend/data.
type MarketSummaryDecisionSnapshot interface {
	MarketSummaryDecisionVersion() string
}

type RecommendService struct {
	operations RecommendOperations
}

func NewRecommendService(operations RecommendOperations) RecommendService {
	return RecommendService{operations: operations}
}

func (s RecommendService) RequireStrategyLive(ctx context.Context, strategyVersion string) error {
	return s.operations.RequireStrategyLive(ctx, strategyVersion)
}

func (s RecommendService) CreateAIResponseReport(ctx context.Context, result *models.AIResponseResult) error {
	return s.operations.CreateAIResponseReport(ctx, result)
}

func (s RecommendService) PersistAIResponseReport(ctx context.Context, result *models.AIResponseResult) error {
	return s.operations.PersistAIResponseReport(ctx, result)
}

func (s RecommendService) PersistMarketSummaryV150Decision(
	ctx context.Context,
	decision MarketSummaryDecisionSnapshot,
	providerName, modelName string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	return s.operations.PersistMarketSummaryV150Decision(ctx, decision, providerName, modelName)
}

func (s RecommendService) GetAIResponseResultList(query models.AIResponseResultQuery) (*models.AIResponseResultPageData, error) {
	return s.operations.GetAIResponseResultList(query)
}

func (s RecommendService) GetEmailSendLogList(query models.EmailSendLogQuery) (*models.EmailSendLogPageData, error) {
	return s.operations.GetEmailSendLogList(query)
}

func (s RecommendService) DeleteAIResponseResult(id uint) error {
	return s.operations.DeleteAIResponseResult(id)
}

func (s RecommendService) BatchDeleteAIResponseResult(ids []uint) error {
	return s.operations.BatchDeleteAIResponseResult(ids)
}

func (s RecommendService) GetAiRecommendStocksList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error) {
	return s.operations.GetAiRecommendStocksList(query)
}

func (s RecommendService) GetAiRecommendStocksDateRange() (string, string, error) {
	return s.operations.GetAiRecommendStocksDateRange()
}

func (s RecommendService) GetAiRecommendStocksYieldList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksYieldPageData, error) {
	return s.operations.GetAiRecommendStocksYieldList(query)
}

func (s RecommendService) GetAiRecommendYieldMinuteChart(recommendID uint) (*models.AiRecommendYieldMinuteChartData, error) {
	return s.operations.GetAiRecommendYieldMinuteChart(recommendID)
}

func (s RecommendService) GetAiRecommendYieldDailyOverview(query *models.AiRecommendStocksQuery) (*models.AiRecommendYieldDailyOverviewData, error) {
	return s.operations.GetAiRecommendYieldDailyOverview(query)
}

func (s RecommendService) StartAiRecommendMinuteDownload() (map[string]any, error) {
	return s.operations.StartAiRecommendMinuteDownload()
}

func (s RecommendService) GetAiRecommendYieldTaskStatus() (*models.AiRecommendStocksYieldPageData, error) {
	return s.operations.GetAiRecommendYieldTaskStatus()
}

func (s RecommendService) GetAiRecommendYieldErrorLogs(limit int) ([]map[string]string, error) {
	return s.operations.GetAiRecommendYieldErrorLogs(limit)
}

func (s RecommendService) GetMarketSummaryRunDiagnostics(query models.MarketSummaryRunDiagnosticQuery) (models.MarketSummaryRunDiagnosticSummary, error) {
	return s.operations.GetMarketSummaryRunDiagnostics(query)
}

func (s RecommendService) GetMarketSummaryEmptyRunCount(query models.MarketSummaryRunDiagnosticQuery) (int64, error) {
	return s.operations.GetMarketSummaryEmptyRunCount(query)
}

func (s RecommendService) GetMarketSummaryBlockedReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	return s.operations.GetMarketSummaryBlockedReasonTop(query)
}

func (s RecommendService) GetMarketSummaryProductionDowngradeReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	return s.operations.GetMarketSummaryProductionDowngradeReasonTop(query)
}

func (s RecommendService) DeleteAiRecommendStocks(id uint) error {
	return s.operations.DeleteAiRecommendStocks(id)
}
