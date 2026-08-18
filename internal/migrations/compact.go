package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Compact reclaims free SQLite pages after an offline destructive migration.
// Callers must stop every application process using the database first.
func Compact(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("database is not initialized")
	}
	if err := database.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return fmt.Errorf("checkpoint database before compact: %w", err)
	}
	if err := database.Exec("VACUUM").Error; err != nil {
		return fmt.Errorf("vacuum database: %w", err)
	}
	if err := database.Exec("PRAGMA optimize").Error; err != nil {
		return fmt.Errorf("optimize database: %w", err)
	}
	result := quickCheck(database)
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("compact quick_check returned %q", result)
	}
	return nil
}
