package data

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-stock/backend/strategy/v150"
)

func TestApplyMarketSummaryV150EventModelResponseDegradesOnlyInvalidCandidate(t *testing.T) {
	now := time.Date(2026, 8, 5, 10, 30, 0, 0, cnLocation())
	run := &MarketSummaryV150RunSnapshot{
		RunContext:          v150.RunContext{AsOf: now, DataCutoffAt: now, StrategyVersion: v150.StrategyVersion},
		VerificationSymbols: []string{"600000.SH", "000001.SZ"},
		Candidates: []MarketSummaryV150CandidateSnapshot{
			{Candidate: v150.Candidate{Symbol: "600000.SH", Signals: v150.ScoreSignals{TrendRelativeStrength: 1}}, Rank: 1},
			{Candidate: v150.Candidate{Symbol: "000001.SZ", Signals: v150.ScoreSignals{TrendRelativeStrength: 1, EventStrength: 1}, EventAt: &now}, Rank: 2},
		},
	}
	request := marketSummaryV150EventModelRequest{Candidates: []marketSummaryV150EventModelCandidate{
		{Symbol: "600000.SH", SourceAt: map[string]MarketSummaryV150EvidenceTiming{"e-a": {EvidenceID: "e-a", SourceAt: now.Add(-time.Hour), AvailableAt: now}}},
		{Symbol: "000001.SZ", SourceAt: map[string]MarketSummaryV150EvidenceTiming{"e-b": {EvidenceID: "e-b", SourceAt: now.Add(-time.Hour), AvailableAt: now}}},
	}}
	content := `{"assessments":[
		{"symbol":"600000.SH","direction":"positive","relevance":1,"importance":0.8,"confidence":0.9,"evidenceIds":["e-a"]},
		{"symbol":"000001.SZ","direction":"positive","relevance":1.2,"importance":0.8,"confidence":0.9,"evidenceIds":["e-b"]}
	]}`
	if err := applyMarketSummaryV150EventModelResponse(run, request, content, "model-x"); err != nil {
		t.Fatal(err)
	}
	if run.Candidates[0].Candidate.Signals.EventStrength <= 0 || run.Candidates[0].Source.EventAssessment.Verifier != "model-x" {
		t.Fatalf("valid candidate assessment not applied: %+v", run.Candidates[0])
	}
	if run.Candidates[1].Candidate.Signals.EventStrength != 0 || run.Candidates[1].Candidate.EventAt != nil {
		t.Fatalf("invalid candidate did not degrade independently: %+v", run.Candidates[1])
	}
	if run.Candidates[0].Rank != 1 || run.Candidates[1].Rank != 2 {
		t.Fatalf("model response changed deterministic order: %+v", run.Candidates)
	}
	found := false
	for _, warning := range run.Warnings {
		if strings.Contains(warning, "000001.SZ:invalid_model_assessment") {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidate-local degradation warning missing: %v", run.Warnings)
	}
}

func TestFinalizeMarketSummaryV150ReranksVerifiedScoresWithoutChangingTopSet(t *testing.T) {
	loc := cnLocation()
	startedAt := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	cutoff := startedAt.Add(time.Minute)
	highBefore := marketSummaryV150TestCandidate("000001.SZ", "bank", 1, cutoff)
	highBefore.Signals.EventStrength = 1
	lowBefore := marketSummaryV150TestCandidate("000002.SZ", "bank", 0.95, cutoff)
	lowBefore.EventAt = nil
	lowBefore.Signals.EventStrength = 0
	evidenceAt := cutoff.Add(-time.Hour)
	evidence := MarketSummaryV150EvidenceTiming{
		EvidenceID: "event-2", EvidenceType: "news", SourceAt: evidenceAt, AvailableAt: cutoff,
	}
	sources := map[string]MarketSummaryV150SourceCandidate{
		highBefore.Symbol: marketSummaryV150TestSource(highBefore, cutoff),
		lowBefore.Symbol:  marketSummaryV150TestSource(lowBefore, cutoff),
	}
	sources[lowBefore.Symbol] = func(value MarketSummaryV150SourceCandidate) MarketSummaryV150SourceCandidate {
		value.EventEvidence = []MarketSummaryV150EvidenceTiming{evidence}
		return value
	}(sources[lowBefore.Symbol])
	run, err := newMarketSummaryV150Run(startedAt, cutoff, "10:00", marketSummaryV150TestBenchmark(false), []v150.Candidate{lowBefore, highBefore}, sources)
	if err != nil {
		t.Fatal(err)
	}
	if got := run.Candidates[0].Candidate.Symbol; got != highBefore.Symbol {
		t.Fatalf("pre-verification leader=%s, want %s", got, highBefore.Symbol)
	}
	applyMarketSummaryV150EventAssessment(run, marketSummaryV150EventModelAssessment{
		Symbol: highBefore.Symbol, Direction: "neutral", Relevance: 0, Importance: 0, Confidence: 1,
	}, nil, "model-x")
	applyMarketSummaryV150EventAssessment(run, marketSummaryV150EventModelAssessment{
		Symbol: lowBefore.Symbol, Direction: "positive", Relevance: 1, Importance: 1, Confidence: 1, EvidenceIDs: []string{evidence.EvidenceID},
	}, map[string]MarketSummaryV150EvidenceTiming{evidence.EvidenceID: evidence}, "model-x")
	verified := []marketSummaryVerifiedCandidate{{StockCode: highBefore.Symbol}, {StockCode: lowBefore.Symbol}}
	if err := finalizeMarketSummaryV150Run(run, verified, v150.PortfolioState{}, cutoff.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.Candidates[0].Candidate.Symbol != lowBefore.Symbol || run.Candidates[0].PreVerificationRank != 2 || run.Candidates[0].FinalRank != 1 || run.Candidates[0].Rank != 1 {
		t.Fatalf("final deterministic ranks were not frozen: %+v", run.Candidates)
	}
	if len(run.Production) != 1 || run.Production[0].Symbol != lowBefore.Symbol {
		t.Fatalf("lower final score reserved the same-sector slot: %+v", run.Production)
	}
	if got, want := run.VerificationSymbols, []string{highBefore.Symbol, lowBefore.Symbol}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top verification membership/order changed: got=%v want=%v", got, want)
	}
}

func TestVerifyMarketSummaryV150EventsUsesOnlyStructuredEventFields(t *testing.T) {
	now := time.Now().In(cnLocation())
	run := &MarketSummaryV150RunSnapshot{
		RunContext:          v150.RunContext{AsOf: now, DataCutoffAt: now, StrategyVersion: v150.StrategyVersion},
		VerificationSymbols: []string{"600000.SH"},
		Candidates:          []MarketSummaryV150CandidateSnapshot{{Candidate: v150.Candidate{Symbol: "600000.SH"}, Rank: 1}},
	}
	verified := []marketSummaryVerifiedCandidate{{
		StockCode:  "600000.SH",
		VerifiedAt: now,
		EvidenceSources: []aiEvidenceReference{{
			Title: "公司公告", Summary: "签署订单", SourceName: "exchange", PublishedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), RawHash: "evidence-1",
		}},
	}}
	original := completeMarketSummaryV150EventModel
	t.Cleanup(func() { completeMarketSummaryV150EventModel = original })
	completeMarketSummaryV150EventModel = func(_ *OpenAi, messages []map[string]any) (string, string, string, error) {
		if len(messages) != 2 {
			t.Fatalf("messages=%d, want 2", len(messages))
		}
		body, _ := messages[1]["content"].(string)
		if strings.Contains(body, "targetPrice") || strings.Contains(body, "executionState") {
			t.Fatalf("forbidden decision fields leaked into event request: %s", body)
		}
		return `{"assessments":[{"symbol":"600000.SH","direction":"positive","relevance":1,"importance":1,"confidence":0.8,"evidenceIds":["evidence-1"]}]}`, "chat", "model-x", nil
	}
	o := &OpenAi{ProviderName: "provider", Model: "configured-model", ApiProtocol: AIAPIProtocolChatCompletions}
	if err := o.verifyMarketSummaryV150Events(run, verified); err != nil {
		t.Fatal(err)
	}
	if run.Candidates[0].Candidate.Signals.EventStrength != 0.8 {
		t.Fatalf("event strength=%v, want 0.8", run.Candidates[0].Candidate.Signals.EventStrength)
	}
	if len(run.ModelHash) != 64 || len(run.PromptHash) != 64 {
		t.Fatalf("model/prompt hashes not frozen: %q %q", run.ModelHash, run.PromptHash)
	}
}

