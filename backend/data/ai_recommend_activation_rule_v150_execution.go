package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
)

const (
	marketSummaryV150DataHealthReject = "v1.5 data_health_reject"
	marketSummaryV150TimeExitStatus   = "已到期退出"
)

// V1.5 portfolio admission is a read-check-append transaction at the process
// boundary. SQLite transactions protect each append, but without this lock two
// symbols can both read the same portfolio snapshot before either fill exists.
// The recalc scheduler supplies deterministic event/rank order; this mutex is
// the final guard for direct/concurrent callers of the execution path.
var marketSummaryV150ExecutionMu sync.Mutex

var marketSummaryV150BeforeEntryCriticalSection = func() {}

// marketSummaryV150EntryExecution is deliberately transient.  The durable
// source of truth is the append-only strategy_order_event ledger; this value
// only carries the exact repriced plan into the exit replay performed during
// the same yield rebuild.
type marketSummaryV150EntryExecution struct {
	Plan     v150.TradePlan
	Position v150.Position
	Cost     v150.TradeCost
}

type marketSummaryV150FrozenExecutionPlan struct {
	Run       models.StrategyRunSnapshot
	Rule      models.RuleSnapshot
	Candidate models.CandidateSnapshot
	Plan      v150.TradePlan
}

type marketSummaryV150SecurityState struct {
	Row      models.SecurityMasterHistory
	Tradable bool
}

type marketSummaryV150EventAccounting struct {
	Entry *v150.TradeCost
	Exit  *v150.TradeCost
}

