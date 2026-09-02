package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/marketdata"
	"go-stock/backend/models"
	"go-stock/backend/research"
	"go-stock/backend/researchaudit"

	"gorm.io/gorm"
)

type researchProviderCompletion func(context.Context, *models.AIConfig, []map[string]any, string, func(AIStreamActivity)) (string, string, string, error)

type ResearchAIClient struct {
	configID         int
	forceConfig      bool
	loadSetting      func() *SettingConfig
	completeProvider researchProviderCompletion
	attemptTimeout   time.Duration
	maxAttempts      int
	retryWait        func(context.Context, time.Duration) error
}

const (
	researchModelAttemptTimeout          = 300 * time.Second
	researchModelMaxAttempts             = 5
	researchModelRetryBaseDelay          = time.Second
	researchModelRetryMaxDelay           = 8 * time.Second
	researchSameEndpointFallbackCooldown = 8 * time.Second
)

var errResearchModelInactive = errors.New("model stream had no activity before timeout")

func NewResearchAIClient(configID int) *ResearchAIClient {
	return &ResearchAIClient{configID: configID}
}

// NewResearchReplayAIClient pins an isolated audit replay to the explicitly
// selected model configuration. Formal research keeps its normal configured
// fallback order; a replay must not silently switch to a different model.
func NewResearchReplayAIClient(configID int) *ResearchAIClient {
	return &ResearchAIClient{configID: configID, forceConfig: true}
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

func researchModelRetryDelay(failedAttempt int) time.Duration {
	if failedAttempt <= 1 {
		return researchModelRetryBaseDelay
	}
	delay := researchModelRetryBaseDelay << (failedAttempt - 1)
	if delay > researchModelRetryMaxDelay {
		return researchModelRetryMaxDelay
	}
	return delay
}

func waitResearchRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return ctx.Err()
	}
}

func (client *ResearchAIClient) waitForRetry(ctx context.Context, delay time.Duration) error {
	if client.retryWait != nil {
		return client.retryWait(ctx, delay)
	}
	return waitResearchRetry(ctx, delay)
}

func sameResearchEndpoint(left, right *models.AIConfig) bool {
	if left == nil || right == nil {
		return false
	}
	leftURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(left.BaseUrl), "/"))
	rightURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(right.BaseUrl), "/"))
	return leftURL != "" && leftURL == rightURL
}

func (client *ResearchAIClient) completeModelAttempt(ctx context.Context, config *models.AIConfig, messages []map[string]any, previousResponseID string, activity func(AIStreamActivity)) (string, string, string, error) {
	if client.completeProvider != nil {
		return client.completeProvider(ctx, config, messages, previousResponseID, activity)
	}
	provider := NewOpenAiWithConfig(ctx, config)
	provider.DisableRequestRetries = true
	return provider.CompleteResearchStream(ctx, messages, previousResponseID, activity)
}

func researchInactivityContext(parent context.Context, timeout time.Duration) (context.Context, func(), func()) {
	ctx, cancel := context.WithCancelCause(parent)
	touchCh := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				select {
				case <-touchCh:
					timer.Reset(timeout)
					continue
				default:
					cancel(errResearchModelInactive)
					return
				}
			case <-touchCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
			case <-ctx.Done():
				return
			}
		}
	}()
	touch := func() {
		select {
		case touchCh <- struct{}{}:
		default:
		}
	}
	stop := func() {
		cancel(context.Canceled)
		<-done
	}
	return ctx, touch, stop
}

type researchProviderErrorInfo struct {
	category   string
	message    string
	statusCode int
	retryable  bool
	lastEvent  string
}

