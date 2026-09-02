package migrations

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestSchema23AddsNullableMetadataWithoutRewritingSchema22History(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_analysis_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    trading_date TEXT NOT NULL,
    scheduled_for DATETIME NOT NULL,
    started_at DATETIME NOT NULL,
    evidence_cutoff_at DATETIME,
    generated_at DATETIME,
    status TEXT NOT NULL,
    report_markdown TEXT,
    recommendation_count INTEGER,
    on_time BOOLEAN,
    created_at DATETIME,
    updated_at DATETIME
);
CREATE TABLE research2_trades (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trade_id TEXT NOT NULL,
    recommendation_id TEXT NOT NULL,
    side TEXT NOT NULL,
    traded_at DATETIME NOT NULL,
    market_price REAL,
    execution_price REAL,
    quantity INTEGER,
    commission REAL,
    stamp_duty REAL,
    transfer_fee REAL,
    slippage_amount REAL,
    net_cash_flow REAL,
    created_at DATETIME
);
CREATE TABLE research2_recommendations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    recommendation_id TEXT NOT NULL,
    net_pn_l REAL,
    net_yield_rate REAL,
    updated_at DATETIME
);
CREATE TABLE research2_accounts (id INTEGER PRIMARY KEY, initial_cash REAL, cash REAL, updated_at DATETIME);
CREATE TABLE research2_account_snapshots (
    id INTEGER PRIMARY KEY,
    snapshot_id TEXT,
    net_asset_value REAL,
    net_profit REAL,
    return_rate REAL,
    created_at DATETIME
);`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 10, 3, 44, 0, time.UTC)
	if err := database.Exec(`INSERT INTO research2_analysis_runs
(run_id,trading_date,scheduled_for,started_at,evidence_cutoff_at,generated_at,status,report_markdown,recommendation_count,on_time,created_at,updated_at)
VALUES ('schema23-run','2026-09-01',?,?,?,?, 'success','historical report',3,0,?,?);
INSERT INTO research2_trades
(trade_id,recommendation_id,side,traded_at,market_price,execution_price,quantity,commission,stamp_duty,transfer_fee,slippage_amount,net_cash_flow,created_at)
VALUES ('schema23-trade','schema23-rec','sell',?,10.22,10.20,100,5,0.51,0.01,2,1014.48,?);
INSERT INTO research2_recommendations (recommendation_id,net_pn_l,net_yield_rate,updated_at)
VALUES ('schema23-rec',88.75,0.0875,?);
INSERT INTO research2_accounts VALUES (1,12000,10981.50,?);
INSERT INTO research2_account_snapshots VALUES (1,'schema23-snapshot',12088.75,88.75,0.0073958333,?);`,
		now.Add(-14*time.Minute), now.Add(-14*time.Minute), now.Add(-9*time.Minute), now, now, now, now, now, now, now).Error; err != nil {
		t.Fatal(err)
	}

	if err := applyResearch2Trailing5Schema(database); err != nil {
		t.Fatalf("apply schema 23: %v", err)
	}
	if err := applyResearch2Trailing5Schema(database); err != nil {
		t.Fatalf("repeat schema 23: %v", err)
	}

	var run struct {
		ReportMarkdown        string
		RecommendationCount   int
		OnTime                bool
		UpdatedAt             time.Time
		EvidenceWindowStartAt sql.NullTime
		EvidenceCoveragePct   sql.NullFloat64
		Degraded              sql.NullBool
	}
	if err := database.Table(research2AnalysisRunsTable).Where("run_id = ?", "schema23-run").Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.ReportMarkdown != "historical report" || run.RecommendationCount != 3 || run.OnTime || !run.UpdatedAt.Equal(now) {
		t.Fatalf("schema 23 changed analysis history: %+v", run)
	}
	if run.EvidenceWindowStartAt.Valid || run.EvidenceCoveragePct.Valid || run.Degraded.Valid {
		t.Fatalf("schema 23 backfilled historical analysis metadata: %+v", run)
	}

	var trade struct {
		ExecutionPrice float64
		NetCashFlow    float64
		PriceSource    sql.NullString
		ExecutionMode  sql.NullString
	}
	if err := database.Table(research2TradesTable).Where("trade_id = ?", "schema23-trade").Take(&trade).Error; err != nil {
		t.Fatal(err)
	}
	if trade.ExecutionPrice != 10.20 || trade.NetCashFlow != 1014.48 || trade.PriceSource.Valid || trade.ExecutionMode.Valid {
		t.Fatalf("schema 23 changed or backfilled trade history: %+v", trade)
	}
	var result struct {
		NetPnL        float64
		NetYieldRate  float64
		Cash          float64
		NetAssetValue float64
		NetProfit     float64
		ReturnRate    float64
	}
	if err := database.Raw(`SELECT r.net_pn_l, r.net_yield_rate, a.cash,
s.net_asset_value, s.net_profit, s.return_rate
FROM research2_recommendations r
JOIN research2_accounts a ON a.id=1
JOIN research2_account_snapshots s ON s.id=1
WHERE r.recommendation_id='schema23-rec'`).Scan(&result).Error; err != nil {
		t.Fatal(err)
	}
	if result.NetPnL != 88.75 || result.NetYieldRate != 0.0875 || result.Cash != 10981.50 || result.NetAssetValue != 12088.75 || result.NetProfit != 88.75 || result.ReturnRate != 0.0073958333 {
		t.Fatalf("schema 23 changed recommendation/account/return history: %+v", result)
	}
	if got := quickCheck(database); got != "ok" {
		t.Fatalf("quick_check = %q", got)
	}
}

func TestSchema23DefinitionAndVerifier(t *testing.T) {
	definition := mainMigrationV23Definition()
	for _, fragment := range []string{"evidence_window_start_at", "evidence_coverage_pct", "degraded", "price_source", "execution_mode", "without backfill"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("schema 23 definition is missing %q: %s", fragment, definition)
		}
	}
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_analysis_runs (id INTEGER PRIMARY KEY);
CREATE TABLE research2_trades (id INTEGER PRIMARY KEY);`).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema23Runtime(database); err == nil {
		t.Fatal("schema 23 verifier accepted missing columns")
	}
}
