package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayBundleManifestPinsReleaseAuditIdentity(t *testing.T) {
	manifest, _, err := loadReplayBundleManifest(filepath.Join("..", "..", "release", "replay_bundle_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.MainDatabase.FileName != "stock.db" || manifest.MinuteDatabase.FileName != "minute.db" {
		t.Fatalf("unexpected bundle file names: %+v", manifest)
	}
	if manifest.MainDatabase.LegacyRuleRows != 226 || manifest.Replay.ExpectedRuleCount != 226 {
		t.Fatalf("unexpected rule corpus identity: %+v", manifest)
	}
	if manifest.MinuteDatabase.MinuteBarRows != 1772597 || manifest.MinuteDatabase.AsOf != "2026-08-05T14:37:00+08:00" {
		t.Fatalf("unexpected minute bundle identity: %+v", manifest)
	}
	if manifest.Replay.ExpectedResultHash != "2de5273b56153368183d8fae919f3397ae7a4d635e99167a8ea978b1c339ba1d" {
		t.Fatalf("unexpected frozen replay hash: %s", manifest.Replay.ExpectedResultHash)
	}
}

func TestValidateReplayBundleManifestRejectsDatabasePathTraversal(t *testing.T) {
	manifest, _, err := loadReplayBundleManifest(filepath.Join("..", "..", "release", "replay_bundle_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.MinuteDatabase.FileName = filepath.Join("..", "minute.db")
	if err := validateReplayBundleManifest(manifest); err == nil || !strings.Contains(err.Error(), "without a directory") {
		t.Fatalf("expected path traversal validation error, got %v", err)
	}
}

func TestVerifyReplayBundleChecksFileHashBeforeOpeningSQLite(t *testing.T) {
	bundleDir := t.TempDir()
	manifest, _, err := loadReplayBundleManifest(filepath.Join("..", "..", "release", "replay_bundle_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, manifest.MainDatabase.FileName), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, manifest.MinuteDatabase.FileName), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = verifyReplayBundle(bundleDir, manifestPath)
	if err == nil || !strings.Contains(err.Error(), "main database: SHA256 mismatch") {
		t.Fatalf("expected hash mismatch before SQLite open, got %v", err)
	}
}
