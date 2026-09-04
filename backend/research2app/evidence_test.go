package research2app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/research2"
	"go-stock/internal/researchevidence"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type evidenceProviderStub struct {
	evidence research2.Evidence
	err      error
}

func (provider evidenceProviderStub) Collect(context.Context, time.Time) (research2.Evidence, error) {
	return provider.evidence, provider.err
}

func (provider evidenceProviderStub) CollectWithExclusions(context.Context, time.Time, map[string]struct{}) (research2.Evidence, error) {
	return provider.evidence, provider.err
}

type freezeErrorEvidenceRepository struct {
	*marketdata.Repository
	err error
}

func (repository freezeErrorEvidenceRepository) FreezeBatch(ctx context.Context, evidenceSetID string, frozenAt time.Time) (marketdata.EvidenceBatch, error) {
	batch, err := repository.Repository.FreezeBatch(ctx, evidenceSetID, frozenAt)
	return batch, errors.Join(err, repository.err)
}

type retryFreezeEvidenceRepository struct {
	*marketdata.Repository
	err   error
	calls int
}

func (repository *retryFreezeEvidenceRepository) FreezeBatch(ctx context.Context, evidenceSetID string, frozenAt time.Time) (marketdata.EvidenceBatch, error) {
	repository.calls++
	if repository.calls == 1 {
		return marketdata.EvidenceBatch{}, repository.err
	}
	return repository.Repository.FreezeBatch(ctx, evidenceSetID, frozenAt)
}

type failedFreezeEvidenceRepository struct {
	*marketdata.Repository
	err error
}

func (repository failedFreezeEvidenceRepository) FreezeBatch(context.Context, string, time.Time) (marketdata.EvidenceBatch, error) {
	return marketdata.EvidenceBatch{}, repository.err
}

func newEvidenceTestRepository(t *testing.T) *marketdata.Repository {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&marketdata.EvidenceBatch{}, &marketdata.EvidenceItem{}); err != nil {
		t.Fatal(err)
	}
	return marketdata.NewRepository(database)
}

func buildEvidenceTestItem(document researchevidence.SourceDocument, _ time.Time, index int, used map[string]int) (marketdata.EvidenceItem, error) {
	sourceID := strings.TrimSpace(document.SourceID)
	if sourceID == "" {
		sourceID = fmt.Sprintf("source-%d", index+1)
	}
	used[sourceID]++
	if used[sourceID] > 1 {
		sourceID = fmt.Sprintf("%s-%d-%d", sourceID, used[sourceID], index+1)
	}
	payload, err := marketdata.MarshalPayload(map[string]any{"content": document.Content, "error": document.Error})
	if err != nil {
		return marketdata.EvidenceItem{}, err
	}
	return marketdata.EvidenceItem{
		EvidenceItemID: uuid.NewString(), SourceID: sourceID, SourceName: document.SourceName,
		SourceRef: document.SourceRef, Category: document.Category, AvailableAt: document.AvailableAt,
		CollectedAt: document.CollectedAt, Status: marketdata.StatusOK, Payload: payload,
	}, nil
}

func TestRetryEvidenceWriteRetriesSQLiteBusy(t *testing.T) {
	attempts := 0
	err := retryEvidenceWrite(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked (SQLITE_BUSY)")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestCollectFailureKeepsEvidenceLinkAndFreezesBatch(t *testing.T) {
	repository := newEvidenceTestRepository(t)
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, time.FixedZone("CST", 8*60*60))
	collector := NewDurableEvidenceCollector(evidenceProviderStub{evidence: research2.Evidence{CutoffAt: cutoff}, err: errors.New("collector is unavailable")}, repository, "profile-test", buildEvidenceTestItem)

	evidence, err := collector.CollectForRun(context.Background(), "run-collect-failure", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") {
		t.Fatalf("expected collection failure, got %v", err)
	}
	if evidence.EvidenceSetID == "" || evidence.EvidenceProfileVersion != "profile-test" {
		t.Fatalf("collection failure lost evidence link: %+v", evidence)
	}
	assertEvidenceBatchStatus(t, repository, evidence.EvidenceSetID, marketdata.StatusFrozen)
}

