package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go-stock/backend/marketintel"
	"go-stock/backend/news"
)

// CompatibilityMarketIntelReader projects causally visible legacy news into
// the provider-neutral evidence contract. It is read-only and never fetches
// data, so the recommendation boundary can be migrated independently of the
// legacy news client.
type CompatibilityMarketIntelReader struct {
	news news.Reader
}

func NewCompatibilityMarketIntelReader(reader news.Reader) CompatibilityMarketIntelReader {
	return CompatibilityMarketIntelReader{news: reader}
}

func (r CompatibilityMarketIntelReader) Evidence(ctx context.Context, query marketintel.Query) ([]marketintel.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query.Start.IsZero() || query.End.IsZero() || query.AsOf.IsZero() || query.End.Before(query.Start) || query.End.After(query.AsOf) {
		return nil, errors.New("market intel query requires start <= end <= asOf")
	}
	if r.news == nil {
		return nil, errors.New("market intel news reader is not configured")
	}
	if !compatibilityWantsEventEvidence(query.Types) {
		return []marketintel.Evidence{}, nil
	}

	result := make([]marketintel.Evidence, 0)
	seen := make(map[string]bool)
	appendWindow := func(scope news.Scope, symbol string) error {
		window, err := r.news.GetNewsWindow(ctx, news.Query{
			Scope: scope, Symbol: symbol, Start: query.Start, End: query.End, AsOf: query.AsOf,
		})
		if err != nil {
			return err
		}
		if window.Status == news.WindowStatusFailed || window.Status == news.WindowStatusStale {
			return fmt.Errorf("market intel news window %s: %s", window.Status, strings.TrimSpace(window.Warning))
		}
		for _, item := range window.Items {
			if item.ID == "" || item.SourceAt.IsZero() || item.AvailableAt.IsZero() || item.SourceAt.After(item.AvailableAt) || item.AvailableAt.After(query.AsOf) {
				continue
			}
			if item.SourceAt.Before(query.Start) || item.SourceAt.After(query.End) {
				continue
			}
			normalizedSymbol := normalizeRecommendStockCode(symbol)
			id := "news:" + strings.TrimSpace(item.ID)
			if normalizedSymbol != "" {
				id += ":" + normalizedSymbol
			}
			if seen[id] {
				continue
			}
			payload, marshalErr := json.Marshal(item)
			if marshalErr != nil {
				return marshalErr
			}
			seen[id] = true
			result = append(result, marketintel.Evidence{
				ID: id, Type: marketintel.EvidenceEvent, Symbol: normalizedSymbol, Title: item.Title,
				Source: item.Source, SourceAt: item.SourceAt, AvailableAt: item.AvailableAt, Payload: payload,
			})
		}
		return nil
	}

	if len(query.Symbols) == 0 {
		if err := appendWindow(news.ScopeMarket, ""); err != nil {
			return nil, err
		}
	} else {
		for _, symbol := range compatibilitySortedUnique(query.Symbols) {
			if err := appendWindow(news.ScopeSecurity, symbol); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func compatibilityWantsEventEvidence(types []marketintel.EvidenceType) bool {
	if len(types) == 0 {
		return true
	}
	for _, kind := range types {
		if kind == marketintel.EvidenceEvent {
			return true
		}
	}
	return false
}

func compatibilitySortedUnique(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeRecommendStockCode(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

var _ marketintel.Reader = CompatibilityMarketIntelReader{}
