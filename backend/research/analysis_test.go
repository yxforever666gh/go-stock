package research

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go-stock/backend/knowledge"
	"go-stock/backend/marketdata"
	"go-stock/backend/researchaudit"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	sharedai "go-stock/backend/ai"
	"go-stock/internal/marketquote"
	"go-stock/internal/researchevidence"
)

type fixedCollector struct{}

type cutoffEvidenceCollector struct{}

type fixtureKnowledgeRetriever struct {
	calls    int
	requests []knowledge.ResearchRetrievalRequest
	prompt   string
	err      error
}

func (retriever *fixtureKnowledgeRetriever) RetrieveForResearch(_ context.Context, request knowledge.ResearchRetrievalRequest) (knowledge.ResearchRetrieval, error) {
	retriever.calls++
	retriever.requests = append(retriever.requests, request)
	return knowledge.ResearchRetrieval{RetrievalRunID: "retrieval-1", Prompt: retriever.prompt}, retriever.err
}

func (cutoffEvidenceCollector) CollectMarket(_ context.Context, now time.Time) ([]researchevidence.SourceDocument, error) {
	future := now.Add(time.Hour)
	return []researchevidence.SourceDocument{
		{SourceName: "market-normal", Category: "market", CollectedAt: now, AvailableAt: &now, Content: "market-normal-content"},
		{SourceID: "theme-snapshot:snapshot-equal", SourceName: "theme-snapshot-equal", Category: "theme", CollectedAt: now, AvailableAt: &now, Content: "THEME_EQUAL"},
		{SourceID: "theme-catalyst:claim-support", SourceName: "theme-claim-support", Category: "catalyst", CollectedAt: now, AvailableAt: &now, Content: "CLAIM_SUPPORT"},
		{SourceID: "theme-catalyst:claim-contradict", SourceName: "theme-claim-contradict", Category: "catalyst", CollectedAt: now, AvailableAt: &now, Content: "CLAIM_CONTRADICT"},
		{SourceID: "theme-catalyst:claim-future", SourceName: "theme-claim-future", Category: "catalyst", CollectedAt: now.Add(-time.Hour), AvailableAt: &future, Content: "THEME_FUTURE_SECRET"},
		{SourceID: "theme-catalyst:claim-null", SourceName: "theme-claim-null", Category: "catalyst", CollectedAt: now.Add(-time.Hour), AvailableAt: nil, Content: "THEME_NULL_SECRET"},
	}, nil
}

func (cutoffEvidenceCollector) CollectSectors(_ context.Context, now time.Time) ([]researchevidence.SourceDocument, error) {
	future := now.Add(time.Hour)
	return []researchevidence.SourceDocument{
		{SourceName: "sector-normal", Category: "sector", CollectedAt: now, AvailableAt: &now, Content: "sector-normal-content"},
		{SourceName: "sector-future", Category: "sector", CollectedAt: now, AvailableAt: &future, Content: "FUTURE_SECRET"},
	}, nil
}

func (cutoffEvidenceCollector) CollectStocks(_ context.Context, now time.Time, _ []researchevidence.StockCandidate) ([]researchevidence.SourceDocument, error) {
	return []researchevidence.SourceDocument{{SourceName: "stock-normal", Category: "stock", CollectedAt: now, AvailableAt: &now, Content: "stock-normal-content"}}, nil
}

type attemptReportingAI struct {
	delegate *scriptedAI
	sequence int
}

type fixedCalendar struct {
	trading bool
	err     error
}

func (c fixedCalendar) IsTradingDay(context.Context, time.Time) (bool, error) {
	return c.trading, c.err
}

func (a *attemptReportingAI) Complete(ctx context.Context, request sharedai.CompletionRequest) (sharedai.CompletionResult, error) {
	a.sequence++
	started := time.Now()
	record := sharedai.ModelAttemptRecord{
		ID: "attempt-" + request.Phase, Phase: request.Phase, ConfigID: 1,
		ProviderName: "provider", ModelName: "model", APIProtocol: "openai_responses",
		Attempt: 1, MaxAttempts: 5, StartedAt: started, Status: "reasoning", LastEventType: "response.in_progress",
	}
	if request.OnAttempt != nil {
		request.OnAttempt(record)
	}
	result, err := a.delegate.Complete(ctx, request)
	completed := time.Now()
	record.CompletedAt, record.DurationMS, record.NextAction = &completed, completed.Sub(started).Milliseconds(), "complete"
	if err == nil {
		record.Status = "success"
	} else {
		record.Status, record.ErrorCategory, record.ErrorMessage = "failed", "test_error", err.Error()
	}
	if request.OnAttempt != nil {
		request.OnAttempt(record)
	}
	return result, err
}

func (fixedCollector) CollectMarket(_ context.Context, now time.Time) ([]researchevidence.SourceDocument, error) {
	return []researchevidence.SourceDocument{{SourceName: "CLS", Category: "market", CollectedAt: now, Content: "market"}}, nil
}

