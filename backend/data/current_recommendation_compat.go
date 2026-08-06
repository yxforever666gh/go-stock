package data

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/portfolio"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
)

// CompatibilityCurrentRecommendationReader derives current recommendations
// from the immutable V1.5.0 snapshots and order-event ledger. The legacy
// ai_recommend_stocks row is optional display metadata only.
type CompatibilityCurrentRecommendationReader struct {
	database *gorm.DB
}

func NewCompatibilityCurrentRecommendationReader(database *gorm.DB) CompatibilityCurrentRecommendationReader {
	return CompatibilityCurrentRecommendationReader{database: database}
}

func (reader CompatibilityCurrentRecommendationReader) List(
	ctx context.Context,
	query portfolio.RecommendationQuery,
) ([]portfolio.CurrentRecommendation, error) {
	if err := validateCompatibilityRecommendationQuery(ctx, reader.database, query); err != nil {
		return nil, err
	}

	inputs, err := persistence.LoadFrozenStrategyInputsAsOf(
		ctx,
		reader.database,
		v150.StrategyVersion,
		query.Start,
		query.End,
		query.AsOf,
	)
	if err != nil {
		if errors.Is(err, persistence.ErrNoFrozenSnapshots) {
			return []portfolio.CurrentRecommendation{}, nil
		}
		return nil, fmt.Errorf("load frozen current recommendations: %w", err)
	}

	entries, eventsByRule, err := compatibilityFrozenRecommendationEntries(inputs, query.AsOf)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return []portfolio.CurrentRecommendation{}, nil
	}

	displays, err := reader.compatibilityRecommendationDisplays(ctx, entries)
	if err != nil {
		return nil, err
	}
	result := make([]portfolio.CurrentRecommendation, 0, len(entries))
	for _, entry := range entries {
		key := compatibilityRecommendationIdentity{
			runID:  entry.RunID,
			ruleID: entry.RuleID,
			symbol: entry.Symbol,
		}
		events := append([]portfolio.LedgerEvent(nil), eventsByRule[key]...)
		sort.Slice(events, func(i, j int) bool {
			if events[i].Sequence != events[j].Sequence {
				return events[i].Sequence < events[j].Sequence
			}
			return events[i].EventID < events[j].EventID
		})
		current, buildErr := portfolio.BuildCurrentRecommendation(entry, displays[key], events)
		if buildErr != nil {
			return nil, fmt.Errorf("derive frozen recommendation run=%s rule=%s: %w", entry.RunID, entry.RuleID, buildErr)
		}
		result = append(result, current)
	}

	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].Frozen, result[j].Frozen
		if !left.DecisionAt.Equal(right.DecisionAt) {
			return left.DecisionAt.Before(right.DecisionAt)
		}
		if left.RunID != right.RunID {
			return left.RunID < right.RunID
		}
		if left.Symbol != right.Symbol {
			return left.Symbol < right.Symbol
		}
		return left.RuleID < right.RuleID
	})
	return result, nil
}

type compatibilityRecommendationIdentity struct {
	runID  string
	ruleID string
	symbol string
}

type compatibilityCandidateIdentity struct {
	runID       string
	candidateID string
}

func validateCompatibilityRecommendationQuery(
	ctx context.Context,
	database *gorm.DB,
	query portfolio.RecommendationQuery,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", portfolio.ErrInvalidRecommendationQuery)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if database == nil {
		return fmt.Errorf("%w: database is nil", portfolio.ErrInvalidRecommendationQuery)
	}
	if query.StrategyVersion != v150.StrategyVersion ||
		query.Start.IsZero() || query.End.IsZero() || query.AsOf.IsZero() || query.End.Before(query.Start) {
		return fmt.Errorf(
			"%w: exact strategy %s and ordered start/end/asOf are required",
			portfolio.ErrInvalidRecommendationQuery,
			v150.StrategyVersion,
		)
	}
	return nil
}

