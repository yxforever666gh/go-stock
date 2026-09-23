package research2

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"go-stock/internal/researchevidence"
	"gorm.io/gorm"
)

func TestRecommendationAffordabilityUsesAvailableCashIncludingFees(t *testing.T) {
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	value := modelRecommendation{Code: "sh600000", MarketScore: 10, SectorScore: 10, StockScore: 20, FinalScore: 40, ReferencePrice: 60, SourceRefs: scoreFixtureRefs("sh600000")}
	cost := -testAShareBuyCost("sh600000", 60, 100).NetCashFlow
	for _, sample := range []struct {
		name string
		cash float64
		want int
	}{
		{"zero", 0, 0}, {"principal only", 6000, 0}, {"including fees", cost + 0.01, 1}, {"initial balance", InitialCash, 1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			evidence := scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: "sh600000"})
			evidence.AvailableCash = sample.cash
			items, _ := validateRecommendations("run", at, prepareEvidence(evidence, at.Add(-24*time.Hour)), []modelRecommendation{value})
			if len(items) != sample.want {
				t.Fatalf("cash %.4f items=%+v", sample.cash, items)
			}
		})
	}
	evidence := scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: "sh600000"})
	evidence.AvailableCash = 25000
	value.ReferencePrice = 150
	items, _ := validateRecommendations("large-cash", at, prepareEvidence(evidence, at.Add(-24*time.Hour)), []modelRecommendation{value})
	if len(items) != 1 {
		t.Fatalf("old 12000 cap still applied: %+v", items)
	}
}

func TestRunnerAnalysisDoesNotUseOriginAccountCash(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	cash := 6789.12
	if err := r.DB().Model(&Account{}).Where("id = ?", 1).Update("cash", cash).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 10, 9, 50, 0, 0, shanghai())
	collector := &recordingEvidence{value: Evidence{Prompt: `{}`, SourceStatusJSON: `[]`}}
	runner := NewRunner(r, &sequenceAI{responses: []string{`{"tradingDay":true,"recommendations":[]}`}}, collector, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at }, nil)
	if _, err := runner.Run(ctx, at); err != nil {
		t.Fatal(err)
	}
	if collector.cash != math.MaxFloat64 {
		t.Fatalf("collector cash=%f want=%f", collector.cash, cash)
	}
}

func TestConcurrentBuyUsesCurrentCashAndFeesWithoutOverdraft(t *testing.T) {
	r := concurrentResearch2Repository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, r, at)
	cost := testAShareBuyCost("sh600001", 10, 100)
	initial := -cost.NetCashFlow + 0.01
	if err := r.DB().Model(&Account{}).Where("id = ?", 1).Update("cash", initial).Error; err != nil {
		t.Fatal(err)
	}
	items := []Recommendation{{RecommendationID: "cash-one", AnalysisRunID: run.RunID, StockCode: "sh600001", Status: "buy_pending", SignalAt: at, TargetBuyAt: at}, {RecommendationID: "cash-two", AnalysisRunID: run.RunID, StockCode: "sh600002", Status: "buy_pending", SignalAt: at, TargetBuyAt: at}}
	if err := r.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(items))
	for i, item := range items {
		wg.Add(1)
		go func(i int, item Recommendation) {
			defer wg.Done()
			trade := Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: at, MarketPrice: 10, ExecutionPrice: cost.ExecutionPrice, Quantity: 100, Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow}
			errs[i] = r.RecordBuy(ctx, item.RecommendationID, trade, at.AddDate(0, 0, 1))
		}(i, item)
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("successful buys=%d errors=%v", success, errs)
	}
	var account Account
	var count int64
	if err := r.DB().First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Model(&Trade{}).Where("side = ?", "buy").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 || account.Cash < 0 || math.Abs(account.Cash-(initial+cost.NetCashFlow)) > 1e-7 {
		t.Fatalf("trades=%d cash=%f", count, account.Cash)
	}
}

