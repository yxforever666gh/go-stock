package funds

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/instruments"
	"go-stock/backend/marketdata"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubFundProvider struct {
	name   string
	result marketdata.ProviderResult[[]FundRankingItem]
	calls  int
}

func (p *stubFundProvider) Name() string { return p.name }
func (p *stubFundProvider) FetchFundRankings(context.Context, FundRankingQuery) marketdata.ProviderResult[[]FundRankingItem] {
	p.calls++
	return p.result
}

type stubIdentityProvider struct {
	result marketdata.ProviderResult[[]ETFIdentity]
	calls  int
}

func (p *stubIdentityProvider) Name() string { return "exchange" }
func (p *stubIdentityProvider) FetchETFIdentities(context.Context) marketdata.ProviderResult[[]ETFIdentity] {
	p.calls++
	return p.result
}

type stubQuoteProvider struct {
	name      string
	result    marketdata.ProviderResult[map[string]ETFQuote]
	calls     int
	requested [][]ETFIdentity
}

func (p *stubQuoteProvider) Name() string { return p.name }
func (p *stubQuoteProvider) FetchETFQuotes(_ context.Context, values []ETFIdentity) marketdata.ProviderResult[map[string]ETFQuote] {
	p.calls++
	p.requested = append(p.requested, append([]ETFIdentity(nil), values...))
	return p.result
}

type stubFundamentalsProvider struct {
	name      string
	result    marketdata.ProviderResult[map[string]ETFFundamentals]
	calls     int
	requested [][]ETFIdentity
}

func (p *stubFundamentalsProvider) Name() string { return p.name }
func (p *stubFundamentalsProvider) FetchETFFundamentals(_ context.Context, values []ETFIdentity) marketdata.ProviderResult[map[string]ETFFundamentals] {
	p.calls++
	p.requested = append(p.requested, append([]ETFIdentity(nil), values...))
	return p.result
}

type stubChartProvider struct {
	result marketdata.ProviderResult[instruments.InstrumentID]
	calls  int
}

func (p *stubChartProvider) Name() string { return "unified-chart" }
func (p *stubChartProvider) ResolveETFChart(context.Context, ETFIdentity) marketdata.ProviderResult[instruments.InstrumentID] {
	p.calls++
	return p.result
}

func TestFundRankingsFallbackDeduplicatesNormalizesDatesAndPaginates(t *testing.T) {
	primary := &stubFundProvider{name: "eastmoney", result: marketdata.ProviderResult[[]FundRankingItem]{
		Status: marketdata.StatusPartial, AsOf: mustTime(t, "2026-08-27"), Err: context.DeadlineExceeded,
		Data: []FundRankingItem{
			{Code: "001001", Name: "价值混合", Category: FundCategoryMixed, NAV: fp(1.2), NAVDate: "2026/08/27", OneYearReturn: fp(12)},
			{Code: "001001", Name: "价值混合", Scale: fp(33), ScaleDate: "20260826"},
			{Code: "001002", Name: "成长股票", Category: FundCategoryStock, NAV: fp(2), NAVDate: "2026-08-27", OneYearReturn: fp(20)},
		}}}
	fallback := &stubFundProvider{name: "sina_fund", result: marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusOK,
		AsOf: mustTime(t, "2026-08-27"), Data: []FundRankingItem{
			{Code: "001001", Name: "价值混合", WeekReturn: fp(3), OneYearReturn: fp(99)},
			{Code: "001003", Name: "海外QDII", Category: FundCategoryQDII, NAV: fp(1.5), NAVDate: "2026.08.26", OneYearReturn: fp(8)},
		}}}
	service := NewService(primary, fallback, nil, nil, nil, nil, nil)
	service.now = func() time.Time { return mustTime(t, "2026-08-28") }

	response := service.FundRankings(context.Background(), FundRankingQuery{Category: FundCategoryAll, Period: FundPeriodOneYear,
		SortDirection: SortDescending, Page: 1, PageSize: 2})

	require.Equal(t, marketdata.StatusPartial, response.Status)
	assert.Equal(t, "eastmoney+sina_fund", response.Source)
	assert.Len(t, response.Data.Items, 2)
	assert.Equal(t, 3, response.Data.Total)
	assert.Equal(t, "001002", response.Data.Items[0].Code)
	assert.Equal(t, 1, response.Data.Items[0].Rank)
	assert.Equal(t, "001001", response.Data.Items[1].Code)
	assert.Equal(t, "2026-08-27", response.Data.Items[1].NAVDate)
	assert.Equal(t, 12.0, *response.Data.Items[1].OneYearReturn, "primary non-null value must win")
	assert.Equal(t, 3.0, *response.Data.Items[1].WeekReturn, "fallback fills missing fields")
	assert.Equal(t, "2026-08-27", response.Data.NAVDate)
	assert.Equal(t, "timeout", response.Errors[0].Code)
	assert.Equal(t, 1, fallback.calls)
}

