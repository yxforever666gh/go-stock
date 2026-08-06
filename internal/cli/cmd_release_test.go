package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cliports "go-stock/internal/cli/ports"
)

type recordingReleaseInspectionRepository struct {
	calls      int
	request    cliports.ReleaseReplayInspectionRequest
	inspection cliports.ReleaseReplayInspection
	err        error
}

func (r *recordingReleaseInspectionRepository) InspectReplayBundle(_ context.Context, request cliports.ReleaseReplayInspectionRequest) (cliports.ReleaseReplayInspection, error) {
	r.calls++
	r.request = request
	return r.inspection, r.err
}

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

	repository := &recordingReleaseInspectionRepository{}
	_, err = verifyReplayBundleWithRepository(bundleDir, manifestPath, repository)
	if err == nil || !strings.Contains(err.Error(), "main database: SHA256 mismatch") {
		t.Fatalf("expected hash mismatch before SQLite open, got %v", err)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls before file hash verification = %d", repository.calls)
	}
}

func TestVerifyReplayBundlePassesManifestIdentityToRepository(t *testing.T) {
	bundleDir := t.TempDir()
	manifest, _, err := loadReplayBundleManifest(filepath.Join("..", "..", "release", "replay_bundle_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	mainBytes := []byte("verified main snapshot")
	minuteBytes := []byte("verified minute snapshot")
	mainHash := sha256.Sum256(mainBytes)
	minuteHash := sha256.Sum256(minuteBytes)
	manifest.MainDatabase.SHA256 = fmt.Sprintf("%x", mainHash)
	manifest.MinuteDatabase.SHA256 = fmt.Sprintf("%x", minuteHash)
	manifest.MainDatabase.LegacyRuleRows = 2
	manifest.MainDatabase.LegacyMinuteBarRows = 3
	manifest.MinuteDatabase.MinuteBarRows = 5
	manifest.Replay.ExpectedRuleCount = 2
	manifest.Replay.ExpectedResultHash = strings.Repeat("a", sha256.Size*2)

	if err := os.WriteFile(filepath.Join(bundleDir, manifest.MainDatabase.FileName), mainBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, manifest.MinuteDatabase.FileName), minuteBytes, 0o600); err != nil {
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
	expectedAsOf, err := time.Parse(time.RFC3339, manifest.MinuteDatabase.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	repository := &recordingReleaseInspectionRepository{inspection: cliports.ReleaseReplayInspection{
		LegacyRuleRows:      manifest.MainDatabase.LegacyRuleRows,
		LegacyMinuteBarRows: manifest.MainDatabase.LegacyMinuteBarRows,
		MinuteBarRows:       manifest.MinuteDatabase.MinuteBarRows,
		MinuteAsOf:          expectedAsOf,
		ReplayRuleCount:     manifest.Replay.ExpectedRuleCount,
		ResultHash:          manifest.Replay.ExpectedResultHash,
		RepeatedResultHash:  manifest.Replay.ExpectedResultHash,
		Deterministic:       true,
	}}

	result, err := verifyReplayBundleWithRepository(bundleDir, manifestPath, repository)
	if err != nil {
		t.Fatalf("verify replay bundle: %v", err)
	}
	if !result.Verified || result.ResultHash != manifest.Replay.ExpectedResultHash || repository.calls != 1 {
		t.Fatalf("verification result = %+v, repository calls = %d", result, repository.calls)
	}
	wantMain, _ := filepath.Abs(filepath.Join(bundleDir, manifest.MainDatabase.FileName))
	wantMinute, _ := filepath.Abs(filepath.Join(bundleDir, manifest.MinuteDatabase.FileName))
	if repository.request.MainDatabasePath != wantMain || repository.request.MinuteDatabasePath != wantMinute {
		t.Fatalf("repository paths = %q / %q, want %q / %q", repository.request.MainDatabasePath, repository.request.MinuteDatabasePath, wantMain, wantMinute)
	}
	wantTo, err := time.ParseInLocation(time.DateOnly, manifest.Replay.RecommendationTo, expectedAsOf.Location())
	if err != nil {
		t.Fatal(err)
	}
	if !repository.request.RecommendationTo.Equal(wantTo) || repository.request.ExpectedRuleCount != manifest.Replay.ExpectedRuleCount {
		t.Fatalf("repository request = %+v, want cutoff %s count %d", repository.request, wantTo, manifest.Replay.ExpectedRuleCount)
	}
}

func TestRunVerifyZoneinfoAcceptsGOROOTZoneinfo(t *testing.T) {
	zoneinfoPath := filepath.Join(runtime.GOROOT(), "lib", "time", "zoneinfo.zip")
	expectedSHA256 := testFileSHA256(t, zoneinfoPath)
	var stdout strings.Builder

	err := runReleaseWithRepository([]string{
		"verify-zoneinfo",
		"--path", zoneinfoPath,
		"--expect-sha256", expectedSHA256,
	}, &stdout, nil)
	if err != nil {
		t.Fatalf("verify GOROOT zoneinfo: %v", err)
	}
	if !strings.Contains(stdout.String(), expectedSHA256) || !strings.Contains(stdout.String(), "Asia/Shanghai=+08:00") {
		t.Fatalf("unexpected verification output: %q", stdout.String())
	}
}

func TestVerifyZoneinfoRejectsMissingFile(t *testing.T) {
	_, err := verifyZoneinfo(filepath.Join(t.TempDir(), "missing-zoneinfo.zip"), strings.Repeat("0", sha256.Size*2))
	if err == nil || !strings.Contains(err.Error(), "read zoneinfo ZIP") {
		t.Fatalf("expected missing file error, got %v", err)
	}
}

func TestVerifyZoneinfoRejectsHashMismatch(t *testing.T) {
	path := writeTestZoneinfoZIP(t, []testZoneinfoEntry{{name: "Asia/Shanghai", data: []byte("not inspected before hash matches")}})
	_, err := verifyZoneinfo(path, strings.Repeat("0", sha256.Size*2))
	if err == nil || !strings.Contains(err.Error(), "zoneinfo ZIP SHA256 mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
}

func TestVerifyZoneinfoRejectsCorruptZIP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zoneinfo.zip")
	if err := os.WriteFile(path, []byte("not a ZIP archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := verifyZoneinfo(path, testFileSHA256(t, path))
	if err == nil || !strings.Contains(err.Error(), "open zoneinfo ZIP") {
		t.Fatalf("expected corrupt ZIP error, got %v", err)
	}
}

func TestVerifyZoneinfoRejectsMissingShanghaiEntry(t *testing.T) {
	path := writeTestZoneinfoZIP(t, []testZoneinfoEntry{{name: "Asia/Chongqing", data: []byte("not relevant")}})
	_, err := verifyZoneinfo(path, testFileSHA256(t, path))
	if err == nil || !strings.Contains(err.Error(), "exactly one Asia/Shanghai entry: found 0") {
		t.Fatalf("expected missing Asia/Shanghai error, got %v", err)
	}
}

func TestVerifyZoneinfoRejectsInvalidShanghaiEntry(t *testing.T) {
	path := writeTestZoneinfoZIP(t, []testZoneinfoEntry{{name: "Asia/Shanghai", data: []byte("not TZif data")}})
	_, err := verifyZoneinfo(path, testFileSHA256(t, path))
	if err == nil || !strings.Contains(err.Error(), "validate zoneinfo ZIP Asia/Shanghai entry") {
		t.Fatalf("expected invalid TZif error, got %v", err)
	}
}

func TestVerifyZoneinfoRequiresExactlyOneNonEmptyShanghaiEntry(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		path := writeTestZoneinfoZIP(t, []testZoneinfoEntry{
			{name: "Asia/Shanghai", data: []byte("first")},
			{name: "Asia/Shanghai", data: []byte("second")},
		})
		_, err := verifyZoneinfo(path, testFileSHA256(t, path))
		if err == nil || !strings.Contains(err.Error(), "exactly one Asia/Shanghai entry: found 2") {
			t.Fatalf("expected duplicate entry error, got %v", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		path := writeTestZoneinfoZIP(t, []testZoneinfoEntry{{name: "Asia/Shanghai"}})
		_, err := verifyZoneinfo(path, testFileSHA256(t, path))
		if err == nil || !strings.Contains(err.Error(), "Asia/Shanghai entry is empty") {
			t.Fatalf("expected empty entry error, got %v", err)
		}
	})
}

func TestRunVerifyZoneinfoRequiresFlagsAndLowercaseSHA256(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "path", args: []string{"verify-zoneinfo"}, want: "--path is required"},
		{name: "hash", args: []string{"verify-zoneinfo", "--path", "zoneinfo.zip"}, want: "--expect-sha256 is required"},
		{name: "lowercase hash", args: []string{"verify-zoneinfo", "--path", "zoneinfo.zip", "--expect-sha256", strings.Repeat("A", sha256.Size*2)}, want: "lowercase SHA256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout strings.Builder
			err := runReleaseWithRepository(tt.args, &stdout, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}

	var stdout strings.Builder
	err := runReleaseWithRepository(nil, &stdout, nil)
	if err == nil || !strings.Contains(err.Error(), "verify-zoneinfo") {
		t.Fatalf("release usage does not include verify-zoneinfo: %v", err)
	}
}

type testZoneinfoEntry struct {
	name string
	data []byte
}

func writeTestZoneinfoZIP(t *testing.T, entries []testZoneinfoEntry) string {
	t.Helper()
	var payload bytes.Buffer
	archive := zip.NewWriter(&payload)
	for _, entry := range entries {
		writer, err := archive.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "zoneinfo.zip")
	if err := os.WriteFile(path, payload.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testFileSHA256(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest)
}
