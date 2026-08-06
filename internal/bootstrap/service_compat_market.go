package bootstrap

import (
	"context"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/models"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/service"

	"github.com/coocood/freecache"
)

type legacyApplicationInitializer struct{}

func (legacyApplicationInitializer) EnsureSettings(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return data.EnsureSettingsRecord()
}

func (legacyApplicationInitializer) InitializeSentiment(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data.InitAnalyzeSentiment()
	return nil
}

// newCompatibilityServiceDependencies is the only place where legacy data
// constructors are assembled into application ports. Keeping this bridge in
// the bootstrap compatibility adapter makes the composition root explicit
// while the old package is being retired.
func newCompatibilityServiceDependencies(storage Storage) service.Dependencies {
	marketData := data.NewCompatibilityMarketDataReader(storage.Main, storage.Minute)
	return service.Dependencies{
		Clock:            systemClock{},
		Initializer:      legacyApplicationInitializer{},
		Operations:       newCompatibilityServiceOperations(storage.Main),
		ExecutionMonitor: data.NewCompatibilityExecutionMonitor(),
		Providers: service.ProviderSet{
			DailyBars:  marketData,
			MinuteBars: marketData,
			Quotes:     marketData,
			Securities: marketData,
			News:       data.NewCompatibilityNewsReader(),
			Ledger:     data.NewCompatibilityPortfolioLedger(storage.Main),
			Legacy:     data.NewCompatibilityLegacyRepository(storage.Main),
		},
	}
}

// marketAuditCompatibilityAdapter is the one legacy bridge used by the CLI
// network-audit command. The command depends on cliports contracts; this
// adapter is the only place where the old provider constructors are visible.
type marketAuditCompatibilityAdapter struct{}

var _ cliports.MarketAuditProvider = (*marketAuditCompatibilityAdapter)(nil)

func NewProductionMarketAuditProvider() cliports.MarketAuditProvider {
	return &marketAuditCompatibilityAdapter{}
}

func (*marketAuditCompatibilityAdapter) Settings() *models.SettingConfig {
	return data.GetSettingConfig()
}

func (*marketAuditCompatibilityAdapter) News() cliports.MarketAuditNews {
	return &marketAuditNewsCompatibilityAdapter{api: data.NewMarketNewsApi()}
}

func (*marketAuditCompatibilityAdapter) Search(words, fingerprint string) cliports.MarketAuditSearch {
	return &marketAuditSearchCompatibilityAdapter{api: data.NewSearchStockApiWithFingerprint(words, fingerprint)}
}

func (*marketAuditCompatibilityAdapter) Stock() cliports.MarketAuditStock {
	return &marketAuditStockCompatibilityAdapter{api: data.NewStockDataApi()}
}

func (*marketAuditCompatibilityAdapter) Fund() cliports.MarketAuditFund {
	return data.NewFundApi()
}

func (*marketAuditCompatibilityAdapter) Tushare(cfg *models.SettingConfig) cliports.MarketAuditTushare {
	return &marketAuditTushareCompatibilityAdapter{api: data.NewTushareApi(cfg)}
}

func (*marketAuditCompatibilityAdapter) MarketNewsFetchMeta(source string) map[string]any {
	return data.GetMarketNewsFetchMeta(source)
}

func (*marketAuditCompatibilityAdapter) TopNewsList(timeoutSeconds int64) *[]string {
	return data.GetTopNewsList(timeoutSeconds)
}

func (*marketAuditCompatibilityAdapter) RealTimeStockPriceInfo(ctx context.Context, stockCode string) (string, string) {
	return data.GetRealTimeStockPriceInfo(ctx, stockCode)
}

func (*marketAuditCompatibilityAdapter) SearchStockPriceInfo(stockName, stockCode string, timeoutSeconds int64) *[]string {
	return data.SearchStockPriceInfo(stockName, stockCode, timeoutSeconds)
}

func (*marketAuditCompatibilityAdapter) SearchGuShiTongStockInfo(stockCode string, timeoutSeconds int64) *[]string {
	return data.SearchGuShiTongStockInfo(stockCode, timeoutSeconds)
}

func (*marketAuditCompatibilityAdapter) FinancialReportsByXueqiu(stockCode string, timeoutSeconds int64) *[]string {
	return data.GetFinancialReportsByXUEQIU(stockCode, timeoutSeconds)
}

