package data

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/ai"
	"go-stock/backend/marketdata"
	"go-stock/backend/research2"
	"go-stock/backend/research2app"
	"go-stock/backend/researchaudit"
	"go-stock/backend/themes"
	"go-stock/internal/researchevidence"
	"go-stock/internal/trading"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewResearch2Dependencies builds only the concrete infrastructure adapters;
// runtime service composition belongs to backend/research2app.
func NewResearch2Dependencies(configID int, mainDB, minuteDB *gorm.DB) (research2app.Dependencies, error) {
	if mainDB == nil {
		return research2app.Dependencies{}, errors.New("database is not initialized")
	}
	stocks := NewStockDataApi()
	news := NewMarketNewsApi()
	quoteProvider := NewResearchQuoteProviderWithStockData(stocks)
	calendar := ResearchTradingCalendar{}
	sources := NewResearchSourceCollectorWithProviders(news, stocks)
	marketEvidence := NewMarketEvidenceServiceWithStorage(mainDB, minuteDB)
	chartProvider := NewResearchChartProviderWithStorage(quoteProvider, minuteDB)
	collector := &research2EvidenceCollector{
		sources: sources, stocks: stocks, market: marketEvidence,
		minuteWindows: &research2DefaultMinuteWindowProvider{stocks: stocks, cache: chartProvider},
		themes:        newThemeEvidenceReader(mainDB),
	}
	market := &research2MarketProvider{quotes: quoteProvider, stocks: stocks, cache: chartProvider}
	return research2app.Dependencies{
		Quotes: quoteProvider, Chart: chartProvider, ChartCalendar: calendar,
		AI: ai.NewResearchClient(configID, ResearchAIClientOptions()), Evidence: collector, EvidenceStore: marketdata.NewRepository(mainDB),
		EvidenceBuild: buildResearch2EvidenceItem, EvidenceProfile: research2EvidenceProfileV7,
		Calendar: calendar, Market: market,
		Audit: researchaudit.NewRecorder(researchaudit.NewRepository(mainDB)), Knowledge: NewKnowledgeService(mainDB),
	}, nil
}

type research2MarketRow struct {
	Price       float64 `json:"f2"`
	ChangeRate  float64 `json:"f3"`
	ChangeValid bool    `json:"-"`
	Volume      float64 `json:"f5"`
	Amount      float64 `json:"f6"`
	Turnover    float64 `json:"f8"`
	Code        string  `json:"f12"`
	Market      int     `json:"f13"`
	Name        string  `json:"f14"`
	High        float64 `json:"f15"`
	Low         float64 `json:"f16"`
	Open        float64 `json:"f17"`
	PreClose    float64 `json:"f18"`
	MainFlow    float64 `json:"f62"`
	Timestamp   int64   `json:"f124"`
	ListingDate int64   `json:"f26"`
}

type research2MarketResponse struct {
	Data struct {
		Total int                 `json:"total"`
		Diff  research2MarketRows `json:"diff"`
	} `json:"data"`
}

type research2MarketRows []research2MarketRow

