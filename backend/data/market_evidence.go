package data

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/instruments"
	"go-stock/backend/marketdata"

	"github.com/go-resty/resty/v2"
	"gorm.io/gorm"
)

const marketEvidenceProfile = "market-evidence-v1"

type BreadthData struct {
	Total           int     `json:"total"`
	Advances        int     `json:"advances"`
	Declines        int     `json:"declines"`
	Flat            int     `json:"flat"`
	LimitUps        int     `json:"limitUps"`
	LimitDowns      int     `json:"limitDowns"`
	NewHighs        *int    `json:"newHighs"`
	NewLows         *int    `json:"newLows"`
	MedianChangePct float64 `json:"medianChangePct"`
}

type FundFlowRow struct {
	Code                string   `json:"code"`
	Name                string   `json:"name"`
	NetAmount           float64  `json:"netAmount"`
	InAmount            *float64 `json:"inAmount"`
	OutAmount           *float64 `json:"outAmount"`
	ChangePct           *float64 `json:"changePct"`
	MainNetRatio        *float64 `json:"mainNetRatio"`
	SuperLargeNetAmount *float64 `json:"superLargeNetAmount"`
	LargeNetAmount      *float64 `json:"largeNetAmount"`
	MediumNetAmount     *float64 `json:"mediumNetAmount"`
	SmallNetAmount      *float64 `json:"smallNetAmount"`
}

type FundFlowTimelinePoint struct {
	At                  time.Time `json:"at"`
	MainNetAmount       float64   `json:"mainNetAmount"`
	SuperLargeNetAmount float64   `json:"superLargeNetAmount"`
	LargeNetAmount      float64   `json:"largeNetAmount"`
	MediumNetAmount     float64   `json:"mediumNetAmount"`
	SmallNetAmount      float64   `json:"smallNetAmount"`
}

type FundFlowTimelineData struct {
	Code        string                  `json:"code"`
	Name        string                  `json:"name,omitempty"`
	TradingDate string                  `json:"tradingDate,omitempty"`
	Points      []FundFlowTimelinePoint `json:"points"`
}

type FuturesPositionRow struct {
	TradeDate     string  `json:"tradeDate"`
	SettlePrice   float64 `json:"settlePrice"`
	LongPosition  int64   `json:"longPosition"`
	LongChange    int64   `json:"longChange"`
	ShortPosition int64   `json:"shortPosition"`
	ShortChange   int64   `json:"shortChange"`
	NetPosition   int64   `json:"netPosition"`
	IndexClose    float64 `json:"indexClose"`
	IndexChange   float64 `json:"indexChange"`
	Basis         float64 `json:"basis"`
}

type FuturesPositionsData struct {
	Variety      string               `json:"variety"`
	VarietyName  string               `json:"varietyName"`
	ContractCode string               `json:"contractCode"`
	IndexCode    string               `json:"indexCode"`
	Rows         []FuturesPositionRow `json:"rows"`
}

type MarginRow struct {
	Code           string  `json:"code,omitempty"`
	Name           string  `json:"name,omitempty"`
	Date           string  `json:"date"`
	Financing      float64 `json:"financingBalance"`
	Securities     float64 `json:"securitiesBalance"`
	MarginBalance  float64 `json:"marginBalance"`
	FinancingBuy   float64 `json:"financingBuy"`
	SecuritiesSell float64 `json:"securitiesSell"`
}

type MarginData struct {
	Scope string      `json:"scope"`
	Rows  []MarginRow `json:"rows"`
}

type AuctionSnapshot struct {
	Time            string   `json:"time"`
	Price           float64  `json:"price"`
	MatchedVolume   float64  `json:"matchedVolume"`
	MatchedAmount   float64  `json:"matchedAmount"`
	UnmatchedVolume *float64 `json:"unmatchedVolume"`
	UnmatchedSide   string   `json:"unmatchedSide,omitempty"`
}

type AuctionData struct {
	Code            string            `json:"code"`
	AssetType       string            `json:"assetType"`
	Date            string            `json:"date"`
	Snapshots       []AuctionSnapshot `json:"snapshots"`
	FinalSnapshot   *AuctionSnapshot  `json:"finalSnapshot"`
	AuctionStrength *float64          `json:"auctionStrength"`
	GapPct          *float64          `json:"gapPct"`
}

type TradeTick struct {
	Time   string  `json:"time"`
	Price  float64 `json:"price"`
	Volume float64 `json:"volume"`
	Amount float64 `json:"amount"`
	Side   string  `json:"side,omitempty"`
}

type TradesData struct {
	Code          string      `json:"code"`
	AssetType     string      `json:"assetType"`
	Date          string      `json:"date"`
	Items         []TradeTick `json:"items"`
	NextCursor    string      `json:"nextCursor,omitempty"`
	PreviousClose float64     `json:"-"`
}

type MarketEvidenceService struct {
	client       *resty.Client
	now          func() time.Time
	urls         marketEvidenceURLs
	mainDB       *gorm.DB
	minuteDB     *gorm.DB
	breadthMu    sync.RWMutex
	breadthCache *breadthCacheEntry
}

type marketEvidenceURLs struct {
	breadth           string
	breadthDelay      string
	breadthTencent    string
	fundFlowEastmoney string
	fundFlowSina      string
	fundFlowTimeline  string
	margin            string
	marginSecurity    string
	marginSSE         string
	marginSZSE        string
	details           string
}

func NewMarketEvidenceService() *MarketEvidenceService {
	return NewMarketEvidenceServiceWithMinuteDB(db.MinuteDao)
}

// NewMarketEvidenceServiceWithMinuteDB keeps the production constructor tied
// to the minute database while allowing isolated cache tests to inject their
// own database.
func NewMarketEvidenceServiceWithMinuteDB(minuteDB *gorm.DB) *MarketEvidenceService {
	service := &MarketEvidenceService{
		client:   newFetchRestyClient().SetTimeout(8 * time.Second),
		now:      time.Now,
		mainDB:   db.Dao,
		minuteDB: minuteDB,
		urls: marketEvidenceURLs{
			breadth:           "https://push2.eastmoney.com/api/qt/clist/get",
			breadthDelay:      "https://push2delay.eastmoney.com/api/qt/clist/get",
			breadthTencent:    "http://qt.gtimg.cn/",
			fundFlowEastmoney: "https://push2delay.eastmoney.com/api/qt/clist/get",
			fundFlowSina:      "https://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow.ssl_bkzj_bk",
			fundFlowTimeline:  "https://push2delay.eastmoney.com/api/qt/stock/fflow/kline/get",
			margin:            "https://datacenter-web.eastmoney.com/api/data/v1/get",
			marginSecurity:    "https://datacenter.eastmoney.com/securities/api/data/v1/get",
			marginSSE:         "https://query.sse.com.cn/commonSoaQuery.do",
			marginSZSE:        "https://www.szse.cn/api/report/ShowReport/data",
			details:           "https://push2.eastmoney.com/api/qt/stock/details/get",
		},
	}
	if minuteDB != nil {
		if minuteDB.Migrator().HasTable(&marketTradeTickCache{}) {
			_ = cleanupTradeTickCache(context.Background(), minuteDB, tradeTickRetentionDays)
		}
		if minuteDB.Migrator().HasTable(&marketAuctionSnapshotCache{}) {
			_ = cleanupAuctionSnapshotCache(context.Background(), minuteDB, auctionRetentionDays)
		}
	}
	return service
}

