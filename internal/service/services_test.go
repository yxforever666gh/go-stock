package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/models"
)

type recordingStockOperations struct {
	StockOperations
	followedCode string
}

func (o *recordingStockOperations) Follow(code string) string {
	o.followedCode = code
	return "followed"
}

type recordingConfigOperations struct {
	ConfigOperations
	config *models.SettingConfig
}

type recordingAIOperations struct {
	AIOperations
	configID      int
	result        *models.AIModelTestResult
	humanizeInput string
	humanized     string
}

type recordingRecommendOperations struct {
	RecommendOperations
	gateVersion string
	gateErr     error
	created     *models.AIResponseResult
}

type recordingMarketSummaryV150Producer struct {
	calls   int
	context context.Context
	request MarketSummaryV150ProductionRequest
	result  *MarketSummaryV150ProductionResult
	err     error
}

func (p *recordingMarketSummaryV150Producer) Produce(
	ctx context.Context,
	request MarketSummaryV150ProductionRequest,
) (*MarketSummaryV150ProductionResult, error) {
	p.calls++
	p.context = ctx
	p.request = request
	return p.result, p.err
}

func (o *recordingRecommendOperations) RequireStrategyLive(_ context.Context, version string) error {
	o.gateVersion = version
	return o.gateErr
}

func (o *recordingRecommendOperations) CreateAIResponseReport(_ context.Context, result *models.AIResponseResult) error {
	o.created = result
	return nil
}

func (o *recordingAIOperations) TestAIConfig(_ context.Context, configID int) *models.AIModelTestResult {
	o.configID = configID
	return o.result
}

func (o *recordingAIOperations) HumanizeMarketSummaryReport(raw string) string {
	o.humanizeInput = raw
	return o.humanized
}

func (o *recordingConfigOperations) GetConfig() *models.SettingConfig {
	return o.config
}

func TestServicesDelegateToInjectedConsumerPorts(t *testing.T) {
	stockOperations := &recordingStockOperations{}
	stockService := NewStockService(stockOperations)
	if got := stockService.Follow("600000.SH"); got != "followed" {
		t.Fatalf("Follow() = %q, want followed", got)
	}
	if stockOperations.followedCode != "600000.SH" {
		t.Fatalf("delegated stock code = %q", stockOperations.followedCode)
	}

	wantConfig := &models.SettingConfig{Settings: &models.Settings{OpenAiEnable: true}}
	configService := NewConfigService(&recordingConfigOperations{config: wantConfig})
	if got := configService.GetConfig(); got != wantConfig {
		t.Fatal("GetConfig() did not return the injected port result")
	}
}

