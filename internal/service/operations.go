package service

import (
	"context"
	"time"

	"go-stock/backend/models"

	"github.com/coocood/freecache"
)

// ServiceOperations are consumer-owned ports for the compatibility-backed use
// cases. Bootstrap supplies the concrete adapters; service code never reaches
// into backend/data or global database handles.
type ServiceOperations struct {
	AI     AIOperations
	Config ConfigOperations
	Fund   FundOperations
	Group  GroupOperations
	Market MarketOperations
	Notify NotifyOperations
	Stock  StockOperations
}

func (o ServiceOperations) Validate() error {
	switch {
	case o.AI == nil:
		return operationRequiredError("ai")
	case o.Config == nil:
		return operationRequiredError("config")
	case o.Fund == nil:
		return operationRequiredError("fund")
	case o.Group == nil:
		return operationRequiredError("group")
	case o.Market == nil:
		return operationRequiredError("market")
	case o.Notify == nil:
		return operationRequiredError("notify")
	case o.Stock == nil:
		return operationRequiredError("stock")
	default:
		return nil
	}
}

type AIOperations interface {
	TestAIConfig(context.Context, int) *models.AIModelTestResult
	AnalyzeSentiment(string) models.SentimentResult
	AnalyzeSentimentWithFreqWeight(string) map[string]any
	GetAIResponseResult(context.Context, string) *models.AIResponseResult
	SaveAIResponseResult(context.Context, string, string, string, string, string, int)
	GetPromptTemplates(string, string) *[]models.PromptTemplate
	AddPrompt(models.PromptTemplate) string
	DelPrompt(uint) string
	GetAIConfigs() []*models.AIConfig
	ResolveDefaultAIConfigID() int
	ResolveAIFallbackOrder(int) []int
	ResolveAIModelName(int) string
	NewChatStream(context.Context, string, string, string, int, *int, []models.Tool, bool) <-chan map[string]any
}

type ConfigOperations interface {
	GetConfig() *models.SettingConfig
	ExportConfig() string
	UpdateConfig(*models.SettingConfig) string
	ResolveFingerprint() (string, error)
}

type FundOperations interface {
	GetFundList(string) []models.FundBasic
	GetFollowedFund() []models.FollowedFund
	FollowFund(string) string
	UnFollowFund(string) string
	AllFund()
	CrawlFundBasic(string) (*models.FundBasic, error)
	CrawlFundNetEstimatedUnit(string)
	CrawlFundNetUnitValue(string)
}

type GroupOperations interface {
	AddGroup(models.Group) bool
	GetGroupList() []models.Group
	UpdateGroupSort(int, int) bool
	InitializeGroupSort() bool
	GetGroupStockList(int) []models.GroupStock
	AddStockGroup(int, string) bool
	RemoveStockGroup(string, string, int) bool
	RemoveGroup(int) bool
}

type MarketOperations interface {
	PersistSyncedTelegraph(context.Context, *models.Telegraph, []string) (bool, error)
	AnalyzeNews(string, bool)
	EnsureMarketDataSelfCheck(string)
	IsCNOpenTradeDay(time.Time) bool
	IsCNOpenTradeDayStrict(time.Time) (bool, error)
	LongTigerRank(string) *[]models.LongTigerRankData
	StockResearchReport(string, int) []any
	StockNotice(string) []any
	IndustryResearchReport(string, int) []any
	EMDictCode(string, *freecache.Cache) []any
	HotStock(string, int) *[]models.HotItem
	HotEvent(int) *[]models.HotEvent
	HotTopic(int) []any
	InvestCalendar(string) []any
	ClsCalendar() []any
	GetTelegraphList(string) *[]*models.Telegraph
	TelegraphList(int64) *[]models.Telegraph
	GetSinaNews(uint) *[]models.Telegraph
	TradingViewNews() *[]models.Telegraph
	GlobalStockIndexes() map[string]any
	GetIndustryRank(string, int) []any
	GetIndustryMoneyRankSina(string, string) []map[string]any
	GetMoneyRankSina(string) []map[string]any
	GetStockMoneyTrendByDay(string, int) []map[string]any
}

type NotifyOperations interface {
	SendDingDingMessage(string) string
	SendAlert(string, string, string, string)
}

type StockOperations interface {
	ReplaceStockBaseInfo(context.Context, []models.StockBasic, []models.StockInfoHK, []models.StockInfoUS) error
	RefreshStockBaseInfo(context.Context) (models.StockMasterRefreshResult, error)
	RefreshIndexBaseInfo()
	Follow(string) string
	UnFollow(string) string
	GetFollowList(int) *[]models.FollowedStock
	GetStockList(string) []models.StockBasic
	SetCostPriceAndVolume(string, float64, int64) string
	SetAlarmChangePercent(float64, float64, string) string
	SetStockSort(int64, string)
	SetStockAICron(string, string)
	GetFollowedStockByStockCode(string) *models.FollowedStock
	GetAllFollowedStocks() []models.FollowedStock
	GetFollowedStockDetail(string) *models.FollowedStock
	UpdateFollowPrice(string, float64)
	GetStoredStockInfo(string) *models.StockInfo
	GetStockKLine(string, int64) *[]models.KLineData
	GetStockCommonKLine(string, int64) *[]models.KLineData
	GetStockMinutePriceLineData(string, string) map[string]any
	SearchStock(string) map[string]any
	SearchStockWithFingerprint(string, string, int) map[string]any
	GetStockCodeRealTimeData(...string) (*[]models.StockInfo, error)
}
