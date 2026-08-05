package persistence

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/models"

	"gorm.io/gorm"
)

// SQLite transactions are deferred by default. Serializing this short
// append transaction prevents two in-process writers from both validating
// against the same tail event before either INSERT reaches the unique index.
// Database uniqueness remains the cross-process safety boundary.
var appendStrategyOrderEventsMu sync.Mutex

// AppendStrategyOrderEvents appends future lifecycle events to an already
// frozen strategy run. It never updates the run row or an existing event.
func AppendStrategyOrderEvents(ctx context.Context, database *gorm.DB, runID string, events []models.OrderEvent) error {
	if database == nil {
		return fmt.Errorf("%w: database is nil", ErrInvalidImmutableRecord)
	}
	runID = strings.TrimSpace(runID)
	if runID == "" || len(events) == 0 {
		return fmt.Errorf("%w: run id and at least one order event are required", ErrInvalidImmutableRecord)
	}
	ordered := append([]models.OrderEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].RuleID != ordered[j].RuleID {
			return ordered[i].RuleID < ordered[j].RuleID
		}
		if ordered[i].Sequence != ordered[j].Sequence {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		return ordered[i].EventID < ordered[j].EventID
	})

	appendStrategyOrderEventsMu.Lock()
	defer appendStrategyOrderEventsMu.Unlock()
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run models.StrategyRunSnapshot
		if err := tx.Where("run_id = ? AND frozen_at IS NOT NULL", runID).First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: frozen run %s does not exist", ErrInvalidImmutableRecord, runID)
			}
			return err
		}

		var rules []models.RuleSnapshot
		if err := tx.Where("run_id = ?", runID).Order("rule_id ASC").Find(&rules).Error; err != nil {
			return err
		}
		var existing []models.OrderEvent
		if err := tx.Where("run_id = ?", runID).Order("rule_id ASC, sequence ASC, event_id ASC").Find(&existing).Error; err != nil {
			return err
		}

		type ledgerTail struct {
			sequence int
			at       time.Time
		}
		tails := make(map[string]ledgerTail, len(rules)+1)
		for i := range existing {
			event := existing[i]
			tail, exists := tails[event.RuleID]
			if !exists || event.Sequence > tail.sequence {
				tails[event.RuleID] = ledgerTail{sequence: event.Sequence, at: event.EventAt}
			}
		}
		eventIDs := make([]string, 0, len(ordered))
		seenIDs := make(map[string]struct{}, len(ordered))
		type ruleSequence struct {
			ruleID   string
			sequence int
		}
		sequenceKeys := make([]ruleSequence, 0, len(ordered))
		seenSequences := make(map[ruleSequence]struct{}, len(ordered))
		for i := range ordered {
			event := &ordered[i]
			tail := tails[event.RuleID]
			if err := validateAppendedOrderEvent(run, *event, tail.sequence, tail.at); err != nil {
				return fmt.Errorf("append order event %d: %w", i, err)
			}
			if _, exists := seenIDs[event.EventID]; exists {
				return fmt.Errorf("%w: duplicate event id %s in append batch", ErrImmutableConflict, event.EventID)
			}
			sequenceKey := ruleSequence{ruleID: event.RuleID, sequence: event.Sequence}
			if _, exists := seenSequences[sequenceKey]; exists {
				return fmt.Errorf("%w: duplicate rule %q sequence %d in append batch", ErrImmutableConflict, event.RuleID, event.Sequence)
			}
			seenIDs[event.EventID] = struct{}{}
			seenSequences[sequenceKey] = struct{}{}
			eventIDs = append(eventIDs, event.EventID)
			sequenceKeys = append(sequenceKeys, sequenceKey)
			tails[event.RuleID] = ledgerTail{sequence: event.Sequence, at: event.EventAt}
		}
		var conflictCount int64
		if err := tx.Model(&models.OrderEvent{}).Where("event_id IN ?", eventIDs).Count(&conflictCount).Error; err != nil {
			return err
		}
		if conflictCount != 0 {
			return fmt.Errorf("%w: event identity already exists", ErrImmutableConflict)
		}
		for _, key := range sequenceKeys {
			conflictCount = 0
			if err := tx.Model(&models.OrderEvent{}).
				Where("run_id = ? AND rule_id = ? AND sequence = ?", runID, key.ruleID, key.sequence).
				Count(&conflictCount).Error; err != nil {
				return err
			}
			if conflictCount != 0 {
				return fmt.Errorf("%w: run %s rule %q sequence %d already exists", ErrImmutableConflict, runID, key.ruleID, key.sequence)
			}
		}
		allEvents := append(append([]models.OrderEvent(nil), existing...), ordered...)
		if err := validateOrderEventStateMachine(run, rules, allEvents); err != nil {
			return fmt.Errorf("append order event state transition: %w", err)
		}
		if err := tx.CreateInBatches(&ordered, 100).Error; err != nil {
			if isOrderEventUniqueConstraintError(err) {
				return fmt.Errorf("%w: %v", ErrImmutableConflict, err)
			}
			return err
		}
		return nil
	})
}

func validateAppendedOrderEvent(run models.StrategyRunSnapshot, event models.OrderEvent, lastSequence int, lastAt time.Time) error {
	if event.ID != 0 || !event.CreatedAt.IsZero() {
		return fmt.Errorf("%w: database identity/timestamp must be unset for event %s", ErrInvalidImmutableRecord, event.EventID)
	}
	if event.RunID != run.RunID || event.StrategyVersion != run.StrategyVersion || event.TradeDate != run.TradeDate {
		return fmt.Errorf("%w: event %s does not belong to frozen run %s", ErrInvalidImmutableRecord, event.EventID, run.RunID)
	}
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.Symbol) == "" || strings.TrimSpace(event.EventType) == "" || event.EventAt.IsZero() || !validFrozenPayload(event.SnapshotHash, event.PayloadJSON, event.FrozenAt) {
		return fmt.Errorf("%w: appended event is incomplete or not frozen", ErrInvalidImmutableRecord)
	}
	if err := verifySnapshotRecord(event); err != nil {
		return fmt.Errorf("%w: appended event seal: %v", ErrInvalidImmutableRecord, err)
	}
	if event.Sequence <= 0 {
		return fmt.Errorf("%w: sequence must be positive", ErrInvalidImmutableRecord)
	}
	if event.Sequence <= lastSequence {
		return fmt.Errorf("%w: rule %q sequence %d must be greater than current tail %d", ErrImmutableConflict, event.RuleID, event.Sequence, lastSequence)
	}
	if !lastAt.IsZero() && event.EventAt.Before(lastAt) {
		return fmt.Errorf("%w: event time occurs before rule %q tail", ErrInvalidImmutableRecord, event.RuleID)
	}
	if run.ValidFromAt != nil && event.EventAt.Before(*run.ValidFromAt) {
		return fmt.Errorf("%w: event occurs before run validFrom", ErrInvalidImmutableRecord)
	}
	if event.FrozenAt.Before(event.EventAt) {
		return fmt.Errorf("%w: event frozenAt occurs before eventAt", ErrInvalidImmutableRecord)
	}
	return nil
}

func isOrderEventUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique constraint") || strings.Contains(text, "duplicate key") || strings.Contains(text, "duplicated key")
}
