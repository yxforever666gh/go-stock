package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/research"
	"go-stock/backend/research2"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type freezeErrorEvidenceRepository struct {
	*marketdata.Repository
	err error
}

func (r freezeErrorEvidenceRepository) FreezeBatch(ctx context.Context, evidenceSetID string, frozenAt time.Time) (marketdata.EvidenceBatch, error) {
	batch, err := r.Repository.FreezeBatch(ctx, evidenceSetID, frozenAt)
	return batch, errors.Join(err, r.err)
}

type retryFreezeEvidenceRepository struct {
	*marketdata.Repository
	err   error
	calls int
}

func (r *retryFreezeEvidenceRepository) FreezeBatch(ctx context.Context, evidenceSetID string, frozenAt time.Time) (marketdata.EvidenceBatch, error) {
	r.calls++
	if r.calls == 1 {
		return marketdata.EvidenceBatch{}, r.err
	}
	return r.Repository.FreezeBatch(ctx, evidenceSetID, frozenAt)
}

type failedFreezeEvidenceRepository struct {
	*marketdata.Repository
	err error
}

func (r failedFreezeEvidenceRepository) FreezeBatch(context.Context, string, time.Time) (marketdata.EvidenceBatch, error) {
	return marketdata.EvidenceBatch{}, r.err
}

func research2EvidenceTestRepository(t *testing.T) *marketdata.Repository {
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

func TestValidateResearch2CandidateCutoff(t *testing.T) {
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, shanghaiDataLocation())
	if err := validateResearch2CandidateCutoff(true, cutoff.Add(time.Nanosecond), cutoff); err == nil {
		t.Fatal("experimental mode accepted an after-cutoff candidate snapshot")
	}
	if err := validateResearch2CandidateCutoff(true, cutoff, cutoff); err != nil {
		t.Fatalf("cutoff-equal candidate rejected: %v", err)
	}
	if err := validateResearch2CandidateCutoff(false, cutoff.Add(time.Minute), cutoff); err != nil {
		t.Fatalf("legacy mode changed: %v", err)
	}
}

func TestResearchEvidenceSourceIdentityAndSummary(t *testing.T) {
	document := research.SourceDocument{SourceID: "S001", SourceName: "东方财富", Category: "market", Content: "候选输入与关键行情字段"}
	used := map[string]int{}
	if got := uniqueResearchEvidenceSourceID(document, 0, used); got != "S001" {
		t.Fatalf("source id not preserved: %q", got)
	}
	if got := uniqueResearchEvidenceSourceID(document, 1, used); got == "S001" || !strings.HasPrefix(got, "S001-") {
		t.Fatalf("duplicate source id not disambiguated: %q", got)
	}
	summary := researchEvidenceDocumentSummary(document)
	if !strings.Contains(summary, "sourceId=S001") || !strings.Contains(summary, "候选输入") {
		t.Fatalf("summary is not traceable or content-bearing: %q", summary)
	}
}

func TestResearch2CollectFailureKeepsEvidenceLinkAndFreezesBatch(t *testing.T) {
	repository := research2EvidenceTestRepository(t)
	collector := &Research2EvidenceCollector{evidence: repository, evidenceProfile: "profile-test"}
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, shanghaiDataLocation())

	evidence, err := collector.CollectForRun(context.Background(), "run-collect-failure", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") {
		t.Fatalf("expected collection failure, got %v", err)
	}
	if evidence.EvidenceSetID == "" || evidence.EvidenceProfileVersion != "profile-test" {
		t.Fatalf("collection failure lost evidence link: %+v", evidence)
	}
	batch, err := repository.Batch(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != marketdata.StatusFrozen || batch.FrozenAt == nil {
		t.Fatalf("failed collection left batch collecting: %+v", batch)
	}
}

