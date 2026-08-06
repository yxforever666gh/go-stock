package recommendation

import (
	"context"
)

// FrozenDecision is the immutable decision envelope accepted by the
// recommendation production boundary.
type FrozenDecision interface {
	MarketSummaryDecisionVersion() string
}

// DecisionPublisher persists a frozen recommendation decision using the
// existing publication transaction.
type DecisionPublisher[Result any] interface {
	PublishDecision(context.Context, FrozenDecision, string, string) (Result, error)
}

// ProductionService exposes the recommendation production use case without
// depending on a concrete persistence implementation.
type ProductionService[Result any] struct {
	publisher DecisionPublisher[Result]
}

func NewProductionService[Result any](publisher DecisionPublisher[Result]) ProductionService[Result] {
	return ProductionService[Result]{publisher: publisher}
}

// PublishDecision preserves the caller's context, decision, provider/model
// identifiers, result pointer, and error exactly as supplied by the publisher.
func (s ProductionService[Result]) PublishDecision(
	ctx context.Context,
	decision FrozenDecision,
	providerName string,
	modelName string,
) (Result, error) {
	return s.publisher.PublishDecision(ctx, decision, providerName, modelName)
}
