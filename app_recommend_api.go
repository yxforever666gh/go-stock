package main

import (
	"time"

	"go-stock/backend/logger"
	"go-stock/backend/models"
)

func (a *App) AnalyzeSentimentWithFreqWeight(text string) map[string]any {
	return a.services.AI.AnalyzeSentimentWithFreqWeight(text)
}

func (a *App) GetAIResponseResultList(query models.AIResponseResultQuery) *models.AIResponseResultPageData {
	page, err := a.services.Recommend.GetAIResponseResultList(query)
	if err != nil {
		return &models.AIResponseResultPageData{}
	}
	return page
}

func (a *App) GetEmailSendLogList(query models.EmailSendLogQuery) *models.EmailSendLogPageData {
	page, err := a.services.Recommend.GetEmailSendLogList(query)
	if err != nil {
		return &models.EmailSendLogPageData{}
	}
	return page
}

func (a *App) DeleteAIResponseResult(id uint) string {
	err := a.services.Recommend.DeleteAIResponseResult(id)
	if err != nil {
		return "删除失败"
	}
	return "删除成功"
}

func (a *App) BatchDeleteAIResponseResult(ids []uint) string {
	err := a.services.Recommend.BatchDeleteAIResponseResult(ids)
	if err != nil {
		return "删除失败"
	}
	return "删除成功"
}

func (a *App) GetAiRecommendStocksList(query models.AiRecommendStocksQuery) *models.AiRecommendStocksPageData {
	page, err := a.services.Recommend.GetAiRecommendStocksList(&query)
	if err != nil {
		logger.SugaredLogger.Errorf("GetAiRecommendStocksList failed: cohort=%q err=%v", query.StrategyCohort, err)
		return &models.AiRecommendStocksPageData{}
	}
	return page
}

func (a *App) GetAiRecommendStocksDateRange() map[string]string {
	startDate, endDate, err := a.services.Recommend.GetAiRecommendStocksDateRange()
	if err != nil {
		return map[string]string{
			"startDate": "",
			"endDate":   "",
		}
	}
	return map[string]string{
		"startDate": startDate,
		"endDate":   endDate,
	}
}

func (a *App) GetAiRecommendStocksYieldList(query models.AiRecommendStocksQuery) *models.AiRecommendStocksYieldPageData {
	page, err := a.services.Recommend.GetAiRecommendStocksYieldList(&query)
	if err != nil {
		logger.SugaredLogger.Warnf("GetAiRecommendStocksYieldList failed: %v", err)
		return emptyAiRecommendStocksYieldPage(query)
	}
	if page == nil {
		return emptyAiRecommendStocksYieldPage(query)
	}
	normalizeAiRecommendStocksYieldPage(page, query)
	return page
}

func emptyAiRecommendStocksYieldPage(query models.AiRecommendStocksQuery) *models.AiRecommendStocksYieldPageData {
	page := &models.AiRecommendStocksYieldPageData{}
	normalizeAiRecommendStocksYieldPage(page, query)
	page.TotalYieldRateText = "--"
	page.BenchmarkRateText = "--"
	page.ExcessYieldRateText = "--"
	page.StrategyXirrText = "--"
	page.BenchmarkXirrText = "--"
	page.ExcessXirrText = "--"
	page.MaxDrawdownText = "--"
	page.WinRateVsBenchmarkText = "--"
	page.MedianExcessYieldRateText = "--"
	return page
}

func normalizeAiRecommendStocksYieldPage(page *models.AiRecommendStocksYieldPageData, query models.AiRecommendStocksQuery) {
	if page == nil {
		return
	}
	if page.List == nil {
		page.List = []models.AiRecommendStocksYieldItem{}
	}
	if page.Page <= 0 {
		page.Page = query.Page
	}
	if page.Page <= 0 {
		page.Page = 1
	}
	if page.PageSize <= 0 {
		page.PageSize = query.PageSize
	}
	if page.PageSize <= 0 || page.PageSize > 100 {
		page.PageSize = 100
	}
}

func (a *App) GetAiRecommendYieldMinuteChart(recommendID uint) *models.AiRecommendYieldMinuteChartData {
	data, err := a.services.Recommend.GetAiRecommendYieldMinuteChart(recommendID)
	if err != nil {
		return &models.AiRecommendYieldMinuteChartData{
			RecommendID: recommendID,
			ChartStatus: "missing",
			Message:     "读取分钟回放失败: " + err.Error(),
			Bars:        []models.AiRecommendYieldMinuteBarDTO{},
			Markers:     []models.AiRecommendYieldChartMarker{},
		}
	}
	return data
}

