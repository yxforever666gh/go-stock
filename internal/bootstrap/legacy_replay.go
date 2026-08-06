package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
	cliports "go-stock/internal/cli/ports"

	"gorm.io/gorm"
)

// LegacyReplayOptions and LegacyReplayReport keep the read-only CLI contract
// independent from the deprecated data package. The database handle remains a
// bootstrap concern while the legacy replay is being strangled out.
type LegacyReplayOptions = data.LegacyStructuredRuleReplayOptions
type LegacyReplayReport = data.LegacyStructuredRuleReplayReport

func ReplayLegacyStructuredRulesCacheOnly(ctx context.Context, options LegacyReplayOptions) (LegacyReplayReport, error) {
	return data.ReplayLegacyStructuredRulesCacheOnly(ctx, db.Dao, options)
}

type releaseInspectionRepository struct{}

var (
	_                   cliports.ReleaseInspectionRepository = (*releaseInspectionRepository)(nil)
	releaseInspectionMu sync.Mutex
)

func NewProductionReleaseInspectionRepository() cliports.ReleaseInspectionRepository {
	return &releaseInspectionRepository{}
}

func (*releaseInspectionRepository) InspectReplayBundle(ctx context.Context, request cliports.ReleaseReplayInspectionRequest) (cliports.ReleaseReplayInspection, error) {
	var inspection cliports.ReleaseReplayInspection
	if strings.TrimSpace(request.MainDatabasePath) == "" || strings.TrimSpace(request.MinuteDatabasePath) == "" {
		return inspection, errors.New("release replay database paths are required")
	}
	if request.RecommendationTo.IsZero() {
		return inspection, errors.New("release replay recommendation cutoff is required")
	}
	if request.ExpectedRuleCount <= 0 {
		return inspection, errors.New("release replay expected rule count must be positive")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// db.InitReadOnly currently owns the legacy minute-cache wiring through
	// process globals. Serialize and restore that compatibility state entirely
	// inside bootstrap so CLI commands never observe a database handle.
	releaseInspectionMu.Lock()
	defer releaseInspectionMu.Unlock()
	oldMain, oldMinute := db.Dao, db.MinuteDao
	oldMinuteEnv, hadMinuteEnv := os.LookupEnv("GO_STOCK_MINUTE_DB_PATH")
	if err := os.Setenv("GO_STOCK_MINUTE_DB_PATH", request.MinuteDatabasePath); err != nil {
		return inspection, err
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

	if _, err := db.InitReadOnly(request.MainDatabasePath); err != nil {
		return inspection, fmt.Errorf("open verified replay bundle: %w", err)
	}
	if db.Dao == nil || db.MinuteDao == nil {
		return inspection, errors.New("open verified replay bundle: main and minute databases are required")
	}

	legacyRuleRows, err := countReleaseReplayRules(db.Dao, request.RecommendationTo)
	if err != nil {
		return inspection, err
	}
	legacyMinuteRows, err := countReleaseReplayTableRows(db.Dao, "ai_recommend_minute_bar")
	if err != nil {
		return inspection, err
	}
	minuteRows, minuteMaxMillis, err := releaseReplayMinuteStats(db.MinuteDao)
	if err != nil {
		return inspection, err
	}
	if err := verifyReleaseReplayQuickCheck(db.Dao, "main"); err != nil {
		return inspection, err
	}
	if err := verifyReleaseReplayQuickCheck(db.MinuteDao, "minute"); err != nil {
		return inspection, err
	}

	report, err := data.ReplayLegacyStructuredRulesCacheOnly(ctx, db.Dao, data.LegacyStructuredRuleReplayOptions{
		To:                request.RecommendationTo,
		ExpectedRuleCount: request.ExpectedRuleCount,
	})
	if err != nil {
		return inspection, err
	}
	return cliports.ReleaseReplayInspection{
		LegacyRuleRows:      legacyRuleRows,
		LegacyMinuteBarRows: legacyMinuteRows,
		MinuteBarRows:       minuteRows,
		MinuteAsOf:          time.UnixMilli(minuteMaxMillis),
		ReplayRuleCount:     report.TotalRules,
		ResultHash:          report.ResultHash,
		RepeatedResultHash:  report.RepeatedResultHash,
		Deterministic:       report.Deterministic,
		DeterminismFailures: report.DeterminismViolations,
	}, nil
}

func countReleaseReplayRules(database *gorm.DB, to time.Time) (int64, error) {
	var count int64
	err := database.Model(&models.AiRecommendStocks{}).
		Where("TRIM(COALESCE(activation_rule_json, '')) <> ''").
		Where("TRIM(COALESCE(summary_version, '')) <> ?", v150.StrategyVersion).
		Where("COALESCE(data_time, created_at) < ?", to.AddDate(0, 0, 1)).
		Count(&count).Error
	return count, err
}

func countReleaseReplayTableRows(database *gorm.DB, table string) (int64, error) {
	var count int64
	err := database.Table(table).Count(&count).Error
	return count, err
}

func releaseReplayMinuteStats(database *gorm.DB) (int64, int64, error) {
	var stats struct {
		Rows      int64
		MaxMillis int64
	}
	err := database.Raw("SELECT COUNT(*) AS rows, COALESCE(MAX(trade_time), 0) AS max_millis FROM minute_bar").Scan(&stats).Error
	return stats.Rows, stats.MaxMillis, err
}

func verifyReleaseReplayQuickCheck(database *gorm.DB, name string) error {
	var result string
	if err := database.Raw("PRAGMA quick_check").Scan(&result).Error; err != nil {
		return fmt.Errorf("%s quick_check: %w", name, err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("%s quick_check returned %q", name, result)
	}
	return nil
}
