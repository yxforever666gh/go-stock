package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/marketdata"
	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMarketHotWordsRouteDefaultsAndEnvelope(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:hot-words-route?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := database.AutoMigrate(&models.Telegraph{}, &models.TelegraphTags{}, &models.Tags{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	rows := []models.Telegraph{
		{DataTime: timePointer(now.Add(-time.Hour)), Content: "人工智能推动算力订单增长并形成利好", Source: "source-a"},
		{DataTime: timePointer(now.Add(-2 * time.Hour)), Content: "人工智能芯片需求上涨，产业持续扩张", Source: "source-b"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatalf("seed news: %v", err)
	}

	previous := marketHotWordsServiceFactory
	marketHotWordsServiceFactory = func() *data.MarketHotWordsService {
		return data.NewMarketHotWordsServiceWithDB(database, func() time.Time { return now })
	}
	t.Cleanup(func() { marketHotWordsServiceFactory = previous })

	mux := http.NewServeMux()
	registerMarketEvidenceRoutes(mux, nil)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:34115/api/v1/market/hot/words", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope marketdata.DataEnvelope[data.HotWordsData]
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Window.Hours != 24 || envelope.Data.Baseline.RequestedDays != 7 {
		t.Fatalf("default query was not applied: %#v %#v", envelope.Data.Window, envelope.Data.Baseline)
	}
	if envelope.Data.CurrentDocumentCount != 2 || len(envelope.Data.Items) == 0 || envelope.Data.Items[0].Word != "人工智能" {
		t.Fatalf("unexpected hot words data: %#v", envelope.Data)
	}
	if envelope.Status != marketdata.StatusPartial || envelope.Data.Baseline.Available {
		t.Fatalf("sparse baseline status=%q baseline=%#v", envelope.Status, envelope.Data.Baseline)
	}
}

func TestMarketHotWordsRouteValidatesQueryBounds(t *testing.T) {
	previous := marketHotWordsServiceFactory
	marketHotWordsServiceFactory = func() *data.MarketHotWordsService { return data.NewMarketHotWordsServiceWithDB(nil, time.Now) }
	t.Cleanup(func() { marketHotWordsServiceFactory = previous })

	mux := http.NewServeMux()
	registerMarketEvidenceRoutes(mux, nil)
	tests := []struct {
		query string
		code  string
	}{
		{query: "hours=0", code: "invalid_hours"},
		{query: "hours=73", code: "invalid_hours"},
		{query: "baselineDays=2", code: "invalid_baseline_days"},
		{query: "baselineDays=31", code: "invalid_baseline_days"},
		{query: "limit=0", code: "invalid_limit"},
		{query: "limit=101", code: "invalid_limit"},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:34115/api/v1/market/hot/words?"+test.query, nil)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func timePointer(value time.Time) *time.Time { return &value }
