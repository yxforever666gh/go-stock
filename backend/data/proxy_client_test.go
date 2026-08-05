package data

import (
	"net/http"
	"path/filepath"
	"testing"

	"go-stock/backend/db"
	appconfig "go-stock/internal/config"
)

func TestNewNoProxyRestyClientDisablesProxy(t *testing.T) {
	client := newNoProxyRestyClient()
	assertRestyProxyDisabled(t, client)
}

func TestNewRealtimeRestyClientDisablesProxy(t *testing.T) {
	client := newRealtimeRestyClient()
	assertRestyProxyDisabled(t, client)
}

func TestNewFetchRestyClientDisablesProxyWhenForced(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "proxy-force.db"))
	if err := db.Dao.AutoMigrate(&Settings{}); err != nil {
		t.Fatalf("migrate settings failed: %v", err)
	}
	if err := db.Dao.Create(&Settings{
		HttpProxy:            "http://127.0.0.1:7890",
		HttpProxyEnabled:     true,
		ForceNoProxyForFetch: true,
	}).Error; err != nil {
		t.Fatalf("seed settings failed: %v", err)
	}

	client := newFetchRestyClient()
	assertRestyProxyDisabled(t, client)
}

func TestNewFetchRestyClientUsesSettingsProxyWhenForceDisabled(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "proxy-enabled.db"))
	if err := db.Dao.AutoMigrate(&Settings{}); err != nil {
		t.Fatalf("migrate settings failed: %v", err)
	}
	if err := db.Dao.Create(&Settings{
		HttpProxy:        "http://127.0.0.1:7890",
		HttpProxyEnabled: true,
	}).Error; err != nil {
		t.Fatalf("seed settings failed: %v", err)
	}
	if err := db.Dao.Model(&Settings{}).Where("1 = 1").Update("force_no_proxy_for_fetch", false).Error; err != nil {
		t.Fatalf("disable force-no-proxy failed: %v", err)
	}

	client := newFetchRestyClient()
	assertRestyProxyEnabled(t, client)
}

func TestNewSettingsProxyRestyClientIfConfiguredUsesProxy(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "proxy-settings-only.db"))
	if err := db.Dao.AutoMigrate(&Settings{}); err != nil {
		t.Fatalf("migrate settings failed: %v", err)
	}
	if err := db.Dao.Create(&Settings{
		HttpProxy:        "http://127.0.0.1:7890",
		HttpProxyEnabled: true,
	}).Error; err != nil {
		t.Fatalf("seed settings failed: %v", err)
	}

	client, ok := newSettingsProxyRestyClientIfConfigured()
	if !ok {
		t.Fatal("expected settings proxy client to be available")
	}
	assertRestyProxyEnabled(t, client)
}

func TestDataFetchClientsDisableProxyByDefault(t *testing.T) {
	t.Setenv("GO_STOCK_DIEMENG_PROXY_MODE", "")
	t.Setenv("GO_STOCK_AKSHARE_PROXY_MODE", "")
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "proxy-test.db"))

	stockAPI := NewStockDataApi()
	assertRestyProxyDisabled(t, stockAPI.client)

	fundAPI := NewFundApi()
	assertRestyProxyDisabled(t, fundAPI.client)

	tushareAPI := NewTushareApi(&SettingConfig{})
	assertRestyProxyDisabled(t, tushareAPI.client)

	diemengClient := newDiemengClient()
	assertRestyProxyDisabled(t, diemengClient)

	sinaClient := newSinaMinuteClient()
	assertRestyProxyDisabled(t, sinaClient)

	tencentClient := newTencentMinuteClient()
	assertRestyProxyDisabled(t, tencentClient)
}

func TestDiemengEffectiveBaseURLUsesDataHostWithoutMohomoparty(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("all_proxy", "")
	appconfig.ResetRuntimeOverride()
	t.Cleanup(appconfig.ResetRuntimeOverride)

	raw := "https://mg.diemeng.chat/api"
	appconfig.SetRuntimeOverride(&appconfig.RuntimeOverride{
		DiemengBaseURL: &raw,
	})
	if got := DiemengEffectiveBaseURLForDisplay(); got != "https://data.diemeng.chat/api" {
		t.Fatalf("expected direct diemeng host to normalize to data.diemeng.chat, got %s", got)
	}

	raw = "https://diemeng.chat/api"
	appconfig.SetRuntimeOverride(&appconfig.RuntimeOverride{
		DiemengBaseURL: &raw,
	})
	if got := DiemengEffectiveBaseURLForDisplay(); got != "https://data.diemeng.chat/api" {
		t.Fatalf("expected direct diemeng host to normalize to data.diemeng.chat, got %s", got)
	}
}

func TestDiemengEffectiveBaseURLKeepsConfiguredHostWithMohomopartyProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("all_proxy", "")
	// Windows treats environment-variable names case-insensitively, so set the
	// value after clearing both spellings above.
	t.Setenv("HTTPS_PROXY", "http://mohomoparty:7890")
	appconfig.ResetRuntimeOverride()
	t.Cleanup(appconfig.ResetRuntimeOverride)

	raw := "https://mg.diemeng.chat/api"
	appconfig.SetRuntimeOverride(&appconfig.RuntimeOverride{
		DiemengBaseURL: &raw,
	})
	if got := DiemengEffectiveBaseURLForDisplay(); got != "https://mg.diemeng.chat/api" {
		t.Fatalf("expected mohomo proxy mode to keep configured host, got %s", got)
	}
}

func TestGetMarketNewsFetchMetaReturnsCopy(t *testing.T) {
	marketNewsSetFetchMeta("cls_telegraph_api", marketNewsFetchMeta{
		NetworkPath:  "proxy",
		FallbackUsed: true,
	})
	meta := GetMarketNewsFetchMeta("cls_telegraph_api")
	if meta["networkPath"] != "proxy" {
		t.Fatalf("expected networkPath proxy, got %+v", meta)
	}
	if meta["fallbackUsed"] != true {
		t.Fatalf("expected fallbackUsed true, got %+v", meta)
	}
	meta["networkPath"] = "direct"
	meta2 := GetMarketNewsFetchMeta("cls_telegraph_api")
	if meta2["networkPath"] != "proxy" {
		t.Fatalf("expected fetch meta to be copied, got %+v", meta2)
	}
}

func assertRestyProxyDisabled(t *testing.T, client interface{ GetClient() *http.Client }) {
	t.Helper()
	httpClient := client.GetClient()
	if httpClient == nil {
		t.Fatal("http client is nil")
	}
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected *http.Transport, got %T", httpClient.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("expected proxy to be disabled")
	}
}

func assertRestyProxyEnabled(t *testing.T, client interface{ GetClient() *http.Client }) {
	t.Helper()
	httpClient := client.GetClient()
	if httpClient == nil {
		t.Fatal("http client is nil")
	}
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected *http.Transport, got %T", httpClient.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("expected proxy to be enabled")
	}
}
