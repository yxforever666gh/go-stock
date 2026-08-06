package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/strategy/v150"
)

const marketSummaryV150EventModelSystemPrompt = `You verify event evidence for an A-share swing strategy.
Return JSON only. For each supplied symbol, output only: symbol, direction,
relevance, importance, confidence, evidenceIds. direction must be positive,
negative, or neutral; the three numeric fields must be in [0,1]; evidenceIds
must be copied from that symbol's supplied evidence. Do not rank securities,
set prices or targets, select trades, or describe execution state.`

const marketSummaryV150EventModelSchemaPrompt = `Return exactly:
{"assessments":[{"symbol":"600000.SH","direction":"neutral","relevance":0,"importance":0,"confidence":0,"evidenceIds":[]}]}
Return one assessment for every input symbol. No markdown and no extra fields.`

type marketSummaryV150EventModelEvidence struct {
	EvidenceID  string `json:"evidenceId"`
	Title       string `json:"title,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Source      string `json:"source,omitempty"`
	PublishedAt string `json:"publishedAt"`
}

type marketSummaryV150EventModelCandidate struct {
	Symbol   string                                     `json:"symbol"`
	Evidence []marketSummaryV150EventModelEvidence      `json:"evidence"`
	SourceAt map[string]MarketSummaryV150EvidenceTiming `json:"-"`
}

type marketSummaryV150EventModelRequest struct {
	AsOf       string                                 `json:"asOf"`
	Candidates []marketSummaryV150EventModelCandidate `json:"candidates"`
}

type marketSummaryV150EventModelAssessment struct {
	Symbol      string   `json:"symbol"`
	Direction   string   `json:"direction"`
	Relevance   float64  `json:"relevance"`
	Importance  float64  `json:"importance"`
	Confidence  float64  `json:"confidence"`
	EvidenceIDs []string `json:"evidenceIds"`
}

// applyMarketSummaryV150NewsEventGate keeps the technical pipeline live when
// news is unavailable, but makes the event component fail closed. Only an OK
// window may be sent to the event verifier.
func applyMarketSummaryV150NewsEventGate(run *MarketSummaryV150RunSnapshot, status NewsWindowStatus) (bool, string) {
	if run == nil || run.Regime.NoTrade {
		return false, "risk_off"
	}
	if status == NewsWindowStatusOK {
		return true, ""
	}
	statusText := strings.TrimSpace(string(status))
	if statusText == "" {
		statusText = "missing"
	}
	reason := "news_" + statusText
	degradeMarketSummaryV150EventCandidates(run, run.VerificationSymbols, reason)
	run.ModelHash = marketSummaryV150StableHash("event-model-not-invoked:" + reason)
	run.PromptHash = marketSummaryV150StableHash(marketSummaryV150EventModelSystemPrompt + "\n" + marketSummaryV150EventModelSchemaPrompt)
	return false, reason
}

func (o *OpenAi) verifyMarketSummaryV150Events(run *MarketSummaryV150RunSnapshot, verified []marketSummaryVerifiedCandidate) error {
	if run == nil {
		return errors.New("v1.5 event verification run is nil")
	}
	request := buildMarketSummaryV150EventModelRequest(run, verified)
	if len(request.Candidates) == 0 {
		degradeMarketSummaryV150EventCandidates(run, run.VerificationSymbols, "model_evidence_empty")
		return nil
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode event verification request: %w", err)
	}
	messages := []map[string]any{
		{"role": "system", "content": marketSummaryV150EventModelSystemPrompt},
		{"role": "user", "content": marketSummaryV150EventModelSchemaPrompt + "\nInput JSON:\n" + string(payload)},
	}
	content, _, modelName, err := o.completeMarketSummaryV150EventBatch(messages, false)
	run.PromptHash = marketSummaryV150StableHash(marketSummaryV150EventModelSystemPrompt + "\n" + marketSummaryV150EventModelSchemaPrompt)
	modelIdentity := strings.Join([]string{strings.TrimSpace(o.ProviderName), strings.TrimSpace(o.Model), strings.TrimSpace(modelName), NormalizeAIAPIProtocol(o.ApiProtocol)}, "|")
	run.ModelHash = marketSummaryV150StableHash(modelIdentity)
	if err != nil {
		return err
	}
	return applyMarketSummaryV150EventModelResponse(run, request, content, firstNonEmptyText(strings.TrimSpace(modelName), strings.TrimSpace(o.Model), "event_model"))
}

func buildMarketSummaryV150EventModelRequest(run *MarketSummaryV150RunSnapshot, verified []marketSummaryVerifiedCandidate) marketSummaryV150EventModelRequest {
	request := marketSummaryV150EventModelRequest{Candidates: []marketSummaryV150EventModelCandidate{}}
	if run == nil {
		return request
	}
	cutoff := time.Now()
	request.AsOf = cutoff.Format(time.RFC3339Nano)
	verifiedBySymbol := make(map[string]marketSummaryVerifiedCandidate, len(verified))
	for _, item := range verified {
		if symbol := normalizeRecommendStockCode(item.StockCode); symbol != "" {
			verifiedBySymbol[symbol] = item
		}
	}
	for _, rawSymbol := range run.VerificationSymbols {
		symbol := normalizeRecommendStockCode(rawSymbol)
		if symbol == "" {
			continue
		}
		candidate := marketSummaryV150EventModelCandidate{Symbol: symbol, Evidence: []marketSummaryV150EventModelEvidence{}, SourceAt: map[string]MarketSummaryV150EvidenceTiming{}}
		item := verifiedBySymbol[symbol]
		for index, ref := range item.EvidenceSources {
			sourceAt, ok := parseMarketSummaryV150EvidenceTime(ref.PublishedAt)
			if !ok || sourceAt.After(cutoff) || cutoff.Sub(sourceAt) > v150.FixedStrategyV150Config().EventFreshness {
				continue
			}
			evidenceID := firstNonEmptyText(strings.TrimSpace(ref.RawHash), strings.TrimSpace(ref.DedupeKey))
			if evidenceID == "" {
				evidenceID = fmt.Sprintf("verified:%s:%d:%s", symbol, index, marketSummaryV150StableHash(ref.SourceName+"|"+ref.Title+"|"+ref.PublishedAt))
			}
			if _, duplicate := candidate.SourceAt[evidenceID]; duplicate {
				continue
			}
			candidate.Evidence = append(candidate.Evidence, marketSummaryV150EventModelEvidence{
				EvidenceID: evidenceID,
				Title:      strings.TrimSpace(ref.Title), Summary: strings.TrimSpace(ref.Summary),
				Source:      firstNonEmptyText(strings.TrimSpace(ref.SourceName), strings.TrimSpace(ref.SourceType)),
				PublishedAt: sourceAt.Format(time.RFC3339Nano),
			})
			candidate.SourceAt[evidenceID] = MarketSummaryV150EvidenceTiming{
				EvidenceID: evidenceID, EvidenceType: firstNonEmptyText(ref.Type, ref.SourceType, "verified_evidence"),
				SourceAt: sourceAt, AvailableAt: cutoff,
			}
		}
		sort.SliceStable(candidate.Evidence, func(i, j int) bool { return candidate.Evidence[i].EvidenceID < candidate.Evidence[j].EvidenceID })
		request.Candidates = append(request.Candidates, candidate)
	}
	return request
}

func applyMarketSummaryV150EventModelResponse(
	run *MarketSummaryV150RunSnapshot,
	request marketSummaryV150EventModelRequest,
	content, verifier string,
) error {
	if run == nil {
		return errors.New("v1.5 event verification run is nil")
	}
	var envelopeFields map[string]json.RawMessage
	if err := decodeJSONPayload(content, &envelopeFields); err != nil {
		return fmt.Errorf("decode event verification response: %w", err)
	}
	if len(envelopeFields) != 1 || envelopeFields["assessments"] == nil {
		return errors.New("event verification response must contain only assessments")
	}
	var assessments []json.RawMessage
	if err := json.Unmarshal(envelopeFields["assessments"], &assessments); err != nil {
		return fmt.Errorf("decode event verification assessments: %w", err)
	}
	if assessments == nil {
		return errors.New("event verification response is missing assessments")
	}
	requestBySymbol := make(map[string]marketSummaryV150EventModelCandidate, len(request.Candidates))
	for _, candidate := range request.Candidates {
		requestBySymbol[normalizeRecommendStockCode(candidate.Symbol)] = candidate
	}
	valid := make(map[string]marketSummaryV150EventModelAssessment, len(requestBySymbol))
	invalid := make(map[string]string)
	seen := make(map[string]bool, len(requestBySymbol))
	for _, raw := range assessments {
		assessment, err := decodeMarketSummaryV150EventAssessment(raw)
		symbol := normalizeRecommendStockCode(assessment.Symbol)
		if symbol == "" {
			continue
		}
		candidate, requested := requestBySymbol[symbol]
		if !requested {
			continue
		}
		if seen[symbol] {
			invalid[symbol] = "duplicate_model_assessment"
			delete(valid, symbol)
			continue
		}
		seen[symbol] = true
		if err != nil {
			invalid[symbol] = "invalid_model_assessment:" + err.Error()
			continue
		}
		if err := validateMarketSummaryV150EventAssessment(assessment, candidate.SourceAt); err != nil {
			invalid[symbol] = "invalid_model_assessment:" + err.Error()
			continue
		}
		if invalid[symbol] == "" {
			valid[symbol] = assessment
		}
	}
	for symbol := range requestBySymbol {
		if _, ok := valid[symbol]; !ok {
			reason := invalid[symbol]
			if reason == "" {
				reason = "missing_model_assessment"
			}
			degradeMarketSummaryV150EventCandidates(run, []string{symbol}, reason)
			continue
		}
		applyMarketSummaryV150EventAssessment(run, valid[symbol], requestBySymbol[symbol].SourceAt, verifier)
	}
	return nil
}

func decodeMarketSummaryV150EventAssessment(raw json.RawMessage) (marketSummaryV150EventModelAssessment, error) {
	var result marketSummaryV150EventModelAssessment
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return result, err
	}
	allowed := map[string]bool{"symbol": true, "direction": true, "relevance": true, "importance": true, "confidence": true, "evidenceIds": true}
	for key := range fields {
		if !allowed[key] {
			return result, fmt.Errorf("unsupported field %s", key)
		}
	}
	for _, key := range []string{"symbol", "direction", "relevance", "importance", "confidence", "evidenceIds"} {
		if _, ok := fields[key]; !ok {
			return result, fmt.Errorf("missing field %s", key)
		}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.EvidenceIDs == nil {
		return result, errors.New("evidenceIds must be an array")
	}
	return result, nil
}

func validateMarketSummaryV150EventAssessment(assessment marketSummaryV150EventModelAssessment, evidence map[string]MarketSummaryV150EvidenceTiming) error {
	assessment.Direction = strings.ToLower(strings.TrimSpace(assessment.Direction))
	switch assessment.Direction {
	case "positive", "negative", "neutral":
	default:
		return fmt.Errorf("unknown direction %q", assessment.Direction)
	}
	for name, value := range map[string]float64{"relevance": assessment.Relevance, "importance": assessment.Importance, "confidence": assessment.Confidence} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("%s is outside [0,1]", name)
		}
	}
	seen := map[string]bool{}
	for _, evidenceID := range assessment.EvidenceIDs {
		evidenceID = strings.TrimSpace(evidenceID)
		if evidenceID == "" || seen[evidenceID] {
			return errors.New("evidenceIds contain an empty or duplicate id")
		}
		if _, exists := evidence[evidenceID]; !exists {
			return fmt.Errorf("unknown evidence id %s", evidenceID)
		}
		seen[evidenceID] = true
	}
	if assessment.Direction != "neutral" && len(seen) == 0 {
		return errors.New("directional assessment requires evidenceIds")
	}
	return nil
}

func applyMarketSummaryV150EventAssessment(
	run *MarketSummaryV150RunSnapshot,
	assessment marketSummaryV150EventModelAssessment,
	evidence map[string]MarketSummaryV150EvidenceTiming,
	verifier string,
) {
	symbol := normalizeRecommendStockCode(assessment.Symbol)
	for index := range run.Candidates {
		row := &run.Candidates[index]
		if normalizeRecommendStockCode(row.Candidate.Symbol) != symbol {
			continue
		}
		direction := strings.ToLower(strings.TrimSpace(assessment.Direction))
		row.Source.EventAssessment = MarketSummaryV150EventAssessment{
			Direction: direction, Relevance: assessment.Relevance, Importance: assessment.Importance,
			Confidence: assessment.Confidence, EvidenceIDs: dedupeNonEmptyStrings(assessment.EvidenceIDs, 16), Verifier: verifier,
		}
		row.Candidate.EventAt = nil
		row.Candidate.Signals.EventStrength = 0
		if direction == "positive" {
			var newest time.Time
			for _, evidenceID := range assessment.EvidenceIDs {
				if timing, ok := evidence[strings.TrimSpace(evidenceID)]; ok && timing.SourceAt.After(newest) {
					newest = timing.SourceAt
				}
			}
			if !newest.IsZero() {
				row.Candidate.EventAt = &newest
				row.Candidate.Signals.EventStrength = clamp01(assessment.Relevance * assessment.Importance * assessment.Confidence)
			}
		}
		row.Score = v150.ScoreCandidate(run.RunContext, row.Candidate, v150.FixedStrategyV150Config())
		return
	}
}

func degradeMarketSummaryV150EventCandidates(run *MarketSummaryV150RunSnapshot, symbols []string, reason string) {
	if run == nil {
		return
	}
	selected := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		if normalized := normalizeRecommendStockCode(symbol); normalized != "" {
			selected[normalized] = true
		}
	}
	for index := range run.Candidates {
		row := &run.Candidates[index]
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		if !selected[symbol] {
			continue
		}
		row.Candidate.EventAt = nil
		row.Candidate.Signals.EventStrength = 0
		row.Source.EventAssessment = MarketSummaryV150EventAssessment{Direction: "neutral", Verifier: "model_degraded"}
		row.Score = v150.ScoreCandidate(run.RunContext, row.Candidate, v150.FixedStrategyV150Config())
		run.Warnings = append(run.Warnings, symbol+":"+strings.TrimSpace(reason))
	}
	run.Warnings = dedupeNonEmptyStrings(run.Warnings, 256)
}
