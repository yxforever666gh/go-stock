package news

import (
	"errors"
	"testing"
	"time"
)

func TestQueryRequiresCausalWindow(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	valid := Query{Scope: ScopeMarket, Start: now.Add(-time.Hour), End: now, AsOf: now}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid query: %v", err)
	}
	future := valid
	future.End = now.Add(time.Minute)
	if err := future.Validate(); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("future window error = %v", err)
	}
	security := valid
	security.Scope = ScopeSecurity
	if err := security.Validate(); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("missing security symbol error = %v", err)
	}
}
