package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const finalReportTableHeader = "| 股票名称 | 股票代码 | AI分析摘要 | 主要风险 | 来源编号 |"

const (
	AnalysisModeManual    = "manual"
	AnalysisModeScheduled = "scheduled"
)

var ErrScheduledAnalysisSkipped = errors.New("scheduled AI analysis skipped outside an open trading session")

type SourceDocument struct {
	SourceID    string    `json:"sourceId"`
	SourceName  string    `json:"sourceName"`
	Category    string    `json:"category"`
	CollectedAt time.Time `json:"collectedAt"`
	Content     string    `json:"content"`
	Error       string    `json:"error,omitempty"`
}

type StockCandidate struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type SourceCollector interface {
	CollectMarket(context.Context, time.Time) ([]SourceDocument, error)
	CollectSectors(context.Context, time.Time) ([]SourceDocument, error)
	CollectStocks(context.Context, time.Time, []StockCandidate) ([]SourceDocument, error)
}

type AnalysisRequest struct {
	ScheduledFor time.Time
	AIConfigID   uint
	ProviderName string
	ModelName    string
	Mode         string
}

type AnalysisRunner struct {
	service   *Service
	collector SourceCollector
}

func NewAnalysisRunner(service *Service, collector SourceCollector) *AnalysisRunner {
	return &AnalysisRunner{service: service, collector: collector}
}

func (r *AnalysisRunner) completeAI(ctx context.Context, request CompletionRequest) (CompletionResult, error) {
	return r.service.ai.Complete(ctx, request)
}

func (r *AnalysisRunner) completeAIForRun(ctx context.Context, run *AnalysisRun, request CompletionRequest) (CompletionResult, error) {
	if run == nil {
		return r.completeAI(ctx, request)
	}
	var persistErr error
	request.OnAttempt = func(record ModelAttemptRecord) {
		if persistErr != nil {
			return
		}
		records := decodeModelAttemptLog(run.ModelAttemptLogJSON)
		updated := false
		for index := range records {
			if records[index].ID == record.ID {
				records[index] = record
				updated = true
				break
			}
		}
		if !updated {
			records = append(records, record)
		}
		body, err := json.Marshal(records)
		if err != nil {
			persistErr = fmt.Errorf("序列化模型调用记录: %w", err)
			return
		}
		run.ModelAttemptLogJSON = string(body)
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err = r.service.repository.UpdateAnalysisAttemptLog(persistCtx, run.RunID, run.ModelAttemptLogJSON); err != nil {
			persistErr = fmt.Errorf("保存模型调用记录: %w", err)
		}
	}
	result, err := r.completeAI(ctx, request)
	if persistErr != nil {
		if err != nil {
			return result, errors.Join(err, persistErr)
		}
		return result, persistErr
	}
	return result, err
}

func decodeModelAttemptLog(value string) []ModelAttemptRecord {
	var records []ModelAttemptRecord
	if json.Unmarshal([]byte(strings.TrimSpace(value)), &records) != nil || records == nil {
		return []ModelAttemptRecord{}
	}
	return records
}

