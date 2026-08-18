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
	calendar        TradingCalendar
	now             func() time.Time
	serial          sync.Mutex
	analysisMu      sync.Mutex
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
	return &Service{repository: repository, ai: ai, quotes: quotes, contextProvider: provider, calendar: calendar, now: time.Now}
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

func (s *Service) ProcessDue(ctx context.Context) error {
	s.serial.Lock()
	defer s.serial.Unlock()
	now := s.now()
	trading, err := s.calendar.IsTradingDay(ctx, now)
	if err != nil {
		return err
	}
	if !trading {
		return nil
	}
	if err := s.expirePending(ctx, now); err != nil {
		return err
	}
	if IsAfterMarketClose(now) {
		return nil
	}
	if !IsTradingSession(now) {
		return nil
	}
	due, err := s.repository.DueRecommendations(ctx, now)
	if err != nil {
		return err
	}
	for index := range due {
		if err := s.processOne(ctx, &due[index], now); err != nil {
			_ = s.recordError(ctx, due[index], now, err)
		}
	}
	return nil
}

func (s *Service) expirePending(ctx context.Context, now time.Time) error {
	pending, err := s.repository.PendingRecommendations(ctx)
	if err != nil {
		return err
	}
	for _, recommendation := range pending {
		elapsed, elapsedErr := s.effectiveActivationElapsed(ctx, recommendation, now)
		if elapsedErr != nil {
			return elapsedErr
		}
		if elapsed < ActivationTradingWindow {
			continue
		}
		if err := s.invalidatePending(ctx, recommendation.RecommendationID, now, "累计开盘交易时长达到4小时，推荐仍未激活"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) effectiveActivationElapsed(ctx context.Context, recommendation Recommendation, now time.Time) (time.Duration, error) {
	raw, err := AccumulatedTradingTime(ctx, s.calendar, recommendation.SignalAt, now, ActivationTradingWindow+time.Duration(MaxDataPauseSecs)*time.Second)
	if err != nil {
		return 0, err
	}
	pause := time.Duration(recommendation.DataPauseSeconds) * time.Second
	if pause > time.Duration(MaxDataPauseSecs)*time.Second {
		pause = time.Duration(MaxDataPauseSecs) * time.Second
	}
	if raw <= pause {
		return 0, nil
	}
	return raw - pause, nil
}

func (s *Service) invalidatePending(ctx context.Context, recommendationID string, now time.Time, reason string) error {
	if err := s.repository.UpdateRecommendation(ctx, recommendationID, map[string]any{
		"status": "invalidated", "last_decision": "失效", "last_decision_at": now, "next_check_at": nil,
	}); err != nil {
		return err
	}
	return s.repository.AppendDecision(ctx, &DecisionEvent{
		EventID: newID(), RecommendationID: recommendationID, DecisionType: "失效", DecidedAt: now, Reason: reason,
	})
}

func (s *Service) processOne(ctx context.Context, recommendation *Recommendation, now time.Time) error {
	if recommendation.Status == "sell_pending" {
		return s.retrySell(ctx, recommendation, now)
	}
	phase, allowed := "activation", map[string]bool{"等待": true, "激活": true, "失效": true}
	if recommendation.Status == "active" {
		phase, allowed = "holding", map[string]bool{"持有": true, "卖出": true}
	}
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
	var position *Position
	if phase == "holding" {
		current, positionErr := s.repository.Position(ctx, recommendation.RecommendationID)
		if positionErr != nil {
			return positionErr
		}
		enrichPositionValue(&current)
		position = &current
	}
	contextRequest := LifecycleContextRequest{ObservationID: newID(), Recommendation: *recommendation, Phase: phase,
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
	prompt := lifecyclePrompt(*recommendation, phase, now, observation, position)
	userMessage := LifecycleMessage{RecommendationID: recommendation.RecommendationID, Role: "user", Phase: phase, Content: prompt, CreatedAt: now}
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
	request := CompletionRequest{RecommendationID: recommendation.RecommendationID, Phase: phase, Prompt: prompt, PreviousResponseID: recommendation.PreviousResponseID}
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
		RecommendationID: recommendation.RecommendationID, Role: "assistant", Phase: phase, Content: result.Content,
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
	if phase == "activation" {
		elapsed, elapsedErr := s.effectiveActivationElapsed(ctx, *recommendation, decisionAt)
		if elapsedErr != nil {
			return elapsedErr
		}
		if elapsed >= ActivationTradingWindow {
			return s.invalidatePending(ctx, recommendation.RecommendationID, decisionAt, "模型响应时累计开盘交易时长已达到4小时，推荐失效")
		}
		trading, tradingErr := s.calendar.IsTradingDay(ctx, decisionAt)
		if tradingErr != nil {
			return tradingErr
		}
		if !trading || !IsTradingSession(decisionAt) {
			next := NextLifecycleCheck(decisionAt)
			if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{
				"previous_response_id": result.ResponseID, "next_check_at": next,
			}); err != nil {
				return err
			}
			return s.repository.AppendDecision(ctx, &DecisionEvent{
				EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: "响应跨休市", DecidedAt: decisionAt,
				AIResponse: result.Content, Reason: "模型响应时市场已休市，本次判断不执行，顺延至下一开盘时段",
				SourceRefs: marshalSourceRefs(decision.SourceRefs), DataStatus: observation.Status,
			})
		}
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
	case "等待", "持有":
		next := NextLifecycleCheck(decisionAt)
		return s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"next_check_at": next})
	case "失效":
		return s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"status": "invalidated", "next_check_at": nil})
	case "激活":
		return s.activate(ctx, recommendation, decisionAt)
	case "卖出":
		return s.trySell(ctx, recommendation, decisionAt)
	default:
		return errors.New("unreachable lifecycle action")
	}
}

