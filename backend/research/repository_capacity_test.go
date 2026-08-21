package research

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestCapacityAdmissionRollsBackRecommendationMemoryAndInitialEventTogether(t *testing.T) {
	repo := researchTestRepo(t)
	if err := repo.DB().Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 500000.0).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, shanghaiLocation)
	if err := repo.AppendDecision(context.Background(), &DecisionEvent{EventID: "duplicate-event", RecommendationID: "other", DecisionType: "测试", DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	recommendation := Recommendation{RecommendationID: "atomic-admission", AnalysisRunID: "run", StockCode: "sh600000",
		StockName: "浦发银行", SignalAt: now, Status: "buy_pending", ReservedCash: MaxCashPerTrade}
	messages := []LifecycleMessage{{RecommendationID: recommendation.RecommendationID, Sequence: 1, Role: "system", Phase: "initial", Content: "memory", CreatedAt: now}}
	err := repo.CreateRecommendationWithinCapacity(context.Background(), &recommendation, messages,
		&DecisionEvent{EventID: "duplicate-event", RecommendationID: recommendation.RecommendationID, DecisionType: "待买入", DecidedAt: now})
	if err == nil {
		t.Fatal("expected duplicate event to roll back admission")
	}
	var recommendations, storedMessages int64
	_ = repo.DB().Model(&Recommendation{}).Where("recommendation_id = ?", recommendation.RecommendationID).Count(&recommendations).Error
	_ = repo.DB().Model(&LifecycleMessage{}).Where("recommendation_id = ?", recommendation.RecommendationID).Count(&storedMessages).Error
	if recommendations != 0 || storedMessages != 0 {
		t.Fatalf("partial admission leaked: recommendations=%d messages=%d", recommendations, storedMessages)
	}
}

func TestCapacityAdmissionAcrossTwoRepositoriesNeverExceedsTen(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "capacity.db"))
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	open := func() *gorm.DB {
		database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := database.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		return database
	}
	databaseA := open()
	if err := databaseA.AutoMigrate(&Recommendation{}, &LifecycleMessage{}, &DecisionEvent{}, &SimulatedAccount{}, &Position{}); err != nil {
		t.Fatal(err)
	}
	repoA := NewRepository(databaseA)
	if err := repoA.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := databaseA.Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 500000.0).Error; err != nil {
		t.Fatal(err)
	}
	repositories := []*Repository{repoA, NewRepository(open())}
	now := time.Date(2026, 8, 19, 15, 30, 0, 0, shanghaiLocation)
	var wait sync.WaitGroup
	var resultMu sync.Mutex
	accepted, rejected := 0, 0
	unexpected := make([]error, 0)
	for index := 0; index < 20; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			recommendation := Recommendation{RecommendationID: fmt.Sprintf("multi-repo-%02d", index), AnalysisRunID: "run",
				StockCode: fmt.Sprintf("sh%06d", 600000+index), StockName: fmt.Sprintf("股票%d", index), SignalAt: now,
				Status: "buy_pending", ReservedCash: MaxCashPerTrade}
			err := repositories[index%len(repositories)].CreateRecommendationWithinCapacity(context.Background(), &recommendation, nil,
				&DecisionEvent{EventID: fmt.Sprintf("multi-repo-event-%02d", index), RecommendationID: recommendation.RecommendationID, DecisionType: "待买入", DecidedAt: now})
			resultMu.Lock()
			defer resultMu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, ErrCapacityReached):
				rejected++
			default:
				unexpected = append(unexpected, err)
			}
		}()
	}
	wait.Wait()
	capacity, err := repoA.RecommendationCapacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(unexpected) != 0 || accepted != 10 || rejected != 10 || capacity.ExposureCount != 10 || capacity.ReservedCash != 500000 {
		t.Fatalf("accepted=%d rejected=%d unexpected=%v capacity=%+v", accepted, rejected, unexpected, capacity)
	}
}

func TestCapacityAllowsSubTargetCashAndRejectsReservationsBeyondUnreservedCash(t *testing.T) {
	repo := researchTestRepo(t)
	if err := repo.DB().Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 49000.0).Error; err != nil {
		t.Fatal(err)
	}
	capacity, err := repo.RecommendationCapacity(context.Background())
	if err != nil || capacity.AllowedNew != 2 || capacity.UnreservedCash != 49000 {
		t.Fatalf("capacity=%+v err=%v", capacity, err)
	}
	if err := repo.DB().Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 100000.0).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 14, 0, 0, 0, shanghaiLocation)
	first := Recommendation{RecommendationID: "reserved-low", AnalysisRunID: "run", StockCode: "sh600000", StockName: "浦发银行", SignalAt: now, Status: "buy_pending", ReservedCash: 60000}
	if err := repo.CreateRecommendationWithinCapacity(context.Background(), &first, nil, &DecisionEvent{EventID: "reserved-low-event", RecommendationID: first.RecommendationID, DecisionType: "待买入", DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	second := Recommendation{RecommendationID: "reserved-over", AnalysisRunID: "run", StockCode: "sh600001", StockName: "浦发银行", SignalAt: now, Status: "buy_pending", ReservedCash: 50000}
	err = repo.CreateRecommendationWithinCapacity(context.Background(), &second, nil, &DecisionEvent{EventID: "reserved-over-event", RecommendationID: second.RecommendationID, DecisionType: "待买入", DecidedAt: now})
	if !errors.Is(err, ErrInsufficientCash) {
		t.Fatalf("err=%v, want ErrInsufficientCash", err)
	}
}
