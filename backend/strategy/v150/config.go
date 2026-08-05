package v150

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const (
	StrategyVersion = "1.5.0"
	BenchmarkCode   = "510300"
)

// StrategyV150Config is the immutable policy surface for strategy 1.5.0.
// FixedStrategyV150Config returns a value copy so callers cannot mutate shared
// process state.
type StrategyV150Config struct {
	Version   string
	Benchmark string

	RiskOnDailyCap  int
	NeutralDailyCap int
	RiskOffDailyCap int

	MinimumListingCalendarDays int
	MinimumAverageAmount20     float64
	MinimumPrice               float64
	MaximumATRRatio            float64
	MaximumDayChange           float64
	MaximumAbsoluteGap         float64
	MaximumDistanceFromMA20ATR float64
	MaximumRealtimeQuoteLag    time.Duration

	TrendRelativeWeight               int
	TrendComponentShare               float64
	RelativeComponentShare            float64
	RelativeStrengthLookbackTradeDays int
	RelativeStrengthFullScoreReturn   float64
	SetupWeight                       int
	SectorWeight                      int
	EventWeight                       int
	LiquidityRiskWeight               int
	EventFreshness                    time.Duration
	ProductionScoreFloor              int
	VerificationLimit                 int

	PullbackZoneATR             float64
	PullbackRecoveryMinutes     int
	BreakoutMinimumVolumeRatio  float64
	BreakoutActivationCutoffMin int
	ActivationValidTradeDays    int
	MaximumHoldTradeDays        int
	StopATRMultiple             float64
	MinimumStopRatio            float64
	MaximumStopRatio            float64
	TargetRiskMultiple          float64
	MinimumAchievableRiskReward float64
	ResistanceTargetMultiplier  float64
	TrailingActivationR         float64
	TrailingATRMultiple         float64
	TimeExitMinute              int

	PortfolioCash           float64
	TargetCashPerPosition   float64
	RoundLotSize            int
	MaximumOpenPositions    int
	MaximumSectorEntriesDay int
	StopCooldownTradeDays   int

	CommissionRate    float64
	MinimumCommission float64
	SellStampDutyRate float64
	TransferFeeRate   float64
	BaseSlippageBPS   float64
	StressSlippageBPS [2]float64
}

func FixedStrategyV150Config() StrategyV150Config {
	return StrategyV150Config{
		Version:   StrategyVersion,
		Benchmark: BenchmarkCode,

		RiskOnDailyCap:  2,
		NeutralDailyCap: 1,
		RiskOffDailyCap: 0,

		MinimumListingCalendarDays: 120,
		MinimumAverageAmount20:     100_000_000,
		MinimumPrice:               3,
		MaximumATRRatio:            0.06,
		MaximumDayChange:           0.05,
		MaximumAbsoluteGap:         0.04,
		MaximumDistanceFromMA20ATR: 1.5,
		MaximumRealtimeQuoteLag:    5 * time.Minute,

		TrendRelativeWeight:               30,
		TrendComponentShare:               0.60,
		RelativeComponentShare:            0.40,
		RelativeStrengthLookbackTradeDays: 20,
		RelativeStrengthFullScoreReturn:   0.10,
		SetupWeight:                       25,
		SectorWeight:                      15,
		EventWeight:                       20,
		LiquidityRiskWeight:               10,
		EventFreshness:                    48 * time.Hour,
		ProductionScoreFloor:              70,
		VerificationLimit:                 18,

		PullbackZoneATR:             0.25,
		PullbackRecoveryMinutes:     15,
		BreakoutMinimumVolumeRatio:  1.2,
		BreakoutActivationCutoffMin: 14 * 60,
		ActivationValidTradeDays:    3,
		MaximumHoldTradeDays:        10,
		StopATRMultiple:             1.5,
		MinimumStopRatio:            0.03,
		MaximumStopRatio:            0.06,
		TargetRiskMultiple:          2,
		MinimumAchievableRiskReward: 1.5,
		ResistanceTargetMultiplier:  0.995,
		TrailingActivationR:         1,
		TrailingATRMultiple:         1.5,
		TimeExitMinute:              14*60 + 45,

		PortfolioCash:           100_000,
		TargetCashPerPosition:   10_000,
		RoundLotSize:            100,
		MaximumOpenPositions:    5,
		MaximumSectorEntriesDay: 1,
		StopCooldownTradeDays:   5,

		CommissionRate:    0.0003,
		MinimumCommission: 5,
		SellStampDutyRate: 0.0005,
		// ChinaClear A-share transfer fee: 0.001% on both buy and sell.
		TransferFeeRate:   0.00001,
		BaseSlippageBPS:   10,
		StressSlippageBPS: [2]float64{20, 50},
	}
}

func (c StrategyV150Config) SlippageScenarios() []SlippageScenario {
	return []SlippageScenario{
		{Name: "base", BPS: c.BaseSlippageBPS},
		{Name: "stress_20bp", BPS: c.StressSlippageBPS[0]},
		{Name: "stress_50bp", BPS: c.StressSlippageBPS[1]},
	}
}

// Hash is persisted with every run. The config is a value with a stable JSON
// field order, so identical 1.5.0 binaries produce the same SHA-256 digest.
func (c StrategyV150Config) Hash() string {
	payload, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func FixedStrategyV150ConfigHash() string {
	return FixedStrategyV150Config().Hash()
}
