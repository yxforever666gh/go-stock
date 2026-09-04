package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/models"
	"go-stock/internal/marketquote"
	"go-stock/internal/researchevidence"
	"go-stock/internal/trading"
)

type ResearchQuoteProvider struct{ stocks *StockDataApi }

func NewResearchQuoteProvider() *ResearchQuoteProvider {
	return NewResearchQuoteProviderWithStockData(NewStockDataApi())
}

func NewResearchQuoteProviderWithStockData(stocks *StockDataApi) *ResearchQuoteProvider {
	if stocks == nil {
		stocks = NewStockDataApi()
	}
	return &ResearchQuoteProvider{stocks: stocks}
}

func (provider *ResearchQuoteProvider) CurrentQuote(ctx context.Context, code string) (marketquote.Quote, error) {
	normalized, ok := trading.NormalizeMainlandCode(code)
	if !ok {
		return marketquote.Quote{}, errors.New("only Shanghai/Shenzhen A shares are supported")
	}
	rows, err := provider.stocks.GetStockCodeRealTimeDataReadOnly(ctx, normalized)
	if err != nil {
		return marketquote.Quote{}, err
	}
	if rows == nil || len(*rows) != 1 {
		return marketquote.Quote{}, errors.New("realtime quote is unavailable")
	}
	row := (*rows)[0]
	rowCode, err := validateResearchQuoteResponseCode(normalized, row.Code)
	if err != nil {
		return marketquote.Quote{}, err
	}
	price, err := strconv.ParseFloat(strings.TrimSpace(row.Price), 64)
	if err != nil || price <= 0 {
		return marketquote.Quote{}, errors.New("realtime quote price is invalid")
	}
	previousClose, _ := strconv.ParseFloat(strings.TrimSpace(row.PreClose), 64)
	quoteText := strings.TrimSpace(row.Date) + " " + strings.TrimSpace(row.Time)
	var quoteAt time.Time
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "20060102 15:04:05", "20060102 15:04"} {
		parsed, parseErr := time.ParseInLocation(layout, quoteText, shanghaiDataLocation())
		if parseErr == nil {
			quoteAt = parsed
			break
		}
	}
	if quoteAt.IsZero() {
		return marketquote.Quote{}, errors.New("realtime quote time is invalid")
	}
	volume, _ := strconv.ParseFloat(strings.TrimSpace(row.Volume), 64)
	amount, _ := strconv.ParseFloat(strings.TrimSpace(row.Amount), 64)
	market := "SZ"
	if strings.HasPrefix(normalized, "sh") {
		market = "SH"
	}
	limitRate := 0.10
	digits := normalized[2:]
	if strings.HasPrefix(digits, "30") || strings.HasPrefix(digits, "68") {
		limitRate = 0.20
	}
	if strings.Contains(strings.ToUpper(row.Name), "ST") {
		limitRate = 0.05
	}
	limitUp, limitDown := false, false
	if previousClose > 0 {
		up := math.Floor(previousClose*(1+limitRate)*100+0.5) / 100
		down := math.Floor(previousClose*(1-limitRate)*100+0.5) / 100
		limitUp, limitDown = price >= up-0.001, price <= down+0.001
	}
	return marketquote.Quote{Code: rowCode, Name: strings.TrimSpace(row.Name), Market: market, Price: price, PreviousClose: previousClose,
		Volume: volume, Amount: amount, At: quoteAt, Suspended: volume == 0, LimitUp: limitUp, LimitDown: limitDown}, nil
}

func validateResearchQuoteResponseCode(requested, response string) (string, error) {
	requestedCode, requestedOK := trading.NormalizeMainlandCode(requested)
	responseCode, responseOK := trading.NormalizeMainlandCode(response)
	if !requestedOK || !responseOK {
		return "", errors.New("realtime quote response code is invalid")
	}
	if requestedCode != responseCode {
		return "", fmt.Errorf("realtime quote response code %s does not match request %s", responseCode, requestedCode)
	}
	return responseCode, nil
}

type ResearchTradingCalendar struct{}

func (ResearchTradingCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	return IsCNOpenTradeDayStrict(value)
}

// IsTradingDayCached is used by the read-only chart endpoint. A cache miss is
// reported as unknown so the caller can use a non-network weekday fallback;
// only an explicit chart refresh is allowed to populate the remote calendar.
func (ResearchTradingCalendar) IsTradingDayCached(value time.Time) (bool, bool) {
	local := value.In(cnLocation())
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return false, true
	}
	return globalCNTradeCalCache.lookup(local)
}

