package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/internal/releaseinfo"

	"gorm.io/gorm"
)

const minuteBarSchemaSQL = `
CREATE TABLE IF NOT EXISTS minute_bar (
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
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_minute_bar_trade_time
ON minute_bar(trade_time);
`

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
	id          int
	name        string
	description string
	apply       func(*gorm.DB) error
}

func (m migration) checksum() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%06d\n%s\n%s\n", m.id, m.name, m.description)))
	return hex.EncodeToString(sum[:])
}

var mainMigrations = []migration{
	{
		id:          1,
		name:        "baseline_app_schema",
		description: "App 1.5.1 baseline of the existing primary schema, paused runtime control, and immutable strategy write guards; no repair or backfill side effects.",
		apply: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(mainModels()...); err != nil {
				return err
			}
			if err := persistence.MigrateStrategyPersistence(tx); err != nil {
				return err
			}
			if err := governance.InitializeStrategyRuntimeControl(context.Background(), tx, releaseinfo.Manifest().CurrentStrategyVersion); err != nil {
				return err
			}
			return installStrategyWriteGuards(tx)
		},
	},
}

var minuteMigrations = []migration{
	{
		id:          1,
		name:        "baseline_minute_bar_schema",
		description: "App 1.5.1 minute_bar WITHOUT ROWID baseline and trade_time index.",
		apply: func(tx *gorm.DB) error {
			return tx.Exec(minuteBarSchemaSQL).Error
		},
	},
}

func mainModels() []any {
	return []any{
		&data.StockInfo{},
		&data.StockBasic{},
		&data.FollowedStock{},
		&data.IndexBasic{},
		&data.Settings{},
		&models.AIResponseResult{},
		&models.AgentChatSession{},
		&models.AgentChatMessage{},
		&models.StockInfoHK{},
		&models.StockInfoUS{},
		&data.FollowedFund{},
		&data.FundBasic{},
		&models.PromptTemplate{},
		&data.Group{},
		&data.GroupStock{},
		&models.Tags{},
		&models.Telegraph{},
		&models.TelegraphTags{},
		&models.LongTigerRankData{},
		&data.AIConfig{},
		&models.BKDict{},
		&models.WordAnalyze{},
		&models.SentimentResultAnalyze{},
		&models.AiRecommendStocks{},
		&models.AiRecommendOpeningReview{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldOverride{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendDailyBar{},
		&models.CronTaskRun{},
		&models.EmailSendLog{},
		&models.MarketSummaryRunDiagnostic{},
	}
}

var pausedStrategyTables = []string{
	"ai_recommend_stocks",
	"ai_recommend_opening_review",
	"ai_recommend_yield_state",
	"ai_recommend_yield_override",
	"ai_recommend_yield_record_state",
	"ai_recommend_yield_meta",
	"ai_recommend_yield_dirty_code",
	"market_summary_run_diagnostics",
	"strategy_run_snapshot",
	"strategy_candidate_snapshot",
	"strategy_rule_snapshot",
	"strategy_order_event",
}

var immutableStrategyTables = []string{
	"strategy_run_snapshot",
	"strategy_candidate_snapshot",
	"strategy_rule_snapshot",
	"strategy_order_event",
}

func installStrategyWriteGuards(database *gorm.DB) error {
	strategyVersion := strings.ReplaceAll(releaseinfo.Manifest().CurrentStrategyVersion, "'", "''")
	livePredicate := fmt.Sprintf(
		"NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '%s')",
		strategyVersion,
	)
	for _, table := range pausedStrategyTables {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			name := fmt.Sprintf("guard_strategy_paused_%s_%s", strings.ToLower(operation), table)
			statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s
BEFORE %s ON %s
WHEN %s
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END`, name, operation, table, livePredicate)
			if err := database.Exec(statement).Error; err != nil {
				return fmt.Errorf("install %s: %w", name, err)
			}
		}
	}

	for _, table := range immutableStrategyTables {
		for _, operation := range []string{"UPDATE", "DELETE"} {
			name := fmt.Sprintf("guard_strategy_immutable_%s_%s", strings.ToLower(operation), table)
			statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s
BEFORE %s ON %s
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END`, name, operation, table)
			if err := database.Exec(statement).Error; err != nil {
				return fmt.Errorf("install %s: %w", name, err)
			}
		}
	}

	legacyStatements := []string{
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_insert
BEFORE INSERT ON ai_recommend_stocks
WHEN COALESCE(NEW.summary_version, '') <> '%s'
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END`, strategyVersion),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_update
BEFORE UPDATE ON ai_recommend_stocks
WHEN COALESCE(OLD.summary_version, '') <> '%s'
  OR COALESCE(NEW.summary_version, '') <> COALESCE(OLD.summary_version, '')
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END`, strategyVersion),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_delete
BEFORE DELETE ON ai_recommend_stocks
WHEN COALESCE(OLD.summary_version, '') <> '%s'
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END`, strategyVersion),
	}
	for _, statement := range legacyStatements {
		if err := database.Exec(statement).Error; err != nil {
			return fmt.Errorf("install legacy strategy guard: %w", err)
		}
	}
	return nil
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
	if err := MigrateMinute(minuteDB); err != nil {
		return err
	}
	return nil
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
	for _, item := range migrations {
		var count int64
		if err := database.Model(&MigrationRecord{}).Where("id = ?", item.id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			continue
		}
		record := MigrationRecord{
			ID:         item.id,
			Name:       item.name,
			Checksum:   item.checksum(),
			AppliedAt:  time.Now().UTC(),
			AppVersion: releaseinfo.Manifest().AppVersion,
		}
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
	return result, nil
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
