package data

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

type checkedMarketRoundTripFunc func(*http.Request) (*http.Response, error)

func (run checkedMarketRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func TestCheckedMarketSourcesDistinguishFailuresFromEmptyResults(t *testing.T) {
	for _, source := range checkedMarketSources() {
		t.Run(source.name, func(t *testing.T) {
			for _, fixture := range []struct {
				name, body string
				status     int
				requestErr error
				wantErr    bool
				wantCount  int
			}{
				{name: "request failure", requestErr: io.ErrUnexpectedEOF, wantErr: true},
				{name: "HTTP failure with valid JSON", status: http.StatusServiceUnavailable, body: source.empty, wantErr: true},
				{name: "invalid JSON", body: `{"broken":`, wantErr: true},
				{name: "empty body", wantErr: true},
				{name: "null body", body: `null`, wantErr: true},
				{name: "invalid structure", body: `{"error":"upstream failed"}`, wantErr: true},
				{name: "upstream failure with data object", body: `{"code":-1,"data":{}}`, wantErr: true},
				{name: "upstream failure with data array", body: `{"code":-1,"data":[]}`, wantErr: true},
				{name: "valid empty", body: source.empty},
				{name: "valid data", body: source.populated, wantCount: 1},
			} {
				t.Run(fixture.name, func(t *testing.T) {
					client := resty.New().SetTransport(checkedMarketRoundTripFunc(func(request *http.Request) (*http.Response, error) {
						if fixture.requestErr != nil {
							return nil, fixture.requestErr
						}
						status := fixture.status
						if status == 0 {
							status = http.StatusOK
						}
						return &http.Response{StatusCode: status, Header: make(http.Header),
							Body: io.NopCloser(strings.NewReader(fixture.body)), Request: request}, nil
					}))
					count, err := source.fetch(context.Background(), MarketNewsApi{client: client})
					if (err != nil) != fixture.wantErr {
						t.Fatalf("count=%d err=%v, want error=%t", count, err, fixture.wantErr)
					}
					if !fixture.wantErr && count != fixture.wantCount {
						t.Fatalf("count=%d, want %d", count, fixture.wantCount)
					}
					if fixture.requestErr != nil && !errors.Is(err, fixture.requestErr) {
						t.Fatalf("original transport error was lost: %v", err)
					}
				})
			}
		})
	}
}

func TestCheckedMarketSourcesKeepCallerContextAndClientTimeout(t *testing.T) {
	for _, source := range checkedMarketSources() {
		t.Run(source.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			deadline, _ := ctx.Deadline()
			client := resty.New().SetTimeout(37 * time.Second).SetTransport(checkedMarketRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if got, ok := request.Context().Deadline(); !ok || !got.Equal(deadline) {
					t.Errorf("request deadline=%v, want caller deadline %v", got, deadline)
				}
				<-request.Context().Done()
				return nil, request.Context().Err()
			}))
			_, err := source.fetch(ctx, MarketNewsApi{client: client})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("caller cancellation was not preserved: %v", err)
			}
			if client.GetClient().Timeout != 37*time.Second {
				t.Fatalf("request changed shared client timeout to %v", client.GetClient().Timeout)
			}
		})
	}
}

