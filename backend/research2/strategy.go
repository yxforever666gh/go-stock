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
	"unicode/utf8"

	"go-stock/backend/knowledge"
	"go-stock/backend/research"
	"go-stock/backend/researchaudit"

	"github.com/google/uuid"
)

//go:embed prompts/overnight_strength.md
var strategyPrompt string

var ErrOutsideMorningStartWindow = errors.New("research2 analysis start is outside the morning window")

type Evidence struct {
	Prompt                   string
	SourceStatusJSON         string
	Candidates               []research.StockCandidate
	Documents                []research.SourceDocument
	EvidenceProfileVersion   string
	EvidenceSetID            string
	CutoffAt                 time.Time
	WindowStartAt            time.Time
	CoveragePct              float64
	Degraded                 bool
	DegradedReasons          []string
	CandidateReferencePrices map[string]float64
}

type EvidenceCollector interface {
	Collect(context.Context, time.Time) (Evidence, error)
}

type RunEvidenceCollector interface {
	CollectForRun(context.Context, string, time.Time) (Evidence, error)
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
	TradingDay bool   `json:"tradingDay"`
	Conclusion string `json:"conclusion"`
	// ReportMarkdown is accepted only for compatibility with archived model
	// fixtures. New runs always render the user-facing report on the server.
	ReportMarkdown  string                `json:"reportMarkdown,omitempty"`
	Recommendations []modelRecommendation `json:"recommendations"`
}

type modelRecommendation struct {
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
	audit      *researchaudit.Recorder
	knowledge  knowledge.ResearchRetriever
	mu         sync.Mutex
}

// ConfigureKnowledge gives Research 2 only the narrow approved-retrieval
// capability; no document or approval methods are exposed to this runner.
func (r *Runner) ConfigureKnowledge(retriever knowledge.ResearchRetriever) {
	if r != nil {
		r.knowledge = retriever
	}
}

