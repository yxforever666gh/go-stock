package migrations

import (
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/research"

	"gorm.io/gorm"
)

const (
	legacyUnversioned              = "legacy-unversioned"
	legacyRecordedQuoteStatus      = "legacy-recorded"
	legacyUnavailableQuoteStatus   = "legacy-unavailable"
	reanalysisOpportunityIndex     = "idx_v3_opportunities_reanalysis"
	supersedingRunOpportunityIndex = "idx_v3_opportunities_superseding_run"
)

func mainMigrationV25Definition() string {
	return strings.Join([]string{
		"research_v160_analysis_runs.data_profile_version VARCHAR(64)",
		"research_v270_buy_opportunities.requested_action VARCHAR(16)",
		"research_v270_buy_opportunities.decision_quote_status VARCHAR(32)",
		"research_v270_buy_opportunities.reanalysis_at DATETIME",
		"research_v270_buy_opportunities.superseded_by_run_id VARCHAR(36)",
		"research_v270_buy_opportunities.data_profile_version VARCHAR(64)",
		"research_v160_lifecycle_observations.data_profile_version VARCHAR(64)",
		"research_v160_decision_events.decision_policy_version VARCHAR(64)",
		"blank Research Center 1 strategy/data/policy versions backfilled as legacy-unversioned",
		"historical requested_action copied from final action without changing action",
		"historical quote status classified as legacy-recorded or legacy-unavailable",
		"no account, trade, position, cash, fee, P&L or return row is rewritten",
	}, "\n")
}

func applyResearch1DataChainV3Schema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	models := []any{
		&research.AnalysisRun{},
		&research.BuyOpportunity{},
		&research.LifecycleObservation{},
		&research.DecisionEvent{},
	}
	for _, model := range models {
		if !tx.Migrator().HasTable(model) {
			return fmt.Errorf("research1 data-chain v3 prerequisite table for %T is unavailable", model)
		}
	}
	fields := []struct {
		model any
		name  string
	}{
		{&research.AnalysisRun{}, "DataProfileVersion"},
		{&research.BuyOpportunity{}, "RequestedAction"},
		{&research.BuyOpportunity{}, "DecisionQuoteStatus"},
		{&research.BuyOpportunity{}, "ReanalysisAt"},
		{&research.BuyOpportunity{}, "SupersededByRunID"},
		{&research.BuyOpportunity{}, "DataProfileVersion"},
		{&research.LifecycleObservation{}, "DataProfileVersion"},
		{&research.DecisionEvent{}, "DecisionPolicyVersion"},
	}
	for _, field := range fields {
		if tx.Migrator().HasColumn(field.model, field.name) {
			continue
		}
		if err := tx.Migrator().AddColumn(field.model, field.name); err != nil {
			return fmt.Errorf("add %T.%s: %w", field.model, field.name, err)
		}
	}

	if err := tx.Exec("CREATE INDEX IF NOT EXISTS " + reanalysisOpportunityIndex + " ON research_v270_buy_opportunities(reanalysis_at)").Error; err != nil {
		return fmt.Errorf("create opportunity reanalysis index: %w", err)
	}
	if err := tx.Exec("CREATE INDEX IF NOT EXISTS " + supersedingRunOpportunityIndex + " ON research_v270_buy_opportunities(superseded_by_run_id)").Error; err != nil {
		return fmt.Errorf("create opportunity superseding-run index: %w", err)
	}

	// Raw updates intentionally avoid GORM UpdatedAt callbacks. Schema 25 labels
	// legacy provenance only; it does not change the historical business data or
	// its modification timestamps.
	if err := tx.Exec(`UPDATE research_v160_analysis_runs
SET strategy_version = ?
WHERE strategy_version IS NULL OR TRIM(strategy_version) = ''`, legacyUnversioned).Error; err != nil {
		return fmt.Errorf("backfill legacy analysis strategy version: %w", err)
	}
	if err := tx.Exec(`UPDATE research_v160_analysis_runs
SET data_profile_version = ?
WHERE data_profile_version IS NULL OR TRIM(data_profile_version) = ''`, legacyUnversioned).Error; err != nil {
		return fmt.Errorf("backfill legacy analysis data profile: %w", err)
	}
	if err := tx.Exec(`UPDATE research_v270_buy_opportunities
SET requested_action = action
WHERE requested_action IS NULL OR TRIM(requested_action) = ''`).Error; err != nil {
		return fmt.Errorf("backfill legacy opportunity requested action: %w", err)
	}
	if err := tx.Exec(`UPDATE research_v270_buy_opportunities
SET decision_quote_status = CASE
  WHEN quote_price > 0 AND quote_at IS NOT NULL THEN ?
  ELSE ?
END
WHERE decision_quote_status IS NULL OR TRIM(decision_quote_status) = '' OR decision_quote_status = ?`,
		legacyRecordedQuoteStatus, legacyUnavailableQuoteStatus, legacyUnavailableQuoteStatus).Error; err != nil {
		return fmt.Errorf("backfill legacy opportunity quote status: %w", err)
	}
	if err := tx.Exec(`UPDATE research_v270_buy_opportunities
SET data_profile_version = ?
WHERE data_profile_version IS NULL OR TRIM(data_profile_version) = ''`, legacyUnversioned).Error; err != nil {
		return fmt.Errorf("backfill legacy opportunity data profile: %w", err)
	}
	if err := tx.Exec(`UPDATE research_v160_lifecycle_observations
SET data_profile_version = ?
WHERE data_profile_version IS NULL OR TRIM(data_profile_version) = ''`, legacyUnversioned).Error; err != nil {
		return fmt.Errorf("backfill legacy lifecycle data profile: %w", err)
	}
	if err := tx.Exec(`UPDATE research_v160_decision_events
SET decision_policy_version = ?
WHERE decision_policy_version IS NULL OR TRIM(decision_policy_version) = ''`, legacyUnversioned).Error; err != nil {
		return fmt.Errorf("backfill legacy lifecycle decision policy: %w", err)
	}
	return verifyMainSchema25Runtime(tx)
}

