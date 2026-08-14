package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func resetRuntimeOverrideForTest(t *testing.T) {
	t.Helper()
	ResetRuntimeOverride()
	t.Cleanup(ResetRuntimeOverride)
}

func TestLoadDefaults(t *testing.T) {
	resetRuntimeOverrideForTest(t)
	for _, key := range []string{
		"GO_STOCK_WEB_ADDR",
		"GO_STOCK_RUNTIME_DIR",
		"GO_STOCK_DB_PATH",
		"GO_STOCK_DB_BUSY_TIMEOUT_MS",
		"GO_STOCK_DB_LOG_LEVEL",
		"GO_STOCK_LOG_LEVEL",
		"GO_STOCK_PYTHON_BIN",
		"GO_STOCK_SELF_UPDATE_ENABLED",
		"GO_STOCK_MINUTE_PROVIDER",
		"GO_STOCK_MINUTE_COVER_TRADE_DAYS",
		"GO_STOCK_MINUTE_FALLBACK_AKSHARE",
		"GO_STOCK_MINUTE_FALLBACK_TENCENT",
		"GO_STOCK_SINA_MIN_INTERVAL_MS",
		"GO_STOCK_TENCENT_MIN_INTERVAL_MS",
		"GO_STOCK_AKSHARE_MINUTE_SOURCE",
		"GO_STOCK_AKSHARE_PROXY_MODE",
		"GO_STOCK_AKSHARE_TIMEOUT_SEC",
		"GO_STOCK_AKSHARE_MIN_INTERVAL_MS",
		"GO_STOCK_AKSHARE_RETRY_WAIT_MS",
		"GO_STOCK_DIEMENG_API_KEY",
		"GO_STOCK_DIEMENG_BASE_URL",
		"GO_STOCK_DIEMENG_TIMEOUT_SEC",
		"GO_STOCK_DIEMENG_MIN_INTERVAL_MS",
		"GO_STOCK_DIEMENG_PROXY_MODE",
		"GO_STOCK_DIEMENG_LEVEL",
		"GO_STOCK_BROWSER_PATH",
	} {
		t.Setenv(key, "")
	}

	cfg := Load()
	if cfg.Web.ListenAddr != DefaultWebListenAddr {
		t.Fatalf("unexpected web addr: %s", cfg.Web.ListenAddr)
	}
	if cfg.DB.Path != DefaultDBPath || cfg.DB.BusyTimeoutMS != DefaultDBBusyTimeoutMS {
		t.Fatalf("unexpected db config: %+v", cfg.DB)
	}
	if cfg.Log.Level != DefaultLogLevel {
		t.Fatalf("unexpected log level: %s", cfg.Log.Level)
	}
	if cfg.Runtime.Dir != "" {
		t.Fatalf("unexpected runtime dir: %s", cfg.Runtime.Dir)
	}
	if cfg.Python.Bin != "" {
		t.Fatalf("unexpected python bin: %s", cfg.Python.Bin)
	}
	if cfg.Update.SelfUpdateEnabled {
		t.Fatalf("expected self update disabled by default")
	}
	if cfg.Minute.Provider != DefaultMinuteProvider || cfg.Minute.CoverTradeDays != DefaultMinuteCoverTradeDays || cfg.Minute.FallbackTencent != DefaultMinuteFallbackTencent || cfg.Minute.TencentMinIntervalMS != DefaultTencentMinIntervalMS {
		t.Fatalf("unexpected minute config: %+v", cfg.Minute)
	}
	if cfg.Akshare.MinuteSource != DefaultAkshareMinuteSource || cfg.Akshare.ProxyMode != DefaultAkshareProxyMode {
		t.Fatalf("unexpected akshare config: %+v", cfg.Akshare)
	}
	if cfg.Diemeng.BaseURL != DefaultDiemengBaseURL || cfg.Diemeng.Level != DefaultDiemengLevel {
		t.Fatalf("unexpected diemeng config: %+v", cfg.Diemeng)
	}
}

