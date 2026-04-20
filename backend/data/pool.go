package data

import (
	"context"
	"go-stock/backend/logger"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// BrowserPool 浏览器池结构
type BrowserPool struct {
	pool chan *browserSession
	mu   sync.Mutex
	size int
}

type browserSession struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NewBrowserPool 创建新的浏览器池
func NewBrowserPool(size int) *BrowserPool {
	pool := make(chan *browserSession, size)
	for i := 0; i < size; i++ {
		path := GetSettingConfig().BrowserPath
		crawlTimeOut := GetSettingConfig().CrawlTimeOut
		if crawlTimeOut < 15 {
			crawlTimeOut = 30
		}
		timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
		allocatorCtx, allocatorCancel := newCrawlerExecAllocator(timeoutCtx, path, true, "")
		browserCtx, browserCancel := chromedp.NewContext(allocatorCtx, chromedp.WithLogf(logger.SugaredLogger.Infof))
		pool <- &browserSession{
			ctx: browserCtx,
			cancel: func() {
				browserCancel()
				allocatorCancel()
				timeoutCancel()
			},
		}
	}
	return &BrowserPool{
		pool: pool,
		size: size,
	}
}

// Get 从池中获取浏览器实例
func (pool *BrowserPool) Get() *browserSession {
	return <-pool.pool
}

// Put 将浏览器实例放回池中
func (pool *BrowserPool) Put(session *browserSession) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	// 检查池是否已满
	if len(pool.pool) >= pool.size {
		// 池已满，关闭并丢弃这个实例
		session.cancel()
		return
	}
	// 归还到池里时不要 Cancel。
	// Cancel 会关闭内部 channel，后续复用该 ctx 会触发 panic（close of closed channel）。
	pool.pool <- session
}

// Close 关闭池中的所有浏览器实例
func (pool *BrowserPool) Close() {
	close(pool.pool)
	for session := range pool.pool {
		session.cancel()
	}
}

// FetchPage 使用浏览器池获取页面内容
func (pool *BrowserPool) FetchPage(url, waitVisible string) (string, error) {
	// 从池中获取浏览器实例
	session := pool.Get()
	defer pool.Put(session) // 使用完毕后放回池中
	var htmlContent string
	err := chromedp.Run(session.ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(waitVisible, chromedp.ByQuery), // 确保  元素可见
		chromedp.WaitReady(waitVisible, chromedp.ByQuery),   // 确保  元素准备好
		chromedp.InnerHTML("body", &htmlContent),
		chromedp.Evaluate(`window.close()`, nil),
	)
	if err != nil {
		return "", err
	}
	return htmlContent, nil
}
