package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research"
	"go-stock/internal/releaseinfo"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const minuteBarTableSQL = `CREATE TABLE IF NOT EXISTS minute_bar (
  stock_code TEXT NOT NULL,
  trade_time INTEGER NOT NULL,
  open REAL NOT NULL,
  high REAL NOT NULL,
  low REAL NOT NULL,
  close REAL NOT NULL,
  volume REAL,
  amount REAL,
  source TEXT,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (stock_code, trade_time)
) WITHOUT ROWID`

const minuteBarIndexSQL = `CREATE INDEX IF NOT EXISTS idx_minute_bar_trade_time
ON minute_bar(trade_time)`

const minuteBarSchemaSQL = minuteBarTableSQL + ";\n\n" + minuteBarIndexSQL + ";\n"

type MigrationRecord struct {
	ID         int       `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Name       string    `gorm:"not null" json:"name"`
	Checksum   string    `gorm:"size:64;not null" json:"checksum"`
	AppliedAt  time.Time `gorm:"not null" json:"appliedAt"`
	AppVersion string    `gorm:"not null" json:"appVersion"`
}

func (MigrationRecord) TableName() string { return "schema_migrations" }

type DatabaseStatus struct {
	Database        string            `json:"database"`
	CurrentVersion  int               `json:"currentVersion"`
	ExpectedVersion int               `json:"expectedVersion"`
	Pending         []int             `json:"pending"`
	Records         []MigrationRecord `json:"records"`
	QuickCheck      string            `json:"quickCheck,omitempty"`
}

type migration struct {
	id                int
	name              string
	description       string
	definition        func() string
	publishedChecksum string
	apply             func(*gorm.DB) error
}

func (m migration) checksum() string {
	if m.publishedChecksum != "" {
		return m.publishedChecksum
	}
	definition := ""
	if m.definition != nil {
		definition = m.definition()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%06d\n%s\n%s\n%s", m.id, m.name, m.description, definition)))
	return hex.EncodeToString(sum[:])
}

var mainMigrations = []migration{
	{
		id: 1, name: "baseline_app_schema",
		description:       "Published App 1.5.1 primary-schema baseline, retained only so existing databases can upgrade without rewriting history.",
		publishedChecksum: "41df05f8dbf7b1c56fe959ee8893d97938ddfe35425e98110333e47e2ee40ba6",
		apply:             applyMainSchema,
	},
	{
		id: 2, name: "lock_app_schema_definition",
		description:       "Published App 1.5.1 schema definition retained as an inert historical baseline.",
		definition:        func() string { return mainMigrationV2Definition },
		publishedChecksum: "616fac7d92781aa3c88470d13f7a34df4ec2d35772978167a03b570385b8e9b3",
		apply:             applyMainSchemaV2,
	},
	{
		id: 3, name: "research_v160_clean_schema",
		description: "App 1.6.0 creates isolated AI-analysis, lifecycle, account, trade and position tables and removes all legacy strategy guard triggers without deleting history.",
		definition:  mainMigrationV3Definition,
		apply:       applyResearchV160Schema,
	},
	{
		id: 4, name: "ai_config_model_switch_fallback_order",
		description: "App 1.6.0 adds a per-model call switch while retaining ai_config.sort as the single fallback order.",
		definition:  func() string { return "ai_config.disabled NOT NULL DEFAULT 0\nai_config.sort is fallback order" },
		apply:       applyAIConfigModelSwitchFallbackOrder,
	},
	{
		id: 5, name: "research_model_attempt_diagnostics",
		description: "App 1.6.2 persists sanitized, structured model-attempt diagnostics for each research analysis run.",
		definition:  func() string { return "research_v160_analysis_runs.model_attempt_log_json TEXT NOT NULL DEFAULT '[]'" },
		apply:       applyResearchModelAttemptDiagnostics,
	},
	{
		id: 6, name: "research_four_hour_activation_recovery",
		description: "App 1.6.3 restores the two after-close recommendations that were invalidated by the former same-day rule while preserving their decision history.",
		definition: func() string {
			return "restore c49ade23-12f4-4aa0-8203-b985bfd9d7e4 and 699640bc-861e-4330-8023-4182173b3e9e as pending at 2026-08-18 09:30 Asia/Shanghai; append deterministic recovery events"
		},
		apply: applyResearchFourHourActivationRecovery,
	},
}

var minuteMigrations = []migration{
	{
		id:                1,
		name:              "baseline_minute_bar_schema",
		description:       "App 1.5.1 minute_bar WITHOUT ROWID baseline and trade_time index.",
		publishedChecksum: "e838c98300ecee89806e5da10fc424bacff60754e212b449066feadecf59c8ec",
		apply:             func(tx *gorm.DB) error { return tx.Exec(minuteBarSchemaSQL).Error },
	},
	{id: 2, name: "lock_minute_bar_schema_definition", description: "App 1.5.1 locks the complete minute_bar DDL without rewriting the published baseline migration.", publishedChecksum: "f479775a220b2f4816aaa254c0193f49861fb8d61181634607b76e338debbde0", definition: func() string { return minuteBarSchemaSQL }, apply: func(tx *gorm.DB) error {
		if err := tx.Exec(minuteBarSchemaSQL).Error; err != nil {
			return err
		}
		return verifyMinuteSchema(tx)
	}},
}

func applyMainSchema(tx *gorm.DB) error   { return applyFrozenMainSchemaV2(tx) }
func applyMainSchemaV2(tx *gorm.DB) error { return applyFrozenMainSchemaV2(tx) }

func mainMigrationV3Definition() string {
	return strings.Join([]string{
		"research_v160_analysis_runs", "research_v160_recommendations", "research_v160_lifecycle_messages",
		"research_v160_decision_events", "research_v160_simulated_accounts", "research_v160_simulated_trades",
		"research_v160_positions", "settings.ai_analysis_enabled", "settings.ai_analysis_config_id",
		"settings.ai_analysis_times", "drop legacy guard_strategy/guard_legacy/immutable_strategy/immutable_corporate_action triggers",
	}, "\n")
}

func applyResearchV160Schema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := dropLegacyStrategyTriggers(tx); err != nil {
		return err
	}
	if err := tx.AutoMigrate(
		&research.AnalysisRun{}, &research.Recommendation{}, &research.LifecycleMessage{},
		&research.DecisionEvent{}, &research.SimulatedAccount{}, &research.SimulatedTrade{}, &research.Position{},
		&models.Settings{},
	); err != nil {
		return fmt.Errorf("create 1.6.0 research schema: %w", err)
	}
	account := research.SimulatedAccount{ID: 1, InitialCash: research.InitialCash, Cash: research.InitialCash}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error; err != nil {
		return fmt.Errorf("initialize 1.6.0 simulated account: %w", err)
	}
	return nil
}

func applyAIConfigModelSwitchFallbackOrder(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&models.AIConfig{}) {
		return errors.New("ai_config table is unavailable")
	}
	if !tx.Migrator().HasColumn(&models.AIConfig{}, "Disabled") {
		if err := tx.Exec("ALTER TABLE ai_config ADD COLUMN disabled numeric NOT NULL DEFAULT 0").Error; err != nil {
			return fmt.Errorf("add ai_config.disabled: %w", err)
		}
	}
	if err := tx.Exec("UPDATE ai_config SET disabled = 0 WHERE disabled IS NULL").Error; err != nil {
		return fmt.Errorf("backfill ai_config.disabled: %w", err)
	}
	return nil
}

func applyResearchModelAttemptDiagnostics(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&research.AnalysisRun{}) {
		return errors.New("research analysis table is unavailable")
	}
	if !tx.Migrator().HasColumn(&research.AnalysisRun{}, "ModelAttemptLogJSON") {
		if err := tx.Exec("ALTER TABLE research_v160_analysis_runs ADD COLUMN model_attempt_log_json TEXT NOT NULL DEFAULT '[]'").Error; err != nil {
			return fmt.Errorf("add research model attempt log: %w", err)
		}
	}
	if err := tx.Exec("UPDATE research_v160_analysis_runs SET model_attempt_log_json = '[]' WHERE model_attempt_log_json IS NULL OR TRIM(model_attempt_log_json) = ''").Error; err != nil {
		return fmt.Errorf("backfill research model attempt log: %w", err)
	}
	return nil
}

func applyResearchFourHourActivationRecovery(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&research.Recommendation{}) || !tx.Migrator().HasTable(&research.DecisionEvent{}) {
		return errors.New("research lifecycle tables are unavailable")
	}
	nextCheck := time.Date(2026, 8, 18, 9, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	recoveries := []struct {
		recommendationID string
		eventID          string
	}{
		{recommendationID: "c49ade23-12f4-4aa0-8203-b985bfd9d7e4", eventID: "16300000-0000-4000-8000-000000000001"},
		{recommendationID: "699640bc-861e-4330-8023-4182173b3e9e", eventID: "16300000-0000-4000-8000-000000000002"},
	}
	for _, recovery := range recoveries {
		result := tx.Model(&research.Recommendation{}).
			Where("recommendation_id = ? AND status = ?", recovery.recommendationID, "invalidated").
			Updates(map[string]any{
				"status": "pending", "next_check_at": nextCheck, "last_decision": "", "last_decision_at": nil,
			})
		if result.Error != nil {
			return fmt.Errorf("restore recommendation %s: %w", recovery.recommendationID, result.Error)
		}
		if result.RowsAffected == 0 {
			continue
		}
		event := research.DecisionEvent{
			EventID: recovery.eventID, RecommendationID: recovery.recommendationID,
			DecisionType: "人工恢复", DecidedAt: time.Now(),
			Reason: "1.6.3 启用累计4小时开盘交易时长规则，恢复为未激活并从下一交易日继续判断",
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
			return fmt.Errorf("record recovery event %s: %w", recovery.recommendationID, err)
		}
	}
	return nil
}

func dropLegacyStrategyTriggers(database *gorm.DB) error {
	var names []string
	if err := database.Raw(`SELECT name FROM sqlite_master
WHERE type = 'trigger' AND (
  name LIKE 'guard_strategy_%' OR
  name LIKE 'guard_legacy_%' OR
  name LIKE 'immutable_strategy_%' OR
  name LIKE 'immutable_corporate_action_%'
) ORDER BY name`).Scan(&names).Error; err != nil {
		return fmt.Errorf("list legacy strategy triggers: %w", err)
	}
	for _, name := range names {
		if err := database.Exec("DROP TRIGGER IF EXISTS " + quoteSQLiteIdentifier(name)).Error; err != nil {
			return fmt.Errorf("drop legacy strategy trigger %s: %w", name, err)
		}
	}
	return nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func verifyMinuteSchema(database *gorm.DB) error {
	if err := verifySQLiteSchemaObject(database, "table", "minute_bar", minuteBarTableSQL); err != nil {
		return fmt.Errorf("verify minute_bar: %w", err)
	}
	if err := verifySQLiteSchemaObject(database, "index", "idx_minute_bar_trade_time", minuteBarIndexSQL); err != nil {
		return fmt.Errorf("verify idx_minute_bar_trade_time: %w", err)
	}
	return nil
}

func verifySQLiteSchemaObject(database *gorm.DB, objectType, name, expectedSQL string) error {
	var actualSQL string
	result := database.Raw("SELECT sql FROM sqlite_master WHERE type = ? AND name = ?", objectType, name).Scan(&actualSQL)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 || strings.TrimSpace(actualSQL) == "" {
		return fmt.Errorf("%s %q is missing", objectType, name)
	}
	if normalizeSQLiteSQL(actualSQL) != normalizeSQLiteSQL(expectedSQL) {
		return fmt.Errorf("%s %q definition conflict: database=%q code=%q", objectType, name, actualSQL, expectedSQL)
	}
	return nil
}

func normalizeSQLiteSQL(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(value), ";")), " ")
	for _, prefix := range []string{"CREATE TRIGGER", "CREATE TABLE", "CREATE INDEX"} {
		normalized = strings.Replace(normalized, prefix+" IF NOT EXISTS", prefix, 1)
	}
	return normalized
}

func MigrateMain(database *gorm.DB) error {
	return migrate(database, "main", mainMigrations, releaseinfo.Manifest().MainSchemaVersion)
}
func MigrateMinute(database *gorm.DB) error {
	return migrate(database, "minute", minuteMigrations, releaseinfo.Manifest().MinuteSchemaVersion)
}
func MigrateAll(mainDB, minuteDB *gorm.DB) error {
	if err := MigrateMain(mainDB); err != nil {
		return err
	}
	return MigrateMinute(minuteDB)
}

func migrate(database *gorm.DB, databaseName string, migrations []migration, expected int) error {
	if database == nil {
		return fmt.Errorf("%s database is not initialized", databaseName)
	}
	if expected != latestVersion(migrations) {
		return fmt.Errorf("%s manifest schema version %d does not match latest migration %d", databaseName, expected, latestVersion(migrations))
	}
	if err := database.AutoMigrate(&MigrationRecord{}); err != nil {
		return fmt.Errorf("initialize %s migration ledger: %w", databaseName, err)
	}
	if _, err := records(database, migrations); err != nil {
		return fmt.Errorf("verify %s migration ledger: %w", databaseName, err)
	}
	if err := ensureNoMigrationGaps(database, migrations); err != nil {
		return fmt.Errorf("verify %s migration ledger: %w", databaseName, err)
	}
	for _, item := range migrations {
		var count int64
		if err := database.Model(&MigrationRecord{}).Where("id = ?", item.id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			continue
		}
		record := MigrationRecord{ID: item.id, Name: item.name, Checksum: item.checksum(), AppliedAt: time.Now().UTC(), AppVersion: releaseinfo.Manifest().AppVersion}
		if err := database.Transaction(func(tx *gorm.DB) error {
			if err := item.apply(tx); err != nil {
				return err
			}
			return tx.Create(&record).Error
		}); err != nil {
			return fmt.Errorf("apply %s migration %06d_%s: %w", databaseName, item.id, item.name, err)
		}
	}
	return Verify(database, databaseName, migrations, expected)
}

func ensureNoMigrationGaps(database *gorm.DB, migrations []migration) error {
	firstMissing := -1
	for index, item := range migrations {
		var count int64
		if err := database.Model(&MigrationRecord{}).Where("id = ?", item.id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 && firstMissing < 0 {
			firstMissing = index
			continue
		}
		if count != 0 && firstMissing >= 0 {
			return fmt.Errorf("migration %d is applied while prior migration %d is missing", item.id, migrations[firstMissing].id)
		}
	}
	return nil
}

func StatusMain(database *gorm.DB) (DatabaseStatus, error) {
	return status(database, "main", mainMigrations, releaseinfo.Manifest().MainSchemaVersion, false)
}
func StatusMinute(database *gorm.DB) (DatabaseStatus, error) {
	return status(database, "minute", minuteMigrations, releaseinfo.Manifest().MinuteSchemaVersion, false)
}
func VerifyMain(database *gorm.DB) (DatabaseStatus, error) {
	return verifiedStatus(database, "main", mainMigrations, releaseinfo.Manifest().MainSchemaVersion)
}
func VerifyMinute(database *gorm.DB) (DatabaseStatus, error) {
	return verifiedStatus(database, "minute", minuteMigrations, releaseinfo.Manifest().MinuteSchemaVersion)
}

// ValidateStrategyRuntimeSchema remains as a temporary source-compatibility alias.
// It validates only the clean 1.6.0 research schema and no strategy runtime.
func ValidateStrategyRuntimeSchema(database *gorm.DB) error {
	return verifyMainSchema3Runtime(database)
}

func verifiedStatus(database *gorm.DB, name string, migrations []migration, expected int) (DatabaseStatus, error) {
	result, err := status(database, name, migrations, expected, true)
	if err != nil {
		return result, err
	}
	if result.CurrentVersion != expected || len(result.Pending) != 0 {
		return result, fmt.Errorf("%s schema version is %d, expected %d", name, result.CurrentVersion, expected)
	}
	if !strings.EqualFold(strings.TrimSpace(result.QuickCheck), "ok") {
		return result, fmt.Errorf("%s quick_check returned %q", name, result.QuickCheck)
	}
	if name == "main" && expected >= 3 {
		if err := verifyMainSchema3Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 4 {
		if err := verifyMainSchema4Runtime(database); err != nil {
			return result, err
		}
	}
	if name == "main" && expected >= 5 {
		if err := verifyMainSchema5Runtime(database); err != nil {
			return result, err
		}
	}
	return result, nil
}

func verifyMainSchema3Runtime(database *gorm.DB) error {
	modelsToCheck := []any{&research.AnalysisRun{}, &research.Recommendation{}, &research.LifecycleMessage{}, &research.DecisionEvent{}, &research.SimulatedAccount{}, &research.SimulatedTrade{}, &research.Position{}}
	for _, model := range modelsToCheck {
		if !database.Migrator().HasTable(model) {
			return fmt.Errorf("main schema 3 table for %T is missing", model)
		}
	}
	var account research.SimulatedAccount
	if err := database.First(&account, 1).Error; err != nil {
		return fmt.Errorf("main schema 3 simulated account is unavailable: %w", err)
	}
	if account.InitialCash != research.InitialCash {
		return fmt.Errorf("main schema 3 initial cash is %.2f, expected %.2f", account.InitialCash, research.InitialCash)
	}
	var triggerCount int64
	if err := database.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND (name LIKE 'guard_strategy_%' OR name LIKE 'guard_legacy_%' OR name LIKE 'immutable_strategy_%' OR name LIKE 'immutable_corporate_action_%')`).Scan(&triggerCount).Error; err != nil {
		return err
	}
	if triggerCount != 0 {
		return fmt.Errorf("main schema 3 still has %d legacy strategy guard triggers", triggerCount)
	}
	return nil
}

