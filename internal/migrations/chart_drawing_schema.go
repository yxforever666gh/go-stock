package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const chartDrawingDocumentsTableSQL = `CREATE TABLE IF NOT EXISTS chart_drawing_documents (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  drawing_document_id TEXT NOT NULL,
  scope_type TEXT NOT NULL,
  scope_id TEXT NOT NULL,
  asset_type TEXT NOT NULL,
  market TEXT NOT NULL,
  code TEXT NOT NULL,
  period TEXT NOT NULL,
  adjustment TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
  drawings_json TEXT NOT NULL DEFAULT '[]',
  deleted_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const chartDrawingRevisionsTableSQL = `CREATE TABLE IF NOT EXISTS chart_drawing_revisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  document_id TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK (revision >= 0),
  drawings_json TEXT NOT NULL,
  deleted_at DATETIME,
  created_at DATETIME NOT NULL
)`

var chartDrawingIndexSQL = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_chart_drawing_documents_public_id ON chart_drawing_documents(drawing_document_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_chart_drawing_documents_scope_asset ON chart_drawing_documents(scope_type, scope_id, asset_type, market, code, period, adjustment)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_chart_drawing_revisions_document_revision ON chart_drawing_revisions(document_id, revision)`,
}

var chartDrawingRevisionTriggerSQL = []string{
	`CREATE TRIGGER IF NOT EXISTS immutable_chart_drawing_revisions_update
BEFORE UPDATE ON chart_drawing_revisions
BEGIN
  SELECT RAISE(ABORT, 'chart drawing revision is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_chart_drawing_revisions_delete
BEFORE DELETE ON chart_drawing_revisions
BEGIN
  SELECT RAISE(ABORT, 'chart drawing revision is immutable');
END`,
}

func mainMigrationV16Definition() string {
	parts := []string{chartDrawingDocumentsTableSQL, chartDrawingRevisionsTableSQL}
	parts = append(parts, chartDrawingIndexSQL...)
	parts = append(parts, chartDrawingRevisionTriggerSQL...)
	return strings.Join(parts, ";\n\n") + ";\n"
}

func applyChartDrawingSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, tableSQL := range []string{chartDrawingDocumentsTableSQL, chartDrawingRevisionsTableSQL} {
		if err := tx.Exec(tableSQL).Error; err != nil {
			return fmt.Errorf("create chart drawing table: %w", err)
		}
	}
	for _, indexSQL := range chartDrawingIndexSQL {
		if err := tx.Exec(indexSQL).Error; err != nil {
			return fmt.Errorf("create chart drawing index: %w", err)
		}
	}
	for _, triggerSQL := range chartDrawingRevisionTriggerSQL {
		if err := tx.Exec(triggerSQL).Error; err != nil {
			return fmt.Errorf("create immutable chart drawing revision trigger: %w", err)
		}
	}
	return verifyMainSchema16Runtime(tx)
}

func verifyMainSchema16Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	objects := []struct {
		objectType string
		name       string
		statement  string
	}{
		{objectType: "table", name: "chart_drawing_documents", statement: chartDrawingDocumentsTableSQL},
		{objectType: "table", name: "chart_drawing_revisions", statement: chartDrawingRevisionsTableSQL},
	}
	for _, statement := range chartDrawingIndexSQL {
		objects = append(objects, struct {
			objectType string
			name       string
			statement  string
		}{objectType: "index", name: sqliteSchemaObjectName(statement), statement: sqliteStoredIndexSQL(statement)})
	}
	triggerNames := []string{"immutable_chart_drawing_revisions_update", "immutable_chart_drawing_revisions_delete"}
	for index, statement := range chartDrawingRevisionTriggerSQL {
		objects = append(objects, struct {
			objectType string
			name       string
			statement  string
		}{objectType: "trigger", name: triggerNames[index], statement: sqliteStoredIndexSQL(statement)})
	}
	for _, object := range objects {
		if err := verifySQLiteSchemaObject(database, object.objectType, object.name, object.statement); err != nil {
			return fmt.Errorf("verify main schema 16 %s %s: %w", object.objectType, object.name, err)
		}
	}
	return nil
}
