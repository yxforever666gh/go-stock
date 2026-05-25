package data

import (
	"fmt"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"strings"
	"time"
)

type yieldBuildContext struct {
	Force               bool
	Reason              string
	Now                 time.Time
	InTradingSession    bool
	LatestTradeDate     time.Time
	CrawlTimeout        int64
	DisableMinuteFetch  bool
	Tushare             *TushareApi
	CurrentPriceMap     map[string]float64
	CurrentPriceTimeMap map[string]string
}

func isSoldPositionStatus(status string) bool {
	status = strings.TrimSpace(status)
	return status == "已止盈" || status == "已止损"
}

func hasActivatedAggregateLifecycle(state *models.AiRecommendYieldState) bool {
	if state == nil {
		return false
	}
	if hasInvalidActivationExitPlan(state.BuyAmount, state.StopProfitAmount) || hasInvalidActivationExitPlan(state.ActivationPrice, state.StopProfitAmount) {
		return false
	}
	if strings.TrimSpace(state.ActivationStatus) != "activated" {
		return false
	}
	if state.ActivationTime == nil || state.ActivationTime.IsZero() {
		return false
	}
	return state.ActivationPrice > 0
}

func hasInvalidActivationExitPlan(buyAmount float64, stopProfit *float64) bool {
	if buyAmount <= 0 || stopProfit == nil || *stopProfit <= 0 {
		return false
	}
	return round2(buyAmount) >= round2(*stopProfit)
}

func restoreActivatedAggregateLifecycle(target *models.AiRecommendYieldState, existing *models.AiRecommendYieldState) {
	if target == nil || !hasActivatedAggregateLifecycle(existing) {
		return
	}
	target.ActivationStatus = "activated"
	if existing.StopProfitAmount != nil {
		v := round2(*existing.StopProfitAmount)
		target.StopProfitAmount = &v
	}
	if existing.StopLossAmount != nil {
		v := round2(*existing.StopLossAmount)
		target.StopLossAmount = &v
	}
	if strings.TrimSpace(existing.SellAmountText) != "" {
		target.SellAmountText = existing.SellAmountText
	}
	if existing.ActivationTime != nil && !existing.ActivationTime.IsZero() {
		t := *existing.ActivationTime
		target.ActivationTime = &t
	}
	target.ActivationPrice = round2(existing.ActivationPrice)
	if existing.BuyTime != nil && !existing.BuyTime.IsZero() {
		t := *existing.BuyTime
		target.BuyTime = &t
	} else if target.ActivationTime != nil && !target.ActivationTime.IsZero() {
		t := *target.ActivationTime
		target.BuyTime = &t
	}
	if existing.BuyAmount > 0 {
		target.BuyAmount = round2(existing.BuyAmount)
	} else {
		target.BuyAmount = round2(existing.ActivationPrice)
	}
	target.PositionStatus = strings.TrimSpace(existing.PositionStatus)
	if target.PositionStatus == "" {
		target.PositionStatus = "持有"
	}
	if existing.SellTime != nil && !existing.SellTime.IsZero() {
		t := *existing.SellTime
		target.SellTime = &t
	} else {
		target.SellTime = nil
	}
	if existing.RealizedSellAmount != nil {
		v := round2(*existing.RealizedSellAmount)
		target.RealizedSellAmount = &v
	} else {
		target.RealizedSellAmount = nil
	}
	target.Frozen = existing.Frozen
	if target.BuyTime != nil && !target.BuyTime.IsZero() {
		target.TotalScopeStart = target.BuyTime.Format("2006-01-02")
	}
	if strings.TrimSpace(existing.DataStatus) != "" {
		target.DataStatus = existing.DataStatus
		target.DataStatusReason = existing.DataStatusReason
	} else {
		target.DataStatus = "正常"
		target.DataStatusReason = ""
	}
}

func sanitizeYieldSellSnapshot(sellFloorTime time.Time, positionStatus *string, sellTime **time.Time, realizedSellAmount **float64, frozen *bool) bool {
	invalid := false
	if positionStatus != nil && isSoldPositionStatus(*positionStatus) {
		if sellTime == nil || *sellTime == nil {
			invalid = true
		} else if !sellFloorTime.IsZero() && (*sellTime).Before(sellFloorTime) {
			invalid = true
		}
	}
	if !invalid {
		return false
	}
	if positionStatus != nil {
		*positionStatus = "持有"
	}
	if sellTime != nil {
		*sellTime = nil
	}
	if realizedSellAmount != nil {
		*realizedSellAmount = nil
	}
	if frozen != nil {
		*frozen = false
	}
	return true
}

func validateActivationExitPlan(activationPrice float64, stopProfit *float64) (string, bool) {
	if activationPrice <= 0 || stopProfit == nil || *stopProfit <= 0 {
		return "", false
	}
	buy := round2(activationPrice)
	profit := round2(*stopProfit)
	if buy < profit {
		return "", false
	}
	return fmt.Sprintf("激活价 %.2f 不低于止盈触发价 %.2f，按追高保护跳过收益率跟踪", buy, profit), true
}