func classifyResearchProviderError(err error) researchProviderErrorInfo {
	if err == nil {
		return researchProviderErrorInfo{}
	}
	var providerErr *aiProviderCallError
	if errors.As(err, &providerErr) {
		return researchProviderErrorInfo{
			category: providerErr.Category, message: providerErr.Message, statusCode: providerErr.StatusCode,
			retryable: providerErr.Retryable, lastEvent: providerErr.LastEventType,
		}
	}
	if errors.Is(err, errResearchModelInactive) {
		return researchProviderErrorInfo{category: "idle_timeout", message: err.Error(), retryable: true}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return researchProviderErrorInfo{category: "stream_interrupted", message: err.Error(), retryable: true}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return researchProviderErrorInfo{category: "network_error", message: err.Error(), retryable: true}
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{"connection reset", "connection refused", "unexpected eof", "tls handshake timeout", "i/o timeout", "temporary failure", "no such host"} {
		if strings.Contains(lower, marker) {
			return researchProviderErrorInfo{category: "network_error", message: err.Error(), retryable: true}
		}
	}
	return researchProviderErrorInfo{category: "request_error", message: err.Error(), retryable: false}
}

func sanitizeResearchProviderError(message string, config *models.AIConfig) string {
	message = strings.TrimSpace(message)
	if config != nil {
		for _, secret := range []string{config.ApiKey, config.BaseUrl, config.HttpProxy} {
			if secret = strings.TrimSpace(secret); secret != "" {
				message = strings.ReplaceAll(message, secret, "[REDACTED]")
			}
		}
	}
	message = strings.Join(strings.Fields(message), " ")
	const maxRunes = 2048
	runes := []rune(message)
	if len(runes) > maxRunes {
		message = string(runes[:maxRunes]) + "…"
	}
	if message == "" {
		return "模型服务返回了空错误"
	}
	return message
}

func emitResearchAttempt(callback func(research.ModelAttemptRecord), record research.ModelAttemptRecord) {
	if callback != nil {
		callback(record)
	}
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
	if client.forceConfig {
		order = []int{client.configID}
	}
	if len(order) == 0 {
		return research.CompletionResult{}, errors.New("没有已启用的 AI 模型")
	}
	configs := make(map[int]*models.AIConfig, len(setting.AiConfigs))
	for _, config := range setting.AiConfigs {
		if config != nil {
			configs[int(config.ID)] = config
		}
	}
	orderedConfigs := make([]*models.AIConfig, 0, len(order))
	for _, configID := range order {
		if config := configs[configID]; config != nil && !config.Disabled {
			orderedConfigs = append(orderedConfigs, config)
		}
	}
	attemptErrors := make([]error, 0, len(orderedConfigs))
	attemptTimeout := client.modelAttemptTimeout()
	maxAttempts := client.modelMaxAttempts()
	for index, config := range orderedConfigs {
		label := strings.TrimSpace(config.Name)
		if label == "" {
			label = DisplayAIProviderName(config)
		}
		var lastErr error
		var lastInfo researchProviderErrorInfo
		attemptsMade := 0
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return research.CompletionResult{}, err
			}
			attemptsMade = attempt
			startedAt := time.Now()
			record := research.ModelAttemptRecord{
				ID:    fmt.Sprintf("%s-%d-%d-%d", request.Phase, config.ID, attempt, startedAt.UnixNano()),
				Phase: request.Phase, ConfigID: config.ID, ProviderName: label,
				ModelName: strings.TrimSpace(config.ModelName), APIProtocol: NormalizeAIAPIProtocol(config.ApiProtocol),
				MaxTokens: config.MaxTokens, Temperature: config.Temperature, RequestTimeoutSeconds: config.TimeOut,
				InactivityTimeoutSeconds: int(attemptTimeout / time.Second), FallbackIndex: index + 1, FallbackCount: len(orderedConfigs),
				ForcedConfig: client.forceConfig, PreviousResponseIDPresent: strings.TrimSpace(request.PreviousResponseID) != "",
				Attempt: attempt, MaxAttempts: maxAttempts, StartedAt: startedAt, Status: "waiting_response",
			}
			emitResearchAttempt(request.OnAttempt, record)
			attemptCtx, touch, stop := researchInactivityContext(ctx, attemptTimeout)
			lastPersistedAt := startedAt
			lastPersistedStatus := record.Status
			content, responseID, model, err := client.completeModelAttempt(attemptCtx, config, messages, request.PreviousResponseID, func(event AIStreamActivity) {
				touch()
				now := time.Now()
				record.LastActivityAt = &now
				record.LastEventType = strings.TrimSpace(event.EventType)
				if strings.TrimSpace(event.State) != "" {
					record.Status = strings.TrimSpace(event.State)
				}
				if record.Status != lastPersistedStatus || now.Sub(lastPersistedAt) >= 5*time.Second {
					record.DurationMS = now.Sub(startedAt).Milliseconds()
					emitResearchAttempt(request.OnAttempt, record)
					lastPersistedAt, lastPersistedStatus = now, record.Status
				}
			})
			cause := context.Cause(attemptCtx)
			stop()
			if errors.Is(cause, errResearchModelInactive) {
				err = fmt.Errorf("连续 %s 未收到有效模型事件: %w", attemptTimeout, errResearchModelInactive)
			}
			if err == nil {
				completedAt := time.Now()
				record.Status, record.CompletedAt, record.DurationMS = "success", &completedAt, completedAt.Sub(startedAt).Milliseconds()
				record.NextAction = "complete"
				emitResearchAttempt(request.OnAttempt, record)
				return research.CompletionResult{Content: content, ResponseID: responseID, Model: model}, nil
			}
			if parentErr := ctx.Err(); parentErr != nil {
				completedAt := time.Now()
				record.Status, record.CompletedAt, record.DurationMS = "cancelled", &completedAt, completedAt.Sub(startedAt).Milliseconds()
				record.ErrorCategory, record.ErrorMessage, record.NextAction = "cancelled", sanitizeResearchProviderError(parentErr.Error(), config), "stop"
				emitResearchAttempt(request.OnAttempt, record)
				return research.CompletionResult{}, parentErr
			}
			lastInfo = classifyResearchProviderError(err)
			lastInfo.message = sanitizeResearchProviderError(lastInfo.message, config)
			lastErr = errors.New(lastInfo.message)
			completedAt := time.Now()
			record.Status, record.CompletedAt, record.DurationMS = "failed", &completedAt, completedAt.Sub(startedAt).Milliseconds()
			record.HTTPStatus, record.ErrorCategory, record.ErrorMessage = lastInfo.statusCode, lastInfo.category, lastInfo.message
			record.Retryable = lastInfo.retryable
			if record.LastEventType == "" {
				record.LastEventType = lastInfo.lastEvent
			}
			if lastInfo.retryable && attempt < maxAttempts {
				record.NextAction = "retry_same_model"
			} else if index+1 < len(orderedConfigs) {
				record.NextAction = "fallback_next_model"
			} else {
				record.NextAction = "stop"
			}
			emitResearchAttempt(request.OnAttempt, record)
			if lastInfo.retryable && attempt < maxAttempts {
				retryDelay := researchModelRetryDelay(attempt)
				logger.SugaredLogger.Warnf("研究中心 AI 调用失败，退避后重试当前模型。phase=%s model=%s/%s attempt=%d/%d retry_in=%s inactivity_timeout=%s category=%s http_status=%d error=%s",
					request.Phase, label, strings.TrimSpace(config.ModelName), attempt, maxAttempts, retryDelay, attemptTimeout, lastInfo.category, lastInfo.statusCode, lastInfo.message)
				if waitErr := client.waitForRetry(ctx, retryDelay); waitErr != nil {
					return research.CompletionResult{}, waitErr
				}
				continue
			}
			break
		}
		attemptErrors = append(attemptErrors, fmt.Errorf("%s/%s 调用 %d 次后失败（%s）: %w",
			label, strings.TrimSpace(config.ModelName), attemptsMade, lastInfo.category, lastErr))
		if index+1 < len(orderedConfigs) {
			next := orderedConfigs[index+1]
			nextLabel := strings.TrimSpace(next.Name) + "/" + strings.TrimSpace(next.ModelName)
			logger.SugaredLogger.Warnf("研究中心 AI 调用失败，按回退顺序切换。phase=%s from=%s/%s attempts=%d to=%s inactivity_timeout=%s category=%s http_status=%d error=%s",
				request.Phase, label, strings.TrimSpace(config.ModelName), attemptsMade, nextLabel, attemptTimeout, lastInfo.category, lastInfo.statusCode, lastInfo.message)
			if lastInfo.retryable && sameResearchEndpoint(config, next) {
				logger.SugaredLogger.Warnf("研究中心 AI 回退配置共用同一端点，冷却后切换。phase=%s from=%s/%s to=%s cooldown=%s",
					request.Phase, label, strings.TrimSpace(config.ModelName), nextLabel, researchSameEndpointFallbackCooldown)
				if waitErr := client.waitForRetry(ctx, researchSameEndpointFallbackCooldown); waitErr != nil {
					return research.CompletionResult{}, waitErr
				}
			}
		}
	}
	if len(attemptErrors) == 0 {
		return research.CompletionResult{}, errors.New("没有已启用的 AI 模型")
	}
	return research.CompletionResult{}, fmt.Errorf("所有已启用模型均调用失败: %w", errors.Join(attemptErrors...))
}

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
	rowCode, err := validateResearchQuoteResponseCode(normalized, row.Code)
	if err != nil {
		return research.Quote{}, err
	}
	price, err := strconv.ParseFloat(strings.TrimSpace(row.Price), 64)
	if err != nil || price <= 0 {
		return research.Quote{}, errors.New("realtime quote price is invalid")
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
		return research.Quote{}, errors.New("realtime quote time is invalid")
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
	return research.Quote{Code: rowCode, Name: strings.TrimSpace(row.Name), Market: market, Price: price, PreviousClose: previousClose,
		Volume: volume, Amount: amount, At: quoteAt, Suspended: volume == 0, LimitUp: limitUp, LimitDown: limitDown}, nil
}