// resolveMarketSummaryV150Activation is the only 1.5.0 entry path.  It uses
// completed 15-minute bars, emits a signal, then executes exclusively at the
// next completed bar's open through strategy/v150.  No legacy V1.3.6 gate is
// involved.
func resolveMarketSummaryV150Activation(rec models.AiRecommendStocks, ctx yieldBuildContext, allowHeadBackfill bool) (*time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常"}
	frozen, err := loadMarketSummaryV150FrozenExecutionPlan(rec)
	if err != nil {
		return rejectMarketSummaryV150Activation(ctx, rec, nil, nil, ctx.Now, marketSummaryV150DataHealthReject+": "+err.Error(), info)
	}

	evaluatedThrough := marketSummaryV150ExecutionEvaluatedThrough(ctx)
	if evaluatedThrough.IsZero() {
		evaluatedThrough = ctx.Now
	}
	if evaluatedThrough.IsZero() || !evaluatedThrough.After(frozen.Plan.ValidFromAt) {
		info.DataStatus = "待激活"
		info.DataStatusReason = "v1.5 is waiting for the first complete 15-minute bar after validFrom"
		return nil, 0, info
	}

	// Breakout volume is compared with prior same-slot bars.  Pulling the
	// lookback from cache/provider does not make it eligible for activation;
	// bars before validFrom are baseline-only.
	// Keep enough calendar history to cover five prior same-slot trading bars
	// even across Spring Festival/National Day closures.
	windowStart := frozen.Plan.ValidFromAt.AddDate(0, 0, -30)
	var raw []minuteBar
	var syncInfo minuteSyncInfo
	code := normalizeRecommendStockCode(rec.StockCode)
	var securityRefreshErr error
	loadSecurityState := loadMarketSummaryV150SecurityState
	if ctx.RequireV150ExecutionObservation {
		loadSecurityState = loadMarketSummaryV150ExecutionObservationState
	}
	if !ctx.DisableMinuteFetch {
		_, securityRefreshErr = refreshMarketSummaryV150ExecutionSecurityObservation(frozen.Run.RunID, code, true)
		loadSecurityState = loadMarketSummaryV150ExecutionObservationState
	}
	if ctx.DisableMinuteFetch {
		raw, syncInfo = syncMinuteBarsFromCacheOnly(code, windowStart, evaluatedThrough)
	} else {
		raw, syncInfo = syncMinuteBars(code, windowStart, evaluatedThrough, ctx.CrawlTimeout, allowHeadBackfill)
	}
	applyMarketSummaryV150SyncInfo(&info, syncInfo)
	bars, gaps := buildMarketSummaryV150CompletedBars(raw, evaluatedThrough, frozen.Plan.ValidFromAt)
	if len(gaps) > 0 {
		reason := marketSummaryV150DataHealthReject + ": incomplete 15-minute data: " + strings.Join(gaps, "; ")
		return rejectMarketSummaryV150Activation(ctx, rec, &frozen, nil, evaluatedThrough, reason, info)
	}
	if len(bars) == 0 {
		if syncInfo.SyncErr != nil {
			reason := marketSummaryV150DataHealthReject + ": minute data unavailable: " + strings.TrimSpace(syncInfo.SyncErr.Error())
			return rejectMarketSummaryV150Activation(ctx, rec, &frozen, nil, evaluatedThrough, reason, info)
		}
		info.DataStatus = "待激活"
		info.DataStatusReason = "v1.5 is waiting for a complete 15-minute bar after validFrom"
		return nil, 0, info
	}

	state := v150.ActivationState{}
	executionPlan := frozen.Plan
	coveredCorporateActionDays := make(map[string]struct{})
	var previous v150.Bar
	for index := range bars {
		bar := bars[index]
		if bar.Start.Before(frozen.Plan.ValidFromAt) {
			previous = bar
			continue
		}
		barDayKey := bar.Start.In(cnLocation()).Format(time.DateOnly)
		if _, covered := coveredCorporateActionDays[barDayKey]; !covered {
			actions, actionErr := loadMarketSummaryV150CorporateActions(code, bar.Start, bar.Start)
			if actionErr != nil {
				reason := marketSummaryV150DataHealthReject + ": corporate action coverage unavailable before activation: " + actionErr.Error()
				return rejectMarketSummaryV150Activation(ctx, rec, &frozen, nil, bar.Start, reason, info)
			}
			if len(actions) > 0 {
				adjusted, adjustErr := v150.ApplyCorporateActionsToPlan(executionPlan, actions, bar.Start)
				if adjustErr != nil {
					reason := marketSummaryV150DataHealthReject + ": corporate action plan adjustment rejected: " + adjustErr.Error()
					return rejectMarketSummaryV150Activation(ctx, rec, &frozen, nil, bar.Start, reason, info)
				}
				factor := marketSummaryV150CorporateActionCombinedFactor(actions)
				previous = adjustMarketSummaryV150BarPriceBasis(previous, factor)
				executionPlan = adjusted
			}
			coveredCorporateActionDays[barDayKey] = struct{}{}
		}
		signal, nextState := v150.DetectActivation(executionPlan, previous, bar, state)
		state = nextState
		if signal.Reason == v150.RejectActivationExpired {
			at := bar.Start
			if err := ctx.appendMarketSummaryV150OrderEvents(rec, frozen.Run, []v150.OrderEvent{{
				Type: v150.OrderEventType("activation_expired"), At: at, Symbol: code, Reason: v150.RejectActivationExpired,
			}}, marketSummaryV150EventAccounting{}); err != nil {
				info.DataStatus = "无法判定"
				info.DataStatusReason = marketSummaryV150DataHealthReject + ": append activation expiry: " + err.Error()
				return nil, 0, info
			}
			info.DataStatus = "已过期"
			info.DataStatusReason = v150.RejectActivationExpired
			return nil, 0, info
		}
		if !signal.Triggered {
			previous = bar
			continue
		}

		security, securityErr := loadSecurityState(frozen.Run.RunID, code, signal.At)
		if securityErr != nil {
			reason := marketSummaryV150DataHealthReject + ": signal security status unavailable: " + marketSummaryV150SecurityRefreshFailure(securityErr, securityRefreshErr)
			return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, nextMarketSummaryV150TradingBucketStart(bar.Start), reason, info)
		}
		if !security.Tradable || security.Row.IsST {
			reason := marketSummaryV150DataHealthReject + ": security is not entry-tradable"
			return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, nextMarketSummaryV150TradingBucketStart(bar.Start), reason, info)
		}

		nextBar, exists := firstMarketSummaryV150BarAfter(bars, index)
		expectedStart := nextMarketSummaryV150TradingBucketStart(bar.Start)
		if !exists {
			expectedEnd := expectedStart.Add(15 * time.Minute)
			if !expectedStart.IsZero() && !evaluatedThrough.Before(expectedEnd) {
				reason := marketSummaryV150DataHealthReject + ": next complete 15-minute execution bar is missing"
				return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, expectedStart, reason, info)
			}
			info.DataStatus = "待激活"
			info.DataStatusReason = "v1.5 signal confirmed; waiting for the next complete 15-minute bar"
			return nil, 0, info
		}
		nextDayKey := nextBar.Start.In(cnLocation()).Format(time.DateOnly)
		if _, covered := coveredCorporateActionDays[nextDayKey]; !covered {
			actions, actionErr := loadMarketSummaryV150CorporateActions(code, nextBar.Start, nextBar.Start)
			if actionErr != nil {
				reason := marketSummaryV150DataHealthReject + ": corporate action coverage unavailable before fill: " + actionErr.Error()
				return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, nextBar.Start, reason, info)
			}
			if len(actions) > 0 {
				adjusted, adjustErr := v150.ApplyCorporateActionsToPlan(executionPlan, actions, nextBar.Start)
				if adjustErr != nil {
					reason := marketSummaryV150DataHealthReject + ": corporate action fill-plan adjustment rejected: " + adjustErr.Error()
					return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, nextBar.Start, reason, info)
				}
				factor := marketSummaryV150CorporateActionCombinedFactor(actions)
				signal.SignalClose *= factor
				executionPlan = adjusted
			}
			coveredCorporateActionDays[nextDayKey] = struct{}{}
		}

		fillSecurity, securityErr := loadSecurityState(frozen.Run.RunID, code, nextBar.Start)
		if securityErr != nil {
			reason := marketSummaryV150DataHealthReject + ": fill security status unavailable: " + marketSummaryV150SecurityRefreshFailure(securityErr, securityRefreshErr)
			return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, nextBar.Start, reason, info)
		}
		previousClose, priceErr := loadMarketSummaryV150PreviousClose(code, nextBar.Start, !ctx.DisableMinuteFetch)
		if priceErr != nil {
			reason := marketSummaryV150DataHealthReject + ": previous close unavailable: " + priceErr.Error()
			return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, nextBar.Start, reason, info)
		}
		decorateMarketSummaryV150Tradability(&nextBar, fillSecurity, previousClose)
		if !fillSecurity.Tradable || fillSecurity.Row.IsST {
			nextBar.Suspended = true
		}

		candidate := v150.Candidate{
			Symbol: code,
			Sector: strings.TrimSpace(frozen.Candidate.Sector),
			Market: v150.ResolveMarket(code),
		}
		order := v150.NewEntryOrder(signal, executionPlan, candidate)
		cfg := v150.FixedStrategyV150Config()
		marketSummaryV150BeforeEntryCriticalSection()
		marketSummaryV150ExecutionMu.Lock()
		portfolio, portfolioErr := loadMarketSummaryV150ExecutionAdmissionPortfolioState(db.Dao, frozen.Rule.RuleID, nextBar.Start)
		if portfolioErr != nil {
			marketSummaryV150ExecutionMu.Unlock()
			reason := marketSummaryV150DataHealthReject + ": point-in-time portfolio unavailable: " + portfolioErr.Error()
			return rejectMarketSummaryV150Activation(ctx, rec, &frozen, &signal, nextBar.Start, reason, info)
		}
		dailyCap := marketSummaryV150ExecutionDailyCap(frozen, cfg)
		portfolio.ExecutionDailyCap = &dailyCap
		fill := v150.TryFillEntryOnNextBar(order, nextBar, portfolio, cfg.PortfolioCash, cfg.SlippageScenarios()[0], cfg)
		if fill.Status != v150.FillFilled {
			events := []v150.OrderEvent{
				{Type: v150.EventSignal, At: signal.At, Symbol: code, Reason: string(signal.Path)},
				{Type: v150.EventOrder, At: nextBar.Start, Symbol: code, Reason: "next_bar_market_order"},
			}
			events = append(events, fill.Events...)
			if err := ctx.appendMarketSummaryV150OrderEvents(rec, frozen.Run, events, marketSummaryV150EventAccounting{}); err != nil {
				fill.Reason = firstNonEmptyText(fill.Reason, "entry rejected") + "; append ledger: " + err.Error()
			}
			marketSummaryV150ExecutionMu.Unlock()
			info.DataStatus = "已跳过"
			info.DataStatusReason = firstNonEmptyText(fill.Reason, "v1.5 entry rejected")
			return nil, 0, info
		}
		if err := ctx.appendMarketSummaryV150OrderEvents(rec, frozen.Run, fill.Events, marketSummaryV150EventAccounting{Entry: &fill.Cost}); err != nil {
			marketSummaryV150ExecutionMu.Unlock()
			info.DataStatus = "已跳过"
			info.DataStatusReason = marketSummaryV150DataHealthReject + ": append entry lifecycle: " + err.Error()
			return nil, 0, info
		}
		marketSummaryV150ExecutionMu.Unlock()

		at := fill.At
		info.ActivationTime = &at
		// State prices remain raw exchange prices because the version-aware
		// yield calculator applies the same fixed 10bp slippage.  The immutable
		// fill event stores the effective price and exact share quantity.
		info.ActivationPrice = fill.Cost.RawPrice
		info.V150Entry = &marketSummaryV150EntryExecution{Plan: fill.Plan, Position: fill.Position, Cost: fill.Cost}
		return &at, fill.Cost.RawPrice, info
	}

	if syncInfo.SyncErr != nil {
		info.DataStatus = "无法判定"
		info.DataStatusReason = marketSummaryV150DataHealthReject + ": minute data not fully covered: " + strings.TrimSpace(syncInfo.SyncErr.Error())
		return nil, 0, info
	}
	info.DataStatus = "待激活"
	info.DataStatusReason = "v1.5 activation condition has not triggered"
	return nil, 0, info
}

