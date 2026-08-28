package data

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/research"
)

// ExperimentalResearchSourceCollector augments the unchanged legacy corpus
// with typed 2.0 market evidence. It is only constructed behind the
// experimental_evidence_enabled switch.
type ExperimentalResearchSourceCollector struct {
	base   research.SourceCollector
	market *MarketEvidenceService
}

func NewExperimentalResearchSourceCollector(base research.SourceCollector, market *MarketEvidenceService) *ExperimentalResearchSourceCollector {
	if market == nil {
		market = NewMarketEvidenceService()
	}
	return &ExperimentalResearchSourceCollector{base: base, market: market}
}

func (c *ExperimentalResearchSourceCollector) CollectMarket(ctx context.Context, at time.Time) ([]research.SourceDocument, error) {
	documents, baseErr := c.base.CollectMarket(ctx, at)
	type result struct {
		index    int
		document research.SourceDocument
	}
	results := make(chan result, 8)
	jobs := []func() research.SourceDocument{
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0市场宽度", "market-breadth", c.market.Breadth(ctx))
		},
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0行业资金流", "fund-flow-sector", c.market.FundFlows(ctx, marketdata.ProviderRequest{Scope: "sector", Sort: "netamount", Limit: 20}))
		},
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0概念资金流", "fund-flow-concept", c.market.FundFlows(ctx, marketdata.ProviderRequest{Scope: "concept", Sort: "netamount", Limit: 20}))
		},
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0 IF期指持仓", "futures-if", c.market.FuturesPositions(ctx, marketdata.ProviderRequest{Symbol: "IF"}))
		},
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0 IH期指持仓", "futures-ih", c.market.FuturesPositions(ctx, marketdata.ProviderRequest{Symbol: "IH"}))
		},
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0 IC期指持仓", "futures-ic", c.market.FuturesPositions(ctx, marketdata.ProviderRequest{Symbol: "IC"}))
		},
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0 IM期指持仓", "futures-im", c.market.FuturesPositions(ctx, marketdata.ProviderRequest{Symbol: "IM"}))
		},
		func() research.SourceDocument {
			return marketEnvelopeDocument("2.0沪深两融汇总", "margin-market", c.market.Margin(ctx, marketdata.ProviderRequest{Scope: "market"}))
		},
	}
	ordered := make([]research.SourceDocument, len(jobs))
	for index, job := range jobs {
		index, job := index, job
		go func() { results <- result{index: index, document: job()} }()
	}
	for range jobs {
		value := <-results
		ordered[value.index] = value.document
	}
	documents = append(documents, ordered...)
	return documents, baseErr
}

func (c *ExperimentalResearchSourceCollector) CollectSectors(ctx context.Context, at time.Time) ([]research.SourceDocument, error) {
	return c.base.CollectSectors(ctx, at)
}

func (c *ExperimentalResearchSourceCollector) CollectStocks(ctx context.Context, at time.Time, candidates []research.StockCandidate) ([]research.SourceDocument, error) {
	return c.base.CollectStocks(ctx, at, candidates)
}

func marketEnvelopeDocument[T any](name, sourceID string, envelope marketdata.DataEnvelope[T]) research.SourceDocument {
	document := research.SourceDocument{SourceID: sourceID, SourceName: name, Category: "market", CollectedAt: envelope.FetchedAt}
	for _, source := range envelope.Sources {
		if source.Provider != envelope.Source {
			continue
		}
		document.AvailableAt, document.SourceRef = source.AvailableAt, source.SourceRef
		break
	}
	if envelope.Status == marketdata.StatusOK || envelope.Status == marketdata.StatusPartial || envelope.Status == marketdata.StatusStale {
		if document.AvailableAt != nil {
			body, err := json.Marshal(envelope)
			if err == nil {
				document.Content = string(body)
				return document
			}
			document.Error = err.Error()
			return document
		}
	}
	messages := make([]string, 0, len(envelope.Errors)+len(envelope.Warnings))
	for _, item := range envelope.Errors {
		messages = append(messages, item.Provider+": "+item.Message)
	}
	messages = append(messages, envelope.Warnings...)
	document.Error = strings.TrimSpace(strings.Join(messages, "; "))
	if document.Error == "" {
		document.Error = errors.New("typed market evidence is unavailable").Error()
	}
	return document
}
