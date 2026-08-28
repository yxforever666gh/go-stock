package research2

import "time"

const (
	InitialCash = 12000.0
	LotSize     = int64(100)
)

type AnalysisRun struct {
	ID                     uint       `json:"id" gorm:"primaryKey"`
	RunID                  string     `json:"runId" gorm:"size:36;uniqueIndex;not null"`
	TradingDate            string     `json:"tradingDate" gorm:"size:10;uniqueIndex;not null"`
	ScheduledFor           time.Time  `json:"scheduledFor" gorm:"index;not null"`
	StartedAt              time.Time  `json:"startedAt" gorm:"index;not null"`
	EvidenceCutoffAt       time.Time  `json:"evidenceCutoffAt"`
	GeneratedAt            *time.Time `json:"generatedAt" gorm:"index"`
	Status                 string     `json:"status" gorm:"size:32;index;not null"`
	ProviderName           string     `json:"providerName" gorm:"size:128"`
	ModelName              string     `json:"modelName" gorm:"size:128"`
	ReportMarkdown         string     `json:"reportMarkdown" gorm:"type:text"`
	SourceStatusJSON       string     `json:"sourceStatusJson" gorm:"type:text;not null;default:'[]'"`
	ModelAttemptLogJSON    string     `json:"modelAttemptLogJson" gorm:"type:text;not null;default:'[]'"`
	StrategyVersion        string     `json:"strategyVersion,omitempty" gorm:"column:strategy_version;size:64"`
	EvidenceProfileVersion string     `json:"evidenceProfileVersion,omitempty" gorm:"column:evidence_profile_version;size:64"`
	EvidenceSetID          string     `json:"evidenceSetId,omitempty" gorm:"column:evidence_set_id;size:36;index"`
	FailureReason          string     `json:"failureReason" gorm:"type:text"`
	RecommendationCount    int        `json:"recommendationCount"`
	OnTime                 bool       `json:"onTime"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
	EmailDeliveryStatus    string     `json:"emailDeliveryStatus,omitempty" gorm:"-"`
	EmailSentAt            *time.Time `json:"emailSentAt,omitempty" gorm:"-"`
	EmailAttemptCount      int        `json:"emailAttemptCount,omitempty" gorm:"-"`
	EmailLastError         string     `json:"emailLastError,omitempty" gorm:"-"`
}

func (AnalysisRun) TableName() string { return "research2_analysis_runs" }

type AnalysisRunSummary struct {
	RunID                  string     `json:"runId"`
	TradingDate            string     `json:"tradingDate"`
	ScheduledFor           time.Time  `json:"scheduledFor"`
	EvidenceCutoffAt       time.Time  `json:"evidenceCutoffAt"`
	GeneratedAt            *time.Time `json:"generatedAt"`
	Status                 string     `json:"status"`
	ProviderName           string     `json:"providerName"`
	ModelName              string     `json:"modelName"`
	StrategyVersion        string     `json:"strategyVersion,omitempty"`
	EvidenceProfileVersion string     `json:"evidenceProfileVersion,omitempty"`
	EvidenceSetID          string     `json:"evidenceSetId,omitempty"`
	RecommendationCount    int        `json:"recommendationCount"`
	OnTime                 bool       `json:"onTime"`
	FailureReason          string     `json:"failureReason"`
	EmailDeliveryStatus    string     `json:"emailDeliveryStatus,omitempty"`
	EmailSentAt            *time.Time `json:"emailSentAt,omitempty"`
	EmailAttemptCount      int        `json:"emailAttemptCount,omitempty"`
	EmailLastError         string     `json:"emailLastError,omitempty"`
}

type EmailDelivery struct {
	ID            uint       `json:"id" gorm:"primaryKey"`
	AnalysisRunID string     `json:"analysisRunId" gorm:"size:36;uniqueIndex;not null"`
	Status        string     `json:"status" gorm:"size:32;index;not null"`
	AttemptCount  int        `json:"attemptCount"`
	NextAttemptAt *time.Time `json:"nextAttemptAt" gorm:"index"`
	SentAt        *time.Time `json:"sentAt" gorm:"index"`
	Recipients    string     `json:"recipients" gorm:"type:text;not null"`
	Sender        string     `json:"sender" gorm:"type:text;not null"`
	Subject       string     `json:"subject" gorm:"size:255;not null"`
	Body          string     `json:"body" gorm:"type:text;not null"`
	MessageID     string     `json:"messageId" gorm:"size:255;not null"`
	LastError     string     `json:"lastError" gorm:"type:text"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func (EmailDelivery) TableName() string { return "research2_email_deliveries" }

type Recommendation struct {
	ID                uint       `json:"id" gorm:"primaryKey"`
	RecommendationID  string     `json:"recommendationId" gorm:"size:36;uniqueIndex;not null"`
	AnalysisRunID     string     `json:"analysisRunId" gorm:"size:36;index;not null"`
	Rank              int        `json:"rank"`
	StockCode         string     `json:"stockCode" gorm:"size:16;index;not null"`
	StockName         string     `json:"stockName" gorm:"size:64;not null"`
	SignalAt          time.Time  `json:"signalAt" gorm:"index;not null"`
	MarketScore       float64    `json:"marketScore"`
	SectorScore       float64    `json:"sectorScore"`
	StockScore        float64    `json:"stockScore"`
	CatalystScore     float64    `json:"catalystScore"`
	RiskDeduction     float64    `json:"riskDeduction"`
	FinalScore        float64    `json:"finalScore" gorm:"index"`
	ReferencePrice    float64    `json:"referencePrice"`
	BuyLower          float64    `json:"buyLower"`
	BuyUpper          float64    `json:"buyUpper"`
	EstimatedLotCost  float64    `json:"estimatedLotCost"`
	Summary           string     `json:"summary" gorm:"type:text"`
	QuantData         string     `json:"quantData" gorm:"type:text"`
	FreshCatalyst     string     `json:"freshCatalyst" gorm:"type:text"`
	OldBackground     string     `json:"oldBackground" gorm:"type:text"`
	MainRisk          string     `json:"mainRisk" gorm:"type:text"`
	CancelConditions  string     `json:"cancelConditions" gorm:"type:text"`
	SourceRefs        string     `json:"sourceRefs" gorm:"type:text"`
	Status            string     `json:"status" gorm:"size:32;index;not null"`
	Late              bool       `json:"late"`
	TargetBuyAt       time.Time  `json:"targetBuyAt" gorm:"index;not null"`
	BuyAt             *time.Time `json:"buyAt" gorm:"index"`
	BuyMarketPrice    float64    `json:"buyMarketPrice"`
	BuyPrice          float64    `json:"buyPrice"`
	Quantity          int64      `json:"quantity"`
	BuyFees           float64    `json:"buyFees"`
	TargetSellAt      *time.Time `json:"targetSellAt" gorm:"index"`
	SellAt            *time.Time `json:"sellAt" gorm:"index"`
	SellMarketPrice   float64    `json:"sellMarketPrice"`
	SellPrice         float64    `json:"sellPrice"`
	SellFees          float64    `json:"sellFees"`
	NetPnL            float64    `json:"netPnl"`
	NetYieldRate      float64    `json:"netYieldRate"`
	HitFiveBeforeSell *bool      `json:"hitFiveBeforeSell"`
	HitLimitUpFullDay *bool      `json:"hitLimitUpFullDay"`
	HitMinusThree     *bool      `json:"hitMinusThree"`
	MetricsFinalized  bool       `json:"metricsFinalized"`
	FailureReason     string     `json:"failureReason" gorm:"type:text"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

func (Recommendation) TableName() string { return "research2_recommendations" }

type Trade struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	TradeID          string    `json:"tradeId" gorm:"size:36;uniqueIndex;not null"`
	RecommendationID string    `json:"recommendationId" gorm:"size:36;index;not null"`
	Side             string    `json:"side" gorm:"size:8;index;not null"`
	TradedAt         time.Time `json:"tradedAt" gorm:"index;not null"`
	MarketPrice      float64   `json:"marketPrice"`
	ExecutionPrice   float64   `json:"executionPrice"`
	Quantity         int64     `json:"quantity"`
	Commission       float64   `json:"commission"`
	StampDuty        float64   `json:"stampDuty"`
	TransferFee      float64   `json:"transferFee"`
	SlippageAmount   float64   `json:"slippageAmount"`
	NetCashFlow      float64   `json:"netCashFlow"`
	CreatedAt        time.Time `json:"createdAt"`
}

func (Trade) TableName() string { return "research2_trades" }

type Account struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	InitialCash float64   `json:"initialCash" gorm:"not null"`
	Cash        float64   `json:"cash" gorm:"not null"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (Account) TableName() string { return "research2_accounts" }

type AccountSnapshot struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	SnapshotID    string    `json:"snapshotId" gorm:"size:36;uniqueIndex;not null"`
	ValuedAt      time.Time `json:"valuedAt" gorm:"index;not null"`
	TradingDate   string    `json:"tradingDate" gorm:"size:10;index;not null"`
	SnapshotType  string    `json:"snapshotType" gorm:"size:32;index;not null"`
	Cash          float64   `json:"cash"`
	PositionValue float64   `json:"positionValue"`
	NetAssetValue float64   `json:"netAssetValue"`
	NetProfit     float64   `json:"netProfit"`
	ReturnRate    float64   `json:"returnRate"`
	CreatedAt     time.Time `json:"createdAt"`
}

func (AccountSnapshot) TableName() string { return "research2_account_snapshots" }

type RecommendationDetail struct {
	Recommendation Recommendation `json:"recommendation"`
	Analysis       AnalysisRun    `json:"analysis"`
	Trades         []Trade        `json:"trades"`
}

type AccountOverview struct {
	InitialCash   float64   `json:"initialCash"`
	Cash          float64   `json:"cash"`
	PositionValue float64   `json:"positionValue"`
	NetAssetValue float64   `json:"netAssetValue"`
	NetProfit     float64   `json:"netProfit"`
	ReturnRate    float64   `json:"returnRate"`
	OpenPositions int64     `json:"openPositions"`
	PendingBuys   int64     `json:"pendingBuys"`
	LastValuedAt  time.Time `json:"lastValuedAt"`
}

type Performance struct {
	AccountOverview
	ClosedTrades       int64             `json:"closedTrades"`
	WinningTrades      int64             `json:"winningTrades"`
	WinRate            *float64          `json:"winRate"`
	TotalFees          float64           `json:"totalFees"`
	MaxDrawdown        *float64          `json:"maxDrawdown"`
	HitFiveCount       int64             `json:"hitFiveCount"`
	HitLimitUpCount    int64             `json:"hitLimitUpCount"`
	HitMinusThreeCount int64             `json:"hitMinusThreeCount"`
	OnTimeReports      int64             `json:"onTimeReports"`
	LateReports        int64             `json:"lateReports"`
	Curve              []AccountSnapshot `json:"curve"`
}
