package models

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/plugin/soft_delete"
)

// Settings is the application-facing configuration contract. Persistence and
// provider adapters may reuse it, but use cases no longer depend on those
// adapters for the type definition.
type Settings struct {
	gorm.Model
	TushareToken             string `json:"tushareToken"`
	LocalPushEnable          bool   `json:"localPushEnable"`
	DingPushEnable           bool   `json:"dingPushEnable"`
	DingRobot                string `json:"dingRobot"`
	YieldEmailEnable         bool   `json:"yieldEmailEnable"`
	YieldEmailTo             string `json:"yieldEmailTo"`
	YieldEmailFrom           string `json:"yieldEmailFrom"`
	YieldEmailSMTPHost       string `json:"yieldEmailSmtpHost"`
	YieldEmailSMTPPort       int    `json:"yieldEmailSmtpPort"`
	YieldEmailSMTPUsername   string `json:"yieldEmailSmtpUsername"`
	YieldEmailSMTPPassword   string `json:"yieldEmailSmtpPassword"`
	YieldEmailCronEnabled    bool   `json:"yieldEmailCronEnabled"`
	YieldEmailCronTimes      string `json:"yieldEmailCronTimes"`
	MarketSummaryEmailEnable bool   `json:"marketSummaryEmailEnabled"`
	UpdateBasicInfoOnStart   bool   `json:"updateBasicInfoOnStart"`
	RefreshInterval          int64  `json:"refreshInterval"`
	OpenAiEnable             bool   `json:"openAiEnable"`
	Prompt                   string `json:"prompt"`
	CheckUpdate              bool   `json:"checkUpdate"`
	QuestionTemplate         string `json:"questionTemplate"`
	CrawlTimeOut             int64  `json:"crawlTimeOut"`
	KDays                    int64  `json:"kDays"`
	EnableDanmu              bool   `json:"enableDanmu"`
	BrowserPath              string `json:"browserPath"`
	EnableNews               bool   `json:"enableNews"`
	DarkTheme                bool   `json:"darkTheme"`
	BrowserPoolSize          int    `json:"browserPoolSize"`
	EnableFund               bool   `json:"enableFund"`
	EnablePushNews           bool   `json:"enablePushNews"`
	EnableOnlyPushRedNews    bool   `json:"enableOnlyPushRedNews"`
	HttpProxy                string `json:"httpProxy"`
	HttpProxyEnabled         bool   `json:"httpProxyEnabled"`
	ForceNoProxyForFetch     bool   `json:"forceNoProxyForFetch" gorm:"default:true"`
	EnableAgent              bool   `json:"enableAgent"`
	QgqpBId                  string `json:"qgqpBId" gorm:"column:qgqp_b_id"`
	MarketSummaryCronEnabled bool   `json:"marketSummaryCronEnabled" gorm:"default:true"`
	MarketSummaryCronTimes   string `json:"marketSummaryCronTimes" gorm:"default:'09:40,11:30,14:30'"`
	MinuteProviderMode       string `json:"minuteProviderMode" gorm:"default:'public'"`
	MinuteLongHistoryHint    bool   `json:"minuteLongHistoryHintEnabled" gorm:"column:minute_long_history_hint_enabled;default:true"`
	PrivateMinuteEnabled     bool   `json:"privateMinuteEnabled"`
	PrivateMinuteBaseURL     string `json:"privateMinuteBaseUrl"`
	PrivateMinuteAPIKey      string `json:"privateMinuteApiKey"`
	PrivateMinuteTimeoutSec  int    `json:"privateMinuteTimeoutSec"`
	PrivateMinuteMinInterval int    `json:"privateMinuteMinIntervalMs"`
	PrivateMinuteProxyMode   string `json:"privateMinuteProxyMode" gorm:"default:'disable'"`
	PrivateMinuteLevel       string `json:"privateMinuteLevel" gorm:"default:'1min'"`
	AkshareEnabled           bool   `json:"akshareEnabled" gorm:"default:true"`
	SinaMinuteEnabled        bool   `json:"sinaMinuteEnabled" gorm:"default:true"`
	TencentMinuteEnabled     bool   `json:"tencentMinuteEnabled" gorm:"default:true"`
	EastmoneyMinuteEnabled   bool   `json:"eastmoneyMinuteEnabled" gorm:"default:true"`
	AkshareMinuteSourceMode  string `json:"akshareMinuteSourceMode" gorm:"default:'auto'"`
}

