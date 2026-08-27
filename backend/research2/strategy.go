package research2

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/research"

	"github.com/google/uuid"
)

//go:embed prompts/overnight_strength.md
var strategyPrompt string

type Evidence struct {
	Prompt           string
	SourceStatusJSON string
	Candidates       []research.StockCandidate
}

type EvidenceCollector interface {
	Collect(context.Context, time.Time) (Evidence, error)
}

type Calendar interface {
	IsTradingDay(context.Context, time.Time) (bool, error)
}

type PriceSnapshot struct {
	Code      string
	Name      string
	Price     float64
	At        time.Time
	Suspended bool
	LimitUp   bool
	LimitDown bool
	Source    string
}

type MetricSnapshot struct {
	HitFiveBeforeSell bool
	HitLimitUpFullDay bool
	HitMinusThree     bool
}

type MarketProvider interface {
	PriceAt(context.Context, string, time.Time, bool) (PriceSnapshot, error)
	Metrics(context.Context, Recommendation) (MetricSnapshot, error)
}

type modelOutput struct {
	TradingDay      bool                  `json:"tradingDay"`
	Conclusion      string                `json:"conclusion"`
	ReportMarkdown  string                `json:"reportMarkdown"`
	Recommendations []modelRecommendation `json:"recommendations"`
}

type modelRecommendation struct {
	Rank             int      `json:"rank"`
	Code             string   `json:"code"`
	Name             string   `json:"name"`
	MarketScore      float64  `json:"marketScore"`
	SectorScore      float64  `json:"sectorScore"`
	StockScore       float64  `json:"stockScore"`
	CatalystScore    float64  `json:"catalystScore"`
	RiskDeduction    float64  `json:"riskDeduction"`
	FinalScore       float64  `json:"finalScore"`
	ReferencePrice   float64  `json:"referencePrice"`
	BuyLower         float64  `json:"buyLower"`
	BuyUpper         float64  `json:"buyUpper"`
	Summary          string   `json:"summary"`
	QuantData        string   `json:"quantData"`
	FreshCatalyst    string   `json:"freshCatalyst"`
	OldBackground    string   `json:"oldBackground"`
	MainRisk         string   `json:"mainRisk"`
	CancelConditions string   `json:"cancelConditions"`
	SourceRefs       []string `json:"sourceRefs"`
}

type Runner struct {
	repository *Repository
	ai         research.AIClient
	collector  EvidenceCollector
	calendar   Calendar
	now        func() time.Time
	waitUntil  func(context.Context, time.Time) error
	mu         sync.Mutex
}

func NewRunner(repository *Repository, ai research.AIClient, collector EvidenceCollector, calendar Calendar) *Runner {
	return &Runner{repository: repository, ai: ai, collector: collector, calendar: calendar, now: time.Now, waitUntil: waitUntil}
}

// ConfigureReplayClock lets an isolated historical replay use the production
// runner without waiting for wall-clock time. Production construction leaves
// both sources untouched.
func (r *Runner) ConfigureReplayClock(now func() time.Time, wait func(context.Context, time.Time) error) {
	if r == nil {
		return
	}
	if now != nil {
		r.now = now
	}
	if wait != nil {
		r.waitUntil = wait
	}
}

