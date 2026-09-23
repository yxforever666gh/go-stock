package migrations

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"go-stock/backend/research2"
	"go-stock/internal/trading"
)

const (
	research2CapitalInitialAmount = 10000.0
	research2CapitalRebaseDate    = "2026-09-21"
	research2CapitalRebaseEpsilon = 1e-7
)

var (
	research2CapitalInitialAt = time.Date(2026, 8, 27, 9, 30, 0, 0, research2Shanghai())
	research2CapitalTopUpAt   = time.Date(2026, 9, 21, 9, 25, 0, 0, research2Shanghai())
)

func mainMigrationV31Definition() string {
	return strings.Join([]string{
		"research2_account_capital_events records 10000 initial external capital at 2026-08-27 09:30 Asia/Shanghai and 10000 top-up at 2026-09-21 09:25 for every slot",
		"pre-top-up slot replay inserts non-external legacy_shared_pool transfer only before a buy that would otherwise overdraft",
		"research2_account_ledger_snapshots is the derived capital-ledger valuation curve; raw research2_account_snapshots remain audit history",
		"all 2026-09-21 winner-run buys are rebuilt from stored execution quotes under the persisted fixed one-fifth sizing rule; no AI, provider or minute-data request",
	}, "\n")
}

func applyResearch2CapitalLedger(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, model := range []any{&research2.AccountCapitalEvent{}, &research2.AccountLedgerSnapshot{}} {
		if err := tx.AutoMigrate(model); err != nil {
			return fmt.Errorf("create research2 capital-ledger table for %T: %w", model, err)
		}
	}
	var existing int64
	if err := tx.Model(&research2.AccountCapitalEvent{}).Count(&existing).Error; err != nil {
		return err
	}
	if existing != 0 {
		return verifyMainSchema31Runtime(tx)
	}
	var accounts []research2.Account
	if err := tx.Order("slot ASC").Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) != len(research2.Slots()) {
		return fmt.Errorf("research2 capital rebase requires %d slot accounts, got %d", len(research2.Slots()), len(accounts))
	}
	if err := research2AssertNoDependentSells(tx); err != nil {
		return err
	}

	events, err := research2BuildCapitalEvents(tx)
	if err != nil {
		return err
	}
	plan, err := research2BuildRebasePlan(tx, events)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
			return fmt.Errorf("write research2 capital event %s: %w", event.EventID, err)
		}
	}
	if err := research2ApplyRebasePlan(tx, accounts, plan); err != nil {
		return err
	}
	if err := research2RebuildLedgerSnapshots(tx); err != nil {
		return err
	}
	return verifyMainSchema31Runtime(tx)
}

func research2Shanghai() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}

func research2CapitalEventID(parts ...string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock:research2:capital-ledger:v1:"+strings.Join(parts, ":"))).String()
}

func research2LedgerSnapshotID(parts ...string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("go-stock:research2:capital-ledger-snapshot:v1:"+strings.Join(parts, ":"))).String()
}

func research2CapitalEvent(slot, kind string, amount float64, external bool, source string, at time.Time, key string) research2.AccountCapitalEvent {
	local := at.In(research2Shanghai())
	return research2.AccountCapitalEvent{
		EventID: research2CapitalEventID(slot, key), Slot: slot, EventType: kind, Amount: amount,
		External: external, Source: source, EffectiveAt: at, TradingDate: local.Format("2006-01-02"),
	}
}

func research2RoundMoney(value float64) float64 { return math.Round(value*100) / 100 }

