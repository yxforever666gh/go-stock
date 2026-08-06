package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"

	"gorm.io/gorm"
)

var (
	ErrInvalidImmutableRecord = errors.New("invalid immutable strategy record")
	ErrImmutableConflict      = errors.New("immutable strategy record already exists with different content")
	ErrNoFrozenSnapshots      = errors.New("no frozen strategy snapshots found")
	ErrIncompleteSnapshots    = errors.New("frozen strategy snapshot set is incomplete")
)

// StrategySnapshotBundle is one atomic append to the immutable strategy cache.
// Existing rows are never updated or deleted by this package.
type StrategySnapshotBundle struct {
	Run              models.StrategyRunSnapshot
	Candidates       []models.CandidateSnapshot
	Rules            []models.RuleSnapshot
	OrderEvents      []models.OrderEvent
	SecurityMaster   []models.SecurityMasterHistory
	CorporateActions []models.CorporateActionEvent
}

// FrozenStrategyInputs is the complete local-only input set consumed by an
// offline backtest. Every returned row has a non-null FrozenAt value.
type FrozenStrategyInputs struct {
	Runs             []models.StrategyRunSnapshot   `json:"runs"`
	Candidates       []models.CandidateSnapshot     `json:"candidates"`
	Rules            []models.RuleSnapshot          `json:"rules"`
	OrderEvents      []models.OrderEvent            `json:"orderEvents"`
	SecurityMaster   []models.SecurityMasterHistory `json:"securityMaster"`
	CorporateActions []models.CorporateActionEvent  `json:"corporateActions"`
}

type BacktestResult struct {
	Run     models.BacktestRun
	Trades  []models.Trade
	Metrics []models.Metric
}

func MigrateStrategyPersistence(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("%w: database is nil", ErrInvalidImmutableRecord)
	}
	// Releases before 1.5.0 previewed a run-global order-event sequence. Audit
	// the existing immutable facts before changing that uniqueness boundary;
	// rows are never rewritten as part of this migration.
	if database.Migrator().HasTable(&models.OrderEvent{}) {
		if err := auditOrderEventLedgerIndexMigration(database); err != nil {
			return err
		}
	}
	if err := database.AutoMigrate(models.StrategyPersistenceModels()...); err != nil {
		return err
	}
	if err := migrateOrderEventLedgerIndexes(database); err != nil {
		return err
	}
	return installImmutableStrategyTriggers(database)
}

const (
	legacyOrderEventSequenceIndex = "idx_strategy_order_run_sequence"
	orderEventRuleSequenceIndex   = "idx_strategy_order_run_rule_sequence"
	orderEventNoTradeRunIndex     = "idx_strategy_order_no_trade_run"
)

func auditOrderEventLedgerIndexMigration(database *gorm.DB) error {
	var duplicateRuleSequences int64
	if err := database.Raw(`
		SELECT COUNT(*) FROM (
			SELECT run_id, COALESCE(rule_id, ''), sequence
			FROM strategy_order_event
			GROUP BY run_id, COALESCE(rule_id, ''), sequence
			HAVING COUNT(*) > 1
		) AS duplicate_rule_sequences`).Scan(&duplicateRuleSequences).Error; err != nil {
		return fmt.Errorf("audit order-event per-rule sequence uniqueness: %w", err)
	}
	if duplicateRuleSequences != 0 {
		return fmt.Errorf("%w: order-event ledger has %d duplicate run/rule/sequence groups", ErrImmutableConflict, duplicateRuleSequences)
	}

	var invalidRuleOwnership int64
	if err := database.Raw(`
		SELECT COUNT(*)
		FROM strategy_order_event
		WHERE (LOWER(TRIM(event_type)) = 'no_trade' AND TRIM(COALESCE(rule_id, '')) <> '')
		   OR (LOWER(TRIM(event_type)) <> 'no_trade' AND TRIM(COALESCE(rule_id, '')) = '')`).Scan(&invalidRuleOwnership).Error; err != nil {
		return fmt.Errorf("audit order-event rule ownership: %w", err)
	}
	if invalidRuleOwnership != 0 {
		return fmt.Errorf("%w: order-event ledger has %d events with invalid rule ownership", ErrInvalidImmutableRecord, invalidRuleOwnership)
	}

	var mixedNoTradeRuns int64
	if err := database.Raw(`
		SELECT COUNT(*) FROM (
			SELECT run_id
			FROM strategy_order_event
			GROUP BY run_id
			HAVING SUM(CASE WHEN LOWER(TRIM(event_type)) = 'no_trade' THEN 1 ELSE 0 END) > 0
			   AND COUNT(*) > 1
		) AS mixed_no_trade_runs`).Scan(&mixedNoTradeRuns).Error; err != nil {
		return fmt.Errorf("audit no-trade ledger isolation: %w", err)
	}
	if mixedNoTradeRuns != 0 {
		return fmt.Errorf("%w: order-event ledger has %d runs mixing no_trade with other events", ErrInvalidImmutableRecord, mixedNoTradeRuns)
	}
	return nil
}