func waitUntil(ctx context.Context, target time.Time) error {
	delay := time.Until(target)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) Run(ctx context.Context, scheduledFor time.Time) (AnalysisRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	local := scheduledFor.In(shanghai())
	tradingDate := local.Format("2006-01-02")
	if existing, ok, err := r.repository.RunForDate(ctx, tradingDate); err != nil {
		return AnalysisRun{}, err
	} else if ok {
		return existing, nil
	}
	now := r.now().In(shanghai())
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 9, 55, 0, 0, shanghai())
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: tradingDate, ScheduledFor: scheduledFor, StartedAt: now, EvidenceCutoffAt: cutoff, Status: "running", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	if err := r.repository.CreateRun(ctx, &run); err != nil {
		return run, err
	}
	finishFailure := func(status, reason string, cause error) (AnalysisRun, error) {
		completed := r.now().In(shanghai())
		run.GeneratedAt = &completed
		run.Status = status
		run.FailureReason = reason
		run.OnTime = !completed.After(time.Date(local.Year(), local.Month(), local.Day(), 10, 0, 0, 0, shanghai()))
		_ = r.repository.SaveRun(ctx, &run)
		return run, cause
	}
	tradeDay, err := r.calendar.IsTradingDay(ctx, local)
	if err != nil {
		return finishFailure("failed", "无法严格确认A股交易日: "+err.Error(), err)
	}
	if !tradeDay {
		run.ReportMarkdown = "今日不是A股正常交易日，不执行选股。"
		return finishFailure("skipped_non_trading_day", "今日不是A股正常交易日，不执行选股。", nil)
	}
	if now.Hour() > 15 || (now.Hour() == 15 && now.Minute() > 0) {
		return finishFailure("missed_window", "报告生成窗口已经结束，今日不执行交易。", nil)
	}
	evidence, err := r.collector.Collect(ctx, cutoff)
	if err != nil {
		return finishFailure("failed", "策略证据采集失败: "+err.Error(), err)
	}
	run.SourceStatusJSON = defaultJSON(evidence.SourceStatusJSON, "[]")
	if r.now().Before(cutoff) {
		if err = r.waitUntil(ctx, cutoff); err != nil {
			return finishFailure("failed", "等待09:55证据截止失败: "+err.Error(), err)
		}
	}
	attempts := make(map[string]research.ModelAttemptRecord)
	modelDeadline := time.Date(local.Year(), local.Month(), local.Day(), 10, 5, 0, 0, shanghai())
	if current := r.now().In(shanghai()); !current.Before(modelDeadline) {
		modelDeadline = current.Add(5 * time.Minute)
		latest := time.Date(local.Year(), local.Month(), local.Day(), 14, 55, 0, 0, shanghai())
		if modelDeadline.After(latest) {
			modelDeadline = latest
		}
	}
	modelBudget := modelDeadline.Sub(r.now().In(shanghai()))
	if modelBudget <= 0 {
		modelBudget = 5 * time.Minute
	}
	modelCtx, cancelModel := context.WithTimeout(ctx, modelBudget)
	defer cancelModel()
	prompt := buildPrompt(evidence, cutoff)
	var result research.CompletionResult
	var output modelOutput
	for structureAttempt := 1; structureAttempt <= 2; structureAttempt++ {
		result, err = r.ai.Complete(modelCtx, research.CompletionRequest{RecommendationID: run.RunID, Phase: "research2_overnight_strength", Prompt: prompt, OnAttempt: func(record research.ModelAttemptRecord) {
			attempts[record.ID] = record
			run.ProviderName = record.ProviderName
			values := make([]research.ModelAttemptRecord, 0, len(attempts))
			for _, item := range attempts {
				values = append(values, item)
			}
			sort.Slice(values, func(i, j int) bool { return values[i].StartedAt.Before(values[j].StartedAt) })
			encoded, _ := json.Marshal(values)
			run.ModelAttemptLogJSON = string(encoded)
			persistCtx, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelPersist()
			_ = r.repository.SaveRun(persistCtx, &run)
		}})
		if err != nil {
			return finishFailure("failed", "大模型分析失败: "+err.Error(), err)
		}
		run.ModelName = result.Model
		output, err = ParseModelOutput(result.Content)
		if err == nil {
			break
		}
		if structureAttempt == 2 {
			return finishFailure("failed", "大模型结构化输出无效: "+err.Error(), err)
		}
		prompt = strings.Join([]string{
			buildPrompt(evidence, cutoff),
			"\n# 上次输出纠正要求",
			"上次响应无法解析为严格 JSON（" + err.Error() + "）。请重新生成完整结果，只输出一个合法 JSON 对象；所有中文、换行和引号必须位于 JSON 字符串内并正确转义，不得输出解释、注释或代码围栏。",
		}, "\n")
	}
	generated := r.now().In(shanghai())
	run.GeneratedAt = &generated
	run.OnTime = !generated.After(time.Date(local.Year(), local.Month(), local.Day(), 10, 0, 0, 0, shanghai()))
	run.ReportMarkdown = strings.TrimSpace(output.ReportMarkdown)
	if run.ReportMarkdown == "" {
		run.ReportMarkdown = strings.TrimSpace(output.Conclusion)
	}
	items, validationMessages := validateRecommendations(run.RunID, generated, local, evidence.Candidates, output.Recommendations)
	if len(validationMessages) > 0 {
		run.ReportMarkdown += "\n\n> 数据校验：" + strings.Join(validationMessages, "；")
	}
	if len(items) == 0 {
		run.Status = "no_recommendation"
		run.FailureReason = strings.TrimSpace(output.Conclusion)
		if run.FailureReason == "" {
			run.FailureReason = "没有满足最终分和可成交约束的标的。"
		}
	} else {
		run.Status = "success"
		run.RecommendationCount = len(items)
		if err = r.repository.CreateRecommendations(ctx, items); err != nil {
			return finishFailure("failed", "保存推荐失败: "+err.Error(), err)
		}
	}
	if err = r.repository.SaveRun(ctx, &run); err != nil {
		return run, err
	}
	return run, nil
}

