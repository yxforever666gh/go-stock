package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-stock/backend/strategy/v150"
	"go-stock/internal/bootstrap"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/releaseinfo"
)

type releaseInspectResult struct {
	Manifest   releaseinfo.ReleaseManifest `json:"manifest"`
	Build      releaseinfo.BuildInfo       `json:"build"`
	ConfigHash string                      `json:"configHash"`
}

const (
	releaseUsage               = "usage: release inspect|verify-replay-bundle|verify-zoneinfo"
	verifyZoneinfoUsage        = "usage: release verify-zoneinfo --path <zoneinfo.zip> --expect-sha256 <lowercase-sha256>"
	maxZoneinfoEntrySize int64 = 1 << 20
)

func runRelease(args []string, _ GlobalOptions, stdout io.Writer) error {
	return runReleaseWithRepository(args, stdout, bootstrap.NewProductionReleaseInspectionRepository())
}

func runReleaseWithRepository(args []string, stdout io.Writer, repository cliports.ReleaseInspectionRepository) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", releaseUsage)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "inspect":
		if len(args) != 1 {
			return fmt.Errorf("usage: release inspect")
		}
		return runReleaseInspect(stdout)
	case "verify-replay-bundle":
		return runVerifyReplayBundle(args[1:], stdout, repository)
	case "verify-zoneinfo":
		return runVerifyZoneinfo(args[1:], stdout)
	default:
		return fmt.Errorf("%s", releaseUsage)
	}
}

func runReleaseInspect(stdout io.Writer) error {
	result := releaseInspectResult{
		Manifest:   releaseinfo.Manifest(),
		Build:      releaseinfo.Build(),
		ConfigHash: v150.FixedStrategyV150ConfigHash(),
	}
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(payload))
	return err
}

type zoneinfoVerification struct {
	Path          string
	SHA256        string
	OffsetSeconds int
}

func runVerifyZoneinfo(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("release verify-zoneinfo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", "", "path to the exact zoneinfo ZIP artifact")
	expectedSHA256 := fs.String("expect-sha256", "", "expected lowercase SHA256 of the exact ZIP")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("verify-zoneinfo does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("%s: --path is required", verifyZoneinfoUsage)
	}
	if *expectedSHA256 == "" {
		return fmt.Errorf("%s: --expect-sha256 is required", verifyZoneinfoUsage)
	}

	result, err := verifyZoneinfo(*path, *expectedSHA256)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Zoneinfo ZIP verified: path=%s sha256=%s Asia/Shanghai=+08:00\n", result.Path, result.SHA256)
	return err
}

