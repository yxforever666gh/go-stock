package research2

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
	"time"

	"go-stock/internal/trading"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	AllocationReplayPolicyVersion = "remaining_cash_by_open_slots_v1"
	allocationReplayMode          = "historical_allocation_replay_v1"
	allocationReplayEpsilon       = 1e-7
)

type AllocationReplayGap struct {
	RecommendationID string    `json:"recommendationId"`
	Slot             string    `json:"slot"`
	StockCode        string    `json:"stockCode"`
	Phase            string    `json:"phase"`
	At               time.Time `json:"at"`
	Reason           string    `json:"reason"`
}

type AllocationReplayResult struct {
	ReplayID         string                    `json:"replayId"`
	PlanHash         string                    `json:"planHash"`
	PolicyVersion    string                    `json:"policyVersion"`
	DryRun           bool                      `json:"dryRun"`
	Reused           bool                      `json:"reused"`
	CandidateCount   int                       `json:"candidateCount"`
	BuyCount         int                       `json:"buyCount"`
	SellCount        int                       `json:"sellCount"`
	MissingBuyCount  int                       `json:"missingBuyCount"`
	MissingSellCount int                       `json:"missingSellCount"`
	Missing          []AllocationReplayGap     `json:"missing"`
	AccountCash      map[string]float64        `json:"accountCash"`
	Performance      PerformanceBackfillResult `json:"performance"`
	PerformanceError string                    `json:"performanceError,omitempty"`
}

type AllocationReplayService struct {
	repository  *Repository
	history     PerformanceHistoryProvider
	calendar    Calendar
	performance *PerformanceBackfillService
	now         func() time.Time
}

func NewAllocationReplayService(repository *Repository, history PerformanceHistoryProvider, calendar Calendar) *AllocationReplayService {
	return &AllocationReplayService{
		repository: repository, history: history, calendar: calendar,
		performance: NewPerformanceBackfillService(repository, history, calendar), now: time.Now,
	}
}

type allocationReplayCandidate struct {
	item        Recommendation
	run         AnalysisRun
	buyAt       time.Time
	targetSlots int
	state       *allocationReplayState
}

type allocationReplayQuote struct {
	at            time.Time
	marketPrice   float64
	previousClose float64
	limitPrice    float64
	distancePct   *float64
	source        string
}

type allocationReplayState struct {
	item              Recommendation
	status            string
	reason            string
	failureCode       string
	quote             *allocationReplayQuote
	targetSellAt      *time.Time
	buyTrade          *Trade
	sellTrade         *Trade
	historicalBlocked bool
}

type allocationReplayPlan struct {
	replayID    string
	planHash    string
	startedAt   time.Time
	states      []*allocationReplayState
	trades      []Trade
	accountCash map[string]float64
	filled      map[string]int
	gaps        []AllocationReplayGap
}

type allocationReplaySession struct {
	data BuyDayMarketData
	err  error
}

type allocationReplayPlanner struct {
	service      *AllocationReplayService
	now          time.Time
	sessions     map[string]allocationReplaySession
	nextSellDays map[string]time.Time
	gaps         []AllocationReplayGap
}

type allocationReplayPosition struct {
	candidate *allocationReplayCandidate
	targetAt  time.Time
	attempted bool
}

func (service *AllocationReplayService) ReplayAll(ctx context.Context, dryRun bool) (AllocationReplayResult, error) {
	result := AllocationReplayResult{PolicyVersion: AllocationReplayPolicyVersion, DryRun: dryRun, AccountCash: map[string]float64{}, Missing: []AllocationReplayGap{}}
	if service == nil || service.repository == nil || service.repository.db == nil || service.history == nil || service.calendar == nil {
		return result, errors.New("research2 allocation replay is unavailable")
	}
	startedAt := service.now().In(shanghai())
	plan, err := service.buildPlan(ctx, startedAt)
	if err != nil {
		return result, err
	}
	result = allocationReplayResultFromPlan(plan, dryRun)
	if dryRun {
		return result, nil
	}
	reused, err := service.applyPlan(ctx, plan)
	if err != nil {
		return result, err
	}
	result.Reused = reused
	performance, performanceErr := service.performance.BackfillAll(ctx)
	result.Performance = performance
	if performanceErr != nil {
		result.PerformanceError = performanceErr.Error()
	}
	return result, performanceErr
}