func buildYieldStateFromAggregate(aggr *aiRecommendYieldAggregate, existing *models.AiRecommendYieldState, ctx yieldBuildContext) models.AiRecommendYieldState {
	state := models.AiRecommendYieldState{
		StockCode:         aggr.StockCode,
		StockName:         aggr.StockName,
		ModelNames:        strings.Join(aggr.ModelNames, "、"),
		BkName:            strings.Join(aggr.BkNames, "、"),
		RecommendCount:    aggr.RecommendCount,
		RecommendCategory: "",
		PositionStatus:    "待激活",
		YieldRateText:     "--",
		DataStatus:        "正常",
		TotalScopeStart:   aggr.SignalTime.Format("2006-01-02"),
		TotalScopeEnd:     ctx.Now.Format("2006-01-02"),
	}
	if !aggr.SignalTime.IsZero() {
		t := aggr.SignalTime
		state.RecommendTime = &t
		state.SignalTime = &t
	}
	state.ActivationStatus = "pending"

	if aggr.StopProfitCount > 0 {
		v := calculateAvg(aggr.StopProfitSum, aggr.StopProfitCount)
		state.StopProfitAmount = &v
	}
	if aggr.StopLossCount > 0 {
		v := calculateAvg(aggr.StopLossSum, aggr.StopLossCount)
		state.StopLossAmount = &v
	}
	state.SellAmountText = buildSellAmountText(state.StopProfitAmount, state.StopLossAmount)

	if existing != nil {
		state.ID = existing.ID
		state.CreatedAt = existing.CreatedAt
		if !hasInvalidActivationExitPlan(existing.BuyAmount, state.StopProfitAmount) && !hasInvalidActivationExitPlan(existing.ActivationPrice, state.StopProfitAmount) {
			state.SignalTime = existing.SignalTime
			state.ActivationStatus = existing.ActivationStatus
			state.ActivationTime = existing.ActivationTime
			state.ActivationPrice = existing.ActivationPrice
			state.BuyTime = existing.BuyTime
			state.BuyAmount = existing.BuyAmount
			state.SellTime = existing.SellTime
			state.RealizedSellAmount = existing.RealizedSellAmount
			state.PositionStatus = existing.PositionStatus
			state.Frozen = existing.Frozen
		}
		state.CurrentPrice = existing.CurrentPrice
		state.CurrentPriceTime = existing.CurrentPriceTime
		state.YieldRate = existing.YieldRate
		state.YieldRateText = existing.YieldRateText
		state.DataStatus = existing.DataStatus
		state.DataStatusReason = existing.DataStatusReason
		state.LastMinuteTs = existing.LastMinuteTs
		state.LastRecalcAt = existing.LastRecalcAt
		state.MinuteCacheStart = existing.MinuteCacheStart
		state.MinuteCacheEnd = existing.MinuteCacheEnd
		state.MinuteCacheSource = existing.MinuteCacheSource
		state.MinuteCacheUpdated = existing.MinuteCacheUpdated
	}
	if state.SignalTime == nil || state.SignalTime.IsZero() {
		if !aggr.SignalTime.IsZero() {
			t := aggr.SignalTime
			state.SignalTime = &t
		}
	}
	state.RecommendCategory = ""
	if !aggr.SignalTime.IsZero() {
		t := aggr.SignalTime
		state.SignalTime = &t
		state.RecommendTime = &t
	}
	if strings.TrimSpace(state.ActivationStatus) == "" {
		state.ActivationStatus = "pending"
	}
	buyTime := aggr.BuyTime
	if state.BuyTime != nil && !state.BuyTime.IsZero() {
		buyTime = *state.BuyTime
	}
	sellFloorTime := time.Time{}
	if !buyTime.IsZero() {
		sellFloorTime = resolveNextSellEligibleTime(buyTime)
	}
	sanitizeYieldSellSnapshot(sellFloorTime, &state.PositionStatus, &state.SellTime, &state.RealizedSellAmount, &state.Frozen)

	if p, ok := ctx.CurrentPriceMap[aggr.StockCode]; ok {
		state.CurrentPrice = round2(p)
	}
	if pTime, ok := ctx.CurrentPriceTimeMap[aggr.StockCode]; ok {
		state.CurrentPriceTime = pTime
	}

	manualBackfill := ctx.Reason == "manual_minute_download"
	frozenSold := state.Frozen && isSoldPositionStatus(state.PositionStatus)

	if !manualBackfill && !shouldUpdateActiveStock(existing, ctx.Force, ctx.InTradingSession, ctx.LatestTradeDate, ctx.Now) {
		fillYieldMetrics(&state)
		return state
	}

	prevPositionStatus := state.PositionStatus
	var prevSellTime *time.Time
	if state.SellTime != nil {
		t := *state.SellTime
		prevSellTime = &t
	}
	var prevRealizedSellAmount *float64
	if state.RealizedSellAmount != nil {
		v := *state.RealizedSellAmount
		prevRealizedSellAmount = &v
	}

	recalcAt := ctx.Now
	state.LastRecalcAt = &recalcAt
	state.PositionStatus = "待激活"
	state.SellTime = nil
	state.RealizedSellAmount = nil
	state.Frozen = false
	state.BuyTime = nil
	state.BuyAmount = 0
	state.ActivationTime = nil
	state.ActivationPrice = 0
	state.ActivationStatus = "pending"

	if !isAShareTsCode(aggr.StockCode) {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "非A股"
		fillYieldMetrics(&state)
		return state
	}

	if state.StopProfitAmount == nil && state.StopLossAmount == nil {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "缺少止盈止损"
		fillYieldMetrics(&state)
		return state
	}

	activationTime, activationPrice, activationInfo := resolveAggregateActivation(aggr, ctx, manualBackfill)
	if activationInfo.LastMinuteTs != nil {
		state.LastMinuteTs = activationInfo.LastMinuteTs
	}
	state.MinuteCacheStart = activationInfo.CacheStart
	state.MinuteCacheEnd = activationInfo.CacheEnd
	if activationInfo.CacheSource != "" {
		state.MinuteCacheSource = activationInfo.CacheSource
	}
	if activationInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = activationInfo.CacheUpdated
	}
	state.DataStatus = activationInfo.DataStatus
	state.DataStatusReason = activationInfo.DataStatusReason

	if activationTime == nil || activationTime.IsZero() || activationPrice <= 0 {
		if hasActivatedAggregateLifecycle(existing) {
			restoreActivatedAggregateLifecycle(&state, existing)
			if existing.Frozen || isSoldPositionStatus(existing.PositionStatus) {
				fillYieldMetrics(&state)
				return state
			}
			activationTime = state.ActivationTime
			activationPrice = state.ActivationPrice
		}
	}

	if activationTime == nil || activationTime.IsZero() || activationPrice <= 0 {
		switch state.DataStatus {
		case "已跳过":
			state.ActivationStatus = "skipped"
			state.PositionStatus = "已放弃"
			fillYieldMetrics(&state)
			return state
		case "已失效":
			state.ActivationStatus = "invalid"
			state.PositionStatus = "已失效"
			fillYieldMetrics(&state)
			return state
		case "已过期":
			state.ActivationStatus = "expired"
			state.PositionStatus = "过期未触发"
			fillYieldMetrics(&state)
			return state
		}
		if state.DataStatus == "正常" {
			state.DataStatus = "待激活"
			state.DataStatusReason = "未触发主买入区"
		}
		fillYieldMetrics(&state)
		return state
	}
	if reason, blocked := validateActivationExitPlan(activationPrice, state.StopProfitAmount); blocked {
		state.ActivationStatus = "skipped"
		state.ActivationTime = nil
		state.ActivationPrice = 0
		state.BuyTime = nil
		state.BuyAmount = 0
		state.PositionStatus = "已放弃"
		state.DataStatus = "已跳过"
		state.DataStatusReason = reason
		fillYieldMetrics(&state)
		return state
	}

	state.ActivationStatus = "activated"
	state.ActivationTime = activationTime
	state.ActivationPrice = round2(activationPrice)
	state.BuyTime = activationTime
	state.BuyAmount = round2(activationPrice)
	state.TotalScopeStart = activationTime.Format("2006-01-02")
	state.PositionStatus = "持有"

	sellFloorTime = resolveNextSellEligibleTime(*activationTime)
	scanStart := sellFloorTime
	manualFullCheck := manualBackfill
	if !manualFullCheck && !frozenSold && existing != nil && existing.LastMinuteTs != nil && existing.LastMinuteTs.After(scanStart) {
		scanStart = existing.LastMinuteTs.Add(time.Minute)
	}
	scanEnd := normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate))
	if manualBackfill {
		if ctx.InTradingSession {
			scanEnd = normalizeMinuteCoverageEnd(ctx.Now)
		} else {
			scanEnd = resolveLatestCloseEvalEnd(ctx.Now, ctx.LatestTradeDate)
		}
	}

	triggerStatus, triggerTime, triggerPrice, evalInfo := evaluatePositionWithMinuteAndDaily(
		aggr.StockCode,
		scanStart,
		scanEnd,
		state.StopProfitAmount,
		state.StopLossAmount,
		ctx.Tushare,
		ctx.CrawlTimeout,
		manualBackfill && !ctx.DisableMinuteFetch,
		ctx.DisableMinuteFetch,
	)

	if evalInfo.LastMinuteTs != nil {
		state.LastMinuteTs = evalInfo.LastMinuteTs
	}
	state.MinuteCacheStart = evalInfo.CacheStart
	state.MinuteCacheEnd = evalInfo.CacheEnd
	if evalInfo.CacheSource != "" {
		state.MinuteCacheSource = evalInfo.CacheSource
	}
	if evalInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = evalInfo.CacheUpdated
	}
	state.DataStatus = evalInfo.DataStatus
	state.DataStatusReason = evalInfo.DataStatusReason

	if triggerStatus != "" {
		if !sellFloorTime.IsZero() && triggerTime.Before(sellFloorTime) {
			logger.SugaredLogger.Warnf("ignore invalid aggregate sell trigger before sell-eligible time: code=%s buy=%s sell_floor=%s sell=%s status=%s", aggr.StockCode, activationTime.In(cnLocation()).Format("2006-01-02 15:04:05"), sellFloorTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerStatus)
		} else {
			state.Frozen = true
			state.PositionStatus = triggerStatus
			t := triggerTime
			state.SellTime = &t
			p := round2(triggerPrice)
			state.RealizedSellAmount = &p
		}
	} else if manualBackfill && frozenSold {
		state.Frozen = true
		state.PositionStatus = prevPositionStatus
		state.SellTime = prevSellTime
		state.RealizedSellAmount = prevRealizedSellAmount
	}

	fillYieldMetrics(&state)
	return state
}

