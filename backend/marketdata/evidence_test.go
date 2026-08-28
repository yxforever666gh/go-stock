package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func testEvidenceRepository(t *testing.T) *Repository {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&EvidenceBatch{}, &EvidenceItem{}); err != nil {
		t.Fatal(err)
	}
	return NewRepository(database)
}

func TestEvidenceCutoffAndFreeze(t *testing.T) {
	repository := testEvidenceRepository(t)
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, time.UTC)
	batch, err := repository.CreateBatch(context.Background(), CreateBatchRequest{OwnerType: "research2", OwnerID: "run-1", CutoffAt: cutoff, CollectorVersion: "2.0", EvidenceProfileVersion: "profile-1"})
	if err != nil {
		t.Fatal(err)
	}
	before, after := cutoff.Add(-time.Second), cutoff.Add(time.Second)
	items := []EvidenceItem{
		{SourceID: "before", SourceName: "before", Category: "market", AvailableAt: &before, CollectedAt: after, Status: StatusOK, Payload: []byte(`{"v":1}`)},
		{SourceID: "after", SourceName: "after", Category: "market", AvailableAt: &after, CollectedAt: before, Status: StatusOK, Payload: []byte(`{"v":2}`)},
		{SourceID: "unknown", SourceName: "unknown", Category: "market", CollectedAt: before, Status: StatusOK, Payload: []byte(`{"v":3}`)},
	}
	if err = repository.AppendItems(context.Background(), batch.EvidenceSetID, items); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Items(context.Background(), batch.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range stored {
		statuses[item.SourceID] = item.Status
	}
	if statuses["before"] != StatusOK || statuses["after"] != StatusAfterCutoff || statuses["unknown"] != StatusUnavailable {
		t.Fatalf("unexpected cutoff statuses: %#v", statuses)
	}
	frozen, err := repository.FreezeBatch(context.Background(), batch.EvidenceSetID, cutoff.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Status != StatusFrozen || frozen.FrozenAt == nil || frozen.ContentHash == "" {
		t.Fatalf("unexpected frozen batch: %#v", frozen)
	}
	if err = repository.AppendItems(context.Background(), batch.EvidenceSetID, []EvidenceItem{{SourceID: "late", SourceName: "late", Category: "market", AvailableAt: &after}}); !errors.Is(err, ErrEvidenceBatchFrozen) {
		t.Fatalf("expected frozen error, got %v", err)
	}
}

func TestEvidenceHashesAreDeterministic(t *testing.T) {
	available := time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC)
	item := EvidenceItem{SourceID: "source", SourceName: "name", SourceRef: "https://example.test", Category: "market", AvailableAt: &available, Status: StatusOK, Payload: []byte(`{"a":1}`), Summary: "summary"}
	if EvidenceItemHash(item) != EvidenceItemHash(item) {
		t.Fatal("item hash changed for identical input")
	}
	changed := item
	changed.Payload = []byte(`{"a":2}`)
	if EvidenceItemHash(item) == EvidenceItemHash(changed) {
		t.Fatal("item hash ignored payload change")
	}
	identified := item
	identified.SourceID, identified.EvidenceItemID, identified.EvidenceSetID = "another-source-id", "another-item-id", "another-set-id"
	if EvidenceItemHash(item) != EvidenceItemHash(identified) {
		t.Fatal("content hash depends on persistence identifiers")
	}
	item.ContentHash, identified.ContentHash = EvidenceItemHash(item), EvidenceItemHash(identified)
	batchA := EvidenceBatch{EvidenceSetID: "set-a", OwnerID: "run-a", CutoffAt: available, CollectorVersion: "2.0", EvidenceProfileVersion: "profile"}
	batchB := batchA
	batchB.EvidenceSetID, batchB.OwnerID = "set-b", "run-b"
	if EvidenceBatchHash(batchA, []EvidenceItem{item}) != EvidenceBatchHash(batchB, []EvidenceItem{identified}) {
		t.Fatal("batch content hash depends on persistence identifiers")
	}
}

