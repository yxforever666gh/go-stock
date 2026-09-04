package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go-stock/backend/research2"
)

// research2RoundTripFunc provides deterministic HTTP fixtures for provider tests.
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
	collector := &research2EvidenceCollector{stocks: &StockDataApi{client: client}}
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
	collector := &research2EvidenceCollector{stocks: &StockDataApi{client: client}}
	rows, err := collector.fetchFullMarket(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 201 || rows[0].Code != "000001" || rows[200].Code != "000201" {
		t.Fatalf("paginated full-market response is incomplete or unordered: len=%d first=%+v last=%+v", len(rows), rows[0], rows[len(rows)-1])
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

func TestSelectResearch2CandidatesExcludesStocksInsideLimitBufferAndHonorsBoundary(t *testing.T) {
	asOf := time.Date(2026, 9, 4, 10, 0, 0, 0, shanghaiDataLocation())
	previousClose := 10.03
	limitPrice := research2.MainBoardLimitPrice(previousClose)
	if limitPrice != 11.03 {
		t.Fatalf("limit price=%v want 11.03", limitPrice)
	}
	row := func(code string, price float64) research2MarketRow {
		return research2MarketRow{Code: code, Name: "候选" + code, Price: price, PreClose: previousClose,
			ChangeRate: (price/previousClose - 1) * 100, ChangeValid: true, Volume: 100000, Amount: 10000000,
			Turnover: 3, ListingDate: 20200101, Timestamp: asOf.Unix()}
	}
	boundaryPrice := limitPrice * (1 - research2.SelectionLimitDistancePct/100)
	rows := []research2MarketRow{
		row("600001", limitPrice),
		row("600002", limitPrice*0.99),
		row("600003", boundaryPrice),
		row("600004", limitPrice*0.98),
		{Code: "600005", Name: "缺前收", Price: 10, PreClose: 0, ChangeRate: 1, ChangeValid: true, Volume: 100000, Amount: 10000000, Turnover: 3, ListingDate: 20200101, Timestamp: asOf.Unix()},
	}
	selected := selectResearch2Candidates(rows, 10, asOf)
	if len(selected) != 2 || selected[0].Code != "sh600003" || selected[1].Code != "sh600004" {
		t.Fatalf("near-limit filter or 1.5%% boundary is wrong: %+v", selected)
	}
	selected = selectResearch2CandidatesWithExclusions(rows, 10, asOf, map[string]struct{}{"SH600003": {}})
	if len(selected) != 1 || selected[0].Code != "sh600004" {
		t.Fatalf("candidate exclusions were not normalized/applied: %+v", selected)
	}
}

func TestLoadResearch2CachedMinuteBarsUsesOnlyUnadjustedCache(t *testing.T) {
	initMinuteCacheTestDB(t, "research2-sell-replay.db")
	target := time.Date(2026, 9, 4, 10, 0, 0, 0, shanghaiDataLocation())
	rows := []minuteBar{
		{TradeTime: target, Open: 10, High: 10.2, Low: 9.9, Close: 10.1, Volume: 100, Source: "tencent"},
		{TradeTime: target.Add(time.Minute), Open: 88, High: 89, Low: 87, Close: 88, Volume: 100, Source: "akshare:adjustment=qfq"},
	}
	if _, err := upsertMinuteBarsToCache("600001.SH", rows, ""); err != nil {
		t.Fatal(err)
	}
	bars, err := loadResearch2CachedMinuteBars(context.Background(), NewResearchChartProvider(nil), "sh600001", target.Add(-time.Minute), target.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 || !bars[0].TradeTime.Equal(target) || bars[0].Close != 10.1 || bars[0].Source != "tencent" {
		t.Fatalf("sell replay cache was not adjustment-safe: %+v", bars)
	}
}
