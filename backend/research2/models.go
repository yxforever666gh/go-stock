package research2

import "time"

const InitialCash = 12000.0

type AnalysisRun struct {
	ScheduledSlot string     `json:"scheduledSlot" gorm:"not null;default:'09:50';uniqueIndex:idx_research2_runs_date_attempt,priority:2"`
	Slot          string     `json:"slot" gorm:"index"`
	Published     bool       `json:"published" gorm:"not null;default:false"`
	ArchiveReason string     `json:"archiveReason"`
	PersistedAt   *time.Time `json:"persistedAt,omitempty"`

	ID                     uint            `json:"id" gorm:"primaryKey"`
	RunID                  string          `json:"runId" gorm:"size:36;uniqueIndex;not null"`
	TradingDate            string          `json:"tradingDate" gorm:"size:10;uniqueIndex:idx_research2_runs_date_attempt,priority:1;not null"`
	AttemptNo              int             `json:"attemptNo" gorm:"not null;default:1;uniqueIndex:idx_research2_runs_date_attempt,priority:3"`
	ChainID                string          `json:"chainId,omitempty" gorm:"size:36;index"`
	ParentRunID            string          `json:"parentRunId,omitempty" gorm:"size:36;index"`
	TriggerSource          string          `json:"triggerSource,omitempty" gorm:"size:32;index;not null;default:'legacy-unversioned'"`
	RequestedSlots         int             `json:"requestedSlots" gorm:"not null;default:0"`
	PrimaryCount           int             `json:"primaryCount" gorm:"not null;default:0"`
	StandbyCount           int             `json:"standbyCount" gorm:"not null;default:0"`
	ScheduledFor           time.Time       `json:"scheduledFor" gorm:"index;not null"`
	StartedAt              time.Time       `json:"startedAt" gorm:"index;not null"`
	EvidenceWindowStartAt  *time.Time      `json:"evidenceWindowStartAt" gorm:"column:evidence_window_start_at;index"`
	EvidenceCutoffAt       time.Time       `json:"evidenceCutoffAt"`
	EvidenceCoveragePct    *float64        `json:"evidenceCoveragePct,omitempty" gorm:"column:evidence_coverage_pct"`
	Degraded               *bool           `json:"degraded,omitempty" gorm:"column:degraded"`
	GeneratedAt            *time.Time      `json:"generatedAt" gorm:"index"`
	Status                 string          `json:"status" gorm:"size:32;index;not null"`
	ProviderName           string          `json:"providerName" gorm:"size:128"`
	ModelName              string          `json:"modelName" gorm:"size:128"`
	ReportMarkdown         string          `json:"reportMarkdown" gorm:"type:text"`
	SourceStatusJSON       string          `json:"sourceStatusJson" gorm:"type:text;not null;default:'[]'"`
	ModelAttemptLogJSON    string          `json:"modelAttemptLogJson" gorm:"type:text;not null;default:'[]'"`
	StrategyVersion        string          `json:"strategyVersion,omitempty" gorm:"column:strategy_version;size:64"`
	EvidenceProfileVersion string          `json:"evidenceProfileVersion,omitempty" gorm:"column:evidence_profile_version;size:64"`
	EvidenceSetID          string          `json:"evidenceSetId,omitempty" gorm:"column:evidence_set_id;size:36;index"`
	FailureReason          string          `json:"failureReason" gorm:"type:text"`
	RecommendationCount    int             `json:"recommendationCount"`
	OnTime                 bool            `json:"onTime"`
	CreatedAt              time.Time       `json:"createdAt"`
	UpdatedAt              time.Time       `json:"updatedAt"`
	EmailDeliveryStatus    string          `json:"emailDeliveryStatus,omitempty" gorm:"-"`
	EmailSentAt            *time.Time      `json:"emailSentAt,omitempty" gorm:"-"`
	EmailAttemptCount      int             `json:"emailAttemptCount,omitempty" gorm:"-"`
	EmailLastError         string          `json:"emailLastError,omitempty" gorm:"-"`
	ExecutionChain         *ExecutionChain `json:"executionChain,omitempty" gorm:"-"`
}

func (AnalysisRun) TableName() string { return "research2_analysis_runs" }

