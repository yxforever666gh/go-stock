package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research"
	"go-stock/internal/researchevidence"
)

const lifecycleSourceTimeout = 20 * time.Second

type lifecycleCachedSource struct {
	name        string
	category    string
	status      string
	content     string
	errorText   string
	fingerprint string
	collectedAt time.Time
}

type ResearchLifecycleContextCollector struct {
	quotes *ResearchQuoteProvider
	stocks *StockDataApi
	news   *MarketNewsApi
	clock  func() time.Time

	cacheMu     sync.Mutex
	cacheBucket string
	marketCache []lifecycleCachedSource
}

func NewResearchLifecycleContextCollector(quotes *ResearchQuoteProvider) *ResearchLifecycleContextCollector {
	return NewResearchLifecycleContextCollectorWithProviders(quotes, NewStockDataApi(), NewMarketNewsApi())
}

func NewResearchLifecycleContextCollectorWithProviders(quotes *ResearchQuoteProvider, stocks *StockDataApi, news *MarketNewsApi) *ResearchLifecycleContextCollector {
	if quotes == nil {
		quotes = NewResearchQuoteProvider()
	}
	if stocks == nil {
		stocks = NewStockDataApi()
	}
	if news == nil {
		news = NewMarketNewsApi()
	}
	return &ResearchLifecycleContextCollector{quotes: quotes, stocks: stocks, news: news, clock: time.Now}
}

func (collector *ResearchLifecycleContextCollector) CollectLifecycleContext(ctx context.Context, request research.LifecycleContextRequest) (research.LifecycleObservationDraft, error) {
	draft := research.LifecycleObservationDraft{Status: "ready", Sources: []research.LifecycleEvidenceSource{}}
	quoteID := research.LifecycleSourceID(request.ObservationID, research.LifecycleQuoteSourceSuffix)
	minuteID := research.LifecycleSourceID(request.ObservationID, research.LifecycleMinuteSourceSuffix)

	quote, quoteErr := collector.quotes.CurrentQuote(ctx, request.Recommendation.StockCode)
	if quoteErr != nil {
		draft.Status, draft.CriticalFailure = "critical_failed", "实时行情不可用: "+quoteErr.Error()
		draft.Sources = append(draft.Sources, failedLifecycleSource(quoteID, "实时行情", "quote", researchSourceNow(collector.clock), quoteErr))
		return draft, nil
	}
	draft.Quote = quote
	quoteContent := map[string]any{
		"code": quote.Code, "name": quote.Name, "price": quote.Price, "previousClose": quote.PreviousClose,
		"changeRate": safeRate(quote.Price-quote.PreviousClose, quote.PreviousClose), "volume": quote.Volume,
		"amount": quote.Amount, "quoteAt": quote.At, "suspended": quote.Suspended, "limitUp": quote.LimitUp, "limitDown": quote.LimitDown,
	}
	draft.Sources = append(draft.Sources, newLifecycleSource(quoteID, "实时行情", "quote", researchSourceNow(collector.clock), quoteContent, nil))

	minuteRows, tradingDate, minuteErr := collector.collectMinute(ctx, request.Recommendation.StockCode)
	if minuteErr == nil {
		draft.MinuteSummary, minuteErr = summarizeLifecycleMinutes(tradingDate, minuteRows)
	}
	if minuteErr != nil {
		draft.Status, draft.CriticalFailure = "critical_failed", "分钟量价不可用: "+minuteErr.Error()
		draft.Sources = append(draft.Sources, failedLifecycleSource(minuteID, "分钟量价", "minute", researchSourceNow(collector.clock), minuteErr))
		return draft, nil
	}
	draft.Sources = append(draft.Sources, newLifecycleSource(minuteID, "分钟量价", "minute", researchSourceNow(collector.clock), draft.MinuteSummary, nil))

	optional := collector.collectOptional(ctx, request)
	draft.Sources = append(draft.Sources, optional...)
	for _, source := range optional {
		if source.Status == "failed" || source.Status == "partial" {
			draft.Status = "partial"
			break
		}
	}
	return draft, nil
}