func TestEndToEndAnalysisDirectBuyTPlusOneSaleAndNetYield(t *testing.T) {
	repo := researchTestRepo(t)
	current := time.Date(2026, 8, 14, 9, 30, 0, 0, shanghaiLocation)
	sector := `{"analysis":"银行资金转强","directions":["银行"],"candidates":[{"code":"600000","name":"浦发银行"}]}`
	stock := `{"analysis":"浦发银行量价结构改善","shortlist":[{"stockName":"浦发银行","stockCode":"sh600000","aiSummary":"结构改善","mainRisk":"资金回落","sourceRefs":"S001"}]}`
	final := "建议直接模拟买入。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|\n|浦发银行|sh600000|结构改善|资金回落|S001|"
	ai := &scriptedAI{results: []sharedai.CompletionResult{
		{Content: "市场风险可控"}, {Content: sector}, {Content: stock}, {Content: final, ResponseID: "shared-final"},
		{Content: `{"action":"卖出","reason":"次日冲高转弱"}`, ResponseID: "stock-sale"},
	}}
	if err := repo.DB().AutoMigrate(&researchaudit.PromptVersion{}, &researchaudit.Payload{}, &researchaudit.RunState{}, &researchaudit.Replay{}, &researchaudit.ReplayResult{}); err != nil {
		t.Fatal(err)
	}
	quotes := &scriptedQuotes{quotes: []marketquote.Quote{
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: current},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: current},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 11, PreviousClose: 10.1, At: time.Date(2026, 8, 17, 9, 50, 0, 0, shanghaiLocation)},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 11.1, PreviousClose: 10.1, At: time.Date(2026, 8, 17, 9, 50, 1, 0, shanghaiLocation)},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 11.1, PreviousClose: 10.1, At: time.Date(2026, 8, 17, 9, 50, 2, 0, shanghaiLocation)},
	}}
	service := NewService(repo, &attemptReportingAI{delegate: ai}, quotes, weekdayTradingCalendar{})
	service.now = func() time.Time { return current }
	runner := NewAnalysisRunner(service, fixedCollector{})
	runner.ConfigureAudit(researchaudit.NewRecorder(researchaudit.NewRepository(repo.DB())))
	run, err := runner.Run(context.Background(), AnalysisRequest{ScheduledFor: current, AIConfigID: 1, ModelName: "gpt-5.6-sol"})
	if err != nil || run.RecommendationCount != 1 {
		t.Fatalf("analysis run=%+v err=%v", run, err)
	}
	audit, auditErr := researchaudit.NewRecorder(researchaudit.NewRepository(repo.DB())).Audit(context.Background(), researchaudit.OwnerResearch1, run.RunID)
	if auditErr != nil || audit.Status != researchaudit.StatusComplete || len(audit.Payloads) != 4 {
		t.Fatalf("audit=%+v err=%v", audit, auditErr)
	}
	for _, payload := range audit.Payloads {
		if payload.ProviderName != "provider" || payload.ModelName != "model" || !strings.Contains(payload.ModelParametersJSON, `"actualConfigId":1`) {
			t.Fatalf("actual fallback identity missing: %+v", payload.Payload)
		}
	}
	items, _ := repo.ListRecommendations(context.Background(), 10, 0)
	active, _ := repo.Recommendation(context.Background(), items[0].RecommendationID)
	if active.Status != "active" || active.Quantity < 100 {
		t.Fatalf("direct buy=%+v", active)
	}
	current = time.Date(2026, 8, 17, 9, 50, 0, 0, shanghaiLocation)
	if err = service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed, _ := repo.Recommendation(context.Background(), items[0].RecommendationID)
	if closed.Status != "closed" || closed.NetPnL <= 0 || closed.NetYieldRate <= 0 {
		t.Fatalf("closed=%+v", closed)
	}
	overview, err := service.AccountOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantYield := (overview.Cash - InitialCash) / InitialCash
	if len(overview.Positions) != 0 || math.Abs(overview.NetYieldRate-wantYield) > 1e-10 || overview.NetProfit <= 0 {
		t.Fatalf("overview=%+v wantYield=%f", overview, wantYield)
	}
	if len(ai.requests) != 5 || ai.requests[4].PreviousResponseID != "" || len(ai.requests[4].Messages) == 0 {
		t.Fatalf("per-stock response chain=%+v", ai.requests)
	}
}
func (fixedCollector) CollectSectors(_ context.Context, now time.Time) ([]researchevidence.SourceDocument, error) {
	return []researchevidence.SourceDocument{{SourceName: "EM", Category: "sector", CollectedAt: now, Content: "sector"}}, nil
}
func (fixedCollector) CollectStocks(_ context.Context, now time.Time, candidates []researchevidence.StockCandidate) ([]researchevidence.SourceDocument, error) {
	result := make([]researchevidence.SourceDocument, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, researchevidence.SourceDocument{SourceName: "stock", Category: "stock", CollectedAt: now, Content: c.Code + " data"})
	}
	return result, nil
}

func TestFinalReportAllowsCashAndAtMostTwo(t *testing.T) {
	empty := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"
	rows, err := parseFinalReport(empty)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty rows=%d err=%v", len(rows), err)
	}
	tooMany := empty + "\n|甲|sh600000|a|b|S1|\n|乙|sz000001|a|b|S2|\n|丙|sz300001|a|b|S3|"
	if _, err := parseFinalReport(tooMany); err == nil {
		t.Fatal("more than two rows must fail")
	}
}

