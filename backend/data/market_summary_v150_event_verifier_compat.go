package data

import (
	"context"
	"errors"

	"go-stock/backend/recommendation"
)

// This alias keeps the deprecated OpenAi carrier independent of the boundary
// import. All recommendation-boundary wiring stays in this explicit
// compatibility adapter while the V1.5 production chain is strangled out of
// backend/data.
type marketSummaryV150EventVerifier = recommendation.EventVerifier

// BindMarketSummaryV150EventVerifier installs the consumer-defined batch port
// on the exact OpenAi instance that owns the phased market-summary run.
func BindMarketSummaryV150EventVerifier(o *OpenAi, verifier recommendation.EventVerifier) *OpenAi {
	if o != nil {
		o.eventVerifier = verifier
	}
	return o
}

func (o *OpenAi) completeMarketSummaryV150EventBatch(messages []map[string]any, think bool) (string, string, string, error) {
	if o == nil || o.eventVerifier == nil {
		return "", "", "", errors.New("event verifier model is unavailable")
	}
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	completion, err := o.eventVerifier.Verify(ctx, recommendation.EventVerificationCall{
		Messages: messages,
		Think:    think,
	})
	return completion.Content, completion.ResponseID, completion.Model, err
}