func migrateOrderEventLedgerIndexes(database *gorm.DB) error {
	if database.Migrator().HasIndex(&models.OrderEvent{}, legacyOrderEventSequenceIndex) {
		if err := database.Migrator().DropIndex(&models.OrderEvent{}, legacyOrderEventSequenceIndex); err != nil {
			return fmt.Errorf("drop legacy run-global order-event sequence index: %w", err)
		}
	}
	if !database.Migrator().HasIndex(&models.OrderEvent{}, orderEventRuleSequenceIndex) {
		if err := database.Migrator().CreateIndex(&models.OrderEvent{}, orderEventRuleSequenceIndex); err != nil {
			return fmt.Errorf("create per-rule order-event sequence index: %w", err)
		}
	}
	// no_trade has no RuleID, so a partial index makes its run-level singleton
	// invariant explicit without imposing a sequence namespace on real rules.
	statement := fmt.Sprintf(
		"CREATE UNIQUE INDEX IF NOT EXISTS %s ON strategy_order_event (run_id) WHERE LOWER(TRIM(event_type)) = 'no_trade'",
		orderEventNoTradeRunIndex,
	)
	if err := database.Exec(statement).Error; err != nil {
		return fmt.Errorf("create no-trade run index: %w", err)
	}
	return nil
}

func installImmutableStrategyTriggers(database *gorm.DB) error {
	tables := []string{
		"strategy_run_snapshot",
		"strategy_candidate_snapshot",
		"strategy_rule_snapshot",
		"strategy_order_event",
		"security_master_history",
		"corporate_action_event",
		"strategy_backtest_run",
		"strategy_backtest_trade",
		"strategy_backtest_metric",
	}
	for _, table := range tables {
		for _, operation := range []string{"UPDATE", "DELETE"} {
			name := "immutable_" + table + "_" + strings.ToLower(operation)
			statement := fmt.Sprintf(
				"CREATE TRIGGER IF NOT EXISTS %s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'immutable table %s'); END",
				name, operation, table, table,
			)
			if err := database.Exec(statement).Error; err != nil {
				return fmt.Errorf("install immutable trigger %s: %w", name, err)
			}
		}
	}
	return nil
}

// AppendStrategySnapshotBundle atomically inserts a frozen run and all of its
// immutable children. A duplicate identity is an error; callers must create a
// new identity for a revised snapshot.
func AppendStrategySnapshotBundle(ctx context.Context, database *gorm.DB, bundle StrategySnapshotBundle) error {
	if database == nil {
		return fmt.Errorf("%w: database is nil", ErrInvalidImmutableRecord)
	}
	bundle.Run.CandidateCount = len(bundle.Candidates)
	bundle.Run.RuleCount = len(bundle.Rules)
	bundle.Run.OrderEventCount = len(bundle.OrderEvents)
	bundle.Run.SecuritySnapshotCount = len(bundle.SecurityMaster)
	bundle.Run.CorporateActionCount = len(bundle.CorporateActions)
	if err := validateStrategySnapshotBundle(bundle); err != nil {
		return err
	}
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&bundle.Run).Error; err != nil {
			return fmt.Errorf("append strategy run %q: %w", bundle.Run.RunID, err)
		}
		if len(bundle.Candidates) > 0 {
			if err := tx.CreateInBatches(&bundle.Candidates, 100).Error; err != nil {
				return fmt.Errorf("append candidate snapshots: %w", err)
			}
		}
		if len(bundle.Rules) > 0 {
			if err := tx.CreateInBatches(&bundle.Rules, 100).Error; err != nil {
				return fmt.Errorf("append rule snapshots: %w", err)
			}
		}
		if len(bundle.OrderEvents) > 0 {
			if err := tx.CreateInBatches(&bundle.OrderEvents, 100).Error; err != nil {
				return fmt.Errorf("append order events: %w", err)
			}
		}
		if len(bundle.SecurityMaster) > 0 {
			if err := tx.CreateInBatches(&bundle.SecurityMaster, 100).Error; err != nil {
				return fmt.Errorf("append security master history: %w", err)
			}
		}
		if len(bundle.CorporateActions) > 0 {
			if err := tx.CreateInBatches(&bundle.CorporateActions, 100).Error; err != nil {
				return fmt.Errorf("append corporate action events: %w", err)
			}
		}
		return nil
	})
}

