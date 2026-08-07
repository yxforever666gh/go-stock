package service

import (
	"encoding/json"
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
