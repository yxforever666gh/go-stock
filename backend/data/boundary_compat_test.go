package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-stock/backend/legacy"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/portfolio"
)

func TestCompatibilityPortfolioLedgerUsesOnlyCausallyVisibleSealedEvents(t *testing.T) {
	database := compatibilityTestDB(t, "ledger")
	if err := database.AutoMigrate(&models.OrderEvent{}); err != nil {
		t.Fatal(err)
	}
	loc := cnLocation()
	eventAt := time.Date(2026, 8, 5, 10, 0, 0, 0, loc)
	frozenAt := eventAt.Add(time.Minute)
	asOf := eventAt.Add(time.Hour)
	futureFrozenAt := asOf.Add(time.Hour)
	events := []models.OrderEvent{
		{EventID: "visible", RunID: "run-a", RuleID: "rule-a", StrategyVersion: "1.5.0", TradeDate: "2026-08-05", Symbol: "600000.SH", EventType: "rule_issued", Sequence: 1, EventAt: eventAt, Reason: "test", PayloadJSON: `{}`, FrozenAt: &frozenAt},
		{EventID: "future", RunID: "run-b", RuleID: "rule-b", StrategyVersion: "1.5.0", TradeDate: "2026-08-05", Symbol: "000001.SZ", EventType: "rule_issued", Sequence: 1, EventAt: eventAt, Reason: "test", PayloadJSON: `{}`, FrozenAt: &futureFrozenAt},
	}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	query := portfolio.LedgerQuery{StrategyVersion: "1.5.0", AsOf: asOf}
	reader := portfolio.NewReader(NewCompatibilityPortfolioLedger(database))
	rows, seal, err := reader.Events(context.Background(), query)
	if err != nil || len(rows) != 1 || rows[0].EventID != "visible" || seal.EventCount != 1 || len(seal.LedgerHash) != 64 {
		t.Fatalf("rows=%+v seal=%+v err=%v", rows, seal, err)
	}
	seal.LedgerHash = strings.Repeat("0", 64)
	if err := (CompatibilityPortfolioLedger{}).VerifyLedgerSeal(context.Background(), query, seal, rows); err == nil {
		t.Fatal("tampered ledger seal was accepted")
	}
}

func TestCompatibilityLegacyRepositoryExcludesCurrentStrategy(t *testing.T) {
	database := compatibilityTestDB(t, "legacy")
	if err := database.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 5, 9, 0, 0, 0, cnLocation())
	rows := []models.AiRecommendStocks{
		{DataTime: &at, StockCode: "sh600000", StockName: "legacy", SummaryVersion: "1.4.2", RecommendStatus: "frozen"},
		{DataTime: &at, StockCode: "sz000001", StockName: "current", SummaryVersion: "1.5.0", RecommendStatus: "conditional"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	repository := NewCompatibilityLegacyRepository(database)
	result, err := repository.List(context.Background(), legacy.Query{Start: at.Add(-time.Hour), End: at.Add(time.Hour)})
	if err != nil || len(result) != 1 || result[0].StrategyVersion != "1.4.2" || result[0].Symbol != "600000.SH" {
		t.Fatalf("legacy result=%+v err=%v", result, err)
	}
	if _, err := repository.Find(context.Background(), rows[1].ID); err == nil {
		t.Fatal("current strategy row was exposed through legacy repository")
	}
}

func TestApplyStrategyCohortFilterIncludesEveryFrozenLegacyVersion(t *testing.T) {
	database := compatibilityTestDB(t, "legacy-cohort-filter")
	if err := database.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatal(err)
	}
	rows := []models.AiRecommendStocks{
		{StockCode: "600001.SH", SummaryVersion: ""},
		{StockCode: "600002.SH", SummaryVersion: marketSummaryPhase3Version},
		{StockCode: "600003.SH", SummaryVersion: marketSummaryVersion141},
		{StockCode: "600004.SH", SummaryVersion: marketSummaryVersion142},
		{StockCode: "600005.SH", SummaryVersion: "legacy-import"},
		{StockCode: "600006.SH", SummaryVersion: marketSummaryCurrentVersion},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	var got []models.AiRecommendStocks
	if err := applyStrategyCohortFilter(
		database.Model(&models.AiRecommendStocks{}),
		strategyCohortLegacy,
	).Order("stock_code ASC").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("legacy cohort rows=%+v, want every five non-current rows", got)
	}
	for _, row := range got {
		if !isFrozenLegacyStrategyRecord(&row) {
			t.Fatalf("legacy cohort exposed current row: %+v", row)
		}
	}
}
