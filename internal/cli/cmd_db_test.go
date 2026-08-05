package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestDBVerifyQuickOnlyAcceptsPreMigrationDatabases(t *testing.T) {
	directory := t.TempDir()
	mainPath := filepath.Join(directory, "stock.db")
	minutePath := filepath.Join(directory, "minute.db")
	createSchemaIndependentDB(t, mainPath, "legacy_main")
	createSchemaIndependentDB(t, minutePath, "legacy_minute")
	t.Setenv("GO_STOCK_MINUTE_DB_PATH", minutePath)

	var stdout, stderr bytes.Buffer
	err := runDB([]string{"verify", "--quick-only"}, GlobalOptions{DBPath: mainPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("quick-only verification failed: %v stderr=%s", err, stderr.String())
	}
	if output := stdout.String(); !strings.Contains(output, "main quick_check=ok") || !strings.Contains(output, "minute quick_check=ok") {
		t.Fatalf("unexpected quick-only output: %q", output)
	}
}

func createSchemaIndependentDB(t *testing.T, path, table string) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}
