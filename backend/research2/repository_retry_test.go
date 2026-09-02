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
