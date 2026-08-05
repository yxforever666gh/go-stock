package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"go-stock/backend/db"
	"go-stock/internal/migrations"
)

func TestStrategyCommandRequiresMigrationAndAuditedResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stock.db")
	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"--db-path", path, "strategy", "status"}, &stdout, &stderr); code == 0 {
		t.Fatalf("status unexpectedly succeeded without runtime migration: %s", stdout.String())
	}

	db.Init(path)
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db.Dao = nil
	db.MinuteDao = nil

	stdout.Reset()
	stderr.Reset()
	if code := Execute([]string{"--db-path", path, "strategy", "status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status failed: code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "strategy mode: paused") {
		t.Fatalf("unexpected initial status: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Execute([]string{"--db-path", path, "strategy", "resume"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "--reason is required") {
		t.Fatalf("unaudited resume: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Execute([]string{"--db-path", path, "strategy", "resume", "--version", "1.4.2", "--reason", "wrong cohort"}, &stdout, &stderr); code == 0 {
		t.Fatalf("mismatched cohort resumed: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Execute([]string{"--db-path", path, "strategy", "resume", "--version", "1.5.0", "--reason", "engineering gates passed", "--operator", "test"}, &stdout, &stderr); code != 0 {
		t.Fatalf("resume failed: code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "strategy mode: live") || !strings.Contains(stdout.String(), "engineering gates passed") {
		t.Fatalf("unexpected resumed status: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Execute([]string{"--db-path", path, "strategy", "pause", "--reason", "maintenance", "--operator", "test"}, &stdout, &stderr); code != 0 {
		t.Fatalf("pause failed: code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "strategy mode: paused") {
		t.Fatalf("unexpected paused status: %s", stdout.String())
	}
}
