package data

import (
	"net/http"
	"path/filepath"
	"testing"

	"go-stock/backend/db"
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
	db.Init(filepath.Join(t.TempDir(), "proxy-force.db"))
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
	db.Init(filepath.Join(t.TempDir(), "proxy-enabled.db"))
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

func TestDataFetchClientsDisableProxyByDefault(t *testing.T) {
	t.Setenv("GO_STOCK_DIEMENG_PROXY_MODE", "")
	t.Setenv("GO_STOCK_AKSHARE_PROXY_MODE", "")
	db.Init(filepath.Join(t.TempDir(), "proxy-test.db"))

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
