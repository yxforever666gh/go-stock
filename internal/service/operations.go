package service

import (
	"context"
	"time"

	"go-stock/backend/governance"
	"go-stock/backend/models"

	"github.com/cloudwego/eino/schema"
	"github.com/coocood/freecache"
)

// ServiceOperations are consumer-owned ports for the compatibility-backed use
// cases. Bootstrap supplies the concrete adapters; service code never reaches
// into backend/data or global database handles.
type ServiceOperations struct {
	AI        AIOperations
	Config    ConfigOperations
	Fund      FundOperations
	Group     GroupOperations
	History   HistoryOperations
	Market    MarketOperations
	Notify    NotifyOperations
	Recommend RecommendOperations
	Scheduler SchedulerOperations
	Stock     StockOperations
	System    SystemOperations
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
	case o.History == nil:
		return operationRequiredError("history")
	case o.Market == nil:
		return operationRequiredError("market")
	case o.Notify == nil:
		return operationRequiredError("notify")
	case o.Recommend == nil:
		return operationRequiredError("recommend")
	case o.Scheduler == nil:
		return operationRequiredError("scheduler")
	case o.Stock == nil:
		return operationRequiredError("stock")
	case o.System == nil:
		return operationRequiredError("system")
	default:
		return nil
	}
}

