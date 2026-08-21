package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func newID() string { return uuid.NewString() }

type CompletionRequest struct {
	RecommendationID   string
	Phase              string
	Prompt             string
	Messages           []LifecycleMessage
	PreviousResponseID string
	OnAttempt          func(ModelAttemptRecord)
}

type CompletionResult struct {
	Content    string
	ResponseID string
	Model      string
}

type AIClient interface {
	Complete(context.Context, CompletionRequest) (CompletionResult, error)
}

type QuoteProvider interface {
	CurrentQuote(context.Context, string) (Quote, error)
}

type TradingCalendar interface {
	IsTradingDay(context.Context, time.Time) (bool, error)
}

type WeekdayCalendar struct{}

func (WeekdayCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	weekday := ShanghaiTime(value).Weekday()
	return weekday != time.Saturday && weekday != time.Sunday, nil
}

type Service struct {
	repository      *Repository
	ai              AIClient
	quotes          QuoteProvider
	contextProvider LifecycleContextProvider
	chartProvider   RecommendationChartProvider
	calendar        TradingCalendar
	reviewSchedule  SellReviewSchedule
	now             func() time.Time
	serial          sync.Mutex // serializes capacity admission and direct buys
	analysisMu      sync.Mutex
	lifecycleScanMu sync.Mutex
	lifecycleMu     sync.Mutex
	lifecycleActive map[string]struct{}
	chartMu         sync.Mutex
	chartRefreshing map[string]struct{}
}

func NewService(repository *Repository, ai AIClient, quotes QuoteProvider, calendar TradingCalendar, providers ...LifecycleContextProvider) *Service {
	if calendar == nil {
		calendar = WeekdayCalendar{}
	}
	var provider LifecycleContextProvider
	if len(providers) > 0 {
		provider = providers[0]
	}
	if provider == nil {
		provider = quoteLifecycleContextProvider{quotes: quotes}
	}
	return &Service{repository: repository, ai: ai, quotes: quotes, contextProvider: provider, calendar: calendar,
		reviewSchedule: DefaultSellReviewSchedule(), now: time.Now,
		lifecycleActive: make(map[string]struct{}), chartRefreshing: make(map[string]struct{})}
}

func (s *Service) SetSellReviewSchedule(schedule SellReviewSchedule) {
	s.reviewSchedule = schedule
}

func (s *Service) firstSellCheck(ctx context.Context, entryAt time.Time) (time.Time, error) {
	return FirstSellCheckWithSchedule(ctx, s.calendar, entryAt, s.reviewSchedule)
}

func (s *Service) nextSellCheck(ctx context.Context, after time.Time) (time.Time, error) {
	return NextSellCheckWithSchedule(ctx, s.calendar, after, s.reviewSchedule)
}

// SetRecommendationChartProvider installs the minute-cache adapter used by
// the research chart endpoints. It is separate from the lifecycle collector:
// a cached chart read must never trigger an upstream request.
func (s *Service) SetRecommendationChartProvider(provider RecommendationChartProvider) {
	s.chartMu.Lock()
	s.chartProvider = provider
	s.chartMu.Unlock()
}

// quoteLifecycleContextProvider keeps direct service construction useful for
// small integrations and tests. The formal runtime always installs the richer
// data adapter from backend/data.
type quoteLifecycleContextProvider struct{ quotes QuoteProvider }

func (provider quoteLifecycleContextProvider) CollectLifecycleContext(ctx context.Context, request LifecycleContextRequest) (LifecycleObservationDraft, error) {
	quote, err := provider.quotes.CurrentQuote(ctx, request.Recommendation.StockCode)
	if err != nil {
		return LifecycleObservationDraft{Status: "critical_failed", CriticalFailure: "实时行情不可用: " + err.Error()}, nil
	}
	quoteID := LifecycleSourceID(request.ObservationID, LifecycleQuoteSourceSuffix)
	minuteID := LifecycleSourceID(request.ObservationID, LifecycleMinuteSourceSuffix)
	minute := MinuteEvidenceSummary{TradingDate: quote.At.Format("2006-01-02"), LatestAt: quote.At, LatestPrice: quote.Price, TotalBars: 1,
		Windows: []MinuteWindowSummary{{Minutes: 15, Bars: 1, High: quote.Price, Low: quote.Price, AveragePrice: quote.Price}}}
	return LifecycleObservationDraft{Quote: quote, MinuteSummary: minute, Status: "ready", Sources: []LifecycleEvidenceSource{
		{ID: quoteID, Name: "实时行情", Category: "quote", Status: "ok", CollectedAt: request.Now, Content: fmt.Sprintf("价格：%.3f，行情时间：%s", quote.Price, quote.At.Format(time.RFC3339))},
		{ID: minuteID, Name: "分钟量价", Category: "minute", Status: "ok", CollectedAt: request.Now, Content: "兼容采集器使用当前行情作为分钟证据"},
	}}, nil
}

