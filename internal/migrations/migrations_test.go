package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/governance"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrateAllIsIdempotentAndInstallsStrategyGuards(t *testing.T) {
	mainDB := openMigrationTestDB(t, filepath.Join(t.TempDir(), "stock.db"))
	minuteDB := openMigrationTestDB(t, filepath.Join(t.TempDir(), "minute.db"))

	if err := mainDB.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}
	legacy := models.AiRecommendStocks{SummaryVersion: "1.4.2", StockCode: "600001.SH", StockName: "legacy"}
	if err := mainDB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatalf("second migration changed the result: %v", err)
	}
	mainStatus, err := VerifyMain(mainDB)
	if err != nil {
		t.Fatal(err)
	}
	minuteStatus, err := VerifyMinute(minuteDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(mainStatus.Records) != 2 || len(minuteStatus.Records) != 2 {
		t.Fatalf("unexpected migration ledgers: main=%+v minute=%+v", mainStatus.Records, minuteStatus.Records)
	}

	current := models.AiRecommendStocks{SummaryVersion: v150.StrategyVersion, StockCode: "600002.SH", StockName: "current"}
	if err := mainDB.Create(&current).Error; err == nil || !strings.Contains(err.Error(), "strategy production is paused") {
		t.Fatalf("paused insert error = %v", err)
	}
	if _, err := governance.SetStrategyRuntimeMode(context.Background(), mainDB, governance.StrategyModeLive, v150.StrategyVersion, "test", "migration-test"); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&current).Error; err != nil {
		t.Fatalf("live current-cohort insert failed: %v", err)
	}
	if err := mainDB.Model(&legacy).Update("stock_name", "mutated").Error; err == nil || !strings.Contains(err.Error(), "legacy strategy cohort is read-only") {
		t.Fatalf("legacy update error = %v", err)
	}
	if err := mainDB.Model(&current).Update("summary_version", "1.4.2").Error; err == nil || !strings.Contains(err.Error(), "legacy strategy cohort is read-only") {
		t.Fatalf("cohort rewrite error = %v", err)
	}

	now := time.Now().UTC()
	run := models.StrategyRunSnapshot{
		RunID: "migration-guard-run", StrategyVersion: v150.StrategyVersion, TradeDate: "2026-08-06",
		StartedAt: now, AsOf: now, DataCutoffAt: now, DecisionAt: now, GeneratedAt: now,
		SnapshotHash: "hash", PayloadJSON: `{}`, FrozenAt: &now,
	}
	if err := mainDB.Create(&run).Error; err != nil {
		t.Fatalf("append frozen snapshot: %v", err)
	}
	if err := mainDB.Model(&run).Update("mode", "changed").Error; err == nil || !strings.Contains(err.Error(), "immutable strategy snapshot") {
		t.Fatalf("immutable update error = %v", err)
	}

	if _, err := governance.SetStrategyRuntimeMode(context.Background(), mainDB, governance.StrategyModePaused, v150.StrategyVersion, "test pause", "migration-test"); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Model(&current).Update("stock_name", "blocked").Error; err == nil || !strings.Contains(err.Error(), "strategy production is paused") {
		t.Fatalf("paused update error = %v", err)
	}
}

func TestMigrateRejectsChecksumConflict(t *testing.T) {
	database := openMigrationTestDB(t, filepath.Join(t.TempDir(), "stock.db"))
	if err := MigrateMain(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&MigrationRecord{}).Where("id = ?", 1).Update("checksum", "tampered").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateMain(database); err == nil || !strings.Contains(err.Error(), "checksum conflict") {
		t.Fatalf("checksum conflict error = %v", err)
	}
}

