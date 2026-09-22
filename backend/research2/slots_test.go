package research2

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go-stock/internal/trading"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func slotRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "slots.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sql.Close() })
	if err = db.AutoMigrate(&AnalysisRun{}, &ExecutionChain{}, &Recommendation{}, &Trade{}, &Account{}, &AccountSnapshot{}); err != nil {
		t.Fatal(err)
	}
	r := NewRepository(db)
	if err = r.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}
func slotClock(hour, minute int) time.Time {
	return time.Date(2026, 9, 18, hour, minute, 0, 0, shanghai())
}

func TestSlotBoundaries(t *testing.T) {
	if len(Slots()) != 24 || Slots()[0] != "09:30" || Slots()[23] != "11:25" {
		t.Fatal(Slots())
	}
	for _, test := range []struct {
		h, m int
		want string
	}{{9, 29, ""}, {9, 30, "09:30"}, {9, 34, "09:30"}, {9, 35, "09:35"}, {11, 29, "11:25"}, {11, 30, ""}} {
		if got := SlotAt(slotClock(test.h, test.m)); got != test.want {
			t.Fatalf("%v: %s", test, got)
		}
	}
	if OpeningMinuteMinimum(slotClock(9, 30)) != 0 || OpeningMinuteMinimum(slotClock(9, 35)) != 4 {
		t.Fatal("opening evidence policy")
	}
}