func verifyMainSchema4Runtime(database *gorm.DB) error {
	if !database.Migrator().HasColumn(&models.AIConfig{}, "Disabled") {
		return errors.New("main schema 4 ai_config.disabled is missing")
	}
	var nullCount int64
	if err := database.Raw("SELECT COUNT(*) FROM ai_config WHERE disabled IS NULL").Scan(&nullCount).Error; err != nil {
		return err
	}
	if nullCount != 0 {
		return fmt.Errorf("main schema 4 has %d ai_config rows without call-switch state", nullCount)
	}
	return nil
}

func verifyMainSchema5Runtime(database *gorm.DB) error {
	if !database.Migrator().HasColumn(&research.AnalysisRun{}, "ModelAttemptLogJSON") {
		return errors.New("main schema 5 research model attempt log is missing")
	}
	var invalidCount int64
	if err := database.Raw("SELECT COUNT(*) FROM research_v160_analysis_runs WHERE model_attempt_log_json IS NULL OR TRIM(model_attempt_log_json) = ''").Scan(&invalidCount).Error; err != nil {
		return err
	}
	if invalidCount != 0 {
		return fmt.Errorf("main schema 5 has %d analysis rows without model attempt diagnostics", invalidCount)
	}
	return nil
}

func Verify(database *gorm.DB, name string, migrations []migration, expected int) error {
	_, err := verifiedStatus(database, name, migrations, expected)
	return err
}