func allocationReplayResultFromPlan(plan allocationReplayPlan, dryRun bool) AllocationReplayResult {
	result := AllocationReplayResult{
		ReplayID: plan.replayID, PlanHash: plan.planHash, PolicyVersion: AllocationReplayPolicyVersion, DryRun: dryRun,
		CandidateCount: len(plan.states), Missing: append([]AllocationReplayGap(nil), plan.gaps...), AccountCash: map[string]float64{},
	}
	for _, state := range plan.states {
		if state.buyTrade != nil {
			result.BuyCount++
		}
		if state.sellTrade != nil {
			result.SellCount++
		}
	}
	for _, gap := range plan.gaps {
		if gap.Phase == "buy" {
			result.MissingBuyCount++
		} else if gap.Phase == "sell" {
			result.MissingSellCount++
		}
	}
	for slot, cash := range plan.accountCash {
		result.AccountCash[slot] = roundMoney(cash)
	}
	return result
}

func (service *AllocationReplayService) buildPlan(ctx context.Context, startedAt time.Time) (allocationReplayPlan, error) {
	plan := allocationReplayPlan{startedAt: startedAt, accountCash: map[string]float64{}, filled: map[string]int{}}
	var runs []AnalysisRun
	if err := service.repository.db.WithContext(ctx).Where("status = ?", "success").Order("trading_date ASC, generated_at ASC, id ASC").Find(&runs).Error; err != nil {
		return plan, err
	}
	if len(runs) == 0 {
		return plan, errors.New("research2 allocation replay has no successful reports")
	}
	runByID := make(map[string]AnalysisRun, len(runs))
	runIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		runByID[run.RunID] = run
		runIDs = append(runIDs, run.RunID)
	}
	var recommendations []Recommendation
	if err := service.repository.db.WithContext(ctx).Where("analysis_run_id IN ?", runIDs).Order("signal_at ASC, id ASC").Find(&recommendations).Error; err != nil {
		return plan, err
	}
	if len(recommendations) == 0 {
		return plan, errors.New("research2 allocation replay has no recommendation rows")
	}
	var allTrades []Trade
	if err := service.repository.db.WithContext(ctx).Find(&allTrades).Error; err != nil {
		return plan, err
	}
	allowed := make(map[string]struct{}, len(recommendations))
	for _, item := range recommendations {
		allowed[item.RecommendationID] = struct{}{}
	}
	for _, trade := range allTrades {
		if _, ok := allowed[trade.RecommendationID]; !ok {
			return plan, fmt.Errorf("research2 trade %s is outside successful report history", trade.TradeID)
		}
	}
	var chains []ExecutionChain
	if err := service.repository.db.WithContext(ctx).Order("trading_date ASC, slot ASC").Find(&chains).Error; err != nil {
		return plan, err
	}
	targetByDay := make(map[string]int, len(chains))
	for _, chain := range chains {
		if chain.TargetSlots != 3 && chain.TargetSlots != DailyTargetSlots {
			return plan, fmt.Errorf("research2 chain %s has unsupported target slots %d", chain.ChainID, chain.TargetSlots)
		}
		targetByDay[allocationReplayDayKey(chain.Slot, chain.TradingDate)] = chain.TargetSlots
	}
	var capital []AccountCapitalEvent
	if err := service.repository.db.WithContext(ctx).Where("external = ?", true).Order("effective_at ASC, event_id ASC").Find(&capital).Error; err != nil {
		return plan, err
	}
	eventsBySlot := make(map[string][]AccountCapitalEvent, len(Slots()))
	for _, event := range capital {
		eventsBySlot[event.Slot] = append(eventsBySlot[event.Slot], event)
	}
	for _, slot := range Slots() {
		total := 0.0
		for _, event := range eventsBySlot[slot] {
			total += event.Amount
		}
		if math.Abs(total-20000) > 0.01 {
			return plan, fmt.Errorf("research2 slot %s external capital is %.2f, want 20000", slot, total)
		}
	}

	bySlot := make(map[string][]*allocationReplayCandidate, len(Slots()))
	for index := range recommendations {
		item := recommendations[index]
		run, ok := runByID[item.AnalysisRunID]
		if !ok || !ValidSlot(item.Slot) {
			return plan, fmt.Errorf("research2 recommendation %s has invalid run or slot", item.RecommendationID)
		}
		target := targetByDay[allocationReplayDayKey(item.Slot, run.TradingDate)]
		if target == 0 {
			target = 3
		}
		state := &allocationReplayState{item: item, status: "analysis_only", reason: "历史仓位重放后未成交"}
		candidate := &allocationReplayCandidate{item: item, run: run, buyAt: allocationReplayFirstCompleteMinute(item), targetSlots: target, state: state}
		bySlot[item.Slot] = append(bySlot[item.Slot], candidate)
		plan.states = append(plan.states, state)
	}
	planner := &allocationReplayPlanner{service: service, now: startedAt, sessions: map[string]allocationReplaySession{}, nextSellDays: map[string]time.Time{}}
	for _, slot := range Slots() {
		candidates := bySlot[slot]
		sort.SliceStable(candidates, func(i, j int) bool { return allocationReplayCandidateLess(candidates[i], candidates[j]) })
		cash, trades, filled, err := planner.replaySlot(ctx, slot, candidates, eventsBySlot[slot])
		if err != nil {
			return plan, err
		}
		plan.accountCash[slot] = cash
		plan.trades = append(plan.trades, trades...)
		for key, count := range filled {
			plan.filled[key] = count
		}
	}
	plan.gaps = planner.gaps
	sort.SliceStable(plan.states, func(i, j int) bool {
		return plan.states[i].item.RecommendationID < plan.states[j].item.RecommendationID
	})
	sort.SliceStable(plan.trades, func(i, j int) bool {
		if !plan.trades[i].TradedAt.Equal(plan.trades[j].TradedAt) {
			return plan.trades[i].TradedAt.Before(plan.trades[j].TradedAt)
		}
		if plan.trades[i].Side != plan.trades[j].Side {
			return plan.trades[i].Side == "sell"
		}
		return plan.trades[i].RecommendationID < plan.trades[j].RecommendationID
	})
	hash, err := allocationReplayPlanHash(plan, capital)
	if err != nil {
		return plan, err
	}
	plan.planHash = hash
	plan.replayID = "allocation-" + hash[:40]
	return plan, nil
}

