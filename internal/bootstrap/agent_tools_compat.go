package bootstrap

import (
	"go-stock/backend/agent/tools"
	"go-stock/backend/data"
	"go-stock/backend/models"

	"github.com/coocood/freecache"
)

// compatibilityAgentToolDataProvider keeps legacy provider construction in
// the application composition root while agent tools migrate away from
// backend/data one tool at a time.
type compatibilityAgentToolDataProvider struct{}

var _ tools.ToolDataProvider = compatibilityAgentToolDataProvider{}

// NewProductionAgentToolDataProvider assembles the legacy-backed provider for
// the local production application. Callers must pass it explicitly into the
// App and agent constructors.
func NewProductionAgentToolDataProvider() tools.ToolDataProvider {
	return compatibilityAgentToolDataProvider{}
}

func (compatibilityAgentToolDataProvider) SearchStocks(searchWord string) []models.StockBasic {
	return data.NewStockDataApi().GetStockList(searchWord)
}

func (compatibilityAgentToolDataProvider) BoardDictionary() []any {
	return data.NewMarketNewsApi().EMDictCode("016", freecache.NewCache(100))
}

func (compatibilityAgentToolDataProvider) MarketCalendar() []any {
	return data.NewMarketNewsApi().ClsCalendar()
}

func (compatibilityAgentToolDataProvider) MarketNews(source string, limit int) []*models.Telegraph {
	result := data.NewMarketNewsApi().GetNewsList(source, limit)
	if result == nil {
		return nil
	}
	return *result
}

func (compatibilityAgentToolDataProvider) TradingViewNews() []models.Telegraph {
	result := data.NewMarketNewsApi().TradingViewNews()
	if result == nil {
		return nil
	}
	return *result
}

func (compatibilityAgentToolDataProvider) ReutersNews() *models.ReutersNews {
	return data.NewMarketNewsApi().ReutersNew()
}

func (compatibilityAgentToolDataProvider) SearchStocksByIndicators(words string, pageSize int) map[string]any {
	return data.NewSearchStockApi(words).SearchStock(pageSize)
}

func (compatibilityAgentToolDataProvider) GDP() *models.GDPResp {
	return data.NewMarketNewsApi().GetGDP()
}
func (compatibilityAgentToolDataProvider) CPI() *models.CPIResp {
	return data.NewMarketNewsApi().GetCPI()
}
func (compatibilityAgentToolDataProvider) PPI() *models.PPIResp {
	return data.NewMarketNewsApi().GetPPI()
}
func (compatibilityAgentToolDataProvider) PMI() *models.PMIResp {
	return data.NewMarketNewsApi().GetPMI()
}

func (compatibilityAgentToolDataProvider) FinancialReports(stockCode string, timeoutSeconds int64) []string {
	result := data.GetFinancialReportsByXUEQIU(stockCode, timeoutSeconds)
	if result == nil {
		return nil
	}
	return *result
}

func (compatibilityAgentToolDataProvider) IndustryResearchReports(industryCode string, days int) []any {
	return data.NewMarketNewsApi().IndustryResearchReport(industryCode, days)
}

func (compatibilityAgentToolDataProvider) IndustryReportInfo(infoCode string) string {
	return data.NewMarketNewsApi().GetIndustryReportInfo(infoCode)
}

func (compatibilityAgentToolDataProvider) InteractiveAnswers(page, pageSize int, keyword string) *models.InteractiveAnswer {
	return data.NewMarketNewsApi().InteractiveAnswer(page, pageSize, keyword)
}

func (compatibilityAgentToolDataProvider) KLines(stockCode, interval string, days int64) []models.KLineData {
	result := data.NewStockDataApi().GetKLineData(stockCode, interval, days)
	if result == nil {
		return nil
	}
	return *result
}

func (compatibilityAgentToolDataProvider) OverseasKLines(stockCode, interval string, days int64) []models.KLineData {
	result := data.NewStockDataApi().GetHK_KLineData(stockCode, interval, days)
	if result == nil {
		return nil
	}
	return *result
}

func (compatibilityAgentToolDataProvider) StockNews(searchWords string) *models.CailianpressWeb {
	return data.NewMarketNewsApi().CailianpressWeb(searchWords)
}

func (compatibilityAgentToolDataProvider) Quotes(stockCodes ...string) ([]models.StockInfo, error) {
	result, err := data.NewStockDataApi().GetStockCodeRealTimeData(stockCodes...)
	if result == nil {
		return nil, err
	}
	return *result, err
}
