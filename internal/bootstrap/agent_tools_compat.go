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
