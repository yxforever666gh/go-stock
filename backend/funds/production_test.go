package funds

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestProductionFundRankingParsers(t *testing.T) {
	eastmoney, err := parseEastmoneyFundRankings([]byte(`{"Data":{"Datas":[{"FCODE":"000001","SHORTNAME":"价值混合","FTYPE":"混合型","DWJZ":"1.2345","FSRQ":"2026-08-28","RZDF":"1.2","SYL_Z":"2.3","SYL_Y":"3.4","SYL_3Y":"4.5","SYL_6Y":"5.6","SYL_1N":"6.7","SYL_3N":"7.8","SYL_JN":"8.9","SYL_LN":"9.1","JJGM":"12.5亿元","GMRQ":"2026-06-30"}]}}`))
	require.NoError(t, err)
	require.Len(t, eastmoney, 1)
	assert.Equal(t, FundCategoryMixed, eastmoney[0].Category)
	assert.Equal(t, 1.2345, *eastmoney[0].NAV)
	assert.Equal(t, 1.25e9, *eastmoney[0].Scale)

	sina, err := parseSinaFundRankings([]byte(`IO.XSRV2.CallbackList({"data":[{"symbol":"000002","sname":"海外QDII","type":"QDII","dwjz":"2.1","jzrq":"2026/08/27","zdf":"0.5","1n":"12.3","jjgm":"2.5亿"}]})`))
	require.NoError(t, err)
	require.Len(t, sina, 1)
	assert.Equal(t, FundCategoryQDII, sina[0].Category)
	assert.Equal(t, 12.3, *sina[0].OneYearReturn)
	assert.Equal(t, 2.5e8, *sina[0].Scale)

	assert.Equal(t, "zs", eastmoneyFundType(FundCategoryIndex))
	assert.Equal(t, "3nzf", eastmoneyFundSort(FundPeriodThreeYears))
	assert.Equal(t, "jjgm", sinaFundSort(FundPeriodScale))
}

func TestEastmoneyRankhandlerParsesAllRankingMetricsAndScale(t *testing.T) {
	body := []byte(`var rankData = {datas:["002910,易方达供给改革混合,YFDGGGGHH,2026-08-27,3.7028,3.7028,-0.4,-0.46,5.35,18.44,20.1,30.2,0,40.3,12.5,130.5,x,x,66.5"],allRecords:1};`)
	items, err := parseEastmoneyFundRankings(body)
	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	assert.Equal(t, "002910", item.Code)
	assert.Equal(t, "2026-08-27", item.NAVDate)
	assert.Equal(t, -0.4, *item.DayReturn)
	assert.Equal(t, -0.46, *item.WeekReturn)
	assert.Equal(t, 5.35, *item.MonthReturn)
	assert.Equal(t, 18.44, *item.ThreeMonthReturn)
	assert.Equal(t, 20.1, *item.SixMonthReturn)
	assert.Equal(t, 30.2, *item.OneYearReturn)
	assert.Equal(t, 40.3, *item.ThreeYearReturn)
	assert.Equal(t, 12.5, *item.YearToDateReturn)
	assert.Equal(t, 130.5, *item.SinceInceptionReturn)
	assert.Equal(t, 6.65e9, *item.Scale)
	assert.Empty(t, item.ScaleDate, "rankhandler does not publish a scale date")
}

func TestEastmoneyHTTP200BusinessErrorIsProviderFailure(t *testing.T) {
	body := `{"Data":null,"ErrCode":4,"ErrMsg":"404"}`
	doer := httpDoerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	provider := NewEastmoneyFundRankingProvider(doer)
	result := provider.FetchFundRankings(context.Background(), FundRankingQuery{Category: FundCategoryAll, Period: FundPeriodOneYear, SortDirection: SortDescending})
	require.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "business error")
	assert.Equal(t, "unavailable", result.Status)
	assert.Empty(t, result.Data)

	_, err := parseEastmoneyFundRankings([]byte(body))
	require.Error(t, err)
}

