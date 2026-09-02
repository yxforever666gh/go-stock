package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	research2AnalysisRunsTable = "research2_analysis_runs"
	research2TradesTable       = "research2_trades"
)

func mainMigrationV23Definition() string {
	return strings.Join([]string{
		"research2_analysis_runs.evidence_window_start_at DATETIME NULL",
		"research2_analysis_runs.evidence_coverage_pct REAL NULL",
		"research2_analysis_runs.degraded BOOLEAN NULL",
		"research2_trades.price_source VARCHAR(64) NULL",
		"research2_trades.execution_mode VARCHAR(32) NULL",
		"preserve schema 22 analysis, recommendation, trade, account and return history without backfill",
	}, "\n")
}

func applyResearch2Trailing5Schema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, table := range []string{research2AnalysisRunsTable, research2TradesTable} {
		if !tx.Migrator().HasTable(table) {
			return fmt.Errorf("%s table is unavailable", table)
		}
	}
	columns := []struct {
		table      string
		name       string
		definition string
	}{
		{research2AnalysisRunsTable, "evidence_window_start_at", "DATETIME"},
		{research2AnalysisRunsTable, "evidence_coverage_pct", "REAL"},
		{research2AnalysisRunsTable, "degraded", "BOOLEAN"},
		{research2TradesTable, "price_source", "VARCHAR(64)"},
		{research2TradesTable, "execution_mode", "VARCHAR(32)"},
	}
	for _, column := range columns {
		if tx.Migrator().HasColumn(column.table, column.name) {
			continue
		}
		if err := tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.name, column.definition)).Error; err != nil {
			return fmt.Errorf("add %s.%s: %w", column.table, column.name, err)
		}
	}
	return verifyMainSchema23Runtime(tx)
}

func verifyMainSchema23Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	columns := map[string][]string{
		research2AnalysisRunsTable: {"evidence_window_start_at", "evidence_coverage_pct", "degraded"},
		research2TradesTable:       {"price_source", "execution_mode"},
	}
	for table, names := range columns {
		if !database.Migrator().HasTable(table) {
			return fmt.Errorf("main schema 23 %s table is missing", table)
		}
		for _, name := range names {
			if !database.Migrator().HasColumn(table, name) {
				return fmt.Errorf("main schema 23 %s.%s is missing", table, name)
			}
		}
	}
	return nil
}
