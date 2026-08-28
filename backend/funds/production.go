package funds

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go-stock/backend/data"
	"go-stock/backend/marketdata"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	maxProviderResponseBytes = 32 << 20
	etfQuoteBatchSize        = 80
	etfQuoteBatchWorkers     = 6
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// NewProductionService wires the read-only production source order. It does
// not receive a database handle and cannot mutate fund watchlists, research,
// recommendations, simulated trades, positions, or accounts.
func NewProductionService() *Service {
	client := &http.Client{Timeout: 15 * time.Second}
	return NewService(
		NewEastmoneyFundRankingProvider(client),
		NewSinaFundRankingProvider(client),
		NewExchangeETFIdentityProvider(client),
		[]ETFQuoteProvider{
			NewTencentETFQuoteProvider(client),
			NewSinaETFQuoteProvider(client),
			NewEastmoneyETFQuoteProvider(client),
		},
		NewEastmoneyETFFundamentalsProvider(client),
		NewSinaETFFundamentalsProvider(client),
		UnifiedETFChartProvider{},
	)
}

type EastmoneyFundRankingProvider struct {
	client   HTTPDoer
	endpoint string
}

func NewEastmoneyFundRankingProvider(client HTTPDoer) *EastmoneyFundRankingProvider {
	return &EastmoneyFundRankingProvider{client: defaultHTTPDoer(client), endpoint: "https://fund.eastmoney.com/data/rankhandler.aspx"}
}

func (*EastmoneyFundRankingProvider) Name() string { return "eastmoney" }

func (p *EastmoneyFundRankingProvider) FetchFundRankings(ctx context.Context, query FundRankingQuery) marketdata.ProviderResult[[]FundRankingItem] {
	params := eastmoneyRankhandlerParams(query.Category, query.Period, query.SortDirection)
	body, sourceRef, err := fetchBytes(ctx, p.client, p.endpoint, params, map[string]string{
		"Accept": "application/json, text/plain, */*", "Referer": "https://fund.eastmoney.com/data/fundranking.html",
	})
	if err != nil {
		return marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusUnavailable, Data: []FundRankingItem{}, SourceRef: sourceRef, Err: err}
	}
	items, err := parseEastmoneyFundRankings(body)
	if err != nil {
		return marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusUnavailable, Data: []FundRankingItem{}, SourceRef: sourceRef, Err: err}
	}
	refs := []string{sourceRef}
	pageCount, recordCount := rankhandlerMetadata(body)
	pageSize, _ := strconv.Atoi(params.Get("pn"))
	if pageCount < 1 && pageSize > 0 && recordCount > 0 {
		pageCount = (recordCount + pageSize - 1) / pageSize
	}
	if pageCount < 1 {
		pageCount = 1
	}
	if pageCount > 100 {
		pageCount = 100
	}
	if pageCount > 1 {
		type pageResult struct {
			items []FundRankingItem
			ref   string
			err   error
		}
		results := make(chan pageResult, pageCount-1)
		workerCount := 4
		if pageCount-1 < workerCount {
			workerCount = pageCount - 1
		}
		pages := make(chan int)
		var workers sync.WaitGroup
		for worker := 0; worker < workerCount; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for page := range pages {
					pageParams := cloneURLValues(params)
					pageParams.Set("pi", strconv.Itoa(page))
					pageBody, pageRef, pageErr := fetchBytes(ctx, p.client, p.endpoint, pageParams, map[string]string{
						"Accept": "application/json, text/plain, */*", "Referer": "https://fund.eastmoney.com/data/fundranking.html",
					})
					pageItems := []FundRankingItem{}
					if pageErr == nil {
						pageItems, pageErr = parseEastmoneyFundRankings(pageBody)
					}
					results <- pageResult{items: pageItems, ref: pageRef, err: pageErr}
				}
			}()
		}
		go func() {
			for page := 2; page <= pageCount; page++ {
				pages <- page
			}
			close(pages)
			workers.Wait()
			close(results)
		}()
		pageErrors := make([]error, 0)
		for result := range results {
			refs = append(refs, result.ref)
			items = append(items, result.items...)
			if result.err != nil {
				pageErrors = append(pageErrors, result.err)
			}
		}
		err = errorsJoin(pageErrors)
	}
	if query.Category != FundCategoryAll {
		for index := range items {
			items[index].Category = query.Category
		}
	}
	sort.Strings(refs)
	result := fundProviderResult(items, strings.Join(refs, ","))
	if err != nil {
		result.Err = err
		if len(items) > 0 {
			result.Status = marketdata.StatusPartial
		} else {
			result.Status = marketdata.StatusUnavailable
		}
	}
	return result
}