func TestExchangeParsersSupportCurrentSSEAndSZSEShapes(t *testing.T) {
	sse, err := parseExchangeETFIdentities([]byte(`{"result":[{"fundCode":"510300","fundAbbr":"300ETF","secNameFull":"华泰柏瑞沪深300交易型开放式指数证券投资基金","INDEX_NAME":"沪深300","listingDate":"2012-05-28","subClass":"01"}],"total":897}`), "SH")
	require.NoError(t, err)
	require.Len(t, sse, 1)
	assert.Equal(t, "sh510300", sse[0].Code)
	assert.Equal(t, "300ETF", sse[0].Name)
	assert.Equal(t, "2012-05-28", normalizeDate(sse[0].ListDate))

	szBody := []byte(`[{"metadata":{"pagesize":20,"pagecount":37,"recordcount":726},"data":[{"sys_key":"<a href='/market/product/list/etfList/index.html?code=159001'>159001</a>","jjjcurl":"<a href='/disclosure/fund/notice/index.html?code=159001'>货币ETF</a>","jjlb":"ETF","tzlb":"货币型","ssrq":"2013-01-28","dqgm":"125.50亿元"},{"sys_key":"160001","jjjcurl":"LOF基金","jjlb":"LOF","tzlb":"混合型","ssrq":"2002-08-30","dqgm":"20亿元"}]}]`)
	rows, err := parseExchangeETFIdentityRows(szBody, "SZ")
	require.NoError(t, err)
	require.Len(t, rows, 1, "non-ETF jjlb rows must be excluded")
	assert.Equal(t, "sz159001", rows[0].Identity.Code)
	assert.Equal(t, "货币ETF", rows[0].Identity.Name)
	assert.Equal(t, ETFCategoryMoney, rows[0].Identity.Category)
	assert.Equal(t, "2013-01-28", normalizeDate(rows[0].Identity.ListDate))
	require.NotNil(t, rows[0].Scale)
	assert.Equal(t, 12.55e9, *rows[0].Scale)
	assert.Equal(t, 37, szsePageCount(szBody))
}

func TestEastmoneyETFNetValueHTMLProvidesNAVDateAndPremium(t *testing.T) {
	body := []byte(`<html><body><table><thead><tr><th>关注</th><th>比较</th><th>序号</th><th>基金代码</th><th>基金简称</th><th colspan="2">2026-08-27</th></tr></thead><tbody><tr id="tr510300"><td>关注</td><td>比较</td><td>1</td><td><a>510300</a></td><td>沪深300ETF</td><td>4.0000</td><td>4.5000</td><td>3.9800</td><td>4.4800</td><td>0.0200</td><td>0.50%</td><td>4.0500</td><td>1.25%</td></tr></tbody></table></body></html>`)
	values, err := parseEastmoneyETFNetValueHTML(body, []ETFIdentity{{Code: "sh510300", Name: "沪深300ETF", Market: "SH", Listed: true}})
	require.NoError(t, err)
	require.Contains(t, values, "sh510300")
	assert.Equal(t, 4.0, *values["sh510300"].NAV)
	assert.Equal(t, "2026-08-27", values["sh510300"].NAVDate)
	assert.InDelta(t, 1.25, *values["sh510300"].PremiumRate, 0.000001)
	assert.Nil(t, values["sh510300"].Scale, "NAV page must not fabricate scale")
	assert.Nil(t, values["sh510300"].Shares, "NAV page must not fabricate shares")
}

func TestEastmoneyETFBasicHTMLParsesTrackingIndexAndManagementFee(t *testing.T) {
	body := []byte(`<html><body><table><tr><th>管理费率</th><td>0.15%（每年）</td><th>托管费率</th><td>0.05%（每年）</td></tr><tr><th>业绩比较基准</th><td>创业板指数</td><th>跟踪标的</th><td>创业板指数(价格)</td></tr></table></body></html>`)
	value, err := parseEastmoneyETFBasicHTML(body, ETFIdentity{Code: "sz159915", Name: "创业板ETF", Market: "SZ", Listed: true})
	require.NoError(t, err)
	assert.Equal(t, "创业板指数(价格)", value.TrackingIndex)
	require.NotNil(t, value.ManagementFee)
	assert.Equal(t, 0.15, *value.ManagementFee)
}

