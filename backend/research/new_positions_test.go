package research

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sharedai "go-stock/backend/ai"
	"go-stock/internal/marketquote"
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

func TestNewPositionsPermissionGuardsAdmissionAndBuyTransaction(t *testing.T) {
	ctx := context.Background()
	repo := researchTestRepo(t)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghaiLocation)
	pending := seedRecommendation(t, repo, "buy_pending", at, at, "")
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
	var before, after SimulatedAccount
	if err := repo.DB().First(&before, 1).Error; err != nil {
		t.Fatal(err)
	}
	quote := marketquote.Quote{Code: pending.StockCode, Name: pending.StockName, Price: 10, At: at, Market: "SH"}
	if err := repo.Buy(ctx, pending.RecommendationID, quote, at.AddDate(0, 0, 1), at); !errors.Is(err, trading.ErrNewPositionsDisabled) {
		t.Fatalf("buy err=%v", err)
	}
	newItem := Recommendation{RecommendationID: newID(), StockCode: "sz000001", Status: "buy_pending", ReservedCash: TargetCashPerTrade}
	if err := repo.CreateRecommendationWithinCapacity(ctx, &newItem, nil, nil); !errors.Is(err, trading.ErrNewPositionsDisabled) {
		t.Fatalf("admission err=%v", err)
	}
	if err := repo.DB().First(&after, 1).Error; err != nil {
		t.Fatal(err)
	}
	var trades, positions, recommendations int64
	repo.DB().Model(&SimulatedTrade{}).Count(&trades)
	repo.DB().Model(&Position{}).Count(&positions)
	repo.DB().Model(&Recommendation{}).Count(&recommendations)
	if calls != 2 || before.Cash != after.Cash || trades != 0 || positions != 0 || recommendations != 1 {
		t.Fatalf("disabled transaction leaked or retried: calls=%d cash=%f/%f trades=%d positions=%d recommendations=%d", calls, before.Cash, after.Cash, trades, positions, recommendations)
	}
}

type permissionBlockingAI struct {
	delegate *scriptedAI
	started  chan struct{}
	resume   chan struct{}
}

func (ai *permissionBlockingAI) Complete(ctx context.Context, request sharedai.CompletionRequest) (sharedai.CompletionResult, error) {
	if request.Phase == "final_decision" {
		close(ai.started)
		select {
		case <-ai.resume:
		case <-ctx.Done():
			return sharedai.CompletionResult{}, ctx.Err()
		}
	}
	return ai.delegate.Complete(ctx, request)
}

func TestDisableDuringAnalysisKeepsRequestedActionAndCompletesReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := researchTestRepo(t)
	setEnabled := configurePermissionFixture(t, repo)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghaiLocation)
	ai := &permissionBlockingAI{started: make(chan struct{}), resume: make(chan struct{}), delegate: &scriptedAI{results: []sharedai.CompletionResult{
		{Content: "市场风险可控"},
		{Content: `{"analysis":"银行资金转强","directions":["银行"],"candidates":[{"code":"600000","name":"浦发银行"}]}`},
		{Content: `{"analysis":"结构改善","shortlist":[{"stockName":"浦发银行","stockCode":"sh600000","aiSummary":"结构改善","mainRisk":"回落","sourceRefs":"S001"}]}`},
		{Content: "建议直接模拟买入。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|\n|浦发银行|sh600000|结构改善|回落|S001|"},
	}}}
	quote := marketquote.Quote{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: at}
	service := NewService(repo, ai, &scriptedQuotes{quotes: []marketquote.Quote{quote}}, weekdayTradingCalendar{})
	service.now = func() time.Time { return at }
	runner := NewAnalysisRunner(service, fixedCollector{})
	var run AnalysisRun
	done := make(chan error, 1)
	go func() { var err error; run, err = runner.Run(ctx, AnalysisRequest{ScheduledFor: at}); done <- err }()
	select {
	case <-ai.started:
	case <-ctx.Done():
		t.Fatal("analysis did not reach final model call")
	}
	setEnabled(false)
	close(ai.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	opportunities, err := repo.BuyOpportunitiesForRun(ctx, run.RunID)
	if err != nil || len(opportunities) != 1 {
		t.Fatalf("opportunities=%+v err=%v", opportunities, err)
	}
	item := opportunities[0]
	if run.Status == "failed" || run.CompletedAt == nil || run.RecommendationCount != 0 || item.RequestedAction != OpportunityActionBuyNow || item.Action != OpportunityActionReject || item.Status != "closed" || !strings.Contains(item.ValidationReason, "自动策略已关闭") || !strings.Contains(run.FinalReport, "自动策略已关闭") {
		t.Fatalf("disabled analysis did not close cleanly: run=%+v opportunity=%+v", run, item)
	}
	wait := BuyOpportunity{AnalysisRunID: run.RunID, Action: OpportunityActionWait, StockCode: "sz000001", ReanalysisAt: &at}
	if err := repo.CreateBuyOpportunity(ctx, &wait); err != nil {
		t.Fatal(err)
	}
	if wait.RequestedAction != OpportunityActionWait || wait.Status != "closed" || wait.ReanalysisAt != nil {
		t.Fatalf("disabled wait remained runnable: %+v", wait)
	}
	var trades int64
	repo.DB().Model(&SimulatedTrade{}).Count(&trades)
	if trades != 0 {
		t.Fatal("disabled analysis created trades")
	}
}

