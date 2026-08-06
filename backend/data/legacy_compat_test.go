package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/legacy"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

func TestCompatibilityLegacyRepositoryUsesCanonicalSQLScope(t *testing.T) {
	database := compatibilityTestDB(t, "canonical-legacy-scope")
	if err := database.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}

	accepted := []string{
		"",
		"phase1-v1",
		"phase2-v1",
		"phase3-v2",
		"phase3-v3",
		"phase3-v4",
		"v1.3.2",
		"1.3.6",
		"1.4.0",
		"1.4.1",
		"1.4.2",
	}
	rejected := []string{"unknown", "legacy-import", "1.4.3", "1.5.0", "1.6.0", "2.0.0"}
	base := time.Date(2026, 8, 5, 9, 0, 0, 0, cnLocation())
	acceptedRows := make([]models.AiRecommendStocks, 0, len(accepted))
	for index, version := range accepted {
		at := base.Add(time.Duration(index) * time.Minute)
		row := models.AiRecommendStocks{
			DataTime:        &at,
			StockCode:       "600001.SH",
			StockName:       "accepted-" + version,
			SummaryVersion:  version,
			RecommendStatus: "frozen",
		}
		if err := database.Create(&row).Error; err != nil {
			t.Fatalf("seed accepted version %q: %v", version, err)
		}
		acceptedRows = append(acceptedRows, row)
	}
	rejectedRows := make([]models.AiRecommendStocks, 0, len(rejected))
	for index, version := range rejected {
		// Rejected rows are newer than every accepted row. Applying LIMIT before
		// the canonical SQL predicate would therefore return the wrong result.
		at := base.Add(time.Duration(len(accepted)+index+1) * time.Minute)
		row := models.AiRecommendStocks{
			DataTime:        &at,
			StockCode:       "600002.SH",
			StockName:       "rejected-" + version,
			SummaryVersion:  version,
			RecommendStatus: "mutable-or-unknown",
		}
		if err := database.Create(&row).Error; err != nil {
			t.Fatalf("seed rejected version %q: %v", version, err)
		}
		rejectedRows = append(rejectedRows, row)
	}

	repository := NewCompatibilityLegacyRepository(database)
	all, err := repository.List(context.Background(), legacy.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(acceptedRows) {
		t.Fatalf("legacy rows = %d, want %d: %+v", len(all), len(acceptedRows), all)
	}
	for _, row := range all {
		if !legacy.IsFrozenVersion(row.StrategyVersion) {
			t.Fatalf("repository exposed non-canonical version %q", row.StrategyVersion)
		}
	}

	limited, err := repository.List(context.Background(), legacy.Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ID != acceptedRows[len(acceptedRows)-1].ID || limited[1].ID != acceptedRows[len(acceptedRows)-2].ID {
		t.Fatalf("limited legacy rows = %+v, want latest two accepted rows", limited)
	}

	for _, row := range acceptedRows {
		found, findErr := repository.Find(context.Background(), row.ID)
		if findErr != nil || found.ID != row.ID {
			t.Fatalf("find accepted version %q: row=%+v err=%v", row.SummaryVersion, found, findErr)
		}
	}
	for _, row := range rejectedRows {
		if _, findErr := repository.Find(context.Background(), row.ID); !errors.Is(findErr, legacy.ErrNotFrozenLegacy) {
			t.Fatalf("find rejected version %q error = %v, want ErrNotFrozenLegacy", row.SummaryVersion, findErr)
		}
	}
	if _, err := repository.Find(context.Background(), 999999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find missing row error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestCompatibilityLegacyRepositoryNormalizesStoredVersionForSQL(t *testing.T) {
	database := compatibilityTestDB(t, "normalized-legacy-scope")
	if err := database.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 5, 9, 0, 0, 0, cnLocation())
	row := models.AiRecommendStocks{
		DataTime: &at, StockCode: "sh600000", StockName: "normalized",
		SummaryVersion: " PHASE3-V4 ", RecommendStatus: "frozen",
	}
	if err := database.Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	repository := NewCompatibilityLegacyRepository(database)
	result, err := repository.List(context.Background(), legacy.Query{Limit: 1})
	if err != nil || len(result) != 1 || result[0].ID != row.ID {
		t.Fatalf("normalized legacy result=%+v err=%v", result, err)
	}
	if _, err := repository.Find(context.Background(), row.ID); err != nil {
		t.Fatalf("find normalized legacy row: %v", err)
	}
}
