package data

import (
	"sync"
	"testing"
	"time"
)

func TestPool(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")

	pool := NewBrowserPool(1)
	defer pool.Close()

	urls := []string{
		"https://fund.eastmoney.com/016533.html",
		"https://fund.eastmoney.com/217021.html",
		"https://fund.eastmoney.com/001125.html",
	}

	var wg sync.WaitGroup
	wg.Add(len(urls))
	for _, url := range urls {
		u := url
		go func() {
			defer wg.Done()
			_, _ = pool.FetchPage(u, "body")
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for pool fetches")
	}

}