func buildYieldRecordStateFromRecommend(rec models.AiRecommendStocks, existing *models.AiRecommendYieldRecordState, ctx yieldBuildContext) models.AiRecommendYieldRecordState {
	recordTime := recommendRecordTime(rec)
	code := normalizeRecommendStockCode(rec.StockCode)
	state := models.AiRecommendYieldRecordState{
		RecommendID:       rec.ID,
		StockCode:         code,
		StockName:         strings.TrimSpace(rec.StockName),
		ModelName:         strings.TrimSpace(rec.ModelName),
		BkName:            strings.TrimSpace(rec.BkName),
		RecommendCategory: strings.TrimSpace(rec.RecommendCategory),
		PositionStatus:    "待激活",
		YieldRateText:     "--",
		DataStatus:        "正常",
		TotalScopeEnd:     ctx.Now.Format("2006-01-02"),
	}
	if !recordTime.IsZero() {
		t := recordTime
		state.RecommendTime = &t
		state.SignalTime = &t
		state.TotalScopeStart = t.Format("2006-01-02")
	}
	state.ActivationStatus = "pending"

	if v, ok := parseStopProfitPrice(rec); ok {
		state.StopProfitAmount = &v
	}
	if v, ok := parseStopLossPrice(rec); ok {
		state.StopLossAmount = &v
	}
	state.SellAmountText = buildSellAmountText(state.StopProfitAmount, state.StopLossAmount)

	if existing != nil {
		state.ID = existing.ID
		state.CreatedAt = existing.CreatedAt
		if !hasInvalidActivationExitPlan(existing.BuyAmount, state.StopProfitAmount) && !hasInvalidActivationExitPlan(existing.ActivationPrice, state.StopProfitAmount) {
			state.SignalTime = existing.SignalTime
			state.ActivationStatus = existing.ActivationStatus
			state.ActivationTime = existing.ActivationTime
			state.ActivationPrice = existing.ActivationPrice
			state.BuyTime = existing.BuyTime
			state.BuyAmount = existing.BuyAmount
			state.SellTime = existing.SellTime
			state.RealizedSellAmount = existing.RealizedSellAmount
			state.PositionStatus = existing.PositionStatus
			state.Frozen = existing.Frozen
		}
		state.CurrentPrice = existing.CurrentPrice
		state.CurrentPriceTime = existing.CurrentPriceTime
		state.YieldRate = existing.YieldRate
		state.YieldRateText = existing.YieldRateText
		state.DataStatus = existing.DataStatus
		state.DataStatusReason = existing.DataStatusReason
		state.LastMinuteTs = existing.LastMinuteTs
		state.LastRecalcAt = existing.LastRecalcAt
		state.MinuteCacheStart = existing.MinuteCacheStart
		state.MinuteCacheEnd = existing.MinuteCacheEnd
		state.MinuteCacheSource = existing.MinuteCacheSource
		state.MinuteCacheUpdated = existing.MinuteCacheUpdated
	}
	if state.SignalTime == nil || state.SignalTime.IsZero() {
		if !recordTime.IsZero() {
			t := recordTime
			state.SignalTime = &t
		}
	}
	state.RecommendCategory = strings.TrimSpace(rec.RecommendCategory)
	if !recordTime.IsZero() {
		t := recordTime
		state.SignalTime = &t
		state.RecommendTime = &t
	}
	if strings.TrimSpace(state.ActivationStatus) == "" {
		state.ActivationStatus = "pending"
	}
	sellFloorTime := time.Time{}
	if state.BuyTime != nil && !state.BuyTime.IsZero() {
		sellFloorTime = resolveNextSellEligibleTime(*state.BuyTime)
		state.TotalScopeStart = state.BuyTime.Format("2006-01-02")
	}
	sanitizeYieldSellSnapshot(sellFloorTime, &state.PositionStatus, &state.SellTime, &state.RealizedSellAmount, &state.Frozen)

	if p, ok := ctx.CurrentPriceMap[code]; ok {
		state.CurrentPrice = round2(p)
	}
	if pTime, ok := ctx.CurrentPriceTimeMap[code]; ok {
		state.CurrentPriceTime = pTime
	}
	if state.CurrentPrice <= 0 {
		if p, ok := parseBuyPrice(rec.StockCurrentPrice); ok {
			state.CurrentPrice = round2(p)
		}
	}
	if strings.TrimSpace(state.CurrentPriceTime) == "" {
		state.CurrentPriceTime = strings.TrimSpace(rec.StockCurrentPriceTime)
	}

	if eligibility, reason := resolveRecommendBacktestEligibility(&rec); eligibility != recommendBacktestEligible {
		recalcAt := ctx.Now
		state.LastRecalcAt = &recalcAt
		switch eligibility {
		case recommendBacktestSkipped:
			activationStatus, positionStatus, dataStatus, _, _ := resolveRecommendYieldSkipInfo(&rec)
			state.ActivationStatus = activationStatus
			state.PositionStatus = positionStatus
			state.DataStatus = dataStatus
		default:
			state.ActivationStatus = "ineligible"
			state.PositionStatus = "未纳入回测"
			state.DataStatus = "未结构化"
		}
		state.ActivationTime = nil
		state.ActivationPrice = 0
		state.BuyTime = nil
		state.BuyAmount = 0
		state.SellTime = nil
		state.RealizedSellAmount = nil
		state.Frozen = false
		state.DataStatusReason = reason
		state.TotalScopeStart = ""
		fillYieldRecordMetrics(&state)
		return state
	}

	manualBackfill := ctx.Reason == "manual_minute_download"
	frozenSold := state.Frozen && isSoldPositionStatus(state.PositionStatus)

	if !manualBackfill && !shouldUpdateActiveRecord(existing, ctx.Force, ctx.InTradingSession, ctx.LatestTradeDate, ctx.Now) {
		fillYieldRecordMetrics(&state)
		return state
	}

	prevPositionStatus := state.PositionStatus
	var prevSellTime *time.Time
	if state.SellTime != nil {
		t := *state.SellTime
		prevSellTime = &t
	}
	var prevRealizedSellAmount *float64
	if state.RealizedSellAmount != nil {
		v := *state.RealizedSellAmount
		prevRealizedSellAmount = &v
	}

	recalcAt := ctx.Now
	state.LastRecalcAt = &recalcAt
	state.PositionStatus = "待激活"
	state.SellTime = nil
	state.RealizedSellAmount = nil
	state.Frozen = false
	state.BuyTime = nil
	state.BuyAmount = 0
	state.ActivationTime = nil
	state.ActivationPrice = 0
	state.ActivationStatus = "pending"

	if !isAShareTsCode(code) {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "非A股"
		fillYieldRecordMetrics(&state)
		return state
	}

	if state.StopProfitAmount == nil && state.StopLossAmount == nil {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "缺少止盈止损"
		fillYieldRecordMetrics(&state)
		return state
	}

	activationTime, activationPrice, activationInfo := resolveRecommendActivation(rec, ctx, manualBackfill)
	if activationInfo.LastMinuteTs != nil {
		state.LastMinuteTs = activationInfo.LastMinuteTs
	}
	state.MinuteCacheStart = activationInfo.CacheStart
	state.MinuteCacheEnd = activationInfo.CacheEnd
	if activationInfo.CacheSource != "" {
		state.MinuteCacheSource = activationInfo.CacheSource
	}
	if activationInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = activationInfo.CacheUpdated
	}
	state.DataStatus = activationInfo.DataStatus
	state.DataStatusReason = activationInfo.DataStatusReason

	if activationTime == nil || activationTime.IsZero() || activationPrice <= 0 {
		switch state.DataStatus {
		case "已跳过":
			state.ActivationStatus = "skipped"
			state.PositionStatus = "已放弃"
			fillYieldRecordMetrics(&state)
			return state
		case "已失效":
			state.ActivationStatus = "invalid"
			state.PositionStatus = "已失效"
			fillYieldRecordMetrics(&state)
			return state
		case "已过期":
			state.ActivationStatus = "expired"
			state.PositionStatus = "过期未触发"
			fillYieldRecordMetrics(&state)
			return state
		}
		if state.DataStatus == "正常" {
			state.DataStatus = "待激活"
			state.DataStatusReason = "未触发主买入区"
		}
		fillYieldRecordMetrics(&state)
		return state
	}
	if reason, blocked := validateActivationExitPlan(activationPrice, state.StopProfitAmount); blocked {
		state.ActivationStatus = "skipped"
		state.ActivationTime = nil
		state.ActivationPrice = 0
		state.BuyTime = nil
		state.BuyAmount = 0
		state.PositionStatus = "已放弃"
		state.DataStatus = "已跳过"
		state.DataStatusReason = appendRecommendInvalidConditionText(reason, rec.InvalidCondition)
		fillYieldRecordMetrics(&state)
		return state
	}

	state.ActivationStatus = "activated"
	state.ActivationTime = activationTime
	state.ActivationPrice = round2(activationPrice)
	state.BuyTime = activationTime
	state.BuyAmount = round2(activationPrice)
	state.TotalScopeStart = activationTime.Format("2006-01-02")
	state.PositionStatus = "持有"

	sellFloorTime = resolveNextSellEligibleTime(*activationTime)
	scanStart := sellFloorTime
	if !manualBackfill && !frozenSold && existing != nil && existing.LastMinuteTs != nil && existing.LastMinuteTs.After(scanStart) {
		scanStart = existing.LastMinuteTs.Add(time.Minute)
	}
	scanEnd := normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate))
	if manualBackfill {
		if ctx.InTradingSession {
			scanEnd = normalizeMinuteCoverageEnd(ctx.Now)
		} else {
			scanEnd = resolveLatestCloseEvalEnd(ctx.Now, ctx.LatestTradeDate)
		}
	}

	triggerStatus, triggerTime, triggerPrice, evalInfo := evaluatePositionWithMinuteAndDaily(
		code,
		scanStart,
		scanEnd,
		state.StopProfitAmount,
		state.StopLossAmount,
		ctx.Tushare,
		ctx.CrawlTimeout,
		manualBackfill && !ctx.DisableMinuteFetch,
		ctx.DisableMinuteFetch,
	)

	if evalInfo.LastMinuteTs != nil {
		state.LastMinuteTs = evalInfo.LastMinuteTs
	}
	state.MinuteCacheStart = evalInfo.CacheStart
	state.MinuteCacheEnd = evalInfo.CacheEnd
	if evalInfo.CacheSource != "" {
		state.MinuteCacheSource = evalInfo.CacheSource
	}
	if evalInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = evalInfo.CacheUpdated
	}
	state.DataStatus = evalInfo.DataStatus
	state.DataStatusReason = evalInfo.DataStatusReason

	if triggerStatus != "" {
		if !sellFloorTime.IsZero() && triggerTime.Before(sellFloorTime) {
			logger.SugaredLogger.Warnf("ignore invalid record sell trigger before sell-eligible time: code=%s recommend_id=%d buy=%s sell_floor=%s sell=%s status=%s", code, rec.ID, activationTime.In(cnLocation()).Format("2006-01-02 15:04:05"), sellFloorTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerStatus)
		} else {
			state.Frozen = true
			state.PositionStatus = triggerStatus
			t := triggerTime
			state.SellTime = &t
			p := round2(triggerPrice)
			state.RealizedSellAmount = &p
		}
	} else if manualBackfill && frozenSold {
		state.Frozen = true
		state.PositionStatus = prevPositionStatus
		state.SellTime = prevSellTime
		state.RealizedSellAmount = prevRealizedSellAmount
	}

	fillYieldRecordMetrics(&state)
	return state
}

