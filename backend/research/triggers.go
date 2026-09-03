package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultAnalysisLease = 10 * time.Minute
	sellTriggerCoalesce  = 2 * time.Minute
)

type AnalysisTriggerClaim struct {
	Run      AnalysisRun       `json:"run"`
	Triggers []AnalysisTrigger `json:"triggers"`
}

type CapitalDeploymentStatus struct {
	Enabled             bool       `json:"enabled"`
	Cash                float64    `json:"cash"`
	ReservedCash        float64    `json:"reservedCash"`
	NetAssetValue       float64    `json:"netAssetValue"`
	CapitalBuffer       float64    `json:"capitalBuffer"`
	DeployableCash      float64    `json:"deployableCash"`
	CapitalUtilization  float64    `json:"capitalUtilization"`
	AvailableSlots      int        `json:"availableSlots"`
	MaxImmediateBuys    int        `json:"maxImmediateBuys"`
	PendingTriggerCount int64      `json:"pendingTriggerCount"`
	RunningTriggerCount int64      `json:"runningTriggerCount"`
	NextAnalysisAt      *time.Time `json:"nextAnalysisAt,omitempty"`
	CanAnalyze          bool       `json:"canAnalyze"`
	Reason              string     `json:"reason"`
}

func triggerBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 5 * time.Minute
	case 2:
		return 10 * time.Minute
	default:
		return 20 * time.Minute
	}
}

func normalizedTriggerSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	switch source {
	case TriggerSourceSell, TriggerSourceStartup, TriggerSourceCapitalGap:
		return source, nil
	default:
		return "", fmt.Errorf("invalid analysis trigger source %q", source)
	}
}

func enqueueTriggerInTransaction(tx *gorm.DB, trigger *AnalysisTrigger) error {
	if trigger.TriggerID == "" {
		trigger.TriggerID = newID()
	}
	if trigger.SourceKey == "" {
		trigger.SourceKey = trigger.TriggerID
	}
	if trigger.Status == "" {
		trigger.Status = TriggerStatusQueued
	}
	if trigger.AvailableAt.IsZero() {
		trigger.AvailableAt = time.Now()
	}
	if trigger.CoalesceUntil.IsZero() {
		trigger.CoalesceUntil = trigger.AvailableAt
	}
	if trigger.Source == TriggerSourceSell {
		windowEnd := trigger.AvailableAt.Add(sellTriggerCoalesce)
		trigger.CoalesceUntil = windowEnd
		// A stream of sales less than two minutes apart forms one debounce
		// window; extending existing queued events prevents the first sale from
		// being claimed while the latest one is still coalescing.
		if err := tx.Model(&AnalysisTrigger{}).
			Where("source = ? AND status = ? AND available_at <= ? AND coalesce_until >= ?", TriggerSourceSell, TriggerStatusQueued, trigger.AvailableAt, trigger.AvailableAt).
			Update("coalesce_until", windowEnd).Error; err != nil {
			return err
		}
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source"}, {Name: "source_key"}}, DoNothing: true}).Create(trigger).Error
}

func (r *Repository) EnqueueAnalysisTrigger(ctx context.Context, source, sourceKey, reason string, availableAt time.Time) (AnalysisTrigger, error) {
	source, err := normalizedTriggerSource(source)
	if err != nil {
		return AnalysisTrigger{}, err
	}
	if availableAt.IsZero() {
		availableAt = time.Now()
	}
	trigger := AnalysisTrigger{TriggerID: newID(), Source: source, SourceKey: strings.TrimSpace(sourceKey), Reason: strings.TrimSpace(reason),
		Status: TriggerStatusQueued, AvailableAt: availableAt, CoalesceUntil: availableAt}
	err = transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error { return enqueueTriggerInTransaction(tx, &trigger) })
	if err != nil {
		return AnalysisTrigger{}, err
	}
	var stored AnalysisTrigger
	if findErr := r.db.WithContext(ctx).Where("source = ? AND source_key = ?", trigger.Source, trigger.SourceKey).First(&stored).Error; findErr == nil {
		return stored, nil
	}
	return trigger, nil
}

func (s *Service) EnqueueCapitalGapTrigger(ctx context.Context, source, sourceKey, reason string, availableAt time.Time) (AnalysisTrigger, error) {
	return s.repository.EnqueueAnalysisTrigger(ctx, source, sourceKey, reason, availableAt)
}

