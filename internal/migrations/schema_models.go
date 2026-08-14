package migrations

import (
	"time"

	"gorm.io/gorm"
)

// IndexBasic is the migration-owned schema contract for the legacy index
// table. Keep it stable so published migration definitions remain immutable.
type IndexBasic struct {
	gorm.Model
	TsCode        string  `json:"ts_code" gorm:"index"`
	Symbol        string  `json:"symbol" gorm:"index"`
	Name          string  `json:"name" gorm:"index"`
	FullName      string  `json:"fullname"`
	IndexType     string  `json:"index_type"`
	IndexCategory string  `json:"category"`
	Market        string  `json:"market"`
	ListDate      string  `json:"list_date"`
	BaseDate      string  `json:"base_date"`
	BasePoint     float64 `json:"base_point"`
	Publisher     string  `json:"publisher"`
	WeightRule    string  `json:"weight_rule"`
	DESC          string  `json:"desc"`
}

func (IndexBasic) TableName() string { return "tushare_index_basic" }

// strategyRuntimeControlV2 is the published schema-2 singleton contract.
// Keep this migration-owned type unchanged when governance models evolve.
type strategyRuntimeControlV2 struct {
	ID                     uint      `gorm:"primaryKey"`
	Mode                   string    `gorm:"size:16;not null"`
	CurrentStrategyVersion string    `gorm:"size:32;not null"`
	Reason                 string    `gorm:"size:512"`
	ChangedBy              string    `gorm:"size:128"`
	ChangedAt              time.Time `gorm:"not null"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (strategyRuntimeControlV2) TableName() string { return "strategy_runtime_control" }

// marketSummaryRunDiagnosticV2 is the published schema-2 diagnostic contract.
// New diagnostic fields must be introduced by a later migration instead of
// leaking into fresh schema-1/schema-2 databases through the runtime model.
type marketSummaryRunDiagnosticV2 struct {
	gorm.Model
	RunID                        string    `json:"runId" gorm:"size:64;uniqueIndex"`
	SummaryVersion               string    `json:"summaryVersion" gorm:"size:32;index"`
	RunSlot                      string    `json:"runSlot" gorm:"size:32;index"`
	StartedAt                    time.Time `json:"startedAt" gorm:"index"`
	FinishedAt                   time.Time `json:"finishedAt" gorm:"index"`
	IndicatorCandidateCount      int       `json:"indicatorCandidateCount"`
	IndicatorAIInputCount        int       `json:"indicatorAiInputCount"`
	DiscoveryCandidateCount      int       `json:"discoveryCandidateCount"`
	VerifiedCandidateCount       int       `json:"verifiedCandidateCount"`
	AIOutputCountFirst           int       `json:"aiOutputCountFirst"`
	AIOutputCountSecond          int       `json:"aiOutputCountSecond"`
	SavedCount                   int       `json:"savedCount"`
	ProductionCount              int       `json:"productionCount"`
	AnalysisOnlyCount            int       `json:"analysisOnlyCount"`
	BlockedCount                 int       `json:"blockedCount"`
	BlockedReasonTop             string    `json:"blockedReasonTop" gorm:"type:text"`
	ProductionDowngradeReasonTop string    `json:"productionDowngradeReasonTop" gorm:"type:text"`
	EmptyRun                     bool      `json:"emptyRun" gorm:"index"`
	NotesJSON                    string    `json:"notesJson" gorm:"type:text"`
}

func (marketSummaryRunDiagnosticV2) TableName() string {
	return "market_summary_run_diagnostics"
}

// marketSummaryRunDiagnosticV3 freezes the App 1.5.3 diagnostic schema. Keep
// this migration-owned type unchanged after migration 3 is published.
type marketSummaryRunDiagnosticV3 struct {
	gorm.Model
	RunID                        string    `json:"runId" gorm:"size:64;uniqueIndex"`
	SummaryVersion               string    `json:"summaryVersion" gorm:"size:32;index"`
	RunSlot                      string    `json:"runSlot" gorm:"size:32;index"`
	Outcome                      string    `json:"outcome" gorm:"size:32;index"`
	SampleValid                  bool      `json:"sampleValid" gorm:"index"`
	FailureCode                  string    `json:"failureCode,omitempty" gorm:"size:64;index"`
	FunnelJSON                   string    `json:"funnelJson,omitempty" gorm:"type:text"`
	StartedAt                    time.Time `json:"startedAt" gorm:"index"`
	FinishedAt                   time.Time `json:"finishedAt" gorm:"index"`
	UniverseCount                int       `json:"universeCount"`
	MasterReadyCount             int       `json:"masterReadyCount"`
	QuoteReadyCount              int       `json:"quoteReadyCount"`
	EligibleCount                int       `json:"eligibleCount"`
	ScoreFloorCount              int       `json:"scoreFloorCount"`
	IndicatorCandidateCount      int       `json:"indicatorCandidateCount"`
	IndicatorAIInputCount        int       `json:"indicatorAiInputCount"`
	DiscoveryCandidateCount      int       `json:"discoveryCandidateCount"`
	VerifiedCandidateCount       int       `json:"verifiedCandidateCount"`
	FeasiblePlanCount            int       `json:"feasiblePlanCount"`
	RuleCount                    int       `json:"ruleCount"`
	AIOutputCountFirst           int       `json:"aiOutputCountFirst"`
	AIOutputCountSecond          int       `json:"aiOutputCountSecond"`
	SavedCount                   int       `json:"savedCount"`
	ProductionCount              int       `json:"productionCount"`
	AnalysisOnlyCount            int       `json:"analysisOnlyCount"`
	BlockedCount                 int       `json:"blockedCount"`
	BlockedReasonTop             string    `json:"blockedReasonTop" gorm:"type:text"`
	ProductionDowngradeReasonTop string    `json:"productionDowngradeReasonTop" gorm:"type:text"`
	EmptyRun                     bool      `json:"emptyRun" gorm:"index"`
	NotesJSON                    string    `json:"notesJson" gorm:"type:text"`
}

func (marketSummaryRunDiagnosticV3) TableName() string {
	return "market_summary_run_diagnostics"
}