type ResearchSourceCollector struct {
	news              *MarketNewsApi
	stocks            *StockDataApi
	clock             func() time.Time
	refreshMarketNews func(context.Context)
	newsWindowMu      sync.Mutex
	newsWindowKey     string
	newsWindow        NewsWindowResult
	newsWindowErr     error
}

type researchSourceJob struct {
	name string
	run  func() any
}

func NewResearchSourceCollector() *ResearchSourceCollector {
	return NewResearchSourceCollectorWithProviders(NewMarketNewsApi(), NewStockDataApi())
}

func NewResearchSourceCollectorWithProviders(news *MarketNewsApi, stocks *StockDataApi) *ResearchSourceCollector {
	if news == nil {
		news = NewMarketNewsApi()
	}
	if stocks == nil {
		stocks = NewStockDataApi()
	}
	return &ResearchSourceCollector{
		news:   news,
		stocks: stocks,
		clock:  time.Now,
		refreshMarketNews: func(ctx context.Context) {
			news.RefreshResearchNews(ctx, 15*time.Second)
		},
	}
}

func (collector *ResearchSourceCollector) collectedAt() time.Time {
	if collector != nil && collector.clock != nil {
		return collector.clock()
	}
	return time.Now()
}

func (collector *ResearchSourceCollector) CollectMarket(ctx context.Context, now time.Time) ([]researchevidence.SourceDocument, error) {
	if collector != nil && collector.refreshMarketNews != nil {
		collector.refreshMarketNews(ctx)
	}
	jobs := []researchSourceJob{
		{"财联社/Sina/TradingView市场快讯", func() any {
			result, err := collector.news.GetNewsWindow(nil, now.Add(-24*time.Hour), now)
			if err != nil {
				return map[string]any{"error": err.Error(), "result": result}
			}
			return result
		}},
		{"全球与国内指数", func() any { return collector.news.GlobalStockIndexes(10) }},
		{"Reuters", func() any { return collector.news.ReutersNew() }},
		{"东方财富GDP", func() any { return collector.news.GetGDP() }},
		{"东方财富CPI", func() any { return collector.news.GetCPI() }},
		{"东方财富PPI", func() any { return collector.news.GetPPI() }},
		{"东方财富PMI", func() any { return collector.news.GetPMI() }},
		{"九阳公社投资日历", func() any { return collector.news.InvestCalendar(now.Format("2006-01")) }},
		{"财联社投资日历", func() any { return collector.news.ClsCalendar() }},
	}
	return runResearchSourceJobs(ctx, "market", collector.collectedAt, jobs)
}

func (collector *ResearchSourceCollector) CollectSectors(ctx context.Context, now time.Time) ([]researchevidence.SourceDocument, error) {
	jobs := []researchSourceJob{
		{"腾讯行业排名", func() any { return collector.news.GetIndustryRank("changepercent", 30) }},
		{"Sina行业资金", func() any { return collector.news.GetIndustryMoneyRankSina("gn", "netamount") }},
		{"Sina个股资金", func() any { return collector.news.GetMoneyRankSina("netamount") }},
		{"雪球沪深热股", func() any { return collector.news.XUEQIUHotStock(50, "10") }},
		{"东方财富热点事件", func() any { return collector.news.HotEvent(30) }},
		{"东方财富热门话题", func() any { return collector.news.HotTopic(30) }},
		{"东方财富龙虎榜", func() any { return collector.news.LongTiger(now.Format("20060102")) }},
	}
	return runResearchSourceJobs(ctx, "sector", collector.collectedAt, jobs)
}