func TestLoadOverridesAndFallbacks(t *testing.T) {
	resetRuntimeOverrideForTest(t)
	t.Setenv("GO_STOCK_WEB_ADDR", "0.0.0.0:9999")
	t.Setenv("GO_STOCK_RUNTIME_DIR", "/tmp/go-stock-runtime")
	t.Setenv("GO_STOCK_DB_BUSY_TIMEOUT_MS", "8000")
	t.Setenv("GO_STOCK_DB_LOG_LEVEL", "warn")
	t.Setenv("GO_STOCK_LOG_LEVEL", "info")
	t.Setenv("GO_STOCK_PYTHON_BIN", "/tmp/python/bin/python3")
	t.Setenv("GO_STOCK_SELF_UPDATE_ENABLED", "false")
	t.Setenv("GO_STOCK_MINUTE_PROVIDER", "auto")
	t.Setenv("GO_STOCK_MINUTE_COVER_TRADE_DAYS", "7")
	t.Setenv("GO_STOCK_MINUTE_FALLBACK_AKSHARE", "true")
	t.Setenv("GO_STOCK_MINUTE_FALLBACK_TENCENT", "true")
	t.Setenv("GO_STOCK_SINA_MIN_INTERVAL_MS", "1200")
	t.Setenv("GO_STOCK_TENCENT_MIN_INTERVAL_MS", "900")
	t.Setenv("GO_STOCK_AKSHARE_MINUTE_SOURCE", "em")
	t.Setenv("GO_STOCK_AKSHARE_PROXY_MODE", "inherit")
	t.Setenv("GO_STOCK_AKSHARE_TIMEOUT_SEC", "45")
	t.Setenv("GO_STOCK_AKSHARE_MIN_INTERVAL_MS", "2500")
	t.Setenv("GO_STOCK_AKSHARE_RETRY_WAIT_MS", "3500")
	t.Setenv("GO_STOCK_DIEMENG_API_KEY", "secret-key")
	t.Setenv("GO_STOCK_DIEMENG_BASE_URL", "https://example.com/api/")
	t.Setenv("GO_STOCK_DIEMENG_TIMEOUT_SEC", "88")
	t.Setenv("GO_STOCK_DIEMENG_MIN_INTERVAL_MS", "1400")
	t.Setenv("GO_STOCK_DIEMENG_PROXY_MODE", "settings")
	t.Setenv("GO_STOCK_DIEMENG_LEVEL", "5min")
	t.Setenv("GO_STOCK_BROWSER_PATH", "/usr/bin/chromium")

	cfg := Load()
	if cfg.Web.ListenAddr != "0.0.0.0:9999" {
		t.Fatalf("unexpected web addr: %s", cfg.Web.ListenAddr)
	}
	if cfg.Runtime.Dir != "/tmp/go-stock-runtime" {
		t.Fatalf("unexpected runtime dir: %s", cfg.Runtime.Dir)
	}
	if cfg.DB.BusyTimeoutMS != 8000 || cfg.DB.LogLevel != "warn" {
		t.Fatalf("unexpected db config: %+v", cfg.DB)
	}
	expectedDBPath := filepath.Join("/tmp/go-stock-runtime", "data", "stock.db") + "?cache_size=-524288&journal_mode=WAL"
	if cfg.DB.Path != expectedDBPath {
		t.Fatalf("unexpected db path: %s", cfg.DB.Path)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("unexpected log level: %s", cfg.Log.Level)
	}
	if cfg.Python.Bin != "/tmp/python/bin/python3" {
		t.Fatalf("unexpected python bin: %s", cfg.Python.Bin)
	}
	if cfg.Update.SelfUpdateEnabled {
		t.Fatalf("expected self update disabled")
	}
	if cfg.Minute.Provider != "auto" || !cfg.Minute.FallbackAkshare || !cfg.Minute.FallbackTencent || cfg.Minute.CoverTradeDays != 7 || cfg.Minute.SinaMinIntervalMS != 1200 || cfg.Minute.TencentMinIntervalMS != 900 {
		t.Fatalf("unexpected minute config: %+v", cfg.Minute)
	}
	if cfg.Akshare.MinuteSource != "em" || cfg.Akshare.ProxyMode != "inherit" || cfg.Akshare.TimeoutSec != 45 || cfg.Akshare.MinIntervalMS != 2500 || cfg.Akshare.RetryWaitMS != 3500 {
		t.Fatalf("unexpected akshare config: %+v", cfg.Akshare)
	}
	if cfg.Diemeng.APIKey != "secret-key" || cfg.Diemeng.BaseURL != "https://example.com/api" || cfg.Diemeng.TimeoutSec != 88 || cfg.Diemeng.MinIntervalMS != 1400 || cfg.Diemeng.ProxyMode != "settings" || cfg.Diemeng.Level != "5min" {
		t.Fatalf("unexpected diemeng config: %+v", cfg.Diemeng)
	}
	if cfg.Browser.Path != "/usr/bin/chromium" {
		t.Fatalf("unexpected browser path: %s", cfg.Browser.Path)
	}
}

func TestLoadNormalizesDiemengBaseURLVariants(t *testing.T) {
	resetRuntimeOverrideForTest(t)
	t.Setenv("GO_STOCK_DIEMENG_BASE_URL", "https://example.com/")
	cfg := Load()
	if cfg.Diemeng.BaseURL != "https://example.com/api" {
		t.Fatalf("expected root url to normalize to /api, got: %s", cfg.Diemeng.BaseURL)
	}

	t.Setenv("GO_STOCK_DIEMENG_BASE_URL", "https://example.com/api/")
	cfg = Load()
	if cfg.Diemeng.BaseURL != "https://example.com/api" {
		t.Fatalf("expected api path to keep normalized form, got: %s", cfg.Diemeng.BaseURL)
	}

	t.Setenv("GO_STOCK_DIEMENG_BASE_URL", "https://example.com/custom-path/")
	cfg = Load()
	if cfg.Diemeng.BaseURL != "https://example.com/custom-path" {
		t.Fatalf("expected custom path to be preserved, got: %s", cfg.Diemeng.BaseURL)
	}
}