func research2BuildCapitalEvents(tx *gorm.DB) ([]research2.AccountCapitalEvent, error) {
	events := make([]research2.AccountCapitalEvent, 0, len(research2.Slots())*2+4)
	for _, slot := range research2.Slots() {
		events = append(events,
			research2CapitalEvent(slot, research2.CapitalEventInitial, research2CapitalInitialAmount, true, "user_initial_capital", research2CapitalInitialAt, "initial"),
			research2CapitalEvent(slot, research2.CapitalEventTopUp, research2CapitalInitialAmount, true, "user_top_up", research2CapitalTopUpAt, "top-up-20260921"),
		)
	}
	var trades []research2.Trade
	if err := tx.Order("traded_at ASC, trade_id ASC, id ASC").Find(&trades).Error; err != nil {
		return nil, err
	}
	sort.SliceStable(trades, func(i, j int) bool {
		if !trades[i].TradedAt.Equal(trades[j].TradedAt) {
			return trades[i].TradedAt.Before(trades[j].TradedAt)
		}
		return trades[i].TradeID < trades[j].TradeID
	})
	cash := make(map[string]float64, len(research2.Slots()))
	for _, slot := range research2.Slots() {
		cash[slot] = research2CapitalInitialAmount
	}
	for _, trade := range trades {
		if !research2.ValidSlot(trade.Slot) || trade.TradedAt.Before(research2CapitalInitialAt) || math.IsNaN(trade.NetCashFlow) || math.IsInf(trade.NetCashFlow, 0) {
			return nil, fmt.Errorf("invalid historical research2 trade %s", trade.TradeID)
		}
		if !trade.TradedAt.Before(research2CapitalTopUpAt) {
			continue
		}
		if trade.NetCashFlow < 0 && cash[trade.Slot]+trade.NetCashFlow < -research2CapitalRebaseEpsilon {
			shortfall := -(cash[trade.Slot] + trade.NetCashFlow)
			if shortfall <= 0 {
				return nil, fmt.Errorf("research2 capital rebase produced invalid transfer for trade %s", trade.TradeID)
			}
			// Preserve the trade's exchange timestamp while ordering this neutral
			// adjustment immediately before the buy in the deterministic replay.
			events = append(events, research2CapitalEvent(trade.Slot, research2.CapitalEventLegacyPoolTransfer, shortfall, false, "legacy_shared_pool", trade.TradedAt.Add(-time.Nanosecond), "legacy-transfer-"+trade.TradeID))
			cash[trade.Slot] += shortfall
		}
		cash[trade.Slot] += trade.NetCashFlow
		if cash[trade.Slot] < -research2CapitalRebaseEpsilon {
			return nil, fmt.Errorf("research2 historical replay overdraws slot %s at trade %s", trade.Slot, trade.TradeID)
		}
	}
	return events, nil
}

func research2AssertNoDependentSells(tx *gorm.DB) error {
	var count int64
	query := tx.Table("research2_trades AS sell").
		Joins("JOIN research2_recommendations AS r ON r.recommendation_id = sell.recommendation_id").
		Joins("JOIN research2_analysis_runs AS a ON a.run_id = r.analysis_run_id").
		Where("a.trading_date = ? AND sell.side = ?", research2CapitalRebaseDate, "sell")
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("research2 capital rebase refuses %d dependent sell trades for %s buys", count, research2CapitalRebaseDate)
	}
	return nil
}

type research2RebaseCandidate struct {
	recommendation research2.Recommendation
	chainID        string
	generatedAt    time.Time
	quoteAt        time.Time
	executeAt      time.Time
	quote          float64
	blocked        bool
	result         research2RebaseResult
}

type research2RebaseResult struct {
	status      string
	reason      string
	quantity    int64
	cost        trading.CostBreakdown
	tradeAt     time.Time
	bought      bool
	marketPrice float64
}

type research2RebasePlan struct {
	candidates  []*research2RebaseCandidate
	chainBases  map[string]float64
	chainFilled map[string]int
	cash        map[string]float64
}

type research2ReplayEvent struct {
	at        time.Time
	priority  int
	sequence  string
	slot      string
	kind      string
	amount    float64
	chainID   string
	candidate *research2RebaseCandidate
}