func (r *AnalysisRunner) Run(ctx context.Context, request AnalysisRequest) (AnalysisRun, error) {
	r.service.analysisMu.Lock()
	defer r.service.analysisMu.Unlock()
	now := r.service.now()
	if request.ScheduledFor.IsZero() {
		request.ScheduledFor = now
	}
	if request.Mode == AnalysisModeScheduled {
		trading, err := r.service.calendar.IsTradingDay(ctx, now)
		if err != nil {
			return AnalysisRun{}, fmt.Errorf("检查自动分析交易日失败: %w", err)
		}
		if !trading {
			return AnalysisRun{}, fmt.Errorf("%w: 非沪深交易日", ErrScheduledAnalysisSkipped)
		}
		if !IsTradingSession(now) {
			return AnalysisRun{}, fmt.Errorf("%w: 当前不在开盘时段", ErrScheduledAnalysisSkipped)
		}
	}
	run := AnalysisRun{
		RunID: newID(), ScheduledFor: request.ScheduledFor, StartedAt: now, Status: "running",
		AIConfigID: request.AIConfigID, ProviderName: request.ProviderName, ModelName: request.ModelName,
		ModelAttemptLogJSON: "[]",
	}
	if err := r.service.repository.CreateAnalysis(ctx, &run); err != nil {
		return run, err
	}
	finishFailure := func(stageErr error) (AnalysisRun, error) {
		completed := r.service.now()
		run.Status, run.CompletedAt, run.FailureReason = "failed", &completed, stageErr.Error()
		if saveErr := r.service.repository.SaveAnalysis(ctx, &run); saveErr != nil {
			return run, errors.Join(stageErr, saveErr)
		}
		return run, stageErr
	}
	capacity, err := r.service.recommendationCapacity(ctx)
	if err != nil {
		return finishFailure(err)
	}
	if capacity.AllowedNew == 0 {
		completed := r.service.now()
		run.Status, run.CompletedAt, run.FailureReason = "skipped_capacity", &completed,
			fmt.Sprintf("容量不足，未调用 AI（敞口 %d/%d，可用现金 %.2f 元，待买预留 %.2f 元）",
				capacity.ExposureCount, MaxPortfolioExposures, capacity.Cash, capacity.ReservedCash)
		return run, r.service.repository.SaveAnalysis(ctx, &run)
	}

	marketSources, marketCollectErr := r.collector.CollectMarket(ctx, now)
	sectorSources, sectorCollectErr := r.collector.CollectSectors(ctx, now)
	allSources := dedupeSources(append(marketSources, sectorSources...))
	if marketCollectErr != nil {
		allSources = append(allSources, failedSource("market", "市场数据汇总", now, marketCollectErr))
	}
	if sectorCollectErr != nil {
		allSources = append(allSources, failedSource("sector", "板块数据汇总", now, sectorCollectErr))
	}
	run.SourceStatusJSON = sourceStatusJSON(allSources)

	marketResult, err := r.completeAIForRun(ctx, &run, CompletionRequest{Phase: "market_analysis", Prompt: marketStagePrompt(now, filterSources(allSources, "market"))})
	if err != nil {
		return finishFailure(fmt.Errorf("大盘层失败: %w", err))
	}
	run.MarketReport = strings.TrimSpace(marketResult.Content)

	sectorResult, err := r.completeAIForRun(ctx, &run, CompletionRequest{Phase: "sector_analysis", Prompt: sectorStagePrompt(now, run.MarketReport, filterSources(allSources, "sector"))})
	if err != nil {
		return finishFailure(fmt.Errorf("板块层失败: %w", err))
	}
	sectorEnvelope, err := parseSectorEnvelope(sectorResult.Content)
	if err != nil {
		return finishFailure(fmt.Errorf("板块层输出不合规: %w", err))
	}
	run.SectorReport = sectorEnvelope.Analysis
	candidates := validUniqueCandidates(sectorEnvelope.Candidates, 50)

	shortlist := make([]recommendationRow, 0, 15)
	stockReports := make([]string, 0)
	for start := 0; start < len(candidates); start += 10 {
		end := start + 10
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[start:end]
		stockSources, stockCollectErr := r.collector.CollectStocks(ctx, now, batch)
		stockSources = dedupeSources(stockSources)
		if stockCollectErr != nil {
			stockSources = append(stockSources, failedSource("stock", fmt.Sprintf("个股数据汇总批次%d", start/10+1), now, stockCollectErr))
		}
		for index := range stockSources {
			stockSources[index].SourceID = fmt.Sprintf("S%03d", len(allSources)+index+1)
		}
		allSources = dedupeSources(append(allSources, stockSources...))
		run.SourceStatusJSON = sourceStatusJSON(allSources)
		batchResult, callErr := r.completeAIForRun(ctx, &run, CompletionRequest{Phase: "stock_analysis", Prompt: stockStagePrompt(now, run.MarketReport, run.SectorReport, batch, stockSources)})
		if callErr != nil {
			allSources = append(allSources, failedSource("stock", fmt.Sprintf("个股分析批次%d", start/10+1), now, callErr))
			continue
		}
		envelope, parseErr := parseStockEnvelope(batchResult.Content)
		if parseErr != nil {
			allSources = append(allSources, failedSource("stock", fmt.Sprintf("个股分析批次%d", start/10+1), now, parseErr))
			continue
		}
		stockReports = append(stockReports, envelope.Analysis)
		for _, item := range shortlistForBatch(envelope.Shortlist, batch) {
			if len(shortlist) >= 15 {
				break
			}
			shortlist = append(shortlist, item)
		}
	}
	run.StockReport = strings.Join(stockReports, "\n\n")
	run.SourceStatusJSON = sourceStatusJSON(allSources)

	finalResult, err := r.completeAIForRun(ctx, &run, CompletionRequest{Phase: "final_decision", Prompt: finalStagePrompt(now, run.MarketReport, run.SectorReport, run.StockReport, shortlist, capacity.AllowedNew)})
	if err != nil {
		return finishFailure(fmt.Errorf("决策层失败: %w", err))
	}
	rows, parseErr := parseFinalReportWithLimit(finalResult.Content, capacity.AllowedNew)
	if parseErr != nil {
		repairResult, repairErr := r.completeAIForRun(ctx, &run, CompletionRequest{Phase: "final_report_repair", Prompt: repairFinalReportPrompt(finalResult.Content, parseErr, capacity.AllowedNew)})
		if repairErr != nil {
			return finishFailure(fmt.Errorf("报告修复失败: %w", repairErr))
		}
		finalResult = repairResult
		rows, parseErr = parseFinalReportWithLimit(finalResult.Content, capacity.AllowedNew)
		if parseErr != nil {
			return finishFailure(fmt.Errorf("报告修复后仍不合规: %w", parseErr))
		}
	}
	run.FinalReport = strings.TrimSpace(finalResult.Content)

	inserted := 0
	acceptedRows := make([]recommendationRow, 0, capacity.AllowedNew)
	allowedFinalCodes := make(map[string]bool, len(shortlist))
	for _, item := range shortlist {
		if code, ok := NormalizeMainlandCode(item.StockCode); ok {
			allowedFinalCodes[code] = true
		}
	}
	for _, row := range rows {
		if inserted >= capacity.AllowedNew {
			break
		}
		code, ok := NormalizeMainlandCode(row.StockCode)
		if !ok || !allowedFinalCodes[code] {
			continue
		}
		quote, quoteErr := r.service.quotes.CurrentQuote(ctx, code)
		if quoteErr != nil || validateBuyQuote(quote) != nil {
			continue
		}
		quoteCode, quoteCodeOK := NormalizeMainlandCode(quote.Code)
		if !quoteCodeOK || quoteCode != code {
			continue
		}
		if !sameStockName(row.StockName, quote.Name) {
			continue
		}
		duplicate, duplicateErr := r.service.repository.HasPendingOrPosition(ctx, code)
		if duplicateErr != nil {
			return finishFailure(duplicateErr)
		}
		if duplicate {
			continue
		}
		signalAt := r.service.now()
		recommendation := Recommendation{
			RecommendationID: newID(), AnalysisRunID: run.RunID, StockCode: code, StockName: quote.Name,
			SignalAt: signalAt, AISummary: row.AISummary,
			MainRisk: row.MainRisk, SourceRefs: row.SourceRefs,
		}
		row.StockCode, row.StockName = code, quote.Name
		initial := []LifecycleMessage{
			{RecommendationID: recommendation.RecommendationID, Sequence: 1, Role: "system", Phase: "initial", Content: isolatedInitialContext(run, recommendation), Model: finalResult.Model, CreatedAt: signalAt},
			{RecommendationID: recommendation.RecommendationID, Sequence: 2, Role: "assistant", Phase: "initial", Content: row.markdownRow(), Model: finalResult.Model, CreatedAt: signalAt},
		}
		if err := r.service.EnqueueRecommendation(ctx, &recommendation, initial); err != nil {
			if errors.Is(err, ErrDuplicateExposure) {
				continue
			}
			if errors.Is(err, ErrCapacityReached) {
				break
			}
			return finishFailure(err)
		}
		inserted++
		acceptedRows = append(acceptedRows, row)
	}
	run.FinalReport = replaceFinalReportRows(run.FinalReport, acceptedRows)
	completed := r.service.now()
	run.CompletedAt, run.RecommendationCount = &completed, inserted
	if inserted == 0 {
		run.Status = "no_recommendation"
	} else {
		run.Status = "success"
	}
	if err := r.service.repository.SaveAnalysis(ctx, &run); err != nil {
		return run, err
	}
	return run, nil
}