func parseRecommendEntryRange(rec models.AiRecommendStocks) (float64, float64, bool) {
	if hasMachineActivationRule(&rec) {
		rule, err := parseActivationRuleJSON(rec.ActivationRuleJSON)
		if err == nil && rule != nil {
			for _, path := range activationRulePaths(rule) {
				if strings.TrimSpace(path.SignalType) != "price_range_with_volume" {
					continue
				}
				minPrice := path.ThresholdValue
				maxPrice := path.ThresholdMax
				if minPrice > 0 && maxPrice <= 0 {
					maxPrice = minPrice
				}
				if minPrice > 0 && maxPrice > 0 {
					if minPrice > maxPrice {
						minPrice, maxPrice = maxPrice, minPrice
					}
					rawText := strings.TrimSpace(rec.RecommendBuyPrice)
					if shouldPreferTextResolvedBuyRange(rawText, minPrice, maxPrice) {
						textMin, okMin := parsePriceMinFromText(rawText)
						textMax, okMax := parsePriceMaxFromText(rawText)
						if okMin && okMax && textMin > 0 && textMax > 0 {
							if textMin > textMax {
								textMin, textMax = textMax, textMin
							}
							return textMin, textMax, true
						}
					}
					return minPrice, maxPrice, true
				}
			}
		}
	}
	_, min, max, ok := resolveRecommendBuyRange(rec)
	if !ok || min <= 0 || max <= 0 {
		return 0, 0, false
	}
	if min > max {
		min, max = max, min
	}
	return min, max, true
}

func scanActivationFromBars(bars []minuteBar, minPrice, maxPrice float64) (time.Time, float64, bool) {
	if len(bars) == 0 || minPrice <= 0 || maxPrice <= 0 {
		return time.Time{}, 0, false
	}
	for _, bar := range bars {
		if activationTime, activationPrice, ok := resolveActivationCandidateFromBar(bar, minPrice, maxPrice); ok {
			return activationTime, activationPrice, true
		}
	}
	return time.Time{}, 0, false
}

func resolveActivationCandidateFromBar(bar minuteBar, minPrice, maxPrice float64) (time.Time, float64, bool) {
	if bar.TradeTime.IsZero() {
		return time.Time{}, 0, false
	}
	if bar.Low > maxPrice || bar.High < minPrice {
		return time.Time{}, 0, false
	}
	price := bar.Close
	if price <= 0 {
		price = bar.Open
	}
	if price < minPrice {
		price = minPrice
	}
	if price > maxPrice {
		price = maxPrice
	}
	return bar.TradeTime, round2(price), true
}

func resolveActivationWindow(recordTime time.Time, now time.Time, inTrading bool, latestTradeDate time.Time) (time.Time, time.Time) {
	start := resolveRecommendBuyTime(recordTime)
	end := normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(now, inTrading, latestTradeDate))
	if start.After(end) {
		return start, start
	}
	return start, end
}

func expandActivationWindowStartForPrevDayActivity(rec models.AiRecommendStocks, start time.Time) time.Time {
	if start.IsZero() || !recommendRequiresPrevDayActivityFilter(rec) {
		return start
	}
	prevStart := resolveActivitySessionStart(previousTradingMoment(start))
	if prevStart.IsZero() || !prevStart.Before(start) {
		return start
	}
	return prevStart
}

func clampRecordActivationTime(recordTime, activationTime time.Time) time.Time {
	if recordTime.IsZero() || activationTime.IsZero() {
		return activationTime
	}
	if activationTime.Before(recordTime) {
		return recordTime
	}
	return activationTime
}