func buildPrompt(evidence Evidence, cutoff time.Time) string {
	return strings.Join([]string{strategyPrompt, "\n# 本次执行的强制参数", "- 数据截止时间：" + cutoff.Format("2006-01-02 15:04:05 Asia/Shanghai"), "- 09:55冻结核心证据；报告尽量在09:55—10:00送达。", "- 正常买入时刻为当日10:00；报告晚于10:00但仍处于连续竞价时，按报告生成后的首个可成交分钟执行并标记late。", "- 卖出时刻固定为下一交易日10:00。", "- 独立账户初始资金12,000元；实际可买标的按数量等额分配现金，数量向下取整到100股并计入费用。", "- 不得编造核心行情；来源引用必须能在下方证据中找到。", "\n# 输出协议（只输出JSON，不加代码围栏）", `{"tradingDay":true,"conclusion":"...","reportMarkdown":"完整Markdown报告","recommendations":[{"rank":1,"code":"sh600000","name":"名称","marketScore":0,"sectorScore":0,"stockScore":0,"catalystScore":0,"riskDeduction":0,"finalScore":0,"referencePrice":0,"buyLower":0,"buyUpper":0,"summary":"...","quantData":"...","freshCatalyst":"...","oldBackground":"...","mainRisk":"...","cancelConditions":"...","sourceRefs":["来源名"]}]}`, "\n# 截止前采集的证据", evidence.Prompt}, "\n")
}

func ParseModelOutput(content string) (modelOutput, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	}
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return modelOutput{}, errors.New("missing JSON object")
	}
	var output modelOutput
	decoder := json.NewDecoder(strings.NewReader(content[start : end+1]))
	if err := decoder.Decode(&output); err != nil {
		return output, err
	}
	if len(output.Recommendations) > 3 {
		output.Recommendations = output.Recommendations[:3]
	}
	return output, nil
}

