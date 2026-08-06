package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/legacy"
	"go-stock/backend/models"
	"go-stock/backend/portfolio"
)

var ErrInvalidRecommendationListQuery = errors.New("invalid recommendation list query")

type recommendationListCohort uint8

const (
	recommendationListCurrent recommendationListCohort = iota + 1
	recommendationListLegacy
)

// GetAiRecommendStocksList is the compatibility API boundary for two isolated
// read models. Current Strategy rows are derived from frozen snapshots and the
// sealed ledger; historical cohorts continue through the legacy operation
// until the public DTO is replaced by the generated Web contract.
func (s RecommendService) GetAiRecommendStocksList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error) {
	cohort, err := classifyRecommendationListCohort(query, s.currentStrategyVersion)
	if err != nil {
		return nil, err
	}
	if cohort == recommendationListLegacy {
		return s.operations.GetAiRecommendStocksList(query)
	}
	return s.currentRecommendationPage(context.Background(), query)
}

func classifyRecommendationListCohort(query *models.AiRecommendStocksQuery, currentStrategyVersion string) (recommendationListCohort, error) {
	if query == nil {
		return 0, fmt.Errorf("%w: query and explicit cohort are required", ErrInvalidRecommendationListQuery)
	}
	raw := strings.TrimSpace(query.StrategyCohort)
	normalized := strings.ToLower(raw)
	switch {
	case normalized == "current", isCurrentRecommendationVersion(normalized, currentStrategyVersion):
		return recommendationListCurrent, nil
	case normalized == "legacy":
		return recommendationListLegacy, nil
	case raw != "" && legacy.IsFrozenVersion(raw):
		return recommendationListLegacy, nil
	case normalized == "all":
		return 0, fmt.Errorf("%w: mixed current and legacy reads are forbidden", ErrInvalidStrategyCohort)
	case raw == "":
		return 0, fmt.Errorf("%w: cohort is required", ErrInvalidStrategyCohort)
	default:
		return 0, fmt.Errorf("%w: unknown recommendation cohort %q", ErrInvalidStrategyCohort, raw)
	}
}

func isCurrentRecommendationVersion(normalized, currentStrategyVersion string) bool {
	current := strings.ToLower(strings.TrimSpace(currentStrategyVersion))
	if current == "" {
		return false
	}
	return strings.TrimPrefix(normalized, "v") == strings.TrimPrefix(current, "v")
}

func (s RecommendService) currentRecommendationPage(
	ctx context.Context,
	query *models.AiRecommendStocksQuery,
) (*models.AiRecommendStocksPageData, error) {
	if s.currentRecommendations == nil || s.clock == nil || s.currentStrategyVersion == "" {
		return nil, fmt.Errorf("%w: current recommendation dependencies are unavailable", ErrInvalidRecommendationListQuery)
	}
	asOf := s.clock.Now()
	if asOf.IsZero() {
		return nil, fmt.Errorf("%w: clock returned a zero asOf", ErrInvalidRecommendationListQuery)
	}
	start, end, err := currentRecommendationDateWindow(query.StartDate, query.EndDate, asOf)
	if err != nil {
		return nil, err
	}
	rows, err := s.currentRecommendations.List(ctx, portfolio.RecommendationQuery{
		StrategyVersion: s.currentStrategyVersion,
		Start:           start,
		End:             end,
		AsOf:            asOf,
	})
	if err != nil {
		return nil, err
	}

	filtered := make([]portfolio.CurrentRecommendation, 0, len(rows))
	for _, row := range rows {
		if currentRecommendationMatches(row, query) {
			filtered = append(filtered, row)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		left, right := filtered[i].Frozen, filtered[j].Frozen
		if !left.DecisionAt.Equal(right.DecisionAt) {
			return left.DecisionAt.After(right.DecisionAt)
		}
		if left.RunID != right.RunID {
			return left.RunID > right.RunID
		}
		if left.Symbol != right.Symbol {
			return left.Symbol < right.Symbol
		}
		return left.RuleID < right.RuleID
	})

	page, pageSize := normalizedRecommendationPage(query.Page, query.PageSize)
	total := int64(len(filtered))
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	offset := (page - 1) * pageSize
	if offset > len(filtered) {
		offset = len(filtered)
	}
	limit := offset + pageSize
	if limit > len(filtered) {
		limit = len(filtered)
	}
	list := make([]models.AiRecommendStocks, 0, limit-offset)
	for _, row := range filtered[offset:limit] {
		list = append(list, currentRecommendationDTO(row))
	}
	return &models.AiRecommendStocksPageData{
		List: list, Total: total, Page: page, PageSize: pageSize,
		TotalPages: totalPages, StrategyCohort: s.currentStrategyVersion,
	}, nil
}