func (collector *ResearchLifecycleContextCollector) collectMinute(ctx context.Context, code string) ([]MinuteData, string, error) {
	ctx, cancel := context.WithTimeout(ctx, lifecycleSourceTimeout)
	defer cancel()
	rows, date, err := collector.stocks.getStockMinutePriceData(ctx, code)
	if err != nil {
		return nil, date, err
	}
	if len(rows) == 0 || strings.TrimSpace(date) == "" {
		return nil, date, errors.New("分钟行情来源返回空数据")
	}
	return rows, date, nil
}

func summarizeLifecycleMinutes(tradingDate string, rows []MinuteData) (research.MinuteEvidenceSummary, error) {
	date, err := parseMinuteTradingDate(tradingDate)
	if err != nil {
		return research.MinuteEvidenceSummary{}, err
	}
	type parsedBar struct {
		at time.Time
		MinuteData
	}
	bars := make([]parsedBar, 0, len(rows))
	for _, row := range rows {
		at, parseErr := time.ParseInLocation("2006-01-02 15:04", date.Format("2006-01-02")+" "+strings.TrimSpace(row.Time), shanghaiDataLocation())
		if parseErr != nil || row.Price <= 0 || math.IsNaN(row.Price) || math.IsInf(row.Price, 0) {
			continue
		}
		bars = append(bars, parsedBar{at: at, MinuteData: row})
	}
	if len(bars) == 0 {
		return research.MinuteEvidenceSummary{}, errors.New("分钟行情没有有效价格记录")
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].at.Before(bars[j].at) })
	volumes, amounts := make([]float64, len(bars)), make([]float64, len(bars))
	for index := range bars {
		volumes[index], amounts[index] = bars[index].Volume, bars[index].Amount
	}
	for index := len(bars) - 1; index > 0; index-- {
		bars[index].Volume = lifecycleCumulativeDelta(volumes[index], volumes[index-1])
		bars[index].Amount = lifecycleCumulativeDelta(amounts[index], amounts[index-1])
	}
	latest := bars[len(bars)-1]
	summary := research.MinuteEvidenceSummary{TradingDate: date.Format("2006-01-02"), LatestAt: latest.at, LatestPrice: latest.Price, TotalBars: len(bars)}
	for _, minutes := range []int{15, 30, 60} {
		// Use the latest trading-minute records rather than wall-clock time so
		// the 30/60 minute windows remain meaningful immediately after lunch.
		start := len(bars) - (minutes + 1)
		if start < 0 {
			start = 0
		}
		selected := bars[start:]
		if len(selected) == 0 {
			continue
		}
		window := research.MinuteWindowSummary{Minutes: minutes, Bars: len(selected), High: selected[0].Price, Low: selected[0].Price}
		weightedPrice, weight := 0.0, 0.0
		completeTurnover := true
		for _, bar := range selected {
			window.High = math.Max(window.High, bar.Price)
			window.Low = math.Min(window.Low, bar.Price)
			validVolume := bar.Volume > 0 && !math.IsNaN(bar.Volume) && !math.IsInf(bar.Volume, 0)
			validAmount := bar.Amount > 0 && !math.IsNaN(bar.Amount) && !math.IsInf(bar.Amount, 0)
			if validVolume {
				window.Volume += bar.Volume
			} else {
				completeTurnover = false
			}
			priceWeight := bar.Volume
			if !validVolume {
				priceWeight = 1
			}
			weightedPrice += bar.Price * priceWeight
			weight += priceWeight
			if validAmount {
				window.Amount += bar.Amount
			} else {
				completeTurnover = false
			}
		}
		window.ReturnRate = safeRate(selected[len(selected)-1].Price-selected[0].Price, selected[0].Price)
		window.AveragePrice, window.AveragePriceMethod = lifecycleWindowAveragePrice(window, completeTurnover, weightedPrice, weight)
		summary.Windows = append(summary.Windows, window)
	}
	return summary, nil
}

