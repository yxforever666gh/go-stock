// Package execution orchestrates frozen rules against point-in-time facts.
package execution

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/strategy/v150"
)

var ErrInvalidExecutionContext = errors.New("invalid execution context")

type FrozenRule struct {
	RunID           string         `json:"runId"`
	RuleID          string         `json:"ruleId"`
	StrategyVersion string         `json:"strategyVersion"`
	ConfigHash      string         `json:"configHash"`
	Candidate       v150.Candidate `json:"candidate"`
	Plan            v150.TradePlan `json:"plan"`
	FrozenAt        time.Time      `json:"frozenAt"`
}

// ExecutionContext contains every mutable input to one evaluation. Wall clock,
// database handles, providers and version routing are deliberately absent.
type ExecutionContext struct {
	EvaluatedAt      time.Time
	Rule             FrozenRule
	PreviousBar      *marketdata.MinuteBar
	CurrentBar       marketdata.MinuteBar
	ActivationState  v150.ActivationState
	PendingOrder     *v150.EntryOrder
	Position         *v150.Position
	Portfolio        v150.PortfolioState
	AvailableCash    float64
	Slippage         v150.SlippageScenario
	CorporateActions []v150.CorporateAction
}

// EvaluationResult contains append-only facts and the next transient state.
// Callers may persist Events, but must not infer ledger state from projections.
type EvaluationResult struct {
	EvaluatedAt     time.Time              `json:"evaluatedAt"`
	ActivationState v150.ActivationState   `json:"activationState"`
	Signal          *v150.ActivationSignal `json:"signal,omitempty"`
	PendingOrder    *v150.EntryOrder       `json:"pendingOrder,omitempty"`
	Fill            *v150.EntryFillResult  `json:"fill,omitempty"`
	Exit            *v150.ExitResult       `json:"exit,omitempty"`
	Position        *v150.Position         `json:"position,omitempty"`
	Events          []v150.OrderEvent      `json:"events"`
}

type Evaluator struct{}

func (Evaluator) Evaluate(ctx ExecutionContext) (EvaluationResult, error) {
	result := EvaluationResult{EvaluatedAt: ctx.EvaluatedAt, ActivationState: ctx.ActivationState}
	if err := validateContext(ctx); err != nil {
		return result, err
	}
	cfg := v150.FixedStrategyV150Config()
	bar := toStrategyBar(ctx.CurrentBar)

	if ctx.Position != nil {
		position := *ctx.Position
		if len(ctx.CorporateActions) > 0 {
			application, err := v150.ApplyCorporateActions(position, ctx.CorporateActions, bar.Start)
			if err != nil {
				return result, err
			}
			position = application.Position
			result.Events = append(result.Events, application.Events...)
		}
		exit := v150.EvaluateExit(position, bar, ctx.Slippage, cfg)
		result.Exit = &exit
		result.Events = append(result.Events, exit.Events...)
		if !exit.Triggered {
			position = v150.AdvanceTrailingStop(position, bar, cfg)
			result.Position = &position
		}
		return result, nil
	}

	if ctx.PendingOrder != nil {
		fill := v150.TryFillEntryOnNextBar(*ctx.PendingOrder, bar, ctx.Portfolio, ctx.AvailableCash, ctx.Slippage, cfg)
		result.Fill = &fill
		result.Events = append(result.Events, fill.Events...)
		if fill.Status == v150.FillFilled {
			position := fill.Position
			result.Position = &position
		} else if fill.Status == v150.FillPending {
			order := *ctx.PendingOrder
			result.PendingOrder = &order
		}
		return result, nil
	}

	previous := v150.Bar{}
	if ctx.PreviousBar != nil {
		previous = toStrategyBar(*ctx.PreviousBar)
	}
	signal, state := v150.DetectActivation(ctx.Rule.Plan, previous, bar, ctx.ActivationState)
	result.ActivationState = state
	if signal.Triggered || signal.Reason != "" {
		result.Signal = &signal
	}
	if signal.Triggered {
		order := v150.NewEntryOrder(signal, ctx.Rule.Plan, ctx.Rule.Candidate)
		result.PendingOrder = &order
	}
	return result, nil
}

func validateContext(ctx ExecutionContext) error {
	if ctx.EvaluatedAt.IsZero() || ctx.CurrentBar.Start.IsZero() || ctx.CurrentBar.End.IsZero() {
		return fmt.Errorf("%w: evaluatedAt and current bar timeline are required", ErrInvalidExecutionContext)
	}
	if strings.TrimSpace(ctx.Rule.RunID) == "" || strings.TrimSpace(ctx.Rule.RuleID) == "" || ctx.Rule.FrozenAt.IsZero() {
		return fmt.Errorf("%w: frozen rule identity is required", ErrInvalidExecutionContext)
	}
	if ctx.Rule.StrategyVersion != v150.StrategyVersion || ctx.Rule.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		return fmt.Errorf("%w: expected frozen strategy %s/%s", ErrInvalidExecutionContext, v150.StrategyVersion, v150.FixedStrategyV150ConfigHash())
	}
	if ctx.Rule.Plan.Symbol == "" || ctx.Rule.Plan.Symbol != ctx.Rule.Candidate.Symbol {
		return fmt.Errorf("%w: plan and candidate symbol mismatch", ErrInvalidExecutionContext)
	}
	return nil
}

func toStrategyBar(bar marketdata.MinuteBar) v150.Bar {
	return v150.Bar{
		Index: bar.Index, TradeDayIndex: bar.TradeDayIndex, IntervalMinutes: bar.IntervalMinutes,
		Start: bar.Start, End: bar.End, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close,
		Volume: bar.Volume, Amount: bar.Amount, VolumeRatioSameSlot: bar.VolumeRatioSameSlot,
		Completed: bar.Completed, Suspended: bar.Suspended, LimitUpLocked: bar.LimitUpLocked,
		LimitDownLocked: bar.LimitDownLocked,
	}
}