type sectorEnvelope struct {
	Analysis   string           `json:"analysis"`
	Directions []string         `json:"directions"`
	Candidates []StockCandidate `json:"candidates"`
}
type stockEnvelope struct {
	Analysis  string              `json:"analysis"`
	Shortlist []recommendationRow `json:"shortlist"`
}
type recommendationRow struct {
	StockName  string `json:"stockName"`
	StockCode  string `json:"stockCode"`
	AISummary  string `json:"aiSummary"`
	MainRisk   string `json:"mainRisk"`
	SourceRefs string `json:"sourceRefs"`
}

func (row recommendationRow) markdownRow() string {
	return fmt.Sprintf("| %s | %s | %s | %s | %s |", row.StockName, row.StockCode, row.AISummary, row.MainRisk, row.SourceRefs)
}

func marketStagePrompt(now time.Time, sources []SourceDocument) string {
	return "你是沪深A股中短线研究员。现在是" + now.Format(time.RFC3339) + "。完成大盘层分析：全球/国内指数、宏观数据、市场快讯、整体资金和风险。只能使用下列带编号数据，失败来源必须说明，不得伪造。输出简洁 Markdown。\n\n" + sourceCorpus(sources, 48000)
}

func sectorStagePrompt(now time.Time, market string, sources []SourceDocument) string {
	return "你是沪深A股板块研究员。现在是" + now.Format(time.RFC3339) + "。参考大盘结论和行业排名、资金、热点、事件、研报，最多给10个重点方向、发现最多50只沪深A股候选，排除北交所/ST/退市。只返回严格 JSON：{\"analysis\":\"Markdown\",\"directions\":[\"方向\"],\"candidates\":[{\"code\":\"sh600000\",\"name\":\"名称\"}]}。\n大盘结论：\n" + market + "\n来源：\n" + sourceCorpus(sources, 48000)
}

