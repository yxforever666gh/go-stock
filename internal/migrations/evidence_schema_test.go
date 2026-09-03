package migrations

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"

	"gorm.io/gorm"
)

func applyPublishedMigrationPrefix(t *testing.T, database *gorm.DB, items []migration, count int, appVersion string) {
	t.Helper()
	if err := database.AutoMigrate(&MigrationRecord{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range items[:count] {
		if err := item.apply(database); err != nil {
			t.Fatalf("apply migration %d: %v", item.id, err)
		}
		record := MigrationRecord{ID: item.id, Name: item.name, Checksum: item.checksum(), AppliedAt: time.Now().UTC(), AppVersion: appVersion}
		if err := database.Create(&record).Error; err != nil {
			t.Fatalf("record migration %d: %v", item.id, err)
		}
	}
}

func stripSchema15ColumnsFromFixture(t *testing.T, database *gorm.DB) {
	t.Helper()
	// Published migrations 3 and 13 use the current research models. Once the
	// schema 15 model fields exist, those historical AutoMigrate calls also
	// create the future evidence-set indexes while constructing this fixture.
	// SQLite refuses to drop a column that is still referenced by an index, so
	// remove the future indexes first and then restore the exact schema 14
	// columns below.
	for table, name := range map[string]string{
		"research_v160_analysis_runs": "idx_research_v160_analysis_runs_evidence_set_id",
		"research2_analysis_runs":     "idx_research2_analysis_runs_evidence_set_id",
	} {
		if err := database.Exec("DROP INDEX IF EXISTS " + quoteSQLiteIdentifier(name)).Error; err != nil {
			t.Fatalf("remove schema 15 fixture index %s: %v", name, err)
		}
		if database.Migrator().HasIndex(table, name) {
			t.Fatalf("schema 14 fixture unexpectedly contains %s", name)
		}
	}
	columns := map[string][]string{
		"settings":                             {"experimental_evidence_enabled"},
		"research_v160_analysis_runs":          {"strategy_version", "evidence_profile_version", "evidence_set_id", "data_profile_version"},
		"research_v160_lifecycle_observations": {"data_profile_version"},
		"research_v160_decision_events":        {"decision_policy_version"},
		"research2_analysis_runs":              {"strategy_version", "evidence_profile_version", "evidence_set_id"},
	}
	for table, names := range columns {
		for _, name := range names {
			if database.Migrator().HasColumn(table, name) {
				statement := "ALTER TABLE " + quoteSQLiteIdentifier(table) + " DROP COLUMN " + quoteSQLiteIdentifier(name)
				if err := database.Exec(statement).Error; err != nil {
					t.Fatalf("remove schema 15 fixture column %s.%s: %v", table, name, err)
				}
			}
			if database.Migrator().HasColumn(table, name) {
				t.Fatalf("schema 14 fixture unexpectedly contains %s.%s", table, name)
			}
		}
	}
}

func captureResearchHistory(t *testing.T, database *gorm.DB) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	models := map[string]any{
		"research1_runs":       &[]research.AnalysisRun{},
		"research1_recs":       &[]research.Recommendation{},
		"research1_accounts":   &[]research.SimulatedAccount{},
		"research1_trades":     &[]research.SimulatedTrade{},
		"research1_positions":  &[]research.Position{},
		"research1_cash_flows": &[]research.AccountCashFlow{},
		"research1_snapshots":  &[]research.AccountValuationSnapshot{},
		"research2_runs":       &[]research2.AnalysisRun{},
		"research2_recs":       &[]research2.Recommendation{},
		"research2_accounts":   &[]research2.Account{},
		"research2_trades":     &[]research2.Trade{},
		"research2_snapshots":  &[]research2.AccountSnapshot{},
	}
	for name, target := range models {
		if err := database.Order("id ASC").Find(target).Error; err != nil {
			t.Fatalf("capture %s: %v", name, err)
		}
		// Schema 25 intentionally labels otherwise-blank Research Center 1
		// provenance. Normalize those metadata-only fields so this fixture keeps
		// checking that the underlying reports and financial history are stable.
		if runs, ok := target.(*[]research.AnalysisRun); ok {
			for index := range *runs {
				(*runs)[index].StrategyVersion = ""
				(*runs)[index].DataProfileVersion = ""
			}
		}
		encoded, err := json.Marshal(target)
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		result[name] = encoded
	}
	return result
}

func TestSchema15AndMinute3AreIdempotentAndStrictlyVerified(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchEvidenceSchema(mainDB); err != nil {
		t.Fatalf("repeat main schema 15: %v", err)
	}
	if err := applyMarketEvidenceCacheSchema(minuteDB); err != nil {
		t.Fatalf("repeat minute schema 3: %v", err)
	}
	if err := verifyMainSchema15Runtime(mainDB); err != nil {
		t.Fatal(err)
	}
	if err := verifyMinuteSchema3Runtime(minuteDB); err != nil {
		t.Fatal(err)
	}

	cutoff := "2026-08-28T09:55:00+08:00"
	collectedBeforeCutoff := "2026-08-28T09:54:00+08:00"
	if err := mainDB.Exec(`INSERT INTO research_evidence_sets(
evidence_set_id,owner_type,owner_id,cutoff_at,collector_version,evidence_profile_version,status,content_hash,created_at)
VALUES (?,?,?,?,?,?,?,?,?)`, "schema15-set", "research1", "schema15-run", cutoff, "2.0.0", "evidence-v1", "collecting", "empty", collectedBeforeCutoff).Error; err != nil {
		t.Fatal(err)
	}
	var collectingRows int64
	if err := mainDB.Raw("SELECT COUNT(*) FROM research_evidence_sets WHERE evidence_set_id = ? AND status = 'collecting' AND frozen_at IS NULL", "schema15-set").Scan(&collectingRows).Error; err != nil {
		t.Fatal(err)
	}
	if collectingRows != 1 {
		t.Fatal("collecting evidence set did not retain a NULL frozen_at")
	}
	if err := mainDB.Exec(`INSERT INTO research_evidence_items(
evidence_item_id,evidence_set_id,source_id,source_name,source_ref,category,available_at,collected_at,status,summary,content_hash,created_at)
VALUES (?,?,?,?,?,?,NULL,?,?,?,?,?)`, "schema15-item", "schema15-set", "fixture-item", "fixture", "https://example.invalid/evidence", "market", collectedBeforeCutoff, "unavailable", "availability unknown", "fixture-hash", collectedBeforeCutoff).Error; err != nil {
		t.Fatal(err)
	}
	var cutoffEligible int64
	if err := mainDB.Raw("SELECT COUNT(*) FROM research_evidence_items WHERE evidence_set_id = ? AND available_at <= ?", "schema15-set", cutoff).Scan(&cutoffEligible).Error; err != nil {
		t.Fatal(err)
	}
	if cutoffEligible != 0 {
		t.Fatal("evidence with unknown available_at became cutoff-eligible through collected_at")
	}
	if err := mainDB.Exec("UPDATE research_evidence_sets SET status = 'frozen', frozen_at = ? WHERE evidence_set_id = ?", cutoff, "schema15-set").Error; err != nil {
		t.Fatal(err)
	}
	var frozenRows int64
	if err := mainDB.Raw("SELECT COUNT(*) FROM research_evidence_sets WHERE evidence_set_id = ? AND status = 'frozen' AND frozen_at IS NOT NULL", "schema15-set").Scan(&frozenRows).Error; err != nil {
		t.Fatal(err)
	}
	if frozenRows != 1 {
		t.Fatal("frozen evidence set did not persist frozen_at")
	}

	barInsert := `INSERT INTO market_bar_cache(asset_type,symbol,period,adjustment,bar_time,open,high,low,close,source,updated_at)
VALUES ('stock','sh600000','1m','none',1,10,11,9,10.5,'fixture',1)`
	if err := minuteDB.Exec(barInsert).Error; err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.Exec(barInsert).Error; err == nil {
		t.Fatal("market_bar_cache accepted a duplicate composite primary key")
	}
	auctionInsert := `INSERT INTO market_auction_snapshot(asset_type,symbol,trade_date,observed_at,phase,source,updated_at)
VALUES ('stock','sh600000','2026-08-28',1,'open','fixture',1)`
	if err := minuteDB.Exec(auctionInsert).Error; err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.Exec(auctionInsert).Error; err == nil {
		t.Fatal("market_auction_snapshot accepted a duplicate composite primary key")
	}
	tickInsert := `INSERT INTO market_trade_tick(asset_type,symbol,traded_at,sequence,price,volume,source,updated_at)
VALUES ('stock','sh600000',1,1,10.5,100,'fixture',1)`
	if err := minuteDB.Exec(tickInsert).Error; err != nil {
		t.Fatal(err)
	}
	if err := minuteDB.Exec(tickInsert).Error; err == nil {
		t.Fatal("market_trade_tick accepted a duplicate composite primary key")
	}
}

func TestSchema14Minute2UpgradesThroughSchema17Minute3WithoutRewritingResearchHistory(t *testing.T) {
	mainDB := openMigrationTestDB(t)
	minuteDB := openMigrationTestDB(t)
	applyPublishedMigrationPrefix(t, mainDB, mainMigrations, 14, "1.8.7")
	applyPublishedMigrationPrefix(t, minuteDB, minuteMigrations, 2, "1.8.7")
	stripSchema15ColumnsFromFixture(t, mainDB)
	for _, table := range []string{"research_evidence_sets", "research_evidence_items"} {
		if mainDB.Migrator().HasTable(table) {
			t.Fatalf("schema 14 fixture unexpectedly contains %s", table)
		}
	}
	for _, table := range []string{"market_bar_cache", "market_auction_snapshot", "market_trade_tick"} {
		if minuteDB.Migrator().HasTable(table) {
			t.Fatalf("minute schema 2 fixture unexpectedly contains %s", table)
		}
	}

	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	completed := now.Add(time.Minute)
	if err := mainDB.Model(&research.SimulatedAccount{}).Where("id = ?", 1).Update("cash", 450000.25).Error; err != nil {
		t.Fatal(err)
	}
	run1 := research.AnalysisRun{RunID: "schema15-r1-run", ScheduledFor: now, StartedAt: now, CompletedAt: &completed, Status: "success", ModelAttemptLogJSON: "[]", RecommendationCount: 1}
	if err := mainDB.Omit("StrategyVersion", "DataProfileVersion", "EvidenceProfileVersion", "EvidenceSetID").Create(&run1).Error; err != nil {
		t.Fatal(err)
	}
	rec1 := research.Recommendation{RecommendationID: "schema15-r1-rec", AnalysisRunID: run1.RunID, StockCode: "sh600000", StockName: "浦发银行", SignalAt: now, Status: "active"}
	if err := mainDB.Create(&rec1).Error; err != nil {
		t.Fatal(err)
	}
	trade1 := research.SimulatedTrade{TradeID: "schema15-r1-trade", RecommendationID: rec1.RecommendationID, StockCode: rec1.StockCode, Side: "buy", TradedAt: now, MarketPrice: 10, ExecutionPrice: 10.01, Quantity: 100, Notional: 1001, Commission: 5, TotalFees: 5.01, NetCashFlow: -1006.01}
	if err := mainDB.Create(&trade1).Error; err != nil {
		t.Fatal(err)
	}
	position1 := research.Position{RecommendationID: rec1.RecommendationID, StockCode: rec1.StockCode, StockName: rec1.StockName, Market: "SH", Quantity: 100, EntryAt: now, EntryPrice: 10.01, BuyFees: 5.01, CurrentPrice: 10.25, CurrentPriceAt: &completed, Status: "open"}
	if err := mainDB.Create(&position1).Error; err != nil {
		t.Fatal(err)
	}

	if err := mainDB.Model(&research2.Account{}).Where("id = ?", 1).Update("cash", 8000.5).Error; err != nil {
		t.Fatal(err)
	}
	run2 := research2.AnalysisRun{RunID: "schema15-r2-run", TradingDate: "2026-08-28", ScheduledFor: now, StartedAt: now, EvidenceCutoffAt: now, GeneratedAt: &completed, Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", RecommendationCount: 1, OnTime: true}
	if err := mainDB.Omit("StrategyVersion", "EvidenceProfileVersion", "EvidenceSetID").Create(&run2).Error; err != nil {
		t.Fatal(err)
	}
	sellAt := now.AddDate(0, 0, 1)
	rec2 := research2.Recommendation{RecommendationID: "schema15-r2-rec", AnalysisRunID: run2.RunID, StockCode: "sz000001", StockName: "平安银行", SignalAt: completed, FinalScore: 60, ReferencePrice: 10, BuyLower: 9.8, BuyUpper: 10.2, Status: "holding", TargetBuyAt: completed, BuyAt: &completed, BuyPrice: 10.01, Quantity: 100, BuyFees: 5.01, TargetSellAt: &sellAt}
	if err := mainDB.Create(&rec2).Error; err != nil {
		t.Fatal(err)
	}
	trade2 := research2.Trade{TradeID: "schema15-r2-trade", RecommendationID: rec2.RecommendationID, Side: "buy", TradedAt: completed, MarketPrice: 10, ExecutionPrice: 10.01, Quantity: 100, Commission: 5, TransferFee: 0.01, NetCashFlow: -1006.01}
	if err := mainDB.Create(&trade2).Error; err != nil {
		t.Fatal(err)
	}
	snapshot2 := research2.AccountSnapshot{SnapshotID: "schema15-r2-snapshot", ValuedAt: completed, TradingDate: "2026-08-28", SnapshotType: "buy", Cash: 8000.5, PositionValue: 1000, NetAssetValue: 9000.5, NetProfit: -2999.5, ReturnRate: -0.2499583333}
	if err := mainDB.Create(&snapshot2).Error; err != nil {
		t.Fatal(err)
	}

	before := captureResearchHistory(t, mainDB)
	if err := MigrateAll(mainDB, minuteDB); err != nil {
		t.Fatal(err)
	}
	after := captureResearchHistory(t, mainDB)
	for name, expected := range before {
		if !bytes.Equal(expected, after[name]) {
			t.Fatalf("schema 15 rewrote %s\nbefore=%s\nafter=%s", name, expected, after[name])
		}
	}

	var research1Legacy, research1EvidenceLinks, research2Populated int64
	if err := mainDB.Raw(`SELECT COUNT(*) FROM research_v160_analysis_runs
WHERE strategy_version = ? AND data_profile_version = ?`, legacyUnversioned, legacyUnversioned).Scan(&research1Legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Raw(`SELECT COUNT(*) FROM research_v160_analysis_runs
WHERE evidence_profile_version IS NOT NULL OR evidence_set_id IS NOT NULL`).Scan(&research1EvidenceLinks).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Raw(`SELECT COUNT(*) FROM research2_analysis_runs
WHERE strategy_version IS NOT NULL OR evidence_profile_version IS NOT NULL OR evidence_set_id IS NOT NULL`).Scan(&research2Populated).Error; err != nil {
		t.Fatal(err)
	}
	if research1Legacy != 1 || research1EvidenceLinks != 0 || research2Populated != 0 {
		t.Fatalf("unexpected version metadata: research1Legacy=%d research1EvidenceLinks=%d research2Populated=%d", research1Legacy, research1EvidenceLinks, research2Populated)
	}
	var experimentalEnabled int64
	if err := mainDB.Raw("SELECT COUNT(*) FROM settings WHERE experimental_evidence_enabled <> 0").Scan(&experimentalEnabled).Error; err != nil {
		t.Fatal(err)
	}
	if experimentalEnabled != 0 {
		t.Fatalf("experimental evidence unexpectedly enabled for %d settings rows", experimentalEnabled)
	}
	mainStatus, err := VerifyMain(mainDB)
	if err != nil {
		t.Fatal(err)
	}
	minuteStatus, err := VerifyMinute(minuteDB)
	if err != nil {
		t.Fatal(err)
	}
	if mainStatus.CurrentVersion != 25 || minuteStatus.CurrentVersion != 3 {
		t.Fatalf("schema versions main=%d minute=%d", mainStatus.CurrentVersion, minuteStatus.CurrentVersion)
	}
	if len(mainStatus.Records) < 11 || mainStatus.Records[len(mainStatus.Records)-11].ID != 15 || mainStatus.Records[len(mainStatus.Records)-10].ID != 16 || mainStatus.Records[len(mainStatus.Records)-9].ID != 17 || mainStatus.Records[len(mainStatus.Records)-8].ID != 18 || mainStatus.Records[len(mainStatus.Records)-7].ID != 19 || mainStatus.Records[len(mainStatus.Records)-6].ID != 20 || mainStatus.Records[len(mainStatus.Records)-5].ID != 21 || mainStatus.Records[len(mainStatus.Records)-4].ID != 22 || mainStatus.Records[len(mainStatus.Records)-3].ID != 23 || mainStatus.Records[len(mainStatus.Records)-2].ID != 24 || mainStatus.Records[len(mainStatus.Records)-1].ID != 25 {
		t.Fatalf("schema 14 fixture did not advance through migrations 15 to 25: %+v", mainStatus.Records)
	}
}