type SinaFundRankingProvider struct {
	client   HTTPDoer
	endpoint string
}

func NewSinaFundRankingProvider(client HTTPDoer) *SinaFundRankingProvider {
	return &SinaFundRankingProvider{client: defaultHTTPDoer(client), endpoint: "https://vip.stock.finance.sina.com.cn/fund_center/data/jsonp.php/IO.XSRV2.CallbackList/NetValueReturn_Service.NetValueReturnOpen"}
}

func (*SinaFundRankingProvider) Name() string { return "sina_fund" }

func (p *SinaFundRankingProvider) FetchFundRankings(ctx context.Context, query FundRankingQuery) marketdata.ProviderResult[[]FundRankingItem] {
	params := url.Values{"page": {"1"}, "num": {"10000"}, "sort": {sinaFundSort(query.Period)}, "asc": {strconv.FormatBool(query.SortDirection == SortAscending)}}
	body, sourceRef, err := fetchBytes(ctx, p.client, p.endpoint, params, map[string]string{
		"Accept": "application/javascript, application/json, text/plain, */*", "Referer": "https://finance.sina.com.cn/fund/",
	})
	if err != nil {
		return marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusUnavailable, Data: []FundRankingItem{}, SourceRef: sourceRef, Err: err}
	}
	items, err := parseSinaFundRankings(body)
	if err != nil {
		return marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusUnavailable, Data: []FundRankingItem{}, SourceRef: sourceRef, Err: err}
	}
	return fundProviderResult(items, sourceRef)
}

type ExchangeETFIdentityProvider struct {
	client            HTTPDoer
	sseEndpoint       string
	szseEndpoint      string
	eastmoneyEndpoint string
}

func NewExchangeETFIdentityProvider(client HTTPDoer) *ExchangeETFIdentityProvider {
	return &ExchangeETFIdentityProvider{
		client:            defaultHTTPDoer(client),
		sseEndpoint:       "https://query.sse.com.cn/commonSoaQuery.do",
		szseEndpoint:      "https://www.szse.cn/api/report/ShowReport/data",
		eastmoneyEndpoint: "https://push2.eastmoney.com/api/qt/clist/get",
	}
}

func (*ExchangeETFIdentityProvider) Name() string { return "sse+szse" }

