package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go-stock/backend/research2"
	"go-stock/internal/marketquote"
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

func fixedResearch2MinuteSource(name string, bars []minuteBar, err error, calls *[]string) research2MinuteSource {
	return research2MinuteSource{name: name, fetch: func(context.Context, string, time.Time, time.Time) ([]minuteBar, string, error) {
		if calls != nil {
			*calls = append(*calls, name)
		}
		return bars, name, err
	}}
}

func TestResearch2TargetMinuteFallbackRequiresUsableExactBar(t *testing.T) {
	target := time.Date(2026, 9, 7, 10, 0, 0, 0, shanghaiDataLocation())
	valid := minuteBar{TradeTime: target, Open: 10, High: 11, Low: 9, Close: 10.5}
	neighbor := valid
	neighbor.TradeTime = target.Add(-time.Minute)
	invalid := valid
	invalid.Close = 0
	for _, tc := range []struct {
		name   string
		first  []minuteBar
		second []minuteBar
		want   string
	}{
		{"neighbor then fallback", []minuteBar{neighbor}, []minuteBar{valid}, "fallback"},
		{"invalid then cache", []minuteBar{invalid}, []minuteBar{neighbor}, "cache"},
		{"primary exact", []minuteBar{valid}, nil, "primary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			provider := research2MarketProvider{minutes: []research2MinuteSource{
				fixedResearch2MinuteSource("primary", tc.first, nil, &calls), fixedResearch2MinuteSource("fallback", tc.second, nil, &calls), fixedResearch2MinuteSource("cache", []minuteBar{valid}, nil, &calls),
			}}
			quote, err := provider.PriceAt(context.Background(), "sh600001", target, false)
			if err != nil || quote.Source != tc.want || !quote.At.Equal(target) || quote.Price != 10.5 {
				t.Fatalf("quote=%+v calls=%v err=%v", quote, calls, err)
			}
			if calls[len(calls)-1] != tc.want {
				t.Fatalf("fallback did not stop: %v", calls)
			}
		})
	}
}

func TestResearch2TargetMinuteFailureRetainsEverySourceReason(t *testing.T) {
	target := time.Date(2026, 9, 7, 10, 0, 0, 0, shanghaiDataLocation())
	provider := research2MarketProvider{minutes: []research2MinuteSource{
		fixedResearch2MinuteSource("primary", nil, errors.New("connection failed"), nil), fixedResearch2MinuteSource("fallback", nil, nil, nil), fixedResearch2MinuteSource("cache", nil, errors.New("not cached"), nil),
	}}
	_, err := provider.PriceAt(context.Background(), "sh600001", target, false)
	if err == nil {
		t.Fatal("missing target was accepted")
	}
	for _, part := range []string{"primary: connection failed", "fallback: valid target minute", "cache: not cached"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("missing %q: %v", part, err)
		}
	}
}

type research2MetricQuote struct {
	value marketquote.Quote
	err   error
}

func (q research2MetricQuote) CurrentQuote(context.Context, string) (marketquote.Quote, error) {
	return q.value, q.err
}

func research2MetricFixture() (research2.Recommendation, []minuteBar, marketquote.Quote) {
	buy := time.Date(2026, 9, 4, 14, 59, 30, 0, shanghaiDataLocation())
	sell := time.Date(2026, 9, 7, 10, 0, 0, 0, shanghaiDataLocation())
	bar := func(at time.Time) minuteBar {
		return minuteBar{TradeTime: at, Open: 10, High: 10, Low: 10, Close: 10, Volume: 100}
	}
	rows := []minuteBar{bar(buy.Truncate(time.Minute).Add(time.Minute))}
	rows[0].High = 10.6
	for _, window := range [][2]int{{9*60 + 31, 11*60 + 30}, {13*60 + 1, 15 * 60}} {
		for minute := window[0]; minute <= window[1]; minute++ {
			rows = append(rows, bar(time.Date(2026, 9, 7, minute/60, minute%60, 0, 0, shanghaiDataLocation())))
		}
	}
	for i := range rows {
		if rows[i].TradeTime.Equal(sell) {
			rows[i].Low = 9.6
		}
	}
	rows[len(rows)-1].High = 11
	return research2.Recommendation{StockCode: "sh600001", BuyAt: &buy, TargetSellAt: &sell, BuyPrice: 10}, rows, marketquote.Quote{Code: "sh600001", Price: 10, PreviousClose: 10, At: sell.Add(5 * time.Hour)}
}

func TestResearch2MetricsRequireCompleteWindowAndTargetSessionPreviousClose(t *testing.T) {
	item, complete, quote := research2MetricFixture()
	for _, tc := range []struct {
		name     string
		rows     []minuteBar
		quote    marketquote.Quote
		quoteErr error
		wantErr  bool
	}{
		{"complete weekend window", complete, quote, nil, false},
		{"missing buy-session close", complete[1:], quote, nil, true},
		{"missing sell-session close", complete[:len(complete)-1], quote, nil, true},
		{"missing middle minute", append(append([]minuteBar{}, complete[:25]...), complete[26:]...), quote, nil, true},
		{"quote failed", complete, quote, errors.New("quote unavailable"), true},
		{"wrong session previous close", complete, marketquote.Quote{Price: 10, PreviousClose: 9, At: quote.At.AddDate(0, 0, 1)}, nil, true},
		{"unknown previous close", complete, marketquote.Quote{Price: 10, At: quote.At}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := research2MarketProvider{quotes: research2MetricQuote{value: tc.quote, err: tc.quoteErr}, minutes: []research2MinuteSource{fixedResearch2MinuteSource("fixture", tc.rows, nil, nil)}}
			metrics, err := provider.Metrics(context.Background(), item)
			if (err != nil) != tc.wantErr {
				t.Fatalf("metrics=%+v err=%v wantErr=%v", metrics, err, tc.wantErr)
			}
			if !tc.wantErr && (!metrics.HitFiveBeforeSell || !metrics.HitMinusThree || !metrics.HitLimitUpFullDay) {
				t.Fatalf("complete evidence lost hits: %+v", metrics)
			}
		})
	}
}

func TestResearch2MetricsFallBackOnPartialMinuteResponse(t *testing.T) {
	item, complete, quote := research2MetricFixture()
	var calls []string
	provider := research2MarketProvider{quotes: research2MetricQuote{value: quote}, minutes: []research2MinuteSource{fixedResearch2MinuteSource("primary", complete[:1], nil, &calls), fixedResearch2MinuteSource("fallback", complete, nil, &calls), fixedResearch2MinuteSource("unused", nil, nil, &calls)}}
	if _, err := provider.Metrics(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "primary,fallback" {
		t.Fatalf("calls=%v", calls)
	}
}