func allocationReplayCandidateLess(left, right *allocationReplayCandidate) bool {
	if !left.buyAt.Equal(right.buyAt) {
		return left.buyAt.Before(right.buyAt)
	}
	leftRun, rightRun := allocationReplayRunTime(left.run), allocationReplayRunTime(right.run)
	if !leftRun.Equal(rightRun) {
		return leftRun.Before(rightRun)
	}
	if left.item.FinalScore != right.item.FinalScore {
		return left.item.FinalScore > right.item.FinalScore
	}
	if left.item.StockCode != right.item.StockCode {
		return left.item.StockCode < right.item.StockCode
	}
	return left.item.RecommendationID < right.item.RecommendationID
}

func allocationReplayRunTime(run AnalysisRun) time.Time {
	if run.GeneratedAt != nil && !run.GeneratedAt.IsZero() {
		return run.GeneratedAt.In(shanghai())
	}
	if run.PersistedAt != nil && !run.PersistedAt.IsZero() {
		return run.PersistedAt.In(shanghai())
	}
	return run.StartedAt.In(shanghai())
}

func allocationReplayFirstCompleteMinute(item Recommendation) time.Time {
	at := item.SignalAt.In(shanghai())
	if item.TargetBuyAt.After(at) {
		at = item.TargetBuyAt.In(shanghai())
	}
	minute := at.Truncate(time.Minute)
	if at.After(minute) {
		minute = minute.Add(time.Minute)
	}
	return minute
}

func allocationReplayDayKey(slot, date string) string { return slot + "|" + date }

