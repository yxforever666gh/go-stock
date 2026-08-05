package data

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/news"
)

// CompatibilityNewsReader preserves the legacy provider's explicit
// ok/empty/failed/stale result while projecting rows into provider-neutral
// point-in-time observations.
type CompatibilityNewsReader struct{}

func NewCompatibilityNewsReader() CompatibilityNewsReader {
	return CompatibilityNewsReader{}
}

func (CompatibilityNewsReader) GetNewsWindow(ctx context.Context, query news.Query) (news.NewsWindowResult, error) {
	if err := ctx.Err(); err != nil {
		return news.NewsWindowResult{}, err
	}
	if err := query.Validate(); err != nil {
		return news.NewsWindowResult{}, err
	}

	legacy, legacyErr := NewMarketNewsApi().GetNewsWindow(query.Sources, query.Start, query.End)
	result := news.NewsWindowResult{
		Items: make([]news.Item, 0, len(legacy.Items)), Status: compatibilityNewsStatus(legacy.Status),
		Sources: append([]string(nil), legacy.Sources...), From: legacy.From, To: legacy.To,
		Warning: legacy.Warning,
	}
	for _, row := range legacy.Items {
		if row == nil || !compatibilityNewsMatchesScope(row, query) {
			continue
		}
		sourceAt := compatibilityNewsEventTime(row)
		availableAt := row.CreatedAt
		if sourceAt.IsZero() || availableAt.IsZero() || sourceAt.After(availableAt) || availableAt.After(query.AsOf) || sourceAt.After(query.End) {
			continue
		}
		payload, err := json.Marshal(row)
		if err != nil {
			result.Status = news.WindowStatusFailed
			result.Warning = fmt.Sprintf("marshal legacy news %d: %v", row.ID, err)
			return result, err
		}
		result.Items = append(result.Items, news.Item{
			ID: "telegraph:" + strconv.FormatUint(uint64(row.ID), 10), Scope: compatibilityNewsScope(query.Scope),
			Symbols: append([]string(nil), row.StocksTags...), Industries: append([]string(nil), row.SubjectTags...),
			Title: strings.TrimSpace(row.Title), Summary: strings.TrimSpace(row.Content), URL: strings.TrimSpace(row.Url),
			PublishedAt: sourceAt, Source: strings.TrimSpace(row.Source), SourceAt: sourceAt,
			AvailableAt: availableAt, Payload: payload,
		})
		if query.Limit > 0 && len(result.Items) >= query.Limit {
			break
		}
	}
	if legacyErr == nil && result.Status == news.WindowStatusOK && len(result.Items) == 0 {
		result.Status = news.WindowStatusEmpty
		result.Warning = "no causally visible news matched the requested scope"
	}
	return result, legacyErr
}

func compatibilityNewsStatus(status NewsWindowStatus) news.WindowStatus {
	switch status {
	case NewsWindowStatusOK:
		return news.WindowStatusOK
	case NewsWindowStatusStale:
		return news.WindowStatusStale
	case NewsWindowStatusFailed:
		return news.WindowStatusFailed
	default:
		return news.WindowStatusEmpty
	}
}

func compatibilityNewsScope(scope news.Scope) news.Scope {
	if scope == "" {
		return news.ScopeMarket
	}
	return scope
}

func compatibilityNewsEventTime(row *models.Telegraph) time.Time {
	if row != nil && row.DataTime != nil && !row.DataTime.IsZero() && row.DataTime.Year() > 1 {
		return *row.DataTime
	}
	if row == nil {
		return time.Time{}
	}
	return row.CreatedAt
}

func compatibilityNewsMatchesScope(row *models.Telegraph, query news.Query) bool {
	switch query.Scope {
	case "", news.ScopeMarket:
		return true
	case news.ScopeSecurity:
		want := normalizeRecommendStockCode(query.Symbol)
		for _, value := range row.StocksTags {
			if normalizeRecommendStockCode(value) == want {
				return true
			}
		}
		return false
	case news.ScopeIndustry:
		want := strings.TrimSpace(query.Industry)
		for _, value := range row.SubjectTags {
			if strings.EqualFold(strings.TrimSpace(value), want) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

var _ news.Reader = CompatibilityNewsReader{}