func (r *Runner) ConfigureAudit(recorder *researchaudit.Recorder) {
	if r != nil {
		r.audit = recorder
	}
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
	now := r.now().In(shanghai())
	startWindow := time.Date(local.Year(), local.Month(), local.Day(), 9, 50, 0, 0, shanghai())
	lastStartExclusive := time.Date(local.Year(), local.Month(), local.Day(), 11, 30, 0, 0, shanghai())
	if now.Before(startWindow) || !now.Before(lastStartExclusive) {
		return AnalysisRun{}, ErrOutsideMorningStartWindow
	}
	cutoff := now
	windowStart := cutoff.Truncate(time.Minute).Add(-5 * time.Minute)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: tradingDate, ScheduledFor: scheduledFor, StartedAt: now, EvidenceCutoffAt: cutoff, EvidenceWindowStartAt: &windowStart, StrategyVersion: "research2-trailing5-v6", Status: "running", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
	selected, created, err := r.repository.CreateRunAttempt(ctx, &run, true)
	if err != nil {
		return run, err
	}
	if !created {
		return selected, nil
	}
	run = selected
	auditStarted := false
	if r.audit != nil {
		if err := r.audit.Begin(ctx, researchaudit.OwnerResearch2, run.RunID); err != nil {
			completed := r.now().In(shanghai())
			run.GeneratedAt, run.Status, run.FailureReason = &completed, "failed", "启动研究审计失败: "+err.Error()
			if saveErr := r.repository.SaveRun(ctx, &run); saveErr != nil {
				return run, errors.Join(err, fmt.Errorf("保存研究运行失败: %w", saveErr))
			}
			return run, err
		}
		auditStarted = true
	}
	finishFailure := func(status, reason string, cause error) (AnalysisRun, error) {
		completed := r.now().In(shanghai())
		run.GeneratedAt = &completed
		run.Status = status
		run.FailureReason = reason
		run.OnTime = !completed.After(time.Date(local.Year(), local.Month(), local.Day(), 10, 0, 0, 0, shanghai()))
		if saveErr := r.repository.SaveRun(ctx, &run); saveErr != nil {
			cause = errors.Join(cause, fmt.Errorf("保存研究运行失败: %w", saveErr))
		}
		if auditStarted {
			auditCtx, cancelAudit := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelAudit()
			if cause != nil {
				_ = r.audit.Fail(auditCtx, researchaudit.OwnerResearch2, run.RunID, cause)
			} else {
				_ = r.audit.Complete(auditCtx, researchaudit.OwnerResearch2, run.RunID)
			}
		}
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
	var evidence Evidence
	if collector, ok := r.collector.(RunEvidenceCollector); ok {
		evidence, err = collector.CollectForRun(ctx, run.RunID, cutoff)
	} else {
		evidence, err = r.collector.Collect(ctx, cutoff)
	}
	run.SourceStatusJSON = defaultJSON(evidence.SourceStatusJSON, "[]")
	if !evidence.CutoffAt.IsZero() {
		cutoff = evidence.CutoffAt.In(shanghai())
		run.EvidenceCutoffAt = cutoff
	} else {
		evidence.CutoffAt = cutoff
	}
	if !evidence.WindowStartAt.IsZero() {
		windowStart = evidence.WindowStartAt.In(shanghai())
		evidence.WindowStartAt = windowStart
		run.EvidenceWindowStartAt = &windowStart
	} else {
		windowStart = cutoff.Truncate(time.Minute).Add(-5 * time.Minute)
		evidence.WindowStartAt = windowStart
		run.EvidenceWindowStartAt = &windowStart
	}
	coverage := evidence.CoveragePct
	run.EvidenceCoveragePct = &coverage
	degraded := evidence.Degraded
	run.Degraded = &degraded
	if evidence.EvidenceSetID != "" {
		run.EvidenceProfileVersion, run.EvidenceSetID = evidence.EvidenceProfileVersion, evidence.EvidenceSetID
	}
	// Persist the collector's authoritative cutoff even when collection failed,
	// so the failure report and immutable audit point at the same snapshot.
	if saveErr := r.repository.SaveRun(ctx, &run); saveErr != nil {
		if err == nil {
			return run, saveErr
		}
		err = errors.Join(err, fmt.Errorf("保存证据关联失败: %w", saveErr))
	}
	if err != nil {
		return finishFailure("failed", "策略证据采集失败: "+err.Error(), err)
	}
	if r.knowledge != nil {
		retrieval, retrievalErr := r.knowledge.RetrieveForResearch(ctx, knowledge.ResearchRetrievalRequest{OwnerType: "research2", OwnerID: run.RunID, Query: research2KnowledgeQuery(evidence.Prompt), CutoffAt: cutoff, Limit: 5, ExperimentalEnabled: true})
		if retrievalErr == nil && strings.TrimSpace(retrieval.Prompt) != "" {
			evidence.Prompt = strings.TrimSpace(evidence.Prompt) + "\n\n" + retrieval.Prompt
		}
	}
	attempts := make(map[string]research.ModelAttemptRecord)
	modelCtx := ctx
	prompt := buildPrompt(evidence, cutoff)
	var result research.CompletionResult
	var output modelOutput
	var sourceValidationMessages []string
	for structureAttempt := 1; structureAttempt <= 2; structureAttempt++ {
		phase := "research2_overnight_strength"
		if structureAttempt > 1 {
			phase += "_repair"
		}
		if r.audit != nil {
			prepared, prepareErr := r.audit.Prepare(modelCtx, researchaudit.CallInput{OwnerType: researchaudit.OwnerResearch2, OwnerID: run.RunID, Phase: phase, CallSequence: structureAttempt, Attempt: 1, CutoffAt: &cutoff, Prompt: prompt, Evidence: research2AuditEvidence(evidence), Tools: []string{}, ModelParameters: map[string]any{}, Template: strategyPrompt, TemplateVersion: "research2-trailing5-v6"})
			err = prepareErr
			if err != nil {
				return finishFailure("failed", "准备研究审计载荷失败: "+err.Error(), err)
			}
			prompt = prepared.Prompt
		}
		callAttempts := make(map[string]research.ModelAttemptRecord)
		result, err = r.ai.Complete(modelCtx, research.CompletionRequest{RecommendationID: run.RunID, Phase: "research2_overnight_strength", Prompt: prompt, OnAttempt: func(record research.ModelAttemptRecord) {
			attempts[record.ID] = record
			callAttempts[record.ID] = record
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
		if r.audit != nil {
			attemptValues := make([]research.ModelAttemptRecord, 0, len(callAttempts))
			for _, item := range callAttempts {
				attemptValues = append(attemptValues, item)
			}
			sort.Slice(attemptValues, func(i, j int) bool { return attemptValues[i].StartedAt.Before(attemptValues[j].StartedAt) })
			auditCtx, cancelAudit := context.WithTimeout(context.WithoutCancel(modelCtx), 15*time.Second)
			auditErr := r.recordResearch2Attempts(auditCtx, run.RunID, phase, structureAttempt, cutoff, prompt, evidence, attemptValues, result, structureAttempt > 1, err)
			cancelAudit()
			if auditErr != nil {
				return finishFailure("failed", "保存研究审计载荷失败: "+auditErr.Error(), auditErr)
			}
		}
		if err != nil {
			return finishFailure("failed", "大模型分析失败: "+err.Error(), err)
		}
		run.ModelName = result.Model
		output, err = ParseModelOutput(result.Content)
		if err == nil {
			sourceValidationMessages = validateModelSourceRefs(output.Recommendations, evidence.Documents, cutoff, evidence.Candidates)
		}
		if err == nil && len(sourceValidationMessages) == 0 {
			break
		}
		if structureAttempt == 2 {
			if err != nil {
				return finishFailure("failed", "大模型结构化输出无效: "+err.Error(), err)
			}
			break
		}
		repairReason := ""
		if err != nil {
			repairReason = "上次响应无法解析为严格 JSON（" + err.Error() + "）。"
		} else {
			repairReason = "上次响应的来源引用未通过校验（" + strings.Join(sourceValidationMessages, "；") + "）。"
		}
		prompt = strings.Join([]string{
			buildPrompt(evidence, cutoff),
			"\n# 上次输出纠正要求",
			repairReason + "请重新生成完整结果，只输出一个合法 JSON 对象；sourceRefs只能填写证据中存在且适用于该股票的sourceId；所有中文、换行和引号必须位于 JSON 字符串内并正确转义，不得输出解释、注释或代码围栏。",
		}, "\n")
	}
	// Complete all source, score, candidate and price validation before taking
	// the authoritative server-side signal timestamp.
	items, validationMessages := validateRecommendationsWithEvidence(run.RunID, cutoff, local, cutoff, evidence.Candidates, evidence.Documents, evidence.CandidateReferencePrices, output.Recommendations)
	generated := r.now().In(shanghai())
	run.GeneratedAt = &generated
	run.OnTime = !generated.After(time.Date(local.Year(), local.Month(), local.Day(), 10, 0, 0, 0, shanghai()))
	for index := range items {
		target, late, status := recommendationExecution(generated, local)
		items[index].SignalAt = generated
		items[index].Late = late
		items[index].TargetBuyAt = target
		items[index].Status = status
		if status == "analysis_only" {
			items[index].FailureReason = "报告在当日连续竞价结束后完成，仅保存分析，不进入模拟交易"
		}
	}
	run.ReportMarkdown = renderAnalysisReport(run, evidence, output, items, validationMessages)
	if len(items) == 0 {
		run.Status = "no_recommendation"
		run.FailureReason = strings.TrimSpace(output.Conclusion)
		if run.FailureReason == "" {
			run.FailureReason = "没有满足最终分和可成交约束的标的。"
		}
	} else {
		run.Status = "success"
		run.RecommendationCount = len(items)
	}
	if err = r.repository.FinalizeRun(ctx, &run, items); err != nil {
		if auditStarted {
			_ = r.audit.Fail(context.Background(), researchaudit.OwnerResearch2, run.RunID, err)
		}
		return run, err
	}
	if auditStarted {
		auditCtx, cancelAudit := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelAudit()
		if err = r.audit.Complete(auditCtx, researchaudit.OwnerResearch2, run.RunID); err != nil {
			return run, err
		}
	}
	return run, nil
}

func research2AuditEvidence(evidence Evidence) map[string]any {
	return map[string]any{
		"evidenceSetId": evidence.EvidenceSetID,
		"cutoffAt":      evidence.CutoffAt,
		"sourceStatus":  json.RawMessage(defaultJSON(evidence.SourceStatusJSON, "[]")),
		"documents":     evidence.Documents,
	}
}

func (r *Runner) recordResearch2Attempts(ctx context.Context, runID, phase string, callSequence int, cutoff time.Time, prompt string, evidence Evidence, attempts []research.ModelAttemptRecord, result research.CompletionResult, repaired bool, callErr error) error {
	if r == nil || r.audit == nil {
		return nil
	}
	if len(attempts) == 0 {
		prepared, err := r.audit.Prepare(ctx, researchaudit.CallInput{
			OwnerType: researchaudit.OwnerResearch2, OwnerID: runID, Phase: phase, CallSequence: callSequence, Attempt: 1,
			CutoffAt: &cutoff, Prompt: prompt, Evidence: research2AuditEvidence(evidence), Tools: []string{},
			ModelParameters: map[string]any{}, Template: strategyPrompt, TemplateVersion: "research2-trailing5-v6",
		})
		if err != nil {
			return err
		}
		logText := "no provider attempt was emitted"
		if callErr != nil {
			logText += ": " + callErr.Error()
		}
		return r.audit.Record(ctx, prepared, researchaudit.CallResult{RepairLog: logText})
	}
	for index, attempt := range attempts {
		prepared, err := r.audit.Prepare(ctx, researchaudit.CallInput{
			OwnerType: researchaudit.OwnerResearch2, OwnerID: runID, Phase: phase, CallSequence: callSequence, Attempt: index + 1,
			ProviderName: attempt.ProviderName, ModelName: attempt.ModelName, CutoffAt: &cutoff, Prompt: prompt,
			Evidence: research2AuditEvidence(evidence), Tools: []string{}, ModelParameters: map[string]any{
				"attempt": attempt,
			}, Template: strategyPrompt, TemplateVersion: "research2-trailing5-v6",
		})
		if err != nil {
			return err
		}
		attemptJSON, _ := json.Marshal(attempt)
		callResult := researchaudit.CallResult{
			ProviderName: attempt.ProviderName, ModelName: attempt.ModelName, ActualConfigID: attempt.ConfigID,
			RepairLog: string(attemptJSON), ModelParameters: research.AuditModelParameters([]research.ModelAttemptRecord{attempt}),
		}
		if attempt.Status == "success" || (index == len(attempts)-1 && callErr == nil) {
			if repaired {
				callResult.RepairedResponse = result.Content
			} else {
				callResult.RawResponse = result.Content
			}
		}
		if index == len(attempts)-1 && callErr != nil {
			callResult.RepairLog += "\nerror=" + callErr.Error()
		}
		if err = r.audit.Record(ctx, prepared, callResult); err != nil {
			return err
		}
	}
	return nil
}

func research2KnowledgeQuery(evidencePrompt string) string {
	value := strings.TrimSpace(evidencePrompt)
	if len(value) > 1024 {
		end := 1024
		for end > 0 && !utf8.ValidString(value[:end]) {
			end--
		}
		value = value[:end]
	}
	if value == "" {
		return "市场 题材 风险 行业 个股"
	}
	return "市场 题材 风险 行业 个股 " + value
}

func buildPrompt(evidence Evidence, cutoff time.Time) string {
	windowStart := evidence.WindowStartAt.In(shanghai())
	if evidence.WindowStartAt.IsZero() {
		windowStart = cutoff.Truncate(time.Minute).Add(-5 * time.Minute)
	}
	return strings.Join([]string{
		strategyPrompt,
		"\n# 本次执行参数",
		"- 策略版本：research2-trailing5-v6",
		"- 核心证据窗口：[" + windowStart.Format("2006-01-02 15:04:05") + ", " + cutoff.Format("2006-01-02 15:04:05") + "] Asia/Shanghai",
		"- 数据截止时间：" + cutoff.Format("2006-01-02 15:04:05 Asia/Shanghai"),
		"- 上午启动后允许跨越午盘继续完成；完成时间只用于统计，不得导致推荐失败。",
		"- 程序将在报告校验完成后获取第一笔有效行情买入；15:00后完成的推荐仅保存分析，不交易；已买入标的卖出目标固定为下一交易日10:00。",
		"- 独立账户初始资金12,000元；最多选择3只，一手100股含费用成本不得超过账户资金。",
		"- 不得访问外部地址、推算缺失值或编造行情；只能使用下方注入的结构化证据。",
		"- sourceRefs只能填写证据sources中存在且适用于该股票的sourceId，不得填写来源名称或URL。",
		"- recommendations是入库清单：程序将按分项重新计算最终分，计算结果>50才可入库。",
		"- 不要输出报告生成时间、买入时间、买入区间或Markdown报告，这些由服务端生成。",
		"\n# 输出协议（只输出JSON，不加代码围栏）",
		`{"tradingDay":true,"conclusion":"简洁结论，不含时间","recommendations":[{"code":"sh600000","name":"名称","marketScore":0,"sectorScore":0,"stockScore":0,"catalystScore":0,"riskDeduction":0,"finalScore":0,"referencePrice":0,"summary":"...","quantData":"...","freshCatalyst":"...","oldBackground":"...","mainRisk":"...","cancelConditions":"...","sourceRefs":["source-id"]}]}`,
		"\n# 系统注入的紧凑结构化证据",
		strings.TrimSpace(evidence.Prompt),
	}, "\n")
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
	return output, nil
}

func validateRecommendations(runID string, generated, tradingDay time.Time, candidates []research.StockCandidate, values []modelRecommendation) ([]Recommendation, []string) {
	return validateRecommendationsWithEvidence(runID, generated, tradingDay, generated, candidates, nil, nil, values)
}

func validateRecommendationsWithEvidence(runID string, generated, tradingDay, cutoff time.Time, candidates []research.StockCandidate, documents []research.SourceDocument, referencePrices map[string]float64, values []modelRecommendation) ([]Recommendation, []string) {
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
			warnings = append(warnings, code+"不在本次冻结候选集合中")
			continue
		}
		if sourceWarnings := validateRecommendationSourceRefs(code, value.SourceRefs, documents, cutoff, candidates); len(sourceWarnings) > 0 {
			warnings = append(warnings, sourceWarnings...)
			continue
		}
		if scoreWarnings := validateRecommendationScoreEvidence(code, value, documents, cutoff, candidates); len(scoreWarnings) > 0 {
			warnings = append(warnings, scoreWarnings...)
			continue
		}
		if !validScoreComponent(value.MarketScore, 20) || !validScoreComponent(value.SectorScore, 30) || !validScoreComponent(value.StockScore, 40) || !validScoreComponent(value.CatalystScore, 10) || !validScoreComponent(value.RiskDeduction, 25) {
			warnings = append(warnings, code+"评分分项超出允许范围")
			continue
		}
		calculated := value.MarketScore + value.SectorScore + value.StockScore + value.CatalystScore - value.RiskDeduction
		if math.Abs(value.FinalScore-calculated) > 0.01 {
			warnings = append(warnings, code+"分项加总与最终分不一致，已按分项重新计算")
		}
		if calculated <= 50 {
			warnings = append(warnings, code+"重新计算后的最终分未超过50")
			continue
		}
		referencePrice := value.ReferencePrice
		if referencePrices != nil {
			var priceExists bool
			referencePrice, priceExists = referencePrices[code]
			if !priceExists {
				warnings = append(warnings, code+"缺少截止点证据参考价")
				continue
			}
		}
		if referencePrice <= 0 || math.IsNaN(referencePrice) || math.IsInf(referencePrice, 0) {
			warnings = append(warnings, code+"参考价格无效")
			continue
		}
		lotCost := -research.CalculateBuyCost(referencePrice, LotSize).NetCashFlow
		if lotCost > InitialCash+1e-7 {
			warnings = append(warnings, code+"一手含费成本超过12000元")
			continue
		}
		stockName := strings.TrimSpace(value.Name)
		if evidenceName != "" {
			stockName = evidenceName
		}
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: runID, StockCode: code, StockName: stockName, SignalAt: generated, MarketScore: value.MarketScore, SectorScore: value.SectorScore, StockScore: value.StockScore, CatalystScore: value.CatalystScore, RiskDeduction: value.RiskDeduction, FinalScore: calculated, ReferencePrice: referencePrice, BuyLower: 0, BuyUpper: 0, EstimatedLotCost: roundMoney(lotCost), Summary: value.Summary, QuantData: value.QuantData, FreshCatalyst: value.FreshCatalyst, OldBackground: value.OldBackground, MainRisk: value.MainRisk, CancelConditions: value.CancelConditions, SourceRefs: strings.Join(value.SourceRefs, "\n"), Status: "buy_pending", TargetBuyAt: generated})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].FinalScore == items[j].FinalScore {
			return items[i].StockCode < items[j].StockCode
		}
		return items[i].FinalScore > items[j].FinalScore
	})
	if len(items) > 3 {
		items = items[:3]
	}
	return items, warnings
}

