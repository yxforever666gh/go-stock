package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/portfolio"

	"gorm.io/gorm"
)

// CompatibilityPortfolioLedger reads only the immutable order-event table.
// Mutable yield projections are deliberately outside this adapter.
type CompatibilityPortfolioLedger struct {
	database *gorm.DB
}

func NewCompatibilityPortfolioLedger(database *gorm.DB) CompatibilityPortfolioLedger {
	return CompatibilityPortfolioLedger{database: database}
}

func (r CompatibilityPortfolioLedger) OrderEvents(ctx context.Context, query portfolio.LedgerQuery) ([]portfolio.LedgerEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateCompatibilityLedgerQuery(query); err != nil {
		return nil, err
	}
	if r.database == nil || !r.database.Migrator().HasTable(&models.OrderEvent{}) {
		return nil, fmt.Errorf("%w: strategy_order_event is unavailable", portfolio.ErrUnsealedLedger)
	}

	dbq := r.database.WithContext(ctx).Model(&models.OrderEvent{}).
		Where("strategy_version = ?", strings.TrimSpace(query.StrategyVersion))
	if len(query.RunIDs) > 0 {
		dbq = dbq.Where("run_id IN ?", normalizedCompatibilityRunIDs(query.RunIDs))
	}
	rows := make([]models.OrderEvent, 0)
	if err := dbq.Order("event_at ASC, run_id ASC, rule_id ASC, sequence ASC, event_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	visibleRows := make([]models.OrderEvent, 0, len(rows))
	for _, row := range rows {
		if row.FrozenAt == nil || row.FrozenAt.IsZero() || row.EventAt.After(query.AsOf) || row.FrozenAt.After(query.AsOf) {
			continue
		}
		if !query.Start.IsZero() && row.EventAt.Before(query.Start) {
			continue
		}
		if !query.End.IsZero() && row.EventAt.After(query.End) {
			continue
		}
		visibleRows = append(visibleRows, row)
	}
	if err := persistence.VerifyStrategyOrderEvents(visibleRows); err != nil {
		return nil, fmt.Errorf("%w: %v", portfolio.ErrUnsealedLedger, err)
	}

	result := make([]portfolio.LedgerEvent, 0, len(visibleRows))
	for _, row := range visibleRows {
		if row.FrozenAt == nil || row.FrozenAt.IsZero() {
			return nil, fmt.Errorf("%w: event %s has no immutable timestamp", portfolio.ErrUnsealedLedger, row.EventID)
		}
		result = append(result, portfolio.LedgerEvent{
			EventID: row.EventID, RunID: row.RunID, RuleID: row.RuleID,
			StrategyVersion: row.StrategyVersion, TradeDate: row.TradeDate, Symbol: row.Symbol,
			EventType: row.EventType, Sequence: row.Sequence, EventAt: row.EventAt,
			Price: row.Price, Quantity: row.Quantity, CashAmount: row.CashAmount,
			AdjustmentFactor: row.AdjustmentFactor, Fees: row.Fees, Reason: row.Reason,
			SnapshotHash: row.SnapshotHash, FrozenAt: *row.FrozenAt,
		})
	}
	return result, nil
}

func (r CompatibilityPortfolioLedger) LedgerSeal(ctx context.Context, query portfolio.LedgerQuery) (portfolio.LedgerSeal, error) {
	events, err := r.OrderEvents(ctx, query)
	if err != nil {
		return portfolio.LedgerSeal{}, err
	}
	return portfolio.LedgerSeal{
		StrategyVersion: strings.TrimSpace(query.StrategyVersion),
		RunIDs:          compatibilityLedgerRunIDs(query.RunIDs, events),
		EventCount:      len(events), LedgerHash: compatibilityLedgerHash(events), SealedThrough: query.AsOf,
	}, nil
}

func (CompatibilityPortfolioLedger) VerifyLedgerSeal(_ context.Context, query portfolio.LedgerQuery, seal portfolio.LedgerSeal, events []portfolio.LedgerEvent) error {
	if err := validateCompatibilityLedgerQuery(query); err != nil {
		return err
	}
	if seal.StrategyVersion != strings.TrimSpace(query.StrategyVersion) || seal.EventCount != len(events) || seal.SealedThrough.IsZero() || !seal.SealedThrough.Equal(query.AsOf) {
		return fmt.Errorf("%w: ledger metadata mismatch", portfolio.ErrUnsealedLedger)
	}
	if want := compatibilityLedgerRunIDs(query.RunIDs, events); !equalCompatibilityStrings(seal.RunIDs, want) {
		return fmt.Errorf("%w: ledger run identity mismatch", portfolio.ErrUnsealedLedger)
	}
	for i, event := range events {
		if event.StrategyVersion != seal.StrategyVersion || event.EventAt.After(query.AsOf) || event.FrozenAt.After(query.AsOf) || event.FrozenAt.Before(event.EventAt) {
			return fmt.Errorf("%w: event %d violates the requested point-in-time boundary", portfolio.ErrUnsealedLedger, i)
		}
	}
	if got := compatibilityLedgerHash(events); got != seal.LedgerHash {
		return fmt.Errorf("%w: ledger hash mismatch", portfolio.ErrUnsealedLedger)
	}
	return nil
}

func equalCompatibilityStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateCompatibilityLedgerQuery(query portfolio.LedgerQuery) error {
	if strings.TrimSpace(query.StrategyVersion) == "" || query.AsOf.IsZero() {
		return fmt.Errorf("%w: strategyVersion and asOf are required", portfolio.ErrUnsealedLedger)
	}
	if !query.Start.IsZero() && !query.End.IsZero() && query.End.Before(query.Start) {
		return fmt.Errorf("%w: end is before start", portfolio.ErrUnsealedLedger)
	}
	if !query.End.IsZero() && query.End.After(query.AsOf) {
		return fmt.Errorf("%w: end is after asOf", portfolio.ErrUnsealedLedger)
	}
	return nil
}

func normalizedCompatibilityRunIDs(runIDs []string) []string {
	seen := make(map[string]struct{}, len(runIDs))
	result := make([]string, 0, len(runIDs))
	for _, runID := range runIDs {
		runID = strings.TrimSpace(runID)
		if runID == "" {
			continue
		}
		if _, exists := seen[runID]; exists {
			continue
		}
		seen[runID] = struct{}{}
		result = append(result, runID)
	}
	sort.Strings(result)
	return result
}

func compatibilityLedgerRunIDs(requested []string, events []portfolio.LedgerEvent) []string {
	if normalized := normalizedCompatibilityRunIDs(requested); len(normalized) > 0 {
		return normalized
	}
	values := make([]string, 0, len(events))
	for _, event := range events {
		values = append(values, event.RunID)
	}
	return normalizedCompatibilityRunIDs(values)
}

func compatibilityLedgerHash(events []portfolio.LedgerEvent) string {
	ordered := append([]portfolio.LedgerEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].EventAt.Equal(ordered[j].EventAt) {
			return ordered[i].EventAt.Before(ordered[j].EventAt)
		}
		if ordered[i].RunID != ordered[j].RunID {
			return ordered[i].RunID < ordered[j].RunID
		}
		if ordered[i].RuleID != ordered[j].RuleID {
			return ordered[i].RuleID < ordered[j].RuleID
		}
		if ordered[i].Sequence != ordered[j].Sequence {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		return ordered[i].EventID < ordered[j].EventID
	})
	payload, _ := json.Marshal(ordered)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

var _ portfolio.ReadOnlyLedger = CompatibilityPortfolioLedger{}
