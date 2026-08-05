// Package legacy provides read-only access to historical strategy cohorts.
package legacy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/marketdata"
)

const FrozenThroughStrategyVersion = "1.4.2"

var ErrNotFrozenLegacy = errors.New("strategy version is not in the frozen legacy cohort")

type Recommendation struct {
	ID              uint            `json:"id"`
	StrategyVersion string          `json:"strategyVersion"`
	TradeDate       time.Time       `json:"tradeDate"`
	Symbol          string          `json:"symbol"`
	Name            string          `json:"name"`
	Status          string          `json:"status"`
	Payload         json.RawMessage `json:"payload"`
}

type Query struct {
	Symbols []string
	Start   time.Time
	End     time.Time
	Limit   int
}

// Repository deliberately has no Create, Update, Delete, Repair or Backfill
// method. Historical rows are query material, never execution inputs.
type Repository interface {
	Find(context.Context, uint) (Recommendation, error)
	List(context.Context, Query) ([]Recommendation, error)
}

type Service struct {
	repository Repository
	marketData marketdata.DailyBarReader
}

func NewService(repository Repository, marketData marketdata.DailyBarReader) Service {
	return Service{repository: repository, marketData: marketData}
}

func (s Service) Find(ctx context.Context, id uint) (Recommendation, error) {
	if s.repository == nil {
		return Recommendation{}, errors.New("legacy repository is nil")
	}
	record, err := s.repository.Find(ctx, id)
	if err != nil {
		return Recommendation{}, err
	}
	if !IsFrozenVersion(record.StrategyVersion) {
		return Recommendation{}, fmt.Errorf("%w: %q", ErrNotFrozenLegacy, record.StrategyVersion)
	}
	return record, nil
}

func (s Service) List(ctx context.Context, query Query) ([]Recommendation, error) {
	if s.repository == nil {
		return nil, errors.New("legacy repository is nil")
	}
	records, err := s.repository.List(ctx, query)
	if err != nil {
		return nil, err
	}
	result := make([]Recommendation, 0, len(records))
	for _, record := range records {
		if IsFrozenVersion(record.StrategyVersion) {
			result = append(result, record)
		}
	}
	return result, nil
}

func IsFrozenVersion(raw string) bool {
	version, ok := parseVersion(raw)
	if !ok || version[0] != 1 {
		return false
	}
	limit := [3]int{1, 4, 2}
	for i := range version {
		if version[i] < limit[i] {
			return true
		}
		if version[i] > limit[i] {
			return false
		}
	}
	return true
}

func parseVersion(raw string) ([3]int, bool) {
	var result [3]int
	raw = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(raw)), "v")
	parts := strings.Split(raw, ".")
	if len(parts) != len(result) {
		return result, false
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return [3]int{}, false
		}
		result[i] = value
	}
	return result, true
}