func TestEastmoneyHoldingBusinessErrorMakesFundamentalsPartial(t *testing.T) {
	doer := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.Contains(request.URL.Path, "ETFN_jzzzl"):
			body = `<html><body><table><thead><tr><th>2026-08-27</th></tr></thead><tbody><tr><td>关注</td><td>比较</td><td>1</td><td>510300</td><td>沪深300ETF</td><td>4.0000</td><td>4.5000</td><td>3.9800</td><td>4.4800</td><td>0.0200</td><td>0.50%</td><td>4.0500</td><td>1.25%</td></tr></tbody></table></body></html>`
		case strings.Contains(request.URL.Path, "jbgk_510300"):
			body = `<table><tr><th>管理费率</th><td>0.15%（每年）</td><th>托管费率</th><td>0.05%（每年）</td></tr><tr><th>业绩比较基准</th><td>沪深300指数</td><th>跟踪标的</th><td>沪深300指数</td></tr></table>`
		case strings.Contains(request.URL.Path, "FundMNInverstPosition"):
			body = `{"Data":null,"ErrCode":61136,"ErrMsg":"holding unavailable"}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("missing")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	provider := NewEastmoneyETFFundamentalsProvider(doer)
	result := provider.FetchETFFundamentals(context.Background(), []ETFIdentity{{Code: "sh510300", Name: "沪深300ETF", Market: "SH", Listed: true}})
	require.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "61136")
	assert.Equal(t, "partial", result.Status)
	require.Contains(t, result.Data, "sh510300")
	assert.Equal(t, "沪深300指数", result.Data["sh510300"].TrackingIndex)
}

func TestExchangeProviderPaginatesCurrentSZSEResponse(t *testing.T) {
	var mutex sync.Mutex
	seenPages := map[string]int{}
	doer := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Host, "sse") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"result":[]}`)), Header: make(http.Header)}, nil
		}
		if strings.Contains(request.URL.Host, "szse") {
			page := request.URL.Query().Get("PAGENO")
			mutex.Lock()
			seenPages[page]++
			mutex.Unlock()
			code := map[string]string{"1": "159001", "2": "159002", "3": "159003"}[page]
			body := `[{"metadata":{"pagesize":20,"pagecount":3,"recordcount":3},"data":[{"sys_key":"` + code + `","jjjcurl":"ETF-` + code + `","jjlb":"ETF","tzlb":"股票型","ssrq":"2020-01-01","dqgm":"1亿元"}]}]`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("unexpected")), Header: make(http.Header)}, nil
	})
	provider := NewExchangeETFIdentityProvider(doer)
	result := provider.FetchETFIdentities(context.Background())
	require.NoError(t, result.Err)
	assert.Len(t, result.Data, 3)
	assert.Equal(t, map[string]int{"1": 1, "2": 1, "3": 1}, seenPages)
}

func TestEastmoneyFundProviderFetchesEveryRankhandlerPage(t *testing.T) {
	var mutex sync.Mutex
	seenPages := map[string]int{}
	doer := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		page := request.URL.Query().Get("pi")
		mutex.Lock()
		seenPages[page]++
		mutex.Unlock()
		code := map[string]string{"1": "000001", "2": "000002", "3": "000003"}[page]
		body := `var rankData={datas:["` + code + `,基金` + code + `,PY,2026-08-27,1.0,1.0,0.1,0.2,0.3,0.4,0.5,0.6,0,0.7,0.8,1.0,x,x,10"],allRecords:20211,allPages:3,pageIndex:` + page + `};`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	provider := NewEastmoneyFundRankingProvider(doer)
	result := provider.FetchFundRankings(context.Background(), FundRankingQuery{Category: FundCategoryAll, Period: FundPeriodOneYear,
		Q: "000003", SortDirection: SortDescending})
	require.NoError(t, result.Err)
	assert.Len(t, result.Data, 3, "provider must fetch all pages; service performs local q filtering")
	assert.Equal(t, map[string]int{"1": 1, "2": 1, "3": 1}, seenPages)
}

