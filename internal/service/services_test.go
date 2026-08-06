package service

import (
	"context"
	"encoding/json"
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
	policy        MarketSummaryRecommendationCountPolicy
	prepared      string
	prepareStats  MarketSummaryReportPrepareStats
	review        string
	merged        string
	mergeStats    MarketSummaryReportMergeStats
}

type recordingRecommendOperations struct {
	RecommendOperations
	gateVersion       string
	created           *models.AIResponseResult
	persisted         *models.AIResponseResult
	decision          MarketSummaryDecisionSnapshot
	providerName      string
	modelName         string
	publicationResult *models.MarketSummaryRecommendSaveResult
}

func (o *recordingRecommendOperations) RequireStrategyLive(_ context.Context, version string) error {
	o.gateVersion = version
	return nil
}

func (o *recordingRecommendOperations) CreateAIResponseReport(_ context.Context, result *models.AIResponseResult) error {
	o.created = result
	return nil
}

func (o *recordingRecommendOperations) PersistAIResponseReport(_ context.Context, result *models.AIResponseResult) error {
	o.persisted = result
	return nil
}

func (o *recordingRecommendOperations) PersistMarketSummaryV150Decision(_ context.Context, decision MarketSummaryDecisionSnapshot, providerName, modelName string) (*models.MarketSummaryRecommendSaveResult, error) {
	o.decision = decision
	o.providerName = providerName
	o.modelName = modelName
	return o.publicationResult, nil
}

type recordingDecisionSnapshot struct{ version string }

func (d recordingDecisionSnapshot) MarketSummaryDecisionVersion() string { return d.version }

func (o *recordingAIOperations) TestAIConfig(_ context.Context, configID int) *models.AIModelTestResult {
	o.configID = configID
	return o.result
}

func (o *recordingAIOperations) HumanizeMarketSummaryReport(raw string) string {
	o.humanizeInput = raw
	return o.humanized
}

func (o *recordingAIOperations) ResolveMarketSummaryRecommendationCountPolicy(string) MarketSummaryRecommendationCountPolicy {
	return o.policy
}

func (o *recordingAIOperations) PrepareMarketSummaryReportForPersistence(string, time.Time, int) (string, MarketSummaryReportPrepareStats, error) {
	return o.prepared, o.prepareStats, nil
}

func (o *recordingAIOperations) RunMorningOpeningReview(time.Time) (string, error) {
	return o.review, nil
}

func (o *recordingAIOperations) MergeMarketSummarySupplementReport(string, string, []string, int) (string, MarketSummaryReportMergeStats) {
	return o.merged, o.mergeStats
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

func TestAIServiceDelegatesMarketSummaryPresentationThroughInjectedPort(t *testing.T) {
	operations := &recordingAIOperations{
		policy:       MarketSummaryRecommendationCountPolicy{MaximumOutput: 12, ProductionTarget: 4},
		prepared:     "prepared",
		prepareStats: MarketSummaryReportPrepareStats{RowsSeen: 3, OutputRowsOmit: 1},
		review:       "opening review",
		merged:       "merged",
		mergeStats:   MarketSummaryReportMergeStats{VisibleCodes: []string{"000001"}},
	}
	service := NewAIService(operations)
	if policy := service.ResolveMarketSummaryRecommendationCountPolicy("recommend"); policy != operations.policy {
		t.Fatalf("policy = %#v", policy)
	}
	prepared, stats, err := service.PrepareMarketSummaryReportForPersistence("raw", time.Unix(0, 0), 12)
	if err != nil || prepared != "prepared" || stats != operations.prepareStats {
		t.Fatalf("prepared=%q stats=%#v err=%v", prepared, stats, err)
	}
	if review, err := service.RunMorningOpeningReview(time.Unix(0, 0)); err != nil || review != "opening review" {
		t.Fatalf("review=%q err=%v", review, err)
	}
	merged, mergeStats := service.MergeMarketSummarySupplementReport("base", "supplement", []string{"000001"}, 12)
	if merged != "merged" || len(mergeStats.VisibleCodes) != 1 || mergeStats.VisibleCodes[0] != "000001" {
		t.Fatalf("merged=%q stats=%#v", merged, mergeStats)
	}
}

func TestDecodeMarketSummaryRuntimeEnvelopes(t *testing.T) {
	route, err := DecodeMarketSummaryRouteLog(map[string]any{
		"runSlot": "close", "indicatorCandidateCount": 18, "indicatorAIInputCount": 12,
		"discoveryCandidateCount": 9, "verifiedCandidateCount": 3, "notes": []string{"fresh"},
	})
	if err != nil || route.RunSlot != "close" || route.VerifiedCandidateCt != 3 || len(route.Notes) != 1 {
		t.Fatalf("route=%#v err=%v", route, err)
	}

	decision, err := DecodeMarketSummaryV150DecisionEnvelope(map[string]any{
		"runContext": map[string]any{"runId": "run-1", "strategyVersion": "1.5.0"},
		"candidates": []map[string]any{{"symbol": "000001.SZ"}, {"symbol": "600000.SH"}},
		"production": []map[string]any{{"symbol": "000001.SZ"}}, "noTradeReason": "",
	})
	if err != nil || decision.RunID != "run-1" || decision.MarketSummaryDecisionVersion() != "1.5.0" || decision.CandidateCount != 2 || decision.ProductionCount != 1 || len(decision.RawJSON) == 0 {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestDecodeMarketSummaryV150DecisionEnvelopeRejectsMalformedOrIncompletePayload(t *testing.T) {
	for _, raw := range []any{
		nil,
		map[string]any{"runContext": map[string]any{"strategyVersion": "1.5.0"}},
		map[string]any{"runContext": map[string]any{"runId": "run-1"}},
		json.RawMessage(`{`),
	} {
		if decision, err := DecodeMarketSummaryV150DecisionEnvelope(raw); err == nil || decision != nil {
			t.Fatalf("raw=%#v decision=%#v err=%v, want rejection", raw, decision, err)
		}
	}
}

func TestRecommendServiceDelegatesMarketSummaryPublicationPorts(t *testing.T) {
	result := &models.AIResponseResult{}
	publication := &models.MarketSummaryRecommendSaveResult{SavedCount: 2}
	operations := &recordingRecommendOperations{publicationResult: publication}
	service := NewRecommendService(operations)
	ctx := context.WithValue(context.Background(), struct{}{}, "summary")
	decision := recordingDecisionSnapshot{version: "1.5.0"}

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
	if err := service.PersistAIResponseReport(ctx, result); err != nil {
		t.Fatalf("PersistAIResponseReport: %v", err)
	}
	if operations.persisted != result {
		t.Fatal("persist report was not delegated")
	}
	got, err := service.PersistMarketSummaryV150Decision(ctx, decision, "provider", "model")
	if err != nil {
		t.Fatalf("PersistMarketSummaryV150Decision: %v", err)
	}
	if got != publication || operations.decision != decision || operations.providerName != "provider" || operations.modelName != "model" {
		t.Fatalf("publication delegation mismatch: result=%p decision=%#v provider=%q model=%q", got, operations.decision, operations.providerName, operations.modelName)
	}
}