func compatibilityFrozenRecommendationEntries(
	inputs persistence.FrozenStrategyInputs,
	asOf time.Time,
) (
	[]portfolio.FrozenRecommendation,
	map[compatibilityRecommendationIdentity][]portfolio.LedgerEvent,
	error,
) {
	runs := make(map[string]models.StrategyRunSnapshot, len(inputs.Runs))
	wantConfigHash := v150.FixedStrategyV150ConfigHash()
	for _, run := range inputs.Runs {
		if run.StrategyVersion != v150.StrategyVersion || run.ConfigHash != wantConfigHash {
			return nil, nil, fmt.Errorf(
				"%w: run %s has strategy/config %q/%q, want %q/%q",
				portfolio.ErrInvalidFrozenRecommendation,
				run.RunID,
				run.StrategyVersion,
				run.ConfigHash,
				v150.StrategyVersion,
				wantConfigHash,
			)
		}
		if _, duplicate := runs[run.RunID]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate frozen run %s", portfolio.ErrInvalidFrozenRecommendation, run.RunID)
		}
		runs[run.RunID] = run
	}

	candidates := make(map[compatibilityCandidateIdentity]models.CandidateSnapshot, len(inputs.Candidates))
	for _, candidate := range inputs.Candidates {
		key := compatibilityCandidateIdentity{runID: candidate.RunID, candidateID: candidate.CandidateID}
		if candidate.StrategyVersion != v150.StrategyVersion {
			return nil, nil, fmt.Errorf("%w: candidate %s has a foreign strategy version", portfolio.ErrInvalidFrozenRecommendation, candidate.CandidateID)
		}
		if _, exists := runs[candidate.RunID]; !exists {
			return nil, nil, fmt.Errorf("%w: candidate %s has no frozen run", portfolio.ErrInvalidFrozenRecommendation, candidate.CandidateID)
		}
		if _, duplicate := candidates[key]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate frozen candidate %s", portfolio.ErrInvalidFrozenRecommendation, candidate.CandidateID)
		}
		candidates[key] = candidate
	}

	rules := make(map[compatibilityRecommendationIdentity]models.RuleSnapshot, len(inputs.Rules))
	entries := make([]portfolio.FrozenRecommendation, 0, len(inputs.Rules))
	for _, rule := range inputs.Rules {
		run, runExists := runs[rule.RunID]
		candidate, candidateExists := candidates[compatibilityCandidateIdentity{runID: rule.RunID, candidateID: rule.CandidateID}]
		symbol := normalizeRecommendStockCode(rule.Symbol)
		if !runExists || rule.StrategyVersion != v150.StrategyVersion || strings.TrimSpace(rule.CandidateID) == "" ||
			!candidateExists || symbol == "" || normalizeRecommendStockCode(candidate.Symbol) != symbol {
			return nil, nil, fmt.Errorf(
				"%w: rule %s is not closed over one same-run, same-symbol candidate",
				portfolio.ErrInvalidFrozenRecommendation,
				rule.RuleID,
			)
		}
		key := compatibilityRecommendationIdentity{runID: rule.RunID, ruleID: rule.RuleID, symbol: symbol}
		if _, duplicate := rules[key]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate frozen rule %s", portfolio.ErrInvalidFrozenRecommendation, rule.RuleID)
		}
		rules[key] = rule
		if !strings.EqualFold(strings.TrimSpace(rule.RuleType), "entry") {
			continue
		}

		if run.FrozenAt == nil || candidate.FrozenAt == nil || rule.FrozenAt == nil {
			return nil, nil, fmt.Errorf("%w: rule %s has an incomplete snapshot seal", portfolio.ErrInvalidFrozenRecommendation, rule.RuleID)
		}
		if run.FrozenAt.After(asOf) || candidate.FrozenAt.After(asOf) || rule.FrozenAt.After(asOf) {
			continue
		}
		entries = append(entries, portfolio.FrozenRecommendation{
			RunID:           run.RunID,
			RuleID:          rule.RuleID,
			CandidateID:     candidate.CandidateID,
			StrategyVersion: v150.StrategyVersion,
			Symbol:          symbol,
			Name:            strings.TrimSpace(candidate.Name),
			Sector:          strings.TrimSpace(candidate.Sector),
			DecisionAt:      run.DecisionAt,
			ValidFromAt:     rule.ValidFromAt,
			Identity: portfolio.FrozenRecommendationIdentity{
				RunSnapshotHash:       run.SnapshotHash,
				RuleSnapshotHash:      rule.SnapshotHash,
				CandidateSnapshotHash: candidate.SnapshotHash,
				RunFrozenAt:           *run.FrozenAt,
				RuleFrozenAt:          *rule.FrozenAt,
				CandidateFrozenAt:     *candidate.FrozenAt,
			},
		})
	}

	eventsByRule := make(map[compatibilityRecommendationIdentity][]portfolio.LedgerEvent, len(entries))
	for _, row := range inputs.OrderEvents {
		if strings.EqualFold(strings.TrimSpace(row.EventType), "no_trade") {
			continue
		}
		symbol := normalizeRecommendStockCode(row.Symbol)
		key := compatibilityRecommendationIdentity{runID: row.RunID, ruleID: row.RuleID, symbol: symbol}
		rule, exists := rules[key]
		if !exists || row.StrategyVersion != v150.StrategyVersion || symbol != normalizeRecommendStockCode(rule.Symbol) {
			return nil, nil, fmt.Errorf("%w: event %s has no matching frozen rule identity", portfolio.ErrInvalidRecommendationLedger, row.EventID)
		}
		if row.FrozenAt == nil {
			return nil, nil, fmt.Errorf("%w: event %s is not frozen", portfolio.ErrInvalidRecommendationLedger, row.EventID)
		}
		if row.EventAt.After(asOf) || row.FrozenAt.After(asOf) {
			continue
		}
		eventsByRule[key] = append(eventsByRule[key], portfolio.LedgerEvent{
			EventID: row.EventID, RunID: row.RunID, RuleID: row.RuleID,
			StrategyVersion: row.StrategyVersion, TradeDate: row.TradeDate, Symbol: symbol,
			EventType: row.EventType, Sequence: row.Sequence, EventAt: row.EventAt,
			Price: row.Price, Quantity: row.Quantity, CashAmount: row.CashAmount,
			AdjustmentFactor: row.AdjustmentFactor, Fees: row.Fees, Reason: row.Reason,
			SnapshotHash: row.SnapshotHash, FrozenAt: *row.FrozenAt,
		})
	}
	return entries, eventsByRule, nil
}

