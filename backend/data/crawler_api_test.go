package data

import (
	"context"
	"testing"
	"time"
)

func TestNewCrawlerPreservesContextAndConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://example.com",
		Headers:     map[string]string{"User-Agent": "test"},
	}

	result := (&CrawlerApi{}).NewCrawler(ctx, config)
	if result.crawlerCtx != ctx {
		t.Fatal("crawler did not preserve the caller context")
	}
	if result.crawlerBaseInfo.Name != config.Name || result.crawlerBaseInfo.BaseUrl != config.BaseUrl {
		t.Fatalf("crawler config = %+v, want %+v", result.crawlerBaseInfo, config)
	}
}