func TestVerifyMarketSummaryV150EventsBatchFailureCanDegradeWithoutStoppingRun(t *testing.T) {
	now := time.Now().In(cnLocation())
	run := &MarketSummaryV150RunSnapshot{
		RunContext:          v150.RunContext{AsOf: now, DataCutoffAt: now, StrategyVersion: v150.StrategyVersion},
		VerificationSymbols: []string{"600000.SH"},
		Candidates: []MarketSummaryV150CandidateSnapshot{{Candidate: v150.Candidate{
			Symbol: "600000.SH", EventAt: &now, Signals: v150.ScoreSignals{EventStrength: 1},
		}}},
	}
	original := completeMarketSummaryV150EventModel
	t.Cleanup(func() { completeMarketSummaryV150EventModel = original })
	completeMarketSummaryV150EventModel = func(_ *OpenAi, _ []map[string]any) (string, string, string, error) {
		return "", "", "", errors.New("provider unavailable")
	}
	err := (&OpenAi{Model: "model-x"}).verifyMarketSummaryV150Events(run, []marketSummaryVerifiedCandidate{{StockCode: "600000.SH"}})
	if err != nil {
		// runMarketSummaryV150Phase owns the batch-level downgrade so that this
		// helper remains explicit about provider failure.
		degradeMarketSummaryV150EventCandidates(run, run.VerificationSymbols, "model_batch_failed:"+err.Error())
	}
	if run.Candidates[0].Candidate.EventAt != nil || run.Candidates[0].Candidate.Signals.EventStrength != 0 {
		t.Fatalf("batch failure did not produce event-score zero: %+v", run.Candidates[0])
	}
}