// RecoverExpiredAnalysisLeases makes an interrupted batch claim retryable and
// closes its reserved run. It is idempotent and safe on every startup/tick.
func (r *Repository) RecoverExpiredAnalysisLeases(ctx context.Context, now time.Time) (int64, error) {
	var recovered int64
	err := transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		recovered = 0
		var triggers []AnalysisTrigger
		if err := tx.Where("status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?", TriggerStatusRunning, now).Find(&triggers).Error; err != nil {
			return err
		}
		for _, trigger := range triggers {
			updates := map[string]any{"lease_owner": "", "lease_expires_at": nil, "claimed_at": nil, "analysis_run_id": "", "last_error": "analysis lease expired"}
			if trigger.AttemptCount >= 3 {
				updates["status"], updates["completed_at"] = TriggerStatusFailed, now
			} else {
				updates["status"], updates["available_at"] = TriggerStatusQueued, now.Add(triggerBackoff(trigger.AttemptCount))
			}
			if err := tx.Model(&AnalysisTrigger{}).Where("id = ? AND status = ?", trigger.ID, TriggerStatusRunning).Updates(updates).Error; err != nil {
				return err
			}
			recovered++
		}
		return tx.Model(&AnalysisRun{}).
			Where("status IN ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?", []string{"queued", "running"}, now).
			Updates(map[string]any{"status": "failed", "completed_at": now, "failure_reason": "analysis lease expired", "lease_owner": "", "lease_expires_at": nil}).Error
	})
	return recovered, err
}

func (s *Service) RecoverExpiredAnalysisLeases(ctx context.Context, now time.Time) (int64, error) {
	return s.repository.RecoverExpiredAnalysisLeases(ctx, now)
}

// ClaimAnalysisTriggerBatch atomically claims every mature trigger currently
// eligible for one full run and reserves that queued AnalysisRun. The run row
// is the database-level singleton guard shared across runtimes/processes.
func (r *Repository) ClaimAnalysisTriggerBatch(ctx context.Context, now time.Time, owner string, lease time.Duration) (AnalysisTriggerClaim, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return AnalysisTriggerClaim{}, false, errors.New("analysis lease owner is required")
	}
	if lease <= 0 {
		lease = defaultAnalysisLease
	}
	var claim AnalysisTriggerClaim
	reservedRunID := newID()
	err := transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		claim = AnalysisTriggerClaim{}
		var active int64
		if err := tx.Model(&AnalysisRun{}).
			Where("status IN ? AND trigger_id <> '' AND (lease_expires_at IS NULL OR lease_expires_at > ?)", []string{"queued", "running"}, now).
			Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		var activeTriggers int64
		if err := tx.Model(&AnalysisTrigger{}).Where("status = ? AND lease_expires_at > ?", TriggerStatusRunning, now).Count(&activeTriggers).Error; err != nil {
			return err
		}
		if activeTriggers > 0 {
			return nil
		}
		var triggers []AnalysisTrigger
		if err := tx.Where("status = ? AND available_at <= ? AND coalesce_until <= ?", TriggerStatusQueued, now, now).
			Order("available_at ASC, created_at ASC, id ASC").Find(&triggers).Error; err != nil {
			return err
		}
		if len(triggers) == 0 {
			return nil
		}
		target, _ := r.capitalDeploymentPolicy()
		capacity, err := recommendationCapacity(tx, target)
		if err != nil {
			return err
		}
		if capacity.DeployableCash < TargetCashPerTrade-1e-8 {
			return nil
		}
		ids, reasons, sources := make([]string, 0, len(triggers)), make([]string, 0, len(triggers)), make([]string, 0, len(triggers))
		seenSource := map[string]bool{}
		for _, trigger := range triggers {
			ids = append(ids, trigger.TriggerID)
			if trigger.Reason != "" {
				reasons = append(reasons, trigger.Reason)
			}
			if !seenSource[trigger.Source] {
				seenSource[trigger.Source] = true
				sources = append(sources, trigger.Source)
			}
		}
		idsJSON, _ := json.Marshal(ids)
		expires := now.Add(lease)
		run := AnalysisRun{RunID: reservedRunID, ScheduledFor: now, StartedAt: now, Status: "queued", ModelAttemptLogJSON: "[]",
			TriggerID: ids[0], TriggerIDsJSON: string(idsJSON), TriggerSource: strings.Join(sources, ","), TriggerReason: strings.Join(reasons, "；"),
			FundingCash: capacity.Cash, FundingReservedCash: capacity.ReservedCash, FundingNetAssetValue: capacity.NetAssetValue,
			FundingCapitalBuffer: capacity.CapitalBuffer, FundingDeployableCash: capacity.DeployableCash, FundingAvailableSlots: capacity.AvailableSlots,
			LeaseOwner: owner, LeaseExpiresAt: &expires, StrategyVersion: CurrentStrategyVersion, DataProfileVersion: CurrentDataProfileVersion}
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		result := tx.Model(&AnalysisTrigger{}).Where("trigger_id IN ? AND status = ?", ids, TriggerStatusQueued).Updates(map[string]any{
			"status": TriggerStatusRunning, "claimed_at": now, "lease_owner": owner, "lease_expires_at": expires,
			"analysis_run_id": run.RunID, "attempt_count": gorm.Expr("attempt_count + 1"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(ids)) {
			return errors.New("analysis trigger batch changed during claim")
		}
		for index := range triggers {
			triggers[index].Status, triggers[index].AnalysisRunID, triggers[index].LeaseOwner = TriggerStatusRunning, run.RunID, owner
			triggers[index].ClaimedAt, triggers[index].LeaseExpiresAt = &now, &expires
			triggers[index].AttemptCount++
		}
		claim = AnalysisTriggerClaim{Run: run, Triggers: triggers}
		return nil
	})
	return claim, claim.Run.RunID != "", err
}

