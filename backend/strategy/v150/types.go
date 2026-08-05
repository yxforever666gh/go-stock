package v150

import "time"

type Market string

const (
	MarketUnknown Market = ""
	MarketSH      Market = "SH"
	MarketSZ      Market = "SZ"
	MarketBJ      Market = "BJ"
)

type MarketRegime string

const (
	RegimeRiskOn  MarketRegime = "risk_on"
	RegimeNeutral MarketRegime = "neutral"
	RegimeRiskOff MarketRegime = "risk_off"
)

type TradePath string

const (
	PathPullback TradePath = "pullback"
	PathBreakout TradePath = "breakout"
)

type RunContext struct {
	RunID           string
	StartedAt       time.Time
	AsOf            time.Time
	DataCutoffAt    time.Time
	DecisionAt      time.Time
	GeneratedAt     time.Time
	ValidFromAt     time.Time
	StrategyVersion string
	ConfigHash      string
	Mode            string
	TradeDayIndex   int
	// ValidFromTradeDayIndex anchors activation expiry to the first trading
	// day on which the rule can legally observe a complete bar. It can differ
	// from TradeDayIndex for decisions made after market close.
	ValidFromTradeDayIndex int
}

func (r RunContext) ValidTimeline() bool {
	if r.StartedAt.IsZero() || r.DataCutoffAt.IsZero() || r.DecisionAt.IsZero() || r.ValidFromAt.IsZero() {
		return false
	}
	return !r.StartedAt.After(r.DecisionAt) && !r.DataCutoffAt.After(r.DecisionAt) && r.DecisionAt.Before(r.ValidFromAt) &&
		r.TradeDayIndex > 0 && r.ValidFromTradeDayIndex >= r.TradeDayIndex
}

// Explicit versioned names keep the public strategy contract unambiguous
// while the shorter aliases remain convenient inside this package.
type StrategyRunContext = RunContext
type TradePlanV150 = TradePlan

type BenchmarkSnapshot struct {
	Code            string
	Close           float64
	MA20            float64
	MA60            float64
	MA20FiveDaysAgo float64
	Return20        float64
	Return20Start   string
	Return20End     string
	HasReturn20Data bool
	DataPresent     bool
	Stale           bool
}

type RegimeDecision struct {
	Regime       MarketRegime
	DailyCap     int
	PullbackOnly bool
	NoTrade      bool
	Warning      string
}

// ScoreSignals are normalized to [0,1]. ScoreCandidate clamps out-of-range
// values, making score production deterministic even with imperfect inputs.
type ScoreSignals struct {
	TrendRelativeStrength float64
	SetupQuality          float64
	SectorStrength        float64
	EventStrength         float64
	LiquidityRiskQuality  float64
}

type Candidate struct {
	Symbol string
	Name   string
	Sector string
	Market Market

	ListedAt       time.Time
	ST             bool
	Suspended      bool
	HasDailyData   bool
	HasCurrentData bool
	// HasRelativeStrengthData is true only when the stock and 510300 returns
	// were computed from the exact same completed, adjusted daily-bar dates.
	HasRelativeStrengthData bool

	Price           float64
	PreviousClose   float64
	MA10            float64
	MA20            float64
	MA60            float64
	ATR14           float64
	AverageAmount20 float64
	DayChangeRatio  float64
	GapRatio        float64

	// The following point-in-time fields make the 30-point trend/relative
	// score independently replayable from an immutable candidate snapshot.
	TrendQuality            float64
	Return20                float64
	BenchmarkReturn20       float64
	RelativeReturn20        float64
	RelativeStrengthQuality float64
	RelativeStrengthStart   string
	RelativeStrengthEnd     string

	// Resistance20 is the prior 20-day resistance used by the breakout path.
	Resistance20 float64
	// TargetResistance is an optional next overhead resistance used to cap a
	// target. For pullbacks, Resistance20 is used when this is absent.
	TargetResistance           float64
	NegativeOvernightGapRisk60 float64

	EventAt *time.Time
	Signals ScoreSignals
}

type ScoreBreakdown struct {
	TrendRelative int
	Setup         int
	Sector        int
	Event         int
	LiquidityRisk int
	Total         int
}

type EligibilityResult struct {
	Eligible bool
	Reasons  []string
}

type ScoredCandidate struct {
	Candidate   Candidate
	Score       ScoreBreakdown
	Eligibility EligibilityResult
	Rank        int
	Verified    bool
}