func (s *Service) Repository() *Repository { return s.repository }

// recommendationCapacity waits for a possibly in-progress scheduled deposit
// or direct buy, then releases the trade lock immediately. The multi-stage AI
// run must never hold this lock while waiting on external services.
func (s *Service) recommendationCapacity(ctx context.Context) (RecommendationCapacity, error) {
	s.serial.Lock()
	defer s.serial.Unlock()
	return s.repository.RecommendationCapacity(ctx)
}

// EnqueueRecommendation persists one AI recommendation and either attempts its
// single direct buy immediately or schedules that attempt for the next valid
// trading session. The same serial lock is shared with the lifecycle scanner so
// account competition is deterministic.
func (s *Service) EnqueueRecommendation(ctx context.Context, recommendation *Recommendation, initial []LifecycleMessage) error {
	s.serial.Lock()
	defer s.serial.Unlock()
	now := s.now()
	next, err := NextTradingSessionOpen(ctx, s.calendar, now)
	if err != nil {
		return err
	}
	recommendation.Status, recommendation.NextCheckAt = "buy_pending", &next
	recommendation.ReservedCash = MaxCashPerTrade
	initialDecision := &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID,
		DecisionType: "待买入", DecidedAt: now, Reason: "AI 推荐已入库，按策略仅尝试一次直接买入"}
	if err := s.repository.CreateRecommendationWithinCapacity(ctx, recommendation, initial, initialDecision); err != nil {
		return err
	}
	if next.After(now) {
		return nil
	}
	if err := s.attemptBuy(ctx, recommendation, now); err != nil {
		// Admission already committed successfully. An internal calendar/database
		// fault must not turn the parent analysis into a failed report while the
		// queued recommendation remains visible and reserved. Leave it retryable.
		_ = s.deferBuyProcessingError(ctx, recommendation.RecommendationID, now, err)
	}
	return nil
}

func (s *Service) ProcessDue(ctx context.Context) error {
	// A minute cron tick must not accumulate behind a slow model response. The
	// next tick simply observes that this scan is still running and exits.
	if !s.lifecycleScanMu.TryLock() {
		return nil
	}
	defer s.lifecycleScanMu.Unlock()
	now := s.now()
	trading, err := s.calendar.IsTradingDay(ctx, now)
	if err != nil {
		return err
	}
	if !trading || !IsTradingSession(now) {
		due, dueErr := s.repository.DueRecommendations(ctx, now)
		if dueErr != nil {
			return dueErr
		}
		for index := range due {
			if due[index].Status != "buy_pending" {
				continue
			}
			next, nextErr := NextTradingSessionOpen(ctx, s.calendar, now)
			if nextErr != nil {
				return nextErr
			}
			if err := s.repository.UpdateRecommendation(ctx, due[index].RecommendationID, map[string]any{"next_check_at": next}); err != nil {
				return err
			}
		}
		return nil
	}
	due, err := s.repository.DueRecommendations(ctx, now)
	if err != nil {
		return err
	}
	// Direct buys remain strictly ordered by due time/signal/id so competing
	// recommendations consume cash deterministically.
	for index := range due {
		if due[index].Status != "buy_pending" {
			continue
		}
		s.serial.Lock()
		taskNow := s.now()
		processErr := s.processOne(ctx, &due[index], taskNow)
		if processErr != nil {
			_ = s.recordError(ctx, due[index], taskNow, processErr)
		}
		s.serial.Unlock()
	}

	// Holding/sell-pending stocks have isolated messages and may collect data
	// and wait for their models independently. At most five are active at once.
	sem := make(chan struct{}, 5)
	var wait sync.WaitGroup
	var errorMu sync.Mutex
	var resultErr error
	for index := range due {
		recommendation := due[index]
		if recommendation.Status != "active" && recommendation.Status != "sell_pending" {
			continue
		}
		if !s.beginLifecycle(recommendation.RecommendationID) {
			continue
		}
		sem <- struct{}{}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { <-sem }()
			defer s.endLifecycle(recommendation.RecommendationID)
			taskNow := s.now()
			if err := s.processOne(ctx, &recommendation, taskNow); err != nil {
				recordErr := s.recordError(ctx, recommendation, taskNow, err)
				errorMu.Lock()
				if recordErr != nil {
					resultErr = errors.Join(resultErr, err, recordErr)
				}
				errorMu.Unlock()
			}
		}()
	}
	wait.Wait()
	return resultErr
}

