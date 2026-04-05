package data

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchMinuteBarsWithTencentParsesRecentM1(t *testing.T) {
	oldURL := tencentMinuteMKLineURL
	oldCircuitUntil := tencentMinuteCircuitOpenUntil
	oldCircuitFailCount := tencentMinuteCircuitFailCount
	oldCircuitLastErr := tencentMinuteCircuitLastErr
	oldLastFetch := tencentMinuteLastFetch
	defer func() {
		tencentMinuteMKLineURL = oldURL
		tencentMinuteCircuitMu.Lock()
		tencentMinuteCircuitOpenUntil = oldCircuitUntil
		tencentMinuteCircuitFailCount = oldCircuitFailCount
		tencentMinuteCircuitLastErr = oldCircuitLastErr
		tencentMinuteCircuitMu.Unlock()
		tencentMinuteFetchMu.Lock()
		tencentMinuteLastFetch = oldLastFetch
		tencentMinuteFetchMu.Unlock()
	}()
	tencentMinuteCircuitMu.Lock()
	tencentMinuteCircuitOpenUntil = time.Time{}
	tencentMinuteCircuitFailCount = 0
	tencentMinuteCircuitLastErr = ""
	tencentMinuteCircuitMu.Unlock()
	tencentMinuteFetchMu.Lock()
	tencentMinuteLastFetch = time.Time{}
	tencentMinuteFetchMu.Unlock()

	loc := cnLocation()
	now := normalizeMinuteTime(time.Now().In(loc))
	base := now.Add(-2 * time.Minute)
	start := base
	end := base.Add(2 * time.Minute)

	ts1 := base.Format("200601021504")
	ts2 := base.Add(1 * time.Minute).Format("200601021504")
	ts3 := base.Add(2 * time.Minute).Format("200601021504")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("param"); got == "" {
			t.Fatalf("missing param query")
		}
		_, _ = fmt.Fprintf(w, `{"code":0,"msg":"","data":{"sh600519":{"m1":[["%s","10.00","10.20","10.30","9.90","123.0"],["%s","10.20","10.10","10.25","10.05","150.0"],["%s","10.10","10.40","10.50","10.10","111.0"],["%s","10.00","10.20","10.30","9.90","123.0"]]}}}`,
			ts2, ts1, ts3, ts2)
	}))
	defer srv.Close()
	tencentMinuteMKLineURL = srv.URL

	bars, source, err := fetchMinuteBarsWithTencent("600519.SH", start, end)
	if err != nil {
		t.Fatalf("fetchMinuteBarsWithTencent err: %v", err)
	}
	if source != "tencent" {
		t.Fatalf("unexpected source: %s", source)
	}
	if len(bars) != 3 {
		t.Fatalf("unexpected bars len: %d", len(bars))
	}
	if !bars[0].TradeTime.Equal(start) {
		t.Fatalf("unexpected first bar time: %s", bars[0].TradeTime)
	}
	if bars[0].Open != 10.20 || bars[0].Close != 10.10 || bars[0].High != 10.25 || bars[0].Low != 10.05 {
		t.Fatalf("unexpected first bar: %+v", bars[0])
	}
	if !bars[2].TradeTime.Equal(end) {
		t.Fatalf("unexpected last bar time: %s", bars[2].TradeTime)
	}
}

func TestFetchMinuteBarsWithTencentRejectsOldWindow(t *testing.T) {
	loc := cnLocation()
	end := time.Now().In(loc).Add(-9 * 24 * time.Hour)
	start := end.Add(-30 * time.Minute)

	bars, source, err := fetchMinuteBarsWithTencent("600519.SH", start, end)
	if err == nil {
		t.Fatal("expected old window error")
	}
	if source != "tencent" {
		t.Fatalf("unexpected source: %s", source)
	}
	if len(bars) != 0 {
		t.Fatalf("unexpected bars len: %d", len(bars))
	}
}

func TestTsCodeToTencentSymbol(t *testing.T) {
	cases := map[string]string{
		"600519.SH": "sh600519",
		"000001.SZ": "sz000001",
		"sh600000":  "sh600000",
		"sz300750":  "sz300750",
	}
	for input, want := range cases {
		got, err := tsCodeToTencentSymbol(input)
		if err != nil {
			t.Fatalf("tsCodeToTencentSymbol(%s) err: %v", input, err)
		}
		if got != want {
			t.Fatalf("tsCodeToTencentSymbol(%s) = %s, want %s", input, got, want)
		}
	}
	if _, err := tsCodeToTencentSymbol("830799.BJ"); err == nil {
		t.Fatal("expected bj symbol error")
	}
}
