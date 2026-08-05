package service

import (
	"time"

	"go-stock/backend/data"
	"go-stock/backend/models"
)

type MarketSummaryActivationRepairResult struct {
	Scanned      int `json:"scanned"`
	Downgraded   int `json:"downgraded"`
	RuleUpgraded int `json:"ruleUpgraded"`
	SkippedNoRef int `json:"skippedNoRef"`
}

type RecommendService struct{}

func NewRecommendService() RecommendService {
	return RecommendService{}
}

func (s RecommendService) GetAIResponseResultList(query models.AIResponseResultQuery) (*models.AIResponseResultPageData, error) {
	return data.NewAIResponseResultService().GetAIResponseResultList(query)
}

func (s RecommendService) GetEmailSendLogList(query models.EmailSendLogQuery) (*models.EmailSendLogPageData, error) {
	return data.NewEmailSendLogService().GetEmailSendLogList(query)
}

func (s RecommendService) DeleteAIResponseResult(id uint) error {
	return data.NewAIResponseResultService().DeleteAIResponseResult(id)
}

func (s RecommendService) BatchDeleteAIResponseResult(ids []uint) error {
	return data.NewAIResponseResultService().BatchDeleteAIResponseResult(ids)
}

func (s RecommendService) GetAiRecommendStocksList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendStocksList(query)
}

func (s RecommendService) GetAiRecommendStocksDateRange() (string, string, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendStocksDateRange()
}

func (s RecommendService) GetAiRecommendStocksYieldList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksYieldPageData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendStocksYieldList(query)
}

func (s RecommendService) GetAiRecommendYieldMinuteChart(recommendID uint) (*models.AiRecommendYieldMinuteChartData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldMinuteChart(recommendID)
}

func (s RecommendService) GetAiRecommendYieldDailyOverview(query *models.AiRecommendStocksQuery) (*models.AiRecommendYieldDailyOverviewData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldDailyOverview(query)
}

func (s RecommendService) StartAiRecommendMinuteDownload() (map[string]any, error) {
	return data.NewAiRecommendStocksService().StartAiRecommendMinuteDownload()
}

func (s RecommendService) GetAiRecommendYieldTaskStatus() (*models.AiRecommendStocksYieldPageData, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldTaskStatus()
}

func (s RecommendService) GetAiRecommendYieldErrorLogs(limit int) ([]map[string]string, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldErrorLogs(limit)
}

func (s RecommendService) GetMarketSummaryRunDiagnostics(query models.MarketSummaryRunDiagnosticQuery) (models.MarketSummaryRunDiagnosticSummary, error) {
	return data.GetMarketSummaryRunDiagnostics(query)
}

func (s RecommendService) GetMarketSummaryEmptyRunCount(query models.MarketSummaryRunDiagnosticQuery) (int64, error) {
	return data.GetMarketSummaryEmptyRunCount(query)
}

func (s RecommendService) GetMarketSummaryBlockedReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	return data.GetMarketSummaryBlockedReasonTop(query)
}

func (s RecommendService) GetMarketSummaryProductionDowngradeReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	return data.GetMarketSummaryProductionDowngradeReasonTop(query)
}

func (s RecommendService) DeleteAiRecommendStocks(id uint) error {
	return data.NewAiRecommendStocksService().DeleteAiRecommendStocks(id)
}

func (s RecommendService) RepairHistoricalMarketSummaryActivationIssues(now time.Time) (MarketSummaryActivationRepairResult, error) {
	result, err := data.RepairHistoricalMarketSummaryActivationIssues(now)
	if err != nil {
		return MarketSummaryActivationRepairResult{}, err
	}
	return MarketSummaryActivationRepairResult{
		Scanned:      result.Scanned,
		Downgraded:   result.Downgraded,
		RuleUpgraded: result.RuleUpgraded,
		SkippedNoRef: result.SkippedNoRef,
	}, nil
}