func (s *Service) deferForCriticalData(ctx context.Context, recommendation *Recommendation, observation LifecycleObservation, now time.Time) error {
	updates := map[string]any{"next_check_at": NextLifecycleCheck(now)}
	pauseGranted := int64(0)
	if recommendation.Status == "pending" && recommendation.DataPauseSeconds < MaxDataPauseSecs {
		pauseGranted = int64(DefaultCheckMins * 60)
		remaining := int64(MaxDataPauseSecs) - recommendation.DataPauseSeconds
		if pauseGranted > remaining {
			pauseGranted = remaining
		}
		updates["data_pause_seconds"] = recommendation.DataPauseSeconds + pauseGranted
	}
	if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, updates); err != nil {
		return err
	}
	reason := strings.TrimSpace(observation.CriticalFailure)
	if pauseGranted > 0 {
		reason += fmt.Sprintf("；本轮抵扣激活交易时长 %d 分钟，累计最多抵扣 30 分钟", pauseGranted/60)
	}
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID,
		DecisionType: "数据不足重试", DecidedAt: now, Reason: reason, DataStatus: "critical_failed"})
}

func (s *Service) activate(ctx context.Context, recommendation *Recommendation, now time.Time) error {
	quote, err := s.quotes.CurrentQuote(ctx, recommendation.StockCode)
	if err != nil {
		return err
	}
	if err = validateBuyQuote(quote); err != nil {
		next := NextLifecycleCheck(now)
		return s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"next_check_at": next})
	}
	err = s.repository.Buy(ctx, recommendation.RecommendationID, quote, now)
	if err == nil {
		return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: "模拟买入", DecidedAt: now, Reason: "AI 判断激活后按最新可交易行情成交", QuotePrice: quote.Price, QuoteAt: &quote.At})
	}
	if strings.Contains(err.Error(), "insufficient cash") || strings.Contains(err.Error(), "minimum order") {
		if updateErr := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"status": "missed_cash", "next_check_at": nil}); updateErr != nil {
			return updateErr
		}
		return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: "错过—资金不足", DecidedAt: now, Reason: err.Error(), QuotePrice: quote.Price, QuoteAt: &quote.At})
	}
	return err
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
	if quote.Suspended || quote.LimitDown || quote.Price <= 0 {
		return s.deferSell(ctx, recommendation.RecommendationID, now, "停牌、跌停或行情不可交易")
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
	next := NextLifecycleCheck(now)
	if err := s.repository.UpdateRecommendation(ctx, recommendationID, map[string]any{"status": "sell_pending", "next_check_at": next}); err != nil {
		return err
	}
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendationID, DecisionType: "待卖", DecidedAt: now, Reason: reason})
}