func (p *ExchangeETFIdentityProvider) FetchETFIdentities(ctx context.Context) marketdata.ProviderResult[[]ETFIdentity] {
	type exchangeCall struct {
		name     string
		endpoint string
		params   url.Values
		headers  map[string]string
		market   string
	}
	calls := []exchangeCall{
		{name: "sse", endpoint: p.sseEndpoint, params: url.Values{
			"isPagination": {"true"}, "sqlId": {"FUND_LIST"}, "fundType": {"00"},
			"subClass": {"01,02,03,04,06,08,09,31,32,33,34,35,36,37,38"}, "pageHelp.pageSize": {"2000"},
			"pageHelp.pageNo": {"1"}, "pageHelp.beginPage": {"1"}, "pageHelp.endPage": {"1"},
		}, headers: map[string]string{"Referer": "https://www.sse.com.cn/assortment/fund/etf/list/"}, market: "SH"},
		{name: "szse", endpoint: p.szseEndpoint, params: url.Values{
			"SHOWTYPE": {"JSON"}, "CATALOGID": {"1105"}, "TABKEY": {"tab1"}, "selectJjlb": {"ETF"}, "PAGENO": {"1"},
		}, headers: map[string]string{"Referer": "https://www.szse.cn/market/product/list/etfList/index.html"}, market: "SZ"},
	}
	type exchangeResult struct {
		name, sourceRef, market string
		items                   []ETFIdentity
		err                     error
	}
	results := make(chan exchangeResult, len(calls))
	for _, call := range calls {
		call := call
		go func() {
			if call.name == "szse" {
				items, refs, err := p.fetchSZSEIdentities(ctx, call.endpoint, call.params, call.headers)
				results <- exchangeResult{name: call.name, sourceRef: strings.Join(refs, ","), market: call.market, items: items, err: err}
				return
			}
			body, sourceRef, err := fetchBytes(ctx, p.client, call.endpoint, call.params, call.headers)
			items := []ETFIdentity{}
			if err == nil {
				items, err = parseExchangeETFIdentities(body, call.market)
			}
			results <- exchangeResult{name: call.name, sourceRef: sourceRef, market: call.market, items: items, err: err}
		}()
	}
	items := make([]ETFIdentity, 0, 1000)
	refs := make([]string, 0, 3)
	errorsOut := make([]error, 0, 2)
	for range calls {
		result := <-results
		refs = append(refs, result.sourceRef)
		items = append(items, result.items...)
		if result.err != nil {
			errorsOut = append(errorsOut, fmt.Errorf("%s: %w", result.name, result.err))
			continue
		}
	}
	// Exchange identity is authoritative. Eastmoney is only used when both
	// exchange lists are unavailable/empty, and the degraded status remains
	// visible to callers.
	if len(items) == 0 {
		params := url.Values{"pn": {"2000"}, "pz": {"2000"}, "po": {"1"}, "np": {"1"}, "fltt": {"2"},
			"fid": {"f12"}, "fs": {"b:MK0021,b:MK0022,b:MK0023,b:MK0024"}, "fields": {"f12,f13,f14"}}
		body, sourceRef, err := fetchBytes(ctx, p.client, p.eastmoneyEndpoint, params, map[string]string{"Referer": "https://quote.eastmoney.com/center/gridlist.html"})
		refs = append(refs, sourceRef)
		if err == nil {
			fallback, parseErr := parseEastmoneyETFIdentities(body)
			if parseErr == nil {
				items = fallback
			} else {
				errorsOut = append(errorsOut, fmt.Errorf("eastmoney identity fallback: %w", parseErr))
			}
		} else {
			errorsOut = append(errorsOut, fmt.Errorf("eastmoney identity fallback: %w", err))
		}
	}
	status := marketdata.StatusOK
	var resultErr error
	warning := ""
	if len(errorsOut) > 0 {
		resultErr = errorsJoin(errorsOut)
		warning = resultErr.Error()
		if len(items) > 0 {
			status = marketdata.StatusPartial
		} else {
			status = marketdata.StatusUnavailable
		}
	} else if len(items) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.ProviderResult[[]ETFIdentity]{Status: status, Data: items, AsOf: latestIdentityDate(items), SourceRef: strings.Join(refs, ","), Warning: warning, Err: resultErr}
}

func (p *ExchangeETFIdentityProvider) fetchSZSEIdentities(ctx context.Context, endpoint string, baseParams url.Values, headers map[string]string) ([]ETFIdentity, []string, error) {
	firstParams := cloneURLValues(baseParams)
	firstParams.Set("PAGENO", "1")
	firstBody, firstRef, err := fetchBytes(ctx, p.client, endpoint, firstParams, headers)
	if err != nil {
		return nil, []string{firstRef}, err
	}
	items, err := parseExchangeETFIdentities(firstBody, "SZ")
	if err != nil {
		return nil, []string{firstRef}, err
	}
	pageCount := szsePageCount(firstBody)
	if pageCount <= 1 {
		return items, []string{firstRef}, nil
	}

	type pageResult struct {
		items []ETFIdentity
		ref   string
		err   error
	}
	workerCount := 6
	if pageCount-1 < workerCount {
		workerCount = pageCount - 1
	}
	pages := make(chan int)
	results := make(chan pageResult, pageCount-1)
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for page := range pages {
				params := cloneURLValues(baseParams)
				params.Set("PAGENO", strconv.Itoa(page))
				body, ref, fetchErr := fetchBytes(ctx, p.client, endpoint, params, headers)
				parsed := []ETFIdentity{}
				if fetchErr == nil {
					parsed, fetchErr = parseExchangeETFIdentities(body, "SZ")
				}
				results <- pageResult{items: parsed, ref: ref, err: fetchErr}
			}
		}()
	}
	go func() {
		for page := 2; page <= pageCount; page++ {
			pages <- page
		}
		close(pages)
		workers.Wait()
		close(results)
	}()
	refs := []string{firstRef}
	errorsOut := make([]error, 0)
	for result := range results {
		refs = append(refs, result.ref)
		if result.err != nil {
			errorsOut = append(errorsOut, result.err)
			continue
		}
		items = append(items, result.items...)
	}
	sort.Strings(refs)
	return items, refs, errorsJoin(errorsOut)
}

