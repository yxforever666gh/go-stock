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
	"unicode/utf8"

	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/research"
)

type researchProviderCompletion func(context.Context, *models.AIConfig, []map[string]any, string) (string, string, string, error)

type ResearchAIClient struct {
	configID         int
	loadSetting      func() *SettingConfig
	completeProvider researchProviderCompletion
	attemptTimeout   time.Duration
	maxAttempts      int
}

const (
	researchModelAttemptTimeout = 30 * time.Second
	researchModelMaxAttempts    = 5
)

func NewResearchAIClient(configID int) *ResearchAIClient {
	return &ResearchAIClient{configID: configID}
}

func (client *ResearchAIClient) modelAttemptTimeout() time.Duration {
	if client.attemptTimeout > 0 {
		return client.attemptTimeout
	}
	return researchModelAttemptTimeout
}

func (client *ResearchAIClient) modelMaxAttempts() int {
	if client.maxAttempts > 0 {
		return client.maxAttempts
	}
	return researchModelMaxAttempts
}

func (client *ResearchAIClient) completeModelAttempt(ctx context.Context, config *models.AIConfig, messages []map[string]any, previousResponseID string) (string, string, string, error) {
	if client.completeProvider != nil {
		return client.completeProvider(ctx, config, messages, previousResponseID)
	}
	provider := NewOpenAiWithConfig(ctx, config)
	provider.TimeOut = int(client.modelAttemptTimeout() / time.Second)
	provider.DisableRequestRetries = true
	return provider.CompleteResearch(ctx, messages, previousResponseID)
}

func (client *ResearchAIClient) Complete(ctx context.Context, request research.CompletionRequest) (research.CompletionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	setting := GetSettingConfig()
	if client.loadSetting != nil {
		setting = client.loadSetting()
	}
	messages := make([]map[string]any, 0, len(request.Messages)+1)
	if len(request.Messages) > 0 {
		for _, message := range request.Messages {
			role := message.Role
			if role != "system" && role != "assistant" {
				role = "user"
			}
			messages = append(messages, map[string]any{"role": role, "content": message.Content})
		}
	} else {
		messages = append(messages, map[string]any{"role": "user", "content": request.Prompt})
	}
	// Research always follows the settings table from top to bottom. The
	// constructor's config ID is retained only for callers that still record the
	// primary row; it must not override the user-visible fallback order.
	order := ResolveAIFallbackOrder(setting, 0)
	if len(order) == 0 {
		return research.CompletionResult{}, errors.New("没有已启用的 AI 模型")
	}
	configs := make(map[int]*models.AIConfig, len(setting.AiConfigs))
	for _, config := range setting.AiConfigs {
		if config != nil {
			configs[int(config.ID)] = config
		}
	}
	attemptErrors := make([]error, 0, len(order))
	attemptTimeout := client.modelAttemptTimeout()
	maxAttempts := client.modelMaxAttempts()
	for index, configID := range order {
		config := configs[configID]
		if config == nil || config.Disabled {
			continue
		}
		label := strings.TrimSpace(config.Name)
		if label == "" {
			label = DisplayAIProviderName(config)
		}
		var lastErr error
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return research.CompletionResult{}, err
			}
			attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
			content, responseID, model, err := client.completeModelAttempt(attemptCtx, config, messages, request.PreviousResponseID)
			attemptTimedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
			cancel()
			if err == nil {
				return research.CompletionResult{Content: content, ResponseID: responseID, Model: model}, nil
			}
			if parentErr := ctx.Err(); parentErr != nil {
				return research.CompletionResult{}, parentErr
			}
			lastErr = err
			if attemptTimedOut {
				lastErr = fmt.Errorf("单次调用超过 %s: %w", attemptTimeout, context.DeadlineExceeded)
			}
			if attempt < maxAttempts {
				logger.SugaredLogger.Warnf("研究中心 AI 调用失败，重试当前模型。phase=%s model=%s/%s attempt=%d/%d timeout=%s error=%v",
					request.Phase, label, strings.TrimSpace(config.ModelName), attempt, maxAttempts, attemptTimeout, lastErr)
			}
		}
		attemptErrors = append(attemptErrors, fmt.Errorf("%s/%s 连续 %d 次失败（单次超时 %s）: %w",
			label, strings.TrimSpace(config.ModelName), maxAttempts, attemptTimeout, lastErr))
		if index+1 < len(order) {
			next := configs[order[index+1]]
			nextLabel := "下一模型"
			if next != nil {
				nextLabel = strings.TrimSpace(next.Name) + "/" + strings.TrimSpace(next.ModelName)
			}
			logger.SugaredLogger.Warnf("研究中心 AI 连续 %d 次调用失败，按回退顺序切换。phase=%s from=%s/%s to=%s timeout=%s error=%v",
				maxAttempts, request.Phase, label, strings.TrimSpace(config.ModelName), nextLabel, attemptTimeout, lastErr)
		}
	}
	if len(attemptErrors) == 0 {
		return research.CompletionResult{}, errors.New("没有已启用的 AI 模型")
	}
	return research.CompletionResult{}, fmt.Errorf("所有已启用模型均调用失败: %w", errors.Join(attemptErrors...))
}

