package migrations

import (
	"context"
	"go-stock/backend/research2"
	"go-stock/internal/trading"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

func TestSchema28SeedsCashOnceAndPartitionsHoldings(t *testing.T) {
	db := openMigrationTestDB(t)
	for _, model := range []any{&research2.Account{}, &research2.AnalysisRun{}, &research2.ExecutionChain{}, &research2.Recommendation{}, &research2.Trade{}, &research2.AccountSnapshot{}} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&research2.Account{ID: 1, InitialCash: 12000, Cash: 4321, Slot: "09:50"}).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 17, 10, 7, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	stock := research2.Recommendation{RecommendationID: "old", AnalysisRunID: "history", StockCode: "sh600000", Status: "active", SignalAt: at, BuyAt: &at, BuyPrice: 10, BuyMarketPrice: 10, CurrentPrice: 11, Quantity: 100}
	if err := db.Create(&stock).Error; err != nil {
		t.Fatal(err)
	}
	// Reproduce the old column and index shape, not just current models with
	// empty account metadata. This catches ALTER/index failures on real upgrades.
	for _, index := range []string{"idx_research2_accounts_slot", "idx_research2_runs_date_attempt", "idx_research2_execution_chains_trading_date", "idx_research2_analysis_runs_slot", "idx_research2_recommendations_slot", "idx_research2_trades_slot", "idx_research2_account_snapshots_slot"} {
		if err := db.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
			t.Fatal(err)
		}
	}
	for table, fields := range map[string][]string{
		"research2_accounts":          {"slot", "baseline_at", "baseline_net_asset_value", "seed_cash"},
		"research2_analysis_runs":     {"scheduled_slot", "slot", "published", "archive_reason", "persisted_at"},
		"research2_execution_chains":  {"slot", "winner_run_id", "sell_completed_at", "allocation_base_cash"},
		"research2_recommendations":   {"slot", "legacy_slot_exception", "baseline_value", "period_pn_l"},
		"research2_trades":            {"slot", "quote_at", "price_stale"},
		"research2_account_snapshots": {"slot"},
	} {
		for _, field := range fields {
			if err := db.Exec("ALTER TABLE " + table + " DROP COLUMN " + field).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_research2_runs_date_attempt ON research2_analysis_runs(trading_date,attempt_no); CREATE UNIQUE INDEX idx_research2_execution_chains_trading_date ON research2_execution_chains(trading_date)").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Transaction(applyResearch2Slots); err != nil {
			t.Fatal(err)
		}
	}
	var accounts []research2.Account
	db.Find(&accounts)
	if len(accounts) != 24 {
		t.Fatal(len(accounts))
	}
	for _, a := range accounts {
		if a.Cash != 14321 || a.SeedCash != 4321 || a.BaselineAt == nil {
			t.Fatal(a)
		}
	}
	if err := db.Where("recommendation_id = ?", "old").First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.Slot != "10:05" || stock.BaselineValue == nil {
		t.Fatal(stock)
	}
	r := research2.NewRepository(db).WithSlot("10:05")
	overview, err := r.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	currentSell, err := trading.CalculateAShareSellCost(stock.StockCode, stock.CurrentPrice, stock.Quantity)
	if err != nil {
		t.Fatal(err)
	}
	legacySell := trading.CalculateSellCost(stock.CurrentPrice, stock.Quantity)
	if math.Abs(overview.NetProfit-(currentSell.NetCashFlow-legacySell.NetCashFlow)) > 1e-8 {
		t.Fatal(overview)
	}
	var count int64
	db.Model(&research2.Recommendation{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicated holding", count)
	}
}

// Earlier migrations promise preservation; migration 28 intentionally changes
// capital and adds accounts and is verified by separate migration tests.
func migrateBeforeResearch2Rebase(mainDB, minuteDB *gorm.DB) error {
	if err := migrate(mainDB, "main", mainMigrations[:27], 27); err != nil {
		return err
	}
	return migrate(minuteDB, "minute", minuteMigrations, 3)
}