func TestCheckedMarketSourcesUseRequestDeadline(t *testing.T) {
	client := resty.New().SetTransport(checkedMarketRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > 7*time.Second {
			t.Errorf("missing per-request timeout: deadline=%v", deadline)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"data":{}}`)), Request: request}, nil
	}))
	if _, err := (MarketNewsApi{client: client}).globalStockIndexes(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if client.GetClient().Timeout != 0 {
		t.Fatal("per-request timeout mutated the client")
	}
}

func TestLegacyMarketSourceGettersExposeErrorsWithoutPanicking(t *testing.T) {
	client := resty.New().SetTransport(checkedMarketRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}))
	api := MarketNewsApi{client: client}
	for _, result := range []map[string]any{api.GlobalStockIndexes(5), api.GetIndustryRank("changepercent", 30)} {
		if result["status"] != "failed" || result["error"] == nil || result["error"] == "" {
			t.Fatalf("legacy map concealed the failure: %+v", result)
		}
	}
	for _, rows := range [][]map[string]any{api.GetIndustryMoneyRankSina("gn", "netamount"), api.GetMoneyRankSina(""), api.GetStockMoneyTrendByDay("sh600000", 10)} {
		if rows == nil || len(rows) != 0 {
			t.Fatalf("legacy list failure must return an empty list: %+v", rows)
		}
	}
	if rows := api.StockNotice("600000"); rows == nil || len(rows) != 0 {
		t.Fatalf("legacy notice failure must return an empty list: %+v", rows)
	}
	if answers := api.InteractiveAnswer(1, 20, "公司"); answers == nil || len(answers.Results) != 0 {
		t.Fatalf("legacy interaction failure must return an empty object: %+v", answers)
	}
}

func TestCheckedMarketSourcesKeepNoticeCodesAndInteractionForm(t *testing.T) {
	client := resty.New().SetTransport(checkedMarketRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"data":{"list":[]}}`
		if request.URL.Host == "np-anotice-stock.eastmoney.com" {
			if request.Method != http.MethodGet || request.URL.Query().Get("stock_list") != "000001,600000" {
				t.Errorf("unexpected notice request: %s %s", request.Method, request.URL)
			}
		} else {
			if request.Method != http.MethodPost || request.URL.Host != "irm.cninfo.com.cn" {
				t.Errorf("unexpected interaction request: %s %s", request.Method, request.URL)
			}
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{"pageNo": "2", "pageSize": "30", "searchTypes": "11", "highLight": "true", "keyWord": "中国平安 & 银行"} {
				if request.Form.Get(key) != value {
					t.Errorf("form %s=%q, want %q", key, request.Form.Get(key), value)
				}
			}
			body = `{"results":[]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	api := MarketNewsApi{client: client}
	if _, err := api.stockNotice(context.Background(), "sz000001,600000.SH"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.interactiveAnswer(context.Background(), 2, 30, "中国平安 & 银行"); err != nil {
		t.Fatal(err)
	}
}

func checkedMarketSources() []struct {
	name, empty, populated string
	fetch                  func(context.Context, MarketNewsApi) (int, error)
} {
	return []struct {
		name, empty, populated string
		fetch                  func(context.Context, MarketNewsApi) (int, error)
	}{
		{"indexes", `{"data":{}}`, `{"data":{"common":[{"name":"index"}]}}`, func(ctx context.Context, api MarketNewsApi) (int, error) {
			data, err := api.globalStockIndexes(ctx, 5)
			if err != nil {
				return 0, err
			}
			return len(data["common"].([]any)), nil
		}},
		{"industry", `{"data":[]}`, `{"data":[{"name":"industry"}]}`, func(ctx context.Context, api MarketNewsApi) (int, error) {
			data, err := api.industryRank(ctx, "changepercent", 30)
			if err != nil {
				return 0, err
			}
			return len(data["data"].([]any)), nil
		}},
		{"industry money", `[]`, `[{"netamount":"1"}]`, func(ctx context.Context, api MarketNewsApi) (int, error) {
			rows, err := api.industryMoneyRankSina(ctx, "gn", "netamount")
			return len(rows), err
		}},
		{"money", `[]`, `[{"netamount":"1"}]`, func(ctx context.Context, api MarketNewsApi) (int, error) {
			rows, err := api.moneyRankSina(ctx, "")
			return len(rows), err
		}},
		{"stock money", `[]`, `[{"netamount":"1"}]`, func(ctx context.Context, api MarketNewsApi) (int, error) {
			rows, err := api.stockMoneyTrendByDay(ctx, "sh600000", 10)
			return len(rows), err
		}},
		{"notice", `{"data":{"list":[]}}`, `{"data":{"list":[{"title":"notice"}]}}`, func(ctx context.Context, api MarketNewsApi) (int, error) {
			rows, err := api.stockNotice(ctx, "600000")
			return len(rows), err
		}},
		{"interaction", `{"results":[]}`, `{"results":[{"mainContent":"question"}]}`, func(ctx context.Context, api MarketNewsApi) (int, error) {
			answers, err := api.interactiveAnswer(ctx, 1, 20, "公司")
			if err != nil {
				return 0, err
			}
			return len(answers.Results), nil
		}},
	}
}