// NewMarketEvidenceServiceWithStorage is the storage-aware constructor used by
// isolated runtimes.  Keeping both databases injectable is important for the
// Tencent breadth fallback: it builds its universe from the main stock master
// while its tick/minute fallbacks use the minute database.
func NewMarketEvidenceServiceWithStorage(mainDB, minuteDB *gorm.DB) *MarketEvidenceService {
	service := NewMarketEvidenceServiceWithMinuteDB(minuteDB)
	service.mainDB = mainDB
	return service
}

func (s *MarketEvidenceService) Breadth(ctx context.Context) marketdata.DataEnvelope[BreadthData] {
	return s.collectBreadth(ctx)
}

func (s *MarketEvidenceService) FundFlows(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[[]FundFlowRow] {
	collector := marketdata.PrimaryFallbackCollector[[]FundFlowRow]{
		Primary:  &eastmoneyFundFlowProvider{service: s},
		Fallback: &sinaFundFlowProvider{service: s},
	}
	return withEvidenceProfile(collector.Collect(ctx, request))
}

func (s *MarketEvidenceService) FundFlowTimeline(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[FundFlowTimelineData] {
	envelope := withEvidenceProfile(marketdata.ProviderChainCollector[FundFlowTimelineData]{Providers: []marketdata.Provider[FundFlowTimelineData]{
		&eastmoneyFundFlowTimelineProvider{service: s},
	}}.Collect(ctx, request))
	if envelope.Data.Points == nil {
		envelope.Data.Points = []FundFlowTimelinePoint{}
	}
	if envelope.Data.Code == "" {
		envelope.Data.Code, _ = NormalizeFundFlowBoardCode(request.Code)
	}
	return envelope
}

func (s *MarketEvidenceService) FuturesPositions(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[FuturesPositionsData] {
	collector := marketdata.PrimaryFallbackCollector[FuturesPositionsData]{
		Primary:  &eastmoneyFuturesProvider{service: s},
		Fallback: &cffexFuturesProvider{service: s},
	}
	envelope := collector.Collect(ctx, request)
	if envelope.Data.Rows == nil {
		meta := futuresMetadata(request.Symbol)
		envelope.Data = FuturesPositionsData{Variety: request.Symbol, VarietyName: meta.name, IndexCode: meta.indexCode, Rows: []FuturesPositionRow{}}
	}
	return withEvidenceProfile(envelope)
}

func (s *MarketEvidenceService) Margin(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[MarginData] {
	var primary marketdata.Provider[MarginData]
	var fallback marketdata.Provider[MarginData]
	if request.Scope == "market" {
		primary = &officialMarginProvider{service: s}
		fallback = unavailableProvider[MarginData]{name: "eastmoney", reason: "东方财富 LSSH 仅覆盖沪市，不能作为全市场汇总备用值"}
	} else {
		primary = &eastmoneyMarginProvider{service: s}
		fallback = unavailableProvider[MarginData]{name: "sse-szse", reason: "交易所个股明细无法完整提供融券余额金额"}
	}
	collector := marketdata.PrimaryFallbackCollector[MarginData]{
		Primary: primary, Fallback: fallback,
	}
	envelope := collector.Collect(ctx, request)
	if envelope.Data.Rows == nil {
		envelope.Data = MarginData{Scope: request.Scope, Rows: []MarginRow{}}
	}
	return withEvidenceProfile(envelope)
}

func (s *MarketEvidenceService) Auction(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[AuctionData] {
	cleanupErr := cleanupAuctionSnapshotCache(ctx, s.minuteDB, auctionRetentionDays)
	if s.isHistoricalDate(request.Date) {
		envelope := s.cachedAuction(ctx, request)
		if cleanupErr != nil {
			envelope = markCacheIssue(envelope, "cache_cleanup_failed", cleanupErr)
		}
		return envelope
	}
	collector := marketdata.PrimaryFallbackCollector[AuctionData]{
		Primary:  &eastmoneyAuctionProvider{service: s},
		Fallback: unavailableProvider[AuctionData]{name: "tencent", reason: "腾讯快照不含可验证的集合竞价未匹配量"},
	}
	envelope := collector.Collect(ctx, request)
	if envelope.Data.Snapshots == nil {
		envelope.Data = AuctionData{Code: request.Code, AssetType: request.AssetType, Date: request.Date, Snapshots: []AuctionSnapshot{}}
	}
	envelope = withEvidenceProfile(envelope)
	envelope = s.cacheAuctionEnvelope(ctx, request, envelope)
	if cleanupErr != nil {
		envelope = markCacheIssue(envelope, "cache_cleanup_failed", cleanupErr)
	}
	return envelope
}

func (s *MarketEvidenceService) Trades(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[TradesData] {
	cleanupErr := cleanupTradeTickCache(ctx, s.minuteDB, tradeTickRetentionDays)
	if s.isHistoricalDate(request.Date) {
		envelope := s.cachedTrades(ctx, request)
		if cleanupErr != nil {
			envelope = markCacheIssue(envelope, "cache_cleanup_failed", cleanupErr)
		}
		return envelope
	}
	collector := marketdata.PrimaryFallbackCollector[TradesData]{
		Primary:  &eastmoneyTradesProvider{service: s},
		Fallback: unavailableProvider[TradesData]{name: "tencent", reason: "腾讯公开接口不提供等价逐笔方向字段"},
	}
	envelope := collector.Collect(ctx, request)
	if envelope.Data.Items == nil {
		envelope.Data = TradesData{Code: request.Code, AssetType: request.AssetType, Date: request.Date, Items: []TradeTick{}}
	}
	envelope = withEvidenceProfile(envelope)
	envelope = s.cacheTradesEnvelope(ctx, request, envelope)
	if cleanupErr != nil {
		envelope = markCacheIssue(envelope, "cache_cleanup_failed", cleanupErr)
	}
	return envelope
}

func withEvidenceProfile[T any](envelope marketdata.DataEnvelope[T]) marketdata.DataEnvelope[T] {
	envelope.EvidenceProfile = marketEvidenceProfile
	if envelope.Errors == nil {
		envelope.Errors = []marketdata.DataError{}
	}
	return envelope
}

type unavailableProvider[T any] struct{ name, reason string }

func (p unavailableProvider[T]) Name() string { return p.name }
func (p unavailableProvider[T]) Collect(_ context.Context, _ marketdata.ProviderRequest) marketdata.ProviderResult[T] {
	var zero T
	return marketdata.ProviderResult[T]{Status: marketdata.StatusUnavailable, Data: zero, Warning: p.reason, Err: errors.New(p.reason)}
}

type eastmoneyFundFlowProvider struct{ service *MarketEvidenceService }

func (p *eastmoneyFundFlowProvider) Name() string { return "eastmoney-delay" }

func (p *eastmoneyFundFlowProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[[]FundFlowRow] {
	now := p.service.now()
	boardCode := "m:90 s:4"
	if request.Scope == "concept" {
		boardCode = "m:90 t:3"
	}
	sortField, complete := eastmoneyFundFlowSortField(request.Sort)
	response, err := p.service.client.R().SetContext(ctx).
		SetHeader("Referer", "https://data.eastmoney.com/bkzj/").
		SetHeader("User-Agent", marketEvidenceUserAgent()).
		SetQueryParams(map[string]string{
			"pn": "1", "pz": strconv.Itoa(request.Limit), "po": "1", "np": "1", "fltt": "2", "invt": "2",
			"fid": sortField, "fs": boardCode, "stat": "1",
			"fields": "f12,f14,f2,f3,f62,f184,f66,f69,f72,f75,f78,f81,f84,f87,f204,f205,f124",
			"ut":     "8dec03ba335b81bf4ebdf7b29ec27d15",
		}).
		Get(p.service.urls.fundFlowEastmoney)
	if err != nil {
		return providerFailure[[]FundFlowRow](now, p.service.urls.fundFlowEastmoney, err)
	}
	if response.StatusCode() >= 400 {
		return providerFailure[[]FundFlowRow](now, p.service.urls.fundFlowEastmoney, fmt.Errorf("HTTP %d", response.StatusCode()))
	}
	rows, asOf, err := parseEastmoneyFundFlows(response.Body())
	if err != nil {
		return providerFailure[[]FundFlowRow](now, p.service.urls.fundFlowEastmoney, err)
	}
	if request.Date != "" && (asOf.IsZero() || request.Date != asOf.In(shanghaiDataLocation()).Format("2006-01-02")) {
		return providerFailure[[]FundFlowRow](now, p.service.urls.fundFlowEastmoney,
			fmt.Errorf("东方财富板块资金流公开接口只提供最新交易日 %s", asOf.In(shanghaiDataLocation()).Format("2006-01-02")))
	}
	available := p.service.now()
	status, warning := marketdata.StatusOK, ""
	if !complete {
		status, warning = marketdata.StatusPartial, "东方财富不提供流入或流出总额，已尝试新浪备用源满足旧排序"
	} else if asOf.IsZero() {
		status, warning = marketdata.StatusPartial, "东方财富板块资金流缺少可验证的行情时间"
	}
	return marketdata.ProviderResult[[]FundFlowRow]{Status: status, AsOf: asOf, AvailableAt: &available, Data: rows, SourceRef: p.service.urls.fundFlowEastmoney, Warning: warning}
}

func eastmoneyFundFlowSortField(sortBy string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "", "netamount":
		return "f62", true
	case "main_ratio":
		return "f184", true
	case "super_large_netamount":
		return "f66", true
	case "large_netamount":
		return "f72", true
	case "medium_netamount":
		return "f78", true
	case "small_netamount":
		return "f84", true
	case "change_pct", "avg_changeratio":
		return "f3", true
	case "inamount", "outamount":
		return "f62", false
	default:
		return "f62", false
	}
}

func parseEastmoneyFundFlows(body []byte) ([]FundFlowRow, time.Time, error) {
	var payload struct {
		Data *struct {
			Diff json.RawMessage `json:"diff"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, time.Time{}, err
	}
	if payload.Data == nil {
		return nil, time.Time{}, errors.New("empty Eastmoney fund-flow response")
	}
	var values []map[string]any
	if err := json.Unmarshal(payload.Data.Diff, &values); err != nil {
		var keyed map[string]map[string]any
		if keyedErr := json.Unmarshal(payload.Data.Diff, &keyed); keyedErr != nil {
			return nil, time.Time{}, fmt.Errorf("decode fund-flow rows: %w", err)
		}
		keys := make([]string, 0, len(keyed))
		for key := range keyed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			values = append(values, keyed[key])
		}
	}
	rows := make([]FundFlowRow, 0, len(values))
	var asOf time.Time
	for _, value := range values {
		code, name := anyString(value["f12"]), anyString(value["f14"])
		netAmount, netOK := anyFloat(value["f62"])
		if code == "" || name == "" || !netOK {
			continue
		}
		rows = append(rows, FundFlowRow{
			Code: code, Name: name, NetAmount: netAmount, ChangePct: floatAnyPointer(value, "f3"), MainNetRatio: floatAnyPointer(value, "f184"),
			SuperLargeNetAmount: floatAnyPointer(value, "f66"), LargeNetAmount: floatAnyPointer(value, "f72"),
			MediumNetAmount: floatAnyPointer(value, "f78"), SmallNetAmount: floatAnyPointer(value, "f84"),
		})
		if timestamp, ok := anyFloat(value["f124"]); ok && timestamp > 0 {
			candidate := time.Unix(int64(timestamp), 0).In(shanghaiDataLocation())
			if candidate.After(asOf) {
				asOf = candidate
			}
		}
	}
	if len(rows) == 0 {
		return nil, time.Time{}, errors.New("empty Eastmoney fund-flow response")
	}
	return rows, asOf, nil
}

type sinaFundFlowProvider struct{ service *MarketEvidenceService }

func (p *sinaFundFlowProvider) Name() string { return "sina" }
func (p *sinaFundFlowProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[[]FundFlowRow] {
	now := p.service.now()
	if request.Date != "" {
		return providerFailure[[]FundFlowRow](now, p.service.urls.fundFlowSina, errors.New("新浪板块资金流公开接口不提供可验证的交易日期"))
	}
	category := "0"
	if request.Scope == "concept" {
		category = "1"
	}
	endpoint, _ := url.Parse(p.service.urls.fundFlowSina)
	query := endpoint.Query()
	query.Set("page", "1")
	query.Set("num", strconv.Itoa(request.Limit))
	query.Set("sort", request.Sort)
	query.Set("asc", "0")
	query.Set("fenlei", category)
	endpoint.RawQuery = query.Encode()
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "https://finance.sina.com.cn").SetHeader("User-Agent", marketEvidenceUserAgent()).Get(endpoint.String())
	if err != nil {
		return providerFailure[[]FundFlowRow](now, endpoint.String(), err)
	}
	if response.StatusCode() >= 400 {
		return providerFailure[[]FundFlowRow](now, endpoint.String(), fmt.Errorf("HTTP %d", response.StatusCode()))
	}
	rows, err := parseSinaFundFlows(response.Body())
	if err != nil {
		return providerFailure[[]FundFlowRow](now, endpoint.String(), err)
	}
	if request.Limit > 0 && len(rows) > request.Limit {
		rows = rows[:request.Limit]
	}
	available := p.service.now()
	return marketdata.ProviderResult[[]FundFlowRow]{Status: marketdata.StatusPartial, AvailableAt: &available, Data: rows, SourceRef: endpoint.String(),
		Warning: "新浪板块资金流不提供可验证的交易日期，仅作为旧版流入/流出字段的降级来源"}
}

func parseSinaFundFlows(body []byte) ([]FundFlowRow, error) {
	var values []map[string]any
	if err := json.Unmarshal(body, &values); err != nil {
		return nil, err
	}
	rows := make([]FundFlowRow, 0, len(values))
	for _, value := range values {
		netAmount, ok := anyFloat(firstAny(value, "netamount", "net_amount"))
		if !ok {
			continue
		}
		changePct := floatAnyPointer(value, "avg_changeratio", "changeratio", "changepercent")
		if changePct != nil {
			converted := *changePct * 100
			changePct = &converted
		}
		rows = append(rows, FundFlowRow{
			Code: anyString(firstAny(value, "category", "code", "symbol")), Name: anyString(firstAny(value, "name", "categoryname")),
			NetAmount: netAmount, InAmount: floatAnyPointer(value, "inamount", "in_amount"),
			OutAmount: floatAnyPointer(value, "outamount", "out_amount"), ChangePct: changePct,
		})
	}
	if rows == nil {
		rows = []FundFlowRow{}
	}
	return rows, nil
}

type eastmoneyFundFlowTimelineProvider struct{ service *MarketEvidenceService }

func (p *eastmoneyFundFlowTimelineProvider) Name() string { return "eastmoney-delay" }

func (p *eastmoneyFundFlowTimelineProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[FundFlowTimelineData] {
	now := p.service.now()
	code, ok := NormalizeFundFlowBoardCode(request.Code)
	if !ok {
		return providerFailure[FundFlowTimelineData](now, p.service.urls.fundFlowTimeline, errors.New("资金流代码必须是 BK 加四位数字"))
	}
	response, err := p.service.client.R().SetContext(ctx).
		SetHeader("Referer", "https://data.eastmoney.com/bkzj/").
		SetHeader("User-Agent", marketEvidenceUserAgent()).
		SetQueryParams(map[string]string{
			"lmt": "0", "klt": "1", "secid": "90." + code, "fields1": "f1,f2,f3,f7",
			"fields2": "f51,f52,f53,f54,f55,f56", "ut": "b2884a393a59ad64002292a3e90d46a5",
		}).Get(p.service.urls.fundFlowTimeline)
	if err != nil {
		return providerFailure[FundFlowTimelineData](now, p.service.urls.fundFlowTimeline, err)
	}
	if response.StatusCode() >= 400 {
		return providerFailure[FundFlowTimelineData](now, p.service.urls.fundFlowTimeline, fmt.Errorf("HTTP %d", response.StatusCode()))
	}
	data, asOf, err := parseEastmoneyFundFlowTimeline(response.Body(), code)
	if err != nil {
		return providerFailure[FundFlowTimelineData](now, p.service.urls.fundFlowTimeline, err)
	}
	available := p.service.now()
	return marketdata.ProviderResult[FundFlowTimelineData]{Status: marketdata.StatusOK, AsOf: asOf, AvailableAt: &available, Data: data, SourceRef: p.service.urls.fundFlowTimeline}
}

func NormalizeFundFlowBoardCode(value string) (string, bool) {
	code := strings.ToUpper(strings.TrimSpace(value))
	if len(code) != 6 || !strings.HasPrefix(code, "BK") {
		return "", false
	}
	for _, digit := range code[2:] {
		if digit < '0' || digit > '9' {
			return "", false
		}
	}
	return code, true
}

func parseEastmoneyFundFlowTimeline(body []byte, expectedCode string) (FundFlowTimelineData, time.Time, error) {
	var payload struct {
		RC   int `json:"rc"`
		Data *struct {
			Code   string   `json:"code"`
			Name   string   `json:"name"`
			KLines []string `json:"klines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return FundFlowTimelineData{}, time.Time{}, err
	}
	code, ok := NormalizeFundFlowBoardCode(expectedCode)
	if payload.RC != 0 || payload.Data == nil || !ok || !strings.EqualFold(payload.Data.Code, code) {
		return FundFlowTimelineData{}, time.Time{}, errors.New("东方财富资金时间线返回无效或代码不匹配")
	}
	pointsByTime := make(map[int64]FundFlowTimelinePoint, len(payload.Data.KLines))
	for index, line := range payload.Data.KLines {
		fields := strings.Split(line, ",")
		if len(fields) < 6 {
			return FundFlowTimelineData{}, time.Time{}, fmt.Errorf("资金时间线第%d行字段不足", index+1)
		}
		at, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(fields[0]), shanghaiDataLocation())
		if err != nil {
			return FundFlowTimelineData{}, time.Time{}, fmt.Errorf("资金时间线第%d行时间无效: %w", index+1, err)
		}
		values := make([]float64, 5)
		for valueIndex := range values {
			parsed, ok := parseFloat(fields[valueIndex+1])
			if !ok {
				return FundFlowTimelineData{}, time.Time{}, fmt.Errorf("资金时间线第%d行数值无效", index+1)
			}
			values[valueIndex] = parsed
		}
		pointsByTime[at.Unix()] = FundFlowTimelinePoint{At: at, MainNetAmount: values[0], SmallNetAmount: values[1], MediumNetAmount: values[2], LargeNetAmount: values[3], SuperLargeNetAmount: values[4]}
	}
	points := make([]FundFlowTimelinePoint, 0, len(pointsByTime))
	for _, point := range pointsByTime {
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].At.Before(points[j].At) })
	if len(points) == 0 {
		return FundFlowTimelineData{}, time.Time{}, errors.New("东方财富资金时间线为空")
	}
	latestDate := points[len(points)-1].At.Format("2006-01-02")
	filtered := points[:0]
	for _, point := range points {
		if point.At.Format("2006-01-02") == latestDate {
			filtered = append(filtered, point)
		}
	}
	data := FundFlowTimelineData{Code: code, Name: strings.TrimSpace(payload.Data.Name), TradingDate: latestDate, Points: filtered}
	return data, filtered[len(filtered)-1].At, nil
}

type futuresMeta struct{ name, indexCode string }

func futuresMetadata(symbol string) futuresMeta {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "IF":
		return futuresMeta{name: "沪深300股指期货", indexCode: "sh000300"}
	case "IH":
		return futuresMeta{name: "上证50股指期货", indexCode: "sh000016"}
	case "IC":
		return futuresMeta{name: "中证500股指期货", indexCode: "sh000905"}
	case "IM":
		return futuresMeta{name: "中证1000股指期货", indexCode: "sh000852"}
	default:
		return futuresMeta{}
	}
}

type eastmoneyFuturesProvider struct{ service *MarketEvidenceService }

func (p *eastmoneyFuturesProvider) Name() string { return "eastmoney" }

func (p *eastmoneyFuturesProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[FuturesPositionsData] {
	now := p.service.now()
	symbol := strings.ToUpper(strings.TrimSpace(request.Symbol))
	contract, err := p.mainContract(ctx, symbol)
	if err != nil {
		return providerFailure[FuturesPositionsData](now, p.service.urls.margin, err)
	}
	params := map[string]string{
		"reportName": "RPT_FUTU_NET_POSITION", "columns": "ALL", "pageSize": "60", "pageNumber": "1", "source": "WEB", "client": "WEB",
		"sortColumns": "TRADE_DATE", "sortTypes": "-1", "filter": fmt.Sprintf("(SECURITY_CODE=\"%s\")", contract),
	}
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "https://data.eastmoney.com/").SetHeader("User-Agent", marketEvidenceUserAgent()).SetQueryParams(params).Get(p.service.urls.margin)
	if err != nil {
		return providerFailure[FuturesPositionsData](now, p.service.urls.margin, err)
	}
	if response.StatusCode() >= 400 {
		return providerFailure[FuturesPositionsData](now, p.service.urls.margin, fmt.Errorf("HTTP %d", response.StatusCode()))
	}
	rows, err := parseEastmoneyFutures(response.Body(), request.Date)
	if err != nil {
		return providerFailure[FuturesPositionsData](now, p.service.urls.margin, err)
	}
	meta := futuresMetadata(symbol)
	available := p.service.now()
	return marketdata.ProviderResult[FuturesPositionsData]{Status: marketdata.StatusOK, AsOf: now, AvailableAt: &available, Data: FuturesPositionsData{Variety: symbol, VarietyName: meta.name, ContractCode: contract, IndexCode: meta.indexCode, Rows: rows}, SourceRef: p.service.urls.margin}
}

func (p *eastmoneyFuturesProvider) mainContract(ctx context.Context, symbol string) (string, error) {
	params := map[string]string{"reportName": "RPT_FUTU_POSITIONCODE", "columns": "ALL", "pageSize": "5", "pageNumber": "1", "source": "WEB", "client": "WEB", "filter": fmt.Sprintf("(TRADE_CODE=\"%s\")(IS_MAINCODE=\"1\")", symbol)}
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "https://data.eastmoney.com/").SetHeader("User-Agent", marketEvidenceUserAgent()).SetQueryParams(params).Get(p.service.urls.margin)
	if err != nil {
		return "", fmt.Errorf("locate main contract: %w", err)
	}
	if response.StatusCode() >= 400 {
		return "", fmt.Errorf("locate main contract: HTTP %d", response.StatusCode())
	}
	var payload struct {
		Result struct {
			Data []map[string]any `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		return "", err
	}
	if len(payload.Result.Data) == 0 {
		return "", errors.New("main futures contract is unavailable")
	}
	contract := strings.ToUpper(anyString(payload.Result.Data[0]["SECURITY_CODE"]))
	if contract == "" {
		return "", errors.New("main futures contract code is empty")
	}
	return contract, nil
}

func parseEastmoneyFutures(body []byte, requestedDate string) ([]FuturesPositionRow, error) {
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Result  struct {
			Data []map[string]any `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !payload.Success && payload.Message != "" {
		return nil, errors.New(payload.Message)
	}
	rows := make([]FuturesPositionRow, 0, len(payload.Result.Data))
	for _, item := range payload.Result.Data {
		date := anyString(item["TRADE_DATE"])
		if len(date) >= 10 {
			date = date[:10]
		}
		if requestedDate != "" && requestedDate != date {
			continue
		}
		rows = append(rows, FuturesPositionRow{TradeDate: date, SettlePrice: floatAny(item, "SETTLE_PRICE"), LongPosition: int64(floatAny(item, "TOTAL_LONG_POSITION")), LongChange: int64(floatAny(item, "LP_CHANGE_TOTAL")), ShortPosition: int64(floatAny(item, "TOTAL_SHORT_POSITION")), ShortChange: int64(floatAny(item, "SP_CHANGE_TOTAL")), NetPosition: int64(floatAny(item, "NET_POSITION")), IndexClose: floatAny(item, "CLOSE_PRICE"), IndexChange: floatAny(item, "CLOSE_PRICE_CHANGE"), Basis: floatAny(item, "BASIS")})
	}
	if len(rows) == 0 {
		return nil, errors.New("empty or unmatched futures position response")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate < rows[j].TradeDate })
	return rows, nil
}

type cffexFuturesProvider struct{ service *MarketEvidenceService }

func (p *cffexFuturesProvider) Name() string { return "cffex" }

func (p *cffexFuturesProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[FuturesPositionsData] {
	now := p.service.now().In(shanghaiDataLocation())
	providerCtx, cancelProvider := context.WithTimeout(ctx, 20*time.Second)
	defer cancelProvider()
	symbol := strings.ToUpper(strings.TrimSpace(request.Symbol))
	meta := futuresMetadata(symbol)
	rows := make([]FuturesPositionRow, 0, 60)
	contract := ""
	start := now
	iterations := 0
	if request.Date != "" {
		parsed, err := time.ParseInLocation("2006-01-02", request.Date, shanghaiDataLocation())
		if err != nil {
			return providerFailure[FuturesPositionsData](now, "http://www.cffex.com.cn/sj/ccpm/", err)
		}
		start = parsed
	}
	for day := start; len(rows) < 60 && iterations < 140; day = day.AddDate(0, 0, -1) {
		iterations++
		if providerCtx.Err() != nil {
			break
		}
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		row, selectedContract, ok := p.fetchDay(providerCtx, symbol, day)
		if !ok {
			if request.Date != "" {
				break
			}
			continue
		}
		if contract == "" {
			contract = selectedContract
		}
		rows = append(rows, row)
		if request.Date != "" {
			break
		}
	}
	if len(rows) == 0 {
		return providerFailure[FuturesPositionsData](now, "http://www.cffex.com.cn/sj/ccpm/", errors.New("CFFEX position CSV is unavailable"))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate < rows[j].TradeDate })
	for index := 1; index < len(rows); index++ {
		rows[index].LongChange = rows[index].LongPosition - rows[index-1].LongPosition
		rows[index].ShortChange = rows[index].ShortPosition - rows[index-1].ShortPosition
	}
	available := p.service.now()
	return marketdata.ProviderResult[FuturesPositionsData]{Status: marketdata.StatusPartial, AsOf: now, AvailableAt: &available, Data: FuturesPositionsData{Variety: symbol, VarietyName: meta.name, ContractCode: contract, IndexCode: meta.indexCode, Rows: rows}, SourceRef: "http://www.cffex.com.cn/sj/ccpm/", Warning: "中金所降级源不含结算价、现货指数收盘与基差"}
}

func (p *cffexFuturesProvider) fetchDay(ctx context.Context, symbol string, day time.Time) (FuturesPositionRow, string, bool) {
	endpoint := fmt.Sprintf("http://www.cffex.com.cn/sj/ccpm/%s/%s/%s_1.csv", day.Format("200601"), day.Format("02"), symbol)
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "http://www.cffex.com.cn/").SetHeader("User-Agent", marketEvidenceUserAgent()).Get(endpoint)
	if err != nil || response.StatusCode() != httpStatusOK || len(response.Body()) == 0 {
		return FuturesPositionRow{}, "", false
	}
	return parseCffexPositionDay(string(GB18030ToUTF8(response.Body())), day)
}

const httpStatusOK = 200

func parseCffexPositionDay(body string, day time.Time) (FuturesPositionRow, string, bool) {
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil || len(records) <= 2 {
		return FuturesPositionRow{}, "", false
	}
	type member struct {
		contract            string
		volume, long, short int64
	}
	values := make([]member, 0, len(records))
	volumeByContract := map[string]int64{}
	for _, record := range records[2:] {
		if len(record) < 12 {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(record[2])); err != nil {
			continue
		}
		item := member{contract: strings.TrimSpace(record[1]), volume: parseEvidenceInt(record[4]), long: parseEvidenceInt(record[7]), short: parseEvidenceInt(record[10])}
		values = append(values, item)
		volumeByContract[item.contract] += item.volume
	}
	contract := ""
	var maxVolume int64 = -1
	for key, volume := range volumeByContract {
		if volume > maxVolume {
			contract, maxVolume = key, volume
		}
	}
	if contract == "" {
		return FuturesPositionRow{}, "", false
	}
	var long, short int64
	for _, value := range values {
		if value.contract == contract {
			long += value.long
			short += value.short
		}
	}
	if long == 0 && short == 0 {
		return FuturesPositionRow{}, "", false
	}
	return FuturesPositionRow{TradeDate: day.Format("2006-01-02"), LongPosition: long, ShortPosition: short, NetPosition: long - short}, contract, true
}

func parseEvidenceInt(value string) int64 {
	result, _ := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(value, ",", "")), 10, 64)
	return result
}

