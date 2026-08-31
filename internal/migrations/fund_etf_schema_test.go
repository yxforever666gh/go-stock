package migrations

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"
)

func TestSchema20FundETFCacheIsIdempotentUpdatableAndIdentitySafe(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyFundETFSchema(database); err != nil {
		t.Fatalf("apply schema 20: %v", err)
	}
	if err := applyFundETFSchema(database); err != nil {
		t.Fatalf("repeat schema 20: %v", err)
	}
	if err := verifyMainSchema20Runtime(database); err != nil {
		t.Fatal(err)
	}
	if definition := mainMigrationV20Definition(); !strings.Contains(definition, "CREATE TABLE IF NOT EXISTS fund_ranking_snapshots") || !strings.Contains(definition, "CREATE TABLE IF NOT EXISTS etf_watchlist") {
		t.Fatalf("schema 20 definition is incomplete: %s", definition)
	}

	now := "2026-08-28T16:00:00+08:00"
	later := "2026-08-28T16:05:00+08:00"
	hash := strings.Repeat("a", 64)
	if err := database.Exec(`INSERT INTO etf_instruments(
etf_id,market,exchange,code,exchange_symbol,fund_code,name,category,tracking_index_code,tracking_index_name,management_fee_pct,status,source_name,source_ref,as_of,fetched_at,created_at,updated_at)
VALUES ('etf-SH-510300','SH','SSE','510300','510300','510300','沪深300ETF','broad','000300','沪深300',0.5,'listed','SSE','https://example.test/510300',?,?,?,?)`, now, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`UPDATE etf_instruments SET name='沪深300ETF（更新）',fetched_at=?,updated_at=? WHERE etf_id='etf-SH-510300'`, later, later).Error; err != nil {
		t.Fatalf("cache metadata update failed: %v", err)
	}
	if err := database.Exec(`UPDATE etf_instruments SET code='510301' WHERE etf_id='etf-SH-510300'`).Error; err == nil {
		t.Fatal("ETF identity update unexpectedly succeeded")
	}
	if err := database.Exec(`INSERT INTO etf_instruments(
etf_id,market,exchange,code,exchange_symbol,name,category,status,source_name,as_of,fetched_at,created_at,updated_at)
VALUES ('etf-duplicate','SH','SSE','510300','510301','duplicate','broad','listed','SSE',?,?,?,?)`, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate ETF market/code unexpectedly succeeded")
	}

	if err := database.Exec(`INSERT INTO fund_ranking_snapshots(
ranking_snapshot_id,source_name,source_ref,fund_code,fund_name,fund_category,ranking_period,trade_date,nav_date,as_of,fetched_at,rank_position,rank_total,unit_nav,return_1m_pct,fund_size_cny,fund_size_as_of,raw_payload_sha256,created_at,updated_at)
VALUES ('rank-1','provider-a','https://example.test/rank','000001','示例基金','stock','month','2026-08-28','2026-08-27',?,?,1,100,1.25,8.5,1000000000,'2026-06-30',?,?,?)`, now, now, hash, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`UPDATE fund_ranking_snapshots SET return_1m_pct=9.1,fetched_at=?,updated_at=? WHERE ranking_snapshot_id='rank-1'`, later, later).Error; err != nil {
		t.Fatalf("fund ranking cache update failed: %v", err)
	}
	if err := database.Exec(`INSERT INTO fund_ranking_snapshots(
ranking_snapshot_id,source_name,fund_code,fund_name,fund_category,ranking_period,trade_date,as_of,fetched_at,rank_position,created_at,updated_at)
VALUES ('rank-duplicate','provider-a','000001','示例基金','stock','month','2026-08-28',?,?,2,?,?)`, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate fund/source/category/trade-date ranking unexpectedly succeeded")
	}

	if err := database.Exec(`INSERT INTO etf_market_snapshots(
market_snapshot_id,etf_id,source_name,trade_date,as_of,fetched_at,open_price,high_price,low_price,close_price,previous_close,change_pct,volume,turnover_amount,raw_payload_sha256,created_at,updated_at)
VALUES ('market-1','etf-SH-510300','provider-a','2026-08-28',?,?,4.10,4.20,4.05,4.18,4.08,2.45,1000000,4180000,?,?,?)`, now, now, hash, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`UPDATE etf_market_snapshots SET close_price=4.19,fetched_at=?,updated_at=? WHERE market_snapshot_id='market-1'`, later, later).Error; err != nil {
		t.Fatalf("ETF market cache update failed: %v", err)
	}
	if err := database.Exec(`INSERT INTO etf_market_snapshots(
market_snapshot_id,etf_id,source_name,trade_date,as_of,fetched_at,created_at,updated_at)
VALUES ('market-duplicate','etf-SH-510300','provider-a','2026-08-28',?,?,?,?)`, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate ETF market source/code/trade-date unexpectedly succeeded")
	}

	if err := database.Exec(`INSERT INTO etf_nav_snapshots(
nav_snapshot_id,etf_id,source_name,nav_date,trade_date,as_of,fetched_at,unit_nav,accumulated_nav,iopv,market_price,premium_discount_pct,fund_size_cny,shares_outstanding,created_at,updated_at)
VALUES ('nav-1','etf-SH-510300','provider-a','2026-08-28','2026-08-28',?,?,4.17,4.17,4.175,4.18,0.24,50000000000,12000000000,?,?)`, now, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO etf_nav_snapshots(
nav_snapshot_id,etf_id,source_name,nav_date,as_of,fetched_at,created_at,updated_at)
VALUES ('nav-duplicate','etf-SH-510300','provider-a','2026-08-28',?,?,?,?)`, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate ETF NAV source/code/date unexpectedly succeeded")
	}

	if err := database.Exec(`INSERT INTO etf_fund_flow_snapshots(
flow_snapshot_id,etf_id,source_name,trade_date,as_of,fetched_at,shares_outstanding,share_change,net_subscription_shares,net_flow_cny,net_flow_5d_cny,fund_size_cny,created_at,updated_at)
VALUES ('flow-1','etf-SH-510300','provider-a','2026-08-28',?,?,12000000000,1000000,1000000,4180000,20000000,50000000000,?,?)`, now, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO etf_fund_flow_snapshots(
flow_snapshot_id,etf_id,source_name,trade_date,as_of,fetched_at,created_at,updated_at)
VALUES ('flow-duplicate','etf-SH-510300','provider-a','2026-08-28',?,?,?,?)`, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate ETF flow source/code/trade-date unexpectedly succeeded")
	}

	if err := database.Exec(`INSERT INTO etf_holding_snapshots(
holding_snapshot_id,etf_id,source_name,report_date,as_of,fetched_at,total_positions,disclosed_asset_ratio_pct,created_at,updated_at)
VALUES ('holding-1','etf-SH-510300','provider-a','2026-06-30',?,?,300,78.5,?,?)`, now, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO etf_holding_snapshots(
holding_snapshot_id,etf_id,source_name,report_date,as_of,fetched_at,created_at,updated_at)
VALUES ('holding-duplicate','etf-SH-510300','provider-a','2026-06-30',?,?,?,?)`, now, now, now, now).Error; err == nil {
		t.Fatal("duplicate ETF holding source/code/report-date unexpectedly succeeded")
	}
	if err := database.Exec(`INSERT INTO etf_holding_positions(
holding_position_id,holding_snapshot_id,rank,asset_type,market,code,name,quantity,market_value,weight_pct,created_at,updated_at)
VALUES ('position-1','holding-1',1,'stock','SH','600519','贵州茅台',10000,15000000,4.5,?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO etf_holding_positions(
holding_position_id,holding_snapshot_id,rank,asset_type,market,code,name,created_at,updated_at)
VALUES ('position-duplicate-rank','holding-1',1,'stock','SZ','000001','平安银行',?,?)`, now, now).Error; err == nil {
		t.Fatal("duplicate ETF holding rank unexpectedly succeeded")
	}

	if err := database.Exec(`INSERT INTO etf_watchlist(code,name,market,category,created_at,updated_at)
VALUES ('510300','沪深300ETF','SH','broad',?,?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`UPDATE etf_watchlist SET name='沪深300ETF（自选）',updated_at=? WHERE code='510300'`, later).Error; err != nil {
		t.Fatalf("ETF watchlist metadata update failed: %v", err)
	}
	if err := database.Exec(`UPDATE etf_watchlist SET code='510301' WHERE code='510300'`).Error; err == nil {
		t.Fatal("ETF watchlist code identity update unexpectedly succeeded")
	}
	if err := database.Exec(`INSERT INTO etf_watchlist(code,name,market,category,created_at,updated_at)
VALUES ('510300','duplicate','SH','broad',?,?)`, now, now).Error; err == nil {
		t.Fatal("duplicate ETF watchlist code unexpectedly succeeded")
	}
}

func TestSchema20VerifierRejectsMissingFundETFTableAndIndex(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		database := openMigrationTestDB(t)
		if err := applyFundETFSchema(database); err != nil {
			t.Fatal(err)
		}
		if err := database.Exec(`DROP TABLE etf_nav_snapshots`).Error; err != nil {
			t.Fatal(err)
		}
		if err := verifyMainSchema20Runtime(database); err == nil {
			t.Fatal("schema 20 verifier accepted a missing ETF NAV table")
		}
	})

	t.Run("index", func(t *testing.T) {
		database := openMigrationTestDB(t)
		if err := applyFundETFSchema(database); err != nil {
			t.Fatal(err)
		}
		if err := database.Exec(`DROP INDEX idx_etf_watchlist_code`).Error; err != nil {
			t.Fatal(err)
		}
		if err := verifyMainSchema20Runtime(database); err == nil {
			t.Fatal("schema 20 verifier accepted a missing ETF watchlist unique-code index")
		}
	})
}