func (collector *ResearchSourceCollector) CollectStocks(ctx context.Context, now time.Time, candidates []researchevidence.StockCandidate) ([]researchevidence.SourceDocument, error) {
	type candidateDocuments struct {
		documents []researchevidence.SourceDocument
	}
	results := make(chan candidateDocuments, len(candidates))
	for _, candidate := range candidates {
		candidate := candidate
		go func() {
			if ctx.Err() != nil {
				results <- candidateDocuments{}
				return
			}
			code := candidate.Code
			digits := strings.TrimPrefix(strings.TrimPrefix(code, "sh"), "sz")
			items := []researchSourceJob{
				{"相关市场新闻 " + code, func() any { return collector.stockRelatedNews(now, candidate) }},
				{"Sina/Tencent实时行情 " + code, func() any {
					rows, err := collector.stocks.GetStockCodeRealTimeDataReadOnly(ctx, code)
					if err != nil {
						return map[string]any{"error": err.Error()}
					}
					return rows
				}},
				{"Sina日K " + code, func() any { return collector.stocks.GetKLineData(code, "240", 61) }},
				{"Tencent分钟K " + code, func() any {
					rows, source := collector.stocks.GetStockMinutePriceData(code)
					return map[string]any{"source": source, "rows": rows}
				}},
				{"东方财富公告 " + code, func() any { return collector.news.StockNotice(digits) }},
				{"东方财富研报 " + code, func() any { return collector.news.StockResearchReportAt(digits, 30, now) }},
				{"东方财富财务 " + code, func() any { return collector.stocks.GetStockFinancialInfo(digits) }},
				{"东方财富概念 " + code, func() any { return collector.stocks.GetStockConceptInfo(digits) }},
				{"Sina资金流 " + code, func() any { return collector.news.GetStockMoneyTrendByDay(code, 10) }},
				{"巨潮互动易 " + code, func() any { return collector.news.InteractiveAnswer(1, 30, candidate.Name) }},
			}
			local := make([]researchevidence.SourceDocument, 0, len(items))
			for _, item := range items {
				local = append(local, executeResearchSourceJob(item.name, "stock", collector.collectedAt, item.run))
			}
			results <- candidateDocuments{documents: local}
		}()
	}
	documents := make([]researchevidence.SourceDocument, 0, len(candidates)*9)
	for range candidates {
		select {
		case result := <-results:
			documents = append(documents, result.documents...)
		case <-ctx.Done():
			return documents, ctx.Err()
		}
	}
	return documents, nil
}

func (collector *ResearchSourceCollector) stockRelatedNews(now time.Time, candidate researchevidence.StockCandidate) any {
	window, err := collector.cachedNewsWindow(now)
	if err != nil {
		return map[string]any{"error": err.Error(), "status": window.Status, "warning": window.Warning}
	}
	digits := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(candidate.Code), "sh"), "sz")
	name := strings.TrimSpace(candidate.Name)
	matched := make([]*models.Telegraph, 0, 30)
	for _, item := range window.Items {
		if item == nil {
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{item.Title, item.Content, strings.Join(item.SubjectTags, " "), strings.Join(item.StocksTags, " ")}, " "))
		if (digits != "" && strings.Contains(haystack, digits)) || (name != "" && strings.Contains(haystack, strings.ToLower(name))) {
			matched = append(matched, item)
			if len(matched) == 30 {
				break
			}
		}
	}
	return map[string]any{"status": window.Status, "warning": window.Warning, "items": matched}
}

func (collector *ResearchSourceCollector) cachedNewsWindow(now time.Time) (NewsWindowResult, error) {
	key := now.Format(time.RFC3339Nano)
	collector.newsWindowMu.Lock()
	defer collector.newsWindowMu.Unlock()
	if collector.newsWindowKey == key {
		return collector.newsWindow, collector.newsWindowErr
	}
	collector.newsWindow, collector.newsWindowErr = collector.news.GetNewsWindow(nil, now.Add(-72*time.Hour), now)
	collector.newsWindowKey = key
	return collector.newsWindow, collector.newsWindowErr
}

func runResearchSourceJobs(ctx context.Context, category string, clock func() time.Time, jobs []researchSourceJob) ([]researchevidence.SourceDocument, error) {
	type indexedDocument struct {
		index    int
		document researchevidence.SourceDocument
	}
	results := make(chan indexedDocument, len(jobs))
	for index := range jobs {
		index := index
		go func() {
			if ctx.Err() != nil {
				available := researchSourceNow(clock)
				results <- indexedDocument{index: index, document: researchevidence.SourceDocument{SourceName: jobs[index].name, Category: category, CollectedAt: available, AvailableAt: &available, Error: ctx.Err().Error()}}
				return
			}
			results <- indexedDocument{index: index, document: executeResearchSourceJob(jobs[index].name, category, clock, jobs[index].run)}
		}()
	}
	documents := make([]researchevidence.SourceDocument, len(jobs))
	for range jobs {
		select {
		case result := <-results:
			documents[result.index] = result.document
		case <-ctx.Done():
			return compactResearchSourceDocuments(documents), ctx.Err()
		}
	}
	return documents, nil
}

func compactResearchSourceDocuments(documents []researchevidence.SourceDocument) []researchevidence.SourceDocument {
	result := make([]researchevidence.SourceDocument, 0, len(documents))
	for _, document := range documents {
		if document.SourceName != "" || document.Error != "" || document.Content != "" {
			result = append(result, document)
		}
	}
	return result
}

func executeResearchSourceJob(name, category string, clock func() time.Time, run func() any) (document researchevidence.SourceDocument) {
	document = researchevidence.SourceDocument{SourceName: name, Category: category}
	defer func() {
		available := researchSourceNow(clock)
		document.CollectedAt = available
		document.AvailableAt = &available
		if recovered := recover(); recovered != nil {
			document.Content = ""
			document.Error = fmt.Sprintf("来源调用异常: %v", recovered)
		}
	}()
	if run == nil {
		document.Error = "来源任务为空"
		return document
	}
	value := run()
	return researchDocument(name, category, researchSourceNow(clock), value)
}

