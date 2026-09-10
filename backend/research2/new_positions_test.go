package research2

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sharedai "go-stock/backend/ai"
	"go-stock/internal/researchevidence"
	"go-stock/internal/trading"

	"gorm.io/gorm"
)

func configurePermissionFixture(t *testing.T, repo *Repository) func(bool) {
	t.Helper()
	if err := repo.DB().Exec("CREATE TABLE test_new_positions (enabled BOOLEAN NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB().Exec("INSERT INTO test_new_positions VALUES (true)").Error; err != nil {
		t.Fatal(err)
	}
	repo.ConfigureNewPositionsPermission(func(ctx context.Context, database *gorm.DB) error {
		var enabled bool
		if err := database.WithContext(ctx).Raw("SELECT enabled FROM test_new_positions").Scan(&enabled).Error; err != nil {
			return err
		}
		if !enabled {
			return trading.ErrNewPositionsDisabled
		}
		return nil
	})
	return func(enabled bool) {
		t.Helper()
		if err := repo.DB().Exec("UPDATE test_new_positions SET enabled = ?", enabled).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewPositionsPermissionGuardsBuyTransaction(t *testing.T) {
	ctx := context.Background()
	repo := research2TestRepository(t)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, repo, at)
	item := Recommendation{RecommendationID: "permission-buy", AnalysisRunID: run.RunID, StockCode: "sh600000", Status: "buy_pending", SignalAt: at, TargetBuyAt: at}
	if err := repo.CreateRecommendations(ctx, []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	setEnabled := configurePermissionFixture(t, repo)
	if err := repo.CheckNewPositionsAllowed(ctx); err != nil {
		t.Fatal(err)
	}
	setEnabled(false)
	if !errors.Is(repo.CheckNewPositionsAllowed(ctx), trading.ErrNewPositionsDisabled) {
		t.Fatal("disabled setting not visible")
	}
	permission := repo.newPositionsPermission
	calls := 0
	repo.ConfigureNewPositionsPermission(func(ctx context.Context, tx *gorm.DB) error {
		calls++
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			t.Error("permission did not receive buy transaction")
		}
		return permission(ctx, tx)
	})
	err := repo.RecordBuy(ctx, item.RecommendationID, Trade{TradeID: "disabled-buy", RecommendationID: item.RecommendationID, Side: "buy", TradedAt: at, NetCashFlow: -1005, Quantity: 100}, at.AddDate(0, 0, 1))
	if !errors.Is(err, trading.ErrNewPositionsDisabled) {
		t.Fatalf("buy err=%v", err)
	}
	var account Account
	if err := repo.DB().First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	var trades int64
	if err := repo.DB().Model(&Trade{}).Count(&trades).Error; err != nil {
		t.Fatal(err)
	}
	if calls != 1 || account.Cash != InitialCash || trades != 0 {
		t.Fatalf("disabled transaction leaked or retried: calls=%d cash=%f trades=%d", calls, account.Cash, trades)
	}
}

type permissionBlockingAI struct {
	started, resume chan struct{}
	response        string
}

func (ai permissionBlockingAI) Complete(ctx context.Context, _ sharedai.CompletionRequest) (sharedai.CompletionResult, error) {
	close(ai.started)
	select {
	case <-ai.resume:
		return sharedai.CompletionResult{Content: ai.response}, nil
	case <-ctx.Done():
		return sharedai.CompletionResult{}, ctx.Err()
	}
}

func TestDisableDuringAnalysisPreservesRunningStateAndCompletesAnalysisOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := research2TestRepository(t)
	setEnabled := configurePermissionFixture(t, repo)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghai())
	ai := permissionBlockingAI{started: make(chan struct{}), resume: make(chan struct{}), response: `{"tradingDay":true,"conclusion":"推荐","recommendations":[{"code":"sh600343","marketScore":20,"stockScore":40,"finalScore":60,"referencePrice":10,"sourceRefs":["market","quote-sh600343"]}]}`}
	evidence := scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: "sh600343"})
	runner := NewRunner(repo, ai, fixedEvidence{value: evidence}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return at }, nil)
	var run AnalysisRun
	done := make(chan error, 1)
	go func() { var err error; run, err = runner.Run(ctx, at); done <- err }()
	select {
	case <-ai.started:
	case <-ctx.Done():
		t.Fatal("analysis did not reach model call")
	}
	setEnabled(false)
	chains, err := repo.DisableRunningExecutionChains(ctx, at.Format("2006-01-02"), at)
	if err != nil || len(chains) != 1 {
		t.Fatalf("chains=%+v err=%v", chains, err)
	}
	var active AnalysisRun
	if err := repo.DB().Where("chain_id = ?", chains[0].ChainID).First(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active.Status != "running" || active.GeneratedAt != nil {
		t.Fatalf("disable pretended to interrupt live analysis: %+v", active)
	}
	// Even an immediate re-enable cannot resurrect this already closed chain.
	setEnabled(true)
	if err := repo.AttachRunToExecutionChain(ctx, chains[0].ChainID, active.RunID); err != nil {
		t.Fatal(err)
	}
	close(ai.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	items, err := repo.RunRecommendations(ctx, run.RunID)
	chain, chainErr := repo.ExecutionChain(ctx, chains[0].ChainID)
	if err != nil || chainErr != nil || run.Status != "success" || len(items) != 1 || items[0].Status != "analysis_only" || chain.Status != "disabled" || !strings.Contains(run.ReportMarkdown, "仅分析，不交易") || !strings.Contains(run.ReportMarkdown, "自动策略已关闭") {
		t.Fatalf("disabled run did not finalize cleanly: run=%+v items=%+v chain=%+v err=%v/%v", run, items, chain, err, chainErr)
	}
}

func TestDisabledResearchStillSellsAndDoesNotRevivePendingBuy(t *testing.T) {
	ctx := context.Background()
	repo := research2TestRepository(t)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, repo, at)
	buyAt := at.AddDate(0, 0, -1)
	items := []Recommendation{
		{RecommendationID: "pending", AnalysisRunID: run.RunID, StockCode: "sh600001", Status: "buy_pending", SignalAt: at, TargetBuyAt: at},
		{RecommendationID: "held", AnalysisRunID: run.RunID, StockCode: "sh600002", Status: "active", SignalAt: buyAt, BuyAt: &buyAt, Quantity: 100, BuyPrice: 10, TargetSellAt: &at},
	}
	if err := repo.CreateRecommendations(ctx, items); err != nil {
		t.Fatal(err)
	}
	setEnabled := configurePermissionFixture(t, repo)
	setEnabled(false)
	if _, err := repo.DisableRunningExecutionChains(ctx, run.TradingDate, at); err != nil {
		t.Fatal(err)
	}
	service := testTradingService(repo, chainMarket{snapshots: map[string]PriceSnapshot{"sh600002": {Price: 11, At: at}}}, testCalendar{})
	if err := service.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	setEnabled(true)
	if err := service.ProcessDue(ctx, at); err != nil {
		t.Fatal(err)
	}
	// A delayed denial from an older runtime must not overwrite a position that
	// already completed a trade while that runtime was waiting.
	if err := repo.MarkStatus(ctx, "held", "analysis_only", "late disable"); err != nil {
		t.Fatal(err)
	}
	var buys, sells int64
	repo.DB().Model(&Trade{}).Where("side = ?", "buy").Count(&buys)
	repo.DB().Model(&Trade{}).Where("side = ?", "sell").Count(&sells)
	var pending, held Recommendation
	repo.DB().Where("recommendation_id = ?", "pending").First(&pending)
	repo.DB().Where("recommendation_id = ?", "held").First(&held)
	if buys != 0 || sells != 1 || pending.Status != "analysis_only" || held.Status != "closed" {
		t.Fatalf("disabled lifecycle leaked: buys=%d sells=%d pending=%+v held=%+v", buys, sells, pending, held)
	}
}