func TestPublishedBaselineUpgradesToDefinitionLock(t *testing.T) {
	mainDB := openMigrationTestDB(t, filepath.Join(t.TempDir(), "stock.db"))
	minuteDB := openMigrationTestDB(t, filepath.Join(t.TempDir(), "minute.db"))

	if err := migrate(mainDB, "main", mainMigrations[:1], 1); err != nil {
		t.Fatalf("apply published main baseline: %v", err)
	}
	if err := migrate(minuteDB, "minute", minuteMigrations[:1], 1); err != nil {
		t.Fatalf("apply published minute baseline: %v", err)
	}
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatalf("upgrade published baselines: %v", err)
	}

	mainStatus, err := VerifyMain(mainDB)
	if err != nil {
		t.Fatal(err)
	}
	minuteStatus, err := VerifyMinute(minuteDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(mainStatus.Records) != 2 || len(minuteStatus.Records) != 2 {
		t.Fatalf("definition-lock migrations were not applied: main=%+v minute=%+v", mainStatus.Records, minuteStatus.Records)
	}
	if mainStatus.Records[0].Checksum != "41df05f8dbf7b1c56fe959ee8893d97938ddfe35425e98110333e47e2ee40ba6" {
		t.Fatalf("published main baseline checksum changed: %s", mainStatus.Records[0].Checksum)
	}
	if minuteStatus.Records[0].Checksum != "e838c98300ecee89806e5da10fc424bacff60754e212b449066feadecf59c8ec" {
		t.Fatalf("published minute baseline checksum changed: %s", minuteStatus.Records[0].Checksum)
	}
}

func TestDefinitionLockRejectsExistingGuardConflict(t *testing.T) {
	database := openMigrationTestDB(t, filepath.Join(t.TempDir(), "stock.db"))
	if err := migrate(database, "main", mainMigrations[:1], 1); err != nil {
		t.Fatal(err)
	}
	guard := strategyGuardStatements()[0]
	if err := database.Exec("DROP TRIGGER " + guard.name).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("CREATE TRIGGER " + guard.name + " BEFORE INSERT ON ai_recommend_stocks BEGIN SELECT RAISE(ABORT, 'tampered'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateMain(database); err == nil || !strings.Contains(err.Error(), "definition conflict") {
		t.Fatalf("guard definition conflict error = %v", err)
	}
}

func TestDefinitionLockRejectsExistingMinuteIndexConflict(t *testing.T) {
	database := openMigrationTestDB(t, filepath.Join(t.TempDir(), "minute.db"))
	if err := migrate(database, "minute", minuteMigrations[:1], 1); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("DROP INDEX idx_minute_bar_trade_time").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("CREATE INDEX idx_minute_bar_trade_time ON minute_bar(stock_code)").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateMinute(database); err == nil || !strings.Contains(err.Error(), "definition conflict") {
		t.Fatalf("minute index definition conflict error = %v", err)
	}
}

func TestMigrationChecksumIncludesDefinition(t *testing.T) {
	base := migration{
		id:          7,
		name:        "definition_test",
		description: "checksum definition coverage",
		definition:  func() string { return "schema-a" },
	}
	changed := base
	changed.definition = func() string { return "schema-b" }
	if base.checksum() == changed.checksum() {
		t.Fatal("migration checksum did not change with its definition")
	}
}

func TestDefinitionLockIgnoresInternalGoPackageLocation(t *testing.T) {
	definition := mainMigrationDefinition()
	if strings.Contains(definition, "go-stock/") {
		t.Fatalf("schema definition contains an internal Go package path: %s", definition)
	}
}

func TestBaselineMigrationChecksums(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "main-v1-published", got: mainMigrations[0].checksum(), want: "41df05f8dbf7b1c56fe959ee8893d97938ddfe35425e98110333e47e2ee40ba6"},
		{name: "main-v2-definition-lock", got: mainMigrations[1].checksum(), want: "616fac7d92781aa3c88470d13f7a34df4ec2d35772978167a03b570385b8e9b3"},
		{name: "minute-v1-published", got: minuteMigrations[0].checksum(), want: "e838c98300ecee89806e5da10fc424bacff60754e212b449066feadecf59c8ec"},
		{name: "minute-v2-definition-lock", got: minuteMigrations[1].checksum(), want: "f479775a220b2f4816aaa254c0193f49861fb8d61181634607b76e338debbde0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("baseline checksum = %s, want %s", test.got, test.want)
			}
		})
	}
}

func openMigrationTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := database.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return database
}