type AnalysisRunSummary struct {
	ScheduledSlot string     `json:"scheduledSlot"`
	Slot          string     `json:"slot"`
	Published     bool       `json:"published"`
	ArchiveReason string     `json:"archiveReason"`
	PersistedAt   *time.Time `json:"persistedAt,omitempty"`

	RunID                  string          `json:"runId"`
	TradingDate            string          `json:"tradingDate"`
	AttemptNo              int             `json:"attemptNo"`
	ChainID                string          `json:"chainId,omitempty"`
	ParentRunID            string          `json:"parentRunId,omitempty"`
	TriggerSource          string          `json:"triggerSource,omitempty"`
	RequestedSlots         int             `json:"requestedSlots"`
	PrimaryCount           int             `json:"primaryCount"`
	StandbyCount           int             `json:"standbyCount"`
	ScheduledFor           time.Time       `json:"scheduledFor"`
	StartedAt              time.Time       `json:"startedAt"`
	EvidenceWindowStartAt  *time.Time      `json:"evidenceWindowStartAt"`
	EvidenceCutoffAt       time.Time       `json:"evidenceCutoffAt"`
	EvidenceCoveragePct    *float64        `json:"evidenceCoveragePct,omitempty"`
	Degraded               *bool           `json:"degraded,omitempty"`
	GeneratedAt            *time.Time      `json:"generatedAt"`
	Status                 string          `json:"status"`
	ProviderName           string          `json:"providerName"`
	ModelName              string          `json:"modelName"`
	StrategyVersion        string          `json:"strategyVersion,omitempty"`
	EvidenceProfileVersion string          `json:"evidenceProfileVersion,omitempty"`
	EvidenceSetID          string          `json:"evidenceSetId,omitempty"`
	RecommendationCount    int             `json:"recommendationCount"`
	OnTime                 bool            `json:"onTime"`
	FailureReason          string          `json:"failureReason"`
	EmailDeliveryStatus    string          `json:"emailDeliveryStatus,omitempty"`
	EmailSentAt            *time.Time      `json:"emailSentAt,omitempty"`
	EmailAttemptCount      int             `json:"emailAttemptCount,omitempty"`
	EmailLastError         string          `json:"emailLastError,omitempty"`
	ExecutionChain         *ExecutionChain `json:"executionChain,omitempty"`
}