func (s *Service) recordError(ctx context.Context, recommendation Recommendation, now time.Time, processErr error) error {
	next := NextLifecycleCheck(now)
	if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{"next_check_at": next}); err != nil {
		return err
	}
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID,
		DecisionType: "错误重试", DecidedAt: now, Reason: processErr.Error(), DataStatus: "model_error"})
}

func (s *Service) AccountOverview(ctx context.Context) (AccountOverview, error) {
	account, err := s.repository.Account(ctx)
	if err != nil {
		return AccountOverview{}, err
	}
	positions, err := s.repository.OpenPositions(ctx)
	if err != nil {
		return AccountOverview{}, err
	}
	value := 0.0
	now := s.now()
	for index := range positions {
		quote, quoteErr := s.quotes.CurrentQuote(ctx, positions[index].StockCode)
		if quoteErr == nil && quote.Price > 0 {
			positions[index].CurrentPrice, positions[index].CurrentPriceAt = quote.Price, &quote.At
			_ = s.repository.UpdatePositionQuote(ctx, positions[index].ID, quote)
		}
		if positions[index].CurrentPrice > 0 {
			enrichPositionValue(&positions[index])
			value += positions[index].NetSellValue
		}
	}
	nav := account.Cash + value
	return AccountOverview{InitialCash: account.InitialCash, Cash: account.Cash, PositionValue: value, NetAssetValue: nav,
		NetProfit: nav - account.InitialCash, NetYieldRate: (nav - account.InitialCash) / account.InitialCash, ValuedAt: now, Positions: positions}, nil
}

func (s *Service) Detail(ctx context.Context, recommendationID string) (RecommendationDetail, error) {
	detail, err := s.repository.Detail(ctx, recommendationID)
	if err != nil {
		return detail, err
	}
	if detail.Recommendation.Status == "pending" {
		elapsed, elapsedErr := s.effectiveActivationElapsed(ctx, detail.Recommendation, s.now())
		if elapsedErr == nil {
			detail.ActivationTradingElapsedSeconds = int64(elapsed / time.Second)
			remaining := ActivationTradingWindow - elapsed
			if remaining < 0 {
				remaining = 0
			}
			detail.ActivationRemainingSeconds = int64(remaining / time.Second)
		}
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

func lifecyclePrompt(recommendation Recommendation, phase string, now time.Time, observation LifecycleObservation, position *Position) string {
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
	common := fmt.Sprintf("现在是 %s。只判断股票 %s(%s)，不得混入其他股票。原始 AI 摘要：%s。原始激活条件：%s。主要风险：%s。\n本轮观察编号：%s，数据状态：%s，增量窗口：%s 至 %s。\n本轮证据：\n%s\n最新证据优先于历史记忆；失败来源不得补造内容。sourceRefs 只能填写本轮方括号中的来源编号。",
		now.Format(time.RFC3339), recommendation.StockName, recommendation.StockCode, recommendation.AISummary,
		recommendation.ActivationCondition, recommendation.MainRisk, observation.ObservationID, observation.Status,
		observation.WindowFrom.Format(time.RFC3339), observation.ObservedAt.Format(time.RFC3339), evidence.String())
	if phase == "activation" {
		return common + fmt.Sprintf("\n只返回 JSON：{\"action\":\"等待|激活|失效\",\"reason\":\"简明理由\",\"sourceRefs\":[\"来源编号\"],\"dataSufficiency\":\"充足|不足\"}。判断激活时必须引用 %s 和 %s；证据不足只能等待，后续已经不容乐观可直接失效。", quoteID, minuteID)
	}
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
	if decision.Action == "激活" || decision.Action == "卖出" {
		sufficiency := strings.ToLower(strings.TrimSpace(decision.DataSufficiency))
		if sufficiency != "充足" && sufficiency != "sufficient" && sufficiency != "ready" {
			return decision, errors.New("activation or sale requires sufficient latest evidence")
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

func isTPlusOne(entryAt, now time.Time) bool {
	entry := ShanghaiTime(entryAt)
	current := ShanghaiTime(now)
	ey, em, ed := entry.Date()
	cy, cm, cd := current.Date()
	return time.Date(cy, cm, cd, 0, 0, 0, 0, shanghaiLocation).After(time.Date(ey, em, ed, 0, 0, 0, 0, shanghaiLocation))
}

var _ = gorm.ErrRecordNotFound
