package migrations

import (
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/research2"

	"gorm.io/gorm"
)

func mainMigrationV33Definition() string {
	return strings.Join([]string{
		"research2_recommendations buy_day_limit_outcome/status/evaluated_at/attempt_count/source_json/failure_reason",
		"research2_account_daily_valuations unique(slot,trading_date) and unique valuation_id",
		"existing hit_five_before_sell/hit_limit_up_full_day/hit_minus_three columns remain inert audit data",
		"no report, recommendation, trade, position, account cash or capital event is rewritten",
	}, "\n")
}

func applyResearch2PortfolioPerformance(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := tx.AutoMigrate(&research2.Recommendation{}, &research2.AccountDailyValuation{}); err != nil {
		return fmt.Errorf("add research2 portfolio performance schema: %w", err)
	}
	if err := tx.Model(&research2.Recommendation{}).
		Where("TRIM(COALESCE(buy_day_limit_status, '')) = ''").
		Updates(map[string]any{"buy_day_limit_status": research2.LimitOutcomePending, "buy_day_limit_source_json": "[]"}).Error; err != nil {
		return fmt.Errorf("initialize research2 buy-day outcomes: %w", err)
	}
	return verifyMainSchema33Runtime(tx)
}

func verifyMainSchema33Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	for _, column := range []string{"BuyDayLimitOutcome", "BuyDayLimitStatus", "BuyDayLimitEvaluatedAt", "BuyDayLimitAttemptCount", "BuyDayLimitSourceJSON", "BuyDayLimitFailureReason"} {
		if !database.Migrator().HasColumn(&research2.Recommendation{}, column) {
			return fmt.Errorf("main schema 33 missing research2 recommendation column %s", column)
		}
	}
	if !database.Migrator().HasTable(&research2.AccountDailyValuation{}) {
		return errors.New("main schema 33 missing research2_account_daily_valuations")
	}
	if !database.Migrator().HasIndex(&research2.AccountDailyValuation{}, "idx_research2_daily_valuation_slot_date") {
		return errors.New("main schema 33 missing daily valuation slot/date uniqueness")
	}
	var invalid int64
	if err := database.Model(&research2.Recommendation{}).Where("buy_day_limit_status NOT IN ? OR buy_day_limit_status IS NULL", []string{research2.LimitOutcomePending, research2.LimitOutcomeComplete, research2.LimitOutcomeUnavailable}).Count(&invalid).Error; err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("main schema 33 has %d invalid buy-day outcome statuses", invalid)
	}
	if err := database.Model(&research2.Recommendation{}).Where("(buy_day_limit_status = ? AND buy_day_limit_outcome NOT IN ?) OR (buy_day_limit_status <> ? AND TRIM(COALESCE(buy_day_limit_outcome, '')) <> '')", research2.LimitOutcomeComplete, []string{research2.LimitOutcomeSealed, research2.LimitOutcomeBroken, research2.LimitOutcomeUntouched}, research2.LimitOutcomeComplete).Count(&invalid).Error; err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("main schema 33 has %d inconsistent buy-day outcomes", invalid)
	}
	return nil
}