func validateRecommendations(runID string, generated, tradingDay time.Time, candidates []research.StockCandidate, values []modelRecommendation) ([]Recommendation, []string) {
	items := make([]Recommendation, 0, len(values))
	warnings := make([]string, 0)
	seen := map[string]struct{}{}
	allowed := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		if code, ok := research.NormalizeMainlandCode(candidate.Code); ok {
			allowed[code] = strings.TrimSpace(candidate.Name)
		}
	}
	restrictToEvidence := candidates != nil
	for _, value := range values {
		code, ok := research.NormalizeMainlandCode(value.Code)
		if !ok || !(strings.HasPrefix(code, "sh60") || strings.HasPrefix(code, "sz00")) {
			warnings = append(warnings, value.Code+"不是沪深主板普通A股")
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		evidenceName, inEvidence := allowed[code]
		if restrictToEvidence && !inEvidence {
			warnings = append(warnings, code+"不在09:55冻结候选集合中")
			continue
		}
		calculated := value.MarketScore + value.SectorScore + value.StockScore + value.CatalystScore - value.RiskDeduction
		if value.FinalScore <= 50 || math.Abs(value.FinalScore-calculated) > 1.01 {
			warnings = append(warnings, code+"评分不满足规则")
			continue
		}
		if value.ReferencePrice <= 0 || value.BuyLower <= 0 || value.BuyUpper < value.BuyLower {
			warnings = append(warnings, code+"价格区间无效")
			continue
		}
		lotCost := -research.CalculateBuyCost(value.ReferencePrice, LotSize).NetCashFlow
		if lotCost > InitialCash+1e-7 {
			warnings = append(warnings, code+"一手含费成本超过12000元")
			continue
		}
		target, late, tradable := buyTarget(generated, tradingDay)
		if !tradable {
			warnings = append(warnings, code+"报告生成时已无当日可成交窗口")
			continue
		}
		stockName := strings.TrimSpace(value.Name)
		if evidenceName != "" {
			stockName = evidenceName
		}
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: runID, Rank: value.Rank, StockCode: code, StockName: stockName, SignalAt: generated, MarketScore: value.MarketScore, SectorScore: value.SectorScore, StockScore: value.StockScore, CatalystScore: value.CatalystScore, RiskDeduction: value.RiskDeduction, FinalScore: value.FinalScore, ReferencePrice: value.ReferencePrice, BuyLower: value.BuyLower, BuyUpper: value.BuyUpper, EstimatedLotCost: roundMoney(lotCost), Summary: value.Summary, QuantData: value.QuantData, FreshCatalyst: value.FreshCatalyst, OldBackground: value.OldBackground, MainRisk: value.MainRisk, CancelConditions: value.CancelConditions, SourceRefs: strings.Join(value.SourceRefs, "\n"), Status: "buy_pending", Late: late, TargetBuyAt: target})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Rank == items[j].Rank {
			return items[i].FinalScore > items[j].FinalScore
		}
		return items[i].Rank < items[j].Rank
	})
	for index := range items {
		items[index].Rank = index + 1
	}
	return items, warnings
}

func buyTarget(generated, tradingDay time.Time) (time.Time, bool, bool) {
	local := generated.In(shanghai())
	ten := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 10, 0, 0, 0, shanghai())
	closeTime := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 15, 0, 0, 0, shanghai())
	if !local.After(ten) {
		return ten, false, true
	}
	if !local.Before(closeTime) {
		return time.Time{}, true, false
	}
	lunchStart := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 11, 30, 0, 0, shanghai())
	lunchEnd := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 13, 0, 0, 0, shanghai())
	if !local.Before(lunchStart) && local.Before(lunchEnd) {
		return lunchEnd, true, true
	}
	return local, true, true
}

func defaultJSON(value, fallback string) string {
	value = strings.TrimSpace(value)
	if !json.Valid([]byte(value)) {
		return fallback
	}
	return value
}

type TradingService struct {
	repository *Repository
	market     MarketProvider
	calendar   Calendar
	now        func() time.Time
	mu         sync.Mutex
}

func NewTradingService(repository *Repository, market MarketProvider, calendar Calendar) *TradingService {
	return &TradingService{repository: repository, market: market, calendar: calendar, now: time.Now}
}

func (s *TradingService) ProcessDue(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now = now.In(shanghai())
	tradingDay, err := s.calendar.IsTradingDay(ctx, now)
	if err != nil {
		return err
	}
	if !tradingDay {
		return nil
	}
	if !continuousAuction(now) {
		if atOrAfterClose(now) {
			return s.processBuys(ctx, now)
		}
		return nil
	}
	if err := s.processSells(ctx, now); err != nil {
		return err
	}
	return s.processBuys(ctx, now)
}

