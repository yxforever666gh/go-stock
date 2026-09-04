//go:build integration

package data

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestResearch2FullMarketProviderLiveContract(t *testing.T) {
	if os.Getenv("GO_STOCK_LIVE_EASTMONEY") != "1" {
		t.Skip("set GO_STOCK_LIVE_EASTMONEY=1 to probe the live Eastmoney contract")
	}
	collector := &research2EvidenceCollector{stocks: &StockDataApi{client: newNoProxyRestyClient()}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	rows, err := collector.fetchFullMarket(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 1000 {
		t.Fatalf("live full-market response is incomplete: %d rows", len(rows))
	}
	if rows[0].Code == "" || rows[0].Price <= 0 || rows[0].Price > 10000 || rows[0].ChangeRate < -100 || rows[0].ChangeRate > 100 {
		t.Fatalf("live quote scaling is invalid: %+v", rows[0])
	}
}
