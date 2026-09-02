package research2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-stock/backend/knowledge"
	"go-stock/backend/research"
	"go-stock/backend/researchaudit"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type sequenceAI struct {
	responses []string
	calls     int
	requests  []research.CompletionRequest
}

type advancingAI struct {
	current  *time.Time
	advance  time.Time
	response string
	calls    int
}

type multiAttemptAI struct{}

func (multiAttemptAI) Complete(_ context.Context, request research.CompletionRequest) (research.CompletionResult, error) {
	if request.OnAttempt != nil {
		started := time.Date(2026, 8, 27, 1, 55, 1, 0, time.UTC)
		failedAt := started.Add(time.Second)
		request.OnAttempt(research.ModelAttemptRecord{ID: "provider-attempt-1", Phase: request.Phase, ConfigID: 7, ProviderName: "primary", ModelName: "fixture", Attempt: 1, MaxAttempts: 2, StartedAt: started, CompletedAt: &failedAt, Status: "failed", ErrorCategory: "network_error", ErrorMessage: "connection reset", Retryable: true})
		succeededAt := failedAt.Add(time.Second)
		request.OnAttempt(research.ModelAttemptRecord{ID: "provider-attempt-2", Phase: request.Phase, ConfigID: 7, ProviderName: "primary", ModelName: "fixture", Attempt: 2, MaxAttempts: 2, StartedAt: failedAt, CompletedAt: &succeededAt, Status: "success"})
	}
	return research.CompletionResult{Content: `{"tradingDay":true,"conclusion":"空仓","recommendations":[]}`, Model: "fixture"}, nil
}

func (a *advancingAI) Complete(_ context.Context, _ research.CompletionRequest) (research.CompletionResult, error) {
	a.calls++
	*a.current = a.advance
	return research.CompletionResult{Content: a.response, Model: "test-model"}, nil
}

func (a *sequenceAI) Complete(_ context.Context, request research.CompletionRequest) (research.CompletionResult, error) {
	a.calls++
	a.requests = append(a.requests, request)
	if request.OnAttempt != nil {
		now := time.Date(2026, 8, 27, 1, 56, a.calls, 0, time.UTC)
		request.OnAttempt(research.ModelAttemptRecord{ID: fmt.Sprintf("attempt-%d", a.calls), Phase: request.Phase, ConfigID: 7, ProviderName: "fixture-provider", ModelName: "fixture-model", Attempt: 1, MaxAttempts: 1, StartedAt: now, CompletedAt: &now, Status: "success"})
	}
	index := a.calls - 1
	if index >= len(a.responses) {
		index = len(a.responses) - 1
	}
	return research.CompletionResult{Content: a.responses[index], Model: "test-model"}, nil
}

type fixtureKnowledgeRetriever struct {
	calls   int
	request knowledge.ResearchRetrievalRequest
	prompt  string
}

func (retriever *fixtureKnowledgeRetriever) RetrieveForResearch(_ context.Context, request knowledge.ResearchRetrievalRequest) (knowledge.ResearchRetrieval, error) {
	retriever.calls++
	retriever.request = request
	return knowledge.ResearchRetrieval{RetrievalRunID: "retrieval-r2", Prompt: retriever.prompt}, nil
}

type fixedEvidence struct{ value Evidence }

func (e fixedEvidence) Collect(context.Context, time.Time) (Evidence, error) { return e.value, nil }

type recordingEvidence struct {
	value  Evidence
	cutoff time.Time
}

func (e *recordingEvidence) Collect(_ context.Context, cutoff time.Time) (Evidence, error) {
	e.cutoff = cutoff
	return e.value, nil
}

type failingRunEvidence struct {
	value Evidence
	err   error
}

func (e failingRunEvidence) Collect(context.Context, time.Time) (Evidence, error) {
	return e.value, e.err
}

func (e failingRunEvidence) CollectForRun(context.Context, string, time.Time) (Evidence, error) {
	return e.value, e.err
}

type testCalendar struct{}

func (testCalendar) IsTradingDay(_ context.Context, value time.Time) (bool, error) {
	return value.Weekday() != time.Saturday && value.Weekday() != time.Sunday, nil
}

type testMarket struct{ price float64 }

func (m testMarket) PriceAt(_ context.Context, code string, target time.Time, _ bool) (PriceSnapshot, error) {
	return PriceSnapshot{Code: code, Price: m.price, At: target, Source: "test"}, nil
}
func (testMarket) Metrics(context.Context, Recommendation) (MetricSnapshot, error) {
	return MetricSnapshot{}, nil
}

