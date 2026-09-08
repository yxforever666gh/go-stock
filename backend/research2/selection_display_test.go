package research2

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestDailySelectionDisplayCombinesBatchesBeforePaginationAndPreservesHistory(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	first := time.Date(2026, 9, 8, 10, 3, 0, 0, shanghai())
	second := first.Add(155 * time.Second)
	previous := first.AddDate(0, 0, -1)
	rows := []Recommendation{
		{RecommendationID: "later", AnalysisRunID: "second", SelectionRole: "primary", SelectionRank: 1, SignalAt: second, BuyAt: &second, Status: "active"},
		{RecommendationID: "first", AnalysisRunID: "first", SelectionRole: "primary", SelectionRank: 1, SignalAt: first, BuyAt: &first, Status: "closed", FinalScore: 53, Summary: "原始理由"},
		{RecommendationID: "second", AnalysisRunID: "first", SelectionRole: "primary", SelectionRank: 2, SignalAt: first, BuyAt: &first, Status: "active"},
		{RecommendationID: "unused", AnalysisRunID: "second", SelectionRole: "standby", SelectionRank: 2, SignalAt: second, Status: "standby_not_used"},
		{RecommendationID: "waiting", AnalysisRunID: "second", SelectionRole: "standby", SelectionRank: 3, SignalAt: second, Status: "standby"},
		{RecommendationID: "failed-standby", AnalysisRunID: "second", SelectionRole: "standby", SelectionRank: 4, SignalAt: second, Status: "missed_cash"},
		{RecommendationID: "previous", AnalysisRunID: "previous", SelectionRole: "primary", SelectionRank: 1, SignalAt: previous, BuyAt: &previous, Status: "closed"},
	}
	for index, id := range []string{"first", "second", "previous"} {
		if err := r.CreateRun(ctx, &AnalysisRun{RunID: id, TradingDate: "2026-09-08", AttemptNo: index + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CreateRecommendations(ctx, rows); err != nil {
		t.Fatal(err)
	}
	var before []Recommendation
	if err := r.DB().Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	page1, err := r.ListRecommendations(ctx, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	page2, err := r.ListRecommendations(ctx, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	got := append(page1, page2...)
	for index, id := range []string{"first", "second", "later", "previous"} {
		if len(got) != 4 || got[index].RecommendationID != id || got[index].DisplaySelectionRole != "primary" || got[index].DisplaySelectionRank != []int{1, 2, 3, 1}[index] {
			t.Fatalf("daily page = %+v", got)
		}
	}
	if next, err := r.ListRecommendations(ctx, 2, 4); err != nil || len(next) != 0 {
		t.Fatalf("last page = %+v, %v", next, err)
	}
	detail, err := r.GetRecommendation(ctx, "later")
	if err != nil || detail.Recommendation.DisplaySelectionRank != 3 || detail.Recommendation.SelectionRank != 1 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	if _, err := r.GetRecommendation(ctx, "unused"); err != nil {
		t.Fatalf("hidden recommendation remains reachable: %v", err)
	}
	var after []Recommendation
	if err := r.DB().Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("display modified persisted history")
	}
	for _, field := range []string{"display_selection_role", "display_selection_rank"} {
		if r.DB().Migrator().HasColumn(&Recommendation{}, field) {
			t.Fatalf("display field migrated: %s", field)
		}
	}
}

func TestDailySelectionDisplayPendingPromotionAndStableTies(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 10, 3, 0, 0, shanghai())
	early := now.Add(-time.Minute)
	rows := []Recommendation{
		{RecommendationID: "promoted", SelectionRole: "standby", SelectionRank: 4, SignalAt: early, BuyAt: &now, Status: "active", PromotionReason: "递补旧主选"},
		{RecommendationID: "same-buy-later-signal", SelectionRole: "primary", SelectionRank: 1, SignalAt: now, BuyAt: &now, Status: "closed"},
		{RecommendationID: "failed-primary", SelectionRole: "primary", SelectionRank: 1, SignalAt: early, Status: "missed_untradable"},
		{RecommendationID: "pending", SelectionRole: "primary", SelectionRank: 2, SignalAt: early, Status: "buy_pending"},
		{RecommendationID: "standby-one", SelectionRole: "standby", SelectionRank: 3, SignalAt: early, Status: "standby"},
		{RecommendationID: "standby-two", SelectionRole: "standby", SelectionRank: 3, SignalAt: early, Status: "standby"},
		{RecommendationID: "later-standby", SelectionRole: "standby", SelectionRank: 1, SignalAt: now, Status: "standby"},
		{RecommendationID: "never-used", SelectionRole: "standby", SelectionRank: 5, SignalAt: early, Status: "standby_not_used"},
	}
	if err := r.CreateRecommendations(ctx, rows); err != nil {
		t.Fatal(err)
	}
	got, err := r.ListRecommendations(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"promoted", "same-buy-later-signal", "failed-primary", "pending", "standby-one", "standby-two", "later-standby"}
	wantRoles := []string{"primary", "primary", "", "pending", "standby", "standby", "standby"}
	wantRanks := []int{1, 2, 0, 0, 1, 2, 3}
	if len(got) != len(wantIDs) {
		t.Fatalf("rows = %+v", got)
	}
	for i := range got {
		if got[i].RecommendationID != wantIDs[i] || got[i].DisplaySelectionRole != wantRoles[i] || got[i].DisplaySelectionRank != wantRanks[i] {
			t.Fatalf("row %d = %+v", i, got[i])
		}
	}
	if got[0].SelectionRole != "standby" || got[0].PromotionReason != "递补旧主选" {
		t.Fatalf("original promotion lost: %+v", got[0])
	}
	// A later buy takes the next slot, and unused candidates disappear without changing storage.
	later := now.Add(time.Minute)
	if err := r.DB().Model(&Recommendation{}).Where("recommendation_id = ?", "standby-one").Updates(map[string]any{"buy_at": later, "status": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ListRecommendations(ctx, 20, 0)
	if err != nil || len(got) != 5 || got[2].RecommendationID != "standby-one" || got[2].DisplaySelectionRank != 3 {
		t.Fatalf("promoted day = %+v, %v", got, err)
	}
}

func TestDailySelectionDisplayUsesShanghaiBuyDateAndIDTieBreaker(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	// UTC timestamps must be grouped in the same Shanghai trading day as local timestamps.
	utc := time.Date(2026, 9, 8, 2, 3, 0, 0, time.UTC)
	local := utc.In(shanghai())
	oldSignal := local.AddDate(0, 0, -1)
	rows := []Recommendation{
		{RecommendationID: "id-first", SelectionRole: "primary", SelectionRank: 1, SignalAt: oldSignal, BuyAt: &utc, Status: "closed"},
		{RecommendationID: "id-second", SelectionRole: "primary", SelectionRank: 1, SignalAt: oldSignal, BuyAt: &local, Status: "active"},
	}
	if err := r.CreateRecommendations(ctx, rows); err != nil {
		t.Fatal(err)
	}
	got, err := r.ListRecommendations(ctx, 10, 0)
	if err != nil || len(got) != 2 || got[0].RecommendationID != "id-first" || got[1].DisplaySelectionRank != 2 {
		t.Fatalf("timezone/ties = %+v, %v", got, err)
	}
}
