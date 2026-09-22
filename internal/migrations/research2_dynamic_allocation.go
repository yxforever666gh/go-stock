package migrations

import (
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/research2"

	"gorm.io/gorm"
)

func mainMigrationV34Definition() string {
	return strings.Join([]string{
		"research2_execution_chains allocation_policy with legacy-recorded default",
		"research2_recommendations historical_replay_id and historical_sell_blocked",
		"research2_allocation_replays immutable plan identity and result summary",
		"migration does not fetch market data or rewrite reports, trades, accounts or capital events",
	}, "\n")
}

func applyResearch2DynamicAllocation(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := tx.AutoMigrate(&research2.ExecutionChain{}, &research2.Recommendation{}, &research2.AllocationReplay{}); err != nil {
		return fmt.Errorf("add research2 dynamic allocation schema: %w", err)
	}
	if err := tx.Model(&research2.ExecutionChain{}).
		Where("TRIM(COALESCE(allocation_policy, '')) = ''").
		Update("allocation_policy", research2.AllocationPolicyLegacyRecorded).Error; err != nil {
		return fmt.Errorf("initialize research2 allocation policy: %w", err)
	}
	return verifyMainSchema34Runtime(tx)
}

func verifyMainSchema34Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	if !database.Migrator().HasColumn(&research2.ExecutionChain{}, "AllocationPolicy") {
		return errors.New("main schema 34 missing research2 execution-chain allocation policy")
	}
	for _, column := range []string{"HistoricalReplayID", "HistoricalSellBlocked"} {
		if !database.Migrator().HasColumn(&research2.Recommendation{}, column) {
			return fmt.Errorf("main schema 34 missing research2 recommendation column %s", column)
		}
	}
	if !database.Migrator().HasTable(&research2.AllocationReplay{}) {
		return errors.New("main schema 34 missing research2_allocation_replays")
	}
	var invalid int64
	if err := database.Model(&research2.ExecutionChain{}).
		Where("allocation_policy NOT IN ? OR allocation_policy IS NULL", []string{research2.AllocationPolicyLegacyRecorded, research2.AllocationPolicyRemainingCashSlots}).
		Count(&invalid).Error; err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("main schema 34 has %d invalid allocation policies", invalid)
	}
	return nil
}
