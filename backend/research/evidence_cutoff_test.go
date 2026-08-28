package research

import (
	"testing"
	"time"
)

func TestResearchEvidenceCutoff(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	morning := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	if got := researchEvidenceCutoff(morning, time.Time{}); !got.Equal(morning.Add(24 * time.Hour)) {
		t.Fatalf("unexpected provisional cutoff: %v", got)
	}
	afterClose := time.Date(2026, 8, 28, 16, 0, 0, 0, location)
	if got := researchEvidenceCutoff(afterClose, time.Time{}); !got.Equal(afterClose.Add(24 * time.Hour)) {
		t.Fatalf("unexpected after-close cutoff: %v", got)
	}
	explicit := morning.Add(5 * time.Minute)
	if got := researchEvidenceCutoff(morning, explicit); !got.Equal(explicit) {
		t.Fatalf("explicit cutoff ignored: %v", got)
	}
}