type recordingMarket struct {
	prices       map[string]float64
	currentFlags []bool
}

func (m *recordingMarket) PriceAt(_ context.Context, code string, target time.Time, current bool) (PriceSnapshot, error) {
	m.currentFlags = append(m.currentFlags, current)
	return PriceSnapshot{Code: code, Price: m.prices[code], At: target, Source: "test"}, nil
}
func (*recordingMarket) Metrics(context.Context, Recommendation) (MetricSnapshot, error) {
	return MetricSnapshot{}, nil
}

type timestampMarket struct {
	price float64
	at    time.Time
}

func (m timestampMarket) PriceAt(_ context.Context, code string, _ time.Time, _ bool) (PriceSnapshot, error) {
	return PriceSnapshot{Code: code, Price: m.price, At: m.at, Source: "timestamp-test"}, nil
}
func (timestampMarket) Metrics(context.Context, Recommendation) (MetricSnapshot, error) {
	return MetricSnapshot{}, nil
}

func research2TestRepository(t *testing.T) *Repository {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&AnalysisRun{}, &Recommendation{}, &Trade{}, &Account{}, &AccountSnapshot{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(database)
	if err = repository.EnsureAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestParseModelOutputAcceptsJSONFence(t *testing.T) {
	value, err := ParseModelOutput("```json\n{\"tradingDay\":true,\"reportMarkdown\":\"ok\",\"recommendations\":[]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !value.TradingDay || value.ReportMarkdown != "ok" {
		t.Fatalf("unexpected output: %+v", value)
	}
}

func TestRunnerRetriesOnceWhenModelJSONIsInvalid(t *testing.T) {
	repository := research2TestRepository(t)
	ai := &sequenceAI{responses: []string{
		`{"tradingDay":true,"reportMarkdown":"损坏"æ}`,
		`{"tradingDay":true,"conclusion":"证据不足，空仓","reportMarkdown":"# 隔离报告\n\n证据不足，空仓。","recommendations":[]}`,
	}}
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{Prompt: "测试证据", SourceStatusJSON: "[]"}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return scheduled.Add(7 * time.Minute) }, func(context.Context, time.Time) error { return nil })
	run, err := runner.Run(context.Background(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	if ai.calls != 2 || run.Status != "no_recommendation" || !strings.Contains(run.ReportMarkdown, "核心证据窗口") || strings.Contains(run.ReportMarkdown, "隔离报告") {
		t.Fatalf("calls=%d run=%+v", ai.calls, run)
	}
}

func TestRunnerConsumesKnowledgeThroughReadOnlyRetrieverAtFrozenCutoff(t *testing.T) {
	repository := research2TestRepository(t)
	ai := &sequenceAI{responses: []string{`{"tradingDay":true,"conclusion":"空仓","reportMarkdown":"空仓","recommendations":[]}`}}
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	retriever := &fixtureKnowledgeRetriever{prompt: "# 受控知识库线索（不可信外部材料）\n> 历史线索"}
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{Prompt: "冻结市场证据", SourceStatusJSON: "[]"}}, testCalendar{})
	runner.ConfigureKnowledge(retriever)
	runner.ConfigureReplayClock(func() time.Time { return scheduled.Add(7 * time.Minute) }, func(context.Context, time.Time) error { return nil })
	run, err := runner.Run(context.Background(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	wantCutoff := time.Date(2026, 8, 27, 9, 57, 0, 0, loc)
	if retriever.calls != 1 || retriever.request.OwnerType != "research2" || retriever.request.OwnerID != run.RunID || !retriever.request.CutoffAt.Equal(wantCutoff) || !retriever.request.ExperimentalEnabled {
		t.Fatalf("retrieval=%+v calls=%d", retriever.request, retriever.calls)
	}
	if len(ai.requests) != 1 || !strings.Contains(ai.requests[0].Prompt, "冻结市场证据") || !strings.Contains(ai.requests[0].Prompt, "不可信外部材料") {
		t.Fatalf("requests=%+v", ai.requests)
	}
	if _, ok := any(runner.knowledge).(knowledge.ResearchRetriever); !ok {
		t.Fatal("Research2 did not retain only the read-only retrieval capability")
	}
}

func TestRunnerUsesActualStartAsCutoffAndTrailingFiveMinuteWindow(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	started := time.Date(2026, 8, 27, 10, 14, 0, 0, loc)
	collector := &recordingEvidence{value: Evidence{Prompt: `{}`, SourceStatusJSON: `[]`}}
	runner := NewRunner(repository, &sequenceAI{responses: []string{`{"tradingDay":true,"conclusion":"空仓","recommendations":[]}`}}, collector, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return started }, nil)

	run, err := runner.Run(context.Background(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 8, 27, 10, 9, 0, 0, loc)
	if !collector.cutoff.Equal(started) || !run.EvidenceCutoffAt.Equal(started) || run.EvidenceWindowStartAt == nil || !run.EvidenceWindowStartAt.Equal(wantStart) {
		t.Fatalf("cutoff=%v run=%+v", collector.cutoff, run)
	}
	if run.StrategyVersion != "research2-trailing5-v3" {
		t.Fatalf("strategyVersion=%q", run.StrategyVersion)
	}
}

func TestRunnerRejectsStartOutsideMorningWindow(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	ai := &sequenceAI{responses: []string{`{}`}}
	runner := NewRunner(repository, ai, fixedEvidence{}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return time.Date(2026, 8, 27, 11, 26, 0, 0, loc) }, nil)

	run, err := runner.Run(context.Background(), scheduled)
	if err != nil || run.Status != "missed_window" || ai.calls != 0 {
		t.Fatalf("run=%+v calls=%d err=%v", run, ai.calls, err)
	}
}

func TestRunnerAllowsTheWhole1125Minute(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	started := time.Date(2026, 8, 27, 11, 25, 59, 0, loc)
	ai := &sequenceAI{responses: []string{`{"tradingDay":true,"conclusion":"空仓","recommendations":[]}`}}
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{Prompt: `{}`, SourceStatusJSON: `[]`}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return started }, nil)

	run, err := runner.Run(context.Background(), scheduled)
	if err != nil || run.Status != "no_recommendation" || ai.calls != 1 {
		t.Fatalf("run=%+v calls=%d err=%v", run, ai.calls, err)
	}
}