func stockStagePrompt(now time.Time, market, sector string, candidates []StockCandidate, sources []SourceDocument) string {
	candidateJSON, _ := json.Marshal(candidates)
	return "你是沪深A股个股研究员。现在是" + now.Format(time.RFC3339) + "。逐只参考实时行情、日/分钟K线、公告、研报、财务、概念、资金流和新闻。本批最多保留3只；可以0只。最终被推荐的股票会由系统按最新可交易行情直接模拟买入，不设置激活条件。不要给买入区间、止损或止盈。只返回严格 JSON：{\"analysis\":\"Markdown\",\"shortlist\":[{\"stockName\":\"名称\",\"stockCode\":\"sh600000\",\"aiSummary\":\"摘要\",\"mainRisk\":\"风险\",\"sourceRefs\":\"S001,S002\"}]}。\n大盘：\n" + market + "\n板块：\n" + sector + "\n候选：" + string(candidateJSON) + "\n来源：\n" + sourceCorpus(filterSourcesForCandidates(sources, candidates), 64000)
}

func finalStagePrompt(now time.Time, market, sector, stocks string, shortlist []recommendationRow, maxRecommendations int) string {
	if maxRecommendations < 0 {
		maxRecommendations = 0
	}
	if maxRecommendations > 2 {
		maxRecommendations = 2
	}
	shortlistJSON, _ := json.Marshal(shortlist)
	return fmt.Sprintf("你是最终投资研究决策员。现在是%s。综合三级结果，本轮账户容量最多允许推荐%d只，请推荐0到%d只并允许明确空仓。周期通常中短线且一般不超过10天但不是硬规则。推荐会触发系统按最新可交易行情直接模拟买入，但不代表真实购买。不要输出激活条件、买入区间、止损、止盈、失效条件、基准或超额收益。输出完整 Markdown 报告，末尾必须严格包含下面5列表格且不可增加/删除/改名；空仓也保留表头和分隔行但无数据行。\n%s\n|---|---|---|---|---|\n大盘：\n%s\n板块：\n%s\n个股：\n%s\n最多15只候选：%s",
		now.Format(time.RFC3339), maxRecommendations, maxRecommendations, finalReportTableHeader, market, sector, stocks, string(shortlistJSON))
}

func repairFinalReportPrompt(report string, parseErr error, maxRecommendations int) string {
	return fmt.Sprintf("以下报告格式不合规（%s）。只修复格式和最多%d行限制，不改变事实。返回完整 Markdown；末尾必须为：\n%s\n|---|---|---|---|---|\n\n原报告：\n%s",
		parseErr.Error(), maxRecommendations, finalReportTableHeader, report)
}

func parseSectorEnvelope(content string) (sectorEnvelope, error) {
	var result sectorEnvelope
	err := parseStrictJSON(content, &result)
	if err == nil && len(result.Directions) > 10 {
		result.Directions = result.Directions[:10]
	}
	return result, err
}

func parseStockEnvelope(content string) (stockEnvelope, error) {
	var result stockEnvelope
	err := parseStrictJSON(content, &result)
	if err == nil && len(result.Shortlist) > 3 {
		return result, errors.New("stock batch returned more than 3 candidates")
	}
	return result, err
}