func status(database *gorm.DB, name string, migrations []migration, expected int, runQuickCheck bool) (DatabaseStatus, error) {
	result := DatabaseStatus{Database: name, ExpectedVersion: expected}
	if database == nil {
		return result, fmt.Errorf("%s database is not initialized", name)
	}
	if !database.Migrator().HasTable(&MigrationRecord{}) {
		for _, item := range migrations {
			result.Pending = append(result.Pending, item.id)
		}
		if runQuickCheck {
			result.QuickCheck = quickCheck(database)
		}
		return result, nil
	}
	rows, err := records(database, migrations)
	if err != nil {
		return result, err
	}
	result.Records = rows
	seen := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		seen[row.ID] = struct{}{}
		if row.ID > result.CurrentVersion {
			result.CurrentVersion = row.ID
		}
	}
	for _, item := range migrations {
		if _, ok := seen[item.id]; !ok {
			result.Pending = append(result.Pending, item.id)
		}
	}
	if runQuickCheck {
		result.QuickCheck = quickCheck(database)
	}
	return result, nil
}

func records(database *gorm.DB, migrations []migration) ([]MigrationRecord, error) {
	var rows []MigrationRecord
	if err := database.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	expected := make(map[int]migration, len(migrations))
	for _, item := range migrations {
		expected[item.id] = item
	}
	for _, row := range rows {
		item, ok := expected[row.ID]
		if !ok {
			return nil, fmt.Errorf("unknown applied migration id %d", row.ID)
		}
		if row.Name != item.name {
			return nil, fmt.Errorf("migration %d name conflict: database=%q code=%q", row.ID, row.Name, item.name)
		}
		if !strings.EqualFold(row.Checksum, item.checksum()) {
			return nil, fmt.Errorf("migration %d checksum conflict: database=%s code=%s", row.ID, row.Checksum, item.checksum())
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func quickCheck(database *gorm.DB) string {
	var result string
	if err := database.Raw("PRAGMA quick_check").Scan(&result).Error; err != nil {
		return "error: " + err.Error()
	}
	return strings.TrimSpace(result)
}
func latestVersion(items []migration) int {
	if len(items) == 0 {
		return 0
	}
	latest := items[0].id
	for _, item := range items[1:] {
		if item.id <= latest {
			panic(errors.New("migrations must have strictly increasing ids"))
		}
		latest = item.id
	}
	return latest
}
