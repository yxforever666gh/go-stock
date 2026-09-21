package diemeng

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewClient(Config{BaseURL: srv.URL + "/api", APIKey: "test-private-key"})
	if err != nil {
		t.Fatal(err)
	}
	c.interval = 0
	c.downloadDir = t.TempDir()
	return c
}

func gzipData(t *testing.T, value string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAllEndpointContracts(t *testing.T) {
	endpoints := Catalog()
	if len(endpoints) != 44 {
		t.Fatal(len(endpoints))
	}
	seen := map[string]bool{}
	for _, e := range endpoints {
		t.Run(e.Name, func(t *testing.T) {
			if seen[e.Name] {
				t.Fatal("duplicate tool")
			}
			seen[e.Name] = true
			var calls atomic.Int32
			zip := gzipData(t, `{"600941.SH":[["13:45",96.43,96.43,96.41,96.41,225,2169410]]}`)
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != e.Path || r.Method != e.Method || r.Header.Get("apiKey") != "test-private-key" {
					t.Errorf("wrong route, method or authentication: %s %s", r.Method, r.URL.Path)
				}
				if r.URL.RawQuery != "" && strings.Contains(r.URL.RawQuery, "test-private-key") {
					t.Error("key in URL")
				}
				if e.Method == "GET" {
					for k, v := range e.Example {
						if r.URL.Query().Get(k) != fmt.Sprint(v) {
							t.Errorf("GET %s not forwarded", k)
						}
					}
				} else {
					var args map[string]any
					if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(args, e.Example) {
						t.Errorf("POST parameters changed: %#v != %#v", args, e.Example)
					}
				}
				if e.Download {
					w.Header().Set("Content-Type", "application/gzip")
					w.Write(zip)
					return
				}
				io.WriteString(w, `{"code":200,"msg":"ok","data":{"page":0,"total":3,"list":[{"value":null,"numeric_string":"1.2300","secret":"test-private-key"}]}}`)
			})
			result, err := c.Call(context.Background(), e.Name, e.Example)
			if err != nil {
				t.Fatal(err)
			}
			if result.Source != "diemeng" || result.Endpoint != e.Path || calls.Load() != 1 {
				t.Fatal("source/page routing mismatch")
			}
			encoded, _ := json.Marshal(result)
			if bytes.Contains(encoded, []byte("test-private-key")) {
				t.Fatal("credential leaked")
			}
			if e.Download {
				if result.Download == nil || result.Download.Bytes != int64(len(zip)) || len(result.Download.SHA256) != 64 {
					t.Fatal(result)
				}
				body, err := os.ReadFile(result.Download.Path)
				if err != nil || !bytes.Equal(body, zip) {
					t.Fatal("download mismatch", err)
				}
			} else {
				if !bytes.Contains(result.Response, []byte(`"numeric_string":"1.2300"`)) || !bytes.Contains(result.Response, []byte(`"value":null`)) {
					t.Fatal("raw data altered")
				}
			}
		})
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid parameters reached network") })
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"diemeng_stock_income", map[string]any{}},
		{"diemeng_stock_income", map[string]any{"stock_code": "600941.SH", "end_date": "2026-02-30"}},
		{"diemeng_stock_income", map[string]any{"stock_code": "600941.SH", "page": -1}},
		{"diemeng_stock_income", map[string]any{"stock_code": "600941.SH", "page_size": 10001}},
		{"diemeng_stock_forecast", map[string]any{"stock_code": "600941.SH", "finish_date": "2026-08-12"}},
		{"diemeng_stock_history", map[string]any{"stock_code": []string{"600941.SH"}, "level": "1min", "start_time": "2026-08-12 13:45:00", "end_time": "2026-08-12 13:45:00"}},
		{"diemeng_stock_macd", map[string]any{"stock_code": "600941.SH", "start_time": "2026-08-12", "end_time": "2026-08-12", "slow_period": 10}},
		{"diemeng_stock_macd", map[string]any{"stock_code": "600941.SH", "start_time": "2026-08-12 13:45:00", "end_time": "2026-08-12 13:45:00"}},
		{"diemeng_stock_macd", map[string]any{"stock_code": []string{"600941.SH", "600000.SH"}, "start_time": "2026-08-12", "end_time": "2026-08-12"}},
		{"diemeng_stock_ma", map[string]any{"stock_code": "600941.SH", "start_time": "2026-08-12", "end_time": "2026-08-12", "ma_periods": "5,0"}},
		{"diemeng_basic_calendar", map[string]any{"start_time": "2026-08-13", "end_time": "2026-08-12"}},
		{"diemeng_dc_daily", map[string]any{}},
		{"diemeng_stock_list", map[string]any{"url": "https://other.example"}},
		{"diemeng_stock_list", map[string]any{"apiKey": "override"}},
		{"arbitrary_url", map[string]any{}},
	} {
		if _, err := c.Call(context.Background(), tc.name, tc.args); err == nil {
			t.Fatalf("accepted: %s %+v", tc.name, tc.args)
		}
	}
}

func TestErrorsTimeoutAndNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, "test-private-key"}, {"forbidden", 403, "denied"}, {"quota", 429, "quota"},
		{"redirect", 302, ""}, {"business", 200, `{"code":403,"msg":"test-private-key denied","data":null}`},
		{"invalid_json", 200, `test-private-key`}, {"trailing", 200, `{"code":200,"data":[]}garbage`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "https://example.invalid")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			_, err := c.Call(context.Background(), "diemeng_stock_list", map[string]any{})
			if err == nil || strings.Contains(err.Error(), "test-private-key") || calls.Load() != 1 {
				t.Fatal("error/retry/redaction", err, calls.Load())
			}
		})
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	c.timeout = 30 * time.Millisecond
	if _, err := c.Call(context.Background(), "diemeng_stock_list", map[string]any{}); err == nil {
		t.Fatal("missing timeout")
	}
	if _, err := (*Client)(nil).Call(context.Background(), "diemeng_stock_list", nil); err == nil {
		t.Fatal("missing configuration error")
	}
}

func TestThrottleAndConfiguration(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"code":200,"data":[]}`) })
	c.interval = 35 * time.Millisecond
	for i := 0; i < 2; i++ {
		start := time.Now()
		if _, err := c.Call(context.Background(), "diemeng_stock_list", map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if i == 1 && time.Since(start) < 25*time.Millisecond {
			t.Fatal("minimum interval not enforced")
		}
	}
	c.next = time.Now().Add(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, "diemeng_stock_list", map[string]any{}); err == nil {
		t.Fatal("cancellation ignored")
	}
	path := filepath.Join(t.TempDir(), "private.json")
	if client, err := Load(path); client != nil || err != nil {
		t.Fatal("missing file should allow local mode")
	}
	if err := os.WriteFile(path, []byte(`{"base_url":"https://example.test/api","api_key":"a-private-key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.interval != 1200*time.Millisecond || loaded.http.Timeout != 60*time.Second || loaded.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("incorrect production network defaults")
	}
	if err := os.WriteFile(path, []byte(`{broken "a-private-key"`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || strings.Contains(err.Error(), "a-private-key") {
		t.Fatal("config error leaked")
	}
}

func TestDownloadFailures(t *testing.T) {
	for _, body := range [][]byte{[]byte(`{"code":429,"msg":"quota test-private-key","data":null}`), {0x1f, 0x8b, 0, 1, 2}, gzipData(t, `{"code":429,"msg":"test-private-key quota","data":null}`), gzipData(t, `[123]`)} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
		_, err := c.Call(context.Background(), "diemeng_stock_daily_dump", map[string]any{"date": "2026-08-12"})
		if err == nil || strings.Contains(err.Error(), "test-private-key") {
			t.Fatal("expected safe error")
		}
		entries, _ := os.ReadDir(c.downloadDir)
		if len(entries) != 0 {
			t.Fatal("incomplete download left behind")
		}
	}
}

func TestMinuteNormalization(t *testing.T) {
	fixture := `{"code":200,"data":{"total":2,"list":[{"trade_time":"2026-08-12 13:46:00","open":96.43,"high":96.43,"low":96.41,"close":96.41,"vol":1.25,"amount":12000},{"trade_time":"2026-08-12 13:45:00","open":96.43,"high":96.43,"low":96.41,"close":96.41,"vol":225,"amount":2169410}]}}`
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var args map[string]any
		json.NewDecoder(r.Body).Decode(&args)
		if args["stock_code"] != "600941.SH" || args["level"] != "1min" || args["page"] != float64(0) {
			t.Error(args)
		}
		io.WriteString(w, fixture)
	})
	start, _ := time.Parse(time.RFC3339, "2026-08-12T13:45:00+08:00")
	end := start.Add(time.Minute)
	rows, err := c.Minutes(context.Background(), "sh600941", "1m", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["time"] != start.Format(time.RFC3339) || rows[0]["volume"] != json.Number("22500") || rows[1]["volume"] != json.Number("125") {
		t.Fatal(rows)
	}
	for _, field := range []string{"change", "change_pct", "turnover_rate_pct", "float_shares", "total_shares"} {
		if v, ok := rows[0][field]; !ok || v != nil {
			t.Fatalf("%s must be null", field)
		}
	}
	fixture = `{"code":200,"data":{"total":10,"list":[]}}`
	if _, err := c.Minutes(context.Background(), "sh600941", "1m", start, end); err == nil {
		t.Fatal("pagination silently truncated")
	}
	fixture = `{"code":200,"data":{"total":0,"list":[]}}`
	if rows, err := c.Minutes(context.Background(), "sh600941", "1m", start, end); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	fixture = `{"code":200,"data":{"list":[{"trade_time":"2026-08-12 13:45:00"}]}}`
	if _, err := c.Minutes(context.Background(), "sh600941", "1m", start, end); err == nil {
		t.Fatal("missing values hidden")
	}
	for _, malformed := range []string{`null`, `{}`, `{"list":null}`, `{"list":[],"total":-1}`} {
		fixture = `{"code":200,"data":` + malformed + `}`
		if _, err := c.Minutes(context.Background(), "sh600941", "1m", start, end); err == nil {
			t.Fatal("invalid structure returned as empty result", malformed)
		}
	}
}