type TradePlan struct {
	Symbol string
	Path   TradePath

	DecisionTradeDayIndex      int
	ValidFromTradeDayIndex     int
	ValidFromAt                time.Time
	EvaluationMinutes          int
	Support                    float64
	EntryMin                   float64
	EntryMax                   float64
	Trigger                    float64
	ReferenceEntry             float64
	TargetResistance           float64
	Stop                       float64
	Target                     float64
	RiskPerShare               float64
	RewardRisk                 float64
	ATR14                      float64
	NegativeOvernightGapRisk60 float64

	MinimumVolumeRatio   float64
	NoActivationAfterMin int
	ValidTradeDays       int
	MaxHoldTradeDays     int
	TrailingActivationR  float64
	TrailingATRMultiple  float64
}

type PlanResult struct {
	Plan     TradePlan
	Accepted bool
	Reason   string
}

type PortfolioState struct {
	OpenSymbols            map[string]bool
	PendingSymbols         map[string]bool
	TodayEntries           int
	TodaySectorEntries     map[string]int
	TradeDaysSinceLastStop map[string]int
	// ExecutionDailyCap is resolved from the immutable run regime at fill
	// time. It is runtime-only so adding the guard cannot change frozen
	// strategy payloads or their hashes. Nil keeps the fixed risk-on ceiling
	// for callers that do not execute a frozen rule.
	ExecutionDailyCap *int `json:"-"`
}

type Bar struct {
	Index               int
	TradeDayIndex       int
	IntervalMinutes     int
	Start               time.Time
	End                 time.Time
	Open                float64
	High                float64
	Low                 float64
	Close               float64
	Volume              float64
	Amount              float64
	VolumeRatioSameSlot float64
	Completed           bool
	Suspended           bool
	LimitUpLocked       bool
	LimitDownLocked     bool
}

type ActivationState struct {
	ZoneTouched bool
	Signaled    bool
}

type ActivationSignal struct {
	Triggered     bool
	Path          TradePath
	At            time.Time
	BarIndex      int
	TradeDayIndex int
	SignalClose   float64
	Reason        string
}

type EntryOrder struct {
	Symbol              string
	Sector              string
	Market              Market
	Plan                TradePlan
	SignalAt            time.Time
	SignalBarIndex      int
	SignalTradeDayIndex int
	SignalClose         float64
	Attempted           bool
}

type Position struct {
	Symbol             string
	Sector             string
	Market             Market
	Quantity           int
	EntryAt            time.Time
	EntryTradeDayIndex int
	EntryPrice         float64
	InitialStop        float64
	Target             float64
	RiskPerShare       float64
	ATR14              float64
	HighestClose       float64
	TrailingActive     bool
	TrailingStop       float64
	MaxHoldTradeDays   int
	// CorporateActionCash is the net cash credited by causally observed cash
	// dividends while the position is open.  Rights subscriptions are not
	// inferred: an unresolved rights event is fail-closed before this state can
	// be mutated.
	CorporateActionCash float64
}

type PositionSize struct {
	Quantity int
	Notional float64
	Rejected bool
	Reason   string
}

type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

type SlippageScenario struct {
	Name string
	BPS  float64
}

type TradeCost struct {
	Side           Side
	RawPrice       float64
	EffectivePrice float64
	Quantity       int
	Notional       float64
	Commission     float64
	StampDuty      float64
	TransferFee    float64
	SlippageCost   float64
	CashFlow       float64
}

type FillStatus string

const (
	FillPending  FillStatus = "pending"
	FillFilled   FillStatus = "filled"
	FillRejected FillStatus = "rejected"
	FillExpired  FillStatus = "expired"
)

type EntryFillResult struct {
	Status   FillStatus
	Reason   string
	At       time.Time
	Cost     TradeCost
	Plan     TradePlan
	Position Position
	Events   []OrderEvent
}

type ExitReason string

const (
	ExitNone   ExitReason = ""
	ExitStop   ExitReason = "stop"
	ExitTarget ExitReason = "target"
	ExitTime   ExitReason = "time"
)

type ExitResult struct {
	Triggered bool
	Reason    ExitReason
	At        time.Time
	Cost      TradeCost
	Events    []OrderEvent
}

type OrderEventType string

const (
	EventSignal          OrderEventType = "signal"
	EventOrder           OrderEventType = "order"
	EventFill            OrderEventType = "fill"
	EventCorporateAction OrderEventType = "corporate_action"
	EventReject          OrderEventType = "reject"
	EventExitSignal      OrderEventType = "exit_signal"
	EventExitFill        OrderEventType = "exit_fill"
)

type OrderEvent struct {
	Type             OrderEventType
	At               time.Time
	Symbol           string
	Price            float64
	Quantity         int
	CashAmount       float64
	AdjustmentFactor float64
	Reason           string
}