func shortlistForBatch(source []recommendationRow, batch []StockCandidate) []recommendationRow {
	allowed, seen := make(map[string]bool, len(batch)), make(map[string]bool, len(source))
	for _, candidate := range batch {
		if code, ok := NormalizeMainlandCode(candidate.Code); ok {
			allowed[code] = true
		}
	}
	result := make([]recommendationRow, 0, 3)
	for _, item := range source {
		code, ok := NormalizeMainlandCode(item.StockCode)
		if !ok || !allowed[code] || seen[code] {
			continue
		}
		item.StockCode, seen[code] = code, true
		result = append(result, item)
		if len(result) == 3 {
			break
		}
	}
	return result
}

func sameStockName(modelName, quoteName string) bool {
	normalize := func(value string) string {
		value = strings.ToUpper(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
		for _, prefix := range []string{"XD", "XR", "DR"} {
			if strings.HasPrefix(value, prefix) {
				return strings.TrimPrefix(value, prefix)
			}
		}
		return value
	}
	model, quote := normalize(modelName), normalize(quoteName)
	if model == "" || quote == "" {
		return false
	}
	if model == quote {
		return true
	}
	shorter := model
	if len([]rune(quote)) < len([]rune(model)) {
		shorter = quote
	}
	return len([]rune(shorter)) >= 3 && (strings.HasPrefix(model, quote) || strings.HasPrefix(quote, model))
}

func parseStrictJSON(content string, target any) error {
	trimmed := strings.TrimSpace(content)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(trimmed)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func parseFinalReport(report string) ([]recommendationRow, error) {
	return parseFinalReportWithLimit(report, 2)
}

func parseFinalReportWithLimit(report string, maxRecommendations int) ([]recommendationRow, error) {
	if maxRecommendations < 0 {
		maxRecommendations = 0
	}
	lines := strings.Split(strings.ReplaceAll(report, "\r\n", "\n"), "\n")
	headerIndex := -1
	for i, line := range lines {
		if normalizeTableLine(line) == normalizeTableLine(finalReportTableHeader) {
			headerIndex = i
		}
	}
	if headerIndex < 0 || headerIndex+1 >= len(lines) {
		return nil, errors.New("missing fixed recommendation table")
	}
	if len(splitTableRow(lines[headerIndex+1])) != 5 {
		return nil, errors.New("invalid table separator")
	}
	rows := make([]recommendationRow, 0, 2)
	for i := headerIndex + 2; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			break
		}
		columns := splitTableRow(lines[i])
		if len(columns) != 5 {
			return nil, errors.New("recommendation row must have 5 columns")
		}
		rows = append(rows, recommendationRow{StockName: columns[0], StockCode: columns[1], AISummary: columns[2], MainRisk: columns[3], SourceRefs: columns[4]})
	}
	if len(rows) > maxRecommendations {
		return nil, fmt.Errorf("final report returned more than %d recommendations", maxRecommendations)
	}
	return rows, nil
}

// replaceFinalReportRows makes the persisted report's fixed table reflect
// exactly the recommendations that passed code, quote, duplicate and capacity
// admission. The model narrative remains unchanged.
func replaceFinalReportRows(report string, rows []recommendationRow) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(report), "\r\n", "\n"), "\n")
	headerIndex := -1
	for index, line := range lines {
		if normalizeTableLine(line) == normalizeTableLine(finalReportTableHeader) {
			headerIndex = index
		}
	}
	if headerIndex < 0 || headerIndex+1 >= len(lines) {
		return strings.TrimSpace(report)
	}
	end := headerIndex + 2
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" && len(splitTableRow(lines[end])) == 5 {
		end++
	}
	replacement := append([]string(nil), lines[:headerIndex+2]...)
	for _, row := range rows {
		replacement = append(replacement, row.markdownRow())
	}
	replacement = append(replacement, lines[end:]...)
	return strings.TrimSpace(strings.Join(replacement, "\n"))
}

func normalizeTableLine(line string) string {
	return strings.ReplaceAll(strings.TrimSpace(line), " ", "")
}
func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func validUniqueCandidates(source []StockCandidate, max int) []StockCandidate {
	seen, result := map[string]bool{}, make([]StockCandidate, 0, max)
	for _, candidate := range source {
		code, ok := NormalizeMainlandCode(candidate.Code)
		if !ok || seen[code] {
			continue
		}
		seen[code], candidate.Code = true, code
		result = append(result, candidate)
		if len(result) == max {
			break
		}
	}
	return result
}

