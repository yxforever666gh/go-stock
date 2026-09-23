package migrations

import (
	"math"
	"testing"

	"go-stock/backend/models"
	"go-stock/backend/research2"

	"gorm.io/gorm"
)

func schema35Fixture(t *testing.T) *gorm.DB {
	t.Helper()
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&research2.Account{}, &research2.Trade{}, &research2.AccountCapitalEvent{}, &research2.AccountLedgerSnapshot{}); err != nil {
		t.Fatal(err)
	}
	for _, slot := range research2.Slots() {
		account := research2.Account{Slot: slot, InitialCash: research2CapitalInitialAmount, Cash: 2 * research2CapitalInitialAmount}
		if err := database.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		events := []research2.AccountCapitalEvent{
			research2CapitalEvent(slot, research2.CapitalEventInitial, research2CapitalInitialAmount, true, "user_initial_capital", research2CapitalInitialAt, "initial"),
			research2CapitalEvent(slot, research2.CapitalEventTopUp, research2CapitalInitialAmount, true, "user_top_up", research2CapitalTopUpAt, "top-up-20260921"),
		}
		if err := database.Create(&events).Error; err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestSchema35RepairsStockMasterMetadataAndCreditsEverySlotOnce(t *testing.T) {
	database := schema35Fixture(t)
	if err := verifyMainSchema35Runtime(database); err == nil {
		t.Fatal("schema 35 verifier accepted missing stock-master metadata and top-ups")
	}
	if err := applyStockMasterMetadataAndResearch2TopUp(database); err != nil {
		t.Fatal(err)
	}
	if err := applyStockMasterMetadataAndResearch2TopUp(database); err != nil {
		t.Fatalf("repeated schema 35 apply was not idempotent: %v", err)
	}
	if !database.Migrator().HasTable(&models.StockMasterRefreshMetadata{}) {
		t.Fatal("stock-master metadata table was not created")
	}
	var events int64
	if err := database.Model(&research2.AccountCapitalEvent{}).Where("external = ?", true).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != int64(len(research2.Slots())*3) {
		t.Fatalf("external capital events=%d", events)
	}
	var accounts []research2.Account
	if err := database.Order("slot ASC").Find(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if math.Abs(account.Cash-30000) > 0.01 || math.Abs(account.InitialCash-10000) > 0.01 {
			t.Fatalf("slot %s account=%+v", account.Slot, account)
		}
	}
	if err := verifyMainSchema35Runtime(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&research2.AccountLedgerSnapshot{}).
		Where("slot = ? AND snapshot_type = ? AND valued_at = ?", "09:30", research2.CapitalEventTopUp, research2CapitalSecondTopUpAt).
		Update("snapshot_id", "replay-derived-top-up-09-30").Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema35Runtime(database); err != nil {
		t.Fatalf("schema 35 verifier rejected an equivalent replay-derived snapshot: %v", err)
	}
}

func TestSchema35RejectsMissingAccountAndBrokenCashEquation(t *testing.T) {
	missing := schema35Fixture(t)
	if err := missing.Where("slot = ?", "11:25").Delete(&research2.Account{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyStockMasterMetadataAndResearch2TopUp(missing); err == nil {
		t.Fatal("schema 35 accepted a missing slot account")
	}

	broken := schema35Fixture(t)
	if err := applyStockMasterMetadataAndResearch2TopUp(broken); err != nil {
		t.Fatal(err)
	}
	if err := broken.Model(&research2.Account{}).Where("slot = ?", "09:30").Update("cash", 30001).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema35Runtime(broken); err == nil {
		t.Fatal("schema 35 accepted a broken cash equation")
	}
}
