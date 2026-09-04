package data

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"

	"github.com/go-resty/resty/v2"
)

func TestMarketEvidenceParsers(t *testing.T) {
	breadth, total, _, err := parseEastmoneyBreadth([]byte(`{"data":{"total":3,"diff":[{"f3":1.2,"f12":"600000","f14":"A"},{"f3":-2.3,"f12":"000001","f14":"B"},{"f3":0,"f12":"510300","f14":"ETF"}]}}`))
	if err != nil || total != 3 || breadth.Advances != 1 || breadth.Declines != 1 || breadth.Flat != 1 {
		t.Fatalf("breadth=%#v total=%d err=%v", breadth, total, err)
	}
	if breadth.NewHighs != nil || breadth.NewLows != nil || breadth.MedianChangePct != 0 {
		t.Fatalf("unexpected breadth derived fields: %#v", breadth)
	}
	flows, err := parseEastmoneyFundFlows([]byte(`{"data":{"diff":[{"f12":"BK001","f14":"半导体","f62":12345,"f3":1.5}]}}`))
	if err != nil || len(flows) != 1 || flows[0].NetAmount != 12345 {
		t.Fatalf("flows=%#v err=%v", flows, err)
	}
	futures, err := parseEastmoneyFutures([]byte(`{"success":true,"result":{"data":[{"TRADE_DATE":"2026-08-27 00:00:00","SETTLE_PRICE":4000.5,"TOTAL_LONG_POSITION":100,"LP_CHANGE_TOTAL":2,"TOTAL_SHORT_POSITION":120,"SP_CHANGE_TOTAL":-3,"NET_POSITION":-20,"CLOSE_PRICE":3990,"CLOSE_PRICE_CHANGE":10,"BASIS":10.5}]}}`), "2026-08-27")
	if err != nil || len(futures) != 1 || futures[0].NetPosition != -20 || futures[0].Basis != 10.5 {
		t.Fatalf("futures=%#v err=%v", futures, err)
	}
	trades, err := parseEastmoneyTrades([]byte(`{"data":{"details":["09:25:00,10.00,2,1,1","09:30:01,10.10,3,1,2"]}}`))
	if err != nil || len(trades) != 2 || trades[0].Amount != 2000 || trades[1].Side != "sell" {
		t.Fatalf("trades=%#v err=%v", trades, err)
	}
}

func TestEastmoneyBreadthFixtureAndProviderStatus(t *testing.T) {
	fixture, err := os.ReadFile("testdata/eastmoney_breadth.json")
	if err != nil {
		t.Fatal(err)
	}
	breadth, total, _, err := parseEastmoneyBreadth(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || breadth.Total != 4 || breadth.NewHighs == nil || *breadth.NewHighs != 1 || breadth.NewLows == nil || *breadth.NewLows != 1 {
		t.Fatalf("breadth=%#v total=%d", breadth, total)
	}

	var requestedFields string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestedFields = request.URL.Query().Get("fields")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()
	service := &MarketEvidenceService{
		client: resty.New(),
		now:    func() time.Time { return time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiDataLocation()) },
		urls:   marketEvidenceURLs{breadth: server.URL},
	}
	result := (&eastmoneyBreadthProvider{service: service}).Collect(context.Background(), marketdata.ProviderRequest{})
	if result.Status != marketdata.StatusOK || result.Warning != "" {
		t.Fatalf("status=%q warning=%q", result.Status, result.Warning)
	}
	for _, field := range []string{"f2", "f15", "f16", "f124"} {
		if !containsCommaSeparatedField(requestedFields, field) {
			t.Fatalf("requested fields %q missing %s", requestedFields, field)
		}
	}
}

