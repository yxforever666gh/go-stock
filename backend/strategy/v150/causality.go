package v150

import (
	"errors"
	"fmt"
	"time"
)

var ErrCausalTimeline = errors.New("strategy causal timeline violation")

// CausalTimeline is the complete point-in-time chain for one executed
// candidate. It deliberately distinguishes source publication and local
// availability from the run's cutoff and decision timestamps.
type CausalTimeline struct {
	SourceAt     time.Time
	AvailableAt  time.Time
	DataCutoffAt time.Time
	DecisionAt   time.Time
	ValidFromAt  time.Time
	TriggerAt    time.Time
	OrderAt      time.Time
	FillAt       time.Time
}

// ValidateCausalTimeline enforces:
// source <= available <= cutoff <= decision < valid_from <= trigger < order <= fill.
// A candidate with any missing timestamp is rejected instead of being
// silently repaired with a later clock value.
func ValidateCausalTimeline(value CausalTimeline) error {
	timestamps := []struct {
		name string
		at   time.Time
	}{
		{"source_at", value.SourceAt},
		{"available_at", value.AvailableAt},
		{"data_cutoff_at", value.DataCutoffAt},
		{"decision_at", value.DecisionAt},
		{"valid_from_at", value.ValidFromAt},
		{"trigger_at", value.TriggerAt},
		{"order_at", value.OrderAt},
		{"fill_at", value.FillAt},
	}
	for _, timestamp := range timestamps {
		if timestamp.at.IsZero() {
			return fmt.Errorf("%w: %s is zero", ErrCausalTimeline, timestamp.name)
		}
	}
	if value.SourceAt.After(value.AvailableAt) {
		return fmt.Errorf("%w: source_at after available_at", ErrCausalTimeline)
	}
	if value.AvailableAt.After(value.DataCutoffAt) {
		return fmt.Errorf("%w: available_at after data_cutoff_at", ErrCausalTimeline)
	}
	if value.DataCutoffAt.After(value.DecisionAt) {
		return fmt.Errorf("%w: data_cutoff_at after decision_at", ErrCausalTimeline)
	}
	if !value.DecisionAt.Before(value.ValidFromAt) {
		return fmt.Errorf("%w: decision_at must be before valid_from_at", ErrCausalTimeline)
	}
	if value.ValidFromAt.After(value.TriggerAt) {
		return fmt.Errorf("%w: valid_from_at after trigger_at", ErrCausalTimeline)
	}
	if !value.TriggerAt.Before(value.OrderAt) {
		return fmt.Errorf("%w: trigger_at must be before order_at", ErrCausalTimeline)
	}
	if value.OrderAt.After(value.FillAt) {
		return fmt.Errorf("%w: order_at after fill_at", ErrCausalTimeline)
	}
	return nil
}
