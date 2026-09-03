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
	if account.Cash != research.LegacyInitialCash || account.InitialCash != research.LegacyInitialCash {
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

func TestSchema7AddsLifecycleEvidenceAndPreservesRecommendations(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	run := research.AnalysisRun{RunID: "schema7-run", ScheduledFor: now, StartedAt: now, Status: "success"}
	if err := database.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	recommendation := research.Recommendation{RecommendationID: "schema7-rec", AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "浦发银行", SignalAt: now, Status: "pending"}
	if err := database.Create(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	event := research.DecisionEvent{EventID: "schema7-event", RecommendationID: recommendation.RecommendationID, DecisionType: "等待", DecidedAt: now, Reason: "legacy"}
	if err := database.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Migrator().DropTable(&research.LifecycleObservation{}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"DataPauseSeconds"} {
		if err := database.Migrator().DropColumn(&research.Recommendation{}, field); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"SourceRefs", "DataStatus"} {
		if err := database.Migrator().DropColumn(&research.DecisionEvent{}, field); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyResearchLifecycleObservationEvidence(database); err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema7Runtime(database); err != nil {
		t.Fatal(err)
	}
	var preserved research.Recommendation
	if err := database.Where("recommendation_id = ?", recommendation.RecommendationID).First(&preserved).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.Status != "pending" || preserved.DataPauseSeconds != 0 {
		t.Fatalf("preserved=%+v", preserved)
	}
	observation := research.LifecycleObservation{ObservationID: "schema7-observation", RecommendationID: recommendation.RecommendationID,
		Phase: "activation", WindowFrom: now, ObservedAt: now, Status: "ready", QuoteJSON: "{}", MinuteSummaryJSON: "{}", EvidenceJSON: "[]", SourceStatusJSON: "[]"}
	if err := database.Create(&observation).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSchema7CreatesCleanLifecycleEvidenceSchema(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchLifecycleObservationEvidence(database); err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema7Runtime(database); err != nil {
		t.Fatal(err)
	}
}

func TestSchema8QueuesLegacyRecommendationsAndRestoresApprovedFour(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchLifecycleObservationEvidence(database); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 14, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	run := research.AnalysisRun{RunID: "schema8-run", ScheduledFor: now, StartedAt: now, Status: "success"}
	if err := database.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	rows := []research.Recommendation{
		{RecommendationID: "c49ade23-12f4-4aa0-8203-b985bfd9d7e4", AnalysisRunID: run.RunID, StockCode: "sz300308", StockName: "中际旭创", SignalAt: now.Add(-4 * time.Hour), Status: "invalidated"},
		{RecommendationID: "699640bc-861e-4330-8023-4182173b3e9e", AnalysisRunID: run.RunID, StockCode: "sh688012", StockName: "中微公司", SignalAt: now.Add(-3 * time.Hour), Status: "invalidated"},
		{RecommendationID: "3bf68fd1-d97f-4426-aa2c-cb63236be808", AnalysisRunID: run.RunID, StockCode: "sz002539", StockName: "云图控股", SignalAt: now.Add(-2 * time.Hour), Status: "pending"},
		{RecommendationID: "053e7c47-a538-4d6d-9dbd-61e9897d8285", AnalysisRunID: run.RunID, StockCode: "sh601899", StockName: "紫金矿业", SignalAt: now.Add(-time.Hour), Status: "pending"},
		{RecommendationID: "schema8-generic-pending", AnalysisRunID: run.RunID, StockCode: "sh600000", StockName: "浦发银行", SignalAt: now, Status: "pending"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearchDirectBuyStrategy(database); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchDirectBuyStrategy(database); err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema8Runtime(database); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		var stored research.Recommendation
		if err := database.Where("recommendation_id = ?", row.RecommendationID).First(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != "buy_pending" || stored.NextCheckAt == nil {
			t.Fatalf("stored=%+v", stored)
		}
		var events int64
		if err := database.Model(&research.DecisionEvent{}).
			Where("recommendation_id = ? AND decision_type = ?", row.RecommendationID, "策略升级待买入").Count(&events).Error; err != nil {
			t.Fatal(err)
		}
		if events != 1 {
			t.Fatalf("recommendation %s migration events=%d", row.RecommendationID, events)
		}
	}
}

func TestSchema8DoesNotRequeueRecommendationWithExistingBuy(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run := research.AnalysisRun{RunID: "schema8-bought-run", ScheduledFor: now, StartedAt: now, Status: "success"}
	if err := database.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	recommendation := research.Recommendation{RecommendationID: "c49ade23-12f4-4aa0-8203-b985bfd9d7e4", AnalysisRunID: run.RunID,
		StockCode: "sz300308", StockName: "中际旭创", SignalAt: now, Status: "invalidated"}
	if err := database.Create(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	trade := research.SimulatedTrade{TradeID: "schema8-existing-buy", RecommendationID: recommendation.RecommendationID,
		StockCode: recommendation.StockCode, Side: "buy", TradedAt: now, Quantity: 100}
	if err := database.Create(&trade).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearchDirectBuyStrategy(database); err != nil {
		t.Fatal(err)
	}
	var stored research.Recommendation
	_ = database.Where("recommendation_id = ?", recommendation.RecommendationID).First(&stored).Error
	if stored.Status != "invalidated" {
		t.Fatalf("bought recommendation was requeued: %+v", stored)
	}
}

func TestSchema9DropsOnlyArchivedLegacyStrategyTables(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	for _, table := range legacyStrategyTables {
		statement := "CREATE TABLE " + quoteSQLiteIdentifier(table) + " (id INTEGER PRIMARY KEY, payload TEXT)"
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("create %s: %v", table, err)
		}
		if err := database.Exec("INSERT INTO " + quoteSQLiteIdentifier(table) + " (id, payload) VALUES (1, 'legacy')").Error; err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
		if err := database.Exec("CREATE INDEX " + quoteSQLiteIdentifier("idx_test_"+table) + " ON " + quoteSQLiteIdentifier(table) + " (payload)").Error; err != nil {
			t.Fatalf("index %s: %v", table, err)
		}
	}
	if err := database.Exec("CREATE TABLE preserved_market_history (id INTEGER PRIMARY KEY, payload TEXT); INSERT INTO preserved_market_history VALUES (1, 'keep')").Error; err != nil {
		t.Fatal(err)
	}
	if err := applyLegacyStrategyArchiveCleanup(database); err != nil {
		t.Fatal(err)
	}
	if err := applyLegacyStrategyArchiveCleanup(database); err != nil {
		t.Fatalf("schema 9 cleanup must be idempotent: %v", err)
	}
	if err := verifyMainSchema9Runtime(database); err != nil {
		t.Fatal(err)
	}
	var preserved int64
	if err := database.Table("preserved_market_history").Count(&preserved).Error; err != nil {
		t.Fatal(err)
	}
	if preserved != 1 {
		t.Fatalf("preserved market rows = %d, want 1", preserved)
	}
}

func TestSchema10RegistersInitialContributionWithoutChangingAccountOrPositions(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyResearchV160Schema(database); err != nil {
		t.Fatal(err)
	}
	if database.Migrator().HasColumn(&research.Recommendation{}, "ReservedCash") {
		if err := database.Migrator().DropColumn(&research.Recommendation{}, "ReservedCash"); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	pendingUpdatedAt := now.Add(-2 * time.Hour)
	pending := research.Recommendation{RecommendationID: "schema10-pending", AnalysisRunID: "schema10-run",
		StockCode: "sz000001", StockName: "平安银行", SignalAt: now.Add(-24 * time.Hour), Status: "pending",
		CreatedAt: pendingUpdatedAt.Add(-time.Hour), UpdatedAt: pendingUpdatedAt}
	if err := database.Omit("ReservedCash").Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&research.SimulatedAccount{}).Where("id = ?", 1).Update("cash", 23456.78).Error; err != nil {
		t.Fatal(err)
	}
	position := research.Position{RecommendationID: "schema10-position", StockCode: "sh600000", StockName: "浦发银行", Market: "SH",
		Quantity: 100, EntryAt: now, EntryPrice: 10, CurrentPrice: 10.2, CurrentPriceAt: &now, Status: "open"}
	if err := database.Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearchMultiPositionFunding(database); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchMultiPositionFunding(database); err != nil {
		t.Fatalf("schema 10 migration must be idempotent: %v", err)
	}
	var account research.SimulatedAccount
	if err := database.First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	if account.Cash != 23456.78 || account.InitialCash != research.LegacyInitialCash {
		t.Fatalf("migration changed account: %+v", account)
	}
	var storedPosition research.Position
	if err := database.Where("recommendation_id = ?", position.RecommendationID).First(&storedPosition).Error; err != nil {
		t.Fatal(err)
	}
	if storedPosition.Quantity != position.Quantity || storedPosition.Status != "open" {
		t.Fatalf("migration changed position: %+v", storedPosition)
	}
	var storedPending research.Recommendation
	if err := database.Where("recommendation_id = ?", pending.RecommendationID).First(&storedPending).Error; err != nil {
		t.Fatal(err)
	}
	if storedPending.ReservedCash != research.MaxCashPerTrade || !storedPending.UpdatedAt.Equal(pendingUpdatedAt) {
		t.Fatalf("queued recommendation migration changed history: reserved=%f updated=%v want=%v", storedPending.ReservedCash, storedPending.UpdatedAt, pendingUpdatedAt)
	}
	var flows []research.AccountCashFlow
	if err := database.Order("sequence asc").Find(&flows).Error; err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].Sequence != 0 || flows[0].Amount != research.LegacyInitialCash {
		t.Fatalf("cash flows=%+v", flows)
	}
	var plan research.FundingPlan
	if err := database.First(&plan, 1).Error; err != nil {
		t.Fatal(err)
	}
	if plan.CompletedDeposits != 0 || plan.PlannedDeposits != 4 || plan.TargetContribution != 500000 {
		t.Fatalf("funding plan=%+v", plan)
	}
	if err := verifyMainSchema10Runtime(database); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyDatabasesUpgradeDirectlyToSchema17AndMinute3(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatalf("repeat migration must be idempotent: %v", err)
	}
	mainStatus, err := VerifyMain(mainDB)
	if err != nil {
		t.Fatal(err)
	}
	minuteStatus, err := VerifyMinute(minuteDB)
	if err != nil {
		t.Fatal(err)
	}
	if mainStatus.CurrentVersion != 24 || minuteStatus.CurrentVersion != 3 {
		t.Fatalf("schema versions main=%d minute=%d", mainStatus.CurrentVersion, minuteStatus.CurrentVersion)
	}
}

func TestPublished151BaselineUpgradesDirectlyToSchema17(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&MigrationRecord{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range mainMigrations[:2] {
		if err := item.apply(database); err != nil {
			t.Fatalf("apply published baseline migration %d: %v", item.id, err)
		}
		record := MigrationRecord{
			ID: item.id, Name: item.name, Checksum: item.checksum(),
			AppliedAt: time.Now().UTC(), AppVersion: "1.5.1",
		}
		if err := database.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	if !database.Migrator().HasTable("ai_recommend_stocks") {
		t.Fatal("published 1.5 baseline fixture is missing a legacy strategy table")
	}
	if err := MigrateMain(database); err != nil {
		t.Fatal(err)
	}
	status, err := VerifyMain(database)
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentVersion != 24 || len(status.Pending) != 0 {
		t.Fatalf("upgraded status=%+v", status)
	}
	if database.Migrator().HasTable("ai_recommend_stocks") {
		t.Fatal("schema 9 retained a 1.5 legacy strategy table")
	}
}

func TestMinuteSchema3IncludesPublishedSchema2(t *testing.T) {
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
		3: "09a4f300170f52ad46b04e70467945ba9d9b12da545ce0f3faafa735ad4544af",
	}
	for _, item := range minuteMigrations {
		if got := item.checksum(); got != want[item.id] {
			t.Fatalf("minute migration %d checksum = %s, want %s", item.id, got, want[item.id])
		}
	}
}