func validateStrategySnapshotBundle(bundle StrategySnapshotBundle) error {
	run := bundle.Run
	if strings.TrimSpace(run.RunID) == "" || strings.TrimSpace(run.StrategyVersion) == "" || strings.TrimSpace(run.TradeDate) == "" {
		return fmt.Errorf("%w: run id, strategy version and trade date are required", ErrInvalidImmutableRecord)
	}
	if _, err := time.Parse(time.DateOnly, run.TradeDate); err != nil {
		return fmt.Errorf("%w: invalid run trade date %q", ErrInvalidImmutableRecord, run.TradeDate)
	}
	if run.StartedAt.IsZero() || run.AsOf.IsZero() || run.DataCutoffAt.IsZero() || run.DecisionAt.IsZero() || run.GeneratedAt.IsZero() || run.FrozenAt == nil || run.FrozenAt.IsZero() {
		return fmt.Errorf("%w: run timestamps and frozenAt are required", ErrInvalidImmutableRecord)
	}
	if run.DataCutoffAt.Before(run.StartedAt) || run.DataCutoffAt.Before(run.AsOf) || run.DecisionAt.Before(run.DataCutoffAt) {
		return fmt.Errorf("%w: run time causality requires started/asOf <= data cutoff <= decision", ErrInvalidImmutableRecord)
	}
	if run.GeneratedAt.Before(run.DecisionAt) || run.FrozenAt.Before(run.GeneratedAt) {
		return fmt.Errorf("%w: run time causality requires decision <= generated <= frozen", ErrInvalidImmutableRecord)
	}
	if run.ValidFromAt != nil {
		if run.ValidFromAt.IsZero() || !run.ValidFromAt.After(run.DecisionAt) {
			return fmt.Errorf("%w: validFrom must be non-zero and strictly after decision", ErrInvalidImmutableRecord)
		}
	} else if len(bundle.Rules) > 0 {
		return fmt.Errorf("%w: a run with executable rules requires validFrom", ErrInvalidImmutableRecord)
	}
	if err := verifySnapshotRecord(run); err != nil {
		return fmt.Errorf("%w: run seal: %v", ErrInvalidImmutableRecord, err)
	}
	for i := range bundle.Candidates {
		row := &bundle.Candidates[i]
		if row.RunID != run.RunID || row.StrategyVersion != run.StrategyVersion || row.TradeDate != run.TradeDate {
			return fmt.Errorf("%w: candidate %d does not belong to run", ErrInvalidImmutableRecord, i)
		}
		if strings.TrimSpace(row.CandidateID) == "" || strings.TrimSpace(row.Symbol) == "" || !validFrozenPayload(row.SnapshotHash, row.PayloadJSON, row.FrozenAt) {
			return fmt.Errorf("%w: candidate %d is incomplete or not frozen", ErrInvalidImmutableRecord, i)
		}
		if err := verifySnapshotRecord(*row); err != nil {
			return fmt.Errorf("%w: candidate %d seal: %v", ErrInvalidImmutableRecord, i, err)
		}
	}
	for i := range bundle.Rules {
		row := &bundle.Rules[i]
		if row.RunID != run.RunID || row.StrategyVersion != run.StrategyVersion || row.TradeDate != run.TradeDate {
			return fmt.Errorf("%w: rule %d does not belong to run", ErrInvalidImmutableRecord, i)
		}
		if strings.TrimSpace(row.RuleID) == "" || strings.TrimSpace(row.Symbol) == "" || !validFrozenPayload(row.SnapshotHash, row.PayloadJSON, row.FrozenAt) {
			return fmt.Errorf("%w: rule %d is incomplete or not frozen", ErrInvalidImmutableRecord, i)
		}
		if err := verifySnapshotRecord(*row); err != nil {
			return fmt.Errorf("%w: rule %d seal: %v", ErrInvalidImmutableRecord, i, err)
		}
		if row.ValidFromAt.IsZero() || !row.ValidFromAt.After(run.DecisionAt) || (run.ValidFromAt != nil && row.ValidFromAt.Before(*run.ValidFromAt)) {
			return fmt.Errorf("%w: rule %d validFrom violates decision/run causality", ErrInvalidImmutableRecord, i)
		}
		if row.ExpiresAt != nil && (row.ExpiresAt.IsZero() || !row.ExpiresAt.After(row.ValidFromAt)) {
			return fmt.Errorf("%w: rule %d expiry must be after validFrom", ErrInvalidImmutableRecord, i)
		}
	}
	for i := range bundle.OrderEvents {
		row := &bundle.OrderEvents[i]
		if row.RunID != run.RunID || row.StrategyVersion != run.StrategyVersion || row.TradeDate != run.TradeDate {
			return fmt.Errorf("%w: order event %d does not belong to run", ErrInvalidImmutableRecord, i)
		}
		if strings.TrimSpace(row.EventID) == "" || strings.TrimSpace(row.Symbol) == "" || strings.TrimSpace(row.EventType) == "" || row.EventAt.IsZero() || !validFrozenPayload(row.SnapshotHash, row.PayloadJSON, row.FrozenAt) {
			return fmt.Errorf("%w: order event %d is incomplete or not frozen", ErrInvalidImmutableRecord, i)
		}
		if err := verifySnapshotRecord(*row); err != nil {
			return fmt.Errorf("%w: order event %d seal: %v", ErrInvalidImmutableRecord, i, err)
		}
	}
	if isExecutionSecurityObservationRun(run) {
		if len(bundle.Candidates) != 0 || len(bundle.Rules) != 0 || len(bundle.OrderEvents) != 0 || len(bundle.SecurityMaster) == 0 || len(bundle.CorporateActions) != 0 {
			return fmt.Errorf("%w: execution security observation must contain only security-master facts", ErrInvalidImmutableRecord)
		}
	} else if isExecutionCorporateActionObservationRun(run) {
		if len(bundle.Candidates) != 0 || len(bundle.Rules) != 0 || len(bundle.OrderEvents) != 0 || len(bundle.SecurityMaster) != 0 || len(bundle.CorporateActions) == 0 {
			return fmt.Errorf("%w: execution corporate-action observation must contain only corporate-action facts", ErrInvalidImmutableRecord)
		}
	} else if err := validateOrderEventStateMachine(run, bundle.Rules, bundle.OrderEvents); err != nil {
		return err
	}
	for i := range bundle.SecurityMaster {
		row := &bundle.SecurityMaster[i]
		if row.RunID != run.RunID || row.SnapshotVersion != run.StrategyVersion || strings.TrimSpace(row.RecordID) == "" || strings.TrimSpace(row.Symbol) == "" || row.EffectiveFrom.IsZero() || !validFrozenPayload(row.SnapshotHash, row.PayloadJSON, row.FrozenAt) {
			return fmt.Errorf("%w: security master row %d is incomplete or not frozen", ErrInvalidImmutableRecord, i)
		}
		if row.EffectiveFrom.After(run.DataCutoffAt) || (row.EffectiveTo != nil && (row.EffectiveTo.IsZero() || row.EffectiveTo.Before(row.EffectiveFrom))) {
			return fmt.Errorf("%w: security master row %d was not effective by run cutoff", ErrInvalidImmutableRecord, i)
		}
		if err := verifySnapshotRecord(*row); err != nil {
			return fmt.Errorf("%w: security master row %d seal: %v", ErrInvalidImmutableRecord, i, err)
		}
	}
	for i := range bundle.CorporateActions {
		row := &bundle.CorporateActions[i]
		if row.RunID != run.RunID || row.SnapshotVersion != run.StrategyVersion || strings.TrimSpace(row.EventID) == "" || strings.TrimSpace(row.Symbol) == "" || strings.TrimSpace(row.ActionType) == "" || row.AnnouncedAt == nil || row.AnnouncedAt.IsZero() || row.ExDate.IsZero() || !validFrozenPayload(row.SnapshotHash, row.PayloadJSON, row.FrozenAt) {
			return fmt.Errorf("%w: corporate action row %d is incomplete or not frozen", ErrInvalidImmutableRecord, i)
		}
		if row.AnnouncedAt.After(run.DataCutoffAt) {
			return fmt.Errorf("%w: corporate action row %d was announced after run cutoff", ErrInvalidImmutableRecord, i)
		}
		if isExecutionCorporateActionObservationRun(run) {
			if row.AvailableAt == nil || row.AvailableAt.IsZero() || row.AvailableAt.After(run.DataCutoffAt) ||
				row.SourceAt == nil || row.SourceAt.IsZero() || row.SourceAt.After(*row.AvailableAt) ||
				row.CoverageStart == nil || row.CoverageStart.IsZero() || row.CoverageEnd == nil || row.CoverageEnd.IsZero() || row.CoverageEnd.Before(*row.CoverageStart) {
				return fmt.Errorf("%w: corporate action row %d lacks a causal coverage window", ErrInvalidImmutableRecord, i)
			}
			switch strings.ToLower(strings.TrimSpace(row.ObservationStatus)) {
			case "ok", "empty", "failed":
			default:
				return fmt.Errorf("%w: corporate action row %d has invalid observation status %q", ErrInvalidImmutableRecord, i, row.ObservationStatus)
			}
		}
		if err := verifySnapshotRecord(*row); err != nil {
			return fmt.Errorf("%w: corporate action row %d seal: %v", ErrInvalidImmutableRecord, i, err)
		}
	}
	return nil
}