func research2BuildRebasePlan(tx *gorm.DB, events []research2.AccountCapitalEvent) (research2RebasePlan, error) {
	plan := research2RebasePlan{chainBases: map[string]float64{}, chainFilled: map[string]int{}, cash: map[string]float64{}}
	var chains []research2.ExecutionChain
	if err := tx.Where("trading_date = ? AND TRIM(winner_run_id) <> ''", research2CapitalRebaseDate).Order("scheduled_for ASC, chain_id ASC").Find(&chains).Error; err != nil {
		return plan, err
	}
	winnerIDs := make([]string, 0, len(chains))
	for _, chain := range chains {
		winnerIDs = append(winnerIDs, chain.WinnerRunID)
	}
	var runs []research2.AnalysisRun
	if err := tx.Where("run_id IN ?", winnerIDs).Find(&runs).Error; err != nil {
		return plan, err
	}
	runByID := make(map[string]research2.AnalysisRun, len(runs))
	for _, run := range runs {
		if run.GeneratedAt == nil || run.GeneratedAt.IsZero() {
			return plan, fmt.Errorf("research2 capital rebase winner %s has no generated time", run.RunID)
		}
		runByID[run.RunID] = run
	}
	for _, chain := range chains {
		run, ok := runByID[chain.WinnerRunID]
		if !ok || run.ChainID != chain.ChainID || run.Slot != chain.Slot || !run.Published {
			return plan, fmt.Errorf("research2 capital rebase chain %s has an invalid winner", chain.ChainID)
		}
	}
	var rows []research2.Recommendation
	if err := tx.Where("analysis_run_id IN ?", winnerIDs).Order("analysis_run_id ASC, final_score DESC, selection_rank ASC, id ASC").Find(&rows).Error; err != nil {
		return plan, err
	}
	targetIDs := make(map[string]bool, len(rows))
	lastExecutionAt := map[string]time.Time{}
	for _, row := range rows {
		targetIDs[row.RecommendationID] = true
		run, ok := runByID[row.AnalysisRunID]
		if !ok {
			return plan, fmt.Errorf("research2 capital rebase recommendation %s has no winner run", row.RecommendationID)
		}
		if row.Slot != run.Slot || row.SellAt != nil {
			return plan, fmt.Errorf("research2 rebase recommendation %s has inconsistent ownership or a dependent sale", row.RecommendationID)
		}
		landedAt := *run.GeneratedAt
		if run.PersistedAt != nil {
			landedAt = *run.PersistedAt
		}
		candidate := &research2RebaseCandidate{recommendation: row, chainID: run.ChainID, generatedAt: landedAt}
		candidate.quote = row.ExecutionQuotePrice
		if candidate.quote <= 0 {
			candidate.quote = row.BuyMarketPrice
		}
		if row.ExecutionQuoteAt != nil && !row.ExecutionQuoteAt.IsZero() {
			candidate.quoteAt = *row.ExecutionQuoteAt
		} else if row.BuyAt != nil {
			candidate.quoteAt = *row.BuyAt
		}
		candidate.blocked = research2BlockedExecutionFailure(row.ExecutionFailureCode)
		candidate.executeAt = landedAt
		if candidate.quoteAt.After(candidate.executeAt) {
			candidate.executeAt = candidate.quoteAt
		}
		// Quotes from the same collection batch may arrive out of rank order.
		// Preserve score priority, and never claim an execution before its quote.
		if previous := lastExecutionAt[run.ChainID]; previous.After(candidate.executeAt) {
			candidate.executeAt = previous
		}
		lastExecutionAt[run.ChainID] = candidate.executeAt
		plan.candidates = append(plan.candidates, candidate)
	}

	var trades []research2.Trade
	if err := tx.Order("traded_at ASC, trade_id ASC, id ASC").Find(&trades).Error; err != nil {
		return plan, err
	}
	replay := make([]research2ReplayEvent, 0, len(events)+len(trades)+len(chains)+len(plan.candidates))
	for _, event := range events {
		replay = append(replay, research2ReplayEvent{at: event.EffectiveAt, priority: 0, sequence: event.EventID, slot: event.Slot, kind: "capital", amount: event.Amount})
	}
	for _, trade := range trades {
		if targetIDs[trade.RecommendationID] && trade.Side == "buy" {
			continue
		}
		replay = append(replay, research2ReplayEvent{at: trade.TradedAt, priority: 1, sequence: trade.TradeID, slot: trade.Slot, kind: "trade", amount: trade.NetCashFlow})
	}
	for _, chain := range chains {
		run := runByID[chain.WinnerRunID]
		landedAt := *run.GeneratedAt
		if run.PersistedAt != nil {
			landedAt = *run.PersistedAt
		}
		replay = append(replay, research2ReplayEvent{at: landedAt, priority: 2, sequence: chain.ChainID, slot: chain.Slot, kind: "base", chainID: chain.ChainID})
	}
	for index, candidate := range plan.candidates {
		replay = append(replay, research2ReplayEvent{at: candidate.executeAt, priority: 3, sequence: fmt.Sprintf("%08d", index), slot: candidate.recommendation.Slot, kind: "candidate", candidate: candidate})
	}
	sort.SliceStable(replay, func(i, j int) bool {
		if !replay[i].at.Equal(replay[j].at) {
			return replay[i].at.Before(replay[j].at)
		}
		if replay[i].priority != replay[j].priority {
			return replay[i].priority < replay[j].priority
		}
		return replay[i].sequence < replay[j].sequence
	})
	for _, slot := range research2.Slots() {
		plan.cash[slot] = 0
	}
	for _, item := range replay {
		if !research2.ValidSlot(item.slot) {
			continue
		}
		switch item.kind {
		case "capital", "trade":
			plan.cash[item.slot] += item.amount
		case "base":
			if plan.cash[item.slot] < -research2CapitalRebaseEpsilon {
				return plan, fmt.Errorf("research2 capital rebase base for %s is negative: %.2f", item.slot, plan.cash[item.slot])
			}
			plan.chainBases[item.chainID] = plan.cash[item.slot]
		case "candidate":
			research2PlanCandidateBuy(&plan, item.candidate)
		}
		if plan.cash[item.slot] < -research2CapitalRebaseEpsilon {
			return plan, fmt.Errorf("research2 capital rebase overdraws slot %s at %s", item.slot, item.sequence)
		}
	}
	for _, chain := range chains {
		if _, ok := plan.chainBases[chain.ChainID]; !ok {
			return plan, fmt.Errorf("research2 capital rebase missed chain base %s", chain.ChainID)
		}
	}
	return plan, nil
}

