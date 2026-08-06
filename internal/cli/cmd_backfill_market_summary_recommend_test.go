package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	cliports "go-stock/internal/cli/ports"
)

type recordingMarketSummaryBackfill struct {
	reports   []cliports.MarketSummaryReport
	listStart time.Time
	listEnd   time.Time
	saved     int
	saveCalls int
}

func (b *recordingMarketSummaryBackfill) ListReports(_ context.Context, start, end time.Time) ([]cliports.MarketSummaryReport, error) {
	b.listStart, b.listEnd = start, end
	return b.reports, nil
}

func (b *recordingMarketSummaryBackfill) SaveRecommendations(_ context.Context, _ cliports.MarketSummaryReport) (int, error) {
	b.saveCalls++
	return b.saved, nil
}

func TestBackfillMarketSummaryRecommendUsesPortAndDryRunDoesNotSave(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	backfill := &recordingMarketSummaryBackfill{
		reports: []cliports.MarketSummaryReport{{
			ID:           11,
			CreatedAt:    time.Date(2026, 8, 6, 10, 0, 0, 0, location),
			ProviderName: "provider",
			ModelName:    "model",
		}},
		saved: 3,
	}
	var stdout, stderr bytes.Buffer
	err := runBackfillMarketSummaryRecommendWithPort(
		[]string{"--date", "2026-08-06", "--dry-run"},
		GlobalOptions{}, &stdout, &stderr, backfill,
	)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if backfill.saveCalls != 0 {
		t.Fatalf("save calls = %d, want 0 in dry-run", backfill.saveCalls)
	}
	if !backfill.listStart.Equal(time.Date(2026, 8, 6, 0, 0, 0, 0, time.Local)) || !backfill.listEnd.Equal(backfill.listStart.Add(24*time.Hour)) {
		t.Fatalf("unexpected query window: %s - %s", backfill.listStart, backfill.listEnd)
	}
	if !strings.Contains(stdout.String(), "1") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}
