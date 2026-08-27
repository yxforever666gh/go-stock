package migrations

import (
	"errors"
	"fmt"
	"math"

	"go-stock/backend/models"
	"go-stock/backend/research2"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func applyResearch2OvernightStrengthStrategy(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := tx.AutoMigrate(&models.Settings{}, &research2.AnalysisRun{}, &research2.Recommendation{}, &research2.Trade{}, &research2.Account{}, &research2.AccountSnapshot{}); err != nil {
		return err
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&research2.Account{ID: 1, InitialCash: research2.InitialCash, Cash: research2.InitialCash}).Error; err != nil {
		return err
	}
	return verifyMainSchema13Runtime(tx)
}

func verifyMainSchema13Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	modelsToCheck := []any{&research2.AnalysisRun{}, &research2.Recommendation{}, &research2.Trade{}, &research2.Account{}, &research2.AccountSnapshot{}}
	for _, model := range modelsToCheck {
		if !database.Migrator().HasTable(model) {
			return fmt.Errorf("main schema 13 missing table for %T", model)
		}
	}
	if !database.Migrator().HasColumn(&models.Settings{}, "research2_auto_enabled") {
		return errors.New("main schema 13 missing settings.research2_auto_enabled")
	}
	var account research2.Account
	if err := database.First(&account, 1).Error; err != nil {
		return err
	}
	if math.Abs(account.InitialCash-research2.InitialCash) > 1e-8 {
		return fmt.Errorf("research2 initial cash is %.2f, expected %.2f", account.InitialCash, research2.InitialCash)
	}
	if account.Cash < -1e-8 {
		return errors.New("research2 account cash is negative")
	}
	return nil
}