func lifecycleCumulativeDelta(current, previous float64) float64 {
	if current <= 0 || math.IsNaN(current) || math.IsInf(current, 0) {
		return 0
	}
	if current >= previous {
		return current - previous
	}
	// A provider-side session/reset starts a new cumulative series.
	return current
}

func lifecycleWindowAveragePrice(window research.MinuteWindowSummary, completeTurnover bool, weightedPrice, weight float64) (float64, string) {
	reasonable := func(value float64) bool {
		return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) &&
			value >= window.Low*0.8 && value <= window.High*1.2
	}
	if completeTurnover && window.Amount > 0 && window.Volume > 0 {
		if candidate := window.Amount / window.Volume; reasonable(candidate) {
			return candidate, "amount_divided_by_share_volume"
		} else if candidate /= 100; reasonable(candidate) {
			return candidate, "amount_divided_by_lot_volume_times_100"
		}
	}
	if weight > 0 {
		return weightedPrice / weight, "volume_weighted_minute_price_proxy"
	}
	return 0, "volume_weighted_minute_price_proxy"
}

func parseMinuteTradingDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"20060102", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, shanghaiDataLocation()); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("分钟行情交易日期无效: %q", value)
}

func (collector *ResearchLifecycleContextCollector) collectOptional(ctx context.Context, request research.LifecycleContextRequest) []research.LifecycleEvidenceSource {
	ctx, cancel := context.WithTimeout(ctx, lifecycleSourceTimeout)
	defer cancel()
	type job struct {
		suffix   string
		name     string
		category string
		run      func() (any, error)
	}
	jobs := []job{{suffix: "NEWS", name: "增量新闻与重要事件", category: "news", run: func() (any, error) { return collector.incrementalNews(ctx, request) }}}
	text := strings.ToLower(strings.Join([]string{request.Recommendation.AISummary, request.Recommendation.MainRisk}, " "))
	if containsLifecycleKeyword(text, "板块", "行业", "概念", "主线", "热点") {
		jobs = append(jobs, job{suffix: "SECTORMONEY", name: "行业资金", category: "sector_money", run: func() (any, error) { return collector.news.industryMoneyRankSina(ctx, "gn", "netamount") }})
	}
	if containsLifecycleKeyword(text, "资金", "承接", "放量", "缩量", "成交", "量能", "换手") {
		jobs = append(jobs, job{suffix: "STOCKMONEY", name: "个股日频资金趋势", category: "money", run: func() (any, error) {
			return collector.news.stockMoneyTrendByDay(ctx, request.Recommendation.StockCode, 10)
		}})
	}
	if containsLifecycleKeyword(text, "公告", "订单", "业绩", "回购", "减持", "增持", "股东", "互动") {
		digits := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(request.Recommendation.StockCode), "sh"), "sz")
		jobs = append(jobs,
			job{suffix: "NOTICE", name: "公司公告", category: "announcement", run: func() (any, error) { return collector.news.stockNotice(ctx, digits) }},
			job{suffix: "INTERACTIVE", name: "互动易", category: "interaction", run: func() (any, error) {
				return collector.news.interactiveAnswer(ctx, 1, 20, request.Recommendation.StockName)
			}},
		)
	}

	shared := collector.sharedMarketSources(ctx, request)
	results := make([]research.LifecycleEvidenceSource, len(jobs))
	var wait sync.WaitGroup
	for index, item := range jobs {
		index, item := index, item
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := runLifecycleOptional(ctx, item.run)
			results[index] = newLifecycleSource(research.LifecycleSourceID(request.ObservationID, item.suffix), item.name, item.category,
				researchSourceNow(collector.clock), value, err)
		}()
	}
	wait.Wait()
	return append(shared, results...)
}