func resolveRecommendActivation(rec models.AiRecommendStocks, ctx yieldBuildContext, allowHeadBackfill bool) (*time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常", DataStatusReason: ""}
	recordTime := recommendRecordTime(rec)
	if recordTime.IsZero() {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "缺少推荐时间"
		return nil, 0, info
	}
	if normalizeRecommendCategory(rec.RecommendCategory) == "avoid" {
		info.DataStatus = "回避"
		info.DataStatusReason = "回避标的不参与收益率"
		return nil, 0, info
	}
	legacyDirectActivation := shouldUseLegacyDirectActivation(&rec)
	if !hasMachineActivationRule(&rec) && !legacyDirectActivation {
		info.DataStatus = "未结构化"
		info.DataStatusReason = "缺少结构化激活规则"
		return nil, 0, info
	}
	minPrice, maxPrice, ok := parseRecommendEntryRange(rec)
	if !ok {
		if legacyDirectActivation {
			info.DataStatus = "无法判定"
			info.DataStatusReason = "历史记录缺少可解析买入区间"
		} else {
			info.DataStatus = "未结构化"
			info.DataStatusReason = "结构化激活规则无法解析"
		}
		return nil, 0, info
	}
	start, end := resolveActivationWindow(recordTime, ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate)
	start = expandActivationWindowStartForPrevDayActivity(rec, start)
	expiryTime, _, hasExpiry := resolveRecommendPendingActivationExpiryForRecommend(rec, recordTime)
	if hasExpiry && end.After(expiryTime) {
		end = expiryTime
	}
	if start.After(end) {
		info.DataStatus = "待激活"
		info.DataStatusReason = "主买入区尚未进入可扫描窗口"
		return nil, 0, info
	}
	var bars []minuteBar
	var cacheInfo minuteSyncInfo
	if ctx.DisableMinuteFetch {
		bars, cacheInfo = syncMinuteBarsFromCacheOnly(normalizeRecommendStockCode(rec.StockCode), start, end)
	} else {
		bars, cacheInfo = syncMinuteBars(normalizeRecommendStockCode(rec.StockCode), start, end, ctx.CrawlTimeout, allowHeadBackfill)
	}
	info.CacheStart = cacheInfo.CacheStart
	info.CacheEnd = cacheInfo.CacheEnd
	info.CacheUpdated = cacheInfo.CacheUpdated
	info.CacheSource = cacheInfo.CacheSource
	info.LastMinuteTs = cacheInfo.LastMinuteTs
	openingNote := ""
	if !legacyDirectActivation {
		if rule, err := parseActivationRuleJSON(rec.ActivationRuleJSON); err == nil {
			if policy := resolveActivationOpeningPolicy(rule); policy != nil {
				if bufferUntil, dateOK := resolveActivationOpeningBufferUntilForEval(recordTime, end, policy); dateOK {
					reviewDate := tradingDayStart(bufferUntil)
					ok := !bufferUntil.IsZero()
					if ok {
						preBars, postBars := splitMinuteBarsByCutoff(bars, bufferUntil)
						sameDayBars := filterMinuteBarsByCNTradeDate(preBars, recordTime)
						// 1. 先检查推荐当日是否已激活
						if scan := resolveActivationRuleScan(rec, sameDayBars); scan.Triggered {
							scan.Time = clampRecordActivationTime(recordTime, scan.Time)
							if gate := evaluateV132ActivationGate(rec, scan.Time, scan.Price, sameDayBars); !gate.Allowed {
								info.DataStatus = "已跳过"
								info.DataStatusReason = gate.Reason
								return nil, 0, info
							}
							t := scan.Time
							info.ActivationTime = &t
							info.ActivationPrice = scan.Price
							return &t, scan.Price, info
						}
						reviewBars := filterMinuteBarsByCNTradeDate(preBars, reviewDate)
						// 2. 再检查信号后第一个交易日的开盘复核（风险保护优先）
						if action := resolveOpeningPolicyAction(rec, policy, reviewBars); action.Status != "" {
							info.DataStatus = dataStatusForInactiveActivationStatus(action.Status)
							info.DataStatusReason = action.Reason
							return nil, 0, info
						}
						// 4. 最后检查缓冲期等待
						if ctx.Now.Before(bufferUntil) || end.Before(bufferUntil) {
							info.DataStatus = "待激活"
							info.DataStatusReason = fmt.Sprintf("隔夜推荐等待 %s 开盘复核完成后再开始激活扫描", bufferUntil.In(cnLocation()).Format("15:04"))
							return nil, 0, info
						}
						if len(reviewBars) == 0 {
							openingNote = fmt.Sprintf("%s 开盘复核窗口分钟线缺失，已继续按有效期扫描", reviewDate.In(cnLocation()).Format("2006-01-02"))
						} else if action := resolveOpeningPolicyAction(rec, policy, reviewBars); action.SkipOpeningWindow {
							openingNote = action.Reason
						}
						bars = postBars
					}
				}
			}
		}
	}
	if legacyDirectActivation {
		activationTime, activationPrice, ok := scanActivationFromBars(bars, minPrice, maxPrice)
		if ok {
			activationTime = clampRecordActivationTime(recordTime, activationTime)
			t := activationTime
			info.ActivationTime = &t
			info.ActivationPrice = activationPrice
			return &t, activationPrice, info
		}
	} else {
		scan := resolveActivationRuleScan(rec, bars)
		if scan.Triggered {
			scan.Time = clampRecordActivationTime(recordTime, scan.Time)
			if gate := evaluateV132ActivationGate(rec, scan.Time, scan.Price, bars); !gate.Allowed {
				info.DataStatus = "已跳过"
				info.DataStatusReason = gate.Reason
				return nil, 0, info
			}
			t := scan.Time
			info.ActivationTime = &t
			info.ActivationPrice = scan.Price
			return &t, scan.Price, info
		}
		if strings.TrimSpace(scan.Reason) != "" {
			info.DataStatus = "待激活"
			info.DataStatusReason = scan.Reason
		}
	}
	if inactiveReason, inactiveStatus, inactive := resolvePendingRecommendInvalidation(rec, recordTime, end, bars, cacheInfo.CoverageOK); inactive {
		info.DataStatus = dataStatusForInactiveActivationStatus(inactiveStatus)
		info.DataStatusReason = inactiveReason
		return nil, 0, info
	}
	if cacheInfo.SyncErr != nil {
		if len(bars) > 0 && strings.TrimSpace(info.DataStatusReason) != "" {
			info.DataStatus = "待激活"
			info.DataStatusReason = info.DataStatusReason + "；分钟线同步未完全覆盖当前激活扫描窗口：" + strings.TrimSpace(cacheInfo.SyncErr.Error())
			return nil, 0, info
		}
		info.DataStatus = "无法判定"
		info.DataStatusReason = "主买入区扫描失败；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
		return nil, 0, info
	}
	if len(bars) == 0 {
		info.DataStatus = "待激活"
		info.DataStatusReason = "分钟线不可用或尚未覆盖当前激活扫描窗口"
		return nil, 0, info
	}
	if strings.TrimSpace(info.DataStatusReason) != "" {
		info.DataStatus = "待激活"
		return nil, 0, info
	}
	info.DataStatus = "待激活"
	info.DataStatusReason = firstNonEmptyText(openingNote, "未触发主买入区")
	return nil, 0, info
}

func shouldApplyOpeningPolicyForActivation(recordTime time.Time, policy *activationOpeningPolicy) bool {
	if policy == nil || recordTime.IsZero() {
		return false
	}
	_, ok := resolveActivationOpeningBufferUntil(recordTime, policy)
	return ok
}

func resolveActivationOpeningBufferUntil(recordTime time.Time, policy *activationOpeningPolicy) (time.Time, bool) {
	if recordTime.IsZero() || policy == nil {
		return time.Time{}, false
	}
	loc := cnLocation()
	t := normalizeMinuteTime(recordTime.In(loc))
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(day) {
		reviewDay := shiftToNextCNOpenTradeDaySafe(day)
		return resolveOpeningPolicyBufferUntil(reviewDay, policy)
	}
	morningBuffer, ok := resolveOpeningPolicyBufferUntil(day, policy)
	if !ok {
		return time.Time{}, false
	}
	close1500 := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)
	switch {
	case t.Before(morningBuffer):
		return morningBuffer, true
	case !t.Before(close1500):
		reviewDay := shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
		return resolveOpeningPolicyBufferUntil(reviewDay, policy)
	}
	return time.Time{}, false
}

func resolveActivationOpeningBufferUntilForEval(recordTime, evalEnd time.Time, policy *activationOpeningPolicy) (time.Time, bool) {
	if recordTime.IsZero() || policy == nil {
		return time.Time{}, false
	}
	loc := cnLocation()
	t := normalizeMinuteTime(recordTime.In(loc))
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(day) {
		reviewDay := shiftToNextCNOpenTradeDaySafe(day)
		return resolveOpeningPolicyBufferUntil(reviewDay, policy)
	}
	morningBuffer, ok := resolveOpeningPolicyBufferUntil(day, policy)
	if !ok {
		return time.Time{}, false
	}
	close1500 := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)
	if t.Before(morningBuffer) || !t.Before(close1500) {
		return resolveActivationOpeningBufferUntil(recordTime, policy)
	}
	reviewDay := shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
	nextBuffer, ok := resolveOpeningPolicyBufferUntil(reviewDay, policy)
	if !ok || evalEnd.IsZero() {
		return time.Time{}, false
	}
	if normalizeMinuteTime(evalEnd.In(loc)).Before(nextBuffer) {
		return time.Time{}, false
	}
	return nextBuffer, true
}

func resolveActivationOpeningReviewDate(recordTime time.Time) (time.Time, bool) {
	if recordTime.IsZero() {
		return time.Time{}, false
	}
	loc := cnLocation()
	day := recordTime.In(loc)
	reviewDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	reviewDay = shiftToNextCNOpenTradeDaySafe(reviewDay)
	if reviewDay.IsZero() {
		return time.Time{}, false
	}
	return reviewDay, true
}

func tradingDayStart(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	day := t.In(loc)
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
}

func resolveOpeningPolicyBufferUntil(latestTradeDate time.Time, policy *activationOpeningPolicy) (time.Time, bool) {
	if latestTradeDate.IsZero() || policy == nil {
		return time.Time{}, false
	}
	hhmm := openingReviewPhase0940
	if strings.TrimSpace(policy.MorningBufferUntil) != "" {
		hhmm = strings.TrimSpace(policy.MorningBufferUntil)
	}
	parsed, err := time.ParseInLocation("15:04", hhmm, cnLocation())
	if err != nil {
		return time.Time{}, false
	}
	loc := cnLocation()
	day := latestTradeDate.In(loc)
	return time.Date(day.Year(), day.Month(), day.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc), true
}

func splitMinuteBarsByCutoff(bars []minuteBar, cutoff time.Time) ([]minuteBar, []minuteBar) {
	if len(bars) == 0 {
		return nil, nil
	}
	before := make([]minuteBar, 0, len(bars))
	after := make([]minuteBar, 0, len(bars))
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		if bar.TradeTime.Before(cutoff) {
			before = append(before, bar)
			continue
		}
		after = append(after, bar)
	}
	return before, after
}

func filterMinuteBarsByCNTradeDate(bars []minuteBar, tradeTime time.Time) []minuteBar {
	if len(bars) == 0 || tradeTime.IsZero() {
		return nil
	}
	loc := cnLocation()
	day := tradeTime.In(loc)
	year, month, date := day.Date()
	filtered := make([]minuteBar, 0, len(bars))
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		cur := bar.TradeTime.In(loc)
		if cur.Year() == year && cur.Month() == month && cur.Day() == date {
			filtered = append(filtered, bar)
		}
	}
	return filtered
}