type permissionBlockingMarket struct {
	chainMarket
	started, resume chan struct{}
}

func (m permissionBlockingMarket) PriceAt(ctx context.Context, code string, at time.Time, current bool) (PriceSnapshot, error) {
	close(m.started)
	select {
	case <-m.resume:
		return m.chainMarket.PriceAt(ctx, code, at, current)
	case <-ctx.Done():
		return PriceSnapshot{}, ctx.Err()
	}
}

func TestDisableWhileBuyQuoteInFlightPreventsCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := research2TestRepository(t)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, repo, at)
	item := Recommendation{RecommendationID: "inflight", AnalysisRunID: run.RunID, SelectionRole: "primary", StockCode: "sh600000", Status: "buy_pending", SignalAt: at, TargetBuyAt: at}
	if err := repo.CreateRecommendations(ctx, []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	setEnabled := configurePermissionFixture(t, repo)
	market := permissionBlockingMarket{chainMarket: chainMarket{snapshots: map[string]PriceSnapshot{"sh600000": {Price: 10, PreviousClose: 10, At: at}}}, started: make(chan struct{}), resume: make(chan struct{})}
	service := testTradingService(repo, market, testCalendar{})
	done := make(chan error, 1)
	go func() { done <- service.ProcessDue(ctx, at) }()
	select {
	case <-market.started:
	case <-ctx.Done():
		t.Fatal("buy did not reach quote request")
	}
	setEnabled(false)
	close(market.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var trades int64
	repo.DB().Model(&Trade{}).Count(&trades)
	var stored Recommendation
	repo.DB().Where("recommendation_id = ?", item.RecommendationID).First(&stored)
	if trades != 0 || stored.Status != "analysis_only" {
		t.Fatalf("inflight buy leaked: trades=%d item=%+v", trades, stored)
	}
}