func TestFundRankingsDoesNotCallFallbackForCompletePrimaryAndSearchesServerSide(t *testing.T) {
	primary := &stubFundProvider{name: "eastmoney", result: marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusOK,
		Data: []FundRankingItem{{Code: "001001", Name: "价值混合", OneYearReturn: fp(1)}, {Code: "001002", Name: "成长股票", OneYearReturn: fp(2)}}}}
	fallback := &stubFundProvider{name: "sina_fund", result: marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusOK}}
	service := NewService(primary, fallback, nil, nil, nil, nil, nil)

	response := service.FundRankings(context.Background(), FundRankingQuery{Category: FundCategoryAll, Period: FundPeriodOneYear,
		Q: "001002", Page: 1, PageSize: 20})

	require.Equal(t, marketdata.StatusOK, response.Status)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, "001002", response.Data.Items[0].Code)
	assert.Zero(t, fallback.calls)
}

func TestFundRankingsReportsRateLimitAndEmptyFallback(t *testing.T) {
	primary := &stubFundProvider{name: "eastmoney", result: marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusUnavailable,
		Err: &ProviderHTTPError{StatusCode: 429, Message: "slow down"}}}
	fallback := &stubFundProvider{name: "sina_fund", result: marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusEmpty}}
	service := NewService(primary, fallback, nil, nil, nil, nil, nil)

	response := service.FundRankings(context.Background(), FundRankingQuery{})

	assert.Equal(t, marketdata.StatusUnavailable, response.Status)
	require.Len(t, response.Errors, 2)
	assert.Equal(t, "rate_limited", response.Errors[0].Code)
	assert.Equal(t, "empty_data", response.Errors[1].Code)
}

func TestETFRankingsUsesExchangeIdentityAndFieldLevelQuoteFundFallback(t *testing.T) {
	identity := &stubIdentityProvider{result: marketdata.ProviderResult[[]ETFIdentity]{Status: marketdata.StatusOK,
		Data: []ETFIdentity{
			{Code: "510300", Name: "沪深300ETF", Category: ETFCategoryBroad, ListDate: "2012/05/28", Listed: true},
			{Code: "sh510300", Name: "duplicate", Listed: true},
			{Code: "159915", Name: "创业板ETF", Category: ETFCategoryBroad, ListDate: "2011-09-20", Listed: true},
			{Code: "513100", Name: "纳指ETF", Category: ETFCategoryCrossBorder, ListDate: "2013.04.25", Listed: true},
		}}}
	tencent := &stubQuoteProvider{name: "tencent", result: marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusPartial,
		Data: map[string]ETFQuote{
			"510300": {Code: "510300", Price: fp(4), ChangeRate: fp(1), Amount: fp(300), TurnoverRate: fp(2), NetInflow: fp(30), QuoteTime: "2026/08/28 10:00:00"},
			"159915": {Code: "159915", Price: fp(2), Amount: fp(200), QuoteTime: "2026-08-28 10:00:00"},
		}}}
	sina := &stubQuoteProvider{name: "sina", result: marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusOK,
		Data: map[string]ETFQuote{
			"159915": {Code: "159915", ChangeRate: fp(2), TurnoverRate: fp(3), Amount: fp(199), NetInflow: fp(19), QuoteTime: "2026-08-28 10:01:00"},
			"513100": {Code: "513100", Price: fp(1.5), ChangeRate: fp(3), Amount: fp(100), TurnoverRate: fp(1), NetInflow: fp(10), QuoteTime: "20260828100100"},
		}}}
	eastFund := &stubFundamentalsProvider{name: "eastmoney", result: marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusPartial,
		Data: map[string]ETFFundamentals{"510300": {Code: "510300", NAV: fp(3.9), NAVDate: "20260827", Shares: fp(250), Scale: fp(1000)}}}}
	sinaFund := &stubFundamentalsProvider{name: "sina_fund", result: marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusOK,
		Data: map[string]ETFFundamentals{
			"159915": {Code: "159915", NAV: fp(2), NAVDate: "2026/08/27", Shares: fp(250), Scale: fp(500)},
			"513100": {Code: "513100", NAV: fp(1.4), NAVDate: "2026-08-27", Shares: fp(130), Scale: fp(200)},
		}}}
	service := NewService(nil, nil, identity, []ETFQuoteProvider{tencent, sina}, eastFund, sinaFund, nil)

	response := service.ETFRankings(context.Background(), ETFQuery{Category: ETFCategoryAll, Sort: ETFSortChangeRate,
		SortDirection: SortDescending, Page: 1, PageSize: 10})

	require.Equal(t, marketdata.StatusPartial, response.Status)
	assert.Equal(t, 3, response.Data.Total, "duplicate exchange identity must be removed")
	assert.Equal(t, ETFSortChangeRate, response.Data.Sort)
	require.Len(t, response.Data.Items, 3)
	assert.Equal(t, "sh513100", response.Data.Items[0].Code)
	assert.Equal(t, "2026-08-28T10:01:00+08:00", response.Data.Items[0].QuoteTime)
	assert.Equal(t, "2026-08-27", response.Data.Items[0].NAVDate)
	assert.InDelta(t, (1.5/1.4-1)*100, *response.Data.Items[0].PremiumRate, 0.000001)
	assert.Len(t, sina.requested[0], 2, "fallback quote source only receives incomplete identities")
	assert.Len(t, sinaFund.requested[0], 2, "fallback fund source only receives missing identities")
}

