package migrations

import (
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestSchema3UpgradePreservesHistoricalTablesAndDropsGuards(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE ai_recommend_stocks (id INTEGER PRIMARY KEY, stock_code TEXT);
INSERT INTO ai_recommend_stocks(id, stock_code) VALUES (1, 'sh600000');
CREATE TRIGGER guard_strategy_paused_insert_ai_recommend_stocks BEFORE INSERT ON ai_recommend_stocks BEGIN SELECT RAISE(ABORT, 'paused'); END;`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	var historicalCount int64
	if err := database.Table("ai_recommend_stocks").Count(&historicalCount).Error; err != nil {
		t.Fatal(err)
	}
	if historicalCount != 1 {
		t.Fatalf("historical row count = %d", historicalCount)
	}
	if err := database.Exec("INSERT INTO ai_recommend_stocks(id, stock_code) VALUES (2, 'sh600001')").Error; err != nil {
		t.Fatalf("legacy guard still blocks writes: %v", err)
	}
	if err := verifyMainSchema3Runtime(database); err != nil {
		t.Fatal(err)
	}
}

func TestSchema3CreatesIndependentResearchTablesAndAccount(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	repository := research.NewRepository(database)
	ctx := t.Context()
	account, err := repository.Account(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if account.Cash != research.InitialCash || account.InitialCash != research.InitialCash {
		t.Fatalf("account = %+v", account)
	}
	run := research.AnalysisRun{RunID: "run-1", ScheduledFor: time.Now(), StartedAt: time.Now(), Status: "running"}
	if err := repository.CreateAnalysis(ctx, &run); err != nil {
		t.Fatal(err)
	}
}

func TestSchema4EnablesExistingAIConfigsByDefault(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE ai_config (
id integer PRIMARY KEY AUTOINCREMENT,
sort integer,
name text
);
INSERT INTO ai_config(sort, name) VALUES (1, 'primary'), (2, 'fallback');`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyAIConfigModelSwitchFallbackOrder(database); err != nil {
		t.Fatal(err)
	}
	if !database.Migrator().HasColumn(&models.AIConfig{}, "Disabled") {
		t.Fatal("ai_config.disabled was not created")
	}
	var callableCount int64
	if err := database.Raw("SELECT COUNT(*) FROM ai_config WHERE disabled = 0").Scan(&callableCount).Error; err != nil {
		t.Fatal(err)
	}
	if callableCount != 2 {
		t.Fatalf("callable rows = %d, want 2", callableCount)
	}
	if err := database.Exec("INSERT INTO ai_config(sort, name) VALUES (3, 'later')").Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema4Runtime(database); err != nil {
		t.Fatal(err)
	}
}

func TestSchema5AddsAttemptDiagnosticsAndPreservesRuns(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	// Recreate the schema-4 shape by removing the field that AutoMigrate adds
	// when tests use the current AnalysisRun model.
	if err := database.Migrator().DropColumn(&research.AnalysisRun{}, "ModelAttemptLogJSON"); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO research_v160_analysis_runs
(run_id, scheduled_for, started_at, status, ai_config_id, provider_name, model_name, market_report, sector_report, stock_report, final_report, source_status_json, failure_reason, recommendation_count, created_at, updated_at)
VALUES ('legacy-run', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'failed', 1, 'provider', 'model', '', '', '', '', '[]', 'old failure', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearchModelAttemptDiagnostics(database); err != nil {
		t.Fatal(err)
	}
	var run research.AnalysisRun
	if err := database.Where("run_id = ?", "legacy-run").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.FailureReason != "old failure" || run.ModelAttemptLogJSON != "[]" {
		t.Fatalf("run=%+v", run)
	}
	if err := verifyMainSchema5Runtime(database); err != nil {
		t.Fatal(err)
	}
}

func TestSchema6RestoresTargetRecommendationsAndPreservesInvalidationHistory(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 15, 43, 28, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	run := research.AnalysisRun{RunID: "00350e77-a1f3-4a4c-8e18-d1cf594e8d26", ScheduledFor: now, StartedAt: now, Status: "success"}
	if err := database.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	ids := []string{"c49ade23-12f4-4aa0-8203-b985bfd9d7e4", "699640bc-861e-4330-8023-4182173b3e9e"}
	for index, id := range ids {
		recommendation := research.Recommendation{
			RecommendationID: id, AnalysisRunID: run.RunID, StockCode: "sh60000" + string(rune('0'+index)),
			StockName: "测试", SignalAt: now, Status: "invalidated", LastDecision: "失效", LastDecisionAt: &now,
		}
		if err := database.Create(&recommendation).Error; err != nil {
			t.Fatal(err)
		}
		original := research.DecisionEvent{EventID: "old-event-" + id, RecommendationID: id, DecisionType: "失效", DecidedAt: now, Reason: "收盘后仍未激活，推荐当日失效"}
		if err := database.Create(&original).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := applyResearchFourHourActivationRecovery(database); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchFourHourActivationRecovery(database); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		var recommendation research.Recommendation
		if err := database.Where("recommendation_id = ?", id).First(&recommendation).Error; err != nil {
			t.Fatal(err)
		}
		if recommendation.Status != "pending" || recommendation.NextCheckAt == nil || recommendation.NextCheckAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04") != "2026-08-18 09:30" {
			t.Fatalf("restored recommendation=%+v", recommendation)
		}
		var originalCount, recoveryCount int64
		if err := database.Model(&research.DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", id, "失效").Count(&originalCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Model(&research.DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", id, "人工恢复").Count(&recoveryCount).Error; err != nil {
			t.Fatal(err)
		}
		if originalCount != 1 || recoveryCount != 1 {
			t.Fatalf("events for %s: invalid=%d recovery=%d", id, originalCount, recoveryCount)
		}
	}
}

func TestMinuteSchemaRemainsVersion2(t *testing.T) {
	database := openMigrationTestDB(t)
	for _, item := range minuteMigrations {
		if err := item.apply(database); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyMinuteSchema(database); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedMinuteMigrationChecksumsRemainFrozen(t *testing.T) {
	want := map[int]string{
		1: "e838c98300ecee89806e5da10fc424bacff60754e212b449066feadecf59c8ec",
		2: "f479775a220b2f4816aaa254c0193f49861fb8d61181634607b76e338debbde0",
	}
	for _, item := range minuteMigrations {
		if got := item.checksum(); got != want[item.id] {
			t.Fatalf("minute migration %d checksum = %s, want %s", item.id, got, want[item.id])
		}
	}
}