func TestLoadInvalidValuesFallbackToDefaults(t *testing.T) {
	resetRuntimeOverrideForTest(t)
	t.Setenv("GO_STOCK_SELF_UPDATE_ENABLED", "maybe")
	t.Setenv("GO_STOCK_MINUTE_PROVIDER", "broken")
	t.Setenv("GO_STOCK_MINUTE_COVER_TRADE_DAYS", "99")
	t.Setenv("GO_STOCK_MINUTE_FALLBACK_AKSHARE", "maybe")
	t.Setenv("GO_STOCK_MINUTE_FALLBACK_TENCENT", "maybe")
	t.Setenv("GO_STOCK_TENCENT_MIN_INTERVAL_MS", "-1")
	t.Setenv("GO_STOCK_AKSHARE_MINUTE_SOURCE", "broken")
	t.Setenv("GO_STOCK_AKSHARE_TIMEOUT_SEC", "0")
	t.Setenv("GO_STOCK_DIEMENG_TIMEOUT_SEC", "0")
	t.Setenv("GO_STOCK_DIEMENG_LEVEL", "2min")

	cfg := Load()
	if cfg.Minute.Provider != DefaultMinuteProvider || cfg.Minute.CoverTradeDays != DefaultMinuteCoverTradeDays || cfg.Minute.FallbackAkshare != DefaultMinuteFallbackAkshare || cfg.Minute.FallbackTencent != DefaultMinuteFallbackTencent || cfg.Minute.TencentMinIntervalMS != DefaultTencentMinIntervalMS {
		t.Fatalf("unexpected minute fallback config: %+v", cfg.Minute)
	}
	if cfg.Akshare.MinuteSource != DefaultAkshareMinuteSource || cfg.Akshare.TimeoutSec != DefaultAkshareTimeoutSec {
		t.Fatalf("unexpected akshare fallback config: %+v", cfg.Akshare)
	}
	if cfg.Diemeng.TimeoutSec != DefaultDiemengTimeoutSec || cfg.Diemeng.Level != DefaultDiemengLevel {
		t.Fatalf("unexpected diemeng fallback config: %+v", cfg.Diemeng)
	}
	if cfg.Update.SelfUpdateEnabled {
		t.Fatalf("unexpected self update fallback: %+v", cfg.Update)
	}
}

func TestStartupSummaryRedactsSecrets(t *testing.T) {
	resetRuntimeOverrideForTest(t)
	t.Setenv("GO_STOCK_DIEMENG_API_KEY", "super-secret")
	summary := Load().StartupSummary()
	if !strings.Contains(summary, `"apiKeyConfigured":true`) {
		t.Fatalf("expected summary to expose configured flag: %s", summary)
	}
	if strings.Contains(summary, "super-secret") {
		t.Fatalf("summary should not expose api key: %s", summary)
	}
}

func TestLoadAppliesRuntimeOverride(t *testing.T) {
	resetRuntimeOverrideForTest(t)

	privateProvider := "diemeng"
	akshareSource := "em"
	apiKey := "override-secret"
	baseURL := "https://example.com/custom-api/"
	timeout := 77
	interval := 2300
	proxyMode := "settings"
	level := "15min"
	SetRuntimeOverride(&RuntimeOverride{
		MinuteProvider:      &privateProvider,
		AkshareMinuteSource: &akshareSource,
		DiemengAPIKey:       &apiKey,
		DiemengBaseURL:      &baseURL,
		DiemengTimeoutSec:   &timeout,
		DiemengMinInterval:  &interval,
		DiemengProxyMode:    &proxyMode,
		DiemengLevel:        &level,
	})

	cfg := Load()
	if cfg.Minute.Provider != "diemeng" {
		t.Fatalf("unexpected provider override: %+v", cfg.Minute)
	}
	if cfg.Akshare.MinuteSource != "em" {
		t.Fatalf("unexpected akshare override: %+v", cfg.Akshare)
	}
	if cfg.Diemeng.APIKey != "override-secret" || cfg.Diemeng.BaseURL != "https://example.com/custom-api" || cfg.Diemeng.TimeoutSec != 77 || cfg.Diemeng.MinIntervalMS != 2300 || cfg.Diemeng.ProxyMode != "settings" || cfg.Diemeng.Level != "15min" {
		t.Fatalf("unexpected diemeng override: %+v", cfg.Diemeng)
	}
}
