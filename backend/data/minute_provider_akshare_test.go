package data

import (
	"os"
	"testing"
	"time"
)

func TestAkShareFetchMinIntervalFromEnv(t *testing.T) {
	const key = "GO_STOCK_AKSHARE_MIN_INTERVAL_MS"
	origin := os.Getenv(key)
	defer os.Setenv(key, origin)

	if err := os.Setenv(key, "2500"); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	if got := akShareFetchMinInterval(); got != 2500*time.Millisecond {
		t.Fatalf("unexpected interval: %v", got)
	}

	if err := os.Setenv(key, ""); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	if got := akShareFetchMinInterval(); got != defaultAkShareFetchMinInterval {
		t.Fatalf("expect default interval: %v", got)
	}
}

func TestWaitForAkShareFetchWindowThrottle(t *testing.T) {
	const key = "GO_STOCK_AKSHARE_MIN_INTERVAL_MS"
	origin := os.Getenv(key)
	defer os.Setenv(key, origin)

	if err := os.Setenv(key, "30"); err != nil {
		t.Fatalf("set env failed: %v", err)
	}

	akShareFetchMu.Lock()
	akShareLastFetch = time.Now()
	akShareFetchMu.Unlock()

	begin := time.Now()
	waitForAkShareFetchWindow()
	cost := time.Since(begin)
	if cost < 25*time.Millisecond {
		t.Fatalf("throttle did not take effect, cost=%v", cost)
	}
}

func TestExtractAShareSymbol(t *testing.T) {
	cases := map[string]string{
		"600519.SH": "600519",
		"sz000001":  "000001",
		"000858":    "000858",
		"usTSLA.OQ": "",
	}
	for input, expected := range cases {
		got := extractAShareSymbol(input)
		if got != expected {
			t.Fatalf("extractAShareSymbol(%s)=%s, expected=%s", input, got, expected)
		}
	}
}

func TestAkShareMinuteSourceLabelCarriesAdjustment(t *testing.T) {
	for _, test := range []struct {
		name       string
		provider   string
		adjustment string
		want       string
	}{
		{name: "sina raw default", provider: "sina", want: "akshare:sina:adjustment=none"},
		{name: "sina qfq", provider: "sina", adjustment: "qfq", want: "akshare:sina:adjustment=qfq"},
		{name: "sina hfq", provider: "sina", adjustment: "hfq", want: "akshare:sina:adjustment=hfq"},
		{name: "em always raw", provider: "em", adjustment: "qfq", want: "akshare:em:adjustment=none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GO_STOCK_AKSHARE_MINUTE_ADJUST", test.adjustment)
			if got := akShareMinuteSourceLabel(test.provider); got != test.want {
				t.Fatalf("source label=%q want %q", got, test.want)
			}
		})
	}
}