func research2BlockedExecutionFailure(code string) bool {
	switch strings.TrimSpace(code) {
	case "suspended", "limit_up", "limit_down", "invalid_price", "near_limit_up", "quote_retry":
		return true
	default:
		return false
	}
}

func research2PlanCandidateBuy(plan *research2RebasePlan, candidate *research2RebaseCandidate) {
	item := candidate.recommendation
	if !research2RebaseEligible(item) {
		candidate.result = research2RebaseResult{status: "analysis_only", reason: item.FailureReason, tradeAt: candidate.quoteAt, marketPrice: candidate.quote}
		return
	}
	if candidate.blocked {
		candidate.result = research2RebaseResult{status: "missed_untradable", reason: item.FailureReason, tradeAt: candidate.quoteAt, marketPrice: candidate.quote}
		return
	}
	if candidate.quote <= 0 || math.IsNaN(candidate.quote) || math.IsInf(candidate.quote, 0) || candidate.quoteAt.IsZero() {
		candidate.result = research2RebaseResult{status: "analysis_only", reason: "历史执行报价不可用，仅保留分析", tradeAt: candidate.quoteAt}
		return
	}
	if candidate.quoteAt.Before(candidate.generatedAt.Truncate(time.Second)) || candidate.executeAt.In(research2Shanghai()).Format("2006-01-02") != research2CapitalRebaseDate || candidate.executeAt.In(research2Shanghai()).Hour()*60+candidate.executeAt.Minute() >= 690 {
		candidate.result = research2RebaseResult{status: "analysis_only", reason: "历史执行报价不在有效买入窗口，仅保留分析"}
		return
	}
	if plan.chainFilled[candidate.chainID] >= 5 {
		candidate.result = research2RebaseResult{status: "analysis_only", reason: "本区间当日已完成五笔买入，剩余评分仅保留分析", tradeAt: candidate.quoteAt, marketPrice: candidate.quote}
		return
	}
	base, ok := plan.chainBases[candidate.chainID]
	if !ok || base <= 0 {
		candidate.result = research2RebaseResult{status: "missed_cash", reason: "本区间启动资金不可用", tradeAt: candidate.quoteAt, marketPrice: candidate.quote}
		return
	}
	quantity, cost, err := research2.SizeFixedAllocationBuy(item.StockCode, candidate.quote, plan.cash[item.Slot], base)
	if err != nil {
		lot, lotErr := trading.LotSize(item.StockCode)
		lotCost := 0.0
		if lotErr == nil {
			lotCost = -trading.CalculateBuyCost(candidate.quote, lot).NetCashFlow
		}
		candidate.result = research2RebaseResult{status: "missed_cash", reason: fmt.Sprintf("剩余现金%.2f元不足支付一手含费成本%.2f元", plan.cash[item.Slot], lotCost), tradeAt: candidate.quoteAt, marketPrice: candidate.quote}
		return
	}
	plan.cash[item.Slot] += cost.NetCashFlow
	plan.chainFilled[candidate.chainID]++
	candidate.result = research2RebaseResult{status: "active", quantity: quantity, cost: cost, tradeAt: candidate.executeAt, bought: true, marketPrice: candidate.quote}
}

