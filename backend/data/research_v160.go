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

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/research"
)

type ResearchAIClient struct{ configID int }

func NewResearchAIClient(configID int) *ResearchAIClient {
	return &ResearchAIClient{configID: configID}
}

func (client *ResearchAIClient) Complete(ctx context.Context, request research.CompletionRequest) (research.CompletionResult, error) {
	setting := GetSettingConfig()
	configID := client.configID
	if configID <= 0 && setting != nil && setting.Settings != nil {
		configID = int(setting.AIAnalysisConfigID)
	}
	provider := NewDeepSeekOpenAi(ctx, configID)
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
	content, responseID, model, err := provider.CompleteResearch(ctx, messages, request.PreviousResponseID)
	if err != nil {
		return research.CompletionResult{}, err
	}
	return research.CompletionResult{Content: content, ResponseID: responseID, Model: model}, nil
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
	if len(data) > 16000 {
		data = append(data[:16000], []byte(`...<truncated>`)...)
	}
	document.Content = string(data)
	return document
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
	if setting == nil || len(setting.AiConfigs) == 0 {
		return nil, errors.New("未配置 AI 模型")
	}
	requested := uint(0)
	if setting.Settings != nil {
		requested = setting.AIAnalysisConfigID
	}
	for _, config := range setting.AiConfigs {
		if config != nil && requested != 0 && config.ID == requested {
			return config, nil
		}
	}
	// First-run preference is the existing wawa / gpt-5.6-sol Responses config.
	for _, config := range setting.AiConfigs {
		if config != nil && strings.EqualFold(strings.TrimSpace(config.Name), "wawa") && strings.EqualFold(strings.TrimSpace(config.ModelName), "gpt-5.6-sol") && NormalizeAIAPIProtocol(config.ApiProtocol) == AIAPIProtocolOpenAIResponses {
			return config, nil
		}
	}
	config := SelectPrimaryAIConfig(setting.AiConfigs)
	if config == nil {
		return nil, fmt.Errorf("未找到可用 AI 配置")
	}
	return config, nil
}
