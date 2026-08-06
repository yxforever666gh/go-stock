package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"
	"go-stock/internal/releaseinfo"
	"go-stock/internal/service"
)

type blockingSummaryRecommendOperations struct {
	service.RecommendOperations
	version string
	err     error
}

func (o *blockingSummaryRecommendOperations) RequireStrategyLive(_ context.Context, version string) error {
	o.version = version
	return o.err
}

type recordingAppMarketSummaryV150Producer struct {
	calls   int
	ctx     context.Context
	request service.MarketSummaryV150ProductionRequest
	result  *service.MarketSummaryV150ProductionResult
	err     error
}

func (p *recordingAppMarketSummaryV150Producer) Produce(
	ctx context.Context,
	request service.MarketSummaryV150ProductionRequest,
) (*service.MarketSummaryV150ProductionResult, error) {
	p.calls++
	p.ctx = ctx
	p.request = request
	return p.result, p.err
}

type failOnLegacyPhasedSummaryOperations struct {
	service.AIOperations
	t           *testing.T
	phasedCalls int
}

func (o *failOnLegacyPhasedSummaryOperations) NewSummaryStockNewsStreamPhased(
	context.Context,
	int,
	string,
	*int,
	bool,
) <-chan map[string]any {
	o.phasedCalls++
	o.t.Fatal("V1.5 routed through the legacy phased AI stream")
	ch := make(chan map[string]any)
	close(ch)
	return ch
}

func (*failOnLegacyPhasedSummaryOperations) HumanizeMarketSummaryReport(raw string) string {
	return raw
}

type recordingAppRecommendationPublisher struct {
	calls int
}

func (p *recordingAppRecommendationPublisher) PublishDecision(
	context.Context,
	recommendation.FrozenDecision,
	string,
	string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	p.calls++
	return nil, errors.New("App must not publish an already-published V1.5 decision")
}

type recordingAppSummaryDeliveryOperations struct {
	service.RecommendOperations
	report     *models.AIResponseResult
	diagnostic *models.MarketSummaryRunDiagnostic
}

func (o *recordingAppSummaryDeliveryOperations) CreateAIResponseReport(_ context.Context, report *models.AIResponseResult) error {
	o.report = report
	return nil
}

func (*recordingAppSummaryDeliveryOperations) EncodeMarketSummaryBlockedReasons([]models.MarketSummaryBlockedReasonItem) string {
	return "[]"
}

func (o *recordingAppSummaryDeliveryOperations) SaveMarketSummaryRunDiagnostic(item *models.MarketSummaryRunDiagnostic) error {
	o.diagnostic = item
	return nil
}

type emptyAppSummaryConfigOperations struct {
	service.ConfigOperations
}

func (*emptyAppSummaryConfigOperations) GetConfig() *models.SettingConfig { return nil }

func TestV150SummaryResultRequiresFrozenBackendDecision(t *testing.T) {
	if !marketSummaryRequiresV150Backend() {
		t.Fatalf("current summary version = %s, want 1.5.0 backend enforcement", releaseinfo.Manifest().CurrentStrategyVersion)
	}
	plain := summaryRunResult{text: "legacy markdown"}
	if usableMarketSummaryRunResult(plain) {
		t.Fatal("plain model text bypassed the V1.5 backend decision")
	}
	rejected := rejectMissingV150BackendResult(plain)
	if len(rejected.errs) != 1 || rejected.errs[0] != marketSummaryV150BackendMissingReason {
		t.Fatalf("missing-backend errors = %v", rejected.errs)
	}

	valid := summaryRunResult{
		text: "presentation",
		v150Production: &service.MarketSummaryV150ProductionResult{
			RunID: "v150-run", StrategyVersion: v150.StrategyVersion,
		},
	}
	if !usableMarketSummaryRunResult(valid) {
		t.Fatal("frozen V1.5 backend decision was rejected")
	}
	valid.text = ""
	if !usableMarketSummaryRunResult(valid) {
		t.Fatal("structured V1.5 no_trade run was rejected because presentation text was empty")
	}

	wrongVersion := valid
	wrongVersion.v150Production = &service.MarketSummaryV150ProductionResult{RunID: "wrong-version", StrategyVersion: "1.4.2"}
	if usableMarketSummaryRunResult(wrongVersion) {
		t.Fatal("wrong-version backend decision was accepted")
	}
}

func TestV150SummaryFailoverDoesNotStopAtPlainText(t *testing.T) {
	if !shouldSummaryFailover(summaryRunResult{text: "plain markdown"}) {
		t.Fatal("V1.5 failover stopped at a plain-text result without a frozen run")
	}
	complete := summaryRunResult{
		text: "presentation",
		errs: []string{"post-publication delivery warning"},
		v150Production: &service.MarketSummaryV150ProductionResult{
			RunID: "v150-run", StrategyVersion: v150.StrategyVersion,
		},
	}
	if shouldSummaryFailover(complete) {
		t.Fatal("complete V1.5 result unexpectedly requested failover")
	}
}

