package service

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/models"

	"github.com/coocood/freecache"
)

var (
	ErrInvalidInput    = errors.New("invalid input")
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrOperationFailed = errors.New("operation failed")
)

type AIService interface {
	TestAIConfig(context.Context, int) *models.AIModelTestResult
	AnalyzeSentiment(string) models.SentimentResult
	AnalyzeSentimentWithFreqWeight(string) map[string]any
	GetAIConfigs() []*models.AIConfig
}

type ConfigService interface {
	GetConfig() *models.SettingConfig
	ExportConfig() string
	UpdateConfig(*models.SettingConfig) (string, error)
	ResolveFingerprint() (string, error)
}

type FundService interface {
	GetFundList(string) []models.FundBasic
	GetFollowedFund() []models.FollowedFund
	GetFollowedETFs() ([]models.ETFWatchlistItem, error)
	FollowFund(string) (string, error)
	FollowETF(models.ETFWatchlistItem) (string, error)
	UnFollowFund(string) (string, error)
	UnFollowETF(string) (string, error)
	AllFund()
	CrawlFundBasic(string) (*models.FundBasic, error)
	CrawlFundNetEstimatedUnit(string)
	CrawlFundNetUnitValue(string)
}

type GroupService interface {
	AddGroup(models.Group) bool
	GetGroupList() []models.Group
	UpdateGroupSort(int, int) bool
	InitializeGroupSort() bool
	GetGroupStockList(int) []models.GroupStock
	AddStockGroup(int, string) bool
	RemoveStockGroup(string, string, int) bool
	RemoveGroup(int) bool
}

type MarketService interface {
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
	RefreshTelegraphList(string) *[]*models.Telegraph
	GlobalStockIndexes() map[string]any
	GetIndustryRank(string, int) []any
	GetIndustryMoneyRankSina(string, string) []map[string]any
	GetMoneyRankSina(string) []map[string]any
	GetStockMoneyTrendByDay(string, int) []map[string]any
}

type StockService interface {
	ReplaceStockBaseInfo(context.Context, []models.StockBasic, []models.StockInfoHK, []models.StockInfoUS) error
	RefreshStockBaseInfo(context.Context) (models.StockMasterRefreshResult, error)
	RefreshIndexBaseInfo()
	Follow(string) (string, error)
	UnFollow(string) (string, error)
	GetFollowList(int) *[]models.FollowedStock
	GetStockList(string) []models.StockBasic
	SetCostPriceAndVolume(string, float64, int64) (string, error)
	SetAlarmChangePercent(float64, float64, string) (string, error)
	SetStockSort(int64, string)
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

func validateServices(ai AIService, config ConfigService, fund FundService, group GroupService, market MarketService, stock StockService) error {
	switch {
	case ai == nil:
		return operationRequiredError("ai")
	case config == nil:
		return operationRequiredError("config")
	case fund == nil:
		return operationRequiredError("fund")
	case group == nil:
		return operationRequiredError("group")
	case market == nil:
		return operationRequiredError("market")
	case stock == nil:
		return operationRequiredError("stock")
	default:
		return nil
	}
}