func (planner *allocationReplayPlanner) replaySlot(ctx context.Context, slot string, candidates []*allocationReplayCandidate, capital []AccountCapitalEvent) (float64, []Trade, map[string]int, error) {
	cash := 0.0
	capitalIndex := 0
	filled := map[string]int{}
	boughtCodes := map[string]map[string]bool{}
	activeCodes := map[string]bool{}
	positions := make([]*allocationReplayPosition, 0)
	trades := make([]Trade, 0)
	eventOrdinal := 0
	applyCapital := func(until time.Time) {
		for capitalIndex < len(capital) && !capital[capitalIndex].EffectiveAt.After(until) {
			cash += capital[capitalIndex].Amount
			capitalIndex++
		}
	}
	processSells := func(until time.Time) error {
		due := make([]*allocationReplayPosition, 0)
		for _, position := range positions {
			if !position.attempted && !position.targetAt.After(until) {
				due = append(due, position)
			}
		}
		sort.SliceStable(due, func(i, j int) bool {
			if !due[i].targetAt.Equal(due[j].targetAt) {
				return due[i].targetAt.Before(due[j].targetAt)
			}
			return due[i].candidate.item.RecommendationID < due[j].candidate.item.RecommendationID
		})
		for _, position := range due {
			position.attempted = true
			candidate, state := position.candidate, position.candidate.state
			quote, _, reason := planner.executionQuote(ctx, candidate.item, position.targetAt, true)
			if reason != "" {
				state.status, state.reason, state.historicalBlocked = "sell_pending", "历史目标卖出未重放："+reason, true
				planner.gaps = append(planner.gaps, AllocationReplayGap{RecommendationID: candidate.item.RecommendationID, Slot: slot, StockCode: candidate.item.StockCode, Phase: "sell", At: position.targetAt, Reason: reason})
				continue
			}
			cost := trading.CalculateSellCost(quote.marketPrice, state.buyTrade.Quantity)
			eventOrdinal++
			tradedAt := quote.at.Add(time.Duration(eventOrdinal) * time.Millisecond)
			quoteAt := quote.at
			trade := Trade{
				TradeID: allocationReplayTradeID(candidate.item.RecommendationID, "sell"), Slot: slot, RecommendationID: candidate.item.RecommendationID,
				Side: "sell", TradedAt: tradedAt, MarketPrice: quote.marketPrice, ExecutionPrice: cost.ExecutionPrice, Quantity: state.buyTrade.Quantity,
				Commission: cost.Commission, StampDuty: cost.StampDuty, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount,
				NetCashFlow: cost.NetCashFlow, PriceSource: quote.source, ExecutionMode: allocationReplayMode, QuoteAt: &quoteAt,
			}
			state.sellTrade, state.status, state.reason, state.failureCode = &trade, "closed", "", ""
			trades = append(trades, trade)
			cash += trade.NetCashFlow
			delete(activeCodes, candidate.item.StockCode)
		}
		return nil
	}

	for _, candidate := range candidates {
		applyCapital(candidate.buyAt)
		if err := processSells(candidate.buyAt); err != nil {
			return 0, nil, nil, err
		}
		state := candidate.state
		quote, code, reason := planner.executionQuote(ctx, candidate.item, candidate.buyAt, false)
		if reason == "" {
			state.quote = &quote
		}
		dayKey := allocationReplayDayKey(slot, candidate.run.TradingDate)
		if filled[dayKey] >= candidate.targetSlots {
			state.status, state.reason = "standby_not_used", fmt.Sprintf("历史重放已完成本账户当日%d笔买入", candidate.targetSlots)
			continue
		}
		if reason != "" {
			state.status, state.reason, state.failureCode = "missed_untradable", reason, code
			planner.gaps = append(planner.gaps, AllocationReplayGap{RecommendationID: candidate.item.RecommendationID, Slot: slot, StockCode: candidate.item.StockCode, Phase: "buy", At: candidate.buyAt, Reason: reason})
			continue
		}
		if activeCodes[candidate.item.StockCode] || (boughtCodes[candidate.run.TradingDate] != nil && boughtCodes[candidate.run.TradingDate][candidate.item.StockCode]) {
			state.status, state.reason = "analysis_only", "历史重放跳过同账户重复股票"
			continue
		}
		remaining := candidate.targetSlots - filled[dayKey]
		quantity, cost, sizeErr := SizeRemainingCashBuy(candidate.item.StockCode, quote.marketPrice, cash, remaining)
		if sizeErr != nil {
			lot, lotErr := trading.LotSize(candidate.item.StockCode)
			if lotErr != nil {
				return 0, nil, nil, lotErr
			}
			lotCash := -trading.CalculateBuyCost(quote.marketPrice, lot).NetCashFlow
			state.status, state.reason = "missed_cash", fmt.Sprintf("历史重放剩余现金%.2f元不足支付一手含费成本%.2f元", cash, lotCash)
			continue
		}
		targetSellAt, sellErr := planner.targetSellAt(ctx, candidate)
		if sellErr != nil {
			return 0, nil, nil, sellErr
		}
		eventOrdinal++
		tradedAt := quote.at.Add(time.Duration(eventOrdinal) * time.Millisecond)
		quoteAt := quote.at
		trade := Trade{
			TradeID: allocationReplayTradeID(candidate.item.RecommendationID, "buy"), Slot: slot, RecommendationID: candidate.item.RecommendationID,
			Side: "buy", TradedAt: tradedAt, MarketPrice: quote.marketPrice, ExecutionPrice: cost.ExecutionPrice, Quantity: quantity,
			Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow,
			PriceSource: quote.source, ExecutionMode: allocationReplayMode, QuoteAt: &quoteAt,
		}
		if cash+trade.NetCashFlow < -allocationReplayEpsilon {
			return 0, nil, nil, fmt.Errorf("research2 allocation replay overdraws slot %s at %s", slot, candidate.item.RecommendationID)
		}
		state.buyTrade, state.targetSellAt, state.status, state.reason, state.failureCode = &trade, &targetSellAt, "active", "", ""
		trades = append(trades, trade)
		cash += trade.NetCashFlow
		filled[dayKey]++
		if boughtCodes[candidate.run.TradingDate] == nil {
			boughtCodes[candidate.run.TradingDate] = map[string]bool{}
		}
		boughtCodes[candidate.run.TradingDate][candidate.item.StockCode] = true
		activeCodes[candidate.item.StockCode] = true
		positions = append(positions, &allocationReplayPosition{candidate: candidate, targetAt: targetSellAt})
	}
	applyCapital(planner.now)
	if err := processSells(planner.now); err != nil {
		return 0, nil, nil, err
	}
	for _, position := range positions {
		if position.candidate.state.sellTrade == nil && !position.candidate.state.historicalBlocked {
			position.candidate.state.status = "active"
		}
	}
	if cash < -allocationReplayEpsilon {
		return 0, nil, nil, fmt.Errorf("research2 allocation replay leaves slot %s cash negative: %.4f", slot, cash)
	}
	return cash, trades, filled, nil
}

