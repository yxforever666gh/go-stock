package research2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-stock/internal/trading"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type chainMarket struct {
	snapshots map[string]PriceSnapshot
	errors    map[string]error
}

func (m chainMarket) PriceAt(_ context.Context, code string, target time.Time, _ bool) (PriceSnapshot, error) {
	if err := m.errors[code]; err != nil {
		return PriceSnapshot{}, err
	}
	snapshot := m.snapshots[code]
	if snapshot.Code == "" {
		snapshot.Code = code
	}
	if snapshot.At.IsZero() {
		snapshot.At = target
	}
	if snapshot.Source == "" {
		snapshot.Source = "chain-test"
	}
	return snapshot, nil
}

func (chainMarket) Metrics(context.Context, Recommendation) (MetricSnapshot, error) {
	return MetricSnapshot{}, nil
}

func TestMainBoardLimitDistanceThresholds(t *testing.T) {
	if got := MainBoardLimitPrice(1.15); got != 1.27 {
		t.Fatalf("half-cent rounding limit=%v want=1.27", got)
	}
	limit := MainBoardLimitPrice(10.01)
	if limit != 11.01 {
		t.Fatalf("rounded limit=%v want=11.01", limit)
	}
	priceAtSelectionBoundary := limit * (1 - SelectionLimitDistancePct/100)
	_, distance, blocked := IsInsideLimitBuffer(priceAtSelectionBoundary, 10.01, SelectionLimitDistancePct)
	if blocked || math.Abs(distance-SelectionLimitDistancePct) > 1e-7 {
		t.Fatalf("selection boundary distance=%v blocked=%v", distance, blocked)
	}
	if _, _, blocked = IsInsideLimitBuffer(priceAtSelectionBoundary+0.001, 10.01, SelectionLimitDistancePct); !blocked {
		t.Fatal("price inside selection buffer was accepted")
	}
	priceAtExecutionBoundary := limit * (1 - ExecutionLimitDistancePct/100)
	if _, distance, blocked = IsInsideLimitBuffer(priceAtExecutionBoundary, 10.01, ExecutionLimitDistancePct); blocked || math.Abs(distance-ExecutionLimitDistancePct) > 1e-7 {
		t.Fatalf("execution boundary distance=%v blocked=%v", distance, blocked)
	}
}

func TestExecutionChainCountsExistingSameDayLegacyBuy(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 4, 9, 55, 0, 0, shanghai())
	buyAt := now.Add(-time.Minute)
	legacy := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: "legacy-run", StockCode: "sh600099", StockName: "legacy", SignalAt: buyAt, Status: "active", TargetBuyAt: buyAt, BuyAt: &buyAt}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{legacy}); err != nil {
		t.Fatal(err)
	}
	chain, err := repository.EnsureExecutionChain(context.Background(), now.Format("2006-01-02"), now, now)
	if err != nil || chain.FilledSlots != 1 || chain.Status != "running" {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}
}

func TestCrossDayRecoveryExpiresOldRunningChain(t *testing.T) {
	repository := research2TestRepository(t)
	dayOne := time.Date(2026, 9, 3, 10, 0, 0, 0, shanghai())
	chain, run := createChainRun(t, repository, dayOne)
	run.Status = "running"
	if err := repository.SaveRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: "primary", SelectionRank: 1, StockCode: "sh600098", StockName: "old", SignalAt: dayOne, Status: "buy_pending", TargetBuyAt: dayOne}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	recoveredAt := dayOne.AddDate(0, 0, 1)
	expired, err := repository.ExpireStaleExecutionChains(context.Background(), recoveredAt.Format("2006-01-02"), recoveredAt)
	if err != nil || len(expired) != 1 || expired[0].ChainID != chain.ChainID {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
	chain, _ = repository.ExecutionChain(context.Background(), chain.ChainID)
	stored, err := repository.GetRecommendation(context.Background(), item.RecommendationID)
	storedRun, runErr := repository.AnalysisRunByID(context.Background(), run.RunID)
	if err != nil || runErr != nil || chain.Status != "cutoff" || stored.Recommendation.Status != "analysis_only" || storedRun.Status != "failed" {
		t.Fatalf("chain=%+v recommendation=%+v err=%v", chain, stored.Recommendation, err)
	}
}