func TestSlotStatusesDescribeReportTimelinessAndBuying(t *testing.T) {
	r := slotRepository(t)
	ctx := context.Background()
	at := slotClock(10, 0)
	date := at.Format("2006-01-02")
	completedAt := slotClock(9, 45)
	runs := []AnalysisRun{
		{RunID: "on-time", TradingDate: date, ScheduledSlot: "09:30", Slot: "09:30", Published: true, ChainID: "chain-on-time", AttemptNo: 1, ScheduledFor: slotClock(9, 30), StartedAt: slotClock(9, 30), EvidenceCutoffAt: slotClock(9, 30), GeneratedAt: &at, Status: "success", OnTime: true},
		{RunID: "late", TradingDate: date, ScheduledSlot: "09:35", Slot: "09:40", Published: true, ChainID: "chain-late", AttemptNo: 1, ScheduledFor: slotClock(9, 35), StartedAt: slotClock(9, 35), EvidenceCutoffAt: slotClock(9, 35), GeneratedAt: &at, Status: "success", OnTime: false},
		{RunID: "empty", TradingDate: date, ScheduledSlot: "09:45", Slot: "09:45", Published: true, ChainID: "chain-empty", AttemptNo: 1, ScheduledFor: slotClock(9, 45), StartedAt: slotClock(9, 45), EvidenceCutoffAt: slotClock(9, 45), GeneratedAt: &at, Status: "no_recommendation", OnTime: true},
	}
	if err := r.db.WithContext(ctx).Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	chains := []ExecutionChain{
		{ChainID: "chain-on-time", Slot: "09:30", TradingDate: date, WinnerRunID: "on-time", Status: "running", TargetSlots: 5, FilledSlots: 2, ScheduledFor: slotClock(9, 30), StartedAt: slotClock(9, 30)},
		{ChainID: "chain-late", Slot: "09:40", TradingDate: date, WinnerRunID: "late", Status: "completed", TargetSlots: 5, FilledSlots: 3, ScheduledFor: slotClock(9, 40), StartedAt: slotClock(9, 40), SellCompletedAt: &completedAt},
		{ChainID: "chain-empty", Slot: "09:45", TradingDate: date, WinnerRunID: "empty", Status: "completed", TargetSlots: 5, ScheduledFor: slotClock(9, 45), StartedAt: slotClock(9, 45)},
		{ChainID: "chain-cutoff", Slot: "09:50", TradingDate: date, Status: "cutoff", TargetSlots: 5, ScheduledFor: slotClock(9, 50), StartedAt: slotClock(9, 50), StopReason: "上午11:30买入窗口截止"},
	}
	if err := r.db.WithContext(ctx).Create(&chains).Error; err != nil {
		t.Fatal(err)
	}
	recommendations := []Recommendation{
		{RecommendationID: "on-active-one", AnalysisRunID: "on-time", Slot: "09:30", StockCode: "sh600001", StockName: "one", SignalAt: at, TargetBuyAt: at, Status: "active"},
		{RecommendationID: "on-active-two", AnalysisRunID: "on-time", Slot: "09:30", StockCode: "sh600002", StockName: "two", SignalAt: at, TargetBuyAt: at, Status: "active"},
		{RecommendationID: "on-pending", AnalysisRunID: "on-time", Slot: "09:30", StockCode: "sh600003", StockName: "pending", SignalAt: at, TargetBuyAt: at, Status: "buy_pending"},
		{RecommendationID: "late-active-one", AnalysisRunID: "late", Slot: "09:40", StockCode: "sh600004", StockName: "one", SignalAt: at, TargetBuyAt: at, Status: "active"},
		{RecommendationID: "late-active-two", AnalysisRunID: "late", Slot: "09:40", StockCode: "sh600005", StockName: "two", SignalAt: at, TargetBuyAt: at, Status: "active"},
		{RecommendationID: "late-active-three", AnalysisRunID: "late", Slot: "09:40", StockCode: "sh600006", StockName: "three", SignalAt: at, TargetBuyAt: at, Status: "active"},
	}
	if err := r.CreateRecommendations(ctx, recommendations); err != nil {
		t.Fatal(err)
	}

	states, err := r.SlotStatuses(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	bySlot := make(map[string]SlotStatus, len(states))
	for _, state := range states {
		bySlot[state.Slot] = state
	}
	onTime := bySlot["09:30"]
	if onTime.ReportStatus != "success" || onTime.ReportOnTime == nil || !*onTime.ReportOnTime || onTime.BuyStatus != "awaiting_quote" || onTime.BoughtCount != 2 || onTime.BuyTargetCount != 5 || onTime.PendingBuyCount != 1 || onTime.OpenPositionCount != 2 {
		t.Fatalf("on-time state = %+v", onTime)
	}
	late := bySlot["09:40"]
	if late.ReportStatus != "success" || late.ReportOnTime == nil || *late.ReportOnTime || late.BuyStatus != "bought_partial" || late.BoughtCount != 3 || late.BuyTargetCount != 5 || late.PendingBuyCount != 0 || late.OpenPositionCount != 3 || late.SellCompletedAt == nil {
		t.Fatalf("late state = %+v", late)
	}
	empty := bySlot["09:45"]
	if empty.ReportStatus != "no_recommendation" || empty.ReportOnTime == nil || !*empty.ReportOnTime || empty.BuyStatus != "no_recommendation" {
		t.Fatalf("empty state = %+v", empty)
	}
	cutoff := bySlot["09:50"]
	if cutoff.ReportStatus != "cutoff" || cutoff.ReportOnTime != nil || cutoff.BuyStatus != "cutoff" || cutoff.StopReason == "" {
		t.Fatalf("cutoff state = %+v", cutoff)
	}
}

func TestSlotConcurrentTaskClaimsAndFirstPublication(t *testing.T) {
	r := slotRepository(t)
	ctx := context.Background()
	now := slotClock(9, 36)
	r.now = func() time.Time { return now }
	runs := []AnalysisRun{{RunID: "early", TradingDate: "2026-09-18", ScheduledSlot: "09:30", ScheduledFor: slotClock(9, 30), StartedAt: slotClock(9, 30), Status: "running"}, {RunID: "later", TradingDate: "2026-09-18", ScheduledSlot: "09:35", ScheduledFor: slotClock(9, 35), StartedAt: slotClock(9, 35), Status: "running"}}
	for i := range runs {
		run, created, err := r.CreateRunAttempt(ctx, &runs[i], true)
		if err != nil || !created {
			t.Fatalf("claim %v %t", err, created)
		}
		runs[i] = run
		runs[i].Status = "success"
	}
	duplicate := runs[0]
	duplicate.RunID = "duplicate"
	if _, created, err := r.CreateRunAttempt(ctx, &duplicate, true); err != nil || created {
		t.Fatalf("duplicate claim %v %t", err, created)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			items := []Recommendation{{RecommendationID: runs[i].RunID + "-stock", AnalysisRunID: runs[i].RunID, StockCode: "sh600000", StockName: "test", FinalScore: 70, ReferencePrice: 10}}
			errs <- r.FinalizeRun(ctx, &runs[i], items, func() string { return "saved report" })
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for _, run := range runs {
		if run.Slot != "09:35" {
			t.Fatal(run)
		}
		if run.Published {
			count++
		} else if run.ArchiveReason == "" {
			t.Fatal("missing archive reason")
		}
	}
	if count != 1 {
		t.Fatalf("published %d", count)
	}
	for i := range runs {
		wasPublished := runs[i].Published
		if err := r.FinalizeRun(ctx, &runs[i], nil, func() string { return "duplicate" }); err != nil || runs[i].Published != wasPublished {
			t.Fatalf("repeated finalization changed publication: %v %+v", err, runs[i])
		}
	}
	var stocks int64
	r.db.Model(&Recommendation{}).Count(&stocks)
	if stocks != 1 {
		t.Fatalf("archived result created stocks: %d", stocks)
	}
	chain, exists, err := r.WithSlot("09:35").ExecutionChainForDate(ctx, "2026-09-18")
	if err != nil || !exists || chain.WinnerRunID == "" {
		t.Fatal(chain, err)
	}
	rows, err := r.WithSlot("09:30").ListRecommendations(ctx, 100, 0)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestSlotLunchAndEmptyPublication(t *testing.T) {
	r := slotRepository(t)
	ctx := context.Background()
	at := slotClock(11, 29)
	r.now = func() time.Time { return at }
	run := AnalysisRun{RunID: "empty", TradingDate: "2026-09-18", ScheduledSlot: "11:25", AttemptNo: 1, Status: "no_recommendation"}
	if err := r.FinalizeRun(ctx, &run, nil, func() string { return "empty" }); err != nil {
		t.Fatal(err)
	}
	if !run.Published {
		t.Fatal(run)
	}
	at = slotClock(11, 30)
	late := AnalysisRun{RunID: "late", TradingDate: "2026-09-18", ScheduledSlot: "11:20", AttemptNo: 1, Status: "success"}
	if err := r.FinalizeRun(ctx, &late, []Recommendation{{RecommendationID: "late-stock", StockCode: "sh600000"}}, func() string { return "late report" }); err != nil {
		t.Fatal(err)
	}
	if late.Published || late.ArchiveReason == "" {
		t.Fatal(late)
	}
}

func TestSlotDiagnosticNeverPublishesEvenDuringTradingWindow(t *testing.T) {
	r := slotRepository(t)
	at := slotClock(9, 35)
	ctx := context.Background()
	runner := NewRunner(r, &sequenceAI{responses: []string{fixtureModelResponse}}, fixedEvidence{value: modelCallEvidence(at, "diagnostic fixture")}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at }, nil)
	run, err := runner.RunDiagnostic(ctx, at)
	if err != nil || run.Status != "success" || run.Published || run.ArchiveReason == "" {
		t.Fatal(run, err)
	}
	var count int64
	r.db.Model(&Recommendation{}).Count(&count)
	if count != 0 {
		t.Fatal("diagnostic published stocks")
	}
}

type slotMarket struct {
	at   time.Time
	fail bool
}

func (m slotMarket) PriceAt(context.Context, string, time.Time, bool) (PriceSnapshot, error) {
	if m.fail {
		return PriceSnapshot{}, errors.New("offline")
	}
	return PriceSnapshot{Price: 12, At: m.at, Source: "test", Suspended: true, LimitDown: true}, nil
}

func TestSlotTimedSellIndependentOfResearchAndRestart(t *testing.T) {
	r := slotRepository(t)
	ctx := context.Background()
	at := slotClock(9, 35)
	yesterday := at.AddDate(0, 0, -1)
	items := []Recommendation{{RecommendationID: "old-0930", Slot: "09:30", StockCode: "sh600000", AnalysisRunID: "historic", Status: "active", BuyAt: &yesterday, SignalAt: yesterday, BuyPrice: 10, BuyMarketPrice: 10, CurrentPrice: 11, Quantity: 100}, {RecommendationID: "future-0940", Slot: "09:40", StockCode: "sh600001", AnalysisRunID: "historic", Status: "active", BuyAt: &yesterday, SignalAt: yesterday, BuyPrice: 10, Quantity: 100}, {RecommendationID: "new-0930", Slot: "09:30", StockCode: "sh600002", AnalysisRunID: "today", Status: "active", BuyAt: &at, SignalAt: at, BuyPrice: 10, Quantity: 100}}
	if err := r.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	service := NewTradingService(r, slotMarket{at: at}, testCalendar{})
	service.now = func() time.Time { return at }
	if err := service.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	overview, err := r.WithSlot("09:30").Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := InitialCash + trading.CalculateSellCost(12, 100).NetCashFlow
	if math.Abs(overview.Cash-want) > 1e-8 {
		t.Fatalf("cash %f want %f", overview.Cash, want)
	}
	if err := service.ProcessDue(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var trades []Trade
	r.db.Find(&trades)
	if len(trades) != 1 || trades[0].RecommendationID != "old-0930" || trades[0].Slot != "09:30" {
		t.Fatal(trades)
	}
	var active int64
	r.db.Model(&Recommendation{}).Where("status = ?", "active").Count(&active)
	if active != 2 {
		t.Fatal(active)
	}
}

func TestSlotSellFallbackChargesFees(t *testing.T) {
	r := slotRepository(t)
	at := slotClock(9, 30)
	before := at.AddDate(0, 0, -1)
	ctx := context.Background()
	if err := r.CreateRecommendations(ctx, []Recommendation{{RecommendationID: "fallback", Slot: "09:30", AnalysisRunID: "old", StockCode: "sh600000", Status: "active", BuyAt: &before, SignalAt: before, BuyPrice: 10, BuyMarketPrice: 10, CurrentPrice: 11, Quantity: 100}}); err != nil {
		t.Fatal(err)
	}
	service := NewTradingService(r, slotMarket{fail: true}, testCalendar{})
	service.now = func() time.Time { return at }
	if err := service.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	var trade Trade
	if err := r.db.First(&trade).Error; err != nil {
		t.Fatal(err)
	}
	if !trade.PriceStale || trade.MarketPrice != 11 || trade.Commission <= 0 || trade.StampDuty <= 0 {
		t.Fatal(trade)
	}
}

func TestSlotFullPipelineEvidenceAnalysisBuyScheduledExitAndPerformance(t *testing.T) {
	ctx := context.Background()
	r := slotRepository(t)
	at := slotClock(9, 30)
	model := &sequenceAI{responses: []string{fixtureModelResponse}}
	runner := NewRunner(r, model, fixedEvidence{value: modelCallEvidence(at, "fixture evidence")}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at }, nil)
	market := chainMarket{snapshots: map[string]PriceSnapshot{"sh600000": {Price: 10, PreviousClose: 10}}}
	trader := NewTradingService(r, market, testCalendar{})
	trader.now = func() time.Time { return at }
	if err := trader.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	run, err := runner.Run(ctx, at)
	if err != nil || !run.Published || run.Slot != "09:30" || model.calls != 1 {
		t.Fatalf("run=%+v calls=%d err=%v", run, model.calls, err)
	}
	if err := trader.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	items, err := r.WithSlot("09:30").ListRecommendations(ctx, 10, 0)
	if err != nil || len(items) != 1 || items[0].BuyAt == nil || items[0].BuyAt.Hour() != 9 || items[0].BuyAt.Minute() != 30 {
		t.Fatal(items, err)
	}
	if run.ReportMarkdown == "" {
		t.Fatal("missing report")
	}
	quantity := items[0].Quantity
	at = at.AddDate(0, 0, 3)
	market.snapshots["sh600000"] = PriceSnapshot{Price: 11, PreviousClose: 11}
	if err := trader.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	performance, err := r.WithSlot("09:30").Performance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := trading.CalculateSellCost(11, quantity).NetCashFlow + trading.CalculateBuyCost(10, quantity).NetCashFlow
	if performance.ClosedTrades != 1 || performance.OpenPositions != 0 || math.Abs(performance.NetProfit-want) > 1e-6 {
		t.Fatalf("performance=%+v wantPnl=%f", performance, want)
	}
	other, err := r.WithSlot(DefaultSlot).Overview(ctx)
	if err != nil || other.Cash != InitialCash || other.OpenPositions != 0 {
		t.Fatal(other, err)
	}
	t.Log("isolated full chain passed: evidence -> AI -> first publication -> immediate buy -> next-session timed sell -> fee-adjusted performance; production DB untouched")
}
