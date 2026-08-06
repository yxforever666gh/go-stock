package service

import (
	"time"

	"go-stock/backend/models"

	"github.com/coocood/freecache"
)

type MarketService struct {
	operations MarketOperations
}

func NewMarketService(operations MarketOperations) MarketService {
	return MarketService{operations: operations}
}

func (s MarketService) AnalyzeNews(text string, save bool) {
	s.operations.AnalyzeNews(text, save)
}

func (s MarketService) EnsureMarketDataSelfCheck(reason string) {
	s.operations.EnsureMarketDataSelfCheck(reason)
}

func (s MarketService) NormalizeYieldEmailCronTimes(input string) ([]string, error) {
	return s.operations.NormalizeYieldEmailCronTimes(input)
}

func (s MarketService) IsCNOpenTradeDay(day time.Time) bool {
	return s.operations.IsCNOpenTradeDay(day)
}

func (s MarketService) IsCNOpenTradeDayStrict(day time.Time) (bool, error) {
	return s.operations.IsCNOpenTradeDayStrict(day)
}

func (s MarketService) LongTigerRank(date string) *[]models.LongTigerRankData {
	return s.operations.LongTigerRank(date)
}

func (s MarketService) StockResearchReport(stockCode string, days int) []any {
	return s.operations.StockResearchReport(stockCode, days)
}

func (s MarketService) StockNotice(stockCode string) []any {
	return s.operations.StockNotice(stockCode)
}

func (s MarketService) IndustryResearchReport(industryCode string, days int) []any {
	return s.operations.IndustryResearchReport(industryCode, days)
}

func (s MarketService) EMDictCode(code string, cache *freecache.Cache) []any {
	return s.operations.EMDictCode(code, cache)
}

func (s MarketService) HotStock(marketType string, size int) *[]models.HotItem {
	return s.operations.HotStock(marketType, size)
}

func (s MarketService) HotEvent(size int) *[]models.HotEvent {
	return s.operations.HotEvent(size)
}

func (s MarketService) HotTopic(size int) []any {
	return s.operations.HotTopic(size)
}

func (s MarketService) InvestCalendar(yearMonth string) []any {
	return s.operations.InvestCalendar(yearMonth)
}

func (s MarketService) ClsCalendar() []any {
	return s.operations.ClsCalendar()
}

func (s MarketService) GetTelegraphList(source string) *[]*models.Telegraph {
	return s.operations.GetTelegraphList(source)
}

func (s MarketService) TelegraphList(timeout int64) *[]models.Telegraph {
	return s.operations.TelegraphList(timeout)
}

func (s MarketService) GetSinaNews(timeout uint) *[]models.Telegraph {
	return s.operations.GetSinaNews(timeout)
}

func (s MarketService) TradingViewNews() *[]models.Telegraph {
	return s.operations.TradingViewNews()
}

func (s MarketService) RefreshTelegraphList(source string) *[]*models.Telegraph {
	go s.operations.TelegraphList(30)
	go s.operations.GetSinaNews(30)
	go s.operations.TradingViewNews()
	return s.operations.GetTelegraphList(source)
}

func (s MarketService) GlobalStockIndexes() map[string]any {
	return s.operations.GlobalStockIndexes()
}

func (s MarketService) GetIndustryRank(sort string, count int) []any {
	return s.operations.GetIndustryRank(sort, count)
}

func (s MarketService) GetIndustryMoneyRankSina(category, sort string) []map[string]any {
	return s.operations.GetIndustryMoneyRankSina(category, sort)
}

func (s MarketService) GetMoneyRankSina(sort string) []map[string]any {
	return s.operations.GetMoneyRankSina(sort)
}

func (s MarketService) GetStockMoneyTrendByDay(stockCode string, days int) []map[string]any {
	return s.operations.GetStockMoneyTrendByDay(stockCode, days)
}