func verifyZoneinfo(path, expectedSHA256 string) (zoneinfoVerification, error) {
	var result zoneinfoVerification
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return result, fmt.Errorf("zoneinfo ZIP path is required")
	}
	if err := validateLowercaseSHA256(expectedSHA256); err != nil {
		return result, fmt.Errorf("expected zoneinfo ZIP SHA256: %w", err)
	}

	absolutePath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return result, fmt.Errorf("resolve zoneinfo ZIP path: %w", err)
	}
	zipData, err := os.ReadFile(absolutePath)
	if err != nil {
		return result, fmt.Errorf("read zoneinfo ZIP %q: %w", absolutePath, err)
	}
	digest := sha256.Sum256(zipData)
	actualSHA256 := hex.EncodeToString(digest[:])
	if actualSHA256 != expectedSHA256 {
		return result, fmt.Errorf("zoneinfo ZIP SHA256 mismatch: got %s want %s", actualSHA256, expectedSHA256)
	}

	archive, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return result, fmt.Errorf("open zoneinfo ZIP %q: %w", absolutePath, err)
	}
	var shanghaiEntries []*zip.File
	for _, entry := range archive.File {
		if entry.Name == "Asia/Shanghai" {
			shanghaiEntries = append(shanghaiEntries, entry)
		}
	}
	if len(shanghaiEntries) != 1 {
		return result, fmt.Errorf("zoneinfo ZIP must contain exactly one Asia/Shanghai entry: found %d", len(shanghaiEntries))
	}

	entry := shanghaiEntries[0]
	if entry.UncompressedSize64 == 0 {
		return result, fmt.Errorf("zoneinfo ZIP Asia/Shanghai entry is empty")
	}
	if entry.UncompressedSize64 > uint64(maxZoneinfoEntrySize) {
		return result, fmt.Errorf("zoneinfo ZIP Asia/Shanghai entry is too large: %d bytes", entry.UncompressedSize64)
	}
	entryReader, err := entry.Open()
	if err != nil {
		return result, fmt.Errorf("open zoneinfo ZIP Asia/Shanghai entry: %w", err)
	}
	zoneData, readErr := io.ReadAll(io.LimitReader(entryReader, maxZoneinfoEntrySize+1))
	closeErr := entryReader.Close()
	if readErr != nil {
		return result, fmt.Errorf("read zoneinfo ZIP Asia/Shanghai entry: %w", readErr)
	}
	if closeErr != nil {
		return result, fmt.Errorf("close zoneinfo ZIP Asia/Shanghai entry: %w", closeErr)
	}
	if len(zoneData) == 0 {
		return result, fmt.Errorf("zoneinfo ZIP Asia/Shanghai entry is empty")
	}
	if int64(len(zoneData)) > maxZoneinfoEntrySize {
		return result, fmt.Errorf("zoneinfo ZIP Asia/Shanghai entry exceeds %d bytes", maxZoneinfoEntrySize)
	}

	location, err := time.LoadLocationFromTZData("Asia/Shanghai", zoneData)
	if err != nil {
		return result, fmt.Errorf("validate zoneinfo ZIP Asia/Shanghai entry: %w", err)
	}
	_, offsetSeconds := time.Date(2024, time.January, 1, 12, 0, 0, 0, location).Zone()
	if offsetSeconds != 8*60*60 {
		return result, fmt.Errorf("zoneinfo ZIP Asia/Shanghai offset mismatch: got %d seconds want %d", offsetSeconds, 8*60*60)
	}

	return zoneinfoVerification{
		Path:          absolutePath,
		SHA256:        actualSHA256,
		OffsetSeconds: offsetSeconds,
	}, nil
}

func validateLowercaseSHA256(value string) error {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) || value != strings.TrimSpace(value) {
		return fmt.Errorf("must be a 64-character lowercase SHA256")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("must be a 64-character lowercase SHA256: %w", err)
	}
	return nil
}

type replayBundleManifest struct {
	BundleVersion int `json:"bundleVersion"`
	MainDatabase  struct {
		FileName            string `json:"fileName"`
		SHA256              string `json:"sha256"`
		LegacyRuleRows      int64  `json:"legacyRuleRows"`
		LegacyMinuteBarRows int64  `json:"legacyMinuteBarRows"`
	} `json:"mainDatabase"`
	MinuteDatabase struct {
		FileName      string `json:"fileName"`
		SHA256        string `json:"sha256"`
		MinuteBarRows int64  `json:"minuteBarRows"`
		AsOf          string `json:"asOf"`
	} `json:"minuteDatabase"`
	Replay struct {
		RecommendationTo   string `json:"recommendationTo"`
		ExpectedRuleCount  int    `json:"expectedRuleCount"`
		ExpectedResultHash string `json:"expectedResultHash"`
	} `json:"replay"`
}

type replayBundleVerification struct {
	BundlePath          string `json:"bundlePath"`
	ManifestPath        string `json:"manifestPath"`
	MainSHA256          string `json:"mainSHA256"`
	MinuteSHA256        string `json:"minuteSHA256"`
	LegacyRuleRows      int64  `json:"legacyRuleRows"`
	LegacyMinuteBarRows int64  `json:"legacyMinuteBarRows"`
	MinuteBarRows       int64  `json:"minuteBarRows"`
	AsOf                string `json:"asOf"`
	ResultHash          string `json:"resultHash"`
	Verified            bool   `json:"verified"`
}