func (Settings) TableName() string { return "settings" }

type AIConfig struct {
	ID               uint `gorm:"primarykey"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Sort             int     `json:"sort" gorm:"index"`
	Name             string  `json:"name"`
	BaseUrl          string  `json:"baseUrl"`
	ApiKey           string  `json:"apiKey" `
	ModelName        string  `json:"modelName"`
	ApiProtocol      string  `json:"apiProtocol" gorm:"default:'chat_completions'"`
	MaxTokens        int     `json:"maxTokens"`
	Temperature      float64 `json:"temperature"`
	TimeOut          int     `json:"timeOut"`
	HttpProxy        string  `json:"httpProxy"`
	HttpProxyEnabled bool    `json:"httpProxyEnabled"`
}

func (AIConfig) TableName() string { return "ai_config" }

type SettingConfig struct {
	*Settings
	AiConfigs []*AIConfig `json:"aiConfigs"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type FunctionParameters struct {
	Type                 string         `json:"type"`
	Properties           map[string]any `json:"properties"`
	Required             []string       `json:"required"`
	AdditionalProperties bool           `json:"additionalProperties"`
}

type ToolFunction struct {
	Name        string              `json:"name"`
	Strict      bool                `json:"strict"`
	Description string              `json:"description"`
	Parameters  *FunctionParameters `json:"parameters"`
}

type Group struct {
	gorm.Model
	Name string `json:"name" gorm:"index"`
	Sort int    `json:"sort"`
}

func (Group) TableName() string { return "stock_groups" }

type GroupStock struct {
	gorm.Model
	StockCode string `json:"stockCode" gorm:"index"`
	GroupId   int    `json:"groupId" gorm:"index"`
	GroupInfo Group  `json:"groupInfo" gorm:"foreignKey:GroupId;references:ID"`
}

func (GroupStock) TableName() string { return "group_stock_info" }

type FollowedFund struct {
	gorm.Model
	Code string `json:"code" gorm:"index"`
	Name string `json:"name"`

	NetUnitValue     *float64 `json:"netUnitValue"`
	NetUnitValueDate string   `json:"netUnitValueDate"`
	NetEstimatedUnit *float64 `json:"netEstimatedUnit"`
	NetEstimatedTime string   `json:"netEstimatedUnitTime"`
	NetAccumulated   *float64 `json:"netAccumulated"`
	NetEstimatedRate *float64 `json:"netEstimatedRate"`

	FundBasic FundBasic `json:"fundBasic" gorm:"foreignKey:Code;references:Code"`
}

func (FollowedFund) TableName() string { return "followed_fund" }

type FundBasic struct {
	gorm.Model
	Code           string `json:"code" gorm:"index"`
	Name           string `json:"name"`
	FullName       string `json:"fullName"`
	Type           string `json:"type"`
	Establishment  string `json:"establishment"`
	Scale          string `json:"scale"`
	Company        string `json:"company"`
	Manager        string `json:"manager"`
	Rating         string `json:"rating"`
	TrackingTarget string `json:"trackingTarget"`

	NetUnitValue     *float64 `json:"netUnitValue"`
	NetUnitValueDate string   `json:"netUnitValueDate"`
	NetEstimatedUnit *float64 `json:"netEstimatedUnit"`
	NetEstimatedTime string   `json:"netEstimatedUnitTime"`
	NetAccumulated   *float64 `json:"netAccumulated"`
	NetGrowth1       *float64 `json:"netGrowth1"`
	NetGrowth3       *float64 `json:"netGrowth3"`
	NetGrowth6       *float64 `json:"netGrowth6"`
	NetGrowth12      *float64 `json:"netGrowth12"`
	NetGrowth36      *float64 `json:"netGrowth36"`
	NetGrowth60      *float64 `json:"netGrowth60"`
	NetGrowthYTD     *float64 `json:"netGrowthYTD"`
	NetGrowthAll     *float64 `json:"netGrowthAll"`
}

func (FundBasic) TableName() string { return "fund_basic" }

type StockInfo struct {
	gorm.Model
	Date     string  `json:"日期" gorm:"index"`
	Time     string  `json:"时间" gorm:"index"`
	Code     string  `json:"股票代码" gorm:"index"`
	Name     string  `json:"股票名称" gorm:"index"`
	PrePrice float64 `json:"上次当前价格"`
	Price    string  `json:"当前价格"`
	Volume   string  `json:"成交的股票数"`
	Amount   string  `json:"成交金额"`
	Open     string  `json:"今日开盘价"`
	PreClose string  `json:"昨日收盘价"`
	High     string  `json:"今日最高价"`
	Low      string  `json:"今日最低价"`
	Bid      string  `json:"竞买价"`
	Ask      string  `json:"竞卖价"`
	B1P      string  `json:"买一报价"`
	B1V      string  `json:"买一申报"`
	B2P      string  `json:"买二报价"`
	B2V      string  `json:"买二申报"`
	B3P      string  `json:"买三报价"`
	B3V      string  `json:"买三申报"`
	B4P      string  `json:"买四报价"`
	B4V      string  `json:"买四申报"`
	B5P      string  `json:"买五报价"`
	B5V      string  `json:"买五申报"`
	A1P      string  `json:"卖一报价"`
	A1V      string  `json:"卖一申报"`
	A2P      string  `json:"卖二报价"`
	A2V      string  `json:"卖二申报"`
	A3P      string  `json:"卖三报价"`
	A3V      string  `json:"卖三申报"`
	A4P      string  `json:"卖四报价"`
	A4V      string  `json:"卖四申报"`
	A5P      string  `json:"卖五报价"`
	A5V      string  `json:"卖五申报"`
	Market   string  `json:"市场"`
	BA       string  `json:"盘前盘后"`
	BAChange string  `json:"盘前盘后涨跌幅"`

	ChangePercent      float64      `json:"changePercent"`
	ChangePrice        float64      `json:"changePrice"`
	HighRate           float64      `json:"highRate"`
	LowRate            float64      `json:"lowRate"`
	CostPrice          float64      `json:"costPrice"`
	CostVolume         int64        `json:"costVolume"`
	Profit             float64      `json:"profit"`
	ProfitAmount       float64      `json:"profitAmount"`
	ProfitAmountToday  float64      `json:"profitAmountToday"`
	Sort               int64        `json:"sort"`
	AlarmChangePercent float64      `json:"alarmChangePercent"`
	AlarmPrice         float64      `json:"alarmPrice"`
	Groups             []GroupStock `gorm:"-:all"`
}

func (StockInfo) TableName() string { return "stock_info" }

type StockBasic struct {
	gorm.Model
	TsCode     string `json:"ts_code" gorm:"index"`
	Symbol     string `json:"symbol" gorm:"index"`
	Name       string `json:"name" gorm:"index"`
	Area       string `json:"area"`
	Industry   string `json:"industry" gorm:"index"`
	Fullname   string `json:"fullname"`
	Ename      string `json:"enname"`
	Cnspell    string `json:"cnspell"`
	Market     string `json:"market"`
	Exchange   string `json:"exchange"`
	CurrType   string `json:"curr_type"`
	ListStatus string `json:"list_status"`
	ListDate   string `json:"list_date"`
	DelistDate string `json:"delist_date"`
	IsHs       string `json:"is_hs"`
	ActName    string `json:"act_name"`
	ActEntType string `json:"act_ent_type"`
	BKName     string `json:"bk_name"`
	BKCode     string `json:"bk_code"`
}

func (StockBasic) TableName() string { return "tushare_stock_basic" }

type FollowedStock struct {
	StockCode          string
	Name               string
	Volume             int64
	CostPrice          float64
	Price              float64
	PriceChange        float64
	ChangePercent      float64
	AlarmChangePercent float64
	AlarmPrice         float64
	Time               time.Time
	Sort               int64
	Cron               *string
	IsDel              soft_delete.DeletedAt `gorm:"softDelete:flag"`
	Groups             []GroupStock          `gorm:"foreignKey:StockCode;references:StockCode"`
	AiConfigId         int
}

func (FollowedStock) TableName() string { return "followed_stock" }

type KLineData struct {
	Day    string `json:"day"`
	Open   string `json:"open"`
	High   string `json:"high"`
	Low    string `json:"low"`
	Close  string `json:"close"`
	Volume string `json:"volume"`
}

type AIEvidenceReference struct {
	Type         string `json:"type"`
	Summary      string `json:"summary"`
	SourceName   string `json:"sourceName,omitempty"`
	SourceType   string `json:"sourceType,omitempty"`
	TrustLevel   string `json:"trustLevel,omitempty"`
	LatencyLevel string `json:"latencyLevel,omitempty"`
	Title        string `json:"title,omitempty"`
	URL          string `json:"url,omitempty"`
	PublishedAt  string `json:"publishedAt,omitempty"`
	EntityType   string `json:"entityType,omitempty"`
	EntityCode   string `json:"entityCode,omitempty"`
	DedupeKey    string `json:"dedupeKey,omitempty"`
	RawHash      string `json:"rawHash,omitempty"`
}

type MarketSummaryTechnicalMetrics struct {
	DayAmount           string `json:"dayAmount,omitempty"`
	DayVolume           string `json:"dayVolume,omitempty"`
	VolumeRatio         string `json:"volumeRatio,omitempty"`
	TurnoverRate        string `json:"turnoverRate,omitempty"`
	Ma5                 string `json:"ma5,omitempty"`
	Ma10                string `json:"ma10,omitempty"`
	Ma20                string `json:"ma20,omitempty"`
	High3d              string `json:"high3d,omitempty"`
	Low3d               string `json:"low3d,omitempty"`
	High5d              string `json:"high5d,omitempty"`
	Low5d               string `json:"low5d,omitempty"`
	High20d             string `json:"high20d,omitempty"`
	Low20d              string `json:"low20d,omitempty"`
	MinuteVolumeVsAvg5  string `json:"minuteVolumeVsAvg5,omitempty"`
	MinuteVolumeVsAvg10 string `json:"minuteVolumeVsAvg10,omitempty"`
	PriceAboveMa5       bool   `json:"priceAboveMa5,omitempty"`
	PriceAboveMa10      bool   `json:"priceAboveMa10,omitempty"`
	Breakout3dHigh      bool   `json:"breakout3dHigh,omitempty"`
	Breakout5dHigh      bool   `json:"breakout5dHigh,omitempty"`
	PullbackNearMa5     bool   `json:"pullbackNearMa5,omitempty"`
}

type MarketSummaryFeasiblePlan struct {
	Path          string  `json:"path"`
	EntryRange    string  `json:"entryRange,omitempty"`
	WorstEntry    float64 `json:"worstEntry"`
	StopLoss      float64 `json:"stopLoss"`
	TakeProfit    float64 `json:"takeProfit"`
	RewardRisk    float64 `json:"rewardRisk"`
	DownsidePct   float64 `json:"downsidePct"`
	PassHardGate  bool    `json:"passHardGate"`
	FailureReason string  `json:"failureReason,omitempty"`
}

type MarketSummaryVerifiedCandidateSnapshot struct {
	StockName         string                        `json:"stockName"`
	StockCode         string                        `json:"stockCode"`
	Direction         string                        `json:"direction,omitempty"`
	BkName            string                        `json:"bkName,omitempty"`
	Reason            string                        `json:"reason,omitempty"`
	CurrentPrice      string                        `json:"currentPrice,omitempty"`
	CurrentPriceTime  string                        `json:"currentPriceTime,omitempty"`
	MinutePrice       string                        `json:"minutePrice,omitempty"`
	MinuteAmount      string                        `json:"minuteAmount,omitempty"`
	MinuteVolume      string                        `json:"minuteVolume,omitempty"`
	MinuteTime        string                        `json:"minuteTime,omitempty"`
	MinuteDate        string                        `json:"minuteDate,omitempty"`
	PriceAnchorSource string                        `json:"priceAnchorSource,omitempty"`
	AuctionPrice      string                        `json:"auctionPrice,omitempty"`
	AuctionAmount     string                        `json:"auctionAmount,omitempty"`
	AuctionVolume     string                        `json:"auctionVolume,omitempty"`
	AuctionTime       string                        `json:"auctionTime,omitempty"`
	AuctionDate       string                        `json:"auctionDate,omitempty"`
	AuctionOpen       string                        `json:"auctionOpen,omitempty"`
	AuctionHigh       string                        `json:"auctionHigh,omitempty"`
	AuctionLow        string                        `json:"auctionLow,omitempty"`
	AuctionPreClose   string                        `json:"auctionPreClose,omitempty"`
	AuctionTurnover   string                        `json:"auctionTurnoverRate,omitempty"`
	AuctionCommittee  string                        `json:"auctionCommitteeRatio,omitempty"`
	AuctionVolumeRate string                        `json:"auctionVolumeRatio,omitempty"`
	AuctionBidPrice   []string                      `json:"auctionBidPrice,omitempty"`
	AuctionAskPrice   []string                      `json:"auctionAskPrice,omitempty"`
	AuctionBidVol     []string                      `json:"auctionBidVol,omitempty"`
	AuctionAskVol     []string                      `json:"auctionAskVol,omitempty"`
	TechnicalMetrics  MarketSummaryTechnicalMetrics `json:"technicalMetrics,omitempty"`
	TechnicalSnapshot string                        `json:"technicalSnapshot,omitempty"`
	EvidenceSources   []AIEvidenceReference         `json:"evidenceSources,omitempty"`
	PositiveSignals   []string                      `json:"positiveSignals,omitempty"`
	NegativeSignals   []string                      `json:"negativeSignals,omitempty"`
	VerdictHints      []string                      `json:"verdictHints,omitempty"`
	FeasiblePlans     []MarketSummaryFeasiblePlan   `json:"feasiblePlans,omitempty"`
	VerifiedAt        time.Time                     `json:"verifiedAt"`
}

type MarketSummarySupplementRequest struct {
	FailureSummary     []MarketSummaryBlockedReasonItem         `json:"failureSummary,omitempty"`
	RemainingVerified  []MarketSummaryVerifiedCandidateSnapshot `json:"remainingVerified,omitempty"`
	ExcludedToday      []string                                 `json:"excludedToday,omitempty"`
	RepairableFailures []MarketSummaryTradePlanRepairCandidate  `json:"repairableFailures,omitempty"`
	TargetProduction   int                                      `json:"targetProduction,omitempty"`
	CurrentProduction  int                                      `json:"currentProduction,omitempty"`
}

type MarketSummaryRecommendSaveOptions struct {
	NewRecordLimit        int
	ProductionLimit       int
	RepairableFailures    []MarketSummaryTradePlanRepairCandidate
	RequireVerifiedRepair bool
}