// Current winner rows have no score threshold. A stored accepted execution
// quote also makes an unused row eligible when the replay frees a seat.
func research2RebaseEligible(item research2.Recommendation) bool {
	switch item.Status {
	case "active", "buy_pending", "standby", "standby_not_used", "missed_cash", "missed_untradable":
		return true
	case "analysis_only":
		// Winner rows created under the current policy have no score gate;
		// an accepted stored quote proves they were in the execution roster.
		return item.ExecutionFailureCode == "" && item.ExecutionQuotePrice > 0 && item.ExecutionQuoteAt != nil
	default:
		return false
	}
}

func research2ApplyRebasePlan(tx *gorm.DB, accounts []research2.Account, plan research2RebasePlan) error {
	chainIDs := make([]string, 0, len(plan.chainBases))
	for chainID := range plan.chainBases {
		chainIDs = append(chainIDs, chainID)
	}
	if len(chainIDs) != 0 {
		runIDs := make([]string, 0, len(plan.candidates))
		seenRuns := map[string]bool{}
		for _, candidate := range plan.candidates {
			if !seenRuns[candidate.recommendation.AnalysisRunID] {
				seenRuns[candidate.recommendation.AnalysisRunID] = true
				runIDs = append(runIDs, candidate.recommendation.AnalysisRunID)
			}
		}
		if err := tx.Where("recommendation_id IN (?) AND side = ?", tx.Model(&research2.Recommendation{}).Select("recommendation_id").Where("analysis_run_id IN ?", runIDs), "buy").Delete(&research2.Trade{}).Error; err != nil {
			return fmt.Errorf("remove superseded %s buys: %w", research2CapitalRebaseDate, err)
		}
	}

	filled := make(map[string]int, len(plan.chainFilled))
	for _, candidate := range plan.candidates {
		item := candidate.recommendation
		result := candidate.result
		updates := map[string]any{
			"status": result.status, "failure_reason": result.reason,
			"buy_at": nil, "buy_market_price": 0, "buy_price": 0, "quantity": 0, "buy_fees": 0,
			"sell_at": nil, "sell_market_price": 0, "sell_price": 0, "sell_fees": 0, "target_sell_at": nil,
			"net_pn_l": 0, "net_yield_rate": 0, "hit_five_before_sell": nil, "hit_limit_up_full_day": nil,
			"hit_minus_three": nil, "metrics_finalized": false,
		}
		if result.bought {
			targetSellAt := item.TargetSellAt
			if targetSellAt == nil || targetSellAt.IsZero() {
				at := research2NextSlotSellAt(result.tradeAt, item.Slot)
				targetSellAt = &at
			}
			currentPrice, currentAt := item.CurrentPrice, item.CurrentPriceAt
			if currentPrice <= 0 {
				currentPrice = result.marketPrice
				currentAt = &result.tradeAt
			}
			updates["status"] = "active"
			updates["failure_reason"] = ""
			updates["execution_failure_code"] = ""
			updates["buy_at"] = result.tradeAt
			updates["buy_market_price"] = result.marketPrice
			updates["buy_price"] = result.cost.ExecutionPrice
			updates["quantity"] = result.quantity
			updates["buy_fees"] = result.cost.Commission + result.cost.TransferFee
			updates["current_price"] = currentPrice
			updates["current_price_at"] = currentAt
			updates["target_sell_at"] = targetSellAt
			trade := research2.Trade{
				TradeID: research2CapitalEventID("rebase-buy", item.RecommendationID), Slot: item.Slot, RecommendationID: item.RecommendationID,
				Side: "buy", TradedAt: result.tradeAt, MarketPrice: result.marketPrice, ExecutionPrice: result.cost.ExecutionPrice,
				Quantity: result.quantity, Commission: result.cost.Commission, TransferFee: result.cost.TransferFee,
				SlippageAmount: result.cost.SlippageAmount, NetCashFlow: result.cost.NetCashFlow,
				PriceSource: "capital_rebase_stored_execution_quote", ExecutionMode: "capital_rebase_fixed_fifth", QuoteAt: &candidate.quoteAt,
			}
			if err := tx.Create(&trade).Error; err != nil {
				return fmt.Errorf("write recalculated buy %s: %w", item.RecommendationID, err)
			}
			filled[candidate.chainID]++
		}
		if err := tx.Model(&research2.Recommendation{}).Where("recommendation_id = ?", item.RecommendationID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update recalculated recommendation %s: %w", item.RecommendationID, err)
		}
	}
	for chainID, base := range plan.chainBases {
		count := filled[chainID]
		reason := "当日有效报告的固定五分之一仓位重算完成"
		if count >= 5 {
			reason = "按固定五分之一仓位重算后已完成五笔买入"
		}
		if err := tx.Model(&research2.ExecutionChain{}).Where("chain_id = ?", chainID).Updates(map[string]any{
			"allocation_base_cash": base, "filled_slots": count, "status": "completed", "stop_reason": reason,
		}).Error; err != nil {
			return fmt.Errorf("update recalculated chain %s: %w", chainID, err)
		}
	}
	for _, account := range accounts {
		cash := plan.cash[account.Slot]
		if cash < -research2CapitalRebaseEpsilon {
			return fmt.Errorf("recalculated account %s is overdrawn: %.2f", account.Slot, cash)
		}
		if err := tx.Model(&research2.Account{}).Where("slot = ?", account.Slot).Updates(map[string]any{"initial_cash": research2CapitalInitialAmount, "cash": cash}).Error; err != nil {
			return fmt.Errorf("update recalculated account %s: %w", account.Slot, err)
		}
	}
	return nil
}

