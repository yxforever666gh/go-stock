package main

import (
	"context"
	"time"

	"go-stock/backend/ai"
	"go-stock/backend/data"
	"go-stock/backend/funds"
	"go-stock/backend/models"

	"github.com/coocood/freecache"
)

type productionMarketAuditProvider struct{}

var _ marketAuditProvider = (*productionMarketAuditProvider)(nil)

func newProductionMarketAuditProvider() marketAuditProvider {
	return &productionMarketAuditProvider{}
}

func (*productionMarketAuditProvider) Settings() *models.SettingConfig {
	return data.GetSettingConfig()
}

func (*productionMarketAuditProvider) News() marketAuditNews {
	return &marketAuditNewsProvider{MarketNewsApi: data.NewMarketNewsApi()}
}

func (*productionMarketAuditProvider) Search(words, fingerprint string) marketAuditSearch {
	return data.NewSearchStockApiWithFingerprint(words, fingerprint)
}

func (*productionMarketAuditProvider) Stock() marketAuditStock {
	return &marketAuditStockProvider{StockDataApi: data.NewStockDataApi()}
}

func (*productionMarketAuditProvider) Fund() marketAuditFund {
	return funds.NewProductionService()
}

func (*productionMarketAuditProvider) Tushare(config *models.SettingConfig) marketAuditTushare {
	return &marketAuditTushareProvider{TushareApi: data.NewTushareApi(config)}
}

func (*productionMarketAuditProvider) MarketNewsFetchMeta(source string) map[string]any {
	return data.GetMarketNewsFetchMeta(source)
}

func (*productionMarketAuditProvider) TopNewsList(timeoutSeconds int64) *[]string {
	return data.GetTopNewsList(timeoutSeconds)
}

func (*productionMarketAuditProvider) RealTimeStockPriceInfo(ctx context.Context, stockCode string) (string, string) {
	return data.GetRealTimeStockPriceInfo(ctx, stockCode)
}

func (*productionMarketAuditProvider) SearchStockPriceInfo(name, code string, timeoutSeconds int64) *[]string {
	return data.SearchStockPriceInfo(name, code, timeoutSeconds)
}

func (*productionMarketAuditProvider) SearchGuShiTongStockInfo(code string, timeoutSeconds int64) *[]string {
	return data.SearchGuShiTongStockInfo(code, timeoutSeconds)
}

func (*productionMarketAuditProvider) FinancialReportsByXueqiu(code string, timeoutSeconds int64) *[]string {
	return data.GetFinancialReportsByXUEQIU(code, timeoutSeconds)
}

func (*productionMarketAuditProvider) FinancialReports(code string, timeoutSeconds int64) *[]string {
	return data.GetFinancialReports(code, timeoutSeconds)
}

func (*productionMarketAuditProvider) DiemengBaseURL() string {
	return data.DiemengEffectiveBaseURLForDisplay()
}

func (*productionMarketAuditProvider) WaitDiemengSelfCheck(reason string, timeout time.Duration) (diemengSelfCheckSnapshot, error) {
	snapshot, err := data.WaitDiemengSelfCheck(reason, timeout)
	return diemengSelfCheckSnapshot{Status: snapshot.Status, Summary: snapshot.Summary, CheckedAt: snapshot.CheckedAt, ProbeCount: len(snapshot.Probes)}, err
}

func (*productionMarketAuditProvider) AuditDiemengMinuteBars(code string, start, end time.Time) (*minuteProviderAuditResult, error) {
	result, err := data.AuditDiemengMinuteBars(code, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func (*productionMarketAuditProvider) AuditAkShareMinuteBars(code string, start, end time.Time) (*minuteProviderAuditResult, error) {
	result, err := data.AuditAkShareMinuteBars(code, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func (*productionMarketAuditProvider) AuditSinaMinuteBars(code string, start, end time.Time) (*minuteProviderAuditResult, error) {
	result, err := data.AuditSinaMinuteBars(code, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func (*productionMarketAuditProvider) AuditTencentMinuteBars(code string, start, end time.Time) (*minuteProviderAuditResult, error) {
	result, err := data.AuditTencentMinuteBars(code, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func mapMinuteProviderAuditResult(result *data.MinuteProviderAuditResult) *minuteProviderAuditResult {
	if result == nil {
		return nil
	}
	return &minuteProviderAuditResult{Provider: result.Provider, Source: result.Source, Bars: result.Bars,
		FirstTradeTime: result.FirstTradeTime, LastTradeTime: result.LastTradeTime}
}

func (*productionMarketAuditProvider) DetectAIProviderName(config *models.AIConfig) string {
	if config == nil {
		return ""
	}
	return ai.DetectProviderName(config.Name, config.BaseUrl, config.ModelName)
}

func (*productionMarketAuditProvider) CompleteChat(ctx context.Context, config *models.AIConfig, messages []map[string]any, think bool) (string, string, string, error) {
	return data.NewOpenAiWithConfig(ctx, config).CompleteChat(messages, think)
}

func (*productionMarketAuditProvider) SendDingDingMessage(message string) string {
	return data.NewDingDingAPI().SendDingDingMessage(message)
}

type marketAuditNewsProvider struct {
	*data.MarketNewsApi
}

func (p *marketAuditNewsProvider) EMDictCode(code string) []any {
	return p.MarketNewsApi.EMDictCode(code, freecache.NewCache(1024*1024))
}

type marketAuditStockProvider struct {
	*data.StockDataApi
}

func (p *marketAuditStockProvider) GetStockCodeRealTimeData(codes ...string) (*[]models.StockInfo, error) {
	return p.StockDataApi.GetStockCodeRealTimeDataReadOnly(context.Background(), codes...)
}

func (p *marketAuditStockProvider) GetStockMinutePriceData(code string) (*[]minutePrice, string) {
	items, date := p.StockDataApi.GetStockMinutePriceData(code)
	if items == nil {
		return nil, date
	}
	result := make([]minutePrice, 0, len(*items))
	for _, item := range *items {
		result = append(result, minutePrice{Time: item.Time, Price: item.Price, Volume: item.Volume, Amount: item.Amount})
	}
	return &result, date
}

type marketAuditTushareProvider struct {
	*data.TushareApi
}

func (p *marketAuditTushareProvider) GetStockMinuteBars(code string, start, end time.Time, timeoutSeconds int64) ([]tushareMinuteBar, error) {
	items, err := p.TushareApi.GetStockMinuteBars(code, start, end, timeoutSeconds)
	if err != nil {
		return nil, err
	}
	result := make([]tushareMinuteBar, 0, len(items))
	for _, item := range items {
		result = append(result, tushareMinuteBar{TradeTime: item.TradeTime, Open: item.Open, High: item.High, Low: item.Low,
			Close: item.Close, Volume: item.Vol, Amount: item.Amount})
	}
	return result, nil
}