// UnmarshalJSON accepts both response shapes currently emitted by Eastmoney:
// np=1 returns an array, while np=2 returns an object keyed by row number.
// Numeric quote fields may also be "-" for unavailable/suspended instruments;
// those values become zero and are filtered out by candidate selection.
func (rows *research2MarketRows) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return err
	}
	var values []any
	switch typed := payload.(type) {
	case nil:
		*rows = nil
		return nil
	case []any:
		values = typed
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			left, leftErr := strconv.Atoi(keys[i])
			right, rightErr := strconv.Atoi(keys[j])
			if leftErr == nil && rightErr == nil {
				return left < right
			}
			return keys[i] < keys[j]
		})
		values = make([]any, 0, len(keys))
		for _, key := range keys {
			values = append(values, typed[key])
		}
	default:
		return fmt.Errorf("unexpected full-market diff type %T", payload)
	}

	result := make(research2MarketRows, 0, len(values))
	for index, value := range values {
		fields, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected full-market row %d type %T", index, value)
		}
		result = append(result, research2MarketRow{
			Price: research2MarketNumber(fields["f2"]), ChangeRate: research2MarketNumber(fields["f3"]), ChangeValid: research2MarketValueAvailable(fields["f3"]),
			Volume: research2MarketNumber(fields["f5"]), Amount: research2MarketNumber(fields["f6"]),
			Turnover: research2MarketNumber(fields["f8"]), Code: research2MarketText(fields["f12"]),
			Market: int(research2MarketNumber(fields["f13"])), Name: research2MarketText(fields["f14"]),
			High: research2MarketNumber(fields["f15"]), Low: research2MarketNumber(fields["f16"]),
			Open: research2MarketNumber(fields["f17"]), PreClose: research2MarketNumber(fields["f18"]),
			ListingDate: int64(research2MarketNumber(fields["f26"])), MainFlow: research2MarketNumber(fields["f62"]),
			Timestamp: int64(research2MarketNumber(fields["f124"])),
		})
	}
	*rows = result
	return nil
}

func research2MarketNumber(value any) float64 {
	switch typed := value.(type) {
	case json.Number:
		result, _ := typed.Float64()
		return result
	case float64:
		return typed
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return result
	default:
		return 0
	}
}

func research2MarketValueAvailable(value any) bool {
	switch typed := value.(type) {
	case json.Number:
		_, err := typed.Float64()
		return err == nil
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case string:
		_, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return err == nil
	default:
		return false
	}
}

