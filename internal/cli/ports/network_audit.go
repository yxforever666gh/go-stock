package ports

import (
	"context"
	"time"

	"go-stock/backend/models"
)

// MarketAuditProvider is the consumer-owned boundary for the network-audit
// command. It exposes only the provider behavior and fields used by the audit;
// legacy provider implementations stay behind the bootstrap adapter.
type MarketAuditProvider interface {
	Settings() *models.SettingConfig
	News() MarketAuditNews
	Search(words, fingerprint string) MarketAuditSearch
	Stock() MarketAuditStock
	Fund() MarketAuditFund
	Tushare(*models.SettingConfig) MarketAuditTushare

	MarketNewsFetchMeta(source string) map[string]any
	TopNewsList(timeoutSeconds int64) *[]string
	RealTimeStockPriceInfo(context.Context, string) (price, priceTime string)
	SearchStockPriceInfo(stockName, stockCode string, timeoutSeconds int64) *[]string
	SearchGuShiTongStockInfo(stockCode string, timeoutSeconds int64) *[]string
	FinancialReportsByXueqiu(stockCode string, timeoutSeconds int64) *[]string
	FinancialReports(stockCode string, timeoutSeconds int64) *[]string

	DiemengBaseURL() string
	WaitDiemengSelfCheck(reason string, timeout time.Duration) (DiemengSelfCheckSnapshot, error)
	AuditDiemengMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error)
	AuditAkShareMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error)
	AuditSinaMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error)
	AuditTencentMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error)

	DetectAIProviderName(*models.AIConfig) string
	CompleteChat(context.Context, *models.AIConfig, []map[string]any, bool) (content, reasoning, modelName string, err error)
	SendDingDingMessage(message string) string
}

type MarketAuditNews interface {
	TelegraphList(timeoutSeconds int64) *[]models.Telegraph
	GetNewTelegraph(timeoutSeconds int64) *[]models.Telegraph
	GetSinaNews(timeoutSeconds uint) *[]models.Telegraph
	GetIndustryRank(sort string, count int) map[string]any
	GetIndustryMoneyRankSina(category, sort string) []map[string]any
	GetMoneyRankSina(sort string) []map[string]any
	GetStockMoneyTrendByDay(stockCode string, days int) []map[string]any
	HotEvent(size int) *[]models.HotEvent
	HotTopic(size int) []any
	ClsCalendar() []any
	GetGDP() *models.GDPResp
	GetCPI() *models.CPIResp
	GetPPI() *models.PPIResp
	GetPMI() *models.PMIResp
	IndustryResearchReport(industryCode string, days int) []any
	GetIndustryReportInfo(infoCode string) string
	InteractiveAnswer(page, pageSize int, keyword string) *models.InteractiveAnswer
	TradingViewNews() *[]models.Telegraph
	ReutersNew() *models.ReutersNews
	XUEQIUHotStock(size int, marketType string) *[]models.HotItem
	GlobalStockIndexes(timeoutSeconds uint) map[string]any
	InvestCalendar(yearMonth string) []any
	LongTiger(date string) *[]models.LongTigerRankData
	StockResearchReport(stockCode string, days int) []any
	StockNotice(stockCode string) []any
	EMDictCode(code string) []any
	TradingViewNewsDetail(id string) *models.TVNewsDetail
	CailianpressWeb(searchWords string) *models.CailianpressWeb
}

type MarketAuditSearch interface {
	SearchStock(pageSize int) map[string]any
	SearchBk(pageSize int) map[string]any
	SearchETF(pageSize int) map[string]any
}

type MarketAuditStock interface {
	GetStockCodeRealTimeData(stockCodes ...string) (*[]models.StockInfo, error)
	GetStockMinutePriceData(stockCode string) (*[]MinutePrice, string)
	GetKLineData(stockCode, kLineType string, days int64) *[]models.KLineData
	GetCommonKLineData(stockCode, kLineType string, days int64) *[]models.KLineData
	GetStockMoneyData() models.StockMoneyDataResp
	GetStockConceptInfo(stockCode string) models.StockConceptInfoResp
	GetStockFinancialInfo(stockCode string) *models.StockFinancialInfoResp
	GetStockHolderNum(stockCode string) *models.StockHolderNumResp
}

type MarketAuditFund interface {
	CrawlFundBasic(fundCode string) (*models.FundBasic, error)
}

type MarketAuditTushare interface {
	GetTradeCalOpenMap(exchange string, startDate, endDate time.Time, timeoutSeconds int64) (map[string]bool, error)
	GetDaily(tsCode, startDate, endDate string, timeoutSeconds int64) string
	GetLatestTradeDate(timeoutSeconds int64) (time.Time, error)
	GetStockMinuteBars(tsCode string, startTime, endTime time.Time, timeoutSeconds int64) ([]TushareMinuteBar, error)
}

type MinutePrice struct {
	Time   string
	Price  float64
	Volume float64
	Amount float64
}

type TushareMinuteBar struct {
	TradeTime time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Amount    float64
}

// TushareRequest and TushareTableResponse describe the public JSON contract
// used by two direct connectivity probes. They intentionally do not expose a
// legacy data package type.
type TushareRequest struct {
	APIName string         `json:"api_name"`
	Token   string         `json:"token"`
	Params  map[string]any `json:"params,omitempty"`
	Fields  string         `json:"fields"`
}

type TushareTableResponse struct {
	RequestID string `json:"request_id"`
	Code      int    `json:"code"`
	Data      struct {
		Fields  []string `json:"fields"`
		Items   [][]any  `json:"items"`
		HasMore bool     `json:"has_more"`
		Count   int      `json:"count"`
	} `json:"data"`
	Msg string `json:"msg"`
}

type DiemengSelfCheckSnapshot struct {
	Status     string
	Summary    string
	CheckedAt  time.Time
	ProbeCount int
}

type MinuteProviderAuditResult struct {
	Provider       string
	Source         string
	Bars           int
	FirstTradeTime string
	LastTradeTime  string
}
