package agent

import (
	"go-stock/backend/agent/tools"
	"go-stock/backend/models"
)

type fakeAgentToolDataProvider struct{}

var _ tools.ToolDataProvider = fakeAgentToolDataProvider{}

func (fakeAgentToolDataProvider) SearchStocks(string) []models.StockBasic { return nil }
func (fakeAgentToolDataProvider) BoardDictionary() []any                  { return nil }
func (fakeAgentToolDataProvider) MarketCalendar() []any                   { return nil }
func (fakeAgentToolDataProvider) MarketNews(string, int) []*models.Telegraph {
	return nil
}
func (fakeAgentToolDataProvider) TradingViewNews() []models.Telegraph { return nil }
func (fakeAgentToolDataProvider) ReutersNews() *models.ReutersNews    { return nil }