func research2MarketText(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

type research2EvidenceCollector struct {
	sources        researchevidence.SourceCollector
	stocks         *StockDataApi
	market         *MarketEvidenceService
	minuteWindows  research2MinuteWindowProvider
	now            func() time.Time
	fetchSnapshot  func(context.Context, time.Time) (research2FullMarketSnapshot, error)
	fetchFallback  func(context.Context) (research2FullMarketSnapshot, error)
	collectBreadth func(context.Context) marketdata.DataEnvelope[BreadthData]
	collectFlows   func(context.Context, marketdata.ProviderRequest) marketdata.DataEnvelope[[]FundFlowRow]
	themes         themes.EvidenceReader
}

var _ research2app.EvidenceProvider = (*research2EvidenceCollector)(nil)

func (c *research2EvidenceCollector) Collect(ctx context.Context, cutoff time.Time) (research2.Evidence, error) {
	return c.collectStructuredEvidence(ctx, cutoff)
}

func (c *research2EvidenceCollector) CollectWithExclusions(ctx context.Context, cutoff time.Time, excludedCodes map[string]struct{}) (research2.Evidence, error) {
	return c.collectStructuredEvidenceWithExclusions(ctx, cutoff, excludedCodes)
}

func validateResearch2CandidateCutoff(experimental bool, availableAt, cutoff time.Time) error {
	if experimental && availableAt.After(cutoff) {
		return fmt.Errorf("候选输入在证据截止后才可用（availableAt=%s cutoff=%s），实验模式拒绝使用截止后候选", availableAt.Format(time.RFC3339Nano), cutoff.Format(time.RFC3339Nano))
	}
	return nil
}

func research2DocumentAtCutoff(document researchevidence.SourceDocument, cutoff time.Time, experimental bool) researchevidence.SourceDocument {
	if experimental && document.AvailableAt == nil {
		document.Content = ""
		document.Error = "来源未提供可验证的 availableAt，未纳入本次评分"
	} else if experimental && document.AvailableAt.After(cutoff) {
		document.Content = ""
		document.Error = "来源在本次证据截止后才可用，未纳入本次评分"
	} else if !experimental && document.CollectedAt.After(cutoff.Add(time.Minute)) {
		document.Content = ""
		document.Error = "来源在本次证据截止后才完成，未纳入本次评分"
	}
	return document
}

func research2DocumentStatus(document researchevidence.SourceDocument, cutoff time.Time, experimental bool) string {
	if !experimental {
		if strings.TrimSpace(document.Error) != "" {
			return "failed"
		}
		return "ok"
	}
	if document.AvailableAt == nil {
		return marketdata.StatusUnavailable
	}
	if document.AvailableAt.After(cutoff) {
		return marketdata.StatusAfterCutoff
	}
	if strings.TrimSpace(document.Error) != "" {
		return marketdata.StatusFailed
	}
	if embedded := research2DocumentEmbeddedStatus(document); embedded != "" {
		return embedded
	}
	if research2DocumentIsEmpty(document) {
		return marketdata.StatusEmpty
	}
	return marketdata.StatusOK
}
func uniqueResearchEvidenceSourceID(document researchevidence.SourceDocument, index int, used map[string]int) string {
	base := strings.TrimSpace(document.SourceID)
	if base == "" {
		sum := sha256.Sum256([]byte(strings.Join([]string{document.Category, document.SourceName, document.SourceRef}, "\x1f")))
		base = "source-" + hex.EncodeToString(sum[:8])
	}
	used[base]++
	if used[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s-%d-%d", base, used[base], index+1)
}

func researchEvidenceDocumentSummary(document researchevidence.SourceDocument) string {
	content := strings.TrimSpace(document.Content)
	if strings.TrimSpace(document.Error) != "" {
		content = "错误: " + strings.TrimSpace(document.Error)
	}
	if content == "" {
		content = "无可用内容"
	}
	prefix := document.SourceName + " / " + document.Category
	if document.SourceID != "" {
		prefix += " / sourceId=" + document.SourceID
	}
	return limitResearch2Text(prefix+" / "+content, 512)
}

func buildResearch2EvidenceItem(document researchevidence.SourceDocument, cutoff time.Time, index int, usedSourceIDs map[string]int) (marketdata.EvidenceItem, error) {
	payload, err := marketdata.MarshalPayload(map[string]any{"content": document.Content, "error": document.Error})
	if err != nil {
		return marketdata.EvidenceItem{}, err
	}
	entityID := research2SourceEntity(document)
	entityType := ""
	if entityID != "" {
		entityType = "stock"
		entityID = strings.TrimPrefix(entityID, "stock:")
	}
	return marketdata.EvidenceItem{
		EvidenceItemID: uuid.NewString(), SourceID: uniqueResearchEvidenceSourceID(document, index, usedSourceIDs),
		SourceName: document.SourceName, SourceRef: document.SourceRef, Category: document.Category,
		EntityType: entityType, EntityID: entityID, AvailableAt: document.AvailableAt, CollectedAt: document.CollectedAt,
		Status: research2DocumentStatus(document, cutoff, true), Payload: payload,
		Summary: researchEvidenceDocumentSummary(document), ErrorMessage: strings.TrimSpace(document.Error),
	}, nil
}

func (c *research2EvidenceCollector) fetchFullMarket(ctx context.Context) ([]research2MarketRow, error) {
	rows, _, err := c.fetchFullMarketWithReported(ctx)
	return rows, err
}

func (c *research2EvidenceCollector) fetchFullMarketWithReported(ctx context.Context) ([]research2MarketRow, int, error) {
	// np=2 can return the complete market in one number-keyed object. Prefer
	// that low-latency shape, then fall back to capped np=1 pages when a large
	// response is interrupted by an Eastmoney edge server.
	if bulk, bulkErr := c.fetchFullMarketRequest(ctx, 1, 20000, "2", 2); bulkErr == nil && len(bulk.Data.Diff) >= bulk.Data.Total && bulk.Data.Total > 0 {
		return []research2MarketRow(bulk.Data.Diff), bulk.Data.Total, nil
	}
	const pageSize = 100
	first, err := c.fetchFullMarketPage(ctx, 1, pageSize)
	if err != nil {
		return nil, 0, err
	}
	if len(first.Data.Diff) == 0 {
		return nil, first.Data.Total, errors.New("empty full-market response")
	}
	pageCount := (first.Data.Total + pageSize - 1) / pageSize
	if pageCount <= 1 {
		if len(first.Data.Diff) < first.Data.Total {
			return nil, first.Data.Total, fmt.Errorf("incomplete full-market response: got %d of %d rows", len(first.Data.Diff), first.Data.Total)
		}
		return []research2MarketRow(first.Data.Diff), first.Data.Total, nil
	}
	pages := make([][]research2MarketRow, pageCount)
	pages[0] = []research2MarketRow(first.Data.Diff)
	type pageResult struct {
		page int
		rows []research2MarketRow
		err  error
	}
	jobs := make(chan int)
	results := make(chan pageResult, pageCount-1)
	workerCount := 6
	if pageCount-1 < workerCount {
		workerCount = pageCount - 1
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for page := range jobs {
				payload, pageErr := c.fetchFullMarketPage(ctx, page, pageSize)
				results <- pageResult{page: page, rows: []research2MarketRow(payload.Data.Diff), err: pageErr}
			}
		}()
	}
	go func() {
		for page := 2; page <= pageCount; page++ {
			jobs <- page
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	for result := range results {
		if result.err != nil {
			return nil, first.Data.Total, fmt.Errorf("full-market page %d/%d: %w", result.page, pageCount, result.err)
		}
		pages[result.page-1] = result.rows
	}
	rows := make([]research2MarketRow, 0, first.Data.Total)
	for _, page := range pages {
		rows = append(rows, page...)
	}
	if len(rows) < first.Data.Total {
		return nil, first.Data.Total, fmt.Errorf("incomplete full-market response: got %d of %d rows", len(rows), first.Data.Total)
	}
	return rows, first.Data.Total, nil
}

func (c *research2EvidenceCollector) fetchFullMarketPage(ctx context.Context, page, pageSize int) (research2MarketResponse, error) {
	return c.fetchFullMarketRequest(ctx, page, pageSize, "1", 3)
}

func (c *research2EvidenceCollector) fetchFullMarketRequest(ctx context.Context, page, pageSize int, responseShape string, attempts int) (research2MarketResponse, error) {
	endpoints := []string{
		"https://82.push2.eastmoney.com/api/qt/clist/get",
		"https://80.push2.eastmoney.com/api/qt/clist/get",
		"https://push2delay.eastmoney.com/api/qt/clist/get",
	}
	var lastErr error
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		endpoint := endpoints[(page+attempt-2)%len(endpoints)]
		response, err := c.stocks.client.R().SetContext(ctx).SetQueryParams(map[string]string{
			"pn": strconv.Itoa(page), "pz": strconv.Itoa(pageSize), "po": "1", "np": responseShape, "fltt": "2", "invt": "2", "fid": "f3",
			"ut": "bd1d9ddb04089700cf9c27f6f7426281", "_": strconv.FormatInt(time.Now().UnixMilli(), 10),
			"fs":     "m:1+t:2,m:0+t:6,b:MK0021,b:MK0022,b:MK0023,b:MK0024",
			"fields": "f2,f3,f5,f6,f8,f12,f13,f14,f15,f16,f17,f18,f26,f62,f124",
		}).SetHeader("Referer", "https://quote.eastmoney.com/center/gridlist.html").SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36").Get(endpoint)
		if err != nil {
			lastErr = err
		} else if response.StatusCode() >= 400 {
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode())
		} else {
			var payload research2MarketResponse
			if decodeErr := json.Unmarshal(response.Body(), &payload); decodeErr == nil {
				return payload, nil
			} else {
				lastErr = decodeErr
			}
		}
		if attempt < attempts {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return research2MarketResponse{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return research2MarketResponse{}, lastErr
}

func selectResearch2Candidates(rows []research2MarketRow, limit int, asOf time.Time) []researchevidence.StockCandidate {
	return selectResearch2CandidatesWithExclusions(rows, limit, asOf, nil)
}

func selectResearch2CandidatesWithExclusions(rows []research2MarketRow, limit int, asOf time.Time, excludedCodes map[string]struct{}) []researchevidence.StockCandidate {
	excluded := normalizeResearch2ExcludedCodes(excludedCodes)
	eligible := make([]research2MarketRow, 0, len(rows))
	for _, row := range rows {
		name := strings.ToUpper(strings.TrimSpace(row.Name))
		code := strings.TrimSpace(row.Code)
		if row.Price <= 0 || row.PreClose <= 0 || math.IsNaN(row.PreClose) || math.IsInf(row.PreClose, 0) || row.Volume <= 0 || row.Amount <= 0 || row.ChangeRate <= 0 || strings.Contains(name, "ST") || strings.Contains(name, "退") {
			continue
		}
		if !(strings.HasPrefix(code, "60") || strings.HasPrefix(code, "00")) {
			continue
		}
		if _, blocked := excluded[research2CanonicalCode(code)]; blocked {
			continue
		}
		if !listedForResearch2Sessions(row.ListingDate, asOf, 10, IsCNOpenTradeDayStrict) {
			continue
		}
		if _, _, blocked := research2.IsInsideLimitBuffer(row.Price, row.PreClose, research2.SelectionLimitDistancePct); blocked {
			continue
		}
		lot, lotErr := trading.LotSize(code)
		if lotErr != nil || -trading.CalculateBuyCost(row.Price, lot).NetCashFlow > research2.InitialCash {
			continue
		}
		eligible = append(eligible, row)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left := eligible[i].ChangeRate*4 + math.Log10(math.Max(1, eligible[i].Amount))*2 + eligible[i].Turnover
		right := eligible[j].ChangeRate*4 + math.Log10(math.Max(1, eligible[j].Amount))*2 + eligible[j].Turnover
		return left > right
	})
	if len(eligible) > limit {
		eligible = eligible[:limit]
	}
	result := make([]researchevidence.StockCandidate, 0, len(eligible))
	for _, row := range eligible {
		prefix := "sz"
		if strings.HasPrefix(row.Code, "60") {
			prefix = "sh"
		}
		result = append(result, researchevidence.StockCandidate{Code: prefix + row.Code, Name: row.Name})
	}
	return result
}

func normalizeResearch2ExcludedCodes(source map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(source))
	for code := range source {
		canonical := research2CanonicalCode(code)
		if normalized, ok := trading.NormalizeMainlandCode(canonical); ok {
			result[normalized] = struct{}{}
		}
	}
	return result
}

func listedForResearch2Sessions(listingDate int64, asOf time.Time, minimum int, isOpen func(time.Time) (bool, error)) bool {
	if listingDate <= 0 || minimum <= 0 || isOpen == nil {
		return false
	}
	listed, err := time.ParseInLocation("20060102", strconv.FormatInt(listingDate, 10), shanghaiDataLocation())
	if err != nil || listed.After(asOf) {
		return false
	}
	// No mainland exchange closure is long enough to put a listing older than
	// 45 calendar days below ten completed sessions. Avoid walking old history.
	if asOf.Sub(listed) >= 45*24*time.Hour {
		return true
	}
	count := 0
	for day := listed; !day.After(asOf); day = day.AddDate(0, 0, 1) {
		open, openErr := isOpen(day)
		if openErr != nil {
			return false
		}
		if open {
			count++
			if count >= minimum {
				return true
			}
		}
	}
	return false
}

func rowsForCandidates(rows []research2MarketRow, candidates []researchevidence.StockCandidate) []research2MarketRow {
	wanted := make(map[string]struct{}, len(candidates))
	for _, item := range candidates {
		wanted[strings.TrimPrefix(strings.TrimPrefix(item.Code, "sh"), "sz")] = struct{}{}
	}
	result := make([]research2MarketRow, 0, len(wanted))
	for _, row := range rows {
		if _, ok := wanted[row.Code]; ok {
			result = append(result, row)
		}
	}
	return result
}

func limitResearch2Text(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	if max <= 0 {
		return "…"
	}
	cut := max
	for cut > 0 && value[cut]&0xc0 == 0x80 {
		cut--
	}
	return value[:cut] + "…"
}

type research2MarketProvider struct {
	quotes *ResearchQuoteProvider
	stocks *StockDataApi
	cache  *ResearchChartProvider
}

func (p *research2MarketProvider) PriceAt(ctx context.Context, code string, target time.Time, current bool) (research2.PriceSnapshot, error) {
	if current {
		quote, err := p.quotes.CurrentQuote(ctx, code)
		if err != nil {
			return research2.PriceSnapshot{}, err
		}
		return research2.PriceSnapshot{Code: quote.Code, Name: quote.Name, Price: quote.Price, PreviousClose: quote.PreviousClose, At: quote.At, Suspended: quote.Suspended, LimitUp: quote.LimitUp, LimitDown: quote.LimitDown, Source: "tencent_realtime"}, nil
	}
	start, end := target.Add(-time.Minute), target.Add(2*time.Minute)
	bars, source, err := fetchMinuteBarsWithTencentContext(ctx, code, start, end)
	if err != nil || len(bars) == 0 {
		bars, source, err = fetchResearch2EastmoneyMinutes(ctx, p.stocks, code, start, end, 2)
	}
	if (err != nil || len(bars) == 0) && p.cache != nil {
		cachedBars, cacheErr := loadResearch2CachedMinuteBars(ctx, p.cache, code, start, end)
		if cacheErr == nil && len(cachedBars) > 0 {
			bars = cachedBars
			source, err = "local-minute-cache", nil
		} else if err == nil {
			err = cacheErr
		}
	}
	if err != nil {
		return research2.PriceSnapshot{}, err
	}
	bar, ok := nearestResearch2Bar(bars, target)
	if !ok {
		return research2.PriceSnapshot{}, errors.New("target minute price is unavailable")
	}
	result := research2.PriceSnapshot{Code: code, Price: research2BarPrice(bar), At: bar.TradeTime, Source: source}
	if quote, quoteErr := p.quotes.CurrentQuote(ctx, code); quoteErr == nil && math.Abs(quote.At.Sub(target).Minutes()) <= 5 {
		result.Name = quote.Name
		result.PreviousClose = quote.PreviousClose
		result.Suspended = quote.Suspended
		result.LimitUp = quote.LimitUp
		result.LimitDown = quote.LimitDown
	}
	return result, nil
}

func loadResearch2CachedMinuteBars(ctx context.Context, cache *ResearchChartProvider, code string, start, end time.Time) ([]minuteBar, error) {
	if cache == nil {
		return nil, errors.New("local minute cache is unavailable")
	}
	cached, err := cache.LoadCached(ctx, code, start, end)
	if err != nil {
		return nil, err
	}
	bars := make([]minuteBar, 0, len(cached.Bars))
	for _, bar := range cached.Bars {
		bars = append(bars, minuteBar{TradeTime: bar.At, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close,
			Volume: bar.Volume, Amount: bar.Amount, Source: bar.Source})
	}
	return bars, nil
}

func (p *research2MarketProvider) Metrics(ctx context.Context, item research2.Recommendation) (research2.MetricSnapshot, error) {
	if item.BuyAt == nil || item.TargetSellAt == nil {
		return research2.MetricSnapshot{}, errors.New("trade window is incomplete")
	}
	end := time.Date(item.TargetSellAt.Year(), item.TargetSellAt.Month(), item.TargetSellAt.Day(), 15, 0, 0, 0, item.TargetSellAt.Location())
	bars, _, err := fetchMinuteBarsWithTencent(item.StockCode, *item.BuyAt, end)
	if err != nil || len(bars) == 0 {
		bars, _, err = fetchResearch2EastmoneyMinutes(ctx, p.stocks, item.StockCode, *item.BuyAt, end, 5)
	}
	if err != nil || len(bars) == 0 {
		return research2.MetricSnapshot{}, errors.New("metric minute window is unavailable")
	}
	result := research2.MetricSnapshot{}
	var sellDayPreviousClose float64
	if quote, quoteErr := p.quotes.CurrentQuote(ctx, item.StockCode); quoteErr == nil {
		sellDayPreviousClose = quote.PreviousClose
	}
	limitPrice := research2.MainBoardLimitPrice(sellDayPreviousClose)
	for _, bar := range bars {
		if item.TargetSellAt != nil && !bar.TradeTime.After(*item.TargetSellAt) && bar.High >= item.BuyPrice*1.05 {
			result.HitFiveBeforeSell = true
		}
		if item.TargetSellAt != nil && !bar.TradeTime.After(*item.TargetSellAt) && bar.Low <= item.BuyPrice*0.97 {
			result.HitMinusThree = true
		}
		if sellDayPreviousClose > 0 && bar.TradeTime.Format("2006-01-02") == item.TargetSellAt.Format("2006-01-02") && bar.High >= limitPrice-0.001 {
			result.HitLimitUpFullDay = true
		}
	}
	return result, nil
}

type research2TrendsResponse struct {
	Data struct {
		Trends []string `json:"trends"`
	} `json:"data"`
}

func fetchResearch2EastmoneyMinutes(ctx context.Context, stocks *StockDataApi, code string, start, end time.Time, days int) ([]minuteBar, string, error) {
	normalized, ok := trading.NormalizeMainlandCode(code)
	if !ok {
		return nil, "eastmoney", errors.New("invalid A-share code")
	}
	secid := "0." + normalized[2:]
	if strings.HasPrefix(normalized, "sh") {
		secid = "1." + normalized[2:]
	}
	response, err := stocks.client.R().SetContext(ctx).SetQueryParams(map[string]string{"secid": secid, "fields1": "f1,f2,f3,f4,f5,f6,f7,f8", "fields2": "f51,f52,f53,f54,f55,f56,f57,f58", "ndays": strconv.Itoa(days), "iscr": "0"}).Get("https://push2his.eastmoney.com/api/qt/stock/trends2/get")
	if err != nil {
		return nil, "eastmoney", err
	}
	if response.StatusCode() >= 400 {
		return nil, "eastmoney", fmt.Errorf("HTTP %d", response.StatusCode())
	}
	var payload research2TrendsResponse
	if err = json.Unmarshal(response.Body(), &payload); err != nil {
		return nil, "eastmoney", err
	}
	loc := shanghaiDataLocation()
	result := make([]minuteBar, 0, len(payload.Data.Trends))
	for _, line := range payload.Data.Trends {
		fields := strings.Split(line, ",")
		if len(fields) < 8 {
			continue
		}
		at, parseErr := time.ParseInLocation("2006-01-02 15:04", fields[0], loc)
		if parseErr != nil || at.Before(start) || at.After(end) {
			continue
		}
		parse := func(index int) float64 { value, _ := strconv.ParseFloat(fields[index], 64); return value }
		result = append(result, minuteBar{TradeTime: at, Open: parse(1), Close: parse(2), High: parse(3), Low: parse(4), Volume: parse(5), Amount: parse(6), Source: "eastmoney"})
	}
	if len(result) == 0 {
		return nil, "eastmoney", errors.New("empty Eastmoney minute response")
	}
	return dedupeMinuteBars(result), "eastmoney", nil
}

func nearestResearch2Bar(bars []minuteBar, target time.Time) (minuteBar, bool) {
	var selected minuteBar
	ok := false
	for _, bar := range bars {
		if bar.TradeTime.Before(target.Add(-time.Minute)) || bar.TradeTime.After(target.Add(2*time.Minute)) {
			continue
		}
		if !ok || math.Abs(bar.TradeTime.Sub(target).Seconds()) < math.Abs(selected.TradeTime.Sub(target).Seconds()) {
			selected, ok = bar, true
		}
	}
	return selected, ok
}

func research2BarPrice(bar minuteBar) float64 {
	if bar.Amount > 0 && bar.Volume > 0 {
		value := bar.Amount / bar.Volume
		if value > bar.Low*0.8 && value < bar.High*1.2 {
			return value
		}
	}
	if bar.Close > 0 {
		return bar.Close
	}
	return (bar.Open + bar.High + bar.Low) / 3
}