func TestRunnerTakesSignalTimeAfterAllValidation(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	started := time.Date(2026, 8, 27, 10, 3, 40, 0, loc)
	validated := started.Add(4 * time.Second)
	clockCalls := 0
	runner := NewRunner(repository, &sequenceAI{responses: []string{`{"tradingDay":true,"conclusion":"推荐","recommendations":[{"code":"sh600000","marketScore":15,"sectorScore":15,"stockScore":20,"catalystScore":10,"riskDeduction":0,"finalScore":60,"referencePrice":10}]}`}}, fixedEvidence{value: Evidence{Prompt: `{}`, SourceStatusJSON: `[]`, Candidates: []research.StockCandidate{{Code: "sh600000", Name: "浦发银行"}}}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time {
		clockCalls++
		if clockCalls >= 3 {
			return validated
		}
		return started
	}, nil)

	run, err := runner.Run(context.Background(), started)
	if err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if run.GeneratedAt == nil || !run.GeneratedAt.Equal(validated) || len(items) != 1 || !items[0].SignalAt.Equal(validated) || !items[0].TargetBuyAt.Equal(validated) {
		t.Fatalf("run=%+v items=%+v clockCalls=%d", run, items, clockCalls)
	}
}

func TestRunnerRejectsModelResultAfterHardDeadline(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	current := time.Date(2026, 8, 27, 11, 25, 0, 0, loc)
	ai := &advancingAI{current: &current, advance: time.Date(2026, 8, 27, 11, 30, 1, 0, loc), response: `{"tradingDay":true,"conclusion":"迟到","recommendations":[]}`}
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{Prompt: `{}`, SourceStatusJSON: `[]`}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return current }, nil)

	run, err := runner.Run(context.Background(), scheduled)
	if err != nil || run.Status != "failed" || run.RecommendationCount != 0 || ai.calls != 1 || !strings.Contains(run.FailureReason, "11:30") {
		t.Fatalf("run=%+v calls=%d err=%v", run, ai.calls, err)
	}
}