func (s *Service) beginLifecycle(recommendationID string) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if _, exists := s.lifecycleActive[recommendationID]; exists {
		return false
	}
	s.lifecycleActive[recommendationID] = struct{}{}
	return true
}

func (s *Service) endLifecycle(recommendationID string) {
	s.lifecycleMu.Lock()
	delete(s.lifecycleActive, recommendationID)
	s.lifecycleMu.Unlock()
}

func (s *Service) processOne(ctx context.Context, recommendation *Recommendation, now time.Time) error {
	if recommendation.Status == "buy_pending" {
		return s.attemptBuy(ctx, recommendation, now)
	}
	if recommendation.Status != "active" && recommendation.Status != "sell_pending" {
		return nil
	}
	current, positionErr := s.repository.Position(ctx, recommendation.RecommendationID)
	if positionErr != nil {
		return positionErr
	}
	firstCheck, err := s.firstSellCheck(ctx, current.EntryAt)
	if err != nil {
		return err
	}
	effectiveDue := recommendation.NextCheckAt
	if effectiveDue == nil || effectiveDue.Before(firstCheck) {
		effectiveDue = &firstCheck
	}
	local := ShanghaiTime(now)
	y, m, d := local.Date()
	staleDue := now.Sub(*effectiveDue) > 2*time.Minute
	firstToday := time.Date(y, m, d, s.reviewSchedule.StartHour, s.reviewSchedule.StartMinute, 0, 0, shanghaiLocation)
	if now.Before(firstCheck) || local.Before(firstToday) || staleDue {
		next, err := s.nextSellCheck(ctx, now)
		if err != nil {
			return err
		}
		return s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"next_check_at": next})
	}
	if recommendation.Status == "sell_pending" {
		return s.retrySell(ctx, recommendation, now)
	}
	allowed := map[string]bool{"持有": true, "卖出": true}
	windowFrom := recommendation.SignalAt
	if previous, previousErr := s.repository.LastUsableObservation(ctx, recommendation.RecommendationID); previousErr == nil && previous.ObservedAt.After(windowFrom) {
		windowFrom = previous.ObservedAt
	} else if previousErr != nil && !errors.Is(previousErr, gorm.ErrRecordNotFound) {
		return previousErr
	}
	knownFingerprints, err := s.repository.ObservationFingerprints(ctx, recommendation.RecommendationID, 200)
	if err != nil {
		return err
	}
	enrichPositionValue(&current)
	position := &current
	contextRequest := LifecycleContextRequest{ObservationID: newID(), Recommendation: *recommendation, Phase: "holding",
		WindowFrom: windowFrom, Now: now, Position: position, KnownFingerprints: knownFingerprints}
	draft, err := s.contextProvider.CollectLifecycleContext(ctx, contextRequest)
	if err != nil {
		return err
	}
	if position != nil && draft.Quote.Price > 0 {
		position.CurrentPrice, position.CurrentPriceAt = draft.Quote.Price, &draft.Quote.At
		enrichPositionValue(position)
	}
	observation, err := NewLifecycleObservation(contextRequest, draft)
	if err != nil {
		return err
	}
	if err := s.repository.AppendObservation(ctx, &observation); err != nil {
		return err
	}
	if observation.Status == "critical_failed" || strings.TrimSpace(observation.CriticalFailure) != "" {
		return s.deferForCriticalData(ctx, recommendation, observation, now)
	}
	prompt := lifecyclePrompt(*recommendation, now, observation, position)
	userMessage := LifecycleMessage{RecommendationID: recommendation.RecommendationID, Role: "user", Phase: "holding", Content: prompt, CreatedAt: now}
	if err := s.repository.AppendMessage(ctx, &userMessage); err != nil {
		return err
	}
	if err := s.repository.MarkObservationModelInvoked(ctx, observation.ObservationID); err != nil {
		return err
	}
	messages, err := s.repository.Messages(ctx, recommendation.RecommendationID)
	if err != nil {
		return err
	}
	request := CompletionRequest{RecommendationID: recommendation.RecommendationID, Phase: "holding", Prompt: prompt, PreviousResponseID: recommendation.PreviousResponseID}
	if recommendation.PreviousResponseID == "" {
		// The first remote response chain is seeded from this recommendation's
		// locally persisted context, never from the shared final-decision call.
		request.Messages = compressMessages(messages, 24)
	}
	result, err := s.ai.Complete(ctx, request)
	if err != nil && recommendation.PreviousResponseID != "" {
		// Some OpenAI-compatible relays do not implement previous_response_id.
		// Retry once with this stock's local history only.
		request.PreviousResponseID = ""
		request.Messages = compressMessages(messages, 24)
		result, err = s.ai.Complete(ctx, request)
	}
	if err != nil {
		return err
	}
	assistantMessage := LifecycleMessage{
		RecommendationID: recommendation.RecommendationID, Role: "assistant", Phase: "holding", Content: result.Content,
		ResponseID: result.ResponseID, PreviousResponseID: recommendation.PreviousResponseID, Model: result.Model, CreatedAt: s.now(),
	}
	if err := s.repository.AppendMessage(ctx, &assistantMessage); err != nil {
		return err
	}
	decision, err := parseLifecycleDecision(result.Content, allowed, observation)
	if err != nil {
		return err
	}
	decisionAt := s.now()
	trading, tradingErr := s.calendar.IsTradingDay(ctx, decisionAt)
	if tradingErr != nil {
		return tradingErr
	}
	if !trading || !IsTradingSession(decisionAt) {
		next, nextErr := s.nextSellCheck(ctx, decisionAt)
		if nextErr != nil {
			return nextErr
		}
		if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{
			"previous_response_id": result.ResponseID, "next_check_at": next,
		}); err != nil {
			return err
		}
		return s.repository.AppendDecision(ctx, &DecisionEvent{
			EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: "响应跨休市", DecidedAt: decisionAt,
			AIResponse: result.Content, Reason: "模型响应时市场已休市，本次判断不执行，顺延至下一固定卖出检查时点",
			SourceRefs: marshalSourceRefs(decision.SourceRefs), DataStatus: observation.Status,
		})
	}
	if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{
		"previous_response_id": result.ResponseID, "last_decision": decision.Action, "last_decision_at": decisionAt,
	}); err != nil {
		return err
	}
	event := DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: decision.Action, DecidedAt: decisionAt,
		AIResponse: result.Content, Reason: decision.Reason, SourceRefs: marshalSourceRefs(decision.SourceRefs), DataStatus: observation.Status}
	if err := s.repository.AppendDecision(ctx, &event); err != nil {
		return err
	}
	switch decision.Action {
	case "持有":
		next, nextErr := s.nextSellCheck(ctx, decisionAt)
		if nextErr != nil {
			return nextErr
		}
		return s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"next_check_at": next})
	case "卖出":
		return s.trySell(ctx, recommendation, decisionAt)
	default:
		return errors.New("unreachable lifecycle action")
	}
}