func (s *TradingService) processBuys(ctx context.Context, now time.Time) error {
	items, err := s.repository.DueRecommendations(ctx, now, []string{"buy_pending"})
	if err != nil || len(items) == 0 {
		return err
	}
	if atOrAfterClose(now) {
		for _, item := range items {
			_ = s.repository.MarkStatus(ctx, item.RecommendationID, "missed_window", "当日连续竞价已结束")
		}
		return nil
	}
	byRun := map[string][]Recommendation{}
	order := make([]string, 0)
	for _, item := range items {
		if _, ok := byRun[item.AnalysisRunID]; !ok {
			order = append(order, item.AnalysisRunID)
		}
		byRun[item.AnalysisRunID] = append(byRun[item.AnalysisRunID], item)
	}
	for _, runID := range order {
		group := byRun[runID]
		snapshots := make(map[string]PriceSnapshot)
		valid := make([]Recommendation, 0, len(group))
		for _, item := range group {
			current := item.Late
			snapshot, quoteErr := s.market.PriceAt(ctx, item.StockCode, item.TargetBuyAt, current)
			if quoteErr != nil {
				_ = s.repository.MarkStatus(ctx, item.RecommendationID, "missed_untradable", "买入行情不可用: "+quoteErr.Error())
				continue
			}
			if snapshot.Suspended || snapshot.LimitUp || snapshot.LimitDown || snapshot.Price <= 0 {
				_ = s.repository.MarkStatus(ctx, item.RecommendationID, "missed_untradable", "停牌、涨跌停或无有效成交价")
				continue
			}
			if snapshot.Price > item.BuyUpper || snapshot.Price < item.BuyLower*0.98 {
				_ = s.repository.MarkStatus(ctx, item.RecommendationID, "cancelled_price", "成交价超出策略买入区间")
				continue
			}
			snapshots[item.RecommendationID] = snapshot
			valid = append(valid, item)
		}
		if len(valid) == 0 {
			continue
		}
		overview, overviewErr := s.repository.Overview(ctx)
		if overviewErr != nil {
			return overviewErr
		}
		eligible, removed := affordableEqualAllocation(valid, snapshots, overview.Cash)
		for _, item := range removed {
			_ = s.repository.MarkStatus(ctx, item.RecommendationID, "missed_cash", "等额分仓后不足买入100股")
		}
		if len(eligible) == 0 {
			continue
		}
		allocation := overview.Cash / float64(len(eligible))
		for _, item := range eligible {
			cashCap := allocation
			quantity, cost, sizeErr := sizeWithin(snapshots[item.RecommendationID].Price, cashCap)
			if sizeErr != nil {
				_ = s.repository.MarkStatus(ctx, item.RecommendationID, "missed_cash", "等额分仓后不足买入100股")
				continue
			}
			sellAt, nextErr := s.nextTradingDayAt(ctx, item.TargetBuyAt, 10, 0)
			if nextErr != nil {
				return nextErr
			}
			tradeAt := snapshots[item.RecommendationID].At
			if tradeAt.IsZero() {
				tradeAt = now
			}
			trade := Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: tradeAt, MarketPrice: snapshots[item.RecommendationID].Price, ExecutionPrice: cost.ExecutionPrice, Quantity: quantity, Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow}
			if err = s.repository.RecordBuy(ctx, item.RecommendationID, trade, sellAt); err != nil {
				return err
			}
		}
	}
	_, err = s.repository.SaveSnapshot(ctx, "trade_cycle", now)
	return err
}

func (s *TradingService) processSells(ctx context.Context, now time.Time) error {
	items, err := s.repository.DueRecommendations(ctx, now, []string{"active", "sell_pending"})
	if err != nil {
		return err
	}
	for _, item := range items {
		current := item.Status == "sell_pending" || now.After(item.TargetSellAt.Add(2*time.Minute))
		snapshot, quoteErr := s.market.PriceAt(ctx, item.StockCode, *item.TargetSellAt, current)
		if quoteErr != nil || snapshot.Price <= 0 || snapshot.Suspended || snapshot.LimitDown {
			reason := "卖出行情不可用"
			if quoteErr != nil {
				reason += ": " + quoteErr.Error()
			}
			_ = s.repository.MarkStatus(ctx, item.RecommendationID, "sell_pending", reason)
			continue
		}
		cost := research.CalculateSellCost(snapshot.Price, item.Quantity)
		tradeAt := snapshot.At
		if tradeAt.IsZero() {
			tradeAt = now
		}
		trade := Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "sell", TradedAt: tradeAt, MarketPrice: snapshot.Price, ExecutionPrice: cost.ExecutionPrice, Quantity: item.Quantity, Commission: cost.Commission, StampDuty: cost.StampDuty, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow}
		if err = s.repository.RecordSell(ctx, item.RecommendationID, trade); err != nil {
			return err
		}
	}
	return nil
}

