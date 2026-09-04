package research2app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/research2"
	"go-stock/internal/researchevidence"
	"go-stock/internal/sqlitedb"

	"github.com/google/uuid"
)

// EvidenceProvider collects the provider-specific evidence payload while this
// package owns run-level persistence and freezing.
type EvidenceProvider interface {
	Collect(context.Context, time.Time) (research2.Evidence, error)
	CollectWithExclusions(context.Context, time.Time, map[string]struct{}) (research2.Evidence, error)
}

type EvidenceRepository interface {
	CreateBatch(context.Context, marketdata.CreateBatchRequest) (marketdata.EvidenceBatch, error)
	AppendItems(context.Context, string, []marketdata.EvidenceItem) error
	FreezeBatch(context.Context, string, time.Time) (marketdata.EvidenceBatch, error)
	SealBatchFailure(context.Context, string, time.Time) (marketdata.EvidenceBatch, error)
	Batch(context.Context, string) (marketdata.EvidenceBatch, error)
}

// EvidenceItemBuilder preserves provider-specific document status and entity
// semantics while the application owns the durable batch lifecycle.
type EvidenceItemBuilder func(researchevidence.SourceDocument, time.Time, int, map[string]int) (marketdata.EvidenceItem, error)

type DurableEvidenceCollector struct {
	provider EvidenceProvider
	store    EvidenceRepository
	profile  string
	build    EvidenceItemBuilder
	now      func() time.Time
}

var (
	_ research2.RunEvidenceCollector         = (*DurableEvidenceCollector)(nil)
	_ research2.FilteredRunEvidenceCollector = (*DurableEvidenceCollector)(nil)
)

func NewDurableEvidenceCollector(provider EvidenceProvider, store EvidenceRepository, profile string, build EvidenceItemBuilder) *DurableEvidenceCollector {
	return &DurableEvidenceCollector{provider: provider, store: store, profile: strings.TrimSpace(profile), build: build, now: time.Now}
}

func (c *DurableEvidenceCollector) Collect(ctx context.Context, cutoff time.Time) (research2.Evidence, error) {
	if c == nil || c.provider == nil {
		return research2.Evidence{CutoffAt: cutoff}, errors.New("research2 evidence collector is unavailable")
	}
	return c.provider.Collect(ctx, cutoff)
}

func (c *DurableEvidenceCollector) CollectForRun(ctx context.Context, runID string, startedAt time.Time) (research2.Evidence, error) {
	return c.collectForRun(ctx, runID, startedAt, nil)
}

func (c *DurableEvidenceCollector) CollectForRunWithExclusions(ctx context.Context, runID string, startedAt time.Time, excludedCodes map[string]struct{}) (research2.Evidence, error) {
	return c.collectForRun(ctx, runID, startedAt, excludedCodes)
}

func (c *DurableEvidenceCollector) collectForRun(ctx context.Context, runID string, startedAt time.Time, excludedCodes map[string]struct{}) (evidence research2.Evidence, err error) {
	if c == nil || c.store == nil || c.profile == "" {
		return c.collect(ctx, startedAt, excludedCodes)
	}
	evidence, collectErr := c.collect(ctx, startedAt, excludedCodes)
	batchCutoff := evidence.FreezeAt
	if batchCutoff.IsZero() {
		batchCutoff = evidence.CutoffAt
	}
	if batchCutoff.IsZero() {
		batchCutoff = startedAt
		evidence.CutoffAt = batchCutoff
	}
	evidence.FreezeAt = batchCutoff
	request := marketdata.CreateBatchRequest{
		EvidenceSetID: uuid.NewString(), OwnerType: "research2", OwnerID: runID, CutoffAt: batchCutoff,
		CollectorVersion: "2.0", EvidenceProfileVersion: c.profile,
	}
	var batch marketdata.EvidenceBatch
	if err = retryEvidenceWrite(ctx, func() error {
		var createErr error
		batch, createErr = c.store.CreateBatch(ctx, request)
		return createErr
	}); err != nil {
		return evidence, errors.Join(collectErr, err)
	}
	evidence.EvidenceProfileVersion, evidence.EvidenceSetID = c.profile, batch.EvidenceSetID
	defer func() {
		frozenAt := c.nowTime()
		if frozenAt.Before(batchCutoff) {
			frozenAt = batchCutoff
		}
		err = errors.Join(err, finalizeEvidenceBatch(c.store, batch.EvidenceSetID, frozenAt))
	}()
	err = collectErr
	if len(evidence.Documents) == 0 {
		return evidence, err
	}
	if c.build == nil {
		return evidence, errors.Join(err, errors.New("research2 evidence item builder is unavailable"))
	}
	items := make([]marketdata.EvidenceItem, 0, len(evidence.Documents))
	usedSourceIDs := make(map[string]int)
	for index, document := range evidence.Documents {
		item, buildErr := c.build(document, batchCutoff, index, usedSourceIDs)
		if buildErr != nil {
			return evidence, buildErr
		}
		items = append(items, item)
	}
	if appendErr := retryEvidenceWrite(ctx, func() error { return c.store.AppendItems(ctx, batch.EvidenceSetID, items) }); appendErr != nil {
		err = errors.Join(err, appendErr)
	}
	return evidence, err
}

func (c *DurableEvidenceCollector) collect(ctx context.Context, cutoff time.Time, excludedCodes map[string]struct{}) (research2.Evidence, error) {
	if c == nil || c.provider == nil {
		return research2.Evidence{CutoffAt: cutoff}, errors.New("research2 evidence collector is unavailable")
	}
	if len(excludedCodes) > 0 {
		return c.provider.CollectWithExclusions(ctx, cutoff, excludedCodes)
	}
	return c.provider.Collect(ctx, cutoff)
}

func (c *DurableEvidenceCollector) nowTime() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

func retryEvidenceWrite(ctx context.Context, operation func() error) error {
	return sqlitedb.Retry(ctx, operation, nil)
}

func finalizeEvidenceBatch(repository EvidenceRepository, evidenceSetID string, frozenAt time.Time) error {
	if repository == nil {
		return errors.New("evidence repository is unavailable")
	}
	var freezeErrors []error
	for attempt := 0; attempt < 2; attempt++ {
		freezeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		batch, freezeErr := repository.FreezeBatch(freezeCtx, evidenceSetID, frozenAt)
		cancel()
		if freezeErr == nil || evidenceBatchIsTerminal(batch) {
			return nil
		}
		freezeErrors = append(freezeErrors, freezeErr)

		readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
		stored, readErr := repository.Batch(readCtx, evidenceSetID)
		readCancel()
		if readErr == nil && evidenceBatchIsTerminal(stored) {
			return nil
		}
		if readErr != nil {
			freezeErrors = append(freezeErrors, fmt.Errorf("verify evidence batch terminal state: %w", readErr))
		}
	}

	failCtx, failCancel := context.WithTimeout(context.Background(), 5*time.Second)
	failed, failErr := repository.SealBatchFailure(failCtx, evidenceSetID, frozenAt)
	failCancel()
	if failErr != nil {
		freezeErrors = append(freezeErrors, fmt.Errorf("seal evidence batch failure: %w", failErr))
	} else if !evidenceBatchIsTerminal(failed) {
		freezeErrors = append(freezeErrors, errors.New("evidence batch remained collecting after freeze recovery"))
	}
	return errors.Join(freezeErrors...)
}

func evidenceBatchIsTerminal(batch marketdata.EvidenceBatch) bool {
	return batch.FrozenAt != nil && (batch.Status == marketdata.StatusFrozen || batch.Status == marketdata.StatusFailed)
}