func (s *Service) deferForCriticalData(ctx context.Context, recommendation *Recommendation, observation LifecycleObservation, now time.Time) error {
	next, err := s.nextSellCheck(ctx, now)
	if err != nil {
		return err
	}
	if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"next_check_at": next}); err != nil {
		return err
	}
	reason := strings.TrimSpace(observation.CriticalFailure)
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID,
		DecisionType: "数据不足重试", DecidedAt: now, Reason: reason, DataStatus: "critical_failed"})
}

func (s *Service) attemptBuy(ctx context.Context, recommendation *Recommendation, now time.Time) error {
	nextSell, err := s.firstSellCheck(ctx, now)
	if err != nil {
		return err
	}
	quote, quoteErr := s.quotes.CurrentQuote(ctx, recommendation.StockCode)
	if quoteErr != nil {
		return s.repository.FailBuy(ctx, recommendation.RecommendationID, "missed_untradable", "错过—不可交易",
			"一次性买入行情读取失败: "+quoteErr.Error(), now, nil)
	}
	if err := validateBuyQuoteAt(now, quote); err != nil {
		return s.repository.FailBuy(ctx, recommendation.RecommendationID, "missed_untradable", "错过—不可交易",
			"一次性买入校验失败: "+err.Error(), now, &quote)
	}
	quoteCode, quoteCodeOK := NormalizeMainlandCode(quote.Code)
	if !quoteCodeOK || quoteCode != recommendation.StockCode || !sameStockName(quote.Name, recommendation.StockName) {
		return s.repository.FailBuy(ctx, recommendation.RecommendationID, "missed_untradable", "错过—不可交易",
			"一次性买入行情与推荐股票不匹配", now, &quote)
	}
	if err := s.repository.Buy(ctx, recommendation.RecommendationID, quote, nextSell, now); err != nil {
		if errors.Is(err, ErrInsufficientCash) || errors.Is(err, ErrMinimumOrder) {
			return s.repository.FailBuy(ctx, recommendation.RecommendationID, "missed_cash", "错过—资金不足", err.Error(), now, &quote)
		}
		return err
	}
	return nil
}