func (collector *ResearchLifecycleContextCollector) sharedMarketSources(ctx context.Context, request research.LifecycleContextRequest) []research.LifecycleEvidenceSource {
	local := research.ShanghaiTime(request.Now)
	minute := (local.Minute() / 15) * 15
	bucket := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), minute, 0, 0, local.Location()).Format(time.RFC3339)
	collector.cacheMu.Lock()
	if collector.cacheBucket != bucket {
		collector.marketCache = nil
		jobs := []struct {
			name, category string
			run            func() (any, error)
		}{
			{name: "主要指数", category: "index", run: func() (any, error) { return collector.news.globalStockIndexes(ctx, 5) }},
			{name: "行业表现", category: "sector", run: func() (any, error) { return collector.news.industryRank(ctx, "changepercent", 30) }},
		}
		fresh := make([]lifecycleCachedSource, len(jobs))
		var wait sync.WaitGroup
		for index, item := range jobs {
			index, item := index, item
			wait.Add(1)
			go func() {
				defer wait.Done()
				value, err := runLifecycleOptional(ctx, item.run)
				source := newLifecycleSource("", item.name, item.category, researchSourceNow(collector.clock), value, err)
				fresh[index] = lifecycleCachedSource{name: source.Name, category: source.Category, status: source.Status,
					content: source.Content, errorText: source.Error, fingerprint: source.Fingerprint, collectedAt: source.CollectedAt}
			}()
		}
		wait.Wait()
		collector.marketCache = fresh
		collector.cacheBucket = bucket
		for _, source := range fresh {
			if source.status == "failed" {
				collector.cacheBucket = ""
				break
			}
		}
	}
	cached := append([]lifecycleCachedSource(nil), collector.marketCache...)
	collector.cacheMu.Unlock()

	result := make([]research.LifecycleEvidenceSource, 0, len(cached))
	for _, source := range cached {
		suffix := "IDX"
		if source.category == "sector" {
			suffix = "SECTOR"
		}
		item := research.LifecycleEvidenceSource{ID: research.LifecycleSourceID(request.ObservationID, suffix), Name: source.name,
			Category: source.category, Status: source.status, CollectedAt: source.collectedAt, Content: source.content,
			Error: source.errorText, Fingerprint: source.fingerprint}
		result = append(result, item)
	}
	return result
}

func (collector *ResearchLifecycleContextCollector) incrementalNews(ctx context.Context, request research.LifecycleContextRequest) (any, error) {
	if collector.news.mainDB == nil {
		return nil, errors.New("market news database is not initialized")
	}
	if request.Now.Before(request.WindowFrom) {
		return nil, errors.New("invalid lifecycle news window")
	}
	digits := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(request.Recommendation.StockCode), "sh"), "sz")
	name := strings.TrimSpace(request.Recommendation.StockName)
	if digits == "" {
		return nil, errors.New("lifecycle news stock code is required")
	}
	zero := time.Date(2, time.January, 1, 0, 0, 0, 0, time.UTC)
	// Event time defines when the news happened; CreatedAt also admits late
	// arrivals since the last successful coverage, without relabelling events.
	query := collector.news.mainDB.WithContext(ctx).Model(&models.Telegraph{}).Preload("TelegraphTags").
		Where("created_at <= ?", request.Now).
		Where(`((data_time > ? AND data_time <= ? AND (data_time >= ? OR created_at >= ?))
			OR ((data_time IS NULL OR data_time <= ?) AND created_at >= ?))`, zero, request.Now, request.WindowFrom, request.WindowFrom, zero, request.WindowFrom).
		Where(`is_red = ? OR instr(title, ?) > 0 OR instr(content, ?) > 0
			OR (? <> '' AND (instr(title, ?) > 0 OR instr(content, ?) > 0))
			OR EXISTS (SELECT 1 FROM telegraph_tags links JOIN tags ON tags.id = links.tag_id
				WHERE links.telegraph_id = telegraph_list.id AND links.deleted_at IS NULL AND tags.deleted_at IS NULL
				AND (instr(tags.name, ?) > 0 OR (? <> '' AND instr(tags.name, ?) > 0)))`, true, digits, digits, name, name, name, digits, name, name)
	if len(request.KnownNewsIDs) > 0 {
		query = query.Where("id NOT IN ?", request.KnownNewsIDs)
	}
	rows := make([]*models.Telegraph, 0, 31)
	if err := query.Order("data_time DESC, created_at DESC, id DESC").Limit(31).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query lifecycle news: %w", err)
	}
	type newsItem struct {
		ID         uint       `json:"id"`
		EventAt    *time.Time `json:"eventAt"`
		IngestedAt time.Time  `json:"ingestedAt"`
		Title      string     `json:"title"`
		Summary    string     `json:"summary"`
		Source     string     `json:"source"`
	}
	items := make([]newsItem, 0, 30)
	payload := map[string]any{"status": "partial", "warning": "新闻本轮分页未完，后续继续读取剩余事件", "coverageComplete": false, "from": request.WindowFrom, "to": request.Now, "items": items}
	for _, row := range rows {
		candidate := newsItem{ID: row.ID, EventAt: row.DataTime, IngestedAt: row.CreatedAt, Title: truncatePromptString(row.Title, 80), Summary: truncatePromptString(row.Content, 160), Source: truncatePromptString(row.Source, 40)}
		items = append(items, candidate)
		payload["items"] = items
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		// Keep complete identifiable records inside the model's source budget.
		// IDs in persisted partial pages let subsequent observations resume even
		// when many events share an identical timestamp.
		if len(items) > 30 || len(encoded) > 2400 {
			items = items[:len(items)-1]
			break
		}
	}
	payload["items"] = items
	if len(items) == len(rows) {
		payload["status"], payload["warning"], payload["coverageComplete"] = "ok", "", true
	}
	if len(rows) == 0 {
		if err := collector.news.marketNewsFetchFailureForWindow(nil, request.WindowFrom, request.Now); err != nil {
			return nil, err
		}
		payload["status"] = "empty"
	}
	return payload, nil
}

