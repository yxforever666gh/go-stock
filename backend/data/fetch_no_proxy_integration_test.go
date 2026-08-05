package data

import (
	"path/filepath"
	"testing"

	"go-stock/backend/db"
)

func TestFetchSourcesWorkWithoutProxy(t *testing.T) {
	requireIntegration(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:9")
	t.Setenv("GO_STOCK_DIEMENG_PROXY_MODE", "inherit")
	t.Setenv("GO_STOCK_AKSHARE_PROXY_MODE", "inherit")

	initDatabaseForTest(t, filepath.Join(t.TempDir(), "fetch-no-proxy.db"))
	if err := db.Dao.AutoMigrate(&Settings{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	if err := db.Dao.Create(&Settings{
		HttpProxy:            "http://127.0.0.1:7890",
		HttpProxyEnabled:     true,
		ForceNoProxyForFetch: true,
		BrowserPoolSize:      1,
		CrawlTimeOut:         20,
	}).Error; err != nil {
		t.Fatalf("seed settings failed: %v", err)
	}
	telegraphs := GetTopNewsList(20)
	if len(*telegraphs) == 0 {
		t.Fatal("expected cls top news without proxy")
	}

	calendar := NewMarketNewsApi().ClsCalendar()
	if len(calendar) == 0 {
		t.Fatal("expected cls calendar without proxy")
	}

	gdp := NewMarketNewsApi().GetGDP()
	if gdp == nil || len(gdp.GDPResult.Data) == 0 {
		t.Fatal("expected eastmoney gdp data without proxy")
	}
}
