package migrations

import (
	"strings"
	"testing"
	"time"
)

func TestSchema24AddsAttemptNumberAndReplacesTradingDateUniqueIndex(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_analysis_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    trading_date TEXT NOT NULL,
    scheduled_for DATETIME NOT NULL,
    started_at DATETIME NOT NULL,
    evidence_cutoff_at DATETIME,
    generated_at DATETIME,
    status TEXT NOT NULL,
    report_markdown TEXT,
    recommendation_count INTEGER,
    on_time BOOLEAN,
    created_at DATETIME,
    updated_at DATETIME
);
CREATE UNIQUE INDEX idx_research2_analysis_runs_run_id ON research2_analysis_runs(run_id);
CREATE UNIQUE INDEX idx_research2_analysis_runs_trading_date ON research2_analysis_runs(trading_date);`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	if err := database.Exec(`INSERT INTO research2_analysis_runs
(run_id,trading_date,scheduled_for,started_at,evidence_cutoff_at,generated_at,status,report_markdown,recommendation_count,on_time,created_at,updated_at)
VALUES ('historical-attempt','2026-09-03',?,?,?,?, 'failed','unchanged report',0,0,?,?)`, now.Add(-10*time.Minute), now.Add(-9*time.Minute), now.Add(-9*time.Minute), now, now, now).Error; err != nil {
		t.Fatal(err)
	}

	if err := applyResearch2AnalysisAttemptSchema(database); err != nil {
		t.Fatalf("apply schema 24: %v", err)
	}
	if err := applyResearch2AnalysisAttemptSchema(database); err != nil {
		t.Fatalf("repeat schema 24: %v", err)
	}
	var historical struct {
		AttemptNo      int
		ReportMarkdown string
		Status         string
		UpdatedAt      time.Time
	}
	if err := database.Table(research2AnalysisRunsTable).Where("run_id = ?", "historical-attempt").Take(&historical).Error; err != nil {
		t.Fatal(err)
	}
	if historical.AttemptNo != 1 || historical.ReportMarkdown != "unchanged report" || historical.Status != "failed" || !historical.UpdatedAt.Equal(now) {
		t.Fatalf("schema 24 changed historical run: %+v", historical)
	}
	insert := `INSERT INTO research2_analysis_runs
(run_id,trading_date,attempt_no,scheduled_for,started_at,status,report_markdown,recommendation_count,on_time,created_at,updated_at)
VALUES (?,?,?,?,?,'running','',0,0,?,?)`
	if err := database.Exec(insert, "second-attempt", "2026-09-03", 2, now, now, now, now).Error; err != nil {
		t.Fatalf("same-day attempt 2 should be accepted: %v", err)
	}
	if err := database.Exec(insert, "duplicate-attempt", "2026-09-03", 1, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate same-day attempt number was accepted")
	}
	if err := database.Exec(insert, "historical-attempt", "2026-09-04", 1, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate run_id was accepted after schema 24")
	}
	if got := quickCheck(database); got != "ok" {
		t.Fatalf("quick_check = %q", got)
	}
}

func TestSchema24DefinitionAndVerifier(t *testing.T) {
	definition := mainMigrationV24Definition()
	for _, fragment := range []string{"attempt_no INTEGER NOT NULL DEFAULT 1", "trading_date, attempt_no", "run_id uniqueness", "without rewriting any other field"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("schema 24 definition is missing %q: %s", fragment, definition)
		}
	}
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_analysis_runs (id INTEGER PRIMARY KEY, attempt_no INTEGER NOT NULL DEFAULT 1);`).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema24Runtime(database); err == nil {
		t.Fatal("schema 24 verifier accepted missing composite uniqueness")
	}
}
