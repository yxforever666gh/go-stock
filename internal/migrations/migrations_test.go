package migrations

import (
	"testing"
	"time"

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