func (s *Service) trySell(ctx context.Context, recommendation *Recommendation, now time.Time) error {
	position, err := s.repository.Position(ctx, recommendation.RecommendationID)
	if err != nil {
		return err
	}
	if !isTPlusOne(position.EntryAt, now) {
		return s.deferSell(ctx, recommendation.RecommendationID, now, "T+1 限制")
	}
	quote, err := s.quotes.CurrentQuote(ctx, recommendation.StockCode)
	if err != nil {
		return err
	}
	if err := validateSellQuoteAt(now, recommendation, quote); err != nil {
		return s.deferSell(ctx, recommendation.RecommendationID, now, err.Error())
	}
	if err := s.repository.Sell(ctx, recommendation.RecommendationID, quote); err != nil {
		return err
	}
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: "模拟卖出", DecidedAt: now, Reason: "按最新可交易行情成交", QuotePrice: quote.Price, QuoteAt: &quote.At})
}

func (s *Service) retrySell(ctx context.Context, recommendation *Recommendation, now time.Time) error {
	return s.trySell(ctx, recommendation, now)
}

func (s *Service) deferSell(ctx context.Context, recommendationID string, now time.Time, reason string) error {
	next, err := s.nextSellCheck(ctx, now)
	if err != nil {
		return err
	}
	if err := s.repository.UpdateRecommendation(ctx, recommendationID, map[string]any{"status": "sell_pending", "next_check_at": next}); err != nil {
		return err
	}
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendationID, DecisionType: "待卖", DecidedAt: now, Reason: reason})
}

func (s *Service) recordError(ctx context.Context, recommendation Recommendation, now time.Time, processErr error) error {
	if recommendation.Status == "buy_pending" {
		return s.deferBuyProcessingError(ctx, recommendation.RecommendationID, now, processErr)
	}
	next, err := s.nextSellCheck(ctx, now)
	if err != nil {
		return err
	}
	if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"next_check_at": next}); err != nil {
		return err
	}
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID,
		DecisionType: "错误重试", DecidedAt: now, Reason: processErr.Error(), DataStatus: "model_error"})
}

func (s *Service) deferBuyProcessingError(ctx context.Context, recommendationID string, now time.Time, processErr error) error {
	next, err := NextTradingSessionOpen(ctx, s.calendar, now.Add(time.Minute))
	if err != nil {
		return err
	}
	return s.repository.DeferBuyProcessingError(ctx, recommendationID,
		"一次性买入内部处理失败，尚未向市场提交模拟成交: "+processErr.Error(), next, now)
}

func (s *Service) AccountOverview(ctx context.Context) (AccountOverview, error) {
	return s.accountOverview(ctx, true)
}

func (s *Service) Detail(ctx context.Context, recommendationID string) (RecommendationDetail, error) {
	detail, err := s.repository.Detail(ctx, recommendationID)
	if err != nil {
		return detail, err
	}
	if detail.Position != nil {
		position := detail.Position
		if position.Status == "open" {
			if quote, quoteErr := s.quotes.CurrentQuote(ctx, position.StockCode); quoteErr == nil && quote.Price > 0 {
				position.CurrentPrice, position.CurrentPriceAt = quote.Price, &quote.At
				_ = s.repository.UpdatePositionQuote(ctx, position.ID, quote)
			}
		}
		enrichPositionValue(position)
	}
	enrichRecommendationAmounts(&detail.Recommendation, detail.Trades, detail.Position)
	return detail, nil
}

