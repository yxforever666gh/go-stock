package research

import "time"

const (
	AppVersion      = "1.6.6"
	InitialCash     = 100000.0
	MaxCashPerTrade = 50000.0
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
	ModelAttemptLogJSON string     `json:"modelAttemptLogJson" gorm:"type:text;not null;default:'[]'"`
	FailureReason       string     `json:"failureReason" gorm:"type:text"`
	RecommendationCount int        `json:"recommendationCount"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// ModelAttemptRecord is the persisted, sanitized state of one provider call.
// It deliberately excludes prompts, response bodies, headers and credentials.
type ModelAttemptRecord struct {
	ID             string     `json:"id"`
	Phase          string     `json:"phase"`
	ConfigID       uint       `json:"configId"`
	ProviderName   string     `json:"providerName"`
	ModelName      string     `json:"modelName"`
	APIProtocol    string     `json:"apiProtocol"`
	Attempt        int        `json:"attempt"`
	MaxAttempts    int        `json:"maxAttempts"`
	StartedAt      time.Time  `json:"startedAt"`
	LastActivityAt *time.Time `json:"lastActivityAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	DurationMS     int64      `json:"durationMs"`
	Status         string     `json:"status"`
	LastEventType  string     `json:"lastEventType,omitempty"`
	HTTPStatus     int        `json:"httpStatus,omitempty"`
	ErrorCategory  string     `json:"errorCategory,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	Retryable      bool       `json:"retryable"`
	NextAction     string     `json:"nextAction,omitempty"`
}

func (AnalysisRun) TableName() string { return "research_v160_analysis_runs" }

// AnalysisRunSummary is the lightweight list representation. Full stage reports
// and source documents are only returned by the detail endpoint.
type AnalysisRunSummary struct {
	RunID               string     `json:"runId"`
	ScheduledFor        time.Time  `json:"scheduledFor"`
	StartedAt           time.Time  `json:"startedAt"`
	CompletedAt         *time.Time `json:"completedAt"`
	Status              string     `json:"status"`
	ProviderName        string     `json:"providerName"`
	ModelName           string     `json:"modelName"`
	RecommendationCount int        `json:"recommendationCount"`
	FailureReason       string     `json:"failureReason"`
	SourceCount         int        `json:"sourceCount"`
	FailedSourceCount   int        `json:"failedSourceCount"`
}

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
	DataPauseSeconds    int64      `json:"dataPauseSeconds" gorm:"not null;default:0"`
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
	SourceRefs       string     `json:"sourceRefs" gorm:"type:text"`
	DataStatus       string     `json:"dataStatus" gorm:"size:32"`
	CreatedAt        time.Time  `json:"createdAt"`
}

func (DecisionEvent) TableName() string { return "research_v160_decision_events" }

// LifecycleObservation is the bounded evidence snapshot collected immediately
// before one lifecycle decision. Large raw upstream responses are deliberately
// not persisted; EvidenceJSON contains compact per-source summaries.
type LifecycleObservation struct {
	ID                 uint      `json:"id" gorm:"primaryKey"`
	ObservationID      string    `json:"observationId" gorm:"size:36;uniqueIndex;not null"`
	RecommendationID   string    `json:"recommendationId" gorm:"size:36;index;not null"`
	Phase              string    `json:"phase" gorm:"size:32;index;not null"`
	WindowFrom         time.Time `json:"windowFrom" gorm:"index"`
	ObservedAt         time.Time `json:"observedAt" gorm:"index;not null"`
	Status             string    `json:"status" gorm:"size:32;index;not null"`
	QuoteJSON          string    `json:"quoteJson" gorm:"type:text;not null;default:'{}'"`
	MinuteSummaryJSON  string    `json:"minuteSummaryJson" gorm:"type:text;not null;default:'{}'"`
	EvidenceJSON       string    `json:"evidenceJson" gorm:"type:text;not null;default:'[]'"`
	SourceStatusJSON   string    `json:"sourceStatusJson" gorm:"type:text;not null;default:'[]'"`
	CriticalFailure    string    `json:"criticalFailure" gorm:"type:text"`
	ContentFingerprint string    `json:"contentFingerprint" gorm:"size:64;index"`
	ModelInvoked       bool      `json:"modelInvoked" gorm:"not null;default:false"`
	CreatedAt          time.Time `json:"createdAt"`
}

func (LifecycleObservation) TableName() string { return "research_v160_lifecycle_observations" }

type LifecycleEvidenceSource struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Status      string    `json:"status"`
	CollectedAt time.Time `json:"collectedAt"`
	Content     string    `json:"content,omitempty"`
	Error       string    `json:"error,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

type MinuteWindowSummary struct {
	Minutes      int     `json:"minutes"`
	Bars         int     `json:"bars"`
	ReturnRate   float64 `json:"returnRate"`
	High         float64 `json:"high"`
	Low          float64 `json:"low"`
	Volume       float64 `json:"volume"`
	Amount       float64 `json:"amount"`
	AveragePrice float64 `json:"averagePrice"`
}

type MinuteEvidenceSummary struct {
	TradingDate string                `json:"tradingDate"`
	LatestAt    time.Time             `json:"latestAt"`
	LatestPrice float64               `json:"latestPrice"`
	TotalBars   int                   `json:"totalBars"`
	Windows     []MinuteWindowSummary `json:"windows"`
}

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
	Recommendation Recommendation         `json:"recommendation"`
	Analysis       AnalysisRun            `json:"analysis"`
	Messages       []LifecycleMessage     `json:"messages"`
	Decisions      []DecisionEvent        `json:"decisions"`
	Observations   []LifecycleObservation `json:"observations"`
	Trades         []SimulatedTrade       `json:"trades"`
	Position       *Position              `json:"position,omitempty"`
	// Retained as zero-valued compatibility fields for pre-1.6.5 clients and
	// historical records. Direct-buy recommendations no longer have an
	// activation window.
	ActivationTradingElapsedSeconds int64 `json:"activationTradingElapsedSeconds"`
	ActivationRemainingSeconds      int64 `json:"activationRemainingSeconds"`
}