func research2NextSlotSellAt(at time.Time, slot string) time.Time {
	local := at.In(research2Shanghai()).AddDate(0, 0, 1)
	var hour, minute int
	_, _ = fmt.Sscanf(slot, "%d:%d", &hour, &minute)
	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, research2Shanghai())
}

func research2RebuildLedgerSnapshots(tx *gorm.DB) error {
	if err := tx.Where("valuation_basis = ?", research2.CapitalValuationBasisLedger).Delete(&research2.AccountLedgerSnapshot{}).Error; err != nil {
		return err
	}
	var events []research2.AccountCapitalEvent
	if err := tx.Order("effective_at ASC, event_id ASC, id ASC").Find(&events).Error; err != nil {
		return err
	}
	var trades []research2.Trade
	if err := tx.Order("traded_at ASC, trade_id ASC, id ASC").Find(&trades).Error; err != nil {
		return err
	}
	type event struct {
		at       time.Time
		priority int
		key      string
		slot     string
		capital  *research2.AccountCapitalEvent
		trade    *research2.Trade
	}
	items := make([]event, 0, len(events)+len(trades))
	for index := range events {
		items = append(items, event{at: events[index].EffectiveAt, priority: 0, key: events[index].EventID, slot: events[index].Slot, capital: &events[index]})
	}
	for index := range trades {
		items = append(items, event{at: trades[index].TradedAt, priority: 1, key: trades[index].TradeID, slot: trades[index].Slot, trade: &trades[index]})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].at.Equal(items[j].at) {
			return items[i].at.Before(items[j].at)
		}
		if items[i].priority != items[j].priority {
			return items[i].priority < items[j].priority
		}
		return items[i].key < items[j].key
	})
	cash, external, transfer := map[string]float64{}, map[string]float64{}, map[string]float64{}
	positions := map[string]map[string]research2.Trade{}
	for _, item := range items {
		if !research2.ValidSlot(item.slot) {
			continue
		}
		kind := "trade"
		if item.capital != nil {
			kind = item.capital.EventType
			cash[item.slot] += item.capital.Amount
			if item.capital.External {
				external[item.slot] += item.capital.Amount
			} else {
				transfer[item.slot] += item.capital.Amount
			}
		} else if item.trade != nil {
			cash[item.slot] += item.trade.NetCashFlow
			if positions[item.slot] == nil {
				positions[item.slot] = map[string]research2.Trade{}
			}
			if item.trade.Side == "buy" {
				positions[item.slot][item.trade.RecommendationID] = *item.trade
			} else if item.trade.Side == "sell" {
				delete(positions[item.slot], item.trade.RecommendationID)
			}
		}
		if cash[item.slot] < -research2CapitalRebaseEpsilon {
			return fmt.Errorf("research2 ledger snapshot replay overdraws slot %s at %s", item.slot, item.key)
		}
		positionValue := 0.0
		for _, holding := range positions[item.slot] {
			if holding.Quantity > 0 && holding.MarketPrice > 0 {
				positionValue += trading.CalculateSellCost(holding.MarketPrice, holding.Quantity).NetCashFlow
			}
		}
		nav := cash[item.slot] + positionValue
		profit := nav - external[item.slot] - transfer[item.slot]
		rate := 0.0
		if external[item.slot] > 0 {
			rate = profit / external[item.slot]
		}
		local := item.at.In(research2Shanghai())
		snapshot := research2.AccountLedgerSnapshot{
			SnapshotID: research2LedgerSnapshotID(item.slot, kind, item.key), Slot: item.slot, ValuedAt: item.at,
			TradingDate: local.Format("2006-01-02"), SnapshotType: kind, Cash: research2RoundMoney(cash[item.slot]),
			PositionValue: research2RoundMoney(positionValue), NetAssetValue: research2RoundMoney(nav),
			CumulativeExternalCapital: research2RoundMoney(external[item.slot]), NetInternalTransfer: research2RoundMoney(transfer[item.slot]),
			NetProfit: research2RoundMoney(profit), CumulativeCapitalReturn: rate, ValuationBasis: research2.CapitalValuationBasisLedger,
		}
		if err := tx.Create(&snapshot).Error; err != nil {
			return fmt.Errorf("write capital-ledger snapshot %s: %w", snapshot.SnapshotID, err)
		}
	}
	return nil
}

