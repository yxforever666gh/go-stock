package research2

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func displayRun(t *testing.T, r *Repository, id string, at time.Time, attempt int, status string) {
	t.Helper()
	run := AnalysisRun{RunID: id, TradingDate: at.In(shanghai()).Format("2006-01-02"), AttemptNo: attempt, Status: status, StartedAt: at, ScheduledFor: at}
	if status != "running" {
		run.GeneratedAt = &at
	}
	if err := r.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
}

func TestDailySelectionCountsTiesAndPagination(t *testing.T) {
	for buys := 0; buys <= 3; buys++ {
		t.Run(fmt.Sprint(buys), func(t *testing.T) {
			r := research2TestRepository(t)
			ctx := context.Background()
			at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
			displayRun(t, r, "old", at, 1, "success")
			displayRun(t, r, "new", at.Add(time.Minute), 2, "no_recommendation")
			var rows []Recommendation
			for i := 0; i < buys; i++ {
				boughtAt := at.Add(time.Duration(i) * time.Second)
				rows = append(rows, Recommendation{RecommendationID: fmt.Sprintf("buy%d", i), AnalysisRunID: "old", StockCode: fmt.Sprintf("sh60000%d", i), SelectionRole: "primary", SelectionRank: i + 1, SignalAt: at, BuyAt: &boughtAt, Status: "active"})
			}
			for i, score := range []float64{49, 48, 48, 47} {
				rows = append(rows, Recommendation{RecommendationID: fmt.Sprintf("candidate%d", i), AnalysisRunID: "new", StockCode: fmt.Sprintf("sz00000%d", i), SelectionRole: "observation", SelectionRank: i + 1, SignalAt: at.Add(time.Minute), FinalScore: score, Status: "analysis_only", Summary: "original"})
			}
			if err := r.CreateRecommendations(ctx, rows); err != nil {
				t.Fatal(err)
			}
			var before []Recommendation
			if err := r.DB().Order("id").Find(&before).Error; err != nil {
				t.Fatal(err)
			}
			got, err := r.ListRecommendations(ctx, 100, 0)
			counts := []int{3, 4, 3, 3}
			if err != nil || len(got) != counts[buys] {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			for i, row := range got {
				role, rank := "primary", i+1
				if i >= buys {
					role = "candidate"
					rank = buys + []int{1, 2, 2}[i-buys]
				}
				if row.DisplaySelectionRole != role || row.DisplaySelectionRank != rank {
					t.Fatalf("row=%+v", row)
				}
				detail, err := r.GetRecommendation(ctx, row.RecommendationID)
				if err != nil || detail.Recommendation.DisplaySelectionRank != rank {
					t.Fatalf("detail=%+v err=%v", detail, err)
				}
			}
			for offset := 0; offset < len(got); offset++ {
				page, err := r.ListRecommendations(ctx, 1, offset)
				if err != nil || len(page) != 1 || page[0].RecommendationID != got[offset].RecommendationID || page[0].DisplaySelectionRank != got[offset].DisplaySelectionRank {
					t.Fatalf("page=%+v %v", page, err)
				}
			}
			var after []Recommendation
			if err := r.DB().Order("id").Find(&after).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("display modified history")
			}
			for _, field := range []string{"display_selection_role", "display_selection_rank"} {
				if r.DB().Migrator().HasColumn(&Recommendation{}, field) {
					t.Fatalf("display field migrated: %s", field)
				}
			}
		})
	}
}

func TestDailySelectionLatestCompletedRunAndExactScores(t *testing.T) {
	r := research2TestRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, shanghai())
	displayRun(t, r, "old", at, 1, "success")
	displayRun(t, r, "new", at.Add(time.Minute), 2, "running")
	utc := at.UTC()
	rows := []Recommendation{
		{RecommendationID: "bought", AnalysisRunID: "old", StockCode: "sh600001", SignalAt: at, BuyAt: &utc, Status: "active"},
		{RecommendationID: "duplicate-buy", AnalysisRunID: "old", StockCode: "sh600001", SignalAt: at, BuyAt: &at, Status: "closed"},
		{RecommendationID: "old-candidate", AnalysisRunID: "old", StockCode: "sh600002", SignalAt: at, Status: "analysis_only", SelectionRole: "observation", FinalScore: 49},
		{RecommendationID: "already-bought", AnalysisRunID: "new", StockCode: "sh600001", SignalAt: at, Status: "analysis_only", SelectionRole: "observation", FinalScore: 50},
		{RecommendationID: "new1", AnalysisRunID: "new", StockCode: "sh600003", SignalAt: at, Status: "analysis_only", SelectionRole: "observation", FinalScore: 49.04},
		{RecommendationID: "new2", AnalysisRunID: "new", StockCode: "sh600004", SignalAt: at, Status: "analysis_only", SelectionRole: "observation", FinalScore: 49.03},
		{RecommendationID: "new3", AnalysisRunID: "new", StockCode: "sh600005", SignalAt: at, Status: "analysis_only", SelectionRole: "observation", FinalScore: 49.02},
		{RecommendationID: "failed", AnalysisRunID: "new", StockCode: "sh600006", SignalAt: at, Status: "missed_untradable", FinalScore: 99},
	}
	if err := r.CreateRecommendations(ctx, rows); err != nil {
		t.Fatal(err)
	}
	check := func(ids ...string) {
		t.Helper()
		got, err := r.ListRecommendations(ctx, 100, 0)
		var actual []string
		for _, row := range got {
			actual = append(actual, row.RecommendationID)
		}
		if err != nil || !reflect.DeepEqual(actual, ids) {
			t.Fatalf("got %v want %v err=%v", actual, ids, err)
		}
	}
	check("bought", "old-candidate")
	if err := r.DB().Model(&AnalysisRun{}).Where("run_id = ?", "new").Update("status", "no_recommendation").Error; err != nil {
		t.Fatal(err)
	}
	check("bought", "new1", "new2")
	// A new empty completed run clears old candidates instead of inventing scores.
	displayRun(t, r, "empty", at.Add(2*time.Minute), 3, "no_recommendation")
	check("bought")
}