func TestExchangeProviderKeepsSuccessfulSZSEPagesWhenOnePageFails(t *testing.T) {
	doer := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Host, "sse") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"result":[{"fundCode":"510300","fundAbbr":"300ETF","listingDate":"2012-05-28"}]}`)), Header: make(http.Header)}, nil
		}
		if strings.Contains(request.URL.Host, "szse") {
			page := request.URL.Query().Get("PAGENO")
			if page == "2" {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader("limited")), Header: make(http.Header)}, nil
			}
			code := map[string]string{"1": "159001", "3": "159003"}[page]
			body := `[{"metadata":{"pagesize":20,"pagecount":3,"recordcount":3},"data":[{"sys_key":"` + code + `","jjjcurl":"ETF-` + code + `","jjlb":"ETF","tzlb":"股票型","ssrq":"2020-01-01","dqgm":"1亿元"}]}]`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("unexpected")), Header: make(http.Header)}, nil
	})
	provider := NewExchangeETFIdentityProvider(doer)
	result := provider.FetchETFIdentities(context.Background())
	require.Error(t, result.Err)
	assert.Equal(t, "partial", result.Status)
	codes := make([]string, 0, len(result.Data))
	for _, item := range result.Data {
		codes = append(codes, item.Code)
	}
	assert.ElementsMatch(t, []string{"sh510300", "sz159001", "sz159003"}, codes)
}

func TestProductionETFIdentityAndQuoteParsers(t *testing.T) {
	sse, err := parseExchangeETFIdentities([]byte(`{"result":[{"SEC_CODE":"510300","SEC_NAME":"沪深300ETF","INDEX_NAME":"沪深300","LIST_DATE":"2012-05-28","STATUS":"listed"}]}`), "SH")
	require.NoError(t, err)
	require.Len(t, sse, 1)
	assert.Equal(t, "sh510300", sse[0].Code)
	assert.Equal(t, ETFCategoryBroad, sse[0].Category)

	szse, err := parseExchangeETFIdentities([]byte(`[{"data":[{"zqdm":"159915","zqjc":"创业板ETF","ssrq":"2011/09/20"}]}]`), "SZ")
	require.NoError(t, err)
	require.Len(t, szse, 1)
	assert.Equal(t, "sz159915", szse[0].Code)

	parts := make([]string, 87)
	parts[1], parts[3], parts[30], parts[32], parts[35], parts[37], parts[38] = "沪深300ETF", "4.123", "20260828100102", "1.23", "x/10/2835120646", "283512", "2.5"
	parts[44], parts[72] = "1103.25", "23578687700"
	tencent, err := parseTencentETFQuotes([]byte(`v_sh510300="` + strings.Join(parts, "~") + `";`))
	require.NoError(t, err)
	assert.Equal(t, 4.123, *tencent["sh510300"].Price)
	assert.Equal(t, 2835120646.0, *tencent["sh510300"].Amount)
	assert.Equal(t, 110325000000.0, *tencent["sh510300"].Scale)
	assert.Equal(t, 23578687700.0, *tencent["sh510300"].Shares)
	assert.Equal(t, "2026-08-28T10:01:02+08:00", tencent["sh510300"].QuoteTime)

	sina, err := parseSinaETFQuotes([]byte(`var hq_str_sz159915="创业板ETF,2.0,2.1,2.2,2.3,1.9,0,0,1000,200000,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,2026-08-28,10:02:03,00";`))
	require.NoError(t, err)
	assert.InDelta(t, (2.2/2.1-1)*100, *sina["sz159915"].ChangeRate, 0.000001)

	eastmoney, err := parseEastmoneyETFQuotes([]byte(`{"data":{"diff":[{"f12":"510300","f13":1,"f2":4.12,"f3":1.1,"f6":900000,"f8":2.2,"f62":88000,"f124":1787882462}]}}`))
	require.NoError(t, err)
	assert.Equal(t, 88000.0, *eastmoney["sh510300"].NetInflow)
}

func TestProductionETFFundamentalAndHoldingParsers(t *testing.T) {
	identities := []ETFIdentity{{Code: "sh510300", Name: "沪深300ETF", Market: "SH", Listed: true}}
	values, err := parseEastmoneyETFFundamentals([]byte(`{"Data":{"Datas":[{"FCODE":"510300","DWJZ":"3.9","FSRQ":"2026-08-27","PREMIUMRATE":"0.2","TOTALSHARES":"10亿份","JJGM":"39亿元","GMRQ":"2026-06-30"}]}}`), identities)
	require.NoError(t, err)
	require.Contains(t, values, "sh510300")
	assert.Equal(t, 1e9, *values["sh510300"].Shares)
	assert.Equal(t, 3.9e9, *values["sh510300"].Scale)

	holdings := parseEastmoneyETFHoldings(map[string]any{"data": []any{map[string]any{"GPDM": "600519", "GPJC": "贵州茅台", "JZBL": "8.5", "FSRQ": "2026/06/30"}}})
	require.Len(t, holdings, 1)
	assert.Equal(t, 8.5, *holdings[0].Weight)
	assert.Equal(t, "2026-06-30", holdings[0].AsOf)
}

func TestTencentETFQuoteProviderChunksLargeUniverse(t *testing.T) {
	requests := make([]string, 0)
	var requestsMu sync.Mutex
	doer := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		requestsMu.Lock()
		requests = append(requests, request.URL.String())
		requestsMu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	provider := NewTencentETFQuoteProvider(doer)
	identities := make([]ETFIdentity, 0, 161)
	for index := 0; index < 161; index++ {
		identities = append(identities, ETFIdentity{Code: "sh51" + leftPad4(index), Market: "SH", Listed: true})
	}

	provider.FetchETFQuotes(context.Background(), identities)

	assert.Len(t, requests, 3)
	for _, request := range requests {
		codes := strings.Split(strings.SplitN(request, "q=", 2)[1], ",")
		assert.LessOrEqual(t, len(codes), etfQuoteBatchSize)
	}
}

func TestTencentETFQuoteProviderRunsLargeUniverseBatchesConcurrently(t *testing.T) {
	started := make(chan struct{}, etfQuoteBatchWorkers)
	release := make(chan struct{})
	doer := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-release
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	provider := NewTencentETFQuoteProvider(doer)
	identities := make([]ETFIdentity, 0, etfQuoteBatchSize*etfQuoteBatchWorkers)
	for index := 0; index < cap(identities); index++ {
		identities = append(identities, ETFIdentity{Code: "sh51" + leftPad4(index), Market: "SH", Listed: true})
	}
	done := make(chan struct{})
	go func() {
		provider.FetchETFQuotes(context.Background(), identities)
		close(done)
	}()
	for index := 0; index < etfQuoteBatchWorkers; index++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("quote batches did not reach the bounded concurrent worker pool")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent quote batch collection did not complete")
	}
}

func leftPad4(value int) string {
	text := "0000" + strconv.Itoa(value)
	return text[len(text)-4:]
}