func (*marketAuditCompatibilityAdapter) FinancialReports(stockCode string, timeoutSeconds int64) *[]string {
	return data.GetFinancialReports(stockCode, timeoutSeconds)
}

func (*marketAuditCompatibilityAdapter) DiemengBaseURL() string {
	return data.DiemengEffectiveBaseURLForDisplay()
}

func (*marketAuditCompatibilityAdapter) WaitDiemengSelfCheck(reason string, timeout time.Duration) (cliports.DiemengSelfCheckSnapshot, error) {
	snapshot, err := data.WaitDiemengSelfCheck(reason, timeout)
	return cliports.DiemengSelfCheckSnapshot{
		Status:     snapshot.Status,
		Summary:    snapshot.Summary,
		CheckedAt:  snapshot.CheckedAt,
		ProbeCount: len(snapshot.Probes),
	}, err
}

func (*marketAuditCompatibilityAdapter) AuditDiemengMinuteBars(tsCode string, start, end time.Time) (*cliports.MinuteProviderAuditResult, error) {
	result, err := data.AuditDiemengMinuteBars(tsCode, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func (*marketAuditCompatibilityAdapter) AuditAkShareMinuteBars(tsCode string, start, end time.Time) (*cliports.MinuteProviderAuditResult, error) {
	result, err := data.AuditAkShareMinuteBars(tsCode, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func (*marketAuditCompatibilityAdapter) AuditSinaMinuteBars(tsCode string, start, end time.Time) (*cliports.MinuteProviderAuditResult, error) {
	result, err := data.AuditSinaMinuteBars(tsCode, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func (*marketAuditCompatibilityAdapter) AuditTencentMinuteBars(tsCode string, start, end time.Time) (*cliports.MinuteProviderAuditResult, error) {
	result, err := data.AuditTencentMinuteBars(tsCode, start, end)
	return mapMinuteProviderAuditResult(result), err
}

func mapMinuteProviderAuditResult(result *data.MinuteProviderAuditResult) *cliports.MinuteProviderAuditResult {
	if result == nil {
		return nil
	}
	return &cliports.MinuteProviderAuditResult{
		Provider:       result.Provider,
		Source:         result.Source,
		Bars:           result.Bars,
		FirstTradeTime: result.FirstTradeTime,
		LastTradeTime:  result.LastTradeTime,
	}
}

func (*marketAuditCompatibilityAdapter) DetectAIProviderName(cfg *models.AIConfig) string {
	return data.DetectAIProviderName(cfg)
}

func (*marketAuditCompatibilityAdapter) CompleteChat(ctx context.Context, cfg *models.AIConfig, messages []map[string]any, think bool) (string, string, string, error) {
	return data.NewOpenAiWithConfig(ctx, cfg).CompleteChat(messages, think)
}

func (*marketAuditCompatibilityAdapter) SendYieldEmailTestMessage() error {
	return data.SendYieldEmailTestMessage()
}

func (*marketAuditCompatibilityAdapter) SendDingDingMessage(message string) string {
	return data.NewDingDingAPI().SendDingDingMessage(message)
}

type marketAuditNewsCompatibilityAdapter struct {
	api *data.MarketNewsApi
}

func (a *marketAuditNewsCompatibilityAdapter) TelegraphList(timeoutSeconds int64) *[]models.Telegraph {
	return a.api.TelegraphList(timeoutSeconds)
}

func (a *marketAuditNewsCompatibilityAdapter) GetNewTelegraph(timeoutSeconds int64) *[]models.Telegraph {
	return a.api.GetNewTelegraph(timeoutSeconds)
}

func (a *marketAuditNewsCompatibilityAdapter) GetSinaNews(timeoutSeconds uint) *[]models.Telegraph {
	return a.api.GetSinaNews(timeoutSeconds)
}

func (a *marketAuditNewsCompatibilityAdapter) GetIndustryRank(sort string, count int) map[string]any {
	return a.api.GetIndustryRank(sort, count)
}

func (a *marketAuditNewsCompatibilityAdapter) GetIndustryMoneyRankSina(category, sort string) []map[string]any {
	return a.api.GetIndustryMoneyRankSina(category, sort)
}

func (a *marketAuditNewsCompatibilityAdapter) GetMoneyRankSina(sort string) []map[string]any {
	return a.api.GetMoneyRankSina(sort)
}

func (a *marketAuditNewsCompatibilityAdapter) GetStockMoneyTrendByDay(stockCode string, days int) []map[string]any {
	return a.api.GetStockMoneyTrendByDay(stockCode, days)
}

func (a *marketAuditNewsCompatibilityAdapter) HotEvent(size int) *[]models.HotEvent {
	return a.api.HotEvent(size)
}

func (a *marketAuditNewsCompatibilityAdapter) HotTopic(size int) []any {
	return a.api.HotTopic(size)
}

func (a *marketAuditNewsCompatibilityAdapter) ClsCalendar() []any {
	return a.api.ClsCalendar()
}

func (a *marketAuditNewsCompatibilityAdapter) GetGDP() *models.GDPResp {
	return a.api.GetGDP()
}

func (a *marketAuditNewsCompatibilityAdapter) GetCPI() *models.CPIResp {
	return a.api.GetCPI()
}

func (a *marketAuditNewsCompatibilityAdapter) GetPPI() *models.PPIResp {
	return a.api.GetPPI()
}

func (a *marketAuditNewsCompatibilityAdapter) GetPMI() *models.PMIResp {
	return a.api.GetPMI()
}

func (a *marketAuditNewsCompatibilityAdapter) IndustryResearchReport(industryCode string, days int) []any {
	return a.api.IndustryResearchReport(industryCode, days)
}

func (a *marketAuditNewsCompatibilityAdapter) GetIndustryReportInfo(infoCode string) string {
	return a.api.GetIndustryReportInfo(infoCode)
}

func (a *marketAuditNewsCompatibilityAdapter) InteractiveAnswer(page, pageSize int, keyword string) *models.InteractiveAnswer {
	return a.api.InteractiveAnswer(page, pageSize, keyword)
}

func (a *marketAuditNewsCompatibilityAdapter) TradingViewNews() *[]models.Telegraph {
	return a.api.TradingViewNews()
}

func (a *marketAuditNewsCompatibilityAdapter) ReutersNew() *models.ReutersNews {
	return a.api.ReutersNew()
}

func (a *marketAuditNewsCompatibilityAdapter) XUEQIUHotStock(size int, marketType string) *[]models.HotItem {
	return a.api.XUEQIUHotStock(size, marketType)
}

func (a *marketAuditNewsCompatibilityAdapter) GlobalStockIndexes(timeoutSeconds uint) map[string]any {
	return a.api.GlobalStockIndexes(timeoutSeconds)
}

func (a *marketAuditNewsCompatibilityAdapter) InvestCalendar(yearMonth string) []any {
	return a.api.InvestCalendar(yearMonth)
}

func (a *marketAuditNewsCompatibilityAdapter) LongTiger(date string) *[]models.LongTigerRankData {
	return a.api.LongTiger(date)
}

func (a *marketAuditNewsCompatibilityAdapter) StockResearchReport(stockCode string, days int) []any {
	return a.api.StockResearchReport(stockCode, days)
}

func (a *marketAuditNewsCompatibilityAdapter) StockNotice(stockCode string) []any {
	return a.api.StockNotice(stockCode)
}

func (a *marketAuditNewsCompatibilityAdapter) EMDictCode(code string) []any {
	return a.api.EMDictCode(code, freecache.NewCache(1024*1024))
}

func (a *marketAuditNewsCompatibilityAdapter) TradingViewNewsDetail(id string) *models.TVNewsDetail {
	return a.api.TradingViewNewsDetail(id)
}

func (a *marketAuditNewsCompatibilityAdapter) CailianpressWeb(searchWords string) *models.CailianpressWeb {
	return a.api.CailianpressWeb(searchWords)
}

type marketAuditSearchCompatibilityAdapter struct {
	api *data.SearchStockApi
}

func (a *marketAuditSearchCompatibilityAdapter) SearchStock(pageSize int) map[string]any {
	return a.api.SearchStock(pageSize)
}

func (a *marketAuditSearchCompatibilityAdapter) SearchBk(pageSize int) map[string]any {
	return a.api.SearchBk(pageSize)
}

func (a *marketAuditSearchCompatibilityAdapter) SearchETF(pageSize int) map[string]any {
	return a.api.SearchETF(pageSize)
}

func (a *marketAuditSearchCompatibilityAdapter) HotStrategy() map[string]any {
	return a.api.HotStrategy()
}

func (a *marketAuditSearchCompatibilityAdapter) StrategySquare() map[string]any {
	return a.api.StrategySquare()
}

type marketAuditStockCompatibilityAdapter struct {
	api *data.StockDataApi
}

func (a *marketAuditStockCompatibilityAdapter) GetStockCodeRealTimeData(stockCodes ...string) (*[]models.StockInfo, error) {
	return a.api.GetStockCodeRealTimeData(stockCodes...)
}

func (a *marketAuditStockCompatibilityAdapter) GetStockMinutePriceData(stockCode string) (*[]cliports.MinutePrice, string) {
	items, date := a.api.GetStockMinutePriceData(stockCode)
	if items == nil {
		return nil, date
	}
	result := make([]cliports.MinutePrice, 0, len(*items))
	for _, item := range *items {
		result = append(result, cliports.MinutePrice{
			Time:   item.Time,
			Price:  item.Price,
			Volume: item.Volume,
			Amount: item.Amount,
		})
	}
	return &result, date
}

func (a *marketAuditStockCompatibilityAdapter) GetKLineData(stockCode, kLineType string, days int64) *[]models.KLineData {
	return a.api.GetKLineData(stockCode, kLineType, days)
}

func (a *marketAuditStockCompatibilityAdapter) GetCommonKLineData(stockCode, kLineType string, days int64) *[]models.KLineData {
	return a.api.GetCommonKLineData(stockCode, kLineType, days)
}

func (a *marketAuditStockCompatibilityAdapter) GetStockMoneyData() models.StockMoneyDataResp {
	return a.api.GetStockMoneyData()
}

func (a *marketAuditStockCompatibilityAdapter) GetStockConceptInfo(stockCode string) models.StockConceptInfoResp {
	return a.api.GetStockConceptInfo(stockCode)
}

func (a *marketAuditStockCompatibilityAdapter) GetStockFinancialInfo(stockCode string) *models.StockFinancialInfoResp {
	return a.api.GetStockFinancialInfo(stockCode)
}

func (a *marketAuditStockCompatibilityAdapter) GetStockHolderNum(stockCode string) *models.StockHolderNumResp {
	return a.api.GetStockHolderNum(stockCode)
}

type marketAuditTushareCompatibilityAdapter struct {
	api *data.TushareApi
}

func (a *marketAuditTushareCompatibilityAdapter) GetTradeCalOpenMap(exchange string, startDate, endDate time.Time, timeoutSeconds int64) (map[string]bool, error) {
	return a.api.GetTradeCalOpenMap(exchange, startDate, endDate, timeoutSeconds)
}

func (a *marketAuditTushareCompatibilityAdapter) GetDaily(tsCode, startDate, endDate string, timeoutSeconds int64) string {
	return a.api.GetDaily(tsCode, startDate, endDate, timeoutSeconds)
}

func (a *marketAuditTushareCompatibilityAdapter) GetLatestTradeDate(timeoutSeconds int64) (time.Time, error) {
	return a.api.GetLatestTradeDate(timeoutSeconds)
}

func (a *marketAuditTushareCompatibilityAdapter) GetStockMinuteBars(tsCode string, startTime, endTime time.Time, timeoutSeconds int64) ([]cliports.TushareMinuteBar, error) {
	items, err := a.api.GetStockMinuteBars(tsCode, startTime, endTime, timeoutSeconds)
	if err != nil {
		return nil, err
	}
	result := make([]cliports.TushareMinuteBar, 0, len(items))
	for _, item := range items {
		result = append(result, cliports.TushareMinuteBar{
			TradeTime: item.TradeTime,
			Open:      item.Open,
			High:      item.High,
			Low:       item.Low,
			Close:     item.Close,
			Volume:    item.Vol,
			Amount:    item.Amount,
		})
	}
	return result, nil
}

func (*compatibilityServiceAdapter) AnalyzeNews(text string, save bool) {
	data.NewsAnalyze(text, save)
}

func (*compatibilityServiceAdapter) EnsureMarketDataSelfCheck(reason string) {
	data.EnsureDiemengSelfCheckAsync(reason)
}

func (*compatibilityServiceAdapter) NormalizeYieldEmailCronTimes(input string) ([]string, error) {
	return data.NormalizeYieldEmailCronTimes(input)
}

func (*compatibilityServiceAdapter) IsCNOpenTradeDay(day time.Time) bool {
	return data.IsCNOpenTradeDay(day)
}

func (*compatibilityServiceAdapter) IsCNOpenTradeDayStrict(day time.Time) (bool, error) {
	return data.IsCNOpenTradeDayStrict(day)
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
