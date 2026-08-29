package migrations

import (
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/models"
	"go-stock/backend/research"

	"gorm.io/gorm"
)

const (
	defaultTargetCapitalUtilization  = 0.90
	defaultMaxImmediateBuysPerRun    = 2
	defaultReanalysisIntervalMinutes = 30
)

func mainMigrationV21Definition() string {
	return strings.Join([]string{
		"settings.ai_capital_deployment_enabled BOOLEAN NOT NULL DEFAULT 1 (initial value copied once from ai_analysis_enabled)",
		"settings.ai_target_capital_utilization REAL NOT NULL DEFAULT 0.90",
		"settings.ai_max_immediate_buys_per_run INTEGER NOT NULL DEFAULT 2",
		"settings.ai_reanalysis_interval_minutes INTEGER NOT NULL DEFAULT 30",
		"settings.ai_analysis_enabled forced to 0; settings.ai_analysis_times retained unchanged",
		"research_v270_analysis_triggers",
		"research_v270_buy_opportunities",
		"research_v160_analysis_runs trigger, funding snapshot, lease and decision-count fields",
		"research_v160_recommendations.opportunity_id",
	}, "\n")
}

func applyEventDrivenCapitalDeploymentSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, model := range []any{&models.Settings{}, &research.AnalysisRun{}, &research.Recommendation{}} {
		if !tx.Migrator().HasTable(model) {
			return fmt.Errorf("event-driven capital deployment prerequisite table for %T is unavailable", model)
		}
	}

	// Remember whether this is the one upgrade pass that introduces the
	// switch. A repeated verifier/repair pass must not copy the legacy value
	// again and overwrite a user's later capital-deployment preference.
	introducedDeploymentSwitch := !tx.Migrator().HasColumn(&models.Settings{}, "AICapitalDeploymentEnabled")
	introducedCapitalTables := !tx.Migrator().HasTable(&research.AnalysisTrigger{}) || !tx.Migrator().HasTable(&research.BuyOpportunity{})
	if err := tx.AutoMigrate(
		&models.Settings{},
		&research.AnalysisRun{},
		&research.Recommendation{},
		&research.AnalysisTrigger{},
		&research.BuyOpportunity{},
	); err != nil {
		return fmt.Errorf("create App 2.7.0 event-driven capital deployment schema: %w", err)
	}
	// Old migrations use the current Settings model, so a direct upgrade from
	// a very old database can add the column before migration 21 runs. The two
	// v2.7 tables are the additional one-time boundary for that upgrade path.
	if introducedDeploymentSwitch || introducedCapitalTables {
		if err := tx.Exec(`UPDATE settings
SET ai_capital_deployment_enabled = CASE WHEN COALESCE(ai_analysis_enabled, 0) <> 0 THEN 1 ELSE 0 END`).Error; err != nil {
			return fmt.Errorf("migrate fixed-time AI preference to capital deployment: %w", err)
		}
	}
	if err := tx.Exec(`UPDATE settings SET
ai_target_capital_utilization = 0.90
WHERE ai_target_capital_utilization IS NULL OR ai_target_capital_utilization <= 0 OR ai_target_capital_utilization > 0.90`).Error; err != nil {
		return fmt.Errorf("backfill target capital utilization: %w", err)
	}
	if err := tx.Exec(`UPDATE settings SET
ai_max_immediate_buys_per_run = 2
WHERE ai_max_immediate_buys_per_run IS NULL OR ai_max_immediate_buys_per_run < 1 OR ai_max_immediate_buys_per_run > 2`).Error; err != nil {
		return fmt.Errorf("backfill maximum immediate buys: %w", err)
	}
	if err := tx.Exec(`UPDATE settings SET
ai_reanalysis_interval_minutes = 30
WHERE ai_reanalysis_interval_minutes IS NULL OR ai_reanalysis_interval_minutes < 5 OR ai_reanalysis_interval_minutes > 120`).Error; err != nil {
		return fmt.Errorf("backfill capital deployment reanalysis interval: %w", err)
	}
	if err := tx.Exec("UPDATE settings SET ai_analysis_enabled = 0 WHERE ai_analysis_enabled IS NULL OR ai_analysis_enabled <> 0").Error; err != nil {
		return fmt.Errorf("disable retired fixed-time AI analysis: %w", err)
	}
	if err := tx.Exec(`UPDATE research_v160_analysis_runs
SET trigger_ids_json = '[]'
WHERE trigger_ids_json IS NULL OR TRIM(trigger_ids_json) = ''`).Error; err != nil {
		return fmt.Errorf("backfill analysis trigger identities: %w", err)
	}
	return verifyMainSchema21Runtime(tx)
}