type officialMarginProvider struct{ service *MarketEvidenceService }

func (p *officialMarginProvider) Name() string { return "sse+szse" }

func (p *officialMarginProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[MarginData] {
	now := p.service.now()
	sse, sseErr := p.fetchSSE(ctx, request.Date)
	date := request.Date
	if date == "" && sseErr == nil {
		date = sse.Date
	}
	szse, szseErr := p.fetchSZSE(ctx, date)
	rows := make([]MarginRow, 0, 2)
	if sseErr == nil {
		rows = append(rows, sse)
	}
	if szseErr == nil {
		rows = append(rows, szse)
	}
	if len(rows) == 0 {
		return providerFailure[MarginData](now, p.service.urls.marginSSE, errors.Join(sseErr, szseErr))
	}
	available := p.service.now()
	status, warning := marketdata.StatusOK, ""
	if sseErr != nil || szseErr != nil {
		status = marketdata.StatusPartial
		warning = "交易所汇总部分不可用: "
		if sseErr != nil {
			warning += "SSE=" + sseErr.Error()
		}
		if szseErr != nil {
			if sseErr != nil {
				warning += "; "
			}
			warning += "SZSE=" + szseErr.Error()
		}
	}
	return marketdata.ProviderResult[MarginData]{Status: status, AsOf: now, AvailableAt: &available, Data: MarginData{Scope: "market", Rows: rows}, SourceRef: p.service.urls.marginSSE + " | " + p.service.urls.marginSZSE, Warning: warning}
}

func (p *officialMarginProvider) fetchSSE(ctx context.Context, requestedDate string) (MarginRow, error) {
	params := map[string]string{"isPagination": "true", "pageHelp.pageSize": "1", "pageHelp.pageNo": "1", "pageHelp.beginPage": "1", "pageHelp.cacheSize": "1", "pageHelp.endPage": "1", "sqlId": "RZRQ_HZ_INFO"}
	if requestedDate != "" {
		compact := strings.ReplaceAll(requestedDate, "-", "")
		params["beginDate"], params["endDate"] = compact, compact
	}
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "https://www.sse.com.cn/market/othersdata/margin/sum/").SetHeader("User-Agent", marketEvidenceUserAgent()).SetQueryParams(params).Get(p.service.urls.marginSSE)
	if err != nil {
		return MarginRow{}, err
	}
	if response.StatusCode() >= 400 {
		return MarginRow{}, fmt.Errorf("HTTP %d", response.StatusCode())
	}
	return parseSSEMargin(response.Body(), requestedDate)
}

