package research

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type sqliteCodeTestError struct{ code int }

func (err *sqliteCodeTestError) Error() string { return fmt.Sprintf("sqlite code %d", err.code) }
func (err *sqliteCodeTestError) Code() int     { return err.code }

func openResearchWALTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(t.TempDir(), name))
	dsn := "file:" + path + "?_pragma=busy_timeout(1)&_pragma=journal_mode(WAL)"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(10)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return database
}

func migrateResearchWriteTestDB(t *testing.T, database *gorm.DB) {
	t.Helper()
	if err := database.AutoMigrate(&AnalysisRun{}, &Recommendation{}, &LifecycleMessage{}, &DecisionEvent{},
		&LifecycleObservation{}, &SimulatedAccount{}, &SimulatedTrade{}, &Position{}, &AnalysisTrigger{}, &BuyOpportunity{}); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteBusyRecognitionIncludesExtendedCodes(t *testing.T) {
	for _, code := range []int{5, 6, 261, 262, 517, 518, 773} {
		if !isSQLiteBusy(&sqliteCodeTestError{code: code}) {
			t.Fatalf("code %d was not recognized as retryable", code)
		}
	}
	for _, err := range []error{
		errors.New("SQLITE_BUSY_SNAPSHOT"),
		errors.New("database is locked (5)"),
		errors.New("database table is locked"),
		errors.New("database is locked (517)"),
	} {
		if !isSQLiteBusy(err) {
			t.Fatalf("error %q was not recognized as retryable", err)
		}
	}
	if isSQLiteBusy(errors.New("unique constraint failed")) {
		t.Fatal("non-locking error must not be retried")
	}
}

func TestTransactionWithWriteRetryUsesAllFiveBackoffs(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	err = transactionWithWriteRetry(context.Background(), database, func(*gorm.DB) error {
		if attempts.Add(1) <= int32(len(writeRetryDelays)) {
			return &sqliteCodeTestError{code: 517}
		}
		return nil
	})
	if err != nil || attempts.Load() != int32(len(writeRetryDelays)+1) {
		t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
	}
}

func TestRepositoryWriteRetrySurvivesExternalWALLock(t *testing.T) {
	database := openResearchWALTestDB(t, "external-lock.db")
	migrateResearchWriteTestDB(t, database)
	repo := NewRepository(database)
	ctx := context.Background()
	if err := repo.EnsureAccount(ctx); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	locker, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locker.ExecContext(ctx, "UPDATE research_v160_simulated_accounts SET cash = cash WHERE id = 1"); err != nil {
		_ = locker.Rollback()
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(75 * time.Millisecond)
		_ = locker.Rollback()
		close(released)
	}()

	event := DecisionEvent{EventID: "after-external-lock", RecommendationID: "rec", DecisionType: "测试", DecidedAt: time.Now()}
	if err = repo.AppendDecision(ctx, &event); err != nil {
		t.Fatalf("write was not recovered after WAL lock: %v", err)
	}
	<-released
	var count int64
	if err = database.Model(&DecisionEvent{}).Where("event_id = ?", event.EventID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("stored events=%d err=%v", count, err)
	}
}

func TestFiveConcurrentLifecycleWritesPreserveSequencesTradesAndCash(t *testing.T) {
	database := openResearchWALTestDB(t, "lifecycle-writes.db")
	migrateResearchWriteTestDB(t, database)
	repo := NewRepository(database)
	ctx := context.Background()
	if err := repo.EnsureAccount(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, shanghaiLocation)
	recommendations := make([]Recommendation, 5)
	for index := range recommendations {
		recommendations[index] = Recommendation{
			RecommendationID: fmt.Sprintf("wal-lifecycle-%d", index), AnalysisRunID: "wal-run",
			StockCode: fmt.Sprintf("sh%06d", 600000+index), StockName: fmt.Sprintf("股票%d", index),
			SignalAt: now, Status: "buy_pending", ReservedCash: TargetCashPerTrade,
		}
		if err := repo.CreateRecommendation(ctx, &recommendations[index], nil); err != nil {
			t.Fatal(err)
		}
	}

	errorsCh := make(chan error, len(recommendations)*5)
	var wait sync.WaitGroup
	for index := range recommendations {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			recommendation := recommendations[index]
			message := LifecycleMessage{RecommendationID: recommendations[0].RecommendationID, Role: "user", Phase: "holding", Content: fmt.Sprintf("并发消息%d", index), CreatedAt: now}
			if err := repo.AppendMessage(ctx, &message); err != nil {
				errorsCh <- fmt.Errorf("append message %d: %w", index, err)
				return
			}
			observation := LifecycleObservation{ObservationID: fmt.Sprintf("wal-observation-%d", index), RecommendationID: recommendation.RecommendationID,
				Phase: "holding", WindowFrom: now.Add(-15 * time.Minute), ObservedAt: now, Status: "ready",
				QuoteJSON: "{}", MinuteSummaryJSON: "{}", EvidenceJSON: "[]", SourceStatusJSON: "[]"}
			if err := repo.AppendObservation(ctx, &observation); err != nil {
				errorsCh <- fmt.Errorf("append observation %d: %w", index, err)
				return
			}
			if err := repo.MarkObservationModelInvoked(ctx, observation.ObservationID); err != nil {
				errorsCh <- fmt.Errorf("mark observation %d: %w", index, err)
				return
			}
			if err := repo.AppendDecision(ctx, &DecisionEvent{EventID: fmt.Sprintf("wal-decision-%d", index), RecommendationID: recommendation.RecommendationID,
				DecisionType: "持有", DecidedAt: now}); err != nil {
				errorsCh <- fmt.Errorf("append decision %d: %w", index, err)
				return
			}
			quote := Quote{Code: recommendation.StockCode, Name: recommendation.StockName, Market: "SH", Price: 10 + float64(index), At: now.Add(time.Duration(index) * time.Second)}
			if err := repo.Buy(ctx, recommendation.RecommendationID, quote, now.Add(15*time.Minute), now); err != nil {
				errorsCh <- fmt.Errorf("buy %d: %w", index, err)
			}
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if t.Failed() {
		return
	}

	var messages []LifecycleMessage
	if err := database.Where("recommendation_id = ?", recommendations[0].RecommendationID).Order("sequence ASC").Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != len(recommendations) {
		t.Fatalf("messages=%d want=%d", len(messages), len(recommendations))
	}
	for index, message := range messages {
		if message.Sequence != index+1 {
			t.Fatalf("message[%d].sequence=%d", index, message.Sequence)
		}
	}

	var trades []SimulatedTrade
	if err := database.Where("side = ?", "buy").Find(&trades).Error; err != nil {
		t.Fatal(err)
	}
	tradeIDs := make(map[string]struct{}, len(trades))
	expectedCash := InitialCash
	for _, trade := range trades {
		tradeIDs[trade.TradeID] = struct{}{}
		expectedCash += trade.NetCashFlow
	}
	if len(trades) != len(recommendations) || len(tradeIDs) != len(recommendations) {
		t.Fatalf("trades=%d distinct trade IDs=%d", len(trades), len(tradeIDs))
	}
	var positions int64
	if err := database.Model(&Position{}).Where("status = ?", "open").Count(&positions).Error; err != nil || positions != int64(len(recommendations)) {
		t.Fatalf("open positions=%d err=%v", positions, err)
	}
	account, err := repo.Account(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(account.Cash-expectedCash) > 1e-7 {
		t.Fatalf("cash=%0.10f want=%0.10f", account.Cash, expectedCash)
	}
	var invoked int64
	if err := database.Model(&LifecycleObservation{}).Where("model_invoked = ?", true).Count(&invoked).Error; err != nil || invoked != int64(len(recommendations)) {
		t.Fatalf("model-invoked observations=%d err=%v", invoked, err)
	}
}

func TestWaitOpportunitiesAreSupersededOnlyByExplicitSuccessor(t *testing.T) {
	database := openResearchWALTestDB(t, "waits.db")
	migrateResearchWriteTestDB(t, database)
	repo := NewRepository(database)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 14, 0, 0, 0, shanghaiLocation)
	rows := []BuyOpportunity{
		{OpportunityID: "prior-wait", AnalysisRunID: "prior", Action: OpportunityActionWait, StockCode: "sh600000", StockName: "甲", Status: "active", CreatedAt: now.Add(-time.Minute)},
		{OpportunityID: "current-wait", AnalysisRunID: "current", Action: OpportunityActionWait, StockCode: "sh600001", StockName: "乙", Status: "active", CreatedAt: now.Add(time.Minute)},
		{OpportunityID: "prior-reject", AnalysisRunID: "prior", Action: OpportunityActionReject, StockCode: "sh600002", StockName: "丙", Status: "active", CreatedAt: now.Add(-time.Minute)},
	}
	for index := range rows {
		if err := repo.CreateBuyOpportunity(ctx, &rows[index]); err != nil {
			t.Fatal(err)
		}
	}
	active, err := repo.ActiveWaitOpportunities(ctx, now, 10)
	if err != nil || len(active) != 1 || active[0].OpportunityID != "prior-wait" {
		t.Fatalf("active waits=%+v err=%v", active, err)
	}
	if err = repo.SupersedeWaitOpportunities(ctx, []string{"prior-wait"}, "successor", now); err != nil {
		t.Fatal(err)
	}
	// Repeating a completed successor update must remain harmless.
	if err = repo.SupersedeWaitOpportunities(ctx, []string{"prior-wait"}, "successor", now); err != nil {
		t.Fatal(err)
	}
	var stored BuyOpportunity
	if err = database.Where("opportunity_id = ?", "prior-wait").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "superseded" || stored.SupersededAt == nil || stored.SupersededByRunID != "successor" {
		t.Fatalf("superseded wait=%+v", stored)
	}
	active, err = repo.ActiveWaitOpportunities(ctx, time.Time{}, 10)
	if err != nil || len(active) != 1 || active[0].OpportunityID != "current-wait" {
		t.Fatalf("remaining active waits=%+v err=%v", active, err)
	}
}
