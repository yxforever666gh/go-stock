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
	if len(mainStatus.Records) != 1 || len(minuteStatus.Records) != 1 {
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
