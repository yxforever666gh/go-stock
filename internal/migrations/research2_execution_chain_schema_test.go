package migrations

import (
	"strings"
	"testing"
	"time"

	"go-stock/backend/research2"

	"gorm.io/gorm"
)

func TestSchema26AddsExecutionChainAuditWithoutChangingFinancialHistory(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_analysis_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL UNIQUE,
  trading_date TEXT NOT NULL,
  attempt_no INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL,
  report_markdown TEXT,
  updated_at DATETIME
);
CREATE TABLE research2_recommendations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  recommendation_id TEXT NOT NULL UNIQUE,
  analysis_run_id TEXT NOT NULL,
  stock_code TEXT NOT NULL,
  status TEXT NOT NULL,
  net_pn_l REAL NOT NULL,
  updated_at DATETIME
);
CREATE TABLE research2_accounts (
  id INTEGER PRIMARY KEY,
  initial_cash REAL NOT NULL,
  cash REAL NOT NULL,
  updated_at DATETIME
);
CREATE TABLE research2_trades (
  id INTEGER PRIMARY KEY,
  trade_id TEXT NOT NULL UNIQUE,
  recommendation_id TEXT NOT NULL,
  net_cash_flow REAL NOT NULL
);
CREATE TABLE research2_account_snapshots (
  id INTEGER PRIMARY KEY,
  snapshot_id TEXT NOT NULL UNIQUE,
  net_asset_value REAL NOT NULL,
  net_profit REAL NOT NULL,
  return_rate REAL NOT NULL
);
INSERT INTO research2_analysis_runs(run_id,trading_date,attempt_no,status,report_markdown,updated_at)
VALUES ('legacy-run','2026-09-03',1,'success','unchanged','2026-09-03 10:00:00');
INSERT INTO research2_recommendations(recommendation_id,analysis_run_id,stock_code,status,net_pn_l,updated_at)
VALUES ('legacy-rec','legacy-run','sh600000','closed',88.75,'2026-09-04 10:00:00');
INSERT INTO research2_accounts VALUES (1,12000,10981.50,'2026-09-04 10:01:00');
INSERT INTO research2_trades VALUES (1,'legacy-trade','legacy-rec',1088.75);
INSERT INTO research2_account_snapshots VALUES (1,'legacy-snapshot',12088.75,88.75,0.0073958333);`).Error; err != nil {
		t.Fatal(err)
	}

	financialBefore := snapshotSchema26FinancialRows(t, database)
	if err := applyResearch2ExecutionChainSchema(database); err != nil {
		t.Fatalf("apply schema 26: %v", err)
	}
	if err := applyResearch2ExecutionChainSchema(database); err != nil {
		t.Fatalf("repeat schema 26: %v", err)
	}

	var run struct {
		TriggerSource  string
		ChainID        string
		ParentRunID    string
		RequestedSlots int
		PrimaryCount   int
		StandbyCount   int
		ReportMarkdown string
		UpdatedAt      string
	}
	if err := database.Table("research2_analysis_runs").Where("run_id = ?", "legacy-run").Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.TriggerSource != research2LegacyUnversioned || run.ChainID != "" || run.ParentRunID != "" || run.RequestedSlots != 0 || run.PrimaryCount != 0 || run.StandbyCount != 0 || run.ReportMarkdown != "unchanged" || !strings.HasPrefix(run.UpdatedAt, "2026-09-03T10:00:00") {
		t.Fatalf("legacy run changed: %+v", run)
	}

	var recommendation struct {
		SelectionRole             string
		SelectionRank             int
		ReplacesRecommendationID  string
		PromotionReason           string
		ExecutionFailureCode      string
		ExecutionQuotePrice       float64
		ExecutionQuoteAt          *time.Time
		ExecutionLimitPrice       float64
		ExecutionLimitDistancePct *float64
		Status                    string
		NetPnL                    float64
		UpdatedAt                 string
	}
	if err := database.Table("research2_recommendations").Where("recommendation_id = ?", "legacy-rec").Take(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	if recommendation.SelectionRole != research2LegacyUnversioned || recommendation.SelectionRank != 0 || recommendation.ReplacesRecommendationID != "" || recommendation.PromotionReason != "" || recommendation.ExecutionFailureCode != "" || recommendation.ExecutionQuotePrice != 0 || recommendation.ExecutionQuoteAt != nil || recommendation.ExecutionLimitPrice != 0 || recommendation.ExecutionLimitDistancePct != nil || recommendation.Status != "closed" || recommendation.NetPnL != 88.75 || !strings.HasPrefix(recommendation.UpdatedAt, "2026-09-04T10:00:00") {
		t.Fatalf("legacy recommendation changed: %+v", recommendation)
	}
	var chainCount int64
	if err := database.Model(&research2.ExecutionChain{}).Count(&chainCount).Error; err != nil || chainCount != 0 {
		t.Fatalf("historical chain count=%d err=%v", chainCount, err)
	}
	if financialAfter := snapshotSchema26FinancialRows(t, database); financialAfter != financialBefore {
		t.Fatalf("schema 26 changed financial history\nbefore: %s\nafter:  %s", financialBefore, financialAfter)
	}
	if err := verifyMainSchema26Runtime(database); err != nil {
		t.Fatal(err)
	}
}

func TestSchema26ExecutionChainUniquenessAndIndexes(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_analysis_runs (id INTEGER PRIMARY KEY, trigger_source TEXT);
CREATE TABLE research2_recommendations (id INTEGER PRIMARY KEY, selection_role TEXT);`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearch2ExecutionChainSchema(database); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 9, 50, 0, 0, time.UTC)
	first := research2.ExecutionChain{ChainID: "chain-1", TradingDate: "2026-09-04", ScheduledFor: now, Status: "running", StartedAt: now}
	if err := database.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicateDate := research2.ExecutionChain{ChainID: "chain-2", TradingDate: first.TradingDate, ScheduledFor: now, Status: "running", StartedAt: now}
	if err := database.Create(&duplicateDate).Error; err == nil {
		t.Fatal("duplicate execution-chain trading date was accepted")
	}
	duplicateID := research2.ExecutionChain{ChainID: first.ChainID, TradingDate: "2026-09-05", ScheduledFor: now, Status: "running", StartedAt: now}
	if err := database.Create(&duplicateID).Error; err == nil {
		t.Fatal("duplicate execution chain ID was accepted")
	}
	definition := mainMigrationV26Definition()
	for _, fragment := range []string{"research2_execution_chains", "trigger_source", "selection_role", "execution_failure_code", "no historical execution chains", "no account, trade"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("schema 26 definition is missing %q: %s", fragment, definition)
		}
	}
}

func snapshotSchema26FinancialRows(t *testing.T, database *gorm.DB) string {
	t.Helper()
	var result string
	if err := database.Raw(`SELECT printf('%.2f|%.2f|%s|%.2f|%.2f|%.10f',
  a.initial_cash,a.cash,t.trade_id,t.net_cash_flow,s.net_profit,s.return_rate)
FROM research2_accounts a
JOIN research2_trades t ON t.id=1
JOIN research2_account_snapshots s ON s.id=1
WHERE a.id=1`).Scan(&result).Error; err != nil {
		t.Fatal(err)
	}
	return result
}