func (a *App) GetAiRecommendYieldDailyOverview(query models.AiRecommendStocksQuery) *models.AiRecommendYieldDailyOverviewData {
	data, err := a.services.Recommend.GetAiRecommendYieldDailyOverview(&query)
	if err != nil {
		return &models.AiRecommendYieldDailyOverviewData{
			CalcMode:       "strict",
			StrategyCohort: query.StrategyCohort,
			Warnings:       []string{"读取全库收益走势失败: " + err.Error()},
			Points:         []models.AiRecommendYieldDailyOverviewPoint{},
		}
	}
	return data
}

func (a *App) StartAiRecommendMinuteDownload() map[string]any {
	resp, err := a.services.Recommend.StartAiRecommendMinuteDownload()
	if err != nil {
		return map[string]any{
			"accepted": false,
			"message":  "触发失败: " + err.Error(),
		}
	}
	return resp
}

func (a *App) GetAiRecommendYieldTaskStatus() *models.AiRecommendStocksYieldPageData {
	page, err := a.services.Recommend.GetAiRecommendYieldTaskStatus()
	if err != nil {
		logger.SugaredLogger.Warnf("GetAiRecommendYieldTaskStatus failed: %v", err)
		return emptyAiRecommendStocksYieldPage(models.AiRecommendStocksQuery{})
	}
	if page == nil {
		return emptyAiRecommendStocksYieldPage(models.AiRecommendStocksQuery{})
	}
	normalizeAiRecommendStocksYieldPage(page, models.AiRecommendStocksQuery{})
	return page
}

func (a *App) GetAiRecommendYieldErrorLogs(limit int) []map[string]string {
	resp, err := a.services.Recommend.GetAiRecommendYieldErrorLogs(limit)
	if err != nil {
		return []map[string]string{
			{
				"time":      time.Now().Format("2006-01-02 15:04:05"),
				"source":    "系统",
				"stockCode": "",
				"stockName": "",
				"status":    "接口错误",
				"reason":    "读取报错日志失败，请稍后重试",
				"rawReason": err.Error(),
			},
		}
	}
	return resp
}

func (a *App) GetMarketSummaryRunDiagnostics(query models.MarketSummaryRunDiagnosticQuery) models.MarketSummaryRunDiagnosticSummary {
	resp, err := a.services.Recommend.GetMarketSummaryRunDiagnostics(query)
	if err != nil {
		logger.SugaredLogger.Warnf("GetMarketSummaryRunDiagnostics failed: %v", err)
		return models.MarketSummaryRunDiagnosticSummary{
			List:                         []models.MarketSummaryRunDiagnostic{},
			BlockedReasonTop:             []models.MarketSummaryBlockedReasonItem{},
			ProductionDowngradeReasonTop: []models.MarketSummaryBlockedReasonItem{},
			StrategyCohort:               query.StrategyCohort,
			SummaryVersion:               query.SummaryVersion,
		}
	}
	if resp.List == nil {
		resp.List = []models.MarketSummaryRunDiagnostic{}
	}
	if resp.BlockedReasonTop == nil {
		resp.BlockedReasonTop = []models.MarketSummaryBlockedReasonItem{}
	}
	if resp.ProductionDowngradeReasonTop == nil {
		resp.ProductionDowngradeReasonTop = []models.MarketSummaryBlockedReasonItem{}
	}
	return resp
}

func (a *App) GetMarketSummaryEmptyRunCount(query models.MarketSummaryRunDiagnosticQuery) int64 {
	count, err := a.services.Recommend.GetMarketSummaryEmptyRunCount(query)
	if err != nil {
		logger.SugaredLogger.Warnf("GetMarketSummaryEmptyRunCount failed: %v", err)
		return 0
	}
	return count
}

func (a *App) GetMarketSummaryBlockedReasonTop(query models.MarketSummaryRunDiagnosticQuery) []models.MarketSummaryBlockedReasonItem {
	items, err := a.services.Recommend.GetMarketSummaryBlockedReasonTop(query)
	if err != nil {
		logger.SugaredLogger.Warnf("GetMarketSummaryBlockedReasonTop failed: %v", err)
		return []models.MarketSummaryBlockedReasonItem{}
	}
	if items == nil {
		return []models.MarketSummaryBlockedReasonItem{}
	}
	return items
}

func (a *App) GetMarketSummaryProductionDowngradeReasonTop(query models.MarketSummaryRunDiagnosticQuery) []models.MarketSummaryBlockedReasonItem {
	items, err := a.services.Recommend.GetMarketSummaryProductionDowngradeReasonTop(query)
	if err != nil {
		logger.SugaredLogger.Warnf("GetMarketSummaryProductionDowngradeReasonTop failed: %v", err)
		return []models.MarketSummaryBlockedReasonItem{}
	}
	if items == nil {
		return []models.MarketSummaryBlockedReasonItem{}
	}
	return items
}

func (a *App) DeleteAiRecommendStocks(id uint) string {
	err := a.services.Recommend.DeleteAiRecommendStocks(id)
	if err != nil {
		return "删除失败"
	}
	return "删除成功"
}