// marketSummaryV150ExecutionDailyCap preserves the regime that was frozen
// when the rule was issued. A neutral rule must never inherit the wider
// risk-on ceiling merely because it activates in a later recalculation. Older
// or incomplete payloads fall back to rule metadata: breakout can only be
// produced in risk-on, while pullback uses the conservative neutral ceiling.
func marketSummaryV150ExecutionDailyCap(frozen marketSummaryV150FrozenExecutionPlan, cfg v150.StrategyV150Config) int {
	var payload struct {
		Run struct {
			Regime v150.RegimeDecision `json:"regime"`
		} `json:"run"`
	}
	if json.Unmarshal([]byte(frozen.Run.PayloadJSON), &payload) == nil {
		declared := payload.Run.Regime.DailyCap
		switch payload.Run.Regime.Regime {
		case v150.RegimeRiskOn:
			return boundedMarketSummaryV150ExecutionDailyCap(declared, cfg.RiskOnDailyCap)
		case v150.RegimeNeutral:
			return boundedMarketSummaryV150ExecutionDailyCap(declared, cfg.NeutralDailyCap)
		case v150.RegimeRiskOff:
			return 0
		}
	}
	if frozen.Plan.Path == v150.PathBreakout || strings.EqualFold(strings.TrimSpace(frozen.Rule.Path), string(v150.PathBreakout)) {
		return maxInt(cfg.RiskOnDailyCap, 0)
	}
	return maxInt(cfg.NeutralDailyCap, 0)
}

func boundedMarketSummaryV150ExecutionDailyCap(declared, ceiling int) int {
	if ceiling <= 0 {
		return 0
	}
	if declared <= 0 || declared > ceiling {
		return ceiling
	}
	return declared
}

func evaluateMarketSummaryV150Exit(rec models.AiRecommendStocks, entry marketSummaryV150EntryExecution, ctx yieldBuildContext, allowHeadBackfill bool) (string, time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常"}
	if entry.Position.EntryAt.IsZero() || entry.Position.Quantity <= 0 {
		info.DataStatus = "无法判定"
		info.DataStatusReason = marketSummaryV150DataHealthReject + ": entry position is incomplete"
		return "", time.Time{}, 0, info
	}
	frozen, err := loadMarketSummaryV150FrozenExecutionPlan(rec)
	if err != nil {
		info.DataStatus = "无法判定"
		info.DataStatusReason = marketSummaryV150DataHealthReject + ": " + err.Error()
		return "", time.Time{}, 0, info
	}
	scanEnd := marketSummaryV150ExecutionEvaluatedThrough(ctx)
	if scanEnd.IsZero() || !scanEnd.After(entry.Position.EntryAt) {
		return "", time.Time{}, 0, info
	}
	code := normalizeRecommendStockCode(rec.StockCode)
	var securityRefreshErr error
	loadSecurityState := loadMarketSummaryV150SecurityState
	if ctx.RequireV150ExecutionObservation {
		loadSecurityState = loadMarketSummaryV150ExecutionObservationState
	}
	if !ctx.DisableMinuteFetch {
		_, securityRefreshErr = refreshMarketSummaryV150ExecutionSecurityObservation(frozen.Run.RunID, code, true)
		loadSecurityState = loadMarketSummaryV150ExecutionObservationState
	}
	var raw []minuteBar
	var syncInfo minuteSyncInfo
	if ctx.DisableMinuteFetch {
		raw, syncInfo = syncMinuteBarsFromCacheOnly(code, entry.Position.EntryAt, scanEnd)
	} else {
		raw, syncInfo = syncMinuteBars(code, entry.Position.EntryAt, scanEnd, ctx.CrawlTimeout, allowHeadBackfill)
	}
	applyMarketSummaryV150SyncInfo(&info, syncInfo)
	bars, gaps := buildMarketSummaryV150CompletedBars(raw, scanEnd, entry.Position.EntryAt)
	if len(gaps) > 0 {
		info.DataStatus = "无法判定"
		info.DataStatusReason = marketSummaryV150DataHealthReject + ": incomplete exit 15-minute data: " + strings.Join(gaps, "; ")
		return "", time.Time{}, 0, info
	}
	position := entry.Position
	cfg := v150.FixedStrategyV150Config()
	coveredCorporateActionDays := map[string]struct{}{
		position.EntryAt.In(cnLocation()).Format(time.DateOnly): {},
	}
	for index := range bars {
		bar := bars[index]
		if bar.Start.Before(position.EntryAt) {
			continue
		}
		barDayKey := bar.Start.In(cnLocation()).Format(time.DateOnly)
		if _, covered := coveredCorporateActionDays[barDayKey]; !covered {
			actions, actionErr := loadMarketSummaryV150CorporateActions(code, bar.Start, bar.Start)
			if actionErr != nil {
				info.DataStatus = "鏃犳硶鍒ゅ畾"
				info.DataStatusReason = marketSummaryV150DataHealthReject + ": corporate action coverage unavailable before exit evaluation: " + actionErr.Error()
				return "", time.Time{}, 0, info
			}
			if len(actions) > 0 {
				application, applyErr := v150.ApplyCorporateActions(position, actions, bar.Start)
				if applyErr != nil {
					info.DataStatus = "鏃犳硶鍒ゅ畾"
					info.DataStatusReason = marketSummaryV150DataHealthReject + ": corporate action position adjustment rejected: " + applyErr.Error()
					return "", time.Time{}, 0, info
				}
				if err := ctx.appendMarketSummaryV150OrderEvents(rec, frozen.Run, application.Events, marketSummaryV150EventAccounting{}); err != nil {
					info.DataStatus = "鏃犳硶鍒ゅ畾"
					info.DataStatusReason = marketSummaryV150DataHealthReject + ": append corporate action lifecycle: " + err.Error()
					return "", time.Time{}, 0, info
				}
				position = application.Position
			}
			coveredCorporateActionDays[barDayKey] = struct{}{}
		}
		barSecurityState := loadSecurityState
		if bar.TradeDayIndex <= position.EntryTradeDayIndex {
			// A-share T+1 makes an exit impossible on the entry trade day. Use
			// the causally frozen same-day security snapshot for those bars so
			// an observation made on the next trading day is never expected to
			// authorize (or backdate) an earlier checkpoint. Every sell-eligible
			// day still requires the dedicated execution-day observation.
			barSecurityState = loadMarketSummaryV150SecurityState
		}
		security, securityErr := barSecurityState(frozen.Run.RunID, code, bar.Start)
		if securityErr != nil {
			info.DataStatus = "无法判定"
			info.DataStatusReason = marketSummaryV150DataHealthReject + ": exit security status unavailable: " + marketSummaryV150SecurityRefreshFailure(securityErr, securityRefreshErr)
			return "", time.Time{}, 0, info
		}
		previousClose, priceErr := loadMarketSummaryV150PreviousClose(code, bar.Start, !ctx.DisableMinuteFetch)
		if priceErr != nil {
			info.DataStatus = "无法判定"
			info.DataStatusReason = marketSummaryV150DataHealthReject + ": exit previous close unavailable: " + priceErr.Error()
			return "", time.Time{}, 0, info
		}
		decorateMarketSummaryV150Tradability(&bar, security, previousClose)
		result := v150.EvaluateExit(position, bar, cfg.SlippageScenarios()[0], cfg)
		if result.Triggered {
			if err := ctx.appendMarketSummaryV150OrderEvents(rec, frozen.Run, result.Events, marketSummaryV150EventAccounting{Exit: &result.Cost}); err != nil {
				info.DataStatus = "无法判定"
				info.DataStatusReason = marketSummaryV150DataHealthReject + ": append exit lifecycle: " + err.Error()
				return "", time.Time{}, 0, info
			}
			status := "已止损"
			switch result.Reason {
			case v150.ExitTarget:
				status = "已止盈"
			case v150.ExitTime:
				status = marketSummaryV150TimeExitStatus
			}
			return status, result.At, result.Cost.RawPrice, info
		}
		position = v150.AdvanceTrailingStop(position, bar, cfg)
	}
	if syncInfo.SyncErr != nil && len(bars) == 0 {
		info.DataStatus = "无法判定"
		info.DataStatusReason = marketSummaryV150DataHealthReject + ": exit minute data unavailable: " + strings.TrimSpace(syncInfo.SyncErr.Error())
	}
	return "", time.Time{}, 0, info
}

