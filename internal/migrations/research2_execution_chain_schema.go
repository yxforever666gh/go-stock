package migrations

import (
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/research2"

	"gorm.io/gorm"
)

const research2LegacyUnversioned = "legacy-unversioned"

var research2ExecutionChainIndexes = []struct {
	name    string
	table   string
	columns string
	unique  bool
}{
	{"idx_research2_execution_chains_chain_id", "research2_execution_chains", "chain_id", true},
	{"idx_research2_execution_chains_trading_date", "research2_execution_chains", "trading_date", true},
	{"idx_research2_execution_chains_status", "research2_execution_chains", "status", false},
	{"idx_research2_analysis_runs_chain_id", "research2_analysis_runs", "chain_id", false},
	{"idx_research2_analysis_runs_parent_run_id", "research2_analysis_runs", "parent_run_id", false},
	{"idx_research2_analysis_runs_trigger_source", "research2_analysis_runs", "trigger_source", false},
	{"idx_research2_recommendations_role_rank", "research2_recommendations", "selection_role, selection_rank", false},
	{"idx_research2_recommendations_replaces_recommendation_id", "research2_recommendations", "replaces_recommendation_id", false},
	{"idx_research2_recommendations_execution_failure_code", "research2_recommendations", "execution_failure_code", false},
	{"idx_research2_recommendations_execution_quote_at", "research2_recommendations", "execution_quote_at", false},
}

func mainMigrationV26Definition() string {
	return strings.Join([]string{
		"research2_execution_chains with unique chain_id and trading_date plus status index",
		"research2_analysis_runs chain_id, parent_run_id, trigger_source, requested_slots, primary_count, standby_count",
		"research2_recommendations selection_role, selection_rank, replaces_recommendation_id, promotion_reason",
		"research2_recommendations execution_failure_code, execution_quote_price, execution_quote_at, execution_limit_price, execution_limit_distance_pct",
		"legacy run trigger_source and recommendation selection_role backfilled as legacy-unversioned",
		"no historical execution chains are synthesized",
		"no account, trade, recommendation result, cash, fee, P&L or return history is rewritten",
	}, "\n")
}

func applyResearch2ExecutionChainSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, model := range []any{&research2.AnalysisRun{}, &research2.Recommendation{}} {
		if !tx.Migrator().HasTable(model) {
			return fmt.Errorf("research2 execution-chain prerequisite table for %T is unavailable", model)
		}
	}
	if err := tx.AutoMigrate(&research2.ExecutionChain{}); err != nil {
		return fmt.Errorf("create research2 execution-chain table: %w", err)
	}
	fields := []struct {
		model any
		name  string
	}{
		{&research2.AnalysisRun{}, "ChainID"},
		{&research2.AnalysisRun{}, "ParentRunID"},
		{&research2.AnalysisRun{}, "TriggerSource"},
		{&research2.AnalysisRun{}, "RequestedSlots"},
		{&research2.AnalysisRun{}, "PrimaryCount"},
		{&research2.AnalysisRun{}, "StandbyCount"},
		{&research2.Recommendation{}, "SelectionRole"},
		{&research2.Recommendation{}, "SelectionRank"},
		{&research2.Recommendation{}, "ReplacesRecommendationID"},
		{&research2.Recommendation{}, "PromotionReason"},
		{&research2.Recommendation{}, "ExecutionFailureCode"},
		{&research2.Recommendation{}, "ExecutionQuotePrice"},
		{&research2.Recommendation{}, "ExecutionQuoteAt"},
		{&research2.Recommendation{}, "ExecutionLimitPrice"},
		{&research2.Recommendation{}, "ExecutionLimitDistancePct"},
	}
	for _, field := range fields {
		if tx.Migrator().HasColumn(field.model, field.name) {
			continue
		}
		if err := tx.Migrator().AddColumn(field.model, field.name); err != nil {
			return fmt.Errorf("add %T.%s: %w", field.model, field.name, err)
		}
	}
	for _, index := range research2ExecutionChainIndexes {
		unique := ""
		if index.unique {
			unique = "UNIQUE "
		}
		statement := "CREATE " + unique + "INDEX IF NOT EXISTS " + quoteSQLiteIdentifier(index.name) +
			" ON " + quoteSQLiteIdentifier(index.table) + "(" + index.columns + ")"
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create research2 execution-chain index %s: %w", index.name, err)
		}
	}

	// Use raw updates so adding provenance labels does not change historical
	// UpdatedAt values or trigger any model hooks.
	if err := tx.Exec(`UPDATE research2_analysis_runs
SET trigger_source = ?
WHERE trigger_source IS NULL OR TRIM(trigger_source) = ''`, research2LegacyUnversioned).Error; err != nil {
		return fmt.Errorf("backfill research2 legacy trigger source: %w", err)
	}
	if err := tx.Exec(`UPDATE research2_recommendations
SET selection_role = ?
WHERE selection_role IS NULL OR TRIM(selection_role) = ''`, research2LegacyUnversioned).Error; err != nil {
		return fmt.Errorf("backfill research2 legacy selection role: %w", err)
	}
	return verifyMainSchema26Runtime(tx)
}

func verifyMainSchema26Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	if !database.Migrator().HasTable(&research2.ExecutionChain{}) {
		return errors.New("main schema 26 research2_execution_chains is missing")
	}
	fields := []struct {
		model any
		name  string
	}{
		{&research2.AnalysisRun{}, "ChainID"},
		{&research2.AnalysisRun{}, "ParentRunID"},
		{&research2.AnalysisRun{}, "TriggerSource"},
		{&research2.AnalysisRun{}, "RequestedSlots"},
		{&research2.AnalysisRun{}, "PrimaryCount"},
		{&research2.AnalysisRun{}, "StandbyCount"},
		{&research2.Recommendation{}, "SelectionRole"},
		{&research2.Recommendation{}, "SelectionRank"},
		{&research2.Recommendation{}, "ReplacesRecommendationID"},
		{&research2.Recommendation{}, "PromotionReason"},
		{&research2.Recommendation{}, "ExecutionFailureCode"},
		{&research2.Recommendation{}, "ExecutionQuotePrice"},
		{&research2.Recommendation{}, "ExecutionQuoteAt"},
		{&research2.Recommendation{}, "ExecutionLimitPrice"},
		{&research2.Recommendation{}, "ExecutionLimitDistancePct"},
	}
	for _, field := range fields {
		if !database.Migrator().HasColumn(field.model, field.name) {
			return fmt.Errorf("main schema 26 %T.%s is missing", field.model, field.name)
		}
	}
	for _, index := range research2ExecutionChainIndexes {
		if !database.Migrator().HasIndex(index.table, index.name) {
			return fmt.Errorf("main schema 26 index %s is missing", index.name)
		}
	}
	checks := []struct {
		table string
		where string
		name  string
	}{
		{"research2_analysis_runs", "trigger_source IS NULL OR TRIM(trigger_source) = ''", "run trigger source"},
		{"research2_recommendations", "selection_role IS NULL OR TRIM(selection_role) = ''", "recommendation selection role"},
	}
	for _, check := range checks {
		var count int64
		if err := database.Table(check.table).Where(check.where).Count(&count).Error; err != nil {
			return fmt.Errorf("verify main schema 26 %s: %w", check.name, err)
		}
		if count != 0 {
			return fmt.Errorf("main schema 26 has %d rows without %s", count, check.name)
		}
	}
	return nil
}