type TencentETFQuoteProvider struct {
	client   HTTPDoer
	endpoint string
}

func NewTencentETFQuoteProvider(client HTTPDoer) *TencentETFQuoteProvider {
	return &TencentETFQuoteProvider{client: defaultHTTPDoer(client), endpoint: "https://qt.gtimg.cn/q="}
}

func (*TencentETFQuoteProvider) Name() string { return "tencent" }

func (p *TencentETFQuoteProvider) FetchETFQuotes(ctx context.Context, identities []ETFIdentity) marketdata.ProviderResult[map[string]ETFQuote] {
	codes := identityCodes(identities)
	if len(codes) == 0 {
		return marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusEmpty, Data: map[string]ETFQuote{}}
	}
	values, refs, batchErr := collectQuoteBatches(ctx, stringBatches(codes, etfQuoteBatchSize), func(batch []string) (map[string]ETFQuote, string, error) {
		body, sourceRef, err := fetchBytes(ctx, p.client, p.endpoint+strings.Join(batch, ","), nil, map[string]string{"Referer": "https://gu.qq.com/"})
		if err != nil {
			return nil, sourceRef, err
		}
		parsed, parseErr := parseTencentETFQuotes(decodeGBKIfNeeded(body))
		return parsed, sourceRef, parseErr
	})
	return quoteProviderResult(values, strings.Join(refs, ","), batchErr)
}

type SinaETFQuoteProvider struct {
	client   HTTPDoer
	endpoint string
}

func NewSinaETFQuoteProvider(client HTTPDoer) *SinaETFQuoteProvider {
	return &SinaETFQuoteProvider{client: defaultHTTPDoer(client), endpoint: "https://hq.sinajs.cn/list="}
}

func (*SinaETFQuoteProvider) Name() string { return "sina" }

func (p *SinaETFQuoteProvider) FetchETFQuotes(ctx context.Context, identities []ETFIdentity) marketdata.ProviderResult[map[string]ETFQuote] {
	codes := identityCodes(identities)
	if len(codes) == 0 {
		return marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusEmpty, Data: map[string]ETFQuote{}}
	}
	values, refs, batchErr := collectQuoteBatches(ctx, stringBatches(codes, etfQuoteBatchSize), func(batch []string) (map[string]ETFQuote, string, error) {
		body, sourceRef, err := fetchBytes(ctx, p.client, p.endpoint+strings.Join(batch, ","), nil, map[string]string{"Referer": "https://finance.sina.com.cn/"})
		if err != nil {
			return nil, sourceRef, err
		}
		parsed, parseErr := parseSinaETFQuotes(decodeGBKIfNeeded(body))
		return parsed, sourceRef, parseErr
	})
	return quoteProviderResult(values, strings.Join(refs, ","), batchErr)
}

type EastmoneyETFQuoteProvider struct {
	client   HTTPDoer
	endpoint string
}

func NewEastmoneyETFQuoteProvider(client HTTPDoer) *EastmoneyETFQuoteProvider {
	return &EastmoneyETFQuoteProvider{client: defaultHTTPDoer(client), endpoint: "https://push2.eastmoney.com/api/qt/ulist.np/get"}
}

func (*EastmoneyETFQuoteProvider) Name() string { return "eastmoney" }