func verifyMainSchema21Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	settingsFields := []string{
		"AICapitalDeploymentEnabled",
		"AITargetCapitalUtilization",
		"AIMaxImmediateBuysPerRun",
		"AIReanalysisIntervalMinutes",
	}
	for _, field := range settingsFields {
		if !database.Migrator().HasColumn(&models.Settings{}, field) {
			return fmt.Errorf("main schema 21 settings.%s is missing", field)
		}
	}
	analysisFields := []string{
		"TriggerID", "TriggerIDsJSON", "TriggerSource", "TriggerReason",
		"FundingCash", "FundingReservedCash", "FundingNetAssetValue", "FundingCapitalBuffer",
		"FundingDeployableCash", "FundingAvailableSlots", "LeaseOwner", "LeaseExpiresAt",
		"BuyNowCount", "WaitCount", "RejectCount",
	}
	for _, field := range analysisFields {
		if !database.Migrator().HasColumn(&research.AnalysisRun{}, field) {
			return fmt.Errorf("main schema 21 analysis run.%s is missing", field)
		}
	}
	if !database.Migrator().HasColumn(&research.Recommendation{}, "OpportunityID") {
		return errors.New("main schema 21 recommendation.OpportunityID is missing")
	}
	for _, model := range []any{&research.AnalysisTrigger{}, &research.BuyOpportunity{}} {
		if !database.Migrator().HasTable(model) {
			return fmt.Errorf("main schema 21 table for %T is missing", model)
		}
	}
	if !database.Migrator().HasIndex(&research.AnalysisTrigger{}, "idx_v270_trigger_source_key") {
		return errors.New("main schema 21 trigger source/source_key uniqueness index is missing")
	}
	triggerFields := []string{"TriggerID", "Source", "SourceKey", "Status", "AvailableAt", "CoalesceUntil", "LeaseExpiresAt", "LeaseOwner", "AttemptCount", "AnalysisRunID"}
	for _, field := range triggerFields {
		if !database.Migrator().HasColumn(&research.AnalysisTrigger{}, field) {
			return fmt.Errorf("main schema 21 analysis trigger.%s is missing", field)
		}
	}
	opportunityFields := []string{"OpportunityID", "AnalysisRunID", "RecommendationID", "Action", "Status", "StockCode", "PriceLow", "PriceHigh", "QuotePrice", "QuoteAt", "TimingReason", "ExpiresAt", "SupersededAt", "ValidationReason"}
	for _, field := range opportunityFields {
		if !database.Migrator().HasColumn(&research.BuyOpportunity{}, field) {
			return fmt.Errorf("main schema 21 buy opportunity.%s is missing", field)
		}
	}

	var invalidSettings int64
	if err := database.Model(&models.Settings{}).Where(`
ai_analysis_enabled IS NULL OR ai_analysis_enabled <> 0 OR
ai_capital_deployment_enabled IS NULL OR
ai_target_capital_utilization IS NULL OR ai_target_capital_utilization <= 0 OR ai_target_capital_utilization > ? OR
ai_max_immediate_buys_per_run IS NULL OR ai_max_immediate_buys_per_run < 1 OR ai_max_immediate_buys_per_run > ? OR
ai_reanalysis_interval_minutes IS NULL OR ai_reanalysis_interval_minutes < 5 OR ai_reanalysis_interval_minutes > 120`,
		defaultTargetCapitalUtilization, defaultMaxImmediateBuysPerRun,
	).Count(&invalidSettings).Error; err != nil {
		return fmt.Errorf("verify main schema 21 settings: %w", err)
	}
	if invalidSettings != 0 {
		return fmt.Errorf("main schema 21 has %d invalid settings rows", invalidSettings)
	}
	var invalidRuns int64
	if err := database.Model(&research.AnalysisRun{}).
		Where("trigger_ids_json IS NULL OR TRIM(trigger_ids_json) = ''").
		Count(&invalidRuns).Error; err != nil {
		return fmt.Errorf("verify main schema 21 analysis trigger identities: %w", err)
	}
	if invalidRuns != 0 {
		return fmt.Errorf("main schema 21 has %d analysis runs without trigger identity JSON", invalidRuns)
	}
	return nil
}
