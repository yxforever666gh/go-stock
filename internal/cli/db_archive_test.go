package cli

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cliports "go-stock/internal/cli/ports"
)

type archiveTestAdmin struct{}

func (*archiveTestAdmin) Status(context.Context) (cliports.DatabaseStatus, cliports.DatabaseStatus, error) {
	return cliports.DatabaseStatus{}, cliports.DatabaseStatus{}, nil
}
func (*archiveTestAdmin) Migrate(context.Context) error { return nil }
func (*archiveTestAdmin) Verify(context.Context) (cliports.DatabaseStatus, cliports.DatabaseStatus, error) {
	return cliports.DatabaseStatus{}, cliports.DatabaseStatus{}, nil
}
func (*archiveTestAdmin) Backup(_ context.Context, mainPath, minutePath string) error {
	if err := os.WriteFile(mainPath, []byte("main snapshot"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(minutePath, []byte("minute snapshot"), 0o600)
}
func (*archiveTestAdmin) Compact(context.Context, string) error { return nil }
func (*archiveTestAdmin) LegacyStrategyRowCounts(context.Context) (map[string]int64, error) {
	names := []string{
		"ai_recommend_daily_bar", "ai_recommend_minute_bar", "ai_recommend_opening_review", "ai_recommend_stocks",
		"ai_recommend_yield_dirty_code", "ai_recommend_yield_meta", "ai_recommend_yield_override",
		"ai_recommend_yield_record_state", "ai_recommend_yield_state", "strategy_backtest_metric",
		"strategy_backtest_run", "strategy_backtest_trade", "strategy_candidate_snapshot", "strategy_order_event",
		"strategy_rule_snapshot", "strategy_run_snapshot", "strategy_runtime_control",
	}
	counts := make(map[string]int64, len(names))
	for _, name := range names {
		counts[name] = 0
	}
	counts["ai_recommend_stocks"] = 540
	return counts, nil
}
func (*archiveTestAdmin) QuickCheck(context.Context) error { return nil }
func (*archiveTestAdmin) Close() error                     { return nil }

func TestCreateDatabaseArchiveWritesVerifiedManifestAndSnapshots(t *testing.T) {
	output := filepath.Join(t.TempDir(), "archive.zip")
	result, err := createDatabaseArchive(t.Context(), &archiveTestAdmin{}, databaseArchiveOptions{
		Output: output, SourceAppVersion: "1.6.5", SourceCommit: "commit-1", MainSchemaVersion: 8, MinuteSchemaVersion: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != output || result.SHA256 == "" || result.SizeBytes == 0 {
		t.Fatalf("archive result = %+v", result)
	}
	reader, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var manifest databaseArchiveManifest
	for _, entry := range reader.File {
		if entry.Name != "manifest.json" {
			continue
		}
		payload, err := readZipEntry(entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(payload, &manifest); err != nil {
			t.Fatal(err)
		}
	}
	if manifest.FormatVersion != 1 || manifest.SourceAppVersion != "1.6.5" || manifest.MainSchemaVersion != 8 || manifest.MinuteSchemaVersion != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.LegacyTableRows["ai_recommend_stocks"] != 540 || len(manifest.Files) != 2 {
		t.Fatalf("manifest inventory = %+v", manifest)
	}
}
