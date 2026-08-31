package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const research2RecommendationsTable = "research2_recommendations"

func mainMigrationV22Definition() string {
	return strings.Join([]string{
		"research2_recommendations DROP COLUMN rank",
		"research2_recommendations.current_price REAL",
		"research2_recommendations.current_price_at DATETIME",
		"idx_research2_recommendations_current_price_at",
		"initialize active and sell_pending last prices from their buy quotes without rewriting realized returns",
	}, "\n")
}

func applyResearch2LiveValuationSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(research2RecommendationsTable) {
		return errors.New("research2 recommendations table is unavailable")
	}
	if !tx.Migrator().HasColumn(research2RecommendationsTable, "current_price") {
		if err := tx.Exec(`ALTER TABLE research2_recommendations ADD COLUMN current_price REAL`).Error; err != nil {
			return fmt.Errorf("add research2 recommendation current price: %w", err)
		}
	}
	if !tx.Migrator().HasColumn(research2RecommendationsTable, "current_price_at") {
		if err := tx.Exec(`ALTER TABLE research2_recommendations ADD COLUMN current_price_at DATETIME`).Error; err != nil {
			return fmt.Errorf("add research2 recommendation current price time: %w", err)
		}
	}
	if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_research2_recommendations_current_price_at
ON research2_recommendations(current_price_at)`).Error; err != nil {
		return fmt.Errorf("index research2 recommendation current price time: %w", err)
	}

	// Raw SQL intentionally avoids GORM's UpdatedAt callback. Existing active
	// positions start with their last observed buy quote and buy time, while an
	// already-refreshed price remains untouched if this function is retried.
	if err := tx.Exec(`UPDATE research2_recommendations
SET current_price = CASE
        WHEN current_price IS NULL OR current_price <= 0 THEN
            CASE WHEN buy_market_price > 0 THEN buy_market_price ELSE buy_price END
        ELSE current_price
    END,
    current_price_at = COALESCE(current_price_at, buy_at)
WHERE status IN ('active', 'sell_pending')
  AND buy_at IS NOT NULL
  AND (buy_market_price > 0 OR buy_price > 0)`).Error; err != nil {
		return fmt.Errorf("initialize research2 recommendation current prices: %w", err)
	}

	if tx.Migrator().HasColumn(research2RecommendationsTable, "rank") {
		if err := tx.Exec(`ALTER TABLE research2_recommendations DROP COLUMN rank`).Error; err != nil {
			return fmt.Errorf("drop research2 recommendation rank: %w", err)
		}
	}
	return verifyMainSchema22Runtime(tx)
}

func verifyMainSchema22Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	if !database.Migrator().HasTable(research2RecommendationsTable) {
		return errors.New("main schema 22 research2 recommendations table is missing")
	}
	if database.Migrator().HasColumn(research2RecommendationsTable, "rank") {
		return errors.New("main schema 22 research2 recommendation rank still exists")
	}
	for _, column := range []string{"current_price", "current_price_at"} {
		if !database.Migrator().HasColumn(research2RecommendationsTable, column) {
			return fmt.Errorf("main schema 22 research2 recommendation %s is missing", column)
		}
	}
	if !database.Migrator().HasIndex(research2RecommendationsTable, "idx_research2_recommendations_current_price_at") {
		return errors.New("main schema 22 research2 recommendation current price time index is missing")
	}
	return nil
}
