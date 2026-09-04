//go:build integration

package data

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRefreshResearchNewsLiveContract(t *testing.T) {
	if os.Getenv("GO_STOCK_LIVE_MARKET_NEWS") != "1" {
		t.Skip("set GO_STOCK_LIVE_MARKET_NEWS=1 to probe live market-news contracts")
	}
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "stock.db"), testSchemaMarketNews)
	InitAnalyzeSentiment()
	now := time.Now()
	NewMarketNewsApi().RefreshResearchNews(context.Background(), 15*time.Second)
	window, err := NewMarketNewsApi().GetNewsWindow(nil, now.Add(-24*time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if window.Status != NewsWindowStatusOK || len(window.Items) == 0 {
		t.Fatalf("refreshed window status=%q items=%d warning=%q", window.Status, len(window.Items), window.Warning)
	}
}
