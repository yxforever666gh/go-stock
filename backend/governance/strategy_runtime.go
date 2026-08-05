package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	StrategyModePaused = "paused"
	StrategyModeLive   = "live"
)

var (
	ErrStrategyRuntimeUnavailable = errors.New("strategy runtime control is unavailable")
	ErrStrategyPaused             = errors.New("strategy production is paused")
)

// StrategyRuntimeControl is a singleton operational gate. Strategy decisions
// remain immutable; this row only controls whether production writes may run.
type StrategyRuntimeControl struct {
	ID                     uint      `json:"id" gorm:"primaryKey"`
	Mode                   string    `json:"mode" gorm:"size:16;not null"`
	CurrentStrategyVersion string    `json:"currentStrategyVersion" gorm:"size:32;not null"`
	Reason                 string    `json:"reason" gorm:"size:512"`
	ChangedBy              string    `json:"changedBy" gorm:"size:128"`
	ChangedAt              time.Time `json:"changedAt" gorm:"not null"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

func (StrategyRuntimeControl) TableName() string { return "strategy_runtime_control" }

type StrategyRuntimeStatus struct {
	Mode                   string    `json:"mode"`
	CurrentStrategyVersion string    `json:"currentStrategyVersion"`
	Reason                 string    `json:"reason,omitempty"`
	ChangedBy              string    `json:"changedBy,omitempty"`
	ChangedAt              time.Time `json:"changedAt,omitempty"`
	Ready                  bool      `json:"ready"`
}

func HasStrategyRuntimeControl(database *gorm.DB) bool {
	return database != nil && database.Migrator().HasTable(&StrategyRuntimeControl{})
}

// InitializeStrategyRuntimeControl is called only by the numbered migration
// runner. It deliberately defaults to paused so a new binary cannot produce
// recommendations before an operator explicitly resumes it.
func InitializeStrategyRuntimeControl(ctx context.Context, database *gorm.DB, strategyVersion string) error {
	if database == nil {
		return ErrStrategyRuntimeUnavailable
	}
	strategyVersion = strings.TrimSpace(strategyVersion)
	if strategyVersion == "" {
		return errors.New("current strategy version is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := database.WithContext(ctx).AutoMigrate(&StrategyRuntimeControl{}); err != nil {
		return fmt.Errorf("migrate strategy runtime control: %w", err)
	}

	now := time.Now().UTC()
	row := StrategyRuntimeControl{
		ID:                     1,
		Mode:                   StrategyModePaused,
		CurrentStrategyVersion: strategyVersion,
		Reason:                 "system governance refactor",
		ChangedBy:              "migration",
		ChangedAt:              now,
	}
	return database.WithContext(ctx).Where("id = ?", 1).FirstOrCreate(&row).Error
}

func GetStrategyRuntimeStatus(ctx context.Context, database *gorm.DB, strategyVersion string) StrategyRuntimeStatus {
	fallback := StrategyRuntimeStatus{
		Mode:                   StrategyModePaused,
		CurrentStrategyVersion: strings.TrimSpace(strategyVersion),
		Reason:                 ErrStrategyRuntimeUnavailable.Error(),
		Ready:                  false,
	}
	if !HasStrategyRuntimeControl(database) {
		return fallback
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var row StrategyRuntimeControl
	if err := database.WithContext(ctx).Where("id = ?", 1).First(&row).Error; err != nil {
		fallback.Reason = err.Error()
		return fallback
	}
	mode := normalizeStrategyMode(row.Mode)
	if mode == "" {
		fallback.Reason = "invalid persisted strategy runtime mode"
		return fallback
	}
	return StrategyRuntimeStatus{
		Mode:                   mode,
		CurrentStrategyVersion: strings.TrimSpace(row.CurrentStrategyVersion),
		Reason:                 strings.TrimSpace(row.Reason),
		ChangedBy:              strings.TrimSpace(row.ChangedBy),
		ChangedAt:              row.ChangedAt,
		Ready:                  true,
	}
}

func RequireStrategyLive(ctx context.Context, database *gorm.DB, strategyVersion string) error {
	status := GetStrategyRuntimeStatus(ctx, database, strategyVersion)
	if !status.Ready {
		return fmt.Errorf("%w: %s", ErrStrategyRuntimeUnavailable, status.Reason)
	}
	if status.Mode != StrategyModeLive {
		return fmt.Errorf("%w: %s", ErrStrategyPaused, status.Reason)
	}
	if strings.TrimSpace(status.CurrentStrategyVersion) != strings.TrimSpace(strategyVersion) {
		return fmt.Errorf("%w: runtime strategy=%s expected=%s", ErrStrategyPaused, status.CurrentStrategyVersion, strategyVersion)
	}
	return nil
}

func SetStrategyRuntimeMode(ctx context.Context, database *gorm.DB, mode, strategyVersion, reason, changedBy string) (StrategyRuntimeStatus, error) {
	mode = normalizeStrategyMode(mode)
	strategyVersion = strings.TrimSpace(strategyVersion)
	if mode == "" {
		return StrategyRuntimeStatus{}, errors.New("strategy runtime mode must be paused or live")
	}
	if strategyVersion == "" {
		return StrategyRuntimeStatus{}, errors.New("current strategy version is required")
	}
	if !HasStrategyRuntimeControl(database) {
		return StrategyRuntimeStatus{}, ErrStrategyRuntimeUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"mode":                     mode,
		"current_strategy_version": strategyVersion,
		"reason":                   strings.TrimSpace(reason),
		"changed_by":               strings.TrimSpace(changedBy),
		"changed_at":               now,
		"updated_at":               now,
	}
	result := database.WithContext(ctx).Model(&StrategyRuntimeControl{}).Where("id = ?", 1).Updates(updates)
	if result.Error != nil {
		return StrategyRuntimeStatus{}, result.Error
	}
	if result.RowsAffected != 1 {
		return StrategyRuntimeStatus{}, ErrStrategyRuntimeUnavailable
	}
	return GetStrategyRuntimeStatus(ctx, database, strategyVersion), nil
}

func normalizeStrategyMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case StrategyModePaused:
		return StrategyModePaused
	case StrategyModeLive:
		return StrategyModeLive
	default:
		return ""
	}
}