func continuousAuction(value time.Time) bool {
	local := value.In(shanghai())
	morningOpen := time.Date(local.Year(), local.Month(), local.Day(), 9, 30, 0, 0, shanghai())
	morningClose := time.Date(local.Year(), local.Month(), local.Day(), 11, 30, 0, 0, shanghai())
	afternoonOpen := time.Date(local.Year(), local.Month(), local.Day(), 13, 0, 0, 0, shanghai())
	afternoonClose := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, shanghai())
	return (!local.Before(morningOpen) && local.Before(morningClose)) || (!local.Before(afternoonOpen) && local.Before(afternoonClose))
}

func atOrAfterClose(value time.Time) bool {
	local := value.In(shanghai())
	closeTime := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, shanghai())
	return !local.Before(closeTime)
}

func affordableEqualAllocation(items []Recommendation, snapshots map[string]PriceSnapshot, cash float64) ([]Recommendation, []Recommendation) {
	eligible := append([]Recommendation(nil), items...)
	removed := make([]Recommendation, 0)
	for len(eligible) > 0 {
		allocation := cash / float64(len(eligible))
		failed := make([]int, 0)
		for index, item := range eligible {
			if _, _, err := sizeWithin(snapshots[item.RecommendationID].Price, allocation); err != nil {
				failed = append(failed, index)
			}
		}
		if len(failed) == 0 {
			break
		}
		// Remove one lowest-priority unaffordable candidate, then recalculate the
		// equal share. This lets the remaining actual buyable stocks move from
		// one-third to one-half (or full) allocation without exceeding cash.
		drop := failed[0]
		for _, index := range failed[1:] {
			if eligible[index].Rank > eligible[drop].Rank {
				drop = index
			}
		}
		removed = append(removed, eligible[drop])
		eligible = append(eligible[:drop], eligible[drop+1:]...)
	}
	return eligible, removed
}

func (s *TradingService) FinalizeMetrics(ctx context.Context, now time.Time) error {
	items, err := s.repository.UnfinalizedMetrics(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		metrics, metricErr := s.market.Metrics(ctx, item)
		if metricErr != nil {
			continue
		}
		if err = s.repository.FinalizeMetrics(ctx, item.RecommendationID, metrics.HitFiveBeforeSell, metrics.HitLimitUpFullDay, metrics.HitMinusThree); err != nil {
			return err
		}
	}
	_, err = s.repository.SaveSnapshot(ctx, "daily_close", now)
	return err
}

func (s *TradingService) nextTradingDayAt(ctx context.Context, from time.Time, hour, minute int) (time.Time, error) {
	day := from.In(shanghai()).AddDate(0, 0, 1)
	for checked := 0; checked < 20; checked++ {
		ok, err := s.calendar.IsTradingDay(ctx, day)
		if err != nil {
			return time.Time{}, err
		}
		if ok {
			return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, shanghai()), nil
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, errors.New("20日内找不到下一A股交易日")
}

func sizeWithin(price, cash float64) (int64, research.CostBreakdown, error) {
	if price <= 0 || cash <= 0 {
		return 0, research.CostBreakdown{}, research.ErrInsufficientCash
	}
	quantity := int64(math.Floor(cash/(price*(1+research.SlippageRate))/float64(LotSize))) * LotSize
	for quantity >= LotSize {
		cost := research.CalculateBuyCost(price, quantity)
		if -cost.NetCashFlow <= cash+1e-7 {
			return quantity, cost, nil
		}
		quantity -= LotSize
	}
	return 0, research.CostBreakdown{}, research.ErrMinimumOrder
}

func (r *Runner) Prompt() string { return strategyPrompt }

func ValidateAllocation(prices []float64, cash float64) ([]int64, error) {
	if len(prices) == 0 || len(prices) > 3 {
		return nil, fmt.Errorf("buyable recommendation count must be 1-3")
	}
	result := make([]int64, len(prices))
	for index, price := range prices {
		cap := cash / float64(len(prices))
		quantity, _, err := sizeWithin(price, cap)
		if err != nil {
			continue
		}
		result[index] = quantity
	}
	return result, nil
}