func verifyMainSchema25Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	fields := []struct {
		model any
		name  string
	}{
		{&research.AnalysisRun{}, "DataProfileVersion"},
		{&research.BuyOpportunity{}, "RequestedAction"},
		{&research.BuyOpportunity{}, "DecisionQuoteStatus"},
		{&research.BuyOpportunity{}, "ReanalysisAt"},
		{&research.BuyOpportunity{}, "SupersededByRunID"},
		{&research.BuyOpportunity{}, "DataProfileVersion"},
		{&research.LifecycleObservation{}, "DataProfileVersion"},
		{&research.DecisionEvent{}, "DecisionPolicyVersion"},
	}
	for _, field := range fields {
		if !database.Migrator().HasColumn(field.model, field.name) {
			return fmt.Errorf("main schema 25 %T.%s is missing", field.model, field.name)
		}
	}
	for _, index := range []string{reanalysisOpportunityIndex, supersedingRunOpportunityIndex} {
		if !database.Migrator().HasIndex(&research.BuyOpportunity{}, index) {
			return fmt.Errorf("main schema 25 buy-opportunity index %s is missing", index)
		}
	}

	checks := []struct {
		table string
		where string
		name  string
	}{
		{"research_v160_analysis_runs", "strategy_version IS NULL OR TRIM(strategy_version) = '' OR data_profile_version IS NULL OR TRIM(data_profile_version) = ''", "analysis provenance"},
		{"research_v270_buy_opportunities", "requested_action IS NULL OR TRIM(requested_action) = '' OR decision_quote_status IS NULL OR TRIM(decision_quote_status) = '' OR data_profile_version IS NULL OR TRIM(data_profile_version) = ''", "opportunity provenance"},
		{"research_v160_lifecycle_observations", "data_profile_version IS NULL OR TRIM(data_profile_version) = ''", "lifecycle data profile"},
		{"research_v160_decision_events", "decision_policy_version IS NULL OR TRIM(decision_policy_version) = ''", "decision policy"},
	}
	for _, check := range checks {
		var count int64
		if err := database.Table(check.table).Where(check.where).Count(&count).Error; err != nil {
			return fmt.Errorf("verify main schema 25 %s: %w", check.name, err)
		}
		if count != 0 {
			return fmt.Errorf("main schema 25 has %d rows with missing %s", count, check.name)
		}
	}
	return nil
}
