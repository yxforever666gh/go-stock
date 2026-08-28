package marketdata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	StatusOK          = "ok"
	StatusPartial     = "partial"
	StatusUnavailable = "unavailable"
	StatusEmpty       = "empty"
	StatusStale       = "stale"
	StatusFailed      = "failed"
	StatusAfterCutoff = "after_cutoff"
	StatusCollecting  = "collecting"
	StatusFrozen      = "frozen"
)

var ErrEvidenceBatchFrozen = errors.New("evidence batch is frozen")

// EvidenceBatch is one reproducible evidence set owned by a research run.
// The public API calls it a batch while the physical schema keeps the
// research_evidence_sets name introduced by the 2.0 migration.
type EvidenceBatch struct {
	ID                     uint       `json:"id" gorm:"primaryKey"`
	EvidenceSetID          string     `json:"evidenceSetId" gorm:"column:evidence_set_id;size:36;uniqueIndex;not null"`
	OwnerType              string     `json:"ownerType" gorm:"column:owner_type;size:32;index;not null"`
	OwnerID                string     `json:"ownerId" gorm:"column:owner_id;size:64;index;not null"`
	CutoffAt               time.Time  `json:"cutoffAt" gorm:"column:cutoff_at;index;not null"`
	CollectorVersion       string     `json:"collectorVersion" gorm:"column:collector_version;size:32;not null"`
	EvidenceProfileVersion string     `json:"evidenceProfileVersion" gorm:"column:evidence_profile_version;size:64;not null"`
	Status                 string     `json:"status" gorm:"size:32;index;not null"`
	ContentHash            string     `json:"contentHash" gorm:"column:content_hash;size:64;not null"`
	FrozenAt               *time.Time `json:"frozenAt,omitempty" gorm:"column:frozen_at"`
	CreatedAt              time.Time  `json:"createdAt" gorm:"column:created_at;not null"`
}

func (EvidenceBatch) TableName() string { return "research_evidence_sets" }

// EvidenceItem stores the immutable, typed output of one source observation.
// AvailableAt is the time at which the observation became usable by the
// collector; that is deliberately distinct from the market event time.
type EvidenceItem struct {
	ID              uint       `json:"id" gorm:"primaryKey"`
	EvidenceItemID  string     `json:"evidenceItemId" gorm:"column:evidence_item_id;size:36;uniqueIndex;not null"`
	EvidenceSetID   string     `json:"evidenceSetId" gorm:"column:evidence_set_id;size:36;index;not null"`
	SourceID        string     `json:"sourceId" gorm:"column:source_id;size:160;not null"`
	SourceName      string     `json:"sourceName" gorm:"column:source_name;size:128;not null"`
	SourceRef       string     `json:"sourceRef,omitempty" gorm:"column:source_ref;type:text"`
	Category        string     `json:"category" gorm:"size:64;index;not null"`
	EntityType      string     `json:"entityType,omitempty" gorm:"column:entity_type;size:32;index"`
	EntityID        string     `json:"entityId,omitempty" gorm:"column:entity_id;size:64;index"`
	EventAt         *time.Time `json:"eventAt,omitempty" gorm:"column:event_at"`
	AvailableAt     *time.Time `json:"availableAt,omitempty" gorm:"column:available_at;index"`
	CollectedAt     time.Time  `json:"collectedAt" gorm:"column:collected_at;index;not null"`
	Status          string     `json:"status" gorm:"size:32;index;not null"`
	Payload         []byte     `json:"-" gorm:"column:payload;type:blob;not null"`
	PayloadEncoding string     `json:"payloadEncoding" gorm:"column:payload_encoding;size:24;not null"`
	Summary         string     `json:"summary,omitempty" gorm:"column:summary;type:text"`
	ContentHash     string     `json:"contentHash" gorm:"column:content_hash;size:64;not null"`
	ErrorMessage    string     `json:"errorMessage,omitempty" gorm:"column:error_message;type:text"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"column:created_at;not null"`
}

func (EvidenceItem) TableName() string { return "research_evidence_items" }