type ResearchQuoteProvider struct{ stocks *StockDataApi }

func NewResearchQuoteProvider() *ResearchQuoteProvider {
	return &ResearchQuoteProvider{stocks: NewStockDataApi()}
}

func (provider *ResearchQuoteProvider) CurrentQuote(ctx context.Context, code string) (research.Quote, error) {
	normalized, ok := research.NormalizeMainlandCode(code)
	if !ok {
		return research.Quote{}, errors.New("only Shanghai/Shenzhen A shares are supported")
	}
	rows, err := provider.stocks.GetStockCodeRealTimeDataReadOnly(ctx, normalized)
	if err != nil {
		return research.Quote{}, err
	}
	if rows == nil || len(*rows) != 1 {
		return research.Quote{}, errors.New("realtime quote is unavailable")
	}
	row := (*rows)[0]
	price, err := strconv.ParseFloat(strings.TrimSpace(row.Price), 64)
	if err != nil || price <= 0 {
		return research.Quote{}, errors.New("realtime quote price is invalid")
	}
	previousClose, _ := strconv.ParseFloat(strings.TrimSpace(row.PreClose), 64)
	quoteAt, _ := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(row.Date)+" "+strings.TrimSpace(row.Time), shanghaiDataLocation())
	if quoteAt.IsZero() {
		quoteAt = time.Now()
	}
	volume, _ := strconv.ParseFloat(strings.TrimSpace(row.Volume), 64)
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
	return research.Quote{Code: normalized, Name: strings.TrimSpace(row.Name), Market: market, Price: price, PreviousClose: previousClose,
		At: quoteAt, Suspended: volume == 0, LimitUp: limitUp, LimitDown: limitDown}, nil
}

type ResearchTradingCalendar struct{}

func (ResearchTradingCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	return IsCNOpenTradeDayStrict(value)
}

type ResearchSourceCollector struct {
	news          *MarketNewsApi
	stocks        *StockDataApi
	newsWindowMu  sync.Mutex
	newsWindowKey string
	newsWindow    NewsWindowResult
	newsWindowErr error
}

type researchSourceJob struct {
	name string
	run  func() any
}

func NewResearchSourceCollector() *ResearchSourceCollector {
	return &ResearchSourceCollector{news: NewMarketNewsApi(), stocks: NewStockDataApi()}
}

func (collector *ResearchSourceCollector) CollectMarket(ctx context.Context, now time.Time) ([]research.SourceDocument, error) {
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
	return runResearchSourceJobs(ctx, now, "market", jobs)
}

func (collector *ResearchSourceCollector) CollectSectors(ctx context.Context, now time.Time) ([]research.SourceDocument, error) {
	jobs := []researchSourceJob{
		{"腾讯行业排名", func() any { return collector.news.GetIndustryRank("changepercent", 30) }},
		{"Sina行业资金", func() any { return collector.news.GetIndustryMoneyRankSina("gn", "netamount") }},
		{"Sina个股资金", func() any { return collector.news.GetMoneyRankSina("netamount") }},
		{"雪球沪深热股", func() any { return collector.news.XUEQIUHotStock(50, "10") }},
		{"东方财富热点事件", func() any { return collector.news.HotEvent(30) }},
		{"东方财富热门话题", func() any { return collector.news.HotTopic(30) }},
		{"东方财富龙虎榜", func() any { return collector.news.LongTiger(now.Format("20060102")) }},
	}
	return runResearchSourceJobs(ctx, now, "sector", jobs)
}