func researchSourceNow(clock func() time.Time) time.Time {
	if clock != nil {
		return clock()
	}
	return time.Now()
}

func researchDocument(name, category string, now time.Time, value any) researchevidence.SourceDocument {
	document := researchevidence.SourceDocument{SourceName: name, Category: category, CollectedAt: now}
	if value == nil {
		document.Error = "来源返回空值"
		return document
	}
	data, err := json.Marshal(value)
	if err != nil {
		document.Error = err.Error()
		return document
	}
	document.Error = semanticResearchSourceError(data)
	if category == "stock" {
		document.PromptContent = compactResearchPromptValue(name, value)
		document.Content = document.PromptContent
		if freshnessErr := validateCompactStockSourceAt(name, document.PromptContent, now); freshnessErr != nil {
			document.Error = appendSourceDocumentError(document.Error, freshnessErr.Error())
		}
	} else {
		document.Content = truncateResearchSourceJSON(string(data), 16000)
	}
	return document
}

func appendSourceDocumentError(existing, message string) string {
	existing, message = strings.TrimSpace(existing), strings.TrimSpace(message)
	if existing == "" {
		return message
	}
	if message == "" {
		return existing
	}
	return existing + "; " + message
}

func semanticResearchSourceError(data []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return ""
	}
	readText := func(key string) string {
		raw, ok := fields[key]
		if !ok || string(raw) == "null" {
			return ""
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(string(raw))
	}
	format := func(prefix string) string {
		parts := []string{prefix}
		if message := readText("message"); message != "" {
			parts = append(parts, message)
		} else if warning := readText("warning"); warning != "" {
			parts = append(parts, warning)
		}
		if code := readText("code"); code != "" && code != "0" {
			parts = append(parts, "code="+code)
		}
		return truncateResearchError(strings.Join(parts, ": "))
	}
	if raw, exists := fields["error"]; exists && string(raw) != "null" {
		var sourceError string
		if json.Unmarshal(raw, &sourceError) == nil {
			if sourceError = strings.TrimSpace(sourceError); sourceError != "" {
				return truncateResearchError(sourceError)
			}
		} else {
			var errorFlag bool
			if json.Unmarshal(raw, &errorFlag) != nil || errorFlag {
				return truncateResearchError(strings.TrimSpace(string(raw)))
			}
		}
	}
	if raw, exists := fields["success"]; exists {
		var success bool
		if json.Unmarshal(raw, &success) == nil && !success {
			return format("来源返回失败")
		}
	}
	switch strings.ToLower(readText("status")) {
	case "failed":
		return format("来源状态失败")
	case "stale":
		return format("来源数据已过期")
	}
	return ""
}

func truncateResearchError(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	const maxRunes = 512
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return value
}

func truncateResearchSourceJSON(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		encoded, _ := json.Marshal(truncatePromptString(value, maxBytes/2))
		return string(encoded)
	}
	for level := 1; level <= 7; level++ {
		encoded, err := json.Marshal(compactJSONAtLevel(decoded, level))
		if err == nil && len(encoded) <= maxBytes {
			return string(encoded)
		}
	}
	return `{"truncated":true}`
}

func compactJSONAtLevel(value any, level int) any {
	arrayLimits := []int{0, 30, 20, 10, 5, 3, 1, 0}
	stringLimits := []int{0, 1000, 600, 300, 160, 80, 40, 0}
	if level < 1 {
		level = 1
	}
	if level >= len(arrayLimits) {
		level = len(arrayLimits) - 1
	}
	switch typed := value.(type) {
	case []any:
		limit := arrayLimits[level]
		if limit == 0 {
			return []any{}
		}
		if len(typed) > limit {
			typed = typed[:limit]
		}
		result := make([]any, 0, len(typed))
		for _, child := range typed {
			result = append(result, compactJSONAtLevel(child, level))
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = compactJSONAtLevel(child, level)
		}
		return result
	case string:
		limit := stringLimits[level]
		if limit == 0 {
			return ""
		}
		return truncatePromptString(typed, limit)
	default:
		return value
	}
}

func shanghaiDataLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}

func ResolveAIAnalysisConfig(setting *SettingConfig) (*models.AIConfig, error) {
	if setting == nil {
		setting = GetSettingConfig()
	}
	config := SelectPrimaryAIConfig(setting.AiConfigs)
	if config == nil {
		return nil, errors.New("没有已启用的 AI 模型")
	}
	return config, nil
}
