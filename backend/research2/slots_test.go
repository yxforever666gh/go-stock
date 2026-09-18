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
func (slotMarket) Metrics(context.Context, Recommendation) (MetricSnapshot, error) {
	return MetricSnapshot{}, nil
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
