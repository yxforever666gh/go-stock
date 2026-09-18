package research

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	sharedai "go-stock/backend/ai"
	"go-stock/internal/marketquote"
	"go-stock/internal/trading"
)

type frozenProviders struct{}

func (frozenProviders) Complete(context.Context, sharedai.CompletionRequest) (sharedai.CompletionResult, error) {
	panic("frozen research invoked AI")
}
func (frozenProviders) CurrentQuote(context.Context, string) (marketquote.Quote, error) {
	panic("frozen research invoked quotes")
}

func TestFreezeLiquidatesWithFeesAndStopsEntireLifecycle(t *testing.T) {
	ctx := context.Background()
	r := researchTestRepo(t)
	at := time.Date(2026, 9, 18, 15, 10, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	entry := at.AddDate(0, 0, -1)
	rec := seedRecommendation(t, r, "active", entry, at, "")
	seedOpenPosition(t, r, rec, entry)
	position, err := r.Position(ctx, rec.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.DB().Model(&Position{}).Where("id = ?", position.ID).Updates(map[string]any{"current_price": 12.0, "current_price_at": at.Add(-time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	pending := seedRecommendation(t, r, "buy_pending", at, at, "")
	if err = r.DB().Model(&Recommendation{}).Where("recommendation_id = ?", pending.RecommendationID).Update("reserved_cash", 50000).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = r.EnqueueAnalysisTrigger(ctx, TriggerSourceCapitalGap, "freeze-fixture", "fixture", at); err != nil {
		t.Fatal(err)
	}
	before, err := r.Account(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.FreezeAndLiquidate(ctx, at); err != nil {
		t.Fatal(err)
	}
	if err = r.FreezeAndLiquidate(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	account, err := r.Account(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := before.Cash + trading.CalculateSellCost(12, position.Quantity).NetCashFlow
	if !account.Frozen || account.FrozenAt == nil || math.Abs(account.Cash-want) > 1e-6 {
		t.Fatalf("account=%+v wantCash=%f", account, want)
	}
	positions, err := r.OpenPositions(ctx)
	if err != nil || len(positions) != 0 {
		t.Fatal(positions, err)
	}
	var tradeCount, activeTriggers int64
	r.DB().Model(&SimulatedTrade{}).Where("side = ?", "sell").Count(&tradeCount)
	r.DB().Model(&AnalysisTrigger{}).Where("status IN ?", []string{"queued", "running"}).Count(&activeTriggers)
	if tradeCount != 1 || activeTriggers != 0 {
		t.Fatal(tradeCount, activeTriggers)
	}
	var stored Recommendation
	r.DB().Where("recommendation_id = ?", pending.RecommendationID).First(&stored)
	if stored.ReservedCash != 0 || stored.NextCheckAt != nil || stored.Status != "missed_window" {
		t.Fatal(stored)
	}
	service := NewService(r, frozenProviders{}, frozenProviders{}, WeekdayCalendar{})
	runner := NewAnalysisRunner(service, nil)
	if _, err = runner.Run(ctx, AnalysisRequest{Mode: AnalysisModeManual, ScheduledFor: at}); !errors.Is(err, ErrFrozen) {
		t.Fatal(err)
	}
	if err = service.ProcessDue(ctx); err != nil {
		t.Fatal(err)
	}
	if changed, err := service.ProcessScheduledSnapshot(ctx, at); err != nil || changed {
		t.Fatal(changed, err)
	}
	if _, err = r.EnqueueAnalysisTrigger(ctx, TriggerSourceCapitalGap, "after-freeze", "not allowed", at); !errors.Is(err, ErrFrozen) {
		t.Fatal(err)
	}
	if err = r.CheckNewPositionsAllowed(ctx); !errors.Is(err, ErrFrozen) {
		t.Fatal(err)
	}
	if err = r.CreateRecommendation(ctx, &Recommendation{RecommendationID: "after-freeze", AnalysisRunID: rec.AnalysisRunID, StockCode: "sh600001", SignalAt: at, Status: "buy_pending"}, nil); !errors.Is(err, ErrFrozen) {
		t.Fatal(err)
	}
	if err = r.Sell(ctx, rec.RecommendationID, marketquote.Quote{Price: 20, At: at}); !errors.Is(err, ErrFrozen) {
		t.Fatal(err)
	}
	after, err := r.Account(ctx)
	if err != nil || after.Cash != account.Cash {
		t.Fatal(after, err)
	}
}