func (p *EastmoneyETFQuoteProvider) FetchETFQuotes(ctx context.Context, identities []ETFIdentity) marketdata.ProviderResult[map[string]ETFQuote] {
	secids := make([]string, 0, len(identities))
	for _, identity := range identities {
		marketID := "0"
		if identity.Market == "SH" || strings.HasPrefix(identity.Code, "sh") {
			marketID = "1"
		}
		secids = append(secids, marketID+"."+identity.Code[2:])
	}
	if len(secids) == 0 {
		return marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusEmpty, Data: map[string]ETFQuote{}}
	}
	values, refs, batchErr := collectQuoteBatches(ctx, stringBatches(secids, etfQuoteBatchSize), func(batch []string) (map[string]ETFQuote, string, error) {
		params := url.Values{"secids": {strings.Join(batch, ",")}, "fltt": {"2"}, "invt": {"2"}, "fields": {"f12,f13,f14,f2,f3,f6,f8,f62,f124"}}
		body, sourceRef, err := fetchBytes(ctx, p.client, p.endpoint, params, map[string]string{"Referer": "https://quote.eastmoney.com/"})
		if err != nil {
			return nil, sourceRef, err
		}
		parsed, parseErr := parseEastmoneyETFQuotes(body)
		return parsed, sourceRef, parseErr
	})
	return quoteProviderResult(values, strings.Join(refs, ","), batchErr)
}

type quoteBatchFetch func([]string) (map[string]ETFQuote, string, error)

type quoteBatchResult struct {
	values map[string]ETFQuote
	ref    string
	err    error
}

func collectQuoteBatches(ctx context.Context, batches [][]string, fetch quoteBatchFetch) (map[string]ETFQuote, []string, error) {
	values := map[string]ETFQuote{}
	if len(batches) == 0 {
		return values, []string{}, nil
	}
	workerCount := minIntValue(etfQuoteBatchWorkers, len(batches))
	tasks := make(chan []string)
	results := make(chan quoteBatchResult, len(batches))
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for batch := range tasks {
				if err := ctx.Err(); err != nil {
					results <- quoteBatchResult{err: err}
					continue
				}
				batchValues, ref, err := fetch(batch)
				results <- quoteBatchResult{values: batchValues, ref: ref, err: err}
			}
		}()
	}
	go func() {
		for _, batch := range batches {
			tasks <- batch
		}
		close(tasks)
		workers.Wait()
		close(results)
	}()
	refs := make([]string, 0, len(batches))
	errorsOut := make([]error, 0)
	for result := range results {
		if result.ref != "" {
			refs = append(refs, result.ref)
		}
		if result.err != nil {
			errorsOut = append(errorsOut, result.err)
			continue
		}
		mergeETFQuotes(values, result.values)
	}
	sort.Strings(refs)
	return values, refs, errorsJoin(errorsOut)
}

type EastmoneyETFFundamentalsProvider struct {
	client          HTTPDoer
	rankingEndpoint string
	basicEndpoint   string
	holdingEndpoint string
}

func NewEastmoneyETFFundamentalsProvider(client HTTPDoer) *EastmoneyETFFundamentalsProvider {
	return &EastmoneyETFFundamentalsProvider{client: defaultHTTPDoer(client),
		rankingEndpoint: "https://fund.eastmoney.com/ETFN_jzzzl.html",
		basicEndpoint:   "https://fundf10.eastmoney.com/jbgk_%s.html",
		holdingEndpoint: "https://fundmobapi.eastmoney.com/FundMNewApi/FundMNInverstPosition"}
}

func (*EastmoneyETFFundamentalsProvider) Name() string { return "eastmoney_fund" }

