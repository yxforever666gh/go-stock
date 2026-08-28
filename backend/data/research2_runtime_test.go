package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type research2RoundTripFunc func(*http.Request) (*http.Response, error)

func (function research2RoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestResearch2MarketRowsAcceptArrayAndNumberKeyObject(t *testing.T) {
	for _, testCase := range []struct {
		name, payload string
	}{
		{name: "array", payload: `[{"f2":5.57,"f3":10.08,"f12":"000059","f14":"华锦股份"},{"f2":"-","f3":"-","f12":"600000","f14":"停牌样本"}]`},
		{name: "number-key-object", payload: `{"1":{"f2":"-","f3":"-","f12":"600000","f14":"停牌样本"},"0":{"f2":5.57,"f3":10.08,"f12":"000059","f14":"华锦股份"}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var rows research2MarketRows
			if err := json.Unmarshal([]byte(testCase.payload), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0].Code != "000059" || rows[0].Price != 5.57 || rows[0].ChangeRate != 10.08 {
				t.Fatalf("unexpected decoded rows: %+v", rows)
			}
			if rows[1].Code != "600000" || rows[1].Price != 0 || rows[1].ChangeRate != 0 {
				t.Fatalf("unavailable values must decode as zero: %+v", rows[1])
			}
		})
	}
}

func TestResearch2FullMarketRequestsArrayWithFloatingPointQuotes(t *testing.T) {
	client := newNoProxyRestyClient()
	client.SetTransport(research2RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		query := request.URL.Query()
		for key, expected := range map[string]string{"np": "1", "fltt": "2", "invt": "2"} {
			if query.Get(key) != expected {
				t.Fatalf("query %s=%q, want %q", key, query.Get(key), expected)
			}
		}
		body := `{"rc":0,"data":{"total":1,"diff":[{"f2":5.57,"f3":10.08,"f5":100,"f6":1000,"f8":2.5,"f12":"000059","f13":0,"f14":"华锦股份","f15":5.57,"f16":5.15,"f17":5.15,"f18":5.06,"f26":19970130,"f62":100,"f124":1787883270}]}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	collector := &Research2EvidenceCollector{stocks: &StockDataApi{client: client}}
	payload, err := collector.fetchFullMarketPage(context.Background(), 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	rows := []research2MarketRow(payload.Data.Diff)
	if len(rows) != 1 || rows[0].Price != 5.57 || rows[0].ChangeRate != 10.08 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestResearch2FullMarketFallsBackToCompletePagination(t *testing.T) {
	client := newNoProxyRestyClient()
	client.SetTransport(research2RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		query := request.URL.Query()
		if query.Get("np") == "2" {
			return nil, io.ErrUnexpectedEOF
		}
		page, err := strconv.Atoi(query.Get("pn"))
		if err != nil {
			return nil, err
		}
		start, end := (page-1)*100+1, page*100
		if end > 201 {
			end = 201
		}
		items := make([]string, 0, end-start+1)
		for index := start; index <= end; index++ {
			items = append(items, fmt.Sprintf(`{"f2":5.57,"f3":1.25,"f5":100,"f6":1000,"f8":2.5,"f12":"%06d","f13":0,"f14":"样本%d"}`, index, index))
		}
		body := fmt.Sprintf(`{"rc":0,"data":{"total":201,"diff":[%s]}}`, strings.Join(items, ","))
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	collector := &Research2EvidenceCollector{stocks: &StockDataApi{client: client}}
	rows, err := collector.fetchFullMarket(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 201 || rows[0].Code != "000001" || rows[200].Code != "000201" {
		t.Fatalf("paginated full-market response is incomplete or unordered: len=%d first=%+v last=%+v", len(rows), rows[0], rows[len(rows)-1])
	}
}

func TestResearch2FullMarketLiveContract(t *testing.T) {
	if os.Getenv("GO_STOCK_LIVE_EASTMONEY") != "1" {
		t.Skip("set GO_STOCK_LIVE_EASTMONEY=1 to probe the live Eastmoney contract")
	}
	collector := &Research2EvidenceCollector{stocks: &StockDataApi{client: newNoProxyRestyClient()}}
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

func TestLimitResearch2TextKeepsUTF8Valid(t *testing.T) {
	result := limitResearch2Text(strings.Repeat("中", 10), 5)
	if !utf8.ValidString(result) || !strings.HasSuffix(result, "…") {
		t.Fatalf("truncated evidence must remain valid UTF-8: %q", result)
	}
}

func TestListedForResearch2SessionsRequiresTenOpenDays(t *testing.T) {
	loc := shanghaiDataLocation()
	asOf := time.Date(2026, 8, 28, 10, 0, 0, 0, loc)
	weekdays := func(day time.Time) (bool, error) {
		return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday, nil
	}
	if !listedForResearch2Sessions(20260817, asOf, 10, weekdays) {
		t.Fatal("ten completed weekday sessions should be eligible")
	}
	if listedForResearch2Sessions(20260820, asOf, 10, weekdays) {
		t.Fatal("a stock with fewer than ten sessions must be excluded")
	}
}
