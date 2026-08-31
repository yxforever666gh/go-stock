package migrations

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestSchema22DropsRankAndInitializesOpenPositionPrices(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_recommendations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    recommendation_id TEXT NOT NULL,
    rank INTEGER NOT NULL DEFAULT 0,
    stock_code TEXT NOT NULL,
    stock_name TEXT NOT NULL,
    status TEXT NOT NULL,
    buy_at DATETIME,
    buy_market_price REAL,
    buy_price REAL,
    quantity INTEGER,
    net_pn_l REAL,
    updated_at DATETIME
);
CREATE UNIQUE INDEX idx_schema22_recommendation_id ON research2_recommendations(recommendation_id);
CREATE INDEX idx_schema22_stock_code ON research2_recommendations(stock_code);
CREATE TABLE research2_trades (id INTEGER PRIMARY KEY, trade_id TEXT, recommendation_id TEXT, net_cash_flow REAL);
CREATE TABLE research2_accounts (id INTEGER PRIMARY KEY, initial_cash REAL, cash REAL);
INSERT INTO research2_trades VALUES (1, 'trade-1', 'active-rec', -1018.50);
INSERT INTO research2_accounts VALUES (1, 12000, 10981.50);`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	updatedAt := now.Add(-time.Hour)
	rows := []struct {
		id             string
		rank           int
		code           string
		status         string
		buyAt          *time.Time
		buyMarketPrice float64
		buyPrice       float64
		quantity       int64
		netPnL         float64
	}{
		{id: "active-rec", rank: 1, code: "sh600000", status: "active", buyAt: &now, buyMarketPrice: 10.12, buyPrice: 10.13, quantity: 100},
		{id: "sell-pending-rec", rank: 2, code: "sz000001", status: "sell_pending", buyAt: &now, buyPrice: 20.30, quantity: 100},
		{id: "closed-rec", rank: 3, code: "sz000002", status: "closed", buyAt: &now, buyMarketPrice: 30.40, buyPrice: 30.42, quantity: 100, netPnL: 88.75},
	}
	for _, row := range rows {
		if err := database.Exec(`INSERT INTO research2_recommendations
(recommendation_id, rank, stock_code, stock_name, status, buy_at, buy_market_price, buy_price, quantity, net_pn_l, updated_at)
VALUES (?, ?, ?, '测试', ?, ?, ?, ?, ?, ?, ?)`, row.id, row.rank, row.code, row.status, row.buyAt, row.buyMarketPrice, row.buyPrice, row.quantity, row.netPnL, updatedAt).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := applyResearch2LiveValuationSchema(database); err != nil {
		t.Fatalf("apply schema 22: %v", err)
	}
	if err := applyResearch2LiveValuationSchema(database); err != nil {
		t.Fatalf("repeat schema 22: %v", err)
	}
	if database.Migrator().HasColumn(research2RecommendationsTable, "rank") {
		t.Fatal("schema 22 retained rank")
	}
	for id, wantPrice := range map[string]float64{"active-rec": 10.12, "sell-pending-rec": 20.30} {
		var stored struct {
			RecommendationID string
			StockCode        string
			CurrentPrice     *float64
			CurrentPriceAt   *time.Time
			NetPnL           float64
			UpdatedAt        time.Time
		}
		if err := database.Table(research2RecommendationsTable).Where("recommendation_id = ?", id).Take(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.CurrentPrice == nil || math.Abs(*stored.CurrentPrice-wantPrice) > 1e-8 || stored.CurrentPriceAt == nil || !stored.CurrentPriceAt.Equal(now) {
			t.Fatalf("current valuation for %s = %+v, want price %.2f at %v", id, stored, wantPrice, now)
		}
		if !stored.UpdatedAt.Equal(updatedAt) {
			t.Fatalf("schema 22 rewrote updated_at for %s: %v", id, stored.UpdatedAt)
		}
	}
	var closed struct {
		CurrentPrice   *float64
		CurrentPriceAt *time.Time
		NetPnL         float64
	}
	if err := database.Table(research2RecommendationsTable).Where("recommendation_id = 'closed-rec'").Take(&closed).Error; err != nil {
		t.Fatal(err)
	}
	if closed.CurrentPrice != nil || closed.CurrentPriceAt != nil || closed.NetPnL != 88.75 {
		t.Fatalf("schema 22 changed closed recommendation valuation: %+v", closed)
	}
	var tradeCashFlow, accountCash float64
	if err := database.Raw("SELECT net_cash_flow FROM research2_trades WHERE trade_id = 'trade-1'").Scan(&tradeCashFlow).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Raw("SELECT cash FROM research2_accounts WHERE id = 1").Scan(&accountCash).Error; err != nil {
		t.Fatal(err)
	}
	if tradeCashFlow != -1018.50 || accountCash != 10981.50 {
		t.Fatalf("schema 22 changed trade/account history: flow=%.2f cash=%.2f", tradeCashFlow, accountCash)
	}
	for _, indexName := range []string{"idx_schema22_recommendation_id", "idx_schema22_stock_code", "idx_research2_recommendations_current_price_at"} {
		var count int64
		if err := database.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", indexName).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("schema 22 removed index %s", indexName)
		}
	}
	var quickCheck string
	if err := database.Raw("PRAGMA quick_check").Scan(&quickCheck).Error; err != nil {
		t.Fatal(err)
	}
	if quickCheck != "ok" {
		t.Fatalf("quick_check = %q", quickCheck)
	}
}

func TestSchema22DefinitionAndVerifier(t *testing.T) {
	definition := mainMigrationV22Definition()
	for _, fragment := range []string{"DROP COLUMN rank", "current_price", "current_price_at", "idx_research2_recommendations_current_price_at", "active and sell_pending"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("schema 22 definition is missing %q: %s", fragment, definition)
		}
	}
	database := openMigrationTestDB(t)
	if err := database.Exec(`CREATE TABLE research2_recommendations (
id INTEGER PRIMARY KEY, rank INTEGER, current_price REAL, current_price_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema22Runtime(database); err == nil {
		t.Fatal("schema 22 verifier accepted a rank column")
	}
}
