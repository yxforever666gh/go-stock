package service

import (
	"go-stock/backend/data"
	"go-stock/backend/models"
)

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

func (s RecommendService) StartAiRecommendMinuteDownload() (map[string]any, error) {
	return data.NewAiRecommendStocksService().StartAiRecommendMinuteDownload()
}

func (s RecommendService) GetAiRecommendYieldErrorLogs(limit int) ([]map[string]string, error) {
	return data.NewAiRecommendStocksService().GetAiRecommendYieldErrorLogs(limit)
}

func (s RecommendService) DeleteAiRecommendStocks(id uint) error {
	return data.NewAiRecommendStocksService().DeleteAiRecommendStocks(id)
}
