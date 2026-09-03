package research2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestResearch2TransactionRetriesSQLiteBusy(t *testing.T) {
	repository := research2TestRepository(t)
	attempts := 0
	err := research2TransactionWithWriteRetry(context.Background(), repository.DB(), func(_ *gorm.DB) error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked (SQLITE_BUSY)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d want=3", attempts)
	}
}

func TestCreateRunAttemptAllowsOnlyOneFailedRunRetry(t *testing.T) {
	repository := research2TestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghai())
	newRun := func(id string) *AnalysisRun {
		return &AnalysisRun{RunID: id, TradingDate: "2026-09-03", ScheduledFor: now, StartedAt: now, EvidenceCutoffAt: now, Status: "running", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	}

	first, created, err := repository.CreateRunAttempt(ctx, newRun(uuid.NewString()), true)
	if err != nil || !created || first.AttemptNo != 1 {
		t.Fatalf("first=%+v created=%v err=%v", first, created, err)
	}
	first.Status = "failed"
	if err = repository.SaveRun(ctx, &first); err != nil {
		t.Fatal(err)
	}

	second, created, err := repository.CreateRunAttempt(ctx, newRun(uuid.NewString()), true)
	if err != nil || !created || second.AttemptNo != 2 || second.RunID == first.RunID {
		t.Fatalf("second=%+v created=%v err=%v", second, created, err)
	}
	second.Status = "failed"
	if err = repository.SaveRun(ctx, &second); err != nil {
		t.Fatal(err)
	}

	latest, created, err := repository.CreateRunAttempt(ctx, newRun(uuid.NewString()), true)
	if err != nil || created || latest.RunID != second.RunID {
		t.Fatalf("latest=%+v created=%v err=%v", latest, created, err)
	}
	lookup, exists, err := repository.RunForDate(ctx, "2026-09-03")
	if err != nil || !exists || lookup.RunID != second.RunID {
		t.Fatalf("lookup=%+v exists=%v err=%v", lookup, exists, err)
	}
}

func TestCreateRunAttemptDoesNotRetryFailedRunOutsideWindow(t *testing.T) {
	repository := research2TestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghai())
	first := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-09-03", ScheduledFor: now, StartedAt: now, EvidenceCutoffAt: now, Status: "failed", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	stored, created, err := repository.CreateRunAttempt(ctx, &first, true)
	if err != nil || !created {
		t.Fatalf("stored=%+v created=%v err=%v", stored, created, err)
	}
	if err = repository.SaveRun(ctx, &first); err != nil {
		t.Fatal(err)
	}
	candidate := AnalysisRun{RunID: uuid.NewString(), TradingDate: first.TradingDate, ScheduledFor: now, StartedAt: now.Add(2 * time.Hour), EvidenceCutoffAt: now.Add(2 * time.Hour), Status: "running", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	latest, created, err := repository.CreateRunAttempt(ctx, &candidate, false)
	if err != nil || created || latest.RunID != first.RunID || latest.AttemptNo != 1 {
		t.Fatalf("latest=%+v created=%v err=%v", latest, created, err)
	}
}

func TestFinalizeRunRollsBackRunWhenRecommendationsFail(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, shanghai())
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now, StartedAt: now, EvidenceCutoffAt: now, Status: "running", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	run.Status = "success"
	recommendationID := uuid.NewString()
	items := []Recommendation{
		{RecommendationID: recommendationID, AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "one", SignalAt: now, FinalScore: 60, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now},
		{RecommendationID: recommendationID, AnalysisRunID: run.RunID, StockCode: "sz000001", StockName: "two", SignalAt: now, FinalScore: 60, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now},
	}
	if err := repository.FinalizeRun(context.Background(), &run, items); err == nil {
		t.Fatal("expected duplicate recommendation to fail finalization")
	}
	var stored AnalysisRun
	if err := repository.DB().Where("run_id = ?", run.RunID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := repository.DB().Model(&Recommendation{}).Where("analysis_run_id = ?", run.RunID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "running" || count != 0 {
		t.Fatalf("finalization was not atomic: run=%+v recommendations=%d", stored, count)
	}
}

func TestDueRecommendationsRequireSuccessfulRun(t *testing.T) {
	repository := research2TestRepository(t)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, shanghai())
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now, StartedAt: now, EvidenceCutoffAt: now, Status: "running", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "test", SignalAt: now, FinalScore: 60, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	due, err := repository.DueRecommendations(context.Background(), now, []string{"buy_pending"})
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("running analysis exposed executable recommendations: %+v", due)
	}
	run.Status = "success"
	if err = repository.SaveRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	due, err = repository.DueRecommendations(context.Background(), now, []string{"buy_pending"})
	if err != nil || len(due) != 1 {
		t.Fatalf("successful analysis did not expose recommendation: due=%+v err=%v", due, err)
	}
}
