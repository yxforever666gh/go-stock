package research2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"
)

type allocationReplayFixtureHistory struct {
	rows map[string]BuyDayMarketData
}

func (fixture allocationReplayFixtureHistory) BuyDayData(_ context.Context, item Recommendation) (BuyDayMarketData, error) {
	if item.BuyAt == nil {
		return BuyDayMarketData{}, errors.New("missing fixture time")
	}
	key := item.StockCode + "|" + item.BuyAt.In(shanghai()).Format("2006-01-02")
	row, ok := fixture.rows[key]
	if !ok {
		return BuyDayMarketData{}, errors.New("missing fixture session " + key)
	}
	return row, nil
}

func (allocationReplayFixtureHistory) DailyCloses(context.Context, string, time.Time, time.Time) ([]DailyClose, string, error) {
	return nil, "[]", errors.New("not used by replay plan fixture")
}

func replayFixtureSession(at time.Time, price float64) BuyDayMarketData {
	return BuyDayMarketData{PreviousClose: price, LimitRate: 0.10, Bars: []PerformanceBar{{
		At: at, Open: price, High: price, Low: price, Close: price, Volume: 100, Amount: price * 100, Source: "fixture-unadjusted",
	}}}
}

func TestAllocationReplayUsesLegacyThreeSlotsAndBlocksMissingSell(t *testing.T) {
	repository := research2TestRepository(t)
	if err := repository.db.AutoMigrate(&AccountCapitalEvent{}, &AccountLedgerSnapshot{}, &AccountDailyValuation{}, &AllocationReplay{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialAt := time.Date(2026, 8, 27, 9, 30, 0, 0, shanghai())
	topUpAt := time.Date(2026, 9, 21, 9, 25, 0, 0, shanghai())
	for _, slot := range Slots() {
		events := []AccountCapitalEvent{
			{EventID: "initial-" + slot, Slot: slot, EventType: CapitalEventInitial, Amount: 10000, External: true, Source: "fixture", EffectiveAt: initialAt, TradingDate: "2026-08-27"},
			{EventID: "topup-" + slot, Slot: slot, EventType: CapitalEventTopUp, Amount: 10000, External: true, Source: "fixture", EffectiveAt: topUpAt, TradingDate: "2026-09-21"},
		}
		if err := repository.db.Create(&events).Error; err != nil {
			t.Fatal(err)
		}
	}
	legacyTransfer := AccountCapitalEvent{EventID: "legacy-transfer", Slot: "10:00", EventType: CapitalEventLegacyPoolTransfer, Amount: 5000, External: false, Source: "legacy_shared_pool", EffectiveAt: initialAt, TradingDate: "2026-08-27"}
	if err := repository.db.Create(&legacyTransfer).Error; err != nil {
		t.Fatal(err)
	}
	signal := time.Date(2026, 9, 18, 10, 0, 30, 0, shanghai())
	run := AnalysisRun{RunID: "replay-run", TradingDate: "2026-09-18", ScheduledSlot: "10:00", Slot: "10:00", Published: true, AttemptNo: 1, ScheduledFor: signal, StartedAt: signal, EvidenceCutoffAt: signal, GeneratedAt: &signal, Status: "success", StrategyVersion: "research2-trailing5-v10", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	if err := repository.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	chain := ExecutionChain{ChainID: "replay-chain", TradingDate: run.TradingDate, Slot: run.Slot, WinnerRunID: run.RunID, ScheduledFor: signal, StartedAt: signal, Status: "completed", TargetSlots: 3, AllocationPolicy: AllocationPolicyLegacyRecorded}
	if err := repository.db.Create(&chain).Error; err != nil {
		t.Fatal(err)
	}
	prices := []float64{60, 10, 100, 5}
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004"}
	items := make([]Recommendation, 0, len(codes))
	for index, code := range codes {
		items = append(items, Recommendation{RecommendationID: fmt.Sprintf("replay-rec-%d", index+1), AnalysisRunID: run.RunID, Slot: run.Slot, StockCode: code, StockName: code, SignalAt: signal, TargetBuyAt: signal, FinalScore: float64(90 - index), SelectionRank: index + 1, Status: "analysis_only"})
	}
	if err := repository.db.Create(&items).Error; err != nil {
		t.Fatal(err)
	}
	buyMinute := time.Date(2026, 9, 18, 10, 1, 0, 0, shanghai())
	sellMinute := time.Date(2026, 9, 21, 10, 0, 0, 0, shanghai())
	history := allocationReplayFixtureHistory{rows: map[string]BuyDayMarketData{}}
	for index, code := range codes {
		history.rows[code+"|2026-09-18"] = replayFixtureSession(buyMinute, prices[index])
	}
	history.rows[codes[0]+"|2026-09-21"] = replayFixtureSession(sellMinute, 61)
	// code 2 deliberately has no sell session and must remain blocked/open.
	history.rows[codes[3]+"|2026-09-21"] = replayFixtureSession(sellMinute, 5.5)
	service := NewAllocationReplayService(repository, history, testCalendar{})
	service.now = func() time.Time { return time.Date(2026, 9, 22, 16, 0, 0, 0, shanghai()) }
	plan, err := service.buildPlan(ctx, service.now())
	if err != nil {
		t.Fatal(err)
	}
	result := allocationReplayResultFromPlan(plan, true)
	if result.BuyCount != 3 || result.SellCount != 2 || result.MissingSellCount != 1 || result.MissingBuyCount != 0 {
		t.Fatalf("unexpected plan result: %+v", result)
	}
	states := map[string]*allocationReplayState{}
	for _, state := range plan.states {
		states[state.item.RecommendationID] = state
	}
	if states["replay-rec-1"].buyTrade.Quantity != 100 || states["replay-rec-2"].buyTrade.Quantity != 100 || states["replay-rec-3"].status != "missed_cash" || states["replay-rec-4"].buyTrade.Quantity != 500 {
		t.Fatalf("unexpected replay sizing: one=%+v two=%+v three=%+v four=%+v", states["replay-rec-1"], states["replay-rec-2"], states["replay-rec-3"], states["replay-rec-4"])
	}
	for _, trade := range plan.trades {
		if trade.ExecutionPrice != trade.MarketPrice || trade.SlippageAmount != 0 {
			t.Fatalf("replayed trade retained slippage: %+v", trade)
		}
	}
	if !states["replay-rec-2"].historicalBlocked || states["replay-rec-2"].status != "sell_pending" {
		t.Fatalf("missing sell was not blocked: %+v", states["replay-rec-2"])
	}
	if _, err = service.applyPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	var stored Recommendation
	if err = repository.db.Where("recommendation_id = ?", "replay-rec-2").First(&stored).Error; err != nil || !stored.HistoricalSellBlocked || stored.Status != "sell_pending" || stored.BuyAt == nil || stored.TargetSellAt == nil || !stored.TargetSellAt.Equal(sellMinute) {
		t.Fatalf("stored blocked position=%+v err=%v", stored, err)
	}
	var transferCount int64
	if err = repository.db.Model(&AccountCapitalEvent{}).Where("external = ?", false).Count(&transferCount).Error; err != nil || transferCount != 0 {
		t.Fatalf("legacy transfers=%d err=%v", transferCount, err)
	}
	var storedChain ExecutionChain
	if err = repository.db.Where("chain_id = ?", chain.ChainID).First(&storedChain).Error; err != nil || storedChain.TargetSlots != 3 || storedChain.FilledSlots != 3 || storedChain.AllocationPolicy != AllocationPolicyRemainingCashSlots {
		t.Fatalf("stored chain=%+v err=%v", storedChain, err)
	}
	var account Account
	if err = repository.db.Where("slot = ?", run.Slot).First(&account).Error; err != nil || account.Cash < 0 || math.Abs(account.Cash-plan.accountCash[run.Slot]) > 1e-7 {
		t.Fatalf("account=%+v planned=%f err=%v", account, plan.accountCash[run.Slot], err)
	}
	reused, err := service.applyPlan(ctx, plan)
	if err != nil || !reused {
		t.Fatalf("idempotent replay reused=%t err=%v", reused, err)
	}
}

func TestAllocationReplayHashIncludesFeeEconomics(t *testing.T) {
	plan := allocationReplayPlan{trades: []Trade{{TradeID: "buy", Side: "buy", MarketPrice: 10, ExecutionPrice: 10, Quantity: 100, Commission: 5, TransferFee: 0.01, NetCashFlow: -1005.01}}}
	first, err := allocationReplayPlanHash(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan.trades[0].TransferFee = 0
	plan.trades[0].NetCashFlow = -1005
	second, err := allocationReplayPlanHash(plan, nil)
	if err != nil || second == first {
		t.Fatalf("fee change must change replay identity: first=%s second=%s err=%v", first, second, err)
	}
}

func TestAllocationReplayAcceptsThreeMatchingCapitalEventsAndRejectsDrift(t *testing.T) {
	initialAt := time.Date(2026, 8, 27, 9, 30, 0, 0, shanghai())
	firstTopUp := time.Date(2026, 9, 21, 9, 25, 0, 0, shanghai())
	secondTopUp := time.Date(2026, 9, 23, 9, 25, 0, 0, shanghai())
	eventsBySlot := make(map[string][]AccountCapitalEvent, len(Slots()))
	for _, slot := range Slots() {
		eventsBySlot[slot] = []AccountCapitalEvent{
			{EventID: "initial-" + slot, Slot: slot, EventType: CapitalEventInitial, Amount: 10000, External: true, Source: "user_initial_capital", EffectiveAt: initialAt, TradingDate: "2026-08-27"},
			{EventID: "topup-1-" + slot, Slot: slot, EventType: CapitalEventTopUp, Amount: 10000, External: true, Source: "user_top_up", EffectiveAt: firstTopUp, TradingDate: "2026-09-21"},
			{EventID: "topup-2-" + slot, Slot: slot, EventType: CapitalEventTopUp, Amount: 10000, External: true, Source: "user_top_up", EffectiveAt: secondTopUp, TradingDate: "2026-09-23"},
		}
	}
	total, err := validateAllocationReplayCapital(eventsBySlot)
	if err != nil || math.Abs(total-30000) > 0.01 {
		t.Fatalf("three-event capital timeline total=%f err=%v", total, err)
	}
	drifted := append([]AccountCapitalEvent(nil), eventsBySlot["11:25"]...)
	drifted[2].Amount = 9999
	eventsBySlot["11:25"] = drifted
	if _, err := validateAllocationReplayCapital(eventsBySlot); err == nil {
		t.Fatal("allocation replay accepted mismatched slot capital")
	}
}

func TestAllocationReplayUsesAuditedStoredSellWhenTargetMinuteIsMissing(t *testing.T) {
	target := time.Date(2026, 9, 23, 9, 30, 0, 0, shanghai())
	quoteAt := target.Add(-20 * time.Second)
	item := Recommendation{RecommendationID: "stored-sell", StockCode: "sh600000"}
	planner := allocationReplayPlanner{storedSells: map[string]Trade{
		item.RecommendationID: {
			RecommendationID: item.RecommendationID, Side: "sell", TradedAt: target.Add(300 * time.Millisecond),
			MarketPrice: 10.2, ExecutionPrice: 10.1898, Quantity: 100, PriceSource: "tencent_realtime", QuoteAt: &quoteAt,
		},
	}}
	quote, ok := planner.storedSellQuote(item, target)
	if !ok || quote.marketPrice != 10.2 || !quote.quoteAt.Equal(quoteAt) || quote.source != "stored_trade:tencent_realtime" || !quote.at.Equal(target) {
		t.Fatalf("stored sell quote=%+v ok=%t", quote, ok)
	}
	if _, ok := planner.storedSellQuote(item, target.Add(time.Minute)); ok {
		t.Fatal("stored sell quote matched a different target minute")
	}
}
