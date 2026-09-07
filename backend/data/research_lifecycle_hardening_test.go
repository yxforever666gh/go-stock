package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/research"
)

func TestLifecycleNewsIncludesLateArrivalsAndReportsTruncation(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "news.db"))
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.Telegraph{}, &models.TelegraphTags{}, &models.Tags{}, &research.LifecycleObservation{}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 7, 10, 15, 0, 0, cnLocation())
	to := from.Add(15 * time.Minute)
	lateEvent, freshEvent := from.Add(-10*time.Minute), from.Add(10*time.Minute)
	for index, eventAt := range []time.Time{lateEvent, freshEvent} {
		item := models.Telegraph{Title: fmt.Sprintf("浦发银行消息%d", index), DataTime: &eventAt}
		item.CreatedAt = from.Add(time.Minute)
		if err := db.Dao.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	collector := &ResearchLifecycleContextCollector{news: NewMarketNewsApi()}
	request := research.LifecycleContextRequest{Now: to, WindowFrom: from, Recommendation: research.Recommendation{StockCode: "sh600000", StockName: "浦发银行"}}
	value, err := collector.incrementalNews(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if !strings.Contains(string(encoded), "浦发银行消息0") || !strings.Contains(string(encoded), "浦发银行消息1") {
		t.Fatalf("late news missing: %s", encoded)
	}
	for index := 0; index < 31; index++ {
		item := models.Telegraph{Title: fmt.Sprintf("浦发银行新增%d", index), DataTime: &freshEvent}
		item.CreatedAt = freshEvent
		if err := db.Dao.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	value, err = collector.incrementalNews(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	source := newLifecycleSource("news", "新闻", "news", to, value, nil)
	if source.Status != "partial" || !strings.Contains(source.Content, `"coverageComplete":false`) {
		t.Fatalf("truncation hidden: %+v", source)
	}
	seen := map[uint]bool{}
	repository := research.NewRepository(db.Dao)
	request.Recommendation.RecommendationID = "news-pages"
	for page := 0; page < 40; page++ {
		encoded, _ := json.Marshal(value)
		var payload struct {
			Complete bool `json:"coverageComplete"`
			Items    []struct {
				ID uint `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatal(err)
		}
		if len(encoded) > 2400 {
			t.Fatalf("news page exceeds prompt budget: %d", len(encoded))
		}
		for _, item := range payload.Items {
			if seen[item.ID] {
				t.Fatalf("news page repeated id %d", item.ID)
			}
			seen[item.ID] = true
		}
		source := newLifecycleSource("NEWS", "新闻", "news", to, value, nil)
		pageRequest := request
		pageRequest.ObservationID = fmt.Sprintf("page-%d", page)
		pageRequest.Now = to.Add(time.Duration(page) * time.Second)
		observation, err := research.NewLifecycleObservation(pageRequest, research.LifecycleObservationDraft{Status: source.Status, Sources: []research.LifecycleEvidenceSource{source}})
		if err != nil {
			t.Fatal(err)
		}
		if err = repository.AppendObservation(context.Background(), &observation); err != nil {
			t.Fatal(err)
		}
		covered, known, err := repository.LifecycleNewsCoverage(context.Background(), request.Recommendation.RecommendationID)
		if err != nil {
			t.Fatal(err)
		}
		request.KnownNewsIDs = known
		if !payload.Complete && !covered.IsZero() {
			t.Fatalf("partial page advanced full coverage: %v", covered)
		}
		if payload.Complete && !covered.Equal(to) {
			t.Fatalf("completed pages failed to advance: %v", covered)
		}
		if payload.Complete {
			break
		}
		value, err = collector.incrementalNews(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 33 {
		t.Fatalf("news paging lost events: %d", len(seen))
	}
}

func TestLifecycleMinuteRequestPreservesErrorsAndCancellation(t *testing.T) {
	for _, failure := range []string{"http", "json", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			client := resty.New()
			client.SetTransport(stockDataRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if failure == "cancel" {
					return nil, context.Canceled
				}
				status, body := 200, "not JSON"
				if failure == "http" {
					status, body = 503, `{}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}))
			api := &StockDataApi{client: client}
			collector := &ResearchLifecycleContextCollector{stocks: api}
			_, _, err := collector.collectMinute(context.Background(), "sh600000")
			if err == nil || strings.Contains(err.Error(), "返回空数据") {
				t.Fatalf("error hidden: %v", err)
			}
			if failure == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestLifecycleSharedSourcesPreserveFailureAndCompletionTime(t *testing.T) {
	client := resty.New()
	client.SetTransport(stockDataRoundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF }))
	start := time.Date(2026, 9, 7, 9, 58, 54, 0, cnLocation())
	completed := start.Add(12 * time.Second)
	collector := &ResearchLifecycleContextCollector{news: &MarketNewsApi{client: client}, clock: func() time.Time { return completed }}
	sources := collector.sharedMarketSources(context.Background(), research.LifecycleContextRequest{Now: start, ObservationID: "observation"})
	for _, source := range sources {
		if source.Status != "failed" || source.Error == "" || !source.CollectedAt.Equal(completed) {
			t.Fatalf("failure or collection time hidden: %+v", source)
		}
	}
}