func TestFreezeBatchClampsProvisionalCutoff(t *testing.T) {
	repository := testEvidenceRepository(t)
	started := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	batch, err := repository.CreateBatch(context.Background(), CreateBatchRequest{
		OwnerType: "research1", OwnerID: "run-1", CutoffAt: started.Add(24 * time.Hour),
		CollectorVersion: "2.0", EvidenceProfileVersion: "profile-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	normal, future := started.Add(5*time.Minute), started.Add(2*time.Hour)
	if err = repository.AppendItems(context.Background(), batch.EvidenceSetID, []EvidenceItem{
		{SourceID: "normal", SourceName: "normal", Category: "market", AvailableAt: &normal, CollectedAt: normal, Status: StatusOK},
		{SourceID: "future", SourceName: "future", Category: "market", AvailableAt: &future, CollectedAt: normal, Status: StatusOK},
	}); err != nil {
		t.Fatal(err)
	}
	frozenAt := started.Add(10 * time.Minute)
	frozen, err := repository.FreezeBatch(context.Background(), batch.EvidenceSetID, frozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen.CutoffAt.Equal(frozenAt) {
		t.Fatalf("frozen cutoff=%v, want %v", frozen.CutoffAt, frozenAt)
	}
	items, err := repository.Items(context.Background(), batch.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range items {
		statuses[item.SourceID] = item.Status
	}
	if statuses["normal"] != StatusOK || statuses["future"] != StatusAfterCutoff {
		t.Fatalf("unexpected clamped statuses: %#v", statuses)
	}
}

type testProvider[T any] struct {
	name   string
	result ProviderResult[T]
	calls  *int
}

func (p testProvider[T]) Name() string { return p.name }
func (p testProvider[T]) Collect(context.Context, ProviderRequest) ProviderResult[T] {
	if p.calls != nil {
		*p.calls++
	}
	return p.result
}

func TestPrimaryFallbackCollector(t *testing.T) {
	primaryCalls, fallbackCalls := 0, 0
	collector := PrimaryFallbackCollector[string]{Primary: testProvider[string]{name: "primary", calls: &primaryCalls, result: ProviderResult[string]{Status: StatusUnavailable, Err: errors.New("down")}}, Fallback: testProvider[string]{name: "fallback", calls: &fallbackCalls, result: ProviderResult[string]{Status: StatusOK, Data: "fallback-data"}}}
	result := collector.Collect(context.Background(), ProviderRequest{})
	if result.Status != StatusOK || result.Source != "fallback" || result.Data != "fallback-data" || primaryCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("unexpected fallback result: %#v calls=%d/%d", result, primaryCalls, fallbackCalls)
	}
}

func TestProviderChainCollectorPrefersCompleteFallbackAndKeepsAttemptStates(t *testing.T) {
	primaryCalls, partialCalls, fallbackCalls := 0, 0, 0
	collector := ProviderChainCollector[string]{Providers: []Provider[string]{
		testProvider[string]{name: "primary", calls: &primaryCalls, result: ProviderResult[string]{Status: StatusUnavailable, Err: errors.New("EOF")}},
		testProvider[string]{name: "partial", calls: &partialCalls, result: ProviderResult[string]{Status: StatusPartial, Data: "partial-data", Warning: "95% coverage"}},
		testProvider[string]{name: "fallback", calls: &fallbackCalls, result: ProviderResult[string]{Status: StatusOK, Data: "complete-data"}},
	}}
	result := collector.Collect(context.Background(), ProviderRequest{})
	if result.Status != StatusOK || result.Source != "fallback" || result.Data != "complete-data" {
		t.Fatalf("unexpected chain result: %#v", result)
	}
	if primaryCalls != 1 || partialCalls != 1 || fallbackCalls != 1 || len(result.Sources) != 3 || len(result.Errors) != 1 {
		t.Fatalf("unexpected chain attempts: calls=%d/%d/%d result=%#v", primaryCalls, partialCalls, fallbackCalls, result)
	}
}

func TestProviderChainCollectorReturnsBestPartialWhenLaterSourcesFail(t *testing.T) {
	collector := ProviderChainCollector[string]{Providers: []Provider[string]{
		testProvider[string]{name: "partial", result: ProviderResult[string]{Status: StatusPartial, Data: "usable"}},
		testProvider[string]{name: "down", result: ProviderResult[string]{Status: StatusUnavailable, Err: errors.New("down")}},
	}}
	result := collector.Collect(context.Background(), ProviderRequest{})
	if result.Status != StatusPartial || result.Source != "partial" || result.Data != "usable" || len(result.Sources) != 2 || len(result.Errors) != 1 {
		t.Fatalf("unexpected partial chain result: %#v", result)
	}
}

func TestDataEnvelopePublicContract(t *testing.T) {
	encoded, err := json.Marshal(DataEnvelope[[]string]{Data: []string{}, Source: "eastmoney", AsOf: time.Now(), FetchedAt: time.Now(), Status: StatusOK, Errors: []DataError{}})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"data", "source", "asOf", "fetchedAt", "status", "errors"} {
		if _, ok := value[key]; !ok {
			t.Fatalf("missing envelope key %q in %s", key, encoded)
		}
	}
}

func TestSourceStatesUseOnlyPublicStatuses(t *testing.T) {
	collector := PrimaryFallbackCollector[string]{
		Primary: testProvider[string]{name: "primary", result: ProviderResult[string]{Status: StatusFailed, Err: errors.New("down")}},
	}
	result := collector.Collect(context.Background(), ProviderRequest{})
	if result.Status != StatusUnavailable || len(result.Sources) != 1 || result.Sources[0].Status != StatusUnavailable {
		t.Fatalf("non-public provider status escaped envelope: %#v", result)
	}
}