func createChainRun(t *testing.T, repository *Repository, now time.Time) (ExecutionChain, AnalysisRun) {
	t.Helper()
	ctx := context.Background()
	chain, err := repository.EnsureExecutionChain(ctx, now.Format("2006-01-02"), now.Add(-5*time.Minute), now.Add(-5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	run := AnalysisRun{
		Slot: DefaultSlot, Published: true, RunID: uuid.NewString(), TradingDate: chain.TradingDate, AttemptNo: 1, ChainID: chain.ChainID,
		TriggerSource: "scheduled", RequestedSlots: 3, PrimaryCount: 3, StandbyCount: 3,
		ScheduledFor: now.Add(-5 * time.Minute), StartedAt: now.Add(-5 * time.Minute), EvidenceCutoffAt: now.Add(-5 * time.Minute),
		StrategyVersion: "research2-trailing5-v9", Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]",
	}
	if err = repository.CreateRun(ctx, &run); err != nil {
		t.Fatal(err)
	}
	if err = repository.AttachRunToExecutionChain(ctx, chain.ChainID, run.RunID); err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Model(&ExecutionChain{}).Where("chain_id = ?", chain.ChainID).Updates(map[string]any{"sell_completed_at": now, "winner_run_id": run.RunID}).Error; err != nil {
		t.Fatal(err)
	}
	chain.SellCompletedAt = &now
	chain.WinnerRunID = run.RunID
	return chain, run
}

func TestTradingServiceSkipsUntradableRowsWithinFiveSeats(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 4, 10, 0, 5, 0, shanghai())
	chain, run := createChainRun(t, repository, now)
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005", "sh600006"}
	items := make([]Recommendation, 0, len(codes))
	for index, code := range codes {
		role, status := "primary", "buy_pending"
		if index >= 3 {
			role, status = "standby", "standby"
		}
		items = append(items, Recommendation{
			RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: role, SelectionRank: index + 1,
			StockCode: code, StockName: code, SignalAt: now.Add(-time.Minute), FinalScore: 80 - float64(index),
			ReferencePrice: 10, Status: status, TargetBuyAt: now.Add(-time.Second),
		})
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	market := chainMarket{snapshots: map[string]PriceSnapshot{
		codes[0]: {Price: 10, PreviousClose: 10},
		codes[1]: {Price: 10.95, PreviousClose: 10},
		codes[2]: {Price: 10, PreviousClose: 10, Suspended: true},
		codes[3]: {Price: 10, PreviousClose: 10},
		codes[4]: {Price: 10, PreviousClose: 10},
		codes[5]: {Price: 10, PreviousClose: 10},
	}}
	if err := testTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.RunRecommendations(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	byCode := make(map[string]Recommendation, len(stored))
	for _, item := range stored {
		byCode[item.StockCode] = item
	}
	if byCode[codes[0]].Status != "active" || byCode[codes[1]].ExecutionFailureCode != "near_limit_up" || byCode[codes[2]].ExecutionFailureCode != "suspended" {
		t.Fatalf("primary outcomes=%+v", stored[:3])
	}
	for _, code := range codes[3:5] {
		if byCode[code].Status != "active" {
			t.Fatalf("standby %s was not promoted: %+v", code, byCode[code])
		}
	}
	if byCode[codes[5]].BuyAt == nil {
		t.Fatalf("unused standby=%+v", byCode[codes[5]])
	}
	chain, err = repository.ExecutionChain(context.Background(), chain.ChainID)
	if err != nil || chain.Status != "completed" || chain.FilledSlots != 4 {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}
	var trades int64
	if err = repository.DB().Model(&Trade{}).Where("side = ?", "buy").Count(&trades).Error; err != nil || trades != 4 {
		t.Fatalf("buy trades=%d err=%v", trades, err)
	}
}

func TestPendingQuoteDoesNotBlockOtherExecutableRows(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 4, 10, 0, 5, 0, shanghai())
	chain, run := createChainRun(t, repository, now)
	codes := []string{"sh600051", "sh600052", "sh600053", "sh600054", "sh600055"}
	items := make([]Recommendation, 0, len(codes))
	for index, code := range codes {
		role, status := "primary", "buy_pending"
		if index >= 3 {
			role, status = "standby", "standby"
		}
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: role, SelectionRank: index + 1, StockCode: code, StockName: code, SignalAt: now.Add(-time.Minute), FinalScore: 80 - float64(index), ReferencePrice: 10, Status: status, TargetBuyAt: now})
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	market := chainMarket{
		errors: map[string]error{codes[0]: errors.New("temporary quote failure")},
		snapshots: map[string]PriceSnapshot{
			codes[1]: {Price: 11, PreviousClose: 10, LimitUp: true},
			codes[2]: {Price: 10, PreviousClose: 10, Suspended: true},
			codes[3]: {Price: 10, PreviousClose: 10},
			codes[4]: {Price: 10, PreviousClose: 10},
		},
	}
	if err := testTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.RunRecommendations(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	byCode := make(map[string]Recommendation, len(stored))
	for _, item := range stored {
		byCode[item.StockCode] = item
	}
	if byCode[codes[0]].Status != "buy_pending" || byCode[codes[0]].ExecutionFailureCode != "quote_retry" {
		t.Fatalf("pending primary=%+v", byCode[codes[0]])
	}
	if byCode[codes[3]].Status != "active" || byCode[codes[4]].Status != "active" {
		t.Fatalf("standbys did not fill the two non-reserved slots: %+v", stored)
	}
	chain, err = repository.RefreshExecutionChainFilled(context.Background(), chain.ChainID)
	if err != nil || chain.FilledSlots != 2 || chain.Status != "running" {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}
}

func TestTradingServiceCompletesUnderfilledReportWithoutAnotherAnalysis(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 4, 10, 0, 5, 0, shanghai())
	chain, run := createChainRun(t, repository, now)
	items := []Recommendation{
		{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: "primary", SelectionRank: 1, StockCode: "sh600011", StockName: "one", SignalAt: now.Add(-time.Minute), FinalScore: 70, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now},
		{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: "primary", SelectionRank: 2, StockCode: "sh600012", StockName: "two", SignalAt: now.Add(-time.Minute), FinalScore: 69, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now},
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	market := chainMarket{snapshots: map[string]PriceSnapshot{
		"sh600011": {Price: 10, PreviousClose: 10},
		"sh600012": {Price: 11, PreviousClose: 10, LimitUp: true},
	}}
	if err := testTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	chain, err := repository.RefreshExecutionChainFilled(context.Background(), chain.ChainID)
	if err != nil || chain.Status != "completed" || chain.FilledSlots != 1 {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}
	overview, err := repository.Overview(context.Background())
	if err != nil || math.Abs(overview.Cash-(InitialCash+trading.CalculateBuyCost(10, 200).NetCashFlow)) > 1e-7 {
		t.Fatalf("cash=%v err=%v; one executed slot must preserve remaining cash", overview.Cash, err)
	}
}

func TestQuoteFailureStaysPendingAndDoesNotSpawnRefill(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 4, 10, 0, 5, 0, shanghai())
	chain, run := createChainRun(t, repository, now)
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: "primary", SelectionRank: 1, StockCode: "sh600021", StockName: "retry", SignalAt: now.Add(-time.Minute), FinalScore: 70, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	market := chainMarket{errors: map[string]error{item.StockCode: errors.New("temporary quote failure")}}
	if err := testTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetRecommendation(context.Background(), item.RecommendationID)
	if err != nil || stored.Recommendation.Status != "buy_pending" || stored.Recommendation.ExecutionFailureCode != "quote_retry" {
		t.Fatalf("stored=%+v err=%v", stored.Recommendation, err)
	}
	chain, _ = repository.ExecutionChain(context.Background(), chain.ChainID)
	if chain.Status != "running" {
		t.Fatalf("chain=%+v", chain)
	}
}