func verifyMainSchema31Runtime(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, model := range []any{&research2.AccountCapitalEvent{}, &research2.AccountLedgerSnapshot{}} {
		if !tx.Migrator().HasTable(model) {
			return fmt.Errorf("main schema 31 table for %T is missing", model)
		}
	}
	var accounts []research2.Account
	if err := tx.Order("slot ASC").Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) != len(research2.Slots()) {
		return fmt.Errorf("main schema 31 has %d research2 accounts, want %d", len(accounts), len(research2.Slots()))
	}
	for _, account := range accounts {
		var external, transfer float64
		if err := tx.Model(&research2.AccountCapitalEvent{}).Where("slot = ? AND external = ?", account.Slot, true).Select("COALESCE(SUM(amount), 0)").Scan(&external).Error; err != nil {
			return err
		}
		if err := tx.Model(&research2.AccountCapitalEvent{}).Where("slot = ? AND external = ?", account.Slot, false).Select("COALESCE(SUM(amount), 0)").Scan(&transfer).Error; err != nil {
			return err
		}
		for _, required := range []research2.AccountCapitalEvent{
			research2CapitalEvent(account.Slot, research2.CapitalEventInitial, research2CapitalInitialAmount, true, "user_initial_capital", research2CapitalInitialAt, "initial"),
			research2CapitalEvent(account.Slot, research2.CapitalEventTopUp, research2CapitalInitialAmount, true, "user_top_up", research2CapitalTopUpAt, "top-up-20260921"),
		} {
			var stored research2.AccountCapitalEvent
			if err := tx.Where("event_id = ?", required.EventID).First(&stored).Error; err != nil {
				return fmt.Errorf("main schema 31 slot %s required capital event %s is unavailable: %w", account.Slot, required.EventID, err)
			}
			if !research2CapitalEventMatches(stored, required) {
				return fmt.Errorf("main schema 31 slot %s required capital event %s conflicts", account.Slot, required.EventID)
			}
		}
		if external < 2*research2CapitalInitialAmount-research2CapitalRebaseEpsilon {
			return fmt.Errorf("main schema 31 slot %s external capital is %.2f", account.Slot, external)
		}
		if math.Abs(account.InitialCash-research2CapitalInitialAmount) > 0.01 || account.Cash < -research2CapitalRebaseEpsilon {
			return fmt.Errorf("main schema 31 slot %s account balance is invalid", account.Slot)
		}
		var tradeFlow float64
		if err := tx.Model(&research2.Trade{}).Where("slot = ?", account.Slot).Select("COALESCE(SUM(net_cash_flow), 0)").Scan(&tradeFlow).Error; err != nil {
			return err
		}
		if math.Abs(account.Cash-(external+transfer+tradeFlow)) > 0.01 {
			return fmt.Errorf("main schema 31 slot %s cash %.2f does not match capital/trade replay %.2f", account.Slot, account.Cash, external+transfer+tradeFlow)
		}
		var count int64
		if err := tx.Model(&research2.AccountLedgerSnapshot{}).Where("slot = ? AND valuation_basis = ?", account.Slot, research2.CapitalValuationBasisLedger).Count(&count).Error; err != nil {
			return err
		}
		if count < 2 {
			return fmt.Errorf("main schema 31 slot %s has %d derived ledger snapshots", account.Slot, count)
		}
	}
	return nil
}