func TestCandidateAndSourceBounds(t *testing.T) {
	candidates := []researchevidence.StockCandidate{{Code: "600000", Name: "甲"}, {Code: "sh600000", Name: "重复"}, {Code: "430001", Name: "北交"}, {Code: "300001", Name: "乙"}, {Code: "sh512000", Name: "ETF背景"}, {Code: "sz159001", Name: "基金背景"}}
	valid := validUniqueCandidates(candidates, 50)
	if len(valid) != 2 || valid[0].Code != "sh600000" || valid[1].Code != "sz300001" {
		t.Fatalf("valid=%+v", valid)
	}
	now := time.Now()
	sources := dedupeSources([]researchevidence.SourceDocument{{SourceName: "Sina", Category: "market", CollectedAt: now, Content: " same  news "}, {SourceName: "Sina", Category: "market", CollectedAt: now, Content: "same news"}})
	if len(sources) != 1 || sources[0].SourceID == "" {
		t.Fatalf("sources=%+v", sources)
	}
	if corpus := sourceCorpus(sources, 64); len(corpus) > 64 || !strings.Contains(corpus, sources[0].SourceID) {
		t.Fatalf("corpus should respect cap and retain id: %q", corpus)
	}
	failedCorpus := sourceCorpus([]researchevidence.SourceDocument{{SourceID: "S999", SourceName: "failed", Category: "stock", CollectedAt: now, Content: "provider-body-must-not-leak", Error: "semantic failure"}}, 256)
	if strings.Contains(failedCorpus, "provider-body-must-not-leak") || !strings.Contains(failedCorpus, "semantic failure") {
		t.Fatalf("failed source corpus=%q", failedCorpus)
	}
}

func TestAnalysisStagePromptsUsePostCollectionTimes(t *testing.T) {
	repo := researchTestRepo(t)
	zone := shanghaiLocation
	times := []time.Time{
		time.Date(2026, 8, 25, 9, 55, 0, 0, zone),
		time.Date(2026, 8, 25, 9, 55, 1, 0, zone),
		time.Date(2026, 8, 25, 9, 55, 2, 0, zone),
		time.Date(2026, 8, 25, 9, 56, 0, 0, zone),
		time.Date(2026, 8, 25, 9, 57, 0, 0, zone),
		time.Date(2026, 8, 25, 9, 58, 0, 0, zone),
		time.Date(2026, 8, 25, 9, 59, 0, 0, zone),
		time.Date(2026, 8, 25, 10, 0, 0, 0, zone),
	}
	index := 0
	ai := &scriptedAI{results: []sharedai.CompletionResult{
		{Content: "大盘"},
		{Content: `{"analysis":"无候选","directions":[],"candidates":[]}`},
		{Content: "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"},
	}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time {
		if index >= len(times) {
			return times[len(times)-1]
		}
		value := times[index]
		index++
		return value
	}
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeManual})
	if err != nil || run.Status != "no_recommendation" || len(ai.requests) != 3 {
		t.Fatalf("run=%+v err=%v requests=%d", run, err, len(ai.requests))
	}
	checks := []struct {
		request int
		values  []string
	}{
		{request: 0, values: []string{"2026-08-25T09:56:00+08:00", "2026-08-25T09:55:02+08:00"}},
		{request: 1, values: []string{"2026-08-25T09:58:00+08:00", "2026-08-25T09:57:00+08:00"}},
		{request: 2, values: []string{"2026-08-25T09:59:00+08:00"}},
	}
	for _, check := range checks {
		for _, value := range check.values {
			if !strings.Contains(ai.requests[check.request].Prompt, value) {
				t.Fatalf("request %d missing %s: %s", check.request, value, ai.requests[check.request].Prompt)
			}
		}
	}
}

func TestRecentRecommendationIsSoftContextAndDoesNotBlockRepeat(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 25, 10, 5, 0, 0, shanghaiLocation)
	previous := seedRecommendation(t, repo, "closed", now.AddDate(0, 0, -1), now, "")
	sector := `{"analysis":"银行","directions":["银行"],"candidates":[{"code":"sh600000","name":"浦发银行"}]}`
	stock := `{"analysis":"新增资金证据支持再次入选","shortlist":[{"stockName":"浦发银行","stockCode":"sh600000","aiSummary":"相对上次新增证据：当日资金与量价共振","mainRisk":"冲高回落","sourceRefs":"S001"}]}`
	final := "允许同股独立持仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|\n|浦发银行|sh600000|相对上次新增证据：当日资金与量价共振|冲高回落|S001|"
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: sector}, {Content: stock}, {Content: final}}}
	quote := marketquote.Quote{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.9, At: now}
	service := NewService(repo, ai, &scriptedQuotes{quotes: []marketquote.Quote{quote}}, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeManual})
	if err != nil || run.Status != "success" || run.RecommendationCount != 1 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	for _, requestIndex := range []int{1, 2, 3} {
		prompt := ai.requests[requestIndex].Prompt
		if !strings.Contains(prompt, previous.StockCode) || !strings.Contains(prompt, previous.SignalAt.Format(time.RFC3339)) || (!strings.Contains(prompt, "不得硬性排除重复股票") && requestIndex != 1) {
			t.Fatalf("request %d missing soft history context: %s", requestIndex, prompt)
		}
	}
	items, err := repo.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range items {
		if item.StockCode == "sh600000" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("same-stock recommendation count=%d want=2 items=%+v", count, items)
	}
}

func latin1DecodedUTF8(value string) string {
	var result strings.Builder
	for _, item := range []byte(value) {
		result.WriteRune(rune(item))
	}
	return result.String()
}

func TestStructuredJSONRecoversUTF8Latin1MojibakeAndStaysStrict(t *testing.T) {
	sectorJSON := `{"analysis":"银行资金转强","directions":["银行"],"candidates":[{"code":"sh600000","name":"浦发银行"}]}`
	sector, err := parseSectorEnvelope(latin1DecodedUTF8(sectorJSON))
	if err != nil || sector.Analysis != "银行资金转强" || len(sector.Candidates) != 1 || sector.Candidates[0].Name != "浦发银行" {
		t.Fatalf("sector=%+v err=%v", sector, err)
	}

	stockJSON := `{"analysis":"量价改善","shortlist":[]}`
	stock, err := parseStockEnvelope(latin1DecodedUTF8(stockJSON))
	if err != nil || stock.Analysis != "量价改善" {
		t.Fatalf("stock=%+v err=%v", stock, err)
	}
	if _, err = parseSectorEnvelope("```json\n" + sectorJSON + "\n```"); err != nil {
		t.Fatalf("fenced JSON failed: %v", err)
	}
	if _, err = parseSectorEnvelope(`{"analysis":"板块","directions":[],"candidates":[],"unexpected":true}`); err == nil {
		t.Fatal("unknown field must remain invalid")
	}
}

