package main

import "go-stock/backend/models"

func (a *App) LongTigerRank(date string) *[]models.LongTigerRankData {
	return a.services.Market.LongTigerRank(date)
}

func (a *App) StockResearchReport(stockCode string) []any {
	return a.services.Market.StockResearchReport(stockCode, 7)
}

func (a *App) StockNotice(stockCode string) []any {
	return a.services.Market.StockNotice(stockCode)
}

func (a *App) IndustryResearchReport(industryCode string) []any {
	return a.services.Market.IndustryResearchReport(industryCode, 7)
}

func (a *App) EMDictCode(code string) []any {
	return a.services.Market.EMDictCode(code, a.cache)
}

func (a *App) AnalyzeSentiment(text string) models.SentimentResult {
	return a.services.AI.AnalyzeSentiment(text)
}

func (a *App) HotStock(marketType string) *[]models.HotItem {
	return a.services.Market.HotStock(marketType, 100)
}

func (a *App) HotEvent(size int) *[]models.HotEvent {
	if size <= 0 {
		size = 10
	}
	return a.services.Market.HotEvent(size)
}

func (a *App) HotTopic(size int) []any {
	if size <= 0 {
		size = 10
	}
	return a.services.Market.HotTopic(size)
}

func (a *App) InvestCalendarTimeLine(yearMonth string) []any {
	return a.services.Market.InvestCalendar(yearMonth)
}

func (a *App) ClsCalendar() []any {
	return a.services.Market.ClsCalendar()
}

func (a *App) SearchStock(words string) map[string]any {
	return a.services.Stock.SearchStock(words)
}

func (a *App) GetHotStrategy() map[string]any {
	return a.services.Stock.GetHotStrategy()
}