func validScoreComponent(value, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= maximum
}

func validateModelSourceRefs(values []modelRecommendation, documents []research.SourceDocument, cutoff time.Time, candidates []research.StockCandidate) []string {
	if documents == nil {
		return nil
	}
	warnings := make([]string, 0)
	for _, value := range values {
		code, ok := research.NormalizeMainlandCode(value.Code)
		if !ok {
			code = strings.TrimSpace(value.Code)
		}
		warnings = append(warnings, validateRecommendationSourceRefs(code, value.SourceRefs, documents, cutoff, candidates)...)
		warnings = append(warnings, validateRecommendationScoreEvidence(code, value, documents, cutoff, candidates)...)
	}
	return warnings
}

func validateRecommendationScoreEvidence(code string, value modelRecommendation, documents []research.SourceDocument, cutoff time.Time, candidates []research.StockCandidate) []string {
	if documents == nil {
		return nil
	}
	byID := make(map[string]research.SourceDocument, len(documents))
	for _, document := range documents {
		byID[strings.TrimSpace(document.SourceID)] = document
	}
	supported := map[string]bool{}
	for _, ref := range value.SourceRefs {
		document, ok := byID[strings.TrimSpace(ref)]
		if !ok || document.AvailableAt == nil || document.AvailableAt.After(cutoff) || strings.TrimSpace(document.Error) != "" || !research2CitationPayloadUsable(document.Content) || !sourceDocumentAppliesToStock(document, code, candidates) {
			continue
		}
		category := strings.ToLower(strings.TrimSpace(document.Category))
		name := strings.ToLower(strings.TrimSpace(document.SourceName))
		sourceID := strings.ToLower(strings.TrimSpace(document.SourceID))
		if category == "market" || strings.HasPrefix(sourceID, "research2:market:") {
			supported["market"] = true
		}
		if category == "sector" || category == "theme" || strings.Contains(sourceID, ":sector:") || strings.Contains(sourceID, ":concept:") {
			supported["sector"] = true
		}
		if category == "stock" || category == "quote" || category == "minute" || strings.HasPrefix(sourceID, "research2:quote:") || strings.HasPrefix(sourceID, "research2:minutes:") {
			supported["stock"] = true
		}
		if category == "catalyst" || strings.Contains(name, "新闻") || strings.Contains(name, "公告") || strings.Contains(name, "催化") || strings.Contains(name, "热点") || strings.Contains(name, "互动") {
			supported["catalyst"] = true
		}
	}
	warnings := make([]string, 0, 4)
	for _, requirement := range []struct {
		name  string
		score float64
		label string
	}{
		{name: "market", score: value.MarketScore, label: "市场"},
		{name: "sector", score: value.SectorScore, label: "板块"},
		{name: "stock", score: value.StockScore, label: "个股"},
		{name: "catalyst", score: value.CatalystScore, label: "催化"},
	} {
		if requirement.score > 0 && !supported[requirement.name] {
			warnings = append(warnings, code+requirement.label+"评分缺少对应可用来源")
		}
	}
	return warnings
}