type AIOperations interface {
	TestAIConfig(context.Context, int) *models.AIModelTestResult
	HumanizeMarketSummaryReport(string) string
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
	NewSummaryStockNewsStream(context.Context, int, string, *int, bool) <-chan map[string]any
	NewSummaryStockNewsStreamPhased(context.Context, int, string, *int, bool) <-chan map[string]any
	GenerateMarketSummarySupplementTable(context.Context, int, models.MarketSummarySupplementRequest) (string, string, string, error)
	NormalizeMarketSummaryQuestion(string) string
	ResolveMarketSummaryRecommendationCountPolicy(string) MarketSummaryRecommendationCountPolicy
	PrepareMarketSummaryReportForPersistence(string, time.Time, int) (string, MarketSummaryReportPrepareStats, error)
	RunMorningOpeningReview(time.Time) (string, error)
	MergeMarketSummarySupplementReport(string, string, []string, int) (string, MarketSummaryReportMergeStats)
	EnsureMarketSummaryRecommendStocksSaved(string, string, string, time.Time) (int, error)
	EnsureMarketSummaryRecommendStocksSavedWithResult(string, string, string, time.Time, []models.MarketSummaryVerifiedCandidateSnapshot) (*models.MarketSummaryRecommendSaveResult, error)
	EnsureMarketSummaryRecommendStocksSavedWithResultLimit(string, string, string, time.Time, []models.MarketSummaryVerifiedCandidateSnapshot, int) (*models.MarketSummaryRecommendSaveResult, error)
	EnsureMarketSummaryRecommendStocksSavedWithResultLimits(string, string, string, time.Time, []models.MarketSummaryVerifiedCandidateSnapshot, int, int) (*models.MarketSummaryRecommendSaveResult, error)
	EnsureMarketSummaryRecommendStocksSavedWithResultOptions(string, string, string, time.Time, []models.MarketSummaryVerifiedCandidateSnapshot, models.MarketSummaryRecommendSaveOptions) (*models.MarketSummaryRecommendSaveResult, error)
	EnsureMarketSummaryYieldOverridesSaved(string, time.Time) (int, error)
	SendYieldEmailTestMessage() error
	SendYieldEmailXLSXNow() (int, error)
	SendLatestAIAnalysisReportEmail() (*models.AIResponseResult, error)
	SendLatestAIAnalysisReportEmailForCron() (*models.AIResponseResult, error)
	SendMarketSummaryEmail(string, *models.AIResponseResult, string) error
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

type HistoryOperations interface {
	DeleteSession(string) error
	EnsureSession(string, string, int, string) (*models.AgentChatSession, error)
	TrimSessions(int) error
	ListRecentSessions(int) ([]models.AgentChatSession, error)
	ListSessionMessages(string, int) ([]models.AgentChatMessage, error)
	GetSession(string) (*models.AgentChatSession, error)
	FirstUserQuestion(string) (string, error)
	UpdateSessionTitle(string, string) error
	UpdateSessionModel(string, int, string) error
	ListSessionMessagesForAgent(string, int) ([]*schema.Message, error)
	AppendMessage(string, string, string, string) error
	TrimSessionMessages(string, int) error
}

type MarketOperations interface {
	PersistSyncedTelegraph(context.Context, *models.Telegraph, []string) (bool, error)
	AnalyzeNews(string, bool)
	EnsureMarketDataSelfCheck(string)
	NormalizeYieldEmailCronTimes(string) ([]string, error)
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

type RecommendOperations interface {
	RequireStrategyLive(context.Context, string) error
	EncodeMarketSummaryBlockedReasons([]models.MarketSummaryBlockedReasonItem) string
	SaveMarketSummaryRunDiagnostic(*models.MarketSummaryRunDiagnostic) error
	CreateAIResponseReport(context.Context, *models.AIResponseResult) error
	PersistAIResponseReport(context.Context, *models.AIResponseResult) error
	PersistMarketSummaryV150Decision(context.Context, MarketSummaryDecisionSnapshot, string, string) (*models.MarketSummaryRecommendSaveResult, error)
	GetAIResponseResultList(models.AIResponseResultQuery) (*models.AIResponseResultPageData, error)
	GetEmailSendLogList(models.EmailSendLogQuery) (*models.EmailSendLogPageData, error)
	DeleteAIResponseResult(uint) error
	BatchDeleteAIResponseResult([]uint) error
	GetAiRecommendStocksList(*models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error)
	GetAiRecommendStocksDateRange() (string, string, error)
	GetAiRecommendStocksYieldList(*models.AiRecommendStocksQuery) (*models.AiRecommendStocksYieldPageData, error)
	GetAiRecommendYieldMinuteChart(uint) (*models.AiRecommendYieldMinuteChartData, error)
	GetAiRecommendYieldDailyOverview(*models.AiRecommendStocksQuery) (*models.AiRecommendYieldDailyOverviewData, error)
	StartAiRecommendMinuteDownload() (map[string]any, error)
	GetAiRecommendYieldTaskStatus() (*models.AiRecommendStocksYieldPageData, error)
	GetAiRecommendYieldErrorLogs(int) ([]map[string]string, error)
	GetMarketSummaryRunDiagnostics(models.MarketSummaryRunDiagnosticQuery) (models.MarketSummaryRunDiagnosticSummary, error)
	GetMarketSummaryEmptyRunCount(models.MarketSummaryRunDiagnosticQuery) (int64, error)
	GetMarketSummaryBlockedReasonTop(models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error)
	GetMarketSummaryProductionDowngradeReasonTop(models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error)
	DeleteAiRecommendStocks(uint) error
}

type SchedulerOperations interface {
	CreateTaskRun(context.Context, *models.CronTaskRun) error
	UpdateTaskRun(context.Context, *models.CronTaskRun) error
	LatestAIResponseSince(context.Context, string, string, time.Time) (models.AIResponseResult, error)
	EarliestTaskRun(context.Context, string, time.Time, time.Time, []string) (models.CronTaskRun, error)
}

type StockOperations interface {
	ReplaceStockBaseInfo(context.Context, []models.StockBasic, []models.StockInfoHK, []models.StockInfoUS) error
	RefreshStockBaseInfo()
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
	GetHotStrategy() map[string]any
	GetStockCodeRealTimeData(...string) (*[]models.StockInfo, error)
}

type SystemOperations interface {
	StrategyRuntime(context.Context, string) governance.StrategyRuntimeStatus
	LatestMarketSummary(context.Context) (models.AIResponseResult, error)
}
