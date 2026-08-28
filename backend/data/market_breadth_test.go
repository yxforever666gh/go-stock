package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestBreadthFallsBackFromEOFToCompleteDelayedPages(t *testing.T) {
	const total = 5554
	quoteAt := time.Date(2026, 8, 28, 14, 52, 33, 0, shanghaiDataLocation())
	direct := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer direct.Close()

	var requests atomic.Int32
	delayed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		page, _ := strconv.Atoi(request.URL.Query().Get("pn"))
		pageSize, _ := strconv.Atoi(request.URL.Query().Get("pz"))
		start := (page - 1) * pageSize
		end := start + pageSize
		if end > total {
			end = total
		}
		rows := make([]map[string]any, 0, end-start)
		for index := start; index < end; index++ {
			rows = append(rows, eastmoneyBreadthFixtureRow(index, quoteAt.Unix()))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rc": 0, "data": map[string]any{"total": total, "diff": rows}})
	}))
	defer delayed.Close()

	now := quoteAt.Add(10 * time.Minute)
	service := NewMarketEvidenceServiceWithMinuteDB(nil)
	service.client = resty.New().SetTimeout(2 * time.Second)
	service.now = func() time.Time { return now }
	service.urls.breadth = direct.URL
	service.urls.breadthDelay = delayed.URL
	service.urls.breadthTencent = ""

	result := service.collectBreadth(context.Background())
	if result.Status != marketdata.StatusOK || result.Source != "eastmoney-delay" || result.Data.Total != total {
		t.Fatalf("delayed fallback result=%#v", result)
	}
	if !result.AsOf.Equal(quoteAt) {
		t.Fatalf("asOf=%v want=%v", result.AsOf, quoteAt)
	}
	if requests.Load() != 56 {
		t.Fatalf("delayed requests=%d want=56", requests.Load())
	}
	if len(result.Sources) != 2 || result.Sources[0].Provider != "eastmoney" || result.Sources[0].Status != marketdata.StatusUnavailable || result.Sources[1].Provider != "eastmoney-delay" || result.Sources[1].Status != marketdata.StatusOK {
		t.Fatalf("source chain=%#v", result.Sources)
	}
	if !result.Sources[0].AsOf.IsZero() {
		t.Fatalf("failed source used collection time as quote time: %#v", result.Sources[0])
	}
	if len(result.Errors) != 1 || result.Errors[0].Provider != "eastmoney" {
		t.Fatalf("errors=%#v", result.Errors)
	}
}

func TestBreadthFallsBackToTencentUsingListedStockUniverse(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&models.StockBasic{}); err != nil {
		t.Fatal(err)
	}
	stocks := make([]models.StockBasic, 0, 100)
	for index := range 100 {
		code := fmt.Sprintf("6%05d", index)
		stocks = append(stocks, models.StockBasic{TsCode: code + ".SH", Symbol: code, Exchange: "SSE", ListStatus: "L"})
	}
	if err = database.Create(&stocks).Error; err != nil {
		t.Fatal(err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer failing.Close()
	quoteAt := time.Date(2026, 8, 28, 14, 48, 28, 0, shanghaiDataLocation())
	var batches atomic.Int32
	tencent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		batches.Add(1)
		for _, symbol := range strings.Split(request.URL.Query().Get("q"), ",") {
			if symbol == "" {
				continue
			}
			_, _ = fmt.Fprintln(w, tencentBreadthFixtureLine(symbol, quoteAt))
		}
	}))
	defer tencent.Close()

	service := NewMarketEvidenceServiceWithMinuteDB(nil)
	service.client = resty.New().SetTimeout(time.Second)
	service.mainDB = database
	service.now = func() time.Time { return quoteAt.Add(time.Minute) }
	service.urls.breadth = failing.URL
	service.urls.breadthDelay = failing.URL
	service.urls.breadthTencent = tencent.URL

	result := service.collectBreadth(context.Background())
	if result.Status != marketdata.StatusPartial || result.Source != "tencent" || result.Data.Total != 100 {
		t.Fatalf("Tencent fallback result=%#v", result)
	}
	if batches.Load() != 2 {
		t.Fatalf("Tencent batches=%d want=2", batches.Load())
	}
	if !result.AsOf.Equal(quoteAt) || len(result.Errors) != 2 || len(result.Sources) != 3 {
		t.Fatalf("Tencent provenance=%#v", result)
	}
}

func TestBreadthCoverageBoundaryRejectsSmallSamples(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, shanghaiDataLocation())
	rows := make([]breadthObservation, 100)
	for index := range rows {
		rows[index] = breadthObservation{key: fmt.Sprintf("1:%06d", index), code: fmt.Sprintf("6%05d", index), current: 10, currentOK: true, changePct: 1, changeOK: true, quoteAt: now}
	}
	rejected := buildBreadthProviderResult(rows[:94], 100, now, "fixture", false, nil)
	if rejected.Status != marketdata.StatusUnavailable || rejected.Err == nil || !strings.Contains(rejected.Err.Error(), "94.00%") {
		t.Fatalf("94%% coverage=%#v", rejected)
	}
	accepted := buildBreadthProviderResult(rows[:95], 100, now, "fixture", false, nil)
	if accepted.Status != marketdata.StatusPartial || accepted.Data.Total != 95 {
		t.Fatalf("95%% coverage=%#v", accepted)
	}
}