func TestAnalysisRunRepairsMalformedSectorOnce(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 24, 14, 30, 0, 0, shanghaiLocation)
	bad := latin1DecodedUTF8("根据现有数据整理板块方向")
	repaired := `{"analysis":"原输出无法可靠恢复","directions":[],"candidates":[]}`
	emptyFinal := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"
	delegate := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: bad}, {Content: repaired}, {Content: emptyFinal}}}
	ai := &attemptReportingAI{delegate: delegate}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }

	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: now, Mode: AnalysisModeManual})
	if err != nil || run.Status != "no_recommendation" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	wantPhases := []string{"market_analysis", "sector_analysis", "sector_analysis_repair", "final_decision"}
	if len(delegate.requests) != len(wantPhases) {
		t.Fatalf("requests=%d want=%d", len(delegate.requests), len(wantPhases))
	}
	for index, phase := range wantPhases {
		if delegate.requests[index].Phase != phase {
			t.Fatalf("phase[%d]=%s want=%s", index, delegate.requests[index].Phase, phase)
		}
	}
	stored, err := repo.Analysis(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	records := decodeModelAttemptLog(stored.ModelAttemptLogJSON)
	if len(records) != 4 || records[2].Phase != "sector_analysis_repair" {
		t.Fatalf("records=%+v", records)
	}
}

func TestAnalysisRunFailsAfterOneInvalidSectorRepair(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 24, 14, 30, 0, 0, shanghaiLocation)
	bad := latin1DecodedUTF8("根据现有数据整理板块方向")
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: bad}, {Content: "仍然不是 JSON"}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }

	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: now, Mode: AnalysisModeManual})
	if err == nil || run.Status != "failed" || !strings.Contains(run.FailureReason, "板块层输出修复后仍不合规") {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if len(ai.requests) != 3 || ai.requests[2].Phase != "sector_analysis_repair" {
		t.Fatalf("requests=%+v", ai.requests)
	}
}

func TestAnalysisRunFailsWhenSectorRepairCallFails(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 24, 14, 30, 0, 0, shanghaiLocation)
	bad := latin1DecodedUTF8("根据现有数据整理板块方向")
	ai := &scriptedAI{
		results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: bad}, {}},
		errors:  []error{nil, nil, errors.New("repair unavailable")},
	}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }

	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: now, Mode: AnalysisModeManual})
	if err == nil || run.Status != "failed" || !strings.Contains(run.FailureReason, "板块层输出修复失败") {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if strings.Contains(run.FailureReason, "根据现有数据") {
		t.Fatalf("failure reason leaked response body: %s", run.FailureReason)
	}
	if len(ai.requests) != 3 || ai.requests[2].Phase != "sector_analysis_repair" {
		t.Fatalf("requests=%+v", ai.requests)
	}
}

func TestAnalysisRunRepairsMalformedStockBatch(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 24, 14, 30, 0, 0, shanghaiLocation)
	sector := `{"analysis":"银行","directions":["银行"],"candidates":[{"code":"sh600000","name":"浦发银行"}]}`
	bad := latin1DecodedUTF8("根据候选数据整理个股")
	repaired := `{"analysis":"原输出无法可靠恢复","shortlist":[]}`
	emptyFinal := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: sector}, {Content: bad}, {Content: repaired}, {Content: emptyFinal}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }

	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: now, Mode: AnalysisModeManual})
	if err != nil || run.Status != "no_recommendation" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if len(ai.requests) != 5 || ai.requests[3].Phase != "stock_analysis_repair" {
		t.Fatalf("requests=%+v", ai.requests)
	}
}

func TestAnalysisRunContinuesAfterOneInvalidStockRepair(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 24, 14, 30, 0, 0, shanghaiLocation)
	sector := `{"analysis":"银行","directions":["银行"],"candidates":[{"code":"sh600000","name":"浦发银行"}]}`
	bad := latin1DecodedUTF8("根据候选数据整理个股")
	emptyFinal := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: sector}, {Content: bad}, {Content: "仍然不是 JSON"}, {Content: emptyFinal}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }

	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: now, Mode: AnalysisModeManual})
	if err != nil || run.Status != "no_recommendation" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if len(ai.requests) != 5 || ai.requests[3].Phase != "stock_analysis_repair" {
		t.Fatalf("requests=%+v", ai.requests)
	}
	var sources []researchevidence.SourceDocument
	if err = json.Unmarshal([]byte(run.SourceStatusJSON), &sources); err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, source := range sources {
		if source.SourceName == "个股分析批次1" && strings.Contains(source.Error, "输出修复后仍不合规") {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed=%d sources=%+v", failed, sources)
	}
}