func TestServiceOperationsValidateNamesMissingPort(t *testing.T) {
	err := (ServiceOperations{}).Validate()
	if err == nil || err.Error() != "ai operations are required" {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAIServiceTestsConfigurationThroughInjectedPort(t *testing.T) {
	want := &models.AIModelTestResult{Success: true, Protocol: models.AIAPIProtocolOpenAIResponses}
	operations := &recordingAIOperations{result: want}
	if got := NewAIService(operations).TestAIConfig(context.Background(), 17); got != want {
		t.Fatal("TestAIConfig() did not return the injected port result")
	}
	if operations.configID != 17 {
		t.Fatalf("delegated AI config id = %d, want 17", operations.configID)
	}
}

func TestAIServiceHumanizesMarketSummaryThroughInjectedPort(t *testing.T) {
	operations := &recordingAIOperations{humanized: "clean report"}
	if got := NewAIService(operations).HumanizeMarketSummaryReport("raw report"); got != "clean report" {
		t.Fatalf("HumanizeMarketSummaryReport() = %q", got)
	}
	if operations.humanizeInput != "raw report" {
		t.Fatalf("delegated report = %q", operations.humanizeInput)
	}
}

func TestRecommendServiceDelegatesMarketSummaryReportPorts(t *testing.T) {
	result := &models.AIResponseResult{}
	operations := &recordingRecommendOperations{}
	service := NewRecommendService(operations, nil, nil, "")
	ctx := context.WithValue(context.Background(), struct{}{}, "summary")

	if err := service.RequireStrategyLive(ctx, "1.5.0"); err != nil {
		t.Fatalf("RequireStrategyLive: %v", err)
	}
	if operations.gateVersion != "1.5.0" {
		t.Fatalf("gate version = %q", operations.gateVersion)
	}
	if err := service.CreateAIResponseReport(ctx, result); err != nil {
		t.Fatalf("CreateAIResponseReport: %v", err)
	}
	if operations.created != result {
		t.Fatal("create report was not delegated")
	}
}

func TestRecommendServiceDelegatesTypedMarketSummaryV150Production(t *testing.T) {
	t.Parallel()

	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("run"), "same-context")
	sysPromptID := 19
	request := MarketSummaryV150ProductionRequest{
		AIConfigID:  7,
		Question:    "market summary",
		SysPromptID: &sysPromptID,
		Think:       true,
		StartedAt:   time.Date(2026, 8, 7, 9, 40, 0, 0, time.FixedZone("CST", 8*60*60)),
	}
	saveResult := &models.MarketSummaryRecommendSaveResult{SavedCount: 2}
	wantResult := &MarketSummaryV150ProductionResult{
		RunID:                  "v150-run",
		StrategyVersion:        "1.5.0",
		ReportText:             "typed report",
		ProviderName:           "provider",
		ModelName:              "model",
		CandidateCount:         18,
		VerifiedCandidateCount: 4,
		ProductionCount:        2,
		NoTradeReason:          "",
		RouteLog:               &MarketSummaryRouteLog{RunSlot: "09:40", VerifiedCandidateCt: 4},
		SaveResult:             saveResult,
	}
	wantErr := errors.New("producer returned result with error")
	producer := &recordingMarketSummaryV150Producer{result: wantResult, err: wantErr}
	operations := &recordingRecommendOperations{}
	service := NewRecommendService(operations, nil, nil, "1.5.0", producer)

	gotResult, gotErr := service.RunMarketSummaryV150(ctx, request)

	if producer.calls != 1 {
		t.Fatalf("Produce() calls = %d, want 1", producer.calls)
	}
	if operations.gateVersion != "1.5.0" {
		t.Fatalf("runtime gate version = %q, want 1.5.0", operations.gateVersion)
	}
	if producer.context != ctx {
		t.Fatal("RecommendService replaced the caller context")
	}
	if producer.request != request {
		t.Fatalf("production request = %#v, want %#v", producer.request, request)
	}
	if gotResult != wantResult {
		t.Fatal("RecommendService copied or replaced the typed production result")
	}
	if gotErr != wantErr {
		t.Fatal("RecommendService wrapped or replaced the producer error")
	}
}

func TestRecommendServiceBlocksTypedMarketSummaryV150ProductionWhilePaused(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("strategy is paused")
	operations := &recordingRecommendOperations{gateErr: wantErr}
	producer := &recordingMarketSummaryV150Producer{}
	service := NewRecommendService(operations, nil, nil, "1.5.0", producer)

	result, err := service.RunMarketSummaryV150(context.Background(), MarketSummaryV150ProductionRequest{})

	if result != nil || err != wantErr {
		t.Fatalf("paused result/error = %#v/%v, want nil/%v", result, err, wantErr)
	}
	if producer.calls != 0 {
		t.Fatalf("paused use case called producer %d time(s), want 0", producer.calls)
	}
}

func TestRecommendServiceRejectsMissingMarketSummaryV150Producer(t *testing.T) {
	t.Parallel()

	var typedNil *recordingMarketSummaryV150Producer
	services := []RecommendService{
		NewRecommendService(nil, nil, nil, "1.5.0"),
		NewRecommendService(nil, nil, nil, "1.5.0", typedNil),
	}
	for index, service := range services {
		result, err := service.RunMarketSummaryV150(context.Background(), MarketSummaryV150ProductionRequest{})
		if result != nil || !errors.Is(err, ErrMarketSummaryV150ProducerUnavailable) {
			t.Fatalf("case %d result=%#v error=%v, want producer unavailable", index, result, err)
		}
	}
}

var _ MarketSummaryV150Producer = (*recordingMarketSummaryV150Producer)(nil)