type openingPolicyActivationAction struct {
	Status            string
	Reason            string
	SkipOpeningWindow bool
}

func resolveOpeningPolicyAction(rec models.AiRecommendStocks, policy *activationOpeningPolicy, bars []minuteBar) openingPolicyActivationAction {
	if len(bars) == 0 || policy == nil {
		return openingPolicyActivationAction{}
	}
	firstBar := minuteBar{}
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		firstBar = bar
		break
	}
	if firstBar.TradeTime.IsZero() {
		return openingPolicyActivationAction{}
	}
	openPrice := firstBar.Open
	if openPrice <= 0 {
		openPrice = firstBar.Close
	}
	stopLoss, hasStopLoss := parseStopLossPrice(rec)
	if hasStopLoss && stopLoss > 0 && openPrice > 0 && openPrice <= stopLoss && strings.TrimSpace(policy.GapBelowStopAction) == "skip" {
		return openingPolicyActivationAction{
			Status: "invalid",
			Reason: appendRecommendInvalidConditionText(
				fmt.Sprintf("09:30 开盘价 %.2f 已低于止损/失效位 %.2f，按开盘复核策略判定信号失效", round2(openPrice), round2(stopLoss)),
				rec.InvalidCondition,
			),
		}
	}
	_, buyMax, ok := parseRecommendEntryRange(rec)
	if ok {
		maxChase := resolveRecommendOpeningMaxChasePrice(&rec, buyMax)
		if maxChase > 0 && openPrice > maxChase && strings.TrimSpace(policy.GapAboveMaxChaseAction) == "skip" {
			return openingPolicyActivationAction{
				SkipOpeningWindow: true,
				Reason: appendRecommendInvalidConditionText(
					fmt.Sprintf("09:30 开盘价 %.2f 高于追价上限 %.2f，已跳过开盘追价窗口并继续按有效期扫描", round2(openPrice), round2(maxChase)),
					rec.InvalidCondition,
				),
			}
		}
	}
	return openingPolicyActivationAction{}
}

func resolvePendingRecommendInvalidation(rec models.AiRecommendStocks, recordTime, evalEnd time.Time, bars []minuteBar, coverageOK bool) (string, string, bool) {
	if !coverageOK {
		return "", "", false
	}
	if stopLoss, ok := parseStopLossPrice(rec); ok && stopLoss > 0 {
		if triggerTime, triggerPrice, hit := scanPendingStopLossInvalidationFromBars(bars, stopLoss); hit {
			reason := fmt.Sprintf(
				"激活前已跌破止损/失效位 %.2f（%s，触发价 %.2f）",
				round2(stopLoss),
				triggerTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
				round2(triggerPrice),
			)
			return appendRecommendInvalidConditionText(reason, rec.InvalidCondition), "invalid", true
		}
	}
	if expiryTime, effectiveCycle, ok := resolveRecommendPendingActivationExpiryForRecommend(rec, recordTime); ok && !evalEnd.Before(expiryTime) {
		rawCycle := strings.TrimSpace(rec.ExpectedCycle)
		reason := ""
		switch {
		case rawCycle != "" && effectiveCycle != "" && rawCycle != effectiveCycle:
			reason = fmt.Sprintf(
				"超过待激活有效期 %s（原预期周期 %s）仍未触发主买入区（截止 %s）",
				effectiveCycle,
				rawCycle,
				expiryTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		case effectiveCycle != "":
			reason = fmt.Sprintf(
				"超过待激活有效期 %s 仍未触发主买入区（截止 %s）",
				effectiveCycle,
				expiryTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		default:
			reason = fmt.Sprintf(
				"超过待激活有效期仍未触发主买入区（截止 %s）",
				expiryTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		}
		return appendRecommendInvalidConditionText(reason, rec.InvalidCondition), "expired", true
	}
	return "", "", false
}

func dataStatusForInactiveActivationStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "invalid":
		return "已失效"
	case "expired":
		return "已过期"
	default:
		return "已跳过"
	}
}

func resolveRecommendPendingActivationExpiryForRecommend(rec models.AiRecommendStocks, recordTime time.Time) (time.Time, string, bool) {
	if rule, err := parseActivationRuleJSON(rec.ActivationRuleJSON); err == nil && rule != nil {
		tradeDays := 0
		for _, path := range activationRulePaths(rule) {
			if path.ExpireTradeDays > tradeDays {
				tradeDays = path.ExpireTradeDays
			}
		}
		if tradeDays <= 0 && rule.ExpireTradeDays > 0 {
			tradeDays = rule.ExpireTradeDays
		}
		if tradeDays > 0 {
			expiry, ok := resolveRecommendTradeDayExpiry(recordTime, tradeDays)
			if !ok {
				return time.Time{}, "", false
			}
			return expiry, fmt.Sprintf("%d个交易日", tradeDays), true
		}
	}
	return resolveRecommendPendingActivationExpiry(recordTime, rec.ExpectedCycle)
}

func scanPendingStopLossInvalidationFromBars(bars []minuteBar, stopLoss float64) (time.Time, float64, bool) {
	if len(bars) == 0 || stopLoss <= 0 {
		return time.Time{}, 0, false
	}
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		if bar.Open > 0 && bar.Open <= stopLoss {
			return bar.TradeTime, bar.Open, true
		}
		if bar.Low > 0 && bar.Low <= stopLoss {
			return bar.TradeTime, stopLoss, true
		}
	}
	return time.Time{}, 0, false
}

func parseExpectedCycleTradeDays(expectedCycle string) (int, bool) {
	text := strings.TrimSpace(strings.ToLower(expectedCycle))
	if text == "" {
		return 0, false
	}
	maxValue, ok := parsePriceMaxFromText(text)
	if !ok || maxValue <= 0 {
		return 0, false
	}

	var multiplier float64
	switch {
	case strings.Contains(text, "月"):
		multiplier = 21
	case strings.Contains(text, "周"):
		multiplier = 5
	case strings.Contains(text, "交易日"), strings.Contains(text, "个交易日"), strings.Contains(text, "天"), strings.Contains(text, "日"):
		multiplier = 1
	default:
		return 0, false
	}

	days := int(maxValue * multiplier)
	if float64(days) < maxValue*multiplier {
		days++
	}
	if days <= 0 {
		return 0, false
	}
	return days, true
}

