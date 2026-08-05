package cli

import (
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

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
	"go-stock/internal/releaseinfo"

	"gorm.io/gorm"
)

type releaseInspectResult struct {
	Manifest   releaseinfo.ReleaseManifest `json:"manifest"`
	Build      releaseinfo.BuildInfo       `json:"build"`
	ConfigHash string                      `json:"configHash"`
}

func runRelease(args []string, _ GlobalOptions, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: release inspect|verify-replay-bundle")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "inspect":
		if len(args) != 1 {
			return fmt.Errorf("usage: release inspect")
		}
		return runReleaseInspect(stdout)
	case "verify-replay-bundle":
		return runVerifyReplayBundle(args[1:], stdout)
	default:
		return fmt.Errorf("usage: release inspect|verify-replay-bundle")
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

func runVerifyReplayBundle(args []string, stdout io.Writer) error {
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
	result, err := verifyReplayBundle(*bundlePath, *manifestPath)
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
	var result replayBundleVerification
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

	oldMain, oldMinute := db.Dao, db.MinuteDao
	oldMinuteEnv, hadMinuteEnv := os.LookupEnv("GO_STOCK_MINUTE_DB_PATH")
	if err := os.Setenv("GO_STOCK_MINUTE_DB_PATH", minutePath); err != nil {
		return result, err
	}
	db.Dao, db.MinuteDao = nil, nil
	defer func() {
		_ = db.Close()
		db.Dao, db.MinuteDao = oldMain, oldMinute
		if hadMinuteEnv {
			_ = os.Setenv("GO_STOCK_MINUTE_DB_PATH", oldMinuteEnv)
		} else {
			_ = os.Unsetenv("GO_STOCK_MINUTE_DB_PATH")
		}
	}()
	if _, err := db.InitReadOnly(mainPath); err != nil {
		return result, fmt.Errorf("open verified replay bundle: %w", err)
	}
	if db.MinuteDao == nil {
		return result, fmt.Errorf("open verified replay bundle: minute database is unavailable")
	}

	expectedAsOf, err := time.Parse(time.RFC3339, manifest.MinuteDatabase.AsOf)
	if err != nil {
		return result, fmt.Errorf("parse minute database asOf: %w", err)
	}
	to, err := time.ParseInLocation(time.DateOnly, manifest.Replay.RecommendationTo, expectedAsOf.Location())
	if err != nil {
		return result, fmt.Errorf("parse replay recommendationTo: %w", err)
	}
	legacyRuleRows, err := countReplayBundleRules(db.Dao, to)
	if err != nil {
		return result, err
	}
	legacyMinuteRows, err := countTableRows(db.Dao, "ai_recommend_minute_bar")
	if err != nil {
		return result, err
	}
	minuteRows, minuteMaxMillis, err := minuteBundleStats(db.MinuteDao)
	if err != nil {
		return result, err
	}
	actualAsOf := time.UnixMilli(minuteMaxMillis)
	if legacyRuleRows != manifest.MainDatabase.LegacyRuleRows {
		return result, fmt.Errorf("legacy rule row count mismatch: got %d want %d", legacyRuleRows, manifest.MainDatabase.LegacyRuleRows)
	}
	if legacyMinuteRows != manifest.MainDatabase.LegacyMinuteBarRows {
		return result, fmt.Errorf("legacy minute row count mismatch: got %d want %d", legacyMinuteRows, manifest.MainDatabase.LegacyMinuteBarRows)
	}
	if minuteRows != manifest.MinuteDatabase.MinuteBarRows {
		return result, fmt.Errorf("minute row count mismatch: got %d want %d", minuteRows, manifest.MinuteDatabase.MinuteBarRows)
	}
	if !actualAsOf.Equal(expectedAsOf) {
		return result, fmt.Errorf("minute asOf mismatch: got %s want %s", actualAsOf.Format(time.RFC3339), expectedAsOf.Format(time.RFC3339))
	}
	if err := verifyReplayQuickCheck(db.Dao, "main"); err != nil {
		return result, err
	}
	if err := verifyReplayQuickCheck(db.MinuteDao, "minute"); err != nil {
		return result, err
	}

	report, err := data.ReplayLegacyStructuredRulesCacheOnly(context.Background(), db.Dao, data.LegacyStructuredRuleReplayOptions{
		To: to, ExpectedRuleCount: manifest.Replay.ExpectedRuleCount,
	})
	if err != nil {
		return result, err
	}
	if !report.Deterministic || report.DeterminismViolations != 0 || report.ResultHash != manifest.Replay.ExpectedResultHash || report.RepeatedResultHash != manifest.Replay.ExpectedResultHash {
		return result, fmt.Errorf("frozen replay mismatch: count=%d deterministic=%t hash=%s repeat=%s", report.TotalRules, report.Deterministic, report.ResultHash, report.RepeatedResultHash)
	}
	result = replayBundleVerification{
		BundlePath: absoluteBundle, ManifestPath: absoluteManifest,
		MainSHA256: mainHash, MinuteSHA256: minuteHash,
		LegacyRuleRows: legacyRuleRows, LegacyMinuteBarRows: legacyMinuteRows,
		MinuteBarRows: minuteRows, AsOf: expectedAsOf.Format(time.RFC3339),
		ResultHash: report.ResultHash, Verified: true,
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

func countReplayBundleRules(database *gorm.DB, to time.Time) (int64, error) {
	var count int64
	err := database.Model(&models.AiRecommendStocks{}).
		Where("TRIM(COALESCE(activation_rule_json, '')) <> ''").
		Where("TRIM(COALESCE(summary_version, '')) <> ?", v150.StrategyVersion).
		Where("COALESCE(data_time, created_at) < ?", to.AddDate(0, 0, 1)).
		Count(&count).Error
	return count, err
}

func countTableRows(database *gorm.DB, table string) (int64, error) {
	var count int64
	err := database.Table(table).Count(&count).Error
	return count, err
}

func minuteBundleStats(database *gorm.DB) (int64, int64, error) {
	var stats struct {
		Rows      int64
		MaxMillis int64
	}
	err := database.Raw("SELECT COUNT(*) AS rows, COALESCE(MAX(trade_time), 0) AS max_millis FROM minute_bar").Scan(&stats).Error
	return stats.Rows, stats.MaxMillis, err
}

func verifyReplayQuickCheck(database *gorm.DB, name string) error {
	var result string
	if err := database.Raw("PRAGMA quick_check").Scan(&result).Error; err != nil {
		return fmt.Errorf("%s quick_check: %w", name, err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("%s quick_check returned %q", name, result)
	}
	return nil
}