func marketSummaryV150ExecutionEvaluatedThrough(ctx yieldBuildContext) time.Time {
	if !ctx.V150EvaluationCutoff.IsZero() {
		return normalizeMinuteCoverageEnd(ctx.V150EvaluationCutoff)
	}
	return normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate))
}

func loadMarketSummaryV150FrozenExecutionPlan(rec models.AiRecommendStocks) (marketSummaryV150FrozenExecutionPlan, error) {
	var result marketSummaryV150FrozenExecutionPlan
	if db.Dao == nil {
		return result, errors.New("strategy database is unavailable")
	}
	runID := strings.TrimSpace(rec.StrategyRunID)
	ruleID := strings.TrimSpace(rec.StrategyRuleID)
	if runID == "" || ruleID == "" {
		return result, errors.New("strategyRunId/strategyRuleId is missing")
	}
	if err := db.Dao.Where("run_id = ? AND strategy_version = ? AND frozen_at IS NOT NULL", runID, v150.StrategyVersion).First(&result.Run).Error; err != nil {
		return result, fmt.Errorf("frozen strategy run unavailable: %w", err)
	}
	if err := db.Dao.Where("rule_id = ? AND run_id = ? AND strategy_version = ? AND frozen_at IS NOT NULL", ruleID, runID, v150.StrategyVersion).First(&result.Rule).Error; err != nil {
		return result, fmt.Errorf("frozen strategy rule unavailable: %w", err)
	}
	if strings.TrimSpace(result.Rule.CandidateID) == "" {
		return result, errors.New("frozen strategy rule has no candidate identity")
	}
	if err := db.Dao.Where("candidate_id = ? AND run_id = ? AND strategy_version = ? AND frozen_at IS NOT NULL", result.Rule.CandidateID, runID, v150.StrategyVersion).First(&result.Candidate).Error; err != nil {
		return result, fmt.Errorf("frozen strategy candidate unavailable: %w", err)
	}
	if result.Run.ValidFromAt == nil || result.Run.ValidFromAt.IsZero() || result.Run.DecisionAt.IsZero() || !result.Run.DecisionAt.Before(*result.Run.ValidFromAt) || result.Run.DataCutoffAt.After(result.Run.DecisionAt) {
		return result, errors.New("frozen strategy run violates cutoff <= decision < validFrom")
	}
	if result.Rule.ValidFromAt.IsZero() || !result.Rule.ValidFromAt.Equal(*result.Run.ValidFromAt) {
		return result, errors.New("frozen rule validFrom does not match its run")
	}
	if normalizeRecommendStockCode(result.Rule.Symbol) != normalizeRecommendStockCode(rec.StockCode) {
		return result, errors.New("frozen rule symbol does not match recommendation")
	}
	if normalizeRecommendStockCode(result.Candidate.Symbol) != normalizeRecommendStockCode(result.Rule.Symbol) || strings.TrimSpace(result.Candidate.Sector) == "" {
		return result, errors.New("frozen strategy candidate symbol/sector is invalid")
	}
	var payload struct {
		Production struct {
			Plan v150.TradePlan `json:"plan"`
		} `json:"production"`
	}
	if err := json.Unmarshal([]byte(result.Rule.PayloadJSON), &payload); err != nil {
		return result, fmt.Errorf("decode frozen rule payload: %w", err)
	}
	result.Plan = payload.Production.Plan
	if result.Plan.Symbol == "" || normalizeRecommendStockCode(result.Plan.Symbol) != normalizeRecommendStockCode(rec.StockCode) {
		return result, errors.New("frozen V1.5 plan symbol is invalid")
	}
	if result.Plan.Path != v150.PathPullback && result.Plan.Path != v150.PathBreakout {
		return result, errors.New("frozen V1.5 plan path is invalid")
	}
	if result.Plan.ValidFromAt.IsZero() || !result.Plan.ValidFromAt.Equal(result.Rule.ValidFromAt) || result.Plan.EvaluationMinutes != 15 || result.Plan.ValidTradeDays <= 0 || result.Plan.MaxHoldTradeDays <= 0 || result.Plan.ATR14 <= 0 {
		return result, errors.New("frozen V1.5 plan is incomplete")
	}
	expectedValidFromIndex := marketSummaryV150TradeDayIndex(result.Plan.ValidFromAt)
	if expectedValidFromIndex <= 0 {
		return result, errors.New("frozen V1.5 plan has an invalid validFrom trading-day index")
	}
	if result.Plan.ValidFromTradeDayIndex == 0 {
		// Compatibility for snapshots written before the explicit anchor was
		// introduced. Derive it from immutable validFrom rather than silently
		// falling back to the (possibly prior-day) decision index.
		result.Plan.ValidFromTradeDayIndex = expectedValidFromIndex
	} else if result.Plan.ValidFromTradeDayIndex != expectedValidFromIndex {
		return result, errors.New("frozen V1.5 plan validFrom trading-day index does not match validFromAt")
	}
	if result.Plan.Path == v150.PathPullback && (result.Plan.EntryMin <= 0 || result.Plan.EntryMax < result.Plan.EntryMin || result.Plan.Support <= 0) {
		return result, errors.New("frozen pullback plan is incomplete")
	}
	if result.Plan.Path == v150.PathBreakout && result.Plan.Trigger <= 0 {
		return result, errors.New("frozen breakout plan is incomplete")
	}
	return result, nil
}

// loadMarketSummaryV150ExecutionPortfolioState reconstructs the portfolio at
// the exchange event time from the immutable V1.5 rule and order-event ledgers.
// It deliberately does not read AiRecommendYieldRecordState: that table is a
// mutable projection which may already contain facts learned after asOf.
//
// The rule currently attempting its fill is excluded because its own
// rule_issued/signal/order lifecycle is expected to be pending at this point.
// Every other frozen rule is classified using only events whose EventAt is not
// later than asOf, so a later exit/fill cannot rewrite a historical decision.
func loadMarketSummaryV150ExecutionPortfolioState(database *gorm.DB, currentRuleID string, asOf time.Time) (v150.PortfolioState, error) {
	return loadMarketSummaryV150ExecutionPortfolioStateWithIngestionPolicy(database, currentRuleID, "", asOf, true)
}

