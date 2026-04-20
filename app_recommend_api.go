package main

import (
	"time"

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
		return &models.AiRecommendStocksYieldPageData{}
	}
	return page
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

func (a *App) DeleteAiRecommendStocks(id uint) string {
	err := a.services.Recommend.DeleteAiRecommendStocks(id)
	if err != nil {
		return "删除失败"
	}
	return "删除成功"
}
