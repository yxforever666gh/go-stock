package data

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/duke-git/lancet/v2/strutil"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
)

func TestNewTimeOutGuShiTongCrawler(t *testing.T) {
	crawlerAPI := CrawlerApi{}
	timeout := 10
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://gushitong.baidu.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}

	result := crawlerAPI.NewTimeOutCrawler(timeout, crawlerBaseInfo)
	assert.NotNil(t, result.crawlerCtx)
	assert.Equal(t, crawlerBaseInfo, result.crawlerBaseInfo)
}

func TestNewGuShiTongCrawler(t *testing.T) {
	crawlerAPI := CrawlerApi{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://gushitong.baidu.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}

	result := crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)
	assert.Equal(t, ctx, result.crawlerCtx)
	assert.Equal(t, crawlerBaseInfo, result.crawlerBaseInfo)
}

func TestGetHtml(t *testing.T) {
	requireIntegration(t)
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://gushitong.baidu.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	url := "https://www.cls.cn/searchPage?type=depth&keyword=%E6%96%B0%E5%B8%8C%E6%9C%9B"
	waitVisible := ".search-telegraph-list,.subject-interest-list"

	//url = "https://gushitong.baidu.com/stock/ab-600745"
	//waitVisible = "div.news-item"
	htmlContent, success := crawlerAPI.GetHtml(url, waitVisible, true)
	if success {
		document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
		}
		var messages []string
		document.Find(waitVisible).Each(func(i int, selection *goquery.Selection) {
			text := strutil.RemoveNonPrintable(selection.Text())
			messages = append(messages, text)
			logger.SugaredLogger.Infof("搜索到消息-%s: %s", "", text)
		})
	}
	//logger.SugaredLogger.Infof("htmlContent:%s", htmlContent)
}

