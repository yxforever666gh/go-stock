package migrations

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research2"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const research2CapitalSecondTopUpAmount = 10000.0

var research2CapitalSecondTopUpAt = time.Date(2026, 9, 23, 9, 25, 0, 0, research2Shanghai())

func mainMigrationV35Definition() string {
	return strings.Join([]string{
		"stock_master_refresh_metadata singleton audit table",
		"every Research Center 2 slot receives one deterministic 10000 external top-up effective 2026-09-23 09:25 Asia/Shanghai",
		"account cash and derived capital-ledger snapshots include the top-up without changing AI reports or minute data",
	}, "\n")
}

func applyStockMasterMetadataAndResearch2TopUp(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := tx.AutoMigrate(&models.StockMasterRefreshMetadata{}); err != nil {
		return fmt.Errorf("create stock master refresh metadata schema: %w", err)
	}
	var accounts []research2.Account
	if err := tx.Order("slot ASC").Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) != len(research2.Slots()) {
		return fmt.Errorf("research2 top-up requires %d slot accounts, got %d", len(research2.Slots()), len(accounts))
	}
	accountBySlot := make(map[string]research2.Account, len(accounts))
	for _, account := range accounts {
		accountBySlot[account.Slot] = account
	}
	for _, slot := range research2.Slots() {
		if _, ok := accountBySlot[slot]; !ok {
			return fmt.Errorf("research2 top-up account %s is missing", slot)
		}
		expected := research2CapitalEvent(slot, research2.CapitalEventTopUp, research2CapitalSecondTopUpAmount, true, "user_top_up", research2CapitalSecondTopUpAt, "top-up-20260923")
		var stored research2.AccountCapitalEvent
		err := tx.Where("event_id = ?", expected.EventID).First(&stored).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&expected).Error; err != nil {
				return fmt.Errorf("write research2 top-up event %s: %w", expected.EventID, err)
			}
			result := tx.Model(&research2.Account{}).Where("slot = ?", slot).Update("cash", gorm.Expr("cash + ?", research2CapitalSecondTopUpAmount))
			if result.Error != nil {
				return fmt.Errorf("credit research2 account %s: %w", slot, result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("credit research2 account %s affected %d rows", slot, result.RowsAffected)
			}
		case err != nil:
			return err
		case !research2CapitalEventMatches(stored, expected):
			return fmt.Errorf("research2 top-up event %s conflicts with the authorized event", expected.EventID)
		}
	}
	if err := research2RebuildLedgerSnapshots(tx); err != nil {
		return fmt.Errorf("rebuild research2 capital ledger after top-up: %w", err)
	}
	return verifyMainSchema35Runtime(tx)
}

func research2CapitalEventMatches(actual, expected research2.AccountCapitalEvent) bool {
	return actual.EventID == expected.EventID && actual.Slot == expected.Slot && actual.EventType == expected.EventType &&
		math.Abs(actual.Amount-expected.Amount) <= 0.01 && actual.External == expected.External && actual.Source == expected.Source &&
		actual.EffectiveAt.Equal(expected.EffectiveAt) && actual.TradingDate == expected.TradingDate
}

func verifyMainSchema35Runtime(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&models.StockMasterRefreshMetadata{}) {
		return errors.New("main schema 35 stock master refresh metadata table is missing")
	}
	for _, column := range []string{"Source", "FetchedAt", "RowCount", "ValidRows", "SHA256", "UsedSeed", "Warnings"} {
		if !tx.Migrator().HasColumn(&models.StockMasterRefreshMetadata{}, column) {
			return fmt.Errorf("main schema 35 stock master refresh metadata column %s is missing", column)
		}
	}
	var accounts []research2.Account
	if err := tx.Order("slot ASC").Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) != len(research2.Slots()) {
		return fmt.Errorf("main schema 35 has %d research2 accounts, want %d", len(accounts), len(research2.Slots()))
	}
	for _, account := range accounts {
		expected := research2CapitalEvent(account.Slot, research2.CapitalEventTopUp, research2CapitalSecondTopUpAmount, true, "user_top_up", research2CapitalSecondTopUpAt, "top-up-20260923")
		var stored research2.AccountCapitalEvent
		if err := tx.Where("event_id = ?", expected.EventID).First(&stored).Error; err != nil {
			return fmt.Errorf("main schema 35 slot %s top-up is unavailable: %w", account.Slot, err)
		}
		if !research2CapitalEventMatches(stored, expected) {
			return fmt.Errorf("main schema 35 slot %s top-up conflicts", account.Slot)
		}
		var external, transfer, tradeFlow float64
		var externalCount int64
		if err := tx.Model(&research2.AccountCapitalEvent{}).Where("slot = ? AND external = ?", account.Slot, true).Count(&externalCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&research2.AccountCapitalEvent{}).Where("slot = ? AND external = ?", account.Slot, true).Select("COALESCE(SUM(amount), 0)").Scan(&external).Error; err != nil {
			return err
		}
		if err := tx.Model(&research2.AccountCapitalEvent{}).Where("slot = ? AND external = ?", account.Slot, false).Select("COALESCE(SUM(amount), 0)").Scan(&transfer).Error; err != nil {
			return err
		}
		if err := tx.Model(&research2.Trade{}).Where("slot = ?", account.Slot).Select("COALESCE(SUM(net_cash_flow), 0)").Scan(&tradeFlow).Error; err != nil {
			return err
		}
		if externalCount != 3 || math.Abs(external-30000) > 0.01 {
			return fmt.Errorf("main schema 35 slot %s external capital is %.2f across %d events", account.Slot, external, externalCount)
		}
		if math.Abs(account.InitialCash-research2CapitalInitialAmount) > 0.01 || account.Cash < -research2CapitalRebaseEpsilon {
			return fmt.Errorf("main schema 35 slot %s account balance is invalid", account.Slot)
		}
		if math.Abs(account.Cash-(external+transfer+tradeFlow)) > 0.01 {
			return fmt.Errorf("main schema 35 slot %s cash %.2f does not match capital/trade replay %.2f", account.Slot, account.Cash, external+transfer+tradeFlow)
		}
		snapshotID := research2LedgerSnapshotID(account.Slot, research2.CapitalEventTopUp, expected.EventID)
		var snapshots int64
		if err := tx.Model(&research2.AccountLedgerSnapshot{}).Where("snapshot_id = ?", snapshotID).Count(&snapshots).Error; err != nil {
			return err
		}
		if snapshots != 1 {
			return fmt.Errorf("main schema 35 slot %s top-up ledger snapshot count is %d", account.Slot, snapshots)
		}
	}
	return nil
}
