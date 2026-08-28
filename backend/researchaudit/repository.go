package researchaudit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound       = errors.New("research audit not found")
	ErrInvalidRequest = errors.New("invalid research audit request")
	ErrImmutable      = errors.New("research audit payload is immutable")
)

type Repository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db, now: time.Now} }

func validOwner(owner string, allowReplay bool) bool {
	return owner == OwnerResearch1 || owner == OwnerResearch2 || (allowReplay && owner == OwnerReplay)
}

func (r *Repository) BeginRun(ctx context.Context, ownerType, ownerID string) (RunState, error) {
	ownerType, ownerID = strings.TrimSpace(ownerType), strings.TrimSpace(ownerID)
	if r == nil || r.db == nil || !validOwner(ownerType, true) || ownerID == "" {
		return RunState{}, ErrInvalidRequest
	}
	now := r.now().UTC()
	state := RunState{OwnerType: ownerType, OwnerID: ownerID, Status: StatusCapturing, CreatedAt: now, UpdatedAt: now}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error
	if err != nil {
		return RunState{}, err
	}
	return r.GetRunState(ctx, ownerType, ownerID)
}

func (r *Repository) GetRunState(ctx context.Context, ownerType, ownerID string) (RunState, error) {
	var state RunState
	err := r.db.WithContext(ctx).Where("owner_type = ? AND owner_id = ?", ownerType, ownerID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RunState{}, ErrNotFound
	}
	return state, err
}

func (r *Repository) FinishRun(ctx context.Context, ownerType, ownerID, status, lastError string) error {
	if status != StatusComplete && status != StatusFailed {
		return ErrInvalidRequest
	}
	values := map[string]any{"status": status, "updated_at": r.now().UTC()}
	if strings.TrimSpace(lastError) == "" {
		values["last_error"] = nil
	} else {
		values["last_error"] = strings.TrimSpace(lastError)
	}
	result := r.db.WithContext(ctx).Model(&RunState{}).Where("owner_type = ? AND owner_id = ? AND status = ?", ownerType, ownerID, StatusCapturing).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		state, err := r.GetRunState(ctx, ownerType, ownerID)
		if err != nil {
			return err
		}
		if state.Status == status {
			return nil
		}
		return fmt.Errorf("%w: run is %s", ErrImmutable, state.Status)
	}
	return nil
}

func (r *Repository) EnsurePromptVersion(ctx context.Context, scope, phase, version, template string) (PromptVersion, error) {
	if !validOwner(scope, false) || strings.TrimSpace(phase) == "" || strings.TrimSpace(version) == "" {
		return PromptVersion{}, ErrInvalidRequest
	}
	blob, digest, err := encodeGZIP(template)
	if err != nil {
		return PromptVersion{}, err
	}
	var existing PromptVersion
	err = r.db.WithContext(ctx).Where("research_scope = ? AND phase = ? AND version = ?", scope, phase, version).First(&existing).Error
	if err == nil {
		if existing.TemplateSHA256 != digest {
			return PromptVersion{}, fmt.Errorf("%w: prompt version content changed", ErrImmutable)
		}
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return PromptVersion{}, err
	}
	row := PromptVersion{PromptVersionID: uuid.NewString(), ResearchScope: scope, Phase: phase, Version: version, TemplateCodec: "gzip", TemplateBlob: blob, TemplateSHA256: digest, CreatedAt: r.now().UTC()}
	if err = r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return PromptVersion{}, err
	}
	return row, nil
}

func (r *Repository) InsertPayload(ctx context.Context, payload *Payload) error {
	if payload == nil || !validOwner(payload.OwnerType, true) || strings.TrimSpace(payload.OwnerID) == "" || payload.CallSequence < 1 || payload.Attempt < 1 {
		return ErrInvalidRequest
	}
	if payload.PayloadID == "" {
		payload.PayloadID = uuid.NewString()
	}
	if payload.CreatedAt.IsZero() {
		payload.CreatedAt = r.now().UTC()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state RunState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_type = ? AND owner_id = ?", payload.OwnerType, payload.OwnerID).First(&state).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if state.Status != StatusCapturing {
			return fmt.Errorf("%w: run is %s", ErrImmutable, state.Status)
		}
		if err := tx.Create(payload).Error; err != nil {
			return err
		}
		return tx.Model(&RunState{}).Where("owner_type = ? AND owner_id = ?", payload.OwnerType, payload.OwnerID).Updates(map[string]any{"payload_count": gorm.Expr("payload_count + 1"), "updated_at": r.now().UTC()}).Error
	})
}

func (r *Repository) ListPayloads(ctx context.Context, ownerType, ownerID string) ([]Payload, error) {
	var rows []Payload
	err := r.db.WithContext(ctx).Where("owner_type = ? AND owner_id = ?", ownerType, ownerID).Order("call_sequence ASC, attempt ASC").Find(&rows).Error
	return rows, err
}

func (r *Repository) CreateReplay(ctx context.Context, replay *Replay) error {
	if replay == nil || !validOwner(replay.SourceOwnerType, false) || replay.SourceOwnerID == "" || replay.ModelConfigID < 1 || replay.CutoffAt.IsZero() {
		return ErrInvalidRequest
	}
	if replay.ReplayID == "" {
		replay.ReplayID = uuid.NewString()
	}
	if replay.Status == "" {
		replay.Status = "queued"
	}
	if replay.CreatedAt.IsZero() {
		replay.CreatedAt = r.now().UTC()
	}
	return r.db.WithContext(ctx).Create(replay).Error
}

func (r *Repository) GetReplay(ctx context.Context, replayID string) (Replay, error) {
	var row Replay
	err := r.db.WithContext(ctx).Where("replay_id = ?", replayID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Replay{}, ErrNotFound
	}
	return row, err
}

func (r *Repository) UpdateReplayStatus(ctx context.Context, replayID, from, to, lastError string) error {
	if !map[string]bool{"queued": true, "running": true, "completed": true, "failed": true}[to] {
		return ErrInvalidRequest
	}
	now := r.now().UTC()
	values := map[string]any{"status": to}
	if to == "running" {
		values["started_at"] = now
	}
	if to == "completed" || to == "failed" {
		values["completed_at"] = now
	}
	if lastError == "" {
		values["last_error"] = nil
	} else {
		values["last_error"] = lastError
	}
	result := r.db.WithContext(ctx).Model(&Replay{}).Where("replay_id = ? AND status = ?", replayID, from).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrImmutable
	}
	return nil
}

func (r *Repository) InsertReplayResult(ctx context.Context, result *ReplayResult) error {
	if result == nil || result.ReplayID == "" || result.ResultCodec != "gzip" || len(result.ResultBlob) == 0 || result.ResultSHA256 == "" {
		return ErrInvalidRequest
	}
	if result.ReplayResultID == "" {
		result.ReplayResultID = uuid.NewString()
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = r.now().UTC()
	}
	return r.db.WithContext(ctx).Create(result).Error
}

func (r *Repository) GetReplayResult(ctx context.Context, replayID string) (ReplayResult, error) {
	var row ReplayResult
	err := r.db.WithContext(ctx).Where("replay_id = ?", replayID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ReplayResult{}, ErrNotFound
	}
	return row, err
}
