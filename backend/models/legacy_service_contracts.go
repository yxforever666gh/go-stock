package models

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/plugin/soft_delete"
)

// Settings is the application-facing configuration contract. Persistence and
// provider adapters may reuse it, but use cases no longer depend on those
// adapters for the type definition.
type Settings struct {
	gorm.Model
	TushareToken                string  `json:"tushareToken"`
	LocalPushEnable             bool    `json:"localPushEnable"`
	DingPushEnable              bool    `json:"dingPushEnable"`
	DingRobot                   string  `json:"dingRobot"`
	UpdateBasicInfoOnStart      bool    `json:"updateBasicInfoOnStart"`
	RefreshInterval             int64   `json:"refreshInterval"`
	OpenAiEnable                bool    `json:"-"`
	Prompt                      string  `json:"-"`
	CheckUpdate                 bool    `json:"checkUpdate"`
	QuestionTemplate            string  `json:"-"`
	CrawlTimeOut                int64   `json:"crawlTimeOut"`
	KDays                       int64   `json:"kDays"`
	EnableDanmu                 bool    `json:"enableDanmu"`
	BrowserPath                 string  `json:"browserPath"`
	EnableNews                  bool    `json:"enableNews"`
	DarkTheme                   bool    `json:"darkTheme"`
	BrowserPoolSize             int     `json:"browserPoolSize"`
	EnableFund                  bool    `json:"enableFund"`
	EnablePushNews              bool    `json:"enablePushNews"`
	EnableOnlyPushRedNews       bool    `json:"enableOnlyPushRedNews"`
	HttpProxy                   string  `json:"httpProxy"`
	HttpProxyEnabled            bool    `json:"httpProxyEnabled"`
	ForceNoProxyForFetch        bool    `json:"forceNoProxyForFetch" gorm:"default:true"`
	QgqpBId                     string  `json:"qgqpBId" gorm:"column:qgqp_b_id"`
	AIAnalysisEnabled           bool    `json:"-" gorm:"default:true"`
	AICapitalDeploymentEnabled  bool    `json:"aiCapitalDeploymentEnabled" gorm:"default:true"`
	AITargetCapitalUtilization  float64 `json:"aiTargetCapitalUtilization" gorm:"default:0.9"`
	AIMaxImmediateBuysPerRun    int     `json:"aiMaxImmediateBuysPerRun" gorm:"default:2"`
	AIReanalysisIntervalMinutes int     `json:"aiReanalysisIntervalMinutes" gorm:"default:30"`
	Research2AutoEnabled        bool    `json:"research2AutoEnabled" gorm:"default:true"`
	Research2EmailEnabled       bool    `json:"research2EmailEnabled"`
	Research2EmailTo            string  `json:"research2EmailTo" gorm:"type:text"`
	Research2EmailFrom          string  `json:"research2EmailFrom"`
	Research2EmailSMTPHost      string  `json:"research2EmailSmtpHost"`
	Research2EmailSMTPPort      int     `json:"research2EmailSmtpPort"`
	Research2EmailSMTPUser      string  `json:"research2EmailSmtpUsername"`
	Research2EmailSMTPPass      string  `json:"research2EmailSmtpPassword"`
	AIAnalysisConfigID          uint    `json:"aiAnalysisConfigId"`
	AIAnalysisTimes             string  `json:"aiAnalysisTimes" gorm:"default:'09:30,11:30,14:30'"`
	AIReviewStartTime           string  `json:"aiReviewStartTime" gorm:"default:'09:50'"`
	AIReviewIntervalMinutes     int     `json:"aiReviewIntervalMinutes" gorm:"default:15"`
	MinuteProviderMode          string  `json:"minuteProviderMode" gorm:"default:'public'"`
	MinuteProviderOrder         string  `json:"-" gorm:"default:'tencent,sina,akshare,private'"`
	MinuteLongHistoryHint       bool    `json:"minuteLongHistoryHintEnabled" gorm:"column:minute_long_history_hint_enabled;default:true"`
	PrivateMinuteEnabled        bool    `json:"privateMinuteEnabled"`
	PrivateMinuteBaseURL        string  `json:"privateMinuteBaseUrl"`
	PrivateMinuteAPIKey         string  `json:"privateMinuteApiKey"`
	PrivateMinuteTimeoutSec     int     `json:"privateMinuteTimeoutSec"`
	PrivateMinuteMinInterval    int     `json:"privateMinuteMinIntervalMs"`
	PrivateMinuteProxyMode      string  `json:"privateMinuteProxyMode" gorm:"default:'disable'"`
	PrivateMinuteLevel          string  `json:"privateMinuteLevel" gorm:"default:'1min'"`
	AkshareEnabled              bool    `json:"akshareEnabled" gorm:"default:true"`
	SinaMinuteEnabled           bool    `json:"sinaMinuteEnabled" gorm:"default:true"`
	TencentMinuteEnabled        bool    `json:"tencentMinuteEnabled" gorm:"default:true"`
	EastmoneyMinuteEnabled      bool    `json:"eastmoneyMinuteEnabled" gorm:"default:true"`
	AkshareMinuteSourceMode     string  `json:"akshareMinuteSourceMode" gorm:"default:'auto'"`
	ExperimentalEvidenceEnabled bool    `json:"experimentalEvidenceEnabled" gorm:"column:experimental_evidence_enabled;default:false"`
}

func (Settings) TableName() string { return "settings" }

type AIConfig struct {
	ID               uint `gorm:"primarykey"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Sort             int     `json:"sort" gorm:"index"`
	Disabled         bool    `json:"disabled"`
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

const (
	AIAPIProtocolChatCompletions  = "chat_completions"
	AIAPIProtocolOpenAIResponses  = "openai_responses"
	AIAPIProtocolAnthropicMessage = "anthropic_messages"
)

// NormalizeAIAPIProtocol keeps the public configuration protocol bounded to
// the provider contracts supported by the application.
func NormalizeAIAPIProtocol(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case AIAPIProtocolOpenAIResponses:
		return AIAPIProtocolOpenAIResponses
	case AIAPIProtocolAnthropicMessage:
		return AIAPIProtocolAnthropicMessage
	default:
		return AIAPIProtocolChatCompletions
	}
}

// AIModelTestResult is the stable RPC response for a provider connectivity
// check. It deliberately excludes configuration secrets.
type AIModelTestResult struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	Protocol       string `json:"protocol"`
	Model          string `json:"model"`
	LatencyMs      int64  `json:"latencyMs"`
	ContentPreview string `json:"contentPreview"`
}

type SettingConfig struct {
	*Settings
	AiConfigs              []*AIConfig `json:"aiConfigs"`
	MinuteProviderOrder    []string    `json:"minuteProviderOrder" gorm:"-"`
	AIAnalysisAutoEnabled  *bool       `json:"aiAnalysisAutoEnabled" gorm:"-"`
	LegacyAIAnalysisEnable *bool       `json:"aiAnalysisEnabled,omitempty" gorm:"-"`
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