func currentRecommendationDateWindow(startText, endText string, asOf time.Time) (time.Time, time.Time, error) {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	localAsOf := asOf.In(zone)
	defaultDay := time.Date(localAsOf.Year(), localAsOf.Month(), localAsOf.Day(), 0, 0, 0, 0, zone)
	start := time.Date(1970, time.January, 1, 0, 0, 0, 0, zone)
	end := defaultDay
	var err error
	if strings.TrimSpace(startText) != "" {
		start, err = parseCurrentRecommendationDate(startText, zone)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: startDate: %v", ErrInvalidRecommendationListQuery, err)
		}
	}
	if strings.TrimSpace(endText) != "" {
		end, err = parseCurrentRecommendationDate(endText, zone)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: endDate: %v", ErrInvalidRecommendationListQuery, err)
		}
	} else if strings.TrimSpace(startText) != "" {
		end = start
	}
	if strings.TrimSpace(startText) == "" && strings.TrimSpace(endText) != "" {
		start = end
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: endDate is before startDate", ErrInvalidRecommendationListQuery)
	}
	return start, end, nil
}

func parseCurrentRecommendationDate(raw string, zone *time.Location) (time.Time, error) {
	text := strings.TrimSpace(raw)
	for _, layout := range []string{time.DateOnly, time.DateTime} {
		if parsed, err := time.ParseInLocation(layout, text, zone); err == nil {
			return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, zone), nil
		}
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		local := parsed.In(zone)
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone), nil
	}
	return time.Time{}, fmt.Errorf("unsupported date %q", raw)
}

func normalizedRecommendationPage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}
	return page, pageSize
}

func currentRecommendationMatches(row portfolio.CurrentRecommendation, query *models.AiRecommendStocksQuery) bool {
	match := func(needle string, values ...string) bool {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle == "" {
			return true
		}
		for _, value := range values {
			if strings.Contains(strings.ToLower(strings.TrimSpace(value)), needle) {
				return true
			}
		}
		return false
	}
	if !match(query.StockCode, row.Frozen.Symbol) ||
		!match(query.StockName, row.Frozen.Name) ||
		!match(query.BkName, row.Frozen.Sector) {
		return false
	}
	// FrozenRecommendation intentionally has no mutable legacy board code.
	// A requested board-code filter therefore cannot match a current row.
	if strings.TrimSpace(query.BkCode) != "" {
		return false
	}
	if row.Display == nil {
		return strings.TrimSpace(query.ModelName) == ""
	}
	return match(query.ModelName, row.Display.Model, row.Display.Provider)
}

func currentRecommendationDTO(row portfolio.CurrentRecommendation) models.AiRecommendStocks {
	decisionAt := row.Frozen.DecisionAt
	status := string(row.Lifecycle.Status)
	result := models.AiRecommendStocks{
		DataTime:         &decisionAt,
		StockCode:        row.Frozen.Symbol,
		StockName:        row.Frozen.Name,
		BkName:           row.Frozen.Sector,
		ExecutionState:   status,
		ActivationStatus: status,
		RecommendStatus:  status,
		SummaryVersion:   row.Frozen.StrategyVersion,
		StrategyRunID:    row.Frozen.RunID,
		StrategyRuleID:   row.Frozen.RuleID,
		Remarks:          strings.TrimSpace(row.Lifecycle.Reason),
	}
	result.CreatedAt = decisionAt
	if row.Lifecycle.Status == portfolio.RecommendationRejected || row.Lifecycle.Status == portfolio.RecommendationExpired {
		result.ActivationInvalidReason = strings.TrimSpace(row.Lifecycle.Reason)
	}
	if row.Display != nil {
		result.ID = row.Display.RecommendID
		result.ProviderName = row.Display.Provider
		result.ModelName = row.Display.Model
		if len(row.Display.OpeningReview) != 0 {
			var review models.AiRecommendOpeningReviewSummary
			if json.Unmarshal(row.Display.OpeningReview, &review) == nil {
				result.LatestOpeningReview = &review
			}
		}
	}
	return result
}