func TestDisabledResearchStillExitsHeldPositionWithoutRetryingBuys(t *testing.T) {
	ctx := context.Background()
	repo := researchTestRepo(t)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghaiLocation)
	pending := seedRecommendation(t, repo, "buy_pending", at, at, "")
	held := seedRecommendation(t, repo, "sell_pending", at.AddDate(0, 0, -1), at, "")
	seedOpenPosition(t, repo, held, at.AddDate(0, 0, -1))
	setEnabled := configurePermissionFixture(t, repo)
	setEnabled(false)
	quote := marketquote.Quote{Code: held.StockCode, Name: held.StockName, Market: "SH", Price: 11, PreviousClose: 10.5, At: at}
	service := NewService(repo, &scriptedAI{}, &scriptedQuotes{quotes: []marketquote.Quote{quote}}, weekdayTradingCalendar{})
	service.now = func() time.Time { return at }
	if err := service.ProcessDue(ctx); err != nil {
		t.Fatal(err)
	}
	setEnabled(true)
	if err := service.ProcessDue(ctx); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Recommendation(ctx, pending.RecommendationID)
	position, _ := repo.Position(ctx, held.RecommendationID)
	var buys, sells, retries int64
	repo.DB().Model(&SimulatedTrade{}).Where("side = ?", "buy").Count(&buys)
	repo.DB().Model(&SimulatedTrade{}).Where("side = ?", "sell").Count(&sells)
	repo.DB().Model(&DecisionEvent{}).Where("decision_type IN ?", []string{"错误重试", "买入处理重试"}).Count(&retries)
	if stored.Status != "missed_untradable" || stored.NextCheckAt != nil || stored.ReservedCash != 0 || position.Status != "closed" || buys != 0 || sells != 1 || retries != 0 {
		t.Fatalf("disabled lifecycle leaked: pending=%+v position=%+v buys=%d sells=%d retries=%d", stored, position, buys, sells, retries)
	}
}

type permissionBlockingQuote struct {
	quote           marketquote.Quote
	started, resume chan struct{}
}

func (q permissionBlockingQuote) CurrentQuote(ctx context.Context, _ string) (marketquote.Quote, error) {
	close(q.started)
	select {
	case <-q.resume:
		return q.quote, nil
	case <-ctx.Done():
		return marketquote.Quote{}, ctx.Err()
	}
}

func TestDisableWhileBuyQuoteInFlightPreventsCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := researchTestRepo(t)
	setEnabled := configurePermissionFixture(t, repo)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghaiLocation)
	quote := permissionBlockingQuote{quote: marketquote.Quote{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: at}, started: make(chan struct{}), resume: make(chan struct{})}
	service := NewService(repo, &scriptedAI{}, quote, weekdayTradingCalendar{})
	service.now = func() time.Time { return at }
	item := Recommendation{RecommendationID: newID(), AnalysisRunID: seedRun(t, repo, at), StockCode: "sh600000", StockName: "浦发银行", SignalAt: at}
	done := make(chan error, 1)
	go func() { done <- service.EnqueueRecommendation(ctx, &item, nil) }()
	select {
	case <-quote.started:
	case <-ctx.Done():
		t.Fatal("buy did not reach quote request")
	}
	setEnabled(false)
	close(quote.resume)
	if err := <-done; !errors.Is(err, trading.ErrNewPositionsDisabled) {
		t.Fatalf("enqueue err=%v", err)
	}
	var trades int64
	repo.DB().Model(&SimulatedTrade{}).Count(&trades)
	stored, _ := repo.Recommendation(ctx, item.RecommendationID)
	if trades != 0 || stored.Status != "missed_untradable" || stored.NextCheckAt != nil {
		t.Fatalf("inflight buy leaked: trades=%d item=%+v", trades, stored)
	}
}