func TestCollectForRunPersistsActualCutoffAndFailureDocuments(t *testing.T) {
	repository := newEvidenceTestRepository(t)
	startedAt := time.Date(2026, 9, 3, 9, 50, 0, 0, time.FixedZone("CST", 8*60*60))
	actualCutoff := startedAt.Add(4 * time.Second)
	provider := evidenceProviderStub{evidence: research2.Evidence{CutoffAt: actualCutoff, Documents: []researchevidence.SourceDocument{{
		SourceID: "market", SourceName: "市场快照", Category: "market", AvailableAt: &actualCutoff, CollectedAt: actualCutoff,
	}}}, err: errors.New("auxiliary collection failed")}
	collector := NewDurableEvidenceCollector(provider, repository, "research2-trailing5-v7", buildEvidenceTestItem)

	evidence, err := collector.CollectForRun(context.Background(), "run-actual-cutoff", startedAt)
	if err == nil || !strings.Contains(err.Error(), "auxiliary collection failed") {
		t.Fatalf("collection failure not retained: %v", err)
	}
	batch, err := repository.Batch(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.CutoffAt.Equal(actualCutoff) || !batch.CutoffAt.Equal(actualCutoff) || batch.Status != marketdata.StatusFrozen {
		t.Fatalf("actual cutoff/frozen state not persisted: evidence=%+v batch=%+v", evidence, batch)
	}
	items, err := repository.Items(context.Background(), evidence.EvidenceSetID)
	if err != nil || len(items) != 1 || items[0].SourceID != "market" {
		t.Fatalf("failure documents were not archived: items=%+v err=%v", items, err)
	}
}

func TestDurableFreezeErrorKeepsFrozenLink(t *testing.T) {
	repository := newEvidenceTestRepository(t)
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, time.UTC)
	freezeErr := errors.New("freeze blocked after durable freeze")
	collector := NewDurableEvidenceCollector(
		evidenceProviderStub{evidence: research2.Evidence{CutoffAt: cutoff}, err: errors.New("collector is unavailable")},
		freezeErrorEvidenceRepository{Repository: repository, err: freezeErr}, "profile-test", buildEvidenceTestItem,
	)

	evidence, err := collector.CollectForRun(context.Background(), "run-freeze-failure", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") || strings.Contains(err.Error(), freezeErr.Error()) {
		t.Fatalf("durably frozen batch returned the wrong failure: %v", err)
	}
	assertEvidenceBatchStatus(t, repository, evidence.EvidenceSetID, marketdata.StatusFrozen)
}

func TestFreezeRetriesWithIndependentContext(t *testing.T) {
	repository := newEvidenceTestRepository(t)
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, time.UTC)
	retrying := &retryFreezeEvidenceRepository{Repository: repository, err: errors.New("first freeze did not reach storage")}
	collector := NewDurableEvidenceCollector(evidenceProviderStub{evidence: research2.Evidence{CutoffAt: cutoff}, err: errors.New("collector is unavailable")}, retrying, "profile-test", buildEvidenceTestItem)

	evidence, err := collector.CollectForRun(context.Background(), "run-freeze-retry", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") || retrying.calls != 2 {
		t.Fatalf("freeze retry result: calls=%d err=%v", retrying.calls, err)
	}
	assertEvidenceBatchStatus(t, repository, evidence.EvidenceSetID, marketdata.StatusFrozen)
}

func TestRepeatedFreezeFailureSealsTerminalBatch(t *testing.T) {
	repository := newEvidenceTestRepository(t)
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, time.UTC)
	freezeErr := errors.New("freeze storage unavailable")
	collector := NewDurableEvidenceCollector(evidenceProviderStub{evidence: research2.Evidence{CutoffAt: cutoff}, err: errors.New("collector is unavailable")}, failedFreezeEvidenceRepository{Repository: repository, err: freezeErr}, "profile-test", buildEvidenceTestItem)

	evidence, err := collector.CollectForRun(context.Background(), "run-freeze-terminal", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") || !strings.Contains(err.Error(), freezeErr.Error()) {
		t.Fatalf("collection and terminal freeze failures were not joined: %v", err)
	}
	assertEvidenceBatchStatus(t, repository, evidence.EvidenceSetID, marketdata.StatusFailed)
	if err = repository.AppendItems(context.Background(), evidence.EvidenceSetID, []marketdata.EvidenceItem{{SourceID: "late", SourceName: "late", Category: "market"}}); !errors.Is(err, marketdata.ErrEvidenceBatchFrozen) {
		t.Fatalf("terminal failed batch accepted a late append: %v", err)
	}
}

func assertEvidenceBatchStatus(t *testing.T, repository *marketdata.Repository, evidenceSetID, status string) {
	t.Helper()
	batch, err := repository.Batch(context.Background(), evidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != status || batch.FrozenAt == nil {
		t.Fatalf("batch status=%s frozenAt=%v, want %s terminal", batch.Status, batch.FrozenAt, status)
	}
}