func resolveRecommendTradeDayExpiry(recordTime time.Time, tradeDays int) (time.Time, bool) {
	if tradeDays <= 0 {
		return time.Time{}, false
	}
	start := resolveRecommendBuyTime(recordTime)
	if start.IsZero() {
		return time.Time{}, false
	}
	loc := cnLocation()
	day := time.Date(start.In(loc).Year(), start.In(loc).Month(), start.In(loc).Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(day) {
		day = shiftToNextCNOpenTradeDaySafe(day)
	}
	for i := 1; i < tradeDays; i++ {
		day = shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc), true
}

func resolveRecommendExpectedCycleExpiry(recordTime time.Time, expectedCycle string) (time.Time, bool) {
	tradeDays, ok := parseExpectedCycleTradeDays(expectedCycle)
	if !ok || tradeDays <= 0 {
		return time.Time{}, false
	}
	return resolveRecommendTradeDayExpiry(recordTime, tradeDays)
}

func resolveRecommendPendingActivationExpiry(recordTime time.Time, expectedCycle string) (time.Time, string, bool) {
	rawLabel := strings.TrimSpace(expectedCycle)
	tradeDays, ok := parseExpectedCycleTradeDays(expectedCycle)
	label := rawLabel
	if !ok || tradeDays <= 0 {
		tradeDays = recommendPendingActivationMaxTradeDays
		label = fmt.Sprintf("%d个交易日", tradeDays)
	}
	if tradeDays > recommendPendingActivationMaxTradeDays {
		tradeDays = recommendPendingActivationMaxTradeDays
		label = fmt.Sprintf("%d个交易日", tradeDays)
	}
	expiry, ok := resolveRecommendTradeDayExpiry(recordTime, tradeDays)
	if !ok {
		return time.Time{}, "", false
	}
	return expiry, label, true
}

func resolveAggregateActivation(aggr *aiRecommendYieldAggregate, ctx yieldBuildContext, allowHeadBackfill bool) (*time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常", DataStatusReason: ""}
	if aggr == nil || aggr.SignalTime.IsZero() {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "缺少推荐时间"
		return nil, 0, info
	}
	if aggr.BuyAmountCount <= 0 {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "缺少主买入区"
		return nil, 0, info
	}
	minPrice := calculateAvg(aggr.BuyAmountSum, aggr.BuyAmountCount)
	if minPrice <= 0 {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "主买入区无效"
		return nil, 0, info
	}
	start, end := resolveActivationWindow(aggr.SignalTime, ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate)
	if !start.Before(end) {
		info.DataStatus = "待激活"
		info.DataStatusReason = "主买入区尚未进入可扫描窗口"
		return nil, 0, info
	}
	var bars []minuteBar
	var cacheInfo minuteSyncInfo
	if ctx.DisableMinuteFetch {
		bars, cacheInfo = syncMinuteBarsFromCacheOnly(aggr.StockCode, start, end)
	} else {
		bars, cacheInfo = syncMinuteBars(aggr.StockCode, start, end, ctx.CrawlTimeout, allowHeadBackfill)
	}
	info.CacheStart = cacheInfo.CacheStart
	info.CacheEnd = cacheInfo.CacheEnd
	info.CacheUpdated = cacheInfo.CacheUpdated
	info.CacheSource = cacheInfo.CacheSource
	info.LastMinuteTs = cacheInfo.LastMinuteTs
	activationTime, activationPrice, ok, activityReason, activitySync := scanActivationFromBarsWithActivityFilter(
		aggr.StockCode,
		bars,
		minPrice,
		minPrice,
		aggr.RequirePrevDayActivityFilter,
		ctx,
		allowHeadBackfill,
	)
	mergeTriggerEvalInfoCache(&info, activitySync)
	if ok {
		t := activationTime
		info.ActivationTime = &t
		info.ActivationPrice = activationPrice
		return &t, activationPrice, info
	}
	if strings.TrimSpace(activityReason) != "" {
		info.DataStatus = "待激活"
		info.DataStatusReason = activityReason
		return nil, 0, info
	}
	if cacheInfo.SyncErr != nil {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "主买入区扫描失败；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
		return nil, 0, info
	}
	if len(bars) == 0 {
		info.DataStatus = "待激活"
		info.DataStatusReason = "分钟线不可用或尚未覆盖主买入区"
		return nil, 0, info
	}
	info.DataStatus = "待激活"
	info.DataStatusReason = "未触发主买入区"
	return nil, 0, info
}

type triggerEvalInfo struct {
	DataStatus       string
	DataStatusReason string
	LastMinuteTs     *time.Time
	CacheStart       *time.Time
	CacheEnd         *time.Time
	CacheUpdated     *time.Time
	CacheSource      string
	ActivationTime   *time.Time
	ActivationPrice  float64
}

type activitySessionSnapshot struct {
	Bars       []minuteBar
	SyncInfo   minuteSyncInfo
	FetchedEnd time.Time
}

type minuteActivityWindow struct {
	Count      int
	AmountSum  float64
	VolumeSum  float64
	Start      time.Time
	End        time.Time
	MetricName string
}

func recommendRequiresPrevDayActivityFilter(rec models.AiRecommendStocks) bool {
	if hasMachineActivationRule(&rec) {
		rule, err := parseActivationRuleJSON(rec.ActivationRuleJSON)
		if err == nil && rule != nil {
			for _, path := range activationRulePaths(rule) {
				if strings.Contains(path.Baseline, "prev_day") {
					return true
				}
			}
			return false
		}
	}
	texts := []string{rec.BuySignal, rec.BuySignalDetail}
	for _, text := range texts {
		normalized := strings.TrimSpace(text)
		if normalized == "" {
			continue
		}
		hasPrevDay := containsAnyKeyword(normalized, []string{"上一交易日", "前一交易日", "上个交易日", "较前一日", "较上一交易日"})
		hasActivity := containsAnyKeyword(normalized, []string{"活跃度", "量能", "成交额", "成交量", "量比"})
		if hasPrevDay && hasActivity {
			return true
		}
	}
	return false
}

func scanActivationFromBarsWithActivityFilter(
	tsCode string,
	bars []minuteBar,
	minPrice, maxPrice float64,
	requireActivity bool,
	ctx yieldBuildContext,
	allowHeadBackfill bool,
) (time.Time, float64, bool, string, minuteSyncInfo) {
	if !requireActivity {
		when, price, ok := scanActivationFromBars(bars, minPrice, maxPrice)
		return when, price, ok, "", minuteSyncInfo{}
	}
	sessionCache := map[string]*activitySessionSnapshot{}
	mergedSync := minuteSyncInfo{}
	lastReason := ""
	for _, bar := range bars {
		activationTime, activationPrice, ok := resolveActivationCandidateFromBar(bar, minPrice, maxPrice)
		if !ok {
			continue
		}
		passed, reason, syncInfo := validatePrevDayActivityForActivation(tsCode, activationTime, ctx, allowHeadBackfill, sessionCache)
		mergedSync = mergeMinuteSyncInfo(mergedSync, syncInfo)
		if passed {
			return activationTime, activationPrice, true, "", mergedSync
		}
		if strings.TrimSpace(reason) != "" {
			lastReason = reason
		}
	}
	return time.Time{}, 0, false, lastReason, mergedSync
}

func validatePrevDayActivityForActivation(
	tsCode string,
	triggerTime time.Time,
	ctx yieldBuildContext,
	allowHeadBackfill bool,
	sessionCache map[string]*activitySessionSnapshot,
) (bool, string, minuteSyncInfo) {
	currentBars, currentSync, ok := loadSessionBarsForActivity(tsCode, triggerTime, ctx, allowHeadBackfill, sessionCache)
	if !ok {
		reason := "已进入主买入区，但当前5分钟活跃度缺失"
		if currentSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(currentSync.SyncErr.Error())
		}
		return false, reason, currentSync
	}
	currentWindow := buildRecentActivityWindow(currentBars, triggerTime, 5)
	if currentWindow.Count <= 0 {
		reason := "已进入主买入区，但当前5分钟活跃度缺失"
		if currentSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(currentSync.SyncErr.Error())
		}
		return false, reason, currentSync
	}

	prevTriggerTime := previousTradingMoment(triggerTime)
	prevBars, prevSync, ok := loadSessionBarsForActivity(tsCode, prevTriggerTime, ctx, allowHeadBackfill, sessionCache)
	mergedSync := mergeMinuteSyncInfo(currentSync, prevSync)
	if !ok {
		reason := "已进入主买入区，但缺少上一交易日活跃度基准"
		if prevSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(prevSync.SyncErr.Error())
		}
		return false, reason, mergedSync
	}
	prevWindow := buildRecentActivityWindow(prevBars, prevTriggerTime, currentWindow.Count)
	if prevWindow.Count < currentWindow.Count {
		reason := "已进入主买入区，但缺少上一交易日活跃度基准"
		if prevSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(prevSync.SyncErr.Error())
		}
		return false, reason, mergedSync
	}

	if currentWindow.AmountSum > 0 && prevWindow.AmountSum > 0 {
		if currentWindow.AmountSum >= prevWindow.AmountSum {
			return true, "", mergedSync
		}
		return false, fmt.Sprintf(
			"已进入主买入区，但5分钟成交额 %.2f 低于上一交易日同一时刻 %.2f",
			round2(currentWindow.AmountSum),
			round2(prevWindow.AmountSum),
		), mergedSync
	}

	if currentWindow.VolumeSum > 0 && prevWindow.VolumeSum > 0 {
		if currentWindow.VolumeSum >= prevWindow.VolumeSum {
			return true, "", mergedSync
		}
		return false, fmt.Sprintf(
			"已进入主买入区，但5分钟成交量 %.2f 低于上一交易日同一时刻 %.2f",
			round2(currentWindow.VolumeSum),
			round2(prevWindow.VolumeSum),
		), mergedSync
	}

	if prevWindow.AmountSum <= 0 && prevWindow.VolumeSum <= 0 {
		reason := "已进入主买入区，但缺少上一交易日活跃度基准"
		if prevSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(prevSync.SyncErr.Error())
		}
		return false, reason, mergedSync
	}

	reason := "已进入主买入区，但当前5分钟活跃度缺失"
	if currentSync.SyncErr != nil {
		reason = reason + "；" + strings.TrimSpace(currentSync.SyncErr.Error())
	}
	return false, reason, mergedSync
}

