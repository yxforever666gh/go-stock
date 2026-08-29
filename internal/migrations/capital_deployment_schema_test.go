package migrations

import (
	"strings"
	"testing"

	"go-stock/backend/models"
	"go-stock/backend/research"
)

func TestSchema21MigratesLegacyPreferenceOnceAndPreservesHistory(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE settings (
id INTEGER PRIMARY KEY AUTOINCREMENT,
created_at DATETIME,
updated_at DATETIME,
deleted_at DATETIME,
ai_analysis_enabled NUMERIC NOT NULL DEFAULT 1,
ai_analysis_times TEXT NOT NULL DEFAULT '09:30,11:30,14:30'
);
INSERT INTO settings(id, ai_analysis_enabled, ai_analysis_times) VALUES
(1, 1, '09:31,14:01'),
(2, 0, '10:15');
CREATE TABLE research_v160_analysis_runs (
id INTEGER PRIMARY KEY AUTOINCREMENT,
run_id TEXT NOT NULL UNIQUE,
status TEXT NOT NULL,
final_report TEXT
);
INSERT INTO research_v160_analysis_runs(run_id, status, final_report)
VALUES ('historical-run', 'success', 'historical report');
CREATE TABLE research_v160_recommendations (
id INTEGER PRIMARY KEY AUTOINCREMENT,
recommendation_id TEXT NOT NULL UNIQUE,
analysis_run_id TEXT NOT NULL,
stock_code TEXT NOT NULL,
stock_name TEXT NOT NULL,
signal_at DATETIME NOT NULL,
status TEXT NOT NULL
);
INSERT INTO research_v160_recommendations(
recommendation_id, analysis_run_id, stock_code, stock_name, signal_at, status)
VALUES ('historical-rec', 'historical-run', 'sh600000', '浦发银行', CURRENT_TIMESTAMP, 'closed');`).Error; err != nil {
		t.Fatal(err)
	}

	if err := applyEventDrivenCapitalDeploymentSchema(database); err != nil {
		t.Fatalf("apply schema 21: %v", err)
	}
	if err := verifyMainSchema21Runtime(database); err != nil {
		t.Fatal(err)
	}

	var settings []models.Settings
	if err := database.Unscoped().Order("id ASC").Find(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if len(settings) != 2 {
		t.Fatalf("settings rows=%d", len(settings))
	}
	if settings[0].AIAnalysisEnabled || !settings[0].AICapitalDeploymentEnabled || settings[0].AIAnalysisTimes != "09:31,14:01" {
		t.Fatalf("enabled legacy preference migration=%+v", settings[0])
	}
	if settings[1].AIAnalysisEnabled || settings[1].AICapitalDeploymentEnabled || settings[1].AIAnalysisTimes != "10:15" {
		t.Fatalf("disabled legacy preference migration=%+v", settings[1])
	}
	for _, row := range settings {
		if row.AITargetCapitalUtilization != 0.90 || row.AIMaxImmediateBuysPerRun != 2 || row.AIReanalysisIntervalMinutes != 30 {
			t.Fatalf("capital deployment defaults=%+v", row)
		}
	}

	var run research.AnalysisRun
	if err := database.Where("run_id = ?", "historical-run").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" || run.FinalReport != "historical report" || run.TriggerIDsJSON != "[]" {
		t.Fatalf("historical run was rewritten: %+v", run)
	}
	var recommendation research.Recommendation
	if err := database.Where("recommendation_id = ?", "historical-rec").First(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	if recommendation.Status != "closed" || recommendation.StockCode != "sh600000" || recommendation.OpportunityID != "" {
		t.Fatalf("historical recommendation was rewritten: %+v", recommendation)
	}

	// Simulate a post-upgrade user choice and a stale legacy client. Repeating
	// the migration may disable the legacy switch again, but must not recopy it.
	if err := database.Model(&models.Settings{}).Where("id = ?", 1).Updates(map[string]any{
		"ai_capital_deployment_enabled": false,
		"ai_analysis_enabled":           true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyEventDrivenCapitalDeploymentSchema(database); err != nil {
		t.Fatalf("repeat schema 21: %v", err)
	}
	var repeated models.Settings
	if err := database.Unscoped().First(&repeated, 1).Error; err != nil {
		t.Fatal(err)
	}
	if repeated.AIAnalysisEnabled || repeated.AICapitalDeploymentEnabled {
		t.Fatalf("repeated migration recopied retired preference: %+v", repeated)
	}
}

func TestSchema21DefinitionAndVerifierCoverDurableCapitalDeployment(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&models.Settings{}, &research.AnalysisRun{}, &research.Recommendation{}); err != nil {
		t.Fatal(err)
	}
	if err := applyEventDrivenCapitalDeploymentSchema(database); err != nil {
		t.Fatal(err)
	}
	definition := mainMigrationV21Definition()
	for _, fragment := range []string{
		"research_v270_analysis_triggers",
		"research_v270_buy_opportunities",
		"settings.ai_analysis_enabled forced to 0",
		"research_v160_recommendations.opportunity_id",
	} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("schema 21 definition is missing %q: %s", fragment, definition)
		}
	}
	if err := database.Migrator().DropTable(&research.BuyOpportunity{}); err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema21Runtime(database); err == nil {
		t.Fatal("schema 21 verifier accepted a missing buy-opportunity table")
	}
}

func TestSchema21CopiesLegacyPreferenceWhenCurrentModelLeakedColumnEarly(t *testing.T) {
	database := openMigrationTestDB(t)
	// Frozen early migrations historically AutoMigrate the current settings
	// model. Reproduce that direct-upgrade shape: new columns exist, but no
	// schema-21 tables or ledger entry exists yet.
	if err := database.AutoMigrate(&models.Settings{}, &research.AnalysisRun{}, &research.Recommendation{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.Settings{AICapitalDeploymentEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.Settings{}).Where("1 = 1").Update("ai_analysis_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyEventDrivenCapitalDeploymentSchema(database); err != nil {
		t.Fatal(err)
	}
	var stored models.Settings
	if err := database.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AICapitalDeploymentEnabled {
		t.Fatal("direct upgrade did not copy the disabled legacy preference")
	}
}

func TestSchema21RejectsDuplicateTriggerSourceKey(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&models.Settings{}, &research.AnalysisRun{}, &research.Recommendation{}); err != nil {
		t.Fatal(err)
	}
	if err := applyEventDrivenCapitalDeploymentSchema(database); err != nil {
		t.Fatal(err)
	}
	first := research.AnalysisTrigger{
		TriggerID: "trigger-1", Source: research.TriggerSourceSell, SourceKey: "trade-1",
		Status: research.TriggerStatusQueued,
	}
	if err := database.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = 0
	duplicate.TriggerID = "trigger-2"
	if err := database.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate trigger source key unexpectedly succeeded")
	}
}