// loadMarketSummaryV150ExecutionAdmissionPortfolioState is used only inside
// the serialized read-check-append entry critical section. A fill already
// committed by an earlier symbol in the same completed bar must consume
// portfolio capacity immediately even though its immutable FrozenAt is later
// than the exchange EventAt being replayed. EventAt still provides the causal
// boundary, while the ordinary replay loader above retains FrozenAt filtering
// for historical point-in-time inspection.
func loadMarketSummaryV150ExecutionAdmissionPortfolioState(database *gorm.DB, currentRuleID string, asOf time.Time) (v150.PortfolioState, error) {
	return loadMarketSummaryV150ExecutionPortfolioStateWithIngestionPolicy(database, currentRuleID, "", asOf, false)
}

// loadMarketSummaryV150ExecutionPortfolioStateWithIngestionPolicy keeps the
// execution replay causal by default, while allowing the final publication
// boundary to include every immutable fact already committed to the database.
// The latter is required for serialized historical/backfill publications:
// their immutable frozen_at is necessarily later than the historical event
// time, but a later overlapping publisher must still observe the earlier
// committed rule before consuming the same shared quota.
func loadMarketSummaryV150ExecutionPortfolioStateWithIngestionPolicy(
	database *gorm.DB,
	currentRuleID string,
	excludedRunID string,
	asOf time.Time,
	requireFrozenByAsOf bool,
) (v150.PortfolioState, error) {
	state := cloneV150PortfolioState(v150.PortfolioState{})
	if database == nil {
		return state, errors.New("strategy database is unavailable")
	}
	if asOf.IsZero() {
		return state, errors.New("portfolio event time is missing")
	}
	for _, table := range []any{&models.RuleSnapshot{}, &models.CandidateSnapshot{}, &models.OrderEvent{}} {
		if !database.Migrator().HasTable(table) {
			return state, fmt.Errorf("immutable portfolio table %T is unavailable", table)
		}
	}

	var rules []models.RuleSnapshot
	if err := database.Model(&models.RuleSnapshot{}).
		Where("strategy_version = ? AND frozen_at IS NOT NULL", v150.StrategyVersion).
		Find(&rules).Error; err != nil {
		return state, err
	}
	var candidates []models.CandidateSnapshot
	if err := database.Model(&models.CandidateSnapshot{}).
		Where("strategy_version = ? AND frozen_at IS NOT NULL", v150.StrategyVersion).
		Find(&candidates).Error; err != nil {
		return state, err
	}
	var events []models.OrderEvent
	if err := database.Model(&models.OrderEvent{}).
		Where("strategy_version = ? AND frozen_at IS NOT NULL", v150.StrategyVersion).
		Find(&events).Error; err != nil {
		return state, err
	}

	sectorByCandidate := make(map[string]string, len(candidates))
	sectorByRunSymbol := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		if candidate.FrozenAt == nil || (requireFrozenByAsOf && candidate.FrozenAt.After(asOf)) ||
			strings.TrimSpace(candidate.RunID) == strings.TrimSpace(excludedRunID) {
			continue
		}
		sector := strings.TrimSpace(candidate.Sector)
		sectorByCandidate[strings.TrimSpace(candidate.CandidateID)] = sector
		sectorByRunSymbol[strings.TrimSpace(candidate.RunID)+"|"+normalizeRecommendStockCode(candidate.Symbol)] = sector
	}
	eventsByRule := make(map[string][]models.OrderEvent)
	for _, event := range events {
		if event.FrozenAt == nil || (requireFrozenByAsOf && event.FrozenAt.After(asOf)) || event.EventAt.IsZero() || event.EventAt.After(asOf) ||
			strings.TrimSpace(event.RunID) == strings.TrimSpace(excludedRunID) {
			continue
		}
		ruleID := strings.TrimSpace(event.RuleID)
		if ruleID == "" || ruleID == strings.TrimSpace(currentRuleID) {
			continue
		}
		eventsByRule[ruleID] = append(eventsByRule[ruleID], event)
	}
	for ruleID := range eventsByRule {
		sort.SliceStable(eventsByRule[ruleID], func(i, j int) bool {
			left, right := eventsByRule[ruleID][i], eventsByRule[ruleID][j]
			if left.Sequence != right.Sequence {
				return left.Sequence < right.Sequence
			}
			if !left.EventAt.Equal(right.EventAt) {
				return left.EventAt.Before(right.EventAt)
			}
			return left.EventID < right.EventID
		})
	}

	todayStart, todayEnd := marketSummaryDayBounds(asOf)
	seenTodayEntries := make(map[string]struct{})
	currentRuleID = strings.TrimSpace(currentRuleID)
	for _, rule := range rules {
		ruleID := strings.TrimSpace(rule.RuleID)
		if ruleID == "" || ruleID == currentRuleID || rule.FrozenAt == nil || (requireFrozenByAsOf && rule.FrozenAt.After(asOf)) ||
			strings.TrimSpace(rule.RunID) == strings.TrimSpace(excludedRunID) ||
			!strings.EqualFold(strings.TrimSpace(rule.RuleType), "entry") {
			continue
		}
		symbol := normalizeRecommendStockCode(rule.Symbol)
		if symbol == "" {
			return state, fmt.Errorf("frozen entry rule %s has no symbol", ruleID)
		}
		sector := sectorByCandidate[strings.TrimSpace(rule.CandidateID)]
		if sector == "" {
			sector = sectorByRunSymbol[strings.TrimSpace(rule.RunID)+"|"+symbol]
		}

		issued := false
		signaled := false
		open := false
		terminal := false
		for _, event := range eventsByRule[ruleID] {
			eventType := strings.ToLower(strings.TrimSpace(event.EventType))
			switch eventType {
			case "rule_issued":
				issued = true
			case string(v150.EventSignal), string(v150.EventOrder):
				signaled = true
			case string(v150.EventFill):
				open = true
				terminal = false
				entryKey := ruleID + "|" + symbol
				if !event.EventAt.Before(todayStart) && event.EventAt.Before(todayEnd) {
					if _, exists := seenTodayEntries[entryKey]; !exists {
						seenTodayEntries[entryKey] = struct{}{}
						state.TodayEntries++
						if sector != "" {
							state.TodaySectorEntries[sector]++
						}
					}
				}
			case string(v150.EventExitFill):
				open = false
				terminal = true
				if strings.EqualFold(strings.TrimSpace(event.Reason), string(v150.ExitStop)) {
					days := marketSummaryV150OpenTradeDaysBetween(event.EventAt, asOf)
					if existing, exists := state.TradeDaysSinceLastStop[symbol]; !exists || days < existing {
						state.TradeDaysSinceLastStop[symbol] = days
					}
				}
			case string(v150.EventReject), "activation_expired", "expired":
				terminal = true
			}
		}
		if !issued {
			return state, fmt.Errorf("frozen entry rule %s has no causal rule_issued event", ruleID)
		}
		if open {
			state.OpenSymbols[symbol] = true
			continue
		}
		expired := rule.ExpiresAt != nil && asOf.After(*rule.ExpiresAt)
		if !terminal && (!expired || signaled) {
			state.PendingSymbols[symbol] = true
		}
	}
	return state, nil
}