func TestBuildPromptUsesCompactInjectedEvidenceWithoutReportOrBuyRange(t *testing.T) {
	loc := shanghai()
	cutoff := time.Date(2026, 8, 27, 10, 14, 0, 0, loc)
	prompt := buildPrompt(Evidence{Prompt: `{"candidates":[{"code":"sh600000"}]}`, WindowStartAt: cutoff.Add(-5 * time.Minute)}, cutoff)
	for _, forbidden := range []string{"https://", "reportMarkdown", "buyLower", "buyUpper", "09:55冻结"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt contains forbidden %q: %s", forbidden, prompt)
		}
	}
	for _, required := range []string{"research2-trailing5-v3", "2026-08-27 10:09:00", "2026-08-27 10:14:00", "系统注入的紧凑结构化证据"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt missing %q: %s", required, prompt)
		}
	}
}

func TestRunnerRepairsInvalidSourceRefsAndStoresOnlyStableIDs(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	started := time.Date(2026, 8, 27, 9, 57, 0, 0, loc)
	available := started.Add(-time.Minute)
	ai := &sequenceAI{responses: []string{
		`{"tradingDay":true,"conclusion":"推荐","recommendations":[{"code":"sh600000","marketScore":15,"sectorScore":0,"stockScore":36,"catalystScore":0,"riskDeduction":0,"finalScore":51,"referencePrice":10,"sourceRefs":["missing"]}]}`,
		`{"tradingDay":true,"conclusion":"推荐","recommendations":[{"code":"sh600000","marketScore":15,"sectorScore":0,"stockScore":36,"catalystScore":0,"riskDeduction":0,"finalScore":51,"referencePrice":10,"sourceRefs":["market","quote-sh600000"]}]}`,
	}}
	evidence := Evidence{
		Prompt:           `{"sources":[{"sourceId":"market"},{"sourceId":"quote-sh600000"}]}`,
		SourceStatusJSON: `[]`,
		Candidates:       []research.StockCandidate{{Code: "sh600000", Name: "浦发银行"}},
		Documents: []research.SourceDocument{
			{SourceID: "market", SourceName: "市场宽度", Category: "market", AvailableAt: &available, CollectedAt: available, Content: `{"advances":3000}`},
			{SourceID: "quote-sh600000", SourceName: "腾讯行情 sh600000", Category: "stock", AvailableAt: &available, CollectedAt: available, Content: "sh600000 quote"},
		},
		CandidateReferencePrices: map[string]float64{"sh600000": 9.88},
	}
	runner := NewRunner(repository, ai, fixedEvidence{value: evidence}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return started }, nil)

	run, err := runner.Run(context.Background(), started.Add(-7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ai.calls != 2 || run.Status != "success" || len(items) != 1 || items[0].SourceRefs != "market\nquote-sh600000" || items[0].ReferencePrice != 9.88 {
		t.Fatalf("calls=%d run=%+v items=%+v", ai.calls, run, items)
	}
}

func TestSourceRefsRejectStaleEnvelopeEvenWhenItHasContent(t *testing.T) {
	cutoff := time.Date(2026, 8, 27, 9, 50, 0, 0, shanghai())
	document := research.SourceDocument{SourceID: "stale-market", SourceName: "stale", Category: "market", AvailableAt: &cutoff, CollectedAt: cutoff,
		Content: `{"status":"stale","data":{"advances":3000}}`}
	warnings := validateRecommendationSourceRefs("sh600000", []string{"stale-market"}, []research.SourceDocument{document}, cutoff, []research.StockCandidate{{Code: "sh600000"}})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "非可用状态") {
		t.Fatalf("warnings=%v", warnings)
	}
}

