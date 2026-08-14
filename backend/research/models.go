package research

import "time"

const (
	AppVersion       = "1.6.0"
	InitialCash      = 100000.0
	MaxCashPerTrade  = 50000.0
	DefaultCheckMins = 15
)

type AnalysisRun struct {
	ID                  uint       `json:"id" gorm:"primaryKey"`
	RunID               string     `json:"runId" gorm:"size:36;uniqueIndex;not null"`
	ScheduledFor        time.Time  `json:"scheduledFor" gorm:"index"`
	StartedAt           time.Time  `json:"startedAt" gorm:"index"`
	CompletedAt         *time.Time `json:"completedAt"`
	Status              string     `json:"status" gorm:"size:32;index;not null"`
	AIConfigID          uint       `json:"aiConfigId"`
	ProviderName        string     `json:"providerName" gorm:"size:128"`
	ModelName           string     `json:"modelName" gorm:"size:128"`
	MarketReport        string     `json:"marketReport" gorm:"type:text"`
	SectorReport        string     `json:"sectorReport" gorm:"type:text"`
	StockReport         string     `json:"stockReport" gorm:"type:text"`
	FinalReport         string     `json:"finalReport" gorm:"type:text"`
	SourceStatusJSON    string     `json:"sourceStatusJson" gorm:"type:text"`
	FailureReason       string     `json:"failureReason" gorm:"type:text"`
	RecommendationCount int        `json:"recommendationCount"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

func (AnalysisRun) TableName() string { return "research_v160_analysis_runs" }

type Recommendation struct {
	ID                  uint       `json:"id" gorm:"primaryKey"`
	RecommendationID    string     `json:"recommendationId" gorm:"size:36;uniqueIndex;not null"`
	AnalysisRunID       string     `json:"analysisRunId" gorm:"size:36;index;not null"`
	StockCode           string     `json:"stockCode" gorm:"size:16;index;not null"`
	StockName           string     `json:"stockName" gorm:"size:64;not null"`
	SignalAt            time.Time  `json:"signalAt" gorm:"index;not null"`
	AISummary           string     `json:"aiSummary" gorm:"type:text"`
	ActivationCondition string     `json:"activationCondition" gorm:"type:text"`
	MainRisk            string     `json:"mainRisk" gorm:"type:text"`
	SourceRefs          string     `json:"sourceRefs" gorm:"type:text"`
	Status              string     `json:"status" gorm:"size:32;index;not null"`
	PreviousResponseID  string     `json:"previousResponseId" gorm:"size:160"`
	NextCheckAt         *time.Time `json:"nextCheckAt" gorm:"index"`
	ActivatedAt         *time.Time `json:"activatedAt"`
	ActivationPrice     float64    `json:"activationPrice"`
	Quantity            int64      `json:"quantity"`
	ClosedAt            *time.Time `json:"closedAt"`
	ClosePrice          float64    `json:"closePrice"`
	TotalFees           float64    `json:"totalFees"`
	NetPnL              float64    `json:"netPnl"`
	NetYieldRate        float64    `json:"netYieldRate"`
	LastDecision        string     `json:"lastDecision" gorm:"size:32"`
	LastDecisionAt      *time.Time `json:"lastDecisionAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

func (Recommendation) TableName() string { return "research_v160_recommendations" }

type LifecycleMessage struct {
	ID                 uint      `json:"id" gorm:"primaryKey"`
	RecommendationID   string    `json:"recommendationId" gorm:"size:36;uniqueIndex:idx_v160_message_seq,priority:1;not null"`
	Sequence           int       `json:"sequence" gorm:"uniqueIndex:idx_v160_message_seq,priority:2;not null"`
	Role               string    `json:"role" gorm:"size:16;not null"`
	Phase              string    `json:"phase" gorm:"size:32;index"`
	Content            string    `json:"content" gorm:"type:text;not null"`
	ResponseID         string    `json:"responseId" gorm:"size:160"`
	PreviousResponseID string    `json:"previousResponseId" gorm:"size:160"`
	Model              string    `json:"model" gorm:"size:128"`
	CreatedAt          time.Time `json:"createdAt" gorm:"index"`
}

func (LifecycleMessage) TableName() string { return "research_v160_lifecycle_messages" }

type DecisionEvent struct {
	ID               uint       `json:"id" gorm:"primaryKey"`
	EventID          string     `json:"eventId" gorm:"size:36;uniqueIndex;not null"`
	RecommendationID string     `json:"recommendationId" gorm:"size:36;index;not null"`
	DecisionType     string     `json:"decisionType" gorm:"size:32;index;not null"`
	DecidedAt        time.Time  `json:"decidedAt" gorm:"index;not null"`
	AIResponse       string     `json:"aiResponse" gorm:"type:text"`
	Reason           string     `json:"reason" gorm:"type:text"`
	QuotePrice       float64    `json:"quotePrice"`
	QuoteAt          *time.Time `json:"quoteAt"`
	CreatedAt        time.Time  `json:"createdAt"`
}

func (DecisionEvent) TableName() string { return "research_v160_decision_events" }

type SimulatedAccount struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	InitialCash float64   `json:"initialCash" gorm:"not null"`
	Cash        float64   `json:"cash" gorm:"not null"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (SimulatedAccount) TableName() string { return "research_v160_simulated_accounts" }

type SimulatedTrade struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	TradeID          string    `json:"tradeId" gorm:"size:36;uniqueIndex;not null"`
	RecommendationID string    `json:"recommendationId" gorm:"size:36;index;not null"`
	StockCode        string    `json:"stockCode" gorm:"size:16;index;not null"`
	Side             string    `json:"side" gorm:"size:8;index;not null"`
	TradedAt         time.Time `json:"tradedAt" gorm:"index;not null"`
	MarketPrice      float64   `json:"marketPrice"`
	ExecutionPrice   float64   `json:"executionPrice"`
	Quantity         int64     `json:"quantity"`
	Notional         float64   `json:"notional"`
	Commission       float64   `json:"commission"`
	StampDuty        float64   `json:"stampDuty"`
	TransferFee      float64   `json:"transferFee"`
	SlippageAmount   float64   `json:"slippageAmount"`
	TotalFees        float64   `json:"totalFees"`
	NetCashFlow      float64   `json:"netCashFlow"`
	CreatedAt        time.Time `json:"createdAt"`
}

func (SimulatedTrade) TableName() string { return "research_v160_simulated_trades" }

type Position struct {
	ID                uint       `json:"id" gorm:"primaryKey"`
	RecommendationID  string     `json:"recommendationId" gorm:"size:36;uniqueIndex;not null"`
	StockCode         string     `json:"stockCode" gorm:"size:16;index;not null"`
	StockName         string     `json:"stockName" gorm:"size:64;not null"`
	Market            string     `json:"market" gorm:"size:16;not null"`
	Quantity          int64      `json:"quantity"`
	EntryAt           time.Time  `json:"entryAt" gorm:"index"`
	EntryPrice        float64    `json:"entryPrice"`
	BuyFees           float64    `json:"buyFees"`
	CurrentPrice      float64    `json:"currentPrice"`
	CurrentPriceAt    *time.Time `json:"currentPriceAt"`
	Status            string     `json:"status" gorm:"size:16;index;not null"`
	ExitAt            *time.Time `json:"exitAt"`
	ExitPrice         float64    `json:"exitPrice"`
	SellFees          float64    `json:"sellFees"`
	NetPnL            float64    `json:"netPnl"`
	NetYieldRate      float64    `json:"netYieldRate" gorm:"-"`
	EstimatedSellFees float64    `json:"estimatedSellFees" gorm:"-"`
	NetSellValue      float64    `json:"netSellValue" gorm:"-"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

func (Position) TableName() string { return "research_v160_positions" }

type AccountOverview struct {
	InitialCash   float64    `json:"initialCash"`
	Cash          float64    `json:"cash"`
	PositionValue float64    `json:"positionValue"`
	NetAssetValue float64    `json:"netAssetValue"`
	NetProfit     float64    `json:"netProfit"`
	NetYieldRate  float64    `json:"netYieldRate"`
	ValuedAt      time.Time  `json:"valuedAt"`
	Positions     []Position `json:"positions"`
}

type RecommendationDetail struct {
	Recommendation Recommendation     `json:"recommendation"`
	Analysis       AnalysisRun        `json:"analysis"`
	Messages       []LifecycleMessage `json:"messages"`
	Decisions      []DecisionEvent    `json:"decisions"`
	Trades         []SimulatedTrade   `json:"trades"`
	Position       *Position          `json:"position,omitempty"`
}
