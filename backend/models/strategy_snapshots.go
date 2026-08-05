package models

import "time"

// StrategyRunSnapshot is the immutable, cache-backed input envelope for one
// strategy evaluation. RunID is the stable identity supplied by the strategy
// engine; a new evaluation must use a new RunID instead of updating this row.
type StrategyRunSnapshot struct {
	ID              uint       `json:"id" gorm:"primarykey"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	RunID           string     `json:"runId" gorm:"size:96;not null;uniqueIndex"`
	StrategyVersion string     `json:"strategyVersion" gorm:"size:32;not null;index:idx_strategy_run_version_date,priority:1"`
	TradeDate       string     `json:"tradeDate" gorm:"size:10;not null;index:idx_strategy_run_version_date,priority:2"`
	RunSlot         string     `json:"runSlot" gorm:"size:32;index:idx_strategy_run_version_date,priority:3"`
	StartedAt       time.Time  `json:"startedAt" gorm:"not null;index"`
	AsOf            time.Time  `json:"asOf" gorm:"not null;index"`
	DataCutoffAt    time.Time  `json:"dataCutoffAt" gorm:"not null;index"`
	DecisionAt      time.Time  `json:"decisionAt" gorm:"not null;index"`
	GeneratedAt     time.Time  `json:"generatedAt" gorm:"not null"`
	ValidFromAt     *time.Time `json:"validFromAt,omitempty" gorm:"index"`
	Mode            string     `json:"mode" gorm:"size:32;index"`
	ConfigHash      string     `json:"configHash" gorm:"size:128;index"`
	InputHash       string     `json:"inputHash" gorm:"size:128;index"`
	CandidateCount  int        `json:"candidateCount"`
	RuleCount       int        `json:"ruleCount"`
	// OrderEventCount is the number frozen atomically with the initial run.
	// Later append-only lifecycle events intentionally do not update this row.
	OrderEventCount       int        `json:"orderEventCount"`
	SecuritySnapshotCount int        `json:"securitySnapshotCount"`
	CorporateActionCount  int        `json:"corporateActionCount"`
	SnapshotHash          string     `json:"snapshotHash" gorm:"size:128;not null;index"`
	PayloadJSON           string     `json:"payloadJson" gorm:"type:text;not null"`
	FrozenAt              *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (StrategyRunSnapshot) TableName() string { return "strategy_run_snapshot" }

// CandidateSnapshot stores the complete candidate feature vector as immutable
// JSON while keeping the stable dimensions needed for deterministic queries.
type CandidateSnapshot struct {
	ID              uint       `json:"id" gorm:"primarykey"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	CandidateID     string     `json:"candidateId" gorm:"size:128;not null;uniqueIndex"`
	RunID           string     `json:"runId" gorm:"size:96;not null;index;uniqueIndex:idx_strategy_candidate_run_symbol,priority:1"`
	StrategyVersion string     `json:"strategyVersion" gorm:"size:32;not null;index:idx_strategy_candidate_version_date,priority:1"`
	TradeDate       string     `json:"tradeDate" gorm:"size:10;not null;index:idx_strategy_candidate_version_date,priority:2"`
	Symbol          string     `json:"symbol" gorm:"size:32;not null;index;uniqueIndex:idx_strategy_candidate_run_symbol,priority:2"`
	Name            string     `json:"name" gorm:"size:128"`
	Sector          string     `json:"sector" gorm:"size:128;index"`
	Market          string     `json:"market" gorm:"size:32;index"`
	Rank            int        `json:"rank" gorm:"index"`
	PreVerifyRank   int        `json:"preVerifyRank" gorm:"index"`
	FinalRank       int        `json:"finalRank" gorm:"index"`
	Decision        string     `json:"decision" gorm:"size:32;index"`
	Score           float64    `json:"score" gorm:"index"`
	Eligible        bool       `json:"eligible" gorm:"index"`
	RejectionReason string     `json:"rejectionReason,omitempty" gorm:"type:text"`
	SnapshotHash    string     `json:"snapshotHash" gorm:"size:128;not null;index"`
	PayloadJSON     string     `json:"payloadJson" gorm:"type:text;not null"`
	FrozenAt        *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (CandidateSnapshot) TableName() string { return "strategy_candidate_snapshot" }

// RuleSnapshot freezes the exact executable trade-plan/rule payload used by a
// run. Rules are never reconstructed from the current strategy implementation.
type RuleSnapshot struct {
	ID              uint       `json:"id" gorm:"primarykey"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	RuleID          string     `json:"ruleId" gorm:"size:128;not null;uniqueIndex"`
	RunID           string     `json:"runId" gorm:"size:96;not null;index;uniqueIndex:idx_strategy_rule_run_symbol_path,priority:1"`
	CandidateID     string     `json:"candidateId" gorm:"size:128;index"`
	StrategyVersion string     `json:"strategyVersion" gorm:"size:32;not null;index:idx_strategy_rule_version_date,priority:1"`
	TradeDate       string     `json:"tradeDate" gorm:"size:10;not null;index:idx_strategy_rule_version_date,priority:2"`
	Symbol          string     `json:"symbol" gorm:"size:32;not null;index;uniqueIndex:idx_strategy_rule_run_symbol_path,priority:2"`
	RuleVersion     string     `json:"ruleVersion" gorm:"size:32;index"`
	RuleType        string     `json:"ruleType" gorm:"size:32;index"`
	Path            string     `json:"path" gorm:"size:32;index;uniqueIndex:idx_strategy_rule_run_symbol_path,priority:3"`
	ValidFromAt     time.Time  `json:"validFromAt" gorm:"not null;index"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty" gorm:"index"`
	SnapshotHash    string     `json:"snapshotHash" gorm:"size:128;not null;index"`
	PayloadJSON     string     `json:"payloadJson" gorm:"type:text;not null"`
	FrozenAt        *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (RuleSnapshot) TableName() string { return "strategy_rule_snapshot" }

// OrderEvent is an append-only strategy event log. Corrections are represented
// by later events and must not overwrite an earlier event.
type OrderEvent struct {
	ID               uint       `json:"id" gorm:"primarykey"`
	CreatedAt        time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	EventID          string     `json:"eventId" gorm:"size:128;not null;uniqueIndex"`
	RunID            string     `json:"runId" gorm:"size:96;not null;index;uniqueIndex:idx_strategy_order_run_rule_sequence,priority:1"`
	RuleID           string     `json:"ruleId" gorm:"size:128;index;uniqueIndex:idx_strategy_order_run_rule_sequence,priority:2"`
	StrategyVersion  string     `json:"strategyVersion" gorm:"size:32;not null;index:idx_strategy_order_version_date,priority:1"`
	TradeDate        string     `json:"tradeDate" gorm:"size:10;not null;index:idx_strategy_order_version_date,priority:2"`
	Symbol           string     `json:"symbol" gorm:"size:32;not null;index"`
	EventType        string     `json:"eventType" gorm:"size:32;not null;index"`
	Sequence         int        `json:"sequence" gorm:"not null;uniqueIndex:idx_strategy_order_run_rule_sequence,priority:3"`
	EventAt          time.Time  `json:"eventAt" gorm:"not null;index"`
	Price            float64    `json:"price"`
	Quantity         float64    `json:"quantity"`
	CashAmount       float64    `json:"cashAmount"`
	AdjustmentFactor float64    `json:"adjustmentFactor"`
	Fees             float64    `json:"fees"`
	Reason           string     `json:"reason,omitempty" gorm:"type:text"`
	SnapshotHash     string     `json:"snapshotHash" gorm:"size:128;not null;index"`
	PayloadJSON      string     `json:"payloadJson" gorm:"type:text;not null"`
	FrozenAt         *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (OrderEvent) TableName() string { return "strategy_order_event" }

// BacktestRun is persisted only when a deterministic cache-only backtest has
// completed. BacktestID is derived from the arguments and frozen input hash.
type BacktestRun struct {
	ID                     uint       `json:"id" gorm:"primarykey"`
	CreatedAt              time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	BacktestID             string     `json:"backtestId" gorm:"size:128;not null;uniqueIndex"`
	StrategyVersion        string     `json:"strategyVersion" gorm:"size:32;not null;index:idx_strategy_backtest_version_range,priority:1"`
	StartDate              string     `json:"startDate" gorm:"size:10;not null;index:idx_strategy_backtest_version_range,priority:2"`
	EndDate                string     `json:"endDate" gorm:"size:10;not null;index:idx_strategy_backtest_version_range,priority:3"`
	InputHash              string     `json:"inputHash" gorm:"size:128;not null;index"`
	Status                 string     `json:"status" gorm:"size:32;not null;index"`
	RunSnapshotCount       int        `json:"runSnapshotCount"`
	CandidateSnapshotCount int        `json:"candidateSnapshotCount"`
	RuleSnapshotCount      int        `json:"ruleSnapshotCount"`
	OrderEventCount        int        `json:"orderEventCount"`
	SecuritySnapshotCount  int        `json:"securitySnapshotCount"`
	CorporateActionCount   int        `json:"corporateActionCount"`
	TradeCount             int        `json:"tradeCount"`
	MetricCount            int        `json:"metricCount"`
	SummaryJSON            string     `json:"summaryJson" gorm:"type:text;not null"`
	StartedAt              time.Time  `json:"startedAt" gorm:"not null;index"`
	CompletedAt            time.Time  `json:"completedAt" gorm:"not null;index"`
	FrozenAt               *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (BacktestRun) TableName() string { return "strategy_backtest_run" }

type Trade struct {
	ID                  uint       `json:"id" gorm:"primarykey"`
	CreatedAt           time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	TradeID             string     `json:"tradeId" gorm:"size:128;not null;uniqueIndex"`
	BacktestID          string     `json:"backtestId" gorm:"size:128;not null;index;uniqueIndex:idx_strategy_backtest_trade_seq,priority:1"`
	StrategyVersion     string     `json:"strategyVersion" gorm:"size:32;not null;index"`
	Sequence            int        `json:"sequence" gorm:"not null;uniqueIndex:idx_strategy_backtest_trade_seq,priority:2"`
	Symbol              string     `json:"symbol" gorm:"size:32;not null;index"`
	EntryAt             time.Time  `json:"entryAt" gorm:"not null;index"`
	ExitAt              *time.Time `json:"exitAt,omitempty" gorm:"index"`
	EntryPrice          float64    `json:"entryPrice"`
	ExitPrice           float64    `json:"exitPrice"`
	Quantity            float64    `json:"quantity"`
	Fees                float64    `json:"fees"`
	GrossPnL            float64    `json:"grossPnl"`
	NetPnL              float64    `json:"netPnl"`
	ReturnPct           float64    `json:"returnPct"`
	ExitReason          string     `json:"exitReason,omitempty" gorm:"size:64;index"`
	SourceOrderEventIDs string     `json:"sourceOrderEventIds" gorm:"type:text"`
	SnapshotHash        string     `json:"snapshotHash" gorm:"size:128;not null;index"`
	PayloadJSON         string     `json:"payloadJson" gorm:"type:text;not null"`
	FrozenAt            *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (Trade) TableName() string { return "strategy_backtest_trade" }

type Metric struct {
	ID          uint       `json:"id" gorm:"primarykey"`
	CreatedAt   time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	MetricID    string     `json:"metricId" gorm:"size:160;not null;uniqueIndex"`
	BacktestID  string     `json:"backtestId" gorm:"size:128;not null;index;uniqueIndex:idx_strategy_backtest_metric_key,priority:1"`
	Name        string     `json:"name" gorm:"size:64;not null;index;uniqueIndex:idx_strategy_backtest_metric_key,priority:2"`
	Scope       string     `json:"scope" gorm:"size:64;not null;default:summary;uniqueIndex:idx_strategy_backtest_metric_key,priority:3"`
	Value       float64    `json:"value"`
	ValueText   string     `json:"valueText,omitempty" gorm:"size:128"`
	Unit        string     `json:"unit,omitempty" gorm:"size:32"`
	Ordinal     int        `json:"ordinal" gorm:"index"`
	PayloadJSON string     `json:"payloadJson" gorm:"type:text"`
	FrozenAt    *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (Metric) TableName() string { return "strategy_backtest_metric" }

// SecurityMasterHistory is a slowly changing, immutable security-master row.
type SecurityMasterHistory struct {
	ID              uint       `json:"id" gorm:"primarykey"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	RecordID        string     `json:"recordId" gorm:"size:128;not null;uniqueIndex:idx_security_master_run_record,priority:2"`
	RunID           string     `json:"runId" gorm:"size:96;not null;index;uniqueIndex:idx_security_master_run_record,priority:1;uniqueIndex:idx_security_master_run_symbol_effective,priority:1"`
	SnapshotVersion string     `json:"snapshotVersion" gorm:"size:32;not null;index:idx_security_master_version_symbol,priority:1"`
	Symbol          string     `json:"symbol" gorm:"size:32;not null;index;index:idx_security_master_version_symbol,priority:2;uniqueIndex:idx_security_master_run_symbol_effective,priority:2"`
	Name            string     `json:"name" gorm:"size:128"`
	Market          string     `json:"market" gorm:"size:32;index"`
	Exchange        string     `json:"exchange" gorm:"size:32;index"`
	Board           string     `json:"board" gorm:"size:64;index"`
	Sector          string     `json:"sector" gorm:"size:128;index"`
	Industry        string     `json:"industry" gorm:"size:128;index"`
	Currency        string     `json:"currency" gorm:"size:16"`
	Status          string     `json:"status" gorm:"size:32;index"`
	IsST            bool       `json:"isSt" gorm:"index"`
	IsSuspended     bool       `json:"isSuspended" gorm:"index"`
	ListedAt        *time.Time `json:"listedAt,omitempty" gorm:"index"`
	DelistedAt      *time.Time `json:"delistedAt,omitempty" gorm:"index"`
	EffectiveFrom   time.Time  `json:"effectiveFrom" gorm:"not null;index;uniqueIndex:idx_security_master_run_symbol_effective,priority:3"`
	EffectiveTo     *time.Time `json:"effectiveTo,omitempty" gorm:"index"`
	Source          string     `json:"source" gorm:"size:64;index"`
	SnapshotHash    string     `json:"snapshotHash" gorm:"size:128;not null;index"`
	PayloadJSON     string     `json:"payloadJson" gorm:"type:text;not null"`
	FrozenAt        *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (SecurityMasterHistory) TableName() string { return "security_master_history" }

// CorporateActionEvent stores split/dividend/rights events exactly as known at
// the frozen snapshot cutoff used by a strategy or backtest.
type CorporateActionEvent struct {
	ID              uint       `json:"id" gorm:"primarykey"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	EventID         string     `json:"eventId" gorm:"size:128;not null;uniqueIndex:idx_corporate_action_run_event,priority:2"`
	RunID           string     `json:"runId" gorm:"size:96;not null;index;uniqueIndex:idx_corporate_action_run_event,priority:1;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:1"`
	SnapshotVersion string     `json:"snapshotVersion" gorm:"size:32;not null;index:idx_corporate_action_version_date,priority:1"`
	Symbol          string     `json:"symbol" gorm:"size:32;not null;index;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:2"`
	ActionType      string     `json:"actionType" gorm:"size:32;not null;index;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:3"`
	AnnouncedAt     *time.Time `json:"announcedAt,omitempty" gorm:"index"`
	// SourceAt is the provider's point-in-time timestamp when one is exposed;
	// AvailableAt is when this process first had the complete response.  A
	// replay may consume a row only when both are no later than its event/bar.
	SourceAt          *time.Time `json:"sourceAt,omitempty" gorm:"index"`
	AvailableAt       *time.Time `json:"availableAt,omitempty" gorm:"index"`
	ObservationStatus string     `json:"observationStatus" gorm:"size:16;index"`
	CoverageStart     *time.Time `json:"coverageStart,omitempty" gorm:"index"`
	CoverageEnd       *time.Time `json:"coverageEnd,omitempty" gorm:"index"`
	ExDate            time.Time  `json:"exDate" gorm:"not null;index;index:idx_corporate_action_version_date,priority:2;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:4"`
	RecordDate        *time.Time `json:"recordDate,omitempty" gorm:"index"`
	PayDate           *time.Time `json:"payDate,omitempty" gorm:"index"`
	CashDividend      float64    `json:"cashDividend"`
	SplitRatio        float64    `json:"splitRatio"`
	BonusRatio        float64    `json:"bonusRatio"`
	RightsRatio       float64    `json:"rightsRatio"`
	RightsPrice       float64    `json:"rightsPrice"`
	AdjustmentFactor  float64    `json:"adjustmentFactor"`
	Currency          string     `json:"currency" gorm:"size:16"`
	Source            string     `json:"source" gorm:"size:64;index"`
	SnapshotHash      string     `json:"snapshotHash" gorm:"size:128;not null;index"`
	PayloadJSON       string     `json:"payloadJson" gorm:"type:text;not null"`
	FrozenAt          *time.Time `json:"frozenAt,omitempty" gorm:"not null;index"`
}

func (CorporateActionEvent) TableName() string { return "corporate_action_event" }

// StrategyPersistenceModels returns every 1.5.0 immutable persistence model in
// dependency order for callers that manage their own GORM database.
func StrategyPersistenceModels() []any {
	return []any{
		&StrategyRunSnapshot{},
		&CandidateSnapshot{},
		&RuleSnapshot{},
		&OrderEvent{},
		&BacktestRun{},
		&Trade{},
		&Metric{},
		&SecurityMasterHistory{},
		&CorporateActionEvent{},
	}
}