type ExecutionChain struct {
	Slot               string     `json:"slot" gorm:"not null;default:'09:50';uniqueIndex:idx_research2_execution_chains_trading_date,priority:2"`
	WinnerRunID        string     `json:"winnerRunId"`
	SellCompletedAt    *time.Time `json:"sellCompletedAt,omitempty"`
	AllocationBaseCash *float64   `json:"-" gorm:"column:allocation_base_cash"`

	ID           uint       `json:"id" gorm:"primaryKey"`
	ChainID      string     `json:"chainId" gorm:"size:36;uniqueIndex;not null"`
	TradingDate  string     `json:"tradingDate" gorm:"size:10;uniqueIndex:idx_research2_execution_chains_trading_date;not null"`
	ScheduledFor time.Time  `json:"scheduledFor" gorm:"not null"`
	Status       string     `json:"status" gorm:"size:32;index:idx_research2_execution_chains_status;not null"`
	TargetSlots  int        `json:"targetSlots" gorm:"not null;default:3"`
	FilledSlots  int        `json:"filledSlots" gorm:"not null;default:0"`
	RootRunID    string     `json:"rootRunId,omitempty" gorm:"size:36;index"`
	LatestRunID  string     `json:"latestRunId,omitempty" gorm:"size:36;index"`
	StopReason   string     `json:"stopReason,omitempty" gorm:"type:text"`
	StartedAt    time.Time  `json:"startedAt" gorm:"not null"`
	CompletedAt  *time.Time `json:"completedAt,omitempty" gorm:"index"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

func (ExecutionChain) TableName() string { return "research2_execution_chains" }

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
	Slot                string   `json:"slot" gorm:"index;not null;default:'09:50'"`
	LegacySlotException bool     `json:"legacySlotException"`
	BaselineValue       *float64 `json:"baselineValue,omitempty"`
	PeriodPnL           *float64 `json:"periodPnl,omitempty"`

	ID                        uint       `json:"id" gorm:"primaryKey"`
	RecommendationID          string     `json:"recommendationId" gorm:"size:36;uniqueIndex;not null"`
	AnalysisRunID             string     `json:"analysisRunId" gorm:"size:36;index;not null"`
	SelectionRole             string     `json:"selectionRole,omitempty" gorm:"size:24;not null;default:'legacy-unversioned';index:idx_research2_recommendations_role_rank,priority:1"`
	SelectionRank             int        `json:"selectionRank" gorm:"not null;default:0;index:idx_research2_recommendations_role_rank,priority:2"`
	DisplaySelectionRole      string     `json:"displaySelectionRole" gorm:"->;-:migration"`
	DisplaySelectionRank      int        `json:"displaySelectionRank" gorm:"->;-:migration"`
	ReplacesRecommendationID  string     `json:"replacesRecommendationId,omitempty" gorm:"size:36;index"`
	PromotionReason           string     `json:"promotionReason,omitempty" gorm:"type:text"`
	StockCode                 string     `json:"stockCode" gorm:"size:16;index;not null"`
	StockName                 string     `json:"stockName" gorm:"size:64;not null"`
	SignalAt                  time.Time  `json:"signalAt" gorm:"index;not null"`
	MarketScore               float64    `json:"marketScore"`
	SectorScore               float64    `json:"sectorScore"`
	StockScore                float64    `json:"stockScore"`
	CatalystScore             float64    `json:"catalystScore"`
	RiskDeduction             float64    `json:"riskDeduction"`
	FinalScore                float64    `json:"finalScore" gorm:"index"`
	ReferencePrice            float64    `json:"referencePrice"`
	BuyLower                  float64    `json:"buyLower"`
	BuyUpper                  float64    `json:"buyUpper"`
	EstimatedLotCost          float64    `json:"estimatedLotCost"`
	Summary                   string     `json:"summary" gorm:"type:text"`
	QuantData                 string     `json:"quantData" gorm:"type:text"`
	FreshCatalyst             string     `json:"freshCatalyst" gorm:"type:text"`
	OldBackground             string     `json:"oldBackground" gorm:"type:text"`
	MainRisk                  string     `json:"mainRisk" gorm:"type:text"`
	CancelConditions          string     `json:"cancelConditions" gorm:"type:text"`
	SourceRefs                string     `json:"sourceRefs" gorm:"type:text"`
	Status                    string     `json:"status" gorm:"size:32;index;not null"`
	Late                      bool       `json:"late"`
	TargetBuyAt               time.Time  `json:"targetBuyAt" gorm:"index;not null"`
	ExecutionFailureCode      string     `json:"executionFailureCode,omitempty" gorm:"size:48;index"`
	ExecutionQuotePrice       float64    `json:"executionQuotePrice"`
	ExecutionQuoteAt          *time.Time `json:"executionQuoteAt,omitempty" gorm:"index"`
	ExecutionLimitPrice       float64    `json:"executionLimitPrice"`
	ExecutionLimitDistancePct *float64   `json:"executionLimitDistancePct,omitempty"`
	BuyAt                     *time.Time `json:"buyAt" gorm:"index"`
	BuyMarketPrice            float64    `json:"buyMarketPrice"`
	BuyPrice                  float64    `json:"buyPrice"`
	Quantity                  int64      `json:"quantity"`
	BuyFees                   float64    `json:"buyFees"`
	CurrentPrice              float64    `json:"currentPrice,omitempty"`
	CurrentPriceAt            *time.Time `json:"currentPriceAt,omitempty"`
	TargetSellAt              *time.Time `json:"targetSellAt" gorm:"index"`
	SellAt                    *time.Time `json:"sellAt" gorm:"index"`
	SellMarketPrice           float64    `json:"sellMarketPrice"`
	SellPrice                 float64    `json:"sellPrice"`
	SellFees                  float64    `json:"sellFees"`
	NetPnL                    float64    `json:"netPnl"`
	NetYieldRate              float64    `json:"netYieldRate"`
	HitFiveBeforeSell         *bool      `json:"hitFiveBeforeSell"`
	HitLimitUpFullDay         *bool      `json:"hitLimitUpFullDay"`
	HitMinusThree             *bool      `json:"hitMinusThree"`
	MetricsFinalized          bool       `json:"metricsFinalized"`
	FailureReason             string     `json:"failureReason" gorm:"type:text"`
	CreatedAt                 time.Time  `json:"createdAt"`
	UpdatedAt                 time.Time  `json:"updatedAt"`
}

func (Recommendation) TableName() string { return "research2_recommendations" }

type Trade struct {
	Slot       string     `json:"slot" gorm:"index;not null;default:'09:50'"`
	QuoteAt    *time.Time `json:"quoteAt,omitempty"`
	PriceStale bool       `json:"priceStale"`

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
	PriceSource      string    `json:"priceSource,omitempty" gorm:"column:price_source;size:64"`
	ExecutionMode    string    `json:"executionMode,omitempty" gorm:"column:execution_mode;size:32"`
	CreatedAt        time.Time `json:"createdAt"`
}

func (Trade) TableName() string { return "research2_trades" }

type Account struct {
	Slot                  string     `json:"slot" gorm:"uniqueIndex;not null;default:'09:50'"`
	BaselineAt            *time.Time `json:"baselineAt,omitempty"`
	BaselineNetAssetValue float64    `json:"baselineNetAssetValue"`
	SeedCash              float64    `json:"seedCash"`

	ID          uint      `json:"id" gorm:"primaryKey"`
	InitialCash float64   `json:"initialCash" gorm:"not null"`
	Cash        float64   `json:"cash" gorm:"not null"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (Account) TableName() string { return "research2_accounts" }

type AccountSnapshot struct {
	Slot string `json:"slot" gorm:"index;not null;default:'09:50'"`

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

// AccountCapitalEvent records capital supplied to an isolated slot and the
// neutral legacy-pool adjustments required to partition the pre-slot history.
// Trading cash flows remain in research2_trades so contributions can never be
// mistaken for strategy profit.
type AccountCapitalEvent struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	EventID     string    `json:"eventId" gorm:"size:64;uniqueIndex;not null"`
	Slot        string    `json:"slot" gorm:"size:5;index:idx_research2_capital_events_slot_time,priority:1;not null"`
	EventType   string    `json:"eventType" gorm:"size:32;index;not null"`
	Amount      float64   `json:"amount" gorm:"not null"`
	External    bool      `json:"external" gorm:"not null"`
	Source      string    `json:"source" gorm:"size:64;not null"`
	EffectiveAt time.Time `json:"effectiveAt" gorm:"index:idx_research2_capital_events_slot_time,priority:2;not null"`
	TradingDate string    `json:"tradingDate" gorm:"size:10;index;not null"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (AccountCapitalEvent) TableName() string { return "research2_account_capital_events" }

// AccountLedgerSnapshot is the derived, versioned valuation curve used for
// account return and event-based drawdown. The older AccountSnapshot rows are
// retained as raw audit history and are never rewritten by a capital rebase.
type AccountLedgerSnapshot struct {
	ID                        uint      `json:"id" gorm:"primaryKey"`
	SnapshotID                string    `json:"snapshotId" gorm:"size:64;uniqueIndex;not null"`
	Slot                      string    `json:"slot" gorm:"size:5;index:idx_research2_ledger_snapshots_slot_time,priority:1;not null"`
	ValuedAt                  time.Time `json:"valuedAt" gorm:"index:idx_research2_ledger_snapshots_slot_time,priority:2;not null"`
	TradingDate               string    `json:"tradingDate" gorm:"size:10;index;not null"`
	SnapshotType              string    `json:"snapshotType" gorm:"size:32;index;not null"`
	Cash                      float64   `json:"cash" gorm:"not null"`
	PositionValue             float64   `json:"positionValue" gorm:"not null"`
	NetAssetValue             float64   `json:"netAssetValue" gorm:"not null"`
	CumulativeExternalCapital float64   `json:"cumulativeExternalCapital" gorm:"not null"`
	NetInternalTransfer       float64   `json:"netInternalTransfer" gorm:"not null"`
	NetProfit                 float64   `json:"netProfit" gorm:"not null"`
	CumulativeCapitalReturn   float64   `json:"cumulativeCapitalReturn" gorm:"not null"`
	ValuationBasis            string    `json:"valuationBasis" gorm:"size:32;not null"`
	CreatedAt                 time.Time `json:"createdAt"`
}

func (AccountLedgerSnapshot) TableName() string { return "research2_account_ledger_snapshots" }

type RecommendationDetail struct {
	Recommendation Recommendation `json:"recommendation"`
	Analysis       AnalysisRun    `json:"analysis"`
	Trades         []Trade        `json:"trades"`
}

type AccountOverview struct {
	Slot                  string     `json:"slot"`
	BaselineAt            *time.Time `json:"baselineAt,omitempty"`
	BaselineNetAssetValue float64    `json:"baselineNetAssetValue"`

	InitialCash   float64   `json:"initialCash"`
	Cash          float64   `json:"cash"`
	PositionValue float64   `json:"positionValue"`
	NetAssetValue float64   `json:"netAssetValue"`
	NetProfit     float64   `json:"netProfit"`
	ReturnRate    float64   `json:"returnRate"`
	OpenPositions int64     `json:"openPositions"`
	PendingBuys   int64     `json:"pendingBuys"`
	LastValuedAt  time.Time `json:"lastValuedAt"`

	InitialContribution       float64 `json:"initialContribution"`
	TopUpContribution         float64 `json:"topUpContribution"`
	CumulativeExternalCapital float64 `json:"cumulativeExternalCapital"`
	NetInternalTransfer       float64 `json:"netInternalTransfer"`
	CumulativeCapitalReturn   float64 `json:"cumulativeCapitalReturn"`
	ValuationBasis            string  `json:"valuationBasis"`
}

type Performance struct {
	AccountOverview
	ClosedTrades       int64                   `json:"closedTrades"`
	WinningTrades      int64                   `json:"winningTrades"`
	WinRate            *float64                `json:"winRate"`
	TotalFees          float64                 `json:"totalFees"`
	MaxDrawdown        *float64                `json:"maxDrawdown"`
	HitFiveCount       int64                   `json:"hitFiveCount"`
	HitLimitUpCount    int64                   `json:"hitLimitUpCount"`
	HitMinusThreeCount int64                   `json:"hitMinusThreeCount"`
	OnTimeReports      int64                   `json:"onTimeReports"`
	LateReports        int64                   `json:"lateReports"`
	Curve              []AccountLedgerSnapshot `json:"curve"`
}