func (p *EastmoneyETFFundamentalsProvider) FetchETFFundamentals(ctx context.Context, identities []ETFIdentity) marketdata.ProviderResult[map[string]ETFFundamentals] {
	if len(identities) == 0 {
		return marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusEmpty, Data: map[string]ETFFundamentals{}}
	}
	body, sourceRef, err := fetchBytes(ctx, p.client, p.rankingEndpoint, nil, map[string]string{"Referer": "https://fund.eastmoney.com/"})
	values := map[string]ETFFundamentals{}
	if err == nil {
		values, err = parseEastmoneyETFNetValueHTML(decodeGBKIfNeeded(body), identities)
	}
	refs := []string{sourceRef}
	// Holdings are a detail-only cost: never fan out one request per ETF on a
	// rankings page.
	if len(identities) == 1 {
		code := identities[0].Code[2:]
		basicBody, basicRef, basicErr := fetchBytes(ctx, p.client, fmt.Sprintf(p.basicEndpoint, code), nil, map[string]string{"Referer": "https://fund.eastmoney.com/"})
		refs = append(refs, basicRef)
		if basicErr == nil {
			var basic ETFFundamentals
			basic, basicErr = parseEastmoneyETFBasicHTML(decodeGBKIfNeeded(basicBody), identities[0])
			if basicErr == nil {
				mergeETFFundamentals(values, map[string]ETFFundamentals{basic.Code: basic})
			}
		} else if err == nil {
			err = basicErr
		}
		if basicErr != nil && err == nil {
			err = basicErr
		}
		holdingBody, holdingRef, holdingErr := fetchBytes(ctx, p.client, p.holdingEndpoint, url.Values{"FCODE": {code}, "pageIndex": {"1"}, "pageSize": {"10"}}, map[string]string{"Referer": "https://fund.eastmoney.com/"})
		refs = append(refs, holdingRef)
		if holdingErr == nil {
			var holdings []ETFHolding
			holdings, holdingErr = parseEastmoneyETFHoldingsBody(holdingBody)
			if holdingErr == nil {
				current := values[identities[0].Code]
				current.Code = identities[0].Code
				current.Holdings = holdings
				values[identities[0].Code] = current
			}
		}
		if holdingErr != nil && err == nil {
			err = holdingErr
		}
	}
	return fundamentalsProviderResult(values, strings.Join(refs, ","), err)
}

type SinaETFFundamentalsProvider struct {
	client   HTTPDoer
	endpoint string
}

func NewSinaETFFundamentalsProvider(client HTTPDoer) *SinaETFFundamentalsProvider {
	return &SinaETFFundamentalsProvider{client: defaultHTTPDoer(client), endpoint: "https://vip.stock.finance.sina.com.cn/fund_center/data/jsonp.php/IO.XSRV2.CallbackList/NetValueReturn_Service.NetValueReturnOpen"}
}

func (*SinaETFFundamentalsProvider) Name() string { return "sina_fund" }

func (p *SinaETFFundamentalsProvider) FetchETFFundamentals(ctx context.Context, identities []ETFIdentity) marketdata.ProviderResult[map[string]ETFFundamentals] {
	if len(identities) == 0 {
		return marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusEmpty, Data: map[string]ETFFundamentals{}}
	}
	params := url.Values{"page": {"1"}, "num": {"10000"}, "sort": {"zmjzz"}, "asc": {"0"}}
	body, sourceRef, err := fetchBytes(ctx, p.client, p.endpoint, params, map[string]string{"Referer": "https://finance.sina.com.cn/fund/"})
	if err != nil {
		return marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusUnavailable, Data: map[string]ETFFundamentals{}, SourceRef: sourceRef, Err: err}
	}
	values, err := parseSinaETFFundamentals(body, identities)
	return fundamentalsProviderResult(values, sourceRef, err)
}

// UnifiedETFChartProvider points ETF details to the unified 2.1 chart service.
// That service already enforces ETF adjustment=none and the Tencent -> Sina ->
// Eastmoney provider chain; duplicating K-line fetching here would split cache
// and provenance semantics.
type UnifiedETFChartProvider struct{}

func (UnifiedETFChartProvider) Name() string { return "unified_chart" }
func (UnifiedETFChartProvider) ResolveETFChart(_ context.Context, identity ETFIdentity) marketdata.ProviderResult[data.InstrumentID] {
	instrument, err := data.ParseInstrumentID(identity.Code, "etf", identity.Market)
	if err != nil {
		return marketdata.ProviderResult[data.InstrumentID]{Status: marketdata.StatusUnavailable, Err: err}
	}
	return marketdata.ProviderResult[data.InstrumentID]{Status: marketdata.StatusOK, Data: instrument,
		SourceRef: "/api/v1/instruments/" + instrument.Code + "/chart?assetType=etf&market=" + instrument.Market + "&adjustment=none"}
}