func TestLegacyAnalysisOnlyDoesNotBecomeExecutable(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, r, at)
	item := Recommendation{RecommendationID: "legacy-analysis", AnalysisRunID: run.RunID, StockCode: "sh600000", SelectionRole: "observation", Status: "analysis_only", FinalScore: 99, SignalAt: at, TargetBuyAt: at}
	if err := r.CreateRecommendations(ctx, []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	if err := testTradingService(r, testMarket{price: 10}, testCalendar{}).ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	stored, err := r.RunRecommendations(ctx, run.RunID)
	if err != nil || len(stored) != 1 || stored[0].Status != "analysis_only" || stored[0].BuyAt != nil {
		t.Fatalf("legacy=%+v err=%v", stored, err)
	}
}

func TestExecutionCanReachLowScoreCandidatesBeyondSix(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, r, at)
	items := []Recommendation{}
	quotes := map[string]PriceSnapshot{}
	for i := 0; i < 8; i++ {
		code := fmt.Sprintf("sh600%03d", i+100)
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: code, StockName: code, FinalScore: float64(55 - i), SelectionRank: i + 1, ReferencePrice: 10, SignalAt: at.Add(-time.Minute), TargetBuyAt: at, Status: "buy_pending"})
		quotes[code] = PriceSnapshot{Price: 10, PreviousClose: 10, Suspended: i < 6}
	}
	if err := r.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	if err := testTradingService(r, chainMarket{snapshots: quotes}, testCalendar{}).ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	stored, err := r.RunRecommendations(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	buys := 0
	for _, item := range stored {
		if item.BuyAt != nil {
			buys++
			if item.StockCode != "sh600106" && item.StockCode != "sh600107" {
				t.Fatalf("untradable stock bought: %+v", item)
			}
		}
	}
	if buys != 2 {
		t.Fatalf("low-ranked affordable stocks not executed: %+v", stored)
	}
}

func concurrentResearch2Repository(t *testing.T) *Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&AnalysisRun{}, &ExecutionChain{}, &Recommendation{}, &Trade{}, &Account{}, &AccountSnapshot{}); err != nil {
		t.Fatal(err)
	}
	r := NewRepository(db)
	if err = r.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCashSkipsDoNotDisplaceHigherRankWaitingForQuote(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 15, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, r, at)
	market := chainMarket{snapshots: map[string]PriceSnapshot{}, errors: map[string]error{}}
	items := []Recommendation{}
	for index, price := range []float64{40, 90, 85, 10, 10, 10} {
		code := fmt.Sprintf("sh600%03d", index+100)
		items = append(items, Recommendation{RecommendationID: code, AnalysisRunID: run.RunID, StockCode: code, FinalScore: float64(60 - index), SignalAt: at, TargetBuyAt: at, Status: "buy_pending"})
		market.snapshots[code] = PriceSnapshot{Price: price, PreviousClose: price}
	}
	market.errors["sh600103"] = fmt.Errorf("quote temporarily unavailable")
	if err := r.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	service := testTradingService(r, market, testCalendar{})
	if err := service.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	rows, _ := r.RunRecommendations(ctx, run.RunID)
	statuses := map[string]string{}
	buys := 0
	for _, row := range rows {
		statuses[row.StockCode] = row.Status
		if row.BuyAt != nil {
			buys++
		}
	}
	if buys != 3 || statuses["sh600101"] != "missed_cash" || statuses["sh600102"] != "missed_cash" || statuses["sh600103"] != "buy_pending" || statuses["sh600105"] != "active" {
		t.Fatalf("statuses=%v buys=%d", statuses, buys)
	}
	delete(market.errors, "sh600103")
	if err := service.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	rows, _ = r.RunRecommendations(ctx, run.RunID)
	for _, row := range rows {
		if row.StockCode == "sh600103" && row.BuyAt == nil {
			t.Fatal("higher-ranked quote recovery was displaced")
		}

	}
}
