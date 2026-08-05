package marketintel

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvidenceJSONKeepsPointInTimeFields(t *testing.T) {
	at := time.Date(2026, 8, 6, 9, 30, 0, 0, time.UTC)
	raw, err := json.Marshal(Evidence{ID: "e1", Type: EvidenceEvent, Source: "cache", SourceAt: at, AvailableAt: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["sourceAt"] == nil || got["availableAt"] == nil {
		t.Fatalf("point-in-time fields missing: %s", raw)
	}
}