func TestSchema19To20FundETFMigrationPreservesResearchAccountingAndHasNoTradingLinks(t *testing.T) {
	database := openMigrationTestDB(t)
	applyPublishedMigrationPrefix(t, database, mainMigrations, 19, "2.4.0")
	if database.Migrator().HasTable("fund_ranking_snapshots") || database.Migrator().HasTable("etf_watchlist") {
		t.Fatal("schema 19 fixture unexpectedly contains schema 20 tables")
	}

	now := time.Date(2026, 8, 28, 15, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	if err := database.Model(&research.SimulatedAccount{}).Where("id = ?", 1).Update("cash", 432100.25).Error; err != nil {
		t.Fatal(err)
	}
	r1Recommendation := research.Recommendation{
		RecommendationID: "schema20-r1-rec", AnalysisRunID: "schema20-r1-run", StockCode: "sh600000", StockName: "浦发银行",
		SignalAt: now, Status: "closed", TotalFees: 7.52173, NetPnL: 91.47827, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&r1Recommendation).Error; err != nil {
		t.Fatal(err)
	}
	r1Trade := research.SimulatedTrade{
		TradeID: "schema20-r1-trade", RecommendationID: r1Recommendation.RecommendationID, StockCode: "sh600000", Side: "sell", TradedAt: now,
		MarketPrice: 10.25, ExecutionPrice: 10.23, Quantity: 100, Notional: 1023, Commission: 5, StampDuty: 0.5115,
		TransferFee: 0.01023, SlippageAmount: 2, TotalFees: 7.52173, NetCashFlow: 1015.47827, CreatedAt: now,
	}
	if err := database.Create(&r1Trade).Error; err != nil {
		t.Fatal(err)
	}
	r1Position := research.Position{
		RecommendationID: r1Recommendation.RecommendationID, StockCode: "sh600000", StockName: "浦发银行", Market: "SH", Quantity: 100,
		EntryAt: now.Add(-time.Hour), EntryPrice: 10.01, BuyFees: 5.01, Status: "closed", ExitAt: &now, ExitPrice: 10.23,
		SellFees: 5.52173, NetPnL: 9.46827, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&r1Position).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&research2.Account{}).Where("id = ?", 1).Update("cash", 9988.75).Error; err != nil {
		t.Fatal(err)
	}
	r2Recommendation := research2.Recommendation{
		RecommendationID: "schema20-r2-rec", AnalysisRunID: "schema20-r2-run", StockCode: "sz000001", StockName: "平安银行",
		SignalAt: now, Status: "closed", TargetBuyAt: now, BuyFees: 5.01, SellFees: 5.52, NetPnL: 21.75, NetYieldRate: 1.8,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&r2Recommendation).Error; err != nil {
		t.Fatal(err)
	}
	r2Trade := research2.Trade{
		TradeID: "schema20-r2-trade", RecommendationID: r2Recommendation.RecommendationID, Side: "sell", TradedAt: now,
		MarketPrice: 10.22, ExecutionPrice: 10.20, Quantity: 100, Commission: 5, StampDuty: 0.51, TransferFee: 0.01,
		SlippageAmount: 2, NetCashFlow: 1014.48, CreatedAt: now,
	}
	if err := database.Create(&r2Trade).Error; err != nil {
		t.Fatal(err)
	}
	r2Snapshot := research2.AccountSnapshot{
		SnapshotID: "schema20-r2-snapshot", ValuedAt: now, TradingDate: "2026-08-28", SnapshotType: "close", Cash: 9988.75,
		PositionValue: 1000, NetAssetValue: 10988.75, NetProfit: -1011.25, ReturnRate: -8.427, CreatedAt: now,
	}
	if err := database.Create(&r2Snapshot).Error; err != nil {
		t.Fatal(err)
	}

	before := captureResearchHistory(t, database)
	if err := MigrateMain(database); err != nil {
		t.Fatal(err)
	}
	if err := MigrateMain(database); err != nil {
		t.Fatalf("repeat schema 20: %v", err)
	}
	afterMigration := captureResearchHistory(t, database)
	assertSchema20ResearchHistoryEqual(t, before, afterMigration)

	stamp := now.Format(time.RFC3339)
	if err := database.Exec(`INSERT INTO etf_watchlist(code,name,market,category,created_at,updated_at)
VALUES ('159915','创业板ETF','SZ','broad',?,?)`, stamp, stamp).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO etf_instruments(
etf_id,market,exchange,code,exchange_symbol,name,category,status,source_name,as_of,fetched_at,created_at,updated_at)
VALUES ('etf-SZ-159915','SZ','SZSE','159915','159915','创业板ETF','broad','listed','SZSE',?,?,?,?)`, stamp, stamp, stamp, stamp).Error; err != nil {
		t.Fatal(err)
	}
	afterCacheWrites := captureResearchHistory(t, database)
	assertSchema20ResearchHistoryEqual(t, before, afterCacheWrites)

	newTables := []string{
		"fund_ranking_snapshots", "etf_instruments", "etf_market_snapshots", "etf_nav_snapshots",
		"etf_fund_flow_snapshots", "etf_holding_snapshots", "etf_holding_positions", "etf_watchlist",
	}
	for _, table := range newTables {
		var foreignKeyCount int64
		if err := database.Raw("SELECT COUNT(*) FROM pragma_foreign_key_list(?)", table).Scan(&foreignKeyCount).Error; err != nil {
			t.Fatalf("inspect %s foreign keys: %v", table, err)
		}
		if foreignKeyCount != 0 {
			t.Fatalf("schema 20 table %s has %d foreign keys; expected cache-only text identities", table, foreignKeyCount)
		}
	}
	var triggerSQL []string
	if err := database.Raw(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND tbl_name IN ?`, newTables).Scan(&triggerSQL).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range triggerSQL {
		lower := strings.ToLower(statement)
		for _, forbidden := range []string{
			"research_v160_", "research_v170_", "research2_", "recommendations", "simulated_accounts", "simulated_trades",
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("schema 20 trigger references forbidden research/trading state %q: %s", forbidden, statement)
			}
		}
	}
	if database.Migrator().HasTable("followed_fund") {
		var followedFundRows int64
		if err := database.Table("followed_fund").Count(&followedFundRows).Error; err != nil {
			t.Fatal(err)
		}
		if followedFundRows != 0 {
			t.Fatalf("ETF watchlist unexpectedly reused followed_fund: rows=%d", followedFundRows)
		}
	}
}

func assertSchema20ResearchHistoryEqual(t *testing.T, expected, actual map[string][]byte) {
	t.Helper()
	for table, expectedRows := range expected {
		if !bytes.Equal(expectedRows, actual[table]) {
			t.Fatalf("schema 20 rewrote historical %s", table)
		}
	}
}
