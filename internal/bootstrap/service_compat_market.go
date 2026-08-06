package bootstrap

import (
	"go-stock/backend/data"
	"go-stock/backend/models"

	"github.com/coocood/freecache"
)

func (*compatibilityServiceAdapter) AnalyzeNews(text string, save bool) {
	data.NewsAnalyze(text, save)
}

func (*compatibilityServiceAdapter) EnsureMarketDataSelfCheck(reason string) {
	data.EnsureDiemengSelfCheckAsync(reason)
}

func (*compatibilityServiceAdapter) GetFundList(key string) []models.FundBasic {
	return data.NewFundApi().GetFundList(key)
}

func (*compatibilityServiceAdapter) GetFollowedFund() []models.FollowedFund {
	return data.NewFundApi().GetFollowedFund()
}

func (*compatibilityServiceAdapter) FollowFund(code string) string {
	return data.NewFundApi().FollowFund(code)
}

func (*compatibilityServiceAdapter) UnFollowFund(code string) string {
	return data.NewFundApi().UnFollowFund(code)
}

func (*compatibilityServiceAdapter) AllFund() {
	data.NewFundApi().AllFund()
}

func (*compatibilityServiceAdapter) CrawlFundBasic(code string) (*models.FundBasic, error) {
	return data.NewFundApi().CrawlFundBasic(code)
}

func (*compatibilityServiceAdapter) CrawlFundNetEstimatedUnit(code string) {
	data.NewFundApi().CrawlFundNetEstimatedUnit(code)
}

func (*compatibilityServiceAdapter) CrawlFundNetUnitValue(code string) {
	data.NewFundApi().CrawlFundNetUnitValue(code)
}

func (*compatibilityServiceAdapter) LongTigerRank(date string) *[]models.LongTigerRankData {
	return data.NewMarketNewsApi().LongTiger(date)
}

func (*compatibilityServiceAdapter) StockResearchReport(code string, days int) []any {
	return data.NewMarketNewsApi().StockResearchReport(code, days)
}

func (*compatibilityServiceAdapter) StockNotice(code string) []any {
	return data.NewMarketNewsApi().StockNotice(code)
}

func (*compatibilityServiceAdapter) IndustryResearchReport(code string, days int) []any {
	return data.NewMarketNewsApi().IndustryResearchReport(code, days)
}

func (*compatibilityServiceAdapter) EMDictCode(code string, cache *freecache.Cache) []any {
	return data.NewMarketNewsApi().EMDictCode(code, cache)
}

func (*compatibilityServiceAdapter) HotStock(marketType string, size int) *[]models.HotItem {
	return data.NewMarketNewsApi().XUEQIUHotStock(size, marketType)
}

func (*compatibilityServiceAdapter) HotEvent(size int) *[]models.HotEvent {
	return data.NewMarketNewsApi().HotEvent(size)
}

func (*compatibilityServiceAdapter) HotTopic(size int) []any {
	return data.NewMarketNewsApi().HotTopic(size)
}

func (*compatibilityServiceAdapter) InvestCalendar(yearMonth string) []any {
	return data.NewMarketNewsApi().InvestCalendar(yearMonth)
}

func (*compatibilityServiceAdapter) ClsCalendar() []any {
	return data.NewMarketNewsApi().ClsCalendar()
}

func (*compatibilityServiceAdapter) GetTelegraphList(source string) *[]*models.Telegraph {
	return data.NewMarketNewsApi().GetTelegraphList(source)
}

func (*compatibilityServiceAdapter) TelegraphList(timeout int64) *[]models.Telegraph {
	return data.NewMarketNewsApi().TelegraphList(timeout)
}

func (*compatibilityServiceAdapter) GetSinaNews(timeout uint) *[]models.Telegraph {
	return data.NewMarketNewsApi().GetSinaNews(timeout)
}

func (*compatibilityServiceAdapter) TradingViewNews() *[]models.Telegraph {
	return data.NewMarketNewsApi().TradingViewNews()
}

func (*compatibilityServiceAdapter) GlobalStockIndexes() map[string]any {
	return data.NewMarketNewsApi().GlobalStockIndexes(30)
}

func (*compatibilityServiceAdapter) GetIndustryRank(sort string, count int) []any {
	result := data.NewMarketNewsApi().GetIndustryRank(sort, count)
	if items, ok := result["data"].([]any); ok && items != nil {
		return items
	}
	return []any{}
}

func (*compatibilityServiceAdapter) GetIndustryMoneyRankSina(category, sort string) []map[string]any {
	return data.NewMarketNewsApi().GetIndustryMoneyRankSina(category, sort)
}

func (*compatibilityServiceAdapter) GetMoneyRankSina(sort string) []map[string]any {
	return data.NewMarketNewsApi().GetMoneyRankSina(sort)
}

func (*compatibilityServiceAdapter) GetStockMoneyTrendByDay(code string, days int) []map[string]any {
	return data.NewMarketNewsApi().GetStockMoneyTrendByDay(code, days)
}

func (*compatibilityServiceAdapter) SendDingDingMessage(message string) string {
	return data.NewDingDingAPI().SendDingDingMessage(message)
}

func (*compatibilityServiceAdapter) SendAlert(title, subtitle, content, icon string) {
	data.NewAlertWindowsApi(title, subtitle, content, icon).SendNotification()
}
