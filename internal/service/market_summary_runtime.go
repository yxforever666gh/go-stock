package service

import (
	"encoding/json"
	"errors"
	"strings"
)

// MarketSummaryRouteLog is the delivery-facing portion of a phased summary
// route log. The full compatibility payload remains owned by its producer.
type MarketSummaryRouteLog struct {
	RunSlot              string
	IndicatorCandidateCt int
	IndicatorAIInputCt   int
	DiscoveryCandidateCt int
	VerifiedCandidateCt  int
	Notes                []string
}

// MarketSummaryV150DecisionEnvelope carries a frozen decision across the
// delivery boundary without exposing the compatibility implementation type.
// RawJSON is retained so bootstrap can restore the exact persisted payload.
type MarketSummaryV150DecisionEnvelope struct {
	RunID           string
	StrategyVersion string
	CandidateCount  int
	ProductionCount int
	NoTradeReason   string
	RawJSON         json.RawMessage
}

func (e *MarketSummaryV150DecisionEnvelope) MarketSummaryDecisionVersion() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.StrategyVersion)
}

func DecodeMarketSummaryRouteLog(raw any) (*MarketSummaryRouteLog, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var wire struct {
		RunSlot              string   `json:"runSlot"`
		IndicatorCandidateCt int      `json:"indicatorCandidateCount"`
		IndicatorAIInputCt   int      `json:"indicatorAIInputCount"`
		DiscoveryCandidateCt int      `json:"discoveryCandidateCount"`
		VerifiedCandidateCt  int      `json:"verifiedCandidateCount"`
		Notes                []string `json:"notes"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, err
	}
	return &MarketSummaryRouteLog{
		RunSlot:              strings.TrimSpace(wire.RunSlot),
		IndicatorCandidateCt: wire.IndicatorCandidateCt,
		IndicatorAIInputCt:   wire.IndicatorAIInputCt,
		DiscoveryCandidateCt: wire.DiscoveryCandidateCt,
		VerifiedCandidateCt:  wire.VerifiedCandidateCt,
		Notes:                append([]string(nil), wire.Notes...),
	}, nil
}

func DecodeMarketSummaryV150DecisionEnvelope(raw any) (*MarketSummaryV150DecisionEnvelope, error) {
	if raw == nil {
		return nil, errors.New("market summary V1.5 decision is required")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var wire struct {
		RunContext struct {
			RunID           string `json:"runId"`
			StrategyVersion string `json:"strategyVersion"`
		} `json:"runContext"`
		Candidates    []json.RawMessage `json:"candidates"`
		Production    []json.RawMessage `json:"production"`
		NoTradeReason string            `json:"noTradeReason"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, err
	}
	if strings.TrimSpace(wire.RunContext.RunID) == "" {
		return nil, errors.New("market summary V1.5 decision run id is required")
	}
	if strings.TrimSpace(wire.RunContext.StrategyVersion) == "" {
		return nil, errors.New("market summary V1.5 decision strategy version is required")
	}
	return &MarketSummaryV150DecisionEnvelope{
		RunID:           strings.TrimSpace(wire.RunContext.RunID),
		StrategyVersion: strings.TrimSpace(wire.RunContext.StrategyVersion),
		CandidateCount:  len(wire.Candidates),
		ProductionCount: len(wire.Production),
		NoTradeReason:   strings.TrimSpace(wire.NoTradeReason),
		RawJSON:         append(json.RawMessage(nil), b...),
	}, nil
}