func defaultHTTPDoer(client HTTPDoer) HTTPDoer {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func cloneURLValues(values url.Values) url.Values {
	result := make(url.Values, len(values))
	for key, items := range values {
		result[key] = append([]string(nil), items...)
	}
	return result
}

func eastmoneyRankhandlerParams(category FundCategory, period FundPeriod, direction SortDirection) url.Values {
	now := time.Now().In(shanghaiLocation())
	return url.Values{
		"op": {"ph"}, "dt": {"kf"}, "ft": {eastmoneyFundType(category)}, "rs": {""}, "gs": {"0"},
		"sc": {eastmoneyFundSort(period)}, "st": {string(direction)}, "sd": {now.AddDate(-3, 0, 0).Format(time.DateOnly)},
		"ed": {now.Format(time.DateOnly)}, "qdii": {""}, "tabSubtype": {",,,,,"}, "pi": {"1"}, "pn": {"10000"},
		"dx": {"1"}, "v": {strconv.FormatInt(now.UnixMilli(), 10)},
	}
}

func fetchBytes(ctx context.Context, client HTTPDoer, endpoint string, params url.Values, headers map[string]string) ([]byte, string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, "", fmt.Errorf("provider endpoint is empty")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, endpoint, err
	}
	if len(params) > 0 {
		query := parsed.Query()
		for key, values := range params {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		parsed.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, parsed.String(), err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124 Safari/537.36")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, parsed.String(), err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, parsed.String(), &ProviderHTTPError{StatusCode: response.StatusCode, Message: strings.TrimSpace(string(message))}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return nil, parsed.String(), err
	}
	if len(body) > maxProviderResponseBytes {
		return nil, parsed.String(), fmt.Errorf("provider response exceeds %d bytes", maxProviderResponseBytes)
	}
	return body, parsed.String(), nil
}

func fundProviderResult(items []FundRankingItem, sourceRef string) marketdata.ProviderResult[[]FundRankingItem] {
	status := marketdata.StatusOK
	if len(items) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.ProviderResult[[]FundRankingItem]{Status: status, Data: items, AsOf: fundItemsAsOf(items), SourceRef: sourceRef}
}

func quoteProviderResult(values map[string]ETFQuote, sourceRef string, err error) marketdata.ProviderResult[map[string]ETFQuote] {
	if values == nil {
		values = map[string]ETFQuote{}
	}
	status := marketdata.StatusOK
	if err != nil {
		if len(values) > 0 {
			status = marketdata.StatusPartial
		} else {
			status = marketdata.StatusUnavailable
		}
	} else if len(values) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.ProviderResult[map[string]ETFQuote]{Status: status, Data: values, AsOf: quoteValuesAsOf(values), SourceRef: sourceRef, Err: err}
}

func fundamentalsProviderResult(values map[string]ETFFundamentals, sourceRef string, err error) marketdata.ProviderResult[map[string]ETFFundamentals] {
	if values == nil {
		values = map[string]ETFFundamentals{}
	}
	status := marketdata.StatusOK
	if err != nil {
		if len(values) > 0 {
			status = marketdata.StatusPartial
		} else {
			status = marketdata.StatusUnavailable
		}
	} else if len(values) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.ProviderResult[map[string]ETFFundamentals]{Status: status, Data: values, AsOf: fundamentalValuesAsOf(values), SourceRef: sourceRef, Err: err}
}

func identityCodes(values []ETFIdentity) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if canonical, ok := data.NormalizeETFCode(value.Code); ok {
			result = append(result, canonical)
		}
	}
	return result
}

func stringBatches(values []string, size int) [][]string {
	if size < 1 {
		size = 1
	}
	result := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		result = append(result, values[start:end])
	}
	return result
}

func decodeGBKIfNeeded(body []byte) []byte {
	if json.Valid(body) || utf8.Valid(body) {
		return body
	}
	decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(body), simplifiedchinese.GBK.NewDecoder()))
	if err == nil {
		return decoded
	}
	return body
}

func bestEffortJSON(body []byte) any {
	trimmed := unwrapJSONP(body)
	var value any
	if json.Unmarshal(trimmed, &value) == nil {
		return value
	}
	return nil
}

func errorsJoin(values []error) error {
	messages := make([]string, 0, len(values))
	for _, value := range values {
		if value != nil {
			messages = append(messages, value.Error())
		}
	}
	if len(messages) == 0 {
		return nil
	}
	sort.Strings(messages)
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}