func TestEastmoneyBreadthPartialWhenHighLowSamplesUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"total":1,"diff":[{"f2":"-","f3":1.2,"f12":"600000","f14":"A","f15":"-","f16":null}]}}`))
	}))
	defer server.Close()
	service := &MarketEvidenceService{
		client: resty.New(),
		now:    func() time.Time { return time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiDataLocation()) },
		urls:   marketEvidenceURLs{breadth: server.URL},
	}
	result := (&eastmoneyBreadthProvider{service: service}).Collect(context.Background(), marketdata.ProviderRequest{})
	if result.Status != marketdata.StatusPartial || result.Data.NewHighs != nil || result.Data.NewLows != nil || !strings.Contains(result.Warning, "没有可判定样本") {
		t.Fatalf("result=%#v", result)
	}
}

func TestEastmoneyBreadthComparableSamplesReturnZeroCounts(t *testing.T) {
	breadth, _, _, err := parseEastmoneyBreadth([]byte(`{"data":{"total":1,"diff":[{"f2":9,"f3":0.1,"f12":"600000","f14":"A","f15":10,"f16":8}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if breadth.NewHighs == nil || *breadth.NewHighs != 0 || breadth.NewLows == nil || *breadth.NewLows != 0 {
		t.Fatalf("zero counts must remain available: %#v", breadth)
	}
}

func containsCommaSeparatedField(fields, want string) bool {
	for _, field := range strings.Split(fields, ",") {
		if field == want {
			return true
		}
	}
	return false
}

func TestMarginFixturesAndUnitConversion(t *testing.T) {
	sse, err := parseSSEMargin([]byte(`{"pageHelp":{"data":[{"opDate":"2026-08-27","rzye":"100","rqylje":"20","rzmre":"3","rqmcl":"4","rzrqjyzl":"120"}]}}`), "2026-08-27")
	if err != nil || sse.MarginBalance != 120 || sse.Securities != 20 {
		t.Fatalf("sse=%#v err=%v", sse, err)
	}
	szse, err := parseSZSEMargin([]byte(`[{"metadata":{"subname":"2026-08-27"},"data":[{"jrrzye":"1,234.5","jrrjye":"2","jrrzrjye":"1,236.5","jrrzmr":"3","jrrjmc":"4"}]}]`), "2026-08-27")
	if err != nil || szse.Financing != 1234.5e8 || szse.SecuritiesSell != 4e8 {
		t.Fatalf("szse=%#v err=%v", szse, err)
	}
	eastmoney, err := parseEastmoneyMargin([]byte(`{"success":true,"result":{"data":[{"TRADE_DATE":"2026-08-27 00:00:00","SECURITY_CODE":"600519","SECURITY_NAME_ABBR":"贵州茅台","FIN_BALANCE":10,"LOAN_BALANCE":2,"MARGIN_BALANCE":12,"FIN_BUY_AMT":3,"LOAN_SELL_VOL":4}]}}`), "2026-08-27")
	if err != nil || len(eastmoney) != 1 || eastmoney[0].MarginBalance != 12 {
		t.Fatalf("eastmoney=%#v err=%v", eastmoney, err)
	}
}

func TestCFFEXFixture(t *testing.T) {
	body := "交易日,合约,排名,成交量排名,,,持买单量排名,,,持卖单量排名,,\n日期,合约,名次,会员,成交量,增减,会员,持买单量,增减,会员,持卖单量,增减\n20260827,IF2609,1,A,100,1,B,50,2,C,60,3\n20260827,IF2610,1,A,10,1,B,5,2,C,6,3\n"
	row, contract, ok := parseCffexPositionDay(body, time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC))
	if !ok || contract != "IF2609" || row.LongPosition != 50 || row.ShortPosition != 60 || row.NetPosition != -10 {
		t.Fatalf("row=%#v contract=%s ok=%v", row, contract, ok)
	}
}

func TestTradePaginationProducesCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"details":["09:30:01,10,1,1,1","09:30:02,11,1,1,1","09:30:03,12,1,1,1"]}}`))
	}))
	defer server.Close()
	service := &MarketEvidenceService{client: resty.New(), now: func() time.Time { return time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiDataLocation()) }, urls: marketEvidenceURLs{details: server.URL}}
	result := (&eastmoneyTradesProvider{service: service}).Collect(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock", Limit: 2})
	if result.Err != nil || len(result.Data.Items) != 2 || result.Data.NextCursor != "2" {
		t.Fatalf("result=%#v", result)
	}
}

func TestAuctionDerivedSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"prePrice":10,"details":["09:20:00,10.10,2,1,1","09:25:00,10.20,3,1,1"]}}`))
	}))
	defer server.Close()
	service := &MarketEvidenceService{client: resty.New(), now: func() time.Time { return time.Date(2026, 8, 28, 9, 26, 0, 0, shanghaiDataLocation()) }, urls: marketEvidenceURLs{details: server.URL}}
	result := (&eastmoneyAuctionProvider{service: service}).Collect(context.Background(), marketdata.ProviderRequest{Code: "sh600000", AssetType: "stock"})
	if result.Err != nil || result.Data.FinalSnapshot == nil || result.Data.FinalSnapshot.Price != 10.2 || result.Data.GapPct == nil || *result.Data.GapPct < 1.99 || result.Data.AuctionStrength == nil {
		t.Fatalf("auction=%#v err=%v", result.Data, result.Err)
	}
}
