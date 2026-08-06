package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-stock/backend/db"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/migrations"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type recordingStorageAdmin struct {
	statusCalls  int
	main         cliports.DatabaseStatus
	minute       cliports.DatabaseStatus
	backupMain   string
	backupMinute string
}

func (a *recordingStorageAdmin) Status(context.Context) (cliports.DatabaseStatus, cliports.DatabaseStatus, error) {
	a.statusCalls++
	return a.main, a.minute, nil
}

func (*recordingStorageAdmin) Migrate(context.Context) error { return nil }

func (a *recordingStorageAdmin) Verify(context.Context) (cliports.DatabaseStatus, cliports.DatabaseStatus, error) {
	return a.main, a.minute, nil
}

func (a *recordingStorageAdmin) Backup(_ context.Context, mainPath, minutePath string) error {
	a.backupMain = mainPath
	a.backupMinute = minutePath
	return nil
}
func (*recordingStorageAdmin) QuickCheck(context.Context) error { return nil }
func (*recordingStorageAdmin) Close() error                     { return nil }

func TestRunDBWithAdminUsesInjectedStoragePort(t *testing.T) {
	admin := &recordingStorageAdmin{
		main:   cliports.DatabaseStatus{CurrentVersion: 2, ExpectedVersion: 2, QuickCheck: "ok"},
		minute: cliports.DatabaseStatus{CurrentVersion: 2, ExpectedVersion: 2, QuickCheck: "ok"},
	}
	var stdout, stderr bytes.Buffer
	if err := runDBWithAdmin("status", nil, GlobalOptions{}, &stdout, &stderr, admin); err != nil {
		t.Fatalf("run status: %v", err)
	}
	if admin.statusCalls != 1 {
		t.Fatalf("status calls = %d, want 1", admin.statusCalls)
	}
	if !strings.Contains(stdout.String(), "main schema: 2/2") {
		t.Fatalf("unexpected status output: %q", stdout.String())
	}
}

func TestRunDBWithAdminParsesBackupOutput(t *testing.T) {
	admin := &recordingStorageAdmin{}
	outputDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := runDBWithAdmin("backup", []string{"--output", outputDir}, GlobalOptions{}, &stdout, &stderr, admin); err != nil {
		t.Fatalf("run backup: %v", err)
	}
	if admin.backupMain != filepath.Join(outputDir, "stock.db") || admin.backupMinute != filepath.Join(outputDir, "minute.db") {
		t.Fatalf("backup paths = %q, %q", admin.backupMain, admin.backupMinute)
	}
}

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

func TestDBStatusDoesNotModifyMigratedDatabases(t *testing.T) {
	directory := t.TempDir()
	mainPath := filepath.Join(directory, "stock.db")
	minutePath := filepath.Join(directory, "minute.db")
	t.Setenv("GO_STOCK_MINUTE_DB_PATH", minutePath)
	db.Init(mainPath)
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db.Dao, db.MinuteDao = nil, nil
	t.Cleanup(func() {
		_ = db.Close()
		db.Dao, db.MinuteDao = nil, nil
	})

	beforeMain := databaseFileHash(t, mainPath)
	beforeMinute := databaseFileHash(t, minutePath)
	var stdout, stderr bytes.Buffer
	if err := runDB([]string{"status"}, GlobalOptions{DBPath: mainPath}, &stdout, &stderr); err != nil {
		t.Fatalf("db status: %v stderr=%s", err, stderr.String())
	}
	if after := databaseFileHash(t, mainPath); after != beforeMain {
		t.Fatal("db status modified the primary database")
	}
	if after := databaseFileHash(t, minutePath); after != beforeMinute {
		t.Fatal("db status modified the minute database")
	}
}

func databaseFileHash(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(contents)
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