func runLifecycleOptional(ctx context.Context, run func() (any, error)) (any, error) {
	type result struct {
		value any
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				resultCh <- result{err: fmt.Errorf("来源调用异常: %v", recovered)}
			}
		}()
		value, err := run()
		resultCh <- result{value: value, err: err}
	}()
	timer := time.NewTimer(lifecycleSourceTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errors.New("来源调用超时")
	case value := <-resultCh:
		return value.value, value.err
	}
}

func newLifecycleSource(id, name, category string, now time.Time, value any, sourceErr error) research.LifecycleEvidenceSource {
	source := research.LifecycleEvidenceSource{ID: id, Name: name, Category: category, CollectedAt: now, Status: "ok"}
	if sourceErr != nil {
		source.Status, source.Error = "failed", sourceErr.Error()
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		source.Status, source.Error = "failed", marshalErr.Error()
		return source
	}
	content := truncateResearchSourceJSON(string(data), 8000)
	var coverage struct {
		Status  string `json:"status"`
		Warning string `json:"warning"`
	}
	if source.Status == "ok" && json.Unmarshal(data, &coverage) == nil && coverage.Status == "partial" {
		source.Status, source.Error = "partial", coverage.Warning
	}
	if len(data) > 8000 && source.Status != "failed" {
		source.Status = "partial"
		source.Error = strings.TrimSpace(source.Error + " 来源内容超过预算，已压缩截断")
	}
	var decoded any
	empty := len(data) == 0 || (json.Unmarshal(data, &decoded) == nil && researchevidence.JSONValueEmpty(decoded))
	if empty {
		if source.Status == "ok" {
			source.Status = "empty"
		}
	}
	source.Fingerprint = research.EvidenceFingerprint(name + "\n" + content)
	source.Content = content
	return source
}

func failedLifecycleSource(id, name, category string, now time.Time, err error) research.LifecycleEvidenceSource {
	return research.LifecycleEvidenceSource{ID: id, Name: name, Category: category, Status: "failed", CollectedAt: now, Error: err.Error()}
}

func containsLifecycleKeyword(value string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(value, keyword) {
			return true
		}
	}
	return false
}

func safeRate(delta, base float64) float64 {
	if base == 0 {
		return 0
	}
	return delta / base
}