func buildMarketSummaryV150CompletedBars(raw []minuteBar, evaluatedThrough, requiredFrom time.Time) ([]v150.Bar, []string) {
	type bucket struct {
		start   time.Time
		minutes map[int64]minuteBar
	}
	buckets := map[int64]*bucket{}
	endLabeledDays := detectMarketSummaryV150EndLabeledDays(raw, requiredFrom)
	for _, row := range raw {
		if row.TradeTime.IsZero() {
			continue
		}
		logicalMinute := normalizeMinuteTime(row.TradeTime.In(cnLocation()))
		if endLabeledDays[logicalMinute.Format(time.DateOnly)] {
			logicalMinute = logicalMinute.Add(-time.Minute)
		}
		start := marketSummary15MinuteBucketStart(logicalMinute)
		if _, ok := marketSummaryV150TradingSlot(start); !ok {
			continue
		}
		end := start.Add(15 * time.Minute)
		if end.After(evaluatedThrough) {
			continue
		}
		key := start.UnixNano()
		item := buckets[key]
		if item == nil {
			item = &bucket{start: start, minutes: map[int64]minuteBar{}}
			buckets[key] = item
		}
		item.minutes[logicalMinute.UnixNano()] = row
	}
	ordered := make([]*bucket, 0, len(buckets))
	for _, item := range buckets {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start.Before(ordered[j].start) })
	result := make([]v150.Bar, 0, len(ordered))
	gaps := make([]string, 0)
	for _, item := range ordered {
		if len(item.minutes) != 15 {
			if !item.start.Before(requiredFrom) {
				gaps = append(gaps, fmt.Sprintf("%s has %d/15 minutes", item.start.Format("2006-01-02 15:04"), len(item.minutes)))
			}
			continue
		}
		rows := make([]minuteBar, 0, 15)
		complete := true
		for offset := 0; offset < 15; offset++ {
			at := item.start.Add(time.Duration(offset) * time.Minute)
			row, ok := item.minutes[at.UnixNano()]
			if !ok || row.Open <= 0 || row.High <= 0 || row.Low <= 0 || row.Close <= 0 || row.High < row.Low {
				complete = false
				break
			}
			rows = append(rows, row)
		}
		if !complete {
			if !item.start.Before(requiredFrom) {
				gaps = append(gaps, item.start.Format("2006-01-02 15:04")+" has missing/invalid OHLC")
			}
			continue
		}
		slot, _ := marketSummaryV150TradingSlot(item.start)
		tradeDayIndex := marketSummaryV150TradeDayIndex(item.start)
		bar := v150.Bar{
			Index:           tradeDayIndex*16 + slot,
			TradeDayIndex:   tradeDayIndex,
			IntervalMinutes: 15,
			Start:           item.start,
			End:             item.start.Add(15*time.Minute - time.Nanosecond),
			Open:            rows[0].Open,
			High:            rows[0].High,
			Low:             rows[0].Low,
			Close:           rows[len(rows)-1].Close,
			Completed:       true,
		}
		for _, row := range rows {
			bar.High = math.Max(bar.High, row.High)
			bar.Low = math.Min(bar.Low, row.Low)
			bar.Volume += row.Volume
			bar.Amount += row.Amount
		}
		result = append(result, bar)
	}
	applyMarketSummaryV150SameSlotVolumeRatios(result)
	completeStarts := make(map[int64]bool, len(result))
	for _, bar := range result {
		completeStarts[bar.Start.UnixNano()] = true
	}
	for _, expected := range marketSummaryV150ExpectedCompletedBucketStarts(requiredFrom, evaluatedThrough) {
		if !completeStarts[expected.UnixNano()] {
			gaps = append(gaps, expected.Format("2006-01-02 15:04")+" bucket is entirely missing")
		}
	}
	gaps = dedupeNonEmptyStrings(gaps, 64)
	if len(gaps) > 3 {
		gaps = append(gaps[:3], fmt.Sprintf("and %d more gaps", len(gaps)-3))
	}
	return result, gaps
}

func marketSummaryV150ExpectedCompletedBucketStarts(requiredFrom, evaluatedThrough time.Time) []time.Time {
	if requiredFrom.IsZero() || evaluatedThrough.IsZero() || !evaluatedThrough.After(requiredFrom) {
		return nil
	}
	current := requiredFrom.In(cnLocation())
	if _, ok := marketSummaryV150TradingSlot(current); !ok {
		return nil
	}
	result := make([]time.Time, 0, 64)
	// Activation is at most three trade days and an open position at most ten,
	// so this guard is far beyond any legitimate execution window while still
	// protecting corrupted timestamps from an unbounded loop.
	for guard := 0; guard < 16*15; guard++ {
		if current.Add(15 * time.Minute).After(evaluatedThrough) {
			break
		}
		result = append(result, current)
		next := nextMarketSummaryV150TradingBucketStart(current)
		if next.IsZero() || !next.After(current) {
			break
		}
		current = next
	}
	return result
}

// Providers disagree on whether a one-minute bar is labelled by its start
// (09:30..09:44) or end (09:31..09:45).  Normalize both conventions before
// checking the strict 15/15-minute coverage requirement.
func detectMarketSummaryV150EndLabeledDays(raw []minuteBar, requiredFrom time.Time) map[string]bool {
	timesByDay := map[string]map[int]bool{}
	for _, row := range raw {
		if row.TradeTime.IsZero() {
			continue
		}
		local := row.TradeTime.In(cnLocation())
		day := local.Format(time.DateOnly)
		if timesByDay[day] == nil {
			timesByDay[day] = map[int]bool{}
		}
		timesByDay[day][local.Hour()*60+local.Minute()] = true
	}
	result := map[string]bool{}
	required := requiredFrom.In(cnLocation())
	for day, minutes := range timesByDay {
		switch {
		case minutes[9*60+30] || minutes[13*60]:
			result[day] = false
		case minutes[9*60+31] || minutes[13*60+1]:
			result[day] = true
		case day == required.Format(time.DateOnly):
			anchor := required.Hour()*60 + required.Minute()
			result[day] = !minutes[anchor] && minutes[anchor+1]
		}
	}
	return result
}

func applyMarketSummaryV150SameSlotVolumeRatios(bars []v150.Bar) {
	history := map[int][]v150.Bar{}
	for index := range bars {
		slot := bars[index].Index % 16
		prior := history[slot]
		values := make([]float64, 0, 5)
		seenDays := map[int]bool{}
		for i := len(prior) - 1; i >= 0 && len(values) < 5; i-- {
			if seenDays[prior[i].TradeDayIndex] || prior[i].TradeDayIndex >= bars[index].TradeDayIndex {
				continue
			}
			seenDays[prior[i].TradeDayIndex] = true
			metric := prior[i].Amount
			if metric <= 0 {
				metric = prior[i].Volume
			}
			if metric > 0 {
				values = append(values, metric)
			}
		}
		metric := bars[index].Amount
		if metric <= 0 {
			metric = bars[index].Volume
		}
		if len(values) > 0 && metric > 0 {
			total := 0.0
			for _, value := range values {
				total += value
			}
			bars[index].VolumeRatioSameSlot = metric / (total / float64(len(values)))
		}
		history[slot] = append(history[slot], bars[index])
	}
}

