package research

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type fixedCollector struct{}

func (fixedCollector) CollectMarket(_ context.Context, now time.Time) ([]SourceDocument, error) {
	return []SourceDocument{{SourceName: "CLS", Category: "market", CollectedAt: now, Content: "market"}}, nil
}

func TestEndToEndAnalysisActivationTPlusOneSaleAndNetYield(t *testing.T) {
	repo := researchTestRepo(t)
	current := time.Date(2026, 8, 14, 9, 30, 0, 0, shanghaiLocation)
	sector := `{"analysis":"银行资金转强","directions":["银行"],"candidates":[{"code":"600000","name":"浦发银行"}]}`
	stock := `{"analysis":"浦发银行量价结构改善","shortlist":[{"stockName":"浦发银行","stockCode":"sh600000","aiSummary":"结构改善","activationCondition":"放量突破早盘高点","mainRisk":"资金回落","sourceRefs":"S001"}]}`
	final := "建议等待动态激活。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|---|\n|浦发银行|sh600000|结构改善|放量突破早盘高点|资金回落|S001|"
	ai := &scriptedAI{results: []CompletionResult{
		{Content: "市场风险可控"}, {Content: sector}, {Content: stock}, {Content: final, ResponseID: "shared-final"},
		{Content: `{"action":"激活","reason":"条件满足"}`, ResponseID: "stock-activation"},
		{Content: `{"action":"卖出","reason":"次日冲高转弱"}`, ResponseID: "stock-sale"},
	}}
	quotes := &scriptedQuotes{quotes: []Quote{
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: current},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10, PreviousClose: 9.8, At: current.Add(15 * time.Minute)},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10.1, PreviousClose: 9.8, At: current.Add(15*time.Minute + time.Second)},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 11, PreviousClose: 10.1, At: current.AddDate(0, 0, 3).Add(15 * time.Minute)},
		{Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 11.1, PreviousClose: 10.1, At: current.AddDate(0, 0, 3).Add(15*time.Minute + time.Second)},
	}}
	service := NewService(repo, ai, quotes, openCalendar{})
	service.now = func() time.Time { return current }
	run, err := NewAnalysisRunner(service, fixedCollector{}).Run(context.Background(), AnalysisRequest{ScheduledFor: current, AIConfigID: 1, ModelName: "gpt-5.6-sol"})
	if err != nil || run.RecommendationCount != 1 {
		t.Fatalf("analysis run=%+v err=%v", run, err)
	}
	items, _ := repo.ListRecommendations(context.Background(), 10, 0)
	current = time.Date(2026, 8, 14, 9, 45, 0, 0, shanghaiLocation)
	if err = service.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	active, _ := repo.Recommendation(context.Background(), items[0].RecommendationID)
	if active.Status != "active" || active.Quantity < 100 {
		t.Fatalf("activation=%+v", active)
	}
	current = time.Date(2026, 8, 17, 9, 45, 0, 0, shanghaiLocation)
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
	if len(ai.requests) != 6 || ai.requests[4].PreviousResponseID != "" || ai.requests[5].PreviousResponseID != "stock-activation" {
		t.Fatalf("per-stock response chain=%+v", ai.requests)
	}
}
func (fixedCollector) CollectSectors(_ context.Context, now time.Time) ([]SourceDocument, error) {
	return []SourceDocument{{SourceName: "EM", Category: "sector", CollectedAt: now, Content: "sector"}}, nil
}
func (fixedCollector) CollectStocks(_ context.Context, now time.Time, candidates []StockCandidate) ([]SourceDocument, error) {
	result := make([]SourceDocument, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, SourceDocument{SourceName: "stock", Category: "stock", CollectedAt: now, Content: c.Code + " data"})
	}
	return result, nil
}

func TestFinalReportAllowsCashAndAtMostTwo(t *testing.T) {
	empty := "空仓。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|---|"
	rows, err := parseFinalReport(empty)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty rows=%d err=%v", len(rows), err)
	}
	tooMany := empty + "\n|甲|sh600000|a|b|c|S1|\n|乙|sz000001|a|b|c|S2|\n|丙|sz300001|a|b|c|S3|"
	if _, err := parseFinalReport(tooMany); err == nil {
		t.Fatal("more than two rows must fail")
	}
}