func validateRecommendationSourceRefs(code string, refs []string, documents []research.SourceDocument, cutoff time.Time, candidates []research.StockCandidate) []string {
	// A nil document slice represents a legacy collector that cannot expose
	// stable source IDs. New trailing5 evidence always supplies Documents.
	if documents == nil {
		return nil
	}
	if len(refs) == 0 {
		return []string{code + "未提供sourceRefs"}
	}
	byID := make(map[string]research.SourceDocument, len(documents))
	for _, document := range documents {
		if id := strings.TrimSpace(document.SourceID); id != "" {
			byID[id] = document
		}
	}
	warnings := make([]string, 0)
	seen := make(map[string]struct{}, len(refs))
	for _, rawRef := range refs {
		ref := strings.TrimSpace(rawRef)
		if ref == "" {
			warnings = append(warnings, code+"包含空sourceRef")
			continue
		}
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		document, exists := byID[ref]
		if !exists {
			warnings = append(warnings, code+"引用不存在的来源"+ref)
			continue
		}
		if strings.TrimSpace(document.Error) != "" || strings.TrimSpace(document.Content) == "" {
			warnings = append(warnings, code+"引用不可用来源"+ref)
			continue
		}
		if !research2CitationPayloadUsable(document.Content) {
			warnings = append(warnings, code+"引用非可用状态来源"+ref)
			continue
		}
		if document.AvailableAt == nil {
			warnings = append(warnings, code+"引用缺少可验证时间的来源"+ref)
			continue
		}
		if document.AvailableAt.After(cutoff) {
			warnings = append(warnings, code+"引用截止后来源"+ref)
			continue
		}
		if !sourceDocumentAppliesToStock(document, code, candidates) {
			warnings = append(warnings, code+"引用属于其他股票的来源"+ref)
		}
	}
	return warnings
}

