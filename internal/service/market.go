package service

import (
	"go-stock/backend/data"
	"go-stock/backend/models"

	"github.com/coocood/freecache"
)

type MarketService struct{}

func NewMarketService() MarketService {
	return MarketService{}
}

func (s MarketService) LongTigerRank(date string) *[]models.LongTigerRankData {
	return data.NewMarketNewsApi().LongTiger(date)
}

func (s MarketService) StockResearchReport(stockCode string, days int) []any {
	return data.NewMarketNewsApi().StockResearchReport(stockCode, days)
}

func (s MarketService) StockNotice(stockCode string) []any {
	return data.NewMarketNewsApi().StockNotice(stockCode)
}

func (s MarketService) IndustryResearchReport(industryCode string, days int) []any {
	return data.NewMarketNewsApi().IndustryResearchReport(industryCode, days)
}

func (s MarketService) EMDictCode(code string, cache *freecache.Cache) []any {
	return data.NewMarketNewsApi().EMDictCode(code, cache)
}

func (s MarketService) HotStock(marketType string, size int) *[]models.HotItem {
	return data.NewMarketNewsApi().XUEQIUHotStock(size, marketType)
}

func (s MarketService) HotEvent(size int) *[]models.HotEvent {
	return data.NewMarketNewsApi().HotEvent(size)
}

func (s MarketService) HotTopic(size int) []any {
	return data.NewMarketNewsApi().HotTopic(size)
}

func (s MarketService) InvestCalendar(yearMonth string) []any {
	return data.NewMarketNewsApi().InvestCalendar(yearMonth)
}

func (s MarketService) ClsCalendar() []any {
	return data.NewMarketNewsApi().ClsCalendar()
}

func (s MarketService) GetTelegraphList(source string) *[]*models.Telegraph {
	return data.NewMarketNewsApi().GetTelegraphList(source)
}

func (s MarketService) TelegraphList(timeout int64) *[]models.Telegraph {
	return data.NewMarketNewsApi().TelegraphList(timeout)
}

func (s MarketService) GetSinaNews(timeout uint) *[]models.Telegraph {
	return data.NewMarketNewsApi().GetSinaNews(timeout)
}

func (s MarketService) TradingViewNews() *[]models.Telegraph {
	return data.NewMarketNewsApi().TradingViewNews()
}

func (s MarketService) RefreshTelegraphList(source string) *[]*models.Telegraph {
	go data.NewMarketNewsApi().TelegraphList(30)
	go data.NewMarketNewsApi().GetSinaNews(30)
	go data.NewMarketNewsApi().TradingViewNews()
	return data.NewMarketNewsApi().GetTelegraphList(source)
}

func (s MarketService) GlobalStockIndexes() map[string]any {
	return data.NewMarketNewsApi().GlobalStockIndexes(30)
}

func (s MarketService) GetIndustryRank(sort string, count int) []any {
	res := data.NewMarketNewsApi().GetIndustryRank(sort, count)
	if items, ok := res["data"].([]any); ok && items != nil {
		return items
	}
	return []any{}
}

func (s MarketService) GetIndustryMoneyRankSina(category, sort string) []map[string]any {
	return data.NewMarketNewsApi().GetIndustryMoneyRankSina(category, sort)
}

func (s MarketService) GetMoneyRankSina(sort string) []map[string]any {
	return data.NewMarketNewsApi().GetMoneyRankSina(sort)
}

func (s MarketService) GetStockMoneyTrendByDay(stockCode string, days int) []map[string]any {
	return data.NewMarketNewsApi().GetStockMoneyTrendByDay(stockCode, days)
}
