package service

import (
	"context"
	"errors"
	"testing"

	"go-stock/backend/legacy"
)

type recordingLegacyReader struct {
	findCalls int
	listCalls int
	record    legacy.Recommendation
	records   []legacy.Recommendation
}

func (r *recordingLegacyReader) Find(context.Context, uint) (legacy.Recommendation, error) {
	r.findCalls++
	return r.record, nil
}

func (r *recordingLegacyReader) List(context.Context, legacy.Query) ([]legacy.Recommendation, error) {
	r.listCalls++
	return append([]legacy.Recommendation(nil), r.records...), nil
}

func TestLegacyServiceRequiresExplicitLegacyCohort(t *testing.T) {
	reader := &recordingLegacyReader{record: legacy.Recommendation{ID: 7, StrategyVersion: "1.4.2"}}
	service := NewLegacyService(reader)

	got, err := service.Find(context.Background(), StrategyCohortLegacy, 7)
	if err != nil || got.ID != 7 || reader.findCalls != 1 {
		t.Fatalf("record=%+v calls=%d err=%v", got, reader.findCalls, err)
	}
	for _, cohort := range []StrategyCohort{"", "all", StrategyCohortCurrent} {
		if _, err := service.List(context.Background(), cohort, legacy.Query{}); !errors.Is(err, ErrInvalidStrategyCohort) {
			t.Fatalf("cohort %q error = %v, want ErrInvalidStrategyCohort", cohort, err)
		}
	}
	if reader.listCalls != 0 {
		t.Fatalf("invalid cohort reached legacy reader %d times", reader.listCalls)
	}
}

func TestLegacyServiceRejectsMissingReader(t *testing.T) {
	service := NewLegacyService(nil)
	if _, err := service.Find(nil, StrategyCohortLegacy, 1); !errors.Is(err, ErrLegacyReaderUnavailable) {
		t.Fatalf("error = %v, want ErrLegacyReaderUnavailable", err)
	}
}