func runVerifyReplayBundle(args []string, stdout io.Writer, repository cliports.ReleaseInspectionRepository) error {
	fs := flag.NewFlagSet("release verify-replay-bundle", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bundlePath := fs.String("bundle", filepath.Join("runtime", "backups", "pre-refactor-20260806-001309"), "directory containing the frozen stock.db and minute.db")
	manifestPath := fs.String("manifest", filepath.Join("release", "replay_bundle_manifest.json"), "frozen replay bundle manifest")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("verify-replay-bundle does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	result, err := verifyReplayBundleWithRepository(*bundlePath, *manifestPath, repository)
	if err != nil {
		return err
	}
	if *jsonOut {
		payload, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(payload))
		return err
	}
	_, err = fmt.Fprintf(stdout, "Frozen replay bundle verified: rules=%d asOf=%s hash=%s\n", result.LegacyRuleRows, result.AsOf, result.ResultHash)
	return err
}

func verifyReplayBundle(bundlePath, manifestPath string) (replayBundleVerification, error) {
	return verifyReplayBundleWithRepository(bundlePath, manifestPath, bootstrap.NewProductionReleaseInspectionRepository())
}

func verifyReplayBundleWithRepository(bundlePath, manifestPath string, repository cliports.ReleaseInspectionRepository) (replayBundleVerification, error) {
	var result replayBundleVerification
	if repository == nil {
		return result, fmt.Errorf("release inspection repository is required")
	}
	manifest, absoluteManifest, err := loadReplayBundleManifest(manifestPath)
	if err != nil {
		return result, err
	}
	absoluteBundle, err := filepath.Abs(strings.TrimSpace(bundlePath))
	if err != nil {
		return result, fmt.Errorf("resolve replay bundle: %w", err)
	}
	mainPath := filepath.Join(absoluteBundle, manifest.MainDatabase.FileName)
	minutePath := filepath.Join(absoluteBundle, manifest.MinuteDatabase.FileName)

	// File identity is deliberately verified before SQLite opens either file.
	mainHash, err := verifyReplayBundleFile(mainPath, manifest.MainDatabase.SHA256)
	if err != nil {
		return result, fmt.Errorf("main database: %w", err)
	}
	minuteHash, err := verifyReplayBundleFile(minutePath, manifest.MinuteDatabase.SHA256)
	if err != nil {
		return result, fmt.Errorf("minute database: %w", err)
	}

	expectedAsOf, err := time.Parse(time.RFC3339, manifest.MinuteDatabase.AsOf)
	if err != nil {
		return result, fmt.Errorf("parse minute database asOf: %w", err)
	}
	to, err := time.ParseInLocation(time.DateOnly, manifest.Replay.RecommendationTo, expectedAsOf.Location())
	if err != nil {
		return result, fmt.Errorf("parse replay recommendationTo: %w", err)
	}
	inspection, err := repository.InspectReplayBundle(context.Background(), cliports.ReleaseReplayInspectionRequest{
		MainDatabasePath:   mainPath,
		MinuteDatabasePath: minutePath,
		RecommendationTo:   to,
		ExpectedRuleCount:  manifest.Replay.ExpectedRuleCount,
	})
	if err != nil {
		return result, err
	}
	if inspection.LegacyRuleRows != manifest.MainDatabase.LegacyRuleRows {
		return result, fmt.Errorf("legacy rule row count mismatch: got %d want %d", inspection.LegacyRuleRows, manifest.MainDatabase.LegacyRuleRows)
	}
	if inspection.LegacyMinuteBarRows != manifest.MainDatabase.LegacyMinuteBarRows {
		return result, fmt.Errorf("legacy minute row count mismatch: got %d want %d", inspection.LegacyMinuteBarRows, manifest.MainDatabase.LegacyMinuteBarRows)
	}
	if inspection.MinuteBarRows != manifest.MinuteDatabase.MinuteBarRows {
		return result, fmt.Errorf("minute row count mismatch: got %d want %d", inspection.MinuteBarRows, manifest.MinuteDatabase.MinuteBarRows)
	}
	if !inspection.MinuteAsOf.Equal(expectedAsOf) {
		return result, fmt.Errorf("minute asOf mismatch: got %s want %s", inspection.MinuteAsOf.Format(time.RFC3339), expectedAsOf.Format(time.RFC3339))
	}
	if !inspection.Deterministic || inspection.DeterminismFailures != 0 || inspection.ResultHash != manifest.Replay.ExpectedResultHash || inspection.RepeatedResultHash != manifest.Replay.ExpectedResultHash {
		return result, fmt.Errorf("frozen replay mismatch: count=%d deterministic=%t hash=%s repeat=%s", inspection.ReplayRuleCount, inspection.Deterministic, inspection.ResultHash, inspection.RepeatedResultHash)
	}
	result = replayBundleVerification{
		BundlePath: absoluteBundle, ManifestPath: absoluteManifest,
		MainSHA256: mainHash, MinuteSHA256: minuteHash,
		LegacyRuleRows: inspection.LegacyRuleRows, LegacyMinuteBarRows: inspection.LegacyMinuteBarRows,
		MinuteBarRows: inspection.MinuteBarRows, AsOf: expectedAsOf.Format(time.RFC3339),
		ResultHash: inspection.ResultHash, Verified: true,
	}
	return result, nil
}

func loadReplayBundleManifest(path string) (replayBundleManifest, string, error) {
	var manifest replayBundleManifest
	absolutePath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return manifest, "", err
	}
	payload, err := os.ReadFile(absolutePath)
	if err != nil {
		return manifest, absolutePath, fmt.Errorf("read replay bundle manifest: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, absolutePath, fmt.Errorf("decode replay bundle manifest: %w", err)
	}
	if err := validateReplayBundleManifest(manifest); err != nil {
		return manifest, absolutePath, err
	}
	return manifest, absolutePath, nil
}

func validateReplayBundleManifest(manifest replayBundleManifest) error {
	if manifest.BundleVersion != 1 {
		return fmt.Errorf("unsupported replay bundle manifest version %d", manifest.BundleVersion)
	}
	for name, value := range map[string]string{"mainDatabase.fileName": manifest.MainDatabase.FileName, "minuteDatabase.fileName": manifest.MinuteDatabase.FileName} {
		if strings.TrimSpace(value) == "" || filepath.Base(value) != value || value == "." || value == ".." {
			return fmt.Errorf("%s must be a file name without a directory", name)
		}
	}
	for name, value := range map[string]string{"mainDatabase.sha256": manifest.MainDatabase.SHA256, "minuteDatabase.sha256": manifest.MinuteDatabase.SHA256, "replay.expectedResultHash": manifest.Replay.ExpectedResultHash} {
		if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
			return fmt.Errorf("%s must be a lowercase SHA256", name)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return fmt.Errorf("%s must be a lowercase SHA256: %w", name, err)
		}
	}
	if manifest.MainDatabase.LegacyRuleRows <= 0 || manifest.MainDatabase.LegacyMinuteBarRows <= 0 || manifest.MinuteDatabase.MinuteBarRows <= 0 || manifest.Replay.ExpectedRuleCount <= 0 {
		return fmt.Errorf("replay bundle row counts must be positive")
	}
	if int64(manifest.Replay.ExpectedRuleCount) != manifest.MainDatabase.LegacyRuleRows {
		return fmt.Errorf("expectedRuleCount must equal mainDatabase.legacyRuleRows")
	}
	if _, err := time.Parse(time.DateOnly, manifest.Replay.RecommendationTo); err != nil {
		return fmt.Errorf("replay.recommendationTo must use YYYY-MM-DD: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, manifest.MinuteDatabase.AsOf); err != nil {
		return fmt.Errorf("minuteDatabase.asOf must use RFC3339: %w", err)
	}
	return nil
}

func verifyReplayBundleFile(path, expectedHash string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	actual := fmt.Sprintf("%x", h.Sum(nil))
	if actual != expectedHash {
		return actual, fmt.Errorf("SHA256 mismatch: got %s want %s", actual, expectedHash)
	}
	return actual, nil
}
