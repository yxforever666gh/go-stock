package funds

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/instruments"
	"go-stock/backend/marketdata"
)

type FundCategory string

const (
	FundCategoryAll   FundCategory = "all"
	FundCategoryStock FundCategory = "stock"
	FundCategoryMixed FundCategory = "mixed"
	FundCategoryBond  FundCategory = "bond"
	FundCategoryIndex FundCategory = "index"
	FundCategoryQDII  FundCategory = "qdii"
	FundCategoryFOF   FundCategory = "fof"
)

type FundPeriod string

const (
	FundPeriodDay            FundPeriod = "day"
	FundPeriodWeek           FundPeriod = "week"
	FundPeriodMonth          FundPeriod = "month"
	FundPeriodThreeMonths    FundPeriod = "3m"
	FundPeriodSixMonths      FundPeriod = "6m"
	FundPeriodOneYear        FundPeriod = "1y"
	FundPeriodThreeYears     FundPeriod = "3y"
	FundPeriodYearToDate     FundPeriod = "ytd"
	FundPeriodSinceInception FundPeriod = "since_inception"
	FundPeriodScale          FundPeriod = "scale"
)

type SortDirection string

const (
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

type FundRankingQuery struct {
	Category      FundCategory  `json:"category"`
	Period        FundPeriod    `json:"period"`
	Q             string        `json:"q,omitempty"`
	SortDirection SortDirection `json:"sortDirection"`
	Page          int           `json:"page"`
	PageSize      int           `json:"pageSize"`
}

type FundRankingItem struct {
	Code                 string       `json:"code"`
	Name                 string       `json:"name"`
	Category             FundCategory `json:"category"`
	NAV                  *float64     `json:"nav"`
	NAVDate              string       `json:"navDate,omitempty"`
	DayReturn            *float64     `json:"dayReturn"`
	WeekReturn           *float64     `json:"weekReturn"`
	MonthReturn          *float64     `json:"monthReturn"`
	ThreeMonthReturn     *float64     `json:"threeMonthReturn"`
	SixMonthReturn       *float64     `json:"sixMonthReturn"`
	OneYearReturn        *float64     `json:"oneYearReturn"`
	ThreeYearReturn      *float64     `json:"threeYearReturn"`
	YearToDateReturn     *float64     `json:"yearToDateReturn"`
	SinceInceptionReturn *float64     `json:"sinceInceptionReturn"`
	Scale                *float64     `json:"scale"`
	ScaleDate            string       `json:"scaleDate,omitempty"`
	Rank                 int          `json:"rank"`
	Source               string       `json:"-"`
}

type FundRankingPage struct {
	Items    []FundRankingItem `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	Category FundCategory      `json:"category"`
	Period   FundPeriod        `json:"period"`
	NAVDate  string            `json:"navDate,omitempty"`
}

type ETFCategory string

const (
	ETFCategoryAll         ETFCategory = "all"
	ETFCategoryBroad       ETFCategory = "broad"
	ETFCategoryIndustry    ETFCategory = "industry"
	ETFCategoryCrossBorder ETFCategory = "cross_border"
	ETFCategoryBond        ETFCategory = "bond"
	ETFCategoryCommodity   ETFCategory = "commodity"
	ETFCategoryMoney       ETFCategory = "money"
)

type ETFSort string

const (
	ETFSortChangeRate   ETFSort = "changeRate"
	ETFSortAmount       ETFSort = "amount"
	ETFSortTurnoverRate ETFSort = "turnoverRate"
	ETFSortPremiumRate  ETFSort = "premiumRate"
	ETFSortScale        ETFSort = "scale"
	ETFSortNetInflow    ETFSort = "netInflow"
)

type ETFQuery struct {
	Category      ETFCategory   `json:"category"`
	Q             string        `json:"q,omitempty"`
	Sort          ETFSort       `json:"sort"`
	SortDirection SortDirection `json:"sortDirection"`
	Page          int           `json:"page"`
	PageSize      int           `json:"pageSize"`
}

type ETFRankingItem struct {
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	Market       string      `json:"market"`
	Category     ETFCategory `json:"category"`
	Price        *float64    `json:"price"`
	ChangeRate   *float64    `json:"changeRate"`
	Amount       *float64    `json:"amount"`
	TurnoverRate *float64    `json:"turnoverRate"`
	NAV          *float64    `json:"nav"`
	NAVDate      string      `json:"navDate,omitempty"`
	PremiumRate  *float64    `json:"premiumRate"`
	Shares       *float64    `json:"shares"`
	Scale        *float64    `json:"scale"`
	NetInflow    *float64    `json:"netInflow"`
	QuoteTime    string      `json:"quoteTime,omitempty"`
	Rank         int         `json:"rank"`
}

type ETFRankingPage struct {
	Items    []ETFRankingItem `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
	Category ETFCategory      `json:"category"`
	Sort     ETFSort          `json:"sort"`
}

type ETFHolding struct {
	Code   string   `json:"code"`
	Name   string   `json:"name"`
	Weight *float64 `json:"weight"`
	AsOf   string   `json:"asOf,omitempty"`
}

type ETFDetail struct {
	ETFRankingItem
	TrackingIndex   string                   `json:"trackingIndex"`
	ManagementFee   *float64                 `json:"managementFee"`
	ListDate        string                   `json:"listDate,omitempty"`
	Holdings        []ETFHolding             `json:"holdings"`
	ChartInstrument instruments.InstrumentID `json:"chartInstrument"`
}

// ETFIdentity is exchange-authoritative identity and listing metadata. It is
// intentionally separate from quote/fund data so a quote vendor cannot change
// a security's asset class, market or listing state.
type ETFIdentity struct {
	Code          string
	Name          string
	Market        string
	Category      ETFCategory
	TrackingIndex string
	ManagementFee *float64
	ListDate      string
	Listed        bool
}

type ETFQuote struct {
	Code         string
	Price        *float64
	ChangeRate   *float64
	Amount       *float64
	TurnoverRate *float64
	NetInflow    *float64
	Shares       *float64
	Scale        *float64
	QuoteTime    string
}

type ETFFundamentals struct {
	Code          string
	NAV           *float64
	NAVDate       string
	PremiumRate   *float64
	Shares        *float64
	Scale         *float64
	ScaleDate     string
	TrackingIndex string
	ManagementFee *float64
	Holdings      []ETFHolding
}

type FundRankingProvider interface {
	Name() string
	FetchFundRankings(context.Context, FundRankingQuery) marketdata.ProviderResult[[]FundRankingItem]
}

type ETFIdentityProvider interface {
	Name() string
	FetchETFIdentities(context.Context) marketdata.ProviderResult[[]ETFIdentity]
}

type ETFQuoteProvider interface {
	Name() string
	FetchETFQuotes(context.Context, []ETFIdentity) marketdata.ProviderResult[map[string]ETFQuote]
}

type ETFFundamentalsProvider interface {
	Name() string
	FetchETFFundamentals(context.Context, []ETFIdentity) marketdata.ProviderResult[map[string]ETFFundamentals]
}

type ETFChartProvider interface {
	Name() string
	ResolveETFChart(context.Context, ETFIdentity) marketdata.ProviderResult[instruments.InstrumentID]
}

var ErrProviderRateLimited = errors.New("provider rate limited")

type ProviderHTTPError struct {
	StatusCode int
	Message    string
}

func (e *ProviderHTTPError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("provider HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("provider HTTP %d: %s", e.StatusCode, e.Message)
}

func (e *ProviderHTTPError) Unwrap() error {
	if e.StatusCode == 429 {
		return ErrProviderRateLimited
	}
	return nil
}

func normalizeFundRankingQuery(query FundRankingQuery) (FundRankingQuery, error) {
	if query.Category == "" {
		query.Category = FundCategoryAll
	}
	if query.Period == "" {
		query.Period = FundPeriodOneYear
	}
	if query.SortDirection == "" {
		query.SortDirection = SortDescending
	}
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 20
	}
	if !validFundCategory(query.Category) {
		return query, fmt.Errorf("invalid fund category %q", query.Category)
	}
	if !validFundPeriod(query.Period) {
		return query, fmt.Errorf("invalid fund period %q", query.Period)
	}
	if !validSortDirection(query.SortDirection) {
		return query, fmt.Errorf("invalid sort direction %q", query.SortDirection)
	}
	if query.Page < 1 {
		return query, errors.New("page must be at least 1")
	}
	if query.PageSize < 1 || query.PageSize > 100 {
		return query, errors.New("pageSize must be between 1 and 100")
	}
	query.Q = strings.TrimSpace(query.Q)
	return query, nil
}

func normalizeETFQuery(query ETFQuery) (ETFQuery, error) {
	if query.Category == "" {
		query.Category = ETFCategoryAll
	}
	if query.Sort == "" {
		query.Sort = ETFSortAmount
	}
	if query.SortDirection == "" {
		query.SortDirection = SortDescending
	}
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 20
	}
	if !validETFCategory(query.Category) {
		return query, fmt.Errorf("invalid ETF category %q", query.Category)
	}
	if !validETFSort(query.Sort) {
		return query, fmt.Errorf("invalid ETF sort %q", query.Sort)
	}
	if !validSortDirection(query.SortDirection) {
		return query, fmt.Errorf("invalid sort direction %q", query.SortDirection)
	}
	if query.Page < 1 {
		return query, errors.New("page must be at least 1")
	}
	if query.PageSize < 1 || query.PageSize > 100 {
		return query, errors.New("pageSize must be between 1 and 100")
	}
	query.Q = strings.TrimSpace(query.Q)
	return query, nil
}