func (planner *allocationReplayPlanner) targetSellAt(ctx context.Context, candidate *allocationReplayCandidate) (time.Time, error) {
	if candidate.item.BuyAt != nil && candidate.item.TargetSellAt != nil && !candidate.item.TargetSellAt.IsZero() {
		return candidate.item.TargetSellAt.In(shanghai()), nil
	}
	hour, minute := 10, 0
	if strings.HasPrefix(strings.TrimSpace(candidate.run.StrategyVersion), "research2-slots-") {
		if _, err := fmt.Sscanf(candidate.item.Slot, "%d:%d", &hour, &minute); err != nil {
			return time.Time{}, err
		}
	}
	key := candidate.buyAt.Format("2006-01-02") + "|" + fmt.Sprintf("%02d:%02d", hour, minute)
	if value, ok := planner.nextSellDays[key]; ok {
		return value, nil
	}
	day := candidate.buyAt.In(shanghai()).AddDate(0, 0, 1)
	for checked := 0; checked < 20; checked++ {
		open, err := planner.service.calendar.IsTradingDay(ctx, day)
		if err != nil {
			return time.Time{}, err
		}
		if open {
			value := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, shanghai())
			planner.nextSellDays[key] = value
			return value, nil
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, errors.New("20日内找不到历史重放卖出交易日")
}

func (planner *allocationReplayPlanner) executionQuote(ctx context.Context, item Recommendation, at time.Time, sell bool) (allocationReplayQuote, string, string) {
	session := planner.marketSession(ctx, item, at)
	phase := "买入"
	if sell {
		phase = "卖出"
	}
	if session.err != nil {
		return allocationReplayQuote{}, "historical_quote_unavailable", phase + "日行情不可用：" + session.err.Error()
	}
	var selected *PerformanceBar
	for index := range session.data.Bars {
		if session.data.Bars[index].At.In(shanghai()).Truncate(time.Minute).Equal(at.In(shanghai()).Truncate(time.Minute)) {
			copy := session.data.Bars[index]
			selected = &copy
			break
		}
	}
	if selected == nil {
		return allocationReplayQuote{}, "historical_minute_missing", phase + "目标分钟线缺失"
	}
	if selected.Open <= 0 || selected.High <= 0 || selected.Low <= 0 || selected.Close <= 0 || selected.High < selected.Low {
		return allocationReplayQuote{}, "historical_minute_invalid", phase + "目标分钟线价格无效"
	}
	if selected.Volume <= 0 && selected.Amount <= 0 {
		return allocationReplayQuote{}, "historical_suspended", phase + "目标分钟无可验证成交量"
	}
	if session.data.PreviousClose <= 0 || session.data.LimitRate <= 0 || strings.TrimSpace(session.data.NoLimitReason) != "" {
		reason := strings.TrimSpace(session.data.NoLimitReason)
		if reason == "" {
			reason = "缺少前收盘价或适用涨跌停规则"
		}
		return allocationReplayQuote{}, "historical_limit_unavailable", phase + "可交易性无法验证：" + reason
	}
	price := allocationReplayBarPrice(*selected)
	if price <= 0 {
		return allocationReplayQuote{}, "historical_minute_invalid", phase + "目标分钟成交价无效"
	}
	upper := UpperLimitPrice(session.data.PreviousClose, session.data.LimitRate)
	lower := lowerLimitPrice(session.data.PreviousClose, session.data.LimitRate)
	if lower <= 0 || upper <= 0 {
		return allocationReplayQuote{}, "historical_limit_unavailable", phase + "涨跌停价无法验证"
	}
	if sell && (price <= lower+0.000001 || selected.High <= lower+0.000001) {
		return allocationReplayQuote{}, "historical_limit_down", "卖出目标分钟封死跌停，无法验证成交"
	}
	if !sell {
		if price >= upper-0.000001 {
			return allocationReplayQuote{}, "limit_up", "历史重放目标分钟已涨停"
		}
		if price <= lower+0.000001 {
			return allocationReplayQuote{}, "limit_down", "历史重放目标分钟已跌停"
		}
		distance, ok := LimitDistancePct(price, upper)
		if !ok {
			return allocationReplayQuote{}, "historical_limit_unavailable", "历史重放涨停距离无法验证"
		}
		if distance+1e-9 < ExecutionLimitDistancePct {
			return allocationReplayQuote{}, "near_limit_up", fmt.Sprintf("历史重放距涨停价仅%.3f%%，低于执行缓冲%.1f%%", distance, ExecutionLimitDistancePct)
		}
		return allocationReplayQuote{at: selected.At.In(shanghai()).Truncate(time.Minute), marketPrice: price, previousClose: session.data.PreviousClose, limitPrice: upper, distancePct: &distance, source: selected.Source}, "", ""
	}
	return allocationReplayQuote{at: selected.At.In(shanghai()).Truncate(time.Minute), marketPrice: price, previousClose: session.data.PreviousClose, limitPrice: upper, source: selected.Source}, "", ""
}

func (planner *allocationReplayPlanner) marketSession(ctx context.Context, item Recommendation, at time.Time) allocationReplaySession {
	key := strings.ToLower(strings.TrimSpace(item.StockCode)) + "|" + at.In(shanghai()).Format("2006-01-02")
	if cached, ok := planner.sessions[key]; ok {
		return cached
	}
	copy := item
	buyAt := at.In(shanghai())
	copy.BuyAt = &buyAt
	data, err := planner.service.history.BuyDayData(ctx, copy)
	value := allocationReplaySession{data: data, err: err}
	planner.sessions[key] = value
	return value
}

func allocationReplayBarPrice(bar PerformanceBar) float64 {
	if bar.Amount > 0 && bar.Volume > 0 {
		value := bar.Amount / bar.Volume
		if value > bar.Low*0.8 && value < bar.High*1.2 {
			return value
		}
	}
	return bar.Close
}

func lowerLimitPrice(previousClose, rate float64) float64 {
	if previousClose <= 0 || rate <= 0 || math.IsNaN(previousClose) || math.IsInf(previousClose, 0) {
		return 0
	}
	previousCloseCents := int64(math.Floor(previousClose*100 + 0.5))
	rateBasisPoints := int64(math.Floor(rate*10000 + 0.5))
	limitCents := (previousCloseCents*(10000-rateBasisPoints) + 5000) / 10000
	return float64(limitCents) / 100
}

func allocationReplayTradeID(recommendationID, side string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock:research2:allocation-replay:"+recommendationID+":"+side)).String()
}

func allocationReplayPlanHash(plan allocationReplayPlan, capital []AccountCapitalEvent) (string, error) {
	type stateHash struct {
		ID, Status, Failure, Source string
		BuyAt, SellAt               string
		BuyPrice, SellPrice         float64
		Quantity                    int64
		Blocked                     bool
	}
	type capitalHash struct {
		ID, Slot, At string
		Amount       float64
	}
	payload := struct {
		Policy  string
		States  []stateHash
		Capital []capitalHash
	}{Policy: AllocationReplayPolicyVersion}
	for _, state := range plan.states {
		row := stateHash{ID: state.item.RecommendationID, Status: state.status, Failure: state.failureCode, Blocked: state.historicalBlocked}
		if state.buyTrade != nil {
			row.BuyAt, row.BuyPrice, row.Quantity, row.Source = state.buyTrade.TradedAt.Format(time.RFC3339Nano), state.buyTrade.MarketPrice, state.buyTrade.Quantity, state.buyTrade.PriceSource
		}
		if state.sellTrade != nil {
			row.SellAt, row.SellPrice = state.sellTrade.TradedAt.Format(time.RFC3339Nano), state.sellTrade.MarketPrice
		}
		payload.States = append(payload.States, row)
	}
	for _, event := range capital {
		payload.Capital = append(payload.Capital, capitalHash{ID: event.EventID, Slot: event.Slot, At: event.EffectiveAt.Format(time.RFC3339Nano), Amount: event.Amount})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func (service *AllocationReplayService) applyPlan(ctx context.Context, plan allocationReplayPlan) (bool, error) {
	reused := false
	err := research2TransactionWithWriteRetry(ctx, service.repository.db, func(tx *gorm.DB) error {
		var existing AllocationReplay
		if err := tx.Where("plan_hash = ? AND status = ?", plan.planHash, "complete").First(&existing).Error; err == nil {
			reused = true
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&Trade{}).Error; err != nil {
			return fmt.Errorf("clear research2 trades: %w", err)
		}
		if err := tx.Where("event_type = ? AND external = ?", CapitalEventLegacyPoolTransfer, false).Delete(&AccountCapitalEvent{}).Error; err != nil {
			return fmt.Errorf("clear legacy research2 transfers: %w", err)
		}
		if err := tx.Where("valuation_basis = ?", CapitalValuationBasisLedger).Delete(&AccountLedgerSnapshot{}).Error; err != nil {
			return fmt.Errorf("clear research2 ledger snapshots: %w", err)
		}
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&AccountDailyValuation{}).Error; err != nil {
			return fmt.Errorf("clear research2 daily valuations: %w", err)
		}
		for _, state := range plan.states {
			if err := tx.Model(&Recommendation{}).Where("recommendation_id = ?", state.item.RecommendationID).Updates(allocationReplayRecommendationUpdates(plan.replayID, state)).Error; err != nil {
				return fmt.Errorf("rewrite research2 recommendation %s: %w", state.item.RecommendationID, err)
			}
		}
		for index := range plan.trades {
			if err := tx.Create(&plan.trades[index]).Error; err != nil {
				return fmt.Errorf("write replay trade %s: %w", plan.trades[index].TradeID, err)
			}
		}
		for _, slot := range Slots() {
			cash, ok := plan.accountCash[slot]
			if !ok || cash < -allocationReplayEpsilon {
				return fmt.Errorf("research2 replay account %s has invalid cash %.4f", slot, cash)
			}
			result := tx.Model(&Account{}).Where("slot = ?", slot).Updates(map[string]any{"initial_cash": 10000.0, "cash": cash})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("research2 replay account %s is missing", slot)
			}
		}
		var chains []ExecutionChain
		if err := tx.Find(&chains).Error; err != nil {
			return err
		}
		for _, chain := range chains {
			count := plan.filled[allocationReplayDayKey(chain.Slot, chain.TradingDate)]
			updates := map[string]any{"allocation_policy": AllocationPolicyRemainingCashSlots, "filled_slots": count}
			if strings.TrimSpace(chain.WinnerRunID) != "" {
				updates["status"] = "completed"
				updates["stop_reason"] = fmt.Sprintf("历史动态仓位重放完成：已买入%d/%d", count, chain.TargetSlots)
				if chain.CompletedAt == nil {
					updates["completed_at"] = plan.startedAt
				}
			}
			if err := tx.Model(&ExecutionChain{}).Where("chain_id = ?", chain.ChainID).Updates(updates).Error; err != nil {
				return err
			}
		}
		if err := rebuildAllocationReplayLedger(tx); err != nil {
			return err
		}
		summary := allocationReplayResultFromPlan(plan, false)
		summaryBody, _ := json.Marshal(summary)
		completedAt := plan.startedAt
		receipt := AllocationReplay{
			ReplayID: plan.replayID, PolicyVersion: AllocationReplayPolicyVersion, PlanHash: plan.planHash, Status: "complete",
			CandidateCount: summary.CandidateCount, BuyCount: summary.BuyCount, SellCount: summary.SellCount,
			MissingBuyCount: summary.MissingBuyCount, MissingSellCount: summary.MissingSellCount,
			SummaryJSON: string(summaryBody), StartedAt: plan.startedAt, CompletedAt: &completedAt,
		}
		return tx.Create(&receipt).Error
	})
	return reused, err
}

func allocationReplayRecommendationUpdates(replayID string, state *allocationReplayState) map[string]any {
	updates := map[string]any{
		"status": state.status, "failure_reason": state.reason, "historical_replay_id": replayID,
		"historical_sell_blocked": state.historicalBlocked, "baseline_value": nil, "period_pn_l": nil,
		"buy_at": nil, "buy_market_price": 0.0, "buy_price": 0.0, "quantity": int64(0), "buy_fees": 0.0,
		"current_price": 0.0, "current_price_at": nil, "target_sell_at": nil, "sell_at": nil,
		"sell_market_price": 0.0, "sell_price": 0.0, "sell_fees": 0.0, "net_pn_l": 0.0, "net_yield_rate": 0.0,
		"execution_failure_code": state.failureCode, "execution_quote_price": 0.0, "execution_quote_at": nil,
		"execution_limit_price": 0.0, "execution_limit_distance_pct": nil,
		"buy_day_limit_outcome": "", "buy_day_limit_status": LimitOutcomePending, "buy_day_limit_evaluated_at": nil,
		"buy_day_limit_attempt_count": 0, "buy_day_limit_source_json": "[]", "buy_day_limit_failure_reason": "",
	}
	if state.quote != nil {
		updates["execution_quote_price"], updates["execution_quote_at"], updates["execution_limit_price"] = state.quote.marketPrice, state.quote.at, state.quote.limitPrice
		if state.quote.distancePct != nil {
			updates["execution_limit_distance_pct"] = *state.quote.distancePct
		}
	}
	if state.buyTrade == nil {
		return updates
	}
	buyFees := state.buyTrade.Commission + state.buyTrade.TransferFee
	updates["buy_at"], updates["buy_market_price"], updates["buy_price"] = state.buyTrade.TradedAt, state.buyTrade.MarketPrice, state.buyTrade.ExecutionPrice
	updates["quantity"], updates["buy_fees"] = state.buyTrade.Quantity, buyFees
	updates["current_price"], updates["current_price_at"], updates["target_sell_at"] = state.buyTrade.MarketPrice, state.buyTrade.TradedAt, state.targetSellAt
	if state.sellTrade == nil {
		return updates
	}
	sellFees := state.sellTrade.Commission + state.sellTrade.StampDuty + state.sellTrade.TransferFee
	buyCost := -state.buyTrade.NetCashFlow
	netPnL := state.sellTrade.NetCashFlow - buyCost
	netRate := 0.0
	if buyCost > 0 {
		netRate = netPnL / buyCost
	}
	updates["sell_at"], updates["sell_market_price"], updates["sell_price"] = state.sellTrade.TradedAt, state.sellTrade.MarketPrice, state.sellTrade.ExecutionPrice
	updates["sell_fees"], updates["current_price"], updates["current_price_at"] = sellFees, state.sellTrade.MarketPrice, state.sellTrade.TradedAt
	updates["net_pn_l"], updates["net_yield_rate"] = netPnL, netRate
	return updates
}

func rebuildAllocationReplayLedger(tx *gorm.DB) error {
	var capital []AccountCapitalEvent
	if err := tx.Order("effective_at ASC, event_id ASC").Find(&capital).Error; err != nil {
		return err
	}
	var trades []Trade
	if err := tx.Order("traded_at ASC, trade_id ASC").Find(&trades).Error; err != nil {
		return err
	}
	type ledgerEvent struct {
		at       time.Time
		priority int
		key      string
		slot     string
		capital  *AccountCapitalEvent
		trade    *Trade
	}
	events := make([]ledgerEvent, 0, len(capital)+len(trades))
	for index := range capital {
		events = append(events, ledgerEvent{at: capital[index].EffectiveAt, priority: 0, key: capital[index].EventID, slot: capital[index].Slot, capital: &capital[index]})
	}
	for index := range trades {
		priority := 2
		if trades[index].Side == "sell" {
			priority = 1
		}
		events = append(events, ledgerEvent{at: trades[index].TradedAt, priority: priority, key: trades[index].TradeID, slot: trades[index].Slot, trade: &trades[index]})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].at.Equal(events[j].at) {
			return events[i].at.Before(events[j].at)
		}
		if events[i].priority != events[j].priority {
			return events[i].priority < events[j].priority
		}
		return events[i].key < events[j].key
	})
	cash, external, transfer := map[string]float64{}, map[string]float64{}, map[string]float64{}
	positions := map[string]map[string]Trade{}
	for _, event := range events {
		if event.capital != nil {
			cash[event.slot] += event.capital.Amount
			if event.capital.External {
				external[event.slot] += event.capital.Amount
			} else {
				transfer[event.slot] += event.capital.Amount
			}
		} else {
			cash[event.slot] += event.trade.NetCashFlow
			if positions[event.slot] == nil {
				positions[event.slot] = map[string]Trade{}
			}
			if event.trade.Side == "buy" {
				positions[event.slot][event.trade.RecommendationID] = *event.trade
			} else {
				delete(positions[event.slot], event.trade.RecommendationID)
			}
		}
		if cash[event.slot] < -allocationReplayEpsilon {
			return fmt.Errorf("research2 replay ledger overdraws slot %s at %s", event.slot, event.key)
		}
		positionValue := 0.0
		for _, holding := range positions[event.slot] {
			positionValue += trading.CalculateSellCost(holding.MarketPrice, holding.Quantity).NetCashFlow
		}
		nav := cash[event.slot] + positionValue
		profit := nav - external[event.slot] - transfer[event.slot]
		rate := 0.0
		if external[event.slot] > 0 {
			rate = profit / external[event.slot]
		}
		kind := "trade"
		if event.capital != nil {
			kind = event.capital.EventType
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock:research2:allocation-ledger:"+event.slot+":"+event.key)).String()
		local := event.at.In(shanghai())
		snapshot := AccountLedgerSnapshot{
			SnapshotID: "replay-" + id, Slot: event.slot, ValuedAt: event.at, TradingDate: local.Format("2006-01-02"), SnapshotType: kind,
			Cash: roundMoney(cash[event.slot]), PositionValue: roundMoney(positionValue), NetAssetValue: roundMoney(nav),
			CumulativeExternalCapital: roundMoney(external[event.slot]), NetInternalTransfer: roundMoney(transfer[event.slot]),
			NetProfit: roundMoney(profit), CumulativeCapitalReturn: rate, ValuationBasis: CapitalValuationBasisLedger,
		}
		if err := tx.Create(&snapshot).Error; err != nil {
			return err
		}
	}
	return nil
}
