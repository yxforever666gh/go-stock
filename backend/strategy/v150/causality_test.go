package v150

import (
	"errors"
	"testing"
	"time"
)

func TestValidateCausalTimeline(t *testing.T) {
	base := strategyTestTime(9, 30)
	valid := CausalTimeline{
		SourceAt:     base,
		AvailableAt:  base.Add(time.Minute),
		DataCutoffAt: base.Add(2 * time.Minute),
		DecisionAt:   base.Add(3 * time.Minute),
		ValidFromAt:  base.Add(15 * time.Minute),
		TriggerAt:    base.Add(30 * time.Minute),
		OrderAt:      base.Add(45 * time.Minute),
		FillAt:       base.Add(45 * time.Minute),
	}
	if err := ValidateCausalTimeline(valid); err != nil {
		t.Fatalf("valid timeline rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*CausalTimeline)
	}{
		{"zero source", func(value *CausalTimeline) { value.SourceAt = time.Time{} }},
		{"future availability", func(value *CausalTimeline) { value.AvailableAt = value.DataCutoffAt.Add(time.Second) }},
		{"future cutoff", func(value *CausalTimeline) { value.DataCutoffAt = value.DecisionAt.Add(time.Second) }},
		{"non-strict valid from", func(value *CausalTimeline) { value.ValidFromAt = value.DecisionAt }},
		{"trigger before valid", func(value *CausalTimeline) { value.TriggerAt = value.ValidFromAt.Add(-time.Second) }},
		{"order at trigger", func(value *CausalTimeline) { value.OrderAt = value.TriggerAt }},
		{"fill before order", func(value *CausalTimeline) { value.FillAt = value.OrderAt.Add(-time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if err := ValidateCausalTimeline(value); !errors.Is(err, ErrCausalTimeline) {
				t.Fatalf("expected causal rejection, got %v", err)
			}
		})
	}
}
