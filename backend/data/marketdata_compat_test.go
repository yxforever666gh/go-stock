package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/marketdata"
)

func TestCompatibilityMarketDataReaderHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (CompatibilityMarketDataReader{}).DailyBars(ctx, marketdata.DailyBarsRequest{
		Symbol: "600000.SH", Start: time.Now(), End: time.Now(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DailyBars error = %v, want context.Canceled", err)
	}
}