func enrichPositionValue(position *Position) {
	if position == nil || position.Quantity <= 0 {
		return
	}
	invested := position.EntryPrice*float64(position.Quantity) + position.BuyFees
	if position.Status == "closed" {
		position.EstimatedSellFees = position.SellFees
		position.NetSellValue = position.ExitPrice*float64(position.Quantity) - position.SellFees
	} else if position.CurrentPrice > 0 {
		sell := CalculateSellCost(position.CurrentPrice, position.Quantity)
		position.EstimatedSellFees = sell.TotalFees
		position.NetSellValue = sell.NetCashFlow
		position.NetPnL = sell.NetCashFlow - invested
	}
	if invested > 0 {
		position.NetYieldRate = position.NetPnL / invested
	}
}

func lifecyclePrompt(recommendation Recommendation, now time.Time, observation LifecycleObservation, position *Position) string {
	quoteID := LifecycleSourceID(observation.ObservationID, LifecycleQuoteSourceSuffix)
	minuteID := LifecycleSourceID(observation.ObservationID, LifecycleMinuteSourceSuffix)
	sources := ParseLifecycleEvidence(observation)
	var evidence strings.Builder
	perSourceBudget := 30000
	if len(sources) > 0 {
		perSourceBudget /= len(sources)
	}
	if perSourceBudget < 800 {
		perSourceBudget = 800
	}
	for _, source := range sources {
		line := fmt.Sprintf("[%s] %s（%s，状态=%s）", source.ID, source.Name, source.Category, source.Status)
		if source.Error != "" {
			line += "，错误=" + truncateLifecycleText(source.Error, 500)
		}
		if source.Content != "" {
			line += "：" + truncateLifecycleText(source.Content, perSourceBudget-len(line)-2)
		}
		evidence.WriteString(line + "\n")
	}
	common := fmt.Sprintf("现在是 %s。只判断股票 %s(%s)，不得混入其他股票。原始 AI 摘要：%s。主要风险：%s。\n本轮观察编号：%s，数据状态：%s，增量窗口：%s 至 %s。\n本轮证据：\n%s\n最新证据优先于历史记忆；失败来源不得补造内容。sourceRefs 只能填写本轮方括号中的来源编号。",
		now.Format(time.RFC3339), recommendation.StockName, recommendation.StockCode, recommendation.AISummary,
		recommendation.MainRisk, observation.ObservationID, observation.Status,
		observation.WindowFrom.Format(time.RFC3339), observation.ObservedAt.Format(time.RFC3339), evidence.String())
	positionText := "持仓数据不可用"
	if position != nil {
		positionText = fmt.Sprintf("买入时间=%s，成本价=%.3f，数量=%d，当前价=%.3f，预估净收益=%.2f，预估净收益率=%.4f，T+1可卖=%t，预估卖出费用=%.2f",
			position.EntryAt.Format(time.RFC3339), position.EntryPrice, position.Quantity, position.CurrentPrice, position.NetPnL,
			position.NetYieldRate, isTPlusOne(position.EntryAt, now), position.EstimatedSellFees)
	}
	return common + fmt.Sprintf("\n当前持仓：%s。只返回 JSON：{\"action\":\"持有|卖出\",\"reason\":\"简明理由\",\"sourceRefs\":[\"来源编号\"],\"dataSufficiency\":\"充足|不足\"}。判断卖出时必须引用 %s 和 %s；证据不足只能持有。", positionText, quoteID, minuteID)
}

func truncateLifecycleText(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	const marker = "...<截断>"
	end := maxBytes - len(marker)
	if end < 0 {
		end = 0
	}
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + marker
}

type lifecycleDecision struct {
	Action          string   `json:"action"`
	Reason          string   `json:"reason"`
	SourceRefs      []string `json:"sourceRefs"`
	DataSufficiency string   `json:"dataSufficiency"`
}