func TestGetHtmlWithActions(t *testing.T) {
	requireIntegration(t)
	crawlerAPI := CrawlerApi{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	crawlerAPI = crawlerAPI.NewCrawler(ctx, CrawlerBaseInfo{
		Name:        "百度股市通",
		Description: "Test Crawler Description",
		BaseUrl:     "https://gushitong.baidu.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	})
	actions := []chromedp.Action{
		chromedp.Navigate("https://gushitong.baidu.com/stock/ab-600745"),
		chromedp.WaitVisible("div.cos-tab"),
		chromedp.Click(".header div.cos-tab:nth-child(6)", chromedp.ByQuery),
		chromedp.ScrollIntoView("div.finance-container >div.row:nth-child(3)"),
		chromedp.WaitVisible("div.cos-tabs-header-container"),
		chromedp.Click(".page-content .cos-tabs-header-container .cos-tabs-header .cos-tab:nth-child(1)", chromedp.ByQuery),
		chromedp.WaitVisible(".page-content .finance-container .report-col-content", chromedp.ByQuery),
		chromedp.Click(".page-content .cos-tabs-header-container .cos-tabs-header .cos-tab:nth-child(4)", chromedp.ByQuery),
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight);`, nil),
		chromedp.Sleep(1 * time.Second),
	}
	htmlContent, success := crawlerAPI.GetHtmlWithActions(&actions, false)
	if success {
		document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
		}
		var messages []string
		document.Find("div.report-table-list-container,div.report-row").Each(func(i int, selection *goquery.Selection) {
			text := strutil.RemoveWhiteSpace(selection.Text(), false)
			messages = append(messages, text)
			logger.SugaredLogger.Infof("搜索到消息-%s: %s", "", text)
		})
		logger.SugaredLogger.Infof("messages:%d", len(messages))
	}
	//logger.SugaredLogger.Infof("htmlContent:%s", htmlContent)
}

func TestHk(t *testing.T) {
	requireIntegration(t)
	//https://stock.finance.sina.com.cn/hkstock/quotes/00001.html
	db.Init("../../data/stock.db")
	hks := &[]models.StockInfoHK{}
	db.Dao.Model(&models.StockInfoHK{}).Limit(1).Find(hks)

	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://stock.finance.sina.com.cn",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	for _, hk := range *hks {
		logger.SugaredLogger.Infof("hk: %+v", hk)
		url := fmt.Sprintf("https://stock.finance.sina.com.cn/hkstock/quotes/%s.html", strings.ReplaceAll(hk.Code, ".HK", ""))
		htmlContent, ok := crawlerAPI.GetHtml(url, "#stock_cname", true)
		if !ok {
			continue
		}
		//logger.SugaredLogger.Infof("htmlContent: %s", htmlContent)
		document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
		}
		document.Find("#stock_cname").Each(func(i int, selection *goquery.Selection) {
			text := strutil.RemoveNonPrintable(selection.Text())
			logger.SugaredLogger.Infof("股票名称-:%s", text)
		})

		document.Find("#mts_stock_hk_price").Each(func(i int, selection *goquery.Selection) {
			text := strutil.RemoveNonPrintable(selection.Text())
			logger.SugaredLogger.Infof("股票名称-现价: %s", text)
		})

		document.Find(".deta_hqContainer >.deta03 li").Each(func(i int, selection *goquery.Selection) {
			text := strutil.RemoveNonPrintable(selection.Text())
			logger.SugaredLogger.Infof("股票名称-%s: %s", "", text)
		})

	}
}

func TestUpdateUSName(t *testing.T) {
	requireIntegration(t)
	db.Init("../../data/stock.db")
	us := &[]models.StockInfoUS{}
	db.Dao.Model(&models.StockInfoUS{}).Where("name = ?", "").Order("RANDOM()").Find(us)

	for _, us := range *us {
		crawlerAPI := CrawlerApi{}
		crawlerBaseInfo := CrawlerBaseInfo{
			Name:        "TestCrawler",
			Description: "Test Crawler Description",
			BaseUrl:     "https://stock.finance.sina.com.cn",
			Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

		url := fmt.Sprintf("https://stock.finance.sina.com.cn/usstock/quotes/%s.html", us.Code[:len(us.Code)-3])
		logger.SugaredLogger.Infof("url: %s", url)
		//waitVisible := "span.quote_title_name"
		waitVisible := "div.hq_title > h1"

		htmlContent, ok := crawlerAPI.GetHtml(url, waitVisible, true)

		if !ok {
			continue
		}
		//logger.SugaredLogger.Infof("htmlContent: %s", htmlContent)
		document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
		}
		name := ""
		document.Find(waitVisible).Each(func(i int, selection *goquery.Selection) {
			name = strutil.RemoveNonPrintable(selection.Text())
			name = strutil.SplitAndTrim(name, " ", "")[0]
			logger.SugaredLogger.Infof("股票名称-:%s", name)
		})
		db.Dao.Model(&models.StockInfoUS{}).Where("code = ?", us.Code).Updates(map[string]interface{}{
			"name":      name,
			"full_name": name,
		})
	}

}
func TestUS(t *testing.T) {
	requireIntegration(t)
	db.Init("../../data/stock.db")
	bytes, err := os.ReadFile("../../build/us.json")
	if err != nil {
		return
	}
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://quote.eastmoney.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	tick := &Tick{}
	json.Unmarshal(bytes, &tick)
	for i, datum := range tick.Data {
		logger.SugaredLogger.Infof("datum: %d, %+v", i, datum)
		name := ""

		//https://quote.eastmoney.com/us/AAPL.html
		//https://stock.finance.sina.com.cn/usstock/quotes/goog.html
		//url := fmt.Sprintf("https://stock.finance.sina.com.cn/usstock/quotes/%s.html", strings.ReplaceAll(datum.C, ".US", ""))
		////waitVisible := "span.quote_title_name"
		//waitVisible := "div.hq_title > h1"
		//
		//htmlContent, ok := crawlerAPI.GetHtml(url, waitVisible, true)
		//
		//if !ok {
		//	continue
		//}
		////logger.SugaredLogger.Infof("htmlContent: %s", htmlContent)
		//document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		//if err != nil {
		//	logger.SugaredLogger.Error(err.Error())
		//}
		//document.Find(waitVisible).Each(func(i int, selection *goquery.Selection) {
		//	name = strutil.RemoveNonPrintable(selection.Text())
		//	name = strutil.SplitAndTrim(name, " ", "")[0]
		//	logger.SugaredLogger.Infof("股票名称-:%s", name)
		//})

		us := &models.StockInfoUS{
			Code:     datum.C + ".US",
			EName:    datum.N,
			FullName: datum.N,
			Name:     name,
			Exchange: datum.E,
			Type:     datum.T,
		}
		db.Dao.Create(us)
	}
}

func TestUSSINA(t *testing.T) {
	requireIntegration(t)
	//https://finance.sina.com.cn/stock/usstock/sector.shtml#cm
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://quote.eastmoney.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	html, ok := crawlerAPI.GetHtml("https://finance.sina.com.cn/stock/usstock/sector.shtml#cm", "div#data", false)
	if !ok {
		return
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	document.Find("div#data > table >tbody >tr").Each(func(i int, selection *goquery.Selection) {
		tr := selection.Text()
		logger.SugaredLogger.Infof("tr: %s", tr)
	})
}

func TestSina(t *testing.T) {
	requireIntegration(t)
	db.Init("../../data/stock.db")
	url := "https://finance.sina.com.cn/realstock/company/sz002906/nc.shtml"
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://finance.sina.com.cn",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)
	html, ok := crawlerAPI.GetHtml(url, "div#hqDetails table", true)
	if !ok {
		return
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}

	//price
	price := strutil.RemoveWhiteSpace(document.Find("div#price").First().Text(), false)
	hqTime := strutil.RemoveWhiteSpace(document.Find("div#hqTime").First().Text(), false)

	var markdown strings.Builder
	markdown.WriteString("\n ## 当前股票数据：\n")
	markdown.WriteString(fmt.Sprintf("### 当前股价：%s 时间：%s\n", price, hqTime))
	GetTableMarkdown(document, "div#hqDetails table", &markdown)

}

func TestDC(t *testing.T) {
	requireIntegration(t)
	url := "https://emweb.securities.eastmoney.com/pc_hsf10/pages/index.html?type=web&code=sh600745#/cwfx"
	db.Init("../../data/stock.db")
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://emweb.securities.eastmoney.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	var markdown strings.Builder
	markdown.WriteString("\n ## 财务数据：\n")
	html, ok := crawlerAPI.GetHtml(url, "div.report_table table", false)
	if !ok {
		return
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	GetTableMarkdown(document, "div.report_table table", &markdown)

}

type Tick struct {
	Code   int    `json:"code"`
	Status string `json:"status"`
	Data   []struct {
		C string `json:"c"`
		N string `json:"n"`
		T string `json:"t"`
		E string `json:"e"`
	} `json:"data"`
}