func dedupeSources(source []SourceDocument) []SourceDocument {
	seen, result := map[string]bool{}, make([]SourceDocument, 0, len(source))
	for _, document := range source {
		canonical := strings.ToLower(strings.Join(strings.Fields(document.Content), " "))
		hash := sha256.Sum256([]byte(document.SourceName + "\x00" + canonical))
		key := hex.EncodeToString(hash[:])
		if seen[key] {
			continue
		}
		seen[key] = true
		if document.SourceID == "" {
			document.SourceID = fmt.Sprintf("S%03d", len(result)+1)
		}
		result = append(result, document)
	}
	return result
}

func sourceCorpus(sources []SourceDocument, maxBytes int) string {
	if len(sources) == 0 || maxBytes <= 0 {
		return ""
	}
	type corpusEntry struct {
		prefix  string
		content string
	}
	entries := make([]corpusEntry, 0, len(sources))
	fixedBytes := 0
	for _, source := range sources {
		entry := corpusEntry{
			prefix:  fmt.Sprintf("[%s][%s][%s][%s] ", source.SourceID, source.SourceName, source.Category, source.CollectedAt.Format(time.RFC3339)),
			content: source.Content,
		}
		if source.Error != "" {
			entry.prefix = fmt.Sprintf("[%s][%s][失败] ", source.SourceID, source.SourceName)
			entry.content = source.Error
		}
		entries = append(entries, entry)
		fixedBytes += len(entry.prefix) + 1
	}
	if fixedBytes > maxBytes {
		// Extreme budgets cannot carry metadata, but retain every source ID and
		// failure marker whenever that is mathematically possible.
		fixedBytes = 0
		for index := range entries {
			status := ""
			if sources[index].Error != "" {
				status = "[失败]"
			}
			entries[index].prefix = fmt.Sprintf("[%s]%s ", sources[index].SourceID, status)
			fixedBytes += len(entries[index].prefix) + 1
		}
	}
	if fixedBytes > maxBytes {
		return truncateUTF8(strings.Join(func() []string {
			ids := make([]string, 0, len(sources))
			for _, source := range sources {
				ids = append(ids, "["+source.SourceID+"]")
			}
			return ids
		}(), ""), maxBytes)
	}
	contentBudget := (maxBytes - fixedBytes) / len(entries)
	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString(entry.prefix)
		builder.WriteString(truncateUTF8(entry.content, contentBudget))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	const marker = "…"
	if maxBytes < len(marker) {
		for end := maxBytes; end > 0; end-- {
			if utf8.ValidString(value[:end]) {
				return value[:end]
			}
		}
		return ""
	}
	end := maxBytes - len(marker)
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + marker
}

func sourceStatusJSON(sources []SourceDocument) string {
	data, _ := json.Marshal(sources)
	return string(data)
}
func failedSource(category, name string, at time.Time, err error) SourceDocument {
	return SourceDocument{SourceName: name, Category: category, CollectedAt: at, Error: err.Error()}
}
func filterSources(sources []SourceDocument, category string) []SourceDocument {
	result := []SourceDocument{}
	for _, s := range sources {
		if s.Category == category || s.Error != "" {
			result = append(result, s)
		}
	}
	return result
}
func filterSourcesForCandidates(sources []SourceDocument, candidates []StockCandidate) []SourceDocument {
	codes := map[string]bool{}
	for _, c := range candidates {
		codes[strings.ToLower(c.Code)] = true
		codes[strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(c.Code), "sh"), "sz")] = true
	}
	result := []SourceDocument{}
	for _, s := range sources {
		lower := strings.ToLower(s.SourceName + " " + s.Content)
		for code := range codes {
			if strings.Contains(lower, code) {
				result = append(result, s)
				break
			}
		}
	}
	return result
}
func isolatedInitialContext(run AnalysisRun, recommendation Recommendation) string {
	return "本会话只属于 " + recommendation.StockName + "(" + recommendation.StockCode + ")，严禁引用或推断其他股票会话。" +
		"\n分析运行：" + run.RunID +
		"\n信号时间：" + recommendation.SignalAt.Format(time.RFC3339) +
		"\nAI摘要：" + recommendation.AISummary +
		"\n主要风险：" + recommendation.MainRisk +
		"\n来源：" + recommendation.SourceRefs
}

func SortedSourceNames(sources []SourceDocument) []string {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.SourceName)
	}
	sort.Strings(names)
	return names
}