func validFundCategory(value FundCategory) bool {
	switch value {
	case FundCategoryAll, FundCategoryStock, FundCategoryMixed, FundCategoryBond, FundCategoryIndex, FundCategoryQDII, FundCategoryFOF:
		return true
	default:
		return false
	}
}

func validFundPeriod(value FundPeriod) bool {
	switch value {
	case FundPeriodDay, FundPeriodWeek, FundPeriodMonth, FundPeriodThreeMonths, FundPeriodSixMonths, FundPeriodOneYear,
		FundPeriodThreeYears, FundPeriodYearToDate, FundPeriodSinceInception, FundPeriodScale:
		return true
	default:
		return false
	}
}

func validETFCategory(value ETFCategory) bool {
	switch value {
	case ETFCategoryAll, ETFCategoryBroad, ETFCategoryIndustry, ETFCategoryCrossBorder, ETFCategoryBond, ETFCategoryCommodity, ETFCategoryMoney:
		return true
	default:
		return false
	}
}

func validETFSort(value ETFSort) bool {
	switch value {
	case ETFSortChangeRate, ETFSortAmount, ETFSortTurnoverRate, ETFSortPremiumRate, ETFSortScale, ETFSortNetInflow:
		return true
	default:
		return false
	}
}

func validSortDirection(value SortDirection) bool {
	return value == SortAscending || value == SortDescending
}

func providerErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrProviderRateLimited):
		return "rate_limited"
	default:
		return "provider_unavailable"
	}
}

func latestTime(values ...time.Time) time.Time {
	var result time.Time
	for _, value := range values {
		if value.After(result) {
			result = value
		}
	}
	return result
}
