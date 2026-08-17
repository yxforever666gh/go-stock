package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	repository *Repository
	ai         AIClient
	quotes     QuoteProvider
	calendar   TradingCalendar
	now        func() time.Time
	serial     sync.Mutex
	analysisMu sync.Mutex
}

func NewService(repository *Repository, ai AIClient, quotes QuoteProvider, calendar TradingCalendar) *Service {
	if calendar == nil {
		calendar = WeekdayCalendar{}
	}
	return &Service{repository: repository, ai: ai, quotes: quotes, calendar: calendar, now: time.Now}
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
		signalDate := ShanghaiTime(recommendation.SignalAt).Format("2006-01-02")
		currentDate := ShanghaiTime(now).Format("2006-01-02")
		if signalDate > currentDate || (signalDate == currentDate && !IsAfterMarketClose(now)) {
			continue
		}
		if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{
			"status": "invalidated", "last_decision": "失效", "last_decision_at": now, "next_check_at": nil,
		}); err != nil {
			return err
		}
		_ = s.repository.AppendDecision(ctx, &DecisionEvent{
			EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: "失效", DecidedAt: now,
			Reason: "收盘后仍未激活，推荐当日失效",
		})
	}
	return nil
}

func (s *Service) processOne(ctx context.Context, recommendation *Recommendation, now time.Time) error {
	if recommendation.Status == "sell_pending" {
		return s.retrySell(ctx, recommendation, now)
	}
	phase, allowed := "activation", map[string]bool{"等待": true, "激活": true, "失效": true}
	if recommendation.Status == "active" {
		phase, allowed = "holding", map[string]bool{"持有": true, "卖出": true}
	}
	decisionQuote, err := s.quotes.CurrentQuote(ctx, recommendation.StockCode)
	if err != nil {
		return err
	}
	prompt := lifecyclePrompt(*recommendation, phase, now, decisionQuote)
	userMessage := LifecycleMessage{RecommendationID: recommendation.RecommendationID, Role: "user", Phase: phase, Content: prompt, CreatedAt: now}
	if err := s.repository.AppendMessage(ctx, &userMessage); err != nil {
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
	decision, err := parseLifecycleDecision(result.Content, allowed)
	if err != nil {
		return err
	}
	decisionAt := s.now()
	if err := s.repository.UpdateRecommendation(ctx, recommendation.RecommendationID, map[string]any{
		"previous_response_id": result.ResponseID, "last_decision": decision.Action, "last_decision_at": decisionAt,
	}); err != nil {
		return err
	}
	event := DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: decision.Action, DecidedAt: decisionAt, AIResponse: result.Content, Reason: decision.Reason}
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
	return s.repository.AppendDecision(ctx, &DecisionEvent{EventID: newID(), RecommendationID: recommendation.RecommendationID, DecisionType: "错误重试", DecidedAt: now, Reason: processErr.Error()})
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
	if err != nil || detail.Position == nil {
		return detail, err
	}
	position := detail.Position
	if position.Status == "open" {
		if quote, quoteErr := s.quotes.CurrentQuote(ctx, position.StockCode); quoteErr == nil && quote.Price > 0 {
			position.CurrentPrice, position.CurrentPriceAt = quote.Price, &quote.At
			_ = s.repository.UpdatePositionQuote(ctx, position.ID, quote)
		}
	}
	enrichPositionValue(position)
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

func lifecyclePrompt(recommendation Recommendation, phase string, now time.Time, quote Quote) string {
	quoteText := fmt.Sprintf("最新行情时间：%s，价格：%.3f，昨收：%.3f，停牌：%t，涨停：%t，跌停：%t。", quote.At.Format(time.RFC3339), quote.Price, quote.PreviousClose, quote.Suspended, quote.LimitUp, quote.LimitDown)
	if phase == "activation" {
		return fmt.Sprintf("现在是 %s。只判断股票 %s(%s) 的激活条件是否成立。原始激活条件：%s。%s 结合该股票独立会话，只返回 JSON：{\"action\":\"等待|激活|失效\",\"reason\":\"简明理由\"}。如果后续已经不容乐观可直接失效。", now.Format(time.RFC3339), recommendation.StockName, recommendation.StockCode, recommendation.ActivationCondition, quoteText)
	}
	return fmt.Sprintf("现在是 %s。只判断已持有股票 %s(%s) 应继续持有还是卖出。%s 结合风险和该股票独立会话，只返回 JSON：{\"action\":\"持有|卖出\",\"reason\":\"简明理由\"}。", now.Format(time.RFC3339), recommendation.StockName, recommendation.StockCode, quoteText)
}

type lifecycleDecision struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func parseLifecycleDecision(content string, allowed map[string]bool) (lifecycleDecision, error) {
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
	return decision, nil
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