func TestListAnalysisReturnsLightweightSourceCounts(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Now()
	run := AnalysisRun{
		RunID: "summary-run", ScheduledFor: now, StartedAt: now, Status: "failed",
		MarketReport: strings.Repeat("full report", 100), SourceStatusJSON: sourceStatusJSON([]researchevidence.SourceDocument{
			{SourceID: "S001", SourceName: "成功来源", Category: "market", CollectedAt: now, Content: "large payload"},
			{SourceID: "S002", SourceName: "失败来源", Category: "sector", CollectedAt: now, Error: "timeout"},
		}),
	}
	if err := repo.CreateAnalysis(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	summaries, err := repo.ListAnalysis(context.Background(), 10, 0)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries=%+v err=%v", summaries, err)
	}
	if summaries[0].SourceCount != 2 || summaries[0].FailedSourceCount != 1 || summaries[0].RunID != run.RunID {
		t.Fatalf("summary=%+v", summaries[0])
	}
}

func TestLatestAnalysisForScheduledSlotSupportsLegacyCronTimestamps(t *testing.T) {
	repo := researchTestRepo(t)
	slot := time.Date(2026, 8, 17, 14, 30, 0, 0, shanghaiLocation)
	manualAt := slot.Add(2*time.Minute + 17*time.Second + 123*time.Nanosecond)
	legacyCronAt := slot.Add(3 * time.Second)
	manual := AnalysisRun{RunID: newID(), ScheduledFor: manualAt, StartedAt: manualAt, Status: "failed"}
	scheduled := AnalysisRun{RunID: newID(), ScheduledFor: legacyCronAt, StartedAt: legacyCronAt, Status: "success"}
	if err := repo.CreateAnalysis(context.Background(), &manual); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateAnalysis(context.Background(), &scheduled); err != nil {
		t.Fatal(err)
	}
	got, exists, err := repo.LatestAnalysisForScheduledSlot(context.Background(), slot)
	if err != nil || !exists || got.RunID != scheduled.RunID {
		t.Fatalf("got=%+v exists=%t err=%v", got, exists, err)
	}
	_, exists, err = repo.LatestAnalysisForScheduledSlot(context.Background(), slot.AddDate(0, 0, 1))
	if err != nil || exists {
		t.Fatalf("missing slot exists=%t err=%v", exists, err)
	}
}

func TestScheduledAnalysisGateCreatesNoRunAndManualBypassesTime(t *testing.T) {
	weekend := time.Date(2026, 8, 15, 10, 0, 0, 0, shanghaiLocation)
	emptySector := `{"analysis":"暂无方向","directions":[],"candidates":[]}`
	emptyFinal := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"

	repo := researchTestRepo(t)
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: emptySector}, {Content: emptyFinal}}}
	service := NewService(repo, ai, &scriptedQuotes{}, fixedCalendar{trading: false})
	service.now = func() time.Time { return weekend }
	runner := NewAnalysisRunner(service, fixedCollector{})
	if _, err := runner.Run(context.Background(), AnalysisRequest{ScheduledFor: weekend, Mode: AnalysisModeScheduled}); !errors.Is(err, ErrScheduledAnalysisSkipped) {
		t.Fatalf("scheduled weekend err=%v", err)
	}
	runs, err := repo.ListAnalysis(context.Background(), 10, 0)
	if err != nil || len(runs) != 0 || len(ai.requests) != 0 {
		t.Fatalf("scheduled skip persisted or invoked AI: runs=%+v calls=%d err=%v", runs, len(ai.requests), err)
	}

	run, err := runner.Run(context.Background(), AnalysisRequest{ScheduledFor: weekend, Mode: AnalysisModeManual})
	if err != nil || run.Status != "no_recommendation" || len(ai.requests) != 3 {
		t.Fatalf("manual run=%+v calls=%d err=%v", run, len(ai.requests), err)
	}

	repo2 := researchTestRepo(t)
	ai2 := &scriptedAI{}
	service2 := NewService(repo2, ai2, &scriptedQuotes{}, fixedCalendar{trading: true})
	service2.now = func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, shanghaiLocation) }
	if _, err = NewAnalysisRunner(service2, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeScheduled}); !errors.Is(err, ErrScheduledAnalysisSkipped) {
		t.Fatalf("scheduled lunch err=%v", err)
	}
	runs, _ = repo2.ListAnalysis(context.Background(), 10, 0)
	if len(runs) != 0 || len(ai2.requests) != 0 {
		t.Fatalf("lunch skip persisted or invoked AI: runs=%+v calls=%d", runs, len(ai2.requests))
	}
}

func TestScheduledCalendarFailureCreatesNoAnalysisRecord(t *testing.T) {
	repo := researchTestRepo(t)
	ai := &scriptedAI{}
	service := NewService(repo, ai, &scriptedQuotes{}, fixedCalendar{err: errors.New("calendar unavailable")})
	service.now = func() time.Time { return time.Date(2026, 8, 17, 10, 0, 0, 0, shanghaiLocation) }
	_, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeScheduled})
	if err == nil || errors.Is(err, ErrScheduledAnalysisSkipped) {
		t.Fatalf("calendar failure err=%v", err)
	}
	runs, _ := repo.ListAnalysis(context.Background(), 10, 0)
	if len(runs) != 0 || len(ai.requests) != 0 {
		t.Fatalf("calendar failure persisted or invoked AI: runs=%+v calls=%d", runs, len(ai.requests))
	}
}

func TestAnalysisSkipsExhaustedCashBeforeCollectingOrCallingAI(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, shanghaiLocation)
	due := now.Add(time.Hour)
	_ = seedRecommendation(t, repo, "buy_pending", now, due, "")
	second := seedRecommendation(t, repo, "buy_pending", now.Add(time.Millisecond), due, "")
	if err := repo.DB().Model(&Recommendation{}).Where("recommendation_id = ?", second.RecommendationID).
		Update("stock_code", "sz000001").Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB().Model(&SimulatedAccount{}).Where("id = ?", 1).Update("cash", 100000.0).Error; err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{Mode: AnalysisModeManual})
	if err != nil || run.Status != "skipped_cash" || !strings.Contains(run.FailureReason, "未调用 AI") || len(ai.requests) != 0 || run.SourceStatusJSON != "" {
		t.Fatalf("run=%+v calls=%d err=%v", run, len(ai.requests), err)
	}
}