func (s *Service) ClaimAnalysisTriggerBatch(ctx context.Context, now time.Time, owner string, lease time.Duration) (AnalysisTriggerClaim, bool, error) {
	return s.repository.ClaimAnalysisTriggerBatch(ctx, now, owner, lease)
}

func (r *Repository) BeginClaimedAnalysis(ctx context.Context, runID, owner string, now time.Time, request AnalysisRequest) (AnalysisRun, error) {
	var run AnalysisRun
	err := transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Where("run_id = ? AND status = ? AND lease_owner = ? AND lease_expires_at > ?", runID, "queued", owner, now).First(&run).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": "running", "started_at": now, "ai_config_id": request.AIConfigID,
			"provider_name": request.ProviderName, "model_name": request.ModelName}
		if err := tx.Model(&AnalysisRun{}).Where("id = ? AND status = ?", run.ID, "queued").Updates(updates).Error; err != nil {
			return err
		}
		run.Status, run.StartedAt = "running", now
		run.AIConfigID, run.ProviderName, run.ModelName = request.AIConfigID, request.ProviderName, request.ModelName
		return nil
	})
	return run, err
}

func (r *Repository) RenewAnalysisTriggerLease(ctx context.Context, runID, owner string, now time.Time, lease time.Duration) error {
	if lease <= 0 {
		lease = defaultAnalysisLease
	}
	expires := now.Add(lease)
	return transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Model(&AnalysisRun{}).Where("run_id = ? AND lease_owner = ? AND status IN ?", runID, owner, []string{"queued", "running"}).Update("lease_expires_at", expires)
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return errors.New("analysis run lease is no longer owned")
		}
		return tx.Model(&AnalysisTrigger{}).Where("analysis_run_id = ? AND lease_owner = ? AND status = ?", runID, owner, TriggerStatusRunning).Update("lease_expires_at", expires).Error
	})
}

func (s *Service) RenewAnalysisTriggerLease(ctx context.Context, runID, owner string, now time.Time, lease time.Duration) error {
	return s.repository.RenewAnalysisTriggerLease(ctx, runID, owner, now, lease)
}

func (r *Repository) CompleteAnalysisTriggerBatch(ctx context.Context, runID, owner string, now time.Time) error {
	return transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Model(&AnalysisTrigger{}).Where("analysis_run_id = ? AND lease_owner = ? AND status = ?", runID, owner, TriggerStatusRunning).
			Updates(map[string]any{"status": TriggerStatusCompleted, "completed_at": now, "lease_owner": "", "lease_expires_at": nil})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("analysis trigger batch is no longer owned")
		}
		return nil
	})
}

func (s *Service) CompleteAnalysisTriggerBatch(ctx context.Context, runID, owner string, now time.Time) error {
	return s.repository.CompleteAnalysisTriggerBatch(ctx, runID, owner, now)
}

