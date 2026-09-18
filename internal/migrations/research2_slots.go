package migrations

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go-stock/backend/research2"
	"go-stock/internal/trading"
	"gorm.io/gorm"
)

func mainMigrationV28Definition() string {
	return "research2 24 five-minute accounts; unique date/scheduled_slot/attempt; unique date/slot execution and sell receipt; first persisted report owns slot; account-scoped history; seed every account old cash plus 10000 once; rebase performance at migration; retain old aggregate snapshots"
}

func applyResearch2Slots(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database unavailable")
	}
	// An explicit baseline is also the idempotence marker for isolated fixture
	// invocation. The outer schema transaction commits it with the migration row.
	if tx.Migrator().HasColumn(&research2.Account{}, "baseline_at") {
		var count int64
		if err := tx.Model(&research2.Account{}).Where("baseline_at IS NOT NULL").Count(&count).Error; err != nil {
			return err
		}
		if count == 24 {
			return verifyMainSchema28Runtime(tx)
		}
		if count != 0 {
			return errors.New("partial research2 slot initialization")
		}
	}
	// Drop replaced uniqueness before adding the new model indexes.
	for _, name := range []string{"idx_research2_runs_date_attempt", "idx_research2_execution_chains_trading_date"} {
		if err := tx.Exec("DROP INDEX IF EXISTS " + name).Error; err != nil {
			return err
		}
	}
	for _, model := range []any{&research2.Account{}, &research2.AnalysisRun{}, &research2.ExecutionChain{}, &research2.Recommendation{}, &research2.Trade{}, &research2.AccountSnapshot{}} {
		if err := tx.AutoMigrate(model); err != nil {
			return err
		}
	}
	var old research2.Account
	if err := tx.First(&old, 1).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	seed := old.Cash
	cash := seed + 10000
	if cash < 0 {
		return errors.New("invalid research2 seed cash")
	}
	var items []research2.Recommendation
	if err := tx.Find(&items).Error; err != nil {
		return err
	}
	values := map[string]float64{}
	for _, item := range items {
		slot := research2.SlotAt(item.SignalAt)
		exception := slot == ""
		if exception {
			slot = research2.DefaultSlot
		}
		updates := map[string]any{"slot": slot, "legacy_slot_exception": exception}
		if item.Status == "active" || item.Status == "sell_pending" {
			price := item.CurrentPrice
			if price <= 0 {
				price = item.BuyMarketPrice
			}
			if price <= 0 {
				price = item.BuyPrice
			}
			if price <= 0 || item.Quantity <= 0 {
				return fmt.Errorf("invalid migrated holding %s", item.RecommendationID)
			}
			value := trading.CalculateSellCost(price, item.Quantity).NetCashFlow
			updates["baseline_value"] = value
			values[slot] += value
		}
		if err := tx.Model(&research2.Recommendation{}).Where("id = ?", item.ID).UpdateColumns(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&research2.Trade{}).Where("recommendation_id = ?", item.RecommendationID).UpdateColumn("slot", slot).Error; err != nil {
			return err
		}
	}
	for _, slot := range research2.Slots() {
		if slot == research2.DefaultSlot {
			if err := tx.Model(&research2.Account{}).Where("id = ?", 1).Updates(map[string]any{"slot": slot, "cash": cash, "seed_cash": seed, "baseline_at": now, "baseline_net_asset_value": cash + values[slot]}).Error; err != nil {
				return err
			}
		} else {
			account := research2.Account{Slot: slot, InitialCash: cash, Cash: cash, SeedCash: seed, BaselineAt: &now, BaselineNetAssetValue: cash + values[slot]}
			// Historical migrations may have created fixture accounts using current models.
			var existing research2.Account
			err := tx.Where("slot = ?", slot).First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = tx.Create(&account).Error
			} else if err == nil {
				err = tx.Model(&existing).Updates(map[string]any{"initial_cash": cash, "cash": cash, "seed_cash": seed, "baseline_at": now, "baseline_net_asset_value": cash + values[slot]}).Error
			}
			if err != nil {
				return err
			}
		}
		snapshot := research2.AccountSnapshot{Slot: slot, SnapshotID: uuid.NewString(), ValuedAt: now, TradingDate: now.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02"), SnapshotType: "slot_baseline", Cash: cash, PositionValue: values[slot], NetAssetValue: cash + values[slot]}
		if err := tx.Create(&snapshot).Error; err != nil {
			return err
		}
	}
	var runs []research2.AnalysisRun
	if err := tx.Find(&runs).Error; err != nil {
		return err
	}
	for _, run := range runs {
		planned := research2.SlotAt(run.ScheduledFor)
		if planned == "" {
			planned = research2.DefaultSlot
		}
		slot := ""
		if run.GeneratedAt != nil {
			slot = research2.SlotAt(*run.GeneratedAt)
		}
		if slot == "" {
			slot = research2.DefaultSlot
		}
		if err := tx.Model(&research2.AnalysisRun{}).Where("id = ?", run.ID).UpdateColumns(map[string]any{"scheduled_slot": planned, "slot": slot, "published": run.Status == "success" || run.Status == "no_recommendation", "persisted_at": run.GeneratedAt}).Error; err != nil {
			return err
		}
	}
	// Legacy pending orders cannot execute under their old 10:00 schedule.
	if err := tx.Model(&research2.Recommendation{}).Where("status IN ?", []string{"buy_pending", "standby"}).UpdateColumns(map[string]any{"status": "analysis_only", "failure_reason": "分区迁移已结束旧买入计划"}).Error; err != nil {
		return err
	}
	return verifyMainSchema28Runtime(tx)
}

func verifyMainSchema28Runtime(db *gorm.DB) error {
	var accounts []research2.Account
	if err := db.Find(&accounts).Error; err != nil {
		return err
	}
	if len(accounts) != 24 {
		return fmt.Errorf("schema 28 requires 24 accounts, got %d", len(accounts))
	}
	seen := map[string]bool{}
	for _, account := range accounts {
		if !research2.ValidSlot(account.Slot) || seen[account.Slot] || account.BaselineAt == nil || account.Cash < 0 {
			return errors.New("invalid research2 slot account")
		}
		seen[account.Slot] = true
	}
	for _, model := range []any{&research2.AnalysisRun{}, &research2.Recommendation{}, &research2.Trade{}, &research2.AccountSnapshot{}, &research2.ExecutionChain{}} {
		if !db.Migrator().HasColumn(model, "slot") {
			return fmt.Errorf("schema 28 missing slot on %T", model)
		}
	}
	return nil
}
