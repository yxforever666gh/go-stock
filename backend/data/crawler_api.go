package data

import (
	"context"
	"github.com/chromedp/chromedp"
	"go-stock/backend/logger"
)

// @Author spark
// @Date 2025/2/13 9:25
// @Desc
// -----------------------------------------------------------------------------------

type CrawlerApi struct {
	crawlerCtx      context.Context
	crawlerBaseInfo CrawlerBaseInfo
	pool            *BrowserPool
}

func (c *CrawlerApi) NewCrawler(ctx context.Context, crawlerBaseInfo CrawlerBaseInfo) CrawlerApi {
	return CrawlerApi{
		crawlerCtx:      ctx,
		crawlerBaseInfo: crawlerBaseInfo,
		pool:            NewBrowserPool(GetSettingConfig().BrowserPoolSize),
	}
}
func (c *CrawlerApi) GetHtml(url, waitVisible string, headless bool) (string, bool) {
	page, err := c.pool.FetchPage(url, waitVisible)
	if err != nil {
		return "", false
	}
	return page, true
}
func (c *CrawlerApi) GetHtmlWithActions(actions *[]chromedp.Action, headless bool) (string, bool) {
	htmlContent := ""
	*actions = append(*actions, chromedp.InnerHTML("body", &htmlContent))

	path := GetSettingConfig().BrowserPath
	pctx, pcancel := newCrawlerExecAllocator(c.crawlerCtx, path, headless, c.crawlerBaseInfo.Headers["User-Agent"])
	defer pcancel()
	ctx, cancel := chromedp.NewContext(pctx, chromedp.WithLogf(logger.SugaredLogger.Infof))
	defer cancel()
	if err := chromedp.Run(ctx, *actions...); err != nil {
		logger.SugaredLogger.Error(err.Error())
		return "", false
	}
	return htmlContent, true
}

type CrawlerBaseInfo struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	BaseUrl     string            `json:"base_url"`
	Headers     map[string]string `json:"headers"`
}