type CreateBatchRequest struct {
	EvidenceSetID          string
	OwnerType              string
	OwnerID                string
	CutoffAt               time.Time
	CollectorVersion       string
	EvidenceProfileVersion string
}

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) CreateBatch(ctx context.Context, request CreateBatchRequest) (EvidenceBatch, error) {
	if r == nil || r.db == nil {
		return EvidenceBatch{}, errors.New("evidence repository is unavailable")
	}
	if request.CutoffAt.IsZero() {
		return EvidenceBatch{}, errors.New("evidence cutoff is required")
	}
	setID := strings.TrimSpace(request.EvidenceSetID)
	if setID == "" {
		setID = uuid.NewString()
	}
	batch := EvidenceBatch{
		EvidenceSetID:          setID,
		OwnerType:              strings.TrimSpace(request.OwnerType),
		OwnerID:                strings.TrimSpace(request.OwnerID),
		CutoffAt:               request.CutoffAt,
		CollectorVersion:       defaultString(request.CollectorVersion, "2.0"),
		EvidenceProfileVersion: defaultString(request.EvidenceProfileVersion, "market-evidence-v1"),
		Status:                 StatusCollecting,
		ContentHash:            emptyHash(),
	}
	if batch.OwnerType == "" || batch.OwnerID == "" {
		return EvidenceBatch{}, errors.New("evidence owner type and id are required")
	}
	if err := r.db.WithContext(ctx).Create(&batch).Error; err != nil {
		return EvidenceBatch{}, fmt.Errorf("create evidence batch: %w", err)
	}
	return batch, nil
}

func (r *Repository) AppendItems(ctx context.Context, evidenceSetID string, values []EvidenceItem) error {
	if r == nil || r.db == nil {
		return errors.New("evidence repository is unavailable")
	}
	if len(values) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var batch EvidenceBatch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("evidence_set_id = ?", evidenceSetID).First(&batch).Error; err != nil {
			return fmt.Errorf("load evidence batch: %w", err)
		}
		if batch.Status == StatusFrozen || batch.FrozenAt != nil {
			return ErrEvidenceBatchFrozen
		}
		now := time.Now()
		for index := range values {
			item := &values[index]
			item.EvidenceSetID = batch.EvidenceSetID
			item.EvidenceItemID = defaultString(item.EvidenceItemID, uuid.NewString())
			item.SourceID = strings.TrimSpace(item.SourceID)
			if item.SourceID == "" {
				item.SourceID = item.EvidenceItemID
			}
			item.SourceName = defaultString(item.SourceName, "unknown")
			item.Category = defaultString(item.Category, "unknown")
			if item.CollectedAt.IsZero() {
				item.CollectedAt = now
			}
			item.Status = normalizeEvidenceItemStatus(item.Status)
			if item.AvailableAt == nil {
				item.Status = StatusUnavailable
			} else if item.AvailableAt.After(batch.CutoffAt) && canBeAfterCutoff(item.Status) {
				item.Status = StatusAfterCutoff
			}
			item.PayloadEncoding = defaultString(item.PayloadEncoding, "identity")
			if item.Payload == nil {
				item.Payload = []byte{}
			}
			item.ContentHash = EvidenceItemHash(*item)
		}
		if err := tx.Create(&values).Error; err != nil {
			return fmt.Errorf("append evidence items: %w", err)
		}
		return nil
	})
}

func (r *Repository) FreezeBatch(ctx context.Context, evidenceSetID string, frozenAt time.Time) (EvidenceBatch, error) {
	if r == nil || r.db == nil {
		return EvidenceBatch{}, errors.New("evidence repository is unavailable")
	}
	var result EvidenceBatch
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("evidence_set_id = ?", evidenceSetID).First(&result).Error; err != nil {
			return fmt.Errorf("load evidence batch: %w", err)
		}
		if result.Status == StatusFrozen || result.FrozenAt != nil {
			return nil
		}
		if frozenAt.IsZero() {
			frozenAt = time.Now()
		}
		if frozenAt.Before(result.CutoffAt) {
			// Research 1 discovers its exact freeze instant only after its
			// staged collection finishes.  Clamp a provisional cutoff so the
			// persisted batch always records the actual latest eligible time.
			result.CutoffAt = frozenAt
		}
		var items []EvidenceItem
		if err := tx.Where("evidence_set_id = ?", evidenceSetID).Order("source_id, evidence_item_id").Find(&items).Error; err != nil {
			return fmt.Errorf("load evidence items: %w", err)
		}
		for index := range items {
			item := &items[index]
			if item.AvailableAt == nil || !item.AvailableAt.After(result.CutoffAt) || !canBeAfterCutoff(item.Status) {
				continue
			}
			item.Status = StatusAfterCutoff
			item.ContentHash = EvidenceItemHash(*item)
			if err := tx.Model(&EvidenceItem{}).Where("id = ?", item.ID).Updates(map[string]any{
				"status": item.Status, "content_hash": item.ContentHash,
			}).Error; err != nil {
				return fmt.Errorf("clamp evidence item cutoff: %w", err)
			}
		}
		result.Status = StatusFrozen
		result.FrozenAt = &frozenAt
		result.ContentHash = EvidenceBatchHash(result, items)
		return tx.Model(&EvidenceBatch{}).Where("evidence_set_id = ? AND status <> ?", evidenceSetID, StatusFrozen).Updates(map[string]any{
			"status": StatusFrozen, "frozen_at": frozenAt, "cutoff_at": result.CutoffAt, "content_hash": result.ContentHash,
		}).Error
	})
	return result, err
}