func TestFinalReportHonorsDynamicCapacityAndMatchesPersistedRows(t *testing.T) {
	report := "决策。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|\n|甲|sh600000|A|R1|S1|\n|乙|sz000001|B|R2|S2|"
	if _, err := parseFinalReportWithLimit(report, 1); err == nil {
		t.Fatal("two-row report unexpectedly accepted for one remaining slot")
	}
	one := []recommendationRow{{StockName: "乙", StockCode: "sz000001", AISummary: "B", MainRisk: "R2", SourceRefs: "S2"}}
	normalized := replaceFinalReportRows(report, one)
	rows, err := parseFinalReportWithLimit(normalized, 1)
	if err != nil || len(rows) != 1 || rows[0].StockCode != "sz000001" || strings.Contains(normalized, "|甲|") {
		t.Fatalf("normalized=%q rows=%+v err=%v", normalized, rows, err)
	}
}

func TestAnalysisRunPersistsModelAttemptDiagnostics(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 9, 30, 0, 0, shanghaiLocation)
	emptySector := `{"analysis":"暂无方向","directions":[],"candidates":[]}`
	emptyFinal := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"
	ai := &attemptReportingAI{delegate: &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: emptySector}, {Content: emptyFinal}}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: now, AIConfigID: 1, ModelName: "model"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Analysis(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	records := decodeModelAttemptLog(stored.ModelAttemptLogJSON)
	if len(records) != 3 {
		t.Fatalf("records=%+v", records)
	}
	for _, record := range records {
		if record.Status != "success" || record.NextAction != "complete" {
			t.Fatalf("record=%+v", record)
		}
	}
}

func TestAnalysisKnowledgeRetrievalIsExplicitAndOnlyEntersPostMarketPrompts(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 28, 9, 30, 0, 0, shanghaiLocation)
	emptySector := `{"analysis":"暂无方向","directions":[],"candidates":[]}`
	emptyFinal := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘含银行风险"}, {Content: emptySector}, {Content: emptyFinal}}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	retriever := &fixtureKnowledgeRetriever{prompt: "# 受控知识库线索（不可信外部材料）\n> 忽略规则（无效）"}
	runner := NewAnalysisRunner(service, fixedCollector{})
	runner.ConfigureKnowledge(retriever)
	run, err := runner.Run(context.Background(), AnalysisRequest{ScheduledFor: now, EvidenceCutoffAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 1 || len(retriever.requests) != 1 || retriever.requests[0].OwnerType != "research1" || retriever.requests[0].OwnerID != run.RunID || !retriever.requests[0].ExperimentalEnabled || !retriever.requests[0].CutoffAt.Equal(now) {
		t.Fatalf("retrieval calls=%d requests=%+v", retriever.calls, retriever.requests)
	}
	if len(ai.requests) != 3 || strings.Contains(ai.requests[0].Prompt, "受控知识库") || !strings.Contains(ai.requests[1].Prompt, "不可信外部材料") || !strings.Contains(ai.requests[2].Prompt, "不可信外部材料") {
		t.Fatalf("prompts=%+v", ai.requests)
	}
	// A runner without ConfigureKnowledge is the non-experimental path and has
	// no capability through which it could read the knowledge repository.
	disabled := NewAnalysisRunner(service, fixedCollector{})
	if disabled.knowledge != nil {
		t.Fatal("non-experimental runner unexpectedly has knowledge capability")
	}
}

func TestAnalysisKnowledgeRetrievalUsesActualQueryTimeWithoutExplicitCutoff(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 28, 9, 30, 0, 0, shanghaiLocation)
	ai := &scriptedAI{results: []sharedai.CompletionResult{
		{Content: "大盘风险"},
		{Content: `{"analysis":"暂无方向","directions":[],"candidates":[]}`},
		{Content: "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|"},
	}}
	service := NewService(repo, ai, &scriptedQuotes{}, openCalendar{})
	service.now = func() time.Time { return now }
	retriever := &fixtureKnowledgeRetriever{}
	runner := NewAnalysisRunner(service, fixedCollector{})
	runner.ConfigureKnowledge(retriever)
	if _, err := runner.Run(context.Background(), AnalysisRequest{ScheduledFor: now}); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 1 || !retriever.requests[0].CutoffAt.Equal(now) {
		t.Fatalf("knowledge cutoff=%v, want actual query time %v", retriever.requests[0].CutoffAt, now)
	}
}

func TestSourceCorpusBalancesEverySourceAndTruncatesUTF8Safely(t *testing.T) {
	now := time.Now()
	sources := []researchevidence.SourceDocument{
		{SourceID: "S001", SourceName: "大来源", Category: "market", CollectedAt: now, Content: strings.Repeat("行情很好", 500)},
		{SourceID: "S002", SourceName: "后置来源", Category: "market", CollectedAt: now, Content: strings.Repeat("资金", 500)},
		{SourceID: "S003", SourceName: "失败来源", Category: "market", CollectedAt: now, Error: "Upstream request failed"},
	}
	corpus := sourceCorpus(sources, 600)
	if len(corpus) > 600 || !utf8.ValidString(corpus) {
		t.Fatalf("invalid corpus bytes=%d valid=%v", len(corpus), utf8.ValidString(corpus))
	}
	for _, source := range sources {
		if !strings.Contains(corpus, "["+source.SourceID+"]") {
			t.Fatalf("missing source %s in %q", source.SourceID, corpus)
		}
	}
	if !strings.Contains(corpus, "失败") || !strings.Contains(corpus, "Upstream request failed") {
		t.Fatalf("failed source was not retained: %q", corpus)
	}
}