func TestApplyMarketSummaryV150EventModelResponseRejectsDuplicateAndMissingFieldsPerCandidate(t *testing.T) {
	now := time.Date(2026, 8, 5, 10, 30, 0, 0, cnLocation())
	run := &MarketSummaryV150RunSnapshot{
		RunContext:          v150.RunContext{AsOf: now, DataCutoffAt: now, StrategyVersion: v150.StrategyVersion},
		VerificationSymbols: []string{"600000.SH", "000001.SZ"},
		Candidates: []MarketSummaryV150CandidateSnapshot{
			{Candidate: v150.Candidate{Symbol: "600000.SH", EventAt: &now, Signals: v150.ScoreSignals{EventStrength: 1}}},
			{Candidate: v150.Candidate{Symbol: "000001.SZ", EventAt: &now, Signals: v150.ScoreSignals{EventStrength: 1}}},
		},
	}
	request := marketSummaryV150EventModelRequest{Candidates: []marketSummaryV150EventModelCandidate{
		{Symbol: "600000.SH", SourceAt: map[string]MarketSummaryV150EvidenceTiming{}},
		{Symbol: "000001.SZ", SourceAt: map[string]MarketSummaryV150EvidenceTiming{}},
	}}
	content := `{"assessments":[
		{"symbol":"600000.SH","direction":"neutral","relevance":0,"importance":0,"confidence":0,"evidenceIds":[]},
		{"symbol":"600000.SH","direction":"neutral","relevance":0,"importance":0,"confidence":0,"evidenceIds":[]},
		{"symbol":"600000.SH","direction":"neutral","relevance":0,"importance":0,"confidence":0,"evidenceIds":[]},
		{"symbol":"000001.SZ","direction":"neutral","relevance":0,"importance":0,"confidence":0}
	]}`
	if err := applyMarketSummaryV150EventModelResponse(run, request, content, "model-x"); err != nil {
		t.Fatal(err)
	}
	for _, row := range run.Candidates {
		if row.Candidate.EventAt != nil || row.Candidate.Signals.EventStrength != 0 || row.Source.EventAssessment.Verifier != "model_degraded" {
			t.Fatalf("malformed assessment did not degrade %s independently: %+v", row.Candidate.Symbol, row)
		}
	}
}

func TestApplyMarketSummaryV150EventModelResponseRejectsEnvelopeFields(t *testing.T) {
	run := &MarketSummaryV150RunSnapshot{}
	err := applyMarketSummaryV150EventModelResponse(run, marketSummaryV150EventModelRequest{}, `{"assessments":[],"ranking":[]}`, "model-x")
	if err == nil || !strings.Contains(err.Error(), "only assessments") {
		t.Fatalf("error=%v, want strict envelope rejection", err)
	}
}

func TestApplyMarketSummaryV150NewsEventGateFailsClosedWithoutStoppingTechnicalRun(t *testing.T) {
	now := time.Date(2026, 8, 5, 10, 30, 0, 0, cnLocation())
	run := &MarketSummaryV150RunSnapshot{
		Regime:              v150.RegimeDecision{Regime: v150.RegimeNeutral},
		VerificationSymbols: []string{"600000.SH"},
		Candidates: []MarketSummaryV150CandidateSnapshot{{Candidate: v150.Candidate{
			Symbol: "600000.SH", EventAt: &now, Signals: v150.ScoreSignals{TrendRelativeStrength: 1, EventStrength: 1},
		}}},
	}
	allowed, reason := applyMarketSummaryV150NewsEventGate(run, NewsWindowStatusFailed)
	if allowed || reason != "news_failed" {
		t.Fatalf("allowed=%v reason=%q", allowed, reason)
	}
	row := run.Candidates[0]
	if row.Candidate.EventAt != nil || row.Candidate.Signals.EventStrength != 0 || row.Candidate.Signals.TrendRelativeStrength != 1 {
		t.Fatalf("event gate must preserve technical inputs and zero only event data: %+v", row.Candidate)
	}
	if len(run.ModelHash) != 64 || len(run.PromptHash) != 64 {
		t.Fatalf("skipped model provenance not frozen: %q %q", run.ModelHash, run.PromptHash)
	}
	if allowed, reason := applyMarketSummaryV150NewsEventGate(run, NewsWindowStatusOK); !allowed || reason != "" {
		t.Fatalf("ok news window unexpectedly rejected: allowed=%v reason=%q", allowed, reason)
	}
}
