package marketdata

import (
	"errors"
	"testing"
	"time"
)

func TestValidateTimelineRejectsLookahead(t *testing.T) {
	asOf := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	if err := ValidateTimeline(asOf.Add(-time.Minute), asOf.Add(time.Second), asOf); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("ValidateTimeline error = %v, want ErrInvalidObservation", err)
	}
	if err := ValidateTimeline(asOf.Add(-2*time.Minute), asOf.Add(-time.Minute), asOf); err != nil {
		t.Fatalf("valid timeline: %v", err)
	}
}

func TestSecurityStateTradableFailsClosed(t *testing.T) {
	if (SecurityState{Status: TradingStatusUnknown}).Tradable() {
		t.Fatal("unknown state must not be tradable")
	}
	if (SecurityState{Status: TradingStatusTradable, ST: true}).Tradable() {
		t.Fatal("ST security must not be tradable")
	}
	if !(SecurityState{Status: TradingStatusTradable}).Tradable() {
		t.Fatal("normal security should be tradable")
	}
}