func (collector *ResearchSourceCollector) CollectStocks(ctx context.Context, now time.Time, candidates []research.StockCandidate) ([]research.SourceDocument, error) {
	documents := make([]research.SourceDocument, 0, len(candidates)*9)
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, candidate := range candidates {
		candidate := candidate
		wait.Add(1)
		go func() {
			defer wait.Done()
			if ctx.Err() != nil {
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
				{"Sina日K " + code, func() any { return collector.stocks.GetKLineData(code, "240", 60) }},
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
			local := make([]research.SourceDocument, 0, len(items))
			for _, item := range items {
				local = append(local, executeResearchSourceJob(item.name, "stock", now, item.run))
			}
			mutex.Lock()
			documents = append(documents, local...)
			mutex.Unlock()
		}()
	}
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return documents, ctx.Err()
	case <-done:
		return documents, nil
	}
}

func (collector *ResearchSourceCollector) stockRelatedNews(now time.Time, candidate research.StockCandidate) any {
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

func runResearchSourceJobs(ctx context.Context, now time.Time, category string, jobs []researchSourceJob) ([]research.SourceDocument, error) {
	documents := make([]research.SourceDocument, len(jobs))
	var wait sync.WaitGroup
	for index := range jobs {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			if ctx.Err() != nil {
				documents[index] = research.SourceDocument{SourceName: jobs[index].name, Category: category, CollectedAt: now, Error: ctx.Err().Error()}
				return
			}
			documents[index] = executeResearchSourceJob(jobs[index].name, category, now, jobs[index].run)
		}()
	}
	wait.Wait()
	return documents, ctx.Err()
}

func executeResearchSourceJob(name, category string, now time.Time, run func() any) (document research.SourceDocument) {
	document = research.SourceDocument{SourceName: name, Category: category, CollectedAt: now}
	defer func() {
		if recovered := recover(); recovered != nil {
			document.Content = ""
			document.Error = fmt.Sprintf("来源调用异常: %v", recovered)
		}
	}()
	if run == nil {
		document.Error = "来源任务为空"
		return document
	}
	return researchDocument(name, category, now, run())
}

func researchDocument(name, category string, now time.Time, value any) research.SourceDocument {
	document := research.SourceDocument{SourceName: name, Category: category, CollectedAt: now}
	if value == nil {
		document.Error = "来源返回空值"
		return document
	}
	if result, ok := value.(map[string]any); ok {
		if sourceError, exists := result["error"]; exists && strings.TrimSpace(fmt.Sprint(sourceError)) != "" {
			document.Error = strings.TrimSpace(fmt.Sprint(sourceError))
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		document.Error = err.Error()
		return document
	}
	document.Content = truncateResearchSourceJSON(string(data), 16000)
	return document
}

func truncateResearchSourceJSON(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	const marker = `...<truncated>`
	end := maxBytes - len(marker)
	if end < 0 {
		end = 0
	}
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + marker
}

func shanghaiDataLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}

type ResearchRuntime struct {
	Repository *research.Repository
	Service    *research.Service
	Runner     *research.AnalysisRunner
}

func NewResearchRuntime(configID int) (*ResearchRuntime, error) {
	if db.Dao == nil {
		return nil, errors.New("database is not initialized")
	}
	repository := research.NewRepository(db.Dao)
	if err := repository.EnsureAccount(context.Background()); err != nil {
		return nil, err
	}
	service := research.NewService(repository, NewResearchAIClient(configID), NewResearchQuoteProvider(), ResearchTradingCalendar{})
	return &ResearchRuntime{Repository: repository, Service: service, Runner: research.NewAnalysisRunner(service, NewResearchSourceCollector())}, nil
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