func TestResearch2CollectForRunCreatesBatchWithActualCutoff(t *testing.T) {
	repository := research2EvidenceTestRepository(t)
	startedAt := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghaiDataLocation())
	actualCutoff := startedAt.Add(4 * time.Second)
	collector := &Research2EvidenceCollector{
		evidence: repository, evidenceProfile: research2EvidenceProfileV6,
		collectEvidence: func(context.Context, time.Time) (research2.Evidence, error) {
			return research2.Evidence{CutoffAt: actualCutoff, Documents: []research.SourceDocument{{
				SourceID: "market", SourceName: "市场快照", Category: "market", AvailableAt: &actualCutoff, CollectedAt: actualCutoff,
			}}}, errors.New("auxiliary collection failed")
		},
	}

	evidence, err := collector.CollectForRun(context.Background(), "run-actual-cutoff", startedAt)
	if err == nil || !strings.Contains(err.Error(), "auxiliary collection failed") {
		t.Fatalf("collection failure not retained: %v", err)
	}
	batch, err := repository.Batch(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.CutoffAt.Equal(actualCutoff) || !batch.CutoffAt.Equal(actualCutoff) {
		t.Fatalf("actual cutoff not persisted: evidence=%s batch=%s", evidence.CutoffAt, batch.CutoffAt)
	}
	if batch.EvidenceProfileVersion != research2EvidenceProfileV6 || batch.Status != marketdata.StatusFrozen {
		t.Fatalf("unexpected frozen batch: %+v", batch)
	}
	items, err := repository.Items(context.Background(), evidence.EvidenceSetID)
	if err != nil || len(items) != 1 || items[0].SourceID != "market" {
		t.Fatalf("failure documents were not archived: items=%+v err=%v", items, err)
	}
}

func TestResearch2DurableFreezeIgnoresProviderErrorAndKeepsFrozenLink(t *testing.T) {
	repository := research2EvidenceTestRepository(t)
	freezeErr := errors.New("freeze blocked after durable freeze")
	collector := &Research2EvidenceCollector{evidence: freezeErrorEvidenceRepository{Repository: repository, err: freezeErr}, evidenceProfile: "profile-test"}
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, shanghaiDataLocation())

	evidence, err := collector.CollectForRun(context.Background(), "run-freeze-failure", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") || strings.Contains(err.Error(), freezeErr.Error()) {
		t.Fatalf("durably frozen batch should only report the collection failure: %v", err)
	}
	if evidence.EvidenceSetID == "" || evidence.EvidenceProfileVersion != "profile-test" {
		t.Fatalf("freeze failure lost evidence link: %+v", evidence)
	}
	batch, err := repository.Batch(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != marketdata.StatusFrozen || batch.FrozenAt == nil {
		t.Fatalf("freeze error path left batch collecting: %+v", batch)
	}
}

func TestResearch2FreezeRetriesWithIndependentContext(t *testing.T) {
	repository := research2EvidenceTestRepository(t)
	freezeErr := errors.New("first freeze did not reach storage")
	retrying := &retryFreezeEvidenceRepository{Repository: repository, err: freezeErr}
	collector := &Research2EvidenceCollector{evidence: retrying, evidenceProfile: "profile-test"}
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, shanghaiDataLocation())

	evidence, err := collector.CollectForRun(context.Background(), "run-freeze-retry", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") || strings.Contains(err.Error(), freezeErr.Error()) {
		t.Fatalf("successful freeze retry should recover the transient freeze failure: %v", err)
	}
	if retrying.calls != 2 {
		t.Fatalf("freeze calls=%d want 2", retrying.calls)
	}
	batch, err := repository.Batch(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != marketdata.StatusFrozen || batch.FrozenAt == nil {
		t.Fatalf("freeze retry left batch collecting: %+v", batch)
	}
}

func TestResearch2RepeatedFreezeFailureSealsTerminalBatch(t *testing.T) {
	repository := research2EvidenceTestRepository(t)
	freezeErr := errors.New("freeze storage unavailable")
	collector := &Research2EvidenceCollector{evidence: failedFreezeEvidenceRepository{Repository: repository, err: freezeErr}, evidenceProfile: "profile-test"}
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, shanghaiDataLocation())

	evidence, err := collector.CollectForRun(context.Background(), "run-freeze-terminal", cutoff)
	if err == nil || !strings.Contains(err.Error(), "collector is unavailable") || !strings.Contains(err.Error(), freezeErr.Error()) {
		t.Fatalf("collection and terminal freeze failures were not joined: %v", err)
	}
	batch, err := repository.Batch(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != marketdata.StatusFailed || batch.FrozenAt == nil {
		t.Fatalf("repeated freeze failure did not seal a terminal batch: %+v", batch)
	}
	if err := repository.AppendItems(context.Background(), evidence.EvidenceSetID, []marketdata.EvidenceItem{{SourceID: "late", SourceName: "late", Category: "market"}}); !errors.Is(err, marketdata.ErrEvidenceBatchFrozen) {
		t.Fatalf("terminal failed batch accepted a late append: %v", err)
	}
}
