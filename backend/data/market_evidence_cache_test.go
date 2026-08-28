package data

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-stock/backend/marketdata"

	"github.com/glebarez/sqlite"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func testMarketEvidenceCacheDB(t *testing.T, migrate bool) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if migrate {
		if err = database.AutoMigrate(&marketTradeTickCache{}, &marketAuctionSnapshotCache{}); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestMarketEvidenceCacheUpsertAndHistoricalRead(t *testing.T) {
	database := testMarketEvidenceCacheDB(t, true)
	service := NewMarketEvidenceServiceWithMinuteDB(database)
	liveNow := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiDataLocation())
	service.now = func() time.Time { return liveNow }
	tradeEnvelope := marketdata.DataEnvelope[TradesData]{Data: TradesData{Code: "sh600000", AssetType: "stock", Date: "2026-08-28", Items: []TradeTick{{Time: "09:30:01", Price: 10, Volume: 2, Amount: 2000, Side: "buy"}, {Time: "09:30:01", Price: 10.01, Volume: 3, Amount: 3003, Side: "sell"}}}, Source: "eastmoney", Status: marketdata.StatusOK, Errors: []marketdata.DataError{}}
	tradeEnvelope = service.cacheTradesEnvelope(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock"}, tradeEnvelope)
	tradeEnvelope.Data.Items[0].Price = 10.05
	tradeEnvelope = service.cacheTradesEnvelope(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock"}, tradeEnvelope)
	var tradeCount int64
	if err := database.Model(&marketTradeTickCache{}).Count(&tradeCount).Error; err != nil || tradeCount != 2 {
		t.Fatalf("trade count=%d err=%v", tradeCount, err)
	}
	var updatedTrade marketTradeTickCache
	if err := database.Order("sequence").First(&updatedTrade).Error; err != nil || updatedTrade.Price != 10.05 {
		t.Fatalf("upsert did not update existing tick: %#v err=%v", updatedTrade, err)
	}

	final := AuctionSnapshot{Time: "09:25:00", Price: 10.2, MatchedVolume: 5, MatchedAmount: 5100}
	auctionEnvelope := marketdata.DataEnvelope[AuctionData]{Data: AuctionData{Code: "sh600000", AssetType: "stock", Date: "2026-08-28", Snapshots: []AuctionSnapshot{{Time: "09:19:59", Price: 10, MatchedVolume: 1}, {Time: "09:22:00", Price: 10.1, MatchedVolume: 2}, final}, FinalSnapshot: &final}, Source: "eastmoney", Status: marketdata.StatusPartial, Errors: []marketdata.DataError{}}
	for range 2 {
		auctionEnvelope = service.cacheAuctionEnvelope(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock"}, auctionEnvelope)
	}
	var auctionRows []marketAuctionSnapshotCache
	if err := database.Order("observed_at, phase").Find(&auctionRows).Error; err != nil {
		t.Fatal(err)
	}
	if len(auctionRows) != 4 {
		t.Fatalf("expected 3 segments plus final, got %d: %#v", len(auctionRows), auctionRows)
	}
	phases := map[string]bool{}
	for _, row := range auctionRows {
		phases[row.Phase] = true
	}
	for _, phase := range []string{"cancellable", "non_cancellable", "opening_match", "final"} {
		if !phases[phase] {
			t.Fatalf("missing auction phase %q", phase)
		}
	}

	service.now = func() time.Time { return liveNow.AddDate(0, 0, 1) }
	cachedTrades := service.Trades(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock", Date: "2026-08-28", Limit: 1})
	if cachedTrades.Status != marketdata.StatusOK || cachedTrades.Source != "cache" || len(cachedTrades.Data.Items) != 1 || cachedTrades.Data.NextCursor != "1" || !hasEnvelopeSource(cachedTrades.Sources, "eastmoney") {
		t.Fatalf("cached trades=%#v", cachedTrades)
	}
	cachedAuction := service.Auction(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock", Date: "2026-08-28"})
	if cachedAuction.Status != marketdata.StatusPartial || cachedAuction.Data.FinalSnapshot == nil || cachedAuction.Data.FinalSnapshot.Price != 10.2 || !hasEnvelopeSource(cachedAuction.Sources, "eastmoney") {
		t.Fatalf("cached auction=%#v", cachedAuction)
	}
	miss := service.Trades(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock", Date: "2026-08-27"})
	if miss.Status != marketdata.StatusUnavailable || !hasDataErrorCode(miss.Errors, "cache_miss") {
		t.Fatalf("cache miss=%#v", miss)
	}
}

func TestTradeRetentionKeepsThirtyObservedTradingDates(t *testing.T) {
	database := testMarketEvidenceCacheDB(t, true)
	dates := observedWeekdays(time.Date(2026, 8, 31, 10, 0, 0, 0, shanghaiDataLocation()), 31)
	rows := make([]marketTradeTickCache, 0, len(dates))
	for index, day := range dates {
		rows = append(rows, marketTradeTickCache{AssetType: "stock", Symbol: "sh600000", TradedAt: day.UnixMilli(), Sequence: int64(index), Price: 10, Volume: 1, Source: "fixture", UpdatedAt: day.UnixMilli()})
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	service := NewMarketEvidenceServiceWithMinuteDB(database)
	var count int64
	if err := database.Model(&marketTradeTickCache{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 30 {
		t.Fatalf("expected 30 observed trading dates, got %d", count)
	}
	var boundary, removed int64
	database.Model(&marketTradeTickCache{}).Where("traded_at = ?", dates[29].UnixMilli()).Count(&boundary)
	database.Model(&marketTradeTickCache{}).Where("traded_at = ?", dates[30].UnixMilli()).Count(&removed)
	if boundary != 1 || removed != 0 {
		t.Fatalf("retention boundary=%d removed=%d dates=%v/%v", boundary, removed, dates[29], dates[30])
	}
	service.now = func() time.Time { return dates[0].AddDate(0, 0, 1) }
	oldDate := dates[30].Format("2006-01-02")
	miss := service.Trades(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock", Date: oldDate})
	if miss.Status != marketdata.StatusUnavailable || !hasDataErrorCode(miss.Errors, "cache_miss") {
		t.Fatalf("expired historical date was returned: %#v", miss)
	}
}

func TestCleanupFailureKeepsLiveTradeData(t *testing.T) {
	database := testMarketEvidenceCacheDB(t, true)
	service := NewMarketEvidenceServiceWithMinuteDB(database)
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, shanghaiDataLocation())
	dates := observedWeekdays(now, 31)
	for index, day := range dates {
		row := marketTradeTickCache{AssetType: "stock", Symbol: "sh600001", TradedAt: day.UnixMilli(), Sequence: int64(index), Price: 10, Volume: 1, Source: "fixture", UpdatedAt: day.UnixMilli()}
		if err := database.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Exec(`CREATE TRIGGER block_trade_cleanup BEFORE DELETE ON market_trade_tick BEGIN SELECT RAISE(FAIL, 'cleanup blocked'); END`).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"prePrice":10,"details":["09:30:01,10.10,2,1,1"]}}`))
	}))
	defer server.Close()
	service.client, service.now, service.urls.details = resty.New(), func() time.Time { return now }, server.URL
	result := service.Trades(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock", Limit: 10})
	if result.Status != marketdata.StatusPartial || len(result.Data.Items) != 1 || !hasDataErrorCode(result.Errors, "cache_cleanup_failed") {
		t.Fatalf("cleanup failure dropped live data: %#v", result)
	}
}

func TestAuctionRetentionHelperAndFailureDegrade(t *testing.T) {
	database := testMarketEvidenceCacheDB(t, true)
	for index, date := range []string{"2026-08-31", "2026-08-28", "2026-08-27", "2026-08-26"} {
		at, _ := evidenceCacheTime(date, "09:25:00")
		row := marketAuctionSnapshotCache{AssetType: "stock", Symbol: "sh600000", TradeDate: date, ObservedAt: at.UnixMilli(), Phase: "final", Source: "fixture", UpdatedAt: int64(index + 1)}
		if err := database.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupAuctionSnapshotCache(context.Background(), database, 3); err != nil {
		t.Fatal(err)
	}
	var dates []string
	if err := database.Model(&marketAuctionSnapshotCache{}).Distinct("trade_date").Order("trade_date DESC").Pluck("trade_date", &dates).Error; err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 || dates[2] != "2026-08-27" {
		t.Fatalf("auction retained dates=%v", dates)
	}

	broken := testMarketEvidenceCacheDB(t, false)
	service := NewMarketEvidenceServiceWithMinuteDB(broken)
	service.now = func() time.Time { return time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiDataLocation()) }
	envelope := marketdata.DataEnvelope[TradesData]{Data: TradesData{Code: "sh600000", AssetType: "stock", Date: "2026-08-28", Items: []TradeTick{{Time: "09:30:01", Price: 10, Volume: 1}}}, Source: "eastmoney", Status: marketdata.StatusOK, Errors: []marketdata.DataError{}}
	degraded := service.cacheTradesEnvelope(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock"}, envelope)
	if degraded.Status != marketdata.StatusPartial || len(degraded.Data.Items) != 1 || !hasDataErrorCode(degraded.Errors, "cache_write_failed") {
		t.Fatalf("degraded=%#v", degraded)
	}
}

func observedWeekdays(latest time.Time, count int) []time.Time {
	result := make([]time.Time, 0, count)
	for day := latest; len(result) < count; day = day.AddDate(0, 0, -1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		result = append(result, day)
	}
	return result
}

func hasEnvelopeSource(values []marketdata.SourceState, provider string) bool {
	for _, value := range values {
		if value.Provider == provider {
			return true
		}
	}
	return false
}
func hasDataErrorCode(values []marketdata.DataError, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}
