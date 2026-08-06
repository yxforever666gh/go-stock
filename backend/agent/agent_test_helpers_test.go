package agent

import (
	"go-stock/backend/agent/tools"
	"go-stock/backend/models"
)

type fakeAgentToolDataProvider struct{}

type fakeAgentConfigurationProvider struct {
	configs []*models.AIConfig
	prompt  string
}

func (p fakeAgentConfigurationProvider) AIConfigs() []*models.AIConfig { return p.configs }
func (p fakeAgentConfigurationProvider) PromptTemplateByID(int) string { return p.prompt }

var _ tools.ToolDataProvider = fakeAgentToolDataProvider{}

func (fakeAgentToolDataProvider) SearchStocks(string) []models.StockBasic { return nil }
func (fakeAgentToolDataProvider) BoardDictionary() []any                  { return nil }
func (fakeAgentToolDataProvider) MarketCalendar() []any                   { return nil }
func (fakeAgentToolDataProvider) MarketNews(string, int) []*models.Telegraph {
	return nil
}
func (fakeAgentToolDataProvider) TradingViewNews() []models.Telegraph { return nil }
func (fakeAgentToolDataProvider) ReutersNews() *models.ReutersNews    { return nil }
func (fakeAgentToolDataProvider) SearchStocksByIndicators(string, int) map[string]any {
	return nil
}
func (fakeAgentToolDataProvider) GDP() *models.GDPResp                      { return nil }
func (fakeAgentToolDataProvider) CPI() *models.CPIResp                      { return nil }
func (fakeAgentToolDataProvider) PPI() *models.PPIResp                      { return nil }
func (fakeAgentToolDataProvider) PMI() *models.PMIResp                      { return nil }
func (fakeAgentToolDataProvider) FinancialReports(string, int64) []string   { return nil }
func (fakeAgentToolDataProvider) IndustryResearchReports(string, int) []any { return nil }
func (fakeAgentToolDataProvider) IndustryReportInfo(string) string          { return "" }
func (fakeAgentToolDataProvider) InteractiveAnswers(int, int, string) *models.InteractiveAnswer {
	return nil
}
func (fakeAgentToolDataProvider) KLines(string, string, int64) []models.KLineData { return nil }
func (fakeAgentToolDataProvider) OverseasKLines(string, string, int64) []models.KLineData {
	return nil
}
func (fakeAgentToolDataProvider) StockNews(string) *models.CailianpressWeb     { return nil }
func (fakeAgentToolDataProvider) Quotes(...string) ([]models.StockInfo, error) { return nil, nil }
