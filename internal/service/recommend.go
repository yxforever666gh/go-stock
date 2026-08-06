package service

import (
	"context"
	"strings"

	"go-stock/backend/models"
	"go-stock/backend/portfolio"
	"go-stock/backend/recommendation"
)

// MarketSummaryDecisionSnapshot is the consumer-owned boundary for publishing
// a frozen strategy decision. Compatibility adapters may accept their concrete
// snapshot type, but the use-case layer does not depend on backend/data.
type MarketSummaryDecisionSnapshot = recommendation.FrozenDecision

type RecommendService struct {
	operations             RecommendOperations
	production             recommendation.ProductionService[*models.MarketSummaryRecommendSaveResult]
	v150Producer           MarketSummaryV150Producer
	currentRecommendations portfolio.CurrentRecommendationReader
	clock                  Clock
	currentStrategyVersion string
}

func NewRecommendService(
	operations RecommendOperations,
	publisher recommendation.DecisionPublisher[*models.MarketSummaryRecommendSaveResult],
	currentRecommendations portfolio.CurrentRecommendationReader,
	clock Clock,
	currentStrategyVersion string,
	v150Producer ...MarketSummaryV150Producer,
) RecommendService {
	var producer MarketSummaryV150Producer
	if len(v150Producer) > 0 && !isNilMarketSummaryV150Producer(v150Producer[0]) {
		producer = v150Producer[0]
	}
	return RecommendService{
		operations:             operations,
		production:             recommendation.NewProductionService(publisher),
		v150Producer:           producer,
		currentRecommendations: currentRecommendations,
		clock:                  clock,
		currentStrategyVersion: strings.TrimSpace(currentStrategyVersion),
	}
}

func (s RecommendService) RunMarketSummaryV150(
	ctx context.Context,
	request MarketSummaryV150ProductionRequest,
) (*MarketSummaryV150ProductionResult, error) {
	if isNilMarketSummaryV150Producer(s.v150Producer) {
		return nil, ErrMarketSummaryV150ProducerUnavailable
	}
	if s.operations == nil {
		return nil, operationRequiredError("recommend")
	}
	// Keep the runtime-mode guard inside the use case as well as at delivery
	// entry points. Cron, CLI, tests, and future Web handlers therefore cannot
	// bypass paused mode by invoking the typed producer directly.
	if err := s.operations.RequireStrategyLive(ctx, s.currentStrategyVersion); err != nil {
		return nil, err
	}
	return s.v150Producer.Produce(ctx, request)
}

func (s RecommendService) RequireStrategyLive(ctx context.Context, strategyVersion string) error {
	return s.operations.RequireStrategyLive(ctx, strategyVersion)
}

func (s RecommendService) EncodeMarketSummaryBlockedReasons(items []models.MarketSummaryBlockedReasonItem) string {
	return s.operations.EncodeMarketSummaryBlockedReasons(items)
}

func (s RecommendService) SaveMarketSummaryRunDiagnostic(item *models.MarketSummaryRunDiagnostic) error {
	return s.operations.SaveMarketSummaryRunDiagnostic(item)
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
	return s.production.PublishDecision(ctx, decision, providerName, modelName)
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