func TestETFDetailCutAcrossProvidersAndChartInstrument(t *testing.T) {
	identity := &stubIdentityProvider{result: marketdata.ProviderResult[[]ETFIdentity]{Status: marketdata.StatusOK,
		Data: []ETFIdentity{{Code: "510300", Name: "沪深300ETF", Category: ETFCategoryBroad, TrackingIndex: "沪深300",
			ManagementFee: fp(0.15), ListDate: "2012-05-28", Listed: true}}}}
	quote := &stubQuoteProvider{name: "tencent", result: marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusOK,
		Data: map[string]ETFQuote{"510300": {Code: "510300", Price: fp(4), ChangeRate: fp(1), Amount: fp(1e9), TurnoverRate: fp(2), NetInflow: fp(1e7), Shares: fp(250), Scale: fp(1000), QuoteTime: "2026-08-28 10:00:00"}}}}
	fund := &stubFundamentalsProvider{name: "eastmoney", result: marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusOK,
		Data: map[string]ETFFundamentals{"510300": {Code: "510300", NAV: fp(3.9), NAVDate: "2026-08-27", Shares: fp(250), Scale: fp(1000),
			Holdings: []ETFHolding{{Code: "600519", Name: "贵州茅台", Weight: fp(5), AsOf: "2026/06/30"},
				{Code: "600519", Name: "duplicate", Weight: fp(4)}, {Code: "601318", Name: "中国平安", Weight: fp(3), AsOf: "20260630"}}}}}}
	chart := &stubChartProvider{result: marketdata.ProviderResult[instruments.InstrumentID]{Status: marketdata.StatusOK,
		Data: instruments.InstrumentID{AssetType: "etf", Market: "SH", Code: "sh510300"}}}
	service := NewService(nil, nil, identity, []ETFQuoteProvider{quote}, fund, nil, chart)

	response := service.ETFDetail(context.Background(), "sh510300")

	assert.Equal(t, marketdata.StatusOK, response.Status)
	assert.Equal(t, "sh510300", response.Data.Code)
	assert.Equal(t, "沪深300", response.Data.TrackingIndex)
	assert.Equal(t, 250.0, *response.Data.Shares)
	assert.Equal(t, 1000.0, *response.Data.Scale)
	assert.Equal(t, "sh510300", response.Data.ChartInstrument.Code)
	require.Len(t, response.Data.Holdings, 2)
	assert.Equal(t, "2026-06-30", response.Data.Holdings[0].AsOf)
	assert.Equal(t, 1, chart.calls)
}

func TestETFDetailUsesFundamentalMetadataWhenExchangeIdentityOmitsIt(t *testing.T) {
	identity := &stubIdentityProvider{result: marketdata.ProviderResult[[]ETFIdentity]{Status: marketdata.StatusOK,
		Data: []ETFIdentity{{Code: "159915", Name: "创业板ETF", Category: ETFCategoryBroad, ListDate: "2011-09-20", Listed: true}}}}
	quote := &stubQuoteProvider{name: "tencent", result: marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusOK,
		Data: map[string]ETFQuote{"159915": {Code: "159915", Price: fp(2), ChangeRate: fp(1), Amount: fp(1e8), TurnoverRate: fp(2),
			NetInflow: fp(3e6), Shares: fp(2.5e9), Scale: fp(5e9), QuoteTime: "2026-08-28 10:00:00"}}}}
	fundamental := &stubFundamentalsProvider{name: "eastmoney", result: marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusOK,
		Data: map[string]ETFFundamentals{"159915": {Code: "159915", NAV: fp(1.99), NAVDate: "2026-08-27", Shares: fp(2.5e9), Scale: fp(5e9),
			TrackingIndex: "创业板指数(价格)", ManagementFee: fp(0.15)}}}}
	service := NewService(nil, nil, identity, []ETFQuoteProvider{quote}, fundamental, nil, nil)

	response := service.ETFDetail(context.Background(), "159915")

	assert.Equal(t, "创业板指数(价格)", response.Data.TrackingIndex)
	require.NotNil(t, response.Data.ManagementFee)
	assert.Equal(t, 0.15, *response.Data.ManagementFee)
}