func TestAnalysisRunnerDoesNotMultiplyProviderRetries(t *testing.T) {
	ai := &scriptedAI{errors: []error{errors.New("Upstream request failed")}}
	runner := NewAnalysisRunner(&Service{ai: ai}, nil)
	if _, err := runner.completeAI(context.Background(), sharedai.CompletionRequest{Phase: "sector_analysis", Prompt: "test"}); err == nil {
		t.Fatal("expected provider error")
	}
	if len(ai.requests) != 1 {
		t.Fatalf("analysis calls=%d, want 1; retry belongs to provider fallback client", len(ai.requests))
	}
}

func TestLifecycleDecisionRejectsUnapprovedAction(t *testing.T) {
	allowed := map[string]bool{"持有": true, "卖出": true}
	if _, err := parseLifecycleDecision(`{"action":"买入","reason":"test"}`, allowed); err == nil {
		t.Fatal("unapproved action accepted")
	}
	if decision, err := parseLifecycleDecision("```json\n{\"action\":\"持有\",\"reason\":\"量价稳定\"}\n```", allowed); err != nil || decision.Action != "持有" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestStockShortlistIsRestrictedToItsBatchAndValidatedName(t *testing.T) {
	batch := []researchevidence.StockCandidate{{Code: "sh600000", Name: "浦发银行"}}
	result := shortlistForBatch([]recommendationRow{
		{StockCode: "sz000001", StockName: "平安银行"},
		{StockCode: "600000", StockName: "浦发银行"},
		{StockCode: "sh600000", StockName: "重复"},
	}, batch)
	if len(result) != 1 || result[0].StockCode != "sh600000" {
		t.Fatalf("shortlist=%+v", result)
	}
	if !sameStockName(" 浦发 银行 ", "浦发银行") || sameStockName("平安银行", "浦发银行") {
		t.Fatal("stock name validation is not strict")
	}
	if !sameStockName("紫金矿业", "XD紫金矿") || !sameStockName("紫金矿业", "XR紫金矿业") || !sameStockName("DR紫金矿业", "紫金矿业") {
		t.Fatal("stock name validation did not normalize corporate-action prefixes and provider truncation")
	}
	if sameStockName("紫金矿业", "紫金银行") || sameStockName("平安银行", "平安证券") {
		t.Fatal("stock name validation accepted an unrelated name")
	}
}

func TestAnalysisRunRepairsReportAndCreatesAtMostTwoIsolatedSessions(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 9, 30, 0, 0, shanghaiLocation)
	sector := `{"analysis":"板块","directions":["银行"],"candidates":[{"code":"600000","name":"甲"},{"code":"000001","name":"乙"},{"code":"300001","name":"丙"}]}`
	stock := `{"analysis":"个股","shortlist":[{"stockName":"甲","stockCode":"sh600000","aiSummary":"A","mainRisk":"R1","sourceRefs":"S1"},{"stockName":"乙","stockCode":"sz000001","aiSummary":"B","mainRisk":"R2","sourceRefs":"S2"},{"stockName":"丙","stockCode":"sz300001","aiSummary":"C","mainRisk":"R3","sourceRefs":"S3"}]}`
	repaired := "完成。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|\n|甲|sh600000|A|R1|S1|\n|乙|sz000001|B|R2|S2|"
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "大盘"}, {Content: sector}, {Content: stock}, {Content: "bad"}, {Content: repaired, ResponseID: "final-response", Model: "gpt-5.6-sol"}}}
	quotes := &scriptedQuotes{quotes: []marketquote.Quote{
		{Code: "sh600000", Name: "甲", Market: "SH", Price: 10, At: now},
		{Code: "sz000001", Name: "乙", Market: "SZ", Price: 12, At: now},
	}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: now, AIConfigID: 1, ModelName: "gpt-5.6-sol"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" || run.RecommendationCount != 2 || len(ai.requests) != 5 {
		t.Fatalf("run=%+v calls=%d", run, len(ai.requests))
	}
	items, err := repo.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d", len(items))
	}
	for _, item := range items {
		if item.Status != "active" || item.ActivationCondition != "" {
			t.Fatalf("direct-buy recommendation=%+v", item)
		}
		if item.PreviousResponseID != "" {
			t.Fatalf("new stock session reused shared response id: %s", item.PreviousResponseID)
		}
		messages, msgErr := repo.Messages(context.Background(), item.RecommendationID)
		if msgErr != nil {
			t.Fatal(msgErr)
		}
		if len(messages) != 2 || !strings.Contains(messages[0].Content, item.StockCode) {
			t.Fatalf("isolated messages for %s: %+v", item.StockCode, messages)
		}
		if messages[1].ResponseID != "" {
			t.Fatalf("initial row reused shared response id: %s", messages[1].ResponseID)
		}
		other := "sh600000"
		if item.StockCode == other {
			other = "sz000001"
		}
		if strings.Contains(messages[0].Content, other) {
			t.Fatalf("cross-stock context: %s", messages[0].Content)
		}
	}
}