func TestV150SummaryUsesTypedProducerAndNeverLegacyPhasedAI(t *testing.T) {
	startedAt := time.Date(2026, 8, 7, 9, 40, 0, 0, time.FixedZone("CST", 8*60*60))
	sysPromptID := 9
	want := &service.MarketSummaryV150ProductionResult{
		RunID:           "typed-production-run",
		StrategyVersion: v150.StrategyVersion,
		ReportText:      "published report",
		ProviderName:    "provider",
		ModelName:       "model",
		CandidateCount:  18,
		ProductionCount: 1,
		SaveResult:      &models.MarketSummaryRecommendSaveResult{SavedCount: 1, ProductionCount: 1},
	}
	producer := &recordingAppMarketSummaryV150Producer{result: want}
	legacyAI := &failOnLegacyPhasedSummaryOperations{t: t}
	app := &App{
		ctx: context.Background(),
		services: service.AppServices{
			AI: service.NewAIService(legacyAI),
			Recommend: service.NewRecommendService(
				&blockingSummaryRecommendOperations{}, nil, nil, nil, v150.StrategyVersion, producer,
			),
		},
	}

	got := app.runSummaryWithFallback(7, "question", &sysPromptID, true, true, startedAt)
	if producer.calls != 1 {
		t.Fatalf("typed producer calls = %d, want 1", producer.calls)
	}
	if legacyAI.phasedCalls != 0 {
		t.Fatalf("legacy phased AI calls = %d, want 0", legacyAI.phasedCalls)
	}
	if producer.ctx != app.ctx || producer.request.AIConfigID != 7 || producer.request.Question != "question" ||
		producer.request.SysPromptID != &sysPromptID || !producer.request.Think || !producer.request.StartedAt.Equal(startedAt) {
		t.Fatalf("typed production request changed: %+v", producer.request)
	}
	if got.v150Production != want || got.text != want.ReportText || got.chatID != want.RunID || got.modelName != want.ModelName {
		t.Fatalf("typed production result changed: %+v", got)
	}
	if !usableMarketSummaryRunResult(got) || shouldSummaryFailover(got) {
		t.Fatal("published typed result was not treated as the terminal production fact")
	}
}

func TestV150PublishedResultHasDeliverySideEffectsWithoutRepublishing(t *testing.T) {
	startedAt := time.Date(2026, 8, 7, 9, 40, 0, 0, time.FixedZone("CST", 8*60*60))
	publisher := &recordingAppRecommendationPublisher{}
	delivery := &recordingAppSummaryDeliveryOperations{}
	ai := &failOnLegacyPhasedSummaryOperations{t: t}
	production := &service.MarketSummaryV150ProductionResult{
		RunID:           "already-published-run",
		StrategyVersion: v150.StrategyVersion,
		ReportText:      "published report",
		ProviderName:    "provider",
		ModelName:       "model",
		CandidateCount:  4,
		ProductionCount: 1,
		SaveResult:      &models.MarketSummaryRecommendSaveResult{SavedCount: 1, ProductionCount: 1},
	}
	app := &App{
		ctx: context.Background(),
		services: service.AppServices{
			AI:     service.NewAIService(ai),
			Config: service.NewConfigService(&emptyAppSummaryConfigOperations{}),
			Recommend: service.NewRecommendService(
				delivery, publisher, nil, nil, v150.StrategyVersion,
			),
		},
	}
	res := summaryRunResult{
		aiConfigId: 1, text: production.ReportText, chatID: production.RunID,
		modelName: production.ModelName, finalQuestion: "question", v150Production: production,
	}

	app.persistSummaryRunResult(res, startedAt)
	if publisher.calls != 0 {
		t.Fatalf("App publisher calls after typed production = %d, want 0", publisher.calls)
	}
	if delivery.report == nil || delivery.report.ChatId != production.RunID || delivery.report.Content != production.ReportText {
		t.Fatalf("delivery report = %+v", delivery.report)
	}
	if delivery.diagnostic == nil || delivery.diagnostic.SavedCount != production.SaveResult.SavedCount {
		t.Fatalf("delivery diagnostic = %+v", delivery.diagnostic)
	}
}

func TestV150AppSourceCannotCallDecisionPublisher(t *testing.T) {
	source, err := os.ReadFile("app_summary_runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), ".PersistMarketSummaryV150Decision(") {
		t.Fatal("App delivery source can still publish a V1.5 decision")
	}
	if strings.Contains(string(source), "DecodeMarketSummaryV150DecisionEnvelope") || strings.Contains(string(source), `msg["v150Run"]`) {
		t.Fatal("App delivery source still restores an opaque V1.5 decision payload")
	}
}

func TestSummaryProductionUsesInjectedStrategyRuntimeGate(t *testing.T) {
	gateErr := errors.New("strategy paused by test")
	operations := &blockingSummaryRecommendOperations{err: gateErr}
	app := &App{
		ctx: context.Background(),
		services: service.AppServices{
			Recommend: service.NewRecommendService(operations, nil, nil, nil, ""),
		},
	}

	result := app.runSummaryStockNewsTask("summary", 1, nil, true, false)
	if operations.version != v150.StrategyVersion {
		t.Fatalf("gate version = %q, want %q", operations.version, v150.StrategyVersion)
	}
	if len(result.errs) != 1 || result.errs[0] != gateErr.Error() {
		t.Fatalf("blocked result errors = %v", result.errs)
	}
}