func TestETFDetailDoesNotReportNotFoundWhenIdentityProviderIsUnavailable(t *testing.T) {
	identity := &stubIdentityProvider{result: marketdata.ProviderResult[[]ETFIdentity]{Status: marketdata.StatusUnavailable, Err: context.DeadlineExceeded}}
	service := NewService(nil, nil, identity, nil, nil, nil, nil)

	response := service.ETFDetail(context.Background(), "510300")

	assert.Equal(t, marketdata.StatusUnavailable, response.Status)
	require.NotEmpty(t, response.Errors)
	for _, item := range response.Errors {
		assert.NotEqual(t, "not_found", item.Code)
	}
	assert.Contains(t, response.Errors[len(response.Errors)-1].Message, "cannot be determined")
}

func TestETFFallbackCompletesNetInflowAndShares(t *testing.T) {
	identity := &stubIdentityProvider{result: marketdata.ProviderResult[[]ETFIdentity]{Status: marketdata.StatusOK,
		Data: []ETFIdentity{{Code: "510300", Name: "沪深300ETF", Category: ETFCategoryBroad, Listed: true}}}}
	primaryQuote := &stubQuoteProvider{name: "tencent", result: marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusOK,
		Data: map[string]ETFQuote{"510300": {Code: "510300", Price: fp(4), ChangeRate: fp(1), Amount: fp(100), TurnoverRate: fp(2), Shares: fp(250), QuoteTime: "2026-08-28 10:00:00"}}}}
	incapableSina := &stubQuoteProvider{name: "sina", result: marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusOK,
		Data: map[string]ETFQuote{"510300": {Code: "510300", Price: fp(4)}}}}
	fallbackQuote := &stubQuoteProvider{name: "eastmoney", result: marketdata.ProviderResult[map[string]ETFQuote]{Status: marketdata.StatusOK,
		Data: map[string]ETFQuote{"510300": {Code: "510300", NetInflow: fp(88)}}}}
	primaryFund := &stubFundamentalsProvider{name: "eastmoney", result: marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusOK,
		Data: map[string]ETFFundamentals{"510300": {Code: "510300", NAV: fp(3.9), NAVDate: "2026-08-27", Scale: fp(1000)}}}}
	fallbackFund := &stubFundamentalsProvider{name: "sina", result: marketdata.ProviderResult[map[string]ETFFundamentals]{Status: marketdata.StatusOK,
		Data: map[string]ETFFundamentals{"510300": {Code: "510300", Shares: fp(250)}}}}
	service := NewService(nil, nil, identity, []ETFQuoteProvider{primaryQuote, incapableSina, fallbackQuote}, primaryFund, fallbackFund, nil)

	response := service.ETFRankings(context.Background(), ETFQuery{Page: 1, PageSize: 20})

	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, 88.0, *response.Data.Items[0].NetInflow)
	assert.Equal(t, 250.0, *response.Data.Items[0].Shares)
	assert.Equal(t, 0, incapableSina.calls, "Sina must not consume the deadline for net-inflow-only gaps")
	assert.Equal(t, 1, fallbackQuote.calls)
	assert.Equal(t, 0, fallbackFund.calls, "fund fallback must only repair missing NAV evidence")
}

func TestETFSearchValidationAndNoPersistenceDependency(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil, nil)
	response := service.ETFSearch(context.Background(), "", 51)
	assert.Equal(t, marketdata.StatusUnavailable, response.Status)
	assert.Equal(t, "validation", response.Errors[0].Code)

	// Service has no database/repository field and providers are read-only
	// interfaces. The ETF path therefore cannot insert a recommendation,
	// candidate, simulated trade, position, or account mutation.
	assert.NotContains(t, []any{service.fundPrimary, service.fundFallback, service.etfIdentity, service.etfPrimary, service.etfFallback}, "gorm")
}

func TestProviderErrorCodeRecognizesWrappedTimeoutAndRateLimit(t *testing.T) {
	assert.Equal(t, "timeout", providerErrorCode(errors.Join(errors.New("fetch"), context.DeadlineExceeded)))
	assert.Equal(t, "rate_limited", providerErrorCode(&ProviderHTTPError{StatusCode: 429}))
}

func fp(value float64) *float64 { return &value }

func mustTime(t *testing.T, date string) time.Time {
	t.Helper()
	value, err := time.ParseInLocation(time.DateOnly, date, shanghaiLocation())
	require.NoError(t, err)
	return value
}
