package researchconfig

import (
	"context"
	"fmt"
	"time"

	"go-stock/backend/models"
	"gorm.io/gorm"
)

type Record struct {
	Center     string `gorm:"primaryKey;size:16;check:research_center,center IN ('research1','research2')"`
	ConfigJSON string `gorm:"not null;type:text"`
	Revision   int64  `gorm:"not null;check:research_revision,revision > 0"`
}

func (Record) TableName() string { return "research_settings" }

type Snapshot struct {
	Center   string
	Revision int64
	Settings *models.SettingConfig
}

type Store struct{ db *gorm.DB }

func New(db *gorm.DB) *Store { return &Store{db: db} }

// ActiveModels confines both normal reads and new execution to one owner.
func ActiveModels(db *gorm.DB, owner string) *gorm.DB {
	return db.Model(&models.AIConfig{}).Where("owner = ? AND archived_at IS NULL", owner)
}

func (s *Store) Load(ctx context.Context, center string) (Snapshot, error) {
	if _, err := ownedFields(center); err != nil {
		return Snapshot{}, err
	}
	if s == nil || s.db == nil {
		return Snapshot{}, fmt.Errorf("research database is unavailable")
	}
	var snapshot Snapshot
	// One read transaction ensures settings and models belong to the same revision.
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = load(tx, center)
		return err
	})
	return snapshot, err
}

func load(tx *gorm.DB, center string) (Snapshot, error) {
	var record Record
	if err := tx.Where("center = ?", center).First(&record).Error; err != nil {
		return Snapshot{}, err
	}
	cfg, err := DecodeConfig(center, []byte(record.ConfigJSON))
	if err != nil {
		return Snapshot{}, err
	}
	cfg.AiConfigs = make([]*models.AIConfig, 0)
	if err := ActiveModels(tx, center).Order("CASE WHEN sort <= 0 THEN id ELSE sort END ASC").Order("id ASC").Find(&cfg.AiConfigs).Error; err != nil {
		return Snapshot{}, err
	}
	for _, model := range cfg.AiConfigs {
		model.ApiProtocol = models.NormalizeAIAPIProtocol(model.ApiProtocol)
		if model.TimeOut <= 0 {
			model.TimeOut = 300
		}
		if !model.Disabled && cfg.AIAnalysisConfigID == 0 {
			cfg.AIAnalysisConfigID = model.ID
		}
	}
	return Snapshot{Center: center, Revision: record.Revision, Settings: cfg}, nil
}

func (s *Store) Save(ctx context.Context, center string, revision int64, cfg *models.SettingConfig) (Snapshot, error) {
	raw, err := ConfigJSON(center, cfg)
	if err != nil {
		return Snapshot{}, err
	}
	if revision <= 0 {
		return Snapshot{}, fmt.Errorf("%w: revision must be positive", ErrInvalidConfig)
	}
	if s == nil || s.db == nil {
		return Snapshot{}, fmt.Errorf("research database is unavailable")
	}
	var snapshot Snapshot
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// CAS is the first statement: SQLite serializes writers before reading model
		// membership, and another center's save never invalidates this revision.
		result := tx.Model(&Record{}).Where("center = ? AND revision = ?", center, revision).
			Updates(map[string]any{"config_json": string(raw), "revision": revision + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		if cfg.AiConfigs != nil {
			if err := SaveModels(tx, center, cfg.AiConfigs); err != nil {
				return err
			}
		}
		var err error
		snapshot, err = load(tx, center)
		return err
	})
	return snapshot, err
}

// SaveModels replaces a scope's active list inside the caller's transaction.
// Removed models are archived rather than deleting IDs referenced by history.
func SaveModels(tx *gorm.DB, owner string, configs []*models.AIConfig) error {
	if owner != Global && owner != Research1 && owner != Research2 {
		return ErrInvalidCenter
	}
	var current []models.AIConfig
	if err := ActiveModels(tx, owner).Find(&current).Error; err != nil {
		return err
	}
	known := make(map[uint]bool, len(current))
	for _, model := range current {
		known[model.ID] = true
	}
	seen := make(map[uint]bool, len(configs))
	for _, model := range configs {
		if model == nil {
			return fmt.Errorf("%w: empty AI model", ErrInvalidConfig)
		}
		if model.ID > 0 && (!known[model.ID] || seen[model.ID]) {
			return ErrModelOwnership
		}
		if model.ID > 0 {
			seen[model.ID] = true
		}
	}
	now := time.Now().UTC()
	for _, model := range current {
		if !seen[model.ID] {
			if err := ActiveModels(tx, owner).Where("id = ?", model.ID).Update("archived_at", now).Error; err != nil {
				return err
			}
		}
	}
	for i, model := range configs {
		item := *model // never mutate a caller's task snapshot or input draft
		item.Owner, item.ArchivedAt, item.Sort = owner, nil, i+1
		item.ApiProtocol = models.NormalizeAIAPIProtocol(item.ApiProtocol)
		if item.ID == 0 {
			item.CreatedAt, item.UpdatedAt = now, now
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		} else {
			item.UpdatedAt = now
			if err := ActiveModels(tx, owner).Where("id = ?", item.ID).Select("*").Omit("id", "created_at", "owner", "archived_at").Updates(&item).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