func validFrozenPayload(hash, payload string, frozenAt *time.Time) bool {
	return strings.TrimSpace(hash) != "" && strings.TrimSpace(payload) != "" && json.Valid([]byte(payload)) && frozenAt != nil && !frozenAt.IsZero()
}

// LoadFrozenStrategyInputs performs database reads only. It deliberately has
// no provider/client dependency, which keeps the backtest path cache-only.
func LoadFrozenStrategyInputs(ctx context.Context, database *gorm.DB, strategyVersion string, startDate, endDate time.Time) (FrozenStrategyInputs, error) {
	var result FrozenStrategyInputs
	if database == nil {
		return result, fmt.Errorf("%w: database is nil", ErrInvalidImmutableRecord)
	}
	strategyVersion = strings.TrimSpace(strategyVersion)
	if strategyVersion == "" || startDate.IsZero() || endDate.IsZero() || endDate.Before(startDate) {
		return result, fmt.Errorf("%w: version and ordered date range are required", ErrInvalidImmutableRecord)
	}
	startText := startDate.Format(time.DateOnly)
	endText := endDate.Format(time.DateOnly)
	dbq := database.WithContext(ctx)
	if err := dbq.Where("strategy_version = ? AND trade_date >= ? AND trade_date <= ? AND frozen_at IS NOT NULL", strategyVersion, startText, endText).
		Order("trade_date ASC, as_of ASC, run_id ASC").Find(&result.Runs).Error; err != nil {
		return result, err
	}
	if len(result.Runs) == 0 {
		return result, fmt.Errorf("%w: version=%s range=%s..%s", ErrNoFrozenSnapshots, strategyVersion, startText, endText)
	}
	runIDs := make([]string, 0, len(result.Runs))
	for _, run := range result.Runs {
		runIDs = append(runIDs, run.RunID)
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL", runIDs).
		Order("trade_date ASC, run_id ASC, rank ASC, symbol ASC").Find(&result.Candidates).Error; err != nil {
		return result, err
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL", runIDs).
		Order("trade_date ASC, run_id ASC, symbol ASC, path ASC, rule_id ASC").Find(&result.Rules).Error; err != nil {
		return result, err
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL", runIDs).
		Order("event_at ASC, event_type ASC, event_id ASC").Find(&result.OrderEvents).Error; err != nil {
		return result, err
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL", runIDs).
		Order("run_id ASC, symbol ASC, effective_from ASC, record_id ASC").Find(&result.SecurityMaster).Error; err != nil {
		return result, err
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL", runIDs).
		Order("run_id ASC, ex_date ASC, symbol ASC, event_id ASC").Find(&result.CorporateActions).Error; err != nil {
		return result, err
	}
	if err := validateFrozenChildCounts(result); err != nil {
		return result, err
	}
	if err := validateLoadedFrozenRuns(result); err != nil {
		return result, err
	}
	return result, nil
}

// LoadFrozenStrategyInputsAsOf returns the immutable input view that was
// available at asOf. A run and each of its initial children must already be
// sealed by the cutoff. Appended lifecycle events additionally have to be
// effective by the cutoff, so a later same-day run or event cannot leak into
// a point-in-time replay.
func LoadFrozenStrategyInputsAsOf(ctx context.Context, database *gorm.DB, strategyVersion string, startDate, endDate, asOf time.Time) (FrozenStrategyInputs, error) {
	var result FrozenStrategyInputs
	if database == nil {
		return result, fmt.Errorf("%w: database is nil", ErrInvalidImmutableRecord)
	}
	strategyVersion = strings.TrimSpace(strategyVersion)
	if strategyVersion == "" || startDate.IsZero() || endDate.IsZero() || endDate.Before(startDate) || asOf.IsZero() {
		return result, fmt.Errorf("%w: version, ordered date range and asOf are required", ErrInvalidImmutableRecord)
	}
	startText := startDate.Format(time.DateOnly)
	endText := endDate.Format(time.DateOnly)
	cutoff := asOf.UTC()
	dbq := database.WithContext(ctx)
	if err := dbq.Where(
		"strategy_version = ? AND trade_date >= ? AND trade_date <= ? AND frozen_at IS NOT NULL AND frozen_at <= ?",
		strategyVersion, startText, endText, cutoff,
	).Order("trade_date ASC, as_of ASC, run_id ASC").Find(&result.Runs).Error; err != nil {
		return result, err
	}
	if len(result.Runs) == 0 {
		return result, fmt.Errorf("%w: version=%s range=%s..%s asOf=%s", ErrNoFrozenSnapshots, strategyVersion, startText, endText, asOf.Format(time.RFC3339Nano))
	}
	runIDs := make([]string, 0, len(result.Runs))
	for _, run := range result.Runs {
		runIDs = append(runIDs, run.RunID)
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL AND frozen_at <= ?", runIDs, cutoff).
		Order("trade_date ASC, run_id ASC, rank ASC, symbol ASC").Find(&result.Candidates).Error; err != nil {
		return result, err
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL AND frozen_at <= ?", runIDs, cutoff).
		Order("trade_date ASC, run_id ASC, symbol ASC, path ASC, rule_id ASC").Find(&result.Rules).Error; err != nil {
		return result, err
	}
	var allOrderEvents []models.OrderEvent
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL", runIDs).
		Order("run_id ASC, rule_id ASC, sequence ASC, event_id ASC").Find(&allOrderEvents).Error; err != nil {
		return result, err
	}
	result.OrderEvents = frozenOrderEventPrefixesAsOf(allOrderEvents, cutoff)
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL AND frozen_at <= ?", runIDs, cutoff).
		Order("run_id ASC, symbol ASC, effective_from ASC, record_id ASC").Find(&result.SecurityMaster).Error; err != nil {
		return result, err
	}
	if err := dbq.Where("run_id IN ? AND frozen_at IS NOT NULL AND frozen_at <= ?", runIDs, cutoff).
		Order("run_id ASC, ex_date ASC, symbol ASC, event_id ASC").Find(&result.CorporateActions).Error; err != nil {
		return result, err
	}
	if err := validateFrozenChildCounts(result); err != nil {
		return result, err
	}
	if err := validateLoadedFrozenRuns(result); err != nil {
		return result, err
	}
	return result, nil
}

func frozenOrderEventPrefixesAsOf(events []models.OrderEvent, asOf time.Time) []models.OrderEvent {
	type ledgerKey struct {
		runID  string
		ruleID string
	}
	blocked := make(map[ledgerKey]bool)
	result := make([]models.OrderEvent, 0, len(events))
	for i := range events {
		event := events[i]
		key := ledgerKey{runID: event.RunID, ruleID: event.RuleID}
		if blocked[key] {
			continue
		}
		if event.EventAt.After(asOf) || event.FrozenAt == nil || event.FrozenAt.After(asOf) {
			blocked[key] = true
			continue
		}
		result = append(result, event)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].EventAt.Equal(result[j].EventAt) {
			return result[i].EventAt.Before(result[j].EventAt)
		}
		if result[i].EventType != result[j].EventType {
			return result[i].EventType < result[j].EventType
		}
		return result[i].EventID < result[j].EventID
	})
	return result
}

func validateLoadedFrozenRuns(inputs FrozenStrategyInputs) error {
	candidates := make(map[string][]models.CandidateSnapshot, len(inputs.Runs))
	rules := make(map[string][]models.RuleSnapshot, len(inputs.Runs))
	orders := make(map[string][]models.OrderEvent, len(inputs.Runs))
	security := make(map[string][]models.SecurityMasterHistory, len(inputs.Runs))
	actions := make(map[string][]models.CorporateActionEvent, len(inputs.Runs))
	for _, row := range inputs.Candidates {
		candidates[row.RunID] = append(candidates[row.RunID], row)
	}
	for _, row := range inputs.Rules {
		rules[row.RunID] = append(rules[row.RunID], row)
	}
	for _, row := range inputs.OrderEvents {
		orders[row.RunID] = append(orders[row.RunID], row)
	}
	for _, row := range inputs.SecurityMaster {
		security[row.RunID] = append(security[row.RunID], row)
	}
	for _, row := range inputs.CorporateActions {
		actions[row.RunID] = append(actions[row.RunID], row)
	}
	for _, run := range inputs.Runs {
		bundle := StrategySnapshotBundle{
			Run:              run,
			Candidates:       candidates[run.RunID],
			Rules:            rules[run.RunID],
			OrderEvents:      orders[run.RunID],
			SecurityMaster:   security[run.RunID],
			CorporateActions: actions[run.RunID],
		}
		if err := validateStrategySnapshotBundle(bundle); err != nil {
			return fmt.Errorf("%w: loaded run %s failed immutable validation: %v", ErrIncompleteSnapshots, run.RunID, err)
		}
	}
	return nil
}

func validateFrozenChildCounts(inputs FrozenStrategyInputs) error {
	candidateCounts := make(map[string]int, len(inputs.Runs))
	ruleCounts := make(map[string]int, len(inputs.Runs))
	orderCounts := make(map[string]int, len(inputs.Runs))
	securityCounts := make(map[string]int, len(inputs.Runs))
	actionCounts := make(map[string]int, len(inputs.Runs))
	for _, row := range inputs.Candidates {
		candidateCounts[row.RunID]++
	}
	for _, row := range inputs.Rules {
		ruleCounts[row.RunID]++
	}
	for _, row := range inputs.OrderEvents {
		orderCounts[row.RunID]++
	}
	for _, row := range inputs.SecurityMaster {
		securityCounts[row.RunID]++
	}
	for _, row := range inputs.CorporateActions {
		actionCounts[row.RunID]++
	}
	for _, run := range inputs.Runs {
		if candidateCounts[run.RunID] != run.CandidateCount || ruleCounts[run.RunID] != run.RuleCount || orderCounts[run.RunID] < run.OrderEventCount || securityCounts[run.RunID] != run.SecuritySnapshotCount || actionCounts[run.RunID] != run.CorporateActionCount {
			return fmt.Errorf("%w: run=%s expected candidates/rules/orders/security/actions=%d/%d/%d/%d/%d got=%d/%d/%d/%d/%d",
				ErrIncompleteSnapshots,
				run.RunID,
				run.CandidateCount,
				run.RuleCount,
				run.OrderEventCount,
				run.SecuritySnapshotCount,
				run.CorporateActionCount,
				candidateCounts[run.RunID],
				ruleCounts[run.RunID],
				orderCounts[run.RunID],
				securityCounts[run.RunID],
				actionCounts[run.RunID],
			)
		}
	}
	return nil
}

// FrozenStrategyInputHash returns a stable digest that excludes database IDs
// and insertion timestamps. Stable snapshot identities/hashes define the input.
func FrozenStrategyInputHash(inputs FrozenStrategyInputs) string {
	parts := make([]string, 0, len(inputs.Runs)+len(inputs.Candidates)+len(inputs.Rules)+len(inputs.OrderEvents)+len(inputs.SecurityMaster)+len(inputs.CorporateActions))
	for _, row := range inputs.Runs {
		parts = append(parts, frozenBusinessHashPart("run", row.RunID, row))
	}
	for _, row := range inputs.Candidates {
		parts = append(parts, frozenBusinessHashPart("candidate", row.CandidateID, row))
	}
	for _, row := range inputs.Rules {
		parts = append(parts, frozenBusinessHashPart("rule", row.RuleID, row))
	}
	for _, row := range inputs.OrderEvents {
		parts = append(parts, frozenBusinessHashPart("order", row.EventID, row))
	}
	for _, row := range inputs.SecurityMaster {
		parts = append(parts, frozenBusinessHashPart("security", row.RecordID, row))
	}
	for _, row := range inputs.CorporateActions {
		parts = append(parts, frozenBusinessHashPart("action", row.EventID, row))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func frozenBusinessHashPart(kind, identity string, row any) string {
	canonical, _, err := canonicalSnapshotRecord(row)
	if err != nil {
		return kind + "|" + identity + "|invalid|" + err.Error()
	}
	return kind + "|" + identity + "|" + string(canonical)
}

// PersistBacktestResult appends a completed result atomically. Repeating the
// exact same deterministic result is idempotent; different content under the
// same BacktestID is rejected and never overwrites the original.
func PersistBacktestResult(ctx context.Context, database *gorm.DB, result BacktestResult) (*models.BacktestRun, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrInvalidImmutableRecord)
	}
	result.Run.TradeCount = len(result.Trades)
	result.Run.MetricCount = len(result.Metrics)
	if err := validateBacktestResult(result); err != nil {
		return nil, err
	}
	var persisted models.BacktestRun
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("backtest_id = ?", result.Run.BacktestID).First(&persisted).Error
		switch {
		case err == nil:
			if backtestRunFingerprint(persisted) != backtestRunFingerprint(result.Run) {
				return fmt.Errorf("%w: backtest id %s", ErrImmutableConflict, result.Run.BacktestID)
			}
			var existingTrades []models.Trade
			if err := tx.Where("backtest_id = ?", result.Run.BacktestID).Order("sequence ASC, trade_id ASC").Find(&existingTrades).Error; err != nil {
				return err
			}
			var existingMetrics []models.Metric
			if err := tx.Where("backtest_id = ?", result.Run.BacktestID).Order("ordinal ASC, metric_id ASC").Find(&existingMetrics).Error; err != nil {
				return err
			}
			if backtestChildrenFingerprint(existingTrades, existingMetrics) != backtestChildrenFingerprint(result.Trades, result.Metrics) {
				return fmt.Errorf("%w: backtest child rows for %s", ErrImmutableConflict, result.Run.BacktestID)
			}
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}
		if err := tx.Create(&result.Run).Error; err != nil {
			return err
		}
		if len(result.Trades) > 0 {
			if err := tx.CreateInBatches(&result.Trades, 100).Error; err != nil {
				return err
			}
		}
		if len(result.Metrics) > 0 {
			if err := tx.CreateInBatches(&result.Metrics, 100).Error; err != nil {
				return err
			}
		}
		persisted = result.Run
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &persisted, nil
}

func validateBacktestResult(result BacktestResult) error {
	run := result.Run
	if strings.TrimSpace(run.BacktestID) == "" || strings.TrimSpace(run.StrategyVersion) == "" || strings.TrimSpace(run.InputHash) == "" || strings.TrimSpace(run.Status) == "" {
		return fmt.Errorf("%w: backtest identity, version, input hash and status are required", ErrInvalidImmutableRecord)
	}
	start, startErr := time.Parse(time.DateOnly, run.StartDate)
	end, endErr := time.Parse(time.DateOnly, run.EndDate)
	if startErr != nil || endErr != nil || end.Before(start) {
		return fmt.Errorf("%w: invalid backtest date range", ErrInvalidImmutableRecord)
	}
	if run.StartedAt.IsZero() || run.CompletedAt.IsZero() || run.FrozenAt == nil || run.FrozenAt.IsZero() || !json.Valid([]byte(run.SummaryJSON)) {
		return fmt.Errorf("%w: completed/frozen timestamps and valid summary JSON are required", ErrInvalidImmutableRecord)
	}
	for i := range result.Trades {
		row := &result.Trades[i]
		if row.BacktestID != run.BacktestID || row.StrategyVersion != run.StrategyVersion || strings.TrimSpace(row.TradeID) == "" || strings.TrimSpace(row.Symbol) == "" || row.EntryAt.IsZero() || !validFrozenPayload(row.SnapshotHash, row.PayloadJSON, row.FrozenAt) {
			return fmt.Errorf("%w: backtest trade %d is incomplete or mismatched", ErrInvalidImmutableRecord, i)
		}
		if err := validateBacktestTradeAccounting(*row); err != nil {
			return fmt.Errorf("%w: backtest trade %d: %v", ErrInvalidImmutableRecord, i, err)
		}
		if err := verifySnapshotRecord(*row); err != nil {
			return fmt.Errorf("%w: backtest trade %d seal: %v", ErrInvalidImmutableRecord, i, err)
		}
	}
	for i := range result.Metrics {
		row := &result.Metrics[i]
		if row.BacktestID != run.BacktestID || strings.TrimSpace(row.MetricID) == "" || strings.TrimSpace(row.Name) == "" || strings.TrimSpace(row.Scope) == "" || row.FrozenAt == nil || row.FrozenAt.IsZero() {
			return fmt.Errorf("%w: backtest metric %d is incomplete or mismatched", ErrInvalidImmutableRecord, i)
		}
	}
	return nil
}

func validateBacktestTradeAccounting(row models.Trade) error {
	if row.Sequence <= 0 {
		return errors.New("trade sequence must be positive")
	}
	if row.ExitAt != nil && (row.ExitAt.IsZero() || !row.ExitAt.After(row.EntryAt)) {
		return errors.New("closed trade exit must be after entry")
	}
	if row.FrozenAt == nil || row.FrozenAt.Before(row.EntryAt) || (row.ExitAt != nil && row.FrozenAt.Before(*row.ExitAt)) {
		return errors.New("trade frozenAt precedes its facts")
	}
	if !finitePositive(row.EntryPrice) || !finitePositive(row.ExitPrice) || !finitePositive(row.Quantity) || !finiteNonNegative(row.Fees) || !finiteNumber(row.GrossPnL) || !finiteNumber(row.NetPnL) || !finiteNumber(row.ReturnPct) {
		return errors.New("price, quantity and fees are invalid")
	}
	var payload struct {
		Status              string  `json:"status"`
		EntryFees           float64 `json:"entryFees"`
		ExitFees            float64 `json:"exitFees"`
		EntryCash           float64 `json:"entryCash"`
		CorporateActionCash float64 `json:"corporateActionCash"`
	}
	if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil || !finiteNonNegative(payload.EntryFees) || !finiteNonNegative(payload.ExitFees) || !finiteNonNegative(payload.CorporateActionCash) {
		return errors.New("trade payload must contain non-negative entryFees and exitFees")
	}
	if row.ExitAt == nil {
		if payload.Status != "open" || payload.ExitFees != 0 || row.ExitReason != "open_at_end" {
			return errors.New("open trade must carry open status, zero exit fees and open_at_end reason")
		}
	} else if payload.Status != "closed" && payload.Status != "" {
		return errors.New("closed trade payload status is invalid")
	}
	if !nearlyEqual(row.Fees, payload.EntryFees+payload.ExitFees) {
		return errors.New("total fees do not match persisted entry/exit fees")
	}
	// EntryPrice and Quantity are the post-corporate-action economic position,
	// while EntryCash is the immutable cash paid for the original fill.  Using
	// adjusted price * adjusted quantity as the cost basis silently loses cash
	// dividends (and can drift on rounded share changes), so reconcile against
	// the original notional persisted by the event replay instead.
	entryCash := payload.EntryCash
	if entryCash == 0 {
		// Compatibility for pre-corporate-action replay payloads.
		entryCash = row.EntryPrice*row.Quantity + payload.EntryFees
	}
	if !finitePositive(entryCash) || entryCash+1e-8 < payload.EntryFees {
		return errors.New("trade payload entryCash is invalid")
	}
	originalEntryNotional := entryCash - payload.EntryFees
	expectedGross := row.ExitPrice*row.Quantity + payload.CorporateActionCash - originalEntryNotional
	expectedNet := expectedGross - row.Fees
	if mathAbs(row.GrossPnL-expectedGross) > 1e-6 || mathAbs(row.NetPnL-expectedNet) > 1e-6 {
		return errors.New("gross or net PnL does not reconcile to frozen fills")
	}
	expectedReturn := expectedNet / entryCash * 100
	// One basis point is 0.01 percentage points. Replays normally match much
	// more closely; this bound is also the release reconciliation threshold.
	if mathAbs(row.ReturnPct-expectedReturn) > 0.01 {
		return errors.New("return differs from frozen-fill recomputation by more than 1bp")
	}
	return nil
}

func mathAbs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func backtestRunFingerprint(row models.BacktestRun) string {
	stable := struct {
		BacktestID             string
		StrategyVersion        string
		StartDate              string
		EndDate                string
		InputHash              string
		Status                 string
		RunSnapshotCount       int
		CandidateSnapshotCount int
		RuleSnapshotCount      int
		OrderEventCount        int
		SecuritySnapshotCount  int
		CorporateActionCount   int
		TradeCount             int
		MetricCount            int
		SummaryJSON            string
		StartedAt              time.Time
		CompletedAt            time.Time
		FrozenAt               *time.Time
	}{
		BacktestID:             row.BacktestID,
		StrategyVersion:        row.StrategyVersion,
		StartDate:              row.StartDate,
		EndDate:                row.EndDate,
		InputHash:              row.InputHash,
		Status:                 row.Status,
		RunSnapshotCount:       row.RunSnapshotCount,
		CandidateSnapshotCount: row.CandidateSnapshotCount,
		RuleSnapshotCount:      row.RuleSnapshotCount,
		OrderEventCount:        row.OrderEventCount,
		SecuritySnapshotCount:  row.SecuritySnapshotCount,
		CorporateActionCount:   row.CorporateActionCount,
		TradeCount:             row.TradeCount,
		MetricCount:            row.MetricCount,
		SummaryJSON:            row.SummaryJSON,
		StartedAt:              row.StartedAt.UTC(),
		CompletedAt:            row.CompletedAt.UTC(),
		FrozenAt:               normalizedTimePointer(row.FrozenAt),
	}
	raw, _ := json.Marshal(stable)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func backtestChildrenFingerprint(trades []models.Trade, metrics []models.Metric) string {
	parts := make([]string, 0, len(trades)+len(metrics))
	for _, row := range trades {
		exitAt := normalizedTimePointer(row.ExitAt)
		frozenAt := normalizedTimePointer(row.FrozenAt)
		stable := struct {
			TradeID             string
			BacktestID          string
			StrategyVersion     string
			Sequence            int
			Symbol              string
			EntryAt             time.Time
			ExitAt              *time.Time
			EntryPrice          float64
			ExitPrice           float64
			Quantity            float64
			Fees                float64
			GrossPnL            float64
			NetPnL              float64
			ReturnPct           float64
			ExitReason          string
			SourceOrderEventIDs string
			SnapshotHash        string
			PayloadJSON         string
			FrozenAt            *time.Time
		}{
			TradeID:             row.TradeID,
			BacktestID:          row.BacktestID,
			StrategyVersion:     row.StrategyVersion,
			Sequence:            row.Sequence,
			Symbol:              row.Symbol,
			EntryAt:             row.EntryAt.UTC(),
			ExitAt:              exitAt,
			EntryPrice:          row.EntryPrice,
			ExitPrice:           row.ExitPrice,
			Quantity:            row.Quantity,
			Fees:                row.Fees,
			GrossPnL:            row.GrossPnL,
			NetPnL:              row.NetPnL,
			ReturnPct:           row.ReturnPct,
			ExitReason:          row.ExitReason,
			SourceOrderEventIDs: row.SourceOrderEventIDs,
			SnapshotHash:        row.SnapshotHash,
			PayloadJSON:         row.PayloadJSON,
			FrozenAt:            frozenAt,
		}
		raw, _ := json.Marshal(stable)
		parts = append(parts, "trade|"+string(raw))
	}
	for _, row := range metrics {
		stable := struct {
			MetricID    string
			BacktestID  string
			Name        string
			Scope       string
			Value       float64
			ValueText   string
			Unit        string
			Ordinal     int
			PayloadJSON string
			FrozenAt    *time.Time
		}{
			MetricID:    row.MetricID,
			BacktestID:  row.BacktestID,
			Name:        row.Name,
			Scope:       row.Scope,
			Value:       row.Value,
			ValueText:   row.ValueText,
			Unit:        row.Unit,
			Ordinal:     row.Ordinal,
			PayloadJSON: row.PayloadJSON,
			FrozenAt:    normalizedTimePointer(row.FrozenAt),
		}
		raw, _ := json.Marshal(stable)
		parts = append(parts, "metric|"+string(raw))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func normalizedTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}