func (r *Repository) FailAnalysisTriggerBatch(ctx context.Context, runID, owner string, now time.Time, cause error) error {
	reason := "analysis failed"
	if cause != nil {
		reason = cause.Error()
	}
	return transactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		var triggers []AnalysisTrigger
		if err := tx.Where("analysis_run_id = ? AND lease_owner = ? AND status = ?", runID, owner, TriggerStatusRunning).Find(&triggers).Error; err != nil {
			return err
		}
		for _, trigger := range triggers {
			updates := map[string]any{"last_error": reason, "lease_owner": "", "lease_expires_at": nil, "claimed_at": nil, "analysis_run_id": ""}
			if trigger.AttemptCount >= 3 {
				updates["status"], updates["completed_at"] = TriggerStatusFailed, now
			} else {
				updates["status"], updates["available_at"] = TriggerStatusQueued, now.Add(triggerBackoff(trigger.AttemptCount))
			}
			if err := tx.Model(&AnalysisTrigger{}).Where("id = ?", trigger.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		return tx.Model(&AnalysisRun{}).Where("run_id = ? AND lease_owner = ? AND status IN ?", runID, owner, []string{"queued", "running"}).
			Updates(map[string]any{"status": "failed", "completed_at": now, "failure_reason": reason, "lease_owner": "", "lease_expires_at": nil}).Error
	})
}

func (s *Service) FailAnalysisTriggerBatch(ctx context.Context, runID, owner string, now time.Time, cause error) error {
	return s.repository.FailAnalysisTriggerBatch(ctx, runID, owner, now, cause)
}

func (s *Service) CapitalDeploymentStatus(ctx context.Context, now time.Time) (CapitalDeploymentStatus, error) {
	capacity, err := s.repository.RecommendationCapacity(ctx)
	if err != nil {
		return CapitalDeploymentStatus{}, err
	}
	_, maxImmediate := s.repository.capitalDeploymentPolicy()
	status := CapitalDeploymentStatus{Enabled: true, Cash: capacity.Cash, ReservedCash: capacity.ReservedCash,
		NetAssetValue: capacity.NetAssetValue, CapitalBuffer: capacity.CapitalBuffer, DeployableCash: capacity.DeployableCash,
		CapitalUtilization: capacity.CapitalUtilization, AvailableSlots: capacity.AvailableSlots, MaxImmediateBuys: maxImmediate}
	var triggers []AnalysisTrigger
	if err := s.repository.db.WithContext(ctx).Where("status IN ?", []string{TriggerStatusQueued, TriggerStatusRunning}).Find(&triggers).Error; err != nil {
		return status, err
	}
	for _, trigger := range triggers {
		if trigger.Status == TriggerStatusQueued {
			status.PendingTriggerCount++
		} else {
			status.RunningTriggerCount++
		}
		candidate := trigger.AvailableAt
		if trigger.CoalesceUntil.After(candidate) {
			candidate = trigger.CoalesceUntil
		}
		if status.NextAnalysisAt == nil || candidate.Before(*status.NextAnalysisAt) {
			value := candidate
			status.NextAnalysisAt = &value
		}
	}
	ready := status.NextAnalysisAt != nil && !status.NextAnalysisAt.After(now)
	status.CanAnalyze = capacity.DeployableCash >= TargetCashPerTrade-1e-8 && status.RunningTriggerCount == 0 && status.PendingTriggerCount > 0 && ready
	switch {
	case capacity.DeployableCash < TargetCashPerTrade-1e-8:
		status.Reason = fmt.Sprintf("可部署资金 %.2f 元不足 %.2f 元", capacity.DeployableCash, TargetCashPerTrade)
	case status.RunningTriggerCount > 0:
		status.Reason = "已有资金补位分析正在运行"
	case status.PendingTriggerCount == 0:
		status.Reason = "当前没有待处理资金补位事件"
	case status.NextAnalysisAt != nil && status.NextAnalysisAt.After(now):
		status.Reason = "等待下一次完整资金补位分析"
	default:
		status.Reason = "资金与触发事件已满足分析条件"
	}
	return status, nil
}

func maxImmediateForCapacity(capacity RecommendationCapacity, configured int) int {
	if configured <= 0 {
		configured = 2
	}
	return int(math.Min(float64(configured), math.Min(2, float64(capacity.AvailableSlots))))
}
