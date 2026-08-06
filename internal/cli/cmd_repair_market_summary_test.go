package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairMarketSummaryIsPermanentlyDisabled(t *testing.T) {
	err := runRepairMarketSummary(nil, GlobalOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errHistoricalMarketSummaryRepairDisabled) {
		t.Fatalf("repair error = %v, want historical read-only gate", err)
	}
}

func TestRepairMarketSummaryRejectsBeforeDatabaseBootstrap(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "missing", "stock.db")
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--db-path", databasePath, "repair-market-summary"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), errHistoricalMarketSummaryRepairDisabled.Error()) {
		t.Fatalf("stderr = %q, want disabled error", stderr.String())
	}
	if _, err := os.Stat(databasePath); !os.IsNotExist(err) {
		t.Fatalf("database was touched before repair rejection: %v", err)
	}
}