func marketSummaryV150CorporateActionCombinedFactor(actions []v150.CorporateAction) float64 {
	factor := 1.0
	for _, action := range actions {
		if action.AdjustmentFactor > 0 {
			factor *= action.AdjustmentFactor
		}
	}
	return factor
}

func adjustMarketSummaryV150BarPriceBasis(bar v150.Bar, factor float64) v150.Bar {
	if factor <= 0 || factor == 1 || bar.Start.IsZero() {
		return bar
	}
	bar.Open *= factor
	bar.High *= factor
	bar.Low *= factor
	bar.Close *= factor
	return bar
}

func marketSummaryV150TradingSlot(start time.Time) (int, bool) {
	if start.IsZero() {
		return 0, false
	}
	local := start.In(cnLocation())
	minute := local.Hour()*60 + local.Minute()
	switch {
	case minute >= 9*60+30 && minute < 11*60+30 && (minute-(9*60+30))%15 == 0:
		return (minute - (9*60 + 30)) / 15, true
	case minute >= 13*60 && minute < 15*60 && (minute-13*60)%15 == 0:
		return 8 + (minute-13*60)/15, true
	default:
		return 0, false
	}
}

func nextMarketSummaryV150TradingBucketStart(start time.Time) time.Time {
	slot, ok := marketSummaryV150TradingSlot(start)
	if !ok {
		return time.Time{}
	}
	local := start.In(cnLocation())
	switch {
	case slot < 7:
		return local.Add(15 * time.Minute)
	case slot == 7:
		return time.Date(local.Year(), local.Month(), local.Day(), 13, 0, 0, 0, local.Location())
	case slot < 15:
		return local.Add(15 * time.Minute)
	default:
		next := shiftToNextCNOpenTradeDaySafe(local.AddDate(0, 0, 1))
		return time.Date(next.Year(), next.Month(), next.Day(), 9, 30, 0, 0, local.Location())
	}
}

func firstMarketSummaryV150BarAfter(bars []v150.Bar, current int) (v150.Bar, bool) {
	if current < 0 || current+1 >= len(bars) {
		return v150.Bar{}, false
	}
	return bars[current+1], true
}

func loadMarketSummaryV150SecurityState(runID, symbol string, at time.Time) (marketSummaryV150SecurityState, error) {
	return loadMarketSummaryV150SecurityStateWithMode(runID, symbol, at, false)
}

func loadMarketSummaryV150ExecutionObservationState(runID, symbol string, at time.Time) (marketSummaryV150SecurityState, error) {
	return loadMarketSummaryV150SecurityStateWithMode(runID, symbol, at, true)
}

func loadMarketSummaryV150SecurityStateWithMode(runID, symbol string, at time.Time, requireExecutionObservation bool) (marketSummaryV150SecurityState, error) {
	var result marketSummaryV150SecurityState
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.SecurityMasterHistory{}) || !db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) {
		return result, errors.New("security_master_history is unavailable")
	}
	symbol = normalizeRecommendStockCode(symbol)
	runID = strings.TrimSpace(runID)
	if runID == "" || symbol == "" || at.IsZero() {
		return result, errors.New("strategy run id is unavailable for security lookup")
	}
	find := func() error {
		var candidates []models.SecurityMasterHistory
		query := db.Dao.Model(&models.SecurityMasterHistory{}).
			Where("snapshot_version = ? AND upper(symbol) = ? AND frozen_at IS NOT NULL", v150.StrategyVersion, symbol)
		if err := query.Find(&candidates).Error; err != nil {
			return err
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if !candidates[i].EffectiveFrom.Equal(candidates[j].EffectiveFrom) {
				return candidates[i].EffectiveFrom.After(candidates[j].EffectiveFrom)
			}
			leftFrozen, rightFrozen := time.Time{}, time.Time{}
			if candidates[i].FrozenAt != nil {
				leftFrozen = *candidates[i].FrozenAt
			}
			if candidates[j].FrozenAt != nil {
				rightFrozen = *candidates[j].FrozenAt
			}
			if !leftFrozen.Equal(rightFrozen) {
				return leftFrozen.After(rightFrozen)
			}
			return candidates[i].ID > candidates[j].ID
		})
		for _, candidate := range candidates {
			// Security status is a daily execution prerequisite, not a permanent
			// property inferred from the recommendation-day snapshot. A prior-day
			// row must never silently authorize a later signal/order/fill.
			if candidate.EffectiveFrom.After(at) ||
				!normalizeDailyTradeDate(candidate.EffectiveFrom).Equal(normalizeDailyTradeDate(at)) ||
				(candidate.EffectiveTo != nil && !candidate.EffectiveTo.After(at)) || candidate.FrozenAt == nil || candidate.FrozenAt.After(at) {
				continue
			}
			var sourceRun models.StrategyRunSnapshot
			if err := db.Dao.Where("run_id = ? AND strategy_version = ? AND frozen_at IS NOT NULL", candidate.RunID, v150.StrategyVersion).First(&sourceRun).Error; err != nil {
				continue
			}
			if sourceRun.FrozenAt == nil || sourceRun.FrozenAt.After(at) || sourceRun.DecisionAt.After(at) || sourceRun.DataCutoffAt.After(at) ||
				sourceRun.TradeDate != at.In(cnLocation()).Format(time.DateOnly) {
				continue
			}
			if requireExecutionObservation && !strings.EqualFold(strings.TrimSpace(sourceRun.Mode), marketSummaryV150ExecutionSecurityObservationMode) {
				continue
			}
			if err := validateMarketSummaryV150SecurityAvailability(candidate, at); err != nil {
				continue
			}
			result.Row = candidate
			return nil
		}
		return gorm.ErrRecordNotFound
	}
	// Choose the most recent same-day observation from any immutable V1.5 run.
	// A later observation supersedes an earlier one only for events at or after
	// its effective/available/frozen times.
	err := find()
	if err != nil {
		return result, fmt.Errorf("no causally available execution-day security state for %s at %s: %w", symbol, at.Format(time.DateTime), err)
	}
	status := strings.ToUpper(strings.TrimSpace(result.Row.Status))
	switch status {
	case "L", "LISTED", "ACTIVE", "TRADING", "NORMAL":
		result.Tradable = !result.Row.IsSuspended
	case "P", "SUSPENDED", "HALTED", "DELISTED", "D":
		result.Tradable = false
	default:
		return result, fmt.Errorf("unknown frozen security status %q", result.Row.Status)
	}
	return result, nil
}

func validateMarketSummaryV150SecurityAvailability(row models.SecurityMasterHistory, at time.Time) error {
	var frozenPayload struct {
		Security struct {
			AvailableAt string `json:"availableAt"`
		} `json:"security"`
	}
	if err := json.Unmarshal([]byte(row.PayloadJSON), &frozenPayload); err != nil {
		return err
	}
	if strings.TrimSpace(frozenPayload.Security.AvailableAt) == "" {
		return errors.New("security source availability is missing")
	}
	availableAt, ok := parseMarketSummaryV150EvidenceTime(frozenPayload.Security.AvailableAt)
	if !ok || availableAt.After(at) || !normalizeDailyTradeDate(availableAt).Equal(normalizeDailyTradeDate(at)) {
		return errors.New("security source availability is not causal")
	}
	return nil
}