func parseLifecycleDecision(content string, allowed map[string]bool, observations ...LifecycleObservation) (lifecycleDecision, error) {
	trimmed := strings.TrimSpace(content)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	var decision lifecycleDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &decision); err != nil {
		return decision, fmt.Errorf("invalid lifecycle JSON: %w", err)
	}
	if !allowed[decision.Action] {
		return decision, fmt.Errorf("disallowed lifecycle action %q", decision.Action)
	}
	if strings.TrimSpace(decision.Reason) == "" {
		return decision, errors.New("lifecycle reason is required")
	}
	if len(observations) == 0 {
		return decision, nil
	}
	observation := observations[0]
	seen := make(map[string]struct{}, len(decision.SourceRefs))
	for _, sourceRef := range decision.SourceRefs {
		sourceRef = strings.TrimSpace(sourceRef)
		if sourceRef == "" {
			continue
		}
		if !ObservationHasSource(observation, sourceRef) {
			return decision, fmt.Errorf("lifecycle sourceRef %q is not a usable source in the latest observation", sourceRef)
		}
		seen[sourceRef] = struct{}{}
	}
	if decision.Action == "卖出" {
		sufficiency := strings.ToLower(strings.TrimSpace(decision.DataSufficiency))
		if sufficiency != "充足" && sufficiency != "sufficient" && sufficiency != "ready" {
			return decision, errors.New("sale requires sufficient latest evidence")
		}
		required := []string{LifecycleSourceID(observation.ObservationID, LifecycleQuoteSourceSuffix), LifecycleSourceID(observation.ObservationID, LifecycleMinuteSourceSuffix)}
		for _, sourceID := range required {
			if _, ok := seen[sourceID]; !ok {
				return decision, fmt.Errorf("%s decision must cite latest source %s", decision.Action, sourceID)
			}
		}
	}
	return decision, nil
}

func marshalSourceRefs(sourceRefs []string) string {
	value, _ := json.Marshal(sourceRefs)
	return string(value)
}

func compressMessages(messages []LifecycleMessage, max int) []LifecycleMessage {
	if len(messages) <= max {
		return messages
	}
	result := make([]LifecycleMessage, 0, max)
	result = append(result, messages[0])
	result = append(result, messages[len(messages)-(max-1):]...)
	return result
}

func validateBuyQuote(quote Quote) error {
	if quote.Price <= 0 || quote.Suspended || quote.LimitUp || quote.LimitDown {
		return errors.New("quote is not tradable")
	}
	if _, ok := NormalizeMainlandCode(quote.Code); !ok {
		return errors.New("quote is not a Shanghai/Shenzhen A share")
	}
	if strings.TrimSpace(quote.Name) == "" {
		return errors.New("quote name is empty")
	}
	return nil
}

func validateBuyQuoteAt(now time.Time, quote Quote) error {
	if err := validateBuyQuote(quote); err != nil {
		return err
	}
	if quote.At.IsZero() {
		return errors.New("quote time is empty")
	}
	localNow, localQuote := ShanghaiTime(now), ShanghaiTime(quote.At)
	if localNow.Format("2006-01-02") != localQuote.Format("2006-01-02") {
		return fmt.Errorf("quote trading date is stale: %s", localQuote.Format(time.RFC3339))
	}
	lag := localNow.Sub(localQuote)
	if lag > 20*time.Minute || lag < -2*time.Minute {
		return fmt.Errorf("quote time is stale or invalid: %s", lag.Round(time.Second))
	}
	return nil
}

func validateSellQuoteAt(now time.Time, recommendation *Recommendation, quote Quote) error {
	if quote.Price <= 0 || quote.Suspended || quote.LimitDown {
		return errors.New("停牌、跌停或行情不可交易")
	}
	if quote.At.IsZero() {
		return errors.New("卖出行情缺少有效时间")
	}
	quoteCode, ok := NormalizeMainlandCode(quote.Code)
	if !ok || recommendation == nil || quoteCode != recommendation.StockCode || !sameStockName(quote.Name, recommendation.StockName) {
		return errors.New("卖出行情与持仓股票不匹配")
	}
	localNow, localQuote := ShanghaiTime(now), ShanghaiTime(quote.At)
	if localNow.Format("2006-01-02") != localQuote.Format("2006-01-02") {
		return fmt.Errorf("卖出行情日期滞后: %s", localQuote.Format(time.RFC3339))
	}
	lag := localNow.Sub(localQuote)
	if lag > 20*time.Minute || lag < -2*time.Minute {
		return fmt.Errorf("卖出行情时间异常: %s", lag.Round(time.Second))
	}
	return nil
}

func isTPlusOne(entryAt, now time.Time) bool {
	entry := ShanghaiTime(entryAt)
	current := ShanghaiTime(now)
	ey, em, ed := entry.Date()
	cy, cm, cd := current.Date()
	return time.Date(cy, cm, cd, 0, 0, 0, 0, shanghaiLocation).After(time.Date(ey, em, ed, 0, 0, 0, 0, shanghaiLocation))
}

var _ = gorm.ErrRecordNotFound