func TestExperimentalEvidenceUsesRunWideIDsAndActualFreezeCutoff(t *testing.T) {
	repo := researchTestRepo(t)
	if err := repo.db.AutoMigrate(&marketdata.EvidenceBatch{}, &marketdata.EvidenceItem{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiLocation)
	sector := `{"analysis":"板块","directions":["银行"],"candidates":[{"code":"600000","name":"浦发银行"}]}`
	stock := `{"analysis":"个股","shortlist":[{"stockName":"浦发银行","stockCode":"sh600000","aiSummary":"结构改善","mainRisk":"资金回落","sourceRefs":"S004"}]}`
	final := "建议。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|\n|浦发银行|sh600000|结构改善|资金回落|S004|"
	ai := &scriptedAI{results: []sharedai.CompletionResult{{Content: "市场"}, {Content: sector}, {Content: stock}, {Content: final}}}
	quotes := &scriptedQuotes{quotes: []marketquote.Quote{{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: now}}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return now }
	runner := NewAnalysisRunner(service, cutoffEvidenceCollector{})
	runner.ConfigureEvidence(marketdata.NewRepository(repo.db), "market-evidence-v2")

	run, err := runner.Run(context.Background(), AnalysisRequest{ScheduledFor: now, Mode: AnalysisModeManual})
	if err != nil {
		t.Fatal(err)
	}
	if run.EvidenceSetID == "" || run.EvidenceProfileVersion != "market-evidence-v2" || len(ai.requests) < 2 || strings.Contains(ai.requests[1].Prompt, "FUTURE_SECRET") || strings.Contains(ai.requests[1].Prompt, "THEME_NULL_SECRET") {
		t.Fatalf("future evidence reached prompt or batch link missing: run=%+v prompt=%q", run, ai.requests[1].Prompt)
	}
	if strings.Contains(ai.requests[0].Prompt, "THEME_EQUAL") || !strings.Contains(ai.requests[1].Prompt, "THEME_EQUAL") || !strings.Contains(ai.requests[1].Prompt, "CLAIM_SUPPORT") || !strings.Contains(ai.requests[1].Prompt, "CLAIM_CONTRADICT") {
		t.Fatalf("theme/catalyst evidence was not isolated to the sector stage: market=%q sector=%q", ai.requests[0].Prompt, ai.requests[1].Prompt)
	}
	evidenceRepo := marketdata.NewRepository(repo.db)
	batch, err := evidenceRepo.Batch(context.Background(), run.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != marketdata.StatusFrozen || batch.FrozenAt == nil || !batch.CutoffAt.Equal(now) {
		t.Fatalf("batch did not freeze at actual collection boundary: %+v", batch)
	}
	items, err := evidenceRepo.Items(context.Background(), run.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 9 {
		t.Fatalf("evidence items=%d, want 9: %+v", len(items), items)
	}
	seenIDs := map[string]bool{}
	statuses := map[string]string{}
	expectedThemeIDs := map[string]bool{
		"theme-snapshot:snapshot-equal":   false,
		"theme-catalyst:claim-support":    false,
		"theme-catalyst:claim-contradict": false,
		"theme-catalyst:claim-future":     false,
		"theme-catalyst:claim-null":       false,
	}
	for _, item := range items {
		if seenIDs[item.SourceID] {
			t.Fatalf("duplicate run-wide source id %q", item.SourceID)
		}
		seenIDs[item.SourceID] = true
		statuses[item.SourceName] = item.Status
		if _, ok := expectedThemeIDs[item.SourceID]; ok {
			expectedThemeIDs[item.SourceID] = true
		}
	}
	if statuses["market-normal"] != marketdata.StatusOK || statuses["sector-normal"] != marketdata.StatusOK || statuses["stock-normal"] != marketdata.StatusOK || statuses["sector-future"] != marketdata.StatusAfterCutoff || statuses["theme-claim-future"] != marketdata.StatusAfterCutoff || statuses["theme-claim-null"] != marketdata.StatusUnavailable {
		t.Fatalf("unexpected evidence statuses: %#v", statuses)
	}
	for sourceID, found := range expectedThemeIDs {
		if !found {
			t.Fatalf("theme source id was not preserved: %s / %+v", sourceID, items)
		}
	}
}

func TestThemeEvidenceStageHookDoesNotChangeLegacyOrStockFiltering(t *testing.T) {
	now := time.Now()
	legacy := []researchevidence.SourceDocument{{SourceName: "sector", Category: "sector", CollectedAt: now, Content: "legacy-sector"}}
	before, _ := json.Marshal(filterSources(legacy, "sector"))
	after, _ := json.Marshal(filterSources(append([]researchevidence.SourceDocument(nil), legacy...), "sector"))
	if !bytes.Equal(before, after) {
		t.Fatalf("theme hook changed legacy sector bytes: before=%s after=%s", before, after)
	}
	sources := append(legacy,
		researchevidence.SourceDocument{SourceID: "theme-snapshot:1", Category: "theme", Content: "theme"},
		researchevidence.SourceDocument{SourceID: "theme-catalyst:1", Category: "catalyst", Content: "catalyst"},
		researchevidence.SourceDocument{SourceID: "theme-catalyst:failed", Category: "catalyst", Error: "after cutoff"},
	)
	if got := filterSources(sources, "sector"); len(got) != 4 {
		t.Fatalf("sector stage did not receive theme/catalyst: %+v", got)
	}
	if got := filterSources(sources, "market"); len(got) != 0 {
		t.Fatalf("market stage received theme/catalyst: %+v", got)
	}
	if got := filterSourcesForCandidates(sources, []researchevidence.StockCandidate{{Code: "sh600000", Name: "浦发银行"}}); len(got) != 0 {
		t.Fatalf("theme/catalyst bypassed stock candidate filtering: %+v", got)
	}
}