func TestRunnerRecordsBothModelCallsWithActualProviderAndIsolatedRetryLogs(t *testing.T) {
	repository := research2TestRepository(t)
	if err := repository.DB().AutoMigrate(&researchaudit.PromptVersion{}, &researchaudit.Payload{}, &researchaudit.RunState{}, &researchaudit.Replay{}, &researchaudit.ReplayResult{}); err != nil {
		t.Fatal(err)
	}
	ai := &sequenceAI{responses: []string{
		`{"tradingDay":true,"reportMarkdown":"损坏"æ}`,
		`{"tradingDay":true,"conclusion":"空仓","reportMarkdown":"空仓","recommendations":[]}`,
	}}
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{Prompt: "测试证据", SourceStatusJSON: "[]"}}, testCalendar{})
	runner.ConfigureAudit(researchaudit.NewRecorder(researchaudit.NewRepository(repository.DB())))
	runner.ConfigureReplayClock(func() time.Time { return scheduled.Add(7 * time.Minute) }, func(context.Context, time.Time) error { return nil })
	run, err := runner.Run(context.Background(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	view, err := researchaudit.NewRecorder(researchaudit.NewRepository(repository.DB())).Audit(context.Background(), researchaudit.OwnerResearch2, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != researchaudit.StatusComplete || len(view.Payloads) != 2 {
		t.Fatalf("audit=%+v", view)
	}
	for _, payload := range view.Payloads {
		if payload.ProviderName != "fixture-provider" || payload.ModelName != "fixture-model" || !strings.Contains(payload.ModelParametersJSON, `"actualConfigId":7`) {
			t.Fatalf("actual provider/model not recorded: %+v", payload.Payload)
		}
	}
	if strings.Contains(view.Payloads[1].RepairLog, "attempt-1") || !strings.Contains(view.Payloads[1].RepairLog, "attempt-2") {
		t.Fatalf("repair retry log crossed calls: %q", view.Payloads[1].RepairLog)
	}
}

func TestRunnerRecordsEveryProviderAttemptAsImmutablePayload(t *testing.T) {
	repository := research2TestRepository(t)
	if err := repository.DB().AutoMigrate(&researchaudit.PromptVersion{}, &researchaudit.Payload{}, &researchaudit.RunState{}, &researchaudit.Replay{}, &researchaudit.ReplayResult{}); err != nil {
		t.Fatal(err)
	}
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	runner := NewRunner(repository, multiAttemptAI{}, fixedEvidence{value: Evidence{Prompt: `{"version":"research2-trailing5-v3","candidates":[],"sources":[]}`, SourceStatusJSON: "[]"}}, testCalendar{})
	runner.ConfigureAudit(researchaudit.NewRecorder(researchaudit.NewRepository(repository.DB())))
	runner.ConfigureReplayClock(func() time.Time { return scheduled.Add(7 * time.Minute) }, func(context.Context, time.Time) error { return nil })
	run, err := runner.Run(context.Background(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	view, err := researchaudit.NewRecorder(researchaudit.NewRepository(repository.DB())).Audit(context.Background(), researchaudit.OwnerResearch2, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Payloads) != 2 || view.Payloads[0].Attempt != 1 || view.Payloads[1].Attempt != 2 {
		t.Fatalf("attempt payloads=%+v", view.Payloads)
	}
	if !strings.Contains(view.Payloads[0].RepairLog, "network_error") || view.Payloads[0].RawResponse != "" {
		t.Fatalf("failed attempt was not archived correctly: %+v", view.Payloads[0])
	}
	if !strings.Contains(view.Payloads[1].RawResponse, `"conclusion":"空仓"`) {
		t.Fatalf("successful attempt response is missing: %+v", view.Payloads[1])
	}
}

func TestRunnerPersistsEvidenceAssociationBeforeCollectionFailure(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	collectorErr := errors.New("fixture collection failed")
	runner := NewRunner(repository, &sequenceAI{responses: []string{`{}`}}, failingRunEvidence{value: Evidence{
		EvidenceProfileVersion: "profile-test",
		EvidenceSetID:          "evidence-set-test",
		SourceStatusJSON:       `[{"source":"fixture","status":"unavailable"}]`,
	}, err: collectorErr}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return scheduled.Add(7 * time.Minute) }, func(context.Context, time.Time) error { return nil })

	run, err := runner.Run(context.Background(), scheduled)
	if !errors.Is(err, collectorErr) || run.Status != "failed" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	var stored AnalysisRun
	if err := repository.DB().Where("run_id = ?", run.RunID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EvidenceSetID != "evidence-set-test" || stored.EvidenceProfileVersion != "profile-test" || stored.StrategyVersion != "research2-trailing5-v3" {
		t.Fatalf("failed run lost evidence association: %+v", stored)
	}
	if !strings.Contains(stored.SourceStatusJSON, "fixture") {
		t.Fatalf("failed run lost evidence source status: %q", stored.SourceStatusJSON)
	}
}

func TestRunnerStoresScoreAbove50EvenWhenModelConclusionSaysStayOut(t *testing.T) {
	repository := research2TestRepository(t)
	ai := &sequenceAI{responses: []string{
		`{"tradingDay":true,"conclusion":"空仓，不推荐任何股票","reportMarkdown":"# 结论\n\n模型伪造报告。","recommendations":[{"code":"sh600000","name":"模型名称","marketScore":15,"sectorScore":15,"stockScore":20,"catalystScore":10,"riskDeduction":0,"finalScore":51,"referencePrice":10,"buyLower":9,"buyUpper":11}]}`,
	}}
	loc := shanghai()
	scheduled := time.Date(2026, 8, 27, 9, 50, 0, 0, loc)
	runner := NewRunner(repository, ai, fixedEvidence{value: Evidence{
		Prompt:           "测试证据",
		SourceStatusJSON: "[]",
		Candidates:       []research.StockCandidate{{Code: "sh600000", Name: "证据名称"}},
	}}, testCalendar{})
	runner.ConfigureReplayClock(func() time.Time { return scheduled.Add(7 * time.Minute) }, func(context.Context, time.Time) error { return nil })

	run, err := runner.Run(context.Background(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" || run.RecommendationCount != 1 || len(items) != 1 {
		t.Fatalf("run=%+v recommendations=%+v", run, items)
	}
	if items[0].FinalScore != 60 || items[0].StockCode != "sh600000" || items[0].BuyLower != 0 || items[0].BuyUpper != 0 {
		t.Fatalf("unexpected stored recommendation: %+v", items[0])
	}
	if !strings.Contains(run.ReportMarkdown, "已按分项重新计算") || strings.Contains(run.ReportMarkdown, "模型伪造报告") {
		t.Fatalf("expected score discrepancy warning, report=%q", run.ReportMarkdown)
	}
}

func TestValidateRecommendationsRejectsStocksOutsideFrozenEvidence(t *testing.T) {
	loc := shanghai()
	generated := time.Date(2026, 8, 27, 9, 58, 0, 0, loc)
	values := []modelRecommendation{
		{Code: "sh600000", Name: "模型名称", MarketScore: 15, SectorScore: 15, StockScore: 20, CatalystScore: 10, FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11},
		{Code: "sz000001", Name: "证据外股票", MarketScore: 15, SectorScore: 15, StockScore: 20, CatalystScore: 10, FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11},
	}
	items, warnings := validateRecommendations("run", generated, generated, []research.StockCandidate{{Code: "sh600000", Name: "证据名称"}}, values)
	if len(items) != 1 || items[0].StockCode != "sh600000" || items[0].StockName != "证据名称" {
		t.Fatalf("unexpected validated items: %+v", items)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one evidence warning, got %v", warnings)
	}
}

func TestValidateRecommendationsUsesScoreAndCodePriority(t *testing.T) {
	loc := shanghai()
	generated := time.Date(2026, 8, 27, 9, 58, 0, 0, loc)
	values := []modelRecommendation{
		{Code: "sz000003", MarketScore: 10, SectorScore: 15, StockScore: 25, CatalystScore: 10, FinalScore: 60, ReferencePrice: 10},
		{Code: "sz000002", MarketScore: 15, SectorScore: 20, StockScore: 25, CatalystScore: 10, FinalScore: 70, ReferencePrice: 10},
		{Code: "sz000001", MarketScore: 15, SectorScore: 20, StockScore: 25, CatalystScore: 10, FinalScore: 70, ReferencePrice: 10},
		{Code: "sh600000", MarketScore: 20, SectorScore: 25, StockScore: 25, CatalystScore: 10, FinalScore: 80, ReferencePrice: 10},
	}
	items, warnings := validateRecommendations("run", generated, generated, nil, values)
	if len(warnings) != 0 {
		t.Fatalf("warnings=%v", warnings)
	}
	want := []string{"sh600000", "sz000001", "sz000002"}
	if len(items) != len(want) {
		t.Fatalf("items=%+v", items)
	}
	for index, code := range want {
		if items[index].StockCode != code {
			t.Fatalf("priority[%d]=%s want=%s", index, items[index].StockCode, code)
		}
	}
}

func TestValidateAllocationUsesEqualCashFractions(t *testing.T) {
	for _, test := range []struct {
		count int
		want  int64
	}{{1, 1100}, {2, 500}, {3, 300}} {
		prices := make([]float64, test.count)
		for index := range prices {
			prices[index] = 10
		}
		quantities, err := ValidateAllocation(prices, InitialCash)
		if err != nil {
			t.Fatal(err)
		}
		for _, quantity := range quantities {
			if quantity != test.want {
				t.Fatalf("count=%d quantities=%v want=%d", test.count, quantities, test.want)
			}
		}
	}
}

func TestTradingServiceBuysThreeRecommendationsAtAboutOneThirdEach(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 10, 0, 5, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-10 * time.Minute), StartedAt: now.Add(-10 * time.Minute), EvidenceCutoffAt: now.Add(-5 * time.Minute), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 3, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	items := make([]Recommendation, 0, 3)
	for _, code := range []string{"sh600000", "sz000001", "sz002594"} {
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: code, StockName: code, SignalAt: now.Add(-time.Minute), FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", TargetBuyAt: now.Add(-5 * time.Second)})
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	service := NewTradingService(repository, testMarket{price: 10}, testCalendar{})
	if err := service.ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	bought, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range bought {
		if item.Status != "active" || item.Quantity != 300 {
			t.Fatalf("recommendation=%+v", item)
		}
	}
	overview, err := repository.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.Cash >= 3000 || overview.Cash <= 2900 {
		t.Fatalf("cash=%f; expected roughly 3000 after three equal one-third purchases", overview.Cash)
	}
}

func TestTradingServiceReallocatesAfterUnaffordableCandidate(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 10, 0, 5, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-10 * time.Minute), StartedAt: now.Add(-10 * time.Minute), EvidenceCutoffAt: now.Add(-5 * time.Minute), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 3, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	prices := map[string]float64{"sh600000": 10, "sz000001": 10, "sz002594": 60}
	items := make([]Recommendation, 0, 3)
	for _, code := range []string{"sh600000", "sz000001", "sz002594"} {
		price := prices[code]
		items = append(items, Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: code, StockName: code, SignalAt: now.Add(-time.Minute), FinalScore: 60, ReferencePrice: price, BuyLower: price * 0.9, BuyUpper: price * 1.1, Status: "buy_pending", TargetBuyAt: now.Add(-5 * time.Second)})
	}
	if err := repository.CreateRecommendations(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	service := NewTradingService(repository, &recordingMarket{prices: prices}, testCalendar{})
	if err := service.ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result {
		if item.StockCode == "sz002594" {
			if item.Status != "missed_cash" {
				t.Fatalf("high-priced third candidate should be skipped: %+v", item)
			}
			continue
		}
		if item.Status != "active" || item.Quantity != 500 {
			t.Fatalf("remaining candidates should each receive about one-half: %+v", item)
		}
	}
}

func TestTradingServiceDoesNotTradeDuringLunch(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-2 * time.Hour), StartedAt: now.Add(-2 * time.Hour), EvidenceCutoffAt: now.Add(-2 * time.Hour), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "test", SignalAt: now.Add(-2 * time.Hour), FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", TargetBuyAt: now.Add(-time.Hour)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	market := &recordingMarket{prices: map[string]float64{"sh600000": 10}}
	if err := NewTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ListRecommendations(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantTarget := time.Date(2026, 8, 27, 13, 0, 0, 0, loc)
	if len(market.currentFlags) != 0 || result[0].Status != "buy_pending" || !result[0].TargetBuyAt.Equal(wantTarget) {
		t.Fatalf("lunch processing must not request a quote or trade: calls=%v item=%+v", market.currentFlags, result[0])
	}
}

func TestSellRetryRecoversTheExactTargetMinuteInsteadOfUsingCurrentQuote(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 28, 10, 3, 0, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.AddDate(0, 0, -1), StartedAt: now.AddDate(0, 0, -1), EvidenceCutoffAt: now.AddDate(0, 0, -1), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1, OnTime: true}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "test", SignalAt: now.AddDate(0, 0, -1), FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", TargetBuyAt: now.AddDate(0, 0, -1)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	buyCost := research.CalculateBuyCost(10, 100)
	sellAt := now.Add(-3 * time.Minute)
	if err := repository.RecordBuy(context.Background(), item.RecommendationID, Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: now.AddDate(0, 0, -1), MarketPrice: 10, ExecutionPrice: buyCost.ExecutionPrice, Quantity: 100, Commission: buyCost.Commission, TransferFee: buyCost.TransferFee, SlippageAmount: buyCost.SlippageAmount, NetCashFlow: buyCost.NetCashFlow}, sellAt); err != nil {
		t.Fatal(err)
	}
	market := &recordingMarket{prices: map[string]float64{"sh600000": 10.5}}
	if err := NewTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(market.currentFlags) != 1 || market.currentFlags[0] {
		t.Fatalf("expected sell retry to request the fixed target minute, got %v", market.currentFlags)
	}
	detail, err := repository.GetRecommendation(context.Background(), item.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Recommendation.Status != "closed" || len(detail.Trades) != 2 {
		t.Fatalf("unexpected recovered sell: %+v", detail)
	}
	sell := detail.Trades[1]
	if sell.ExecutionMode != "recovered_target_minute" || sell.PriceSource != "test" || !sell.TradedAt.Equal(sellAt) {
		t.Fatalf("unexpected recovered sell provenance: %+v", sell)
	}
}

func TestSellRecoveryStillRunsAfterMarketClose(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 28, 15, 5, 0, 0, loc)
	targetSell := time.Date(2026, 8, 28, 10, 0, 0, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.AddDate(0, 0, -1), StartedAt: now.AddDate(0, 0, -1), EvidenceCutoffAt: now.AddDate(0, 0, -1), Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "test", SignalAt: now.AddDate(0, 0, -1), FinalScore: 60, ReferencePrice: 10, Status: "buy_pending", TargetBuyAt: now.AddDate(0, 0, -1)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	buyCost := research.CalculateBuyCost(10, 100)
	if err := repository.RecordBuy(context.Background(), item.RecommendationID, Trade{TradeID: uuid.NewString(), RecommendationID: item.RecommendationID, Side: "buy", TradedAt: now.AddDate(0, 0, -1), MarketPrice: 10, ExecutionPrice: buyCost.ExecutionPrice, Quantity: 100, Commission: buyCost.Commission, TransferFee: buyCost.TransferFee, SlippageAmount: buyCost.SlippageAmount, NetCashFlow: buyCost.NetCashFlow}, targetSell); err != nil {
		t.Fatal(err)
	}
	market := &recordingMarket{prices: map[string]float64{"sh600000": 10.5}}
	if err := NewTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetRecommendation(context.Background(), item.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Recommendation.Status != "closed" || len(detail.Trades) != 2 || detail.Trades[1].ExecutionMode != "recovered_target_minute" {
		t.Fatalf("after-close sell was not recovered: %+v", detail)
	}
}

func TestTrailing5BuyIgnoresLegacyPriceRange(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 10, 3, 50, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-13 * time.Minute), StartedAt: now.Add(-13 * time.Minute), EvidenceCutoffAt: now.Add(-13 * time.Minute), StrategyVersion: "research2-trailing5-v3", Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "test", SignalAt: now.Add(-time.Minute), FinalScore: 60, ReferencePrice: 10, BuyLower: 9, BuyUpper: 11, Status: "buy_pending", Late: true, TargetBuyAt: now.Add(-5 * time.Second)}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	market := &recordingMarket{prices: map[string]float64{"sh600000": 20}}
	if err := NewTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetRecommendation(context.Background(), item.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Recommendation.Status != "active" || detail.Recommendation.BuyMarketPrice != 20 || len(detail.Trades) != 1 || detail.Trades[0].ExecutionMode != "live_after_signal" {
		t.Fatalf("price range still blocked v3 buy: %+v", detail)
	}
}

func TestTrailing5BuyWaitsForQuoteAtOrAfterSignal(t *testing.T) {
	repository := research2TestRepository(t)
	loc := shanghai()
	now := time.Date(2026, 8, 27, 10, 3, 50, 0, loc)
	run := AnalysisRun{RunID: uuid.NewString(), TradingDate: "2026-08-27", ScheduledFor: now.Add(-13 * time.Minute), StartedAt: now.Add(-13 * time.Minute), EvidenceCutoffAt: now.Add(-13 * time.Minute), StrategyVersion: "research2-trailing5-v3", Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1}
	if err := repository.CreateRun(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	signal := now.Add(-5 * time.Second)
	item := Recommendation{RecommendationID: uuid.NewString(), AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "test", SignalAt: signal, FinalScore: 60, ReferencePrice: 10, BuyLower: 0, BuyUpper: 0, Status: "buy_pending", Late: true, TargetBuyAt: signal}
	if err := repository.CreateRecommendations(context.Background(), []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
	market := timestampMarket{price: 10, at: signal.Add(-time.Second)}
	if err := NewTradingService(repository, market, testCalendar{}).ProcessDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetRecommendation(context.Background(), item.RecommendationID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Recommendation.Status != "buy_pending" || detail.Recommendation.BuyAt != nil || len(detail.Trades) != 0 {
		t.Fatalf("stale pre-signal quote was executed: %+v", detail)
	}
}