func TestStaleExecutionQuoteStaysPending(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 4, 10, 5, 5, 0, shanghai())
	_, run := createChainRun(t, repository, now)
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: "primary", SelectionRank: 1, StockCode: "sh600023", StockName: "stale", SignalAt: now.Add(-2 * time.Minute), FinalScore: 70, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now.Add(-time.Minute)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	market := chainMarket{snapshots: map[string]PriceSnapshot{item.StockCode: {Price: 10, PreviousClose: 10, At: now.Add(-61 * time.Second)}}}
	if err := testTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetRecommendation(context.Background(), item.RecommendationID)
	if err != nil || detail.Recommendation.Status != "buy_pending" || detail.Recommendation.ExecutionFailureCode != "quote_retry" {
		t.Fatalf("stale quote outcome=%+v err=%v", detail.Recommendation, err)
	}
}

func TestExecutionChainConcurrentBuysNeverExceedFive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "research2-wal.db")
	database, err := gorm.Open(sqlite.Open(path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, openErr := database.DB(); openErr == nil {
		sqlDB.SetMaxOpenConns(8)
		defer sqlDB.Close()
	}
	if err = database.AutoMigrate(&AnalysisRun{}, &ExecutionChain{}, &Recommendation{}, &Trade{}, &Account{}, &AccountSnapshot{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(database)
	if err = repository.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 10, 0, 5, 0, shanghai())
	chain, run := createChainRun(t, repository, now)
	items := make([]Recommendation, 8)
	for index := range items {
		items[index] = Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, SelectionRole: "primary", SelectionRank: index + 1, StockCode: fmt.Sprintf("sh6001%02d", index), StockName: "concurrent", SignalAt: now.Add(-time.Minute), Status: "buy_pending", TargetBuyAt: now}
	}
	if err = repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	cost := trading.CalculateBuyCost(10, 100)
	var wait sync.WaitGroup
	errorsByIndex := make([]error, len(items))
	for index, item := range items {
		wait.Add(1)
		go func(index int, item Recommendation) {
			defer wait.Done()
			trade := Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: now, MarketPrice: 10, ExecutionPrice: cost.ExecutionPrice, Quantity: 100, Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow}
			errorsByIndex[index] = repository.RecordBuy(context.Background(), item.RecommendationID, trade, now.AddDate(0, 0, 1))
		}(index, item)
	}
	wait.Wait()
	var successes int
	for _, buyErr := range errorsByIndex {
		if buyErr == nil {
			successes++
		}
	}
	if successes != DailyTargetSlots {
		t.Fatalf("successful concurrent buys=%d errors=%v", successes, errorsByIndex)
	}
	chain, err = repository.RefreshExecutionChainFilled(context.Background(), chain.ChainID)
	if err != nil || chain.Status != "completed" || chain.FilledSlots != DailyTargetSlots {
		t.Fatalf("chain=%+v err=%v", chain, err)
	}
	if err = testTradingService(repository, chainMarket{}, testCalendar{}).ProcessDue(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var pending int64
	if err = database.Model(&Recommendation{}).Where("analysis_run_id = ? AND status = ?", run.RunID, "buy_pending").Count(&pending).Error; err != nil || pending != 0 {
		t.Fatalf("completed chain retained %d pending primaries err=%v", pending, err)
	}
	var trades int64
	if err = database.Model(&Trade{}).Where("side = ?", "buy").Count(&trades).Error; err != nil || trades != DailyTargetSlots {
		t.Fatalf("trades=%d err=%v", trades, err)
	}
	var account Account
	if err = database.First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	wantCash := InitialCash + float64(DailyTargetSlots)*cost.NetCashFlow
	if math.Abs(account.Cash-wantCash) > 1e-7 {
		t.Fatalf("cash=%f want=%f", account.Cash, wantCash)
	}
}

func TestChainlessLegacyPendingCannotExceedAccountDailyFiveBuys(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 9, 4, 10, 5, 0, 0, shanghai())
	items := make([]Recommendation, 0, 4)
	for index := 0; index < DailyTargetSlots; index++ {
		buyAt := now.Add(-time.Duration(index+1) * time.Minute)
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: "legacy", StockCode: fmt.Sprintf("sh6002%02d", index), StockName: "bought", SignalAt: buyAt, Status: "active", TargetBuyAt: buyAt, BuyAt: &buyAt})
	}
	pending := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: "legacy", StockCode: "sh600299", StockName: "pending", SignalAt: now.Add(-time.Minute), Status: "buy_pending", TargetBuyAt: now}
	items = append(items, pending)
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	cost := trading.CalculateBuyCost(10, 100)
	trade := Trade{TradeID: uuid.NewString(), RecommendationID: pending.RecommendationID, Side: "buy", TradedAt: now, MarketPrice: 10, ExecutionPrice: cost.ExecutionPrice, Quantity: 100, Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow}
	if err := repository.RecordBuy(context.Background(), pending.RecommendationID, trade, now.AddDate(0, 0, 1)); err == nil {
		t.Fatal("chainless fourth same-day buy was accepted")
	}
}