func loadSessionBarsForActivity(
	tsCode string,
	endTime time.Time,
	ctx yieldBuildContext,
	allowHeadBackfill bool,
	sessionCache map[string]*activitySessionSnapshot,
) ([]minuteBar, minuteSyncInfo, bool) {
	sessionStart := resolveActivitySessionStart(endTime)
	if sessionStart.IsZero() {
		return nil, minuteSyncInfo{}, false
	}
	cacheKey := buildActivitySessionCacheKey(tsCode, endTime)
	if snapshot, ok := sessionCache[cacheKey]; ok && !snapshot.FetchedEnd.Before(normalizeMinuteTime(endTime)) {
		return snapshot.Bars, snapshot.SyncInfo, len(snapshot.Bars) > 0
	}
	var bars []minuteBar
	var syncInfo minuteSyncInfo
	if ctx.DisableMinuteFetch {
		bars, syncInfo = syncMinuteBarsFromCacheOnly(tsCode, sessionStart, normalizeMinuteTime(endTime))
	} else {
		bars, syncInfo = syncMinuteBars(tsCode, sessionStart, normalizeMinuteTime(endTime), ctx.CrawlTimeout, allowHeadBackfill)
	}
	sessionCache[cacheKey] = &activitySessionSnapshot{
		Bars:       bars,
		SyncInfo:   syncInfo,
		FetchedEnd: normalizeMinuteTime(endTime),
	}
	return bars, syncInfo, len(bars) > 0
}

func resolveActivitySessionStart(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	loc := t.Location()
	if loc == nil {
		loc = cnLocation()
	}
	morningClose := time.Date(t.Year(), t.Month(), t.Day(), 11, 30, 0, 0, loc)
	afternoonOpen := time.Date(t.Year(), t.Month(), t.Day(), 13, 1, 0, 0, loc)
	switch {
	case !t.After(morningClose):
		return time.Date(t.Year(), t.Month(), t.Day(), 9, 31, 0, 0, loc)
	case !t.Before(afternoonOpen):
		return afternoonOpen
	default:
		return time.Time{}
	}
}

func buildActivitySessionCacheKey(tsCode string, t time.Time) string {
	sessionStart := resolveActivitySessionStart(t)
	if sessionStart.IsZero() {
		return normalizeRecommendStockCode(tsCode)
	}
	return normalizeRecommendStockCode(tsCode) + "|" + sessionStart.Format("2006-01-02 15:04")
}

func buildRecentActivityWindow(bars []minuteBar, endTime time.Time, maxCount int) minuteActivityWindow {
	if len(bars) == 0 || endTime.IsZero() || maxCount <= 0 {
		return minuteActivityWindow{}
	}
	sessionStart := resolveActivitySessionStart(endTime)
	if sessionStart.IsZero() {
		return minuteActivityWindow{}
	}
	endTime = normalizeMinuteTime(endTime)
	selected := make([]minuteBar, 0, maxCount)
	for idx := len(bars) - 1; idx >= 0; idx-- {
		bar := bars[idx]
		if bar.TradeTime.IsZero() {
			continue
		}
		if bar.TradeTime.After(endTime) || bar.TradeTime.Before(sessionStart) {
			continue
		}
		selected = append(selected, bar)
		if len(selected) >= maxCount {
			break
		}
	}
	if len(selected) == 0 {
		return minuteActivityWindow{}
	}
	window := minuteActivityWindow{
		Count: len(selected),
		End:   selected[0].TradeTime,
		Start: selected[len(selected)-1].TradeTime,
	}
	for _, bar := range selected {
		if bar.Amount > 0 {
			window.AmountSum += bar.Amount
		}
		if bar.Volume > 0 {
			window.VolumeSum += bar.Volume
		}
	}
	return window
}

func previousTradingMoment(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	loc := t.Location()
	if loc == nil {
		loc = cnLocation()
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	prevDay := subtractTradingDaysByWeekday(day, 1)
	return time.Date(prevDay.Year(), prevDay.Month(), prevDay.Day(), t.Hour(), t.Minute(), 0, 0, loc)
}

func mergeMinuteSyncInfo(base, current minuteSyncInfo) minuteSyncInfo {
	result := base
	result.SyncErr = mergeSyncErr(base.SyncErr, current.SyncErr)
	if current.LastMinuteTs != nil && (result.LastMinuteTs == nil || result.LastMinuteTs.Before(*current.LastMinuteTs)) {
		t := *current.LastMinuteTs
		result.LastMinuteTs = &t
	}
	if current.CacheStart != nil && (result.CacheStart == nil || result.CacheStart.After(*current.CacheStart)) {
		t := *current.CacheStart
		result.CacheStart = &t
	}
	if current.CacheEnd != nil && (result.CacheEnd == nil || result.CacheEnd.Before(*current.CacheEnd)) {
		t := *current.CacheEnd
		result.CacheEnd = &t
	}
	if current.CacheUpdated != nil && (result.CacheUpdated == nil || result.CacheUpdated.Before(*current.CacheUpdated)) {
		t := *current.CacheUpdated
		result.CacheUpdated = &t
	}
	if strings.TrimSpace(current.CacheSource) != "" {
		result.CacheSource = current.CacheSource
	}
	result.CoverageOK = result.CoverageOK || current.CoverageOK
	return result
}

func mergeTriggerEvalInfoCache(info *triggerEvalInfo, syncInfo minuteSyncInfo) {
	if info == nil {
		return
	}
	if syncInfo.LastMinuteTs != nil && (info.LastMinuteTs == nil || info.LastMinuteTs.Before(*syncInfo.LastMinuteTs)) {
		t := *syncInfo.LastMinuteTs
		info.LastMinuteTs = &t
	}
	if syncInfo.CacheStart != nil && (info.CacheStart == nil || info.CacheStart.After(*syncInfo.CacheStart)) {
		t := *syncInfo.CacheStart
		info.CacheStart = &t
	}
	if syncInfo.CacheEnd != nil && (info.CacheEnd == nil || info.CacheEnd.Before(*syncInfo.CacheEnd)) {
		t := *syncInfo.CacheEnd
		info.CacheEnd = &t
	}
	if syncInfo.CacheUpdated != nil && (info.CacheUpdated == nil || info.CacheUpdated.Before(*syncInfo.CacheUpdated)) {
		t := *syncInfo.CacheUpdated
		info.CacheUpdated = &t
	}
	if strings.TrimSpace(syncInfo.CacheSource) != "" {
		info.CacheSource = syncInfo.CacheSource
	}
}

func evaluatePositionWithMinuteAndDaily(
	tsCode string,
	start, end time.Time,
	stopProfit, stopLoss *float64,
	_ *TushareApi,
	crawlTimeout int64,
	allowHeadBackfill bool,
	disableMinuteFetch bool,
) (string, time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常", DataStatusReason: ""}
	if !start.Before(end) {
		return "", time.Time{}, 0, info
	}

	var bars []minuteBar
	var cacheInfo minuteSyncInfo
	if disableMinuteFetch {
		bars, cacheInfo = syncMinuteBarsFromCacheOnly(tsCode, start, end)
	} else {
		bars, cacheInfo = syncMinuteBars(tsCode, start, end, crawlTimeout, allowHeadBackfill)
	}
	info.CacheStart = cacheInfo.CacheStart
	info.CacheEnd = cacheInfo.CacheEnd
	info.CacheUpdated = cacheInfo.CacheUpdated
	info.CacheSource = cacheInfo.CacheSource
	info.LastMinuteTs = cacheInfo.LastMinuteTs

	if len(bars) > 0 {
		status, t, price := scanMinuteTriggerFromBars(bars, stopProfit, stopLoss)
		if status != "" {
			return status, t, price, info
		}
		if cacheInfo.CoverageOK {
			return "", time.Time{}, 0, info
		}
		info.DataStatus = "无法判定"
		if cacheInfo.CacheStart != nil && cacheInfo.CacheEnd != nil {
			info.DataStatusReason = fmt.Sprintf(
				"分钟线覆盖不完整（缓存 %s~%s，目标 %s~%s）",
				cacheInfo.CacheStart.In(cnLocation()).Format("2006-01-02 15:04:05"),
				cacheInfo.CacheEnd.In(cnLocation()).Format("2006-01-02 15:04:05"),
				start.In(cnLocation()).Format("2006-01-02 15:04:05"),
				end.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		} else {
			info.DataStatusReason = "分钟线覆盖不完整"
		}
		if cacheInfo.SyncErr != nil {
			info.DataStatusReason = info.DataStatusReason + "；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
		}
		return "", time.Time{}, 0, info
	}

	info.DataStatus = "无法判定"
	if cacheInfo.SyncErr != nil {
		info.DataStatusReason = "分钟线不可用；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
	} else {
		info.DataStatusReason = "分钟线不可用"
	}
	return "", time.Time{}, 0, info
}
