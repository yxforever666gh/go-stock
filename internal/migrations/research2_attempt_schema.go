package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const research2DateAttemptUniqueIndex = "idx_research2_runs_date_attempt"

func mainMigrationV24Definition() string {
	return strings.Join([]string{
		"research2_analysis_runs.attempt_no INTEGER NOT NULL DEFAULT 1",
		"drop the former single-column unique index on research2_analysis_runs.trading_date",
		"unique research2_analysis_runs(trading_date, attempt_no)",
		"preserve run_id uniqueness and all historical rows as attempt 1 without rewriting any other field",
	}, "\n")
}

func applyResearch2AnalysisAttemptSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(research2AnalysisRunsTable) {
		return fmt.Errorf("%s table is unavailable", research2AnalysisRunsTable)
	}
	if !tx.Migrator().HasColumn(research2AnalysisRunsTable, "attempt_no") {
		if err := tx.Exec("ALTER TABLE research2_analysis_runs ADD COLUMN attempt_no INTEGER NOT NULL DEFAULT 1").Error; err != nil {
			return fmt.Errorf("add research2_analysis_runs.attempt_no: %w", err)
		}
	}
	indexes, err := research2AnalysisRunIndexes(tx)
	if err != nil {
		return err
	}
	for _, index := range indexes {
		if !index.Unique || len(index.Columns) != 1 || index.Columns[0] != "trading_date" {
			continue
		}
		if strings.HasPrefix(index.Name, "sqlite_autoindex_") {
			return fmt.Errorf("legacy trading_date uniqueness is an inline SQLite constraint %q; refusing to rebuild the table", index.Name)
		}
		if err := tx.Exec("DROP INDEX IF EXISTS " + quoteSQLiteIdentifier(index.Name)).Error; err != nil {
			return fmt.Errorf("drop legacy trading-date index %s: %w", index.Name, err)
		}
	}
	if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS " + research2DateAttemptUniqueIndex + " ON research2_analysis_runs(trading_date, attempt_no)").Error; err != nil {
		return fmt.Errorf("create research2 date-attempt unique index: %w", err)
	}
	return verifyMainSchema24Runtime(tx)
}

type research2SQLiteIndex struct {
	Name    string
	Unique  bool
	Columns []string
}

func research2AnalysisRunIndexes(database *gorm.DB) ([]research2SQLiteIndex, error) {
	var rows []struct {
		Name   string `gorm:"column:name"`
		Unique int    `gorm:"column:unique"`
	}
	if err := database.Raw("PRAGMA index_list('research2_analysis_runs')").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list research2 analysis indexes: %w", err)
	}
	result := make([]research2SQLiteIndex, 0, len(rows))
	for _, row := range rows {
		var columns []struct {
			Name string `gorm:"column:name"`
		}
		if err := database.Raw("PRAGMA index_info(" + quoteSQLiteIdentifier(row.Name) + ")").Scan(&columns).Error; err != nil {
			return nil, fmt.Errorf("inspect research2 analysis index %s: %w", row.Name, err)
		}
		index := research2SQLiteIndex{Name: row.Name, Unique: row.Unique != 0, Columns: make([]string, 0, len(columns))}
		for _, column := range columns {
			index.Columns = append(index.Columns, column.Name)
		}
		result = append(result, index)
	}
	return result, nil
}

func verifyMainSchema24Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	if !database.Migrator().HasColumn(research2AnalysisRunsTable, "attempt_no") {
		return errors.New("main schema 24 research2_analysis_runs.attempt_no is missing")
	}
	indexes, err := research2AnalysisRunIndexes(database)
	if err != nil {
		return err
	}
	foundComposite := false
	foundRunID := false
	for _, index := range indexes {
		if index.Unique && len(index.Columns) == 1 && index.Columns[0] == "trading_date" {
			return fmt.Errorf("main schema 24 still has single-column trading_date uniqueness: %s", index.Name)
		}
		if index.Unique && len(index.Columns) == 2 && index.Columns[0] == "trading_date" && index.Columns[1] == "attempt_no" {
			foundComposite = true
		}
		if index.Unique && len(index.Columns) == 1 && index.Columns[0] == "run_id" {
			foundRunID = true
		}
	}
	if !foundComposite {
		return errors.New("main schema 24 trading_date/attempt_no unique index is missing")
	}
	if !foundRunID {
		return errors.New("main schema 24 run_id unique index is missing")
	}
	return nil
}