func marketSummaryV150SecurityRefreshFailure(lookupErr, refreshErr error) string {
	if lookupErr == nil {
		return ""
	}
	if refreshErr == nil {
		return lookupErr.Error()
	}
	return lookupErr.Error() + "; online refresh failed: " + refreshErr.Error()
}

func loadMarketSummaryV150PreviousClose(symbol string, at time.Time, allowFetch bool) (float64, error) {
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.AiRecommendDailyBar{}) {
		return 0, errors.New("daily price cache is unavailable")
	}
	day := normalizeDailyTradeDate(at)
	expectedDay := previousMarketSummaryV150OpenTradeDay(day)
	if expectedDay.IsZero() {
		return 0, errors.New("previous trading day is unavailable")
	}
	if allowFetch {
		// Held symbols are not guaranteed to re-enter a later candidate pool.
		// Refresh their adjusted daily cache explicitly before applying gap and
		// price-limit rules; cache-only callers retain strict read-only behavior.
		if _, err := loadDailyBarsWithCache(
			normalizeRecommendStockCode(symbol),
			toQuoteCode(symbol),
			expectedDay.AddDate(0, 0, -30),
			expectedDay,
			40,
		); err != nil {
			return 0, fmt.Errorf("refresh adjusted previous close: %w", err)
		}
	}
	var row models.AiRecommendDailyBar
	err := db.Dao.Model(&models.AiRecommendDailyBar{}).
		Where("upper(stock_code) = ? AND trade_date < ? AND close > 0", normalizeRecommendStockCode(symbol), day).
		Order("trade_date DESC, id DESC").First(&row).Error
	if err != nil {
		return 0, err
	}
	if row.Close <= 0 {
		return 0, errors.New("previous close is non-positive")
	}
	actualDay := normalizeDailyTradeDate(row.TradeDate)
	if expectedDay.IsZero() || !actualDay.Equal(expectedDay) {
		return 0, fmt.Errorf("stale previous close: got %s, require %s", actualDay.Format(time.DateOnly), expectedDay.Format(time.DateOnly))
	}
	return row.Close, nil
}

func previousMarketSummaryV150OpenTradeDay(day time.Time) time.Time {
	day = normalizeDailyTradeDate(day)
	for probe := day.AddDate(0, 0, -1); !probe.IsZero(); probe = probe.AddDate(0, 0, -1) {
		if isCNOpenTradeDaySafe(probe) {
			return normalizeDailyTradeDate(probe)
		}
	}
	return time.Time{}
}

func decorateMarketSummaryV150Tradability(bar *v150.Bar, security marketSummaryV150SecurityState, previousClose float64) {
	if bar == nil {
		return
	}
	bar.Suspended = !security.Tradable
	if previousClose <= 0 || bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 {
		bar.Suspended = true
		return
	}
	ratio := marketSummaryV150PriceLimitRatio(security.Row)
	limitUp := round2(previousClose * (1 + ratio))
	limitDown := round2(previousClose * (1 - ratio))
	tolerance := 0.0051
	bar.LimitUpLocked = bar.Open >= limitUp-tolerance && bar.High >= limitUp-tolerance && bar.Low >= limitUp-tolerance
	bar.LimitDownLocked = bar.Open <= limitDown+tolerance && bar.High <= limitDown+tolerance && bar.Low <= limitDown+tolerance
}

func marketSummaryV150PriceLimitRatio(security models.SecurityMasterHistory) float64 {
	if security.IsST {
		return 0.05
	}
	board := strings.ToUpper(strings.TrimSpace(security.Board))
	symbol := normalizeRecommendStockCode(security.Symbol)
	switch {
	case strings.Contains(board, "BEIJING"), strings.Contains(board, "北交"), strings.HasSuffix(symbol, ".BJ"):
		return 0.30
	case strings.Contains(board, "STAR"), strings.Contains(board, "科创"), strings.Contains(board, "CHINEXT"), strings.Contains(board, "创业"), strings.HasPrefix(symbol, "688"), strings.HasPrefix(symbol, "300"):
		return 0.20
	default:
		return 0.10
	}
}

func appendMarketSummaryV150OrderEvents(rec models.AiRecommendStocks, run models.StrategyRunSnapshot, source []v150.OrderEvent, accounting marketSummaryV150EventAccounting) error {
	return appendMarketSummaryV150OrderEventsWithStore(
		context.Background(),
		persistence.NewGORMOrderEventStore(db.Dao),
		rec,
		run,
		source,
		accounting,
	)
}

func marketSummaryV150LifecycleEventID(runID, ruleID string, event v150.OrderEvent) string {
	identity := strings.Join([]string{runID, ruleID, string(event.Type), event.At.UTC().Format(time.RFC3339Nano), normalizeRecommendStockCode(event.Symbol)}, "|")
	digest := sha256.Sum256([]byte(identity))
	return "v150-event-" + hex.EncodeToString(digest[:20])
}

func marketSummaryV150EventTypeOrder(kind v150.OrderEventType) int {
	switch kind {
	case v150.EventSignal:
		return 1
	case v150.EventOrder:
		return 2
	case v150.EventFill:
		return 3
	case v150.EventCorporateAction:
		return 4
	case v150.EventExitSignal:
		return 5
	case v150.EventExitFill:
		return 6
	case v150.EventReject:
		return 7
	default:
		return 9
	}
}

func rejectMarketSummaryV150Activation(ctx yieldBuildContext, rec models.AiRecommendStocks, frozen *marketSummaryV150FrozenExecutionPlan, signal *v150.ActivationSignal, at time.Time, reason string, info triggerEvalInfo) (*time.Time, float64, triggerEvalInfo) {
	info.DataStatus = "已跳过"
	info.DataStatusReason = strings.TrimSpace(reason)
	if frozen == nil {
		return nil, 0, info
	}
	if at.IsZero() {
		at = frozen.Plan.ValidFromAt
	}
	events := make([]v150.OrderEvent, 0, 3)
	if signal != nil && signal.Triggered {
		events = append(events, v150.OrderEvent{Type: v150.EventSignal, At: signal.At, Symbol: normalizeRecommendStockCode(rec.StockCode), Reason: string(signal.Path)})
		if !at.After(signal.At) {
			at = signal.At.Add(time.Nanosecond)
		}
	}
	events = append(events, v150.OrderEvent{Type: v150.EventReject, At: at, Symbol: normalizeRecommendStockCode(rec.StockCode), Reason: strings.TrimSpace(reason)})
	if err := ctx.appendMarketSummaryV150OrderEvents(rec, frozen.Run, events, marketSummaryV150EventAccounting{}); err != nil {
		info.DataStatusReason += "; append reject lifecycle: " + err.Error()
	}
	return nil, 0, info
}

func applyMarketSummaryV150SyncInfo(info *triggerEvalInfo, syncInfo minuteSyncInfo) {
	if info == nil {
		return
	}
	info.CacheStart = syncInfo.CacheStart
	info.CacheEnd = syncInfo.CacheEnd
	info.CacheUpdated = syncInfo.CacheUpdated
	info.CacheSource = syncInfo.CacheSource
	info.LastMinuteTs = syncInfo.LastMinuteTs
}