func TestDelayedBreadthRetriesTwiceAndDeduplicatesPages(t *testing.T) {
	quoteAt := time.Date(2026, 8, 28, 14, 52, 33, 0, shanghaiDataLocation())
	var firstPageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		page, _ := strconv.Atoi(request.URL.Query().Get("pn"))
		if page == 1 && firstPageRequests.Add(1) <= 2 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		rows := make([]map[string]any, 0, 100)
		for index := range 100 {
			// Both pages deliberately return the same identities. The coverage
			// gate must use the deduplicated set, not the raw response count.
			rows = append(rows, eastmoneyBreadthFixtureRow(index, quoteAt.Unix()))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rc": 0, "data": map[string]any{"total": 200, "diff": rows}})
	}))
	defer server.Close()

	service := NewMarketEvidenceServiceWithMinuteDB(nil)
	service.client = resty.New().SetTimeout(time.Second)
	service.now = func() time.Time { return quoteAt.Add(time.Minute) }
	service.urls.breadthDelay = server.URL
	result := (&eastmoneyDelayedBreadthProvider{service: service}).Collect(context.Background(), marketdata.ProviderRequest{})
	if firstPageRequests.Load() != 3 {
		t.Fatalf("first page attempts=%d want=3", firstPageRequests.Load())
	}
	if result.Status != marketdata.StatusUnavailable || result.Err == nil || !strings.Contains(result.Err.Error(), "50.00%") {
		t.Fatalf("duplicate pages bypassed coverage gate: %#v", result)
	}
}

func TestBreadthCalculationHandlesLimitsMedianAndRealZero(t *testing.T) {
	rows := []breadthObservation{
		{key: "1:600001", code: "600001", name: "普通", current: 10, currentOK: true, changePct: 10, changeOK: true},
		{key: "1:600002", code: "600002", name: "ST样本", current: 5, currentOK: true, changePct: -5, changeOK: true},
		{key: "0:300001", code: "300001", current: 20, currentOK: true, changePct: 20, changeOK: true},
		{key: "1:688001", code: "688001", current: 20, currentOK: true, changePct: -20, changeOK: true},
		{key: "0:000001", code: "000001", current: 10, currentOK: true, changePct: 0, changeOK: true},
	}
	result, _, samples := calculateBreadth(rows)
	if samples != 5 || result.Advances != 2 || result.Declines != 2 || result.Flat != 1 || result.LimitUps != 2 || result.LimitDowns != 2 || result.MedianChangePct != 0 {
		t.Fatalf("calculated breadth=%#v samples=%d", result, samples)
	}
}

func TestBreadthSuccessfulSnapshotExpiresAfterTwentyFourHours(t *testing.T) {
	base := time.Date(2026, 8, 28, 15, 0, 0, 0, shanghaiDataLocation())
	now := base
	service := NewMarketEvidenceServiceWithMinuteDB(nil)
	service.now = func() time.Time { return now }
	service.storeBreadthSnapshot(marketdata.DataEnvelope[BreadthData]{
		Data: BreadthData{Total: 10, Advances: 6, Declines: 4}, Source: "eastmoney-delay", AsOf: base.Add(-time.Minute), FetchedAt: base, Status: marketdata.StatusOK,
	})
	failed := marketdata.DataEnvelope[BreadthData]{Status: marketdata.StatusUnavailable, Errors: []marketdata.DataError{{Provider: "eastmoney", Message: "EOF"}}, Sources: []marketdata.SourceState{{Provider: "eastmoney", Status: marketdata.StatusUnavailable}}}

	now = base.Add(23 * time.Hour)
	stale := service.staleBreadthSnapshot(failed)
	if stale.Status != marketdata.StatusStale || stale.Data.Total != 10 || !stale.AsOf.Equal(base.Add(-time.Minute)) || stale.Source != "eastmoney-delay" {
		t.Fatalf("stale snapshot=%#v", stale)
	}
	now = base.Add(24*time.Hour + time.Second)
	expired := service.staleBreadthSnapshot(failed)
	if expired.Status != marketdata.StatusUnavailable || expired.Data.Total != 0 {
		t.Fatalf("expired snapshot=%#v", expired)
	}
}

func eastmoneyBreadthFixtureRow(index int, quoteUnix int64) map[string]any {
	change := float64(index%3 - 1)
	return map[string]any{
		"f2": 10, "f3": change, "f12": fmt.Sprintf("%06d", index), "f13": index % 2,
		"f14": fmt.Sprintf("样本%d", index), "f15": 11, "f16": 9, "f124": quoteUnix,
	}
}

func tencentBreadthFixtureLine(symbol string, quoteAt time.Time) string {
	parts := make([]string, 38)
	parts[0] = "51"
	parts[1] = "腾讯样本"
	parts[2] = strings.TrimPrefix(strings.TrimPrefix(symbol, "sh"), "sz")
	parts[3] = "10.10"
	parts[4] = "10.00"
	parts[5] = "10.00"
	parts[29] = quoteAt.Format("20060102150405")
	parts[31] = "0.10"
	parts[32] = "1.00"
	parts[33] = "10.20"
	parts[34] = "9.90"
	parts[35] = "10.10/100/101"
	return "v_" + symbol + "=\"" + strings.Join(parts, "~") + "\";"
}
