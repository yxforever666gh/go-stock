package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestCompactReclaimsFreePagesAndKeepsIntegrity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compact.db")
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Exec(`CREATE TABLE payloads (id INTEGER PRIMARY KEY, payload BLOB);
WITH RECURSIVE rows(id) AS (SELECT 1 UNION ALL SELECT id + 1 FROM rows WHERE id < 5000)
INSERT INTO payloads(id, payload) SELECT id, randomblob(2048) FROM rows;
DELETE FROM payloads WHERE id > 10;`).Error; err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Compact(database); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("compact size = %d, want less than %d", after.Size(), before.Size())
	}
	if err := VerifySQLiteIntegrity(database); err != nil {
		t.Fatal(err)
	}
}