// SealBatchFailure makes an evidence batch terminal when normal freezing cannot
// be completed.  Failed batches remain inspectable, but FrozenAt prevents any
// later collector from appending evidence to a run that has already ended.
func (r *Repository) SealBatchFailure(ctx context.Context, evidenceSetID string, failedAt time.Time) (EvidenceBatch, error) {
	if r == nil || r.db == nil {
		return EvidenceBatch{}, errors.New("evidence repository is unavailable")
	}
	var result EvidenceBatch
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("evidence_set_id = ?", evidenceSetID).First(&result).Error; err != nil {
			return fmt.Errorf("load evidence batch: %w", err)
		}
		if result.Status == StatusFrozen || result.FrozenAt != nil {
			return nil
		}
		if failedAt.IsZero() {
			failedAt = time.Now()
		}
		var items []EvidenceItem
		if err := tx.Where("evidence_set_id = ?", evidenceSetID).Order("source_id, evidence_item_id").Find(&items).Error; err != nil {
			return fmt.Errorf("load evidence items: %w", err)
		}
		result.Status = StatusFailed
		result.FrozenAt = &failedAt
		result.ContentHash = EvidenceBatchHash(result, items)
		return tx.Model(&EvidenceBatch{}).Where("evidence_set_id = ? AND frozen_at IS NULL", evidenceSetID).Updates(map[string]any{
			"status": StatusFailed, "frozen_at": failedAt, "content_hash": result.ContentHash,
		}).Error
	})
	return result, err
}

func (r *Repository) Batch(ctx context.Context, evidenceSetID string) (EvidenceBatch, error) {
	var result EvidenceBatch
	if r == nil || r.db == nil {
		return result, errors.New("evidence repository is unavailable")
	}
	err := r.db.WithContext(ctx).Where("evidence_set_id = ?", evidenceSetID).First(&result).Error
	return result, err
}

func (r *Repository) Items(ctx context.Context, evidenceSetID string) ([]EvidenceItem, error) {
	var result []EvidenceItem
	if r == nil || r.db == nil {
		return nil, errors.New("evidence repository is unavailable")
	}
	err := r.db.WithContext(ctx).Where("evidence_set_id = ?", evidenceSetID).Order("source_id, evidence_item_id").Find(&result).Error
	return result, err
}

func MarshalPayload(value any) ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(value)
}

func EvidenceItemHash(item EvidenceItem) string {
	eventAt := ""
	if item.EventAt != nil {
		eventAt = item.EventAt.UTC().Format(time.RFC3339Nano)
	}
	availableAt := ""
	if item.AvailableAt != nil {
		availableAt = item.AvailableAt.UTC().Format(time.RFC3339Nano)
	}
	value := strings.Join([]string{
		item.SourceName, item.SourceRef, item.Category, item.EntityType, item.EntityID,
		eventAt, availableAt, item.Status,
		item.PayloadEncoding, string(item.Payload), item.Summary, item.ErrorMessage,
	}, "\x1f")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func EvidenceBatchHash(batch EvidenceBatch, items []EvidenceItem) string {
	values := append([]EvidenceItem(nil), items...)
	sort.Slice(values, func(i, j int) bool {
		left := strings.Join([]string{values[i].Category, values[i].SourceName, values[i].SourceRef, values[i].EntityType, values[i].EntityID, values[i].ContentHash, values[i].Status}, "\x1f")
		right := strings.Join([]string{values[j].Category, values[j].SourceName, values[j].SourceRef, values[j].EntityType, values[j].EntityID, values[j].ContentHash, values[j].Status}, "\x1f")
		return left < right
	})
	parts := []string{batch.CutoffAt.UTC().Format(time.RFC3339Nano), batch.CollectorVersion, batch.EvidenceProfileVersion}
	for _, item := range values {
		parts = append(parts, item.ContentHash, item.Status)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

func canBeAfterCutoff(status string) bool {
	switch strings.TrimSpace(status) {
	case "", StatusOK, StatusPartial, StatusEmpty, StatusStale:
		return true
	default:
		return false
	}
}

func normalizeEvidenceItemStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "", StatusOK, StatusEmpty:
		return StatusOK
	case StatusPartial:
		return StatusPartial
	case StatusStale:
		return StatusStale
	case StatusAfterCutoff:
		return StatusAfterCutoff
	default:
		return StatusUnavailable
	}
}

func emptyHash() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}

func defaultString(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
