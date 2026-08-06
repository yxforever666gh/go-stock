// Package legacy provides read-only access to historical strategy cohorts.
package legacy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/marketdata"
)

const FrozenThroughStrategyVersion = "1.4.2"

var ErrNotFrozenLegacy = errors.New("strategy version is not in the frozen legacy cohort")

// frozenVersionAliases is the canonical allowlist for historical rows that
// existed before Strategy 1.5.0. Keep this list explicit: an unknown or future
// version must not become readable merely because it sorts below a current
// version or differs from it.
var frozenVersionAliases = []string{
	"",
	"phase1-v1",
	"phase2-v1",
	"phase3-v2",
	"phase3-v3",
	"phase3-v4",
	"1.3.2",
	"v1.3.2",
	"1.3.6",
	"v1.3.6",
	"1.4.0",
	"v1.4.0",
	"1.4.1",
	"v1.4.1",
	"1.4.2",
	"v1.4.2",
}

var frozenVersionAliasSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(frozenVersionAliases))
	for _, version := range frozenVersionAliases {
		result[version] = struct{}{}
	}
	return result
}()

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
	_, ok := frozenVersionAliasSet[normalizeVersion(raw)]
	return ok
}

// FrozenVersionAliases returns normalized values suitable for a storage-level
// allowlist. The returned slice is a copy so callers cannot widen the legacy
// boundary at runtime.
func FrozenVersionAliases() []string {
	return append([]string(nil), frozenVersionAliases...)
}

func normalizeVersion(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