type compatibilityDisplayMatch struct {
	count   int
	display portfolio.DisplayMetadata
}

func (reader CompatibilityCurrentRecommendationReader) compatibilityRecommendationDisplays(
	ctx context.Context,
	entries []portfolio.FrozenRecommendation,
) (map[compatibilityRecommendationIdentity]*portfolio.DisplayMetadata, error) {
	result := make(map[compatibilityRecommendationIdentity]*portfolio.DisplayMetadata, len(entries))
	if len(entries) == 0 || !reader.database.WithContext(ctx).Migrator().HasTable(&models.AiRecommendStocks{}) {
		return result, nil
	}

	visible := make(map[compatibilityRecommendationIdentity]struct{}, len(entries))
	runSet := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key := compatibilityRecommendationIdentity{runID: entry.RunID, ruleID: entry.RuleID, symbol: entry.Symbol}
		visible[key] = struct{}{}
		runSet[entry.RunID] = struct{}{}
	}
	runIDs := make([]string, 0, len(runSet))
	for runID := range runSet {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)

	rows := make([]models.AiRecommendStocks, 0)
	if err := reader.database.WithContext(ctx).
		Model(&models.AiRecommendStocks{}).
		Select("id", "provider_name", "model_name", "stock_code", "summary_version", "strategy_run_id", "strategy_rule_id").
		Where("summary_version = ? AND strategy_run_id IN ?", v150.StrategyVersion, runIDs).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load optional recommendation display metadata: %w", err)
	}

	matches := make(map[compatibilityRecommendationIdentity]compatibilityDisplayMatch, len(rows))
	for _, row := range rows {
		if row.SummaryVersion != v150.StrategyVersion {
			continue
		}
		key := compatibilityRecommendationIdentity{
			runID:  row.StrategyRunID,
			ruleID: row.StrategyRuleID,
			symbol: normalizeRecommendStockCode(row.StockCode),
		}
		if _, exists := visible[key]; !exists {
			continue
		}
		match := matches[key]
		match.count++
		if match.count == 1 {
			match.display = portfolio.DisplayMetadata{
				RecommendID: row.ID,
				Provider:    strings.TrimSpace(row.ProviderName),
				Model:       strings.TrimSpace(row.ModelName),
			}
		}
		matches[key] = match
	}
	for key, match := range matches {
		if match.count != 1 {
			continue
		}
		display := match.display
		result[key] = &display
	}
	return result, nil
}

var _ portfolio.CurrentRecommendationReader = CompatibilityCurrentRecommendationReader{}