func parseSSEMargin(body []byte, requestedDate string) (MarginRow, error) {
	var payload struct {
		Result   []map[string]any `json:"result"`
		PageHelp struct {
			Data []map[string]any `json:"data"`
		} `json:"pageHelp"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return MarginRow{}, err
	}
	values := payload.Result
	if len(values) == 0 {
		values = payload.PageHelp.Data
	}
	if len(values) == 0 {
		return MarginRow{}, errors.New("empty SSE margin response")
	}
	value := values[0]
	date := normalizeMarginDate(anyString(value["opDate"]))
	if requestedDate != "" && date != requestedDate {
		return MarginRow{}, errors.New("SSE margin date mismatch")
	}
	return MarginRow{Code: "SSE", Name: "上海证券交易所", Date: date, Financing: floatAny(value, "rzye"), Securities: floatAny(value, "rqylje"), MarginBalance: floatAny(value, "rzrqjyzl"), FinancingBuy: floatAny(value, "rzmre"), SecuritiesSell: floatAny(value, "rqmcl")}, nil
}

func (p *officialMarginProvider) fetchSZSE(ctx context.Context, requestedDate string) (MarginRow, error) {
	params := map[string]string{"SHOWTYPE": "JSON", "CATALOGID": "1837_xxpl", "TABKEY": "tab1"}
	if requestedDate != "" {
		params["txtDate"] = requestedDate
	}
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "https://www.szse.cn/disclosure/margin/object/index.html").SetHeader("User-Agent", marketEvidenceUserAgent()).SetQueryParams(params).Get(p.service.urls.marginSZSE)
	if err != nil {
		return MarginRow{}, err
	}
	if response.StatusCode() >= 400 {
		return MarginRow{}, fmt.Errorf("HTTP %d", response.StatusCode())
	}
	return parseSZSEMargin(response.Body(), requestedDate)
}

func parseSZSEMargin(body []byte, requestedDate string) (MarginRow, error) {
	var payload []struct {
		Metadata map[string]any   `json:"metadata"`
		Data     []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return MarginRow{}, err
	}
	if len(payload) == 0 || len(payload[0].Data) == 0 {
		return MarginRow{}, errors.New("empty SZSE margin response")
	}
	date := normalizeMarginDate(anyString(payload[0].Metadata["subname"]))
	if date == "" {
		date = requestedDate
	}
	if requestedDate != "" && date != requestedDate {
		return MarginRow{}, errors.New("SZSE margin date mismatch")
	}
	value := payload[0].Data[0]
	return MarginRow{Code: "SZSE", Name: "深圳证券交易所", Date: date, Financing: scaledMarginValue(value["jrrzye"], 1e8), Securities: scaledMarginValue(value["jrrjye"], 1e8), MarginBalance: scaledMarginValue(value["jrrzrjye"], 1e8), FinancingBuy: scaledMarginValue(value["jrrzmr"], 1e8), SecuritiesSell: scaledMarginValue(value["jrrjmc"], 1e8)}, nil
}

type eastmoneyMarginProvider struct{ service *MarketEvidenceService }

func (p *eastmoneyMarginProvider) Name() string { return "eastmoney" }
func (p *eastmoneyMarginProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[MarginData] {
	now := p.service.now()
	digits := strings.TrimPrefix(strings.TrimPrefix(request.Code, "sh"), "sz")
	suffix := ".SZ"
	if strings.HasPrefix(request.Code, "sh") {
		suffix = ".SH"
	}
	params := map[string]string{"reportName": "RPT_RZRQ_STOCKS_DETAIL", "columns": "MARKET_NAME,MARKET_CODE,TRADE_DATE,SECURITY_CODE,SECUCODE,SECURITY_NAME_ABBR,FIN_BALANCE,FIN_BUY_AMT,FIN_REPAY_AMT,LOAN_BALANCE,LOAN_SELL_VOL,LOAN_REPAY_VOL,MARGIN_BALANCE,LOAN_BALANCE_VOL,FIN_NETBUY_AMT", "sortColumns": "TRADE_DATE", "sortTypes": "-1", "pageNumber": "1", "pageSize": "50", "source": "Datacenter", "client": "PC", "filter": fmt.Sprintf("(SECUCODE=\"%s%s\")", digits, suffix)}
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "https://data.eastmoney.com/rzrq/").SetHeader("User-Agent", marketEvidenceUserAgent()).SetQueryParams(params).Get(p.service.urls.marginSecurity)
	if err != nil {
		return providerFailure[MarginData](now, p.service.urls.marginSecurity, err)
	}
	if response.StatusCode() >= 400 {
		return providerFailure[MarginData](now, p.service.urls.marginSecurity, fmt.Errorf("HTTP %d", response.StatusCode()))
	}
	rows, err := parseEastmoneyMargin(response.Body(), request.Date)
	if err != nil {
		return providerFailure[MarginData](now, p.service.urls.marginSecurity, err)
	}
	available := p.service.now()
	return marketdata.ProviderResult[MarginData]{Status: marketdata.StatusOK, AsOf: now, AvailableAt: &available, Data: MarginData{Scope: request.Scope, Rows: rows}, SourceRef: p.service.urls.marginSecurity}
}

func parseEastmoneyMargin(body []byte, requestedDate string) ([]MarginRow, error) {
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Result  struct {
			Data []map[string]any `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !payload.Success && payload.Message != "" {
		return nil, errors.New(payload.Message)
	}
	rows := make([]MarginRow, 0, len(payload.Result.Data))
	for _, value := range payload.Result.Data {
		date := normalizeMarginDate(anyString(firstAny(value, "TRADE_DATE", "DIM_DATE", "DATE")))
		if requestedDate != "" && date != requestedDate {
			continue
		}
		rows = append(rows, MarginRow{Code: anyString(firstAny(value, "SCODE", "SECURITY_CODE")), Name: anyString(firstAny(value, "SECNAME", "SECURITY_NAME_ABBR")), Date: date, Financing: floatAny(value, "RZYE", "FIN_BALANCE"), Securities: floatAny(value, "RQYE", "LOAN_BALANCE"), MarginBalance: floatAny(value, "RZRQYE", "MARGIN_BALANCE"), FinancingBuy: floatAny(value, "RZMRE", "FIN_BUY_AMT"), SecuritiesSell: floatAny(value, "RQMCL", "LOAN_SELL_VOL")})
	}
	if len(rows) == 0 {
		return nil, errors.New("empty or unmatched margin response")
	}
	return rows, nil
}

func normalizeMarginDate(value string) string {
	value = strings.TrimSpace(value)
	for start := 0; start+10 <= len(value); start++ {
		if parsed, err := time.Parse("2006-01-02", value[start:start+10]); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	if len(value) == 8 {
		if parsed, err := time.Parse("20060102", value); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return ""
}

func scaledMarginValue(value any, scale float64) float64 {
	clean := html.UnescapeString(anyString(value))
	clean = strings.ReplaceAll(clean, "\u00a0", "")
	clean = strings.ReplaceAll(clean, "&nbsp;", "")
	parsed, ok := parseFloat(clean)
	if !ok {
		return 0
	}
	return parsed * scale
}

type eastmoneyAuctionProvider struct{ service *MarketEvidenceService }

func (p *eastmoneyAuctionProvider) Name() string { return "eastmoney" }
func (p *eastmoneyAuctionProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[AuctionData] {
	today := p.service.now().In(shanghaiDataLocation()).Format("2006-01-02")
	if request.Date != "" && request.Date != today {
		return providerFailure[AuctionData](p.service.now(), p.service.urls.details, errors.New("公开逐笔接口不支持可靠历史集合竞价回放"))
	}
	result := (&eastmoneyTradesProvider{service: p.service}).collectAll(ctx, request)
	if result.Err != nil {
		return providerFailure[AuctionData](p.service.now(), p.service.urls.details, result.Err)
	}
	snapshots := make([]AuctionSnapshot, 0)
	for _, tick := range result.Data.Items {
		if tick.Time >= "09:15:00" && tick.Time < "09:30:00" {
			snapshots = append(snapshots, AuctionSnapshot{Time: tick.Time, Price: tick.Price, MatchedVolume: tick.Volume, MatchedAmount: tick.Amount})
		}
	}
	if len(snapshots) == 0 {
		return providerFailure[AuctionData](p.service.now(), p.service.urls.details, errors.New("当前来源未返回集合竞价时段快照"))
	}
	finalSnapshot := snapshots[len(snapshots)-1]
	var strength, gap *float64
	if snapshots[0].Price > 0 {
		value := (finalSnapshot.Price - snapshots[0].Price) / snapshots[0].Price * 100
		strength = &value
	}
	if result.Data.PreviousClose > 0 {
		value := (finalSnapshot.Price - result.Data.PreviousClose) / result.Data.PreviousClose * 100
		gap = &value
	}
	available := p.service.now()
	return marketdata.ProviderResult[AuctionData]{Status: marketdata.StatusPartial, AsOf: result.AsOf, AvailableAt: &available, Data: AuctionData{Code: request.Code, AssetType: request.AssetType, Date: today, Snapshots: snapshots, FinalSnapshot: &finalSnapshot, AuctionStrength: strength, GapPct: gap}, SourceRef: p.service.urls.details, Warning: "auctionStrength 为竞价首末价格变化百分比；公开来源仅提供已撮合逐笔，未匹配量不可用"}
}

type eastmoneyTradesProvider struct{ service *MarketEvidenceService }

func (p *eastmoneyTradesProvider) Name() string { return "eastmoney" }
func (p *eastmoneyTradesProvider) Collect(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[TradesData] {
	result := p.collectAll(ctx, request)
	if result.Err != nil {
		return result
	}
	offset, _ := strconv.Atoi(request.Cursor)
	if offset < 0 {
		offset = 0
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}
	total := len(result.Data.Items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	result.Data.Items = result.Data.Items[offset:end]
	if end < total {
		result.Data.NextCursor = strconv.Itoa(end)
	}
	return result
}
func (p *eastmoneyTradesProvider) collectAll(ctx context.Context, request marketdata.ProviderRequest) marketdata.ProviderResult[TradesData] {
	now := p.service.now()
	today := now.In(shanghaiDataLocation()).Format("2006-01-02")
	if request.Date != "" && request.Date != today {
		return providerFailure[TradesData](now, p.service.urls.details, errors.New("公开逐笔接口不支持可靠历史日期查询"))
	}
	secid, err := eastmoneySecurityID(request.Code, request.AssetType)
	if err != nil {
		return providerFailure[TradesData](now, p.service.urls.details, err)
	}
	response, err := p.service.client.R().SetContext(ctx).SetHeader("Referer", "https://quote.eastmoney.com/").SetHeader("User-Agent", marketEvidenceUserAgent()).SetQueryParams(map[string]string{"secid": secid, "fields1": "f1,f2,f3,f4", "fields2": "f51,f52,f53,f54,f55", "pos": "-2000"}).Get(p.service.urls.details)
	if err != nil {
		return providerFailure[TradesData](now, p.service.urls.details, err)
	}
	if response.StatusCode() >= 400 {
		return providerFailure[TradesData](now, p.service.urls.details, fmt.Errorf("HTTP %d", response.StatusCode()))
	}
	items, previousClose, err := parseEastmoneyTradesPayload(response.Body())
	if err != nil {
		return providerFailure[TradesData](now, p.service.urls.details, err)
	}
	available := p.service.now()
	return marketdata.ProviderResult[TradesData]{Status: marketdata.StatusOK, AsOf: now, AvailableAt: &available, Data: TradesData{Code: request.Code, AssetType: request.AssetType, Date: today, Items: items, PreviousClose: previousClose}, SourceRef: p.service.urls.details}
}

func parseEastmoneyTrades(body []byte) ([]TradeTick, error) {
	items, _, err := parseEastmoneyTradesPayload(body)
	return items, err
}

func parseEastmoneyTradesPayload(body []byte) ([]TradeTick, float64, error) {
	var payload struct {
		Data struct {
			Details  []string `json:"details"`
			PrePrice float64  `json:"prePrice"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, err
	}
	items := make([]TradeTick, 0, len(payload.Data.Details))
	for _, detail := range payload.Data.Details {
		fields := strings.Split(detail, ",")
		if len(fields) < 3 {
			continue
		}
		price, ok := parseFloat(fields[1])
		if !ok {
			continue
		}
		volume, ok := parseFloat(fields[2])
		if !ok {
			continue
		}
		side := ""
		if len(fields) >= 5 {
			switch strings.TrimSpace(fields[4]) {
			case "1", "B":
				side = "buy"
			case "2", "S":
				side = "sell"
			case "4", "N":
				side = "neutral"
			}
		}
		items = append(items, TradeTick{Time: normalizeTickTime(fields[0]), Price: price, Volume: volume, Amount: price * volume * 100, Side: side})
	}
	if len(items) == 0 {
		return nil, 0, errors.New("empty trade details response")
	}
	return items, payload.Data.PrePrice, nil
}

func providerFailure[T any](asOf time.Time, sourceRef string, err error) marketdata.ProviderResult[T] {
	var zero T
	return marketdata.ProviderResult[T]{Status: marketdata.StatusUnavailable, AsOf: asOf, Data: zero, SourceRef: sourceRef, Warning: err.Error(), Err: err}
}

func eastmoneySecurityID(code, assetType string) (string, error) {
	normalized, ok := instruments.NormalizeInstrumentID(code, assetType)
	if !ok {
		return "", errors.New("invalid Shanghai/Shenzhen instrument code")
	}
	market := "0"
	if strings.HasPrefix(normalized, "sh") {
		market = "1"
	}
	return market + "." + normalized[2:], nil
}

func normalizeTickTime(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 6 && !strings.Contains(value, ":") {
		return value[:2] + ":" + value[2:4] + ":" + value[4:]
	}
	return value
}

func marketEvidenceUserAgent() string {
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
}

func firstAny(value map[string]any, keys ...string) any {
	for _, key := range keys {
		if item, ok := value[key]; ok {
			return item
		}
	}
	return nil
}
func floatAny(value map[string]any, keys ...string) float64 {
	item, _ := anyFloat(firstAny(value, keys...))
	return item
}
func floatAnyPointer(value map[string]any, keys ...string) *float64 {
	item, ok := anyFloat(firstAny(value, keys...))
	if !ok {
		return nil
	}
	return &item
}
func anyString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
func anyFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		return typed, true
	case json.Number:
		return parseFloat(string(typed))
	case string:
		return parseFloat(typed)
	default:
		return parseFloat(fmt.Sprint(value))
	}
}
func parseFloat(value string) (float64, bool) {
	result, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(value, ",", "")), 64)
	return result, err == nil
}
