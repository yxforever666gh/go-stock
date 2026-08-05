package data

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLoadV150ImmutableRunHealthWarningsUsesFrozenProductionPayload(t *testing.T) {
	original := db.Dao
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "v150-health.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Dao = original
		_ = sqlDB.Close()
	})
	db.Dao = database
	if err := database.AutoMigrate(&models.StrategyRunSnapshot{}); err != nil {
		t.Fatal(err)
	}

	loc := cnLocation()
	seed := func(runID, tradeDate, slot, mode string, decisionAt time.Time, warnings []string) {
		payload, marshalErr := json.Marshal(map[string]any{"run": map[string]any{"warnings": warnings}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		frozenAt := decisionAt
		row := models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: marketSummaryVersion150, TradeDate: tradeDate, RunSlot: slot,
			StartedAt: decisionAt.Add(-time.Minute), AsOf: decisionAt.Add(-time.Minute), DataCutoffAt: decisionAt.Add(-time.Second),
			DecisionAt: decisionAt, GeneratedAt: decisionAt, Mode: mode, ConfigHash: "cfg", InputHash: "input-" + runID,
			SnapshotHash: "snapshot-" + runID, PayloadJSON: string(payload), FrozenAt: &frozenAt,
		}
		if createErr := database.Create(&row).Error; createErr != nil {
			t.Fatal(createErr)
		}
	}
	seed("older", "2026-08-04", "midday", "production", time.Date(2026, 8, 4, 12, 0, 0, 0, loc), []string{"news_status:failed"})
	seed("newer", "2026-08-05", "morning_open", "production", time.Date(2026, 8, 5, 10, 0, 0, 0, loc), []string{"benchmark:stale", "news_status:failed", "000001.SZ:current_quote_missing_or_stale"})
	seed("observation", "2026-08-05", "execution_security_observation", "execution_security_observation", time.Date(2026, 8, 5, 10, 1, 0, 0, loc), []string{"must_not_surface"})

	warnings, err := loadV150ImmutableRunHealthWarnings(30)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"[2026-08-05 morning_open] benchmark:stale", "news_status:failed", "current_quote_missing_or_stale"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing immutable warning %q in %q", want, joined)
		}
	}
	if strings.Count(joined, "news_status:failed") != 1 || strings.Contains(joined, "must_not_surface") {
		t.Fatalf("warnings were not deduplicated/production-scoped: %q", joined)
	}
}