func research2CitationPayloadUsable(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" || content == "null" || content == "[]" || content == "{}" {
		return false
	}
	var envelope map[string]any
	if json.Unmarshal([]byte(content), &envelope) != nil || envelope == nil {
		return true
	}
	if success, exists := envelope["success"].(bool); exists && !success {
		return false
	}
	status, _ := envelope["status"].(string)
	if status == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "partial", "frozen":
		return true
	default:
		return false
	}
}

func sourceDocumentAppliesToStock(document research.SourceDocument, code string, candidates []research.StockCandidate) bool {
	category := strings.ToLower(strings.TrimSpace(document.Category))
	stockScoped := category == "stock" || category == "quote" || category == "minute" || strings.HasPrefix(category, "stock_")
	if !stockScoped {
		return true
	}
	code = strings.ToLower(strings.TrimSpace(code))
	digits := strings.TrimPrefix(strings.TrimPrefix(code, "sh"), "sz")
	haystack := strings.ToLower(strings.Join([]string{document.SourceID, document.SourceName, document.Content}, " "))
	if strings.Contains(haystack, code) || (len(digits) == 6 && strings.Contains(haystack, digits)) {
		return true
	}
	// If a stock-scoped source clearly mentions another frozen candidate, it
	// cannot be cited for this one. Sources without any entity marker are also
	// rejected because ownership cannot be proven.
	for _, candidate := range candidates {
		candidateCode, ok := research.NormalizeMainlandCode(candidate.Code)
		if !ok || candidateCode == code {
			continue
		}
		candidateDigits := strings.TrimPrefix(strings.TrimPrefix(candidateCode, "sh"), "sz")
		if strings.Contains(haystack, candidateCode) || strings.Contains(haystack, candidateDigits) {
			return false
		}
	}
	return false
}

