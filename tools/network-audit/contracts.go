package main

import (
	"context"
	"time"

	"go-stock/backend/models"
)

type marketAuditProvider interface {
	Settings() *models.SettingConfig
	News() marketAuditNews
	Search(words, fingerprint string) marketAuditSearch
	Stock() marketAuditStock
	Fund() marketAuditFund
	Tushare(*models.SettingConfig) marketAuditTushare
	MarketNewsFetchMeta(string) map[string]any
	TopNewsList(int64) *[]string
	RealTimeStockPriceInfo(context.Context, string) (string, string)
	SearchStockPriceInfo(string, string, int64) *[]string
	SearchGuShiTongStockInfo(string, int64) *[]string
	FinancialReportsByXueqiu(string, int64) *[]string
	FinancialReports(string, int64) *[]string
	DiemengBaseURL() string
	WaitDiemengSelfCheck(string, time.Duration) (diemengSelfCheckSnapshot, error)
	AuditDiemengMinuteBars(string, time.Time, time.Time) (*minuteProviderAuditResult, error)
	AuditAkShareMinuteBars(string, time.Time, time.Time) (*minuteProviderAuditResult, error)
	AuditSinaMinuteBars(string, time.Time, time.Time) (*minuteProviderAuditResult, error)
	AuditTencentMinuteBars(string, time.Time, time.Time) (*minuteProviderAuditResult, error)
	DetectAIProviderName(*models.AIConfig) string
	CompleteChat(context.Context, *models.AIConfig, []map[string]any, bool) (string, string, string, error)
	SendDingDingMessage(string) string
}

type marketAuditNews interface {
	TelegraphList(int64) *[]models.Telegraph
	GetNewTelegraph(int64) *[]models.Telegraph
	GetSinaNews(uint) *[]models.Telegraph
	GetIndustryRank(string, int) map[string]any
	GetIndustryMoneyRankSina(string, string) []map[string]any
	GetMoneyRankSina(string) []map[string]any
	GetStockMoneyTrendByDay(string, int) []map[string]any
	HotEvent(int) *[]models.HotEvent
	HotTopic(int) []any
	ClsCalendar() []any
	GetGDP() *models.GDPResp
	GetCPI() *models.CPIResp
	GetPPI() *models.PPIResp
	GetPMI() *models.PMIResp
	IndustryResearchReport(string, int) []any
	GetIndustryReportInfo(string) string
	InteractiveAnswer(int, int, string) *models.InteractiveAnswer
	TradingViewNews() *[]models.Telegraph
	ReutersNew() *models.ReutersNews
	XUEQIUHotStock(int, string) *[]models.HotItem
	GlobalStockIndexes(uint) map[string]any
	InvestCalendar(string) []any
	LongTiger(string) *[]models.LongTigerRankData
	StockResearchReport(string, int) []any
	StockNotice(string) []any
	EMDictCode(string) []any
	TradingViewNewsDetail(string) *models.TVNewsDetail
	CailianpressWeb(string) *models.CailianpressWeb
}

type marketAuditSearch interface {
	SearchStock(int) map[string]any
	SearchBk(int) map[string]any
	SearchETF(int) map[string]any
}

type marketAuditStock interface {
	GetStockCodeRealTimeData(...string) (*[]models.StockInfo, error)
	GetStockMinutePriceData(string) (*[]minutePrice, string)
	GetKLineData(string, string, int64) *[]models.KLineData
	GetCommonKLineData(string, string, int64) *[]models.KLineData
	GetStockMoneyData() models.StockMoneyDataResp
	GetStockConceptInfo(string) models.StockConceptInfoResp
	GetStockFinancialInfo(string) *models.StockFinancialInfoResp
	GetStockHolderNum(string) *models.StockHolderNumResp
}

type marketAuditFund interface {
	CrawlFundBasic(string) (*models.FundBasic, error)
}

type marketAuditTushare interface {
	GetTradeCalOpenMap(string, time.Time, time.Time, int64) (map[string]bool, error)
	GetDaily(string, string, string, int64) string
	GetLatestTradeDate(int64) (time.Time, error)
	GetStockMinuteBars(string, time.Time, time.Time, int64) ([]tushareMinuteBar, error)
}

type minutePrice struct {
	Time   string
	Price  float64
	Volume float64
	Amount float64
}

type tushareMinuteBar struct {
	TradeTime time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Amount    float64
}

type tushareRequest struct {
	APIName string         `json:"api_name"`
	Token   string         `json:"token"`
	Params  map[string]any `json:"params,omitempty"`
	Fields  string         `json:"fields"`
}

type tushareTableResponse struct {
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

type diemengSelfCheckSnapshot struct {
	Status     string
	Summary    string
	CheckedAt  time.Time
	ProbeCount int
}

type minuteProviderAuditResult struct {
	Provider       string
	Source         string
	Bars           int
	FirstTradeTime string
	LastTradeTime  string
}