func validateResearchQuoteResponseCode(requested, response string) (string, error) {
	requestedCode, requestedOK := research.NormalizeMainlandCode(requested)
	responseCode, responseOK := research.NormalizeMainlandCode(response)
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

func (collector *ResearchSourceCollector) CollectMarket(ctx context.Context, now time.Time) ([]research.SourceDocument, error) {
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
	return runResearchSourceJobs(ctx, "sector", collector.collectedAt, jobs)
}

func (collector *ResearchSourceCollector) CollectStocks(ctx context.Context, now time.Time, candidates []research.StockCandidate) ([]research.SourceDocument, error) {
	type candidateDocuments struct{ documents []research.SourceDocument }
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
				local = append(local, executeResearchSourceJob(item.name, "stock", collector.collectedAt, item.run))
			}
			results <- candidateDocuments{documents: local}
		}()
	}
	documents := make([]research.SourceDocument, 0, len(candidates)*9)
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

func runResearchSourceJobs(ctx context.Context, category string, clock func() time.Time, jobs []researchSourceJob) ([]research.SourceDocument, error) {
	type indexedDocument struct {
		index    int
		document research.SourceDocument
	}
	results := make(chan indexedDocument, len(jobs))
	for index := range jobs {
		index := index
		go func() {
			if ctx.Err() != nil {
				available := researchSourceNow(clock)
				results <- indexedDocument{index: index, document: research.SourceDocument{SourceName: jobs[index].name, Category: category, CollectedAt: available, AvailableAt: &available, Error: ctx.Err().Error()}}
				return
			}
			results <- indexedDocument{index: index, document: executeResearchSourceJob(jobs[index].name, category, clock, jobs[index].run)}
		}()
	}
	documents := make([]research.SourceDocument, len(jobs))
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

func compactResearchSourceDocuments(documents []research.SourceDocument) []research.SourceDocument {
	result := make([]research.SourceDocument, 0, len(documents))
	for _, document := range documents {
		if document.SourceName != "" || document.Error != "" || document.Content != "" {
			result = append(result, document)
		}
	}
	return result
}

func executeResearchSourceJob(name, category string, clock func() time.Time, run func() any) (document research.SourceDocument) {
	document = research.SourceDocument{SourceName: name, Category: category}
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
	return researchDocument(name, category, researchSourceNow(clock), run())
}

func researchSourceNow(clock func() time.Time) time.Time {
	if clock != nil {
		return clock()
	}
	return time.Now()
}

func researchDocument(name, category string, now time.Time, value any) research.SourceDocument {
	document := research.SourceDocument{SourceName: name, Category: category, CollectedAt: now}
	if value == nil {
		document.Error = "来源返回空值"
		return document
	}
	data, err := json.Marshal(value)
	if err != nil {
		document.Error = err.Error()
		return document
	}
	document.Content = truncateResearchSourceJSON(string(data), 16000)
	document.Error = semanticResearchSourceError(data)
	return document
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
	return NewResearchRuntimeWithStorage(configID, db.Dao, db.MinuteDao)
}

func NewResearchRuntimeWithStorage(configID int, mainDB, minuteDB *gorm.DB) (*ResearchRuntime, error) {
	if mainDB == nil {
		return nil, errors.New("database is not initialized")
	}
	repository := research.NewRepository(mainDB)
	if err := repository.EnsureAccount(context.Background()); err != nil {
		return nil, err
	}
	stocks := NewStockDataApi()
	news := NewMarketNewsApi()
	quoteProvider := NewResearchQuoteProviderWithStockData(stocks)
	lifecycleCollector := NewResearchLifecycleContextCollectorWithProviders(quoteProvider, stocks, news)
	service := research.NewService(repository, NewResearchAIClient(configID), quoteProvider, ResearchTradingCalendar{}, lifecycleCollector)
	setting := GetSettingConfig()
	if setting != nil && setting.Settings != nil {
		schedule, scheduleErr := research.NewSellReviewSchedule(setting.AIReviewStartTime, setting.AIReviewIntervalMinutes)
		if scheduleErr != nil {
			return nil, scheduleErr
		}
		service.SetSellReviewSchedule(schedule)
		target, maxImmediate, _, policyErr := NormalizeAICapitalDeploymentSettings(
			setting.AITargetCapitalUtilization,
			setting.AIMaxImmediateBuysPerRun,
			setting.AIReanalysisIntervalMinutes,
		)
		if policyErr != nil {
			return nil, policyErr
		}
		service.SetCapitalDeploymentPolicy(target, maxImmediate)
	}
	service.SetRecommendationChartProvider(NewResearchChartProviderWithStorage(quoteProvider, minuteDB))
	var collector research.SourceCollector = NewResearchSourceCollectorWithProviders(news, stocks)
	runner := research.NewAnalysisRunner(service, collector)
	runner.ConfigureAudit(researchaudit.NewRecorder(researchaudit.NewRepository(mainDB)))
	experimentalEvidence := setting != nil && setting.Settings != nil && setting.ExperimentalEvidenceEnabled
	if experimentalEvidence {
		collector = researchCollectorWithExperimentalEvidence(true, collector, NewMarketEvidenceService(), newThemeEvidenceReader(mainDB))
		runner = research.NewAnalysisRunner(service, collector)
		runner.ConfigureAudit(researchaudit.NewRecorder(researchaudit.NewRepository(mainDB)))
		runner.ConfigureEvidence(marketdata.NewRepository(mainDB), researchThemeEvidenceProfile)
		runner.ConfigureKnowledge(NewKnowledgeService(mainDB))
	}
	return &ResearchRuntime{Repository: repository, Service: service, Runner: runner}, nil
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