func renderAnalysisReport(run AnalysisRun, evidence Evidence, output modelOutput, items []Recommendation, warnings []string) string {
	generated := run.StartedAt
	if run.GeneratedAt != nil {
		generated = run.GeneratedAt.In(shanghai())
	}
	windowStart := run.StartedAt.Add(-5 * time.Minute)
	if run.EvidenceWindowStartAt != nil {
		windowStart = run.EvidenceWindowStartAt.In(shanghai())
	}
	timing := "按时（不晚于10:00）"
	if !run.OnTime {
		timing = "迟到（允许执行，买入以报告校验完成后行情为准）"
	}
	quality := fmt.Sprintf("覆盖率 %.2f%%", evidence.CoveragePct*100)
	if evidence.CoveragePct > 1 {
		quality = fmt.Sprintf("覆盖率 %.2f%%", evidence.CoveragePct)
	}
	if evidence.Degraded {
		quality += "，降级"
	} else {
		quality += "，完整"
	}
	lines := []string{
		"# 研究中心2 隔日强势筛选",
		"",
		"## 分析结论",
		"",
		"- 交易日：是",
		fmt.Sprintf("- 推荐数量：%d", len(items)),
		"- 策略版本：" + run.StrategyVersion,
		fmt.Sprintf("- 当日尝试：第%d次", normalizedAttemptNo(run.AttemptNo)),
		"- 计划时间：" + formatResearch2Time(run.ScheduledFor),
		"- 实际启动：" + formatResearch2Time(run.StartedAt),
		"- 核心证据窗口：" + formatResearch2Time(windowStart) + " 至 " + formatResearch2Time(run.EvidenceCutoffAt),
		"- 报告校验完成：" + formatResearch2Time(generated),
		"- 送达状态：" + timing,
		"- 证据质量：" + quality,
	}
	if len(evidence.DegradedReasons) > 0 {
		lines = append(lines, "- 降级原因："+strings.Join(evidence.DegradedReasons, "；"))
	}
	if conclusion := strings.TrimSpace(output.Conclusion); conclusion != "" {
		lines = append(lines, "", conclusion)
	}
	if len(items) > 0 {
		lines = append(lines,
			"", "## 入选标的", "",
			"| 代码 | 名称 | 最终分 | 参考价 | 100股预计成本 | 市场/板块/个股/催化 | 风险扣分 | 执行安排 |",
			"|---|---|---:|---:|---:|---|---:|---|",
		)
		for _, item := range items {
			execution := formatResearch2Time(item.TargetBuyAt)
			if item.Status == "analysis_only" {
				execution = "仅分析，不交易"
			}
			lines = append(lines, fmt.Sprintf("| %s | %s | %.2f | %.3f | %.2f | %.1f / %.1f / %.1f / %.1f | %.1f | %s |", item.StockCode, escapeMarkdownCell(item.StockName), item.FinalScore, item.ReferencePrice, item.EstimatedLotCost, item.MarketScore, item.SectorScore, item.StockScore, item.CatalystScore, item.RiskDeduction, execution))
		}
		for _, item := range items {
			lines = append(lines,
				"", "### "+item.StockName+"（"+item.StockCode+"）", "",
				"- 入选理由："+defaultReportText(item.Summary),
				"- 关键量化数据："+defaultReportText(item.QuantData),
				"- 当天新催化："+defaultReportText(item.FreshCatalyst),
				"- 旧消息背景："+defaultReportText(item.OldBackground),
				"- 主要风险："+defaultReportText(item.MainRisk),
				"- 取消条件："+defaultReportText(item.CancelConditions),
				"- 来源ID："+defaultReportText(strings.ReplaceAll(item.SourceRefs, "\n", "、")),
			)
		}
	}
	if len(warnings) > 0 {
		lines = append(lines, "", "> 数据校验："+strings.Join(warnings, "；"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatResearch2Time(value time.Time) string {
	if value.IsZero() {
		return "--"
	}
	return value.In(shanghai()).Format("2006-01-02 15:04:05")
}

func normalizedAttemptNo(value int) int {
	if value < 1 {
		return 1
	}
	return value
}

func escapeMarkdownCell(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "|", "\\|"), "\n", " ")
}

func defaultReportText(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "无"
}

func recommendationExecution(generated, tradingDay time.Time) (time.Time, bool, string) {
	local := generated.In(shanghai())
	ten := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 10, 0, 0, 0, shanghai())
	closeTime := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 15, 0, 0, 0, shanghai())
	if !local.After(ten) {
		return ten, false, "buy_pending"
	}
	if !local.Before(closeTime) {
		return local, true, "analysis_only"
	}
	lunchStart := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 11, 30, 0, 0, shanghai())
	lunchEnd := time.Date(tradingDay.Year(), tradingDay.Month(), tradingDay.Day(), 13, 0, 0, 0, shanghai())
	if !local.Before(lunchStart) && local.Before(lunchEnd) {
		return lunchEnd, true, "buy_pending"
	}
	return local, true, "buy_pending"
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
		local := now.In(shanghai())
		lunchStart := time.Date(local.Year(), local.Month(), local.Day(), 11, 30, 0, 0, shanghai())
		afternoonOpen := time.Date(local.Year(), local.Month(), local.Day(), 13, 0, 0, 0, shanghai())
		if !local.Before(lunchStart) && local.Before(afternoonOpen) {
			return s.repository.DeferDueBuys(ctx, local, afternoonOpen)
		}
		if atOrAfterClose(now) {
			if err := s.processSells(ctx, now); err != nil {
				return err
			}
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
	activeDay := now.In(shanghai()).Format("2006-01-02")
	eligible := make([]Recommendation, 0, len(items))
	for _, item := range items {
		if atOrAfterClose(now) || item.SignalAt.In(shanghai()).Format("2006-01-02") != activeDay {
			_ = s.repository.MarkStatus(ctx, item.RecommendationID, "analysis_only", "当日未取得有效买入行情，仅保存分析，不进入模拟交易")
			continue
		}
		eligible = append(eligible, item)
	}
	items = eligible
	if len(items) == 0 {
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
			// V3 recommendations always execute from a quote fetched after the
			// server validated the report. Legacy rows retain their former mode.
			current := item.Late || (item.BuyLower == 0 && item.BuyUpper == 0)
			snapshot, quoteErr := s.market.PriceAt(ctx, item.StockCode, item.TargetBuyAt, current)
			if quoteErr != nil {
				_ = s.repository.MarkStatus(ctx, item.RecommendationID, "buy_pending", "等待报告生成后的有效买入行情: "+quoteErr.Error())
				continue
			}
			if snapshot.Suspended || snapshot.LimitUp || snapshot.LimitDown || snapshot.Price <= 0 {
				_ = s.repository.MarkStatus(ctx, item.RecommendationID, "missed_untradable", "停牌、涨跌停或无有效成交价")
				continue
			}
			notBefore := item.SignalAt
			if item.TargetBuyAt.After(notBefore) {
				notBefore = item.TargetBuyAt
			}
			// Public quote timestamps have one-second precision. Accept the same
			// exchange second, but never a prior second or an unknown timestamp.
			if snapshot.At.IsZero() || snapshot.At.Before(notBefore.Truncate(time.Second)) {
				_ = s.repository.MarkStatus(ctx, item.RecommendationID, "buy_pending", "等待报告生成后的新行情")
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
			trade := Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: tradeAt, MarketPrice: snapshots[item.RecommendationID].Price, ExecutionPrice: cost.ExecutionPrice, Quantity: quantity, Commission: cost.Commission, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow, PriceSource: snapshots[item.RecommendationID].Source, ExecutionMode: "live_after_signal"}
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
		recovered := item.Status == "sell_pending" || now.After(item.TargetSellAt.Add(2*time.Minute))
		// Never substitute the recovery-time quote for the strategy's fixed
		// next-session 10:00 exit. Providers may fetch/cache the historical bar,
		// but the requested target remains immutable.
		snapshot, quoteErr := s.market.PriceAt(ctx, item.StockCode, *item.TargetSellAt, false)
		if quoteErr != nil || snapshot.Price <= 0 || snapshot.Suspended || snapshot.LimitDown {
			reason := "卖出行情不可用"
			if quoteErr != nil {
				reason += ": " + quoteErr.Error()
			}
			_ = s.repository.MarkStatus(ctx, item.RecommendationID, "sell_pending", reason)
			continue
		}
		if snapshot.At.IsZero() || !snapshot.At.In(shanghai()).Truncate(time.Minute).Equal(item.TargetSellAt.In(shanghai()).Truncate(time.Minute)) {
			_ = s.repository.MarkStatus(ctx, item.RecommendationID, "sell_pending", "等待下一交易日10:00目标分钟行情")
			continue
		}
		cost := research.CalculateSellCost(snapshot.Price, item.Quantity)
		tradeAt := snapshot.At
		if tradeAt.IsZero() {
			tradeAt = now
		}
		executionMode := "live_after_signal"
		if recovered {
			executionMode = "recovered_target_minute"
		}
		trade := Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "sell", TradedAt: tradeAt, MarketPrice: snapshot.Price, ExecutionPrice: cost.ExecutionPrice, Quantity: item.Quantity, Commission: cost.Commission, StampDuty: cost.StampDuty, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow, PriceSource: snapshot.Source, ExecutionMode: executionMode}
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
			if lowerRecommendationPriority(eligible[index], eligible[drop]) {
				drop = index
			}
		}
		removed = append(removed, eligible[drop])
		eligible = append(eligible[:drop], eligible[drop+1:]...)
	}
	return eligible, removed
}

func lowerRecommendationPriority(left, right Recommendation) bool {
	if left.FinalScore == right.FinalScore {
		return left.StockCode > right.StockCode
	}
	return left.FinalScore < right.FinalScore
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