func TestCandidateAndSourceBounds(t *testing.T) {
	candidates := []StockCandidate{{Code: "600000", Name: "甲"}, {Code: "sh600000", Name: "重复"}, {Code: "430001", Name: "北交"}, {Code: "300001", Name: "乙"}}
	valid := validUniqueCandidates(candidates, 50)
	if len(valid) != 2 || valid[0].Code != "sh600000" || valid[1].Code != "sz300001" {
		t.Fatalf("valid=%+v", valid)
	}
	now := time.Now()
	sources := dedupeSources([]SourceDocument{{SourceName: "Sina", Category: "market", CollectedAt: now, Content: " same  news "}, {SourceName: "Sina", Category: "market", CollectedAt: now, Content: "same news"}})
	if len(sources) != 1 || sources[0].SourceID == "" {
		t.Fatalf("sources=%+v", sources)
	}
	if corpus := sourceCorpus(sources, 64); len(corpus) > 64 || !strings.Contains(corpus, sources[0].SourceID) {
		t.Fatalf("corpus should respect cap and retain id: %q", corpus)
	}
}

func TestListAnalysisReturnsLightweightSourceCounts(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Now()
	run := AnalysisRun{
		RunID: "summary-run", ScheduledFor: now, StartedAt: now, Status: "failed",
		MarketReport: strings.Repeat("full report", 100), SourceStatusJSON: sourceStatusJSON([]SourceDocument{
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

func TestSourceCorpusBalancesEverySourceAndTruncatesUTF8Safely(t *testing.T) {
	now := time.Now()
	sources := []SourceDocument{
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
	if _, err := runner.completeAI(context.Background(), CompletionRequest{Phase: "sector_analysis", Prompt: "test"}); err == nil {
		t.Fatal("expected provider error")
	}
	if len(ai.requests) != 1 {
		t.Fatalf("analysis calls=%d, want 1; retry belongs to provider fallback client", len(ai.requests))
	}
}

func TestLifecycleDecisionRejectsUnapprovedAction(t *testing.T) {
	allowed := map[string]bool{"等待": true, "激活": true, "失效": true}
	if _, err := parseLifecycleDecision(`{"action":"买入","reason":"test"}`, allowed); err == nil {
		t.Fatal("unapproved action accepted")
	}
	if decision, err := parseLifecycleDecision("```json\n{\"action\":\"等待\",\"reason\":\"未满足\"}\n```", allowed); err != nil || decision.Action != "等待" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestStockShortlistIsRestrictedToItsBatchAndValidatedName(t *testing.T) {
	batch := []StockCandidate{{Code: "sh600000", Name: "浦发银行"}}
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
}

func TestAnalysisRunRepairsReportAndCreatesAtMostTwoIsolatedSessions(t *testing.T) {
	repo := researchTestRepo(t)
	now := time.Date(2026, 8, 14, 9, 30, 0, 0, shanghaiLocation)
	sector := `{"analysis":"板块","directions":["银行"],"candidates":[{"code":"600000","name":"甲"},{"code":"000001","name":"乙"},{"code":"300001","name":"丙"}]}`
	stock := `{"analysis":"个股","shortlist":[{"stockName":"甲","stockCode":"sh600000","aiSummary":"A","activationCondition":"A1","mainRisk":"R1","sourceRefs":"S1"},{"stockName":"乙","stockCode":"sz000001","aiSummary":"B","activationCondition":"B1","mainRisk":"R2","sourceRefs":"S2"},{"stockName":"丙","stockCode":"sz300001","aiSummary":"C","activationCondition":"C1","mainRisk":"R3","sourceRefs":"S3"}]}`
	repaired := "完成。\n\n" + finalReportTableHeader + "\n|---|---|---|---|---|---|\n|甲|sh600000|A|A1|R1|S1|\n|乙|sz000001|B|B1|R2|S2|"
	ai := &scriptedAI{results: []CompletionResult{{Content: "大盘"}, {Content: sector}, {Content: stock}, {Content: "bad"}, {Content: repaired, ResponseID: "final-response", Model: "gpt-5.6-sol"}}}
	quotes := &scriptedQuotes{quotes: []Quote{{Code: "sh600000", Name: "甲", Market: "SH", Price: 10, At: now}, {Code: "sz000001", Name: "乙", Market: "SZ", Price: 12, At: now}}}
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
